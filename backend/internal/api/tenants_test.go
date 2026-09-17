package api_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

// fakeTenants stands in for the tenant store. The end-to-end test uses the
// real one.
type fakeTenants struct {
	tenants []store.Tenant
	err     error
	// asked records each call's only.
	asked []string
}

func (f *fakeTenants) List(_ context.Context, only string) ([]store.Tenant, error) {
	f.asked = append(f.asked, only)
	if only == "" {
		return f.tenants, f.err
	}
	var out []store.Tenant
	for _, t := range f.tenants {
		if t.ID == only {
			out = append(out, t)
		}
	}
	return out, f.err
}

func TestListTenants(t *testing.T) {
	demo := []store.Tenant{{ID: "demoA", Name: "Demo A"}, {ID: "demoB", Name: "Demo B"}}
	for _, tc := range []struct {
		name   string
		caller string
		want   string
		asked  []string
	}{
		{"admin", tokenFor(t, adminCaller), `{"items":[{"id":"demoA","name":"Demo A"},{"id":"demoB","name":"Demo B"}]}`, []string{""}},
		{"viewer", tokenFor(t, viewerCaller), `{"items":[{"id":"demoA","name":"Demo A"}]}`, []string{"demoA"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenants := fakeTenants{tenants: demo}
			h := newServer(t, api.Config{Tenants: &tenants})
			callAs(t, h, tc.caller, "GET", "/api/v1/tenants", "", nil).check(t, 200, tc.want)
			if !reflect.DeepEqual(tenants.asked, tc.asked) {
				t.Errorf("asked for %q, want %q", tenants.asked, tc.asked)
			}
		})
	}

	t.Run("none", func(t *testing.T) {
		h := newServer(t, api.Config{Tenants: &fakeTenants{}})
		callAs(t, h, tokenFor(t, adminCaller), "GET", "/api/v1/tenants", "", nil).check(t, 200, `{"items":[]}`)
	})

	t.Run("store fails", func(t *testing.T) {
		h := newServer(t, api.Config{Tenants: &fakeTenants{err: errors.Unavailable("database_unavailable", "cannot reach the database")}})
		callAs(t, h, tokenFor(t, adminCaller), "GET", "/api/v1/tenants", "", nil).
			check(t, 503, errorBody("database_unavailable", "cannot reach the database"))
	})
}
