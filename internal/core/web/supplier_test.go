package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

type stubSupplier struct {
	status int
	body   string
	calls  []string
}

func (s *stubSupplier) PostSupplier(_ context.Context, action, _ string, _ []byte) (int, []byte, error) {
	s.calls = append(s.calls, action)
	if s.status != 0 {
		return s.status, []byte(s.body), nil
	}
	switch action {
	case "balance":
		return 200, []byte(`{"balance":"0.40","currency":"USDT"}`), nil
	case "products":
		return 200, []byte(`{"products":[{"id":"gem:1","name":"Gemini Pro","description":"supplier text","price":2.6,"stock":4},{"id":"b.2","name":"B","price":1}]}`), nil
	}
	return 404, []byte(`{}`), nil
}

// The supplier endpoints are the owner's: an admin gets 403 on all of them, the API key is written once and never read
// back, the catalog comes in off, and the owner prices and switches a product on.
func TestSupplierEndpointsAreTheOwners(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env, PrivateFeaturesOverride: &granted})
	f.api.cfg.Domain = f.dom
	stub := &stubSupplier{}
	f.dom.SetSupplierTransport(stub)

	f.staffUser(8931, "admin")
	adm := f.browser()
	adm.loginWithLink(8931)
	const apiKey = "supkey-xxxxxxxxxxxxxxxx"
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/supplier"}, {http.MethodPut, "/api/v1/supplier/config"}, {http.MethodGet, "/api/v1/supplier/balance"},
		{http.MethodPost, "/api/v1/supplier/sync"}, {http.MethodGet, "/api/v1/supplier/products"}, {http.MethodPut, "/api/v1/supplier/products/gem:1"},
		{http.MethodGet, "/api/v1/supplier/orders"}, {http.MethodPost, "/api/v1/supplier/orders/00000000-0000-0000-0000-000000000000/retry"},
		{http.MethodPost, "/api/v1/supplier/orders/00000000-0000-0000-0000-000000000000/refund"},
	} {
		if code, _ := adm.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("admin %s %s: %d, want 403", c.method, c.path, code)
		}
	}

	code, out := b.do(http.MethodGet, "/api/v1/supplier", nil)
	if code != 200 || out["available"] != true || out["configured"] != false || out["on"] != false {
		t.Fatalf("fresh: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/config", map[string]any{"api_key": "no way"}); code != 400 || out["field"] != "api_key" {
		t.Fatalf("bad key: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodPost, "/api/v1/supplier/sync", nil); code != http.StatusConflict {
		t.Fatalf("sync without a key: %d, want 409", code)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/config", map[string]any{"api_key": apiKey}); code != 200 || out["configured"] != true {
		t.Fatalf("set: %d %v", code, out)
	}
	_, _, raw := b.get("/api/v1/supplier")
	if strings.Contains(string(raw), apiKey) || strings.Contains(string(raw), "api_key") {
		t.Fatalf("the status carries the key: %s", raw)
	}

	// The catalog comes in OFF and unpriced; the owner prices and switches one on.
	if code, out = b.do(http.MethodPost, "/api/v1/supplier/sync", nil); code != 200 || out["count"] != float64(2) {
		t.Fatalf("sync: %d %v", code, out)
	}
	code, out = b.do(http.MethodGet, "/api/v1/supplier/products", nil)
	items, _ := out["items"].([]any)
	if code != 200 || len(items) != 2 {
		t.Fatalf("products: %d %v", code, out)
	}
	for _, it := range items {
		m := it.(map[string]any)
		if m["enabled"] != false || m["sell_price"] != nil {
			t.Fatalf("a product came in on or priced: %v", m)
		}
	}
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/products/gem:1", map[string]any{"enabled": true}); code != 400 || out["field"] != "sell_price" {
		t.Fatalf("on without a price: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/supplier/products/gem:1", map[string]any{"enabled": true, "sell_price": 450000, "title": map[string]any{"fa": "جمینای پرو"}, "desc": map[string]any{"en": "Gemini"}})
	if code != 200 || out["enabled"] != true || out["sell_price"] != float64(450000) || out["title"].(map[string]any)["fa"] != "جمینای پرو" {
		t.Fatalf("edit: %d %v", code, out)
	}
	for _, p := range []string{"/api/v1/supplier/products/bad%20id", "/api/v1/supplier/products/x%2F..%2Fy"} {
		if code, _ = b.do(http.MethodPut, p, map[string]any{"sort": 1}); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}
	if code, _ = b.do(http.MethodPut, "/api/v1/supplier/products/nope", map[string]any{"sort": 1}); code != http.StatusNotFound {
		t.Errorf("unknown product: %d", code)
	}
	if code, out = b.do(http.MethodGet, "/api/v1/supplier/balance", nil); code != 200 || out["amount"] != "0.40" || out["currency"] != "USDT" {
		t.Fatalf("balance: %d %v", code, out)
	}
	if code, out = b.do(http.MethodGet, "/api/v1/supplier/orders", nil); code != 200 || out["total"] != float64(0) {
		t.Fatalf("orders: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodGet, "/api/v1/supplier/orders?status=gone", nil); code != 400 {
		t.Fatalf("bad status filter: %d", code)
	}

	// The supplier's answers keep their meaning; one that is down is a 502.
	for name, c := range map[string]struct {
		status int
		body   string
		want   int
	}{"a wrong key": {401, `{"error":"bad key"}`, 409}, "a refusal": {402, `{"error":"x","message":"no money"}`, 409}, "a server error": {503, `{}`, 502}} {
		stub.status, stub.body = c.status, c.body
		if code, out = b.do(http.MethodGet, "/api/v1/supplier/balance?fresh=1", nil); code != c.want {
			t.Errorf("%s: %d %v, want %d", name, code, out, c.want)
		}
	}
}

// The pricing endpoints are the owner's too. Turning the pricing on needs a known USDT rate and then prices every product that follows it
// (the catalog's cost x the store's rate x the markup, rounded up); a price typed makes a product fixed, and "Apply to all" takes it back.
func TestSupplierPricingEndpoints(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env, PrivateFeaturesOverride: &granted})
	f.api.cfg.Domain = f.dom
	f.dom.SetSupplierTransport(&stubSupplier{})
	// The tests of this package share one database: start from no products, no pricing and no rate.
	for _, q := range []string{`DELETE FROM core.supplier_orders`, `DELETE FROM core.supplier_products`,
		`DELETE FROM core.settings WHERE key IN ('state.supplier.pricing', 'state.usdt_rate.live', 'payments.usdt_rate_margin', 'payments.usdt_rate')`} {
		if _, err := f.st.DB().Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}

	f.staffUser(8932, "admin")
	adm := f.browser()
	adm.loginWithLink(8932)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/supplier/pricing"}, {http.MethodPut, "/api/v1/supplier/pricing"}, {http.MethodPost, "/api/v1/supplier/pricing/apply"},
	} {
		if code, _ := adm.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("admin %s %s: %d, want 403", c.method, c.path, code)
		}
	}

	if code, out := b.do(http.MethodPut, "/api/v1/supplier/config", map[string]any{"api_key": "supkey-xxxxxxxxxxxxxxxx"}); code != 200 {
		t.Fatalf("key: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/supplier/sync", nil); code != 200 {
		t.Fatalf("sync: %d", code)
	}
	code, out := b.do(http.MethodGet, "/api/v1/supplier/pricing", nil)
	counts, _ := out["counts"].(map[string]any)
	if code != 200 || out["auto"] != false || out["markup"] != float64(0) || out["max_markup"] != float64(500) || out["step"] != float64(1000) || counts["auto"] != float64(2) {
		t.Fatalf("pricing: %d %v", code, out)
	}
	if usdt, _ := out["usdt"].(map[string]any); usdt["rate"] != float64(0) {
		t.Fatalf("a rate without any being read: %v", out["usdt"])
	}
	// No rate: it cannot be turned on; a markup outside 0..500 is refused; the markup alone can be saved.
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/pricing", map[string]any{"auto": true, "markup": 20}); code != http.StatusConflict {
		t.Fatalf("on without a rate: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/pricing", map[string]any{"auto": false, "markup": 501}); code != 400 || out["field"] != "markup" {
		t.Fatalf("markup 501: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPost, "/api/v1/supplier/pricing/apply", map[string]any{"all": true}); code != http.StatusConflict {
		t.Fatalf("apply while off: %d %v", code, out)
	}

	raw, _ := json.Marshal(map[string]any{"rate": 100_000, "at": time.Now().Unix(), "sources": []string{"test"}})
	if err := f.st.SetSetting(context.Background(), f.st.Conn(), "state.usdt_rate.live", raw); err != nil {
		t.Fatal(err)
	}
	code, out = b.do(http.MethodPut, "/api/v1/supplier/pricing", map[string]any{"auto": true, "markup": 20})
	if code != 200 || out["auto"] != true || out["markup"] != float64(20) || out["changed"] != float64(2) || out["applied_rate"] != float64(100_000) {
		t.Fatalf("turn on: %d %v", code, out)
	}
	if usdt, _ := out["usdt"].(map[string]any); usdt["rate"] != float64(100_000) || usdt["source"] != "live" {
		t.Fatalf("the rate: %v", out["usdt"])
	}
	prices := func() map[string]map[string]any {
		_, o := b.do(http.MethodGet, "/api/v1/supplier/products", nil)
		m := map[string]map[string]any{}
		for _, it := range o["items"].([]any) {
			p := it.(map[string]any)
			m[p["id"].(string)] = p
		}
		return m
	}
	ps := prices()
	if g := ps["gem:1"]; g["sell_price"] != float64(312_000) || g["price_mode"] != "auto" || g["can_follow"] != true || g["enabled"] != false {
		t.Fatalf("gem: %v", g) // 2.6 x 100,000 x 1.2
	}
	if x := ps["b.2"]; x["sell_price"] != float64(120_000) || x["price_mode"] != "auto" {
		t.Fatalf("b.2: %v", x)
	}

	// A typed price is the owner's; asking to follow prices it again; "Apply to all" takes every typed price back.
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/products/gem:1", map[string]any{"sell_price": 500_000}); code != 200 || out["price_mode"] != "fixed" || out["sell_price"] != float64(500_000) {
		t.Fatalf("typed: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/products/gem:1", map[string]any{"price_mode": "sometimes"}); code != 400 || out["field"] != "price_mode" {
		t.Fatalf("bad mode: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/products/gem:1", map[string]any{"price_mode": "auto"}); code != 200 || out["price_mode"] != "auto" || out["sell_price"] != float64(312_000) {
		t.Fatalf("follow: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/supplier/products/gem:1", map[string]any{"sell_price": 500_000}); code != 200 || out["price_mode"] != "fixed" {
		t.Fatalf("typed again: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/supplier/pricing/apply", map[string]any{"all": true})
	counts, _ = out["counts"].(map[string]any)
	if code != 200 || out["switched"] != float64(1) || out["replaced"] != float64(1) || out["changed"] != float64(1) || counts["fixed"] != float64(0) || counts["auto"] != float64(2) {
		t.Fatalf("apply all: %d %v", code, out)
	}
	if g := prices()["gem:1"]; g["sell_price"] != float64(312_000) || g["price_mode"] != "auto" {
		t.Fatalf("gem after apply: %v", g)
	}

	// The settings of the pricing are not in the dashboard's settings list (they are the pricing's own state).
	if code, out = b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]any{"state.supplier.pricing": `{"auto":false}`}}); code == 200 {
		t.Fatalf("the pricing state was set as a setting: %v", out)
	}
}
