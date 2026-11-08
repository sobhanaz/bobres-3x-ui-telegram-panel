package notify

import (
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

var notDelivered = events.SystemAlertEvent{Key: "orders.provision_failed", Severity: events.SeverityWarning, State: events.AlertProblem,
	TitleKey: "alert.provision_failed", Args: map[string]string{"count": "1"}, Since: 1760000000, At: 1760000060}

// The alerts about paid orders not delivered (the watchdog's, its repeats, and the one per order after its third
// failed try) carry "Undelivered orders", which opens the bot's list, for the owners and admins, in their language;
// the watchdog's end of it carries none, and support get none.
func TestUndeliveredAlertsHaveTheButton(t *testing.T) {
	n, tgf, set := alertNotifier(t)
	set.SetStaff([]settings.Contact{
		{TelegramID: ownerTG, Language: "fa", Role: "owner"},
		{TelegramID: adminTG, Language: "en", Role: "admin"},
	})
	set.Replace(map[string]string{"notify.recipients": "staff"})
	check := func(what string) {
		t.Helper()
		got := tgf.take()
		var staff int
		for _, s := range got {
			if s.chat != ownerTG && s.chat != adminTG {
				continue // the buyer's own message
			}
			staff++
			if s.kb == nil || len(s.kb.InlineKeyboard) != 1 || len(s.kb.InlineKeyboard[0]) != 1 || s.kb.InlineKeyboard[0][0].CallbackData != "adm:ud" {
				t.Fatalf("%s, chat %d: keyboard %+v", what, s.chat, s.kb)
			}
			label := s.kb.InlineKeyboard[0][0].Text
			if (s.chat == ownerTG && label != "📦 سفارش\u200cهای تحویل\u200cنشده") || (s.chat == adminTG && label != "📦 Undelivered orders") {
				t.Fatalf("%s, chat %d: label %q", what, s.chat, label)
			}
		}
		if staff != 2 {
			t.Fatalf("%s: sent %+v", what, got)
		}
	}

	repeat := notDelivered
	repeat.Repeat = true
	for _, e := range []events.SystemAlertEvent{notDelivered, repeat} {
		if err := handleAlert(t, n, e); err != nil {
			t.Fatal(err)
		}
		check("the watchdog's alert")
	}
	failed := events.ProvisionFailedEvent{OrderID: "order-001", UserID: "user-1", TelegramID: 4242, Attempts: 3, Error: "panel down"}
	if err := handleEvent(t, n, events.ProvisionFailed, failed); err != nil {
		t.Fatal(err)
	}
	check("the order's alert")

	fixed := notDelivered
	fixed.State = events.AlertResolved
	if err := handleAlert(t, n, fixed); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 2 || got[0].kb != nil || got[1].kb != nil {
		t.Fatalf("fixed: %+v", got)
	}
	if kb := n.systemAlertKeyboard("en", settings.Contact{TelegramID: supportTG, Role: "support"}, notDelivered); kb != nil {
		t.Fatalf("support: %+v", kb)
	}
	if kb := n.undeliveredKeyboard("en", settings.Contact{TelegramID: supportTG, Role: "support"}); kb != nil {
		t.Fatalf("support, the order's alert: %+v", kb)
	}
}
