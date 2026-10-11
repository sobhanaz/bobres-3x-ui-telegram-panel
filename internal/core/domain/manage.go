package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Staff operations on services and orders (the dashboard). The caller has
// checked that the actor's role allows each one; each is audited with the
// staff member's reason.

// Limits on a staff member's extension of a service.
const (
	maxExtendDays  = 3650
	maxExtendBytes = int64(100) << 40 // 100 TiB
)

// ExtendParams is a staff member's extension of a service.
type ExtendParams struct {
	SubscriptionID string
	Days           int32
	Bytes          int64
	Reason         string
	IdempotencyKey string
}

func needReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", invalid("a reason is required")
	}
	if len(reason) > 500 {
		return "", invalid("the reason is too long")
	}
	return reason, nil
}

// ExtendSubscription adds days and/or traffic to a service through a free,
// already-paid renewal (a top-up when it adds traffic only). The provisioning
// worker applies it in turn with the customer's own renewals, a retry never
// adds twice, and the bot tells the customer as for a renewal they bought.
func (s *Service) ExtendSubscription(ctx context.Context, actor *store.User, p ExtendParams) (*store.Order, error) {
	reason, err := needReason(p.Reason)
	if err != nil {
		return nil, err
	}
	switch {
	case p.Days < 0 || p.Days > maxExtendDays || p.Bytes < 0 || p.Bytes > maxExtendBytes:
		return nil, invalid("extension out of range")
	case p.Days == 0 && p.Bytes == 0:
		return nil, invalid("add days, traffic or both")
	case p.IdempotencyKey == "":
		return nil, invalid("idempotency key required")
	}
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), p.SubscriptionID)
	if err != nil {
		return nil, err
	}
	switch {
	case sub.ServerID == "" || sub.Status == "pending":
		return nil, stateErr("the service is not delivered yet")
	case sub.Status == "disabled" || sub.Status == "deleted":
		return nil, stateErr("the service is %s", sub.Status)
	case p.Days > 0 && sub.ExpiresAt == nil:
		return nil, stateErr("the service never expires")
	case p.Bytes > 0 && sub.TrafficTotal == nil:
		return nil, stateErr("the service's traffic is unlimited")
	case p.Days == 0 && sub.ExpiresAt != nil && !sub.ExpiresAt.After(time.Now()):
		return nil, stateErr("the service expired: add days too")
	}
	first, err := s.st.GetOrder(ctx, s.st.Conn(), sub.OrderID)
	if err != nil {
		return nil, err
	}
	o := &store.Order{
		UserID: sub.UserID, PlanID: first.PlanID, Type: OrderRenew, Status: "paid", Currency: first.Currency,
		IdempotencyKey: "staff_extend:" + p.IdempotencyKey, SubscriptionID: &sub.ID, CreatedBy: &actor.ID,
	}
	if p.Days > 0 {
		o.ExtendDays = &p.Days
	} else {
		o.Type = OrderTrafficTopup
	}
	if p.Bytes > 0 {
		o.ExtendBytes = &p.Bytes
	}
	var out *store.Order
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		got, inserted, err := s.st.CreateOrder(ctx, tx, o)
		if err != nil {
			return err
		}
		out = got
		if !inserted {
			return nil // a retry of the same request
		}
		return s.audit(ctx, tx, actor, "subscription.extend", "subscription", sub.ID,
			map[string]any{"order_id": got.ID, "days": p.Days, "bytes": p.Bytes}, reason)
	})
	if err != nil {
		return nil, err
	}
	s.kickProvisioning()
	return out, nil
}

// deliveredSubscription loads a service that exists on the panel.
func (s *Service) deliveredSubscription(ctx context.Context, id string) (*store.Subscription, error) {
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), id)
	if err != nil {
		return nil, err
	}
	switch {
	case sub.Status == "deleted":
		return nil, stateErr("the service was deleted")
	case sub.ServerID == "" || sub.Status == "pending":
		return nil, stateErr("the service is not delivered yet")
	}
	return sub, nil
}

// SetSubscriptionEnabled turns a service off on the panel (status disabled:
// no renewals, no usage sync, no reminders) or back on (status active until
// the next sync says otherwise; SyncSubscription does it at once).
func (s *Service) SetSubscriptionEnabled(ctx context.Context, actor *store.User, prov Provisioner, id string, enabled bool, reason string) (*store.Subscription, error) {
	reason, err := needReason(reason)
	if err != nil {
		return nil, err
	}
	sub, err := s.deliveredSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if enabled == (sub.Status != "disabled") {
		return sub, nil // already so
	}
	if err := prov.SetEnabled(ctx, sub.ID, enabled); err != nil {
		return nil, err
	}
	status, action := "disabled", "subscription.disable"
	if enabled {
		status, action = "active", "subscription.enable"
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetSubscriptionStatus(ctx, tx, sub.ID, status); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, action, "subscription", sub.ID, nil, reason)
	})
	if err != nil {
		return nil, err
	}
	sub.Status = status
	return sub, nil
}

// ResetSubscriptionTraffic zeroes a service's used traffic on the panel (the
// panel enables a client that had used it all) and records the new state.
func (s *Service) ResetSubscriptionTraffic(ctx context.Context, actor *store.User, prov Provisioner, id, reason string) error {
	reason, err := needReason(reason)
	if err != nil {
		return err
	}
	sub, err := s.deliveredSubscription(ctx, id)
	if err != nil {
		return err
	}
	if err := prov.ResetTraffic(ctx, sub.ID); err != nil {
		return err
	}
	if err := s.audit(ctx, nil, actor, "subscription.reset_traffic", "subscription", sub.ID,
		map[string]int64{"used_before": sub.TrafficUsed}, reason); err != nil {
		return err
	}
	return s.SyncSubscription(ctx, prov, sub.ID)
}

