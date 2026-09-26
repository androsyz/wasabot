package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a sliding-window limiter: at most limit hits per key within window. A limit of 0 or less
// disables it. It is safe for concurrent use.
type Limiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	hits      map[string][]time.Time
	nextSweep time.Time
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, hits: make(map[string][]time.Time)}
}

// Allow records a hit and reports whether it is within the limit. A denied hit is not recorded,
// so a flood does not extend its own block.
func (l *Limiter) Allow(key string, now time.Time) bool {
	if l.limit <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	recent := l.recent(key, now)
	if len(recent) >= l.limit {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

// Check reports whether another hit would be allowed, without recording one.
func (l *Limiter) Check(key string, now time.Time) bool {
	if l.limit <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, now)) < l.limit
}

// Record adds a hit even if the limit is already reached, for counting failures separately from checks.
func (l *Limiter) Record(key string, now time.Time) {
	if l.limit <= 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	l.hits[key] = append(l.recent(key, now), now)
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

func (l *Limiter) recent(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// sweep drops keys that have been idle for a whole window, so the map cannot grow forever.
func (l *Limiter) sweep(now time.Time) {
	if now.Before(l.nextSweep) {
		return
	}
	l.nextSweep = now.Add(l.window)

	cutoff := now.Add(-l.window)
	for key, hits := range l.hits {
		if len(hits) == 0 || !hits[len(hits)-1].After(cutoff) {
			delete(l.hits, key)
		}
	}
}
