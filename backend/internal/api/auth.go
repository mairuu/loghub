package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

// maxLoginBytes bounds a sign-in body.
const maxLoginBytes = 64 << 10

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
	// An unknown email carries on with no hash, so it takes as long to
	// refuse as a wrong password.
	user, err := s.users.FindByEmail(r.Context(), email)
	if err != nil && errors.KindOf(err) != errors.KindNotFound {
		s.fail(w, r, err)
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		s.logger.LogAttrs(r.Context(), slog.LevelInfo, "sign-in refused",
			slog.String("request_id", requestID(r.Context())),
			slog.String("email", email),
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
