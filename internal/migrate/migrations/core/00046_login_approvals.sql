-- +goose Up
-- +goose StatementBegin
-- Approving a dashboard sign-in in Telegram (docs/04-dashboard.md, "Signing in"). While security.login_approval is on, a staff sign-in (a login
-- link, or a password with its authenticator code) does not open a session: it makes one row here, pending for at most 5 minutes. An approver's
-- Approve in the bot turns it into a session at the browser's next poll; Deny, the clock or the owner switching the setting off end it. Nothing
-- usable exists before that, so an older core that knows nothing of this table can never mistake a pending sign-in for a session.
-- Only hashes are kept: the pending cookie's (ticket_hash); each approver's own nonce is in login_approval_targets (00047).
CREATE TABLE login_approvals (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users (id),
    method      text NOT NULL CHECK (method IN ('link', 'password', 'password_initial')),
    ticket_hash bytea NOT NULL UNIQUE CHECK (octet_length(ticket_hash) = 32),
    ip          text,
    user_agent  text,
    -- No earlier session of this account came from the address (said in the message the approver reads).
    new_address boolean NOT NULL DEFAULT false,
    status      text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'denied', 'expired', 'cancelled', 'undeliverable', 'consumed')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    -- Who decided and when (approved, denied), or when it ended otherwise; decided_via: the staff member themself or an owner.
    decided_at  timestamptz,
    decided_by  uuid REFERENCES users (id),
    decided_via text CHECK (decided_via IN ('self', 'owner')),
    -- The session was made from an approved sign-in (once).
    consumed_at timestamptz,
    CHECK (expires_at > created_at)
);
CREATE INDEX login_approvals_user_idx ON login_approvals (user_id, created_at DESC);
CREATE INDEX login_approvals_open_idx ON login_approvals (expires_at) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS login_approvals;
-- +goose StatementEnd
