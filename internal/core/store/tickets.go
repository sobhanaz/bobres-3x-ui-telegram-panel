package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// ErrTicketOpen: the customer already has a ticket that is not closed.
var ErrTicketOpen = errors.New("store: the customer has an open ticket")

// TicketMessage mirrors core.ticket_messages.
type TicketMessage struct {
	ID             string
	TicketID       string
	Kind           string // customer | staff | note
	AuthorID       *string
	Body           string
	FileID         string
	FileType       string // photo | document
	FileMime       string
	FileName       string
	FileSize       int64
	TGMessageID    *int64
	IdempotencyKey *string
	Delivery       string // staff replies: pending | sent | blocked | failed
	DeliveredAt    *time.Time
	CreatedAt      time.Time
}

const ticketCols = `t.id, t.number, t.user_id, t.category, t.subject, t.status, t.assigned_to, t.created_at, t.updated_at,
	t.last_customer_at, t.last_staff_at, t.closed_at, t.closed_by, COALESCE(t.close_reason, '')`

func scanTicket(row pgx.Row, extra ...any) (*Ticket, error) {
	var t Ticket
	dest := append([]any{&t.ID, &t.Number, &t.UserID, &t.Category, &t.Subject, &t.Status, &t.AssignedTo, &t.CreatedAt,
		&t.UpdatedAt, &t.LastCustomerAt, &t.LastStaffAt, &t.ClosedAt, &t.ClosedBy, &t.CloseReason}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

const messageCols = `m.id, m.ticket_id, m.kind, m.author_id, m.body, COALESCE(m.file_id, ''), COALESCE(m.file_type, ''),
	COALESCE(m.file_mime, ''), COALESCE(m.file_name, ''), COALESCE(m.file_size, 0), m.tg_message_id, m.idempotency_key,
	COALESCE(m.delivery, ''), m.delivered_at, m.created_at`

func scanMessage(row pgx.Row) (*TicketMessage, error) {
	var m TicketMessage
	err := row.Scan(&m.ID, &m.TicketID, &m.Kind, &m.AuthorID, &m.Body, &m.FileID, &m.FileType, &m.FileMime, &m.FileName,
		&m.FileSize, &m.TGMessageID, &m.IdempotencyKey, &m.Delivery, &m.DeliveredAt, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetTicket loads a ticket; lock takes it FOR UPDATE (inside a transaction).
func (s *Store) GetTicket(ctx context.Context, q querier, id string, lock bool) (*Ticket, error) {
	sql := `SELECT ` + ticketCols + ` FROM core.tickets t WHERE t.id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	t, err := scanTicket(q.QueryRow(ctx, sql, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get ticket: %w", err)
	}
	return t, err
}

// OpenTicketOf is the customer's ticket that is not closed, locked for update.
func (s *Store) OpenTicketOf(ctx context.Context, q querier, userID string) (*Ticket, error) {
	t, err := scanTicket(q.QueryRow(ctx, `SELECT `+ticketCols+` FROM core.tickets t
		WHERE t.user_id = $1 AND t.status <> 'closed' FOR UPDATE`, userID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("open ticket: %w", err)
	}
	return t, err
}

// TicketByDelivered finds the customer's ticket whose staff reply was
// delivered as the Telegram message tgMessageID (a reply-to in the chat).
func (s *Store) TicketByDelivered(ctx context.Context, q querier, userID string, tgMessageID int64) (*Ticket, error) {
	t, err := scanTicket(q.QueryRow(ctx, `SELECT `+ticketCols+` FROM core.tickets t
		JOIN core.ticket_messages m ON m.ticket_id = t.id
		WHERE t.user_id = $1 AND m.tg_message_id = $2 AND m.kind = 'staff'
		ORDER BY m.created_at DESC LIMIT 1 FOR UPDATE OF t`, userID, tgMessageID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("ticket by delivered message: %w", err)
	}
	return t, err
}

// InsertTicket starts a ticket; ErrTicketOpen when the customer has one.
func (s *Store) InsertTicket(ctx context.Context, q querier, t *Ticket) error {
	t.ID = buuid.MustV7().String()
	t.Status = "open"
	err := q.QueryRow(ctx, `
		INSERT INTO core.tickets (id, user_id, category, subject, status, last_customer_at)
		VALUES ($1, $2, $3, $4, 'open', now()) RETURNING number, created_at, updated_at, last_customer_at`,
		t.ID, t.UserID, t.Category, t.Subject).Scan(&t.Number, &t.CreatedAt, &t.UpdatedAt, &t.LastCustomerAt)
	if isUniqueViolation(err) {
		return ErrTicketOpen
	}
	if err != nil {
		return fmt.Errorf("insert ticket: %w", err)
	}
	return nil
}

// AddTicketMessage stores a message. A repeated idempotency key stores
// nothing and returns the first message with created=false.
func (s *Store) AddTicketMessage(ctx context.Context, q querier, m *TicketMessage) (*TicketMessage, bool, error) {
	m.ID = buuid.MustV7().String()
	var delivery *string
	if m.Delivery != "" {
		delivery = &m.Delivery
	}
	err := q.QueryRow(ctx, `
		INSERT INTO core.ticket_messages (id, ticket_id, kind, author_id, body, file_id, file_type, file_mime, file_name,
			file_size, tg_message_id, idempotency_key, delivery)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, 0),
			$11, $12, $13)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING created_at`,
		m.ID, m.TicketID, m.Kind, m.AuthorID, m.Body, m.FileID, m.FileType, m.FileMime, m.FileName, m.FileSize,
		m.TGMessageID, m.IdempotencyKey, delivery).Scan(&m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) && m.IdempotencyKey != nil {
		first, ferr := s.TicketMessageByKey(ctx, q, *m.IdempotencyKey)
		return first, false, ferr
	}
	if err != nil {
		return nil, false, fmt.Errorf("add ticket message: %w", err)
	}
	return m, true, nil
}

// TicketMessageByKey finds a message by its idempotency key.
func (s *Store) TicketMessageByKey(ctx context.Context, q querier, key string) (*TicketMessage, error) {
	m, err := scanMessage(q.QueryRow(ctx, `SELECT `+messageCols+` FROM core.ticket_messages m WHERE m.idempotency_key = $1`, key))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("ticket message by key: %w", err)
	}
	return m, err
}

// GetTicketMessage loads one message.
func (s *Store) GetTicketMessage(ctx context.Context, q querier, id string) (*TicketMessage, error) {
	m, err := scanMessage(q.QueryRow(ctx, `SELECT `+messageCols+` FROM core.ticket_messages m WHERE m.id = $1`, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get ticket message: %w", err)
	}
	return m, err
}

// TicketMessages lists a ticket's messages, oldest first; notes only when
// asked for, and only the last `last` when last > 0.
func (s *Store) TicketMessages(ctx context.Context, q querier, ticketID string, notes bool, last int) ([]TicketMessage, error) {
	sql := `SELECT * FROM (SELECT ` + messageCols + ` FROM core.ticket_messages m
		WHERE m.ticket_id = $1 AND ($2 OR m.kind <> 'note') ORDER BY m.created_at DESC, m.id DESC`
	args := []any{ticketID, notes}
	if last > 0 {
		sql += ` LIMIT $3`
		args = append(args, last)
	}
	sql += `) m ORDER BY m.created_at, m.id`
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("ticket messages: %w", err)
	}
	defer rows.Close()
	var out []TicketMessage
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// CustomerWrote marks a ticket as waiting for staff.
func (s *Store) CustomerWrote(ctx context.Context, q querier, id string) error {
	if _, err := q.Exec(ctx, `UPDATE core.tickets SET status = 'open', last_customer_at = now(), updated_at = now()
		WHERE id = $1`, id); err != nil {
		return fmt.Errorf("customer wrote: %w", err)
	}
	return nil
}

// StaffReplied marks a ticket answered (or closed with the reply) and gives
// it to the replier when nobody has it.
func (s *Store) StaffReplied(ctx context.Context, q querier, id, actorID string, closeIt bool) error {
	_, err := q.Exec(ctx, `UPDATE core.tickets SET
			status = CASE WHEN $3 THEN 'closed' ELSE 'answered' END,
			last_staff_at = now(), updated_at = now(), assigned_to = COALESCE(assigned_to, $2),
			closed_at = CASE WHEN $3 THEN now() END,
			closed_by = CASE WHEN $3 THEN $2::uuid END,
			close_reason = CASE WHEN $3 THEN 'staff' END
		WHERE id = $1`, id, actorID, closeIt)
	if err != nil {
		return fmt.Errorf("staff replied: %w", err)
	}
	return nil
}

// TouchTicket records a change that leaves the status (a note).
func (s *Store) TouchTicket(ctx context.Context, q querier, id string) error {
	if _, err := q.Exec(ctx, `UPDATE core.tickets SET updated_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("touch ticket: %w", err)
	}
	return nil
}

// CloseTicket closes a ticket; by is nil for the system. Returns false when
// it was closed already.
func (s *Store) CloseTicket(ctx context.Context, q querier, id string, by *string, reason string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.tickets SET status = 'closed', closed_at = now(), closed_by = $2,
			close_reason = $3, updated_at = now()
		WHERE id = $1 AND status <> 'closed'`, id, by, reason)
	if err != nil {
		return false, fmt.Errorf("close ticket: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ReopenTicket opens a closed ticket again; ErrTicketOpen when the customer
// has another one.
func (s *Store) ReopenTicket(ctx context.Context, q querier, id string) error {
	_, err := q.Exec(ctx, `UPDATE core.tickets SET status = 'open', closed_at = NULL, closed_by = NULL,
			close_reason = NULL, updated_at = now()
		WHERE id = $1 AND status = 'closed'`, id)
	if isUniqueViolation(err) {
		return ErrTicketOpen
	}
	if err != nil {
		return fmt.Errorf("reopen ticket: %w", err)
	}
	return nil
}

// AssignTicket sets (or clears, nil) who handles a ticket.
func (s *Store) AssignTicket(ctx context.Context, q querier, id string, assignee *string) error {
	if _, err := q.Exec(ctx, `UPDATE core.tickets SET assigned_to = $2, updated_at = now() WHERE id = $1`, id, assignee); err != nil {
		return fmt.Errorf("assign ticket: %w", err)
	}
	return nil
}

// RecordTicketDelivery records how a staff reply's delivery went; only a
// pending one changes (a repeat report is harmless).
func (s *Store) RecordTicketDelivery(ctx context.Context, q querier, id, status string, tgMessageID *int64) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.ticket_messages SET delivery = $2, delivered_at = now(),
			tg_message_id = COALESCE($3, tg_message_id)
		WHERE id = $1 AND kind = 'staff' AND delivery = 'pending'`, id, status, tgMessageID)
	if err != nil {
		return false, fmt.Errorf("record delivery: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// UserTickets lists a customer's tickets, newest first.
func (s *Store) UserTickets(ctx context.Context, q querier, userID string, limit int) ([]Ticket, error) {
	rows, err := q.Query(ctx, `SELECT `+ticketCols+` FROM core.tickets t WHERE t.user_id = $1
		ORDER BY t.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("user tickets: %w", err)
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// LastFrom says who wrote last on a ticket: customer or staff.
func LastFrom(t *Ticket) string {
	if t.LastStaffAt != nil && (t.LastCustomerAt == nil || t.LastStaffAt.After(*t.LastCustomerAt)) {
		return "staff"
	}
	return "customer"
}

// TicketFilter narrows the staff's ticket list.
type TicketFilter struct {
	Status   string // open | answered | closed | active (open and answered) | "" (all)
	Assigned string // me | none | a user id | ""
	ActorID  string // for "me"
	Category string
	UserID   string
	Query    string
}

// TicketRow is a ticket with what the list shows.
type TicketRow struct {
	Ticket
	TelegramID       int64
	Username         string
	UserStatus       string
	Language         string
	BotBlocked       bool
	AssigneeTG       int64
	AssigneeUsername string
	LastKind         string
	LastPreview      string
	LastHasFile      bool
	LastAt           *time.Time
}

var hexSuffixRe = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// ListTickets lists tickets for staff: waiting ones longest-waiting first,
// the others most recent first.
func (s *Store) ListTickets(ctx context.Context, q querier, f TicketFilter, p Page) ([]TicketRow, int64, error) {
	p = p.clamp()
	var (
		where []string
		args  []any
	)
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	switch f.Status {
	case "open", "answered", "closed":
		where = append(where, "t.status = "+arg(f.Status))
	case "active":
		where = append(where, "t.status <> 'closed'")
	}
	switch f.Assigned {
	case "":
	case "me":
		where = append(where, "t.assigned_to = "+arg(f.ActorID)+"::uuid")
	case "none":
		where = append(where, "t.assigned_to IS NULL")
	default:
		where = append(where, "t.assigned_to = "+arg(f.Assigned)+"::uuid")
	}
	if f.Category != "" {
		where = append(where, "t.category = "+arg(f.Category))
	}
	if f.UserID != "" {
		where = append(where, "t.user_id = "+arg(f.UserID)+"::uuid")
	}
	if qs := strings.TrimSpace(f.Query); qs != "" {
		num := strings.TrimPrefix(qs, "#")
		switch n, err := strconv.ParseInt(num, 10, 64); {
		case err == nil && n > 0 && hexSuffixRe.MatchString(qs):
			// Six digits: a ticket number, or an id suffix that has no letters.
			where = append(where, "(t.number = "+arg(n)+" OR right(t.id::text, 6) = "+arg(qs)+")")
		case err == nil && n > 0 && (strings.HasPrefix(qs, "#") || n < 1_000_000):
			where = append(where, "t.number = "+arg(n))
		case err == nil && n > 0:
			where = append(where, "u.telegram_id = "+arg(n))
		case hexSuffixRe.MatchString(qs):
			where = append(where, "right(t.id::text, 6) = "+arg(strings.ToLower(qs)))
		case strings.HasPrefix(qs, "@"):
			_, _, like := search(qs)
			where = append(where, "lower(u.username) LIKE "+arg(like))
		default:
			_, _, like := search(qs)
			a := arg(like)
			where = append(where, "(lower(t.subject) LIKE "+a+" OR lower(COALESCE(u.username, '')) LIKE "+a+
				" OR EXISTS (SELECT 1 FROM core.ticket_messages x WHERE x.ticket_id = t.id AND lower(x.body) LIKE "+a+"))")
		}
	}
	sql := `SELECT ` + ticketCols + `, u.telegram_id, COALESCE(u.username, ''), u.status, u.language, u.bot_blocked_at IS NOT NULL,
			COALESCE(a.telegram_id, 0), COALESCE(a.username, ''),
			COALESCE(lm.kind, ''), COALESCE(left(lm.body, 200), ''), lm.file_id IS NOT NULL, lm.created_at,
			count(*) OVER ()
		FROM core.tickets t
		JOIN core.users u ON u.id = t.user_id
		LEFT JOIN core.users a ON a.id = t.assigned_to
		LEFT JOIN LATERAL (SELECT kind, body, file_id, created_at FROM core.ticket_messages
			WHERE ticket_id = t.id ORDER BY created_at DESC, id DESC LIMIT 1) lm ON true`
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	order := ` ORDER BY t.updated_at DESC, t.id DESC`
	if f.Status == "open" {
		order = ` ORDER BY t.last_customer_at ASC NULLS LAST, t.id`
	}
	sql += order + ` LIMIT ` + arg(p.Limit) + ` OFFSET ` + arg(p.Offset)
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()
	var (
		out   []TicketRow
		total int64
	)
	for rows.Next() {
		var r TicketRow
		t, err := scanTicket(rows, &r.TelegramID, &r.Username, &r.UserStatus, &r.Language, &r.BotBlocked, &r.AssigneeTG,
			&r.AssigneeUsername, &r.LastKind, &r.LastPreview, &r.LastHasFile, &r.LastAt, &total)
		if err != nil {
			return nil, 0, err
		}
		r.Ticket = *t
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// GetTicketRow is one ticket as the list shows it.
func (s *Store) GetTicketRow(ctx context.Context, q querier, id string) (*TicketRow, error) {
	list, _, err := s.listByID(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (s *Store) listByID(ctx context.Context, q querier, id string) ([]TicketRow, int64, error) {
	rows, err := q.Query(ctx, `SELECT `+ticketCols+`, u.telegram_id, COALESCE(u.username, ''), u.status, u.language, u.bot_blocked_at IS NOT NULL,
			COALESCE(a.telegram_id, 0), COALESCE(a.username, ''),
			COALESCE(lm.kind, ''), COALESCE(left(lm.body, 200), ''), lm.file_id IS NOT NULL, lm.created_at, 1::bigint
		FROM core.tickets t
		JOIN core.users u ON u.id = t.user_id
		LEFT JOIN core.users a ON a.id = t.assigned_to
		LEFT JOIN LATERAL (SELECT kind, body, file_id, created_at FROM core.ticket_messages
			WHERE ticket_id = t.id ORDER BY created_at DESC, id DESC LIMIT 1) lm ON true
		WHERE t.id = $1`, id)
	if err != nil {
		return nil, 0, fmt.Errorf("get ticket row: %w", err)
	}
	defer rows.Close()
	var out []TicketRow
	var total int64
	for rows.Next() {
		var r TicketRow
		t, err := scanTicket(rows, &r.TelegramID, &r.Username, &r.UserStatus, &r.Language, &r.BotBlocked, &r.AssigneeTG,
			&r.AssigneeUsername, &r.LastKind, &r.LastPreview, &r.LastHasFile, &r.LastAt, &total)
		if err != nil {
			return nil, 0, err
		}
		r.Ticket = *t
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// TicketCounts are the support inbox's figures.
type TicketCounts struct {
	Waiting, Mine, Unassigned, Answered int64
	OldestWaiting                       *time.Time
}

// CountTickets counts tickets for the inbox (Mine: assigned to actorID).
func (s *Store) CountTickets(ctx context.Context, q querier, actorID string) (*TicketCounts, error) {
	var c TicketCounts
	err := q.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE status = 'open'),
			count(*) FILTER (WHERE status <> 'closed' AND assigned_to = NULLIF($1, '')::uuid),
			count(*) FILTER (WHERE status <> 'closed' AND assigned_to IS NULL),
			count(*) FILTER (WHERE status = 'answered'),
			min(last_customer_at) FILTER (WHERE status = 'open')
		FROM core.tickets WHERE status <> 'closed'`, actorID).Scan(&c.Waiting, &c.Mine, &c.Unassigned, &c.Answered, &c.OldestWaiting)
	if err != nil {
		return nil, fmt.Errorf("count tickets: %w", err)
	}
	return &c, nil
}

// CountUserTickets counts a customer's tickets (the user page).
func (s *Store) CountUserTickets(ctx context.Context, q querier, userID string) (int64, error) {
	var n int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.tickets WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count user tickets: %w", err)
	}
	return n, nil
}

// StaleAnswered lists tickets answered more than `after` ago with no reply
// from the customer since (to close them).
func (s *Store) StaleAnswered(ctx context.Context, q querier, after time.Duration, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM core.tickets WHERE status = 'answered' AND last_staff_at < now() - $1::interval
		ORDER BY last_staff_at LIMIT $2`, after.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("stale tickets: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// AutoCloseTicket closes a ticket the system finds stale (as StaleAnswered
// lists them); false when the customer wrote or staff replied since.
func (s *Store) AutoCloseTicket(ctx context.Context, q querier, id string, after time.Duration) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.tickets SET status = 'closed', closed_at = now(), closed_by = NULL,
			close_reason = 'auto', updated_at = now()
		WHERE id = $1 AND status = 'answered' AND last_staff_at < now() - $2::interval`, id, after.String())
	if err != nil {
		return false, fmt.Errorf("auto-close ticket: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Canned is a saved reply.
type Canned struct {
	ID        string
	Title     string
	Body      map[string]string
	Sort      int
	UpdatedBy *string
	UpdatedAt time.Time
}

// ListCanned lists the saved replies in their order.
func (s *Store) ListCanned(ctx context.Context, q querier) ([]Canned, error) {
	rows, err := q.Query(ctx, `SELECT id, title, body, sort, updated_by, updated_at FROM core.ticket_canned ORDER BY sort, title`)
	if err != nil {
		return nil, fmt.Errorf("list canned: %w", err)
	}
	defer rows.Close()
	var out []Canned
	for rows.Next() {
		var c Canned
		var body []byte
		if err := rows.Scan(&c.ID, &c.Title, &body, &c.Sort, &c.UpdatedBy, &c.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(body, &c.Body)
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCanned loads one saved reply.
func (s *Store) GetCanned(ctx context.Context, q querier, id string) (*Canned, error) {
	var c Canned
	var body []byte
	err := q.QueryRow(ctx, `SELECT id, title, body, sort, updated_by, updated_at FROM core.ticket_canned WHERE id = $1`, id).
		Scan(&c.ID, &c.Title, &body, &c.Sort, &c.UpdatedBy, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get canned: %w", err)
	}
	_ = json.Unmarshal(body, &c.Body)
	return &c, nil
}

// SaveCanned creates (empty ID) or updates a saved reply.
func (s *Store) SaveCanned(ctx context.Context, q querier, c *Canned) error {
	body, err := json.Marshal(c.Body)
	if err != nil {
		return err
	}
	if c.ID == "" {
		c.ID = buuid.MustV7().String()
		_, err = q.Exec(ctx, `INSERT INTO core.ticket_canned (id, title, body, sort, updated_by) VALUES ($1, $2, $3, $4, $5)`,
			c.ID, c.Title, body, c.Sort, c.UpdatedBy)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = q.Exec(ctx, `UPDATE core.ticket_canned SET title = $2, body = $3, sort = $4, updated_by = $5, updated_at = now()
			WHERE id = $1`, c.ID, c.Title, body, c.Sort, c.UpdatedBy)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
	}
	if err != nil {
		return fmt.Errorf("save canned: %w", err)
	}
	return nil
}

// DeleteCanned removes a saved reply.
func (s *Store) DeleteCanned(ctx context.Context, q querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM core.ticket_canned WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete canned: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveStaff lists active staff (ticket assignees), owners first.
func (s *Store) ActiveStaff(ctx context.Context, q querier) ([]User, error) {
	rows, err := q.Query(ctx, `SELECT `+userCols+` FROM core.users WHERE status = 'active' AND role IN ('owner', 'admin', 'support')
		ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, created_at`)
	if err != nil {
		return nil, fmt.Errorf("active staff: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}
