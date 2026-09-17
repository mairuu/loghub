package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/ingest"
	"github.com/mairuu/loghub/backend/internal/jobs"
	"github.com/mairuu/loghub/backend/internal/platform/config"
	"github.com/mairuu/loghub/backend/internal/platform/log"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store"
)

type serveConfig struct {
	LogLevel    slog.Level `env:"LOG_LEVEL" default:"info"`
	ListenAddr  string     `env:"LISTEN_ADDR" default:":8080"`
	DatabaseURL string     `env:"DATABASE_URL" required:"true"`
	AuthSecret  string     `env:"AUTH_SECRET" required:"true"`
	IngestToken string     `env:"INGEST_TOKEN" required:"true"`
}

func serve(ctx context.Context) error {
	cfg, err := config.Load[serveConfig]()
	if err != nil {
		return err
	}

	logger := log.New(cfg.LogLevel)

	tokens, err := auth.NewTokens(auth.TokenConfig{Secret: cfg.AuthSecret})
	if err != nil {
		return fmt.Errorf("AUTH_SECRET: %w", err)
	}
	authenticator, err := auth.NewAuthenticator(tokens, cfg.IngestToken)
	if err != nil {
		return fmt.Errorf("INGEST_TOKEN: %w", err)
	}

	pool, err := pg.NewPool(ctx, pg.Config{
		URL: cfg.DatabaseURL,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	db := store.New(pool)
	events := store.NewEventRepo(db)

	stopJobs := jobs.Start(ctx, logger, jobs.Job{
		Name:  "maintain_partitions",
		Every: time.Hour,
		Run: func(ctx context.Context) error {
			return events.MaintainPartitions(ctx, ingest.DefaultRetention)
		},
	})
	defer stopJobs()

	server, err := api.New(api.Config{
		Logger:        logger,
		Events:        events,
		Users:         store.NewUserRepo(db),
		Tokens:        tokens,
		Authenticator: authenticator,
		Ready:         func(ctx context.Context) error { return pg.Check(ctx, pool) },
	})
	if err != nil {
		return err
	}
	srv := http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", slog.String("addr", cfg.ListenAddr))

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Info("shutting down")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// force close if graceful shutdown fails
		srv.Close()
		return err
	}

	return nil
}
