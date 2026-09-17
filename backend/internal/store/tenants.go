package store

import (
	"context"
	"regexp"

	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store/gen"
)

// tenantIDPattern is the CHECK on tenants.id.
var tenantIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidTenantID reports whether id could name a tenant.
func ValidTenantID(id string) bool { return tenantIDPattern.MatchString(id) }

// InvalidTenantID describes an id ValidTenantID refuses.
const InvalidTenantID = "tenant must be 1 to 64 letters, digits, '-' or '_'"

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

type Tenant = gen.ListTenantsRow

// List returns every tenant, or only the one named if only isn't empty,
// ordered by ID. tenants has no row-level security, so whoever calls this
// decides which tenants the caller may know about.
func (r *TenantRepo) List(ctx context.Context, only string) ([]Tenant, error) {
	var id *string
	if only != "" {
		id = &only
	}
	tenants, err := r.store.q.ListTenants(ctx, id)
	if err != nil {
		return nil, pg.Wrap(err, "cannot list tenants")
	}
	return tenants, nil
}
