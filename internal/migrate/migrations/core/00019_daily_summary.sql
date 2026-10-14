-- +goose Up
-- +goose StatementBegin
-- The daily sales summary (docs/14-reports.md): once a local day, core tells the
-- staff who may read the reports how yesterday went. A row here is the claim on
-- one day's summary, written in the same transaction as the outbox events that
-- carry it: a crash between the two can neither lose the message nor send it
-- twice, a restart finds the claim, and so does a second claimer.
-- day is the store-local date the summary is about (not the day it is sent), zone
-- the time zone that date was read in and recipients how many staff members the
-- bot was told to message. Nothing else reads this table.
CREATE TABLE IF NOT EXISTS daily_summary_claims (
    day        date PRIMARY KEY,
    zone       text NOT NULL CHECK (zone <> '' AND length(zone) <= 64),
    recipients integer NOT NULL CHECK (recipients > 0),
    claimed_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS daily_summary_claims;
-- +goose StatementEnd
