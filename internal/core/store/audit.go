package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
)

type (
	clientIPKey struct{}
	sourceKey   struct{}
)

// Where a change was made (audit_log.source).
const (
	SourceDashboard = "dashboard"
	SourceBot       = "bot"
	SourceCLI       = "cli"
	SourceSystem    = "system"
)

// WithSource records where the changes made with ctx come from (one of the
// Source* values); entries written without one say "system".
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

func sourceOf(ctx context.Context) string {
	switch s, _ := ctx.Value(sourceKey{}).(string); s {
	case SourceDashboard, SourceBot, SourceCLI:
		return s
	}
	return SourceSystem
}

// WithClientIP records the address a dashboard request came from, so the
// audit entries it writes say where a change was made.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// clientIP is the context's address when it is a valid IP (the column is inet).
func clientIP(ctx context.Context) *string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	if net.ParseIP(ip) == nil {
		return nil
	}
	return &ip
}

// AuditFilter narrows the audit log. Action matches one action
// ("wallet.adjust") or a group ("subscription" matches subscription.*).
type AuditFilter struct {
	ActorID, Action, Entity, EntityID string
	From, To                          *time.Time
}

// AuditRow is one audit entry with who made it.
type AuditRow struct {
	ID              string
	ActorID         *string
	ActorTelegramID int64
	ActorUsername   string
	ActorRole       string
	Action          string
	Entity          string
	EntityID        *string
	Before, After   json.RawMessage
	Reason          string
	IP              string
	Source          string // "" for entries older than the column
	CreatedAt       time.Time
}

const auditWhere = `
		WHERE ($1 = '' OR a.actor_id = NULLIF($1, '')::uuid)
		  AND ($2 = '' OR a.action = $2 OR left(a.action, length($2) + 1) = $2 || '.')
		  AND ($3 = '' OR a.entity = $3)
		  AND ($4 = '' OR a.entity_id = $4)
		  AND ($5::timestamptz IS NULL OR a.created_at >= $5)
		  AND ($6::timestamptz IS NULL OR a.created_at < $6)`

const auditCols = `a.id, a.actor_id, COALESCE(u.telegram_id, 0), COALESCE(u.username, ''), COALESCE(u.role, ''),
	a.action, a.entity, a.entity_id, a.before, a.after, COALESCE(a.reason, ''), COALESCE(host(a.ip), ''),
	COALESCE(a.source, ''), a.created_at`

func scanAudit(rows pgx.Rows, extra ...any) (AuditRow, error) {
	var (
		r             AuditRow
		before, after []byte
	)
	dest := append([]any{&r.ID, &r.ActorID, &r.ActorTelegramID, &r.ActorUsername, &r.ActorRole,
		&r.Action, &r.Entity, &r.EntityID, &before, &after, &r.Reason, &r.IP, &r.Source, &r.CreatedAt}, extra...)
	if err := rows.Scan(dest...); err != nil {
		return r, err
	}
	r.Before, r.After = before, after
	return r, nil
}

// ListAudit returns a page of the audit log, newest first, and the number of matches.
func (s *Store) ListAudit(ctx context.Context, q querier, f AuditFilter, p Page) ([]AuditRow, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `
		SELECT `+auditCols+`, count(*) OVER ()
		FROM core.audit_log a LEFT JOIN core.users u ON u.id = a.actor_id`+auditWhere+`
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $7 OFFSET $8`, f.ActorID, f.Action, f.Entity, f.EntityID, f.From, f.To, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()
	var (
		out   []AuditRow
		total int64
	)
	for rows.Next() {
		r, err := scanAudit(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// EachAudit calls fn for every matching entry, newest first, up to max
// entries; it reports whether more matched than max.
func (s *Store) EachAudit(ctx context.Context, q querier, f AuditFilter, max int, fn func(AuditRow) error) (bool, error) {
	rows, err := q.Query(ctx, `
		SELECT `+auditCols+`
		FROM core.audit_log a LEFT JOIN core.users u ON u.id = a.actor_id`+auditWhere+`
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $7`, f.ActorID, f.Action, f.Entity, f.EntityID, f.From, f.To, max+1)
	if err != nil {
		return false, fmt.Errorf("each audit: %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		if n == max {
			return true, nil
		}
		r, err := scanAudit(rows)
		if err != nil {
			return false, err
		}
		if err := fn(r); err != nil {
			return false, err
		}
		n++
	}
	return false, rows.Err()
}

// AuditActions lists the actions recorded so far with how often (the
// dashboard's action filter).
func (s *Store) AuditActions(ctx context.Context, q querier) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT action, count(*) FROM core.audit_log GROUP BY action`)
	if err != nil {
		return nil, fmt.Errorf("audit actions: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			a string
			n int64
		)
		if err := rows.Scan(&a, &n); err != nil {
			return nil, err
		}
		out[a] = n
	}
	return out, rows.Err()
}
