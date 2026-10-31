package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// InstallID is this install's id (core.install), made when the schema was
// created; it is made again here if the row is ever missing.
func (s *Store) InstallID(ctx context.Context, q querier) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id::text FROM core.install`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = q.QueryRow(ctx, `
			WITH made AS (INSERT INTO core.install DEFAULT VALUES ON CONFLICT DO NOTHING RETURNING id)
			SELECT id::text FROM made UNION ALL SELECT id::text FROM core.install LIMIT 1`).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			// Another caller inserted the row at the same moment: the insert above did nothing, and the
			// statement's snapshot is older than that row. A new statement sees it.
			err = q.QueryRow(ctx, `SELECT id::text FROM core.install`).Scan(&id)
		}
	}
	if err != nil {
		return "", fmt.Errorf("install id: %w", err)
	}
	return id, nil
}

// TrialStartedAt is when this install's implicit trial began: the first
// start of a version with the license policy, written once by migration
// 00017 (core.install.trial_started_at).
func (s *Store) TrialStartedAt(ctx context.Context, q querier) (time.Time, error) {
	var t time.Time
	err := q.QueryRow(ctx, `SELECT trial_started_at FROM core.install`).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		// The row is made again, as InstallID does, with the trial starting now.
		if _, err = s.InstallID(ctx, q); err == nil {
			err = q.QueryRow(ctx, `SELECT trial_started_at FROM core.install`).Scan(&t)
		}
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("trial start: %w", err)
	}
	return t, nil
}

// NoteLimitHit records that a new purchase was refused at at because the
// install is at its active-services limit. A refusal within a minute of the
// last one is not written again (many customers may be turned away at once).
func (s *Store) NoteLimitHit(ctx context.Context, q querier, at time.Time) error {
	_, err := q.Exec(ctx, `UPDATE core.install SET limit_hit_at = $1::timestamptz
		WHERE limit_hit_at IS NULL OR limit_hit_at < $1::timestamptz - interval '1 minute'`, at)
	if err != nil {
		return fmt.Errorf("note limit hit: %w", err)
	}
	return nil
}

// LimitHitAt is when a new purchase was last refused for the active-services
// limit (nil: never).
func (s *Store) LimitHitAt(ctx context.Context, q querier) (*time.Time, error) {
	var t *time.Time
	if err := q.QueryRow(ctx, `SELECT limit_hit_at FROM core.install`).Scan(&t); err != nil {
		return nil, fmt.Errorf("limit hit: %w", err)
	}
	return t, nil
}

// How the license token in use came (core.license.source).
const (
	// LicenseManual: pasted in the dashboard or imported with the CLI.
	LicenseManual = "manual"
	// LicenseActivation: fetched from the vendor's license server with a
	// license key; only such a license is refreshed.
	LicenseActivation = "activation"
	// LicenseRefresh: renewed by the daily refresh (of an activated license).
	LicenseRefresh = "refresh"
)

// LicenseRow is the stored license token and who installed it.
type LicenseRow struct {
	Token     string
	UpdatedAt time.Time
	UpdatedBy *string // user id; nil when installed from the CLI
	// UpdatedByName is that user's Telegram username ("" when none).
	UpdatedByName string
	// Source is how the token in use came (LicenseManual, LicenseActivation
	// or LicenseRefresh).
	Source string
	// RefreshedAt is when the daily refresh installed it (nil unless Source
	// is LicenseRefresh).
	RefreshedAt *time.Time
}

// GetLicense returns the stored license token (ErrNotFound when none).
func (s *Store) GetLicense(ctx context.Context, q querier) (*LicenseRow, error) {
	var r LicenseRow
	err := q.QueryRow(ctx, `
		SELECT l.token, l.updated_at, l.updated_by::text, coalesce(u.username, ''), l.source, l.refreshed_at
		FROM core.license l LEFT JOIN core.users u ON u.id = l.updated_by WHERE l.id = 1`).
		Scan(&r.Token, &r.UpdatedAt, &r.UpdatedBy, &r.UpdatedByName, &r.Source, &r.RefreshedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get license: %w", err)
	}
	return &r, nil
}

// SetLicense stores the license token, replacing any other. updatedBy is the
// staff member's id, or nil from the CLI; source is how the token came
// (LicenseManual or LicenseActivation). It has not been refreshed.
func (s *Store) SetLicense(ctx context.Context, q querier, token string, updatedBy *string, source string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.license (id, token, updated_at, updated_by, source, refreshed_at) VALUES (1, $1, now(), $2, $3, NULL)
		ON CONFLICT (id) DO UPDATE SET token = EXCLUDED.token, updated_at = now(), updated_by = EXCLUDED.updated_by,
			source = EXCLUDED.source, refreshed_at = NULL`,
		token, updatedBy, source)
	if err != nil {
		return fmt.Errorf("set license: %w", err)
	}
	return nil
}

