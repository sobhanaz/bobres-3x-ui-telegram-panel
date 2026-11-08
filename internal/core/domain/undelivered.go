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
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Undelivered orders (docs/04-dashboard.md and docs/03-bot-ux.md, "Undelivered orders"): paid orders whose service
// could not be made or applied yet (status provision_failed). The worker tries each again on its own (provisionBackoff,
// at most every 30 minutes); staff may try one or all of them now, deliver a new service on another server
// (MoveOrder), or refund one to the buyer's wallet (RefundOrderNote).

// moveHold is how long MoveOrder keeps the worker off an order while it releases the order's reservation on the old
// server: longer than the provisioner's try to delete the client there.
const moveHold = 2 * time.Minute

// UndeliveredPage is a page of the undelivered orders, the longest waiting first, and how many there are in all.
type UndeliveredPage struct {
	Orders []store.OrderRow
	Total  int64
}

// ListUndelivered lists a page of the undelivered orders, the longest waiting first.
func (s *Service) ListUndelivered(ctx context.Context, p store.Page) (*UndeliveredPage, error) {
	rows, total, err := s.st.ListOrders(ctx, s.st.Conn(), store.OrderFilter{Status: "provision_failed", Oldest: true}, p)
	if err != nil {
		return nil, err
	}
	return &UndeliveredPage{Orders: rows, Total: total}, nil
}

// RetryUndelivered makes every undelivered order due now and returns how many there were (audit order.retry_all).
func (s *Service) RetryUndelivered(ctx context.Context, actor *store.User) (int64, error) {
	var n int64
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		if n, err = s.st.RetryFailedOrders(ctx, tx); err != nil || n == 0 {
			return err
		}
		return s.audit(ctx, tx, actor, "order.retry_all", "order", "", map[string]any{"count": n}, "")
	})
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.kickProvisioning()
	}
	return n, nil
}

// MoveResult is what MoveOrder did. Leftover is true when the old server's panel could not be asked and may keep a
// half-made client of the order that nothing points to (staff delete it there by hand); FromServer is the server the
// order was held on ("" when it had none).
type MoveResult struct {
	Leftover   bool
	FromServer string
}

// MoveOrder delivers an undelivered new service on the server staff chose instead of the plan's servers: a server
// that answers when the plan's do not. The order keeps the choice (orders.server_id) and is tried at once; a
// reservation on another server is released first, so the next try picks the chosen one. The plan and the price stay
// as they were. Renewals and top-ups stay on their service's server: only a new service moves. Audited order.move.
func (s *Service) MoveOrder(ctx context.Context, actor *store.User, prov Provisioner, sa ServerAdmin, orderID, serverID, reason string) (*MoveResult, error) {
	reason, err := needReason(reason)
	if err != nil {
		return nil, err
	}
	serverID = strings.TrimSpace(serverID)
	if u, err := buuid.Parse(serverID); err != nil || u.String() != serverID {
		return nil, invalid("not a server id")
	}
	list, err := sa.ListServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	var target *Server
	for i := range list.Servers {
		if list.Servers[i].ID == serverID {
			target = &list.Servers[i]
		}
	}
	switch {
	case target == nil:
		return nil, invalid("no such server")
	case !target.Enabled:
		return nil, stateErr("the server is turned off: it takes no new services")
	}
	o, err := s.st.GetOrder(ctx, s.st.Conn(), orderID)
	if err != nil {
		return nil, err
	}
	switch {
	case o.SubscriptionID != nil:
		return nil, stateErr("a renewal or a top-up stays on its service's server: retry it or refund it")
	case o.Status != "provision_failed":
		return nil, stateErr("the order is not waiting for a retry")
	}
	// The worker must not try the order on the old server while its reservation there is released.
	held, err := s.st.HoldFailedOrder(ctx, s.st.Conn(), o.ID, time.Now().Add(moveHold))
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, stateErr("the order is not waiting for a retry")
	}
	res := &MoveResult{}
	sub, err := s.st.SubscriptionByOrder(ctx, s.st.Conn(), o.ID)
	switch {
	case errors.Is(err, store.ErrNotFound): // never tried far enough to have a service: nothing reserved
	case err != nil:
		return nil, errors.Join(err, s.releaseHold(ctx, o.ID))
	default:
		rel, err := prov.ReleaseClient(ctx, sub.ID)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("release the old server: %w", err), s.releaseHold(ctx, o.ID))
		}
		res.Leftover, res.FromServer = rel.Leftover, rel.ServerID
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		ok, err := s.st.SetOrderServer(ctx, tx, o.ID, serverID)
		if err != nil {
			return err
		}
		if !ok {
			return stateErr("the order changed meanwhile")
		}
		detail := map[string]any{"server_id": serverID, "server": target.Name}
		if res.FromServer != "" {
			detail["from_server_id"] = res.FromServer
		}
		if res.Leftover {
			detail["leftover"] = "the old server's panel did not answer: a half-made client may be left there"
		}
		return s.audit(ctx, tx, actor, "order.move", "order", o.ID, detail, reason)
	})
	if err != nil {
		return nil, err
	}
	s.kickProvisioning()
	return res, nil
}

