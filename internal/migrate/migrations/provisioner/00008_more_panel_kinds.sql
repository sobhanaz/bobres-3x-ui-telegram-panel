-- +goose Up
-- +goose StatementBegin
-- Two more kinds of panel: S-UI ('sui', a sing-box panel) and Hiddify Manager
-- ('hiddify'). For an S-UI row api_token_enc holds the encrypted API token the
-- panel made; for a Hiddify row the encrypted API key, the UUID of an admin.
ALTER TABLE xui_servers DROP CONSTRAINT xui_servers_kind_check;
ALTER TABLE xui_servers
    ADD CONSTRAINT xui_servers_kind_check CHECK (kind IN ('xui', 'marzban', 'sui', 'hiddify'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The old code knows 3x-ui and Marzban only: an S-UI or Hiddify server (and
-- the clients on it) cannot be served, so its rows go, with its client_map rows.
DELETE FROM client_map WHERE server_id IN (SELECT id FROM xui_servers WHERE kind IN ('sui', 'hiddify'));
DELETE FROM xui_servers WHERE kind IN ('sui', 'hiddify');
ALTER TABLE xui_servers DROP CONSTRAINT xui_servers_kind_check;
ALTER TABLE xui_servers
    ADD CONSTRAINT xui_servers_kind_check CHECK (kind IN ('xui', 'marzban'));
-- +goose StatementEnd
