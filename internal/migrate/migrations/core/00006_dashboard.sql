-- +goose Up
-- +goose StatementBegin
-- Phase 4: the web dashboard. Staff are users with a staff role (owner, admin,
-- support). They log in with a one-time link from the bot, or with a password
-- that always needs an authenticator code as well.

-- Never used: credentials now hang off the staff member's user row.
DROP TABLE IF EXISTS staff;

CREATE TABLE staff_credentials (
    user_id uuid PRIMARY KEY REFERENCES users (id),
    username text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9_.-]{3,32}$'),
    password_hash text NOT NULL,
    totp_secret_enc bytea NOT NULL,
    -- NULL until the first authenticator code is entered: no login before.
    totp_confirmed_at timestamptz,
    -- The last code step used: a code works once.
    totp_last_step bigint NOT NULL DEFAULT 0,
    failed_attempts int NOT NULL DEFAULT 0,
    locked_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Only a hash of the session cookie is stored.
CREATE TABLE web_sessions (
    id_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    csrf_token text NOT NULL,
    method text NOT NULL CHECK (method IN ('link', 'password')),
    ip text,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX web_sessions_user_idx ON web_sessions (user_id) WHERE revoked_at IS NULL;

-- One-time login links: only a hash of the token is stored; each works once.
CREATE TABLE login_links (
    token_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at timestamptz
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS login_links;
DROP TABLE IF EXISTS web_sessions;
DROP TABLE IF EXISTS staff_credentials;
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
-- +goose StatementEnd
