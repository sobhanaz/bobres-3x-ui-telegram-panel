package domain

import (
	"context"
	"crypto/subtle"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A customer finds a service by its link: the subscription link (".../sub/<subscription id>") or the branded page's address
// (".../sub/<token>") that they were given. Whoever holds the link holds the service (that is what the link is), so with it they may see
// the service's state and renew it, paying from their own account. It is the link's secret that is checked, again at every order: a
// guessed id finds nothing, and a renewal of a service that is not the buyer's needs it.

// LinkSecret reads the secret out of what a customer sent: a whole link (a query or a fragment after it is ignored, a closing "/"
// too), the end of one, or the secret itself. ErrInvalid when it is none of them.
func LinkSecret(input string) (string, error) {
	if secret, ok := store.ParseLinkSecret(input); ok {
		return secret, nil
	}
	return "", invalid("not a subscription link")
}

// FindServiceByLink finds the service a link belongs to. It returns the service and the secret that proves the link (what a renewal of
// it carries); store.ErrNotFound when no service has it.
func (s *Service) FindServiceByLink(ctx context.Context, userID, input string) (*store.Subscription, string, error) {
	if _, err := s.activeUser(ctx, nil, userID); err != nil {
		return nil, "", err
	}
	secret, err := LinkSecret(input)
	if err != nil {
		return nil, "", err
	}
	sub, err := s.st.SubscriptionByLinkSecret(ctx, s.st.Conn(), secret)
	if err != nil {
		return nil, "", err
	}
	if sub.Status == "deleted" {
		return nil, "", store.ErrNotFound
	}
	return sub, secret, nil
}

// holdsLink reports whether secret is the secret of the service's link: its subscription id or its page token.
func holdsLink(sub *store.Subscription, secret string) bool {
	if secret == "" {
		return false
	}
	eq := func(a string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(secret)) == 1 }
	return (sub.SubID != nil && eq(*sub.SubID)) || eq(sub.PageToken)
}
