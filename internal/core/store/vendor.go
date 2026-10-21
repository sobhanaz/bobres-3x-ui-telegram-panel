package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Selling BOBRES licenses from the owner's own store (migration 00032): where the license server is, the licenses a
// customer bought, and the purchases and renewals that make them. Only the owner's install has rows here.

// VendorConfig is where the license server is and the (encrypted) key that signs requests to it.
type VendorConfig struct {
	AdminURL    string
	AdminKeyEnc []byte
	UpdatedAt   time.Time
}

// VendorLicense is a license a customer bought. KeyEnc is its activation key under core's master key.
type VendorLicense struct {
	ID        string
	UserID    string
	LicenseID string // the license server's id; "" until the server made it
	Tier      string
	EndsAt    *time.Time
	Status    string
	KeyEnc    []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

// VendorOrder is one purchase ("new") or renewal ("renew") of a license.
type VendorOrder struct {
	ID              string
	UserID          string
	VendorLicenseID *string
	Kind            string
	Tier            string
	Months          int
	Amount          int64
	Currency        string
	Status          string // paid, done, refunded
	IdempotencyKey  string
	Attempts        int
	NextAttemptAt   time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// GetVendorConfig returns the license server's address and key, or ErrNotFound when none is set.
func (s *Store) GetVendorConfig(ctx context.Context, q querier) (*VendorConfig, error) {
	var c VendorConfig
	err := q.QueryRow(ctx, `SELECT admin_url, admin_key_enc, updated_at FROM core.vendor_config WHERE id`).Scan(&c.AdminURL, &c.AdminKeyEnc, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get vendor config: %w", err)
	}
	return &c, nil
}

// SetVendorConfig stores the address and the encrypted key (replacing any before).
func (s *Store) SetVendorConfig(ctx context.Context, q querier, url string, keyEnc []byte) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.vendor_config (id, admin_url, admin_key_enc) VALUES (true, $1, $2)
		ON CONFLICT (id) DO UPDATE SET admin_url = EXCLUDED.admin_url, admin_key_enc = EXCLUDED.admin_key_enc, updated_at = now()`,
		url, keyEnc)
	if err != nil {
		return fmt.Errorf("set vendor config: %w", err)
	}
	return nil
}

const vendorLicenseCols = `id, user_id, coalesce(license_id, ''), tier, ends_at, status, key_enc, created_at, updated_at`

func scanVendorLicense(row pgx.Row) (*VendorLicense, error) {
	var l VendorLicense
	err := row.Scan(&l.ID, &l.UserID, &l.LicenseID, &l.Tier, &l.EndsAt, &l.Status, &l.KeyEnc, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &l, err
}

// CreateVendorLicense inserts a license the server made.
func (s *Store) CreateVendorLicense(ctx context.Context, q querier, l *VendorLicense) error {
	l.ID = buuid.MustV7().String()
	_, err := q.Exec(ctx, `
		INSERT INTO core.vendor_licenses (id, user_id, license_id, tier, ends_at, status, key_enc)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		l.ID, l.UserID, l.LicenseID, l.Tier, l.EndsAt, l.Status, l.KeyEnc)
	if err != nil {
		return fmt.Errorf("create vendor license: %w", err)
	}
	return nil
}

// GetVendorLicense returns a license by id; forUpdate locks the row.
func (s *Store) GetVendorLicense(ctx context.Context, q querier, id string, forUpdate bool) (*VendorLicense, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanVendorLicense(q.QueryRow(ctx, `SELECT `+vendorLicenseCols+` FROM core.vendor_licenses WHERE id = $1`+lock, id))
}

