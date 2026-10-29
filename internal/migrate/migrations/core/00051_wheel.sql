-- +goose Up
-- +goose StatementBegin
-- The daily wheel of fortune (docs/01-database.md): a customer who has made enough paid orders spins once per store-local day for a chance at wallet
-- credit. A spin is one row, whatever it won: the primary key of the day is (user, day), so a second spin on the same day cannot be written, and a
-- prize is a ledger entry of kind wheel_prize under the key wheel:<user id>:<day> that the row points at, written in the same transaction.
--
-- The day's budget is not a counter: it is what the day's rows add up to, read under a lock on the day, so there is no second number that could
-- drift from the ledger.
CREATE TABLE wheel_spins (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users (id),
    -- The store-local calendar day of the spin (the zone of the reports), not the instant.
    day             date NOT NULL,
    -- Toman won; 0 when the wheel stopped on "no prize".
    prize           bigint NOT NULL CHECK (prize >= 0),
    -- The credit it paid: there if and only if there was a prize.
    ledger_entry_id uuid UNIQUE REFERENCES ledger_entries (id),
    -- What was drawn: a number from 0 to total_weight - 1 out of the weights in force at that moment, so a result can be explained afterwards.
    roll            bigint NOT NULL CHECK (roll >= 0),
    total_weight    bigint NOT NULL CHECK (total_weight > 0),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT wheel_spins_once_a_day UNIQUE (user_id, day),
    CONSTRAINT wheel_spins_roll_in_range CHECK (roll < total_weight),
    CONSTRAINT wheel_spins_prize_has_entry CHECK ((prize > 0) = (ledger_entry_id IS NOT NULL))
);
CREATE INDEX wheel_spins_day_idx ON wheel_spins (day);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS wheel_spins;
-- The ledger keeps allowing 'wheel_prize': it is append-only, so the prizes already paid cannot be removed.
-- +goose StatementEnd
