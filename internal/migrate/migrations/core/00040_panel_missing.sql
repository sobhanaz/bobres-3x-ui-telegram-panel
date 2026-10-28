-- +goose Up
-- +goose StatementBegin
-- A service whose client is gone from its panel (an operator deleted it there, or the panel was rebuilt): when core first saw that.
-- The usage worker asks about such a service rarely instead of every 10 minutes, no reminder is sent for it, and the dashboard shows it
-- and lists it. NULL: the panel has the client (or core has not asked yet).
ALTER TABLE subscriptions ADD COLUMN panel_missing_since timestamptz;
CREATE INDEX subscriptions_panel_missing_idx ON subscriptions (panel_missing_since) WHERE panel_missing_since IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS subscriptions_panel_missing_idx;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS panel_missing_since;
-- +goose StatementEnd
