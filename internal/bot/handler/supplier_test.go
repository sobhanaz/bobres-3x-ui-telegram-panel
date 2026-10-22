package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

// testSupplier is the supplier's reseller API behind core's transport: one order per external id, and it can be down.
type testSupplier struct {
	mu     sync.Mutex
	orders map[string]string
	n      int
	down   bool
}

func (s *testSupplier) PostSupplier(_ context.Context, action, key string, body []byte) (int, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != "supkey-xxxxxxxxxxxxxxxx" {
		return 401, []byte(`{"error":"bad key"}`), nil
	}
	if s.down {
		return 503, []byte(`{"error":"down"}`), nil
	}
	switch action {
	case "products":
		return 200, []byte(`{"products":[{"id":"a-very-long-supplier-product-id-that-would-never-fit-a-callback-0001","name":"Gemini Pro","price":2.6,"stock":9},{"id":"chat","name":"Chat","price":1,"stock":0}]}`), nil
	case "order":
		var o struct {
			ExternalID string `json:"external_order_id"`
		}
		_ = json.Unmarshal(body, &o)
		d, ok := s.orders[o.ExternalID]
		if !ok {
			s.n++
			d = fmt.Sprintf("acc%d@example.com / pw<1>%d", s.n, s.n)
			s.orders[o.ExternalID] = d
		}
		b, _ := json.Marshal(map[string]any{"status": "delivered", "delivery": d})
		return 200, b, nil
	}
	return 404, []byte(`{}`), nil
}

func (s *testSupplier) made() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }

func supplierWorld(t *testing.T) (*world, *testSupplier, *person) {
	t.Helper()
	sup := &testSupplier{orders: map[string]string{}}
	w := newWorldOpts(t, []int64{ownerID}, testenv.WithDomainConfig(func(c *coredomain.Config) {
		c.SupplierTransport = sup
		yes := true
		c.PrivateFeaturesOverride = &yes
	}))
	owner := w.customer(ownerID, "owner")
	ctx := context.Background()
	o := w.storeUser(ownerID)
	if err := w.stack.Domain.SetSupplierKey(ctx, o, "supkey-xxxxxxxxxxxxxxxx"); err != nil {
		t.Fatal(err)
	}
	if n, err := w.stack.Domain.SyncSupplierCatalog(ctx); err != nil || n != 2 {
		t.Fatalf("sync: %d %v", n, err)
	}
	on, price := true, int64(450_000)
	if _, err := w.stack.Domain.UpdateSupplierProduct(ctx, o, "a-very-long-supplier-product-id-that-would-never-fit-a-callback-0001",
		coredomain.SupplierEdit{Enabled: &on, SellPrice: &price, Title: map[string]string{"en": "Gemini Pro 1 month"}, Desc: map[string]string{"en": "A <b>fresh</b> account"}}); err != nil {
		t.Fatal(err)
	}
	return w, sup, owner
}

