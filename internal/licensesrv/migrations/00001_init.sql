-- +goose Up
-- +goose StatementBegin
-- The vendor license server's own database (docs/13-license-server.md). It
-- is never part of a customer install. Every name is schema-qualified, so
-- nothing depends on search_path; the schema itself is made by Migrate.

-- One row per sold license. The activation key is secret and shown once:
-- only its SHA-256 is kept (hex), so a database leak gives no usable key.
CREATE TABLE licensesrv.licenses (
    id           text PRIMARY KEY CHECK (id ~ '^lic_[a-z0-9_-]{1,60}$'),
    key_hash     text NOT NULL UNIQUE CHECK (key_hash ~ '^[0-9a-f]{64}$'),
    customer     text NOT NULL CHECK (customer <> '' AND length(customer) <= 200),
    tier         text NOT NULL CHECK (tier IN ('trial', 'standard', 'pro')),
    entitlements jsonb NOT NULL CHECK (jsonb_typeof(entitlements) = 'object'),
    ends_at      timestamptz, -- NULL: the license has no end date
    grace_days   integer NOT NULL DEFAULT 14 CHECK (grace_days BETWEEN 0 AND 90),
    status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    revoked_at   timestamptz,
    reason       text CHECK (length(reason) <= 500),
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);

-- Where a license runs. One active row per license; moving to another
-- (install id, domain) pair deactivates the old row and, after the first
-- activation, counts against the re-activation limit (counted).
CREATE TABLE licensesrv.activations (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    license_id     text NOT NULL REFERENCES licensesrv.licenses (id),
    install_id     uuid NOT NULL,
    domain         text NOT NULL CHECK (domain <> '' AND length(domain) <= 253),
    counted        boolean NOT NULL,
    activated_at   timestamptz NOT NULL,
    last_seen_at   timestamptz NOT NULL,
    last_version   text NOT NULL DEFAULT '' CHECK (length(last_version) <= 64),
    deactivated_at timestamptz,
    CHECK (deactivated_at IS NULL OR deactivated_at >= activated_at)
);
CREATE UNIQUE INDEX activations_one_active ON licensesrv.activations (license_id)
    WHERE deactivated_at IS NULL;
CREATE INDEX activations_license_idx ON licensesrv.activations (license_id, activated_at DESC);

-- What happened to each license: made, changed, revoked, activated, moved,
-- refused. Never a key or a token. Append-only, like core's audit log.
CREATE TABLE licensesrv.audit (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at         timestamptz NOT NULL,
    actor      text NOT NULL CHECK (actor <> ''),
    action     text NOT NULL CHECK (action <> ''),
    license_id text REFERENCES licensesrv.licenses (id),
    ip         text NOT NULL DEFAULT '',
    details    jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_license_idx ON licensesrv.audit (license_id, at DESC);

CREATE FUNCTION licensesrv.audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'licensesrv.audit is append-only';
END
$$;
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON licensesrv.audit
    FOR EACH ROW EXECUTE FUNCTION licensesrv.audit_append_only();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON licensesrv.audit
    FOR EACH STATEMENT EXECUTE FUNCTION licensesrv.audit_append_only();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS licensesrv.audit;
DROP FUNCTION IF EXISTS licensesrv.audit_append_only();
DROP TABLE IF EXISTS licensesrv.activations;
DROP TABLE IF EXISTS licensesrv.licenses;
-- +goose StatementEnd
