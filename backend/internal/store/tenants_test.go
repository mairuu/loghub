package store_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

func TestListTenants(t *testing.T) {
	db := storetest.Open(t)
	owner := store.NewTenantRepo(store.New(db.Owner))
	// Other tests add tenants to the same database, so these are told apart
	// by their names.
	suffix := db.Tenant(t)
	for _, p := range []store.NewTenantParams{
		{ID: "z_" + suffix, Name: "Zulu " + suffix},
		{ID: "a_" + suffix, Name: "Alpha " + suffix},
	} {
		if err := owner.CreateIfMissing(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	// The app role lists them, although tenants has no policy of its own.
	repo := store.NewTenantRepo(store.New(db.App))

	t.Run("every tenant, by ID", func(t *testing.T) {
		all, err := repo.List(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.IsSortedFunc(all, func(a, b store.Tenant) int { return strings.Compare(a.ID, b.ID) }) {
			t.Errorf("not in ID order: %v", all)
		}
		var ours []store.Tenant
		for _, tenant := range all {
			if strings.HasSuffix(tenant.ID, suffix) {
				ours = append(ours, tenant)
			}
		}
		want := []store.Tenant{
			{ID: "a_" + suffix, Name: "Alpha " + suffix},
			{ID: suffix, Name: suffix},
			{ID: "z_" + suffix, Name: "Zulu " + suffix},
		}
		if !slices.Equal(ours, want) {
			t.Errorf("got %v, want %v", ours, want)
		}
	})

	t.Run("only one", func(t *testing.T) {
		got, err := repo.List(t.Context(), "z_"+suffix)
		if err != nil {
			t.Fatal(err)
		}
		if want := []store.Tenant{{ID: "z_" + suffix, Name: "Zulu " + suffix}}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("only one that doesn't exist", func(t *testing.T) {
		got, err := repo.List(t.Context(), "missing_"+suffix)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})
}
