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

// ErrStale: the item changed since it was read (an edit with an old version).
var ErrStale = errors.New("store: changed meanwhile")

// Broadcast mirrors core.broadcasts, with its pending count.
type Broadcast struct {
	ID           string
	Status       string // draft | scheduled | running | paused | done | cancelled
	Segment      string // all | active | expired | never_bought | resellers | announce
	Language     string // "" | fa | en
	Texts        map[string]string
	ButtonURL    string
	ButtonLabels map[string]string // nil: no button
	// Segment resellers only: who of the resellers it goes to, whether a Settle / Top up button is under it, and whether core queued it (the
	// weekly reminder, Kind "reminder", CreatedBy "") or staff wrote it ("manual").
	ResellerDebtOnly bool
	ResellerTierID   string
	SettleButton     bool
	Kind             string
	// BuyCallback is the callback data of the Buy button under a product announcement (segment announce; "" otherwise).
	BuyCallback string
	ScheduledAt *time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	Total       int
	Sent        int
	Blocked     int
	Failed      int
	Unknown     int
	Pending     int // recipients still to send (pending or claimed)
	LastError   string
	Version     int
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const broadcastCols = `b.id, b.status, b.segment, b.language, b.texts, COALESCE(b.button_url, ''), b.button_labels,
	b.scheduled_at, b.started_at, b.finished_at, b.total, b.sent, b.blocked, b.failed, b.unknown,
	(SELECT count(*) FROM core.broadcast_recipients r WHERE r.broadcast_id = b.id AND r.status IN ('pending', 'claimed')),
	COALESCE(b.last_error, ''), b.version, COALESCE(b.created_by::text, ''), b.created_at, b.updated_at,
	b.reseller_debt_only, COALESCE(b.reseller_tier_id::text, ''), b.settle_button, b.kind, COALESCE(b.buy_callback, '')`

func scanBroadcast(row pgx.Row, extra ...any) (*Broadcast, error) {
	var (
		b              Broadcast
		texts, buttons []byte
	)
	dest := append([]any{&b.ID, &b.Status, &b.Segment, &b.Language, &texts, &b.ButtonURL, &buttons, &b.ScheduledAt,
		&b.StartedAt, &b.FinishedAt, &b.Total, &b.Sent, &b.Blocked, &b.Failed, &b.Unknown, &b.Pending, &b.LastError,
		&b.Version, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt, &b.ResellerDebtOnly, &b.ResellerTierID, &b.SettleButton, &b.Kind, &b.BuyCallback}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(texts, &b.Texts)
	if len(buttons) > 0 && string(buttons) != "null" {
		_ = json.Unmarshal(buttons, &b.ButtonLabels)
	}
	return &b, nil
}

func labelsJSON(m map[string]string) []byte {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}

// InsertBroadcast stores a new draft.
func (s *Store) InsertBroadcast(ctx context.Context, q querier, b *Broadcast) error {
	b.ID = buuid.MustV7().String()
	b.Status = "draft"
	if b.Kind == "" {
		b.Kind = "manual"
	}
	texts, _ := json.Marshal(b.Texts)
	err := q.QueryRow(ctx, `
		INSERT INTO core.broadcasts (id, status, segment, language, texts, button_url, button_labels, created_by,
			reseller_debt_only, reseller_tier_id, settle_button, kind, buy_callback)
		VALUES ($1, 'draft', $2, $3, $4, NULLIF($5, ''), $6, NULLIF($7, '')::uuid, $8, NULLIF($9, '')::uuid, $10, $11, NULLIF($12, ''))
		RETURNING version, created_at, updated_at`,
		b.ID, b.Segment, b.Language, texts, b.ButtonURL, labelsJSON(b.ButtonLabels), b.CreatedBy,
		b.ResellerDebtOnly, b.ResellerTierID, b.SettleButton, b.Kind, b.BuyCallback).
		Scan(&b.Version, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert broadcast: %w", err)
	}
	return nil
}

// UpdateBroadcast changes a draft or scheduled broadcast's content when its
// version still matches (ErrStale otherwise, ErrNotFound when it is gone).
func (s *Store) UpdateBroadcast(ctx context.Context, q querier, b *Broadcast, version int) error {
	texts, _ := json.Marshal(b.Texts)
	tag, err := q.Exec(ctx, `UPDATE core.broadcasts SET segment = $2, language = $3, texts = $4, button_url = NULLIF($5, ''),
			button_labels = $6, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $7 AND status IN ('draft', 'scheduled')`,
		b.ID, b.Segment, b.Language, texts, b.ButtonURL, labelsJSON(b.ButtonLabels), version)
	if err != nil {
		return fmt.Errorf("update broadcast: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetBroadcast(ctx, q, b.ID, false); err != nil {
			return err
		}
		return ErrStale
	}
	return nil
}

// GetBroadcast loads a broadcast; lock takes it FOR UPDATE.
func (s *Store) GetBroadcast(ctx context.Context, q querier, id string, lock bool) (*Broadcast, error) {
	sql := `SELECT ` + broadcastCols + ` FROM core.broadcasts b WHERE b.id = $1`
	if lock {
		sql += ` FOR UPDATE OF b`
	}
	b, err := scanBroadcast(q.QueryRow(ctx, sql, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get broadcast: %w", err)
	}
	return b, err
}

// ListBroadcasts lists broadcasts, newest first.
func (s *Store) ListBroadcasts(ctx context.Context, q querier, status string, p Page) ([]Broadcast, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `SELECT `+broadcastCols+`, count(*) OVER () FROM core.broadcasts b
		WHERE ($1 = '' OR b.status = $1) ORDER BY b.created_at DESC, b.id DESC LIMIT $2 OFFSET $3`, status, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list broadcasts: %w", err)
	}
	defer rows.Close()
	var (
		out   []Broadcast
		total int64
	)
	for rows.Next() {
		b, err := scanBroadcast(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *b)
	}
	return out, total, rows.Err()
}

// SetBroadcastStatus moves a broadcast from one of the from statuses to to
// (with scheduledAt when scheduling, clearing last_error when resuming);
// false when it was in none of them.
func (s *Store) SetBroadcastStatus(ctx context.Context, q querier, id string, from []string, to string, scheduledAt *time.Time) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.broadcasts SET status = $3,
			scheduled_at = CASE WHEN $3 = 'scheduled' THEN $4 ELSE scheduled_at END,
			last_error = CASE WHEN $3 = 'running' THEN NULL ELSE last_error END,
			finished_at = CASE WHEN $3 IN ('done', 'cancelled') THEN now() ELSE finished_at END,
			updated_at = now()
		WHERE id = $1 AND status = ANY($2)`, id, from, to, scheduledAt)
	if err != nil {
		return false, fmt.Errorf("broadcast status: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CancelPending cancels a broadcast's recipients still waiting in line.
func (s *Store) CancelPending(ctx context.Context, q querier, id string) error {
	if _, err := q.Exec(ctx, `UPDATE core.broadcast_recipients SET status = 'cancelled', done_at = now()
		WHERE broadcast_id = $1 AND status = 'pending'`, id); err != nil {
		return fmt.Errorf("cancel recipients: %w", err)
	}
	return nil
}

// DeleteBroadcast deletes a draft; false when it is not a draft (or gone).
func (s *Store) DeleteBroadcast(ctx context.Context, q querier, id string) (bool, error) {
	tag, err := q.Exec(ctx, `DELETE FROM core.broadcasts WHERE id = $1 AND status = 'draft'`, id)
	if err != nil {
		return false, fmt.Errorf("delete broadcast: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// audienceWhere selects the users of a segment ($1: the language, "" = all).
// Banned users and users who blocked the bot are never in an audience. A
// purchase is a paid order with a price (not a trial, free order or staff
// extension), as the overview counts sales.
func audienceWhere(segment string) (string, error) {
	base := `u.status = 'active' AND u.bot_blocked_at IS NULL AND ($1 = '' OR u.language = $1)`
	active := `EXISTS (SELECT 1 FROM core.subscriptions s WHERE s.user_id = u.id AND s.status IN ('active', 'expiring_soon'))`
	switch segment {
	case "all":
		return base, nil
	case "active":
		return base + ` AND ` + active, nil
	case "expired":
		return base + ` AND NOT ` + active + ` AND EXISTS (SELECT 1 FROM core.subscriptions s
			WHERE s.user_id = u.id AND s.status IN ('expired', 'depleted'))`, nil
	case SegmentAnnounce: // the customers who said yes to product news (no answer is no)
		return base + ` AND u.announce_optin IS TRUE`, nil
	case "never_bought":
		return base + ` AND NOT EXISTS (SELECT 1 FROM core.orders o WHERE o.user_id = u.id AND o.amount > 0
			AND o.created_by IS NULL AND o.status IN ('paid', 'provisioning', 'active', 'provision_failed'))`, nil
	}
	return "", fmt.Errorf("store: unknown audience %q", segment)
}

// AudienceCount counts a segment's users, in all and per language.
func (s *Store) AudienceCount(ctx context.Context, q querier, segment, language string) (total, fa, en int64, err error) {
	where, err := audienceWhere(segment)
	if err != nil {
		return 0, 0, 0, err
	}
	err = q.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE u.language = 'fa'), count(*) FILTER (WHERE u.language = 'en')
		FROM core.users u WHERE `+where, language).Scan(&total, &fa, &en)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("audience count: %w", err)
	}
	return total, fa, en, nil
}

