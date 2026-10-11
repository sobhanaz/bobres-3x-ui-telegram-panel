// Package store persists provisioner state: 3x-ui servers (tokens encrypted
// with the envelope), provisioning jobs, and the subscription→panel client map.
package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// ErrNotFound is returned by Get* when no row matches.
var ErrNotFound = errors.New("store: not found")

// ErrNameTaken is returned when another server already has the name.
var ErrNameTaken = errors.New("store: server name taken")

// ErrServerInUse is returned when deleting a server that clients map to.
var ErrServerInUse = errors.New("store: server has clients")

// ErrEmailTaken is returned when another subscription already holds the
// email on that server.
var ErrEmailTaken = errors.New("store: email taken on the server")

// ErrTokenUnreadable is returned when a stored token does not decrypt (the
// master key changed).
var ErrTokenUnreadable = errors.New("store: server token cannot be decrypted")

// Who manages a server row.
const (
	ManagedEnv       = "env"       // seeded from the installer's environment
	ManagedDashboard = "dashboard" // added or edited in the dashboard
)

// DefaultWeight is a server's share of new clients unless set.
const DefaultWeight = 100

// Store wraps a pool for the provisioner schema.
type Store struct {
	db  *pgxpool.Pool
	env *bcrypto.Envelope
}

// Server mirrors provisioner.xui_servers. Token is plaintext only in memory.
type Server struct {
	ID           string
	Name         string
	BaseURL      string
	Token        string
	PanelVersion string
	Enabled      bool
	// SubBaseURL is the public prefix of the panel's subscription server
	// (subscription link = SubBaseURL + subId); empty when not configured.
	SubBaseURL string
	// AllowPrivate: the operator opted in to a panel on a private address.
	AllowPrivate bool
	Weight       int32
	Managed      string
	// The last health check: HealthOK is nil until the first one.
	HealthOK    *bool
	HealthError string
	CheckedAt   *time.Time
	UpdatedAt   time.Time
}

// ServerInfo is a server as listed: the token is never decrypted for it.
type ServerInfo struct {
	Server
	HasToken bool
	Clients  int64 // client_map rows, reservations included
}

// Job mirrors provisioner.provision_jobs.
type Job struct {
	ID             string
	SubscriptionID *string
	Action         string
	Status         string
	Attempts       int
}

// ClientMap mirrors provisioner.client_map.
type ClientMap struct {
	SubscriptionID string
	ServerID       string
	Email          string
	XUISubID       *string
	InboundIDs     []int32
	// Pending: reserved before the panel was called; the panel may or may
	// not have the client yet.
	Pending bool
}

// New opens the pool and keeps the envelope for token encryption.
func New(ctx context.Context, dsn string, env *bcrypto.Envelope) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("provisioner store: connect: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("provisioner store: ping: %w", err)
	}
	return &Store{db: db, env: env}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.db.Close() }

// DB exposes the pool for readiness probes and tests.
func (s *Store) DB() *pgxpool.Pool { return s.db }

// SubIDFor derives the 3x-ui subscription id for one of our subscriptions:
// stable (so a retried create reuses it), unguessable without the master key
// (the panel serves configs to anyone who knows a subId), and collision-free.
func (s *Store) SubIDFor(subscriptionID string) string {
	return hex.EncodeToString(s.env.Tag([]byte("xui-sub-id:" + subscriptionID)))[:32]
}

func pgCode(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && (constraint == "" || pgErr.ConstraintName == constraint)
}

// AddServer encrypts the token and inserts the server row (weight
// DefaultWeight and managed ManagedEnv unless set).
func (s *Store) AddServer(ctx context.Context, sv *Server) (*Server, error) {
	enc, err := s.env.Encrypt([]byte(sv.Token))
	if err != nil {
		return nil, err
	}
	if sv.Weight == 0 {
		sv.Weight = DefaultWeight
	}
	if sv.Managed == "" {
		sv.Managed = ManagedEnv
	}
	sv.ID = buuid.MustV7().String()
	err = s.db.QueryRow(ctx, `
		INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, enabled, sub_base_url, allow_private, weight, managed)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9) RETURNING updated_at`,
		sv.ID, sv.Name, sv.BaseURL, enc, sv.Enabled, sv.SubBaseURL, sv.AllowPrivate, sv.Weight, sv.Managed).Scan(&sv.UpdatedAt)
	if pgCode(err, "23505", "xui_servers_name_key") {
		return nil, ErrNameTaken
	}
	if err != nil {
		return nil, fmt.Errorf("add server: %w", err)
	}
	return sv, nil
}

