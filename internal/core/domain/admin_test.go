package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func owner(t *testing.T, svc *Service) *store.User {
	t.Helper()
	u, err := svc.UpsertUser(context.Background(), UpsertUserParams{TelegramID: ownerTG})
	if err != nil || u.Role != "owner" {
		t.Fatalf("owner: %v %+v", err, u)
	}
	return u
}

func auditCount(t *testing.T, s *store.Store, action string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdminOperationsRequireStaff(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	user := seedUser(t, s, 4001)
	pay, prov := &fakePayments{}, &fakeProvisioner{}
	checks := map[string]error{}
	_, checks["stats"] = svc.AdminStats(ctx, 4001, pay, prov)
	_, checks["pending"] = svc.AdminListPendingPayments(ctx, 4001, pay, 10)
	_, checks["review"] = svc.AdminReviewPayment(ctx, 4001, pay, "i", "approved", "")
	_, checks["find"] = svc.AdminFindUser(ctx, 4001, "4001")
	_, checks["adjust"] = svc.AdminAdjustBalance(ctx, 4001, user.ID, 10, "IRT", "r", "k")
	_, checks["status"] = svc.AdminSetUserStatus(ctx, 4001, user.ID, "banned", "r")
	_, checks["plan"] = svc.AdminUpsertPlan(ctx, 4001, &store.Plan{})
	checks["setting"] = svc.AdminSetSetting(ctx, 4001, "branding.name", "x")
	_, checks["unknown actor"] = svc.AdminStats(ctx, 999999, pay, prov)
	for name, err := range checks {
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("%s by a regular user: want ErrForbidden, got %v", name, err)
		}
	}
	if len(pay.reviews) != 0 {
		t.Fatal("a non-admin review reached payments")
	}
}

func TestAdminAdjustBalance(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner(t, svc)
	u := seedUser(t, s, 4002)

	w, err := svc.AdminAdjustBalance(ctx, ownerTG, u.ID, 5000, "IRT", "compensation for downtime", "adj-1")
	if err != nil || w.Balance != 5000 {
		t.Fatalf("credit: %v %+v", err, w)
	}
	if w, err := svc.AdminAdjustBalance(ctx, ownerTG, u.ID, 5000, "IRT", "compensation for downtime", "adj-1"); err != nil || w.Balance != 5000 {
		t.Fatalf("replay must not credit again: %v %+v", err, w)
	}
	if _, err := svc.AdminAdjustBalance(ctx, ownerTG, u.ID, -9000, "IRT", "typo", "adj-2"); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("overdraw: %v", err)
	}
	if _, err := svc.AdminAdjustBalance(ctx, ownerTG, u.ID, 10, "IRT", " ", "adj-3"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing reason: %v", err)
	}
	if bal, sum, _ := ledger(t, s, u.ID); bal != 5000 || sum != 5000 {
		t.Fatalf("balance=%d ledger=%d", bal, sum)
	}
	if n := auditCount(t, s, "wallet.adjust"); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	if n := len(outboxEvents(t, s, events.WalletCredited)); n != 1 {
		t.Fatalf("user notified %d times", n)
	}
}

func TestAdminSetUserStatus(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	o := owner(t, svc)
	u := seedUser(t, s, 4003)
	got, err := svc.AdminSetUserStatus(ctx, ownerTG, u.ID, "banned", "fraud")
	if err != nil || got.Status != "banned" {
		t.Fatalf("ban: %v %+v", err, got)
	}
	if _, err := svc.AdminSetUserStatus(ctx, ownerTG, o.ID, "banned", "oops"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("banning the owner/self: %v", err)
	}
	if _, err := svc.AdminSetUserStatus(ctx, ownerTG, u.ID, "active", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing reason: %v", err)
	}
	if n := auditCount(t, s, "user.status"); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
}

