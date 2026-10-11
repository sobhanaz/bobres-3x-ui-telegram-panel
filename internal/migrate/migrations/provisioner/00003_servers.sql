-- +goose Up
-- +goose StatementBegin
-- Several panels, managed from the dashboard.
--   last_health_ok/last_error/last_checked_at: the last health check (NULL ok =
--     never checked). Checks do not touch updated_at, which keys the cached
--     panel clients.
--   weight: share of new clients; the least clients/weight is picked.
--   managed: 'env' = seeded from the installer's environment, 'dashboard' =
--     added or edited in the dashboard. The environment only seeds an empty
--     table; it never overwrites a row.
ALTER TABLE xui_servers
    ADD COLUMN last_health_ok boolean,
    ADD COLUMN last_error text NOT NULL DEFAULT '',
    ADD COLUMN last_checked_at timestamptz,
    ADD COLUMN weight int NOT NULL DEFAULT 100 CONSTRAINT xui_servers_weight_check CHECK (weight BETWEEN 1 AND 1000),
    ADD COLUMN managed text NOT NULL DEFAULT 'env' CONSTRAINT xui_servers_managed_check CHECK (managed IN ('env', 'dashboard'));

-- Names were never unique: keep the oldest of each name and suffix the others.
UPDATE xui_servers s SET name = s.name || ' (' || left(s.id::text, 8) || ')'
WHERE EXISTS (
    SELECT 1 FROM xui_servers o
    WHERE o.name = s.name AND (o.created_at, o.id) < (s.created_at, s.id));
ALTER TABLE xui_servers ADD CONSTRAINT xui_servers_name_key UNIQUE (name);

-- pending: a reservation written before the panel is called, so a retried
-- create goes to the same server (and finds, or adopts, its client there).
-- It becomes a client (pending false) once the panel has it.
ALTER TABLE client_map ADD COLUMN pending boolean NOT NULL DEFAULT false;

-- Inbounds new clients are attached to. A missing row means attach; the
-- panel's own enable flag still applies.
CREATE TABLE server_inbounds (
    server_id uuid NOT NULL REFERENCES xui_servers (id) ON DELETE CASCADE,
    inbound_id int NOT NULL,
    attach boolean NOT NULL DEFAULT true,
    PRIMARY KEY (server_id, inbound_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The old code has no pending state and would take a reservation for a
-- delivered client; without the row a retry starts over.
DELETE FROM client_map WHERE pending;
DROP TABLE IF EXISTS server_inbounds;
ALTER TABLE client_map DROP COLUMN IF EXISTS pending;
ALTER TABLE xui_servers DROP CONSTRAINT IF EXISTS xui_servers_name_key;
ALTER TABLE xui_servers
    DROP COLUMN IF EXISTS managed,
    DROP COLUMN IF EXISTS weight,
    DROP COLUMN IF EXISTS last_checked_at,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS last_health_ok;
-- +goose StatementEnd
