-- +goose Up
-- +goose StatementBegin
-- The group a 3x-ui client BOBRES made should be in. NULL is the default group, "BOBRES"; a reseller's clients are in
-- "BOBRES-<NAME>" (core brings the name with the create, and with SetClientGroups for the clients made before). It is what
-- the group backfill converges the panel to, moving only clients that are in a group BOBRES owns.
ALTER TABLE client_map ADD COLUMN panel_group text
    CHECK (panel_group IS NULL OR (panel_group <> '' AND char_length(panel_group) <= 64));

-- When something last asked the backfill to go through a server again (SetClientGroups): a pass that began before it must not
-- record the server as done.
ALTER TABLE xui_servers ADD COLUMN group_reset_at timestamptz;

-- The default group was renamed (bobres_bot to BOBRES): every 3x-ui server goes through the backfill once more, which
-- moves the clients that are in the old group (or in none) and leaves any group an operator chose.
UPDATE xui_servers SET group_backfilled_at = NULL WHERE kind = 'xui';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The groups already set on the panels stay there.
ALTER TABLE xui_servers DROP COLUMN IF EXISTS group_reset_at;
ALTER TABLE client_map DROP COLUMN IF EXISTS panel_group;
-- +goose StatementEnd
