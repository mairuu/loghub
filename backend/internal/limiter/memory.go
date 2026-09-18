package limiter

import (
	"context"
	"sync"
	"time"
)

// Memory keeps the buckets in this process, so they start full again when
// it restarts, and each replica would count on its own. loghub runs one
// backend. The zero value is ready to use.
type Memory struct {
	// Now is the clock. nil means time.Now.
	Now func() time.Time

	mu sync.Mutex
	// full is when each key's bucket is full again. A bucket that is full
	// has no entry, so the map holds only keys that took a token in the
	// last minute or so.
	full  map[string]time.Time
	swept time.Time
}

var _ Backend = (*Memory)(nil)

func (m *Memory) Take(_ context.Context, key string, perMinute int, burst int) (float64, error) {
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	interval := time.Minute / time.Duration(perMinute)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.full == nil {
		m.full = make(map[string]time.Time)
	}
	m.sweep(now)

	full := m.full[key]
	if full.Before(now) {
		full = now
	}
	remaining := float64(burst) - float64(full.Sub(now))/float64(interval) - 1
	if remaining >= 0 {
		m.full[key] = full.Add(interval)
	}
	return remaining, nil
}

// sweep forgets full buckets, at most once a minute.
func (m *Memory) sweep(now time.Time) {
	if now.Sub(m.swept) < time.Minute {
		return
	}
	m.swept = now
	for key, full := range m.full {
		if !full.After(now) {
			delete(m.full, key)
		}
	}
}
