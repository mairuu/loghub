package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/platform/config"
	"github.com/mairuu/loghub/backend/internal/platform/log"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store"
)

type serveConfig struct {
	LogLevel    slog.Level `env:"LOG_LEVEL" default:"info"`
	ListenAddr  string     `env:"LISTEN_ADDR" default:":8080"`
	DatabaseURL string     `env:"DATABASE_URL" required:"true"`
}

func serve(ctx context.Context) error {
	cfg, err := config.Load[serveConfig]()
	if err != nil {
		return err
	}

	logger := log.New(cfg.LogLevel)

	pool, err := pg.NewPool(ctx, pg.Config{
		URL: cfg.DatabaseURL,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	server := api.New(api.Config{
		Logger: logger,
		Events: store.NewEventRepo(store.New(pool)),
		Ready:  func(ctx context.Context) error { return pg.Check(ctx, pool) },
	})
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