// CountServers returns how many panels are registered.
func (s *Store) CountServers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM provisioner.xui_servers`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count servers: %w", err)
	}
	return n, nil
}

const serverCols = `s.id, s.name, s.base_url, s.api_token_enc, s.panel_version, s.enabled, s.sub_base_url, s.allow_private,
	s.weight, s.managed, s.last_health_ok, s.last_error, s.last_checked_at, s.updated_at`

// scanServerRow reads serverCols (then extra) without decrypting the token.
func scanServerRow(row pgx.Row, extra ...any) (*Server, []byte, error) {
	var (
		sv       Server
		enc      []byte
		ver, sub *string
	)
	dest := append([]any{&sv.ID, &sv.Name, &sv.BaseURL, &enc, &ver, &sv.Enabled, &sub, &sv.AllowPrivate,
		&sv.Weight, &sv.Managed, &sv.HealthOK, &sv.HealthError, &sv.CheckedAt, &sv.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if ver != nil {
		sv.PanelVersion = *ver
	}
	if sub != nil {
		sv.SubBaseURL = *sub
	}
	return &sv, enc, nil
}

func (s *Store) scanServer(row pgx.Row) (*Server, error) {
	sv, enc, err := scanServerRow(row)
	if err != nil {
		return nil, err
	}
	tok, err := s.env.Decrypt(enc)
	if err != nil {
		return nil, ErrTokenUnreadable
	}
	sv.Token = string(tok)
	return sv, nil
}

// FirstServer returns the first enabled server (calls that name no server).
func (s *Store) FirstServer(ctx context.Context) (*Server, error) {
	sv, err := s.scanServer(s.db.QueryRow(ctx,
		`SELECT `+serverCols+` FROM provisioner.xui_servers s WHERE s.enabled ORDER BY s.created_at LIMIT 1`))
	if err != nil {
		return nil, fmt.Errorf("first server: %w", err)
	}
	return sv, nil
}

// ServerByName returns the server with that name (ErrNotFound if none).
func (s *Store) ServerByName(ctx context.Context, name string) (*Server, error) {
	sv, err := s.scanServer(s.db.QueryRow(ctx, `SELECT `+serverCols+` FROM provisioner.xui_servers s WHERE s.name = $1`, name))
	if err != nil {
		return nil, fmt.Errorf("server by name: %w", err)
	}
	return sv, nil
}

// GetServer returns one server row by id, decrypting its token. It ignores
// enabled: existing clients keep working on a disabled server.
func (s *Store) GetServer(ctx context.Context, id string) (*Server, error) {
	sv, err := s.scanServer(s.db.QueryRow(ctx, `SELECT `+serverCols+` FROM provisioner.xui_servers s WHERE s.id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("get server: %w", err)
	}
	return sv, nil
}

const infoQuery = `SELECT ` + serverCols + `, length(s.api_token_enc) > 0,
	(SELECT count(*) FROM provisioner.client_map c WHERE c.server_id = s.id)
	FROM provisioner.xui_servers s`

func scanInfo(row pgx.Row) (*ServerInfo, error) {
	var in ServerInfo
	sv, _, err := scanServerRow(row, &in.HasToken, &in.Clients)
	if err != nil {
		return nil, err
	}
	in.Server = *sv
	return &in, nil
}

