package eventbus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, `TRUNCATE core.outbox_core, core.outbox_core_cursors`); err != nil {
		t.Fatal(err)
	}
	return db
}

func publish(t *testing.T, db *pgxpool.Pool, topic string, payload any) string {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewOutbox("outbox_core").Publish(ctx, tx, topic, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func count(t *testing.T, db *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM core.outbox_core`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPublishCommitsWithTheTransaction(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("outbox_core")

	tx, _ := db.Begin(ctx)
	if _, err := ob.Publish(ctx, tx, "order.paid.v1", map[string]string{"order_id": "o1"}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db); n != 0 {
		t.Fatalf("event visible before commit: %d", n)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db); n != 1 {
		t.Fatalf("rows after commit = %d", n)
	}

	tx, _ = db.Begin(ctx)
	_, _ = ob.Publish(ctx, tx, "order.paid.v1", map[string]string{})
	_ = tx.Rollback(ctx)
	if n := count(t, db); n != 1 {
		t.Fatalf("rolled-back event persisted: %d rows", n)
	}
}

func TestPublishValidatesPayload(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	tx, _ := db.Begin(ctx)
	defer tx.Rollback(ctx) //nolint:errcheck // test cleanup
	ob := NewOutbox("outbox_core")
	if _, err := ob.Publish(ctx, tx, "x.v1", []byte("not json")); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if _, err := ob.Publish(ctx, tx, "", map[string]int{}); err == nil {
		t.Fatal("empty topic accepted")
	}
}

func TestConsumersHaveIndependentCursors(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	feed := NewFeed(db, "outbox_core")
	a := publish(t, db, "t.v1", map[string]int{"n": 1})
	b := publish(t, db, "t.v1", map[string]int{"n": 2})

	bot, err := feed.Pending(ctx, "bot", 10)
	if err != nil || len(bot) != 2 || bot[0].EventID != a || bot[1].EventID != b || bot[0].Seq >= bot[1].Seq {
		t.Fatalf("pending for bot: %v %+v", err, bot)
	}
	if err := feed.Ack(ctx, "bot", bot[0].Seq); err != nil {
		t.Fatal(err)
	}
	bot, _ = feed.Pending(ctx, "bot", 10)
	other, _ := feed.Pending(ctx, "other", 10)
	if len(bot) != 1 || bot[0].EventID != b || len(other) != 2 {
		t.Fatalf("cursors not independent: bot=%d other=%d", len(bot), len(other))
	}
	// Acks never move a cursor backwards.
	_ = feed.Ack(ctx, "bot", bot[0].Seq)
	_ = feed.Ack(ctx, "bot", 1)
	if rest, _ := feed.Pending(ctx, "bot", 10); len(rest) != 0 {
		t.Fatalf("cursor moved backwards: %d pending", len(rest))
	}
}

func TestAckCannotSkipFutureEvents(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	feed := NewFeed(db, "outbox_core")
	publish(t, db, "t.v1", map[string]int{})
	if err := feed.Ack(ctx, "bot", 1<<40); err != nil {
		t.Fatal(err)
	}
	id := publish(t, db, "t.v1", map[string]int{})
	got, _ := feed.Pending(ctx, "bot", 10)
	if len(got) != 1 || got[0].EventID != id {
		t.Fatalf("an over-eager ack skipped a later event: %+v", got)
	}
}

// Without serialized publishers, a transaction holding seq N could commit after
// seq N+1 was already delivered and acked, and event N would be lost.
func TestPublishersAreSerialized(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	ob := NewOutbox("outbox_core")

	slow, _ := db.Begin(ctx)
	if _, err := ob.Publish(ctx, slow, "t.v1", map[string]string{"who": "slow"}); err != nil {
		t.Fatal(err)
	}
	fast, _ := db.Begin(ctx)
	defer fast.Rollback(ctx) //nolint:errcheck // test cleanup
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := ob.Publish(short, fast, "t.v1", map[string]string{"who": "fast"}); err == nil {
		t.Fatal("second publisher did not wait for the first transaction")
	}
	_ = slow.Commit(ctx)
}

// fakeSource is an in-memory Source.
type fakeSource struct {
	msgs  []Message
	acked int64
}

func (f *fakeSource) Fetch(_ context.Context, limit int) ([]Message, error) {
	var out []Message
	for _, m := range f.msgs {
		if m.Seq > f.acked && len(out) < limit {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeSource) Ack(_ context.Context, upTo int64) error {
	if upTo > f.acked {
		f.acked = upTo
	}
	return nil
}

func msgs(n int) []Message {
	out := make([]Message, n)
	for i := range out {
		out[i] = Message{Seq: int64(i + 1), EventID: string(rune('a' + i)), Topic: "t.v1", Payload: []byte(`{}`)}
	}
	return out
}

func TestConsumerRetriesThenAcks(t *testing.T) {
	src := &fakeSource{msgs: msgs(3)}
	calls := map[string]int{}
	c, err := NewConsumer(ConsumerConfig{
		Source: src,
		Handle: func(_ context.Context, m Message) error {
			calls[m.EventID]++
			if m.EventID == "b" && calls["b"] < 3 {
				return errors.New("db hiccup")
			}
			return nil
		},
		DeadLetter: func(context.Context, Message, error) error { t.Fatal("unexpected dead letter"); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if n, err := c.Step(ctx); n != 1 || err == nil || src.acked != 1 {
		t.Fatalf("step 1: handled=%d err=%v acked=%d", n, err, src.acked)
	}
	_, _ = c.Step(ctx) // b fails again
	if n, err := c.Step(ctx); n != 2 || err != nil || src.acked != 3 {
		t.Fatalf("step 3: handled=%d err=%v acked=%d", n, err, src.acked)
	}
}

// The old relay retried a bad event forever and never reached later events.
func TestConsumerDeadLettersAndMovesOn(t *testing.T) {
	src := &fakeSource{msgs: msgs(3)}
	var dead []string
	c, _ := NewConsumer(ConsumerConfig{
		Source: src,
		Handle: func(_ context.Context, m Message) error {
			switch m.EventID {
			case "a":
				return Permanent(errors.New("malformed"))
			case "b":
				return errors.New("keeps failing")
			}
			return nil
		},
		DeadLetter:  func(_ context.Context, m Message, _ error) error { dead = append(dead, m.EventID); return nil },
		PoisonAfter: 3,
	})
	ctx := context.Background()
	for i := 0; i < 5 && src.acked < 3; i++ {
		_, _ = c.Step(ctx)
	}
	if src.acked != 3 || len(dead) != 2 || dead[0] != "a" || dead[1] != "b" {
		t.Fatalf("acked=%d dead=%v", src.acked, dead)
	}
}

func TestConsumerKeepsEventWhenDeadLetterFails(t *testing.T) {
	src := &fakeSource{msgs: msgs(1)}
	c, _ := NewConsumer(ConsumerConfig{
		Source:     src,
		Handle:     func(context.Context, Message) error { return Permanent(errors.New("bad")) },
		DeadLetter: func(context.Context, Message, error) error { return errors.New("db down") },
	})
	if _, err := c.Step(context.Background()); err == nil || src.acked != 0 {
		t.Fatalf("event acked although it was neither applied nor dead-lettered (acked=%d, err=%v)", src.acked, err)
	}
}

func TestConsumerRunStopsOnCancel(t *testing.T) {
	src := &fakeSource{}
	c, _ := NewConsumer(ConsumerConfig{
		Source: src, Interval: 10 * time.Millisecond,
		Handle:     func(context.Context, Message) error { return nil },
		DeadLetter: func(context.Context, Message, error) error { return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestNewConsumerValidates(t *testing.T) {
	if _, err := NewConsumer(ConsumerConfig{}); err == nil {
		t.Fatal("empty config accepted")
	}
}
