package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GatewayProviders are the automated providers the reconciler checks.
var GatewayProviders = []string{"zarinpal"}

// SetGatewayStart records the gateway's id for the payment and where the
// customer pays. It only applies to an open intent without an external id, so a
// retried creation cannot swap one gateway payment for another.
func (s *Store) SetGatewayStart(ctx context.Context, tx pgx.Tx, id, externalID, payURL string) (*Intent, error) {
	row := s.q(tx).QueryRow(ctx, `
		UPDATE payments.payment_intents
		SET external_id = $2, pay_url = NULLIF($3, ''), updated_at = now()
		WHERE id = $1 AND status = 'pending' AND external_id IS NULL
		RETURNING `+intentCols, id, externalID, payURL)
	in, err := scanIntent(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, fmt.Errorf("set gateway start: %w", err)
	}
	return in, nil
}

// SettleGatewayPaid moves an open gateway intent to succeeded (compare-and-set
// on the status, so a payment settles once) and keeps the gateway's reference.
func (s *Store) SettleGatewayPaid(ctx context.Context, tx pgx.Tx, id, externalID, reference string) (*Intent, error) {
	row := s.q(tx).QueryRow(ctx, `
		UPDATE payments.payment_intents
		SET status = 'succeeded', external_id = COALESCE(external_id, NULLIF($2, '')),
		    provider_ref = NULLIF($3, ''), failure_reason = NULL, checked_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('pending','confirming')
		RETURNING `+intentCols, id, externalID, reference)
	in, err := scanIntent(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, fmt.Errorf("settle gateway intent: %w", err)
	}
	return in, nil
}

// FailGateway moves an open gateway intent to failed (or expired) with a reason.
func (s *Store) FailGateway(ctx context.Context, tx pgx.Tx, id, status, reason string) (*Intent, error) {
	if status != "failed" && status != "expired" {
		return nil, fmt.Errorf("fail gateway intent: bad status %q", status)
	}
	row := s.q(tx).QueryRow(ctx, `
		UPDATE payments.payment_intents
		SET status = $2, failure_reason = NULLIF($3, ''), checked_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('pending','confirming')
		RETURNING `+intentCols, id, status, reason)
	in, err := scanIntent(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, fmt.Errorf("fail gateway intent: %w", err)
	}
	return in, nil
}

// TouchCheck records that the gateway was asked about an intent.
func (s *Store) TouchCheck(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payments.payment_intents SET checked_at = now(), check_attempts = check_attempts + 1
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("touch check: %w", err)
	}
	return nil
}

// DueGatewayIntents lists open gateway intents with an external id that were
// not checked within minGap, least recently checked first.
func (s *Store) DueGatewayIntents(ctx context.Context, minGap time.Duration, limit int) ([]*Intent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+intentCols+` FROM payments.payment_intents
		WHERE status IN ('pending','confirming') AND provider = ANY($1) AND external_id IS NOT NULL
		  AND (checked_at IS NULL OR checked_at < now() - $2::interval * LEAST(power(2, check_attempts), 64))
		ORDER BY checked_at NULLS FIRST, created_at
		LIMIT $3`, GatewayProviders, minGap.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("due gateway intents: %w", err)
	}
	defer rows.Close()
	var out []*Intent
	for rows.Next() {
		in, err := scanIntent(rows)
		if err != nil {
			return nil, fmt.Errorf("due gateway intents: %w", err)
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// IntentByExternalID finds the intent a gateway notification refers to.
func (s *Store) IntentByExternalID(ctx context.Context, provider, externalID string) (*Intent, error) {
	in, err := scanIntent(s.db.QueryRow(ctx, `
		SELECT `+intentCols+` FROM payments.payment_intents WHERE provider = $1 AND external_id = $2`,
		provider, externalID))
	if err != nil {
		return nil, fmt.Errorf("intent by external id: %w", err)
	}
	return in, nil
}

// RecordGatewayEvent appends to the audit trail. detail must already be
// redacted (no card numbers, tokens or signatures).
func (s *Store) RecordGatewayEvent(ctx context.Context, tx pgx.Tx, intentID, provider, kind, outcome string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("gateway event: %w", err)
	}
	var iid *string
	if intentID != "" {
		iid = &intentID
	}
	if _, err := s.q(tx).Exec(ctx, `
		INSERT INTO payments.gateway_events (intent_id, provider, kind, outcome, detail)
		VALUES ($1, $2, $3, $4, $5)`, iid, provider, kind, outcome, b); err != nil {
		return fmt.Errorf("gateway event: %w", err)
	}
	return nil
}

// SettleStarsPaid settles a Stars intent with Telegram's charge id. Unlike
// other gateways it also accepts an expired intent: Telegram has already taken
// the Stars. The unique (provider, external_id) index means one charge settles
// at most one intent.
func (s *Store) SettleStarsPaid(ctx context.Context, tx pgx.Tx, id, chargeID string) (*Intent, error) {
	row := s.q(tx).QueryRow(ctx, `
		UPDATE payments.payment_intents
		SET status = 'succeeded', external_id = $2, provider_ref = $2, failure_reason = NULL,
		    checked_at = now(), updated_at = now()
		WHERE id = $1 AND provider = 'stars' AND status IN ('pending','confirming','expired')
		RETURNING `+intentCols, id, chargeID)
	in, err := scanIntent(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, fmt.Errorf("settle stars intent: %w", err)
	}
	return in, nil
}

// FailStars fails a Stars intent whose payment cannot settle it (also from
// expired, since the Stars were taken).
func (s *Store) FailStars(ctx context.Context, tx pgx.Tx, id, reason string) (*Intent, error) {
	row := s.q(tx).QueryRow(ctx, `
		UPDATE payments.payment_intents
		SET status = 'failed', failure_reason = $2, checked_at = now(), updated_at = now()
		WHERE id = $1 AND provider = 'stars' AND status IN ('pending','confirming','expired')
		RETURNING `+intentCols, id, reason)
	in, err := scanIntent(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, fmt.Errorf("fail stars intent: %w", err)
	}
	return in, nil
}