// releaseHold makes an order MoveOrder held due again after the move failed: it waits as before.
func (s *Service) releaseHold(ctx context.Context, orderID string) error {
	_, err := s.st.RetryOrderNow(ctx, s.st.Conn(), orderID)
	return err
}

// reverseCommission takes back the commission a reseller was paid on an order refunded before it was delivered, in
// the refund's transaction: as far as the reseller's wallet holds it, never below zero (as reverseCashback does); what
// could not be taken is written to the audit log. It returns what was taken.
func (s *Service) reverseCommission(ctx context.Context, tx pgx.Tx, actor *store.User, o *store.Order) (int64, error) {
	paid, err := s.st.LedgerByKey(ctx, tx, "commission:"+o.ID) // payCommission's entry
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if paid.Amount <= 0 {
		return 0, nil
	}
	entry := &store.LedgerEntry{Kind: "commission_reversal", RefType: strptr("order"), RefID: strptr(o.ID),
		IdempotencyKey: "commission_reversal:" + o.ID}
	taken, w, err := s.st.DebitAtMost(ctx, tx, paid.UserID, paid.Currency, paid.Amount, entry)
	if errors.Is(err, store.ErrDuplicateIdempotency) {
		return taken, nil // taken back already
	}
	if err != nil {
		return 0, err
	}
	detail := map[string]any{"reseller_id": paid.UserID, "credited": paid.Amount, "taken": taken,
		"shortfall": paid.Amount - taken, "currency": paid.Currency}
	if err := s.audit(ctx, tx, actor, "order.commission_reversal", "order", o.ID, detail, ""); err != nil {
		return 0, err
	}
	if taken == 0 {
		return 0, nil
	}
	reseller, err := s.st.GetUser(ctx, tx, paid.UserID)
	if err != nil {
		return 0, err
	}
	return taken, s.publish(ctx, tx, events.CommissionReversed, events.CommissionReversedEvent{
		UserID: reseller.ID, TelegramID: reseller.TelegramID, OrderID: o.ID,
		Credited: paid.Amount, Amount: taken, Currency: paid.Currency, Balance: w.Balance,
	})
}

// The outcomes of AdminRefundUndelivered.
const (
	UndeliveredDone = "done"
	UndeliveredGone = "gone" // the order is not waiting any more: nothing was done
)

// AdminListUndelivered is ListUndelivered for a staff member in the bot (owner and admin, as payments.review on the
// dashboard).
func (s *Service) AdminListUndelivered(ctx context.Context, actorTelegramID int64, offset, limit int) (*UndeliveredPage, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	return s.ListUndelivered(ctx, store.Page{Limit: limit, Offset: offset})
}

// AdminRetryUndelivered tries one undelivered order again now, or every one (orderID ""), for a staff member in the
// bot. It returns how many orders are tried; an order that is not waiting any more counts 0 (the button that named it
// is stale), not an error.
func (s *Service) AdminRetryUndelivered(ctx context.Context, actorTelegramID int64, orderID string) (int64, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return 0, err
	}
	if orderID == "" {
		return s.RetryUndelivered(ctx, actor)
	}
	err = s.RetryOrder(ctx, actor, orderID)
	if errors.Is(err, ErrState) || errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return 1, nil
}

// AdminRefundUndelivered refunds an undelivered order to the buyer's wallet for a staff member in the bot, as the
// dashboard does (RefundOrderNote). An order that is not undelivered any more (delivered, refunded, being tried right
// now, or unknown) is the outcome gone, and nothing is done: the bot refunds only what it listed.
func (s *Service) AdminRefundUndelivered(ctx context.Context, actorTelegramID int64, prov Provisioner, orderID, reason, note string) (string, *RefundResult, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return "", nil, err
	}
	o, err := s.st.GetOrder(ctx, s.st.Conn(), orderID)
	if errors.Is(err, store.ErrNotFound) {
		return UndeliveredGone, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if o.Status != "provision_failed" || o.RefundedAt != nil {
		return UndeliveredGone, nil, nil
	}
	res, err := s.RefundOrderNote(ctx, actor, prov, orderID, reason, note)
	if errors.Is(err, ErrState) { // it changed between the look and the refund
		return UndeliveredGone, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return UndeliveredDone, res, nil
}
