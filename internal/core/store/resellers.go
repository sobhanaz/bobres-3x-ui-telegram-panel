package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

var (
	// ErrTierNameTaken: another reseller tier has this name.
	ErrTierNameTaken = errors.New("store: tier name taken")
	// ErrTierInUse: resellers are on the tier, so it cannot be deleted.
	ErrTierInUse = errors.New("store: tier in use")
)

// ResellerTier mirrors core.reseller_tiers with its fixed prices.
type ResellerTier struct {
	ID                string
	Name              string
	DiscountPercent   int32
	CommissionPercent int32
	Prices            map[string]int64 // plan id -> fixed price, in the plan's currency
	// Currencies: plan id -> the currency Prices were set in (writes only;
	// reads return only prices still in their plan's currency).
	Currencies map[string]string
	Version    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// TierRow is a tier in the tiers list, with how many resellers are on it.
type TierRow struct {
	ResellerTier
	Resellers int64
}

// Reseller mirrors core.resellers.
type Reseller struct {
	UserID            string
	TierID            string
	CommissionPercent *int32 // nil: the tier's
	Note              string
	Version           int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ResellerTerms is what a reseller buys and earns at.
type ResellerTerms struct {
	TierID, TierName  string
	DiscountPercent   int32
	CommissionPercent int32 // the reseller's own rate when set, else the tier's
}

const tierCols = `t.id, t.name, t.discount_percent, t.commission_percent, t.version, t.created_at, t.updated_at`

func tierDest(t *ResellerTier) []any {
	return []any{&t.ID, &t.Name, &t.DiscountPercent, &t.CommissionPercent, &t.Version, &t.CreatedAt, &t.UpdatedAt}
}

const resellerCols = `r.user_id, r.tier_id, r.commission_percent, r.note, r.version, r.created_at, r.updated_at`

func resellerDest(r *Reseller) []any {
	return []any{&r.UserID, &r.TierID, &r.CommissionPercent, &r.Note, &r.Version, &r.CreatedAt, &r.UpdatedAt}
}

// tierNameConstraint is the UNIQUE constraint on reseller_tiers.name.
const tierNameConstraint = "reseller_tiers_name_key"

func tierWriteErr(what string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == tierNameConstraint {
		return ErrTierNameTaken
	}
	return fmt.Errorf("%s: %w", what, err)
}

// InsertTier creates a tier with its fixed prices.
func (s *Store) InsertTier(ctx context.Context, q querier, t *ResellerTier) error {
	t.ID = buuid.MustV7().String()
	err := q.QueryRow(ctx, `INSERT INTO core.reseller_tiers (id, name, discount_percent, commission_percent)
		VALUES ($1, $2, $3, $4) RETURNING version, created_at, updated_at`,
		t.ID, t.Name, t.DiscountPercent, t.CommissionPercent).Scan(&t.Version, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return tierWriteErr("insert tier", err)
	}
	return setTierPrices(ctx, q, t)
}

// UpdateTier replaces a tier's terms and its whole list of fixed prices when
// its version still matches (ErrStale otherwise, ErrNotFound when it is gone).
func (s *Store) UpdateTier(ctx context.Context, q querier, t *ResellerTier, version int) error {
	err := q.QueryRow(ctx, `UPDATE core.reseller_tiers
		SET name = $2, discount_percent = $3, commission_percent = $4, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $5 RETURNING version, created_at, updated_at`,
		t.ID, t.Name, t.DiscountPercent, t.CommissionPercent, version).Scan(&t.Version, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.GetTier(ctx, q, t.ID, false); err != nil {
			return err
		}
		return ErrStale
	}
	if err != nil {
		return tierWriteErr("update tier", err)
	}
	if _, err := q.Exec(ctx, `DELETE FROM core.reseller_tier_prices WHERE tier_id = $1`, t.ID); err != nil {
		return fmt.Errorf("update tier prices: %w", err)
	}
	return setTierPrices(ctx, q, t)
}

// setTierPrices stores t's fixed prices, each with the currency it was set
// in (the plan's current one when t.Currencies leaves it out).
func setTierPrices(ctx context.Context, q querier, t *ResellerTier) error {
	if len(t.Prices) == 0 {
		return nil
	}
	plans := make([]string, 0, len(t.Prices))
	amounts := make([]int64, 0, len(t.Prices))
	curs := make([]string, 0, len(t.Prices))
	for id, p := range t.Prices {
		plans = append(plans, id)
		amounts = append(amounts, p)
		curs = append(curs, t.Currencies[id])
	}
	if _, err := q.Exec(ctx, `INSERT INTO core.reseller_tier_prices (tier_id, plan_id, price, currency)
		SELECT $1, x.plan_id, x.price, COALESCE(NULLIF(x.currency, ''), p.currency)
		FROM unnest($2::uuid[], $3::bigint[], $4::text[]) AS x(plan_id, price, currency)
		JOIN core.plans p ON p.id = x.plan_id`, t.ID, plans, amounts, curs); err != nil {
		return fmt.Errorf("tier prices: %w", err)
	}
	return nil
}

// DropStaleTierPrices removes a plan's fixed prices set in another currency
// than its own (the plan's currency changed); it returns how many went.
func (s *Store) DropStaleTierPrices(ctx context.Context, q querier, planID string) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM core.reseller_tier_prices tp USING core.plans p
		WHERE tp.plan_id = $1 AND p.id = tp.plan_id AND tp.currency <> p.currency`, planID)
	if err != nil {
		return 0, fmt.Errorf("drop stale tier prices: %w", err)
	}
	return tag.RowsAffected(), nil
}

// GetTier loads a tier with its fixed prices; lock takes it FOR UPDATE.
func (s *Store) GetTier(ctx context.Context, q querier, id string, lock bool) (*ResellerTier, error) {
	sql := `SELECT ` + tierCols + ` FROM core.reseller_tiers t WHERE t.id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var t ResellerTier
	if err := q.QueryRow(ctx, sql, id).Scan(tierDest(&t)...); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get tier: %w", err)
	}
	prices, err := s.TierPrices(ctx, q, id)
	if err != nil {
		return nil, err
	}
	t.Prices = prices
	return &t, nil
}

