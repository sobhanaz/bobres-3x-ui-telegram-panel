package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

const supplierTestKey = "supkey-xxxxxxxxxxxxxxxx"

// fakeSupplier is the supplier's reseller API behind a transport: it checks the key, keeps one order per external id
// (a retry returns the same order and charges nothing again), and can be told to fail.
type fakeSupplier struct {
	mu       sync.Mutex
	orders   map[string]string // external id -> the delivery it made
	charged  int               // orders charged
	calls    []string          // actions
	failWith int               // answer every call with this status (0: work)
	failBody string
	pending  bool // the order answer says pending, without a delivery
	catalog  string
}

func newFakeSupplier() *fakeSupplier {
	return &fakeSupplier{orders: map[string]string{}, catalog: `{"products":[
		{"id":"gem-1m","name":"Gemini Pro 1 month","description":"A Gemini Pro account","price":"2.60","stock":5},
		{"id":"chat-1m","name":"Chat Plus","price":4,"stock":0},
		{"id":"vpn-1y","title":"VPN 1 year","cost":10.5,"in_stock":true}]}`}
}

func (f *fakeSupplier) PostSupplier(_ context.Context, action, key string, body []byte) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, action)
	if key != supplierTestKey {
		return 401, []byte(`{"error":"bad key"}`), nil
	}
	if f.failWith != 0 {
		if f.failBody != "" {
			return f.failWith, []byte(f.failBody), nil
		}
		return f.failWith, []byte(`{"error":"nope","message":"refused"}`), nil
	}
	switch action {
	case "products":
		return 200, []byte(f.catalog), nil
	case "balance":
		return 200, []byte(`{"balance":"0.40","currency":"USDT"}`), nil
	case "order":
		var o struct {
			ProductID  string `json:"product_id"`
			Quantity   int    `json:"quantity"`
			ExternalID string `json:"external_order_id"`
		}
		if err := json.Unmarshal(body, &o); err != nil || o.ExternalID == "" {
			return 400, []byte(`{"error":"bad order"}`), nil
		}
		if f.pending {
			return 200, []byte(`{"status":"processing","order_id":"x"}`), nil
		}
		d, ok := f.orders[o.ExternalID]
		if !ok {
			f.charged++
			d = fmt.Sprintf("login%04d@example.com\npassword: s3cret%04d", f.charged, f.charged)
			f.orders[o.ExternalID] = d
		}
		b, _ := json.Marshal(map[string]any{"status": "delivered", "order_id": "x", "balance": "0.10", "delivery": map[string]any{"account": d}})
		return 200, b, nil
	}
	return 404, []byte(`{}`), nil
}

func (f *fakeSupplier) count(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == action {
			n++
		}
	}
	return n
}

func (f *fakeSupplier) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

type supplierEnv struct {
	t     *testing.T
	svc   *Service
	s     *store.Store
	fake  *fakeSupplier
	owner *store.User
	clock *time.Time
}

