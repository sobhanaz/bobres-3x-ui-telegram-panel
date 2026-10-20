package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// GiftCode mirrors core.gift_codes (migration 00026). The code itself is never
// stored: only a keyed hash of it (Hash, set when a row is read to be redeemed)
// and its last four characters (Tail), so staff can tell codes apart.
type GiftCode struct {
	ID               string
	Hash             []byte // read by LockGiftCodeByHash only
	Tail             string
	Generated        bool
	Amount           int64
	Currency         string
	MaxRedemptions   int
	PerCustomerLimit int
	Redeemed         int
	ExpiresAt        *time.Time
	Enabled          bool
	Note             string
	CreatedBy        *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastRedeemedAt   *time.Time
}

// GiftRedemption mirrors core.gift_code_redemptions: one customer's use of a
// code, with the one ledger entry it wrote.
type GiftRedemption struct {
	ID            string
	GiftCodeID    string
	UserID        string
	Seq           int // the customer's n-th redemption of this code
	RequestKey    string
	Amount        int64
	Currency      string
	LedgerEntryID string
	BalanceAfter  int64
	CreatedAt     time.Time
}

// GiftRedemptionRow is a redemption with who made it, for the dashboard.
type GiftRedemptionRow struct {
	GiftRedemption
	TelegramID int64
	Username   string
}

// GiftCodeFilter narrows the list of gift codes. Status is "", active, disabled,
// expired or used_up (a code is the first that applies, in the order disabled,
// expired, used up); Query matches a code's note or its last four characters.
type GiftCodeFilter struct {
	Status string
	Query  string
}

const giftCols = `id, code_tail, generated, amount, currency, max_redemptions, per_customer_limit, redeemed_count,
	expires_at, enabled, note, created_by, created_at, updated_at, last_redeemed_at`

func giftDest(g *GiftCode) []any {
	return []any{&g.ID, &g.Tail, &g.Generated, &g.Amount, &g.Currency, &g.MaxRedemptions, &g.PerCustomerLimit, &g.Redeemed,
		&g.ExpiresAt, &g.Enabled, &g.Note, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt, &g.LastRedeemedAt}
}

