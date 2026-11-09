package notify

import (
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

const ruTG = 400

// A service waiting for its first connection (FirstUseDays, no end date) is delivered, renewed and topped up with the
// days it will run from its first connection where the end goes, in the customer's language (Persian digits, Russian
// plural forms); a service that does not wait keeps its date or "never".
func TestWaitingServiceMessagesSayTheDaysStartAtTheFirstConnection(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tgf := &fakeTG{}
	core := &linksCore{fakeCore{langs: map[int64]string{ownerTG: "fa", adminTG: "en", ruTG: "ru"}}}
	n := New(core, tgf, cat, settings.New(), []int64{ownerTG}, nil)
	one := func(what string) string {
		t.Helper()
		got := tgf.take()
		if len(got) != 1 {
			t.Fatalf("%s: %d messages: %+v", what, len(got), got)
		}
		return got[0].text
	}

	prov := events.SubscriptionProvisionedEvent{SubscriptionID: "sub-1", OrderID: "order-1", UserID: "u1", TelegramID: adminTG,
		SubscriptionLink: "https://sub.example.com/sub/x", TrafficBytes: 50 << 30, FirstUseDays: 30}
	if err := handleEvent(t, n, events.SubscriptionProvisioned, prov); err != nil {
		t.Fatal(err)
	}
	if got := one("delivery"); !strings.HasSuffix(got, "\n\nExpires: 30 days from the first connection\nTraffic: 50 GB") {
		t.Fatalf("the delivery: %q", got)
	}
	prov.TelegramID = ownerTG
	if err := handleEvent(t, n, events.SubscriptionProvisioned, prov); err != nil {
		t.Fatal(err)
	}
	if got := one("Persian delivery"); !strings.Contains(got, "\nتاریخ انقضا: ۳۰ روز از اولین اتصال\n") {
		t.Fatalf("the delivery in Persian: %q", got)
	}
	prov.TelegramID, prov.FirstUseDays = ruTG, 21
	if err := handleEvent(t, n, events.SubscriptionProvisioned, prov); err != nil {
		t.Fatal(err)
	}
	if got := one("Russian delivery"); !strings.Contains(got, "\nИстекает: 21 день с первого подключения\n") {
		t.Fatalf("the delivery in Russian: %q", got)
	}
	prov.TelegramID, prov.FirstUseDays = adminTG, 0 // a service with no days at all
	if err := handleEvent(t, n, events.SubscriptionProvisioned, prov); err != nil {
		t.Fatal(err)
	}
	if got := one("no end"); !strings.Contains(got, "\nExpires: never\n") {
		t.Fatalf("a service with no end: %q", got)
	}

	// A renewal adds to the wait; a traffic package keeps it.
	ext := events.SubscriptionExtendedEvent{SubscriptionID: "00000000-0000-0000-0000-00000000abcd", OrderID: "o2", UserID: "u1", TelegramID: adminTG,
		Type: "renew", TrafficTotalBytes: 100 << 30, FirstUseDays: 60}
	if err := handleEvent(t, n, events.SubscriptionExtended, ext); err != nil {
		t.Fatal(err)
	}
	if got := one("renewal"); got != "✅ <b>Service #00abcd renewed.</b>\nExpires: 60 days from the first connection\nTraffic: 100 GB" {
		t.Fatalf("the renewal: %q", got)
	}
	ext.Type, ext.TelegramID = "traffic_topup", ownerTG
	if err := handleEvent(t, n, events.SubscriptionExtended, ext); err != nil {
		t.Fatal(err)
	}
	if got := one("Persian traffic package"); !strings.HasSuffix(got, "\nانقضا: ۶۰ روز از اولین اتصال") {
		t.Fatalf("the traffic package in Persian: %q", got)
	}
	// Once it connected the panel set its end: the date, as before.
	ext.Type, ext.TelegramID, ext.FirstUseDays, ext.ExpiresAt = "renew", adminTG, 0, 1_800_000_000
	if err := handleEvent(t, n, events.SubscriptionExtended, ext); err != nil {
		t.Fatal(err)
	}
	if got := one("dated renewal"); strings.Contains(got, "first connection") || strings.Contains(got, "never") {
		t.Fatalf("a renewal of a service that runs: %q", got)
	}
}
