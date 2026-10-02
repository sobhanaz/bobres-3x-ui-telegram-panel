-- +goose Up
-- +goose StatementBegin
-- One trial grant per user, enforced at the storage layer. Two concurrent
-- trial requests both see zero trial rows in their snapshots, but only one
-- INSERT can commit under this partial unique index.
CREATE UNIQUE INDEX IF NOT EXISTS ledger_entries_trial_once_idx
    ON ledger_entries (user_id)
    WHERE kind = 'trial_grant';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS ledger_entries_trial_once_idx;
-- +goose StatementEnd
