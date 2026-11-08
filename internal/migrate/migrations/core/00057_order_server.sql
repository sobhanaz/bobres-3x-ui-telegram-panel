-- +goose Up
-- +goose StatementBegin
-- Undelivered orders (docs/04-dashboard.md, "Undelivered orders"): staff may deliver a paid order whose servers do not
-- answer on another server they choose. The order keeps the choice until it is delivered: the worker offers the
-- provisioner that server instead of the plan's. Servers live in the provisioner's schema, so there is no foreign key
-- (a server deleted since is refused when the order is tried, and the order waits as any undelivered one).
ALTER TABLE orders ADD COLUMN server_id uuid;

-- A refund of an undelivered order takes back the commission its reseller was paid on it: the ledger takes the kind
-- commission_reversal, put into the rule in force (read from the constraint itself, as 00048 and 00054 do, so a kind
-- another migration added is kept).
DO $$
DECLARE
    kinds text;
BEGIN
    SELECT string_agg(quote_literal(k), ', ' ORDER BY k) INTO kinds FROM (
        SELECT (regexp_matches(pg_get_constraintdef(c.oid), '''([a-z_]+)''', 'g'))[1] AS k
        FROM pg_constraint c WHERE c.conname = 'ledger_entries_kind_check' AND c.conrelid = 'ledger_entries'::regclass
        UNION SELECT 'commission_reversal'
    ) t;
    ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
    EXECUTE 'ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN (' || kinds || '))';
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN IF EXISTS server_id;
-- The ledger is append-only: entries of this kind stay, so the old rule is put back for new rows only (NOT VALID leaves
-- the rows that are there).
DO $$
DECLARE
    kinds text;
BEGIN
    SELECT string_agg(quote_literal(k), ', ' ORDER BY k) INTO kinds FROM (
        SELECT (regexp_matches(pg_get_constraintdef(c.oid), '''([a-z_]+)''', 'g'))[1] AS k
        FROM pg_constraint c WHERE c.conname = 'ledger_entries_kind_check' AND c.conrelid = 'ledger_entries'::regclass
    ) t WHERE k <> 'commission_reversal';
    ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
    EXECUTE 'ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN (' || kinds || ')) NOT VALID';
END $$;
-- +goose StatementEnd
