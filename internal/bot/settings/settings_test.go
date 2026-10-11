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
	if got := s.Recipients(0); got != nil {
		t.Fatalf("no admin configured: %v", got)
	}
	if got := s.Recipients(9); !slices.Equal(got, []Contact{{TelegramID: 9}}) {
		t.Fatalf("owner only: %v", got)
	}
	staff := []Contact{{TelegramID: 9, Language: "en", Role: "owner"}, {TelegramID: 5, Language: "fa", Role: "admin"}}
	s.SetStaff(staff)
	staff[0].Language = "xx" // the store keeps its own copy
	if got := s.Recipients(9); !slices.Equal(got, []Contact{{TelegramID: 9, Language: "en", Role: "owner"}}) {
		t.Fatalf("owner with a known language: %v", got)
	}
	s.Replace(map[string]string{"notify.recipients": "staff"})
	if got := s.Recipients(9); len(got) != 2 || got[1].TelegramID != 5 {
		t.Fatalf("staff: %v", got)
	}
	s.SetStaff(nil)
	if got := s.Recipients(9); len(got) != 1 || got[0].TelegramID != 9 {
		t.Fatalf("staff unknown yet: the configured admin still gets alerts: %v", got)
	}
}
