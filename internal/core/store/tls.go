package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TLSRow is the HTTPS setting Caddy last accepted (core.tls_config).
type TLSRow struct {
	// Mode is auto, ip or manual.
	Mode string
	// IP is ip mode's address ("" otherwise).
	IP string
	// CertPEM and KeySealed are manual mode's chain and its private key,
	// sealed with core's master key (empty otherwise).
	CertPEM   string
	KeySealed []byte
	UpdatedAt time.Time
}

// tlsLockKey is the advisory lock that serializes HTTPS changes (the core
// server and `/app ssl` both take it). It only has to differ from the
// migrator's (migrate.advisoryLockID).
const tlsLockKey int64 = 0x746c73 // "tls"

// GetTLS reads the HTTPS setting. A missing row (a store emptied by tests)
// reads as auto mode.
func (s *Store) GetTLS(ctx context.Context, q querier) (*TLSRow, error) {
	var (
		r       TLSRow
		ip, pem *string
	)
	err := q.QueryRow(ctx, `SELECT mode, ip, cert_pem, key_sealed, updated_at FROM core.tls_config WHERE id = 1`).
		Scan(&r.Mode, &ip, &pem, &r.KeySealed, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &TLSRow{Mode: "auto"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tls: %w", err)
	}
	if ip != nil {
		r.IP = *ip
	}
	if pem != nil {
		r.CertPEM = *pem
	}
	return &r, nil
}

// SetTLS replaces the HTTPS setting (empty values are stored as none).
func (s *Store) SetTLS(ctx context.Context, q querier, r *TLSRow) error {
	null := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	var key []byte
	if len(r.KeySealed) > 0 {
		key = r.KeySealed
	}
	_, err := q.Exec(ctx, `
		INSERT INTO core.tls_config (id, mode, ip, cert_pem, key_sealed, updated_at) VALUES (1, $1, $2, $3, $4, now())
		ON CONFLICT (id) DO UPDATE SET mode = EXCLUDED.mode, ip = EXCLUDED.ip, cert_pem = EXCLUDED.cert_pem,
			key_sealed = EXCLUDED.key_sealed, updated_at = now()`,
		r.Mode, null(r.IP), null(r.CertPEM), key)
	if err != nil {
		return fmt.Errorf("set tls: %w", err)
	}
	return nil
}

// LockTLS holds the HTTPS lock until tx ends, waiting for it.
func (s *Store) LockTLS(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, tlsLockKey); err != nil {
		return fmt.Errorf("lock tls: %w", err)
	}
	return nil
}

// TryLockTLS takes the HTTPS lock until tx ends when nobody holds it
// (false: somebody does).
func (s *Store) TryLockTLS(ctx context.Context, tx pgx.Tx) (bool, error) {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, tlsLockKey).Scan(&ok); err != nil {
		return false, fmt.Errorf("lock tls: %w", err)
	}
	return ok, nil
}
