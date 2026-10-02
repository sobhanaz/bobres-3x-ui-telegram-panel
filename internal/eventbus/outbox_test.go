package eventbus

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
)

func testOutbox(t *testing.T) (*sql.DB, *Outbox) {
	t.Helper()
	dsn := os.Getenv("BOBRES_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"
	}
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		t.Skipf("migration: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("TRUNCATE core.outbox_core"); err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	return db, NewOutbox(db, "outbox_core")
}

func TestPublishInTransaction(t *testing.T) {
	db, ob := testOutbox(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ob.Publish(ctx, tx, "order.created.v1", []byte(`{"id":"1"}`)); err != nil {
		t.Fatal(err)
	}
	// invisible before commit (different connection)
	if n := countRows(t, db); n != 0 {
		t.Fatalf("row visible before commit: %d", n)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db); n != 1 {
		t.Fatalf("rows after commit = %d, want 1", n)
	}
}

func TestPublishRollbackLeavesNoRow(t *testing.T) {
	db, ob := testOutbox(t)
	ctx := context.Background()
	tx, _ := db.BeginTx(ctx, nil)
	_ = ob.Publish(ctx, tx, "order.created.v1", []byte(`{}`))
	_ = tx.Rollback()
	if n := countRows(t, db); n != 0 {
		t.Fatalf("rows after rollback = %d", n)
	}
}

func TestRelayDeliversAndMarksPublished(t *testing.T) {
	db, ob := testOutbox(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tx, _ := db.BeginTx(ctx, nil)
	if err := ob.Publish(ctx, tx, "ping.v1", []byte(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()

	got := make(chan Message, 1)
	relay := NewRelay(db, "outbox_core", 25*time.Millisecond)
	go relay.Start(ctx, func(ctx context.Context, m Message) error {
		got <- m
		return nil
	})

	select {
	case m := <-got:
		if m.Topic != "ping.v1" || string(m.Payload) != `{"n": 1}` {
			t.Errorf("bad message: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not deliver within 5s")
	}

	// marked published → not delivered twice
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var published bool
		if err := db.QueryRow(`SELECT published_at IS NOT NULL FROM core.outbox_core LIMIT 1`).Scan(&published); err == nil && published {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("row not marked published")
}

func TestRelaySkipsFailedHandlerRowForRetry(t *testing.T) {
	db, ob := testOutbox(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tx, _ := db.BeginTx(ctx, nil)
	_ = ob.Publish(ctx, tx, "flaky.v1", []byte(`{}`))
	_ = tx.Commit()

	var firstErr = errInject
	calls := 0
	done := make(chan struct{})
	relay := NewRelay(db, "outbox_core", 25*time.Millisecond)
	go relay.Start(ctx, func(ctx context.Context, m Message) error {
		calls++
		if calls == 1 {
			return firstErr // fail: must be retried later
		}
		close(done)
		return nil
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not retry failed message")
	}
}

func countRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM core.outbox_core`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var errInject = errInjectT{}

type errInjectT struct{}

func (errInjectT) Error() string { return "injected failure" }
