-- +goose Up
-- +goose StatementBegin
-- A reseller's private note about one of their customers' services ("pays on
-- the 5th", "owes 50k"): one line, at most 200 characters, next to the name
-- (label) they give it. Only the reseller who owns the service reads or
-- writes it; the customer never sees it. NULL: no note.
ALTER TABLE subscriptions ADD COLUMN note text CHECK (char_length(note) <= 200);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE subscriptions DROP COLUMN IF EXISTS note;
-- +goose StatementEnd
