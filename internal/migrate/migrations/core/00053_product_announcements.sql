-- +goose Up
-- +goose StatementBegin
-- New product announcements (docs/04-dashboard.md, "Announcing a product"; docs/03-bot-ux.md, "Product news"): staff announce a plan, a
-- supplier's digital account or a BOBRES license as a card with a Buy button, in the store's channel and/or in private messages to the
-- customers who said yes to product news.

-- Whether the customer wants product news in private messages: NULL until they answer (no answer is no), and when the bot asked them (it asks
-- once: a NULL here means the question is still to come).
ALTER TABLE users
    ADD COLUMN announce_optin    boolean,
    ADD COLUMN announce_asked_at timestamptz;

-- The private part of an announcement is a broadcast of the segment "announce" (the customers who said yes), sent by the broadcast machinery.
-- Its Buy button opens the product in the bot: buy_callback is that button's callback data (the label is the bot's own text).
ALTER TABLE broadcasts DROP CONSTRAINT broadcasts_segment_check;
ALTER TABLE broadcasts ADD CONSTRAINT broadcasts_segment_check
    CHECK (segment IN ('all', 'active', 'expired', 'never_bought', 'resellers', 'announce'));
ALTER TABLE broadcasts
    ADD COLUMN buy_callback text CHECK (char_length(buy_callback) BETWEEN 1 AND 64),
    ADD CONSTRAINT broadcasts_buy_callback_announce CHECK (buy_callback IS NULL OR segment = 'announce');

-- One row per announcement. due_at is when it goes out: now, or the end of the quiet hours it was sent in (it waits queued until then).
CREATE TABLE announcements (
    id                 uuid PRIMARY KEY,
    product_kind       text NOT NULL CHECK (product_kind IN ('plan', 'supplier', 'license')),
    -- A plan's id, a supplier product's id, or a license offer as tier:months.
    product_id         text NOT NULL CHECK (char_length(product_id) BETWEEN 1 AND 200),
    -- The product's name when it was announced (for the history, also after the product is gone).
    product_name       text NOT NULL DEFAULT '',
    -- The card as staff wrote it, per language, with its placeholders ({name}, {price}, ...): written with the product's values when it goes out.
    texts              jsonb NOT NULL,
    to_channel         boolean NOT NULL,
    to_private         boolean NOT NULL,
    -- The channel's text: the store's default language, or both languages one under the other.
    channel_langs      text NOT NULL DEFAULT 'default' CHECK (channel_langs IN ('default', 'both')),
    -- Sent although the same product was announced in the last 24 hours.
    forced             boolean NOT NULL DEFAULT false,
    status             text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'cancelled')),
    due_at             timestamptz NOT NULL,
    sent_at            timestamptz,
    -- The private messages (NULL: none, or not started yet).
    broadcast_id       uuid REFERENCES broadcasts (id) ON DELETE SET NULL,
    -- The channel post: none (not asked), pending, sent or failed (with the reason).
    channel_chat       text,
    channel_status     text NOT NULL DEFAULT 'none' CHECK (channel_status IN ('none', 'pending', 'sent', 'failed')),
    channel_message_id bigint,
    channel_error      text CHECK (char_length(channel_error) <= 500),
    created_by         uuid NOT NULL REFERENCES users (id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT announcements_somewhere CHECK (to_channel OR to_private),
    CONSTRAINT announcements_channel_status CHECK (to_channel OR channel_status = 'none')
);
CREATE INDEX announcements_product_idx ON announcements (product_kind, product_id, created_at DESC);
CREATE INDEX announcements_due_idx ON announcements (due_at) WHERE status = 'queued';
CREATE INDEX announcements_created_idx ON announcements (created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS announcements;
-- The private messages of announcements go with the segment that describes them.
DELETE FROM broadcasts WHERE segment = 'announce';
ALTER TABLE broadcasts DROP CONSTRAINT IF EXISTS broadcasts_buy_callback_announce;
ALTER TABLE broadcasts DROP COLUMN IF EXISTS buy_callback;
ALTER TABLE broadcasts DROP CONSTRAINT broadcasts_segment_check;
ALTER TABLE broadcasts ADD CONSTRAINT broadcasts_segment_check
    CHECK (segment IN ('all', 'active', 'expired', 'never_bought', 'resellers'));
ALTER TABLE users DROP COLUMN IF EXISTS announce_asked_at, DROP COLUMN IF EXISTS announce_optin;
-- +goose StatementEnd
