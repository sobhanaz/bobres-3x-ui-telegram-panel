-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS xui_servers (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    base_url text NOT NULL,
    api_token_enc bytea NOT NULL,
    panel_version text,
    enabled boolean NOT NULL DEFAULT true,
    last_health_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS provision_jobs (
    id uuid PRIMARY KEY,
    subscription_id uuid,
    action text NOT NULL CHECK (action IN ('create','renew','reset','delete','disable')),
    status text NOT NULL CHECK (status IN ('pending','running','done','failed')),
    attempts int NOT NULL DEFAULT 0,
    next_run_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS provision_jobs_pending_idx ON provision_jobs (next_run_at) WHERE status IN ('pending','failed');

CREATE TABLE IF NOT EXISTS client_map (
    subscription_id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES xui_servers (id),
    email text NOT NULL,
    xui_sub_id text,
    inbound_ids int[] NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS client_map_server_email_idx ON client_map (server_id, email);

CREATE TABLE IF NOT EXISTS outbox_provisioner (
    id bigserial PRIMARY KEY,
    topic text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_provisioner_unpublished_idx ON outbox_provisioner (id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS inbox_provisioner (
    message_id text PRIMARY KEY,
    handled_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inbox_provisioner;
DROP TABLE IF EXISTS outbox_provisioner;
DROP TABLE IF EXISTS client_map;
DROP TABLE IF EXISTS provision_jobs;
DROP TABLE IF EXISTS xui_servers;
-- +goose StatementEnd
