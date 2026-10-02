package eventbus

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Message is one outbox row handed to consumers.
type Message struct {
	ID      int64
	Topic   string
	Payload []byte
}

// Outbox appends events to a schema's outbox table inside the caller's
// transaction, so domain writes and event emission commit or roll back
// together (transactional outbox pattern).
type Outbox struct {
	db    *sql.DB
	table string // validated: outbox_core | outbox_payments | outbox_provisioner
}

var outboxTables = map[string]bool{
	"outbox_core":        true,
	"outbox_payments":    true,
	"outbox_provisioner": true,
}

// NewOutbox validates the table name (identifier, never parameterized in SQL).
func NewOutbox(db *sql.DB, table string) *Outbox {
	if !outboxTables[table] {
		panic("eventbus: invalid outbox table " + table)
	}
	return &Outbox{db: db, table: table}
}

// DBTX is the minimal executor Publish needs. Both database/sql and pgx
// transactions are wrapped by callers (see SQLTx and PgxTx).
type DBTX interface {
	ExecPublish(ctx context.Context, query string, args ...any) error
}

// SQLTx adapts *sql.Tx.
type SQLTx struct{ Tx *sql.Tx }

// ExecPublish implements DBTX.
func (w SQLTx) ExecPublish(ctx context.Context, query string, args ...any) error {
	_, err := w.Tx.ExecContext(ctx, query, args...)
	return err
}

// Publish inserts the event inside tx.
func (o *Outbox) Publish(ctx context.Context, tx DBTX, topic string, payload []byte) error {
	q := fmt.Sprintf(`INSERT INTO %s.%s (topic, payload) VALUES ($1, $2)`, schemaOf(o.table), o.table) //nolint:gosec // table name is whitelisted
	if err := tx.ExecPublish(ctx, q, topic, payload); err != nil {
		return fmt.Errorf("eventbus: publish: %w", err)
	}
	return nil
}

func schemaOf(table string) string {
	switch table {
	case "outbox_payments":
		return "payments"
	case "outbox_provisioner":
		return "provisioner"
	default:
		return "core"
	}
}

// Relay polls one outbox table and delivers unpublished rows in id order.
type Relay struct {
	db       *sql.DB
	table    string
	interval time.Duration
}

// NewRelay validates the table and sets the poll interval.
func NewRelay(db *sql.DB, table string, interval time.Duration) *Relay {
	if !outboxTables[table] {
		panic("eventbus: invalid outbox table " + table)
	}
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	return &Relay{db: db, table: table, interval: interval}
}

// Start polls until ctx is done. A handler error leaves the row unpublished
// so it is retried on the next tick (at-least-once delivery; consumers must
// be idempotent via their inbox tables).
func (r *Relay) Start(ctx context.Context, handle func(context.Context, Message) error) error {
	q := fmt.Sprintf(`SELECT id, topic, payload FROM %s.%s
		WHERE published_at IS NULL ORDER BY id LIMIT 100`, schemaOf(r.table), r.table) //nolint:gosec // whitelisted
	mark := fmt.Sprintf(`UPDATE %s.%s SET published_at = now() WHERE id = $1`, schemaOf(r.table), r.table)

	tick := func() {
		rows, err := r.db.QueryContext(ctx, q)
		if err != nil {
			return // transient DB errors: retry next tick
		}
		defer rows.Close() //nolint:errcheck // read errors handled per-row below
		for rows.Next() {
			var m Message
			if err := rows.Scan(&m.ID, &m.Topic, &m.Payload); err != nil {
				return
			}
			if err := handle(ctx, m); err != nil {
				return // leave unpublished; retry this and later rows next tick
			}
			_, _ = r.db.ExecContext(ctx, mark, m.ID)
		}
	}

	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		tick()
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
