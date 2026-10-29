package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A customer's codes: the dashboard's Codes tab on their page (staff who may read it) and the Mini App's My codes (the customer themselves) are
// one list, of the reward codes issued to the customer and the gift codes they applied, and of nothing that cannot be attributed to them.

// rewardCode gives u a reward code of pct percent, ending in endsIn (0: never), made by a purchase of plan (nil: by none), dated at.
func (f *fixture) rewardCode(u *store.User, code string, pct int32, endsIn time.Duration, plan *store.Plan, at time.Time) {
	f.t.Helper()
	d := &store.Discount{Code: code, Percent: &pct, UserID: &u.ID}
	if endsIn != 0 {
		end := time.Now().Add(endsIn)
		d.ExpiresAt = &end
	}
	if plan != nil {
		o, _ := f.order(u, plan, "active", "", time.Time{}, 0)
		d.SourceOrderID = &o.ID
	}
	if made, err := f.st.InsertRewardCode(context.Background(), f.st.Conn(), d); err != nil || !made {
		f.t.Fatalf("reward code %s: %v %v", code, made, err)
	}
	f.sql(`UPDATE core.discount_codes SET created_at = $2 WHERE code = $1`, code, at)
}

// giftApplied makes a gift code that uses customers may apply, has u apply it on the day at, and returns it (with the code as typed).
func (f *fixture) giftApplied(owner, u *store.User, amount int64, uses int, at time.Time) domain.NewGiftCode {
	f.t.Helper()
	made, err := f.dom.CreateGiftCodes(context.Background(), owner, domain.GiftCodeSpec{Amount: amount, Currency: "IRT", MaxRedemptions: uses})
	if err != nil {
		f.t.Fatal(err)
	}
	f.redeemGift(u, made[0], at)
	return made[0]
}

func (f *fixture) redeemGift(u *store.User, g domain.NewGiftCode, at time.Time) {
	f.t.Helper()
	if _, err := f.dom.RedeemGiftCode(context.Background(), domain.RedeemGiftCodeParams{UserID: u.ID, Code: g.Code, RequestKey: "k-" + u.ID + g.ID}); err != nil {
		f.t.Fatal(err)
	}
	f.sql(`UPDATE core.gift_code_redemptions SET created_at = $3 WHERE user_id = $1 AND gift_code_id = $2`, u.ID, g.ID, at)
}

// codeWorld is a store with two customers who hold codes and codes staff made for no one in particular.
type codeWorld struct {
	*miniFix
	ali, sara   *store.User
	plan        *store.Plan
	own, shared domain.NewGiftCode // Ali's own gift code, and the one Ali and Sara both applied
}

