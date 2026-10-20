-- +goose Up
-- +goose StatementBegin
-- Every client BOBRES makes in a 3x-ui panel is put in the panel's group
-- "bobres_bot". group_backfilled_at records, per server, that the clients it
-- made before that (the rows of client_map) were found in a group: put in the
-- bot group, or already in one an operator chose. NULL = the provisioner still
-- has to do it (every existing server starts so). A Marzban server has no
-- groups and stays NULL. Setting it does not move updated_at, which keys the
-- cached panel clients.
ALTER TABLE xui_servers ADD COLUMN group_backfilled_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The groups already set on the panels stay there; after an Up again the
-- provisioner goes through every 3x-ui server once more, which changes
-- nothing for a client that has a group.
ALTER TABLE xui_servers DROP COLUMN IF EXISTS group_backfilled_at;
-- +goose StatementEnd
