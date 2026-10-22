-- +goose Up
-- +goose StatementBegin
-- What a reseller's clients are called and grouped as in the 3x-ui panel. The owner gives the reseller a group name when making
-- them ("BOBRES-<panel_group>" is the group, e.g. BOBRES-SOBHANADMIN); the reseller chooses a prefix once for the names of their
-- configs ("<config_prefix>-u<id>-<random>"), and only the owner changes it after that. Both are unique across resellers, and
-- NULL for a reseller who has none yet (their clients then go in the default group "BOBRES" and have the usual names).
ALTER TABLE resellers ADD COLUMN config_prefix text
    CHECK (config_prefix IS NULL OR config_prefix ~ '^[a-z0-9]{2,10}$');
ALTER TABLE resellers ADD COLUMN panel_group text
    CHECK (panel_group IS NULL OR panel_group ~ '^[A-Z0-9_]{2,24}$');
-- NULL: the reseller's clients made before panel_group was set are still to be moved into the group (a worker does it and
-- records the time); setting or changing panel_group puts it back to NULL.
ALTER TABLE resellers ADD COLUMN groups_synced_at timestamptz;
CREATE UNIQUE INDEX resellers_config_prefix_key ON resellers (config_prefix) WHERE config_prefix IS NOT NULL;
CREATE UNIQUE INDEX resellers_panel_group_key ON resellers (panel_group) WHERE panel_group IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS resellers_panel_group_key;
DROP INDEX IF EXISTS resellers_config_prefix_key;
ALTER TABLE resellers DROP COLUMN IF EXISTS groups_synced_at;
ALTER TABLE resellers DROP COLUMN IF EXISTS panel_group;
ALTER TABLE resellers DROP COLUMN IF EXISTS config_prefix;
-- +goose StatementEnd
