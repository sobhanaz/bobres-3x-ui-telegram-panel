-- +goose Up
-- +goose StatementBegin
-- Plan groups: staff put plans into named groups and write a text for each group. In the bot the customer first picks a
-- group, reads its text, then picks a plan of it. A plan with no group is listed under "Other plans" (or alone, when no
-- group exists: a store that never makes a group sells exactly as before).
CREATE TABLE plan_groups (
    id               uuid PRIMARY KEY,
    name_i18n        jsonb NOT NULL,
    description_i18n jsonb NOT NULL DEFAULT '{}'::jsonb,
    sort             int NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Deleting a group frees its plans: they stay on sale, ungrouped.
ALTER TABLE plans ADD COLUMN group_id uuid REFERENCES plan_groups (id) ON DELETE SET NULL;
CREATE INDEX plans_group_idx ON plans (group_id) WHERE group_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS plans_group_idx;
ALTER TABLE plans DROP COLUMN IF EXISTS group_id;
DROP TABLE IF EXISTS plan_groups;
-- +goose StatementEnd
