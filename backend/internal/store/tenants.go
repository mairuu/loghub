package store

import (
	"context"

	"github.com/mairuu/loghub/backend/internal/store/gen"
)

type TenantRepo struct{ store *Store }

func NewTenantRepo(store *Store) *TenantRepo { return &TenantRepo{store: store} }

type NewTenantParams struct {
	ID   string
	Name string
}

// CreateIfMissing inserts the tenant, leaving an existing row with the same ID
// untouched.
func (r *TenantRepo) CreateIfMissing(ctx context.Context, p NewTenantParams) error {
	return r.store.q.InsertTenantIfMissing(ctx, gen.InsertTenantIfMissingParams{
		ID:   p.ID,
		Name: p.Name,
	})
}

func (r *TenantRepo) ListIDs(ctx context.Context) ([]string, error) {
	return r.store.q.ListTenantIDs(ctx)
}