func newSupplierEnv(t *testing.T) *supplierEnv {
	t.Helper()
	svc, s := testService(t)
	grant(svc)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	e := &supplierEnv{t: t, svc: svc, s: s, owner: staffMember(t, s, 9500, "owner"), clock: &now, fake: newFakeSupplier()}
	svc.cfg.VendorNow = func() time.Time { return *e.clock }
	svc.SetSupplierTransport(e.fake)
	ctx := context.Background()
	if err := svc.SetSupplierKey(ctx, e.owner, supplierTestKey); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(ctx, e.owner, SettingSupplierEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *supplierEnv) sync() {
	e.t.Helper()
	if n, err := e.svc.SyncSupplierCatalog(context.Background()); err != nil || n != 3 {
		e.t.Fatalf("sync: %d %v", n, err)
	}
}

// sell switches a product on at a price.
func (e *supplierEnv) sell(id string, price int64) {
	e.t.Helper()
	on := true
	if _, err := e.svc.UpdateSupplierProduct(context.Background(), e.owner, id, SupplierEdit{Enabled: &on, SellPrice: &price}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *supplierEnv) fundIRT(u *store.User, amount int64) {
	e.t.Helper()
	fund(e.t, e.s, u.ID, amount)
}

func (e *supplierEnv) balance(u *store.User) int64 {
	e.t.Helper()
	w, err := e.s.GetWallet(context.Background(), e.s.Conn(), u.ID, "IRT")
	if err != nil {
		e.t.Fatal(err)
	}
	return w.Balance
}

func (e *supplierEnv) buy(u *store.User, product string, qty int, key string) (*SupplierPurchase, error) {
	return e.svc.BuySupplierProduct(context.Background(), SupplierBuyParams{UserID: u.ID, ProductID: product, Quantity: qty, IdempotencyKey: key})
}

func TestSupplierCatalogIsImportedOffAndKeepsTheOwnersChoices(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	all, err := e.svc.ListSupplierProducts(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("list: %v %d", err, len(all))
	}
	for _, p := range all {
		if p.Enabled || p.SellPrice != nil || !p.Listed {
			t.Fatalf("a product must come in off and unpriced: %+v", p)
		}
	}
	var gem, vpn store.SupplierProduct
	for _, p := range all {
		switch p.ID {
		case "gem-1m":
			gem = p
		case "vpn-1y":
			vpn = p
		}
	}
	if gem.CostMicro == nil || *gem.CostMicro != 2_600_000 || gem.Stock == nil || *gem.Stock != 5 || gem.Description != "A Gemini Pro account" {
		t.Fatalf("gem: %+v", gem)
	}
	if vpn.Name != "VPN 1 year" || vpn.CostMicro == nil || *vpn.CostMicro != 10_500_000 || vpn.Stock != nil {
		t.Fatalf("vpn read from other names: %+v", vpn)
	}
	// Nothing is offered until the owner prices and switches it on.
	if offers, _ := e.svc.SupplierOffers(ctx); len(offers) != 0 {
		t.Fatalf("offers before the owner chose: %d", len(offers))
	}
	on := true
	if _, err := e.svc.UpdateSupplierProduct(ctx, e.owner, "gem-1m", SupplierEdit{Enabled: &on}); err == nil {
		t.Fatal("a product without a price was switched on")
	}
	price := int64(450_000)
	p, err := e.svc.UpdateSupplierProduct(ctx, e.owner, "gem-1m", SupplierEdit{Enabled: &on, SellPrice: &price,
		Title: map[string]string{"fa": "جمینای پرو", "en": "  "}, Desc: map[string]string{"fa": "یک ماهه"}})
	if err != nil || !p.Enabled || *p.SellPrice != 450_000 || p.Title["fa"] != "جمینای پرو" || p.Title["en"] != "" {
		t.Fatalf("edit: %v %+v", err, p)
	}
	for name, bad := range map[string]SupplierEdit{
		"a negative price": {SellPrice: ptr64(-1)}, "a huge price": {SellPrice: ptr64(supplierMaxPrice + 1)},
		"another language": {Title: map[string]string{"de": "x"}}, "a long title": {Title: map[string]string{"fa": strings.Repeat("x", 201)}},
	} {
		if _, err := e.svc.UpdateSupplierProduct(ctx, e.owner, "gem-1m", bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// Clearing the price of a product on sale is refused (switch it off first).
	if _, err := e.svc.UpdateSupplierProduct(ctx, e.owner, "gem-1m", SupplierEdit{SellPrice: ptr64(0)}); err == nil {
		t.Fatal("the price of a product on sale was cleared")
	}
	// A second sync keeps the price, the texts and the switch, and takes the supplier's new stock.
	e.fake.set(func() { e.fake.catalog = strings.Replace(e.fake.catalog, `"stock":5`, `"stock":2`, 1) })
	e.sync()
	p, _ = e.s.GetSupplierProduct(ctx, e.s.Conn(), "gem-1m", false)
	if !p.Enabled || *p.SellPrice != 450_000 || p.Title["fa"] != "جمینای پرو" || *p.Stock != 2 {
		t.Fatalf("a sync lost the owner's choices: %+v", p)
	}
	// Offers: on, priced and in stock. An out-of-stock product, and one that vanished, are not offered.
	offers, _ := e.svc.SupplierOffers(ctx)
	if len(offers) != 1 || offers[0].ID != "gem-1m" {
		t.Fatalf("offers: %+v", offers)
	}
	e.fake.set(func() { e.fake.catalog = `{"products":[{"id":"chat-1m","name":"Chat Plus","price":4,"stock":9}]}` })
	if n, err := e.svc.SyncSupplierCatalog(ctx); err != nil || n != 1 {
		t.Fatalf("shrunk sync: %d %v", n, err)
	}
	p, _ = e.s.GetSupplierProduct(ctx, e.s.Conn(), "gem-1m", false)
	if p.Listed {
		t.Fatal("a vanished product is still listed")
	}
	if offers, _ := e.svc.SupplierOffers(ctx); len(offers) != 0 {
		t.Fatalf("a vanished product is offered: %+v", offers)
	}
	// A catalog that cannot be read changes nothing.
	e.fake.set(func() { e.fake.catalog = `<html>` })
	if _, err := e.svc.SyncSupplierCatalog(ctx); err == nil {
		t.Fatal("an unreadable catalog was accepted")
	}
	if b, err := e.svc.SupplierBalance(ctx, true); err != nil || b.Amount != "0.40" || b.Currency != "USDT" || b.Raw != "" {
		t.Fatalf("balance: %+v %v", b, err)
	}
}

// The supplier's balance answer is read in the shapes such APIs use; what is not understood is shown as it came.
func TestFindBalance(t *testing.T) {
	for name, tc := range map[string]struct{ in, amount, cur string }{
		"a number":          {`{"balance":5.53}`, "5.53", "USDT"},
		"a string":          {`{"balance":"5.53","currency":"usdt"}`, "5.53", "USDT"},
		"text with unit":    {`{"api_balance":"5.53 USDT"}`, "5.53", "USDT"},
		"a decimal comma":   {`{"balance":"5,53"}`, "5.53", "USDT"},
		"wrapped in data":   {`{"status":"ok","data":{"balance":5.53,"currency":"USD"}}`, "5.53", "USD"},
		"a balance object":  {`{"success":true,"balance":{"amount":"5.53","currency":"USDT"}}`, "5.53", "USDT"},
		"another name":      {`{"ok":true,"usdt_balance":5.53}`, "5.53", "USDT"},
		"capital letters":   {`{"Balance":5.53}`, "5.53", "USDT"},
		"zero is a balance": {`{"balance":0}`, "0", "USDT"},
		"dollar sign":       {`{"balance":"$5.53"}`, "5.53", "USDT"},
		"nothing about it":  {`{"status":"ok","orders":3}`, "", ""},
		"a bool":            {`{"balance":true}`, "", ""},
		"deeper than three": {`{"a":{"b":{"c":{"d":{"balance":1}}}}}`, "", ""},
	} {
		var v any
		if err := json.Unmarshal([]byte(tc.in), &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if amount, cur := findBalance(v); amount != tc.amount || cur != tc.cur {
			t.Errorf("%s: %q %q, want %q %q", name, amount, cur, tc.amount, tc.cur)
		}
	}
}

// An answer that cannot be read comes back as it is, without the key, so the owner can see what the supplier says.
func TestSupplierBalanceUnreadable(t *testing.T) {
	tr := supplierTransportFunc(func(action, key string) (int, []byte) {
		return 200, []byte(`{"status":"ok","note":"key ` + key + ` is active"}`)
	})
	b, err := NewSupplierClient(tr, "supkey-xxxxxxxxxxxxxxxx").Balance(context.Background())
	if err != nil || b.Amount != "" || !strings.Contains(b.Raw, `"status":"ok"`) || strings.Contains(b.Raw, "supkey") {
		t.Fatalf("%+v %v", b, err)
	}
}

type supplierTransportFunc func(action, key string) (int, []byte)

func (f supplierTransportFunc) PostSupplier(_ context.Context, action, key string, _ []byte) (int, []byte, error) {
	status, body := f(action, key)
	return status, body, nil
}

func ptr64(n int64) *int64 { return &n }

func TestSupplierBuyDeliversEncryptedOnceAndReplaysCleanly(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	e.sell("gem-1m", 450_000)
	buyer := seedUser(t, e.s, 9501)
	e.fundIRT(buyer, 2_000_000)

	p, err := e.buy(buyer, "gem-1m", 2, "tap-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Order.Status != "done" || p.Pending() || p.Replayed || !strings.Contains(p.Delivery, "login0001@example.com") || p.Order.Amount != 900_000 {
		t.Fatalf("purchase: %+v delivery=%q", p.Order, p.Delivery)
	}
	if got := e.balance(buyer); got != 1_100_000 {
		t.Fatalf("balance %d", got)
	}
	// What was delivered is stored encrypted, and shown again to its buyer only.
	o, _ := e.s.GetSupplierOrder(ctx, e.s.Conn(), p.Order.ID, false)
	if strings.Contains(string(o.DeliveryEnc), "login0001") || len(o.DeliveryEnc) == 0 {
		t.Fatal("the delivery is stored in clear")
	}
	if again, err := e.svc.SupplierDelivery(ctx, buyer.ID, p.Order.ID); err != nil || again.Delivery != p.Delivery {
		t.Fatalf("show again: %v %+v", err, again)
	}
	other := seedUser(t, e.s, 9502)
	if _, err := e.svc.SupplierDelivery(ctx, other.ID, p.Order.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another customer saw the account: %v", err)
	}
	// The same tap again: the first answer, no money, the supplier charges nothing more.
	rp, err := e.buy(buyer, "gem-1m", 2, "tap-1")
	if err != nil || !rp.Replayed || rp.Delivery != p.Delivery || rp.Order.ID != p.Order.ID {
		t.Fatalf("replay: %+v %v", rp, err)
	}
	if got := e.balance(buyer); got != 1_100_000 {
		t.Fatalf("a replay moved money: %d", got)
	}
	e.fake.mu.Lock()
	charged := e.fake.charged
	e.fake.mu.Unlock()
	if charged != 1 {
		t.Fatalf("the supplier charged %d times", charged)
	}
	if _, err := e.buy(buyer, "gem-1m", 1, "tap-1"); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("reused key: %v", err)
	}
	if bal, sum, _ := ledger(t, e.s, buyer.ID); bal != sum {
		t.Fatalf("wallet %d, ledger %d", bal, sum)
	}
}

func TestSupplierBuyRefusesWhatItShouldNot(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	e.sell("gem-1m", 450_000)
	buyer := seedUser(t, e.s, 9511)
	if _, err := e.buy(buyer, "gem-1m", 1, "poor"); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("an empty wallet: %v", err)
	}
	e.fundIRT(buyer, 5_000_000)
	for name, p := range map[string]SupplierBuyParams{
		"no key":          {UserID: buyer.ID, ProductID: "gem-1m", Quantity: 1},
		"zero quantity":   {UserID: buyer.ID, ProductID: "gem-1m", IdempotencyKey: "k1"},
		"eleven":          {UserID: buyer.ID, ProductID: "gem-1m", Quantity: 11, IdempotencyKey: "k2"},
		"more than stock": {UserID: buyer.ID, ProductID: "gem-1m", Quantity: 6, IdempotencyKey: "k3"},
		"a product off":   {UserID: buyer.ID, ProductID: "vpn-1y", Quantity: 1, IdempotencyKey: "k4"},
		"unknown product": {UserID: buyer.ID, ProductID: "nope", Quantity: 1, IdempotencyKey: "k5"},
		"a stale price":   {UserID: buyer.ID, ProductID: "gem-1m", Quantity: 1, IdempotencyKey: "k6", ExpectedAmount: 1},
	} {
		if _, err := e.svc.BuySupplierProduct(ctx, p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got := e.balance(buyer); got != 5_000_000 {
		t.Fatalf("a refused purchase cost money: %d", got)
	}
	if n := e.fake.count("order"); n != 0 {
		t.Fatalf("the supplier was asked without payment: %d", n)
	}
	if err := e.svc.SetSetting(ctx, e.owner, SettingSupplierEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.buy(buyer, "gem-1m", 1, "off"); !errors.Is(err, ErrState) {
		t.Fatalf("sales off: %v", err)
	}
	if err := e.svc.SetSetting(ctx, e.owner, SettingSupplierEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	e.s.DB().Exec(ctx, `UPDATE core.users SET status = 'banned' WHERE id = $1`, buyer.ID) //nolint:errcheck // test
	if _, err := e.buy(buyer, "gem-1m", 1, "banned"); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("banned: %v", err)
	}
}

func TestSupplierDownThenWorkerSettlesAndARefusalRefunds(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	e.sell("gem-1m", 450_000)
	buyer := seedUser(t, e.s, 9521)
	e.fundIRT(buyer, 3_000_000)

	// The supplier is down: paid, waiting.
	e.fake.set(func() { e.fake.failWith = 503 })
	p, err := e.buy(buyer, "gem-1m", 1, "down-1")
	if err != nil || !p.Pending() || p.Delivery != "" {
		t.Fatalf("down: %v %+v", err, p.Order)
	}
	if got := e.balance(buyer); got != 2_550_000 {
		t.Fatalf("balance %d", got)
	}
	if n, _ := e.svc.SettleSupplierOrders(ctx); n != 0 {
		t.Fatalf("settled before the wait: %d", n)
	}
	e.fake.set(func() { e.fake.failWith = 0 })
	*e.clock = e.clock.Add(supplierFirstWait * 4)
	if n, err := e.svc.SettleSupplierOrders(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	done, _ := e.svc.SupplierDelivery(ctx, buyer.ID, p.Order.ID)
	if done.Order.Status != "done" || !strings.Contains(done.Delivery, "login0001") {
		t.Fatalf("after the worker: %+v", done)
	}

	// The supplier says it has no stock or no balance: refunded at once.
	e.fake.set(func() { e.fake.failWith, e.fake.failBody = 402, `{"error":"insufficient_balance","message":"top up"}` })
	r, err := e.buy(buyer, "gem-1m", 1, "refuse-1")
	if err != nil || r.Order.Status != "refunded" || r.Delivery != "" || !strings.Contains(r.Order.LastError, "insufficient_balance") {
		t.Fatalf("refusal: %v %+v", err, r.Order)
	}
	if got := e.balance(buyer); got != 2_550_000 {
		t.Fatalf("the refund did not restore the balance: %d", got)
	}
	if bal, sum, _ := ledger(t, e.s, buyer.ID); bal != sum {
		t.Fatalf("wallet %d, ledger %d", bal, sum)
	}
	// A supplier that says "processing" with nothing delivered keeps the order waiting; the owner can retry or refund it.
	e.fake.set(func() { e.fake.failWith, e.fake.failBody, e.fake.pending = 0, "", true })
	w, err := e.buy(buyer, "gem-1m", 1, "wait-1")
	if err != nil || !w.Pending() {
		t.Fatalf("pending: %v %+v", err, w.Order)
	}
	if _, err := e.svc.RefundSupplierOrder(ctx, e.owner, w.Order.ID, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("refund without a reason: %v", err)
	}
	rf, err := e.svc.RefundSupplierOrder(ctx, e.owner, w.Order.ID, "the supplier never delivered")
	if err != nil || rf.Status != "refunded" {
		t.Fatalf("owner refund: %v %+v", err, rf)
	}
	if got := e.balance(buyer); got != 2_550_000 {
		t.Fatalf("balance after the owner's refund: %d", got)
	}
	if _, err := e.svc.RefundSupplierOrder(ctx, e.owner, done.Order.ID, "again"); !errors.Is(err, ErrState) {
		t.Fatalf("refunding a delivered order: %v", err)
	}
	rows, total, err := e.svc.ListSupplierOrdersAdmin(ctx, "", store.Page{Limit: 10})
	if err != nil || total != 3 || len(rows) != 3 || rows[0].ProductName == "" || len(rows[0].DeliveryEnc) != 0 {
		t.Fatalf("admin list: %v %d", err, total)
	}
}

func TestSupplierLostAnswerDoesNotChargeTwice(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	e.sell("gem-1m", 450_000)
	buyer := seedUser(t, e.s, 9531)
	e.fundIRT(buyer, 1_000_000)
	// The supplier made the order but its answer was lost: the same order id again returns it, charged once.
	e.fake.mu.Lock()
	e.fake.charged = 0
	e.fake.mu.Unlock()
	p, err := e.buy(buyer, "gem-1m", 1, "lost-1")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the retry of the same external id (what the worker does for a waiting order).
	if _, err := e.svc.RetrySupplierOrder(ctx, p.Order.ID); err != nil {
		t.Fatal(err)
	}
	e.fake.mu.Lock()
	charged := e.fake.charged
	e.fake.mu.Unlock()
	if charged != 1 {
		t.Fatalf("charged %d times", charged)
	}
}

func TestSupplierKeyIsWriteOnlyAndCoreNeedsABot(t *testing.T) {
	svc, s := testService(t)
	grant(svc)
	owner := staffMember(t, s, 9541, "owner")
	ctx := context.Background()
	for name, key := range map[string]string{"empty": "", "short": "abc", "a space": "sk live 0123456789", "a newline": "supkey-0123456789\nX: y"} {
		if err := svc.SetSupplierKey(ctx, owner, key); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := svc.SetSupplierKey(ctx, owner, supplierTestKey); err != nil {
		t.Fatal(err)
	}
	enc, err := s.GetSupplierKey(ctx, s.Conn())
	if err != nil || strings.Contains(string(enc), supplierTestKey) || len(enc) == 0 {
		t.Fatalf("the key is stored in clear: %v", err)
	}
	var details string
	if err := s.DB().QueryRow(ctx, `SELECT coalesce(before::text, '') || coalesce(after::text, '') FROM core.audit_log WHERE action = 'supplier.config' ORDER BY created_at DESC LIMIT 1`).Scan(&details); err != nil || strings.Contains(details, supplierTestKey) {
		t.Fatalf("the audit holds the key: %v %s", err, details)
	}
	// Without a way to reach the supplier nothing is sold, and no money moves.
	if err := svc.SetSetting(ctx, owner, SettingSupplierEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	buyer := seedUser(t, s, 9542)
	fund(t, s, buyer.ID, 1_000_000)
	if _, err := svc.BuySupplierProduct(ctx, SupplierBuyParams{UserID: buyer.ID, ProductID: "x", Quantity: 1, IdempotencyKey: "k"}); !errors.Is(err, ErrState) {
		t.Fatalf("without a transport: %v", err)
	}
}

func TestSupplierParsersAreTolerantAndNeverTrustTheAnswer(t *testing.T) {
	list, err := parseSupplierCatalog(json.RawMessage(`[{"product_id":"a","title":"A","price":"1.5"},{"id":"a","name":"dup"},{"id":"bad id","name":"x"},{"name":"no id"},{"id":"b","active":false}]`))
	if err != nil || len(list) != 2 || list[0].ID != "a" || *list[0].CostMicro != 1_500_000 || *list[1].Stock != 0 {
		t.Fatalf("root array: %v %+v", err, list)
	}
	for _, bad := range []string{`{"x":1}`, `"str"`, `{"products":[]}`, `{"products":[{"name":"no id"}]}`, `nope`} {
		if _, err := parseSupplierCatalog(json.RawMessage(bad)); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
	for name, c := range map[string]struct {
		in      string
		text    string
		pending bool
		fails   bool
	}{
		"a delivery map":   {`{"status":"ok","delivery":{"email":"a@b.c","password":"p"}}`, "email: a@b.c\npassword: p", false, false},
		"accounts list":    {`{"accounts":["a:1","b:2"]}`, "a:1\n\nb:2", false, false},
		"a nested order":   {`{"order":{"id":"9","delivery":"code-1"},"balance":3}`, "code-1", false, false},
		"nothing named":    {`{"id":"1","status":"done","login":"u","pass":"p"}`, "login: u\npass: p", false, false},
		"processing":       {`{"status":"processing"}`, "", true, false},
		"a failure status": {`{"status":"failed","message":"no stock"}`, "", false, true},
		"success false":    {`{"success":false,"message":"no"}`, "", false, true},
		"an error field":   {`{"error":"out_of_stock"}`, "", false, true},
	} {
		d, err := parseSupplierOrder(json.RawMessage(c.in))
		switch {
		case c.fails:
			var se *SupplierError
			if !errors.As(err, &se) || !se.Final() {
				t.Errorf("%s: %v, want a final SupplierError", name, err)
			}
		case err != nil:
			t.Errorf("%s: %v", name, err)
		case d.Text != c.text || d.Pending != c.pending:
			t.Errorf("%s: %q pending=%v, want %q pending=%v", name, d.Text, d.Pending, c.text, c.pending)
		}
	}
	if _, err := parseSupplierOrder(json.RawMessage(`[1]`)); err == nil {
		t.Error("a list answer was accepted for an order")
	}
	if got := (&SupplierError{Status: 429}).Final() || (&SupplierError{Status: 503}).Final() || (&SupplierError{Status: 408}).Final(); got {
		t.Error("a retryable status is final")
	}
	if !(&SupplierError{Status: 402}).Final() {
		t.Error("a 402 is not final")
	}
}

// The supplier's texts carry Telegram custom-emoji placeholders ("{pe:123}") and Windows line breaks: they are read without them
// (the raw product keeps them).
func TestSupplierCatalogTextIsCleaned(t *testing.T) {
	raw := `{"products":[{"id":19,"name":"{pe:5244675575502628242} GitHub Student","description":"{pe:5447644880824181073}⚠️ Use it\r\n\r\n{pe:1}✔️ Personal only","price":10,"stock":1}]}`
	got, err := parseSupplierCatalog([]byte(raw))
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].Name != "GitHub Student" || got[0].Description != "⚠️ Use it\n\n✔️ Personal only" {
		t.Fatalf("%q %q", got[0].Name, got[0].Description)
	}
	if !strings.Contains(string(got[0].Raw), "{pe:5244675575502628242}") {
		t.Fatal("the raw product was changed")
	}
}

// A balance read a moment ago is shown again; fresh asks the supplier; a new key forgets it.
func TestSupplierBalanceIsKeptForAMinute(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	read := func(fresh bool) SupplierBalance {
		b, err := e.svc.SupplierBalance(ctx, fresh)
		if err != nil || b.Amount != "0.40" {
			t.Fatalf("balance %+v %v", b, err)
		}
		return b
	}
	read(false)
	read(false)
	if n := e.fake.count("balance"); n != 1 {
		t.Fatalf("%d calls for two reads", n)
	}
	read(true)
	if n := e.fake.count("balance"); n != 2 {
		t.Fatalf("a fresh read made %d calls in all", n)
	}
	e.svc.supplierBal.mu.Lock()
	e.svc.supplierBal.at = time.Now().Add(-2 * supplierBalanceTTL)
	e.svc.supplierBal.mu.Unlock()
	read(false)
	if n := e.fake.count("balance"); n != 3 {
		t.Fatalf("an old balance was shown again: %d calls", n)
	}
	if err := e.svc.SetSupplierKey(ctx, e.owner, supplierTestKey); err != nil {
		t.Fatal(err)
	}
	read(false)
	if n := e.fake.count("balance"); n != 4 {
		t.Fatalf("a new key kept the old balance: %d calls", n)
	}
}

// The shelf shows a product that is switched on and priced even when it is sold out or the supplier no longer lists it; only an
// available one can be bought; the catalog is read every five minutes while something on the shelf is sold out, else every half hour.
func TestSupplierShelfShowsSoldOutProducts(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	e.sell("gem-1m", 450_000)  // 5 in stock
	e.sell("chat-1m", 300_000) // none in stock
	shelf, err := e.svc.SupplierShelf(ctx)
	if err != nil || len(shelf) != 2 {
		t.Fatalf("shelf: %d %v", len(shelf), err)
	}
	byID := map[string]store.SupplierProduct{}
	for _, p := range shelf {
		byID[p.ID] = p
	}
	if !byID["gem-1m"].Available() || byID["chat-1m"].Available() {
		t.Fatalf("availability: gem %v chat %v", byID["gem-1m"].Available(), byID["chat-1m"].Available())
	}
	offers, err := e.svc.SupplierOffers(ctx)
	if err != nil || len(offers) != 1 || offers[0].ID != "gem-1m" {
		t.Fatalf("offers (what can be bought): %v %v", offers, err)
	}
	if _, err := e.svc.SupplierProductBySID(ctx, SupplierSID("chat-1m")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a sold-out product found to buy: %v", err)
	}
	if got := e.svc.supplierSyncEvery(ctx); got != supplierSoldOutSyncInterval {
		t.Fatalf("interval with a sold-out product: %v", got)
	}
	// A product that is off or has no price is not on the shelf at all.
	if _, err := e.s.DB().Exec(ctx, `UPDATE core.supplier_products SET enabled = false, sell_price = NULL WHERE id = 'chat-1m'`); err != nil {
		t.Fatal(err)
	}
	if shelf, _ = e.svc.SupplierShelf(ctx); len(shelf) != 1 {
		t.Fatalf("shelf with a product off: %d", len(shelf))
	}
	if got := e.svc.supplierSyncEvery(ctx); got != supplierSyncInterval {
		t.Fatalf("interval with everything in stock: %v", got)
	}
	// The supplier stops listing the one in stock: sold out again.
	if _, err := e.s.DB().Exec(ctx, `UPDATE core.supplier_products SET listed = false WHERE id = 'gem-1m'`); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.supplierSyncEvery(ctx); got != supplierSoldOutSyncInterval {
		t.Fatalf("interval with an unlisted product: %v", got)
	}
}
