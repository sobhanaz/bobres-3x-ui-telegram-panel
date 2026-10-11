package web

import (
	"sync"
	"time"
)

// limiter allows n requests per window per key (fixed windows, in memory:
// one core process serves the dashboard).
type limiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	hits   map[string]*bucket
}

type bucket struct {
	start time.Time
	count int
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, hits: map[string]*bucket{}}
}

// allow counts a request for key and reports whether it is within the limit.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 10_000 { // drop finished windows so the map cannot grow without end
		for k, b := range l.hits {
			if now.Sub(b.start) >= l.window {
				delete(l.hits, k)
			}
		}
	}
	b := l.hits[key]
	if b == nil || now.Sub(b.start) >= l.window {
		b = &bucket{start: now}
		l.hits[key] = b
	}
	b.count++
	return b.count <= l.n
}
