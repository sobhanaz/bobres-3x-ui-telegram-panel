-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS payment_intents (
    id uuid PRIMARY KEY,
    order_id uuid,
    user_id uuid NOT NULL,
    provider text NOT NULL CHECK (provider IN ('wallet','manual_card','manual_crypto')),
    amount bigint NOT NULL CHECK (amount >= 0),
    currency text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','confirming','succeeded','failed','expired')),
    provider_ref text,
    expires_at timestamptz,
    idempotency_key text UNIQUE NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS payment_intents_status_idx ON payment_intents (status);
CREATE INDEX IF NOT EXISTS payment_intents_user_id_idx ON payment_intents (user_id);

CREATE TABLE IF NOT EXISTS manual_receipts (
    id uuid PRIMARY KEY,
    intent_id uuid NOT NULL UNIQUE REFERENCES payment_intents (id),
    network text,
    txid text,
    receipt_file text,
    reference_number text,
    submitted_at timestamptz NOT NULL DEFAULT now(),
    reviewed_by uuid,
    decision text CHECK (decision IN ('approved','rejected')),
    reason text,
    reviewed_at timestamptz
);

-- Mirror of core.ledger_entries for provider-side idempotency and reconciliation.
CREATE TABLE IF NOT EXISTS ledger_entries (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    currency text NOT NULL,
    amount bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('topup','purchase','refund')),
    ref_type text,
    ref_id uuid,
    idempotency_key text UNIQUE NOT NULL,
    balance_after bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS outbox_payments (
    id bigserial PRIMARY KEY,
    topic text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_payments_unpublished_idx ON outbox_payments (id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS inbox_payments (
    message_id text PRIMARY KEY,
    handled_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inbox_payments;
DROP TABLE IF EXISTS outbox_payments;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS manual_receipts;
DROP TABLE IF EXISTS payment_intents;
-- +goose StatementEnd
