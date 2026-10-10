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
	Role       string // owner or admin
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

// Recipients lists who gets staff alerts: with notify.recipients = staff,
// every active owner and admin core listed at the last refresh; otherwise
// (or while that list is empty) the configured admin, adminTG (0: nobody).
func (s *Store) Recipients(adminTG int64) []Contact {
	staff := s.Staff()
	if s.Get("notify.recipients") == "staff" && len(staff) > 0 {
		return staff
	}
	if adminTG == 0 {
		return nil
	}
	for _, c := range staff {
		if c.TelegramID == adminTG {
			return []Contact{c}
		}
	}
	return []Contact{{TelegramID: adminTG}}
}
