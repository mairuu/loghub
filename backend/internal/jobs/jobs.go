// Package jobs runs background work inside the serve process (ADR 0001):
// each job in its own goroutine, once at start and then on a fixed interval.
package jobs

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/prometheus/client_golang/prometheus"
)

type Job struct {
	// Name identifies the job in the log.
	Name string
	// Every is how often the job runs, and must be positive. Runs never
	// overlap: one that overruns is followed straight away by the next, and
	// the runs it missed are skipped.
	Every time.Duration
	Run   func(ctx context.Context) error
}

// Start runs each job now and then every job.Every, until ctx is done or stop
// is called. A run that fails or panics is logged, and the job carries on.
// Every run is counted in metrics registered with reg; nil keeps them to
// itself. stop cancels the context of any run in progress and returns once
// every job has returned.
func Start(ctx context.Context, logger *slog.Logger, reg prometheus.Registerer, jobs ...Job) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	m := newMetrics(reg, jobs)
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Go(func() { loop(ctx, logger, m, job) })
	}
	return func() {
		cancel()
		wg.Wait()
	}
}

func loop(ctx context.Context, logger *slog.Logger, m *metrics, job Job) {
	ticker := time.NewTicker(job.Every)
	defer ticker.Stop()
	for {
		runOnce(ctx, logger, m, job)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runOnce(ctx context.Context, logger *slog.Logger, m *metrics, job Job) {
	start := time.Now()
	defer func() {
		// A panic here would take the API down with it.
		if p := recover(); p != nil {
			logger.Error("job panicked", "job", job.Name, "panic", p, "stack", string(debug.Stack()))
			m.finished(job.Name, outcomePanicked, time.Since(start))
		}
	}()

	err := job.Run(ctx)
	took := time.Since(start)

	level, message := slog.LevelDebug, "job finished"
	attrs := []slog.Attr{
		slog.String("job", job.Name),
		slog.Float64("duration_ms", float64(took.Microseconds())/1000),
	}
	switch {
	case err == nil:
		m.finished(job.Name, outcomeOK, took)
	case ctx.Err() != nil:
		// Shutting down. The next start runs the job again.
		message = "job stopped"
	default:
		m.finished(job.Name, outcomeFailed, took)
		level, message = slog.LevelError, "job failed"
		if errors.KindOf(err) == errors.KindUnavailable {
			level = slog.LevelWarn
		}
		attrs = append(attrs, slog.String("error", err.Error()))
		attrs = append(attrs, errors.AttrsOf(err)...)
	}
	logger.LogAttrs(ctx, level, message, attrs...)
}