func TestSupplierResaleFlow(t *testing.T) {
	w, sup, owner := supplierWorld(t)
	buyer := w.customer(7201, "buyer")

	// Off by default: no button, and an old one is told so.
	buyer.text("/menu")
	if hasButton(buyer.shown(), "sup") {
		t.Fatalf("a store that does not resell shows the button: %v", buyer.shown().labels())
	}
	buyer.tap("sup")
	buyer.toast("not for sale")
	owner.text("/set supplier.enabled true")
	owner.sees("Saved")
	buyer.text("/menu")
	if !hasButton(buyer.shown(), "sup") {
		t.Fatalf("no button once on: %v", buyer.shown().labels())
	}

	// The list: only the product the owner switched on, with the owner's name and price; a long supplier id fits a button.
	buyer.tap("sup")
	buyer.lastButton("Gemini Pro 1 month")
	if strings.Contains(strings.Join(buyer.shown().labels(), "|"), "Chat") {
		t.Fatalf("a product that is off is listed: %v", buyer.shown().labels())
	}
	var sidData string
	for _, c := range callbacks(buyer.shown()) {
		if strings.HasPrefix(c, "sup:p:") {
			sidData = c
		}
		if len(c) > 64 {
			t.Fatalf("callback data over 64 bytes: %q", c)
		}
	}
	if sidData == "" {
		t.Fatal("no product button")
	}
	// A product page with the owner's description (escaped), and no Pay button while the wallet is short.
	buyer.tap(sidData)
	buyer.shownHas("A &lt;b&gt;fresh&lt;/b&gt; account")
	if hasButton(buyer.shown(), "sup:y") {
		t.Fatal("a Pay button for an empty wallet")
	}
	buyer.lastButton("Top up")
	if sup.made() != 0 {
		t.Fatal("the supplier was asked without payment")
	}

	// Funded, the buyer pays; the account arrives escaped; the same button again is the same order.
	w.fund(7201, 1_000_000)
	buyer.tap(sidData)
	pay := callbackWithPrefix(buyer.shown(), "sup:y:")
	buyer.tap(pay)
	buyer.shownHas("acc1@example.com / pw&lt;1&gt;1")
	if got := w.balanceOf(7201); got != 550_000 {
		t.Fatalf("balance %d, want 550000", got)
	}
	buyer.tap(pay)
	if got := w.balanceOf(7201); got != 550_000 || sup.made() != 1 {
		t.Fatalf("a double tap: balance %d, orders %d", got, sup.made())
	}
	// My purchases lists it and shows the account again; another customer sees nothing.
	buyer.tap("sup:my")
	buyer.shownHas("Gemini Pro 1 month")
	buyer.press("sup:o:")
	buyer.shownHas("acc1@example.com")
	other := w.customer(7202, "other")
	other.text("/menu")
	other.tap("sup:my")
	other.shownHas("no purchases yet")
}

func TestSupplierDownLeavesTheOrderWaitingAndTheWorkerDelivers(t *testing.T) {
	w, sup, owner := supplierWorld(t)
	owner.text("/set supplier.enabled true")
	buyer := w.customer(7211, "buyer")
	w.fund(7211, 1_000_000)
	sup.mu.Lock()
	sup.down = true
	sup.mu.Unlock()
	buyer.text("/menu")
	buyer.tap("sup")
	var sidData string
	for _, c := range callbacks(buyer.shown()) {
		if strings.HasPrefix(c, "sup:p:") {
			sidData = c
		}
	}
	buyer.tap(sidData)
	buyer.tap(callbackWithPrefix(buyer.shown(), "sup:y:"))
	buyer.shownHas("Payment received")
	if got := w.balanceOf(7211); got != 550_000 {
		t.Fatalf("balance %d", got)
	}
	sup.mu.Lock()
	sup.down = false
	sup.mu.Unlock()
	w.stack.Store.DB().Exec(context.Background(), `UPDATE core.supplier_orders SET next_attempt_at = now() - interval '1 minute'`) //nolint:errcheck // test
	if n, err := w.stack.Domain.SettleSupplierOrders(context.Background()); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	buyer.tap("sup:my")
	buyer.press("sup:o:")
	buyer.shownHas("acc1@example.com")
}

