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

// VendorConfig is the (encrypted) key that signs requests to the license server's admin API.
type VendorConfig struct {
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
	// ListAmount, ListCurrency and Rate, when the order was charged in another currency than its list price (a license is priced in USDT and
	// charged from the Toman wallet): the list price and the Toman per USDT it was charged at. Kept for the records, not read back.
	ListAmount     int64
	ListCurrency   string
	Rate           int64
	IdempotencyKey string
	Attempts       int
	NextAttemptAt  time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// AffiliateID is the affiliate who brought the buyer (migration 00054), recorded on a new license of a referred customer; nil otherwise.
	AffiliateID *string
}

// GetVendorConfig returns the admin key, or ErrNotFound when none is set.
func (s *Store) GetVendorConfig(ctx context.Context, q querier) (*VendorConfig, error) {
	var c VendorConfig
	err := q.QueryRow(ctx, `SELECT admin_key_enc, updated_at FROM core.vendor_config WHERE id`).Scan(&c.AdminKeyEnc, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get vendor config: %w", err)
	}
	return &c, nil
}

// SetVendorConfig stores the encrypted key (replacing any before).
func (s *Store) SetVendorConfig(ctx context.Context, q querier, keyEnc []byte) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.vendor_config (id, admin_key_enc) VALUES (true, $1)
		ON CONFLICT (id) DO UPDATE SET admin_key_enc = EXCLUDED.admin_key_enc, updated_at = now()`,
		keyEnc)
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
	attempts, next_attempt_at, last_error, created_at, updated_at, affiliate_id`

func scanVendorOrder(row pgx.Row) (*VendorOrder, error) {
	var o VendorOrder
	err := row.Scan(&o.ID, &o.UserID, &o.VendorLicenseID, &o.Kind, &o.Tier, &o.Months, &o.Amount, &o.Currency, &o.Status,
		&o.IdempotencyKey, &o.Attempts, &o.NextAttemptAt, &o.LastError, &o.CreatedAt, &o.UpdatedAt, &o.AffiliateID)
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
		INSERT INTO core.vendor_orders (id, user_id, vendor_license_id, kind, tier, months, amount, currency, idempotency_key,
			list_amount, list_currency, rate, affiliate_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10::bigint, 0), NULLIF($11::text, ''), NULLIF($12::bigint, 0), $13)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING `+vendorOrderCols+`, (xmax = 0)`,
		o.ID, o.UserID, o.VendorLicenseID, o.Kind, o.Tier, o.Months, o.Amount, o.Currency, o.IdempotencyKey,
		o.ListAmount, o.ListCurrency, o.Rate, o.AffiliateID)
	var got VendorOrder
	err := row.Scan(&got.ID, &got.UserID, &got.VendorLicenseID, &got.Kind, &got.Tier, &got.Months, &got.Amount, &got.Currency,
		&got.Status, &got.IdempotencyKey, &got.Attempts, &got.NextAttemptAt, &got.LastError, &got.CreatedAt, &got.UpdatedAt, &got.AffiliateID, &inserted)
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

// SyncVendorLicense mirrors a change made on the license server (by the owner, from the dashboard) into the sold
// license with that server id, if the store sold one: its tier, end and status. It changes nothing when there is none.
func (s *Store) SyncVendorLicense(ctx context.Context, q querier, licenseID, tier string, ends *time.Time, status string) error {
	_, err := q.Exec(ctx, `
		UPDATE core.vendor_licenses SET tier = $2, ends_at = $3, status = $4, updated_at = now()
		WHERE license_id = $1 AND tier IN ('standard', 'pro') AND $2 IN ('standard', 'pro')`, licenseID, tier, ends, status)
	if err != nil {
		return fmt.Errorf("sync vendor license: %w", err)
	}
	return nil
}

// VendorIntent is what a customer meant to buy when they tapped a payment method for the shortfall: a license of a tier for a number of
// months, or the renewal of one of theirs. At most one per customer.
type VendorIntent struct {
	UserID    string
	Tier      string
	Months    int
	LicenseID *string // a vendor_licenses id: a renewal; nil: a new license
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SetVendorIntent records (or replaces) a customer's intent.
func (s *Store) SetVendorIntent(ctx context.Context, q querier, in *VendorIntent) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.vendor_intents (user_id, tier, months, license_id, expires_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET tier = EXCLUDED.tier, months = EXCLUDED.months, license_id = EXCLUDED.license_id,
			created_at = now(), expires_at = EXCLUDED.expires_at`, in.UserID, in.Tier, in.Months, in.LicenseID, in.ExpiresAt)
	if err != nil {
		return fmt.Errorf("set vendor intent: %w", err)
	}
	return nil
}

// DeleteVendorIntent forgets a customer's intent (no error when there is none).
func (s *Store) DeleteVendorIntent(ctx context.Context, q querier, userID string) error {
	if _, err := q.Exec(ctx, `DELETE FROM core.vendor_intents WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete vendor intent: %w", err)
	}
	return nil
}

// ClaimVendorIntent forgets the intent of a customer that was made at createdAt and says whether it was still there. A purchase made from an
// intent calls it in the same transaction as the debit, so an intent that was replaced, bought by hand or already used is not bought again.
func (s *Store) ClaimVendorIntent(ctx context.Context, q querier, userID string, createdAt time.Time) (bool, error) {
	tag, err := q.Exec(ctx, `DELETE FROM core.vendor_intents WHERE user_id = $1 AND created_at = $2`, userID, createdAt)
	if err != nil {
		return false, fmt.Errorf("claim vendor intent: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// LiveVendorIntents lists the intents that have not expired and whose customer has had a top-up credited since they made them (an intent
// whose payment has not landed is not looked at), oldest first, and forgets the ones that have expired.
func (s *Store) LiveVendorIntents(ctx context.Context, q querier, now time.Time, limit int) ([]VendorIntent, error) {
	if _, err := q.Exec(ctx, `DELETE FROM core.vendor_intents WHERE expires_at <= $1`, now); err != nil {
		return nil, fmt.Errorf("expire vendor intents: %w", err)
	}
	rows, err := q.Query(ctx, `SELECT i.user_id::text, i.tier, i.months, i.license_id::text, i.created_at, i.expires_at
		FROM core.vendor_intents i
		WHERE EXISTS (SELECT 1 FROM core.ledger_entries l
			WHERE l.user_id = i.user_id AND l.kind = 'topup' AND l.amount > 0 AND l.created_at > i.created_at)
		ORDER BY i.created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list vendor intents: %w", err)
	}
	defer rows.Close()
	var out []VendorIntent
	for rows.Next() {
		var in VendorIntent
		if err := rows.Scan(&in.UserID, &in.Tier, &in.Months, &in.LicenseID, &in.CreatedAt, &in.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}
