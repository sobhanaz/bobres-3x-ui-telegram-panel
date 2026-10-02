package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// ErrInsufficientFunds is returned when a debit would drop balance below 0.
var ErrInsufficientFunds = errors.New("store: insufficient funds")

// ErrDuplicateIdempotency marks a replayed ledger operation with a different payload.
var ErrDuplicateIdempotency = errors.New("store: duplicate idempotency key")

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

// writeLedger appends an entry; idempotency makes retries return the
// existing entry instead of moving money twice.
func writeLedger(ctx context.Context, tx pgx.Tx, e *LedgerEntry) (*LedgerEntry, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO core.ledger_entries
			(id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id, created_at`,
		buuid.MustV7(), e.UserID, e.Currency, e.Amount, e.Kind,
		e.RefType, e.RefID, e.IdempotencyKey, e.BalanceAfter)
	err := row.Scan(&e.ID, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDuplicateIdempotency
	}
	if err != nil {
		// Keep the pg error unwrap-able so callers can key off constraint names
		// (e.g. the trial-once partial index) and SQLSTATE codes.
		return nil, fmt.Errorf("ledger: %w", err)
	}
	return e, nil
}

// applyBalance moves balance by delta (may be negative) inside tx and writes
// the ledger entry, enforcing the no-negative-balance rule atomically.
// Returns ErrDuplicateIdempotency without changing anything on replay.
func applyBalance(ctx context.Context, tx pgx.Tx, userID, currency string, delta int64, e *LedgerEntry) (*Wallet, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO core.wallets (user_id, currency) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, currency); err != nil {
		return nil, fmt.Errorf("wallet upsert: %w", err)
	}
	var balance int64
	err := tx.QueryRow(ctx, `
		UPDATE core.wallets SET balance = balance + $3, updated_at = now()
		WHERE user_id = $1 AND currency = $2
		RETURNING balance`, userID, currency, delta).Scan(&balance)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "wallets_balance_check" {
			return nil, ErrInsufficientFunds
		}
		return nil, fmt.Errorf("wallet update: %w", err)
	}
	e.BalanceAfter = balance
	if _, err := writeLedger(ctx, tx, e); err != nil {
		return nil, err
	}
	return &Wallet{UserID: userID, Currency: currency, Balance: balance}, nil
}

// AppendLedger writes a zero-balance-movement entry (trial grants, purchase
// records for manual payments) using the current wallet balance as
// balance_after.
func (s *Store) AppendLedger(ctx context.Context, tx pgx.Tx, e *LedgerEntry) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO core.wallets (user_id, currency) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, e.UserID, e.Currency); err != nil {
		return fmt.Errorf("wallet upsert: %w", err)
	}
	var balance int64
	if err := tx.QueryRow(ctx,
		`SELECT balance FROM core.wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`,
		e.UserID, e.Currency).Scan(&balance); err != nil {
		return fmt.Errorf("wallet read: %w", err)
	}
	e.BalanceAfter = balance
	_, err := writeLedger(ctx, tx, e)
	return err
}

// Debit subtracts amount (>0) in tx; insufficient funds abort the transaction.
func (s *Store) Debit(ctx context.Context, tx pgx.Tx, userID, currency string, amount int64, e *LedgerEntry) (*Wallet, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("debit amount must be positive: %d", amount)
	}
	return applyBalance(ctx, tx, userID, currency, -amount, e)
}

// Credit adds amount (>0) in tx.
func (s *Store) Credit(ctx context.Context, tx pgx.Tx, userID, currency string, amount int64, e *LedgerEntry) (*Wallet, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("credit amount must be positive: %d", amount)
	}
	return applyBalance(ctx, tx, userID, currency, amount, e)
}
