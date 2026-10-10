package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/runner"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

func TestStaffAlertsFollowTheSettings(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	adm := w.person(841, "ali")
	adm.text("/start")
	adm.press("lang:fa")
	w.role(841, "admin")
	u := w.customer(842, "cust")
	ticket := func(p *person, text string) {
		p.text("/menu")
		p.press("support")
		p.text(text)
	}

	// By default only the configured owner is alerted.
	ticket(u, "first question")
	owner.sees("Support message from @cust")
	if adm.count("@cust") != 0 {
		t.Fatal("an admin was alerted with notify.recipients = owner")
	}

	// staff: every owner and admin, each in their language, but not the one
	// who wrote.
	owner.text("/set notify.recipients staff")
	ticket(u, "second question")
	adm.sees("پیام پشتیبانی از @cust")
	ticket(adm, "a question from the admin")
	owner.sees("a question from the admin")
	if adm.count("a question from the admin") != 0 {
		t.Fatal("the admin was alerted about their own ticket")
	}

	owner.text("/set notify.new_ticket false")
	ticket(u, "third question")
	if owner.count("third question") != 0 || adm.count("third question") != 0 {
		t.Fatal("ticket alert sent while switched off")
	}

	owner.text("/set notify.new_payment false")
	u.text("/menu")
	u.press("buy")
	u.press("plan:")
	u.press("pay:c:")
	u.photo("receipt-quiet", "REF-1")
	u.lastHas("Received")
	if owner.count("New payment to review") != 0 || adm.count("پرداخت جدید برای بررسی") != 0 {
		t.Fatal("payment alert sent while switched off")
	}

	// Sales (off by default): one alert per paid order, in each language.
	owner.text("/set notify.sales true")
	w.fund(842, 150_000)
	u.text("/menu")
	u.press("buy")
	u.press("plan:")
	u.press("pay:w:")
	owner.eventuallySees("New sale: @cust bought <b>Monthly 50GB</b> for <b>150,000 Toman</b>")
	adm.eventuallySees("فروش جدید: @cust پلن <b>ماهانه ۵۰ گیگ</b> را به مبلغ <b>۱۵۰٬۰۰۰ تومان</b> خرید.")

	// Provisioning failures: on by default, can be switched off; the customer
	// is told either way.
	n := notify.New(w.stack.Core, w.tg, w.h.cat, w.h.Settings(), ownerID, nil)
	failed := func() {
		t.Helper()
		payload, _ := json.Marshal(events.ProvisionFailedEvent{OrderID: "order-1", TelegramID: 842, Attempts: 5, Error: "panel <down>"})
		if err := n.Handle(context.Background(), eventbus.Message{Topic: events.ProvisionFailed, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	failed()
	owner.sees("Provisioning failed 5 times for order <code>order-1</code>")
	owner.sees("panel &lt;down&gt;")
	adm.sees("ساخت سرویس برای سفارش <code>order-1</code>")
	owner.text("/set notify.provision_failed false")
	failed()
	if owner.count("Provisioning failed") != 1 || u.count("taking longer than usual") != 2 {
		t.Fatalf("switched off: %d admin alerts, %d customer messages", owner.count("Provisioning failed"), u.count("taking longer than usual"))
	}
}

func TestTermsAndPrivacyLinks(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(851, "tess")
	u.text("/terms")
	u.lastHas("No terms of service or privacy policy")

	owner.text("/set branding.terms_url https://example.com/terms")
	u.text("/terms")
	u.lastHas("Terms and privacy")
	if l := u.link("Terms of service"); l != "https://example.com/terms" {
		t.Fatalf("terms link: %q", l)
	}
	if strings.Contains(strings.Join(u.last().labels(), "|"), "Privacy policy") {
		t.Fatal("a privacy button without a privacy link")
	}

	owner.text("/set branding.privacy_url https://example.com/privacy")
	u.text("/menu")
	u.press("support")
	labels := strings.Join(u.last().labels(), "|")
	if !strings.Contains(labels, "Terms of service") || !strings.Contains(labels, "Privacy policy") || !strings.Contains(labels, "Cancel") {
		t.Fatalf("support screen buttons: %s", labels)
	}
	if l := u.link("Privacy policy"); l != "https://example.com/privacy" {
		t.Fatalf("privacy link: %q", l)
	}
}

func TestDashboardForAllStaff(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	sup := w.customer(861, "sara")
	w.role(861, "support")

	sup.text("/menu")
	for _, row := range sup.last().kb.InlineKeyboard {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "adm") {
				t.Fatal("support was shown the admin panel")
			}
		}
	}
	sup.press("dash")
	// The test core has no public address: the bot asked for a link.
	sup.lastHas("The dashboard has no address yet")
	sup.text("/dashboard")
	if sup.count("The dashboard has no address yet") != 2 {
		t.Fatal("/dashboard did not ask for a link")
	}
	sup.text("/admin")
	sup.lastHas("for admins only")

	owner.text("/menu")
	if labels := strings.Join(owner.last().labels(), "|"); !strings.Contains(labels, "Admin panel") || !strings.Contains(labels, "Dashboard") {
		t.Fatalf("owner's menu: %s", labels)
	}

	c := w.customer(862, "carl")
	c.text("/dashboard")
	c.lastHas("for admins only")
	if strings.Contains(strings.Join(c.last().labels(), "|"), "Dashboard") {
		t.Fatal("a customer was offered the dashboard")
	}
}

func TestSetShowsCoresReason(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set payments.usdt_trc20 TXYZ1234567890")
	owner.lastHas("Usage: <code>/set key value</code>\npayments.usdt_trc20: not a TRC20 address")
	if strings.Contains(owner.last().text, "maintenance.enabled") {
		t.Fatal("the key list shown for a known key")
	}
	owner.text("/set limits.bot_per_10s 1000")
	owner.lastHas("a whole number from 5 to 200")
	owner.text("/set texts.en.welcome Hi {brand} <")
	owner.lastHas("bad_entity (&lt;)") // core's reason, escaped
	owner.text("/set no.such_key 1")
	owner.lastHas("unknown setting")
	owner.lastHas("maintenance.enabled") // the keys, for a mistyped one

	adm := w.customer(871, "adam")
	w.role(871, "admin")
	adm.text("/set payments.card_number 6037991122223333")
	adm.lastHas("only the store owner can change payment details")
	adm.text("/set maintenance.enabled on")
	adm.lastHas("Saved")
}

// Every key on the bot's settings screen is a setting core takes.
func TestSettingsScreenKeysAreCoreSettings(t *testing.T) {
	for _, k := range settingKeys {
		if _, ok := coredomain.SettingKeys[k]; ok {
			continue
		}
		if strings.HasPrefix(k, "texts.") && coredomain.SettingPerm(k) != "" {
			continue // a bot text, checked by the catalog
		}
		t.Errorf("%s is on the bot's settings screen but core has no such setting", k)
	}
}

// syncBuffer is a log destination tests can read while the bot writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Core asks the bot to reload after a dashboard change: the settings, the
// time zone, the currency names and the staff list apply at once, and an
// override that would break messages is left out and logged.
func TestRefreshEndpointAppliesSettings(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	var logs syncBuffer
	w.h.log = slog.New(slog.NewTextHandler(&logs, nil))
	t.Cleanup(func() {
		i18n.SetLocation(defaultLocation())
		i18n.SetCurrencyNames(nil)
	})
	srv := httptest.NewServer(runner.Refresh(w.h.RefreshSettings, testenv.CoreToken, nil))
	defer srv.Close()
	refresh := func(token string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	w.setInCore("maintenance.enabled", "true")
	w.setInCore("general.timezone", "Europe/Berlin")
	w.setInCore("branding.currency.en", "Tomans")
	// Written behind core's back (core refuses it): an unclosed tag.
	if err := w.stack.Store.SetSetting(context.Background(), w.stack.Store.Conn(), "texts.en.welcome", json.RawMessage(`"Hi <b>{brand}"`)); err != nil {
		t.Fatal(err)
	}
	if w.h.set.Bool("maintenance.enabled", false) {
		t.Fatal("the bot knew before the refresh")
	}
	if code := refresh(""); code != http.StatusUnauthorized {
		t.Fatalf("refresh without core's token: %d", code)
	}
	if code := refresh(testenv.CoreToken); code != http.StatusNoContent {
		t.Fatalf("refresh: %d", code)
	}
	if !w.h.set.Bool("maintenance.enabled", false) {
		t.Fatal("maintenance not applied")
	}
	if loc := i18n.CurrentLocation().String(); loc != "Europe/Berlin" {
		t.Fatalf("time zone: %s", loc)
	}
	if got := i18n.Money("en", 150000, "IRT"); got != "150,000 Tomans" {
		t.Fatalf("currency name: %q", got)
	}
	if got := w.h.cat.T("en", "welcome", "brand", "X"); strings.Contains(got, "Hi <b>") {
		t.Fatalf("a broken override was used: %q", got)
	}
	if !strings.Contains(logs.String(), "texts.en.welcome") || strings.Contains(logs.String(), "Hi <b>") {
		t.Fatalf("the dropped override must be logged by key only: %s", logs.String())
	}
	if staff := w.h.Settings().Staff(); len(staff) != 1 || staff[0].TelegramID != ownerID || staff[0].Language != "en" {
		t.Fatalf("staff contacts: %+v", staff)
	}

	// Cleared: back to the configured zone and the built-in names.
	w.setInCore("general.timezone", "")
	w.setInCore("branding.currency.en", "")
	if code := refresh(testenv.CoreToken); code != http.StatusNoContent {
		t.Fatalf("refresh: %d", code)
	}
	if loc := i18n.CurrentLocation().String(); loc != "Asia/Tehran" {
		t.Fatalf("time zone after clearing: %s", loc)
	}
	if got := i18n.Money("en", 1, "IRT"); got != "1 Toman" {
		t.Fatalf("currency name after clearing: %q", got)
	}
}

// A paid order's confirmation goes to the customer first: when it fails the
// event is retried without announcing the sale again, and a failed sale
// alert does not hold the customer's message back.
func TestSaleAlertAfterTheCustomer(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set notify.sales true")
	owner.sees("Saved")
	u := w.customer(851, "paid")
	n := notify.New(w.stack.Core, w.tg, w.h.cat, w.h.Settings(), ownerID, nil)
	paid := func() error {
		payload, _ := json.Marshal(events.OrderPaidEvent{OrderID: "order-9", TelegramID: 851, PlanID: "plan-x",
			Amount: 150_000, Currency: "IRT", Source: events.PaidManually})
		return n.Handle(context.Background(), eventbus.Message{Topic: events.OrderPaid, Payload: payload})
	}

	w.tg.setDown(851, true)
	if err := paid(); err == nil {
		t.Fatal("a failed confirmation was not retried")
	}
	if owner.count("New sale") != 0 {
		t.Fatal("the sale was announced before the customer was told")
	}
	w.tg.setDown(851, false)
	w.tg.setDown(ownerID, true)
	if err := paid(); err != nil {
		t.Fatalf("a failed sale alert held the event: %v", err)
	}
	u.lastHas("Payment confirmed")
	w.tg.setDown(ownerID, false)
}
