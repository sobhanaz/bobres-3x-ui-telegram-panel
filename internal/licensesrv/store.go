package licensesrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// ErrNotFound is returned when a license does not exist.
var ErrNotFound = errors.New("licensesrv: not found")

// License statuses.
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

// License is one sold license (its key is never kept, only its hash).
type License struct {
	ID           string
	Customer     string
	Tier         string
	Entitlements license.Entitlements
	EndsAt       *time.Time // nil: no end date
	GraceDays    int
	Status       string
	RevokedAt    *time.Time
	Reason       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Activation is where a license runs (or ran).
type Activation struct {
	ID            int64
	LicenseID     string
	InstallID     string
	Domain        string
	Counted       bool // a re-activation, counted against the limit
	ActivatedAt   time.Time
	LastSeenAt    time.Time
	LastVersion   string
	DeactivatedAt *time.Time
}

// AuditEntry is one row of the audit table.
type AuditEntry struct {
	At        time.Time
	Actor     string
	Action    string
	LicenseID string
	IP        string
	Details   map[string]any
}

// Store keeps licenses, activations and the audit trail in Postgres.
type Store struct {
	db *pgxpool.Pool
}

// querier is a pool or a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// OpenStore connects to dsn and checks the connection.
func OpenStore(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("licensesrv store: connect: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("licensesrv store: ping: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.db.Close() }

// Ping is the readiness check.
func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

// WithTx runs fn in a transaction, committing when it returns nil.
func (s *Store) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("licensesrv store: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("licensesrv store: commit: %w", err)
	}
	return nil
}

const licenseCols = `id, customer, tier, entitlements, ends_at, grace_days, status, revoked_at, coalesce(reason, ''), created_at, updated_at`

func scanLicense(row pgx.Row) (*License, error) {
	var l License
	var ent []byte
	err := row.Scan(&l.ID, &l.Customer, &l.Tier, &ent, &l.EndsAt, &l.GraceDays, &l.Status, &l.RevokedAt, &l.Reason, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(ent, &l.Entitlements); err != nil {
		return nil, fmt.Errorf("license %s: entitlements: %w", l.ID, err)
	}
	return &l, nil
}

// CreateLicense inserts a license with its key's hash.
func (s *Store) CreateLicense(ctx context.Context, q querier, l License, keyHash string) error {
	ent, err := json.Marshal(l.Entitlements)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO licensesrv.licenses (id, key_hash, customer, tier, entitlements, ends_at, grace_days, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, $8)`,
		l.ID, keyHash, l.Customer, l.Tier, ent, l.EndsAt, l.GraceDays, l.CreatedAt)
	if err != nil {
		return fmt.Errorf("create license: %w", err)
	}
	return nil
}

// LicenseByKeyHash finds the license a key opens. forUpdate locks its row
// until the transaction ends, so two activations of one key run one by one.
func (s *Store) LicenseByKeyHash(ctx context.Context, q querier, hash string, forUpdate bool) (*License, error) {
	sql := `SELECT ` + licenseCols + ` FROM licensesrv.licenses WHERE key_hash = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	l, err := scanLicense(q.QueryRow(ctx, sql, hash))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("license by key: %w", err)
	}
	return l, err
}

// LicenseByID finds a license by its id (forUpdate as LicenseByKeyHash).
func (s *Store) LicenseByID(ctx context.Context, q querier, id string, forUpdate bool) (*License, error) {
	sql := `SELECT ` + licenseCols + ` FROM licensesrv.licenses WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	l, err := scanLicense(q.QueryRow(ctx, sql, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("license %s: %w", id, err)
	}
	return l, err
}

// UpdateLicense saves a license's customer, tier, entitlements, end and
// grace (never its key or status).
func (s *Store) UpdateLicense(ctx context.Context, q querier, l License) error {
	ent, err := json.Marshal(l.Entitlements)
	if err != nil {
		return err
	}
	tag, err := q.Exec(ctx, `
		UPDATE licensesrv.licenses
		SET customer = $2, tier = $3, entitlements = $4, ends_at = $5, grace_days = $6, updated_at = $7
		WHERE id = $1`,
		l.ID, l.Customer, l.Tier, ent, l.EndsAt, l.GraceDays, l.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update license: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeLicense marks a license revoked.
func (s *Store) RevokeLicense(ctx context.Context, q querier, id, reason string, at time.Time) error {
	tag, err := q.Exec(ctx, `
		UPDATE licensesrv.licenses SET status = 'revoked', revoked_at = $2, reason = $3, updated_at = $2
		WHERE id = $1`, id, at, reason)
	if err != nil {
		return fmt.Errorf("revoke license: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LicenseSummary is a license with where it runs, for `key list`.
type LicenseSummary struct {
	License
	ActiveDomain string     // "" when not activated
	LastSeenAt   *time.Time // the active activation's
	Moves        int        // counted re-activations since the window start
}

// ListLicenses lists licenses, newest first; status filters when not "".
// Moves counts re-activations made after since.
func (s *Store) ListLicenses(ctx context.Context, q querier, status string, since time.Time) ([]LicenseSummary, error) {
	rows, err := q.Query(ctx, `
		SELECT l.id, l.customer, l.tier, l.entitlements, l.ends_at, l.grace_days, l.status, l.revoked_at,
		       coalesce(l.reason, ''), l.created_at, l.updated_at,
		       coalesce(a.domain, ''), a.last_seen_at,
		       (SELECT count(*) FROM licensesrv.activations c WHERE c.license_id = l.id AND c.counted AND c.activated_at > $2)
		FROM licensesrv.licenses l
		LEFT JOIN licensesrv.activations a ON a.license_id = l.id AND a.deactivated_at IS NULL
		WHERE $1 = '' OR l.status = $1
		ORDER BY l.created_at DESC, l.id`, status, since)
	if err != nil {
		return nil, fmt.Errorf("list licenses: %w", err)
	}
	defer rows.Close()
	var out []LicenseSummary
	for rows.Next() {
		var ls LicenseSummary
		var ent []byte
		if err := rows.Scan(&ls.ID, &ls.Customer, &ls.Tier, &ent, &ls.EndsAt, &ls.GraceDays, &ls.Status, &ls.RevokedAt,
			&ls.Reason, &ls.CreatedAt, &ls.UpdatedAt, &ls.ActiveDomain, &ls.LastSeenAt, &ls.Moves); err != nil {
			return nil, fmt.Errorf("list licenses: %w", err)
		}
		if err := json.Unmarshal(ent, &ls.Entitlements); err != nil {
			return nil, fmt.Errorf("license %s: entitlements: %w", ls.ID, err)
		}
		out = append(out, ls)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list licenses: %w", err)
	}
	return out, nil
}

const activationCols = `id, license_id, install_id::text, domain, counted, activated_at, last_seen_at, last_version, deactivated_at`

func scanActivation(row pgx.Row) (*Activation, error) {
	var a Activation
	err := row.Scan(&a.ID, &a.LicenseID, &a.InstallID, &a.Domain, &a.Counted, &a.ActivatedAt, &a.LastSeenAt, &a.LastVersion, &a.DeactivatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ActiveActivation is the license's active activation, or nil when none.
func (s *Store) ActiveActivation(ctx context.Context, q querier, licenseID string) (*Activation, error) {
	a, err := scanActivation(q.QueryRow(ctx, `
		SELECT `+activationCols+` FROM licensesrv.activations
		WHERE license_id = $1 AND deactivated_at IS NULL`, licenseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active activation: %w", err)
	}
	return a, nil
}

// HasActivations reports whether the license was ever activated.
func (s *Store) HasActivations(ctx context.Context, q querier, licenseID string) (bool, error) {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM licensesrv.activations WHERE license_id = $1)`, licenseID).Scan(&ok); err != nil {
		return false, fmt.Errorf("has activations: %w", err)
	}
	return ok, nil
}

