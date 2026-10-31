package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// New product announcements (migration 00053): a card for a product, posted in the store's channel and/or sent in private messages (a broadcast
// of the segment announce) to the customers who said yes to product news.

// SegmentAnnounce is the broadcasts' segment of the private part of an announcement: the active customers who said yes to product news and
// have not blocked the bot.
const SegmentAnnounce = "announce"

// Announcement mirrors core.announcements.
type Announcement struct {
	ID               string
	ProductKind      string // plan | supplier | license
	ProductID        string
	ProductName      string
	Texts            map[string]string // the card as written, per language, with its placeholders
	ToChannel        bool
	ToPrivate        bool
	ChannelLangs     string // default | both
	Forced           bool
	Status           string // queued | sent | cancelled
	DueAt            time.Time
	SentAt           *time.Time
	BroadcastID      string
	ChannelChat      string
	ChannelStatus    string // none | pending | sent | failed
	ChannelMessageID int64
	ChannelError     string
	CreatedBy        string
	CreatedAt        time.Time
}

const announcementCols = `id, product_kind, product_id, product_name, texts, to_channel, to_private, channel_langs, forced, status, due_at,
	sent_at, COALESCE(broadcast_id::text, ''), COALESCE(channel_chat, ''), channel_status, COALESCE(channel_message_id, 0),
	COALESCE(channel_error, ''), created_by, created_at`

func scanAnnouncement(row pgx.Row, extra ...any) (*Announcement, error) {
	var (
		a     Announcement
		texts []byte
	)
	dest := append([]any{&a.ID, &a.ProductKind, &a.ProductID, &a.ProductName, &texts, &a.ToChannel, &a.ToPrivate, &a.ChannelLangs, &a.Forced,
		&a.Status, &a.DueAt, &a.SentAt, &a.BroadcastID, &a.ChannelChat, &a.ChannelStatus, &a.ChannelMessageID, &a.ChannelError, &a.CreatedBy,
		&a.CreatedAt}, extra...)
	if err := row.Scan(dest...); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(texts, &a.Texts)
	return &a, nil
}

// LockAnnouncements serialises the decisions about announcements (the daily limit, the same product twice) until the transaction ends.
func (s *Store) LockAnnouncements(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('core.announcements'))`); err != nil {
		return fmt.Errorf("lock announcements: %w", err)
	}
	return nil
}

// InsertAnnouncement stores a new, queued announcement.
func (s *Store) InsertAnnouncement(ctx context.Context, q querier, a *Announcement) error {
	a.ID = buuid.MustV7().String()
	a.Status = "queued"
	if a.ChannelLangs == "" {
		a.ChannelLangs = "default"
	}
	a.ChannelStatus = "none"
	if a.ToChannel {
		a.ChannelStatus = "pending"
	}
	texts, _ := json.Marshal(a.Texts)
	err := q.QueryRow(ctx, `INSERT INTO core.announcements (id, product_kind, product_id, product_name, texts, to_channel, to_private,
			channel_langs, forced, status, due_at, channel_status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued', $10, $11, $12) RETURNING created_at`,
		a.ID, a.ProductKind, a.ProductID, a.ProductName, texts, a.ToChannel, a.ToPrivate, a.ChannelLangs, a.Forced, a.DueAt, a.ChannelStatus,
		a.CreatedBy).Scan(&a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert announcement: %w", err)
	}
	return nil
}

// GetAnnouncement loads one announcement; lock takes it FOR UPDATE.
func (s *Store) GetAnnouncement(ctx context.Context, q querier, id string, lock bool) (*Announcement, error) {
	sql := `SELECT ` + announcementCols + ` FROM core.announcements WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	a, err := scanAnnouncement(q.QueryRow(ctx, sql, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get announcement: %w", err)
	}
	return a, err
}

// ListAnnouncements lists announcements, newest first: of one product (kind and id), of a kind (id ""), or all (both "").
func (s *Store) ListAnnouncements(ctx context.Context, q querier, kind, productID string, p Page) ([]Announcement, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `SELECT `+announcementCols+`, count(*) OVER () FROM core.announcements
		WHERE ($1 = '' OR product_kind = $1) AND ($2 = '' OR product_id = $2)
		ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4`, kind, productID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list announcements: %w", err)
	}
	defer rows.Close()
	var (
		out   []Announcement
		total int64
	)
	for rows.Next() {
		a, err := scanAnnouncement(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *a)
	}
	return out, total, rows.Err()
}

// CountAnnouncementsDue counts the announcements (not cancelled) that go out from from to before to: what a store day already holds.
func (s *Store) CountAnnouncementsDue(ctx context.Context, q querier, from, to time.Time) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.announcements WHERE status <> 'cancelled' AND due_at >= $1 AND due_at < $2`,
		from, to).Scan(&n); err != nil {
		return 0, fmt.Errorf("count announcements: %w", err)
	}
	return n, nil
}

