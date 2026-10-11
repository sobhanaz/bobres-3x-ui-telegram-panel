package ratelimit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestAllowUnderAndDenyOverLimit(t *testing.T) {
	rdb := memClient(t)
	l := New(rdb, testPrefix(t))
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
	l := New(rdb, testPrefix(t))
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
	l := New(rdb, testPrefix(t))
	ctx := context.Background()

	if _, err := l.Allow(ctx, "user:a", time.Minute, 1); err != nil {
		t.Fatal(err)
	}
	if ok, _ := l.Allow(ctx, "user:b", time.Minute, 1); !ok {
		t.Fatal("quota leaked across keys")
	}
}

// testPrefix gives every test run its own keys: the counters live in a shared
// Redis for up to a minute, so fixed keys made back-to-back runs fail.
func testPrefix(t *testing.T) string {
	return fmt.Sprintf("rltest:%s:%d:", t.Name(), time.Now().UnixNano())
}

// memClient connects to the test Redis (see testdb.RedisAddr). It skips when
// Redis is unreachable locally and fails in CI (BOBRES_TEST_REQUIRE_DB=1).
func memClient(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: testdb.RedisAddr()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		if testdb.Required() {
			t.Fatalf("redis required but unavailable: %v", err)
		}
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestGiveReturnsOneRequest(t *testing.T) {
	rdb := memClient(t)
	prefix := testPrefix(t)
	l := New(rdb, prefix)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := l.Allow(ctx, "user:g", time.Minute, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Give(ctx, "user:g"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := l.Allow(ctx, "user:g", time.Minute, 2); !ok {
		t.Fatal("a given-back request was still counted")
	}
	if ok, _ := l.Allow(ctx, "user:g", time.Minute, 2); ok {
		t.Fatal("over the limit after a give-back")
	}
	// Nothing to give back: no key is made (it would never expire).
	if err := l.Give(ctx, "user:none"); err != nil {
		t.Fatal(err)
	}
	if n, err := rdb.Exists(ctx, prefix+"user:none").Result(); err != nil || n != 0 {
		t.Fatalf("give-back made a key: %d %v", n, err)
	}
}
