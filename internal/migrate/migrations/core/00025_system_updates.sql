-- +goose Up
-- +goose StatementBegin
-- Updates of the install asked for in the dashboard (System > Updates > Update
-- now, docs/04-dashboard.md): one row for each request core handed to the
-- server's updater, written before the request file is, so no request exists
-- without its row, its audit entry and its Telegram notices, and read again
-- when the update ends (core itself is restarted by the update): the row
-- records who asked, from which version to which, and how it ended.
-- outcome is NULL while the update is open; core ends it exactly once, in the
-- transaction that writes the audit entry and the notices to the owners. At
-- most one update is open at a time (the unique index): a second request while
-- one is open is refused, and two requests at the same moment cannot both
-- succeed. requested_by is the dashboard user's account (kept NULL if it is
-- removed) and requested_by_name the name shown in the audit log and notices.
CREATE TABLE IF NOT EXISTS system_updates (
    id                text PRIMARY KEY
        CHECK (id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    requested_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    requested_by_name text NOT NULL CHECK (requested_by_name <> '' AND char_length(requested_by_name) <= 64),
    from_version      text NOT NULL CHECK (from_version <> '' AND char_length(from_version) <= 64),
    to_version        text NOT NULL CHECK (to_version <> '' AND char_length(to_version) <= 64),
    channel           text NOT NULL CHECK (channel IN ('stable', 'beta')),
    requested_at      timestamptz NOT NULL DEFAULT now(),
    outcome           text CHECK (outcome IN ('done', 'failed', 'rolled_back', 'not_started')),
    outcome_at        timestamptz,
    message           text NOT NULL DEFAULT '' CHECK (char_length(message) <= 300),
    CHECK ((outcome IS NULL) = (outcome_at IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS system_updates_one_open ON system_updates ((true)) WHERE outcome IS NULL;
CREATE INDEX IF NOT EXISTS system_updates_recent ON system_updates (requested_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS system_updates;
-- +goose StatementEnd