func newCodeWorld(t *testing.T) *codeWorld {
	t.Helper()
	m := newMiniFixture(t, true)
	owner := actor0(m.fixture)
	w := &codeWorld{miniFix: m, ali: m.customer(8601, "ali"), sara: m.customer(8602, "sara"), plan: m.plan("Monthly 50GB", 30, 50<<30, 100_000)}
	base := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	// Ali: a reward code from a purchase (oldest), a code that ended, a gift code of his own, and one he shares with Sara (newest).
	m.rewardCode(w.ali, "THX-ALI00001", 20, 5*24*time.Hour, w.plan, base)
	m.rewardCode(w.ali, "THX-ALI00002", 10, -time.Hour, nil, base.Add(time.Hour))
	w.own = m.giftApplied(owner, w.ali, 25_000, 1, base.Add(2*time.Hour))
	w.shared = m.giftApplied(owner, w.ali, 40_000, 2, base.Add(3*time.Hour))
	// Sara: a reward code, and the shared gift code.
	m.rewardCode(w.sara, "THX-SARA0001", 30, 24*time.Hour, nil, base.Add(30*time.Minute))
	m.redeemGift(w.sara, w.shared, base.Add(4*time.Hour))
	// Codes nobody owns: staff's own discount, and a gift code nobody applied.
	pct := int32(25)
	if _, err := m.dom.UpsertDiscount(context.Background(), owner, &store.Discount{Code: "SPRING25", Percent: &pct, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.dom.CreateGiftCodes(context.Background(), owner, domain.GiftCodeSpec{Amount: 99_000, Currency: "IRT", MaxRedemptions: 1}); err != nil {
		t.Fatal(err)
	}
	return w
}

// codesOf are the entries of a Mini App or dashboard answer, by their id.
func codesOf(out map[string]any, key string) []map[string]any {
	raw, _ := out[key].([]any)
	res := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		m, _ := it.(map[string]any)
		res = append(res, m)
	}
	return res
}

func idsOf(list []map[string]any) []string {
	out := make([]string, len(list))
	for i, it := range list {
		out[i], _ = it["id"].(string)
	}
	return out
}

// The customer's own list in the Mini App: theirs only, newest first, in pages; the customer is the session's, never a parameter.
func TestMiniAppCodes(t *testing.T) {
	w := newCodeWorld(t)
	ali, sara := w.signIn("tg:8601:ali"), w.signIn("tg:8602:sara")
	if f := w.features(ali); f["codes"] != true {
		t.Fatalf("features: %v", f)
	}

	code, out := w.call(http.MethodGet, "/api/mini/v1/codes", ali, nil)
	list := codesOf(out, "codes")
	if code != http.StatusOK || out["total"] != float64(4) || out["page"] != float64(1) || out["size"] != float64(10) || len(list) != 4 {
		t.Fatalf("ali's codes: %d %v", code, out)
	}
	// Newest first: the gift code he shares with Sara, his own gift code, the code that ended, the code from a purchase.
	shared, own, ended, bought := list[0], list[1], list[2], list[3]
	if shared["kind"] != "gift" || shared["tail"] != w.shared.Tail || shared["code"] != nil || shared["state"] != "used" || shared["amount"] != float64(40_000) ||
		shared["currency"] != "IRT" || shared["scale"] != float64(0) || shared["used_at"] == nil || shared["expires_at"] != nil || shared["percent"] != nil {
		t.Fatalf("the shared gift code: %v", shared)
	}
	if own["kind"] != "gift" || own["tail"] != w.own.Tail || own["amount"] != float64(25_000) {
		t.Fatalf("his own gift code: %v", own)
	}
	if ended["kind"] != "reward" || ended["code"] != "THX-ALI00002" || ended["state"] != "expired" || ended["percent"] != float64(10) || ended["expires_at"] == nil ||
		ended["plan"] != nil || ended["order_id"] != nil || ended["tail"] != nil {
		t.Fatalf("the code that ended: %v", ended)
	}
	names, _ := bought["plan"].(map[string]any)
	if bought["code"] != "THX-ALI00001" || bought["state"] != "unused" || bought["percent"] != float64(20) || names["en"] != "Monthly 50GB" || bought["order_id"] == "" || bought["used_at"] != nil {
		t.Fatalf("the code from a purchase: %v", bought)
	}

	// Pages: the total stays, the page moves, a size is bounded.
	_, p1 := w.call(http.MethodGet, "/api/mini/v1/codes?page=1&size=3", ali, nil)
	_, p2 := w.call(http.MethodGet, "/api/mini/v1/codes?page=2&size=3", ali, nil)
	if p1["total"] != float64(4) || p2["total"] != float64(4) || p2["page"] != float64(2) || p2["size"] != float64(3) ||
		!slices.Equal(append(idsOf(codesOf(p1, "codes")), idsOf(codesOf(p2, "codes"))...), idsOf(list)) || len(codesOf(p2, "codes")) != 1 {
		t.Fatalf("pages: %v | %v", p1, p2)
	}
	if _, past := w.call(http.MethodGet, "/api/mini/v1/codes?page=9", ali, nil); past["total"] != float64(4) || len(codesOf(past, "codes")) != 0 {
		t.Fatalf("a page past the end: %v", past)
	}
	for _, bad := range []string{"page=0", "page=-1", "page=x", "size=0", "size=51", "size=x", "page=100001"} {
		if code, out := w.call(http.MethodGet, "/api/mini/v1/codes?"+bad, ali, nil); code != http.StatusBadRequest || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", bad, code, out)
		}
	}

	// Sara has her own two: her reward code and the shared gift code as she applied it. None of Ali's.
	_, mine := w.call(http.MethodGet, "/api/mini/v1/codes", sara, nil)
	got := codesOf(mine, "codes")
	if mine["total"] != float64(2) || len(got) != 2 || got[0]["kind"] != "gift" || got[0]["tail"] != w.shared.Tail || got[0]["amount"] != float64(40_000) ||
		got[1]["code"] != "THX-SARA0001" || got[1]["percent"] != float64(30) {
		t.Fatalf("sara's codes: %v", mine)
	}
	// Who is asking is the session's: a customer, an id or a code in the request changes nothing.
	for _, extra := range []string{"?user=" + w.ali.ID, "?user_id=" + w.ali.ID, "?code=THX-ALI00001", "?telegram_id=8601"} {
		_, out := w.call(http.MethodGet, "/api/mini/v1/codes"+extra, sara, nil)
		if out["total"] != float64(2) || strings.Contains(fmt.Sprint(out), "THX-ALI") || strings.Contains(fmt.Sprint(out), w.own.Tail) {
			t.Errorf("sara asking with %s: %v", extra, out)
		}
	}
	// A gift code is never sent in the clear to anyone, and a customer with no codes gets an empty page.
	for _, tok := range []string{ali, sara} {
		_, out := w.call(http.MethodGet, "/api/mini/v1/codes?size=50", tok, nil)
		if strings.Contains(strings.ToUpper(fmt.Sprint(out)), strings.ToUpper(strings.ReplaceAll(w.shared.Code, "-", ""))) {
			t.Errorf("a gift code is in the answer: %v", out)
		}
	}
	if _, none := w.call(http.MethodGet, "/api/mini/v1/codes", w.signIn("tg:8603:nika"), nil); none["total"] != float64(0) || len(codesOf(none, "codes")) != 0 {
		t.Fatalf("no codes: %v", none)
	}
	if code, _ := w.call(http.MethodGet, "/api/mini/v1/codes", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", code)
	}
	if code, _ := w.call(http.MethodPost, "/api/mini/v1/codes", ali, map[string]any{}); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Fatalf("a write to the list: %d", code)
	}
}

