-- +goose Up
-- +goose StatementBegin
-- Phase 7: system alerts. Core's watchdog checks the store every minute and
-- keeps each check's last state here, so staff are told when a check goes
-- wrong and when it is fixed, not every minute, and a restart neither
-- repeats nor forgets an open problem. A check seen failing once is
-- 'pending'; seen failing again it is a 'problem' (the alert is sent).
-- A problem is fixed only once it has passed for a few minutes:
-- passing_since is when it first passed again (NULL while it fails).
-- title_key and args are the bot's text (alert.*) and its values, so the
-- dashboard and the Telegram message name the problem the same way.
CREATE TABLE alert_state (
    key           text PRIMARY KEY CHECK (key <> '' AND length(key) <= 200),
    state         text NOT NULL CHECK (state IN ('ok', 'pending', 'problem')),
    since         timestamptz NOT NULL DEFAULT now(),
    last_sent_at  timestamptz,
    passing_since timestamptz,
    detail        text NOT NULL DEFAULT '' CHECK (length(detail) <= 1000),
    severity      text NOT NULL DEFAULT 'warning' CHECK (severity IN ('critical', 'warning')),
    title_key     text NOT NULL DEFAULT '' CHECK (length(title_key) <= 100),
    args          jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(args) = 'object'),
    -- only a problem can be passing again
    CHECK (passing_since IS NULL OR state = 'problem')
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS alert_state;
-- +goose StatementEnd
