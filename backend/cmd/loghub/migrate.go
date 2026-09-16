package main

import (
	"context"

	"github.com/mairuu/loghub/backend/internal/platform/config"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store/migrations"
)

// appRole is the role the backend connects as. The migrations grant its privileges.
const appRole = "loghub_app"

type migrateConfig struct {
	MigrateDatabaseURL string `env:"MIGRATE_DATABASE_URL" required:"true"`
	AppDBPassword      string `env:"APP_DB_PASSWORD" required:"true"`
}

func migrate(ctx context.Context) error {
	cfg, err := config.Load[migrateConfig]()
	if err != nil {
		return err
	}

	// The role lives outside the migrations so its password comes from config and
	// follows it when it changes, on any Postgres, not just one set up by compose.
	if err := pg.EnsureLoginRole(ctx, cfg.MigrateDatabaseURL, appRole, cfg.AppDBPassword); err != nil {
		return err
	}

	return pg.Migrate(ctx, cfg.MigrateDatabaseURL, migrations.FS, migrations.Dir)
}
