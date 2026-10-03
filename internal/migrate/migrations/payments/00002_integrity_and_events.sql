-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger_entries_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only (% is not allowed)', TG_OP;
END;
$$;
CREATE TRIGGER ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION ledger_entries_append_only();

-- A crypto transaction can pay for exactly one intent. TXIDs are compared
-- case-insensitively (hex hashes are often pasted in either case).
CREATE UNIQUE INDEX manual_receipts_txid_once_idx
    ON manual_receipts (lower(txid)) WHERE txid IS NOT NULL;
-- Card reference numbers can legitimately repeat across banks, so they are only
-- flagged to the reviewer, never rejected; this index backs that lookup.
CREATE INDEX manual_receipts_reference_idx
    ON manual_receipts (reference_number) WHERE reference_number IS NOT NULL;

ALTER TABLE outbox_payments ADD COLUMN event_id uuid;
UPDATE outbox_payments SET event_id = gen_random_uuid() WHERE event_id IS NULL;
ALTER TABLE outbox_payments ALTER COLUMN event_id SET NOT NULL;
CREATE UNIQUE INDEX outbox_payments_event_id_idx ON outbox_payments (event_id);

CREATE TABLE outbox_payments_cursors (
    consumer text PRIMARY KEY,
    last_id bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbox_payments_cursors;
DROP INDEX IF EXISTS outbox_payments_event_id_idx;
ALTER TABLE outbox_payments DROP COLUMN IF EXISTS event_id;
DROP INDEX IF EXISTS manual_receipts_reference_idx;
DROP INDEX IF EXISTS manual_receipts_txid_once_idx;
DROP TRIGGER IF EXISTS ledger_entries_append_only ON ledger_entries;
DROP FUNCTION IF EXISTS ledger_entries_append_only();
-- +goose StatementEnd