// DueBroadcasts lists scheduled broadcasts whose time has come, oldest first.
func (s *Store) DueBroadcasts(ctx context.Context, q querier, now time.Time) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM core.broadcasts WHERE status = 'scheduled' AND scheduled_at <= $1
		ORDER BY scheduled_at, id`, now)
	if err != nil {
		return nil, fmt.Errorf("due broadcasts: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Personalize writes the recipients of a broadcast whose every recipient gets
// a text of their own (the segment resellers): who they are now and the text
// made for each. StartBroadcast calls it in its transaction.
type Personalize func(ctx context.Context, tx pgx.Tx, b *Broadcast) ([]Recipient, error)

// StartBroadcast puts a due broadcast's audience in line (a snapshot taken
// now) and sets it running; done at once when nobody matches. False when
// another worker took it or it is no longer due. A broadcast to resellers
// takes its recipients, each with their text, from personal.
func (s *Store) StartBroadcast(ctx context.Context, tx pgx.Tx, id string, now time.Time, personal Personalize) (*Broadcast, bool, error) {
	var segment, language string
	err := tx.QueryRow(ctx, `SELECT segment, language FROM core.broadcasts WHERE id = $1 AND status = 'scheduled'
		AND scheduled_at <= $2 FOR UPDATE SKIP LOCKED`, id, now).Scan(&segment, &language)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("start broadcast: %w", err)
	}
	var n int64
	if segment == SegmentResellers {
		if n, err = s.insertPersonal(ctx, tx, id, personal); err != nil {
			return nil, false, err
		}
	} else {
		where, err := audienceWhere(segment)
		if err != nil {
			return nil, false, err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO core.broadcast_recipients (broadcast_id, user_id, telegram_id, language)
			SELECT $2, u.id, u.telegram_id, u.language FROM core.users u WHERE `+where, language, id)
		if err != nil {
			return nil, false, fmt.Errorf("broadcast recipients: %w", err)
		}
		n = tag.RowsAffected()
	}
	status := "running"
	if n == 0 {
		status = "done"
	}
	if _, err := tx.Exec(ctx, `UPDATE core.broadcasts SET status = $2, total = $3, started_at = now(),
			finished_at = CASE WHEN $2 = 'done' THEN now() END, updated_at = now() WHERE id = $1`,
		id, status, n); err != nil {
		return nil, false, fmt.Errorf("start broadcast: %w", err)
	}
	b, err := s.GetBroadcast(ctx, tx, id, false)
	return b, err == nil, err
}

