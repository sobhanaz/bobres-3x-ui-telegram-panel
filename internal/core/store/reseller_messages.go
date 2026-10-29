package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Messages to resellers and the weekly settlement reminder (migration 00052): a broadcast of the segment resellers whose recipients each get a
// text of their own, written when it starts.

// SegmentResellers is the broadcasts' segment of the messages to resellers.
const SegmentResellers = "resellers"

// ResellerAudience is who of the resellers a message goes to. Every one of them is an active reseller who has not blocked the bot; Language ""
// takes every language.
type ResellerAudience struct {
	DebtOnly bool   // only those with a negative balance in some currency
	TierID   string // "" = every tier
	Language string
}

// ResellerContact is a reseller as a message to them is written: who they are, their tier, how many customers joined through their link, and
// their wallets (with their credit limits).
type ResellerContact struct {
	UserID     string
	TelegramID int64
	Username   string
	Language   string
	TierName   string
	PanelGroup string
	Customers  int64
	Wallets    []Wallet
}

const resellerContactSelect = `
	SELECT u.id, u.telegram_id, COALESCE(u.username, ''), u.language, t.name, COALESCE(r.panel_group, ''),
		(SELECT count(*) FROM core.users c WHERE c.referred_by = u.id)
	FROM core.resellers r
	JOIN core.users u ON u.id = r.user_id
	JOIN core.reseller_tiers t ON t.id = r.tier_id`

// resellerAudienceWhere selects the audience ($1 language, $2 tier id, $3 debt only).
const resellerAudienceWhere = `
	WHERE u.role = 'reseller' AND u.status = 'active' AND u.bot_blocked_at IS NULL
	  AND ($1 = '' OR u.language = $1)
	  AND ($2 = '' OR r.tier_id::text = $2)
	  AND (NOT $3 OR EXISTS (SELECT 1 FROM core.wallets w WHERE w.user_id = r.user_id AND w.balance < 0))`

