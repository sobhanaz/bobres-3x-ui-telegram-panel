// Package migrate applies the embedded goose migrations for one service
// schema. Schemas are whitelisted to prevent SQL injection through schema
// names, and a Postgres advisory lock serializes concurrent migrators.
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations
var fsys embed.FS

// advisoryLockID is arbitrary but fixed; it only has to not collide with
// other users of advisory locks in the same database. It is shared so that
// core/payments/provisioner migrations never run concurrently with each
// other (they live in one database and one migrator at a time is enough).
const advisoryLockID int64 = 872134

// ErrUnknownSchema is returned for schema names outside the whitelist.
var ErrUnknownSchema = errors.New("migrate: unknown schema")

var allowed = map[string]bool{
	"core":        true,
	"payments":    true,
	"provisioner": true,
}

// globalMu serializes Up calls because goose.SetBaseFS mutates package-global
// state.
var globalMu sync.Mutex

// Up applies all pending migrations for the given schema. The DSN is opened
// with a single-connection pool so that the pg_advisory_lock, SET search_path,
// and every migration statement share one session. A pooled *sql.DB would
// risk goose bookkeeping statements landing on a connection with a different
// search_path.
func Up(ctx context.Context, dsn, schema string) error {
	if !allowed[schema] {
		return fmt.Errorf("%w: %q", ErrUnknownSchema, schema)
	}
	globalMu.Lock()
	defer globalMu.Unlock()

	goose.SetBaseFS(fsys)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: dialect: %w", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("migrate: open: %w", err)
	}
	defer db.Close() //nolint:errcheck // close error irrelevant after Up result
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		return fmt.Errorf("migrate: advisory lock: %w", err)
	}
	// Session-scoped lock: released automatically when db closes.
	if _, err := db.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		return fmt.Errorf("migrate: set search_path: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations/"+schema); err != nil {
		return fmt.Errorf("migrate: up %s: %w", schema, err)
	}
	return nil
}

// Schemas lists the whitelisted schema names.
func Schemas() []string { return []string{"core", "payments", "provisioner"} }