// ExpireClaims gives up on recipients claimed longer than lease ago (the
// bot died mid-send): they become unknown and are never sent again.
func (s *Store) ExpireClaims(ctx context.Context, q querier, lease time.Duration) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `
		WITH x AS (
			UPDATE core.broadcast_recipients SET status = 'unknown', done_at = now(), error = 'no report from the bot'
			WHERE status = 'claimed' AND claimed_at < now() - $1::interval
			RETURNING broadcast_id),
		c AS (SELECT broadcast_id, count(*) AS n FROM x GROUP BY broadcast_id),
		u AS (UPDATE core.broadcasts b SET unknown = b.unknown + c.n, updated_at = now() FROM c WHERE b.id = c.broadcast_id)
		SELECT COALESCE(sum(n), 0) FROM c`, lease.String()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("expire claims: %w", err)
	}
	return n, nil
}

// FinishBroadcasts marks running broadcasts with nobody left in line done.
func (s *Store) FinishBroadcasts(ctx context.Context, q querier) ([]Broadcast, error) {
	rows, err := q.Query(ctx, `UPDATE core.broadcasts b SET status = 'done', finished_at = now(), updated_at = now()
		WHERE b.status = 'running' AND NOT EXISTS (SELECT 1 FROM core.broadcast_recipients r
			WHERE r.broadcast_id = b.id AND r.status IN ('pending', 'claimed'))
		RETURNING `+broadcastCols)
	if err != nil {
		return nil, fmt.Errorf("finish broadcasts: %w", err)
	}
	defer rows.Close()
	var out []Broadcast
	for rows.Next() {
		b, err := scanBroadcast(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Recipient is a user a broadcast goes to.
type Recipient struct {
	UserID     string
	TelegramID int64
	Language   string
	// Text is the recipient's own text ("": the broadcast's in their language).
	Text     string
	Username string
	Status   string
	Error    string
	DoneAt   *time.Time
}

// ClaimRecipients takes the next recipients of the running broadcast that
// started first, marking them claimed (a lease). Nil when nothing waits.
func (s *Store) ClaimRecipients(ctx context.Context, tx pgx.Tx, limit int) (*Broadcast, []Recipient, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT b.id FROM core.broadcasts b WHERE b.status = 'running' AND EXISTS (
			SELECT 1 FROM core.broadcast_recipients r WHERE r.broadcast_id = b.id AND r.status = 'pending')
		ORDER BY b.started_at, b.id LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("claim broadcast: %w", err)
	}
	rows, err := tx.Query(ctx, `
		WITH c AS (
			SELECT user_id FROM core.broadcast_recipients
			WHERE broadcast_id = $1 AND status = 'pending' ORDER BY telegram_id LIMIT $2 FOR UPDATE SKIP LOCKED)
		UPDATE core.broadcast_recipients r SET status = 'claimed', claimed_at = now()
		FROM c WHERE r.broadcast_id = $1 AND r.user_id = c.user_id
		RETURNING r.user_id, r.telegram_id, r.language, COALESCE(r.text, '')`, id, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("claim recipients: %w", err)
	}
	var out []Recipient
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.UserID, &r.TelegramID, &r.Language, &r.Text); err != nil {
			rows.Close()
			return nil, nil, err
		}
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	b, err := s.GetBroadcast(ctx, tx, id, false)
	if err != nil {
		return nil, nil, err
	}
	return b, out, nil
}

// Result is what the bot says about one recipient.
type Result struct {
	UserID    string
	Outcome   string // sent | blocked | failed | unknown | released
	MessageID int64
	Error     string
}

// RecordResults applies the bot's results to recipients it still holds
// (claimed): a repeated report changes nothing. Released recipients go
// back in line, or are cancelled when the broadcast was. Users who blocked
// the bot are marked.
func (s *Store) RecordResults(ctx context.Context, tx pgx.Tx, broadcastID string, results []Result) error {
	if len(results) == 0 {
		return nil
	}
	users := make([]string, len(results))
	outcomes := make([]string, len(results))
	msgs := make([]int64, len(results))
	errs := make([]string, len(results))
	for i, r := range results {
		users[i], outcomes[i], msgs[i], errs[i] = r.UserID, r.Outcome, r.MessageID, r.Error
	}
	// Wait for a pause or cancel under way (it locks the broadcast FOR UPDATE)
	// and block one from starting, so released recipients follow the status
	// it ends with. KEY SHARE leaves the counters' updates free.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM core.broadcasts WHERE id = $1 FOR KEY SHARE`, broadcastID); err != nil {
		return fmt.Errorf("record results: %w", err)
	}
	_, err := tx.Exec(ctx, `
		WITH res AS (
			SELECT * FROM unnest($2::uuid[], $3::text[], $4::bigint[], $5::text[]) AS x(user_id, outcome, message_id, error)),
		b AS (SELECT status FROM core.broadcasts WHERE id = $1),
		upd AS (
			UPDATE core.broadcast_recipients r SET
				status = CASE WHEN res.outcome <> 'released' THEN res.outcome
				              WHEN (SELECT status FROM b) = 'cancelled' THEN 'cancelled' ELSE 'pending' END,
				claimed_at = CASE WHEN res.outcome = 'released' THEN NULL ELSE r.claimed_at END,
				done_at = CASE WHEN res.outcome = 'released' AND (SELECT status FROM b) <> 'cancelled' THEN NULL ELSE now() END,
				message_id = NULLIF(res.message_id, 0), error = NULLIF(left(res.error, 500), '')
			FROM res WHERE r.broadcast_id = $1 AND r.user_id = res.user_id AND r.status = 'claimed'
			RETURNING r.status, r.user_id),
		cnt AS (SELECT count(*) FILTER (WHERE status = 'sent') AS sent, count(*) FILTER (WHERE status = 'blocked') AS blocked,
			count(*) FILTER (WHERE status = 'failed') AS failed, count(*) FILTER (WHERE status = 'unknown') AS unknown FROM upd),
		blk AS (UPDATE core.users u SET bot_blocked_at = COALESCE(u.bot_blocked_at, now())
			FROM upd WHERE upd.status = 'blocked' AND u.id = upd.user_id)
		UPDATE core.broadcasts bc SET sent = bc.sent + cnt.sent, blocked = bc.blocked + cnt.blocked,
			failed = bc.failed + cnt.failed, unknown = bc.unknown + cnt.unknown, updated_at = now()
		FROM cnt WHERE bc.id = $1`, broadcastID, users, outcomes, msgs, errs)
	if err != nil {
		return fmt.Errorf("record results: %w", err)
	}
	return nil
}