// ListServers lists every server, oldest first, with its client count.
func (s *Store) ListServers(ctx context.Context) ([]*ServerInfo, error) {
	rows, err := s.db.Query(ctx, infoQuery+` ORDER BY s.created_at, s.id`)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()
	var out []*ServerInfo
	for rows.Next() {
		in, err := scanInfo(rows)
		if err != nil {
			return nil, fmt.Errorf("list servers: %w", err)
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	return out, nil
}

// ServerInfo returns one server as listed.
func (s *Store) ServerInfo(ctx context.Context, id string) (*ServerInfo, error) {
	in, err := scanInfo(s.db.QueryRow(ctx, infoQuery+` WHERE s.id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("server info: %w", err)
	}
	return in, nil
}

// UpdateServer replaces a server's settings and token (re-encrypted) and
// marks it dashboard-managed. updated_at moves, so cached panel clients are
// rebuilt. connChanged clears the last health check, which was about the
// old connection.
func (s *Store) UpdateServer(ctx context.Context, sv *Server, connChanged bool) error {
	enc, err := s.env.Encrypt([]byte(sv.Token))
	if err != nil {
		return err
	}
	err = s.db.QueryRow(ctx, `
		UPDATE provisioner.xui_servers SET
			name = $2, base_url = $3, api_token_enc = $4, enabled = $5, sub_base_url = NULLIF($6, ''),
			allow_private = $7, weight = $8, managed = 'dashboard', updated_at = now(),
			last_health_ok = CASE WHEN $9 THEN NULL ELSE last_health_ok END,
			last_error = CASE WHEN $9 THEN '' ELSE last_error END,
			last_checked_at = CASE WHEN $9 THEN NULL ELSE last_checked_at END
		WHERE id = $1 RETURNING updated_at`,
		sv.ID, sv.Name, sv.BaseURL, enc, sv.Enabled, sv.SubBaseURL, sv.AllowPrivate, sv.Weight, connChanged).Scan(&sv.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case pgCode(err, "23505", "xui_servers_name_key"):
		return ErrNameTaken
	case err != nil:
		return fmt.Errorf("update server: %w", err)
	}
	sv.Managed = ManagedDashboard
	return nil
}

// DeleteServer removes a server no client maps to (its inbound settings go
// with it). It reports the client count when refusing with ErrServerInUse.
func (s *Store) DeleteServer(ctx context.Context, id string) (int64, error) {
	var (
		clients int64
		deleted bool
	)
	err := s.db.QueryRow(ctx, `
		WITH n AS (SELECT count(*) AS c FROM provisioner.client_map WHERE server_id = $1),
		d AS (DELETE FROM provisioner.xui_servers WHERE id = $1 AND (SELECT c FROM n) = 0 RETURNING id)
		SELECT (SELECT c FROM n), EXISTS (SELECT 1 FROM d)`, id).Scan(&clients, &deleted)
	switch {
	case pgCode(err, "23503", ""): // a client was mapped meanwhile
		return 0, ErrServerInUse
	case err != nil:
		return 0, fmt.Errorf("delete server: %w", err)
	case clients > 0:
		return clients, ErrServerInUse
	case !deleted:
		return 0, ErrNotFound
	}
	return 0, nil
}

// RecordHealth stores a health check of the server as it was at version
// (its updated_at): a check that raced an edit is dropped. updated_at does
// not move, so cached panel clients stay.
func (s *Store) RecordHealth(ctx context.Context, id string, version time.Time, ok bool, panelVersion, detail string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE provisioner.xui_servers SET
			last_health_ok = $3, last_error = $4, last_checked_at = now(),
			panel_version = CASE WHEN $3 THEN $5 ELSE panel_version END,
			last_health_at = CASE WHEN $3 THEN now() ELSE last_health_at END
		WHERE id = $1 AND updated_at = $2`, id, version, ok, detail, panelVersion)
	if err != nil {
		return fmt.Errorf("record health: %w", err)
	}
	return nil
}

// PickServer returns the server a new client goes to: enabled, not failing
// its last health check, among candidates (empty = any), with the fewest
// clients per weight (reservations count), oldest first on a tie.
func (s *Store) PickServer(ctx context.Context, candidates []string) (*Server, error) {
	sv, err := s.scanServer(s.db.QueryRow(ctx, `
		SELECT `+serverCols+` FROM provisioner.xui_servers s
		WHERE s.enabled AND s.last_health_ok IS NOT FALSE
			AND (coalesce(cardinality($1::text[]), 0) = 0 OR s.id::text = ANY ($1::text[]))
		ORDER BY (SELECT count(*) FROM provisioner.client_map c WHERE c.server_id = s.id)::float8 / s.weight,
			s.created_at, s.id
		LIMIT 1`, candidates))
	if err != nil {
		return nil, fmt.Errorf("pick server: %w", err)
	}
	return sv, nil
}

// EnqueueJob inserts a pending provision job.
func (s *Store) EnqueueJob(ctx context.Context, j *Job) error {
	j.ID = buuid.MustV7().String()
	_, err := s.db.Exec(ctx, `
		INSERT INTO provisioner.provision_jobs (id, subscription_id, action, status)
		VALUES ($1, $2, $3, 'pending')`, j.ID, j.SubscriptionID, j.Action)
	if err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}
	return nil
}

// ReserveClient records, before the panel is called, which server a new
// client goes to (pending, no inbounds yet), committed on its own. When the
// subscription already has a row (an earlier attempt, or a concurrent one),
// that row is returned instead and created is false.
func (s *Store) ReserveClient(ctx context.Context, cm *ClientMap) (*ClientMap, bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO provisioner.client_map (subscription_id, server_id, email, xui_sub_id, inbound_ids, pending)
		VALUES ($1, $2, $3, $4, '{}', true)
		ON CONFLICT (subscription_id) DO NOTHING`,
		cm.SubscriptionID, cm.ServerID, cm.Email, cm.XUISubID)
	switch {
	case pgCode(err, "23505", "client_map_server_email_idx"):
		// A concurrent attempt for this subscription can surface here
		// instead of as the conflict on its id.
		if existing, gerr := s.GetClientMap(ctx, cm.SubscriptionID); gerr == nil {
			return existing, false, nil
		}
		return nil, false, ErrEmailTaken
	case pgCode(err, "23503", ""): // the server was deleted meanwhile
		return nil, false, ErrNotFound
	case err != nil:
		return nil, false, fmt.Errorf("reserve client: %w", err)
	case tag.RowsAffected() == 1:
		r := *cm
		r.InboundIDs, r.Pending = []int32{}, true
		return &r, true, nil
	}
	existing, err := s.GetClientMap(ctx, cm.SubscriptionID)
	if err != nil {
		return nil, false, err
	}
	return existing, false, nil
}

// ReleaseReservation drops a reservation (never a delivered client's row).
func (s *Store) ReleaseReservation(ctx context.Context, subscriptionID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM provisioner.client_map WHERE subscription_id = $1 AND pending`, subscriptionID)
	if err != nil {
		return fmt.Errorf("release reservation: %w", err)
	}
	return nil
}

// PutClientMap upserts the subscription→client mapping of a client the
// panel has (no longer pending).
func (s *Store) PutClientMap(ctx context.Context, cm *ClientMap) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO provisioner.client_map (subscription_id, server_id, email, xui_sub_id, inbound_ids, pending)
		VALUES ($1, $2, $3, $4, $5, false)
		ON CONFLICT (subscription_id) DO UPDATE SET
			server_id = EXCLUDED.server_id, email = EXCLUDED.email,
			xui_sub_id = EXCLUDED.xui_sub_id,
			inbound_ids = EXCLUDED.inbound_ids, pending = false`,
		cm.SubscriptionID, cm.ServerID, cm.Email, cm.XUISubID, cm.InboundIDs)
	if err != nil {
		return fmt.Errorf("put client map: %w", err)
	}
	cm.Pending = false
	return nil
}

// GetClientMap returns the mapping for a subscription.
func (s *Store) GetClientMap(ctx context.Context, subscriptionID string) (*ClientMap, error) {
	var cm ClientMap
	err := s.db.QueryRow(ctx, `
		SELECT subscription_id, server_id, email, xui_sub_id, inbound_ids, pending
		FROM provisioner.client_map WHERE subscription_id = $1`, subscriptionID).
		Scan(&cm.SubscriptionID, &cm.ServerID, &cm.Email, &cm.XUISubID, &cm.InboundIDs, &cm.Pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get client map: %w", err)
	}
	return &cm, nil
}

// DeleteClientMap removes the mapping (after panel delete).
func (s *Store) DeleteClientMap(ctx context.Context, subscriptionID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM provisioner.client_map WHERE subscription_id = $1`, subscriptionID)
	if err != nil {
		return fmt.Errorf("delete client map: %w", err)
	}
	return nil
}

// InboundAttach returns the server's attach settings by inbound id; an
// inbound without one is attached.
func (s *Store) InboundAttach(ctx context.Context, serverID string) (map[int]bool, error) {
	rows, err := s.db.Query(ctx, `SELECT inbound_id, attach FROM provisioner.server_inbounds WHERE server_id = $1`, serverID)
	if err != nil {
		return nil, fmt.Errorf("inbound attach: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var (
			id     int
			attach bool
		)
		if err := rows.Scan(&id, &attach); err != nil {
			return nil, fmt.Errorf("inbound attach: %w", err)
		}
		out[id] = attach
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inbound attach: %w", err)
	}
	return out, nil
}

// SetInboundAttach sets whether new clients of the server are attached to
// the inbound (ErrNotFound for an unknown server).
func (s *Store) SetInboundAttach(ctx context.Context, serverID string, inboundID int, attach bool) error {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO provisioner.server_inbounds (server_id, inbound_id, attach)
		SELECT id, $2, $3 FROM provisioner.xui_servers WHERE id = $1
		ON CONFLICT (server_id, inbound_id) DO UPDATE SET attach = EXCLUDED.attach`, serverID, inboundID, attach)
	if err != nil {
		return fmt.Errorf("set inbound attach: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
