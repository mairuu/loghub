package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/limiter"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

// maxLoginBytes bounds a sign-in body.
const maxLoginBytes = 64 << 10

// signInsPerMinute is how many sign-in attempts one client address may make
// a minute
const signInsPerMinute = 10

var invalidCredentials = errors.Unauthenticated("invalid_credentials", "email or password is incorrect")

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	if err := s.authz.Authorize(auth.CallerFrom(r.Context()), authz.Sessions, authz.Create, ""); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := requireMediaType(r, "application/json"); err != nil {
		s.failWith(w, r, http.StatusUnsupportedMediaType, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLoginBytes))
	if err != nil {
		s.failRead(w, r, err, maxLoginBytes)
		return
	}
	req, err := decodeLogin(body)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	email := strings.TrimSpace(req.Email)
	// Only attempts that reach the password check count.
	client := clientAddr(r)
	limit := s.signIns.Allow(r.Context(), limitKey(client))
	setRateLimitHeaders(w.Header(), limit)
	if !limit.Allowed {
		s.metrics.signIns.WithLabelValues(signInLimited).Inc()
		s.logger.LogAttrs(r.Context(), slog.LevelInfo, "sign-in limited",
			slog.String("request_id", requestID(r.Context())),
			slog.String("email", email),
			slog.String("client", client.String()),
		)
		s.fail(w, r, tooManyAttempts(limit.RetryAfter))
		return
	}

	// An unknown email carries on with no hash, so it takes as long to
	// refuse as a wrong password.
	user, err := s.users.FindByEmail(r.Context(), email)
	if err != nil && errors.KindOf(err) != errors.KindNotFound {
		s.fail(w, r, err)
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		s.metrics.signIns.WithLabelValues(signInRefused).Inc()
		s.logger.LogAttrs(r.Context(), slog.LevelInfo, "sign-in refused",
			slog.String("request_id", requestID(r.Context())),
			slog.String("email", email),
			slog.String("client", client.String()),
		)
		s.fail(w, r, invalidCredentials)
		return
	}

	role, ok := authz.UserRole(user.Role)
	if !ok {
		s.fail(w, r, errors.Internal("user has an unknown role").With("user_id", user.ID, "role", user.Role))
		return
	}
	caller := authz.Caller{Role: role, Tenant: deref(user.TenantID), UserID: user.ID}
	token, expires, err := s.tokens.Issue(caller)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	s.metrics.signIns.WithLabelValues(signInOK).Inc()
	s.logger.LogAttrs(r.Context(), slog.LevelInfo, "signed in",
		slog.String("request_id", requestID(r.Context())),
		slog.Int64("user_id", user.ID),
		slog.String("role", role.String()),
	)
	// The body is a credential.
	w.Header().Set("Cache-Control", "no-store")
	s.respond(w, r, http.StatusOK, gen.Session{
		Token:     token,
		ExpiresAt: expires.UTC(),
		Role:      gen.Role(role.String()),
		Tenant:    user.TenantID,
	})
}

// clientAddr is the address a request came from. Caddy is the only way in
// from outside, and it replaces X-Forwarded-For with the address it saw,
// since it trusts no proxy in front of it. A request without the header came
// straight to the backend, from inside the stack.
func clientAddr(r *http.Request) netip.Addr {
	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		last := values[len(values)-1]
		if i := strings.LastIndexByte(last, ','); i >= 0 {
			last = last[i+1:]
		}
		if addr, err := netip.ParseAddr(strings.TrimSpace(last)); err == nil {
			return addr.Unmap().WithZone("")
		}
	}
	addr, _ := netip.ParseAddrPort(r.RemoteAddr)
	return addr.Addr().Unmap().WithZone("")
}

// limitKey counts an IPv6 client by its /64, the block one subscriber is
// usually given, so that it can't get a fresh allowance from each address in
// it.
func limitKey(addr netip.Addr) string {
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String()
	}
	return addr.String()
}

// setRateLimitHeaders tells the client how many attempts it has left, and
// when it has them all again, in the fields of the IETF RateLimit draft.
func setRateLimitHeaders(h http.Header, d limiter.Decision) {
	h.Set("RateLimit-Limit", strconv.Itoa(d.Limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(d.Remaining))
	h.Set("RateLimit-Reset", strconv.Itoa(wholeSeconds(d.ResetAfter)))
	if !d.Allowed {
		h.Set("Retry-After", strconv.Itoa(wholeSeconds(d.RetryAfter)))
	}
}

func tooManyAttempts(retryAfter time.Duration) error {
	unit := "seconds"
	n := wholeSeconds(retryAfter)
	if n == 1 {
		unit = "second"
	}
	return errors.RateLimited("too_many_attempts",
		fmt.Sprintf("too many sign-in attempts from this address; try again in %d %s", n, unit))
}

// wholeSeconds rounds up, so that a client that waits as long as it is told
// is never early.
func wholeSeconds(d time.Duration) int {
	return int(math.Ceil(d.Seconds()))
}

func decodeLogin(body []byte) (gen.LoginRequest, error) {
	var req gen.LoginRequest
	err := json.Unmarshal(body, &req)
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr) && (typeErr.Field == "email" || typeErr.Field == "password"):
		return req, invalidRequest(errors.FieldError{
			Field: typeErr.Field, Code: "invalid_type", Message: typeErr.Field + " must be a string",
		})
	case err != nil || !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")):
		return req, errors.Malformed("invalid_json", "request body is not a JSON object").Wrapping(err)
	}

	var missing []errors.FieldError
	if strings.TrimSpace(req.Email) == "" {
		missing = append(missing, errors.FieldError{Field: "email", Code: "required", Message: "email is required"})
	}
	if req.Password == "" {
		missing = append(missing, errors.FieldError{Field: "password", Code: "required", Message: "password is required"})
	}
	if len(missing) > 0 {
		return req, invalidRequest(missing...)
	}
	return req, nil
}

func invalidRequest(fields ...errors.FieldError) error {
	return errors.Invalid("invalid_request", "the request contains invalid fields").WithFields(fields...)
}
