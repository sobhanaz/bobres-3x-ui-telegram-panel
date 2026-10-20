-- +goose Up
-- +goose StatementBegin
-- The channels and groups a customer must join besides the first one
-- (docs/03-bot-ux.md, "Required chats"). The FIRST required chat stays where
-- it always was: the settings join.channel and join.link (and join.label, its
-- optional name), which the bot's /set editor, the Mini App and every older
-- bot read. This table holds the EXTRA ones, in the order customers are asked:
-- position 1 is the first extra, right after that setting.
-- chat is what the setting join.channel takes (a public chat's @name, or a
-- private chat's -100... id; one chat once, the case of an @name aside), link
-- the https://t.me/... invite link of the Join button (a private chat needs
-- it), label a short name customers see on the button (empty: its @name).
-- How many of them are enforced is the license's: only the first N chats in
-- all, the setting's chat included, count (max_join_chats); the rest stay saved,
-- never deleted, and count again when the limit allows. Core checks every value
-- before it writes (domain.SetJoinChats); the checks here are the last line.
CREATE TABLE IF NOT EXISTS join_chats (
    position integer PRIMARY KEY CHECK (position >= 1),
    chat     text NOT NULL CHECK (chat ~ '^(@[A-Za-z][A-Za-z0-9_]{4,31}|-100[0-9]{5,15})$'),
    link     text NOT NULL DEFAULT '' CHECK (char_length(link) <= 1000),
    label    text NOT NULL DEFAULT '' CHECK (char_length(label) <= 32),
    CONSTRAINT join_chats_private_needs_link CHECK (chat !~ '^-100' OR link <> '')
);
CREATE UNIQUE INDEX IF NOT EXISTS join_chats_chat_key ON join_chats (lower(chat));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS join_chats;
-- +goose StatementEnd
