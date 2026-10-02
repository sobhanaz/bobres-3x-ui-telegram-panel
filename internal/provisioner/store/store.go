// Package store persists provisioner state: 3x-ui servers (tokens encrypted
// with the envelope), provisioning jobs, and the subscription→panel client map.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// ErrNotFound is returned by Get* when no row matches.
var ErrNotFound = errors.New("store: not found")

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

// AddServer encrypts the token and inserts the server row.
func (s *Store) AddServer(ctx context.Context, sv *Server) (*Server, error) {
	enc, err := s.env.Encrypt([]byte(sv.Token))
	if err != nil {
		return nil, err
	}
	sv.ID = buuid.MustV7().String()
	err = s.db.QueryRow(ctx, `
		INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, enabled)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, sv.ID, sv.Name, sv.BaseURL, enc, sv.Enabled).Scan(&sv.ID)
	if err != nil {
		return nil, fmt.Errorf("add server: %w", err)
	}
	return sv, nil
}

// FirstServer returns the first enabled server (Phase 1: single server).
func (s *Store) FirstServer(ctx context.Context) (*Server, error) {
	var (
		sv  Server
		enc []byte
		ver *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, name, base_url, api_token_enc, panel_version, enabled
		FROM provisioner.xui_servers WHERE enabled ORDER BY created_at LIMIT 1`).
		Scan(&sv.ID, &sv.Name, &sv.BaseURL, &enc, &ver, &sv.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("first server: %w", err)
	}
	tok, err := s.env.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt server token: %w", err)
	}
	sv.Token = string(tok)
	if ver != nil {
		sv.PanelVersion = *ver
	}
	return &sv, nil
}

// GetServer returns one server row by id, decrypting its token.
func (s *Store) GetServer(ctx context.Context, id string) (*Server, error) {
	var (
		sv  Server
		enc []byte
		ver *string
	)
	err := s.db.QueryRow(ctx, `
		SELECT id, name, base_url, api_token_enc, panel_version, enabled
		FROM provisioner.xui_servers WHERE id = $1`, id).
		Scan(&sv.ID, &sv.Name, &sv.BaseURL, &enc, &ver, &sv.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get server: %w", err)
	}
	tok, err := s.env.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt server token: %w", err)
	}
	sv.Token = string(tok)
	if ver != nil {
		sv.PanelVersion = *ver
	}
	return &sv, nil
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

// PutClientMap upserts the subscription→client mapping.
func (s *Store) PutClientMap(ctx context.Context, cm *ClientMap) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO provisioner.client_map (subscription_id, server_id, email, xui_sub_id, inbound_ids)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (subscription_id) DO UPDATE SET
			server_id = EXCLUDED.server_id, email = EXCLUDED.email,
			xui_sub_id = EXCLUDED.xui_sub_id,
			inbound_ids = EXCLUDED.inbound_ids`,
		cm.SubscriptionID, cm.ServerID, cm.Email, cm.XUISubID, cm.InboundIDs)
	if err != nil {
		return fmt.Errorf("put client map: %w", err)
	}
	return nil
}

// GetClientMap returns the mapping for a subscription.
func (s *Store) GetClientMap(ctx context.Context, subscriptionID string) (*ClientMap, error) {
	var cm ClientMap
	err := s.db.QueryRow(ctx, `
		SELECT subscription_id, server_id, email, xui_sub_id, inbound_ids
		FROM provisioner.client_map WHERE subscription_id = $1`, subscriptionID).
		Scan(&cm.SubscriptionID, &cm.ServerID, &cm.Email, &cm.XUISubID, &cm.InboundIDs)
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

// TouchHealth records a successful panel health check.
func (s *Store) TouchHealth(ctx context.Context, serverID, panelVersion string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE provisioner.xui_servers
		SET panel_version = $2, last_health_at = $3, updated_at = now() WHERE id = $1`,
		serverID, panelVersion, time.Now().UTC())
	return err
}
