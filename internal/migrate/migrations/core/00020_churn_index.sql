-- +goose Up
-- +goose StatementBegin
-- Churn in the sales reports (docs/14-reports.md): services are counted by the
-- local date their current expiry falls on, so the report reads a range of
-- expires_at. Only the rows a report can count are indexed: a pending service
-- has no real expiry yet and a deleted one is gone (disabled ones are left out
-- of the report by its query, which is still inside this predicate). No table
-- and no column is added.
CREATE INDEX IF NOT EXISTS subscriptions_expires_idx ON subscriptions (expires_at)
    WHERE expires_at IS NOT NULL AND status NOT IN ('pending', 'deleted');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS subscriptions_expires_idx;
-- +goose StatementEnd
