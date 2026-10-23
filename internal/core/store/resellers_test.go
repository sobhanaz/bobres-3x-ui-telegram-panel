package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func debit(t *testing.T, s *Store, userID, currency string, amount int64, key string) (*Wallet, error) {
	t.Helper()
	var w *Wallet
	err := s.WithTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		w, err = s.Debit(context.Background(), tx, userID, currency, amount, &LedgerEntry{Kind: "purchase", IdempotencyKey: key})
		return err
	})
	return w, err
}

func setLimit(t *testing.T, s *Store, userID, currency string, limit int64) error {
	t.Helper()
	return s.SetCreditLimit(context.Background(), s.Conn(), userID, currency, limit)
}

// A reseller's wallet goes down to -credit_limit and no further; credits and
// zero entries go through while it is negative; the balance stays the sum of
// the ledger; a wallet without a limit behaves as before.
func TestCreditLimitFloor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 301, "reseller", "", "")
	if err := setLimit(t, s, u.ID, "IRT", 1000); err != nil {
		t.Fatal(err)
	}
	if w, err := debit(t, s, u.ID, "IRT", 600, "d1"); err != nil || w.Balance != -600 || w.CreditLimit != 1000 {
		t.Fatalf("debit on credit: %v %+v", err, w)
	}
	if w, err := debit(t, s, u.ID, "IRT", 400, "d2"); err != nil || w.Balance != -1000 {
		t.Fatalf("debit to the limit: %v %+v", err, w)
	}
	if _, err := debit(t, s, u.ID, "IRT", 1, "d3"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("past the limit: want ErrInsufficientFunds, got %v", err)
	}
	// A credit while negative lowers the debt; a zero entry passes too.
	if err := credit(t, s, u.ID, 100, "c1"); err != nil {
		t.Fatalf("credit while in debt: %v", err)
	}
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		return s.AppendLedger(ctx, tx, &LedgerEntry{UserID: u.ID, Currency: "IRT", Kind: "trial_grant", IdempotencyKey: "zero"})
	}); err != nil {
		t.Fatalf("zero entry while in debt: %v", err)
	}
	if bal, sum := balanceAndLedgerSum(t, s, u.ID); bal != -900 || sum != -900 {
		t.Fatalf("balance=%d ledger=%d, want -900/-900", bal, sum)
	}
	// A replay of a debit made on credit moves nothing.
	if _, err := debit(t, s, u.ID, "IRT", 600, "d1"); !errors.Is(err, ErrDuplicateIdempotency) {
		t.Fatalf("replay: %v", err)
	}

	// The limit cannot go below the debt (the database refuses it), and the
	// check holds against a direct write too.
	if err := setLimit(t, s, u.ID, "IRT", 899); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("limit below the debt: %v", err)
	}
	if err := setLimit(t, s, u.ID, "IRT", 900); err != nil {
		t.Fatalf("limit at the debt: %v", err)
	}
	_, err := s.DB().Exec(ctx, `UPDATE core.wallets SET balance = -901 WHERE user_id = $1 AND currency = 'IRT'`, u.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "wallets_balance_check" {
		t.Fatalf("balance below -credit_limit written directly: %v", err)
	}
	if err := s.ClearCreditLimits(ctx, s.Conn(), u.ID); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("credit taken away while in debt: %v", err)
	}

	// Limits are per currency: none in USDT.
	if _, err := debit(t, s, u.ID, "USDT", 1, "usdt"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("USDT without a limit: %v", err)
	}
	// Someone without a limit cannot go below zero; a credit at zero works.
	other, _ := s.UpsertUser(ctx, s.Conn(), 302, "customer", "", "")
	if _, err := debit(t, s, other.ID, "IRT", 1, "o1"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("customer overdraft: %v", err)
	}
	if err := credit(t, s, other.ID, 5, "o2"); err != nil {
		t.Fatal(err)
	}
	// (The refused USDT debit rolled its new wallet back.)
	if err := setLimit(t, s, u.ID, "USDT", 0); err != nil {
		t.Fatal(err)
	}
	ws, err := s.ListWallets(ctx, s.Conn(), u.ID)
	if err != nil || len(ws) != 2 || ws[0].Currency != "IRT" || ws[0].CreditLimit != 900 || ws[0].Balance != -900 || ws[1].CreditLimit != 0 {
		t.Fatalf("wallets: %v %+v", err, ws)
	}
	locked := 0
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.LockWallets(ctx, tx, u.ID)
		locked = len(got)
		return err
	}); err != nil || locked != 2 {
		t.Fatalf("lock wallets: %v %d", err, locked)
	}
}

