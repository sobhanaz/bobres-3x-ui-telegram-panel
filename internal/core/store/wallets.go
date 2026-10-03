package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

var (
	// ErrInsufficientFunds is returned when a debit would drop balance below 0.
	ErrInsufficientFunds = errors.New("store: insufficient funds")
	// ErrDuplicateIdempotency means the key was already used for this exact
	// movement. Nothing changed; callers may treat it as a successful replay.
	ErrDuplicateIdempotency = errors.New("store: duplicate idempotency key")
	// ErrIdempotencyConflict means the key was already used for a different
	// operation (another user, amount, kind, order...). The caller's
	// transaction must be aborted: silently accepting it would either move
	// money twice or mark something paid that was never paid.
	ErrIdempotencyConflict = errors.New("store: idempotency key reused for a different operation")
)

// ledgerKeyConstraint is the UNIQUE constraint on ledger_entries.idempotency_key.
const ledgerKeyConstraint = "ledger_entries_idempotency_key_key"

const ledgerCols = `id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at`

func scanLedger(row pgx.Row) (*LedgerEntry, error) {
	var e LedgerEntry
	err := row.Scan(&e.ID, &e.UserID, &e.Currency, &e.Amount, &e.Kind,
		&e.RefType, &e.RefID, &e.IdempotencyKey, &e.BalanceAfter, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &e, err
}

// GetWallet returns the wallet row, creating it implicitly if missing.
func (s *Store) GetWallet(ctx context.Context, q querier, userID, currency string) (*Wallet, error) {
	var w Wallet
	err := q.QueryRow(ctx, `
		INSERT INTO core.wallets (user_id, currency) VALUES ($1, $2)
		ON CONFLICT (user_id, currency) DO NOTHING
		RETURNING user_id, currency, balance, updated_at`, userID, currency).
		Scan(&w.UserID, &w.Currency, &w.Balance, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = q.QueryRow(ctx,
			`SELECT user_id, currency, balance, updated_at FROM core.wallets WHERE user_id=$1 AND currency=$2`,
			userID, currency).
			Scan(&w.UserID, &w.Currency, &w.Balance, &w.UpdatedAt)
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return &w, nil
}

// lockWallet ensures the wallet exists and locks its row for the rest of tx.
// Every balance change for one user+currency serializes on this lock.
func lockWallet(ctx context.Context, tx pgx.Tx, userID, currency string) (int64, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO core.wallets (user_id, currency) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, currency); err != nil {
		return 0, fmt.Errorf("wallet upsert: %w", err)
	}
	var balance int64
	if err := tx.QueryRow(ctx,
		`SELECT balance FROM core.wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`,
		userID, currency).Scan(&balance); err != nil {
		return 0, fmt.Errorf("wallet lock: %w", err)
	}
	return balance, nil
}

// LedgerByKey returns the entry recorded under an idempotency key.
func (s *Store) LedgerByKey(ctx context.Context, q querier, key string) (*LedgerEntry, error) {
	e, err := scanLedger(q.QueryRow(ctx, `SELECT `+ledgerCols+` FROM core.ledger_entries WHERE idempotency_key = $1`, key))
	if err != nil {
		return nil, fmt.Errorf("ledger by key: %w", err)
	}
	return e, nil
}

// move is the only code path that changes a wallet balance. It locks the
// wallet, THEN checks the idempotency key, so a replay (even a concurrent
// one) never moves money: it returns ErrDuplicateIdempotency with the
// balance untouched. e.Amount is stored signed (debits negative), so a
// wallet's balance always equals the SUM of its ledger amounts.
func move(ctx context.Context, tx pgx.Tx, e *LedgerEntry, delta int64) (*Wallet, error) {
	if e.UserID == "" || e.Currency == "" || e.Kind == "" || e.IdempotencyKey == "" {
		return nil, errors.New("ledger: user, currency, kind and idempotency key are required")
	}
	balance, err := lockWallet(ctx, tx, e.UserID, e.Currency)
	if err != nil {
		return nil, err
	}
	prev, err := scanLedger(tx.QueryRow(ctx,
		`SELECT `+ledgerCols+` FROM core.ledger_entries WHERE idempotency_key = $1`, e.IdempotencyKey))
	switch {
	case err == nil:
		if prev.UserID != e.UserID || prev.Currency != e.Currency || prev.Amount != delta || prev.Kind != e.Kind {
			return nil, ErrIdempotencyConflict
		}
		*e = *prev
		return &Wallet{UserID: e.UserID, Currency: e.Currency, Balance: balance}, ErrDuplicateIdempotency
	case !errors.Is(err, ErrNotFound):
		return nil, fmt.Errorf("ledger lookup: %w", err)
	}
	next := balance + delta
	if next < 0 {
		return nil, ErrInsufficientFunds
	}
	if delta != 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE core.wallets SET balance = $3, updated_at = now()
			WHERE user_id = $1 AND currency = $2`, e.UserID, e.Currency, next); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.ConstraintName == "wallets_balance_check" {
				return nil, ErrInsufficientFunds
			}
			return nil, fmt.Errorf("wallet update: %w", err)
		}
	}
	e.Amount, e.BalanceAfter = delta, next
	if err := insertLedger(ctx, tx, e); err != nil {
		return nil, err
	}
	return &Wallet{UserID: e.UserID, Currency: e.Currency, Balance: next}, nil
}

// insertLedger appends an entry. A key collision here (the same key used for
// a different user's wallet concurrently) is a conflict, never a replay: the
// balance already moved in this transaction, so it must roll back.
func insertLedger(ctx context.Context, tx pgx.Tx, e *LedgerEntry) error {
	err := tx.QueryRow(ctx, `
		INSERT INTO core.ledger_entries
			(id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at`,
		buuid.MustV7(), e.UserID, e.Currency, e.Amount, e.Kind,
		e.RefType, e.RefID, e.IdempotencyKey, e.BalanceAfter).Scan(&e.ID, &e.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == ledgerKeyConstraint {
			return ErrIdempotencyConflict
		}
		// Keep the pg error unwrap-able: callers key off other constraints
		// (e.g. the trial-once partial index) by name.
		return fmt.Errorf("ledger: %w", err)
	}
	return nil
}

// Credit adds amount (>0) in tx.
func (s *Store) Credit(ctx context.Context, tx pgx.Tx, userID, currency string, amount int64, e *LedgerEntry) (*Wallet, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("credit amount must be positive: %d", amount)
	}
	e.UserID, e.Currency = userID, currency
	return move(ctx, tx, e, amount)
}

// Debit subtracts amount (>0) in tx; insufficient funds abort the transaction.
func (s *Store) Debit(ctx context.Context, tx pgx.Tx, userID, currency string, amount int64, e *LedgerEntry) (*Wallet, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("debit amount must be positive: %d", amount)
	}
	e.UserID, e.Currency = userID, currency
	return move(ctx, tx, e, -amount)
}

// AppendLedger records a zero-amount entry (e.g. a trial grant). It follows
// the same lock-then-check rule as money movements.
func (s *Store) AppendLedger(ctx context.Context, tx pgx.Tx, e *LedgerEntry) error {
	_, err := move(ctx, tx, e, 0)
	return err
}

// ListLedger returns a user's ledger, newest first.
func (s *Store) ListLedger(ctx context.Context, q querier, userID string, limit, offset int) ([]LedgerEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := q.Query(ctx, `SELECT `+ledgerCols+` FROM core.ledger_entries
		WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list ledger: %w", err)
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		e, err := scanLedger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}
