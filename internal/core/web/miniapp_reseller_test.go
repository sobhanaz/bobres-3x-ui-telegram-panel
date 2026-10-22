package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// miniReseller is a Mini App fixture with a tier and a plan, and the trial
// running (resellers are in it).
type miniReseller struct {
	*miniFix
	tier *store.ResellerTier
	plan *store.Plan
	n    int
}

func newMiniReseller(t *testing.T) *miniReseller {
	t.Helper()
	m := newMiniFixture(t, true)
	m.sql(`UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`)
	t.Cleanup(func() { m.sql(`UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`) })
	tier, err := m.dom.CreateTier(context.Background(), actor0(m.fixture), domain.TierInput{Name: fmt.Sprintf("Gold %d", time.Now().UnixNano()),
		DiscountPercent: 20, CommissionPercent: 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &miniReseller{miniFix: m, tier: tier, plan: m.plan("Monthly", 30, 50<<30, 100_000)}
}

// reseller signs a new user in (the init data names them) and makes them a
// reseller with this much credit; it returns their session token and user.
func (m *miniReseller) reseller(username string, credit int64) (string, *store.User) {
	m.t.Helper()
	m.n++
	tg := int64(61000 + m.n)
	tok := m.signIn(fmt.Sprintf("tg:%d:%s", tg, username))
	u, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), tg)
	if err != nil {
		m.t.Fatal(err)
	}
	if _, err := m.dom.MakeReseller(context.Background(), actor0(m.fixture), u.ID, domain.ResellerInput{TierID: m.tier.ID,
		CreditLimits: map[string]int64{"IRT": credit}}, "a shop"); err != nil {
		m.t.Fatal(err)
	}
	return tok, u
}

// service gives u a delivered service named label.
func (m *miniReseller) service(u *store.User, label string) *store.Subscription {
	m.t.Helper()
	_, sub := m.orderSub(u, m.plan, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	if label != "" {
		m.sql(`UPDATE core.subscriptions SET label = $2 WHERE id = $1`, sub.ID, label)
	}
	sub, _ = m.st.GetSubscription(context.Background(), m.st.Conn(), sub.ID)
	return sub
}

func (m *miniReseller) resellersLeave() {
	m.t.Helper()
	m.sql(`UPDATE core.install SET trial_started_at = now() - interval '31 days'`)
}

func (m *miniReseller) resellersReturn() {
	m.t.Helper()
	m.sql(`UPDATE core.install SET trial_started_at = now()`)
}

func featuresOf(out map[string]any) map[string]any {
	f, _ := out["features"].(map[string]any)
	return f
}

func jlist(out map[string]any, key string) []map[string]any {
	raw, _ := out[key].([]any)
	res := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		m, _ := it.(map[string]any)
		res = append(res, m)
	}
	return res
}

// The section is offered to an active reseller whose license includes
// resellers, and to nobody else; the answer follows the license live.
func TestMiniResellerFlagInMe(t *testing.T) {
	m := newMiniReseller(t)
	customer := m.signIn("tg:61500:cust")
	staffTok := m.signIn("tg:61501:staff")
	staff, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 61501)
	if err := m.st.SetUserRole(context.Background(), m.st.Conn(), staff.ID, "admin", "active"); err != nil {
		t.Fatal(err)
	}
	tok, r := m.reseller("rsl", 0)

	flag := func(token string) any {
		t.Helper()
		code, out := m.call(http.MethodGet, "/api/mini/v1/me", token, nil)
		if code != http.StatusOK {
			t.Fatalf("me: %d %v", code, out)
		}
		return featuresOf(out)["reseller"]
	}
	if flag(customer) != false || flag(staffTok) != false {
		t.Fatal("a customer or a staff member is offered the reseller section")
	}
	if flag(tok) != true {
		t.Fatal("an active reseller is not offered the section")
	}
	// The sign-in answer carries it too.
	code, out := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": fmt.Sprintf("tg:%d", r.TelegramID)})
	if code != http.StatusOK || featuresOf(out)["reseller"] != true {
		t.Fatalf("sign-in: %d %v", code, out)
	}

	m.resellersLeave()
	if flag(tok) != false {
		t.Fatal("the section is still offered after resellers left the license")
	}
	m.resellersReturn()
	if flag(tok) != true {
		t.Fatal("the section did not come back with the license")
	}
	// A reseller who is removed is a customer again.
	if err := m.dom.RemoveReseller(context.Background(), actor0(m.fixture), r.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	if flag(tok) != false {
		t.Fatal("a former reseller is offered the section")
	}
	// A banned reseller is turned away before anything else.
	tok2, r2 := m.reseller("rsl2", 0)
	m.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, r2.ID)
	if code, out := m.call(http.MethodGet, "/api/mini/v1/me", tok2, nil); code != http.StatusForbidden || out["error"] != "banned" {
		t.Fatalf("a banned reseller: %d %v", code, out)
	}
}

