-- +goose Up
-- +goose StatementBegin
-- Restores of a backup asked for in the dashboard (System > Backups > Restore, docs/04-dashboard.md)
-- are recorded in the table of the updates (00025): both are one request core hands the server's
-- helper, at most one is open at a time (the unique index of 00025 covers both), and both end
-- exactly once with an audit entry and a Telegram notice to every owner. A row now says what it is:
--   action  update (the default: every row there is) or restore;
--   subject the backup of a restore (its file name, or the name an upload had), '' for an update;
--   source  where the backup of a restore came from: backup (the backup service's list) or upload.
-- A restore carries no channel and no release (from and to are the version core runs), so the
-- channel may be '' for it and only for it.
-- A restore replaces this very database with the backup's, which does not hold the row: when core
-- is back it records the end of the restore itself, as a row that is already ended, from what the
-- helper's status says (domain.Updates.adoptRestore), and inserts it once (the id is the key).
ALTER TABLE system_updates ADD COLUMN IF NOT EXISTS action text NOT NULL DEFAULT 'update'
    CHECK (action IN ('update', 'restore'));
ALTER TABLE system_updates ADD COLUMN IF NOT EXISTS subject text NOT NULL DEFAULT ''
    CHECK (char_length(subject) <= 200);
ALTER TABLE system_updates ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT ''
    CHECK (source IN ('', 'backup', 'upload'));
ALTER TABLE system_updates DROP CONSTRAINT IF EXISTS system_updates_channel_check;
ALTER TABLE system_updates ADD CONSTRAINT system_updates_channel_check
    CHECK (channel IN ('stable', 'beta', ''));
ALTER TABLE system_updates DROP CONSTRAINT IF EXISTS system_updates_shape;
ALTER TABLE system_updates ADD CONSTRAINT system_updates_shape CHECK (
    (action = 'update' AND channel <> '' AND subject = '' AND source = '')
    OR (action = 'restore' AND channel = '' AND subject <> '' AND source <> ''));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The restores go; the updates stay as they were.
DELETE FROM system_updates WHERE action = 'restore';
ALTER TABLE system_updates DROP CONSTRAINT IF EXISTS system_updates_shape;
ALTER TABLE system_updates DROP CONSTRAINT IF EXISTS system_updates_channel_check;
ALTER TABLE system_updates ADD CONSTRAINT system_updates_channel_check CHECK (channel IN ('stable', 'beta'));
ALTER TABLE system_updates DROP COLUMN IF EXISTS source;
ALTER TABLE system_updates DROP COLUMN IF EXISTS subject;
ALTER TABLE system_updates DROP COLUMN IF EXISTS action;
-- +goose StatementEnd
