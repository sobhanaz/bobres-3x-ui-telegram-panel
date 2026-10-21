-- +goose Up
-- +goose StatementBegin
-- What the signed admin API (/admin/v1, docs/13-license-server.md) needs to be safe to retry.

-- Every change the API makes carries a caller-chosen reference, and a reference works once: a retry of the same
-- request (a timeout, a restart in the middle) finds the first answer here and changes nothing again. The result
-- never holds a license key.
CREATE TABLE licensesrv.admin_ops (
    ref        text PRIMARY KEY CHECK (ref ~ '^[A-Za-z0-9._:-]{8,100}$'),
    action     text NOT NULL CHECK (action <> ''),
    license_id text REFERENCES licensesrv.licenses (id),
    result     jsonb NOT NULL DEFAULT '{}',
    at         timestamptz NOT NULL
);

-- The one-time values of signed requests: a recorded request cannot be played again.
CREATE TABLE licensesrv.admin_nonces (
    nonce      text PRIMARY KEY CHECK (nonce ~ '^[A-Za-z0-9_-]{16,64}$'),
    expires_at timestamptz NOT NULL
);
CREATE INDEX admin_nonces_expiry_idx ON licensesrv.admin_nonces (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS licensesrv.admin_nonces;
DROP TABLE IF EXISTS licensesrv.admin_ops;
-- +goose StatementEnd
