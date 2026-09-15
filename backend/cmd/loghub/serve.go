package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mairuu/loghub/backend/internal/platform/config"
	"github.com/mairuu/loghub/backend/internal/platform/log"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
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

	srv := http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler(logger, pool),
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

const readyCheckTimeout = 2 * time.Second

func handler(logger *slog.Logger, pool *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
		defer cancel()

		if err := pg.Check(ctx, pool); err != nil {
			logger.Warn("health check: database unavailable", "error", err)
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	return mux
}
