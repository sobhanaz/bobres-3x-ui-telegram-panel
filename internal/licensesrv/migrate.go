package licensesrv

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" database/sql driver goose needs
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrationFiles are the license server's own migrations. They are not in
// internal/migrate: a customer install never has these tables.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// lockID is the advisory lock that keeps two migrators apart. It differs
// from internal/migrate's, although the databases never meet.
const lockID int64 = 872135

// Migrate makes the licensesrv schema if it is missing (the role needs
// CREATE on its database, which the database's owner has) and applies every
// pending migration. It is safe to run on every start and from two
// processes at once.
func Migrate(ctx context.Context, dsn string, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("migrate: open: %w", err)
	}
	defer db.Close() //nolint:errcheck // close error irrelevant after the result
	if err := createSchema(ctx, db); err != nil {
		return err
	}
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(lockID))
	if err != nil {
		return fmt.Errorf("migrate: locker: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, sub,
		goose.WithTableName("licensesrv.goose_db_version"),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		goose.WithSlog(log),
	)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

// createSchema makes the schema under a transaction-scoped advisory lock:
// two CREATE SCHEMA IF NOT EXISTS racing on a fresh database can otherwise
// fail on pg_namespace's unique index.
func createSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID+1); err != nil {
		return fmt.Errorf("migrate: schema lock: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS licensesrv`); err != nil {
		return fmt.Errorf("migrate: create schema licensesrv: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: create schema licensesrv: %w", err)
	}
	return nil
}