// CountedSince counts the re-activations made after since, and returns the
// oldest of them (nil when none): the limit frees up a year after it.
func (s *Store) CountedSince(ctx context.Context, q querier, licenseID string, since time.Time) (int, *time.Time, error) {
	var n int
	var oldest *time.Time
	if err := q.QueryRow(ctx, `
		SELECT count(*), min(activated_at) FROM licensesrv.activations
		WHERE license_id = $1 AND counted AND activated_at > $2`, licenseID, since).Scan(&n, &oldest); err != nil {
		return 0, nil, fmt.Errorf("count re-activations: %w", err)
	}
	return n, oldest, nil
}

// InsertActivation adds an active activation.
func (s *Store) InsertActivation(ctx context.Context, q querier, a Activation) error {
	_, err := q.Exec(ctx, `
		INSERT INTO licensesrv.activations (license_id, install_id, domain, counted, activated_at, last_seen_at, last_version)
		VALUES ($1, $2, $3, $4, $5, $5, $6)`,
		a.LicenseID, a.InstallID, a.Domain, a.Counted, a.ActivatedAt, a.LastVersion)
	if err != nil {
		return fmt.Errorf("insert activation: %w", err)
	}
	return nil
}

// Deactivate ends an activation (never before it began, should the clock
// have stepped back).
func (s *Store) Deactivate(ctx context.Context, q querier, id int64, at time.Time) error {
	if _, err := q.Exec(ctx, `
		UPDATE licensesrv.activations SET deactivated_at = greatest(activated_at, $2)
		WHERE id = $1 AND deactivated_at IS NULL`, id, at); err != nil {
		return fmt.Errorf("deactivate: %w", err)
	}
	return nil
}