// 20 workers race to spend 100 each on a credit of 1000 from a zero balance:
// exactly 10 succeed and the balance ends at the limit.
func TestCreditLimitConcurrentDebits(t *testing.T) {
	s := testStore(t)
	u, _ := s.UpsertUser(context.Background(), s.Conn(), 303, "racer", "", "")
	if err := setLimit(t, s, u.ID, "IRT", 1000); err != nil {
		t.Fatal(err)
	}
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := debit(t, s, u.ID, "IRT", 100, fmt.Sprintf("race-%d", i)); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 10 {
		t.Errorf("concurrent debits on credit: %d succeeded, want 10", ok)
	}
	if bal, sum := balanceAndLedgerSum(t, s, u.ID); bal != -1000 || sum != -1000 {
		t.Fatalf("balance=%d ledger=%d, want -1000/-1000", bal, sum)
	}
}

func seedSalePlan(t *testing.T, s *Store, price int64) *Plan {
	t.Helper()
	p := &Plan{NameI18n: map[string]string{"en": "P"}, Kind: "both", Price: price, Currency: "IRT", Enabled: true}
	if err := s.UpsertPlan(context.Background(), s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

// An order's label is copied to its subscription by provisioning; the
// customer's list pages, counts and searches by label or id suffix.
func TestSubscriptionLabelsAndPaging(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 311, "shop", "", "")
	other, _ := s.UpsertUser(ctx, s.Conn(), 312, "other", "", "")
	p := seedSalePlan(t, s, 100)
	labels := []string{"Ali's phone", "Sara laptop", "", "100% done", "ALI tablet"}
	var subs []*Subscription
	for i, l := range labels {
		var label *string
		if l != "" {
			label = &l
		}
		o, _, err := s.CreateOrder(ctx, s.Conn(), &Order{UserID: u.ID, PlanID: p.ID, Type: "new", Status: "paid",
			Amount: 100, Currency: "IRT", IdempotencyKey: fmt.Sprintf("lab-%d", i), Label: label})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetOrder(ctx, s.Conn(), o.ID); err != nil || deref(got.Label) != l {
			t.Fatalf("order label: %v %+v", err, got)
		}
		sub, err := s.CreateSubscription(ctx, s.Conn(), &Subscription{UserID: u.ID, OrderID: o.ID,
			ClientEmail: fmt.Sprintf("lab-%d", i), Label: o.Label})
		if err != nil || deref(sub.Label) != l {
			t.Fatalf("subscription label: %v %+v", err, sub)
		}
		subs = append(subs, sub)
	}
	// Someone else's and a deleted one never show.
	oo, _, _ := s.CreateOrder(ctx, s.Conn(), &Order{UserID: other.ID, PlanID: p.ID, Type: "new", Status: "paid",
		Amount: 100, Currency: "IRT", IdempotencyKey: "lab-other"})
	if _, err := s.CreateSubscription(ctx, s.Conn(), &Subscription{UserID: other.ID, OrderID: oo.ID, ClientEmail: "lab-other",
		Label: strptrT("Ali other")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSubscriptionStatus(ctx, s.Conn(), subs[1].ID, "deleted"); err != nil {
		t.Fatal(err)
	}

	page1, total, err := s.ListUserSubscriptions(ctx, s.Conn(), u.ID, "", Page{Limit: 2})
	if err != nil || total != 4 || len(page1) != 2 || page1[0].ID != subs[4].ID {
		t.Fatalf("page 1: %v total=%d %+v", err, total, page1)
	}
	page2, _, _ := s.ListUserSubscriptions(ctx, s.Conn(), u.ID, "", Page{Limit: 2, Offset: 2})
	page3, _, _ := s.ListUserSubscriptions(ctx, s.Conn(), u.ID, "", Page{Limit: 2, Offset: 4})
	if len(page2) != 2 || page2[0].ID != subs[2].ID || page2[1].ID != subs[0].ID || len(page3) != 0 {
		t.Fatalf("pages 2-3: %+v %+v", page2, page3)
	}

	cases := map[string][]string{
		"ali":   {subs[4].ID, subs[0].ID}, // any case, only this user's
		"  Ali": {subs[4].ID, subs[0].ID},
		"sara":  {},           // deleted
		"100%":  {subs[3].ID}, // % is a letter, not a wildcard
		"%":     {subs[3].ID},
		"_":     {},
		"zzz":   {},
	}
	short := strings.ReplaceAll(subs[2].ID, "-", "")[26:]
	cases["#"+short] = []string{subs[2].ID}
	cases[short] = []string{subs[2].ID}
	cases[strings.ReplaceAll(subs[2].ID, "-", "")[:8]] = []string{} // the id's start: only the end matches
	for q, want := range cases {
		got, total, err := s.ListUserSubscriptions(ctx, s.Conn(), u.ID, q, Page{Limit: 10})
		if err != nil || int(total) != len(want) || len(got) != len(want) {
			t.Errorf("query %q: %v total=%d got %d, want %d", q, err, total, len(got), len(want))
			continue
		}
		for i := range want {
			if got[i].ID != want[i] {
				t.Errorf("query %q: item %d is %s, want %s", q, i, got[i].ID, want[i])
			}
		}
	}

	// Renaming and clearing.
	if err := s.SetSubscriptionLabel(ctx, s.Conn(), subs[2].ID, strptrT("Reza")); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSubscription(ctx, s.Conn(), subs[2].ID); deref(got.Label) != "Reza" {
		t.Fatalf("renamed: %+v", got)
	}
	if err := s.SetSubscriptionLabel(ctx, s.Conn(), subs[2].ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSubscription(ctx, s.Conn(), subs[2].ID); got.Label != nil {
		t.Fatalf("cleared: %+v", got)
	}
	if err := s.SetSubscriptionLabel(ctx, s.Conn(), "00000000-0000-0000-0000-000000000099", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown subscription: %v", err)
	}
	// More than 64 characters is refused by the database.
	long := strings.Repeat("x", 65)
	if err := s.SetSubscriptionLabel(ctx, s.Conn(), subs[2].ID, &long); err == nil {
		t.Fatal("a 65-character label was stored")
	}
}

func strptrT(s string) *string { return &s }

func TestResellerTiersAndRows(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p1, p2 := seedSalePlan(t, s, 1000), seedSalePlan(t, s, 2000)

	gold := &ResellerTier{Name: "Gold", DiscountPercent: 20, CommissionPercent: 10, Prices: map[string]int64{p1.ID: 700}}
	if err := s.InsertTier(ctx, s.Conn(), gold); err != nil || gold.Version != 1 {
		t.Fatalf("insert tier: %v %+v", err, gold)
	}
	if err := s.InsertTier(ctx, s.Conn(), &ResellerTier{Name: "Gold"}); !errors.Is(err, ErrTierNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	silver := &ResellerTier{Name: "silver", DiscountPercent: 5, CommissionPercent: 3}
	if err := s.InsertTier(ctx, s.Conn(), silver); err != nil {
		t.Fatal(err)
	}
	// An update replaces the whole price list; an old version is stale.
	gold.Prices = map[string]int64{p2.ID: 1500}
	gold.DiscountPercent = 25
	if err := s.UpdateTier(ctx, s.Conn(), gold, 1); err != nil || gold.Version != 2 {
		t.Fatalf("update tier: %v %+v", err, gold)
	}
	if err := s.UpdateTier(ctx, s.Conn(), gold, 1); !errors.Is(err, ErrStale) {
		t.Fatalf("stale update: %v", err)
	}
	if err := s.UpdateTier(ctx, s.Conn(), &ResellerTier{ID: "00000000-0000-0000-0000-000000000099", Name: "x"}, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of a missing tier: %v", err)
	}
	silver.Name = "Gold"
	if err := s.UpdateTier(ctx, s.Conn(), silver, 1); !errors.Is(err, ErrTierNameTaken) {
		t.Fatalf("rename onto another tier: %v", err)
	}
	got, err := s.GetTier(ctx, s.Conn(), gold.ID, false)
	if err != nil || got.DiscountPercent != 25 || len(got.Prices) != 1 || got.Prices[p2.ID] != 1500 {
		t.Fatalf("get tier: %v %+v", err, got)
	}

	// Resellers on the tier: terms with and without their own rate.
	r1, _ := s.UpsertUser(ctx, s.Conn(), 321, "r_one", "", "")
	r2, _ := s.UpsertUser(ctx, s.Conn(), 322, "r_two", "", "")
	own := int32(15)
	for _, r := range []*Reseller{{UserID: r1.ID, TierID: gold.ID, Note: "pays on Fridays"}, {UserID: r2.ID, TierID: silver.ID, CommissionPercent: &own}} {
		if err := s.InsertReseller(ctx, s.Conn(), r); err != nil || r.Version != 1 {
			t.Fatalf("insert reseller: %v %+v", err, r)
		}
	}
	if terms, err := s.ResellerTerms(ctx, s.Conn(), r1.ID); err != nil || terms.TierName != "Gold" || terms.DiscountPercent != 25 || terms.CommissionPercent != 10 {
		t.Fatalf("terms r1: %v %+v", err, terms)
	}
	if terms, err := s.ResellerTerms(ctx, s.Conn(), r2.ID); err != nil || terms.CommissionPercent != 15 {
		t.Fatalf("terms r2 (own rate): %v %+v", err, terms)
	}
	if _, err := s.ResellerTerms(ctx, s.Conn(), p1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terms of a non-reseller: %v", err)
	}
	if err := s.DeleteTier(ctx, s.Conn(), gold.ID); !errors.Is(err, ErrTierInUse) {
		t.Fatalf("delete a tier in use: %v", err)
	}
	tiers, err := s.ListTiers(ctx, s.Conn())
	if err != nil || len(tiers) != 2 || tiers[0].Name != "Gold" || tiers[0].Resellers != 1 || tiers[0].Prices[p2.ID] != 1500 || tiers[1].Resellers != 1 {
		t.Fatalf("list tiers: %v %+v", err, tiers)
	}

	// Stale updates of a reseller; r1 in debt with two customers and a commission.
	upd := &Reseller{UserID: r1.ID, TierID: silver.ID, Note: "moved"}
	if err := s.UpdateReseller(ctx, s.Conn(), upd, 1); err != nil || upd.Version != 2 {
		t.Fatalf("update reseller: %v %+v", err, upd)
	}
	if err := s.UpdateReseller(ctx, s.Conn(), upd, 1); !errors.Is(err, ErrStale) {
		t.Fatalf("stale reseller update: %v", err)
	}
	if err := setLimit(t, s, r1.ID, "IRT", 5000); err != nil {
		t.Fatal(err)
	}
	if _, err := debit(t, s, r1.ID, "IRT", 3000, "r1-buy"); err != nil {
		t.Fatal(err)
	}
	for tg := int64(331); tg <= 332; tg++ {
		if _, err := s.UpsertUser(ctx, s.Conn(), tg, "", "", r1.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := s.Credit(ctx, tx, r1.ID, "IRT", 250, &LedgerEntry{Kind: "commission", IdempotencyKey: "commission:x"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	rows, total, err := s.ListResellers(ctx, s.Conn(), ResellerFilter{}, Page{Limit: 10})
	if err != nil || total != 2 || len(rows) != 2 || rows[0].User.ID != r2.ID {
		t.Fatalf("list resellers: %v total=%d %+v", err, total, rows)
	}
	one := rows[1]
	if one.TierName != "silver" || one.Customers != 2 || one.Earned["IRT"] != 250 || len(one.Wallets) != 1 ||
		one.Wallets[0].Balance != -2750 || one.Wallets[0].CreditLimit != 5000 || one.Commission() != 3 || one.Reseller.Note != "moved" {
		t.Fatalf("reseller row: %+v", one)
	}
	if rows[0].Commission() != 15 {
		t.Fatalf("own rate: %d", rows[0].Commission())
	}
	for name, f := range map[string]ResellerFilter{
		"by username": {Query: "@R_ONE"}, "by telegram id": {Query: "321"}, "in debt": {InDebt: true},
	} {
		if rows, total, err := s.ListResellers(ctx, s.Conn(), f, Page{Limit: 10}); err != nil || total != 1 || rows[0].User.ID != r1.ID {
			t.Errorf("%s: %v %d %+v", name, err, total, rows)
		}
	}
	if rows, total, _ := s.ListResellers(ctx, s.Conn(), ResellerFilter{TierID: gold.ID}, Page{Limit: 10}); total != 0 || len(rows) != 0 {
		t.Errorf("by tier: %d %+v", total, rows)
	}
	if row, err := s.GetResellerRow(ctx, s.Conn(), r1.ID); err != nil || row.Customers != 2 || row.Earned["IRT"] != 250 {
		t.Fatalf("get reseller row: %v %+v", err, row)
	}
	if _, err := s.GetResellerRow(ctx, s.Conn(), p1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row of a non-reseller: %v", err)
	}
	if n, err := s.CountInvited(ctx, s.Conn(), r1.ID); err != nil || n != 2 {
		t.Fatalf("invited: %v %d", err, n)
	}
	if earned, err := s.CommissionEarned(ctx, s.Conn(), r1.ID); err != nil || earned["IRT"] != 250 {
		t.Fatalf("earned: %v %v", err, earned)
	}
	n, debt, err := s.ResellerSummary(ctx, s.Conn())
	if err != nil || n != 2 || debt["IRT"] != 2750 || len(debt) != 1 {
		t.Fatalf("summary: %v %d %v", err, n, debt)
	}

	// Removal: the row goes, and a tier nobody uses can be deleted.
	if err := s.DeleteReseller(ctx, s.Conn(), r2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteReseller(ctx, s.Conn(), r2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if err := s.DeleteTier(ctx, s.Conn(), gold.ID); err != nil {
		t.Fatalf("delete an unused tier: %v", err)
	}
	if _, err := s.GetTier(ctx, s.Conn(), gold.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted tier: %v", err)
	}
	if err := s.DeleteTier(ctx, s.Conn(), gold.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete a missing tier: %v", err)
	}
}

// The users list narrows to the users someone invited.
func TestListUsersReferredBy(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	inviter, _ := s.UpsertUser(ctx, s.Conn(), 341, "inviter", "", "")
	a, _ := s.UpsertUser(ctx, s.Conn(), 342, "a", "", inviter.ID)
	_, _ = s.UpsertUser(ctx, s.Conn(), 343, "b", "", "")
	rows, total, err := s.ListUsers(ctx, s.Conn(), UserFilter{ReferredBy: inviter.ID}, Page{Limit: 10})
	if err != nil || total != 1 || rows[0].ID != a.ID {
		t.Fatalf("referred by: %v %d %+v", err, total, rows)
	}
	if _, total, _ := s.ListUsers(ctx, s.Conn(), UserFilter{}, Page{Limit: 10}); total != 3 {
		t.Fatalf("everyone: %d", total)
	}
}

// CancelOpenOrders cancels only the user's orders still waiting for payment.
func TestCancelOpenOrders(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 351, "shop", "", "")
	other, _ := s.UpsertUser(ctx, s.Conn(), 352, "other", "", "")
	p := seedSalePlan(t, s, 100)
	order := func(userID, status, key string) *Order {
		t.Helper()
		o, _, err := s.CreateOrder(ctx, s.Conn(), &Order{UserID: userID, PlanID: p.ID, Type: "new", Status: status,
			Amount: 100, Currency: "IRT", IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	want := map[*Order]string{
		order(u.ID, "created", "co-1"):              "cancelled",
		order(u.ID, "awaiting_payment", "co-2"):     "cancelled",
		order(u.ID, "paid", "co-3"):                 "paid",
		order(u.ID, "active", "co-4"):               "active",
		order(u.ID, "provision_failed", "co-5"):     "provision_failed",
		order(other.ID, "awaiting_payment", "co-6"): "awaiting_payment",
	}
	if n, err := s.CancelOpenOrders(ctx, s.Conn(), u.ID); err != nil || n != 2 {
		t.Fatalf("cancel: %v %d", err, n)
	}
	for o, status := range want {
		if got, _ := s.GetOrder(ctx, s.Conn(), o.ID); got.Status != status {
			t.Errorf("order %s: %s, want %s", o.IdempotencyKey, got.Status, status)
		}
	}
	if n, err := s.CancelOpenOrders(ctx, s.Conn(), u.ID); err != nil || n != 0 {
		t.Fatalf("cancel again: %v %d", err, n)
	}
}

// HoldReseller keeps a reseller's row from being locked for update (as a
// removal does) until its transaction ends; a non-reseller is not found.
func TestHoldReseller(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tier := &ResellerTier{Name: "Gold", DiscountPercent: 20, CommissionPercent: 10}
	if err := s.InsertTier(ctx, s.Conn(), tier); err != nil {
		t.Fatal(err)
	}
	r, _ := s.UpsertUser(ctx, s.Conn(), 361, "shop", "", "")
	c, _ := s.UpsertUser(ctx, s.Conn(), 362, "customer", "", "")
	if err := s.InsertReseller(ctx, s.Conn(), &Reseller{UserID: r.ID, TierID: tier.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx pgx.Tx) error { return s.HoldReseller(ctx, tx, c.ID) }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hold a customer: %v", err)
	}
	lockForUpdate := func() error {
		return s.WithTx(ctx, func(tx pgx.Tx) error {
			var one int
			return tx.QueryRow(ctx, `SELECT 1 FROM core.resellers WHERE user_id = $1 FOR UPDATE NOWAIT`, r.ID).Scan(&one)
		})
	}
	tx, err := s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if err := s.HoldReseller(ctx, tx, r.ID); err != nil {
		t.Fatalf("hold: %v", err)
	}
	var pgErr *pgconn.PgError
	if err := lockForUpdate(); !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("locked for update while held: %v", err)
	}
	// Two orders of one reseller can hold the row at once.
	if err := s.WithTx(ctx, func(tx pgx.Tx) error { return s.HoldReseller(ctx, tx, r.ID) }); err != nil {
		t.Fatalf("a second hold: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lockForUpdate(); err != nil {
		t.Fatalf("locked for update after the hold: %v", err)
	}
}

// A service is found by what a reseller pastes: its subscription link, its page address, a client config that carries its email in the name
// (vless, trojan, vmess), or the email itself. Another customer's service is never found, whatever is pasted.
func TestServicesAreFoundByLinkOrConfig(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 321, "shop2", "", "")
	other, _ := s.UpsertUser(ctx, s.Conn(), 322, "other2", "", "")
	p := seedSalePlan(t, s, 100)
	mk := func(owner *User, key, email, subID string) *Subscription {
		o, _, err := s.CreateOrder(ctx, s.Conn(), &Order{UserID: owner.ID, PlanID: p.ID, Type: "new", Status: "paid", Amount: 100, Currency: "IRT", IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := s.CreateSubscription(ctx, s.Conn(), &Subscription{UserID: owner.ID, OrderID: o.ID, ClientEmail: email})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET sub_id = $2 WHERE id = $1`, sub.ID, subID); err != nil {
			t.Fatal(err)
		}
		return sub
	}
	mine := mk(u, "lnk-1", "bobres-k3x9a7q2", "AbCdEf0123456789")
	_ = mk(u, "lnk-2", "bobres-zz11yy22", "ZyXwVu9876543210")
	theirs := mk(other, "lnk-3", "bobres-q8w7e6r5", "OtherSubId123456")

	vmess := `{"v":"2","ps":"Germany-bobres-k3x9a7q2","add":"h.example.com","port":"443","id":"11111111-2222-3333-4444-555555555555"}`
	cases := map[string]string{
		"https://sub.example.com:2096/sub/AbCdEf0123456789":                                              mine.ID,
		"https://sub.example.com:2096/sub/AbCdEf0123456789/":                                             mine.ID,
		"https://sub.example.com/sub/AbCdEf0123456789?name=x#frag":                                       mine.ID,
		"  https://sub.example.com/sub/AbCdEf0123456789  ":                                               mine.ID,
		"https://shop.example.com/s/" + mine.PageToken:                                                   mine.ID,
		"vless://11111111-2222-3333-4444-555555555555@h.example.com:443?type=ws#Germany-bobres-k3x9a7q2": mine.ID,
		"trojan://pw@h.example.com:443#Germany%2Dbobres-k3x9a7q2":                                        mine.ID,
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(vmess)):                                    mine.ID,
		"bobres-k3x9a7q2": mine.ID,
		"K3X9A7Q2":        mine.ID,
	}
	for query, want := range cases {
		got, total, err := s.ListUserServices(ctx, s.Conn(), u.ID, query, "", Page{Limit: 10})
		if err != nil || total != 1 || len(got) != 1 || got[0].ID != want {
			t.Errorf("%q: %v total=%d %+v", query, err, total, got)
		}
	}
	// Someone else's link or config, an unknown link, and text that only looks like one find nothing.
	for _, query := range []string{
		"https://sub.example.com/sub/OtherSubId123456",
		"vless://x@h:443#Germany-bobres-q8w7e6r5",
		"https://sub.example.com/sub/NoSuchSubId99999",
		"vless://x@h:443#nobody",
		"https://example.com/",
		"vless://no-name-at-all@h:443",
	} {
		if got, total, err := s.ListUserServices(ctx, s.Conn(), u.ID, query, "", Page{Limit: 10}); err != nil || total != 0 || len(got) != 0 {
			t.Errorf("%q found something: %v total=%d %+v", query, err, total, got)
		}
	}
	if got, total, _ := s.ListUserServices(ctx, s.Conn(), other.ID, "https://sub.example.com/sub/OtherSubId123456", "", Page{Limit: 10}); total != 1 || got[0].ID != theirs.ID {
		t.Errorf("the owner of the other service: %d %+v", total, got)
	}
	// The status filter still applies to a link.
	if _, total, _ := s.ListUserServices(ctx, s.Conn(), u.ID, "https://sub.example.com/sub/AbCdEf0123456789", "expired", Page{Limit: 10}); total != 0 {
		t.Errorf("a link ignored the status filter: %d", total)
	}
}
