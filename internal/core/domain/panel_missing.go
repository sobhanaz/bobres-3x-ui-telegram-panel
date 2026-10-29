package domain

import (
	"context"
	"errors"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// What the owner can do about a service whose client the panel no longer has (an operator deleted it there).
const (
	PanelMissingRecreate = "recreate" // make the client again (RecreateSubscriptionClient)
	PanelMissingDelete   = "delete"   // delete the service from the bot too (DeleteSubscription)
)

// The outcomes of ResolvePanelMissing. Back and Deleted are skips: the service changed since it was listed,
// and nothing was done to it.
const (
	PanelMissingDone    = "done"
	PanelMissingBack    = "back"    // the panel has the client again (or the service is no longer live)
	PanelMissingDeleted = "deleted" // the service was deleted already
)

// ResolvePanelMissing makes the client of a service the panel lacks again, or deletes the service, as
// RecreateSubscriptionClient and DeleteSubscription do (the same checks, the same audit entries), but only
// while the service is still one the panel lacks: one whose client came back since it was listed is left
// alone (a delete would remove the client the panel has again), and one deleted meanwhile is not touched
// again. The outcome says which.
func (s *Service) ResolvePanelMissing(ctx context.Context, actor *store.User, prov Provisioner, id, action, reason string) (string, error) {
	reason, err := needReason(reason)
	if err != nil {
		return "", err
	}
	if action != PanelMissingRecreate && action != PanelMissingDelete {
		return "", invalid("the action is recreate or delete")
	}
	if u, err := buuid.Parse(strings.TrimSpace(id)); err != nil || u.String() != id {
		return "", invalid("not a service id")
	}
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), id)
	if err != nil {
		return "", err
	}
	switch {
	case sub.Status == "deleted":
		return PanelMissingDeleted, nil
	case sub.PanelMissingSince == nil || !livePanelStatus(sub.Status):
		return PanelMissingBack, nil
	}
	if action == PanelMissingRecreate {
		err = s.RecreateSubscriptionClient(ctx, actor, prov, id, reason)
	} else {
		err = s.DeleteSubscription(ctx, actor, prov, id, reason)
	}
	if err != nil {
		return "", err
	}
	return PanelMissingDone, nil
}

// livePanelStatus: the statuses the usage worker asks the panel about (store's livePanelStatuses), the ones a
// "not on the panel" mark means something for.
func livePanelStatus(st string) bool {
	switch st {
	case "active", "expiring_soon", "expired", "depleted":
		return true
	}
	return false
}

// PanelMissingPage is a page of the services the panel lacks, and how many there are in all.
type PanelMissingPage struct {
	Services []store.SubscriptionRow
	Total    int64
}

// AdminListPanelMissing lists a page of the live services the panel has no client for, the longest missing
// first, for the bot's staff (owner and admin: services.write on the dashboard).
func (s *Service) AdminListPanelMissing(ctx context.Context, actorTelegramID int64, offset, limit int) (*PanelMissingPage, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	rows, total, err := s.st.ListPanelMissing(ctx, s.st.Conn(), store.Page{Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}
	return &PanelMissingPage{Services: rows, Total: total}, nil
}

// AdminResolvePanelMissing is ResolvePanelMissing for a staff member in the bot (owner and admin, as
// services.write on the dashboard). An unknown service is an outcome too (deleted), not an error: the
// button that named it is stale.
func (s *Service) AdminResolvePanelMissing(ctx context.Context, actorTelegramID int64, prov Provisioner, id, action, reason string) (string, *store.Subscription, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return "", nil, err
	}
	out, err := s.ResolvePanelMissing(ctx, actor, prov, id, action, reason)
	if errors.Is(err, store.ErrNotFound) {
		return PanelMissingDeleted, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	// The service as it is now, for the message (its email); the action stands when it cannot be read.
	var sub *store.Subscription
	if got, gerr := s.st.GetSubscription(ctx, s.st.Conn(), id); gerr == nil {
		sub = got
	}
	return out, sub, nil
}