// TouchActivation records a refresh: when, and the version when one was sent.
func (s *Store) TouchActivation(ctx context.Context, q querier, id int64, at time.Time, version string) error {
	if _, err := q.Exec(ctx, `
		UPDATE licensesrv.activations
		SET last_seen_at = greatest(last_seen_at, $2), last_version = CASE WHEN $3 = '' THEN last_version ELSE $3 END
		WHERE id = $1`, id, at, version); err != nil {
		return fmt.Errorf("touch activation: %w", err)
	}
	return nil
}

// Activations lists a license's activations, newest first.
func (s *Store) Activations(ctx context.Context, q querier, licenseID string, limit int) ([]Activation, error) {
	rows, err := q.Query(ctx, `
		SELECT `+activationCols+` FROM licensesrv.activations
		WHERE license_id = $1 ORDER BY activated_at DESC, id DESC LIMIT $2`, licenseID, limit)
	if err != nil {
		return nil, fmt.Errorf("activations: %w", err)
	}
	defer rows.Close()
	var out []Activation
	for rows.Next() {
		a, err := scanActivation(rows)
		if err != nil {
			return nil, fmt.Errorf("activations: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("activations: %w", err)
	}
	return out, nil
}

// AddAudit appends an audit row.
func (s *Store) AddAudit(ctx context.Context, q querier, e AuditEntry) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var lic *string
	if e.LicenseID != "" {
		lic = &e.LicenseID
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO licensesrv.audit (at, actor, action, license_id, ip, details) VALUES ($1, $2, $3, $4, $5, $6)`,
		e.At, e.Actor, e.Action, lic, e.IP, raw); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return nil
}

// Audit lists a license's audit rows, newest first.
func (s *Store) Audit(ctx context.Context, q querier, licenseID string, limit int) ([]AuditEntry, error) {
	rows, err := q.Query(ctx, `
		SELECT at, actor, action, coalesce(license_id, ''), ip, details FROM licensesrv.audit
		WHERE license_id = $1 ORDER BY at DESC, id DESC LIMIT $2`, licenseID, limit)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var raw []byte
		if err := rows.Scan(&e.At, &e.Actor, &e.Action, &e.LicenseID, &e.IP, &raw); err != nil {
			return nil, fmt.Errorf("audit: %w", err)
		}
		if err := json.Unmarshal(raw, &e.Details); err != nil {
			return nil, fmt.Errorf("audit: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	return out, nil
}
