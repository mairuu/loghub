package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
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

type User struct {
	ID           int64
	PasswordHash string
	Role         string
	// TenantID is nil for admins.
	TenantID *string
}

// FindByEmail returns the user whose email is email, ignoring case. A user
// that doesn't exist is an errors.KindNotFound error.
func (r *UserRepo) FindByEmail(ctx context.Context, email string) (User, error) {
	row, err := r.store.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, errors.NotFound("user_not_found", "no user has this email")
	}
	if err != nil {
		return User{}, pg.Wrap(err, "cannot look up user")
	}
	return User(row), nil
}
