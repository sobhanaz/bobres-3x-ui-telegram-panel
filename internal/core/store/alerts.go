package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Alert states (core.alert_state.state).
const (
	AlertOK      = "ok"
	AlertPending = "pending" // seen failing; not announced (yet)
	AlertProblem = "problem" // announced
)

// AlertState is one watchdog check's last known state.
type AlertState struct {
	Key        string
	State      string
	Since      time.Time  // when the check entered this state
	LastSentAt *time.Time // the last alert sent about it (nil: none)
	// PassingSince is when a problem first passed again (nil while it
	// fails): it is fixed once it has passed for a few minutes.
	PassingSince *time.Time
	Detail       string
	Severity     string
	TitleKey     string
	Args         map[string]string
}

const alertCols = `key, state, since, last_sent_at, passing_since, detail, severity, title_key, args`

func scanAlert(row pgx.Row) (*AlertState, error) {
	var (
		a    AlertState
		args []byte
	)
	if err := row.Scan(&a.Key, &a.State, &a.Since, &a.LastSentAt, &a.PassingSince, &a.Detail, &a.Severity, &a.TitleKey,
		&args); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(args, &a.Args); err != nil {
		return nil, fmt.Errorf("alert %s args: %w", a.Key, err)
	}
	return &a, nil
}

func (s *Store) listAlerts(ctx context.Context, q querier, sql string) ([]*AlertState, error) {
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("alert states: %w", err)
	}
	defer rows.Close()
	var out []*AlertState
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("alert states: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LockAlertStates reads every check's state and holds the rows until the
// transaction ends, so two watchdog runs cannot both announce a change.
func (s *Store) LockAlertStates(ctx context.Context, tx pgx.Tx) (map[string]*AlertState, error) {
	list, err := s.listAlerts(ctx, tx, `SELECT `+alertCols+` FROM core.alert_state ORDER BY key FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*AlertState, len(list))
	for _, a := range list {
		out[a.Key] = a
	}
	return out, nil
}

// OpenAlerts lists the announced problems, critical first, then the oldest.
func (s *Store) OpenAlerts(ctx context.Context, q querier) ([]*AlertState, error) {
	return s.listAlerts(ctx, q, `SELECT `+alertCols+` FROM core.alert_state WHERE state = 'problem'
		ORDER BY severity = 'critical' DESC, since, key`)
}

// PutAlertState writes a check's state.
func (s *Store) PutAlertState(ctx context.Context, q querier, a *AlertState) error {
	args := a.Args
	if args == nil {
		args = map[string]string{}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("put alert state: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO core.alert_state (`+alertCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (key) DO UPDATE SET state = EXCLUDED.state, since = EXCLUDED.since,
			last_sent_at = EXCLUDED.last_sent_at, passing_since = EXCLUDED.passing_since, detail = EXCLUDED.detail,
			severity = EXCLUDED.severity, title_key = EXCLUDED.title_key, args = EXCLUDED.args`,
		a.Key, a.State, a.Since, a.LastSentAt, a.PassingSince, a.Detail, a.Severity, a.TitleKey, raw)
	if err != nil {
		return fmt.Errorf("put alert state %s: %w", a.Key, err)
	}
	return nil
}

// DeleteAlertState forgets a check (a server removed, the backup service
// uninstalled).
func (s *Store) DeleteAlertState(ctx context.Context, q querier, key string) error {
	if _, err := q.Exec(ctx, `DELETE FROM core.alert_state WHERE key = $1`, key); err != nil {
		return fmt.Errorf("delete alert state %s: %w", key, err)
	}
	return nil
}

// RecentProvisionFailures counts the orders whose last provisioning attempt
// failed at or after since (a failing order is retried at least every 30
// minutes, so it stays counted until it is delivered or given up). An order
// being retried right now counts too: it is 'provisioning' for the whole
// panel call (up to half a minute when the panel is unreachable) and keeps
// its last error, which a first attempt does not have; leaving it out would
// call the problem fixed during every retry.
func (s *Store) RecentProvisionFailures(ctx context.Context, q querier, since time.Time) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT count(*) FROM core.orders
		WHERE (status = 'provision_failed' OR (status = 'provisioning' AND last_error IS NOT NULL)) AND updated_at >= $1`,
		since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("recent provision failures: %w", err)
	}
	return n, nil
}

// OldestUnconsumedEvent is when the oldest core event the consumer has not
// acknowledged was published; ok=false when it has read them all.
func (s *Store) OldestUnconsumedEvent(ctx context.Context, q querier, consumer string) (at time.Time, ok bool, err error) {
	err = q.QueryRow(ctx, `
		SELECT o.created_at FROM core.outbox_core o
		WHERE o.id > COALESCE((SELECT last_id FROM core.outbox_core_cursors WHERE consumer = $1), 0)
		ORDER BY o.id LIMIT 1`, consumer).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("oldest unconsumed event: %w", err)
	}
	return at, true, nil
}
