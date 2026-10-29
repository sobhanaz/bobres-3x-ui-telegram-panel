-- +goose Up
-- +goose StatementBegin
-- Who is asked to approve a sign-in (login_approvals, 00046): the staff member themself and, for a staff member who is not an owner, the owners
-- (security.login_approval_notify_owner). Each approver has a nonce of their own, only its hash stored, carried by the buttons of the message
-- the bot sent them: a button works for that person and that sign-in only. message_id is the bot's message, so that the result can be written
-- into it (resolved_at: that was done, or is not needed: the approver's own tap did it); send_error is a short code (never Telegram's text)
-- when the bot could not deliver it.
CREATE TABLE login_approval_targets (
    approval_id uuid NOT NULL REFERENCES login_approvals (id) ON DELETE CASCADE,
    telegram_id bigint NOT NULL CHECK (telegram_id > 0),
    user_id     uuid NOT NULL REFERENCES users (id),
    role        text NOT NULL CHECK (role IN ('self', 'owner')),
    nonce_hash  bytea NOT NULL CHECK (octet_length(nonce_hash) = 32),
    sent_at     timestamptz,
    message_id  bigint,
    send_error  text CHECK (send_error IS NULL OR length(send_error) <= 40),
    resolved_at timestamptz,
    PRIMARY KEY (approval_id, telegram_id)
);
-- The messages still to write the result into (the worker looks at these only).
CREATE INDEX login_approval_targets_open_idx ON login_approval_targets (approval_id) WHERE message_id IS NOT NULL AND resolved_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS login_approval_targets;
-- +goose StatementEnd