// LastAnnouncementDue is when the product was last announced (or is queued to be; cancelled ones do not count); nil: never.
func (s *Store) LastAnnouncementDue(ctx context.Context, q querier, kind, productID string) (*time.Time, error) {
	var at *time.Time
	if err := q.QueryRow(ctx, `SELECT max(due_at) FROM core.announcements WHERE product_kind = $1 AND product_id = $2 AND status <> 'cancelled'`,
		kind, productID).Scan(&at); err != nil {
		return nil, fmt.Errorf("last announcement: %w", err)
	}
	return at, nil
}

// DueAnnouncements lists the queued announcements whose time has come, oldest first.
func (s *Store) DueAnnouncements(ctx context.Context, q querier, now time.Time) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM core.announcements WHERE status = 'queued' AND due_at <= $1 ORDER BY due_at, id`, now)
	if err != nil {
		return nil, fmt.Errorf("due announcements: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// StartAnnouncement marks a queued announcement sent (its private broadcast, "" for none; the channel it is posted in, "" for none). False
// when it was no longer queued.
func (s *Store) StartAnnouncement(ctx context.Context, q querier, id, broadcastID, channelChat string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.announcements SET status = 'sent', sent_at = now(), broadcast_id = NULLIF($2, '')::uuid,
			channel_chat = NULLIF($3, '') WHERE id = $1 AND status = 'queued'`, id, broadcastID, channelChat)
	if err != nil {
		return false, fmt.Errorf("start announcement: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetAnnouncementChannel records how the channel post went: sent (with its message id) or failed (with the reason).
func (s *Store) SetAnnouncementChannel(ctx context.Context, q querier, id, status string, messageID int64, reason string) error {
	if _, err := q.Exec(ctx, `UPDATE core.announcements SET channel_status = $2, channel_message_id = NULLIF($3, 0),
			channel_error = NULLIF(left($4, 500), '') WHERE id = $1 AND to_channel`, id, status, messageID, reason); err != nil {
		return fmt.Errorf("announcement channel: %w", err)
	}
	return nil
}

// PendingChannelPosts lists the sent announcements whose channel post has not been made yet (pending), oldest first.
func (s *Store) PendingChannelPosts(ctx context.Context, q querier) ([]Announcement, error) {
	rows, err := q.Query(ctx, `SELECT `+announcementCols+` FROM core.announcements
		WHERE status = 'sent' AND channel_status = 'pending' ORDER BY sent_at, id LIMIT 20`)
	if err != nil {
		return nil, fmt.Errorf("pending channel posts: %w", err)
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// CancelAnnouncement cancels a queued announcement; false when it is not queued.
func (s *Store) CancelAnnouncement(ctx context.Context, q querier, id string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.announcements SET status = 'cancelled',
			channel_status = CASE WHEN to_channel THEN 'failed' ELSE 'none' END,
			channel_error = CASE WHEN to_channel THEN 'cancelled' END
		WHERE id = $1 AND status = 'queued'`, id)
	if err != nil {
		return false, fmt.Errorf("cancel announcement: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ClaimAnnounceAsk records that the bot asks an active customer about product news now, once: false when they were asked (or answered)
// before, or are not an active user.
func (s *Store) ClaimAnnounceAsk(ctx context.Context, q querier, telegramID int64) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.users SET announce_asked_at = now()
		WHERE telegram_id = $1 AND status = 'active' AND announce_asked_at IS NULL AND announce_optin IS NULL`, telegramID)
	if err != nil {
		return false, fmt.Errorf("claim announce ask: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetAnnounceOptIn stores a customer's answer about product news (by user id, or by Telegram id when userID is ""); ErrNotFound when there
// is no such user. It counts as their having been asked.
func (s *Store) SetAnnounceOptIn(ctx context.Context, q querier, userID string, telegramID int64, optIn bool) (*User, error) {
	u, err := scanUser(q.QueryRow(ctx, `UPDATE core.users SET announce_optin = $3, announce_asked_at = COALESCE(announce_asked_at, now()),
			updated_at = now()
		WHERE ($1 <> '' AND id::text = $1) OR ($1 = '' AND telegram_id = $2) RETURNING `+userCols, userID, telegramID, optIn))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("set announce opt-in: %w", err)
	}
	return u, err
}
