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
	// ErrInsufficientFunds is returned when a debit would drop the balance
	// below 0 (below -credit_limit for a reseller).
	ErrInsufficientFunds = errors.New("store: insufficient funds")
	// ErrDuplicateIdempotency means the key was already used for this exact
	// movement. Nothing changed; callers may treat it as a successful replay.
	ErrDuplicateIdempotency = errors.New("store: duplicate idempotency key")
	// ErrIdempotencyConflict means the key was already used for a different
	// operation (another user, amount, kind, order...). The caller's
	// transaction must be aborted: silently accepting it would either move
	// money twice or mark something paid that was never paid.
	ErrIdempotencyConflict = errors.New("store: idempotency key reused for a different operation")
	// ErrBalanceOverflow means a credit would take the balance past what a
	// bigint holds. Nothing changed. (Never seen in practice; a wrapped sum
	// must still never be stored.)
	ErrBalanceOverflow = errors.New("store: wallet balance would overflow")
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

const walletCols = `user_id, currency, balance, credit_limit, updated_at`

func walletDest(w *Wallet) []any {
	return []any{&w.UserID, &w.Currency, &w.Balance, &w.CreditLimit, &w.UpdatedAt}
}

// GetWallet returns the wallet row, creating it implicitly if missing.
func (s *Store) GetWallet(ctx context.Context, q querier, userID, currency string) (*Wallet, error) {
	var w Wallet
	err := q.QueryRow(ctx, `
		INSERT INTO core.wallets (user_id, currency) VALUES ($1, $2)
		ON CONFLICT (user_id, currency) DO NOTHING
		RETURNING `+walletCols, userID, currency).
		Scan(walletDest(&w)...)
	if errors.Is(err, pgx.ErrNoRows) {
		err = q.QueryRow(ctx,
			`SELECT `+walletCols+` FROM core.wallets WHERE user_id=$1 AND currency=$2`,
			userID, currency).
			Scan(walletDest(&w)...)
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return &w, nil
}

// lockWallet ensures the wallet exists and locks its row for the rest of tx.
// Every balance change for one user+currency serializes on this lock, and the
// credit limit read here holds until the change commits.
func lockWallet(ctx context.Context, tx pgx.Tx, userID, currency string) (balance, limit int64, err error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO core.wallets (user_id, currency) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, currency); err != nil {
		return 0, 0, fmt.Errorf("wallet upsert: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT balance, credit_limit FROM core.wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`,
		userID, currency).Scan(&balance, &limit); err != nil {
		return 0, 0, fmt.Errorf("wallet lock: %w", err)
	}
	return balance, limit, nil
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
//
// A debit may take the balance down to -credit_limit (0 for everyone but
// resellers) and no further. A credit or a zero entry always goes through,
// even when the balance stays below zero: it only lowers a reseller's debt.
func move(ctx context.Context, tx pgx.Tx, e *LedgerEntry, delta int64) (*Wallet, error) {
	if e.UserID == "" || e.Currency == "" || e.Kind == "" || e.IdempotencyKey == "" {
		return nil, errors.New("ledger: user, currency, kind and idempotency key are required")
	}
	balance, limit, err := lockWallet(ctx, tx, e.UserID, e.Currency)
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
		return &Wallet{UserID: e.UserID, Currency: e.Currency, Balance: balance, CreditLimit: limit}, ErrDuplicateIdempotency
	case !errors.Is(err, ErrNotFound):
		return nil, fmt.Errorf("ledger lookup: %w", err)
	}
	next := balance + delta
	if delta > 0 && next < balance {
		return nil, ErrBalanceOverflow
	}
	if delta < 0 && next < -limit {
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
	return &Wallet{UserID: e.UserID, Currency: e.Currency, Balance: next, CreditLimit: limit}, nil
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

// DebitAtMost takes up to most (> 0) from a wallet in tx: the most it holds, never more, so the balance does not go below zero. A wallet that
// holds nothing, or owes (a reseller's debt), loses nothing and no entry is written (taken is 0). It follows the lock-then-check rule of every
// movement: under the wallet's lock a key already in the ledger is a replay and answers ErrDuplicateIdempotency with the first entry, whatever the
// balance is now.
func (s *Store) DebitAtMost(ctx context.Context, tx pgx.Tx, userID, currency string, most int64, e *LedgerEntry) (taken int64, w *Wallet, err error) {
	if most <= 0 {
		return 0, nil, fmt.Errorf("debit amount must be positive: %d", most)
	}
	if userID == "" || currency == "" || e.Kind == "" || e.IdempotencyKey == "" {
		return 0, nil, errors.New("ledger: user, currency, kind and idempotency key are required")
	}
	balance, limit, err := lockWallet(ctx, tx, userID, currency)
	if err != nil {
		return 0, nil, err
	}
	prev, err := scanLedger(tx.QueryRow(ctx,
		`SELECT `+ledgerCols+` FROM core.ledger_entries WHERE idempotency_key = $1`, e.IdempotencyKey))
	switch {
	case err == nil:
		if prev.UserID != userID || prev.Currency != currency || prev.Kind != e.Kind || prev.Amount >= 0 {
			return 0, nil, ErrIdempotencyConflict
		}
		*e = *prev
		return -prev.Amount, &Wallet{UserID: userID, Currency: currency, Balance: balance, CreditLimit: limit}, ErrDuplicateIdempotency
	case !errors.Is(err, ErrNotFound):
		return 0, nil, fmt.Errorf("ledger lookup: %w", err)
	}
	taken = min(most, max(balance, 0))
	if taken == 0 {
		return 0, &Wallet{UserID: userID, Currency: currency, Balance: balance, CreditLimit: limit}, nil
	}
	e.UserID, e.Currency = userID, currency
	w, err = move(ctx, tx, e, -taken)
	return taken, w, err
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

// ListWallets returns every wallet of a user.
func (s *Store) ListWallets(ctx context.Context, q querier, userID string) ([]Wallet, error) {
	rows, err := q.Query(ctx, `SELECT `+walletCols+` FROM core.wallets
		WHERE user_id = $1 ORDER BY currency`, userID)
	if err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	return collectWallets(rows)
}

// collectWallets reads wallet rows (walletCols) and closes them.
func collectWallets(rows pgx.Rows) ([]Wallet, error) {
	defer rows.Close()
	var out []Wallet
	for rows.Next() {
		var w Wallet
		if err := rows.Scan(walletDest(&w)...); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ForeignBalance is a wallet that holds money in a currency other than Toman.
type ForeignBalance struct {
	UserID, Currency string
	Balance          int64
}

// ForeignBalances lists wallets (other than Toman) that hold money, oldest update first, at most limit: what the sweeper converts to Toman.
func (s *Store) ForeignBalances(ctx context.Context, q querier, limit int) ([]ForeignBalance, error) {
	rows, err := q.Query(ctx, `SELECT user_id::text, currency, balance FROM core.wallets
		WHERE currency <> 'IRT' AND balance > 0 ORDER BY updated_at, user_id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("foreign balances: %w", err)
	}
	defer rows.Close()
	var out []ForeignBalance
	for rows.Next() {
		var b ForeignBalance
		if err := rows.Scan(&b.UserID, &b.Currency, &b.Balance); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