// The panel: tier, discount, commission, balances with the credit and what
// can be spent, customers, the invite link, counts of services.
func TestMiniResellerPanel(t *testing.T) {
	m := newMiniReseller(t)
	m.setting(domain.SettingBotUsername, "acme_bot")
	tok, r := m.reseller("rsl", 500_000)
	m.service(r, "a")
	m.service(r, "b")
	if _, err := m.st.UpsertUser(context.Background(), m.st.Conn(), 61600, "joined", "fa", r.ID); err != nil {
		t.Fatal(err)
	}

	code, out := m.call(http.MethodGet, "/api/mini/v1/reseller", tok, nil)
	if code != http.StatusOK || out["tier"] != m.tier.Name || out["discount_percent"] != float64(20) || out["commission_percent"] != float64(10) ||
		out["customers"] != float64(1) {
		t.Fatalf("panel: %d %v", code, out)
	}
	b := jlist(out, "balances")
	if len(b) != 1 || b[0]["currency"] != "IRT" || b[0]["balance"] != float64(0) || b[0]["credit_limit"] != float64(500_000) || b[0]["available"] != float64(500_000) {
		t.Fatalf("balances: %v", b)
	}
	code4, _ := out["ref_code"].(string)
	if len(code4) != 8 || out["invite_link"] != "https://t.me/acme_bot?start=r_"+code4 {
		t.Fatalf("invite link: %v %v", out["ref_code"], out["invite_link"])
	}
	if c, _ := out["counts"].(map[string]any); c["total"] != float64(2) || c["active"] != float64(2) {
		t.Fatalf("counts: %v", out["counts"])
	}
	if e := jlist(out, "commission_earned"); len(e) != 0 {
		t.Fatalf("commission earned: %v", e)
	}

	// After a purchase on credit the balance is a debt and less can be spent.
	o := m.call2(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": m.plan.ID, "key": "panelkey123"})
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders/"+o["id"].(string)+"/pay-wallet", tok, map[string]any{}); code != http.StatusOK {
		t.Fatalf("pay on credit: %d", code)
	}
	_, out = m.call(http.MethodGet, "/api/mini/v1/reseller", tok, nil)
	b = jlist(out, "balances")
	if b[0]["balance"] != float64(-80_000) || b[0]["available"] != float64(420_000) {
		t.Fatalf("balances after a purchase on credit: %v", b)
	}

	// No bot username: no link, not a broken one.
	m.setting(domain.SettingBotUsername, "")
	if _, out := m.call(http.MethodGet, "/api/mini/v1/reseller", tok, nil); out["invite_link"] != "" {
		t.Fatalf("invite link without a bot: %v", out["invite_link"])
	}
	// The bot's own username, told at a sign-in, stands in for Branding's.
	m.api.cfg.MiniAppVerify = fakeVerifier{bot: "told_bot"}
	m.signIn("tg:61999:someone")
	if _, out := m.call(http.MethodGet, "/api/mini/v1/reseller", tok, nil); out["invite_link"] != "https://t.me/told_bot?start=r_"+code4 {
		t.Fatalf("invite link from the bot's own username: %v", out["invite_link"])
	}
	m.setting(domain.SettingBotUsername, "acme_bot") // and Branding wins when it is set
	if _, out := m.call(http.MethodGet, "/api/mini/v1/reseller", tok, nil); out["invite_link"] != "https://t.me/acme_bot?start=r_"+code4 {
		t.Fatalf("invite link with Branding's username: %v", out["invite_link"])
	}
}

// call2 is a call that must succeed.
func (m *miniReseller) call2(method, path, token string, body any) map[string]any {
	m.t.Helper()
	code, out := m.call(method, path, token, body)
	if code != http.StatusOK {
		m.t.Fatalf("%s %s: %d %v", method, path, code, out)
	}
	return out
}

