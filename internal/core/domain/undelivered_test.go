package domain

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

const (
	healthyServer = "00000000-0000-7000-8000-0000000000a1"
	offServer     = "00000000-0000-7000-8000-0000000000a2"
)

// undeliveredWorld is a store with a plan, a worker whose panel does not answer, and two servers: one on, one off.
type undeliveredWorld struct {
	svc     *Service
	s       *store.Store
	plan    *store.Plan
	fp      *fakeProvisioner
	w       *ProvisionWorker
	servers *fakeServers
	owner   *store.User
}

func newUndeliveredWorld(t *testing.T) *undeliveredWorld {
	t.Helper()
	svc, s := testService(t)
	fp := &fakeProvisioner{failures: 1 << 20} // every create fails until reset
	return &undeliveredWorld{
		svc: svc, s: s, plan: planWithLimits(t, s, 30, 0), fp: fp, w: svc.NewProvisionWorker(fp, nil),
		servers: &fakeServers{servers: []Server{
			{ID: healthyServer, Name: "germany", Enabled: true, Healthy: true, CheckedAt: 1},
			{ID: offServer, Name: "old", Enabled: false},
		}},
		owner: owner(t, svc),
	}
}

// fail runs the worker once on whatever is due: one failed attempt.
func (u *undeliveredWorld) fail(t *testing.T) {
	t.Helper()
	if worked, err := u.w.ProvisionNext(context.Background()); !worked || err != nil {
		t.Fatalf("attempt: worked=%v err=%v", worked, err)
	}
}

// buy makes a paid wallet order of the plan for a new user tg and lets its first delivery fail.
func (u *undeliveredWorld) buy(t *testing.T, tg int64) (*store.User, *store.Order) {
	t.Helper()
	buyer, o := paidWalletOrder(t, u.svc, u.s, tg, u.plan)
	u.fail(t)
	return buyer, o
}

// panelDown makes every create fail again.
func (u *undeliveredWorld) panelDown() {
	u.fp.mu.Lock()
	u.fp.failures = 1 << 20
	u.fp.mu.Unlock()
}

func (u *undeliveredWorld) deliver(t *testing.T) {
	t.Helper()
	u.fp.mu.Lock()
	u.fp.failures = 0
	u.fp.mu.Unlock()
	if worked, err := u.w.ProvisionNext(context.Background()); !worked || err != nil {
		t.Fatalf("delivery: worked=%v err=%v", worked, err)
	}
}

