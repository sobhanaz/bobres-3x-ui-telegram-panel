package eventbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MaxBatch caps how many events one pull returns.
const MaxBatch = 500

// Querier is what Feed needs; *pgxpool.Pool satisfies it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Feed serves one outbox to named consumers. Each consumer has its own cursor,
// stored with the producer, so consumers progress independently and a restored
// producer database re-delivers events instead of skipping them.
type Feed struct {
	db      Querier
	pending string
	ack     string
}

// NewFeed returns the feed for an outbox table (see NewOutbox).
func NewFeed(db Querier, table string) *Feed {
	t := lookupTable(table)
	q := t.schema + "." + table
	c := q + "_cursors"
	return &Feed{
		db: db,
		pending: fmt.Sprintf(`SELECT o.id, o.event_id::text, o.topic, o.payload, o.created_at FROM %s o
			WHERE o.id > COALESCE((SELECT last_id FROM %s WHERE consumer = $1), 0)
			ORDER BY o.id LIMIT $2`, q, c), //nolint:gosec // whitelisted identifiers
		// The cursor only moves forward, and never past the newest event, so a
		// confused consumer cannot make itself skip events that do not exist yet.
		ack: fmt.Sprintf(`INSERT INTO %s AS c (consumer, last_id)
			VALUES ($1, LEAST($2, (SELECT COALESCE(MAX(id), 0) FROM %s)))
			ON CONFLICT (consumer) DO UPDATE
			SET last_id = GREATEST(c.last_id, EXCLUDED.last_id), updated_at = now()`, c, q), //nolint:gosec // whitelisted identifiers
	}
}

// Pending returns up to limit events after consumer's cursor, in order.
func (f *Feed) Pending(ctx context.Context, consumer string, limit int) ([]Message, error) {
	if consumer == "" {
		return nil, errors.New("eventbus: consumer required")
	}
	if limit <= 0 || limit > MaxBatch {
		limit = MaxBatch
	}
	rows, err := f.db.Query(ctx, f.pending, consumer, limit)
	if err != nil {
		return nil, fmt.Errorf("eventbus: pending: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var (
			m  Message
			at time.Time
		)
		if err := rows.Scan(&m.Seq, &m.EventID, &m.Topic, &m.Payload, &at); err != nil {
			return nil, fmt.Errorf("eventbus: scan: %w", err)
		}
		m.CreatedAt = at
		out = append(out, m)
	}
	return out, rows.Err()
}

// Ack moves consumer's cursor to upTo: every event with seq <= upTo is done.
func (f *Feed) Ack(ctx context.Context, consumer string, upTo int64) error {
	if consumer == "" {
		return errors.New("eventbus: consumer required")
	}
	if upTo <= 0 {
		return nil
	}
	if _, err := f.db.Exec(ctx, f.ack, consumer, upTo); err != nil {
		return fmt.Errorf("eventbus: ack: %w", err)
	}
	return nil
}

// LocalSource reads a Feed in-process as one named consumer (same database).
type LocalSource struct {
	Feed     *Feed
	Consumer string
}

// Fetch implements Source.
func (s LocalSource) Fetch(ctx context.Context, limit int) ([]Message, error) {
	return s.Feed.Pending(ctx, s.Consumer, limit)
}

// Ack implements Source.
func (s LocalSource) Ack(ctx context.Context, upTo int64) error {
	return s.Feed.Ack(ctx, s.Consumer, upTo)
}
