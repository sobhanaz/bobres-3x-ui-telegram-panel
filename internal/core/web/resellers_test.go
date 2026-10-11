package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func item(out map[string]any) map[string]any {
	m, _ := out["item"].(map[string]any)
	return m
}

// Every reseller endpoint is owner and admin only.
func TestResellerPermissions(t *testing.T) {
	f, _, _, _ := salesFixture(t)
	f.staffUser(8501, "support")
	f.staffUser(8502, "admin")
	sup, adm := f.browser(), f.browser()
	sup.loginWithLink(8501)
	adm.loginWithLink(8502)
	id := "00000000-0000-0000-0000-000000000099"
	calls := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/resellers"},
		{http.MethodPost, "/api/v1/resellers"},
		{http.MethodGet, "/api/v1/resellers/" + id},
		{http.MethodPut, "/api/v1/resellers/" + id},
		{http.MethodPost, "/api/v1/resellers/" + id + "/payments"},
		{http.MethodPost, "/api/v1/resellers/" + id + "/remove"},
		{http.MethodGet, "/api/v1/reseller-tiers"},
		{http.MethodPost, "/api/v1/reseller-tiers"},
		{http.MethodPut, "/api/v1/reseller-tiers/" + id},
		{http.MethodDelete, "/api/v1/reseller-tiers/" + id},
	}
	for _, c := range calls {
		if code, _ := sup.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("support %s %s: %d", c.method, c.path, code)
		}
	}
	for _, c := range []struct{ method, path string }{{http.MethodGet, "/api/v1/resellers"}, {http.MethodGet, "/api/v1/reseller-tiers"}} {
		if code, _ := adm.do(c.method, c.path, nil); code != http.StatusOK {
			t.Errorf("admin %s %s: %d", c.method, c.path, code)
		}
	}
	if code, _ := adm.do(http.MethodGet, "/api/v1/resellers/"+id, nil); code != http.StatusNotFound {
		t.Errorf("admin, unknown reseller: %d", code)
	}
	_, me := adm.do(http.MethodGet, "/api/v1/me", nil)
	if !strings.Contains(asJSON(me["permissions"]), PermResellersWrite) {
		t.Errorf("admin permissions: %v", me["permissions"])
	}
	_, me = sup.do(http.MethodGet, "/api/v1/me", nil)
	if strings.Contains(asJSON(me["permissions"]), PermResellersWrite) {
		t.Errorf("support permissions: %v", me["permissions"])
	}
}

// Resellers never use the dashboard: no login link, and a staff member made
// one loses their session.
func TestResellerHasNoDashboard(t *testing.T) {
	f := newFixture(t)
	f.staffUser(ownerTG, "owner")
	sup := f.staffUser(8511, "support")
	b := f.browser()
	b.loginWithLink(8511)
	if err := f.st.SetUserRole(context.Background(), f.st.Conn(), sup.ID, "reseller", "active"); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("a reseller kept a dashboard session: %d", code)
	}
	if _, _, err := f.dom.CreateLoginLink(context.Background(), 8511); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reseller got a login link: %v", err)
	}
}