// The list: this reseller's services only, newest first, filtered by group
// and searched, paged, each with its usage, link, note and what can be done;
// the counts do not follow the filter.
func TestMiniResellerServices(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 0)
	_, other := m.reseller("other", 0)
	mk := func(label, status string) *store.Subscription {
		sub := m.service(r, label)
		if status != "active" {
			m.sql(`UPDATE core.subscriptions SET status = $2 WHERE id = $1`, sub.ID, status)
		}
		time.Sleep(5 * time.Millisecond)
		return sub
	}
	a := mk("Ali", "active")
	mk("Sara", "expiring_soon")
	mk("Reza", "expired")
	mk("Mina", "depleted")
	mk("Omid", "disabled")
	mk("Pending", "pending")
	gone := mk("Gone", "deleted")
	theirs := m.service(other, "Theirs")
	m.sql(`UPDATE core.subscriptions SET note = 'pays on the 5th', traffic_used_bytes = $2 WHERE id = $1`, a.ID, int64(10<<30))
	_ = gone

	code, out := m.call(http.MethodGet, "/api/mini/v1/reseller/services", tok, nil)
	svcs := jlist(out, "services")
	if code != http.StatusOK || len(svcs) != 6 || out["total"] != float64(6) || out["page"] != float64(1) || out["size"] != float64(10) {
		t.Fatalf("list: %d %v", code, out)
	}
	if svcs[0]["label"] != "Pending" || svcs[5]["label"] != "Ali" {
		t.Fatalf("newest first: %v ... %v", svcs[0]["label"], svcs[5]["label"])
	}
	counts, _ := out["counts"].(map[string]any)
	if counts["total"] != float64(6) || counts["active"] != float64(1) || counts["expiring_soon"] != float64(1) || counts["expired"] != float64(2) ||
		counts["paused"] != float64(1) || counts["pending"] != float64(1) {
		t.Fatalf("counts: %v", counts)
	}
	if strings.Contains(fmt.Sprint(out), theirs.ID) || strings.Contains(fmt.Sprint(out), gone.ID) {
		t.Fatal("another reseller's or a deleted service is listed")
	}
	ali := svcs[5]
	if ali["id"] != a.ID || ali["note"] != "pays on the 5th" || ali["status"] != "active" || ali["traffic_used"] != float64(10<<30) ||
		ali["traffic_total"] != float64(50<<30) || ali["link"] == "" || ali["can_renew"] != true || ali["can_topup"] != true ||
		ali["can_pause"] != true || ali["can_resume"] != false || ali["can_delete"] != true || ali["expires_at"] == nil {
		t.Fatalf("a running service: %v", ali)
	}
	// What can be done follows the status.
	byLabel := map[string]map[string]any{}
	for _, s := range svcs {
		byLabel[s["label"].(string)] = s
	}
	if p := byLabel["Omid"]; p["can_resume"] != true || p["can_pause"] != false || p["can_renew"] != false || p["can_delete"] != true {
		t.Fatalf("a paused service: %v", p)
	}
	if p := byLabel["Pending"]; p["can_pause"] != false || p["can_delete"] != false || p["can_renew"] != false {
		t.Fatalf("a service being delivered: %v", p)
	}
	if p := byLabel["Reza"]; p["can_renew"] != true || p["can_topup"] != false || p["can_pause"] != true {
		t.Fatalf("an expired service: %v", p)
	}
	// The shop's own rule for renewing agrees with the section's.
	_, shop := m.call(http.MethodGet, "/api/mini/v1/services", tok, nil)
	for _, s := range jlist(shop, "services") {
		if mine := byLabel[s["label"].(string)]; mine["can_renew"] != s["can_renew"] || mine["can_topup"] != s["can_topup"] {
			t.Errorf("%v: the shop says renew %v top-up %v, the section %v %v", s["label"], s["can_renew"], s["can_topup"], mine["can_renew"], mine["can_topup"])
		}
	}

	get := func(query string) map[string]any {
		t.Helper()
		code, out := m.call(http.MethodGet, "/api/mini/v1/reseller/services?"+query, tok, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %v", query, code, out)
		}
		return out
	}
	labels := func(out map[string]any) string {
		var l []string
		for _, s := range jlist(out, "services") {
			l = append(l, s["label"].(string))
		}
		return strings.Join(l, " ")
	}
	for query, want := range map[string]string{
		"status=active": "Ali", "status=expiring_soon": "Sara", "status=expired": "Mina Reza", "status=paused": "Omid", "status=pending": "Pending",
		"q=ALI": "Ali", "q=a&status=expired": "Mina Reza", "q=zzz": "", "status=active&q=sara": "",
	} {
		if got := labels(get(query)); got != want {
			t.Errorf("?%s: %q, want %q", query, got, want)
		}
	}
	if out := get("status=expired&q=zzz"); out["total"] != float64(0) || out["counts"].(map[string]any)["total"] != float64(6) {
		t.Errorf("counts follow the filter: %v", out["counts"])
	}
	short := strings.ReplaceAll(a.ID, "-", "")[26:]
	if got := labels(get("q=%23" + short)); got != "Ali" {
		t.Errorf("by number: %q", got)
	}
	p2 := get("size=4&page=2")
	if labels(p2) != "Sara Ali" || p2["total"] != float64(6) || p2["page"] != float64(2) || p2["size"] != float64(4) {
		t.Errorf("page 2: %v", p2)
	}
	if past := get("size=4&page=9"); labels(past) != "" || past["total"] != float64(6) {
		t.Errorf("past the end: %v", past)
	}

	// Bad input.
	for _, q := range []string{"status=deleted", "status=bogus", "page=0", "page=x", "page=100001", "size=0", "size=51", "size=-1",
		"q=" + strings.Repeat("x", 101)} {
		if code, out := m.call(http.MethodGet, "/api/mini/v1/reseller/services?"+q, tok, nil); code != http.StatusBadRequest || out["error"] != "invalid" {
			t.Errorf("?%s: %d %v", q, code, out)
		}
	}
}

// Pause and resume turn the client off and on, audited as the reseller's, and
// repeating either changes nothing; support's pause cannot be undone;
// a service that is not theirs is not found; a panel that is down is 503.
func TestMiniResellerPauseResume(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 0)
	_, other := m.reseller("other", 0)
	sub := m.service(r, "Ali")
	theirs := m.service(other, "Theirs")
	base := "/api/mini/v1/reseller/services/"

	code, out := m.call(http.MethodPost, base+sub.ID+"/pause", tok, map[string]any{})
	svc, _ := out["service"].(map[string]any)
	if code != http.StatusOK || svc["status"] != "disabled" || svc["can_resume"] != true || svc["can_pause"] != false {
		t.Fatalf("pause: %d %v", code, out)
	}
	if m.prov.done() != "disable:"+sub.ID {
		t.Fatalf("panel calls: %s", m.prov.done())
	}
	rows, _, _ := m.st.ListAudit(context.Background(), m.st.Conn(), store.AuditFilter{Action: "subscription.disable", EntityID: sub.ID}, store.Page{})
	if len(rows) != 1 || rows[0].ActorID == nil || *rows[0].ActorID != r.ID || rows[0].Source != store.SourceBot || rows[0].ActorRole != domain.ResellerRole {
		t.Fatalf("audit of the pause: %+v", rows)
	}
	if code, _ := m.call(http.MethodPost, base+sub.ID+"/pause", tok, map[string]any{}); code != http.StatusOK || m.prov.done() != "disable:"+sub.ID {
		t.Fatalf("pause again: %d, panel calls %s", code, m.prov.done())
	}

	code, out = m.call(http.MethodPost, base+sub.ID+"/resume", tok, map[string]any{})
	svc, _ = out["service"].(map[string]any)
	if code != http.StatusOK || svc["status"] != "active" || svc["can_pause"] != true {
		t.Fatalf("resume: %d %v", code, out)
	}
	if !strings.HasPrefix(m.prov.done(), "disable:"+sub.ID+",enable:"+sub.ID) {
		t.Fatalf("panel calls: %s", m.prov.done())
	}
	rows, _, _ = m.st.ListAudit(context.Background(), m.st.Conn(), store.AuditFilter{Action: "subscription.enable", EntityID: sub.ID}, store.Page{})
	if len(rows) != 1 || *rows[0].ActorID != r.ID || rows[0].Source != store.SourceBot {
		t.Fatalf("audit of the resume: %+v", rows)
	}

	// Support pauses it: the reseller cannot resume it.
	if _, err := m.dom.SetSubscriptionEnabled(context.Background(), actor0(m.fixture), m.prov, sub.ID, false, "abuse"); err != nil {
		t.Fatal(err)
	}
	ops := m.prov.done()
	if code, out := m.call(http.MethodPost, base+sub.ID+"/resume", tok, map[string]any{}); code != http.StatusConflict || out["error"] != "paused_by_staff" {
		t.Fatalf("resumed what support paused: %d %v", code, out)
	}
	if m.prov.done() != ops {
		t.Fatalf("the panel was called: %s", m.prov.done())
	}

	// Not theirs, unknown, malformed: 404, and the panel is never called.
	ops = m.prov.done()
	for _, id := range []string{theirs.ID, "00000000-0000-0000-0000-0000000000aa", "not-an-id"} {
		for _, verb := range []string{"pause", "resume"} {
			if code, out := m.call(http.MethodPost, base+id+"/"+verb, tok, map[string]any{}); code != http.StatusNotFound {
				t.Errorf("%s %s: %d %v", verb, id, code, out)
			}
		}
	}
	if m.prov.done() != ops {
		t.Fatalf("the panel was called for a service that is not theirs: %s", m.prov.done())
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), theirs.ID); got.Status != "active" {
		t.Fatalf("their service changed: %s", got.Status)
	}

	// Nothing but an empty body: an amount or price sent is refused.
	if code, _ := m.call(http.MethodPost, base+sub.ID+"/pause", tok, map[string]any{"amount": 5}); code != http.StatusBadRequest {
		t.Errorf("a body with an amount: %d", code)
	}

	// The panel does not answer.
	other2 := m.service(r, "Other")
	m.prov.mu.Lock()
	m.prov.down = true
	m.prov.mu.Unlock()
	if code, out := m.call(http.MethodPost, base+other2.ID+"/pause", tok, map[string]any{}); code != http.StatusServiceUnavailable || out["error"] != "unavailable" {
		t.Fatalf("panel down: %d %v", code, out)
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), other2.ID); got.Status != "active" {
		t.Fatalf("a failed pause changed the service: %s", got.Status)
	}
}