// A product that is switched on shows even when it is out of stock or the supplier no longer lists it: marked sold out, its description can
// be read, and it cannot be bought (not even with a crafted button); when it is back in stock it is for sale again at once.
func TestSupplierSoldOutProductsAreShownButNotSold(t *testing.T) {
	w, sup, owner := supplierWorld(t)
	owner.text("/set supplier.enabled true")
	ctx := context.Background()
	o := w.storeUser(ownerID)
	on, price := true, int64(300_000)
	if _, err := w.stack.Domain.UpdateSupplierProduct(ctx, o, "chat", coredomain.SupplierEdit{Enabled: &on, SellPrice: &price,
		Title: map[string]string{"en": "Chat Plus"}, Desc: map[string]string{"en": "Read <this> first"}}); err != nil {
		t.Fatal(err)
	}
	buyer := w.customer(7211, "buyer")
	w.fund(7211, 2_000_000)

	// Chat has no stock: listed, marked, with its description, and no way to pay.
	buyer.text("/menu")
	buyer.tap("sup")
	buyer.lastButton("Gemini Pro 1 month · ")
	buyer.lastButton("Chat Plus · ⛔ Sold out")
	var chat string
	for _, b := range buttons(buyer.shown()) {
		if strings.HasPrefix(b.Text, "Chat Plus") {
			chat = b.CallbackData
		}
	}
	buyer.tap(chat)
	buyer.shownHas("Read &lt;this&gt; first")
	buyer.shownHas("Sold out right now")
	if hasButton(buyer.shown(), "sup:y") {
		t.Fatalf("a Pay button for a sold-out product: %v", callbacks(buyer.shown()))
	}
	// A crafted Pay button for it buys nothing and charges nothing.
	buyer.tap(strings.Replace(chat, "sup:p:", "sup:y:", 1) + ":n1")
	if sup.made() != 0 || w.balanceOf(7211) != 2_000_000 {
		t.Fatalf("a sold-out product was bought: %d orders, balance %d", sup.made(), w.balanceOf(7211))
	}

	// The supplier no longer lists Gemini: it is shown as sold out too.
	if err := w.stack.Store.DB().QueryRow(ctx, `UPDATE core.supplier_products SET listed = false WHERE name = 'Gemini Pro' RETURNING 1`).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	buyer.tap("sup")
	buyer.lastButton("Gemini Pro 1 month · ⛔ Sold out")

	// Back in stock: for sale again.
	if _, err := w.stack.Store.DB().Exec(ctx, `UPDATE core.supplier_products SET listed = true, stock = 3 WHERE name IN ('Gemini Pro', 'Chat')`); err != nil {
		t.Fatal(err)
	}
	buyer.tap("sup")
	if strings.Contains(strings.Join(buyer.shown().labels(), "|"), "Sold out") {
		t.Fatalf("a restocked product is still sold out: %v", buyer.shown().labels())
	}
	buyer.tap(chat)
	buyer.press("sup:y:")
	buyer.shownHas("acc1@example.com")
}

// The owner's own store has its two buttons in the bottom menu too, each while it is on, and a tap opens the screen.
func TestBottomMenuHasTheOwnersStoreButtons(t *testing.T) {
	w, _, owner := supplierWorld(t)
	cat := w.h.cat
	u := w.customer(7221, "menu_user")
	u.text("/menu")
	texts := func() string {
		var all []string
		for _, row := range u.lastMenu().Keyboard {
			for _, b := range row {
				all = append(all, b.Text)
			}
		}
		return strings.Join(all, "|")
	}
	if got := texts(); strings.Contains(got, cat.T("en", "btn.supplier")) || strings.Contains(got, cat.T("en", "btn.licenses")) {
		t.Fatalf("buttons of a store that sells neither: %s", got)
	}
	owner.text("/set supplier.enabled true")
	owner.text("/set vendor.enabled true")
	u.text("/menu")
	got := texts()
	if !strings.Contains(got, cat.T("en", "btn.supplier")) || !strings.Contains(got, cat.T("en", "btn.licenses")) {
		t.Fatalf("the bottom menu lacks the owner's buttons: %s", got)
	}
	// They sit right under Buy, side by side.
	var buyRow int
	for i, row := range u.lastMenu().Keyboard {
		if len(row) == 1 && row[0].Text == cat.T("en", "btn.buy") {
			buyRow = i
		}
	}
	if next := u.lastMenu().Keyboard[buyRow+1]; len(next) != 2 || next[0].Text != cat.T("en", "btn.supplier") || next[1].Text != cat.T("en", "btn.licenses") {
		t.Fatalf("the row under Buy: %+v", next)
	}
	// A tap opens the screen.
	u.tapMenu("en", "btn.supplier")
	u.shownHas("Digital")
	u.tapMenu("en", "btn.licenses")
	u.shownHas("Licenses") // the screen opens (this world has no license server, so it says licenses are not for sale)
	// One at a time, alone in its row.
	owner.text("/set vendor.enabled false")
	u.text("/menu")
	if next := u.lastMenu().Keyboard[buyRow+1]; len(next) != 1 || next[0].Text != cat.T("en", "btn.supplier") {
		t.Fatalf("the row with only digital accounts: %+v", next)
	}
}
