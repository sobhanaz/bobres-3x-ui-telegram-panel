// Package settings holds the store settings the bot last read from core, and
// the staff who get alerts. The handler refreshes them; the handler and the
// notifier read them from any goroutine.
package settings

import (
	"slices"
	"sync"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
)

// Contact is a staff member who gets alerts. Language is "" when unknown.
type Contact struct {
	TelegramID int64
	Language   string
	Role       string // owner, admin or support
}

// Store is safe for concurrent use. The zero value is not usable; use New.
type Store struct {
	mu     sync.RWMutex
	values map[string]string
	staff  []Contact
}

// New returns an empty Store: every setting has its default.
func New() *Store { return &Store{values: map[string]string{}} }

// Replace swaps in the settings read from core (key -> value).
func (s *Store) Replace(values map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = values
}

// Get returns a setting's value ("" when unset, which means its default).
func (s *Store) Get(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[key]
}

// Bool reads a true/false setting (core stores "true" or "false"); def when
// unset or unreadable.
func (s *Store) Bool(key string, def bool) bool {
	switch s.Get(key) {
	case "true":
		return true
	case "false":
		return false
	}
	return def
}

// Int reads a whole-number setting, clamped to lo..hi; def when unset or
// unreadable.
func (s *Store) Int(key string, def, lo, hi int64) int64 {
	n, ok := i18n.ParseNumber(s.Get(key))
	if !ok {
		return def
	}
	return min(max(n, lo), hi)
}

// SetStaff replaces the cached staff list (core's ListStaffContacts).
func (s *Store) SetStaff(staff []Contact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.staff = slices.Clone(staff)
}

// Staff returns the cached staff list.
func (s *Store) Staff() []Contact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.staff)
}

// Recipients lists who gets staff alerts about payments, sales and failed
// deliveries: with notify.recipients = staff, every active owner and admin
// core listed at the last refresh (never support); otherwise (or while that
// list is empty) every configured owner, adminIDs (none: nobody), in order.
func (s *Store) Recipients(adminIDs []int64) []Contact {
	return s.recipients(adminIDs, false)
}

// TicketRecipients lists who gets support ticket alerts: as Recipients, but
// with notify.recipients = staff support staff too.
func (s *Store) TicketRecipients(adminIDs []int64) []Contact {
	return s.recipients(adminIDs, true)
}

func (s *Store) recipients(adminIDs []int64, support bool) []Contact {
	staff := s.Staff()
	if s.Get("notify.recipients") == "staff" {
		var out []Contact
		for _, c := range staff {
			if c.Role != "support" || support {
				out = append(out, c)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	var out []Contact
	for _, id := range adminIDs {
		if id == 0 {
			continue
		}
		c := Contact{TelegramID: id}
		for _, sc := range staff {
			if sc.TelegramID == id {
				c = sc
				break
			}
		}
		out = append(out, c)
	}
	return out
}

// Contact is the cached staff member with this Telegram id (ok=false: not
// staff, or not known yet).
func (s *Store) Contact(telegramID int64) (Contact, bool) {
	for _, c := range s.Staff() {
		if c.TelegramID == telegramID {
			return c, true
		}
	}
	return Contact{}, false
}
