package settings

import (
	"slices"
	"testing"
)

func TestValuesAndDefaults(t *testing.T) {
	s := New()
	if s.Get("branding.name") != "" || !s.Bool("notify.new_payment", true) || s.Int("limits.bot_per_10s", 20, 5, 200) != 20 {
		t.Fatal("an empty store must give the defaults")
	}
	s.Replace(map[string]string{
		"notify.new_payment": "false", "maintenance.enabled": "true", "notify.sales": "maybe",
		"limits.bot_per_10s": "۵۰", "limits.tickets_per_day": "1000", "limits.other": "-3", "x": "abc",
	})
	if s.Bool("notify.new_payment", true) || !s.Bool("maintenance.enabled", false) || s.Bool("notify.sales", false) {
		t.Fatal("bool settings")
	}
	for _, c := range []struct {
		key         string
		def, lo, hi int64
		want        int64
		why         string
	}{
		{"limits.bot_per_10s", 20, 5, 200, 50, "Persian digits"},
		{"limits.tickets_per_day", 10, 0, 100, 100, "clamped down"},
		{"limits.other", 20, 5, 200, 5, "clamped up"},
		{"x", 7, 0, 10, 7, "unreadable"},
	} {
		if got := s.Int(c.key, c.def, c.lo, c.hi); got != c.want {
			t.Errorf("%s (%s): %d, want %d", c.key, c.why, got, c.want)
		}
	}
}

func TestRecipients(t *testing.T) {
	s := New()
	if got := s.Recipients(nil); got != nil {
		t.Fatalf("no admin configured: %v", got)
	}
	if got := s.Recipients([]int64{9}); !slices.Equal(got, []Contact{{TelegramID: 9}}) {
		t.Fatalf("owner only: %v", got)
	}
	staff := []Contact{{TelegramID: 9, Language: "en", Role: "owner"}, {TelegramID: 5, Language: "fa", Role: "admin"}}
	s.SetStaff(staff)
	staff[0].Language = "xx" // the store keeps its own copy
	if got := s.Recipients([]int64{9}); !slices.Equal(got, []Contact{{TelegramID: 9, Language: "en", Role: "owner"}}) {
		t.Fatalf("owner with a known language: %v", got)
	}
	s.Replace(map[string]string{"notify.recipients": "staff"})
	if got := s.Recipients([]int64{9}); len(got) != 2 || got[1].TelegramID != 5 {
		t.Fatalf("staff: %v", got)
	}
	s.SetStaff(nil)
	if got := s.Recipients([]int64{9}); len(got) != 1 || got[0].TelegramID != 9 {
		t.Fatalf("staff unknown yet: the configured admin still gets alerts: %v", got)
	}
}

// Support staff get ticket alerts (with notify.recipients = staff), never
// payment ones.
func TestTicketRecipients(t *testing.T) {
	s := New()
	s.SetStaff([]Contact{{TelegramID: 9, Role: "owner"}, {TelegramID: 5, Role: "admin"}, {TelegramID: 3, Language: "fa", Role: "support"}})
	if got := s.TicketRecipients([]int64{9}); len(got) != 1 || got[0].TelegramID != 9 {
		t.Fatalf("owner only: %v", got)
	}
	s.Replace(map[string]string{"notify.recipients": "staff"})
	if got := s.TicketRecipients([]int64{9}); len(got) != 3 || got[2].TelegramID != 3 {
		t.Fatalf("tickets with staff: %v", got)
	}
	if got := s.Recipients([]int64{9}); len(got) != 2 || slices.ContainsFunc(got, func(c Contact) bool { return c.Role == "support" }) {
		t.Fatalf("payments with staff: %v", got)
	}
	if c, ok := s.Contact(3); !ok || c.Language != "fa" {
		t.Fatalf("contact: %+v %v", c, ok)
	}
	if _, ok := s.Contact(4); ok {
		t.Fatal("not staff")
	}
	s.SetStaff([]Contact{{TelegramID: 3, Role: "support"}})
	if got := s.Recipients([]int64{9}); len(got) != 1 || got[0].TelegramID != 9 {
		t.Fatalf("only support listed: payment alerts go to the configured admin: %v", got)
	}
}

// Several root admins: all of them get the alerts, in the order the server
// lists them, each with what core knows of them, until notify.recipients
// widens the list to the staff.
func TestRecipientsOfSeveralOwners(t *testing.T) {
	s := New()
	got := s.Recipients([]int64{9, 0, 7, 8})
	if !slices.Equal(got, []Contact{{TelegramID: 9}, {TelegramID: 7}, {TelegramID: 8}}) {
		t.Fatalf("every configured owner, in order, ignoring a zero: %v", got)
	}
	s.SetStaff([]Contact{{TelegramID: 8, Language: "fa", Role: "owner"}, {TelegramID: 9, Language: "en", Role: "owner"}, {TelegramID: 5, Role: "admin"}})
	got = s.Recipients([]int64{9, 7, 8})
	if len(got) != 3 || got[0].TelegramID != 9 || got[0].Language != "en" || got[1].TelegramID != 7 || got[1].Language != "" ||
		got[2].TelegramID != 8 || got[2].Language != "fa" {
		t.Fatalf("owners with what core knows: %v", got)
	}
	if got := s.TicketRecipients([]int64{9, 7}); len(got) != 2 {
		t.Fatalf("tickets go to every owner too: %v", got)
	}
	s.Replace(map[string]string{"notify.recipients": "staff"})
	if got := s.Recipients([]int64{9, 7, 8}); len(got) != 3 {
		t.Fatalf("staff mode lists the staff core knows (two owners, one admin): %v", got)
	}
	s.SetStaff(nil)
	if got := s.Recipients([]int64{9, 7}); len(got) != 2 {
		t.Fatalf("no staff known yet: the owners still get alerts: %v", got)
	}
}
