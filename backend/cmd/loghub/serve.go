package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mairuu/loghub/backend/internal/alerting"
	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/ingest"
	"github.com/mairuu/loghub/backend/internal/jobs"
	"github.com/mairuu/loghub/backend/internal/platform/config"
	"github.com/mairuu/loghub/backend/internal/platform/log"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type serveConfig struct {
	LogLevel    slog.Level `env:"LOG_LEVEL" default:"info"`
	ListenAddr  string     `env:"LISTEN_ADDR" default:":8080"`
	DatabaseURL string     `env:"DATABASE_URL" required:"true"`
	AuthSecret  string     `env:"AUTH_SECRET" required:"true"`
	IngestToken string     `env:"INGEST_TOKEN" required:"true"`
	// MetricsAddr serves /metrics for Prometheus (ADR 0012). Compose
	// publishes no port for it, and Caddy doesn't route to it.
	MetricsAddr string `env:"METRICS_ADDR" default:":9090"`
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

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewBuildInfoCollector(),
		pg.NewPoolCollector(pool),
	)

	db := store.New(pool)
	events := store.NewEventRepo(db)
	alerts := store.NewAlertRepo(db)
	tenants := store.NewTenantRepo(db)

	evaluator := alerting.New(alerting.Config{
		Tenants: tenants,
		Rules:   alerts,
		Logger:  logger,
		Metrics: reg,
	})
	stopJobs := jobs.Start(ctx, logger, reg, jobs.Job{
		Name:  "maintain_partitions",
		Every: time.Hour,
		Run: func(ctx context.Context) error {
			return events.MaintainPartitions(ctx, ingest.DefaultRetention)
		},
	}, evaluator.Job())
	defer stopJobs()

	server, err := api.New(api.Config{
		Logger:        logger,
		Events:        events,
		Users:         store.NewUserRepo(db),
		Tenants:       tenants,
		Alerts:        alerts,
		Tokens:        tokens,
		Authenticator: authenticator,
		Ready:         func(ctx context.Context) error { return pg.Check(ctx, pool) },
		Metrics:       reg,
	})
	if err != nil {
		return err
	}
	srv := http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	metricsSrv := http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Either server failing stops serve.
	errc := make(chan error, 2)
	for name, s := range map[string]*http.Server{"api": &srv, "metrics": &metricsSrv} {
		go func() {
			logger.Info("listening", slog.String("server", name), slog.String("addr", s.Addr))

			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s server: %w", name, err)
				return
			}
			errc <- nil
		}()
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Info("shutting down")
	// Scrapes can stop straight away; API requests get to finish.
	metricsSrv.Close()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// force close if graceful shutdown fails
		srv.Close()
		return err
	}

	return nil
}
