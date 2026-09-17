package api

import (
	"net/http"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
)

func (s *Server) ListTenants(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFrom(r.Context())
	readable := s.authz.Scopes(caller, authz.TenantNames, authz.Read)
	if readable.Empty() {
		s.fail(w, r, authz.Deny(caller))
		return
	}
	// tenants has no row-level security, so the policy's answer is the only
	// thing narrowing this list.
	only, _ := readable.Only()
	tenants, err := s.tenants.List(r.Context(), only)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := gen.TenantList{Items: make([]gen.Tenant, len(tenants))}
	for i, t := range tenants {
		out.Items[i] = gen.Tenant{ID: t.ID, Name: t.Name}
	}
	s.respond(w, r, http.StatusOK, out)
}
