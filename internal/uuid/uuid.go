// Package uuid issues UUIDv7 identifiers: time-ordered, so index locality is
// better than v4 for append-heavy tables (orders, ledger_entries).
package uuid

import "github.com/google/uuid"

// UUID re-exports the upstream type so callers never import two uuid pkgs.
type UUID = uuid.UUID

// Parse re-exports uuid.Parse.
func Parse(s string) (UUID, error) { return uuid.Parse(s) }

// NewV7 returns a random time-ordered UUIDv7.
func NewV7() (UUID, error) { return uuid.NewV7() }

// MustV7 returns NewV7 and panics on error (entropy failure only).
func MustV7() UUID {
	id, err := NewV7()
	if err != nil {
		panic(err)
	}
	return id
}
