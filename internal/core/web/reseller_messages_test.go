package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Messages to resellers: owner and admin only; the audience, the live check (unknown placeholders), the preview for one reseller, the test
// to me with the Settle button, and the send through the broadcasts.
func TestResellerMessagesAPI(t *testing.T) {
	f, b, bot := setupFixture(t)
	ctx := context.Background()
	tier, err := f.dom.CreateTier(ctx, actor0(f), domain.TierInput{Name: "Gold"}, "")
	if err != nil {
		t.Fatal(err)
	}
	shop := f.customer(8701, "tabriz_shop")
	calm := f.customer(8702, "calm_shop")
	for _, u := range []*store.User{shop, calm} {
		if _, err := f.dom.MakeReseller(ctx, actor0(f), u.ID, domain.ResellerInput{TierID: tier.ID, CreditLimits: map[string]int64{"IRT": 1_000_000}}, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := f.st.Debit(ctx, tx, shop.ID, "IRT", 250_000, &store.LedgerEntry{Kind: "adjust", IdempotencyKey: "debt:shop"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	f.staffUser(8703, "support")
	sup := f.browser()
	sup.loginWithLink(8703)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/reseller-messages"}, {http.MethodPost, "/api/v1/reseller-messages/audience"},
		{http.MethodPost, "/api/v1/reseller-messages/preview"}, {http.MethodPost, "/api/v1/reseller-messages/test"},
		{http.MethodGet, "/api/v1/reseller-messages/reminder"}, {http.MethodPut, "/api/v1/reseller-messages/reminder"},
		{http.MethodPost, "/api/v1/reseller-messages/reminder/test"},
	} {
		if code, _ := sup.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("support %s %s: %d", c.method, c.path, code)
		}
	}

	if code, out := b.do(http.MethodPost, "/api/v1/reseller-messages/audience", map[string]any{"debt_only": true}); code != 200 || out["total"] != float64(1) {
		t.Fatalf("in debt: %d %v", code, out)
	}
	if _, out := b.do(http.MethodPost, "/api/v1/reseller-messages/audience", map[string]any{"tier_id": tier.ID}); out["total"] != float64(2) {
		t.Fatalf("the tier: %v", out)
	}
	if _, out := b.do(http.MethodPost, "/api/v1/reseller-messages/check", map[string]any{"lang": "fa", "text": "بدهی {debt} و {dbt}"}); out["ok"] != false ||
		!strings.Contains(asJSON(out["problems"]), `"arg":"{dbt}"`) || !strings.Contains(asJSON(out["placeholders"]), "credit_limit") {
		t.Fatalf("check: %v", out)
	}

	code, out := b.do(http.MethodPost, "/api/v1/reseller-messages/preview", map[string]any{"reseller_id": shop.ID, "lang": "en", "text": "{name} owes <b>{debt}</b>"})
	if code != 200 || out["text"] != "@tabriz_shop owes <b>250,000 Toman</b>" || out["reseller"].(map[string]any)["username"] != "tabriz_shop" {
		t.Fatalf("preview: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/reseller-messages/preview", map[string]any{"lang": "en", "text": "{owed}"}); code != 400 ||
		!strings.Contains(asJSON(out["problems"]), "placeholder_unknown") {
		t.Fatalf("preview of an unknown placeholder: %d %v", code, out)
	}

	code, out = b.do(http.MethodPost, "/api/v1/reseller-messages/test", map[string]any{"reseller_id": calm.ID, "lang": "fa", "text": "{name}: {debt}",
		"settle_button": true})
	if code != 200 || out["ok"] != true || len(bot.tests) != 1 || bot.tests[0].ChatID != ownerTG || bot.tests[0].Text != "@calm_shop: ۰ تومان" ||
		bot.tests[0].Settle != "💳 تسویه / افزایش موجودی" {
		t.Fatalf("test to me: %d %v %+v", code, out, bot.tests)
	}

	code, out = b.do(http.MethodPost, "/api/v1/reseller-messages", map[string]any{"debt_only": true, "texts": map[string]string{"fa": "{debt}", "en": "{owed}"}})
	if code != 400 || !strings.Contains(asJSON(out["problems"]), `"lang":"en"`) {
		t.Fatalf("an unknown placeholder: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/reseller-messages", map[string]any{"debt_only": true, "settle_button": true,
		"texts": map[string]string{"fa": "بدهی شما {debt}", "en": "You owe {debt}"}})
	it := item(out)
	if code != 200 || it["segment"] != "resellers" || it["status"] != "scheduled" || it["resellers"].(map[string]any)["debt_only"] != true ||
		it["resellers"].(map[string]any)["settle_button"] != true {
		t.Fatalf("send: %d %v", code, out)
	}
	if n := countAudit(t, f, "reseller_message.send"); n != 1 {
		t.Errorf("audited sends: %d", n)
	}
	if n := countAudit(t, f, "reseller_message.test"); n != 1 {
		t.Errorf("audited tests: %d", n)
	}
}

// The reminder's card: its defaults, a save of its settings (an unknown placeholder refused, the default text kept as the default), and a test
// to me in my language.
func TestReminderAPI(t *testing.T) {
	f, b, bot := setupFixture(t)
	code, out := b.do(http.MethodGet, "/api/v1/reseller-messages/reminder", nil)
	if code != 200 || out["enabled"] != false || out["weekday"] != "thursday" || out["time"] != "21:00" || out["debt_only"] != true ||
		out["next_due_at"] != nil || out["last"] != nil || out["zone"] != "Asia/Tehran" {
		t.Fatalf("defaults: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/reseller-messages/reminder", map[string]any{"values": map[string]string{"general.timezone": "UTC"}}); code != 400 {
		t.Fatalf("another setting: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/reseller-messages/reminder", map[string]any{"values": map[string]string{
		domain.SettingReminderText + "en": "Pay {amount}"}}); code != 400 || !strings.Contains(asJSON(out["problems"]), "{amount}") {
		t.Fatalf("an unknown placeholder: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/reseller-messages/reminder", map[string]any{"values": map[string]string{
		domain.SettingReminderEnabled: "true", domain.SettingReminderWeekday: "saturday", domain.SettingReminderTime: "20:30",
		domain.SettingReminderText + "fa": domain.DefaultReminderTexts["fa"], domain.SettingReminderText + "en": "Please pay {debt}"}})
	if code != 200 || out["enabled"] != true || out["weekday"] != "saturday" || out["time"] != "20:30" || out["next_due_at"] == nil ||
		out["custom"].(map[string]any)["fa"] != false || out["custom"].(map[string]any)["en"] != true {
		t.Fatalf("saved: %d %v", code, out)
	}
	if n := countAudit(t, f, "setting.set"); n != 4 {
		t.Errorf("audited changes: %d", n)
	}
	// No reseller yet: the sample one. The owner reads English (the fixture's user).
	code, out = b.do(http.MethodPost, "/api/v1/reseller-messages/reminder/test", map[string]any{})
	if code != 200 || out["ok"] != true || len(bot.tests) != 1 || bot.tests[0].Text != "Please pay 1,250,000 Toman" || bot.tests[0].Settle == "" {
		t.Fatalf("test to me: %d %v %+v", code, out, bot.tests)
	}
	if n := countAudit(t, f, "reseller_reminder.test"); n != 1 {
		t.Errorf("audited tests: %d", n)
	}
}
