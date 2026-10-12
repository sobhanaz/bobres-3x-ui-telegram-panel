package tests

// Failure drills: each switches one part of the running backend off
// (internal/testenv's gates and fakes), checks that nothing is done twice or
// lost while it is down, and that everything recovers once it is back.
// Waits that last minutes in production (retry backoff, claim leases) are
// passed by moving the stored time back, never by sleeping.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/broadcast"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
	"google.golang.org/grpc"
)

// Drill: the provisioner goes down while a paid order is being delivered.
// Core keeps retrying with backoff (alerting the staff once), and when the
// provisioner is back the order is delivered exactly once. The second half
// cuts the provisioner after the panel created the client but before core
// heard back: the retry adopts that client instead of making another.
func TestDrillProvisionerDownWhileProvisioning(t *testing.T) {
	s := testenv.Start(t, ownerTG, testenv.WithGates())
	c, prov := s.Core, s.Gates.Provisioner
	plan := drillStore(t, c)

	// 1. Down before core reaches it: every attempt fails and is retried.
	u1 := drillCustomer(t, c, 6001, drillPrice)
	prov.Down()
	o1 := buyFromWallet(t, c, u1, plan, "drill-o1").GetId()
	for n := 1; n <= 3; n++ {
		if n > 1 {
			dueNow(t, s, o1) // the backoff passes; still down
		}
		waitFailed(t, s, o1, n)
	}
	if n := s.Panel.Calls("add"); n != 0 {
		t.Fatalf("the panel got %d creates while the provisioner was down", n)
	}
	prov.Up()
	retryUntilActive(t, s, o1)
	deliveredOnce(t, s, o1)
	if n := coreEvents(t, s, events.ProvisionFailed, "order_id", o1); n != 1 {
		t.Fatalf("the staff were alerted %d times about one order", n)
	}

	// 2. Cut just after the panel created the client: its reply is held, so
	// neither the provisioner nor core ever hears of it.
	u2 := drillCustomer(t, c, 6002, drillPrice)
	adds := s.Panel.Calls("add")
	created := s.Gates.Panel.HoldReply("/panel/api/clients/add")
	o2 := buyFromWallet(t, c, u2, plan, "drill-o2").GetId()
	select {
	case <-created:
	case <-time.After(20 * time.Second):
		t.Fatal("core never reached the panel")
	}
	prov.Down()
	waitFailed(t, s, o2, 1)
	if !s.Panel.Has(subscriptionEmail(t, s, o2)) {
		t.Fatal("the panel has no client: the cut came before the create")
	}
	prov.Up()
	retryUntilActive(t, s, o2)
	deliveredOnce(t, s, o2)
	// The retry created it again, was told the email is taken, and adopted
	// the client as its own: two creates, one client.
	if n := s.Panel.Calls("add") - adds; n != 2 {
		t.Fatalf("the panel got %d creates for the order, want 2 (the retry did not adopt the client)", n)
	}
}

// Drill: payments' payment.succeeded comes again, both as the same event
// (a lost ack, or the payments database restored from a backup) and as a
// new event for the same payment. Core credits the wallet, pays the order
// and delivers it once; the replays change nothing and are not dead-lettered.
func TestDrillPaymentSucceededReplayed(t *testing.T) {
	s := testenv.Start(t, ownerTG)
	ctx := context.Background()
	c := s.Core
	plan := drillStore(t, c)
	u := drillCustomer(t, c, 6101, 0)

	cardPayment(t, c, u.GetId(), "", 70000, "drill-topup")
	waitFor(t, "the top-up credited", func() bool { return balance(t, c, u.GetId()) == 70000 })
	o, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: plan.GetId(), IdempotencyKey: "drill-order"})
	if err != nil {
		t.Fatal(err)
	}
	cardPayment(t, c, u.GetId(), o.GetId(), 0, "drill-pay")
	waitOrder(t, c, o.GetId(), "active")
	waitFor(t, "core caught up with the payments feed", func() bool { return paymentsFeedDone(t, s) })

	want := trailOf(t, s, u.GetId())
	if want.balance != 70000 || want.credited != 1 || want.paid != 1 || want.provisioned != 1 {
		t.Fatalf("before the replays: %+v", want)
	}
	check := func(step string) {
		t.Helper()
		waitFor(t, "core caught up with the payments feed", func() bool { return paymentsFeedDone(t, s) })
		if got := trailOf(t, s, u.GetId()); got != want {
			t.Fatalf("%s: %+v, want %+v", step, got, want)
		}
		deliveredOnce(t, s, o.GetId())
	}

	// 1. The whole feed again: core's cursor moves back to the start.
	mustExec(t, s, `UPDATE payments.outbox_payments_cursors SET last_id = 0 WHERE consumer = 'core'`)
	check("the same events again")

	// 2. Every payment.succeeded published again under a new event id.
	mustExec(t, s, `INSERT INTO payments.outbox_payments (event_id, topic, payload)
		SELECT gen_random_uuid(), topic, payload FROM payments.outbox_payments WHERE topic = $1 ORDER BY id`,
		events.PaymentsPaymentSucceeded)
	check("new events for the same payments")
}

