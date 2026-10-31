-- +goose Up
-- +goose StatementBegin
-- Messages to resellers only (docs/04-dashboard.md, Resellers): a broadcast of the segment "resellers" goes to the active resellers (all of them,
-- only those in debt, or those of one tier) and each of them gets a text made for them: the placeholders ({name}, {debt}, ...) are filled in when
-- the broadcast starts and the result is kept with the recipient, so the bot sends exactly what was written for them. The weekly settlement
-- reminder is such a broadcast too, queued by core itself (created_by NULL) once a week.
ALTER TABLE broadcasts DROP CONSTRAINT broadcasts_segment_check;
ALTER TABLE broadcasts ADD CONSTRAINT broadcasts_segment_check CHECK (segment IN ('all', 'active', 'expired', 'never_bought', 'resellers'));
ALTER TABLE broadcasts
    -- Who of the resellers (segment resellers only): those with a negative balance in any currency, and/or those of one tier. The tier is not a
    -- foreign key: a tier deleted after the message was written matches nobody (never "every reseller").
    ADD COLUMN reseller_debt_only boolean NOT NULL DEFAULT false,
    ADD COLUMN reseller_tier_id   uuid,
    -- A "Settle / Top up" button under the message that opens the reseller's wallet in the bot.
    ADD COLUMN settle_button      boolean NOT NULL DEFAULT false,
    -- manual: written by staff; reminder: the weekly settlement reminder.
    ADD COLUMN kind               text NOT NULL DEFAULT 'manual' CHECK (kind IN ('manual', 'reminder')),
    ADD CONSTRAINT broadcasts_reseller_filters CHECK (segment = 'resellers' OR (NOT reseller_debt_only AND reseller_tier_id IS NULL AND NOT settle_button AND kind = 'manual'));
-- Core's own broadcasts (the weekly reminder) have no author.
ALTER TABLE broadcasts ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE broadcasts ADD CONSTRAINT broadcasts_author CHECK (created_by IS NOT NULL OR kind = 'reminder');

-- The recipient's own text (NULL: the broadcast's text in their language).
ALTER TABLE broadcast_recipients ADD COLUMN text text CHECK (char_length(text) <= 4096);

-- The weekly settlement reminder's claims: one row for each week it was queued, keyed by the store-local date it was due on. The row is written
-- in the transaction that writes the reminder's broadcast, so a crash can neither lose nor repeat it, and a restart or a second claimer finds it.
CREATE TABLE reseller_reminder_claims (
    day          date PRIMARY KEY,
    zone         text NOT NULL,
    due_at       timestamptz NOT NULL,
    broadcast_id uuid REFERENCES broadcasts (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS reseller_reminder_claims;
-- The messages to resellers go with the columns that describe them.
DELETE FROM broadcasts WHERE segment = 'resellers';
ALTER TABLE broadcast_recipients DROP COLUMN IF EXISTS text;
ALTER TABLE broadcasts DROP CONSTRAINT IF EXISTS broadcasts_author;
ALTER TABLE broadcasts ALTER COLUMN created_by SET NOT NULL;
ALTER TABLE broadcasts DROP CONSTRAINT IF EXISTS broadcasts_reseller_filters;
ALTER TABLE broadcasts DROP COLUMN IF EXISTS kind, DROP COLUMN IF EXISTS settle_button, DROP COLUMN IF EXISTS reseller_tier_id,
    DROP COLUMN IF EXISTS reseller_debt_only;
ALTER TABLE broadcasts DROP CONSTRAINT broadcasts_segment_check;
ALTER TABLE broadcasts ADD CONSTRAINT broadcasts_segment_check CHECK (segment IN ('all', 'active', 'expired', 'never_bought'));
-- +goose StatementEnd
