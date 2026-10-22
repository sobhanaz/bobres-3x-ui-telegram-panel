package domain

import (
	"context"
	"crypto/subtle"
	"net/url"
	"regexp"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A customer finds a service by its link: the subscription link (".../sub/<subscription id>") or the branded page's address
// (".../sub/<token>") that they were given. Whoever holds the link holds the service (that is what the link is), so with it they may see
// the service's state and renew it, paying from their own account. It is the link's secret that is checked, again at every order: a
// guessed id finds nothing, and a renewal of a service that is not the buyer's needs it.

// linkSecretRe is what the end of a link looks like: letters, digits, "." "_" "-", 8 to 64 of them.
var linkSecretRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// LinkSecret reads the secret out of what a customer sent: a whole link (a query or a fragment after it is ignored, a closing "/"
// too), the end of one, or the secret itself. ErrInvalid when it is none of them.
func LinkSecret(input string) (string, error) {
	s := strings.TrimSpace(input)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", invalid("not a subscription link")
		}
		s = u.Path
	} else if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if !linkSecretRe.MatchString(s) {
		return "", invalid("not a subscription link")
	}
	return s, nil
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