func TestResellerTiersAPI(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	monthly := f.plan("Monthly", 30, 50<<30, 120_000)
	usdt := &store.Plan{NameI18n: map[string]string{"en": "Crypto"}, Kind: "time", DurationDays: ptr32w(30), Price: 5_000_000,
		Currency: "USDT", Enabled: true}
	if err := f.st.UpsertPlan(context.Background(), f.st.Conn(), usdt); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{"name": "Gold", "discount_percent": 20, "commission_percent": 10,
		"prices": []map[string]string{{"plan_id": monthly.ID, "price": "90,000"}, {"plan_id": usdt.ID, "price": "4.5"}}}
	code, out := b.do(http.MethodPost, "/api/v1/reseller-tiers", body)
	gold := item(out)
	if code != http.StatusCreated || gold["name"] != "Gold" || gold["version"] != float64(1) || gold["resellers"] != float64(0) {
		t.Fatalf("create: %d %v", code, out)
	}
	prices := asJSON(gold["prices"])
	if !strings.Contains(prices, `{"currency":"IRT","plan_id":"`+monthly.ID+`","price":"90000"}`) ||
		!strings.Contains(prices, `{"currency":"USDT","plan_id":"`+usdt.ID+`","price":"4.500000"}`) {
		t.Fatalf("prices: %s", prices)
	}
	gid := gold["id"].(string)

	// Field errors.
	for field, bad := range map[string]map[string]any{
		"name":                   {"name": "", "discount_percent": 0, "commission_percent": 0},
		"discount_percent":       {"name": "X", "discount_percent": 95, "commission_percent": 0},
		"commission_percent":     {"name": "X", "discount_percent": 0, "commission_percent": 60},
		"prices." + monthly.ID:   {"name": "X", "prices": []map[string]string{{"plan_id": monthly.ID, "price": "-1"}}},
		"prices.not-a-plan":      {"name": "X", "prices": []map[string]string{{"plan_id": "not-a-plan", "price": "1"}}},
		"prices." + usdt.ID + "": {"name": "X", "prices": []map[string]string{{"plan_id": usdt.ID, "price": "1.0000001"}}},
	} {
		if code, out := b.do(http.MethodPost, "/api/v1/reseller-tiers", bad); code != http.StatusBadRequest || out["field"] != field {
			t.Errorf("%s: %d %v", field, code, out)
		}
	}
	if code, out := b.do(http.MethodPost, "/api/v1/reseller-tiers", map[string]any{"name": "Gold"}); code != http.StatusBadRequest || out["field"] != "name" {
		t.Fatalf("duplicate name: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/reseller-tiers", map[string]any{"name": "Gold", "bogus": 1}); code != http.StatusBadRequest || out["error"] != "invalid" {
		t.Fatalf("unknown field: %d %v", code, out)
	}

	// Update: the version is required and checked; the price list is replaced.
	upd := map[string]any{"name": "Gold+", "discount_percent": 25, "commission_percent": 12,
		"prices": []map[string]string{{"plan_id": monthly.ID, "price": ""}}, "reason": "summer"}
	if code, out := b.do(http.MethodPut, "/api/v1/reseller-tiers/"+gid, upd); code != http.StatusBadRequest || out["field"] != "version" {
		t.Fatalf("no version: %d %v", code, out)
	}
	upd["version"] = 1
	code, out = b.do(http.MethodPut, "/api/v1/reseller-tiers/"+gid, upd)
	if it := item(out); code != http.StatusOK || it["name"] != "Gold+" || it["version"] != float64(2) || asJSON(it["prices"]) != "[]" {
		t.Fatalf("update: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/reseller-tiers/"+gid, upd); code != http.StatusConflict || out["error"] != "state" {
		t.Fatalf("stale update: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/reseller-tiers/00000000-0000-0000-0000-000000000099", upd); code != http.StatusNotFound {
		t.Fatalf("missing tier: %d", code)
	}
	code, out = b.do(http.MethodGet, "/api/v1/reseller-tiers", nil)
	if list := items(out); code != http.StatusOK || len(list) != 1 || list[0]["name"] != "Gold+" {
		t.Fatalf("list: %d %v", code, out)
	}

	// Deleting a tier in use is refused; an unused one goes.
	c := f.customer(8521, "shop")
	if _, err := f.dom.MakeReseller(context.Background(), actor0(f), c.ID, domain.ResellerInput{TierID: gid}, "x"); err != nil {
		t.Fatal(err)
	}
	if code, out := b.do(http.MethodDelete, "/api/v1/reseller-tiers/"+gid, nil); code != http.StatusConflict || out["error"] != "state" {
		t.Fatalf("delete in use: %d %v", code, out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/reseller-tiers", nil)
	if items(out)[0]["resellers"] != float64(1) {
		t.Fatalf("reseller count: %v", out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/reseller-tiers", map[string]any{"name": "Spare"})
	if code != http.StatusCreated {
		t.Fatalf("spare: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodDelete, "/api/v1/reseller-tiers/"+item(out)["id"].(string), nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	var n int
	if err := f.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.audit_log WHERE entity = 'reseller_tier'`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("tier audit entries: %v %d", err, n)
	}
}

func ptr32w(v int32) *int32 { return &v }

// actor0 is the fixture's owner, for setting things up through the domain.
func actor0(f *fixture) *store.User {
	u, err := f.st.GetUserByTelegramID(context.Background(), f.st.Conn(), ownerTG)
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

func TestResellersAPI(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	ctx := context.Background()
	plan := f.plan("Monthly", 30, 50<<30, 100_000)
	code, out := b.do(http.MethodPost, "/api/v1/reseller-tiers", map[string]any{"name": "Gold", "discount_percent": 20, "commission_percent": 10})
	if code != http.StatusCreated {
		t.Fatalf("tier: %d %v", code, out)
	}
	gid := item(out)["id"].(string)
	shop := f.customer(8531, "tabriz_shop")
	other := f.customer(8532, "sara")

	// Making a reseller: field errors and refusals first.
	mk := func(body map[string]any) (int, map[string]any) {
		return b.do(http.MethodPost, "/api/v1/resellers", body)
	}
	good := map[string]any{"user_id": shop.ID, "tier_id": gid, "commission_percent": nil, "note": "bazaar",
		"credit_limits": []map[string]string{{"currency": "IRT", "amount": "500,000"}}, "reason": "trusted shop"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range good {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	for field, body := range map[string]map[string]any{
		"user_id":            with("user_id", "x"),
		"tier_id":            with("tier_id", "not-a-tier"),
		"credit_limits":      with("credit_limits", []map[string]string{{"currency": "EUR", "amount": "1"}}),
		"credit_limit.IRT":   with("credit_limits", []map[string]string{{"currency": "IRT", "amount": "1.5"}}),
		"commission_percent": with("commission_percent", 70),
		"note":               with("note", strings.Repeat("n", 501)),
	} {
		if code, out := mk(body); code != http.StatusBadRequest || out["field"] != field {
			t.Errorf("%s: %d %v", field, code, out)
		}
	}
	if code, out := mk(with("credit_limits", []map[string]string{{"currency": "IRT", "amount": "1"}, {"currency": "irt", "amount": "2"}})); code != http.StatusBadRequest || out["field"] != "credit_limit.IRT" {
		t.Errorf("a currency twice: %d %v", code, out)
	}
	if code, out := mk(with("tier_id", "00000000-0000-0000-0000-000000000099")); code != http.StatusBadRequest || out["field"] != "tier_id" {
		t.Errorf("unknown tier: %d %v", code, out)
	}
	if code, _ := mk(with("reason", "")); code != http.StatusBadRequest {
		t.Errorf("no reason: %d", code)
	}
	adm := f.staffUser(8533, "admin")
	if code, out := mk(with("user_id", adm.ID)); code != http.StatusConflict || out["error"] != "state" {
		t.Errorf("staff made a reseller: %d %v", code, out)
	}
	if code, _ := mk(with("user_id", "00000000-0000-0000-0000-000000000099")); code != http.StatusNotFound {
		t.Errorf("unknown user: %d", code)
	}

	code, out = mk(good)
	r := item(out)
	if code != http.StatusCreated || asJSON(r["user"]) != `{"id":"`+shop.ID+`","status":"active","telegram_id":8531,"username":"tabriz_shop"}` ||
		asJSON(r["tier"]) != `{"commission_percent":10,"discount_percent":20,"id":"`+gid+`","name":"Gold"}` ||
		r["commission_percent"] != float64(10) || r["commission_override"] != nil || r["note"] != "bazaar" || r["version"] != float64(1) ||
		r["can_remove"] != true || asJSON(r["balances"]) != `[{"balance":0,"credit_limit":500000,"currency":"IRT"}]` {
		t.Fatalf("make: %d %v", code, out)
	}
	if code, _ := mk(good); code != http.StatusConflict {
		t.Fatalf("made twice: %d", code)
	}

	// Two customers joined through the link; one buys, the commission shows.
	c1, err := f.st.UpsertUser(ctx, f.st.Conn(), 8534, "cust1", "fa", shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.UpsertUser(ctx, f.st.Conn(), 8535, "cust2", "fa", shop.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dom.AdjustBalance(ctx, actor0(f), c1.ID, 100_000, "IRT", "gift", "fund-c1-0000000001"); err != nil {
		t.Fatal(err)
	}
	o, err := f.dom.CreateOrder(ctx, domain.CreateOrderParams{UserID: c1.ID, PlanID: plan.ID, IdempotencyKey: "c1-buy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.dom.PayOrderWithWallet(ctx, o.ID, c1.ID); err != nil {
		t.Fatal(err)
	}
	// The reseller buys on credit: 80,000 at the tier price.
	ro, err := f.dom.CreateOrder(ctx, domain.CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "shop-buy", Label: "Ali"})
	if err != nil || ro.Amount != 80_000 {
		t.Fatalf("reseller order: %v %+v", err, ro)
	}
	if _, err := f.dom.PayOrderWithWallet(ctx, ro.ID, shop.ID); err != nil {
		t.Fatal(err)
	}

	code, out = b.do(http.MethodGet, "/api/v1/resellers/"+shop.ID, nil)
	r = item(out)
	if code != http.StatusOK || r["customers"] != float64(2) || asJSON(r["commission_earned"]) != `[{"amount":10000,"currency":"IRT"}]` ||
		asJSON(r["balances"]) != `[{"balance":-70000,"credit_limit":500000,"currency":"IRT"}]` || r["can_remove"] != false {
		t.Fatalf("detail in debt: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/resellers/"+other.ID, nil); code != http.StatusNotFound {
		t.Fatalf("a customer's reseller page: %d", code)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/resellers/bad", nil); code != http.StatusNotFound {
		t.Fatalf("a malformed id: %d", code)
	}

	// The list and its filters.
	second := f.customer(8536, "second")
	if _, err := f.dom.MakeReseller(ctx, actor0(f), second.ID, domain.ResellerInput{TierID: gid, CommissionPercent: ptr32w(30)}, "x"); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string][]string{
		"":                 {second.ID, shop.ID},
		"?q=tabriz":        {shop.ID},
		"?q=8536":          {second.ID},
		"?in_debt=true":    {shop.ID},
		"?in_debt=1&q=sec": {},
		"?tier=" + gid:     {second.ID, shop.ID},
		"?tier=bad":        {},
		"?size=1&page=2":   {shop.ID},
	} {
		code, out := b.do(http.MethodGet, "/api/v1/resellers"+q, nil)
		list := items(out)
		if code != http.StatusOK || len(list) != len(want) {
			t.Errorf("list %q: %d %v", q, code, out)
			continue
		}
		for i, id := range want {
			if list[i]["user"].(map[string]any)["id"] != id {
				t.Errorf("list %q: item %d %v", q, i, list[i]["user"])
			}
		}
	}
	_, out = b.do(http.MethodGet, "/api/v1/resellers?q=second", nil)
	if l := items(out); len(l) != 1 || l[0]["commission_percent"] != float64(30) || l[0]["commission_override"] != float64(30) || out["total"] != float64(1) {
		t.Fatalf("own rate in the list: %v", out)
	}

	// The customers tab: users invited by the reseller.
	code, out = b.do(http.MethodGet, "/api/v1/users?referred_by="+shop.ID, nil)
	if code != http.StatusOK || out["total"] != float64(2) {
		t.Fatalf("customers: %d %v", code, out)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/users?referred_by=bad", nil); code != http.StatusOK || out["total"] != float64(0) || asJSON(out["items"]) != "[]" {
		t.Fatalf("customers of a bad id: %d %v", code, out)
	}
	// The services and orders tabs show the name the reseller gave.
	worker := f.dom.NewProvisionWorker(&labelProv{}, nil)
	for i := 0; i < 2; i++ {
		if worked, err := worker.ProvisionNext(ctx); err != nil || !worked {
			t.Fatalf("provision: %v %v", worked, err)
		}
	}
	_, out = b.do(http.MethodGet, "/api/v1/services?user="+shop.ID, nil)
	if l := items(out); len(l) != 1 || l[0]["label"] != "Ali" {
		t.Fatalf("services tab: %v", out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/services?user="+c1.ID, nil)
	if l := items(out); len(l) != 1 || l[0]["label"] != nil {
		t.Fatalf("a customer's service: %v", out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/orders?user="+shop.ID, nil)
	if l := items(out); len(l) != 1 || l[0]["label"] != "Ali" {
		t.Fatalf("orders tab: %v", out)
	}

	// Terms: version required and checked; limits below the debt refused.
	put := func(body map[string]any) (int, map[string]any) {
		return b.do(http.MethodPut, "/api/v1/resellers/"+shop.ID, body)
	}
	terms := map[string]any{"tier_id": gid, "commission_percent": 15, "note": "pays weekly",
		"credit_limits": []map[string]string{{"currency": "IRT", "amount": "60000"}}, "reason": "lowered"}
	if code, out := put(terms); code != http.StatusBadRequest || out["field"] != "version" {
		t.Fatalf("no version: %d %v", code, out)
	}
	terms["version"] = 1
	if code, out := put(terms); code != http.StatusBadRequest || out["field"] != "credit_limit.IRT" || !strings.Contains(out["message"].(string), "70000 IRT") {
		t.Fatalf("limit below the debt: %d %v", code, out)
	}
	terms["credit_limits"] = []map[string]string{{"currency": "IRT", "amount": "70000"}, {"currency": "USDT", "amount": "25"}}
	code, out = put(terms)
	r = item(out)
	if code != http.StatusOK || r["version"] != float64(2) || r["commission_percent"] != float64(15) || r["note"] != "pays weekly" ||
		asJSON(r["balances"]) != `[{"balance":-70000,"credit_limit":70000,"currency":"IRT"},{"balance":0,"credit_limit":25000000,"currency":"USDT"}]` {
		t.Fatalf("update: %d %v", code, out)
	}
	if code, out := put(terms); code != http.StatusConflict || out["error"] != "state" {
		t.Fatalf("stale: %d %v", code, out)
	}

	// Remove is refused in debt; a payment (replayed) settles it.
	if code, out := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/remove", map[string]string{"reason": "closing"}); code != http.StatusConflict || out["error"] != "state" {
		t.Fatalf("remove in debt: %d %v", code, out)
	}
	k := key()
	pay := map[string]any{"amount": "70,000", "currency": "IRT", "reason": "cash", "key": k}
	code, first := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/payments", pay)
	entry, _ := first["entry"].(map[string]any)
	if code != http.StatusOK || entry["kind"] != "settlement" || asJSON(entry["amount"]) != `{"amount":70000,"currency":"IRT"}` ||
		asJSON(entry["balance_after"]) != `{"amount":0,"currency":"IRT"}` || entry["ref_type"] != "admin" || item(first)["can_remove"] != true {
		t.Fatalf("payment: %d %v", code, first)
	}
	code, again := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/payments", pay)
	if code != http.StatusOK || again["entry"].(map[string]any)["id"] != entry["id"] {
		t.Fatalf("replay: %d %v", code, again)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/payments", map[string]any{"amount": "5", "currency": "IRT", "reason": "x", "key": k}); code != http.StatusConflict || out["error"] != "conflict" {
		t.Fatalf("a key reused for another amount: %d %v", code, out)
	}
	for name, bad := range map[string]map[string]any{
		"zero":     {"amount": "0", "currency": "IRT", "reason": "x", "key": key()},
		"negative": {"amount": "-5", "currency": "IRT", "reason": "x", "key": key()},
		"currency": {"amount": "5", "currency": "EUR", "reason": "x", "key": key()},
	} {
		if code, out := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/payments", bad); code != http.StatusBadRequest || out["field"] != "amount" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/payments", map[string]any{"amount": "5", "currency": "IRT", "reason": "x", "key": "short"}); code != http.StatusBadRequest {
		t.Errorf("bad key: %d", code)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/payments", map[string]any{"amount": "5", "currency": "IRT", "reason": "", "key": key()}); code != http.StatusBadRequest {
		t.Errorf("no reason: %d", code)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/resellers/"+other.ID+"/payments", map[string]any{"amount": "5", "currency": "IRT", "reason": "x", "key": key()}); code != http.StatusNotFound {
		t.Errorf("a payment from a customer: %d", code)
	}
	_, out = b.do(http.MethodGet, "/api/v1/ledger?kind=settlement&user="+shop.ID, nil)
	if out["total"] != float64(1) {
		t.Fatalf("statement: %v", out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/ledger?kind=commission", nil)
	if l := items(out); len(l) != 1 || l[0]["ref_type"] != "order" || l[0]["ref_id"] != o.ID {
		t.Fatalf("commissions in the ledger: %v", out)
	}

	if code, _ := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/remove", map[string]string{"reason": ""}); code != http.StatusBadRequest {
		t.Fatalf("remove without a reason: %d", code)
	}
	code, out = b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/remove", map[string]string{"reason": "closing the shop"})
	if code != http.StatusOK || out["role"] != "user" || out["user_id"] != shop.ID {
		t.Fatalf("remove: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/resellers/"+shop.ID, nil); code != http.StatusNotFound {
		t.Fatalf("after removal: %d", code)
	}
	_, out = b.do(http.MethodGet, "/api/v1/users/"+shop.ID, nil)
	if out["user"].(map[string]any)["role"] != "user" {
		t.Fatalf("role after removal: %v", out)
	}
	// The audit log tells the story under the reseller's id.
	_, out = b.do(http.MethodGet, "/api/v1/audit?entity=reseller&entity_id="+shop.ID, nil)
	actions := []string{}
	for _, it := range items(out) {
		actions = append(actions, it["action"].(string))
	}
	if strings.Join(actions, ",") != "reseller.remove,reseller.payment,reseller.terms,reseller.add" {
		t.Fatalf("audit: %v", actions)
	}
}

// The overview shows what resellers owe to those who manage them.
func TestOverviewResellerDebt(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	ctx := context.Background()
	tier, err := f.dom.CreateTier(ctx, actor0(f), domain.TierInput{Name: "Gold"}, "")
	if err != nil {
		t.Fatal(err)
	}
	shop := f.customer(8541, "shop")
	if _, err := f.dom.MakeReseller(ctx, actor0(f), shop.ID, domain.ResellerInput{TierID: tier.ID,
		CreditLimits: map[string]int64{"IRT": 50_000}}, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dom.AdjustBalance(ctx, actor0(f), shop.ID, -12_500, "IRT", "goods on credit", "debt-0000000000001"); err != nil {
		t.Fatal(err)
	}
	code, out := b.do(http.MethodGet, "/api/v1/overview", nil)
	if code != http.StatusOK || asJSON(out["resellers"]) != `{"count":1,"debt":[{"amount":12500,"currency":"IRT"}]}` {
		t.Fatalf("owner's overview: %d %v", code, out["resellers"])
	}
	f.staffUser(8542, "support")
	sup := f.browser()
	sup.loginWithLink(8542)
	if code, out := sup.do(http.MethodGet, "/api/v1/overview", nil); code != http.StatusOK || out["resellers"] != nil {
		t.Fatalf("support's overview: %d %v", code, out["resellers"])
	}
}

// labelProv delivers any order (the worker's provisioner for these tests).
type labelProv struct{ fakeProv }

func (p *labelProv) CreateClient(_ context.Context, id, _ string, _, _ int64, _ []string) (domain.ProvisionedClient, error) {
	return domain.ProvisionedClient{ServerID: "00000000-0000-7000-8000-00000000c0de", XUISubID: "sub-" + id[:8]}, nil
}

func (p *labelProv) GetLinks(context.Context, string) (domain.Links, error) {
	return domain.Links{SubscriptionLink: "https://sub.example/x"}, nil
}
