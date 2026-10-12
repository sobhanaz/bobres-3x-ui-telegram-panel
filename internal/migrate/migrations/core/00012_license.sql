-- +goose Up
-- +goose StatementBegin
-- Phase 4, milestone 6b: the license. core.install holds this install's id,
-- made once when the schema is created (a vendor binds a license to it);
-- the expression index allows only one row.
CREATE TABLE install (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX install_one_row ON install ((true));
INSERT INTO install DEFAULT VALUES;

-- The installed license token (at most one). It is verified every time it is
-- read, against the public keys built into core, so a row here proves
-- nothing by itself.
CREATE TABLE license (
    id         int PRIMARY KEY CHECK (id = 1),
    token      text NOT NULL CHECK (token <> '' AND length(token) <= 16384),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users (id) ON DELETE SET NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS license;
DROP TABLE IF EXISTS install;
-- +goose StatementEnd