// Deleting needs the confirmation, removes the client for good without
// moving money, is audited, and repeats harmlessly.
func TestMiniResellerDelete(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 0)
	_, other := m.reseller("other", 0)
	sub := m.service(r, "Ali")
	theirs := m.service(other, "Theirs")
	pending := m.service(r, "Pending")
	m.sql(`UPDATE core.subscriptions SET status = 'pending' WHERE id = $1`, pending.ID)
	base := "/api/mini/v1/reseller/services/"
	m.fund(r.ID, 1000)

	// No confirmation, a refused confirmation, a missing body: nothing happens.
	for name, body := range map[string]any{"empty": map[string]any{}, "false": map[string]any{"confirm": false}, "wrong type": map[string]any{"confirm": "yes"}} {
		if code, out := m.call(http.MethodPost, base+sub.ID+"/delete", tok, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), sub.ID); got.Status != "active" || m.prov.done() != "" {
		t.Fatalf("deleted without a confirmation: %s, panel calls %q", got.Status, m.prov.done())
	}

	code, out := m.call(http.MethodPost, base+sub.ID+"/delete", tok, map[string]any{"confirm": true})
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("delete: %d %v", code, out)
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), sub.ID); got.Status != "deleted" || m.prov.done() != "delete:"+sub.ID {
		t.Fatalf("after the delete: %s, panel calls %q", got.Status, m.prov.done())
	}
	rows, _, _ := m.st.ListAudit(context.Background(), m.st.Conn(), store.AuditFilter{Action: "subscription.delete", EntityID: sub.ID}, store.Page{})
	if len(rows) != 1 || *rows[0].ActorID != r.ID || rows[0].Source != store.SourceBot || rows[0].ActorRole != domain.ResellerRole {
		t.Fatalf("audit: %+v", rows)
	}
	var bal int64
	if err := m.st.DB().QueryRow(context.Background(), `SELECT balance FROM core.wallets WHERE user_id = $1 AND currency = 'IRT'`, r.ID).Scan(&bal); err != nil || bal != 1000 {
		t.Fatalf("balance after the delete (no refund): %d %v", bal, err)
	}
	// Again: still fine, once.
	if code, _ := m.call(http.MethodPost, base+sub.ID+"/delete", tok, map[string]any{"confirm": true}); code != http.StatusOK || m.prov.done() != "delete:"+sub.ID {
		t.Fatalf("delete again: %d, panel calls %q", code, m.prov.done())
	}
	if _, out := m.call(http.MethodGet, "/api/mini/v1/reseller/services", tok, nil); strings.Contains(fmt.Sprint(out), sub.ID) {
		t.Fatal("the deleted service is still listed")
	}
	// Gone for the other powers.
	if code, _ := m.call(http.MethodPost, base+sub.ID+"/pause", tok, map[string]any{}); code != http.StatusNotFound {
		t.Errorf("pausing a deleted service: %d", code)
	}

	// Not theirs: 404, nothing deleted. Being delivered: refused as a state.
	if code, _ := m.call(http.MethodPost, base+theirs.ID+"/delete", tok, map[string]any{"confirm": true}); code != http.StatusNotFound {
		t.Errorf("deleting another reseller's service: %d", code)
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), theirs.ID); got.Status != "active" {
		t.Fatalf("their service was deleted: %s", got.Status)
	}
	if code, out := m.call(http.MethodPost, base+pending.ID+"/delete", tok, map[string]any{"confirm": true}); code != http.StatusConflict || out["error"] != "state" {
		t.Errorf("deleting a service being delivered: %d %v", code, out)
	}
	// The panel is down: the service stays.
	keep := m.service(r, "Keep")
	m.prov.mu.Lock()
	m.prov.down = true
	m.prov.mu.Unlock()
	if code, out := m.call(http.MethodPost, base+keep.ID+"/delete", tok, map[string]any{"confirm": true}); code != http.StatusServiceUnavailable {
		t.Errorf("panel down: %d %v", code, out)
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), keep.ID); got.Status != "active" {
		t.Fatalf("a failed delete changed the service: %s", got.Status)
	}
}

