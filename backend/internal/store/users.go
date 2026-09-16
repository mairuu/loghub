package store

import (
	"context"

	"github.com/mairuu/loghub/backend/internal/store/gen"
)

type UserRepo struct{ store *Store }

func NewUserRepo(store *Store) *UserRepo { return &UserRepo{store: store} }

type NewUserParams struct {
	Email        string
	PasswordHash string
	Role         string
	// TenantID is nil for admins, who see every tenant.
	TenantID *string
}

// CreateIfMissing inserts the user and reports whether a row was created. An
// existing row with the same email is left untouched, credentials included.
func (r *UserRepo) CreateIfMissing(ctx context.Context, p NewUserParams) (bool, error) {
	rows, err := r.store.q.InsertUserIfMissing(ctx, gen.InsertUserIfMissingParams{
		Email:        p.Email,
		PasswordHash: p.PasswordHash,
		Role:         p.Role,
		TenantID:     p.TenantID,
	})
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}
