-- +goose Up
-- +goose StatementBegin
-- The kind of panel a server row is: 3x-ui (the original, and the default
-- for every existing row) or Marzban. It decides which adapter the
-- provisioner builds; it is fixed at creation, because the clients mapped
-- to a server live in that panel's own format.
-- For a Marzban row api_token_enc holds the encrypted "username:password"
-- of an admin (a Marzban username cannot contain a colon).
ALTER TABLE xui_servers
    ADD COLUMN kind text NOT NULL DEFAULT 'xui' CONSTRAINT xui_servers_kind_check CHECK (kind IN ('xui', 'marzban'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The old code only knows 3x-ui: a Marzban server (and the clients on it)
-- cannot be served, so its rows go. Its client_map rows go with it.
DELETE FROM client_map WHERE server_id IN (SELECT id FROM xui_servers WHERE kind <> 'xui');
DELETE FROM xui_servers WHERE kind <> 'xui';
ALTER TABLE xui_servers DROP COLUMN IF EXISTS kind;
-- +goose StatementEnd
