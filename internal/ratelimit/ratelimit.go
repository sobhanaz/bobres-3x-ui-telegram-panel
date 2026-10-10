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

// giveBack undoes one count, only while the window is still open.
var giveBack = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 and tonumber(redis.call('GET', KEYS[1])) > 0 then
  return redis.call('DECR', KEYS[1])
end
return 0
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

// Give returns one request to key's window, for a request that did not
// happen after all (it failed before doing anything).
func (l *Limiter) Give(ctx context.Context, key string) error {
	if err := giveBack.Run(ctx, l.rdb, []string{l.prefix + key}).Err(); err != nil {
		return fmt.Errorf("ratelimit: %w", err)
	}
	return nil
}
