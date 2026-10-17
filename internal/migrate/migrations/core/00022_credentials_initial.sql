-- +goose Up
-- +goose StatementBegin
-- First login with an initial password (bobres install, bobres admin
-- password): the owner needs no /start in the bot. The server's command line
-- sets a random password and marks the login must_change; with it (and no
-- authenticator code, since none is set up yet) a RESTRICTED session opens
-- (method 'password_initial') that can do nothing but choose a new password
-- and enrol an authenticator app. Confirming the first code clears the
-- flag and the session becomes a full 'password' one.
ALTER TABLE staff_credentials ADD COLUMN must_change boolean NOT NULL DEFAULT false;

ALTER TABLE web_sessions DROP CONSTRAINT web_sessions_method_check;
ALTER TABLE web_sessions ADD CONSTRAINT web_sessions_method_check
    CHECK (method IN ('link', 'password', 'password_initial'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- A restricted session is ended, never turned into a full one.
DELETE FROM web_sessions WHERE method = 'password_initial';
ALTER TABLE web_sessions DROP CONSTRAINT web_sessions_method_check;
ALTER TABLE web_sessions ADD CONSTRAINT web_sessions_method_check CHECK (method IN ('link', 'password'));
ALTER TABLE staff_credentials DROP COLUMN must_change;
-- +goose StatementEnd
