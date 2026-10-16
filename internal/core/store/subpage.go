package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// pageTokenBytes is the entropy of a page token: 128 bits.
const pageTokenBytes = 16

// NewPageToken makes the secret of a service's branded page URL: 128 bits
// from the system CSPRNG as 32 lowercase hex characters. It panics when the
// system has no randomness, like uuid generation does: a token must never be
// weak.
func NewPageToken() string {
	b := make([]byte, pageTokenBytes)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("page token: %w", err))
	}
	return hex.EncodeToString(b)
}

// ValidPageToken reports whether s has the shape of a page token. A request
// whose token does not is answered like an unknown one, without a query.
func ValidPageToken(s string) bool {
	if len(s) != pageTokenBytes*2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// SubscriptionByPageToken returns the service a page token belongs to
// (ErrNotFound when none does).
func (s *Store) SubscriptionByPageToken(ctx context.Context, q querier, token string) (*Subscription, error) {
	sc, err := scanSubscription(q.QueryRow(ctx, `SELECT `+subscriptionCols+` FROM core.subscriptions WHERE page_token = $1`, token))
	if err != nil {
		return nil, fmt.Errorf("subscription by page token: %w", err)
	}
	return sc, nil
}

// RegeneratePageToken replaces a service's page token with a new one and
// returns it; the old URL stops working at once.
func (s *Store) RegeneratePageToken(ctx context.Context, q querier, id string) (string, error) {
	token := NewPageToken()
	tag, err := q.Exec(ctx, `UPDATE core.subscriptions SET page_token = $2, updated_at = now() WHERE id = $1`, id, token)
	if err != nil {
		return "", fmt.Errorf("regenerate page token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return token, nil
}
