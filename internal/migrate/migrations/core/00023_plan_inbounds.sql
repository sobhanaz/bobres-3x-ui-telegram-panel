-- +goose Up
-- +goose StatementBegin
-- A plan may name, per server, the inbounds its new services are attached to.
-- No row for a (plan, server) means the provisioner's default for that
-- server: every inbound enabled in the panel and not detached in BOBRES. Like
-- plan_servers, server_id is an opaque reference to the provisioner's
-- xui_servers and inbound_id is the panel's own inbound id (core never reads
-- those schemas): one that is gone from the panel stays here until staff take
-- it out, and provisioning then fails with a clear reason.
CREATE TABLE plan_inbounds (
    plan_id    uuid   NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    server_id  uuid   NOT NULL,
    inbound_id int    NOT NULL CHECK (inbound_id > 0),
    PRIMARY KEY (plan_id, server_id, inbound_id)
);
CREATE INDEX plan_inbounds_server_idx ON plan_inbounds (server_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plan_inbounds;
-- +goose StatementEnd
