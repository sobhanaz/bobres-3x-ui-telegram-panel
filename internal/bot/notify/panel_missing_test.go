package notify

import (
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

var panelMissing = events.SystemAlertEvent{Key: "subscriptions.panel_missing", Severity: events.SeverityWarning, State: events.AlertProblem,
	TitleKey: "alert.panel_missing", Args: map[string]string{"count": "8"}, Detail: "a@x, b@x", Since: 1760000000, At: 1760000060}

// The notice about services not on the panel, and its daily repeats, carry "Manage these services" for the
// owners and admins who get it, in their language; its end and every other alert carry no button.
func TestPanelMissingAlertHasTheButton(t *testing.T) {
	n, tgf, set := alertNotifier(t)
	set.SetStaff([]settings.Contact{
		{TelegramID: ownerTG, Language: "fa", Role: "owner"},
		{TelegramID: adminTG, Language: "en", Role: "admin"},
		{TelegramID: supportTG, Language: "en", Role: "support"},
	})
	set.Replace(map[string]string{"notify.recipients": "staff"})

	repeat := panelMissing
	repeat.Repeat = true
	for _, e := range []events.SystemAlertEvent{panelMissing, repeat} {
		if err := handleAlert(t, n, e); err != nil {
			t.Fatal(err)
		}
		got := tgf.take()
		if len(got) != 2 {
			t.Fatalf("sent: %+v", got)
		}
		for _, s := range got {
			if s.kb == nil || len(s.kb.InlineKeyboard) != 1 || len(s.kb.InlineKeyboard[0]) != 1 || s.kb.InlineKeyboard[0][0].CallbackData != "adm:pm" {
				t.Fatalf("chat %d: keyboard %+v", s.chat, s.kb)
			}
			label := s.kb.InlineKeyboard[0][0].Text
			if (s.chat == ownerTG && label != "🛠 مدیریت این سرویس\u200cها") || (s.chat == adminTG && label != "🛠 Manage these services") {
				t.Fatalf("chat %d: label %q", s.chat, label)
			}
		}
		if !strings.Contains(got[1].text, "Services whose client is gone from the panel: 8") || !strings.Contains(got[1].text, "Tap the button below") {
			t.Fatalf("text: %q", got[1].text)
		}
	}

	// The configured owner core has not listed yet gets it too (core checks the role on the tap).
	set.Replace(nil)
	set.SetStaff(nil)
	if err := handleAlert(t, n, panelMissing); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || got[0].kb == nil {
		t.Fatalf("the configured owner: %+v", got)
	}

	// Its end, and another alert: no button.
	fixed := panelMissing
	fixed.State = events.AlertResolved
	for _, e := range []events.SystemAlertEvent{fixed, botDown} {
		if err := handleAlert(t, n, e); err != nil {
			t.Fatal(err)
		}
		if got := tgf.take(); len(got) != 1 || got[0].kb != nil {
			t.Fatalf("%s %s: %+v", e.Key, e.State, got)
		}
	}
	// Support never gets staff alerts, and would get no button.
	if kb := n.systemAlertKeyboard("en", settings.Contact{TelegramID: supportTG, Role: "support"}, panelMissing); kb != nil {
		t.Fatalf("support: %+v", kb)
	}
}
