-- +goose Up
-- +goose StatementBegin
-- Monitoring time series (docs/04-dashboard.md, Monitoring). Core samples
-- about a hundred metrics every 30 seconds; one row per sample would be
-- millions of rows, so a row holds a block of one metric: 120 slots of 30
-- seconds (one hour) at full resolution (step 30), or 288 slots of 5 minutes
-- (one day) once rolled up (step 300). A slot nobody sampled is NULL (a
-- source that is unavailable records nothing, never a zero). real, not double:
-- a graph does not need more digits.
--
-- bucket is the block's first second (unix, UTC, a multiple of 3600 for step
-- 30 and of 86400 for step 300); the slot i (1-based) is the time
-- bucket + (i-1) * step. A background job rolls a day whose end is more than 7
-- days old into one step-300 block per metric and drops the step-30 blocks of
-- that day in the same transaction, and drops step-300 blocks that ended more
-- than 30 days ago, so the two steps never overlap.
CREATE TABLE IF NOT EXISTS monitor_metric (
    id   integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (name <> '' AND char_length(name) <= 120)
);

CREATE TABLE IF NOT EXISTS monitor_series (
    metric_id integer  NOT NULL REFERENCES monitor_metric (id) ON DELETE CASCADE,
    step      smallint NOT NULL CHECK (step IN (30, 300)),
    bucket    bigint   NOT NULL CHECK (bucket >= 0),
    vals      real[]   NOT NULL CHECK (array_length(vals, 1) = CASE step WHEN 30 THEN 120 ELSE 288 END),
    PRIMARY KEY (metric_id, step, bucket)
);
CREATE INDEX IF NOT EXISTS monitor_series_bucket_idx ON monitor_series (step, bucket);

-- The Monitoring page's event list reads the watchdog's alert events
-- (system.alert.v1) from core's outbox, which is kept for ever and has no index
-- on topic. A partial index on the one topic keeps "the newest alerts" a short
-- walk of the index however large the outbox grows.
CREATE INDEX IF NOT EXISTS outbox_core_alerts_idx ON outbox_core (id) WHERE topic = 'system.alert.v1';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS outbox_core_alerts_idx;
DROP TABLE IF EXISTS monitor_series;
DROP TABLE IF EXISTS monitor_metric;
-- +goose StatementEnd
