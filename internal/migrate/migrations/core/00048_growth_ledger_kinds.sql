-- +goose Up
-- +goose StatementBegin
-- Growth tools (docs/01-database.md): the ledger takes four kinds. cashback is a share of a delivered order given back to the buyer's wallet,
-- cashback_reversal takes it back when the order is refunded, target_bonus is a reseller's bonus for a monthly sales target and wheel_prize is a
-- prize of the daily wheel of fortune.
--
-- The new rule is the rule in force plus these four, read from the constraint itself, so a kind that another migration added is kept (the
-- constraint has been replaced by hand-written lists before, and two branches adding a kind each would otherwise drop each other's).
DO $$
DECLARE
    kinds text;
BEGIN
    SELECT string_agg(quote_literal(k), ', ' ORDER BY k) INTO kinds FROM (
        SELECT (regexp_matches(pg_get_constraintdef(c.oid), '''([a-z_]+)''', 'g'))[1] AS k
        FROM pg_constraint c WHERE c.conname = 'ledger_entries_kind_check' AND c.conrelid = 'ledger_entries'::regclass
        UNION SELECT unnest(ARRAY['cashback', 'cashback_reversal', 'target_bonus', 'wheel_prize'])
    ) t;
    ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
    EXECUTE 'ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN (' || kinds || '))';
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The ledger is append-only: the entries of these kinds stay, so the old rule is put back for new rows only (NOT VALID leaves the rows that are
-- there).
DO $$
DECLARE
    kinds text;
BEGIN
    SELECT string_agg(quote_literal(k), ', ' ORDER BY k) INTO kinds FROM (
        SELECT (regexp_matches(pg_get_constraintdef(c.oid), '''([a-z_]+)''', 'g'))[1] AS k
        FROM pg_constraint c WHERE c.conname = 'ledger_entries_kind_check' AND c.conrelid = 'ledger_entries'::regclass
    ) t WHERE k NOT IN ('cashback', 'cashback_reversal', 'target_bonus', 'wheel_prize');
    ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
    EXECUTE 'ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN (' || kinds || ')) NOT VALID';
END $$;
-- +goose StatementEnd
