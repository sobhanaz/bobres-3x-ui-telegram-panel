-- +goose Up
-- +goose StatementBegin
-- Phase 4, milestone 3 (store setup in the dashboard).

-- Audit entries can point at items without a uuid (a discount code, a
-- setting key, a bot text), and say where a change was made.
ALTER TABLE audit_log ALTER COLUMN entity_id TYPE text USING entity_id::text;
ALTER TABLE audit_log ADD COLUMN source text
    CHECK (source IN ('dashboard', 'bot', 'cli', 'system')); -- NULL: written before this column
UPDATE audit_log SET entity_id = after->>'Code' WHERE action = 'discount.upsert' AND entity_id IS NULL;
UPDATE audit_log SET entity_id = after->>'key' WHERE action = 'setting.set' AND entity_id IS NULL;

-- The Audit log page: newest first, by staff member, by item.
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC, id DESC);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_id, created_at DESC) WHERE actor_id IS NOT NULL;
CREATE INDEX audit_log_entity_idx ON audit_log (entity_id) WHERE entity_id IS NOT NULL;

-- The audit log is append-only: nothing may change or remove an entry.
CREATE FUNCTION audit_log_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END
$$;
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_append_only();

-- Store files (the logo). Not in settings: the bot reads every setting each minute.
CREATE TABLE assets (
    name         text PRIMARY KEY CHECK (name IN ('logo')),
    content_type text NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/webp')),
    data         bytea NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- The Staff page: each member's last login.
CREATE INDEX web_sessions_user_created_idx ON web_sessions (user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS web_sessions_user_created_idx;
DROP TABLE IF EXISTS assets;
DROP TRIGGER IF EXISTS audit_log_no_truncate ON audit_log;
DROP TRIGGER IF EXISTS audit_log_append_only ON audit_log;
DROP FUNCTION IF EXISTS audit_log_append_only();
DROP INDEX IF EXISTS audit_log_entity_idx;
DROP INDEX IF EXISTS audit_log_actor_idx;
DROP INDEX IF EXISTS audit_log_created_idx;
ALTER TABLE audit_log DROP COLUMN IF EXISTS source;
ALTER TABLE audit_log ALTER COLUMN entity_id TYPE uuid USING
    CASE WHEN entity_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN entity_id::uuid END;
-- +goose StatementEnd
