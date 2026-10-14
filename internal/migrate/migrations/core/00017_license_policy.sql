-- +goose Up
-- +goose StatementBegin
-- The license policy ("free core, paid premium"): every install has a
-- 30-day implicit trial that starts the first time a version with this
-- policy runs. trial_started_at is that moment, written once by this
-- migration: an upgraded install and a fresh one both get it when they first
-- start with this version (the default applies to the existing row, and to
-- the row 00012 made on a fresh install). Nothing ever updates it.
--
-- limit_hit_at is when a new purchase was last refused because the
-- install is at its active-services limit; the watchdog turns it into one
-- staff alert a day (license_limit).
ALTER TABLE install
    ADD COLUMN trial_started_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN limit_hit_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE install
    DROP COLUMN IF EXISTS limit_hit_at,
    DROP COLUMN IF EXISTS trial_started_at;
-- +goose StatementEnd
