package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrClientNameTaken: another service has this config name (subscriptions.client_email is unique).
var ErrClientNameTaken = errors.New("store: config name already taken")

// subscriptionsEmailKey is the unique constraint on subscriptions.client_email.
const subscriptionsEmailKey = "subscriptions_client_email_key"

// unpaidNameHold is how long an unpaid order holds the config name chosen for it: long enough to pay, short enough that
// an order never paid does not keep a name from everyone else.
const unpaidNameHold = "1 day"

// ClientNameTaken reports whether a config name is someone's: a service has it (a deleted one too: its name stays in
// the records), other than exceptSubscription, or an order on its way to a service of another user than exceptUser
// chose it (paid and not yet delivered, or unpaid for less than a day). "" excepts nothing.
func (s *Store) ClientNameTaken(ctx context.Context, q querier, name, exceptUser, exceptSubscription string) (bool, error) {
	var taken bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM core.subscriptions WHERE client_email = $1 AND id::text <> $3)
			OR EXISTS (SELECT 1 FROM core.orders WHERE client_name = $1 AND user_id::text <> $2 AND (
				status IN ('paid', 'provisioning', 'provision_failed')
				OR (status IN ('created', 'awaiting_payment') AND created_at > now() - interval '`+unpaidNameHold+`')))`,
		name, exceptUser, exceptSubscription).Scan(&taken)
	if err != nil {
		return false, fmt.Errorf("client name taken: %w", err)
	}
	return taken, nil
}

// SetSubscriptionClientEmail gives a service a new config name: a rename, or the automatic name when the one chosen
// turned out taken on a panel. ErrClientNameTaken when another service has it, ErrNotFound for no such service.
func (s *Store) SetSubscriptionClientEmail(ctx context.Context, q querier, id, email string) error {
	tag, err := q.Exec(ctx, `UPDATE core.subscriptions SET client_email = $2 WHERE id = $1`, id, email)
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == subscriptionsEmailKey:
		return ErrClientNameTaken
	case err != nil:
		return fmt.Errorf("set client email: %w", err)
	case tag.RowsAffected() == 0:
		return ErrNotFound
	}
	return nil
}