// Drill: the bot restarts (a graceful stop, as an update does) while core's
// feed holds notifications for it: one is on its way to Telegram when the
// stop comes. The restarted bot sends the rest, and every customer gets
// their notification exactly once.
func TestDrillNotifierRestartsWithEventsPending(t *testing.T) {
	s := testenv.Start(t, ownerTG)
	ctx := context.Background()
	c := s.Core
	drillStore(t, c)
	// Twelve wallet credits wait on core's feed while the bot is down.
	var chats []int64
	for i := range int64(12) {
		u := drillCustomer(t, c, 6201+i, 1000+i)
		chats = append(chats, u.GetTelegramId())
	}

	phone := newTelegram()
	phone.hold(5) // Telegram takes five, then stops answering
	first := notifier(t, s, phone)
	runCtx, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = first.Run(runCtx)
	}()
	waitFor(t, "five notifications sent and the sixth on its way", func() bool {
		return phone.total() == 5 && phone.waiting() == 1
	})
	stop() // the bot is stopped
	<-stopped

	phone.release()
	second := notifier(t, s, phone) // the restarted bot: nothing kept in memory
	for range 20 {
		n, err := second.Step(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	for _, chat := range chats {
		if n := phone.count(chat); n != 1 {
			t.Errorf("customer %d got %d notifications, want 1", chat, n)
		}
	}
}

// Drill: core restarts in the middle of a broadcast. As the bot sees it, core
// is gone and comes back; core itself keeps a broadcast's state only in the
// database. The bot sent a batch whose results core never got: those
// recipients count as unknown once the claim's lease passes and are never
// sent again. Everyone else gets the message after core is back: at most
// once per recipient, nobody left out.
func TestDrillCoreRestartsDuringBroadcast(t *testing.T) {
	s := testenv.Start(t, ownerTG, testenv.WithGates())
	ctx := context.Background()
	c := s.Core
	drillStore(t, c)
	for i := range int64(40) {
		drillCustomer(t, c, 6301+i, 0)
	}
	owner, err := s.Store.GetUserByTelegramID(ctx, s.Store.Conn(), ownerTG)
	if err != nil {
		t.Fatal(err)
	}
	dom := coredomain.New(s.Store, coredomain.Config{OwnerTelegramID: ownerTG})
	b, err := dom.CreateBroadcast(ctx, owner, coredomain.BroadcastInput{Segment: "all",
		Texts: map[string]string{"fa": "خبر تازه", "en": "Fresh news"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dom.SendBroadcast(ctx, owner, b.ID, nil, b.Version); err != nil {
		t.Fatal(err)
	}
	if err := dom.NewBroadcastWorker(nil).Step(ctx); err != nil { // starts it
		t.Fatal(err)
	}

	phone := newTelegram()
	phone.hold(5)
	bc := &broadcastCore{c: c, late: map[string]bool{}, reported: map[string]bool{}}
	w := broadcast.New(bc, phone, nil, nil, nil, nil)
	w.Idle, w.FlushEvery = 50*time.Millisecond, 100*time.Millisecond
	runCtx, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = w.Run(runCtx)
	}()

	// Core goes down while the first batch is being sent; the bot finishes
	// it and fails to report, then fails to claim more.
	waitFor(t, "the first batch on its way", func() bool { return phone.total() == 5 && phone.waiting() > 0 })
	s.Gates.Core.Down()
	phone.release()
	waitFor(t, "the bot failing to reach core", func() bool { return bc.claimFailed() })

	// Core is back; its broadcast worker is a new one below.
	bc.markLate() // before Up: only claims core answers after it count
	s.Gates.Core.Up()
	waitFor(t, "the rest sent and reported", func() bool {
		return bc.lateDone() && count(t, s, `SELECT count(*) FROM core.broadcast_recipients
			WHERE broadcast_id = $1 AND status = 'pending'`, b.ID) == 0
	})
	stop()
	<-stopped
	lost := count(t, s, `SELECT count(*) FROM core.broadcast_recipients WHERE broadcast_id = $1 AND status = 'claimed'`, b.ID)
	if lost == 0 {
		t.Fatal("no results were lost: the drill did not cut core in time")
	}
	mustExec(t, s, `UPDATE core.broadcast_recipients SET claimed_at = claimed_at - make_interval(secs => $2)
		WHERE broadcast_id = $1 AND status = 'claimed'`, b.ID, (coredomain.BroadcastLease + time.Minute).Seconds())
	restarted := coredomain.New(s.Store, coredomain.Config{OwnerTelegramID: ownerTG})
	if err := restarted.NewBroadcastWorker(nil).Step(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := s.Store.GetBroadcast(ctx, s.Store.Conn(), b.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || got.Total != 41 || got.Unknown != lost || got.Sent != got.Total-lost || got.Failed+got.Blocked != 0 {
		t.Fatalf("broadcast after the restart: %+v (lost reports: %d)", got, lost)
	}
	if n := phone.chats(); n != got.Total {
		t.Fatalf("%d chats got the broadcast, want %d", n, got.Total)
	}
	for chat, n := range phone.counts() {
		if n != 1 {
			t.Errorf("chat %d got the broadcast %d times", chat, n)
		}
	}
	if n, err := w.Step(ctx); err != nil || n != 0 {
		t.Fatalf("the bot claimed %d more (%v)", n, err)
	}
}

// Drill: Postgres goes away under every service. Core's /readyz fails
// (liveness stays up: a restart cannot help), its calls fail, and once the
// database is back it is ready again and every service works without a
// restart.
func TestDrillPostgresConnectionLost(t *testing.T) {
	s := testenv.Start(t, ownerTG, testenv.WithGates())
	ctx := context.Background()
	c := s.Core
	plan := drillStore(t, c)
	u := drillCustomer(t, c, 6401, drillPrice)

	// Core's health endpoints, wired as cmd/core wires them.
	h := health.New("core", nil)
	h.AddCheck("db", func(ctx context.Context) error { return s.Store.DB().Ping(ctx) })
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	probe := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path) //nolint:gosec,noctx // test server
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body struct {
			Checks map[string]string `json:"checks"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body.Checks["db"]
	}
	if code, db := probe("/readyz"); code != http.StatusOK || db != "ok" {
		t.Fatalf("readyz before: %d db=%q", code, db)
	}

	// Gone for a second and a half (an outage, not a blip): readyz fails
	// all along.
	s.Gates.DB.Down()
	for end := time.Now().Add(1500 * time.Millisecond); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if code, db := probe("/readyz"); code != http.StatusServiceUnavailable || db != "fail" {
			t.Fatalf("readyz with the database gone: %d db=%q", code, db)
		}
	}
	if code, _ := probe("/healthz"); code != http.StatusOK {
		t.Fatalf("healthz with the database gone: %d (it must stay up)", code)
	}
	if _, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 6402, Language: "en"}); err == nil {
		t.Fatal("core saved a user with the database gone")
	}

	s.Gates.DB.Up()
	waitFor(t, "core ready again", func() bool {
		code, db := probe("/readyz")
		return code == http.StatusOK && db == "ok"
	})
	// Core, the provisioner and payments each reconnected by themselves.
	o := buyFromWallet(t, c, u, plan, "drill-db")
	waitOrder(t, c, o.GetId(), "active")
	if _, err := c.CreatePaymentIntent(ctx, &corev1.CreatePaymentIntentRequest{UserId: u.GetId(), Provider: "manual_card",
		Amount: &commonv1.Money{Amount: 10000, Currency: "IRT"}, IdempotencyKey: "drill-db-topup"}); err != nil {
		t.Fatalf("payments after the database came back: %v", err)
	}
	if _, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 6402, Language: "en"}); err != nil {
		t.Fatalf("core after the database came back: %v", err)
	}
}

// --- the store the drills buy from ---

const drillPrice = 150000

// drillStore sets the owner up with a plan and card payments.
func drillStore(t *testing.T, c corev1.CoreServiceClient) *corev1.Plan {
	t.Helper()
	ctx := context.Background()
	if _, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: ownerTG, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	plan, err := c.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: ownerTG, Plan: &corev1.Plan{
		NameI18N: map[string]string{"en": "Monthly", "fa": "ماهانه"}, Kind: "both", DurationDays: 30,
		TrafficBytes: 50 << 30, Price: &commonv1.Money{Amount: drillPrice, Currency: "IRT"}, Enabled: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AdminSetSetting(ctx, &corev1.AdminSetSettingRequest{ActorTelegramId: ownerTG,
		Key: "payments.card_number", Value: "6037-0000-1111-2222"}); err != nil {
		t.Fatal(err)
	}
	return plan
}

// drillCustomer registers a customer, with credit in their wallet (0: none).
func drillCustomer(t *testing.T, c corev1.CoreServiceClient, telegramID, credit int64) *corev1.User {
	t.Helper()
	ctx := context.Background()
	u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: telegramID, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if credit > 0 {
		if _, err := c.AdminAdjustBalance(ctx, &corev1.AdminAdjustBalanceRequest{ActorTelegramId: ownerTG, UserId: u.GetId(),
			Delta: &commonv1.Money{Amount: credit, Currency: "IRT"}, Reason: "drill",
			IdempotencyKey: fmt.Sprintf("drill-credit-%d", telegramID)}); err != nil {
			t.Fatal(err)
		}
	}
	return u
}

func buyFromWallet(t *testing.T, c corev1.CoreServiceClient, u *corev1.User, plan *corev1.Plan, key string) *corev1.Order {
	t.Helper()
	ctx := context.Background()
	o, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: plan.GetId(), IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o.GetId(), UserId: u.GetId()}); err != nil {
		t.Fatal(err)
	}
	return o
}

// cardPayment pays by card (an order, or a top-up of amount when orderID is
// empty), and the owner approves it.
func cardPayment(t *testing.T, c corev1.CoreServiceClient, userID, orderID string, amount int64, key string) {
	t.Helper()
	ctx := context.Background()
	req := &corev1.CreatePaymentIntentRequest{UserId: userID, OrderId: orderID, Provider: "manual_card", IdempotencyKey: key}
	if orderID == "" {
		req.Amount = &commonv1.Money{Amount: amount, Currency: "IRT"}
	}
	in, err := c.CreatePaymentIntent(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SubmitPaymentProof(ctx, &corev1.SubmitPaymentProofRequest{UserId: userID, IntentId: in.GetId(),
		ReceiptFile: "tg-" + key, ReferenceNumber: key}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReviewManualPayment(ctx, &corev1.ReviewManualPaymentRequest{ActorTelegramId: ownerTG,
		IntentId: in.GetId(), Decision: "approved"}); err != nil {
		t.Fatal(err)
	}
}

func balance(t *testing.T, c corev1.CoreServiceClient, userID string) int64 {
	t.Helper()
	w, err := c.GetWallet(context.Background(), &corev1.GetWalletRequest{UserId: userID, Currency: "IRT"})
	if err != nil {
		t.Fatal(err)
	}
	return w.GetBalance()
}

// --- reading and moving the shared database ---

// waitFor polls cond (on the test goroutine) until it holds; the drill fails
// after 20 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func count(t *testing.T, s *testenv.Stack, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.Store.DB().QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustExec(t *testing.T, s *testenv.Stack, q string, args ...any) {
	t.Helper()
	if _, err := s.Store.DB().Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

// coreEvents counts the events of topic on core's feed whose field
// (order_id, user_id) is id.
func coreEvents(t *testing.T, s *testenv.Stack, topic, field, id string) int {
	t.Helper()
	return count(t, s, `SELECT count(*) FROM core.outbox_core WHERE topic = $1 AND payload->>$2 = $3`, topic, field, id)
}

// orderState is an order's status and how many delivery attempts it had.
func orderState(t *testing.T, s *testenv.Stack, orderID string) (status string, attempts int) {
	t.Helper()
	err := s.Store.DB().QueryRow(context.Background(), `SELECT status, provision_attempts FROM core.orders WHERE id = $1`,
		orderID).Scan(&status, &attempts)
	if err != nil {
		t.Fatal(err)
	}
	return status, attempts
}

// waitFailed waits until attempt n to deliver the order failed.
func waitFailed(t *testing.T, s *testenv.Stack, orderID string, n int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("delivery attempt %d to fail", n), func() bool {
		st, attempts := orderState(t, s, orderID)
		return st == "provision_failed" && attempts >= n
	})
}

// dueNow makes a failed order's next attempt due: its backoff passed.
func dueNow(t *testing.T, s *testenv.Stack, orderID string) {
	t.Helper()
	mustExec(t, s, `UPDATE core.orders SET next_attempt_at = now() - interval '1 second'
		WHERE id = $1 AND status = 'provision_failed'`, orderID)
}

// retryUntilActive passes each backoff until the order is delivered (core's
// connection to a service that came back may take a retry or two).
func retryUntilActive(t *testing.T, s *testenv.Stack, orderID string) {
	t.Helper()
	waitFor(t, "the order delivered", func() bool {
		st, _ := orderState(t, s, orderID)
		if st == "provision_failed" {
			dueNow(t, s, orderID)
		}
		return st == "active"
	})
}

func subscriptionEmail(t *testing.T, s *testenv.Stack, orderID string) string {
	t.Helper()
	rows, err := s.Store.DB().Query(context.Background(), `SELECT client_email FROM core.subscriptions WHERE order_id = $1`, orderID)
	if err != nil {
		t.Fatal(err)
	}
	emails, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(emails) != 1 {
		t.Fatalf("order %s has %d subscriptions, want 1", orderID, len(emails))
	}
	return emails[0]
}

// deliveredOnce checks an active order has one subscription, one client on
// the panel, and was announced paid and delivered once each.
func deliveredOnce(t *testing.T, s *testenv.Stack, orderID string) {
	t.Helper()
	if st, _ := orderState(t, s, orderID); st != "active" {
		t.Fatalf("order %s is %q", orderID, st)
	}
	if email := subscriptionEmail(t, s, orderID); !s.Panel.Has(email) {
		t.Fatalf("client %s missing on the panel", email)
	}
	for _, topic := range []string{events.OrderPaid, events.SubscriptionProvisioned} {
		if n := coreEvents(t, s, topic, "order_id", orderID); n != 1 {
			t.Fatalf("%s published %d times for order %s", topic, n, orderID)
		}
	}
}

// paidTrail is what a customer's payments left behind in core.
type paidTrail struct {
	balance                                              int64
	ledgerRows, credited, paid, provisioned, deadLetters int
}

func trailOf(t *testing.T, s *testenv.Stack, userID string) paidTrail {
	t.Helper()
	w, err := s.Store.GetWallet(context.Background(), s.Store.Conn(), userID, "IRT")
	if err != nil {
		t.Fatal(err)
	}
	return paidTrail{
		balance:     w.Balance,
		ledgerRows:  count(t, s, `SELECT count(*) FROM core.ledger_entries WHERE user_id = $1`, userID),
		credited:    coreEvents(t, s, events.WalletCredited, "user_id", userID),
		paid:        coreEvents(t, s, events.OrderPaid, "user_id", userID),
		provisioned: coreEvents(t, s, events.SubscriptionProvisioned, "user_id", userID),
		deadLetters: count(t, s, `SELECT count(*) FROM core.dead_letters`),
	}
}

// paymentsFeedDone reports whether core acknowledged every payments event.
func paymentsFeedDone(t *testing.T, s *testenv.Stack) bool {
	t.Helper()
	return count(t, s, `SELECT count(*) FROM payments.outbox_payments o WHERE o.id > COALESCE(
		(SELECT last_id FROM payments.outbox_payments_cursors WHERE consumer = 'core'), 0)`) == 0
}

// --- the bot's side ---

// notifier is the bot's notification consumer on core's feed, as cmd/bot
// builds it, sending with phone.
func notifier(t *testing.T, s *testenv.Stack, phone *telegram) *eventbus.Consumer {
	t.Helper()
	n := notify.New(s.Core, phone, i18n.MustLoad(), nil, ownerTG, nil)
	c, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Name:       "core-events",
		Source:     eventbus.NewGRPCSource(s.Feed),
		Handle:     n.Handle,
		DeadLetter: n.DeadLetter,
		Interval:   20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// telegram is a fake Bot API for the notifier and the broadcast worker. It
// records each chat's messages, and can stop answering after a number of
// them: the sends that follow wait until release, or are given up by their
// sender (then nothing was delivered).
type telegram struct {
	mu      sync.Mutex
	sent    map[int64]int
	n       int
	left    int           // sends answered before holding (-1: all)
	open    chan struct{} // closed by release
	waiters int
}

func newTelegram() *telegram {
	return &telegram{sent: map[int64]int{}, left: -1, open: make(chan struct{})}
}

func (f *telegram) hold(after int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.left, f.open = after, make(chan struct{})
}

func (f *telegram) release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.left = -1
	close(f.open)
}

func (f *telegram) SendMessage(ctx context.Context, chatID int64, _ string, _ *tg.Keyboard) (*tg.Message, error) {
	f.mu.Lock()
	if f.left == 0 {
		f.waiters++
		open := f.open
		f.mu.Unlock()
		select {
		case <-open:
		case <-ctx.Done():
			f.mu.Lock()
			f.waiters--
			f.mu.Unlock()
			return nil, ctx.Err()
		}
		f.mu.Lock()
		f.waiters--
	} else if f.left > 0 {
		f.left--
	}
	defer f.mu.Unlock()
	f.sent[chatID]++
	f.n++
	return &tg.Message{MessageID: f.n, Chat: tg.Chat{ID: chatID}}, nil
}

func (f *telegram) SendPhoto(ctx context.Context, chatID int64, _ tg.Photo, caption string, kb *tg.Keyboard) (*tg.Message, error) {
	return f.SendMessage(ctx, chatID, caption, kb)
}

func (f *telegram) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func (f *telegram) waiting() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waiters
}

func (f *telegram) count(chatID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[chatID]
}

func (f *telegram) chats() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *telegram) counts() map[int64]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[int64]int, len(f.sent))
	for k, v := range f.sent {
		out[k] = v
	}
	return out
}

// broadcastCore is core as the broadcast worker sees it, keeping what
// happened: failed claims, and for the claims made after markLate, which
// recipients core has a result for.
type broadcastCore struct {
	c corev1.CoreServiceClient

	mu        sync.Mutex
	failed    bool
	lateClaim bool
	late      map[string]bool // claimed after markLate
	reported  map[string]bool
}

func (b *broadcastCore) ClaimBroadcastBatch(ctx context.Context, in *corev1.ClaimBroadcastBatchRequest, opts ...grpc.CallOption) (*corev1.BroadcastBatch, error) {
	out, err := b.c.ClaimBroadcastBatch(ctx, in, opts...)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.failed = true
		return nil, err
	}
	if b.lateClaim {
		for _, r := range out.GetRecipients() {
			b.late[r.GetUserId()] = true
		}
	}
	return out, nil
}

func (b *broadcastCore) ReportBroadcastResults(ctx context.Context, in *corev1.ReportBroadcastResultsRequest, opts ...grpc.CallOption) (*corev1.BroadcastState, error) {
	out, err := b.c.ReportBroadcastResults(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range in.GetResults() {
		if r.GetOutcome() != corev1.BroadcastOutcome_BROADCAST_OUTCOME_RELEASED {
			b.reported[r.GetUserId()] = true
		}
	}
	return out, nil
}

func (b *broadcastCore) claimFailed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failed
}

func (b *broadcastCore) markLate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lateClaim = true
}

// lateDone reports whether core has a result for everyone claimed after
// markLate (and someone was).
func (b *broadcastCore) lateDone() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range b.late {
		if !b.reported[id] {
			return false
		}
	}
	return len(b.late) > 0
}