// Naming and noting a service: one line each, limits, clearing, and only the
// reseller's own.
func TestMiniResellerLabelAndNote(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 0)
	_, other := m.reseller("other", 0)
	sub := m.service(r, "Ali")
	theirs := m.service(other, "Theirs")
	base := "/api/mini/v1/reseller/services/"

	code, out := m.call(http.MethodPost, base+sub.ID+"/label", tok, map[string]any{"label": "  Reza's phone "})
	svc, _ := out["service"].(map[string]any)
	if code != http.StatusOK || svc["label"] != "Reza's phone" {
		t.Fatalf("rename: %d %v", code, out)
	}
	code, out = m.call(http.MethodPost, base+sub.ID+"/note", tok, map[string]any{"note": "  pays on the 5th "})
	svc, _ = out["service"].(map[string]any)
	if code != http.StatusOK || svc["note"] != "pays on the 5th" || svc["label"] != "Reza's phone" {
		t.Fatalf("note: %d %v", code, out)
	}
	long := strings.Repeat("ب", domain.MaxNote)
	if code, out := m.call(http.MethodPost, base+sub.ID+"/note", tok, map[string]any{"note": long}); code != http.StatusOK || out["service"].(map[string]any)["note"] != long {
		t.Fatalf("a 200-character note: %d", code)
	}
	for name, body := range map[string]map[string]any{
		"a long label": {"label": strings.Repeat("x", domain.MaxLabel+1)}, "two lines": {"label": "a\nb"},
	} {
		if code, out := m.call(http.MethodPost, base+sub.ID+"/label", tok, body); code != http.StatusBadRequest || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	for name, note := range map[string]string{"a long note": long + "x", "two lines": "a\nb", "a tab": "a\tb"} {
		if code, out := m.call(http.MethodPost, base+sub.ID+"/note", tok, map[string]any{"note": note}); code != http.StatusBadRequest || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, _ := m.call(http.MethodPost, base+sub.ID+"/note", tok, map[string]any{"note": "x", "label": "y"}); code != http.StatusBadRequest {
		t.Errorf("an unknown field: %d", code)
	}
	// Clearing.
	if code, out := m.call(http.MethodPost, base+sub.ID+"/note", tok, map[string]any{"note": ""}); code != http.StatusOK || out["service"].(map[string]any)["note"] != "" {
		t.Fatalf("clear the note: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, base+sub.ID+"/label", tok, map[string]any{"label": ""}); code != http.StatusOK || out["service"].(map[string]any)["label"] != "" {
		t.Fatalf("clear the name: %d %v", code, out)
	}
	// Not theirs: 404, unchanged.
	for _, verb := range []string{"label", "note"} {
		body := map[string]any{verb: "hijacked"}
		if code, _ := m.call(http.MethodPost, base+theirs.ID+"/"+verb, tok, body); code != http.StatusNotFound {
			t.Errorf("%s of another reseller's service: %d", verb, code)
		}
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), theirs.ID); got.Note != nil || *got.Label != "Theirs" {
		t.Fatalf("their service changed: %+v", got)
	}
}

// The customers who joined through the link, as the section shows them.
func TestMiniResellerCustomers(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 0)
	_, other := m.reseller("rival", 0)
	ctx := context.Background()
	for i := 1; i <= 12; i++ {
		u, err := m.st.UpsertUser(ctx, m.st.Conn(), int64(61700+i), fmt.Sprintf("cust%02d", i), "fa", r.ID)
		if err != nil {
			t.Fatal(err)
		}
		m.sql(`UPDATE core.users SET created_at = now() - make_interval(hours => $2::int) WHERE id = $1`, u.ID, 100-i)
	}
	rival, err := m.st.UpsertUser(ctx, m.st.Conn(), 61790, "rivals", "fa", other.ID)
	if err != nil {
		t.Fatal(err)
	}
	stranger := m.customer(61791, "nobodys")
	// cust12 (the newest) buys twice at the store price: 10% to the reseller.
	buyer, _ := m.st.GetUserByTelegramID(ctx, m.st.Conn(), 61712)
	for i, who := range []*store.User{buyer, buyer, rival, stranger} {
		m.fund(who.ID, 1_000_000+int64(i))
		o, err := m.dom.CreateOrder(ctx, domain.CreateOrderParams{UserID: who.ID, PlanID: m.plan.ID, IdempotencyKey: fmt.Sprintf("cust-order-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.dom.PayOrderWithWallet(ctx, o.ID, who.ID); err != nil {
			t.Fatal(err)
		}
	}

	code, out := m.call(http.MethodGet, "/api/mini/v1/reseller/customers", tok, nil)
	cs := jlist(out, "customers")
	if code != http.StatusOK || out["total"] != float64(12) || len(cs) != 10 || out["page"] != float64(1) || out["size"] != float64(10) {
		t.Fatalf("customers: %d %v", code, out)
	}
	if cs[0]["username"] != "cust12" || cs[0]["telegram_id"] != float64(61712) || cs[0]["paid_orders"] != float64(2) || cs[0]["joined_at"] == nil {
		t.Fatalf("newest customer: %v", cs[0])
	}
	spent, _ := cs[0]["spent"].([]any)
	comm, _ := cs[0]["commission"].([]any)
	if len(spent) != 1 || spent[0].(map[string]any)["amount"] != float64(200_000) || spent[0].(map[string]any)["currency"] != "IRT" ||
		len(comm) != 1 || comm[0].(map[string]any)["amount"] != float64(20_000) {
		t.Fatalf("spent and commission: %v %v", spent, comm)
	}
	if cs[1]["username"] != "cust11" || cs[1]["paid_orders"] != float64(0) || len(cs[1]["spent"].([]any)) != 0 {
		t.Fatalf("second customer: %v", cs[1])
	}
	// Only these fields, and nobody else's customers.
	for _, c := range cs {
		if len(c) != 6 {
			t.Fatalf("a customer carries %d fields: %v", len(c), c)
		}
	}
	if strings.Contains(fmt.Sprint(out), "rivals") || strings.Contains(fmt.Sprint(out), "nobodys") || strings.Contains(fmt.Sprint(out), "61790") {
		t.Fatalf("someone who did not join through this link: %v", out)
	}
	_, p2 := m.call(http.MethodGet, "/api/mini/v1/reseller/customers?page=2", tok, nil)
	if c2 := jlist(p2, "customers"); len(c2) != 2 || c2[1]["username"] != "cust01" || p2["total"] != float64(12) {
		t.Fatalf("page 2: %v", p2)
	}
	for _, q := range []string{"page=0", "size=51", "page=abc"} {
		if code, _ := m.call(http.MethodGet, "/api/mini/v1/reseller/customers?"+q, tok, nil); code != http.StatusBadRequest {
			t.Errorf("?%s: %d", q, code)
		}
	}
	// The rival sees only theirs.
	rtok := m.tokenOf(other)
	_, rout := m.call(http.MethodGet, "/api/mini/v1/reseller/customers", rtok, nil)
	if rc := jlist(rout, "customers"); rout["total"] != float64(1) || len(rc) != 1 || rc[0]["username"] != "rivals" {
		t.Fatalf("the rival's customers: %v", rout)
	}
}

// tokenOf signs a user in again (their init data names them).
func (m *miniReseller) tokenOf(u *store.User) string {
	m.t.Helper()
	return m.signIn(fmt.Sprintf("tg:%d", u.TelegramID))
}

// The statement pages the reseller's wallet movements.
func TestMiniResellerStatement(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 1_000_000)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if _, _, err := m.dom.RecordResellerPayment(ctx, actor0(m.fixture), r.ID, 1000+int64(i), "IRT", "paid", fmt.Sprintf("stmt-key-0000%02d", i)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Millisecond)
	}
	code, out := m.call(http.MethodGet, "/api/mini/v1/reseller/statement", tok, nil)
	es := jlist(out, "entries")
	if code != http.StatusOK || len(es) != 10 || out["has_more"] != true || out["page"] != float64(1) {
		t.Fatalf("page 1: %d %v", code, out)
	}
	if es[0]["kind"] != "settlement" || es[0]["amount"] != float64(1011) || es[0]["currency"] != "IRT" || es[0]["balance_after"] == nil || es[0]["at"] == nil {
		t.Fatalf("newest entry: %v", es[0])
	}
	_, p2 := m.call(http.MethodGet, "/api/mini/v1/reseller/statement?page=2", tok, nil)
	if e2 := jlist(p2, "entries"); len(e2) != 2 || p2["has_more"] != false {
		t.Fatalf("page 2: %v", p2)
	}
	_, exact := m.call(http.MethodGet, "/api/mini/v1/reseller/statement?size=12", tok, nil)
	if len(jlist(exact, "entries")) != 12 || exact["has_more"] != false {
		t.Fatalf("an exact page: %v", exact)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/reseller/statement?page=0", tok, nil); code != http.StatusBadRequest {
		t.Errorf("page 0: %d", code)
	}
}

