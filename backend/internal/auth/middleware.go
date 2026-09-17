package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/mairuu/loghub/backend/internal/authz"
)

// Authenticator works out who sent a request.
type Authenticator struct {
	tokens *Tokens
	// ingestKey is the key's digest, so comparing it takes the same time
	// whatever the length of what was sent.
	ingestKey [sha256.Size]byte
}

// NewAuthenticator accepts tokens from tokens, and ingestKey (INGEST_TOKEN)
// as the collector.
func NewAuthenticator(tokens *Tokens, ingestKey string) (*Authenticator, error) {
	if len(ingestKey) < MinSecretBytes {
		return nil, fmt.Errorf("the ingest key must be at least %d bytes, got %d", MinSecretBytes, len(ingestKey))
	}
	return &Authenticator{tokens: tokens, ingestKey: sha256.Sum256([]byte(ingestKey))}, nil
}

// Authenticate returns the caller behind r's bearer credential. A request
// without one is anonymous, and whether that is enough is for the endpoint
// to decide. A credential that is present but not valid is an invalid_token
// error, rather than anonymous, so the client learns why it was refused.
func (a *Authenticator) Authenticate(r *http.Request) (authz.Caller, error) {
	values := r.Header.Values("Authorization")
	switch len(values) {
	case 0:
		return authz.Caller{Role: authz.Anonymous}, nil
	case 1:
	default:
		return authz.Caller{}, invalidToken(fmt.Errorf("%d Authorization headers", len(values)))
	}

	scheme, credential, _ := strings.Cut(strings.TrimSpace(values[0]), " ")
	credential = strings.TrimSpace(credential)
	if !strings.EqualFold(scheme, "Bearer") || credential == "" {
		return authz.Caller{}, invalidToken(fmt.Errorf("not a bearer credential"))
	}

	digest := sha256.Sum256([]byte(credential))
	if subtle.ConstantTimeCompare(digest[:], a.ingestKey[:]) == 1 {
		return authz.Caller{Role: authz.Collector}, nil
	}
	return a.tokens.Parse(credential)
}

// Middleware puts each request's caller in its context. A request whose
// credential is invalid goes no further: fail answers it.
func (a *Authenticator) Middleware(fail func(http.ResponseWriter, *http.Request, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, err := a.Authenticate(r)
			if err != nil {
				fail(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithCaller(r.Context(), caller)))
		})
	}
}

type callerKey struct{}

func WithCaller(ctx context.Context, c authz.Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller Middleware found, or an anonymous one.
func CallerFrom(ctx context.Context) authz.Caller {
	c, _ := ctx.Value(callerKey{}).(authz.Caller)
	return c
}
