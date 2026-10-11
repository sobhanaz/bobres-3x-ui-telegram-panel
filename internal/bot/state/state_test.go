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
	flag := "bot:test:" + time.Now().Format(time.RFC3339Nano)
	if set, err := s.Recall(ctx, flag); set || err != nil {
		t.Fatalf("unset flag: %v %v", set, err)
	}
	if err := s.Remember(ctx, flag, 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if set, err := s.Recall(ctx, flag); !set || err != nil {
		t.Fatalf("remembered flag: %v %v", set, err)
	}
	time.Sleep(300 * time.Millisecond)
	if set, _ := s.Recall(ctx, flag); set {
		t.Fatal("the flag outlived its ttl")
	}
	n := "n" + time.Now().Format("150405.000000")
	if _, ok, err := s.LoadCheckout(ctx, 1, n); ok || err != nil {
		t.Fatalf("unknown checkout: %v %v", ok, err)
	}
	want := Checkout{Type: "renew", PlanID: "p1", SubscriptionID: "s1", DiscountCode: "SPRING20"}
	if err := s.SaveCheckout(ctx, 1, n, want); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.LoadCheckout(ctx, 1, n); !ok || err != nil || got != want {
		t.Fatalf("checkout round trip: %+v %v %v", got, ok, err)
	}
	if _, ok, _ := s.LoadCheckout(ctx, 2, n); ok {
		t.Fatal("another user read the checkout")
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