// Every section call refuses anyone who is not a signed-in, active reseller
// with resellers in the license, and changes nothing.
func TestMiniResellerCallsAreForResellersOnly(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 0)
	sub := m.service(r, "Ali")
	customer := m.signIn("tg:61800:plain")
	custUser, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 61800)
	custSub := m.service(custUser, "customers")
	admin := m.signIn("tg:61801:admin")
	adminUser, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 61801)
	if err := m.st.SetUserRole(context.Background(), m.st.Conn(), adminUser.ID, "admin", "active"); err != nil {
		t.Fatal(err)
	}
	base := "/api/mini/v1/reseller"
	calls := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, base, nil},
		{http.MethodGet, base + "/services", nil},
		{http.MethodGet, base + "/customers", nil},
		{http.MethodGet, base + "/statement", nil},
		{http.MethodPost, base + "/services/" + custSub.ID + "/pause", map[string]any{}},
		{http.MethodPost, base + "/services/" + custSub.ID + "/resume", map[string]any{}},
		{http.MethodPost, base + "/services/" + custSub.ID + "/delete", map[string]any{"confirm": true}},
		{http.MethodPost, base + "/services/" + custSub.ID + "/label", map[string]any{"label": "mine now"}},
		{http.MethodPost, base + "/services/" + custSub.ID + "/note", map[string]any{"note": "mine now"}},
	}
	untouched := func(what string) {
		t.Helper()
		if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), custSub.ID); got.Status != "active" || got.Note != nil || *got.Label != "customers" {
			t.Fatalf("%s: a customer's service changed: %+v", what, got)
		}
		if m.prov.done() != "" {
			t.Fatalf("%s: the panel was called: %s", what, m.prov.done())
		}
	}
	for _, c := range calls {
		// No session, a wrong token: 401.
		for _, token := range []string{"", "not-a-token"} {
			if code, _ := m.call(c.method, c.path, token, c.body); code != http.StatusUnauthorized {
				t.Errorf("%s %s with token %q: %d", c.method, c.path, token, code)
			}
		}
		// A customer and a staff member are not resellers: 403.
		for who, token := range map[string]string{"a customer": customer, "a staff member": admin} {
			if code, out := m.call(c.method, c.path, token, c.body); code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d %v", c.method, c.path, who, code, out)
			}
		}
		untouched(c.method + " " + c.path)
	}

	// The reseller's own ids on another's service, and the rest of the section
	// after resellers left the license.
	for _, verb := range []string{"pause", "resume", "label", "note"} {
		var body any = map[string]any{}
		if verb == "label" || verb == "note" {
			body = map[string]any{verb: "x"}
		}
		if code, _ := m.call(http.MethodPost, base+"/services/"+custSub.ID+"/"+verb, tok, body); code != http.StatusNotFound {
			t.Errorf("%s of a customer's service as a reseller: %d", verb, code)
		}
	}
	if code, _ := m.call(http.MethodPost, base+"/services/"+custSub.ID+"/delete", tok, map[string]any{"confirm": true}); code != http.StatusNotFound {
		t.Errorf("delete of a customer's service as a reseller: %d", code)
	}
	untouched("a reseller on a customer's service")

	m.resellersLeave()
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, base, nil}, {http.MethodGet, base + "/services", nil}, {http.MethodGet, base + "/customers", nil},
		{http.MethodGet, base + "/statement", nil},
		{http.MethodPost, base + "/services/" + sub.ID + "/pause", map[string]any{}},
		{http.MethodPost, base + "/services/" + sub.ID + "/delete", map[string]any{"confirm": true}},
		{http.MethodPost, base + "/services/" + sub.ID + "/label", map[string]any{"label": "x"}},
		{http.MethodPost, base + "/services/" + sub.ID + "/note", map[string]any{"note": "x"}},
	} {
		code, out := m.call(c.method, c.path, tok, c.body)
		if code != http.StatusForbidden || strings.Contains(strings.ToLower(fmt.Sprint(out)), "licen") {
			t.Errorf("%s %s after the license: %d %v", c.method, c.path, code, out)
		}
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), sub.ID); got.Status != "active" || got.Note != nil || *got.Label != "Ali" || m.prov.done() != "" {
		t.Fatalf("a refused call changed the service: %+v, panel calls %q", got, m.prov.done())
	}
	m.resellersReturn()
	if code, _ := m.call(http.MethodGet, base, tok, nil); code != http.StatusOK {
		t.Fatalf("the section after the license came back: %d", code)
	}

	// A banned reseller is turned away by the session.
	m.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, r.ID)
	if code, out := m.call(http.MethodPost, base+"/services/"+sub.ID+"/delete", tok, map[string]any{"confirm": true}); code != http.StatusForbidden || out["error"] != "banned" {
		t.Fatalf("a banned reseller: %d %v", code, out)
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), sub.ID); got.Status != "active" {
		t.Fatalf("a banned reseller deleted a service: %s", got.Status)
	}
}

