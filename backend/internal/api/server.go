// Package api serves the HTTP API described by api/openapi.yaml. Routing and
// parameter binding are generated into internal/api/gen; this package
// supplies the handlers behind them and the envelope every error shares.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/ingest"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

// Events is where ingested events go and searches read from.
// *store.EventRepo is the implementation.
type Events interface {
	Insert(ctx context.Context, events []store.NewEventParams) ([]error, error)
	Search(ctx context.Context, scope store.Scope, p store.SearchParams) (store.EventPage, error)
}

// Users is who may sign in. *store.UserRepo is the implementation.
type Users interface {
	FindByEmail(ctx context.Context, email string) (store.User, error)
}

// Alerts is where alert rules and the alerts they raise are kept.
// *store.AlertRepo is the implementation.
type Alerts interface {
	CreateRule(ctx context.Context, scope store.Scope, p store.NewAlertRule) (store.AlertRule, error)
	ListRules(ctx context.Context, scope store.Scope, tenant string) ([]store.AlertRule, error)
	ListAlerts(ctx context.Context, scope store.Scope, tenant string, limit int) ([]store.Alert, error)
}

type Config struct {
	Logger *slog.Logger
	Events Events
	Users  Users
	Alerts Alerts
	// Tokens issues the tokens Login answers with, and Authenticator
	// accepts them along with the ingest key.
	Tokens        *auth.Tokens
	Authenticator *auth.Authenticator
	// Ready reports whether the service can serve requests. Its error is
	// rendered as the healthz response, normally 503 database_unavailable.
	Ready func(context.Context) error
	// Normalizer's zero value is the production configuration.
	Normalizer ingest.Normalizer
}

type Server struct {
	logger     *slog.Logger
	events     Events
	users      Users
	alerts     Alerts
	tokens     *auth.Tokens
	authn      *auth.Authenticator
	authz      *authz.Enforcer
	ready      func(context.Context) error
	normalizer ingest.Normalizer
}

var _ gen.ServerInterface = (*Server)(nil)

// New fails if the API's policy sets don't hold together.
func New(cfg Config) (*Server, error) {
	enforcer, err := authz.New(policies...)
	if err != nil {
		return nil, fmt.Errorf("api policies: %w", err)
	}
	return &Server{
		logger:     cfg.Logger,
		events:     cfg.Events,
		users:      cfg.Users,
		alerts:     cfg.Alerts,
		tokens:     cfg.Tokens,
		authn:      cfg.Authenticator,
		authz:      enforcer,
		ready:      cfg.Ready,
		normalizer: cfg.Normalizer,
	}, nil
}

// Handler serves the generated API routes, the spec and the docs page. Every
// response carries X-Request-Id, and every request is logged with its
// caller. A request whose credential is invalid is refused before routing.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	gen.HandlerWithOptions(s, gen.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: s.paramError,
	})

	// The spec the generated code was built from, so the docs can never
	// describe a different API than the one running.
	mux.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		spec, err := gen.GetSpecJSON()
		if err != nil {
			s.fail(w, r, errors.Internalf(err, "cannot load embedded spec"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(spec)
	})

	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(docsPage))
	})

	return s.observe(s.authenticate(mux))
}

// authenticate puts the caller in the request context, and in the request's
// log line.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return s.authn.Middleware(s.fail)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info := infoFrom(r.Context()); info != nil {
			info.caller = auth.CallerFrom(r.Context())
		}
		next.ServeHTTP(w, r)
	}))
}

const readyCheckTimeout = 2 * time.Second

func (s *Server) GetHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.authz.Authorize(auth.CallerFrom(r.Context()), authz.Health, authz.Read, ""); err != nil {
		s.fail(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
	defer cancel()

	if err := s.ready(ctx); err != nil {
		s.fail(w, r, err)
		return
	}
	s.respond(w, r, http.StatusOK, gen.HealthStatus{Status: gen.HealthStatusStatusOk})
}

// paramFormats describe the query parameters the generated code parses into
// something other than a string, for when that parsing fails.
var paramFormats = map[string]string{
	"from":         "an RFC 3339 timestamp, with '+' in the offset escaped as %2B",
	"to":           "an RFC 3339 timestamp, with '+' in the offset escaped as %2B",
	"limit":        "an integer between 1 and 1000",
	"severity_min": "an integer between 0 and 10",
	"severity_max": "an integer between 0 and 10",
}

// paramError answers a query parameter the generated code could not bind.
func (s *Server) paramError(w http.ResponseWriter, r *http.Request, err error) {
	message := "a query parameter is not in the expected format"
	var formatErr *gen.InvalidParamFormatError
	if errors.As(err, &formatErr) {
		message = formatErr.ParamName + " is not in the expected format"
		if format, ok := paramFormats[formatErr.ParamName]; ok {
			message = fmt.Sprintf("%s must be %s", formatErr.ParamName, format)
		}
	}
	s.fail(w, r, invalidParam(message).Wrapping(err))
}

func invalidParam(message string) *errors.Error {
	return errors.Malformed("invalid_parameter", message)
}

// Scalar loads from a CDN, so /api/docs needs outbound network access. An
// appliance that is firewalled off renders a blank page; vendoring the bundle
// into the frontend assets is the fix if that becomes a problem.
//
// The page shares an origin with the UI, which keeps its token there
// (ADR 0009), so the script is pinned to one release and checked against its
// hash. To upgrade, change both:
//
//	curl -s https://cdn.jsdelivr.net/npm/@scalar/api-reference@$VERSION/dist/browser/standalone.js |
//	  openssl dgst -sha384 -binary | openssl base64 -A
const (
	scalarVersion   = "1.69.0"
	scalarIntegrity = "sha384-UL+pt9bcR3hCuzEybA1bAyu6yv9qkzJuYCP5N+HZPOo9ZkUXcMflxqBjC1vfDzfe"
)

const docsPage = `<!doctype html>
<html>
  <head>
    <title>loghub API</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
  </head>
  <body>
    <div id="app"></div>
    <script
      src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@` + scalarVersion + `/dist/browser/standalone.js"
      integrity="` + scalarIntegrity + `"
      crossorigin="anonymous"></script>
    <script>
      Scalar.createApiReference('#app', { url: '/api/openapi.json' })
    </script>
  </body>
</html>
`
