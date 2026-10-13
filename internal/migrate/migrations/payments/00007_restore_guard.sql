-- Notices that the database was restored from a backup (bobres restore,
-- bobres rollback, or the automatic rollback of a failed update). Automatic
-- USDT payments created after the backup are gone then, but their customers
-- were still told an exact amount, which a new payment could be given again.
-- The one row keeps this table's own OID; a restore recreates every table
-- (pg_restore), so the OID stops matching the row's, and payments records
-- when it noticed (restored_at). Payments writes the row itself, never this
-- migration: a backup taken before this table existed restores without it,
-- and then nothing is noticed.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE restore_guard (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    table_oid oid NOT NULL,
    restored_at timestamptz
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS restore_guard;
-- +goose StatementEnd