// RefreshLicense replaces the stored token old with its renewal (source
// LicenseRefresh) and notes when, keeping who installed the license. false:
// the stored token is no longer old (someone installed another meanwhile),
// nothing changed.
func (s *Store) RefreshLicense(ctx context.Context, q querier, old, token string) (bool, error) {
	tag, err := q.Exec(ctx, `
		UPDATE core.license SET token = $2, source = 'refresh', refreshed_at = now() WHERE id = 1 AND token = $1`, old, token)
	if err != nil {
		return false, fmt.Errorf("refresh license: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CountStaffSeats counts the staff a license's dashboard seats cover: every
// admin, support member and owner except the main owner the server names
// (ownerTelegramID, the first of BOBRES_ADMIN_TELEGRAM_ID; 0 when it names
// none, which leaves no one out). Another owner, a second root admin
// included, is a seat like any other, or promoting the two seat holders to
// owner would empty the seats.
func (s *Store) CountStaffSeats(ctx context.Context, q querier, ownerTelegramID int64) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.users
		WHERE role IN ('admin', 'support', 'owner') AND telegram_id <> $1`, ownerTelegramID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count staff seats: %w", err)
	}
	return n, nil
}

// ServiceSlotsLockKey is the advisory lock that serializes the check of the
// active-services limit with the insert of the order it lets through: it
// only has to differ from the other advisory locks of the database (the
// migrator's, HTTPS', the outboxes').
const serviceSlotsLockKey int64 = 0x736c6f74 // "slot"

// LockServiceSlots takes the transaction-scoped advisory lock that makes a
// "count the services, then insert the order" pair atomic across
// connections: the next transaction to ask waits for this one to end, and
// then counts the order this one inserted.
func (s *Store) LockServiceSlots(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, serviceSlotsLockKey); err != nil {
		return fmt.Errorf("lock service slots: %w", err)
	}
	return nil
}

// UnpaidOrderSlotHold is how long an unpaid new order keeps a place against
// max_active_services. Unpaid orders never expire, so counting all of them
// would pin places forever; a customer who has not paid within this time
// holds none (paying later is still honored: the order is then counted as a
// paid one).
const UnpaidOrderSlotHold = 2 * time.Hour

// CountActiveServices counts the services that take a place against a
// license's max_active_services: those customers have now (active or about
// to expire) and those on their way, so a check made before payment cannot
// be overshot by orders made together:
//   - subscriptions being delivered (pending);
//   - new orders paid or being delivered that have no subscription yet;
//   - new orders awaiting payment made within UnpaidOrderSlotHold.
//
// A renewal or a top-up is never a place of its own. Call it under
// LockServiceSlots when the answer decides whether to insert an order.
func (s *Store) CountActiveServices(ctx context.Context, q querier) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM core.subscriptions WHERE status IN ('active', 'expiring_soon', 'pending'))
			+ (SELECT count(*) FROM core.orders o
				WHERE o.type = 'new' AND (
					(o.status IN ('paid', 'provisioning')
						AND NOT EXISTS (SELECT 1 FROM core.subscriptions s WHERE s.order_id = o.id))
					OR (o.status IN ('created', 'awaiting_payment') AND o.created_at > now() - make_interval(secs => $1))))`,
		UnpaidOrderSlotHold.Seconds()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active services: %w", err)
	}
	return n, nil
}

// ClampTrialStart moves a trial start that lies after now back to now (a
// clock that was set back, a restore from a machine that ran ahead), so the
// trial really ends TrialDays after the first look at it. An earlier start
// is left alone: the trial never starts over.
func (s *Store) ClampTrialStart(ctx context.Context, q querier, now time.Time) error {
	if _, err := q.Exec(ctx, `UPDATE core.install SET trial_started_at = $1::timestamptz WHERE trial_started_at > $1::timestamptz`, now); err != nil {
		return fmt.Errorf("clamp trial start: %w", err)
	}
	return nil
}