// TierPrices returns a tier's fixed prices by plan id: those still in their
// plan's currency.
func (s *Store) TierPrices(ctx context.Context, q querier, tierID string) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT tp.plan_id, tp.price FROM core.reseller_tier_prices tp
		JOIN core.plans p ON p.id = tp.plan_id AND p.currency = tp.currency WHERE tp.tier_id = $1`, tierID)
	if err != nil {
		return nil, fmt.Errorf("tier prices: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			id    string
			price int64
		)
		if err := rows.Scan(&id, &price); err != nil {
			return nil, err
		}
		out[id] = price
	}
	return out, rows.Err()
}

// ListTiers lists every tier by name, with its prices and reseller count.
func (s *Store) ListTiers(ctx context.Context, q querier) ([]TierRow, error) {
	rows, err := q.Query(ctx, `SELECT `+tierCols+`, (SELECT count(*) FROM core.resellers r WHERE r.tier_id = t.id)
		FROM core.reseller_tiers t ORDER BY lower(t.name), t.id`)
	if err != nil {
		return nil, fmt.Errorf("list tiers: %w", err)
	}
	defer rows.Close()
	var (
		out []TierRow
		idx = map[string]int{}
	)
	for rows.Next() {
		r := TierRow{ResellerTier: ResellerTier{Prices: map[string]int64{}}}
		if err := rows.Scan(append(tierDest(&r.ResellerTier), &r.Resellers)...); err != nil {
			return nil, err
		}
		idx[r.ID] = len(out)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	prows, err := q.Query(ctx, `SELECT tp.tier_id, tp.plan_id, tp.price FROM core.reseller_tier_prices tp
		JOIN core.plans p ON p.id = tp.plan_id AND p.currency = tp.currency`)
	if err != nil {
		return nil, fmt.Errorf("list tiers: prices: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var (
			tier, plan string
			price      int64
		)
		if err := prows.Scan(&tier, &plan, &price); err != nil {
			return nil, err
		}
		if i, ok := idx[tier]; ok {
			out[i].Prices[plan] = price
		}
	}
	return out, prows.Err()
}

// DeleteTier deletes a tier and its prices; ErrTierInUse while resellers are
// on it.
func (s *Store) DeleteTier(ctx context.Context, q querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM core.reseller_tiers WHERE id = $1`, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrTierInUse
	}
	if err != nil {
		return fmt.Errorf("delete tier: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertReseller records a reseller's terms (the role is set separately).
func (s *Store) InsertReseller(ctx context.Context, q querier, r *Reseller) error {
	err := q.QueryRow(ctx, `INSERT INTO core.resellers (user_id, tier_id, commission_percent, note)
		VALUES ($1, $2, $3, $4) RETURNING version, created_at, updated_at`,
		r.UserID, r.TierID, r.CommissionPercent, r.Note).Scan(&r.Version, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert reseller: %w", err)
	}
	return nil
}

// GetReseller loads a reseller's terms; lock takes the row FOR UPDATE.
func (s *Store) GetReseller(ctx context.Context, q querier, userID string, lock bool) (*Reseller, error) {
	sql := `SELECT ` + resellerCols + ` FROM core.resellers r WHERE r.user_id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var r Reseller
	if err := q.QueryRow(ctx, sql, userID).Scan(resellerDest(&r)...); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get reseller: %w", err)
	}
	return &r, nil
}

// HoldReseller locks a reseller's row in share mode until tx ends, so a
// removal (which locks it for update) waits for tx. ErrNotFound when they
// are not a reseller (any more).
func (s *Store) HoldReseller(ctx context.Context, tx pgx.Tx, userID string) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM core.resellers WHERE user_id = $1 FOR SHARE`, userID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("hold reseller: %w", err)
	}
	return nil
}

// UpdateReseller changes a reseller's terms when the version still matches
// (ErrStale otherwise, ErrNotFound when they are no longer a reseller).
func (s *Store) UpdateReseller(ctx context.Context, q querier, r *Reseller, version int) error {
	err := q.QueryRow(ctx, `UPDATE core.resellers
		SET tier_id = $2, commission_percent = $3, note = $4, version = version + 1, updated_at = now()
		WHERE user_id = $1 AND version = $5 RETURNING version, created_at, updated_at`,
		r.UserID, r.TierID, r.CommissionPercent, r.Note, version).Scan(&r.Version, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.GetReseller(ctx, q, r.UserID, false); err != nil {
			return err
		}
		return ErrStale
	}
	if err != nil {
		return fmt.Errorf("update reseller: %w", err)
	}
	return nil
}

// DeleteReseller removes a reseller's terms (the role is set separately).
func (s *Store) DeleteReseller(ctx context.Context, q querier, userID string) error {
	tag, err := q.Exec(ctx, `DELETE FROM core.resellers WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete reseller: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResellerTerms returns what a reseller buys and earns at; ErrNotFound when
// the user is not a reseller.
func (s *Store) ResellerTerms(ctx context.Context, q querier, userID string) (*ResellerTerms, error) {
	var t ResellerTerms
	err := q.QueryRow(ctx, `SELECT t.id, t.name, t.discount_percent, COALESCE(r.commission_percent, t.commission_percent)
		FROM core.resellers r JOIN core.reseller_tiers t ON t.id = r.tier_id WHERE r.user_id = $1`, userID).
		Scan(&t.TierID, &t.TierName, &t.DiscountPercent, &t.CommissionPercent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reseller terms: %w", err)
	}
	return &t, nil
}

// SetCreditLimit sets how far below zero a wallet may go (creating the
// wallet). A limit below the wallet's debt is ErrInsufficientFunds.
func (s *Store) SetCreditLimit(ctx context.Context, q querier, userID, currency string, limit int64) error {
	_, err := q.Exec(ctx, `INSERT INTO core.wallets (user_id, currency, credit_limit) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, currency) DO UPDATE SET credit_limit = EXCLUDED.credit_limit, updated_at = now()`,
		userID, currency, limit)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "wallets_balance_check" {
		return ErrInsufficientFunds
	}
	if err != nil {
		return fmt.Errorf("set credit limit: %w", err)
	}
	return nil
}

// LockWallets returns a user's wallets, locked until the transaction ends.
func (s *Store) LockWallets(ctx context.Context, tx pgx.Tx, userID string) ([]Wallet, error) {
	rows, err := tx.Query(ctx, `SELECT `+walletCols+` FROM core.wallets WHERE user_id = $1 ORDER BY currency FOR UPDATE`, userID)
	if err != nil {
		return nil, fmt.Errorf("lock wallets: %w", err)
	}
	return collectWallets(rows)
}

// ClearCreditLimits takes a user's credit away in every currency.
func (s *Store) ClearCreditLimits(ctx context.Context, q querier, userID string) error {
	_, err := q.Exec(ctx, `UPDATE core.wallets SET credit_limit = 0, updated_at = now() WHERE user_id = $1 AND credit_limit <> 0`, userID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "wallets_balance_check" {
		return ErrInsufficientFunds
	}
	if err != nil {
		return fmt.Errorf("clear credit limits: %w", err)
	}
	return nil
}

// ResellerFilter narrows the resellers list. Query matches a Telegram id or
// part of a username; InDebt keeps those with a negative balance.
type ResellerFilter struct {
	Query, TierID string
	InDebt        bool
}

// ResellerRow is a reseller with their tier, wallets (with credit limits),
// customers (users who joined through their link) and commissions earned.
type ResellerRow struct {
	User           User
	Reseller       Reseller
	TierName       string
	TierDiscount   int32
	TierCommission int32
	Wallets        []Wallet
	Customers      int64
	Earned         map[string]int64 // commissions per currency
}

// Commission is the reseller's effective commission rate.
func (r *ResellerRow) Commission() int32 {
	if r.Reseller.CommissionPercent != nil {
		return *r.Reseller.CommissionPercent
	}
	return r.TierCommission
}

const resellerRowSelect = `
	SELECT ` + userColsU + `, ` + resellerCols + `, t.name, t.discount_percent, t.commission_percent,
		(SELECT count(*) FROM core.users c WHERE c.referred_by = u.id)`

const resellerRowFrom = `
	FROM core.resellers r
	JOIN core.users u ON u.id = r.user_id
	JOIN core.reseller_tiers t ON t.id = r.tier_id`

// ListResellers returns a page of resellers, newest first, and the number of
// matches.
func (s *Store) ListResellers(ctx context.Context, q querier, f ResellerFilter, p Page) ([]ResellerRow, int64, error) {
	p = p.clamp()
	num, isNum, like := search(f.Query)
	rows, err := q.Query(ctx, resellerRowSelect+`, count(*) OVER ()`+resellerRowFrom+`
		WHERE ($1 = '' OR (CASE WHEN $2 THEN u.telegram_id = $3 ELSE false END) OR lower(u.username) LIKE $4 ESCAPE '\')
		  AND ($5 = '' OR r.tier_id::text = $5)
		  AND (NOT $6 OR EXISTS (SELECT 1 FROM core.wallets w WHERE w.user_id = r.user_id AND w.balance < 0))
		ORDER BY r.created_at DESC, r.user_id DESC
		LIMIT $7 OFFSET $8`,
		strings.TrimSpace(f.Query), isNum, num, like, f.TierID, f.InDebt, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list resellers: %w", err)
	}
	defer rows.Close()
	var (
		out   []ResellerRow
		total int64
	)
	for rows.Next() {
		var r ResellerRow
		if err := rows.Scan(append(resellerRowDest(&r), &total)...); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := s.fillResellerMoney(ctx, q, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// GetResellerRow is one reseller as the list shows them.
func (s *Store) GetResellerRow(ctx context.Context, q querier, userID string) (*ResellerRow, error) {
	var r ResellerRow
	err := q.QueryRow(ctx, resellerRowSelect+resellerRowFrom+` WHERE r.user_id = $1`, userID).Scan(resellerRowDest(&r)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get reseller: %w", err)
	}
	out := []ResellerRow{r}
	if err := s.fillResellerMoney(ctx, q, out); err != nil {
		return nil, err
	}
	return &out[0], nil
}

func resellerRowDest(r *ResellerRow) []any {
	dest := append(userDest(&r.User), resellerDest(&r.Reseller)...)
	return append(dest, &r.TierName, &r.TierDiscount, &r.TierCommission, &r.Customers)
}

// fillResellerMoney loads the wallets and commissions of listed resellers.
func (s *Store) fillResellerMoney(ctx context.Context, q querier, out []ResellerRow) error {
	if len(out) == 0 {
		return nil
	}
	ids := make([]string, len(out))
	idx := map[string]int{}
	for i := range out {
		ids[i] = out[i].User.ID
		idx[out[i].User.ID] = i
		out[i].Earned = map[string]int64{}
	}
	wrows, err := q.Query(ctx, `SELECT `+walletCols+` FROM core.wallets WHERE user_id = ANY($1) ORDER BY currency`, ids)
	if err != nil {
		return fmt.Errorf("resellers: wallets: %w", err)
	}
	wallets, err := collectWallets(wrows)
	if err != nil {
		return err
	}
	for _, w := range wallets {
		r := &out[idx[w.UserID]]
		r.Wallets = append(r.Wallets, w)
	}
	erows, err := q.Query(ctx, `SELECT user_id, currency, sum(amount) FROM core.ledger_entries
		WHERE kind = 'commission' AND user_id = ANY($1) GROUP BY user_id, currency`, ids)
	if err != nil {
		return fmt.Errorf("resellers: commissions: %w", err)
	}
	defer erows.Close()
	for erows.Next() {
		var (
			id, cur string
			sum     int64
		)
		if err := erows.Scan(&id, &cur, &sum); err != nil {
			return err
		}
		out[idx[id]].Earned[cur] = sum
	}
	return erows.Err()
}

// CommissionEarned sums the commissions paid to a reseller, per currency.
func (s *Store) CommissionEarned(ctx context.Context, q querier, userID string) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT currency, sum(amount) FROM core.ledger_entries
		WHERE user_id = $1 AND kind = 'commission' GROUP BY currency`, userID)
	if err != nil {
		return nil, fmt.Errorf("commission earned: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			cur string
			sum int64
		)
		if err := rows.Scan(&cur, &sum); err != nil {
			return nil, err
		}
		out[cur] = sum
	}
	return out, rows.Err()
}

// CountInvited counts the users who joined through someone's invite link.
func (s *Store) CountInvited(ctx context.Context, q querier, userID string) (int64, error) {
	var n int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.users WHERE referred_by = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count invited: %w", err)
	}
	return n, nil
}

// ResellerSummary counts the resellers and sums what is owed to the store
// (the negative balances), per currency.
func (s *Store) ResellerSummary(ctx context.Context, q querier) (int64, map[string]int64, error) {
	var count int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.resellers`).Scan(&count); err != nil {
		return 0, nil, fmt.Errorf("reseller summary: %w", err)
	}
	rows, err := q.Query(ctx, `SELECT currency, -sum(balance) FROM core.wallets WHERE balance < 0 GROUP BY currency`)
	if err != nil {
		return 0, nil, fmt.Errorf("reseller debt: %w", err)
	}
	defer rows.Close()
	debt := map[string]int64{}
	for rows.Next() {
		var (
			cur string
			sum int64
		)
		if err := rows.Scan(&cur, &sum); err != nil {
			return 0, nil, err
		}
		debt[cur] = sum
	}
	return count, debt, rows.Err()
}