// UserVendorLicenses lists a customer's licenses, newest first.
func (s *Store) UserVendorLicenses(ctx context.Context, q querier, userID string) ([]VendorLicense, error) {
	rows, err := q.Query(ctx, `SELECT `+vendorLicenseCols+` FROM core.vendor_licenses WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT 50`, userID)
	if err != nil {
		return nil, fmt.Errorf("list vendor licenses: %w", err)
	}
	defer rows.Close()
	var out []VendorLicense
	for rows.Next() {
		l, err := scanVendorLicense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// SetVendorLicenseEnd records a renewal's new end.
func (s *Store) SetVendorLicenseEnd(ctx context.Context, q querier, id string, ends *time.Time) error {
	tag, err := q.Exec(ctx, `UPDATE core.vendor_licenses SET ends_at = $2, updated_at = now() WHERE id = $1`, id, ends)
	if err != nil {
		return fmt.Errorf("set vendor license end: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const vendorOrderCols = `id, user_id, vendor_license_id, kind, tier, months, amount, currency, status, idempotency_key,
	attempts, next_attempt_at, last_error, created_at, updated_at`

func scanVendorOrder(row pgx.Row) (*VendorOrder, error) {
	var o VendorOrder
	err := row.Scan(&o.ID, &o.UserID, &o.VendorLicenseID, &o.Kind, &o.Tier, &o.Months, &o.Amount, &o.Currency, &o.Status,
		&o.IdempotencyKey, &o.Attempts, &o.NextAttemptAt, &o.LastError, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &o, err
}

// CreateVendorOrder inserts an order and reports whether it is new. A retry with the same idempotency key returns
// the existing order (inserted=false); the same key with other terms is ErrIdempotencyConflict.
func (s *Store) CreateVendorOrder(ctx context.Context, q querier, o *VendorOrder) (*VendorOrder, bool, error) {
	o.ID = buuid.MustV7().String()
	var inserted bool
	row := q.QueryRow(ctx, `
		INSERT INTO core.vendor_orders (id, user_id, vendor_license_id, kind, tier, months, amount, currency, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING `+vendorOrderCols+`, (xmax = 0)`,
		o.ID, o.UserID, o.VendorLicenseID, o.Kind, o.Tier, o.Months, o.Amount, o.Currency, o.IdempotencyKey)
	var got VendorOrder
	err := row.Scan(&got.ID, &got.UserID, &got.VendorLicenseID, &got.Kind, &got.Tier, &got.Months, &got.Amount, &got.Currency,
		&got.Status, &got.IdempotencyKey, &got.Attempts, &got.NextAttemptAt, &got.LastError, &got.CreatedAt, &got.UpdatedAt, &inserted)
	if err != nil {
		return nil, false, fmt.Errorf("create vendor order: %w", err)
	}
	if !inserted && (got.UserID != o.UserID || got.Kind != o.Kind || got.Tier != o.Tier || got.Months != o.Months ||
		(o.Kind == "renew" && deref(got.VendorLicenseID) != deref(o.VendorLicenseID))) {
		return nil, false, ErrIdempotencyConflict
	}
	return &got, inserted, nil
}

// GetVendorOrder returns an order by id; forUpdate locks the row.
func (s *Store) GetVendorOrder(ctx context.Context, q querier, id string, forUpdate bool) (*VendorOrder, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanVendorOrder(q.QueryRow(ctx, `SELECT `+vendorOrderCols+` FROM core.vendor_orders WHERE id = $1`+lock, id))
}

// DueVendorOrders lists the ids of paid orders whose next attempt is due, oldest first.
func (s *Store) DueVendorOrders(ctx context.Context, q querier, now time.Time, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM core.vendor_orders WHERE status = 'paid' AND next_attempt_at <= $1 ORDER BY next_attempt_at, id LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("due vendor orders: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FinishVendorOrder marks a paid order done (with its license) or refunded.
func (s *Store) FinishVendorOrder(ctx context.Context, q querier, id, status string, licenseID *string, lastError string) error {
	tag, err := q.Exec(ctx, `
		UPDATE core.vendor_orders SET status = $2, vendor_license_id = coalesce($3, vendor_license_id), last_error = $4, updated_at = now()
		WHERE id = $1 AND status = 'paid'`, id, status, licenseID, lastError)
	if err != nil {
		return fmt.Errorf("finish vendor order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeferVendorOrder records a failed attempt and when the next one is due.
func (s *Store) DeferVendorOrder(ctx context.Context, q querier, id string, next time.Time, lastError string) error {
	_, err := q.Exec(ctx, `
		UPDATE core.vendor_orders SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
		WHERE id = $1 AND status = 'paid'`, id, next, lastError)
	if err != nil {
		return fmt.Errorf("defer vendor order: %w", err)
	}
	return nil
}

// VendorLicenseRow is a sold license with who bought it, for the dashboard.
type VendorLicenseRow struct {
	VendorLicense
	TelegramID int64
	Username   string
}

// ListVendorLicenses lists every sold license, newest first, with the number of matches; status filters when not "".
func (s *Store) ListVendorLicenses(ctx context.Context, q querier, status string, p Page) ([]VendorLicenseRow, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `
		SELECT l.id, l.user_id, coalesce(l.license_id, ''), l.tier, l.ends_at, l.status, l.key_enc, l.created_at, l.updated_at,
		       u.telegram_id, coalesce(u.username, ''), count(*) OVER ()
		FROM core.vendor_licenses l JOIN core.users u ON u.id = l.user_id
		WHERE $1 = '' OR l.status = $1
		ORDER BY l.created_at DESC, l.id DESC LIMIT $2 OFFSET $3`, status, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list vendor licenses: %w", err)
	}
	defer rows.Close()
	var out []VendorLicenseRow
	var total int64
	for rows.Next() {
		var r VendorLicenseRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.LicenseID, &r.Tier, &r.EndsAt, &r.Status, &r.KeyEnc, &r.CreatedAt, &r.UpdatedAt,
			&r.TelegramID, &r.Username, &total); err != nil {
			return nil, 0, err
		}
		r.KeyEnc = nil // a list never carries the key
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// SetVendorLicenseStatus marks a sold license active or revoked.
func (s *Store) SetVendorLicenseStatus(ctx context.Context, q querier, id, status string) error {
	tag, err := q.Exec(ctx, `UPDATE core.vendor_licenses SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("set vendor license status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