// The shared limits apply to the section: changes are 20 a minute per
// customer (the answer says to wait), and nothing changes past the limit.
func TestMiniResellerSharedLimits(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 0)
	sub := m.service(r, "Ali")
	other := m.service(r, "Other")
	url := "/api/mini/v1/reseller/services/"
	last := 0
	for i := 0; i < miniWriteRate; i++ { // every change within the limit goes through
		if last, _ = m.call(http.MethodPost, url+sub.ID+"/note", tok, map[string]any{"note": fmt.Sprint("n", i)}); last != http.StatusOK {
			t.Fatalf("change %d of %d: %d", i+1, miniWriteRate, last)
		}
	}
	code, out := m.call(http.MethodPost, url+other.ID+"/delete", tok, map[string]any{"confirm": true})
	if code != http.StatusTooManyRequests || out["error"] != "rate_limited" {
		t.Fatalf("change %d in a minute: %d %v", miniWriteRate+1, code, out)
	}
	if got, _ := m.st.GetSubscription(context.Background(), m.st.Conn(), other.ID); got.Status != "active" || m.prov.done() != "" {
		t.Fatalf("a change over the limit went through: %s, panel calls %q", got.Status, m.prov.done())
	}
	// Reading has a limit of its own, much higher.
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/reseller", tok, nil); code != http.StatusOK {
		t.Fatalf("reading after the changes: %d", code)
	}
}

// A maintenance break closes the section like the rest of the shop.
func TestMiniResellerMaintenance(t *testing.T) {
	m := newMiniReseller(t)
	tok, _ := m.reseller("rsl", 0)
	m.setting("maintenance.enabled", "true")
	t.Cleanup(func() { m.setting("maintenance.enabled", "false") })
	for _, path := range []string{"/api/mini/v1/reseller", "/api/mini/v1/reseller/services", "/api/mini/v1/reseller/customers", "/api/mini/v1/reseller/statement"} {
		if code, out := m.call(http.MethodGet, path, tok, nil); code != http.StatusServiceUnavailable || out["error"] != "maintenance" {
			t.Errorf("%s in maintenance: %d %v", path, code, out)
		}
	}
}

