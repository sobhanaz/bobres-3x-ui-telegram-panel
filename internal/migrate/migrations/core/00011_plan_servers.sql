-- +goose Up
-- +goose StatementBegin
-- Phase 4, milestone 6a: several 3x-ui servers. A plan may name the servers
-- its new services go to; no row means any server. server_id is an opaque
-- reference to the provisioner's xui_servers (core never reads that schema):
-- core refuses to delete a server a plan names, and one removed another way
-- stays named here until staff take it out.
CREATE TABLE plan_servers (
    plan_id   uuid NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    server_id uuid NOT NULL,
    PRIMARY KEY (plan_id, server_id)
);
CREATE INDEX plan_servers_server_idx ON plan_servers (server_id);

-- A server's subscription address changed: its services' stored links are
-- fetched again in the background. The time it was marked, so a link fetched
-- before a second change is not taken for the new one; a fetch the panel did
-- not answer moves it to the time of the next try, and counts the attempt.
ALTER TABLE subscriptions ADD COLUMN sub_link_stale_at timestamptz;
ALTER TABLE subscriptions ADD COLUMN sub_link_attempts int NOT NULL DEFAULT 0;
CREATE INDEX subscriptions_sub_link_stale_idx ON subscriptions (sub_link_stale_at)
    WHERE sub_link_stale_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS subscriptions_sub_link_stale_idx;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS sub_link_attempts;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS sub_link_stale_at;
DROP TABLE IF EXISTS plan_servers;
-- +goose StatementEnd
