package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/platform/config"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/platform/log"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
)

type seedConfig struct {
	LogLevel           slog.Level `env:"LOG_LEVEL" default:"info"`
	MigrateDatabaseURL string     `env:"MIGRATE_DATABASE_URL" required:"true"`
	AdminEmail         string     `env:"ADMIN_EMAIL" default:"admin@loghub.local"`
	AdminPassword      string     `env:"ADMIN_PASSWORD" required:"true"`
	ViewerPassword     string     `env:"VIEWER_PASSWORD" required:"true"`
}

func seed(ctx context.Context) error {
	cfg, err := config.Load[seedConfig]()
	if err != nil {
		return err
	}

	logger := log.New(cfg.LogLevel)

	pool, err := pg.NewPool(ctx, pg.Config{
		URL: cfg.MigrateDatabaseURL,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	tenants := []struct {
		ID   string
		Name string
	}{
		{ID: "demoA", Name: "Demo A"},
		{ID: "demoB", Name: "Demo B"},
	}

	return pg.InTx(ctx, pool, func(tx pgx.Tx) error {
		for _, t := range tenants {
			if _, err := tx.Exec(ctx,
				`INSERT INTO tenants (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
				t.ID, t.Name,
			); err != nil {
				return errors.Internalf(err, "cannot seed tenant %s", t.ID)
			}

			email := "viewer@" + strings.ToLower(t.ID) + ".local"
			if err := seedUser(ctx, tx, logger, email, cfg.ViewerPassword, "viewer", &t.ID); err != nil {
				return err
			}
		}
		return seedUser(ctx, tx, logger, cfg.AdminEmail, cfg.AdminPassword, "admin", nil)
	})
}

func seedUser(ctx context.Context, tx pgx.Tx, logger *slog.Logger, email, password, role string, tenantID *string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return errors.Internalf(err, "cannot hash %s password", role)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO users (email, password_hash, role, tenant_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO NOTHING`,
		email, hash, role, tenantID,
	)
	if err != nil {
		return errors.Internalf(err, "cannot seed %s user", role)
	}

	if tag.RowsAffected() == 0 {
		logger.Info("user already exists; left unchanged", "email", email, "role", role)
		return nil
	}
	logger.Info("created user", "email", email, "role", role)
	return nil
}
