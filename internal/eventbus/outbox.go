package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// outboxTable describes one service's outbox. Identifiers are whitelisted
// here and never come from input, because table names cannot be SQL parameters.
type outboxTable struct {
	schema string
	// lockKey serializes publishers of this outbox (see Publish). It only has
	// to differ from other advisory locks in the database.
	lockKey int64
}

var outboxTables = map[string]outboxTable{
	"outbox_core":        {schema: "core", lockKey: 7_301_001},
	"outbox_payments":    {schema: "payments", lockKey: 7_301_002},
	"outbox_provisioner": {schema: "provisioner", lockKey: 7_301_003},
}

func lookupTable(table string) outboxTable {
	t, ok := outboxTables[table]
	if !ok {
		panic("eventbus: unknown outbox table " + table)
	}
	return t
}

// DBTX is what Publish needs from a transaction; pgx.Tx satisfies it.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Outbox appends events to one service's outbox table.
type Outbox struct {
	lockKey int64
	insert  string
}

// NewOutbox returns the outbox for table (outbox_core, outbox_payments or
// outbox_provisioner). An unknown table is a programming error and panics.
func NewOutbox(table string) *Outbox {
	t := lookupTable(table)
	return &Outbox{
		lockKey: t.lockKey,
		insert: fmt.Sprintf( //nolint:gosec // identifiers come from the whitelist above
			`INSERT INTO %s.%s (topic, payload, event_id) VALUES ($1, $2, $3)`, t.schema, table),
	}
}

// Publish appends an event inside tx and returns its event id. payload is
// marshalled to JSON ([]byte and json.RawMessage are taken as JSON already).
//
// Publishers of one outbox are serialized by a transaction-scoped advisory
// lock taken right before the insert, so sequence numbers become visible in
// increasing order: a consumer that has seen seq N can never later find a
// committed event with a smaller seq. Publish as the last locking step of a
// transaction, so the lock is held only for the commit.
func (o *Outbox) Publish(ctx context.Context, tx DBTX, topic string, payload any) (string, error) {
	if topic == "" {
		return "", errors.New("eventbus: topic required")
	}
	var body []byte
	switch p := payload.(type) {
	case []byte:
		body = p
	case json.RawMessage:
		body = p
	default:
		b, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("eventbus: encode %s: %w", topic, err)
		}
		body = b
	}
	if !json.Valid(body) {
		return "", fmt.Errorf("eventbus: %s payload is not valid JSON", topic)
	}
	id := buuid.MustV7().String()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, o.lockKey); err != nil {
		return "", fmt.Errorf("eventbus: publish lock: %w", err)
	}
	if _, err := tx.Exec(ctx, o.insert, topic, body, id); err != nil {
		return "", fmt.Errorf("eventbus: publish %s: %w", topic, err)
	}
	return id, nil
}
