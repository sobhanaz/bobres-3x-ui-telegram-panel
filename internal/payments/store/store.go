// Package store persists payments intents, manual receipts, and the
// provider-side ledger mirror. All money movement goes through idempotent,
// status-guarded transitions so races cannot double-approve.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

var (
	// ErrNotFound is returned by Get* when no row matches.
	ErrNotFound = errors.New("store: not found")
	// ErrInvalidTransition rejects status updates outside the allowed path
	// (double approve, submit after review).
	ErrInvalidTransition = errors.New("store: invalid status transition")
	// ErrIdempotencyConflict: the idempotency key belongs to a different intent.
	ErrIdempotencyConflict = errors.New("store: idempotency key reused for a different intent")
	// ErrDuplicateProof: this crypto TXID was already submitted for an intent.
	ErrDuplicateProof = errors.New("store: this transaction was already submitted")
)

// Intent mirrors payments.payment_intents.
type Intent struct {
	ID             string
	OrderID        *string
	UserID         string
	Provider       string
	Amount         int64
	Currency       string
	Status         string
	IdempotencyKey string
	ExpiresAt      *time.Time
	CreatedAt      time.Time

	// Automated gateways: what the customer pays there (fixed at creation),
	// the gateway's id for the payment, where to pay, and the outcome.
	GatewayAmount   *int64
	GatewayCurrency *string
	ExternalID      *string
	PayURL          *string
	ProviderRef     *string // bank reference / transaction hash, for support
	FailureReason   *string
	CheckAttempts   int // reconciler checks only (drives its backoff)
	CheckedAt       *time.Time
}

// Receipt mirrors payments.manual_receipts.
type Receipt struct {
	ID              string
	IntentID        string
	Network         *string
	TXID            *string
	ReceiptFile     *string
	ReferenceNumber *string
	SubmittedAt     time.Time
	ReviewedBy      *string
	Decision        *string
	Reason          *string
}

// Store wraps the payments-schema pool.
type Store struct{ db *pgxpool.Pool }

// New opens and pings the pool.
func New(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("payments store: connect: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("payments store: ping: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.db.Close() }

// DB exposes the pool for probes/tests.
func (s *Store) DB() *pgxpool.Pool { return s.db }

// DBTX abstracts pool and Tx.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Store) q(tx pgx.Tx) DBTX {
	if tx != nil {
		return tx
	}
	return s.db
}

// WithTx runs fn in a transaction.
func (s *Store) WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("payments store: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("payments store: commit: %w", err)
	}
	return nil
}

const intentCols = `id, order_id, user_id, provider, amount, currency, status, idempotency_key, expires_at, created_at,
	gateway_amount, gateway_currency, external_id, pay_url, provider_ref, failure_reason, check_attempts, checked_at`

