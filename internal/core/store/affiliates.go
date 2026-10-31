package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The affiliate program of the owner's own store (migration 00054): the affiliates and their codes, who brought whom, the commissions paid on
// the first license of a referred buyer and what the owner paid out by hand.

// ErrAffiliateCodeTaken: another affiliate has this code (a new one is drawn).
var ErrAffiliateCodeTaken = errors.New("store: affiliate code taken")

// Affiliate mirrors core.affiliates. Percent nil: the program's percent.
type Affiliate struct {
	ID        string
	UserID    string
	Code      string
	Percent   *int
	Status    string // active, disabled
	CreatedAt time.Time
	UpdatedAt time.Time
}

const affiliateCols = `id, user_id, code, percent, status, created_at, updated_at`

func scanAffiliate(row pgx.Row) (*Affiliate, error) {
	var a Affiliate
	err := row.Scan(&a.ID, &a.UserID, &a.Code, &a.Percent, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("affiliate: %w", err)
	}
	return &a, nil
}

// CreateAffiliate makes a user an affiliate with code, or returns the affiliate they already are. A code another affiliate has is
// ErrAffiliateCodeTaken.
func (s *Store) CreateAffiliate(ctx context.Context, q querier, userID, code string) (*Affiliate, error) {
	a, err := scanAffiliate(q.QueryRow(ctx, `
		INSERT INTO core.affiliates (id, user_id, code) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO NOTHING
		RETURNING `+affiliateCols, buuid.MustV7().String(), userID, code))
	if errors.Is(err, ErrNotFound) {
		return s.GetAffiliateByUser(ctx, q, userID)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "affiliates_code_key" {
		return nil, ErrAffiliateCodeTaken
	}
	return a, err
}

// GetAffiliate returns an affiliate by id.
func (s *Store) GetAffiliate(ctx context.Context, q querier, id string) (*Affiliate, error) {
	return scanAffiliate(q.QueryRow(ctx, `SELECT `+affiliateCols+` FROM core.affiliates WHERE id = $1`, id))
}

// GetAffiliateByUser returns the affiliate a user is, or ErrNotFound.
func (s *Store) GetAffiliateByUser(ctx context.Context, q querier, userID string) (*Affiliate, error) {
	return scanAffiliate(q.QueryRow(ctx, `SELECT `+affiliateCols+` FROM core.affiliates WHERE user_id = $1`, userID))
}

// GetAffiliateByCode returns the affiliate with a code, or ErrNotFound.
func (s *Store) GetAffiliateByCode(ctx context.Context, q querier, code string) (*Affiliate, error) {
	return scanAffiliate(q.QueryRow(ctx, `SELECT `+affiliateCols+` FROM core.affiliates WHERE code = $1`, code))
}

// UpdateAffiliate sets an affiliate's percent override (nil: the program's) and status.
func (s *Store) UpdateAffiliate(ctx context.Context, q querier, id string, percent *int, status string) error {
	tag, err := q.Exec(ctx, `UPDATE core.affiliates SET percent = $2, status = $3, updated_at = now() WHERE id = $1`, id, percent, status)
	if err != nil {
		return fmt.Errorf("update affiliate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAffiliateReferral records that an affiliate brought a customer, unless one was recorded before (the first link wins). It reports whether
// this call recorded it.
func (s *Store) SetAffiliateReferral(ctx context.Context, q querier, userID, affiliateID string) (bool, error) {
	tag, err := q.Exec(ctx, `
		INSERT INTO core.affiliate_referrals (user_id, affiliate_id) VALUES ($1, $2) ON CONFLICT (user_id) DO NOTHING`, userID, affiliateID)
	if err != nil {
		return false, fmt.Errorf("set affiliate referral: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AffiliateOfBuyer is the affiliate who brought a customer ("" when none). forUpdate locks the customer's referral row, which serializes the
// commissions of one buyer: two of their orders finishing at once are paid one after the other, and only the first earns.
func (s *Store) AffiliateOfBuyer(ctx context.Context, q querier, userID string, forUpdate bool) (string, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	var id string
	err := q.QueryRow(ctx, `SELECT affiliate_id::text FROM core.affiliate_referrals WHERE user_id = $1`+lock, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("affiliate of buyer: %w", err)
	}
	return id, nil
}

// HasLicenseOrder reports whether a customer has bought a license already: an order that is paid or done, or one refunded after it was
// delivered. An order the license server refused (refunded before any license) does not count.
func (s *Store) HasLicenseOrder(ctx context.Context, q querier, userID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.vendor_orders WHERE user_id = $1 AND (status <> 'refunded' OR vendor_license_id IS NOT NULL))`,
		userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("has license order: %w", err)
	}
	return ok, nil
}

// AffiliateCommission mirrors core.affiliate_commissions.
type AffiliateCommission struct {
	OrderID         string
	AffiliateID     string
	AffiliateUserID string
	BuyerUserID     string
	Currency        string
	BaseAmount      int64
	Percent         int
	Amount          int64
	LedgerEntryID   string
	CreatedAt       time.Time
	ReversedAmount  *int64
	ReversedAt      *time.Time
	ReversalEntryID *string
}

const commissionCols = `order_id, affiliate_id, affiliate_user_id, buyer_user_id, currency, base_amount, percent, amount, ledger_entry_id,
	created_at, reversed_amount, reversed_at, reversal_entry_id`

func scanCommission(row pgx.Row) (*AffiliateCommission, error) {
	var c AffiliateCommission
	err := row.Scan(&c.OrderID, &c.AffiliateID, &c.AffiliateUserID, &c.BuyerUserID, &c.Currency, &c.BaseAmount, &c.Percent, &c.Amount,
		&c.LedgerEntryID, &c.CreatedAt, &c.ReversedAmount, &c.ReversedAt, &c.ReversalEntryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("affiliate commission: %w", err)
	}
	return &c, nil
}

// BuyerHasCommission reports whether a commission was ever paid on a customer's licenses (the first payment only).
func (s *Store) BuyerHasCommission(ctx context.Context, q querier, buyerID string) (bool, error) {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.affiliate_commissions WHERE buyer_user_id = $1)`, buyerID).Scan(&ok); err != nil {
		return false, fmt.Errorf("buyer has commission: %w", err)
	}
	return ok, nil
}

// InsertAffiliateCommission records a commission in the transaction that credited it.
func (s *Store) InsertAffiliateCommission(ctx context.Context, q querier, c *AffiliateCommission) error {
	err := q.QueryRow(ctx, `
		INSERT INTO core.affiliate_commissions (order_id, affiliate_id, affiliate_user_id, buyer_user_id, currency, base_amount, percent, amount,
			ledger_entry_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING created_at`,
		c.OrderID, c.AffiliateID, c.AffiliateUserID, c.BuyerUserID, c.Currency, c.BaseAmount, c.Percent, c.Amount, c.LedgerEntryID).Scan(&c.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert affiliate commission: %w", err)
	}
	return nil
}

// GetAffiliateCommission returns the commission of an order, or ErrNotFound; forUpdate locks it.
func (s *Store) GetAffiliateCommission(ctx context.Context, q querier, orderID string, forUpdate bool) (*AffiliateCommission, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanCommission(q.QueryRow(ctx, `SELECT `+commissionCols+` FROM core.affiliate_commissions WHERE order_id = $1`+lock, orderID))
}

// MarkAffiliateCommissionReversed records what a refund took back (taken, and the ledger entry that took it: nil when nothing was taken). It
// reports false when the commission was reversed already.
func (s *Store) MarkAffiliateCommissionReversed(ctx context.Context, q querier, orderID string, taken int64, entryID *string) (bool, error) {
	tag, err := q.Exec(ctx, `
		UPDATE core.affiliate_commissions SET reversed_amount = $2, reversed_at = now(), reversal_entry_id = $3
		WHERE order_id = $1 AND reversed_at IS NULL`, orderID, taken, entryID)
	if err != nil {
		return false, fmt.Errorf("reverse affiliate commission: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AffiliatePayout mirrors core.affiliate_payouts.
type AffiliatePayout struct {
	ID            string
	AffiliateID   string
	Currency      string
	Amount        int64
	Note          string
	RequestID     string
	LedgerEntryID string
	CreatedBy     *string
	CreatedAt     time.Time
}

// InsertAffiliatePayout records a payout in the transaction that took it from the wallet.
func (s *Store) InsertAffiliatePayout(ctx context.Context, q querier, p *AffiliatePayout) error {
	p.ID = buuid.MustV7().String()
	err := q.QueryRow(ctx, `
		INSERT INTO core.affiliate_payouts (id, affiliate_id, currency, amount, note, request_id, ledger_entry_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING created_at`,
		p.ID, p.AffiliateID, p.Currency, p.Amount, p.Note, p.RequestID, p.LedgerEntryID, p.CreatedBy).Scan(&p.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert affiliate payout: %w", err)
	}
	return nil
}

// GetAffiliatePayoutByRequest returns the payout made under a request id, or ErrNotFound.
func (s *Store) GetAffiliatePayoutByRequest(ctx context.Context, q querier, requestID string) (*AffiliatePayout, error) {
	var p AffiliatePayout
	err := q.QueryRow(ctx, `SELECT id, affiliate_id, currency, amount, note, request_id, ledger_entry_id, created_by::text, created_at
		FROM core.affiliate_payouts WHERE request_id = $1`, requestID).
		Scan(&p.ID, &p.AffiliateID, &p.Currency, &p.Amount, &p.Note, &p.RequestID, &p.LedgerEntryID, &p.CreatedBy, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("affiliate payout: %w", err)
	}
	return &p, nil
}

// AffiliateStats are an affiliate's figures: the customers their link brought, the licenses those bought that earned (Licenses, refunded ones
// included), what they earned net of what refunds took back (per currency), and what the owner paid them.
type AffiliateStats struct {
	Referred int64
	Licenses int64
	Earned   map[string]int64
	Paid     map[string]int64
}

// GetAffiliateStats reads one affiliate's figures.
func (s *Store) GetAffiliateStats(ctx context.Context, q querier, affiliateID string) (*AffiliateStats, error) {
	st := &AffiliateStats{Earned: map[string]int64{}, Paid: map[string]int64{}}
	if err := q.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM core.affiliate_referrals WHERE affiliate_id = $1),
			(SELECT count(*) FROM core.affiliate_commissions WHERE affiliate_id = $1)`, affiliateID).Scan(&st.Referred, &st.Licenses); err != nil {
		return nil, fmt.Errorf("affiliate stats: %w", err)
	}
	if err := sumByCurrency(ctx, q, st.Earned, `SELECT currency, sum(amount - coalesce(reversed_amount, 0))::bigint FROM core.affiliate_commissions
		WHERE affiliate_id = $1 GROUP BY currency`, affiliateID); err != nil {
		return nil, err
	}
	if err := sumByCurrency(ctx, q, st.Paid, `SELECT currency, sum(amount)::bigint FROM core.affiliate_payouts WHERE affiliate_id = $1 GROUP BY currency`,
		affiliateID); err != nil {
		return nil, err
	}
	return st, nil
}

func sumByCurrency(ctx context.Context, q querier, into map[string]int64, sql string, args ...any) error {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("affiliate sums: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cur string
		var n int64
		if err := rows.Scan(&cur, &n); err != nil {
			return err
		}
		into[cur] = n
	}
	return rows.Err()
}

// AffiliateRow is an affiliate with who they are and their figures in the program's currency, for the dashboard.
type AffiliateRow struct {
	Affiliate
	TelegramID int64
	Username   string
	UserStatus string
	Referred   int64
	Licenses   int64
	Earned     int64
	Paid       int64
	Balance    int64
}

// ListAffiliates lists the affiliates, newest first, with the number of them; the money figures are in currency.
func (s *Store) ListAffiliates(ctx context.Context, q querier, currency string, p Page) ([]AffiliateRow, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `
		SELECT a.id, a.user_id, a.code, a.percent, a.status, a.created_at, a.updated_at,
		       u.telegram_id, coalesce(u.username, ''), u.status,
		       (SELECT count(*) FROM core.affiliate_referrals r WHERE r.affiliate_id = a.id),
		       (SELECT count(*) FROM core.affiliate_commissions c WHERE c.affiliate_id = a.id),
		       coalesce((SELECT sum(c.amount - coalesce(c.reversed_amount, 0)) FROM core.affiliate_commissions c
		                 WHERE c.affiliate_id = a.id AND c.currency = $1), 0)::bigint,
		       coalesce((SELECT sum(o.amount) FROM core.affiliate_payouts o WHERE o.affiliate_id = a.id AND o.currency = $1), 0)::bigint,
		       coalesce((SELECT w.balance FROM core.wallets w WHERE w.user_id = a.user_id AND w.currency = $1), 0)::bigint,
		       count(*) OVER ()
		FROM core.affiliates a JOIN core.users u ON u.id = a.user_id
		ORDER BY a.created_at DESC, a.id DESC LIMIT $2 OFFSET $3`, currency, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list affiliates: %w", err)
	}
	defer rows.Close()
	var out []AffiliateRow
	var total int64
	for rows.Next() {
		var r AffiliateRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.Code, &r.Percent, &r.Status, &r.CreatedAt, &r.UpdatedAt, &r.TelegramID, &r.Username, &r.UserStatus,
			&r.Referred, &r.Licenses, &r.Earned, &r.Paid, &r.Balance, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// AffiliateCommissionRow is a commission with the license it was paid on and the buyer, for the dashboard.
type AffiliateCommissionRow struct {
	AffiliateCommission
	Tier          string
	OrderStatus   string
	LicenseID     string
	BuyerTelegram int64
	BuyerUsername string
}

// ListAffiliateCommissions lists an affiliate's commissions, newest first (at most limit).
func (s *Store) ListAffiliateCommissions(ctx context.Context, q querier, affiliateID string, limit int) ([]AffiliateCommissionRow, error) {
	rows, err := q.Query(ctx, `
		SELECT c.order_id, c.affiliate_id, c.affiliate_user_id, c.buyer_user_id, c.currency, c.base_amount, c.percent, c.amount, c.ledger_entry_id,
		       c.created_at, c.reversed_amount, c.reversed_at, c.reversal_entry_id,
		       o.tier, o.status, coalesce(l.license_id, ''), u.telegram_id, coalesce(u.username, '')
		FROM core.affiliate_commissions c
		JOIN core.vendor_orders o ON o.id = c.order_id
		LEFT JOIN core.vendor_licenses l ON l.id = o.vendor_license_id
		JOIN core.users u ON u.id = c.buyer_user_id
		WHERE c.affiliate_id = $1 ORDER BY c.created_at DESC, c.order_id DESC LIMIT $2`, affiliateID, limit)
	if err != nil {
		return nil, fmt.Errorf("list affiliate commissions: %w", err)
	}
	defer rows.Close()
	var out []AffiliateCommissionRow
	for rows.Next() {
		var r AffiliateCommissionRow
		if err := rows.Scan(&r.OrderID, &r.AffiliateID, &r.AffiliateUserID, &r.BuyerUserID, &r.Currency, &r.BaseAmount, &r.Percent, &r.Amount,
			&r.LedgerEntryID, &r.CreatedAt, &r.ReversedAmount, &r.ReversedAt, &r.ReversalEntryID,
			&r.Tier, &r.OrderStatus, &r.LicenseID, &r.BuyerTelegram, &r.BuyerUsername); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAffiliatePayouts lists an affiliate's payouts, newest first (at most limit).
func (s *Store) ListAffiliatePayouts(ctx context.Context, q querier, affiliateID string, limit int) ([]AffiliatePayout, error) {
	rows, err := q.Query(ctx, `SELECT id, affiliate_id, currency, amount, note, request_id, ledger_entry_id, created_by::text, created_at
		FROM core.affiliate_payouts WHERE affiliate_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, affiliateID, limit)
	if err != nil {
		return nil, fmt.Errorf("list affiliate payouts: %w", err)
	}
	defer rows.Close()
	var out []AffiliatePayout
	for rows.Next() {
		var p AffiliatePayout
		if err := rows.Scan(&p.ID, &p.AffiliateID, &p.Currency, &p.Amount, &p.Note, &p.RequestID, &p.LedgerEntryID, &p.CreatedBy, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// NewLicenseOrderOf returns the order that bought a sold license (kind new), or ErrNotFound; forUpdate locks it.
func (s *Store) NewLicenseOrderOf(ctx context.Context, q querier, vendorLicenseID string, forUpdate bool) (*VendorOrder, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanVendorOrder(q.QueryRow(ctx, `SELECT `+vendorOrderCols+` FROM core.vendor_orders
		WHERE vendor_license_id = $1 AND kind = 'new' ORDER BY created_at LIMIT 1`+lock, vendorLicenseID))
}

// RefundDoneVendorOrder marks a delivered order refunded (the owner gave the money back).
func (s *Store) RefundDoneVendorOrder(ctx context.Context, q querier, id, reason string) error {
	tag, err := q.Exec(ctx, `UPDATE core.vendor_orders SET status = 'refunded', last_error = $2, updated_at = now() WHERE id = $1 AND status = 'done'`,
		id, reason)
	if err != nil {
		return fmt.Errorf("refund vendor order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