// HaltBroadcast pauses a running broadcast with the bot's reason; false when
// it was not running.
func (s *Store) HaltBroadcast(ctx context.Context, q querier, id, reason string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.broadcasts SET status = 'paused', last_error = left($2, 500), updated_at = now()
		WHERE id = $1 AND status = 'running'`, id, reason)
	if err != nil {
		return false, fmt.Errorf("halt broadcast: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListRecipients lists a broadcast's recipients with one of the statuses.
func (s *Store) ListRecipients(ctx context.Context, q querier, broadcastID string, statuses []string, p Page) ([]Recipient, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `SELECT r.user_id, r.telegram_id, r.language, COALESCE(u.username, ''), r.status,
			COALESCE(r.error, ''), r.done_at, count(*) OVER ()
		FROM core.broadcast_recipients r JOIN core.users u ON u.id = r.user_id
		WHERE r.broadcast_id = $1 AND r.status = ANY($2)
		ORDER BY r.done_at DESC NULLS LAST, r.telegram_id LIMIT $3 OFFSET $4`, broadcastID, statuses, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list recipients: %w", err)
	}
	defer rows.Close()
	var (
		out   []Recipient
		total int64
	)
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.UserID, &r.TelegramID, &r.Language, &r.Username, &r.Status, &r.Error, &r.DoneAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