func scanIntent(row pgx.Row) (*Intent, error) {
	var in Intent
	err := row.Scan(&in.ID, &in.OrderID, &in.UserID, &in.Provider, &in.Amount,
		&in.Currency, &in.Status, &in.IdempotencyKey, &in.ExpiresAt, &in.CreatedAt,
		&in.GatewayAmount, &in.GatewayCurrency, &in.ExternalID, &in.PayURL, &in.ProviderRef,
		&in.FailureReason, &in.CheckAttempts, &in.CheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &in, err
}

// CreateIntent inserts idempotently: same key returns the existing row.
func (s *Store) CreateIntent(ctx context.Context, tx pgx.Tx, in *Intent) (*Intent, error) {
	in.ID = buuid.MustV7().String()
	row := s.q(tx).QueryRow(ctx, `
		INSERT INTO payments.payment_intents
			(id, order_id, user_id, provider, amount, currency, status, idempotency_key, expires_at,
			 gateway_amount, gateway_currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING `+intentCols,
		in.ID, in.OrderID, in.UserID, in.Provider, in.Amount, in.Currency,
		in.Status, in.IdempotencyKey, in.ExpiresAt, in.GatewayAmount, in.GatewayCurrency)
	got, err := scanIntent(row)
	if err != nil {
		return nil, fmt.Errorf("create intent: %w", err)
	}
	if got.UserID != in.UserID || got.Provider != in.Provider || got.Amount != in.Amount ||
		got.Currency != in.Currency || !sameOptional(got.OrderID, in.OrderID) {
		return nil, ErrIdempotencyConflict
	}
	return got, nil
}

func sameOptional(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// GetIntent returns one intent.
func (s *Store) GetIntent(ctx context.Context, tx pgx.Tx, id string) (*Intent, error) {
	row := s.q(tx).QueryRow(ctx, `SELECT `+intentCols+` FROM payments.payment_intents WHERE id = $1`, id)
	in, err := scanIntent(row)
	if err != nil {
		return nil, fmt.Errorf("get intent: %w", err)
	}
	return in, nil
}

// transition flips status only when the current status is in from.
func (s *Store) transition(ctx context.Context, tx pgx.Tx, id string, from []string, to string) (*Intent, error) {
	row := s.q(tx).QueryRow(ctx, `
		UPDATE payments.payment_intents SET status = $3, updated_at = now()
		WHERE id = $1 AND status = ANY($2)
		RETURNING `+intentCols, id, from, to)
	in, err := scanIntent(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, fmt.Errorf("transition: %w", err)
	}
	return in, nil
}

// TransitionConfirming moves pending -> confirming (user submitted proof).
func (s *Store) TransitionConfirming(ctx context.Context, tx pgx.Tx, id string) (*Intent, error) {
	return s.transition(ctx, tx, id, []string{"pending"}, "confirming")
}

// TransitionSucceeded moves confirming -> succeeded (admin approve).
func (s *Store) TransitionSucceeded(ctx context.Context, tx pgx.Tx, id string) (*Intent, error) {
	return s.transition(ctx, tx, id, []string{"confirming"}, "succeeded")
}

// TransitionFailed moves confirming -> failed (admin reject).
func (s *Store) TransitionFailed(ctx context.Context, tx pgx.Tx, id string) (*Intent, error) {
	return s.transition(ctx, tx, id, []string{"confirming"}, "failed")
}

// SaveReceipt upserts the receipt for an intent.
func (s *Store) SaveReceipt(ctx context.Context, tx pgx.Tx, r *Receipt) error {
	r.ID = buuid.MustV7().String()
	_, err := s.q(tx).Exec(ctx, `
		INSERT INTO payments.manual_receipts
			(id, intent_id, network, txid, receipt_file, reference_number)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (intent_id) DO UPDATE SET
			network = EXCLUDED.network, txid = EXCLUDED.txid,
			receipt_file = EXCLUDED.receipt_file,
			reference_number = EXCLUDED.reference_number`,
		r.ID, r.IntentID, r.Network, r.TXID, r.ReceiptFile, r.ReferenceNumber)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "manual_receipts_txid_once_idx" {
			return ErrDuplicateProof
		}
		return fmt.Errorf("save receipt: %w", err)
	}
	return nil
}

// ReviewReceipt stamps the decision on the receipt row (once only).
func (s *Store) ReviewReceipt(ctx context.Context, tx pgx.Tx, intentID, reviewer, decision, reason string) error {
	tag, err := s.q(tx).Exec(ctx, `
		UPDATE payments.manual_receipts
		SET reviewed_by = $2, decision = $3, reason = $4, reviewed_at = now()
		WHERE intent_id = $1 AND reviewed_at IS NULL`, intentID, reviewer, decision, reason)
	if err != nil {
		return fmt.Errorf("review receipt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidTransition
	}
	return nil
}

// Pending is one intent awaiting manual review, with its proof.
type Pending struct {
	Intent  Intent
	Receipt Receipt
	// PossibleDuplicate: another receipt carries the same card reference number.
	PossibleDuplicate bool
}

// ListPendingReceipts returns intents awaiting manual review, oldest first.
func (s *Store) ListPendingReceipts(ctx context.Context, limit int) ([]Pending, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT i.id, i.order_id, i.user_id, i.provider, i.amount, i.currency, i.status, i.idempotency_key,
		       i.expires_at, i.created_at,
		       r.network, r.txid, r.receipt_file, r.reference_number, r.submitted_at,
		       (r.reference_number IS NOT NULL AND EXISTS (
		           SELECT 1 FROM payments.manual_receipts d
		           WHERE d.reference_number = r.reference_number AND d.intent_id <> r.intent_id))
		FROM payments.payment_intents i
		JOIN payments.manual_receipts r ON r.intent_id = i.id
		WHERE i.status = 'confirming' AND i.provider IN ('manual_card','manual_crypto','manual_zarinpal')
		ORDER BY r.submitted_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending: %w", err)
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var p Pending
		in := &p.Intent
		if err := rows.Scan(&in.ID, &in.OrderID, &in.UserID, &in.Provider, &in.Amount, &in.Currency,
			&in.Status, &in.IdempotencyKey, &in.ExpiresAt, &in.CreatedAt,
			&p.Receipt.Network, &p.Receipt.TXID, &p.Receipt.ReceiptFile, &p.Receipt.ReferenceNumber,
			&p.Receipt.SubmittedAt, &p.PossibleDuplicate); err != nil {
			return nil, err
		}
		p.Receipt.IntentID = in.ID
		out = append(out, p)
	}
	return out, rows.Err()
}

// AppendLedgerCredit writes an idempotent topup credit to the ledger mirror.
// balance_after mirrors the running credited total for reconciliation.
func (s *Store) AppendLedgerCredit(ctx context.Context, tx pgx.Tx, userID, currency string, amount int64, refType, refID, idemKey string) error {
	var exists bool
	if err := s.q(tx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM payments.ledger_entries WHERE idempotency_key = $1)`,
		idemKey).Scan(&exists); err != nil {
		return fmt.Errorf("ledger check: %w", err)
	}
	if exists {
		return nil
	}
	var balance int64
	if err := s.q(tx).QueryRow(ctx, `
		SELECT COALESCE((SELECT balance_after FROM payments.ledger_entries
			WHERE user_id = $1 AND currency = $2 ORDER BY created_at DESC, id DESC LIMIT 1), 0)`,
		userID, currency).Scan(&balance); err != nil {
		return fmt.Errorf("ledger balance: %w", err)
	}
	var rt, rid *string
	if refType != "" {
		rt = &refType
	}
	if refID != "" {
		rid = &refID
	}
	_, err := s.q(tx).Exec(ctx, `
		INSERT INTO payments.ledger_entries
			(id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
		VALUES ($1, $2, $3, $4, 'topup', $5, $6, $7, $8)`,
		buuid.MustV7(), userID, currency, amount, rt, rid, idemKey, balance+amount)
	if err != nil {
		return fmt.Errorf("ledger insert: %w", err)
	}
	return nil
}
