// Package limiter limits how often a key, such as a client address, may do
// something, with a token bucket per key: a bucket holds up to burst tokens,
// gains perMinute of them a minute, and each attempt takes one.
package limiter

import (
	"context"
	"math"
	"time"
)

// Decision is what Allow answers, with what a client needs to pace itself.
type Decision struct {
	Allowed bool
	// Limit is how many attempts a full bucket allows at once.
	Limit int
	// Remaining is how many more attempts are allowed straight away.
	Remaining int
	// RetryAfter is how long until the next attempt is allowed, once one
	// has been refused.
	RetryAfter time.Duration
	// ResetAfter is how long until the bucket is full again.
	ResetAfter time.Duration
}

// Backend keeps the buckets. Memory is the implementation.
type Backend interface {
	// Take takes a token from key's bucket and returns how many are left.
	// When there was no whole token, it takes nothing and returns less than
	// zero: minus the part of a token still to come.
	Take(ctx context.Context, key string, perMinute int, burst int) (remaining float64, err error)
}

type Limiter struct {
	backend   Backend
	name      string
	perMinute int
	burst     int
}

// New allows each key perMinute attempts a minute, all of them at once if
// it has been quiet for a minute. name keeps its keys apart from those of
// other limiters on the same backend.
func New(backend Backend, name string, perMinute int) *Limiter {
	if perMinute < 1 {
		panic("limiter: perMinute must be at least 1")
	}
	return &Limiter{
		backend:   backend,
		name:      name,
		perMinute: perMinute,
		burst:     perMinute,
	}
}

// Allow counts an attempt by key. If the backend fails, the attempt is
// allowed: a limiter that can't count shouldn't lock everyone out.
func (l *Limiter) Allow(ctx context.Context, key string) Decision {
	decision := Decision{Limit: l.burst}

	remaining, err := l.backend.Take(ctx, "ratelimit:"+l.name+":"+key, l.perMinute, l.burst)
	if err != nil {
		return Decision{Limit: l.burst, Remaining: l.burst, Allowed: true}
	}

	rate := float64(l.perMinute)
	// What the bucket holds now. A refused attempt took nothing.
	left := remaining
	if remaining < 0 {
		decision.RetryAfter = time.Duration(-remaining / rate * float64(time.Minute))
		decision.Remaining = 0
		left = remaining + 1
	} else {
		decision.Allowed = true
		decision.Remaining = int(remaining)
	}

	decision.ResetAfter = time.Duration(
		(float64(l.burst) - math.Max(left, 0)) / rate * float64(time.Minute))
	return decision
}
