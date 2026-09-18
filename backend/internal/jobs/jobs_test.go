package jobs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mairuu/loghub/backend/internal/jobs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRunsAtStartThenEveryInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		stop := jobs.Start(t.Context(), slog.New(slog.DiscardHandler), nil, jobs.Job{
			Name:  "count",
			Every: time.Hour,
			Run:   func(context.Context) error { runs.Add(1); return nil },
		})
		defer stop()

		for want := int32(1); want <= 3; want++ {
			synctest.Wait()
			if got := runs.Load(); got != want {
				t.Fatalf("after %d hours: %d runs, want %d", want-1, got, want)
			}
			time.Sleep(time.Hour)
		}
	})
}

func TestOverrunSkipsMissedRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var mu sync.Mutex
		var starts []time.Duration
		stop := jobs.Start(t.Context(), slog.New(slog.DiscardHandler), nil, jobs.Job{
			Name:  "slow",
			Every: time.Hour,
			Run: func(context.Context) error {
				mu.Lock()
				starts = append(starts, time.Since(start))
				mu.Unlock()
				time.Sleep(150 * time.Minute)
				return nil
			},
		})
		defer stop()

		time.Sleep(4 * time.Hour)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		// Back to back, not overlapping, and not once for each missed hour.
		want := []time.Duration{0, 150 * time.Minute}
		if !slices.Equal(starts, want) {
			t.Errorf("runs started at %v, want %v", starts, want)
		}
	})
}

func TestFailedRunIsLoggedAndRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log logBuffer
		var runs atomic.Int32
		stop := jobs.Start(t.Context(), log.logger(), nil, jobs.Job{
			Name:  "flaky",
			Every: time.Hour,
			Run: func(context.Context) error {
				if runs.Add(1) == 1 {
					return errors.New("boom")
				}
				return nil
			},
		})
		defer stop()

		time.Sleep(time.Hour)
		synctest.Wait()
		if got := runs.Load(); got != 2 {
			t.Errorf("%d runs, want 2", got)
		}
		entries := log.entries(t)
		if len(entries) != 1 || entries[0]["msg"] != "job failed" || entries[0]["job"] != "flaky" || entries[0]["error"] != "boom" {
			t.Errorf("log = %v, want one failure", entries)
		}
	})
}

func TestPanicIsLoggedAndRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log logBuffer
		var runs atomic.Int32
		stop := jobs.Start(t.Context(), log.logger(), nil, jobs.Job{
			Name:  "fragile",
			Every: time.Hour,
			Run: func(context.Context) error {
				if runs.Add(1) == 1 {
					panic("boom")
				}
				return nil
			},
		})
		defer stop()

		time.Sleep(time.Hour)
		synctest.Wait()
		if got := runs.Load(); got != 2 {
			t.Errorf("%d runs, want 2", got)
		}
		entries := log.entries(t)
		if len(entries) != 1 || entries[0]["msg"] != "job panicked" || entries[0]["panic"] != "boom" {
			t.Errorf("log = %v, want one panic", entries)
		}
	})
}

func TestStopCancelsRunAndWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log logBuffer
		var returned atomic.Bool
		stop := jobs.Start(t.Context(), log.logger(), nil, jobs.Job{
			Name:  "long",
			Every: time.Hour,
			Run: func(ctx context.Context) error {
				<-ctx.Done()
				time.Sleep(time.Second) // cleanup stop has to wait for
				returned.Store(true)
				return ctx.Err()
			},
		})

		synctest.Wait()
		stop()
		if !returned.Load() {
			t.Error("stop returned before the run did")
		}
		// Being stopped is not a failure.
		if entries := log.entries(t); len(entries) != 0 {
			t.Errorf("log = %v, want nothing at info or above", entries)
		}
	})
}

// Every run is counted by outcome, and only a success sets the time of the
// last one.
func TestRunsCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := prometheus.NewRegistry()
		stop := jobs.Start(t.Context(), slog.New(slog.DiscardHandler), reg,
			jobs.Job{Name: "fine", Every: time.Hour, Run: func(context.Context) error { return nil }},
			jobs.Job{Name: "failing", Every: time.Hour, Run: func(context.Context) error { return errors.New("boom") }},
			jobs.Job{Name: "fragile", Every: time.Hour, Run: func(context.Context) error { panic("boom") }},
		)
		defer stop()

		time.Sleep(time.Hour)
		synctest.Wait()
		want := `
# HELP loghub_job_runs_total Background job runs, by job and outcome: ok, failed or panicked.
# TYPE loghub_job_runs_total counter
loghub_job_runs_total{job="failing",outcome="failed"} 2
loghub_job_runs_total{job="failing",outcome="ok"} 0
loghub_job_runs_total{job="failing",outcome="panicked"} 0
loghub_job_runs_total{job="fine",outcome="failed"} 0
loghub_job_runs_total{job="fine",outcome="ok"} 2
loghub_job_runs_total{job="fine",outcome="panicked"} 0
loghub_job_runs_total{job="fragile",outcome="failed"} 0
loghub_job_runs_total{job="fragile",outcome="ok"} 0
loghub_job_runs_total{job="fragile",outcome="panicked"} 2
`
		if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "loghub_job_runs_total"); err != nil {
			t.Error(err)
		}
		want = `
# HELP loghub_job_last_success_timestamp_seconds When a background job last finished without an error, in Unix seconds.
# TYPE loghub_job_last_success_timestamp_seconds gauge
loghub_job_last_success_timestamp_seconds{job="fine"} ` + strconv.FormatInt(time.Now().Unix(), 10) + `
`
		if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "loghub_job_last_success_timestamp_seconds"); err != nil {
			t.Error(err)
		}
		if n, err := testutil.GatherAndCount(reg, "loghub_job_duration_seconds"); err != nil || n != 3 {
			t.Errorf("%d duration series (%v), want one for each job", n, err)
		}
	})
}

// logBuffer collects JSON log lines written from the jobs' goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) logger() *slog.Logger { return slog.New(slog.NewJSONHandler(b, nil)) }

func (b *logBuffer) entries(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	dec := json.NewDecoder(bytes.NewReader(b.buf.Bytes()))
	for dec.More() {
		var e map[string]any
		if err := dec.Decode(&e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}