func scanContacts(rows pgx.Rows) ([]ResellerContact, error) {
	defer rows.Close()
	var out []ResellerContact
	for rows.Next() {
		var c ResellerContact
		if err := rows.Scan(&c.UserID, &c.TelegramID, &c.Username, &c.Language, &c.TierName, &c.PanelGroup, &c.Customers); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResellerContacts lists the audience, in the order of their Telegram ids, with their wallets.
func (s *Store) ResellerContacts(ctx context.Context, q querier, a ResellerAudience) ([]ResellerContact, error) {
	rows, err := q.Query(ctx, resellerContactSelect+resellerAudienceWhere+` ORDER BY u.telegram_id`, a.Language, a.TierID, a.DebtOnly)
	if err != nil {
		return nil, fmt.Errorf("reseller contacts: %w", err)
	}
	out, err := scanContacts(rows)
	if err != nil {
		return nil, fmt.Errorf("reseller contacts: %w", err)
	}
	return out, s.fillContactWallets(ctx, q, out)
}

// ResellerContact is one reseller (whatever their status), for a preview; ErrNotFound when the user is no reseller.
func (s *Store) ResellerContact(ctx context.Context, q querier, userID string) (*ResellerContact, error) {
	rows, err := q.Query(ctx, resellerContactSelect+` WHERE r.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("reseller contact: %w", err)
	}
	out, err := scanContacts(rows)
	if err != nil {
		return nil, fmt.Errorf("reseller contact: %w", err)
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	if err := s.fillContactWallets(ctx, q, out); err != nil {
		return nil, err
	}
	return &out[0], nil
}

// CountResellerAudience counts the audience, in all and per language.
func (s *Store) CountResellerAudience(ctx context.Context, q querier, a ResellerAudience) (total, fa, en int64, err error) {
	err = q.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE u.language = 'fa'), count(*) FILTER (WHERE u.language = 'en')
		FROM core.resellers r JOIN core.users u ON u.id = r.user_id`+resellerAudienceWhere, a.Language, a.TierID, a.DebtOnly).Scan(&total, &fa, &en)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("reseller audience: %w", err)
	}
	return total, fa, en, nil
}

func (s *Store) fillContactWallets(ctx context.Context, q querier, out []ResellerContact) error {
	if len(out) == 0 {
		return nil
	}
	ids := make([]string, len(out))
	idx := make(map[string]int, len(out))
	for i := range out {
		ids[i], idx[out[i].UserID] = out[i].UserID, i
	}
	rows, err := q.Query(ctx, `SELECT `+walletCols+` FROM core.wallets WHERE user_id = ANY($1) ORDER BY currency`, ids)
	if err != nil {
		return fmt.Errorf("reseller wallets: %w", err)
	}
	wallets, err := collectWallets(rows)
	if err != nil {
		return err
	}
	for _, w := range wallets {
		c := &out[idx[w.UserID]]
		c.Wallets = append(c.Wallets, w)
	}
	return nil
}

// insertPersonal puts the recipients personal writes in line, each with their text, and answers how many.
func (s *Store) insertPersonal(ctx context.Context, tx pgx.Tx, id string, personal Personalize) (int64, error) {
	if personal == nil {
		return 0, fmt.Errorf("start broadcast %s: a message to resellers needs its texts written", id)
	}
	b, err := s.GetBroadcast(ctx, tx, id, false)
	if err != nil {
		return 0, err
	}
	rs, err := personal(ctx, tx, b)
	if err != nil {
		return 0, err
	}
	if len(rs) == 0 {
		return 0, nil
	}
	users := make([]string, len(rs))
	tgs := make([]int64, len(rs))
	langs := make([]string, len(rs))
	texts := make([]string, len(rs))
	for i, r := range rs {
		users[i], tgs[i], langs[i], texts[i] = r.UserID, r.TelegramID, r.Language, r.Text
	}
	tag, err := tx.Exec(ctx, `INSERT INTO core.broadcast_recipients (broadcast_id, user_id, telegram_id, language, text)
		SELECT $1, x.user_id, x.telegram_id, x.language, NULLIF(x.text, '')
		FROM unnest($2::uuid[], $3::bigint[], $4::text[], $5::text[]) AS x(user_id, telegram_id, language, text)
		ON CONFLICT DO NOTHING`, id, users, tgs, langs, texts)
	if err != nil {
		return 0, fmt.Errorf("broadcast recipients: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ReminderClaim is a week the settlement reminder was queued: the store-local date it was due on, the zone it was read in, and the broadcast it
// went out as (nil once that was deleted).
type ReminderClaim struct {
	Day       string
	Zone      string
	DueAt     time.Time
	CreatedAt time.Time
	Broadcast *Broadcast
}

// ClaimResellerReminder takes the claim on the reminder due on day (a store-local date, "2026-10-29"). The caller does it in the transaction
// that writes the reminder's broadcast (SetReminderBroadcast), so both commit or neither does. False when the day is claimed already (by an
// earlier run, or a claimer that committed first: one still open is waited for, and when it rolls back this one gets the claim).
func (s *Store) ClaimResellerReminder(ctx context.Context, tx pgx.Tx, day, zone string, dueAt time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO core.reseller_reminder_claims (day, zone, due_at) VALUES ($1::date, $2, $3)
		ON CONFLICT (day) DO NOTHING`, day, zone, dueAt)
	if err != nil {
		return false, fmt.Errorf("claim reseller reminder %s: %w", day, err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetReminderBroadcast records the broadcast a claimed reminder went out as.
func (s *Store) SetReminderBroadcast(ctx context.Context, tx pgx.Tx, day, broadcastID string) error {
	if _, err := tx.Exec(ctx, `UPDATE core.reseller_reminder_claims SET broadcast_id = $2 WHERE day = $1::date`, day, broadcastID); err != nil {
		return fmt.Errorf("reseller reminder %s: %w", day, err)
	}
	return nil
}

// LastResellerReminder is the newest claim (nil: the reminder never went out).
func (s *Store) LastResellerReminder(ctx context.Context, q querier) (*ReminderClaim, error) {
	var (
		c  ReminderClaim
		id *string
	)
	err := q.QueryRow(ctx, `SELECT day::text, zone, due_at, created_at, broadcast_id::text FROM core.reseller_reminder_claims
		ORDER BY day DESC LIMIT 1`).Scan(&c.Day, &c.Zone, &c.DueAt, &c.CreatedAt, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last reseller reminder: %w", err)
	}
	if id != nil {
		b, err := s.GetBroadcast(ctx, q, *id, false)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		c.Broadcast = b
	}
	return &c, nil
}
