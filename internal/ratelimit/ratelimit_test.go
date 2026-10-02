package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestAllowUnderAndDenyOverLimit(t *testing.T) {
	rdb := memClient(t)
	l := New(rdb, "rltest:")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		ok, err := l.Allow(ctx, "user:1", time.Minute, 3)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !ok {
			t.Fatalf("request %d wrongly denied", i+1)
		}
	}
	ok, err := l.Allow(ctx, "user:1", time.Minute, 3)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if ok {
		t.Fatal("request over limit allowed")
	}
}

func TestWindowExpiry(t *testing.T) {
	rdb := memClient(t)
	l := New(rdb, "rltest:")
	ctx := context.Background()

	if _, err := l.Allow(ctx, "user:3", 900*time.Millisecond, 1); err != nil {
		t.Fatal(err)
	}
	if ok, _ := l.Allow(ctx, "user:3", 900*time.Millisecond, 1); ok {
		t.Fatal("second request inside window allowed")
	}
	time.Sleep(time.Second)
	if ok, err := l.Allow(ctx, "user:3", 900*time.Millisecond, 1); err != nil || !ok {
		t.Fatalf("request after window expiry denied: ok=%v err=%v", ok, err)
	}
}

func TestKeysAreIsolated(t *testing.T) {
	rdb := memClient(t)
	l := New(rdb, "rltest:")
	ctx := context.Background()

	if _, err := l.Allow(ctx, "user:a", time.Minute, 1); err != nil {
		t.Fatal(err)
	}
	if ok, _ := l.Allow(ctx, "user:b", time.Minute, 1); !ok {
		t.Fatal("quota leaked across keys")
	}
}

// memClient uses miniredis when available; otherwise a real redis on
// 127.0.0.1:6379 (skipped if unreachable).
func memClient(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}
