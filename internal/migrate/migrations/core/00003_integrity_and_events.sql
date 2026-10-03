-- +goose Up
-- +goose StatementBegin
-- Ledger rows are facts: a correction is a new entry, never an edit.
CREATE OR REPLACE FUNCTION ledger_entries_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only (% is not allowed)', TG_OP;
END;
$$;
CREATE TRIGGER ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION ledger_entries_append_only();

-- Events get a globally unique id that consumers de-duplicate on (outbox ids
-- restart after a restore, event ids never collide). Consumers pull the outbox
-- through eventbus.Feed and keep one cursor each; published_at is unused.
ALTER TABLE outbox_core ADD COLUMN event_id uuid;
UPDATE outbox_core SET event_id = gen_random_uuid() WHERE event_id IS NULL;
ALTER TABLE outbox_core ALTER COLUMN event_id SET NOT NULL;
CREATE UNIQUE INDEX outbox_core_event_id_idx ON outbox_core (event_id);

CREATE TABLE outbox_core_cursors (
    consumer text PRIMARY KEY,
    last_id bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Events from other services that could not be applied (malformed, or failing
-- for longer than the retry budget). Kept for an operator to inspect and replay.
CREATE TABLE dead_letters (
    id uuid PRIMARY KEY,
    source text NOT NULL,
    event_id text NOT NULL,
    topic text NOT NULL,
    payload text,
    error text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, event_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS dead_letters;
DROP TABLE IF EXISTS outbox_core_cursors;
DROP INDEX IF EXISTS outbox_core_event_id_idx;
ALTER TABLE outbox_core DROP COLUMN IF EXISTS event_id;
DROP TRIGGER IF EXISTS ledger_entries_append_only ON ledger_entries;
DROP FUNCTION IF EXISTS ledger_entries_append_only();
-- +goose StatementEnd
