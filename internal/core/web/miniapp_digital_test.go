package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

// digitalSupplier is the supplier's API: a catalog, and an order that delivers one account per external id (a retry gives the same one).
type digitalSupplier struct {
	mu      sync.Mutex
	orders  map[string]string
	pending bool
}

func (d *digitalSupplier) PostSupplier(_ context.Context, action, _ string, body []byte) (int, []byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch action {
	case "products":
		return 200, []byte(`{"products":[{"id":"gem-1m","name":"Gemini Pro 1 month","category":"AI","description":"A Gemini account","price":2.6,"stock":5},
			{"id":"chat-1m","name":"Chat Plus","category":"AI","price":4,"stock":0}]}`), nil
	case "order":
		if d.pending {
			return 200, []byte(`{"status":"processing","order_id":"x"}`), nil
		}
		var o struct {
			ExternalID string `json:"external_order_id"`
		}
		if err := json.Unmarshal(body, &o); err != nil || o.ExternalID == "" {
			return 400, []byte(`{"error":"bad order"}`), nil
		}
		ext := o.ExternalID
		acct, ok := d.orders[ext]
		if !ok {
			acct = fmt.Sprintf("user%d@example.com\npassword: pw%d", len(d.orders), len(d.orders))
			d.orders[ext] = acct
		}
		return 200, []byte(fmt.Sprintf(`{"status":"delivered","order_id":"x","delivery":{"account":%q}}`, acct)), nil
	}
	return 404, []byte(`{}`), nil
}

// digitalFix is a Mini App with a store that sells the supplier's accounts: one product priced and on, one sold out.
func digitalFix(t *testing.T) (*miniFix, *digitalSupplier) {
	t.Helper()
	m := newMiniFixture(t, true)
	clean := func() {
		m.sql(`DELETE FROM core.supplier_orders`)
		m.sql(`DELETE FROM core.supplier_products`)
		m.sql(`DELETE FROM core.supplier_config`)
	}
	clean()
	t.Cleanup(clean)
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	m.dom = domain.New(m.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env, PrivateFeaturesOverride: &granted})
	m.api.cfg.Domain = m.dom
	sup := &digitalSupplier{orders: map[string]string{}}
	m.dom.SetSupplierTransport(sup)
	return m, sup
}

func (m *miniFix) openDigitalShelf() {
	m.t.Helper()
	ctx, owner := context.Background(), actor0(m.fixture)
	if err := m.dom.SetSupplierKey(ctx, owner, "supkey-xxxxxxxxxxxxxxxx"); err != nil {
		m.t.Fatal(err)
	}
	m.setting(domain.SettingSupplierEnabled, "true")
	if n, err := m.dom.SyncSupplierCatalog(ctx); err != nil || n != 2 {
		m.t.Fatalf("sync: %d %v", n, err)
	}
	on, price := true, int64(450000)
	title := map[string]string{"fa": "جمینای پرو ۱ ماهه"}
	if _, err := m.dom.UpdateSupplierProduct(ctx, owner, "gem-1m", domain.SupplierEdit{Enabled: &on, SellPrice: &price, Title: title}); err != nil {
		m.t.Fatal(err)
	}
}

func userID(m *miniFix, tok string) string {
	m.t.Helper()
	_, out := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil)
	return out["user"].(map[string]any)["id"].(string)
}