// DeleteSubscription removes a service from the panel and marks it deleted
// (the row stays: orders point at it). A service with a delivery still
// running is refused: refund the order or let it finish first.
func (s *Service) DeleteSubscription(ctx context.Context, actor *store.User, prov Provisioner, id, reason string) error {
	reason, err := needReason(reason)
	if err != nil {
		return err
	}
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), id)
	if err != nil {
		return err
	}
	switch sub.Status {
	case "deleted":
		return nil
	case "pending":
		return stateErr("the service is being delivered: refund its order or let it finish")
	}
	open, err := s.st.OpenOrdersFor(ctx, s.st.Conn(), sub.ID)
	if err != nil {
		return err
	}
	if open > 0 {
		return stateErr("a renewal of this service is still being applied: refund it or let it finish")
	}
	if err := prov.Delete(ctx, sub.ID); err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetSubscriptionStatus(ctx, tx, sub.ID, "deleted"); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "subscription.delete", "subscription", sub.ID,
			map[string]string{"client_email": sub.ClientEmail}, reason)
	})
}

// SyncSubscription reads one service from the panel now (usage, limits and
// status) instead of waiting for the next sync. A panel that does not answer
// is an error and nothing changes.
func (s *Service) SyncSubscription(ctx context.Context, prov Provisioner, id string) error {
	return s.NewUsageWorker(prov, nil).SyncOne(ctx, id)
}

// RetryOrder makes a failed delivery run again now.
func (s *Service) RetryOrder(ctx context.Context, actor *store.User, orderID string) error {
	ok, err := s.st.RetryOrderNow(ctx, s.st.Conn(), orderID)
	if err != nil {
		return err
	}
	if !ok {
		if _, err := s.st.GetOrder(ctx, s.st.Conn(), orderID); err != nil {
			return err
		}
		return stateErr("the order is not waiting for a retry")
	}
	if err := s.audit(ctx, nil, actor, "order.retry", "order", orderID, nil, ""); err != nil {
		return err
	}
	s.kickProvisioning()
	return nil
}

// RefundResult is a refund's outcome. PanelCleanup holds why a half-made
// panel client of a cancelled order could not be removed (the refund stands).
type RefundResult struct {
	Order        *store.Order
	Wallet       *store.Wallet
	PanelCleanup string
}

// RefundOrder credits what the customer paid for an order to their wallet,
// once (ledger key refund:<order id>). A paid order not delivered yet is
// cancelled too: its deliveries stop and a half-made panel client is
// removed. A delivered order keeps its service (delete it separately).
func (s *Service) RefundOrder(ctx context.Context, actor *store.User, prov Provisioner, orderID, reason string) (*RefundResult, error) {
	reason, err := needReason(reason)
	if err != nil {
		return nil, err
	}
	res := &RefundResult{}
	var cleanup string
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.st.LockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		switch {
		case o.RefundedAt != nil:
			return stateErr("the order was refunded already")
		case o.Status == "provisioning":
			return stateErr("the order is being delivered right now: try again in a minute")
		case !isPaid(o.Status):
			return stateErr("the order was not paid")
		case o.Amount <= 0:
			return stateErr("nothing was paid for this order")
		}
		user, err := s.st.GetUser(ctx, tx, o.UserID)
		if err != nil {
			return err
		}
		w, err := s.st.Credit(ctx, tx, o.UserID, o.Currency, o.Amount, &store.LedgerEntry{
			Kind: "refund", RefType: strptr("order"), RefID: strptr(o.ID), IdempotencyKey: "refund:" + o.ID,
		})
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			return fmt.Errorf("order %s has a refund in the ledger but none recorded: %w", o.ID, store.ErrIdempotencyConflict)
		}
		if err != nil {
			return err
		}
		if err := s.st.MarkOrderRefunded(ctx, tx, o.ID, o.Amount); err != nil {
			return err
		}
		if o.Status == "paid" || o.Status == "provision_failed" {
			if ok, err := s.st.TransitionOrder(ctx, tx, o.ID, []string{"paid", "provision_failed"}, "cancelled"); err != nil || !ok {
				return errors.Join(err, stateErr("the order changed meanwhile"))
			}
			o.Status = "cancelled"
			if o.SubscriptionID == nil { // a new service that was never delivered
				sub, err := s.st.SubscriptionByOrder(ctx, tx, o.ID)
				switch {
				case errors.Is(err, store.ErrNotFound):
				case err != nil:
					return err
				default:
					if err := s.st.SetSubscriptionStatus(ctx, tx, sub.ID, "deleted"); err != nil {
						return err
					}
					cleanup = sub.ID
				}
			}
		}
		if err := s.audit(ctx, tx, actor, "order.refund", "order", o.ID,
			map[string]any{"amount": o.Amount, "currency": o.Currency, "status": o.Status}, reason); err != nil {
			return err
		}
		now := time.Now()
		o.RefundedAt, o.RefundAmount = &now, &o.Amount
		res.Order, res.Wallet = o, w
		return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
			UserID: o.UserID, TelegramID: user.TelegramID, OrderID: o.ID,
			Amount: o.Amount, Currency: o.Currency, Balance: w.Balance, Reason: events.CreditRefund,
		})
	})
	if err != nil {
		return nil, err
	}
	if cleanup != "" {
		if err := prov.Delete(ctx, cleanup); err != nil {
			res.PanelCleanup = err.Error()
		}
	}
	return res, nil
}
