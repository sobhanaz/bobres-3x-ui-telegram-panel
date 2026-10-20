-- +goose Up
-- +goose StatementBegin
-- Help content the owner writes in the dashboard and the bot shows (docs/03-bot-ux.md,
-- "Help content"): the questions of the FAQ under Support and the tutorials behind the
-- Tutorials button, in Persian and English. One table for both kinds.
CREATE TABLE help_entries (
    id         uuid PRIMARY KEY,
    kind       text NOT NULL CHECK (kind IN ('faq', 'tutorial')),
    -- By language, {"fa": "...", "en": "..."}: the question, or the tutorial's title.
    -- Plain text on one line (the bot escapes it where it goes into HTML).
    title      jsonb NOT NULL,
    -- By language: the answer, or the tutorial's text. The limited HTML the bot's
    -- own texts allow (Telegram's subset, https links only); core checks it on every write.
    body       jsonb NOT NULL,
    -- Tutorials only: [{"url": "https://...", "label": {"fa": "...", "en": "..."}}], at most 8.
    buttons    jsonb NOT NULL DEFAULT '[]',
    -- The order customers see (lowest first; equal values keep the order they were made in).
    sort       integer NOT NULL DEFAULT 0,
    enabled    boolean NOT NULL DEFAULT true,
    -- Bumped by every change of the content, so an edit made from an old copy is refused.
    version    integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users (id),
    CONSTRAINT help_entries_shape CHECK (
        jsonb_typeof(title) = 'object' AND jsonb_typeof(body) = 'object' AND jsonb_typeof(buttons) = 'array'
        AND jsonb_array_length(buttons) <= 8
        AND (kind = 'tutorial' OR jsonb_array_length(buttons) = 0)),
    -- An entry is readable in at least one language: a title and a text in it.
    CONSTRAINT help_entries_readable CHECK (
        (coalesce(title ->> 'fa', '') <> '' AND coalesce(body ->> 'fa', '') <> '')
        OR (coalesce(title ->> 'en', '') <> '' AND coalesce(body ->> 'en', '') <> ''))
);
CREATE INDEX help_entries_order_idx ON help_entries (kind, sort, created_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS help_entries;
-- The default content is offered once per store, and remembered in settings: forget it, so that
-- going up again offers it again.
DELETE FROM settings WHERE key = 'state.help.default_applied';
-- +goose StatementEnd
