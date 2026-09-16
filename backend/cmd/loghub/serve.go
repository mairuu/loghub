package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mairuu/loghub/backend/internal/api/gen"
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

	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
		defer cancel()

		if err := pg.Check(ctx, pool); err != nil {
			logger.Warn("health check: database unavailable", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, gen.ErrorResponse{
				Code:    "database_unavailable",
				Message: "cannot reach the database",
			})
			return
		}

		writeJSON(w, http.StatusOK, gen.HealthStatus{Status: gen.HealthStatusStatusOk})
	})

	// The spec the generated code was built from, so the docs can never
	// describe a different API than the one running.
	mux.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		spec, err := gen.GetSpecJSON()
		if err != nil {
			logger.Error("cannot load embedded spec", "error", err)
			writeJSON(w, http.StatusInternalServerError, gen.ErrorResponse{
				Code:    "internal_error",
				Message: "an unexpected error occurred",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(spec)
	})

	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(docsPage))
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Scalar loads from a CDN, so /api/docs needs outbound network access. An
// appliance that is firewalled off renders a blank page; vendoring the bundle
// into the frontend assets is the fix if that becomes a problem.
const docsPage = `<!doctype html>
<html>
  <head>
    <title>loghub API</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
  </head>
  <body>
    <div id="app"></div>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
    <script>
      Scalar.createApiReference('#app', { url: '/api/openapi.json' })
    </script>
  </body>
</html>
`
