-- +goose Up
-- +goose StatementBegin
-- sub_base_url: public prefix of the panel's subscription server, e.g.
-- https://sub.example.com:2096/sub/ ; the user's link is sub_base_url || subId.
-- allow_private: the operator opted in to a panel on a private/loopback address.
ALTER TABLE xui_servers ADD COLUMN sub_base_url text;
ALTER TABLE xui_servers ADD COLUMN allow_private boolean NOT NULL DEFAULT false;

ALTER TABLE outbox_provisioner ADD COLUMN event_id uuid;
UPDATE outbox_provisioner SET event_id = gen_random_uuid() WHERE event_id IS NULL;
ALTER TABLE outbox_provisioner ALTER COLUMN event_id SET NOT NULL;
CREATE UNIQUE INDEX outbox_provisioner_event_id_idx ON outbox_provisioner (event_id);

CREATE TABLE outbox_provisioner_cursors (
    consumer text PRIMARY KEY,
    last_id bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbox_provisioner_cursors;
DROP INDEX IF EXISTS outbox_provisioner_event_id_idx;
ALTER TABLE outbox_provisioner DROP COLUMN IF EXISTS event_id;
ALTER TABLE xui_servers DROP COLUMN IF EXISTS allow_private;
ALTER TABLE xui_servers DROP COLUMN IF EXISTS sub_base_url;
-- +goose StatementEnd
