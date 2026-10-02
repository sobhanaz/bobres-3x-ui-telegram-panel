-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY,
    telegram_id bigint UNIQUE NOT NULL,
    username text,
    language text NOT NULL DEFAULT 'fa',
    role text NOT NULL DEFAULT 'user' CHECK (role IN ('user','support','admin','owner','reseller')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','banned')),
    referred_by uuid REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wallets (
    user_id uuid NOT NULL REFERENCES users (id),
    currency text NOT NULL,
    balance bigint NOT NULL DEFAULT 0 CHECK (balance >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, currency)
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    currency text NOT NULL,
    amount bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant')),
    ref_type text,
    ref_id uuid,
    idempotency_key text UNIQUE NOT NULL,
    balance_after bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plans (
    id uuid PRIMARY KEY,
    name_i18n jsonb NOT NULL,
    kind text NOT NULL CHECK (kind IN ('traffic','time','both')),
    duration_days int,
    traffic_bytes bigint,
    price bigint NOT NULL CHECK (price >= 0),
    currency text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    is_trial boolean NOT NULL DEFAULT false,
    sort int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS orders (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    plan_id uuid NOT NULL REFERENCES plans (id),
    type text NOT NULL CHECK (type IN ('new','renew','traffic_topup')),
    status text NOT NULL CHECK (status IN ('created','awaiting_payment','paid','provisioning','active','provision_failed','cancelled','expired')),
    amount bigint NOT NULL CHECK (amount >= 0),
    currency text NOT NULL,
    idempotency_key text UNIQUE NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS orders_user_id_idx ON orders (user_id);

CREATE TABLE IF NOT EXISTS subscriptions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    order_id uuid NOT NULL REFERENCES orders (id),
    server_id uuid NOT NULL,
    client_email text UNIQUE NOT NULL,
    sub_id text,
    status text NOT NULL CHECK (status IN ('pending','active','expiring_soon','expired','disabled','depleted')),
    expires_at timestamptz,
    traffic_total_bytes bigint,
    traffic_used_bytes bigint DEFAULT 0,
    last_synced_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS subscriptions_user_id_idx ON subscriptions (user_id);

CREATE TABLE IF NOT EXISTS tickets (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    category text NOT NULL,
    text text NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_log (
    id uuid PRIMARY KEY,
    actor_id uuid,
    action text NOT NULL,
    entity text NOT NULL,
    entity_id uuid,
    before jsonb,
    after jsonb,
    reason text,
    ip inet,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS staff (
    id uuid PRIMARY KEY,
    username text UNIQUE NOT NULL,
    password_hash text NOT NULL,
    totp_secret_enc bytea,
    role text NOT NULL DEFAULT 'admin' CHECK (role IN ('support','admin','owner')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS outbox_core (
    id bigserial PRIMARY KEY,
    topic text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_core_unpublished_idx ON outbox_core (id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS inbox_core (
    message_id text PRIMARY KEY,
    handled_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inbox_core;
DROP TABLE IF EXISTS outbox_core;
DROP TABLE IF EXISTS staff;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS tickets;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
