-- +goose Up
-- +goose StatementBegin
-- The most clients a server takes. 0 is no limit, and every server that exists starts so. A server whose
-- clients (the rows of client_map that name it, reservations included) have reached it is skipped when the
-- provisioner picks one for a new client, and refuses a request that names it. The clients it has keep
-- working, and the limit may be set below how many it has.
ALTER TABLE xui_servers
    ADD COLUMN max_clients int NOT NULL DEFAULT 0
        CONSTRAINT xui_servers_max_clients_check CHECK (max_clients BETWEEN 0 AND 1000000);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The clients stay where they are; an older provisioner has no limit to keep.
ALTER TABLE xui_servers DROP COLUMN IF EXISTS max_clients;
-- +goose StatementEnd
