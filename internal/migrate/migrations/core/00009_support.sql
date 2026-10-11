-- +goose Up
-- +goose StatementBegin
-- Phase 4, milestone 4: support tickets answered through the bot, broadcasts.

-- Tickets become conversations: one open per customer, numbered, with messages.
ALTER TABLE tickets RENAME COLUMN text TO subject;
ALTER TABLE tickets ADD COLUMN number bigint GENERATED ALWAYS AS IDENTITY;
ALTER TABLE tickets DROP CONSTRAINT tickets_status_check;
ALTER TABLE tickets ADD CONSTRAINT tickets_status_check CHECK (status IN ('open', 'answered', 'closed'));
ALTER TABLE tickets
    ADD COLUMN assigned_to      uuid REFERENCES users (id),
    ADD COLUMN updated_at       timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN last_customer_at timestamptz,
    ADD COLUMN last_staff_at    timestamptz,
    ADD COLUMN closed_at        timestamptz,
    ADD COLUMN closed_by        uuid REFERENCES users (id), -- NULL on a closed ticket: the system
    ADD COLUMN close_reason     text CHECK (close_reason IN ('staff', 'customer', 'auto', 'merged'));

CREATE TABLE ticket_messages (
    id              uuid PRIMARY KEY,
    ticket_id       uuid NOT NULL REFERENCES tickets (id),
    kind            text NOT NULL CHECK (kind IN ('customer', 'staff', 'note')),
    author_id       uuid REFERENCES users (id),
    body            text NOT NULL DEFAULT '' CHECK (char_length(body) <= 4096),
    file_id         text,
    file_type       text CHECK (file_type IN ('photo', 'document')),
    file_mime       text,
    file_name       text,
    file_size       integer,
    tg_message_id   bigint, -- the customer's message, or the delivered copy of a staff reply
    idempotency_key text UNIQUE,
    delivery        text CHECK (delivery IN ('pending', 'sent', 'blocked', 'failed')), -- staff replies only
    delivered_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (body <> '' OR file_id IS NOT NULL)
);

-- Existing tickets: their text becomes the first message; only each
-- customer's newest open ticket stays open.
INSERT INTO ticket_messages (id, ticket_id, kind, author_id, body, created_at)
SELECT gen_random_uuid(), t.id, 'customer', t.user_id, left(t.subject, 4096), t.created_at FROM tickets t;
UPDATE tickets SET last_customer_at = created_at, updated_at = created_at;
UPDATE tickets t SET status = 'closed', closed_at = now(), close_reason = 'merged'
WHERE t.status = 'open' AND EXISTS (
    SELECT 1 FROM tickets n WHERE n.user_id = t.user_id AND n.status = 'open'
      AND (n.created_at, n.id) > (t.created_at, t.id));
UPDATE tickets SET subject = left(split_part(subject, E'\n', 1), 100);

CREATE UNIQUE INDEX tickets_number_idx ON tickets (number);
CREATE UNIQUE INDEX tickets_one_open_idx ON tickets (user_id) WHERE status <> 'closed';
CREATE INDEX tickets_status_idx ON tickets (status, last_customer_at);
CREATE INDEX tickets_user_idx ON tickets (user_id, created_at DESC);
CREATE INDEX tickets_assigned_idx ON tickets (assigned_to) WHERE status <> 'closed';
CREATE INDEX ticket_messages_ticket_idx ON ticket_messages (ticket_id, created_at);
CREATE INDEX ticket_messages_tg_idx ON ticket_messages (tg_message_id) WHERE tg_message_id IS NOT NULL;

-- Saved answers staff pick from, per language.
CREATE TABLE ticket_canned (
    id         uuid PRIMARY KEY,
    title      text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 64),
    body       jsonb NOT NULL, -- {"fa": "...", "en": "..."}
    sort       integer NOT NULL DEFAULT 0,
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Broadcasts. Users who blocked the bot are left out of them.
ALTER TABLE users ADD COLUMN bot_blocked_at timestamptz;

CREATE TABLE broadcasts (
    id            uuid PRIMARY KEY,
    status        text NOT NULL CHECK (status IN ('draft', 'scheduled', 'running', 'paused', 'done', 'cancelled')),
    segment       text NOT NULL CHECK (segment IN ('all', 'active', 'expired', 'never_bought')),
    language      text NOT NULL DEFAULT '' CHECK (language IN ('', 'fa', 'en')),
    texts         jsonb NOT NULL, -- {"fa": "...", "en": "..."}
    button_url    text,
    button_labels jsonb,          -- {"fa": "...", "en": "..."}; NULL: no button
    scheduled_at  timestamptz,
    started_at    timestamptz,
    finished_at   timestamptz,
    total         integer NOT NULL DEFAULT 0,
    sent          integer NOT NULL DEFAULT 0,
    blocked       integer NOT NULL DEFAULT 0,
    failed        integer NOT NULL DEFAULT 0,
    unknown       integer NOT NULL DEFAULT 0,
    last_error    text,
    version       integer NOT NULL DEFAULT 1,
    created_by    uuid NOT NULL REFERENCES users (id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX broadcasts_due_idx ON broadcasts (scheduled_at) WHERE status = 'scheduled';
CREATE INDEX broadcasts_created_idx ON broadcasts (created_at DESC);

CREATE TABLE broadcast_recipients (
    broadcast_id uuid NOT NULL REFERENCES broadcasts (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id),
    telegram_id  bigint NOT NULL,
    language     text NOT NULL,
    status       text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'claimed', 'sent', 'blocked', 'failed', 'unknown', 'cancelled')),
    claimed_at   timestamptz,
    done_at      timestamptz,
    message_id   bigint,
    error        text,
    PRIMARY KEY (broadcast_id, user_id)
);
CREATE INDEX broadcast_recipients_todo_idx ON broadcast_recipients (broadcast_id, telegram_id)
    WHERE status IN ('pending', 'claimed');
CREATE INDEX broadcast_recipients_status_idx ON broadcast_recipients (broadcast_id, status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS broadcast_recipients;
DROP TABLE IF EXISTS broadcasts;
ALTER TABLE users DROP COLUMN IF EXISTS bot_blocked_at;
DROP TABLE IF EXISTS ticket_canned;
DROP TABLE IF EXISTS ticket_messages;
DROP INDEX IF EXISTS tickets_assigned_idx;
DROP INDEX IF EXISTS tickets_user_idx;
DROP INDEX IF EXISTS tickets_status_idx;
DROP INDEX IF EXISTS tickets_one_open_idx;
DROP INDEX IF EXISTS tickets_number_idx;
ALTER TABLE tickets
    DROP COLUMN IF EXISTS close_reason, DROP COLUMN IF EXISTS closed_by, DROP COLUMN IF EXISTS closed_at,
    DROP COLUMN IF EXISTS last_staff_at, DROP COLUMN IF EXISTS last_customer_at, DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS assigned_to;
UPDATE tickets SET status = 'open' WHERE status = 'answered';
ALTER TABLE tickets DROP CONSTRAINT tickets_status_check;
ALTER TABLE tickets ADD CONSTRAINT tickets_status_check CHECK (status IN ('open', 'closed'));
ALTER TABLE tickets DROP COLUMN IF EXISTS number;
ALTER TABLE tickets RENAME COLUMN subject TO text;
-- +goose StatementEnd