// A banned customer gets no list, and the store in maintenance holds it (the sweep of the routes covers the rest).
func TestMiniAppCodesRefusals(t *testing.T) {
	w := newCodeWorld(t)
	ali := w.signIn("tg:8601:ali")
	w.setting("maintenance.enabled", "true")
	if code, out := w.call(http.MethodGet, "/api/mini/v1/codes", ali, nil); code != http.StatusServiceUnavailable || out["error"] != "maintenance" {
		t.Fatalf("in maintenance: %d %v", code, out)
	}
	w.setting("maintenance.enabled", "false")
	w.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, w.ali.ID)
	if code, out := w.call(http.MethodGet, "/api/mini/v1/codes", ali, nil); code != http.StatusForbidden || out["error"] != "banned" {
		t.Fatalf("banned: %d %v", code, out)
	}
}

// The customer page's Codes tab: staff who may read the page read the list, read only, for the customer in the address.
func TestDashboardCustomerCodes(t *testing.T) {
	w := newCodeWorld(t)
	owner := w.browser()
	if code := owner.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	code, out := owner.do(http.MethodGet, "/api/v1/codes?user="+w.ali.ID, nil)
	list := items(out)
	if code != 200 || out["total"] != float64(4) || out["page"] != float64(1) || out["size"] != float64(25) || len(list) != 4 {
		t.Fatalf("ali's codes: %d %v", code, out)
	}
	if list[0]["kind"] != "gift" || list[0]["tail"] != w.shared.Tail || list[0]["code"] != nil || list[3]["code"] != "THX-ALI00001" || list[3]["state"] != "unused" ||
		list[2]["state"] != "expired" || list[3]["order_id"] == nil {
		t.Fatalf("the list: %v", list)
	}
	// Sara's page is Sara's.
	_, sara := owner.do(http.MethodGet, "/api/v1/codes?user="+w.sara.ID, nil)
	if sara["total"] != float64(2) || strings.Contains(fmt.Sprint(sara), "THX-ALI") || strings.Contains(fmt.Sprint(sara), w.own.Tail) {
		t.Fatalf("sara's codes: %v", sara)
	}
	// Pages.
	_, second := owner.do(http.MethodGet, "/api/v1/codes?user="+w.ali.ID+"&page=2&size=3", nil)
	if second["total"] != float64(4) || second["page"] != float64(2) || second["size"] != float64(3) || len(items(second)) != 1 || items(second)[0]["id"] != list[3]["id"] {
		t.Fatalf("page 2: %v", second)
	}
	// A banned customer's list can still be read.
	w.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, w.sara.ID)
	if _, banned := owner.do(http.MethodGet, "/api/v1/codes?user="+w.sara.ID, nil); banned["total"] != float64(2) {
		t.Fatalf("a banned customer's codes: %v", banned)
	}

	// The customer is required, and must be one.
	for path, want := range map[string]int{
		"/api/v1/codes":          http.StatusBadRequest,
		"/api/v1/codes?user=":    http.StatusBadRequest,
		"/api/v1/codes?user=ali": http.StatusBadRequest,
		"/api/v1/codes?user=" + url.QueryEscape("1' OR '1'='1"):   http.StatusBadRequest,
		"/api/v1/codes?user=00000000-0000-7000-8000-00000000dead": http.StatusNotFound,
	} {
		if code, out := owner.do(http.MethodGet, path, nil); code != want {
			t.Errorf("%s: %d %v", path, code, out)
		}
	}
	// It is read only.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if code, _ := owner.do(method, "/api/v1/codes?user="+w.ali.ID, map[string]any{}); code != http.StatusMethodNotAllowed && code != http.StatusNotFound {
			t.Errorf("%s: %d", method, code)
		}
	}
}

// Who may read it is who may read a customer's page: the owner, an admin and support; a customer, or someone not signed in, may not.
func TestDashboardCustomerCodesPermission(t *testing.T) {
	w := newCodeWorld(t)
	path := "/api/v1/codes?user=" + w.ali.ID
	for role, tg := range map[string]int64{"owner": ownerTG, "admin": 8711, "support": 8712} {
		w.staffUser(tg, role)
		b := w.browser()
		if code := b.loginWithLink(tg); code != 200 {
			t.Fatalf("login %s: %d", role, code)
		}
		if code, out := b.do(http.MethodGet, path, nil); code != 200 || out["total"] != float64(4) {
			t.Errorf("%s: %d %v", role, code, out)
		}
	}
	if code, _ := w.browser().do(http.MethodGet, path, nil); code != http.StatusUnauthorized {
		t.Errorf("not signed in: %d", code)
	}
	// The permission is the page's own (users.read), and the route needs nothing more.
	if !Allowed("support", PermUsersRead) || Allowed("support", PermDiscountsWrite) {
		t.Fatal("the roles changed: this test assumes support reads customers but cannot manage discounts")
	}
}