// A store that does not sell accounts shows no section and its routes do not exist; one that does lists the priced products in both
// languages, with the stock, and a customer buys with the wallet, once however often the request is sent.
func TestMiniDigitalAccounts(t *testing.T) {
	m, sup := digitalFix(t)
	tok := m.signIn("tg:6101:dina")

	// Not for sale: no flag, no shelf.
	if f := m.features(tok); f["digital"] != false {
		t.Fatalf("flag with nothing for sale: %v", f)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/digital", tok, nil); code != http.StatusNotFound {
		t.Fatalf("shelf with nothing for sale: %d", code)
	}

	m.openDigitalShelf()
	if f := m.features(tok); f["digital"] != true {
		t.Fatalf("flag with a product on sale: %v", f)
	}
	code, out := m.call(http.MethodGet, "/api/mini/v1/digital", tok, nil)
	items, _ := out["items"].([]any)
	if code != http.StatusOK || len(items) != 1 || out["max_quantity"] != float64(25) {
		t.Fatalf("shelf: %d %v", code, out)
	}
	it := items[0].(map[string]any)
	names := it["names"].(map[string]any)
	if it["price"] != float64(450000) || names["fa"] != "جمینای پرو ۱ ماهه" || names["en"] != "Gemini Pro 1 month" || it["category"] != "AI" ||
		it["stock"] != float64(5) || it["stock_known"] != true || it["sold_out"] != false {
		t.Fatalf("item: %v", it)
	}
	sid, _ := it["sid"].(string)

	// The wallet is empty: refused, and nothing was bought.
	buy := func(key string, qty int, total int64) (int, map[string]any) {
		return m.call(http.MethodPost, "/api/mini/v1/digital/buy", tok, map[string]any{"sid": sid, "quantity": qty, "expected_total": total, "key": key})
	}
	if code, out = buy("first-try-1", 2, 900000); code != http.StatusConflict || out["error"] != "insufficient_funds" {
		t.Fatalf("buy with an empty wallet: %d %v", code, out)
	}
	m.fund(userID(m, tok), 2_000_000)

	// A stale price, a bad quantity, a bad key and an unknown product are refused before any money moves.
	if code, out = buy("stale-price-1", 2, 800000); code != http.StatusConflict {
		t.Fatalf("a changed price: %d %v", code, out)
	}
	if code, _ = buy("zero-qty-123", 0, 0); code != http.StatusBadRequest {
		t.Fatalf("quantity 0: %d", code)
	}
	if code, _ = buy("huge-qty-123", 26, 0); code != http.StatusBadRequest {
		t.Fatalf("quantity 26: %d", code)
	}
	if code, _ = buy("x", 1, 450000); code != http.StatusBadRequest {
		t.Fatalf("a short key: %d", code)
	}
	if code, _ = m.call(http.MethodPost, "/api/mini/v1/digital/buy", tok, map[string]any{"sid": "nopenopeno", "quantity": 1, "key": "unknown-sid-1"}); code != http.StatusNotFound {
		t.Fatalf("an unknown product: %d", code)
	}

	// The purchase: delivered at once, charged once, and the same request again is the same order.
	code, out = buy("buy-key-0001", 2, 900000)
	if code != http.StatusOK || out["status"] != "done" || out["quantity"] != float64(2) || out["delivery"] == "" || out["replayed"] != false {
		t.Fatalf("buy: %d %v", code, out)
	}
	oid, _ := out["id"].(string)
	first, _ := out["delivery"].(string)
	if code, out = buy("buy-key-0001", 2, 900000); code != http.StatusOK || out["id"] != oid || out["replayed"] != true || out["delivery"] != first {
		t.Fatalf("a retry: %d %v", code, out)
	}
	var balance int64
	if err := m.st.DB().QueryRow(context.Background(), `SELECT balance FROM core.wallets WHERE user_id = $1 AND currency = 'IRT'`, userID(m, tok)).Scan(&balance); err != nil || balance != 1_100_000 {
		t.Fatalf("the wallet after one purchase: %d %v", balance, err)
	}
	if len(sup.orders) != 1 {
		t.Fatalf("the supplier was asked to deliver %d times", len(sup.orders))
	}

	// My purchases list names and totals, not the account; one order shows the account again, to its buyer only.
	code, out = m.call(http.MethodGet, "/api/mini/v1/digital/orders", tok, nil)
	orders, _ := out["orders"].([]any)
	if code != http.StatusOK || len(orders) != 1 || orders[0].(map[string]any)["delivery"] != nil {
		t.Fatalf("orders: %d %v", code, out)
	}
	if code, out = m.call(http.MethodGet, "/api/mini/v1/digital/orders/"+oid, tok, nil); code != http.StatusOK || out["delivery"] != first || out["status"] != "done" {
		t.Fatalf("one order: %d %v", code, out)
	}
	tok2 := m.signIn("tg:6102:eli")
	if code, _ = m.call(http.MethodGet, "/api/mini/v1/digital/orders/"+oid, tok2, nil); code != http.StatusNotFound {
		t.Fatalf("another customer's order: %d, want 404", code)
	}
	if code, out = m.call(http.MethodGet, "/api/mini/v1/digital/orders", tok2, nil); code != http.StatusOK || len(out["orders"].([]any)) != 0 {
		t.Fatalf("another customer's list: %d %v", code, out)
	}
	if code, _ = m.call(http.MethodGet, "/api/mini/v1/digital/orders/not-an-id", tok, nil); code != http.StatusNotFound {
		t.Fatalf("a bad order id: %d", code)
	}
	if code, _ = m.call(http.MethodGet, "/api/mini/v1/digital", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("the shelf without a session: %d", code)
	}
}

// While the supplier has not delivered, the order is "paid" and has no account; asking for it again once the supplier has gives the account.
func TestMiniDigitalPendingOrder(t *testing.T) {
	m, sup := digitalFix(t)
	m.openDigitalShelf()
	tok := m.signIn("tg:6103:fari")
	m.fund(userID(m, tok), 1_000_000)
	sup.mu.Lock()
	sup.pending = true
	sup.mu.Unlock()
	_, out := m.call(http.MethodGet, "/api/mini/v1/digital", tok, nil)
	sid := out["items"].([]any)[0].(map[string]any)["sid"]
	code, out := m.call(http.MethodPost, "/api/mini/v1/digital/buy", tok, map[string]any{"sid": sid, "quantity": 1, "expected_total": 450000, "key": "pending-key-1"})
	if code != http.StatusOK || out["status"] != "paid" || out["delivery"] != "" {
		t.Fatalf("a pending purchase: %d %v", code, out)
	}
	if code, out = m.call(http.MethodGet, "/api/mini/v1/digital/orders/"+out["id"].(string), tok, nil); code != http.StatusOK || out["status"] != "paid" || out["delivery"] != "" {
		t.Fatalf("the pending order: %d %v", code, out)
	}
}