// A reseller buys for a customer through the shop's own calls: the plans at
// their price, a name on the new order (a customer's name for it is ignored),
// payment from the credit, no price or amount from the client.
func TestMiniResellerBuysForACustomer(t *testing.T) {
	m := newMiniReseller(t)
	tok, r := m.reseller("rsl", 500_000)
	cust := m.signIn("tg:61900:plain")
	ctx := context.Background()

	_, out := m.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	var seen map[string]any
	for _, p := range jlist(out, "plans") {
		if p["id"] == m.plan.ID {
			seen = p
		}
	}
	if seen == nil || seen["price"] != float64(80_000) || seen["list_price"] != float64(100_000) {
		t.Fatalf("the reseller's price: %v", seen)
	}

	order := func(token, key, label string) map[string]any {
		t.Helper()
		body := map[string]any{"plan_id": m.plan.ID, "key": key}
		if label != "" {
			body["label"] = label
		}
		return m.call2(http.MethodPost, "/api/mini/v1/orders", token, body)
	}
	o := order(tok, "buy-for-ali-1", "  Ali's phone ")
	stored, err := m.st.GetOrder(ctx, m.st.Conn(), o["id"].(string))
	if err != nil || stored.Label == nil || *stored.Label != "Ali's phone" || stored.Amount != 80_000 || o["amount"] != float64(80_000) {
		t.Fatalf("order with a name: %+v %v", stored, err)
	}
	// The same key is the same order, whatever the name says the second time.
	if again := order(tok, "buy-for-ali-1", "Another"); again["id"] != o["id"] {
		t.Fatalf("a repeated key made another order: %v", again)
	}
	if stored, _ := m.st.GetOrder(ctx, m.st.Conn(), o["id"].(string)); *stored.Label != "Ali's phone" {
		t.Fatalf("a repeated request renamed the order: %q", *stored.Label)
	}
	// Paid from the credit: the balance goes below zero within the limit.
	paid := m.call2(http.MethodPost, "/api/mini/v1/orders/"+o["id"].(string)+"/pay-wallet", tok, map[string]any{})
	if paid["status"] == "awaiting_payment" {
		t.Fatalf("pay on credit: %v", paid)
	}
	_, wallet := m.call(http.MethodGet, "/api/mini/v1/wallet", tok, nil)
	if ws := jlist(wallet, "wallets"); ws[0]["balance"] != float64(-80_000) || ws[0]["credit_limit"] != float64(500_000) {
		t.Fatalf("wallet after paying on credit: %v", ws)
	}
	// Past the credit: refused as insufficient funds, the balance stays.
	m.sql(`UPDATE core.wallets SET balance = -450000 WHERE user_id = $1 AND currency = 'IRT'`, r.ID)
	big := order(tok, "buy-for-ali-2", "Too much")
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+big["id"].(string)+"/pay-wallet", tok, map[string]any{}); code != http.StatusConflict || out["error"] != "insufficient_funds" {
		t.Fatalf("past the credit: %d %v", code, out)
	}

	// A name that is too long or has two lines is refused.
	for name, label := range map[string]string{"too long": strings.Repeat("x", domain.MaxLabel+1), "two lines": "a\nb"} {
		if code, out := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": m.plan.ID, "key": "label-" + strings.ReplaceAll(name, " ", ""), "label": label}); code != http.StatusBadRequest || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	// A customer's label is ignored: the order has no name, at the store price.
	co := order(cust, "customer-label-1", "my label")
	cstored, _ := m.st.GetOrder(ctx, m.st.Conn(), co["id"].(string))
	if cstored.Label != nil || cstored.Amount != 100_000 {
		t.Fatalf("a customer's order: label %v, amount %d", cstored.Label, cstored.Amount)
	}
	// A renewal takes no name either, and nothing takes a price or an amount.
	sub := m.service(r, "Ali")
	renew := m.call2(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": m.plan.ID, "type": "renew", "subscription_id": sub.ID, "key": "renew-for-ali-1", "label": "ignored"})
	if rstored, _ := m.st.GetOrder(ctx, m.st.Conn(), renew["id"].(string)); rstored.Label != nil || rstored.Amount != 80_000 {
		t.Fatalf("a renewal: label %v amount %d", rstored.Label, rstored.Amount)
	}
	for _, field := range []string{"price", "amount", "total", "discount_amount"} {
		if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": m.plan.ID, "key": "price-" + field, field: 1}); code != http.StatusBadRequest {
			t.Errorf("a %s from the client: %d", field, code)
		}
	}
	// After resellers left the license the shop refuses their purchases like
	// any other new service (core says "unavailable now"), and says nothing of licensing.
	m.resellersLeave()
	code, lout := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": m.plan.ID, "key": "after-the-license", "label": "late"})
	if code != http.StatusConflict || lout["error"] != "unavailable_now" || strings.Contains(strings.ToLower(fmt.Sprint(lout)), "licen") {
		t.Fatalf("a reseller's purchase after the license: %d %v", code, lout)
	}
}

// A reseller chooses their config prefix from the Mini App, once; the panel shows it; another reseller cannot take it, a bad one is
// refused, a second try is a conflict, and a customer who is not a reseller has no such call.
func TestMiniResellerChoosesAConfigPrefixOnce(t *testing.T) {
	m := newMiniReseller(t)
	tok, _ := m.reseller("pfx1", 0)
	tok2, _ := m.reseller("pfx2", 0)
	cust := m.signIn("tg:61950:plain")
	post := func(token, prefix string) (int, map[string]any) {
		return m.call(http.MethodPost, "/api/mini/v1/reseller/prefix", token, map[string]any{"prefix": prefix})
	}

	if code, out := m.call(http.MethodGet, "/api/mini/v1/reseller", tok, nil); code != http.StatusOK || out["config_prefix"] != "" || out["panel_group"] != "" {
		t.Fatalf("the panel before: %d %v", code, out)
	}
	for _, bad := range []string{"", "x", "has-dash", "u12", "waytoolongprefix"} {
		if code, out := post(tok, bad); code != http.StatusBadRequest {
			t.Errorf("prefix %q: %d %v", bad, code, out)
		}
	}
	code, out := post(tok, " Sob ")
	if code != http.StatusOK || out["config_prefix"] != "sob" {
		t.Fatalf("set: %d %v", code, out)
	}
	if code, out := post(tok, "other"); code != http.StatusConflict || out["error"] != "state" {
		t.Errorf("a second try: %d %v", code, out)
	}
	if code, out := post(tok2, "sob"); code != http.StatusBadRequest {
		t.Errorf("another reseller's prefix: %d %v", code, out)
	}
	if code, out := m.call(http.MethodGet, "/api/mini/v1/reseller", tok, nil); code != http.StatusOK || out["config_prefix"] != "sob" {
		t.Errorf("the panel after: %d %v", code, out)
	}
	if code, _ := post(cust, "mine"); code == http.StatusOK {
		t.Errorf("a customer set a reseller prefix")
	}
}
