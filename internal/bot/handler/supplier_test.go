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
	mu      sync.Mutex
	orders  map[string]string
	n       int
	down    bool
	catalog string // the products answer; "" is the two products below
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
		if s.catalog != "" {
			return 200, []byte(s.catalog), nil
		}
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
	w, sup, owner := supplierWorldWith(t, "", 2)
	ctx := context.Background()
	on, price := true, int64(450_000)
	if _, err := w.stack.Domain.UpdateSupplierProduct(ctx, w.storeUser(ownerID), "a-very-long-supplier-product-id-that-would-never-fit-a-callback-0001",
		coredomain.SupplierEdit{Enabled: &on, SellPrice: &price, Title: map[string]string{"en": "Gemini Pro 1 month"}, Desc: map[string]string{"en": "A <b>fresh</b> account"}}); err != nil {
		t.Fatal(err)
	}
	return w, sup, owner
}

// supplierWorldWith is a store that can resell, whose supplier answers catalog ("" is the default two products) with count products.
func supplierWorldWith(t *testing.T, catalog string, count int) (*world, *testSupplier, *person) {
	t.Helper()
	sup := &testSupplier{orders: map[string]string{}, catalog: catalog}
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
	if n, err := w.stack.Domain.SyncSupplierCatalog(ctx); err != nil || n != count {
		t.Fatalf("sync: %d %v", n, err)
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
	// A product page with the owner's description (escaped), the unit price and the stock, and Buy now.
	sid := strings.TrimPrefix(sidData, "sup:p:")
	buyer.tap(sidData)
	buyer.shownHas("A &lt;b&gt;fresh&lt;/b&gt; account")
	buyer.shownHas("Unit price")
	buyer.shownHas("Stock:")
	// How many: no more than the 9 in stock; the order summary says the wallet is short, with no Pay button.
	buyer.tap(callbackWithPrefix(buyer.shown(), "sup:q:"))
	buyer.shownHas("Select quantity")
	if hasButton(buyer.shown(), "sup:s:"+sid+":10") || !hasButton(buyer.shown(), "sup:s:"+sid+":5") {
		t.Fatalf("the quantities with 9 in stock: %v", callbacks(buyer.shown()))
	}
	buyer.tap("sup:s:" + sid + ":2")
	buyer.shownHas("Order summary")
	buyer.shownHas("short by")
	if hasButton(buyer.shown(), "sup:y") {
		t.Fatal("a Pay button for an empty wallet")
	}
	buyer.lastButton("Top up")
	if sup.made() != 0 {
		t.Fatal("the supplier was asked without payment")
	}

	// Funded, the buyer pays; the account arrives escaped; the same button again is the same order.
	w.fund(7201, 1_000_000)
	buyer.tap("sup:s:" + sid + ":1")
	buyer.shownHas("Ready to pay")
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
	buyer.tap("sup:s:" + strings.TrimPrefix(sidData, "sup:p:") + ":1")
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
	buyer.lastButton("Gemini Pro 1 month — ")
	buyer.lastButton("Chat Plus — (out of stock)")
	var chat string
	for _, b := range buttons(buyer.shown()) {
		if strings.HasPrefix(b.Text, "Chat Plus") {
			chat = b.CallbackData
		}
	}
	buyer.tap(chat)
	buyer.shownHas("Read &lt;this&gt; first")
	buyer.shownHas("Sold out right now")
	if hasButton(buyer.shown(), "sup:y") || hasButton(buyer.shown(), "sup:q") {
		t.Fatalf("a Buy or Pay button for a sold-out product: %v", callbacks(buyer.shown()))
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
	buyer.lastButton("Gemini Pro 1 month — (out of stock)")

	// Back in stock: for sale again.
	if _, err := w.stack.Store.DB().Exec(ctx, `UPDATE core.supplier_products SET listed = true, stock = 3 WHERE name IN ('Gemini Pro', 'Chat')`); err != nil {
		t.Fatal(err)
	}
	buyer.tap("sup")
	if strings.Contains(strings.Join(buyer.shown().labels(), "|"), "Sold out") {
		t.Fatalf("a restocked product is still sold out: %v", buyer.shown().labels())
	}
	buyer.tap(chat)
	buyer.tap(callbackWithPrefix(buyer.shown(), "sup:q:"))
	buyer.tap(strings.Replace(chat, "sup:p:", "sup:s:", 1) + ":1")
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

// The supplier's own categories make the digital accounts home a grid (two to a row, red when nothing in one is in stock), each category lists
// its products (green, red and "(out of stock)" when sold out, with a red Back), a product shows the supplier's description until the owner
// writes one, and the order goes through how many (a button or a typed number) and a summary before it is paid.
func TestSupplierCategoriesQuantityAndSummary(t *testing.T) {
	const catalog = `{"products":[
	 {"id":"tp3","name":"Telegram Premium — 3 Months","price":13.5,"stock":5,"category":"Telegram Premium","description":"Premium for 3 months, ready at once."},
	 {"id":"tp6","name":"Telegram Premium — 6 Months","price":17.5,"stock":0,"category":"Telegram Premium"},
	 {"id":"cl1","name":"100$ API Claude 30D","price":2,"stock":7,"category":"{pe:5244675575502628242} Claude"},
	 {"id":"vp1","name":"NordVpn 3 Months Link","price":2,"stock":0,"category":"VPN"},
	 {"id":"mi1","name":"Misc thing","price":1,"stock":2}]}`
	w, sup, owner := supplierWorldWith(t, catalog, 5)
	owner.text("/set supplier.enabled true")
	ctx := context.Background()
	on := true
	for id, price := range map[string]int64{"tp3": 600_000, "tp6": 800_000, "cl1": 90_000, "vp1": 90_000, "mi1": 40_000} {
		price := price
		if _, err := w.stack.Domain.UpdateSupplierProduct(ctx, w.storeUser(ownerID), id, coredomain.SupplierEdit{Enabled: &on, SellPrice: &price}); err != nil {
			t.Fatal(err)
		}
	}
	buyer := w.customer(7231, "buyer")
	w.fund(7231, 5_000_000)

	// The home: four categories in two rows of two (the order of the shelf, "Other" last); the VPN one is red because nothing in it is in stock.
	buyer.text("/menu")
	buyer.tap("sup")
	buyer.shownHas("Pick a category")
	kb := buyer.shown().kb.InlineKeyboard
	if len(kb) < 3 || len(kb[0]) != 2 || len(kb[1]) != 2 {
		t.Fatalf("the grid: %+v", kb)
	}
	styles := map[string]string{"✈️ Telegram Premium": "primary", "🧠 Claude": "primary", "🛡 VPN": "danger", "📦 Other": "primary"}
	data := map[string]string{}
	var order []string
	for _, row := range kb[:2] {
		for _, b := range row {
			if styles[b.Text] == "" || b.Style != styles[b.Text] || !strings.HasPrefix(b.CallbackData, "sup:c:") {
				t.Errorf("a category button: %+v", b)
			}
			data[b.Text] = b.CallbackData
			order = append(order, b.Text)
		}
	}
	if len(data) != 4 || order[3] != "📦 Other" {
		t.Fatalf("the categories: %v", order)
	}
	tgData := data["✈️ Telegram Premium"]

	// A category: its products, green or red, and a red Back.
	buyer.tap(tgData)
	buyer.shownHas("Telegram Premium")
	rows := buyer.shown().kb.InlineKeyboard
	if len(rows) != 3 || len(rows[0]) != 1 || rows[0][0].Style != "success" || !strings.Contains(rows[0][0].Text, "Telegram Premium — 3 Months — ") ||
		rows[1][0].Style != "danger" || !strings.HasSuffix(rows[1][0].Text, "(out of stock)") {
		t.Fatalf("the products: %+v", rows)
	}
	if back := rows[2][0]; back.CallbackData != "sup" || back.Style != "danger" {
		t.Fatalf("Back: %+v", back)
	}
	// The product page shows the supplier's description (the owner wrote none), the price and the stock; Back returns to the category.
	buyer.tap(rows[0][0].CallbackData)
	buyer.shownHas("Premium for 3 months, ready at once.")
	buyer.shownHas("Unit price")
	buyer.shownHas("Stock:")
	if back := buyer.shown().kb.InlineKeyboard; back[len(back)-1][0].CallbackData != tgData {
		t.Fatalf("Back from the product: %+v", back)
	}
	sid := strings.TrimPrefix(rows[0][0].CallbackData, "sup:p:")

	// How many: a typed number is checked against the stock (5); 3 gives the summary.
	buyer.tap("sup:q:" + sid)
	buyer.tap("sup:n:" + sid)
	buyer.shownHas("Type how many")
	buyer.text("abc")
	buyer.shownHas("whole number from 1 to 5")
	buyer.text("99")
	buyer.shownHas("whole number from 1 to 5")
	buyer.text("3")
	buyer.shownHas("Order summary")
	buyer.shownHas("3 × 600,000")
	buyer.shownHas("Ready to pay")
	pay := callbackWithPrefix(buyer.shown(), "sup:y:")
	if !strings.HasPrefix(pay, "sup:y:"+sid+":3:") || len(pay) > 64 {
		t.Fatalf("the pay button: %q", pay)
	}
	buyer.tap(pay)
	buyer.shownHas("acc1@example.com")
	if got := w.balanceOf(7231); got != 5_000_000-3*600_000 || sup.made() != 1 {
		t.Fatalf("balance %d, orders %d", got, sup.made())
	}
	// "Other" holds the product the supplier gave no category.
	buyer.tap("sup")
	buyer.tap(data["📦 Other"])
	buyer.lastButton("Misc thing — ")
}