func scanGift(row pgx.Row) (*GiftCode, error) {
	var g GiftCode
	err := row.Scan(giftDest(&g)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &g, err
}

// InsertGiftCode adds a code (g.ID, g.CreatedAt and g.UpdatedAt are set). It
// reports false, and adds nothing, when a code with this hash exists already.
func (s *Store) InsertGiftCode(ctx context.Context, tx pgx.Tx, g *GiftCode, hash []byte) (bool, error) {
	g.ID = buuid.MustV7().String()
	err := tx.QueryRow(ctx, `
		INSERT INTO core.gift_codes (id, code_hash, code_tail, generated, amount, currency, max_redemptions, per_customer_limit,
			expires_at, enabled, note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (code_hash) DO NOTHING
		RETURNING created_at, updated_at`,
		g.ID, hash, g.Tail, g.Generated, g.Amount, g.Currency, g.MaxRedemptions, g.PerCustomerLimit,
		g.ExpiresAt, g.Enabled, g.Note, g.CreatedBy).Scan(&g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert gift code: %w", err)
	}
	return true, nil
}

// LockGiftCodeByHash returns the code whose hash this is and holds its row
// until the transaction ends, so two redemptions of one code take turns.
func (s *Store) LockGiftCodeByHash(ctx context.Context, tx pgx.Tx, hash []byte) (*GiftCode, error) {
	var g GiftCode
	err := tx.QueryRow(ctx, `SELECT code_hash, `+giftCols+` FROM core.gift_codes WHERE code_hash = $1 FOR UPDATE`, hash).
		Scan(append([]any{&g.Hash}, giftDest(&g)...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock gift code: %w", err)
	}
	return &g, nil
}

// LockGiftCode is LockGiftCodeByHash for a code staff picked from the list.
func (s *Store) LockGiftCode(ctx context.Context, tx pgx.Tx, id string) (*GiftCode, error) {
	g, err := scanGift(tx.QueryRow(ctx, `SELECT `+giftCols+` FROM core.gift_codes WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, fmt.Errorf("lock gift code: %w", err)
	}
	return g, nil
}

// GetGiftCode returns one code.
func (s *Store) GetGiftCode(ctx context.Context, q querier, id string) (*GiftCode, error) {
	g, err := scanGift(q.QueryRow(ctx, `SELECT `+giftCols+` FROM core.gift_codes WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("get gift code: %w", err)
	}
	return g, nil
}

// SetGiftCodeEnabled switches a code on or off (its terms and its past
// redemptions stay).
func (s *Store) SetGiftCodeEnabled(ctx context.Context, tx pgx.Tx, id string, enabled bool) error {
	tag, err := tx.Exec(ctx, `UPDATE core.gift_codes SET enabled = $2, updated_at = now() WHERE id = $1`, id, enabled)
	if err != nil {
		return fmt.Errorf("set gift code enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountGiftRedemptions is how many times one customer redeemed one code.
func (s *Store) CountGiftRedemptions(ctx context.Context, q querier, codeID, userID string) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.gift_code_redemptions WHERE gift_code_id = $1 AND user_id = $2`,
		codeID, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count gift redemptions: %w", err)
	}
	return n, nil
}

const redemptionCols = `r.id, r.gift_code_id, r.user_id, r.seq, r.request_key, r.amount, r.currency, r.ledger_entry_id,
	r.balance_after, r.created_at`

func redemptionDest(r *GiftRedemption) []any {
	return []any{&r.ID, &r.GiftCodeID, &r.UserID, &r.Seq, &r.RequestKey, &r.Amount, &r.Currency, &r.LedgerEntryID,
		&r.BalanceAfter, &r.CreatedAt}
}

// GiftRedemptionByKey finds what a customer's request key redeemed, with the
// hash of that code (nil, ErrNotFound when the key made no redemption).
func (s *Store) GiftRedemptionByKey(ctx context.Context, q querier, userID, key string) (*GiftRedemption, []byte, error) {
	var (
		r    GiftRedemption
		hash []byte
	)
	err := q.QueryRow(ctx, `SELECT `+redemptionCols+`, g.code_hash
		FROM core.gift_code_redemptions r JOIN core.gift_codes g ON g.id = r.gift_code_id
		WHERE r.user_id = $1 AND r.request_key = $2`, userID, key).Scan(append(redemptionDest(&r), &hash)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("gift redemption by key: %w", err)
	}
	return &r, hash, nil
}

// InsertGiftRedemption records a redemption (r.ID and r.CreatedAt are set) and
// counts it on its code, which the caller holds locked. The CHECK on the code
// refuses a count above its limit even if the caller's own check were wrong.
func (s *Store) InsertGiftRedemption(ctx context.Context, tx pgx.Tx, r *GiftRedemption) error {
	r.ID = buuid.MustV7().String()
	if err := tx.QueryRow(ctx, `
		INSERT INTO core.gift_code_redemptions (id, gift_code_id, user_id, seq, request_key, amount, currency, ledger_entry_id, balance_after)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at`,
		r.ID, r.GiftCodeID, r.UserID, r.Seq, r.RequestKey, r.Amount, r.Currency, r.LedgerEntryID, r.BalanceAfter).
		Scan(&r.CreatedAt); err != nil {
		return fmt.Errorf("insert gift redemption: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE core.gift_codes SET redeemed_count = redeemed_count + 1, last_redeemed_at = now(), updated_at = now()
		WHERE id = $1`, r.GiftCodeID); err != nil {
		return fmt.Errorf("count gift redemption: %w", err)
	}
	return nil
}

// giftWhere is the filter of the list and its export: $1 the status ("" any),
// $2 a LIKE pattern for the note ("" none), $3 a code's last four characters.
const giftWhere = `
	WHERE ($1 = '' OR (CASE WHEN NOT enabled THEN 'disabled'
	                        WHEN expires_at IS NOT NULL AND expires_at <= now() THEN 'expired'
	                        WHEN redeemed_count >= max_redemptions THEN 'used_up'
	                        ELSE 'active' END) = $1)
	  AND (($2 = '' AND $3 = '') OR ($2 <> '' AND note ILIKE $2 ESCAPE '\') OR ($3 <> '' AND code_tail = $3))`

// giftArgs reads a filter into the arguments giftWhere takes.
func giftArgs(f GiftCodeFilter) (status, like, tail string) {
	status = f.Status
	if q := strings.TrimSpace(f.Query); q != "" {
		like = "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
		tail = strings.ToUpper(q)
	}
	if len(tail) > 8 { // a tail is at most 8 characters: nothing longer is one
		tail = ""
	}
	return status, like, tail
}

// ListGiftCodes returns a page of codes, newest first, and how many match.
func (s *Store) ListGiftCodes(ctx context.Context, q querier, f GiftCodeFilter, p Page) ([]GiftCode, int64, error) {
	p = p.clamp()
	status, like, tail := giftArgs(f)
	rows, err := q.Query(ctx, `SELECT `+giftCols+`, count(*) OVER () FROM core.gift_codes`+giftWhere+`
		ORDER BY created_at DESC, id DESC LIMIT $4 OFFSET $5`, status, like, tail, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list gift codes: %w", err)
	}
	defer rows.Close()
	var (
		out   []GiftCode
		total int64
	)
	for rows.Next() {
		var g GiftCode
		if err := rows.Scan(append(giftDest(&g), &total)...); err != nil {
			return nil, 0, err
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}

// GiftCodeRow is a code with the Telegram id of whoever made it (0: nobody),
// for the export.
type GiftCodeRow struct {
	GiftCode
	CreatedByTelegramID int64
}

// EachGiftCode calls fn for every matching code, newest first, up to max
// codes, and reports whether more matched than max. It reads as EachLedger
// does: a read-only transaction under the exports' statement timeout.
func (s *Store) EachGiftCode(ctx context.Context, f GiftCodeFilter, max int, fn func(GiftCodeRow) error) (bool, error) {
	more := false
	status, like, tail := giftArgs(f)
	err := s.readOnly(ctx, ExportTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT g.id, g.code_tail, g.generated, g.amount, g.currency, g.max_redemptions, g.per_customer_limit,
				g.redeemed_count, g.expires_at, g.enabled, g.note, g.created_by, g.created_at, g.updated_at, g.last_redeemed_at,
				COALESCE(u.telegram_id, 0)
			FROM (SELECT * FROM core.gift_codes`+giftWhere+` ORDER BY created_at DESC, id DESC LIMIT $4) g
			LEFT JOIN core.users u ON u.id = g.created_by
			ORDER BY g.created_at DESC, g.id DESC`, status, like, tail, max+1)
		if err != nil {
			return reportErr("each gift code", err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			if n == max {
				more = true
				return nil
			}
			var g GiftCodeRow
			if err := rows.Scan(append(giftDest(&g.GiftCode), &g.CreatedByTelegramID)...); err != nil {
				return err
			}
			if err := fn(g); err != nil {
				return err
			}
			n++
		}
		if err := rows.Err(); err != nil {
			return reportErr("each gift code", err)
		}
		return nil
	})
	return more, err
}

// ListGiftRedemptions returns a page of one code's redemptions, newest first,
// and how many it has.
func (s *Store) ListGiftRedemptions(ctx context.Context, q querier, codeID string, p Page) ([]GiftRedemptionRow, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `SELECT `+redemptionCols+`, u.telegram_id, COALESCE(u.username, ''), count(*) OVER ()
		FROM core.gift_code_redemptions r JOIN core.users u ON u.id = r.user_id
		WHERE r.gift_code_id = $1 ORDER BY r.created_at DESC, r.id DESC LIMIT $2 OFFSET $3`, codeID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list gift redemptions: %w", err)
	}
	defer rows.Close()
	var (
		out   []GiftRedemptionRow
		total int64
	)
	for rows.Next() {
		var r GiftRedemptionRow
		if err := rows.Scan(append(redemptionDest(&r.GiftRedemption), &r.TelegramID, &r.Username, &total)...); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// EachGiftRedemption is EachGiftCode for one code's redemptions.
func (s *Store) EachGiftRedemption(ctx context.Context, codeID string, max int, fn func(GiftRedemptionRow) error) (bool, error) {
	more := false
	err := s.readOnly(ctx, ExportTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+redemptionCols+`, u.telegram_id, COALESCE(u.username, '')
			FROM core.gift_code_redemptions r JOIN core.users u ON u.id = r.user_id
			WHERE r.gift_code_id = $1 ORDER BY r.created_at DESC, r.id DESC LIMIT $2`, codeID, max+1)
		if err != nil {
			return reportErr("each gift redemption", err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			if n == max {
				more = true
				return nil
			}
			var r GiftRedemptionRow
			if err := rows.Scan(append(redemptionDest(&r.GiftRedemption), &r.TelegramID, &r.Username)...); err != nil {
				return err
			}
			if err := fn(r); err != nil {
				return err
			}
			n++
		}
		if err := rows.Err(); err != nil {
			return reportErr("each gift redemption", err)
		}
		return nil
	})
	return more, err
}

// GiftLockedFor says how long entering gift codes stays closed to a customer
// who got too many wrong (0: it is open).
func (s *Store) GiftLockedFor(ctx context.Context, q querier, userID string) (time.Duration, error) {
	var secs float64
	err := q.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM (locked_until - now()))::float8
		FROM core.gift_code_failures WHERE user_id = $1 AND locked_until > now()`, userID).Scan(&secs)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("gift code lock: %w", err)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// GiftFailure is a customer's count of wrong codes after one more.
type GiftFailure struct {
	Failures int
	// Locked: this wrong code was the one that closed entering codes (it is
	// true once for every lock, not for the further wrong codes that follow).
	Locked bool
}

// RecordGiftFailure counts one more wrong code for a customer, in one
// statement that is safe against concurrent attempts. The count restarts when
// the window that opened with its first wrong code has passed; at limit wrong
// codes in a window entering codes closes for lockFor (and again, for as long,
// with every further wrong code that gets through meanwhile).
func (s *Store) RecordGiftFailure(ctx context.Context, q querier, userID string, window, lockFor time.Duration, limit int) (GiftFailure, error) {
	var out GiftFailure
	err := q.QueryRow(ctx, `
		INSERT INTO core.gift_code_failures AS f (user_id, failures, window_start, locked_until)
		VALUES ($1, 1, now(), CASE WHEN 1 >= $3 THEN now() + make_interval(secs => $4) END)
		ON CONFLICT (user_id) DO UPDATE SET
			failures = CASE WHEN f.window_start + make_interval(secs => $2) <= now() THEN 1 ELSE f.failures + 1 END,
			window_start = CASE WHEN f.window_start + make_interval(secs => $2) <= now() THEN now() ELSE f.window_start END,
			locked_until = CASE
				WHEN (CASE WHEN f.window_start + make_interval(secs => $2) <= now() THEN 1 ELSE f.failures + 1 END) >= $3
				THEN now() + make_interval(secs => $4)
				ELSE f.locked_until END
		RETURNING failures`, userID, window.Seconds(), limit, lockFor.Seconds()).Scan(&out.Failures)
	if err != nil {
		return out, fmt.Errorf("record gift failure: %w", err)
	}
	out.Locked = out.Failures == limit
	return out, nil
}
