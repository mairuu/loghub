package main

import (
	"context"
	"log/slog"

	"github.com/mairuu/loghub/backend/internal/platform/config"
	"github.com/mairuu/loghub/backend/internal/platform/log"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store"
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

	return store.Seed(ctx, store.New(pool), logger, store.SeedParams{
		AdminEmail:     cfg.AdminEmail,
		AdminPassword:  cfg.AdminPassword,
		ViewerPassword: cfg.ViewerPassword,
	})
}
