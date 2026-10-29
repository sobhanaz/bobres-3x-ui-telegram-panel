-- +goose Up
-- +goose StatementBegin
-- My codes (the customer's own list of codes): a customer's reward codes are read by user_id, newest first. Only a code made for a buyer
-- has one, so the index leaves out the codes staff made for everyone. It adds no column and changes no data.
CREATE INDEX IF NOT EXISTS discount_codes_user_idx ON discount_codes (user_id, created_at DESC) WHERE user_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS discount_codes_user_idx;
-- +goose StatementEnd