func TestAdminPlansAndSettings(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner(t, svc)
	days, gb := int32(30), int64(50<<30)
	p, err := svc.AdminUpsertPlan(ctx, ownerTG, &store.Plan{
		NameI18n: map[string]string{"en": "Monthly", "fa": "ماهانه"}, Kind: "both",
		DurationDays: &days, TrafficBytes: &gb, Price: 150000, Currency: "IRT", Enabled: true,
	})
	if err != nil || p.ID == "" {
		t.Fatalf("create plan: %v", err)
	}
	for name, bad := range map[string]*store.Plan{
		"paid trial":       {NameI18n: map[string]string{"en": "T"}, Kind: "time", DurationDays: &days, Price: 1, Currency: "IRT", IsTrial: true},
		"no duration":      {NameI18n: map[string]string{"en": "T"}, Kind: "time", Price: 1, Currency: "IRT"},
		"no name":          {Kind: "time", DurationDays: &days, Price: 1, Currency: "IRT"},
		"unknown currency": {NameI18n: map[string]string{"en": "T"}, Kind: "time", DurationDays: &days, Price: 1, Currency: "EUR"},
	} {
		if _, err := svc.AdminUpsertPlan(ctx, ownerTG, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if err := svc.AdminSetSetting(ctx, ownerTG, "payments.card_number", "6037-9911-0000-0000"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdminSetSetting(ctx, ownerTG, "payments.webhook_secret", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown setting: %v", err)
	}
	u := seedUser(t, s, 4004)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "set-1"})
	res, err := svc.CreatePaymentIntent(ctx, &fakePayments{id: "00000000-0000-7000-8000-0000abcdef12"}, CreatePaymentIntentParams{
		UserID: u.ID, OrderID: o.ID, Provider: "manual_card", IdempotencyKey: "set-int",
	})
	if err != nil || res.Details["card_number"] != "6037-9911-0000-0000" || res.Details["reference"] != "ABCDEF12" {
		t.Fatalf("payment details: %v %+v", err, res)
	}
}

func TestAdminReviewAndQueue(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	o := owner(t, svc)
	u := seedUser(t, s, 4005)
	pay := &fakePayments{pending: []PendingPayment{{IntentID: "00000000-0000-7000-8000-0000000000a9", UserID: u.ID, Amount: 10, Currency: "IRT", SubmittedAt: time.Now()}}}
	q, err := svc.AdminListPendingPayments(ctx, ownerTG, pay, 10)
	if err != nil || len(q) != 1 || q[0].User == nil || q[0].User.TelegramID != 4005 {
		t.Fatalf("queue: %v %+v", err, q)
	}
	st, err := svc.AdminReviewPayment(ctx, ownerTG, pay, "00000000-0000-7000-8000-0000000000a9", "approved", "")
	if err != nil || st != "succeeded" || pay.reviews[0] != o.ID+"|00000000-0000-7000-8000-0000000000a9|approved" {
		t.Fatalf("review: %v %s %v", err, st, pay.reviews)
	}
	if n := auditCount(t, s, "payment.review"); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	v, err := svc.AdminFindUser(ctx, ownerTG, "4005")
	if err != nil || v.User.ID != u.ID {
		t.Fatalf("find by id: %v", err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.users SET username = 'Ali_Reza' WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if v, err := svc.AdminFindUser(ctx, ownerTG, "@ali_reza"); err != nil || v.User.ID != u.ID {
		t.Fatalf("find by username: %v", err)
	}
}

func TestProofAndLinks(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 4006)
	pay := &fakePayments{}
	if _, err := svc.SubmitPaymentProof(ctx, pay, ProofParams{UserID: u.ID, IntentID: "i1", Network: "TRC20", TXID: "0xabc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitPaymentProof(ctx, pay, ProofParams{UserID: u.ID, IntentID: "i2", ReceiptFile: "f", ReferenceNumber: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitPaymentProof(ctx, pay, ProofParams{UserID: u.ID, IntentID: "i3"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty proof: %v", err)
	}
	if len(pay.proofs) != 2 || pay.proofs[0][:6] != "crypto" || pay.proofs[1][:4] != "card" {
		t.Fatalf("proof routing: %v", pay.proofs)
	}

	plan := planWithLimits(t, s, 30, 0)
	buyer, o := paidWalletOrder(t, svc, s, 4007, plan)
	fp := &fakeProvisioner{}
	w := svc.NewProvisionWorker(fp, nil)
	if _, err := w.ProvisionNext(ctx); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.SubscriptionByOrder(ctx, s.Conn(), o.ID)
	if l, err := svc.SubscriptionLinks(ctx, fp, buyer.ID, sub.ID); err != nil || l.SubscriptionLink == "" {
		t.Fatalf("links: %v", err)
	}
	if _, err := svc.SubscriptionLinks(ctx, fp, u.ID, sub.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else's subscription: %v", err)
	}
}

func TestAdminStatsDegradeGracefully(t *testing.T) {
	svc, _ := testService(t)
	owner(t, svc)
	st, err := svc.AdminStats(context.Background(), ownerTG, &fakePayments{}, &fakeProvisioner{})
	if err != nil || st.UsersTotal != 1 || !st.PanelHealthy || st.PendingPayments != 0 {
		t.Fatalf("stats: %v %+v", err, st)
	}
}
