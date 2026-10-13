-- +goose Up
-- +goose StatementBegin
-- Phase 8: the bot's Telegram profile photo, uploaded on the Branding page
-- and kept beside the logo. Core stores it as a square JPEG (Telegram takes
-- only JPEG profile photos) and the bot sets it in Telegram while
-- general.bot_profile is on. 00008 declared the name check inline, so it has
-- Postgres's default name.
ALTER TABLE assets
    DROP CONSTRAINT assets_name_check,
    ADD CONSTRAINT assets_name_check CHECK (name IN ('logo', 'bot_photo')),
    ADD CONSTRAINT assets_bot_photo_jpeg CHECK (name <> 'bot_photo' OR content_type = 'image/jpeg');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM assets WHERE name = 'bot_photo';
ALTER TABLE assets
    DROP CONSTRAINT IF EXISTS assets_bot_photo_jpeg,
    DROP CONSTRAINT IF EXISTS assets_name_check,
    ADD CONSTRAINT assets_name_check CHECK (name IN ('logo'));
-- +goose StatementEnd
