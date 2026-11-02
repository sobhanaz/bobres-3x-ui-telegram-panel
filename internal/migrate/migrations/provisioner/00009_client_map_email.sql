-- +goose Up
-- +goose StatementBegin
-- Config names (core 00056): CheckClientName and RenameClient look a client up
-- by its name alone, on any server, once per name a buyer types. The unique
-- index is on (server_id, email) and cannot serve that lookup; this one does.
CREATE INDEX IF NOT EXISTS client_map_email_idx ON client_map (email);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS client_map_email_idx;
-- +goose StatementEnd
