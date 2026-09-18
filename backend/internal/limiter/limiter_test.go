package limiter_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/limiter"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)} }

// check compares to the millisecond, since the durations come from floats.
func check(t *testing.T, what string, got, want limiter.Decision) {
	t.Helper()
	got.RetryAfter = got.RetryAfter.Round(time.Millisecond)
	got.ResetAfter = got.ResetAfter.Round(time.Millisecond)
	if got != want {
		t.Errorf("%s: got %+v, want %+v", what, got, want)
	}
}

func TestLimiter(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	l := limiter.New(&limiter.Memory{Now: c.now}, "sign-in", 10)

	// A quiet key may make all ten attempts at once. One comes back every six
	// seconds.
	for i := range 10 {
		check(t, "burst", l.Allow(ctx, "a"), limiter.Decision{
			Allowed: true, Limit: 10, Remaining: 9 - i, ResetAfter: time.Duration(i+1) * 6 * time.Second,
		})
	}
	// Refused attempts take nothing, so they don't push the wait back.
	for range 3 {
		check(t, "empty", l.Allow(ctx, "a"), limiter.Decision{
			Limit: 10, RetryAfter: 6 * time.Second, ResetAfter: time.Minute,
		})
	}
	check(t, "another key", l.Allow(ctx, "b"), limiter.Decision{
		Allowed: true, Limit: 10, Remaining: 9, ResetAfter: 6 * time.Second,
	})

	c.advance(2 * time.Second)
	check(t, "part of a token", l.Allow(ctx, "a"), limiter.Decision{
		Limit: 10, RetryAfter: 4 * time.Second, ResetAfter: 58 * time.Second,
	})
	c.advance(4 * time.Second)
	check(t, "one token", l.Allow(ctx, "a"), limiter.Decision{
		Allowed: true, Limit: 10, Remaining: 0, ResetAfter: time.Minute,
	})
	check(t, "taken", l.Allow(ctx, "a"), limiter.Decision{
		Limit: 10, RetryAfter: 6 * time.Second, ResetAfter: time.Minute,
	})

	// A bucket never holds more than the burst, however long it waits.
	c.advance(time.Hour)
	check(t, "after an hour", l.Allow(ctx, "a"), limiter.Decision{
		Allowed: true, Limit: 10, Remaining: 9, ResetAfter: 6 * time.Second,
	})
}

// Limiters on one backend count separately, even for the same key.
func TestLimiterNames(t *testing.T) {
	ctx := context.Background()
	backend := &limiter.Memory{Now: newClock().now}
	one, other := limiter.New(backend, "one", 1), limiter.New(backend, "other", 1)
	if !one.Allow(ctx, "a").Allowed || one.Allow(ctx, "a").Allowed {
		t.Fatal("one allows one attempt a minute")
	}
	if !other.Allow(ctx, "a").Allowed {
		t.Error("other shares one's bucket")
	}
}

type failing struct{}

func (failing) Take(context.Context, string, int, int) (float64, error) {
	return 0, errors.New("cannot reach the store")
}

func TestLimiterAllowsWhenBackendFails(t *testing.T) {
	l := limiter.New(failing{}, "sign-in", 3)
	check(t, "failing backend", l.Allow(context.Background(), "a"), limiter.Decision{
		Allowed: true, Limit: 3, Remaining: 3,
	})
}

// However many attempts arrive at once, only the burst gets through.
func TestMemoryConcurrent(t *testing.T) {
	l := limiter.New(&limiter.Memory{Now: newClock().now}, "sign-in", 10)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if l.Allow(context.Background(), "a").Allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != 10 {
		t.Errorf("allowed %d attempts, want 10", n)
	}
}

func TestNewRefusesNoRate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New allowed a rate of zero")
		}
	}()
	limiter.New(&limiter.Memory{}, "sign-in", 0)
}
