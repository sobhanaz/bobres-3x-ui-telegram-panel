-- +goose Up
-- +goose StatementBegin
-- P8: the HTTPS setting (one row). It holds only a state Caddy accepted:
-- core asks the tls-agent to apply a change and commits it here once the
-- agent says Caddy loaded it. mode 'auto' is Caddy's automatic HTTPS for
-- the domain, 'ip' also serves the server's public IP (ip) with a Let's
-- Encrypt short-lived certificate, 'manual' serves the operator's own
-- certificate chain (cert_pem) and private key, sealed with core's master
-- key (key_sealed: the key is never stored in clear). Leaving manual mode
-- drops the pair.
CREATE TABLE tls_config (
    id         smallint PRIMARY KEY CHECK (id = 1),
    mode       text NOT NULL DEFAULT 'auto' CHECK (mode IN ('auto', 'ip', 'manual')),
    ip         text CHECK (ip <> '' AND length(ip) <= 64),
    cert_pem   text CHECK (cert_pem <> '' AND length(cert_pem) <= 65536),
    key_sealed bytea CHECK (length(key_sealed) BETWEEN 1 AND 70000),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- ip mode has its address and only ip mode; manual mode has its whole
    -- pair and only manual mode
    CONSTRAINT tls_config_ip CHECK ((mode = 'ip') = (ip IS NOT NULL)),
    CONSTRAINT tls_config_pair CHECK ((mode = 'manual') = (cert_pem IS NOT NULL)
        AND (cert_pem IS NULL) = (key_sealed IS NULL))
);
INSERT INTO tls_config (id) VALUES (1);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tls_config;
-- +goose StatementEnd
