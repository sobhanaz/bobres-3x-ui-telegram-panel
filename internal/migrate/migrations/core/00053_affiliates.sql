-- +goose Up
-- +goose StatementBegin
-- The affiliate program of the owner's own store (docs/18-hosted-and-affiliate-plans.md, section 2): a person who brings a new license buyer
-- earns a share of that buyer's first license payment. Only the owner's install uses these tables (the license sales are the vendor's own);
-- on every other install they stay empty.
--
-- The ledger takes three kinds, put into the rule in force (read from the constraint itself, as 00048 does, so a kind another migration added is
-- kept): affiliate_commission credits an affiliate's wallet when a license they brought is paid and delivered, affiliate_reversal takes it back
-- when that license is refunded within 30 days, and affiliate_payout takes from the wallet what the owner paid the affiliate by hand.
DO $$
DECLARE
    kinds text;
BEGIN
    SELECT string_agg(quote_literal(k), ', ' ORDER BY k) INTO kinds FROM (
        SELECT (regexp_matches(pg_get_constraintdef(c.oid), '''([a-z_]+)''', 'g'))[1] AS k
        FROM pg_constraint c WHERE c.conname = 'ledger_entries_kind_check' AND c.conrelid = 'ledger_entries'::regclass
        UNION SELECT unnest(ARRAY['affiliate_commission', 'affiliate_reversal', 'affiliate_payout'])
    ) t;
    ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
    EXECUTE 'ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN (' || kinds || '))';
END $$;

-- One affiliate per person (a customer of the owner's store): the code of their link (t.me/<bot>?start=aff_<code>), and an override of the
-- program's percent (NULL: the program's percent; 0: this affiliate earns nothing). disabled: their link records nobody new and earns nothing new.
CREATE TABLE affiliates (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL UNIQUE REFERENCES users (id),
    code       text NOT NULL UNIQUE CHECK (code ~ '^[a-z0-9]{6,16}$'),
    percent    integer CHECK (percent IS NULL OR percent BETWEEN 0 AND 100),
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Who brought whom: the first affiliate link a customer opened before they bought their first license. One row per customer (the first link
-- wins); never the affiliate themselves (no self-referral).
CREATE TABLE affiliate_referrals (
    user_id      uuid PRIMARY KEY REFERENCES users (id),
    affiliate_id uuid NOT NULL REFERENCES affiliates (id),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX affiliate_referrals_affiliate_idx ON affiliate_referrals (affiliate_id, created_at DESC);

-- The affiliate a license order was brought by, recorded when the order is made (a new license of a referred customer).
ALTER TABLE vendor_orders ADD COLUMN affiliate_id uuid REFERENCES affiliates (id);
CREATE INDEX vendor_orders_affiliate_idx ON vendor_orders (affiliate_id, created_at DESC) WHERE affiliate_id IS NOT NULL;

-- A commission paid: one per license order, and one per buyer (the first payment only). The credit is the ledger entry (key
-- affiliate_commission:<order id>), written in the transaction that marks the order done. A refund within 30 days takes it back as far as the
-- wallet holds it, never below zero (reversed_amount; the shortfall is in the audit log).
CREATE TABLE affiliate_commissions (
    order_id          uuid PRIMARY KEY REFERENCES vendor_orders (id),
    affiliate_id      uuid NOT NULL REFERENCES affiliates (id),
    affiliate_user_id uuid NOT NULL REFERENCES users (id),
    buyer_user_id     uuid NOT NULL UNIQUE REFERENCES users (id),
    currency          text NOT NULL CHECK (currency ~ '^[A-Z]{3,5}$'),
    base_amount       bigint NOT NULL CHECK (base_amount > 0),
    percent           integer NOT NULL CHECK (percent BETWEEN 1 AND 100),
    amount            bigint NOT NULL CHECK (amount > 0),
    ledger_entry_id   uuid NOT NULL UNIQUE REFERENCES ledger_entries (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    reversed_amount   bigint CHECK (reversed_amount IS NULL OR (reversed_amount >= 0 AND reversed_amount <= amount)),
    reversed_at       timestamptz,
    reversal_entry_id uuid UNIQUE REFERENCES ledger_entries (id),
    CONSTRAINT affiliate_commissions_reversal_check CHECK ((reversed_at IS NULL) = (reversed_amount IS NULL)),
    CONSTRAINT affiliate_commissions_not_self CHECK (affiliate_user_id <> buyer_user_id)
);
CREATE INDEX affiliate_commissions_affiliate_idx ON affiliate_commissions (affiliate_id, created_at DESC);

-- What the owner paid an affiliate by hand (USDT, a card transfer): the amount leaves the affiliate's wallet (ledger kind affiliate_payout, key
-- affiliate_payout:<request id>), at least the program's minimum payout.
CREATE TABLE affiliate_payouts (
    id              uuid PRIMARY KEY,
    affiliate_id    uuid NOT NULL REFERENCES affiliates (id),
    currency        text NOT NULL CHECK (currency ~ '^[A-Z]{3,5}$'),
    amount          bigint NOT NULL CHECK (amount > 0),
    note            text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    request_id      text NOT NULL UNIQUE CHECK (char_length(request_id) BETWEEN 8 AND 40),
    ledger_entry_id uuid NOT NULL UNIQUE REFERENCES ledger_entries (id),
    created_by      uuid REFERENCES users (id),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX affiliate_payouts_affiliate_idx ON affiliate_payouts (affiliate_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS affiliate_payouts;
DROP TABLE IF EXISTS affiliate_commissions;
DROP INDEX IF EXISTS vendor_orders_affiliate_idx;
ALTER TABLE vendor_orders DROP COLUMN IF EXISTS affiliate_id;
DROP TABLE IF EXISTS affiliate_referrals;
DROP TABLE IF EXISTS affiliates;
-- The ledger is append-only: entries of these kinds stay, so the old rule is put back for new rows only (NOT VALID leaves the rows that are there).
DO $$
DECLARE
    kinds text;
BEGIN
    SELECT string_agg(quote_literal(k), ', ' ORDER BY k) INTO kinds FROM (
        SELECT (regexp_matches(pg_get_constraintdef(c.oid), '''([a-z_]+)''', 'g'))[1] AS k
        FROM pg_constraint c WHERE c.conname = 'ledger_entries_kind_check' AND c.conrelid = 'ledger_entries'::regclass
    ) t WHERE k NOT IN ('affiliate_commission', 'affiliate_reversal', 'affiliate_payout');
    ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
    EXECUTE 'ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN (' || kinds || ')) NOT VALID';
END $$;
-- +goose StatementEnd
