package eventbus

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// PgxTx adapts pgx.Tx to DBTX.
type PgxTx struct{ Tx pgx.Tx }

// ExecPublish implements DBTX.
func (w PgxTx) ExecPublish(ctx context.Context, query string, args ...any) error {
	_, err := w.Tx.Exec(ctx, query, args...)
	return err
}
