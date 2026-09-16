package store

import (
	"context"
	"log/slog"
	"strings"

	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

type SeedParams struct {
	AdminEmail     string
	AdminPassword  string
	ViewerPassword string
}

// demoTenants get one viewer each, alongside the single admin.
var demoTenants = []NewTenantParams{
	{ID: "demoA", Name: "Demo A"},
	{ID: "demoB", Name: "Demo B"},
}

// Seed creates the demo tenants and their users in one transaction. It is safe
// to re-run: rows that already exist are left as they are, so a password
// changed after the first seed survives.
func Seed(ctx context.Context, s *Store, logger *slog.Logger, p SeedParams) error {
	return s.InTx(ctx, func(s *Store) error {
		tenants := NewTenantRepo(s)
		users := NewUserRepo(s)

		for _, t := range demoTenants {
			if err := tenants.CreateIfMissing(ctx, t); err != nil {
				return errors.Internalf(err, "cannot seed tenant %s", t.ID)
			}

			email := "viewer@" + strings.ToLower(t.ID) + ".local"
			if err := seedUser(ctx, users, logger, email, p.ViewerPassword, "viewer", &t.ID); err != nil {
				return err
			}
		}
		return seedUser(ctx, users, logger, p.AdminEmail, p.AdminPassword, "admin", nil)
	})
}

func seedUser(ctx context.Context, users *UserRepo, logger *slog.Logger, email, password, role string, tenantID *string) error {
	email = strings.ToLower(email)

	hash, err := auth.HashPassword(password)
	if err != nil {
		return errors.Internalf(err, "cannot hash %s password", role)
	}

	created, err := users.CreateIfMissing(ctx, NewUserParams{
		Email:        email,
		PasswordHash: hash,
		Role:         role,
		TenantID:     tenantID,
	})
	if err != nil {
		return errors.Internalf(err, "cannot seed %s user", role)
	}

	if !created {
		logger.Info("user already exists; left unchanged", "email", email, "role", role)
		return nil
	}
	logger.Info("created user", "email", email, "role", role)
	return nil
}
