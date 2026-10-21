-- +goose Up
-- +goose StatementBegin
-- Selling BOBRES licenses from the owner's own store (docs/13-license-server.md, "The signed admin API"). A buyer pays
-- from their wallet in USDT (any top-up method fills it, automatic TRC20 included); core then asks the license server
-- for a key. Only the owner's install uses these tables; on every other install they stay empty.

-- The wallet entries a purchase and its refund write have their own kinds.
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement','gift_code',
                    'license_purchase','license_refund'));

-- The key that signs the requests to the license server's admin API. One row. Core has no internet access, so the
-- requests go through the bot to the license server the bot is configured for (BOBRES_LICENSE_SERVER). The private key
-- is encrypted with core's master key (the same envelope as the other secrets); it is write-only: no read path returns it.
CREATE TABLE vendor_config (
    id            boolean PRIMARY KEY DEFAULT true CHECK (id),
    admin_key_enc bytea NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- A license a customer bought. license_id is the license server's id (set once the server made it); key_enc is the
-- activation key under core's master key, so the buyer can be shown it again from "My licenses" (the license server
-- itself keeps only its hash).
CREATE TABLE vendor_licenses (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id),
    license_id text UNIQUE CHECK (license_id ~ '^lic_[a-z0-9_-]{1,60}$'),
    tier       text NOT NULL CHECK (tier IN ('standard', 'pro')),
    ends_at    timestamptz,
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    key_enc    bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX vendor_licenses_user_idx ON vendor_licenses (user_id, created_at DESC);

-- One row per purchase or renewal. paid: the wallet was debited and the license server has not answered yet (a worker
-- retries it); done: the license exists or was extended; refunded: the server refused it for good, the money went back.
-- The idempotency key makes a double tap or a retried request one purchase.
CREATE TABLE vendor_orders (
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users (id),
    vendor_license_id uuid REFERENCES vendor_licenses (id),
    kind              text NOT NULL CHECK (kind IN ('new', 'renew')),
    tier              text NOT NULL CHECK (tier IN ('standard', 'pro')),
    months            integer NOT NULL CHECK (months IN (1, 12)),
    amount            bigint NOT NULL CHECK (amount > 0),
    currency          text NOT NULL CHECK (currency ~ '^[A-Z]{3,5}$'),
    status            text NOT NULL DEFAULT 'paid' CHECK (status IN ('paid', 'done', 'refunded')),
    idempotency_key   text NOT NULL UNIQUE CHECK (char_length(idempotency_key) BETWEEN 1 AND 200),
    attempts          integer NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    last_error        text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 500),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT vendor_orders_renew_has_license CHECK (kind <> 'renew' OR vendor_license_id IS NOT NULL)
);
CREATE INDEX vendor_orders_due_idx ON vendor_orders (next_attempt_at) WHERE status = 'paid';
CREATE INDEX vendor_orders_user_idx ON vendor_orders (user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vendor_orders;
DROP TABLE IF EXISTS vendor_licenses;
DROP TABLE IF EXISTS vendor_config;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement','gift_code'));
-- +goose StatementEnd
