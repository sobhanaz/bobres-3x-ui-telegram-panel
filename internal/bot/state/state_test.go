package state

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func exercise(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if st, err := s.Get(ctx, 1); err != nil || st.Scene != "" {
		t.Fatalf("empty state: %+v %v", st, err)
	}
	if err := s.Set(ctx, 1, State{Scene: "await_txid", Data: map[string]string{"intent": "i1"}}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Get(ctx, 1); st.Scene != "await_txid" || st.Get("intent") != "i1" {
		t.Fatalf("round trip: %+v", st)
	}
	if st, _ := s.Get(ctx, 2); st.Scene != "" {
		t.Fatal("state leaked to another user")
	}
	_ = s.Clear(ctx, 1)
	if st, _ := s.Get(ctx, 1); st.Scene != "" {
		t.Fatal("clear failed")
	}
	k := "upd:" + time.Now().Format(time.RFC3339Nano)
	if first, _ := s.Once(ctx, k, time.Minute); !first {
		t.Fatal("first Once must be true")
	}
	if again, _ := s.Once(ctx, k, time.Minute); again {
		t.Fatal("second Once must be false")
	}
}

func TestMemory(t *testing.T) { exercise(t, NewMemory()) }

func TestRedis(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: testdb.RedisAddr()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		if testdb.Required() {
			t.Fatalf("redis required: %v", err)
		}
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Del(context.Background(), key(1), key(2)).Err(); _ = rdb.Close() })
	exercise(t, NewRedis(rdb))
}
