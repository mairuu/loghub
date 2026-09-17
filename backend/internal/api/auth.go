package api

import (
	"net/http"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

// Login is declared by the spec ahead of its implementation (ADR 0009).
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	s.fail(w, r, errors.Unavailable("login_unavailable", "sign-in is not available yet"))
}
