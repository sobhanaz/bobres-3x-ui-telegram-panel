// Package ratelimit implements a Redis-backed fixed-window counter. The Lua
// script makes INCR+PEXPIRE atomic so concurrent requests cannot race past
// the limit.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var script = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`)

// Limiter counts requests per key inside a window.
type Limiter struct {
	rdb    redis.Scripter
	prefix string
}

// New returns a Limiter storing keys as prefix+key.
func New(rdb redis.Scripter, prefix string) *Limiter {
	return &Limiter{rdb: rdb, prefix: prefix}
}

// Allow reports whether the caller identified by key may proceed, given max
// requests per window.
func (l *Limiter) Allow(ctx context.Context, key string, window time.Duration, max int) (bool, error) {
	if window <= 0 || max <= 0 {
		return false, fmt.Errorf("ratelimit: window and max must be positive")
	}
	n, err := script.Run(ctx, l.rdb, []string{l.prefix + key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("ratelimit: %w", err)
	}
	return n <= int64(max), nil
}
