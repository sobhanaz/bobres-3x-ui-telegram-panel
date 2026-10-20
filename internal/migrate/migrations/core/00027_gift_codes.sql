-- +goose Up
-- +goose StatementBegin
-- Gift codes (docs/01-database.md): a code a customer applies from the bot's wallet screen that adds a fixed amount
-- of credit to their wallet. A discount code takes money off a purchase; a gift code is money in the wallet.

-- The wallet entry a redemption writes has its own kind.
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement','gift_code'));

-- The codes. A code is never stored: code_hash is HMAC-SHA256 of its normalised form (capitals, no separators,
-- Latin digits) under a key derived from core's master key, so a copy of the database (a dump, a backup) cannot be
-- read back into usable codes and cannot be tested offline either. code_tail (its last four characters) is only
-- there so staff can tell codes apart in a list. The terms are fixed when the code is made: only enabled changes
-- (a disabled code stays in the list, and its redemptions stay in the ledger).
CREATE TABLE gift_codes (
    id                 uuid PRIMARY KEY,
    code_hash          bytea NOT NULL UNIQUE CHECK (octet_length(code_hash) = 32),
    code_tail          text NOT NULL CHECK (char_length(code_tail) BETWEEN 1 AND 8),
    generated          boolean NOT NULL,
    amount             bigint NOT NULL CHECK (amount > 0),
    currency           text NOT NULL CHECK (currency ~ '^[A-Z]{3,5}$'),
    max_redemptions    integer NOT NULL CHECK (max_redemptions > 0),
    per_customer_limit integer NOT NULL DEFAULT 1 CHECK (per_customer_limit > 0),
    -- Counted under the row's lock, never above max_redemptions (the CHECK is the last guard, not the first).
    redeemed_count     integer NOT NULL DEFAULT 0,
    expires_at         timestamptz,
    enabled            boolean NOT NULL DEFAULT true,
    note               text NOT NULL DEFAULT '' CHECK (char_length(note) <= 200),
    created_by         uuid REFERENCES users (id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    last_redeemed_at   timestamptz,
    CONSTRAINT gift_codes_redeemed_check CHECK (redeemed_count BETWEEN 0 AND max_redemptions),
    CONSTRAINT gift_codes_limit_check CHECK (per_customer_limit <= max_redemptions)
);
CREATE INDEX gift_codes_created_idx ON gift_codes (created_at DESC, id DESC);

-- One row per redemption. seq is the customer's n-th redemption of the code (1 to per_customer_limit), so a
-- code that may be used once per customer can be used once per customer even if the application checked
-- nothing; request_key is the caller's idempotency key (the bot's message), so a retried request finds its
-- first answer instead of a second credit. Each row names the one ledger entry it wrote.
CREATE TABLE gift_code_redemptions (
    id              uuid PRIMARY KEY,
    gift_code_id    uuid NOT NULL REFERENCES gift_codes (id),
    user_id         uuid NOT NULL REFERENCES users (id),
    seq             integer NOT NULL CHECK (seq > 0),
    request_key     text NOT NULL CHECK (char_length(request_key) BETWEEN 1 AND 128),
    amount          bigint NOT NULL CHECK (amount > 0),
    currency        text NOT NULL,
    ledger_entry_id uuid NOT NULL UNIQUE REFERENCES ledger_entries (id),
    balance_after   bigint NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT gift_code_redemptions_seq_key UNIQUE (gift_code_id, user_id, seq),
    CONSTRAINT gift_code_redemptions_request_key UNIQUE (user_id, request_key)
);
CREATE INDEX gift_code_redemptions_code_idx ON gift_code_redemptions (gift_code_id, created_at DESC, id DESC);
CREATE INDEX gift_code_redemptions_user_idx ON gift_code_redemptions (user_id, created_at DESC);

-- Wrong codes per customer: failures in the window that opened at window_start; locked_until is when entering
-- gift codes opens again for a customer who got too many wrong. Only wrong codes (unknown, switched off, expired
-- or not a code at all) are counted, never a right one, and a success does not reset the count.
CREATE TABLE gift_code_failures (
    user_id      uuid PRIMARY KEY REFERENCES users (id),
    failures     integer NOT NULL CHECK (failures >= 0),
    window_start timestamptz NOT NULL,
    locked_until timestamptz
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gift_code_failures;
DROP TABLE IF EXISTS gift_code_redemptions;
DROP TABLE IF EXISTS gift_codes;
-- The ledger keeps allowing 'gift_code': it is append-only, so credits already paid cannot be removed and the
-- old constraint would reject them.
-- +goose StatementEnd