// The list holds the undelivered orders only, the longest waiting first, each with who sold it: a reseller buying
// for a customer, a reseller whose link brought the buyer, or nobody (the store).
func TestUndeliveredListsTheWaitingOrdersWithTheirSeller(t *testing.T) {
	u := newUndeliveredWorld(t)
	ctx := context.Background()
	admin := staffMember(t, u.s, 9600, "admin")
	tier := seedTier(t, u.svc, admin, "Gold", 0, 10, nil)
	r := reseller(t, u.svc, u.s, admin, 9601, tier, 0)
	info, err := u.svc.ResellerInfo(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, plain := u.buy(t, 9602)
	brought, err := u.svc.UpsertUser(ctx, UpsertUserParams{TelegramID: 9603, ReferralCode: info.RefCode})
	if err != nil {
		t.Fatal(err)
	}
	fund(t, u.s, brought.ID, u.plan.Price)
	ref, err := u.svc.CreateOrder(ctx, CreateOrderParams{UserID: brought.ID, PlanID: u.plan.ID, IdempotencyKey: "ud-ref"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.PayOrderWithWallet(ctx, ref.ID, brought.ID); err != nil {
		t.Fatal(err)
	}
	u.fail(t)
	fund(t, u.s, r.ID, 10_000)
	own, err := u.svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: u.plan.ID, IdempotencyKey: "ud-own", Label: "Ali"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.PayOrderWithWallet(ctx, own.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	u.fail(t)
	// A delivered order is not listed.
	u.fp.mu.Lock()
	u.fp.failures = 0
	u.fp.mu.Unlock()
	paidWalletOrder(t, u.svc, u.s, 9604, u.plan)
	if worked, err := u.w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatal(err)
	}

	page, err := u.svc.ListUndelivered(ctx, store.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Orders) != 3 {
		t.Fatalf("listed %d of %d", len(page.Orders), page.Total)
	}
	got := map[string]store.OrderRow{}
	var ids []string
	for _, o := range page.Orders {
		got[o.ID] = o
		ids = append(ids, o.ID)
	}
	if !slices.Equal(ids, []string{plain.ID, ref.ID, own.ID}) {
		t.Fatalf("not the longest waiting first: %v", ids)
	}
	if o := got[plain.ID]; o.SellerKind != "" || o.SellerID != "" || o.Attempts != 1 || o.LastError == "" {
		t.Errorf("the store's own sale: %+v", o)
	}
	if o := got[ref.ID]; o.SellerKind != store.SellerReferrer || o.SellerID != r.ID || o.SellerTelegramID != 9601 {
		t.Errorf("a customer of the reseller: %+v", o)
	}
	if o := got[own.ID]; o.SellerKind != store.SellerReseller || o.SellerID != r.ID || o.Label == nil || *o.Label != "Ali" {
		t.Errorf("the reseller's own purchase: %+v", o)
	}

	// The bot's list is for the owner and admins only.
	if _, err := u.svc.AdminListUndelivered(ctx, 9602, 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer listed them: %v", err)
	}
	if p, err := u.svc.AdminListUndelivered(ctx, 9600, 0, 2); err != nil || p.Total != 3 || len(p.Orders) != 2 {
		t.Fatalf("admin: %+v %v", p, err)
	}
}

// Retry all makes every undelivered order due at once (one audit entry with the count); the bot's retry of one
// order that is not waiting any more counts 0.
func TestRetryEveryUndeliveredOrder(t *testing.T) {
	u := newUndeliveredWorld(t)
	ctx := context.Background()
	_, a := u.buy(t, 9610)
	_, b := u.buy(t, 9611)
	if worked, _ := u.w.ProvisionNext(ctx); worked {
		t.Fatal("tried again before the backoff")
	}
	n, err := u.svc.RetryUndelivered(ctx, u.owner)
	if err != nil || n != 2 {
		t.Fatalf("retry all: %d %v", n, err)
	}
	if auditCount(t, u.s, "order.retry_all") != 1 {
		t.Fatal("no audit entry")
	}
	u.deliver(t)
	u.deliver(t)
	for _, o := range []*store.Order{a, b} {
		if got, _ := u.s.GetOrder(ctx, u.s.Conn(), o.ID); got.Status != "active" {
			t.Fatalf("order %s: %s", o.ID, got.Status)
		}
	}
	if n, err := u.svc.RetryUndelivered(ctx, u.owner); err != nil || n != 0 {
		t.Fatalf("nothing waits: %d %v", n, err)
	}
	if n, err := u.svc.AdminRetryUndelivered(ctx, ownerTG, a.ID); err != nil || n != 0 {
		t.Fatalf("a delivered order from an old button: %d %v", n, err)
	}
}

// Moving an undelivered new service to another server releases its reservation on the old one, keeps the choice and
// tries it at once on that server only; the plan and the price stay. Renewals, orders not waiting and servers that
// are off or unknown are refused, and nothing changes.
func TestMoveAnUndeliveredOrderToAnotherServer(t *testing.T) {
	u := newUndeliveredWorld(t)
	ctx := context.Background()
	_, o := u.buy(t, 9620)
	sub, err := u.s.SubscriptionByOrder(ctx, u.s.Conn(), o.ID)
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		server, reason string
		want           error
	}{
		"no reason":      {healthyServer, " ", ErrInvalid},
		"not an id":      {"germany", "panel down", ErrInvalid},
		"unknown server": {"00000000-0000-7000-8000-0000000000ff", "panel down", ErrInvalid},
		"server off":     {offServer, "panel down", ErrState},
	} {
		if _, err := u.svc.MoveOrder(ctx, u.owner, u.fp, u.servers, o.ID, tc.server, tc.reason); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if len(u.fp.released) != 0 || auditCount(t, u.s, "order.move") != 0 {
		t.Fatalf("a refused move changed something: %v", u.fp.released)
	}

	u.fp.release = Release{Released: true, Leftover: true, ServerID: offServer}
	res, err := u.svc.MoveOrder(ctx, u.owner, u.fp, u.servers, o.ID, healthyServer, "the old panel is down")
	if err != nil || !res.Leftover || res.FromServer != offServer {
		t.Fatalf("move: %+v %v", res, err)
	}
	if !slices.Equal(u.fp.released, []string{sub.ID}) {
		t.Fatalf("released %v, want the order's service", u.fp.released)
	}
	got, _ := u.s.GetOrder(ctx, u.s.Conn(), o.ID)
	if got.ServerID == nil || *got.ServerID != healthyServer || got.Amount != o.Amount || got.PlanID != o.PlanID {
		t.Fatalf("after the move: %+v", got)
	}
	var after map[string]any
	if err := u.s.DB().QueryRow(ctx, `SELECT after FROM core.audit_log WHERE action = 'order.move' AND entity_id = $1`, o.ID).Scan(&after); err != nil ||
		after["server_id"] != healthyServer || after["from_server_id"] != offServer || after["leftover"] == nil {
		t.Fatalf("audit: %v %v", after, err)
	}
	u.deliver(t) // due at once, on the chosen server only
	if !slices.Equal(u.fp.candidates, []string{healthyServer}) {
		t.Fatalf("offered %v, want the chosen server", u.fp.candidates)
	}
	if got, _ := u.s.GetOrder(ctx, u.s.Conn(), o.ID); got.Status != "active" {
		t.Fatalf("not delivered: %s", got.Status)
	}
	if _, err := u.svc.MoveOrder(ctx, u.owner, u.fp, u.servers, o.ID, healthyServer, "again"); !errors.Is(err, ErrState) {
		t.Fatalf("moving a delivered order: %v", err)
	}

	// A renewal stays on its service's server.
	ren, err := u.svc.CreateOrder(ctx, CreateOrderParams{UserID: o.UserID, PlanID: u.plan.ID, Type: OrderRenew,
		SubscriptionID: sub.ID, IdempotencyKey: "ud-renew"})
	if err != nil {
		t.Fatal(err)
	}
	fund(t, u.s, o.UserID, u.plan.Price+1) // another amount than the first funding: another ledger key
	if _, err := u.svc.PayOrderWithWallet(ctx, ren.ID, o.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := u.s.DB().Exec(ctx, `UPDATE core.orders SET status = 'provision_failed' WHERE id = $1`, ren.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.MoveOrder(ctx, u.owner, u.fp, u.servers, ren.ID, healthyServer, "panel down"); !errors.Is(err, ErrState) {
		t.Fatalf("moving a renewal: %v", err)
	}

	// The provisioner refusing to release (the client was made meanwhile): the order waits as before.
	u.panelDown()
	_, o2 := u.buy(t, 9621)
	u.fp.releaseErr = errors.New("the client was made on its panel")
	if _, err := u.svc.MoveOrder(ctx, u.owner, u.fp, u.servers, o2.ID, healthyServer, "panel down"); err == nil {
		t.Fatal("moved though the release failed")
	}
	got2, _ := u.s.GetOrder(ctx, u.s.Conn(), o2.ID)
	if got2.ServerID != nil || got2.Status != "provision_failed" {
		t.Fatalf("after a failed move: %+v", got2)
	}
	if worked, err := u.w.ProvisionNext(ctx); !worked || err != nil { // due again, not held for two minutes
		t.Fatalf("held after a failed move: %v %v", worked, err)
	}
}

// A refund of an undelivered order takes back the commission its reseller was paid (as far as the wallet holds it),
// tells the reseller, and carries the staff's note to the buyer; a delivered order's refund leaves the commission.
func TestRefundOfAnUndeliveredOrderTakesTheCommissionBack(t *testing.T) {
	u := newUndeliveredWorld(t)
	ctx := context.Background()
	admin := staffMember(t, u.s, 9630, "admin")
	tier := seedTier(t, u.svc, admin, "Gold", 0, 10, nil)
	r := reseller(t, u.svc, u.s, admin, 9631, tier, 0)
	info, err := u.svc.ResellerInfo(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := u.svc.UpsertUser(ctx, UpsertUserParams{TelegramID: 9632, ReferralCode: info.RefCode})
	if err != nil {
		t.Fatal(err)
	}
	fund(t, u.s, c.ID, 3*u.plan.Price)
	buy := func(key string) *store.Order {
		t.Helper()
		o, err := u.svc.CreateOrder(ctx, CreateOrderParams{UserID: c.ID, PlanID: u.plan.ID, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		if o, err = u.svc.PayOrderWithWallet(ctx, o.ID, c.ID); err != nil {
			t.Fatal(err)
		}
		return o
	}
	waiting := buy("ud-c1")
	u.fail(t)
	if bal, _, _ := ledger(t, u.s, r.ID); bal != 100 {
		t.Fatalf("commission paid: %d", bal)
	}

	if _, err := u.svc.RefundOrderNote(ctx, u.owner, u.fp, waiting.ID, "panel down", string(make([]rune, 501))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a note too long: %v", err)
	}
	res, err := u.svc.RefundOrderNote(ctx, u.owner, u.fp, waiting.ID, "panel down", "  Sorry, our server is down.  ")
	if err != nil || res.CommissionTaken != 100 {
		t.Fatalf("refund: %+v %v", res, err)
	}
	if bal, _, _ := ledger(t, u.s, r.ID); bal != 0 {
		t.Fatalf("the reseller kept the commission: %d", bal)
	}
	refunds := walletEvents(t, u.s, c.ID, events.CreditRefund)
	if len(refunds) != 1 || refunds[0].Note != "Sorry, our server is down." {
		t.Fatalf("the buyer's refund event: %+v", refunds)
	}
	var ce events.CommissionReversedEvent
	if evs := outboxEvents(t, u.s, events.CommissionReversed); len(evs) != 1 || json.Unmarshal(evs[0], &ce) != nil ||
		ce.TelegramID != 9631 || ce.OrderID != waiting.ID || ce.Credited != 100 || ce.Amount != 100 || ce.Balance != 0 {
		t.Fatalf("the reseller's event: %s", evs)
	}
	if auditCount(t, u.s, "order.commission_reversal") != 1 {
		t.Fatal("no audit entry for the commission")
	}

	// The reseller spent the commission of the next one: what is left is taken, never below zero.
	short := buy("ud-c2")
	u.fail(t)
	if _, err := u.s.DB().Exec(ctx, `UPDATE core.wallets SET balance = 30 WHERE user_id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if res, err := u.svc.RefundOrderNote(ctx, u.owner, u.fp, short.ID, "panel down", ""); err != nil || res.CommissionTaken != 30 {
		t.Fatalf("a short wallet: %+v %v", res, err)
	}
	var detail map[string]any
	if err := u.s.DB().QueryRow(ctx, `SELECT after FROM core.audit_log WHERE action = 'order.commission_reversal' AND entity_id = $1`, short.ID).Scan(&detail); err != nil ||
		detail["shortfall"] != float64(70) {
		t.Fatalf("shortfall: %v %v", detail, err)
	}

	// A delivered order refunded: its commission stays (the service was delivered).
	u.fp.mu.Lock()
	u.fp.failures = 0
	u.fp.mu.Unlock()
	done := buy("ud-c3")
	if worked, err := u.w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatal(err)
	}
	before, _, _ := ledger(t, u.s, r.ID)
	if res, err := u.svc.RefundOrder(ctx, u.owner, u.fp, done.ID, "asked for it back"); err != nil || res.CommissionTaken != 0 {
		t.Fatalf("a delivered order's refund: %+v %v", res, err)
	}
	if after, _, _ := ledger(t, u.s, r.ID); after != before {
		t.Fatalf("a delivered order's commission was taken: %d -> %d", before, after)
	}
}

// The bot's refund acts only on an order that still waits: one delivered or refunded since is the outcome gone.
func TestBotRefundsOnlyWhatStillWaits(t *testing.T) {
	u := newUndeliveredWorld(t)
	ctx := context.Background()
	buyer, o := u.buy(t, 9640)
	if _, _, err := u.svc.AdminRefundUndelivered(ctx, 9640, u.fp, o.ID, "x", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer refunded: %v", err)
	}
	out, res, err := u.svc.AdminRefundUndelivered(ctx, ownerTG, u.fp, o.ID, "Refunded from the bot", "")
	if err != nil || out != UndeliveredDone || res == nil || res.Order.Amount != o.Amount {
		t.Fatalf("refund: %s %+v %v", out, res, err)
	}
	if bal, _, _ := ledger(t, u.s, buyer.ID); bal != o.Amount {
		t.Fatalf("the buyer's wallet: %d", bal)
	}
	if out, _, err := u.svc.AdminRefundUndelivered(ctx, ownerTG, u.fp, o.ID, "again", ""); err != nil || out != UndeliveredGone {
		t.Fatalf("again: %s %v", out, err)
	}
	if out, _, err := u.svc.AdminRefundUndelivered(ctx, ownerTG, u.fp, "00000000-0000-7000-8000-00000000dead", "x", ""); err != nil || out != UndeliveredGone {
		t.Fatalf("unknown: %s %v", out, err)
	}
}

// A delivery the customer was told is delayed says sorry: an attempt after the one that announced the failure.
// One that worked before that does not.
func TestLateDeliveryIsMarked(t *testing.T) {
	u := newUndeliveredWorld(t)
	ctx := context.Background()
	_, quick := u.buy(t, 9650) // failed once, then delivered: the customer was never told
	if _, err := u.svc.RetryUndelivered(ctx, u.owner); err != nil {
		t.Fatal(err)
	}
	u.deliver(t)
	u.panelDown()
	_, slow := u.buy(t, 9651)
	for range u.w.AlertAfter - 1 {
		if _, err := u.svc.RetryUndelivered(ctx, u.owner); err != nil {
			t.Fatal(err)
		}
		u.fail(t)
	}
	if n := len(outboxEvents(t, u.s, events.ProvisionFailed)); n != 1 {
		t.Fatalf("delayed announced %d times", n)
	}
	if _, err := u.svc.RetryUndelivered(ctx, u.owner); err != nil {
		t.Fatal(err)
	}
	u.deliver(t)
	late := map[string]bool{}
	for _, raw := range outboxEvents(t, u.s, events.SubscriptionProvisioned) {
		var e events.SubscriptionProvisionedEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		late[e.OrderID] = e.Late
	}
	if late[quick.ID] || !late[slow.ID] || len(late) != 2 {
		t.Fatalf("late: %v", late)
	}
}
