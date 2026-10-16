package domain

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Settings of the branded subscription page (docs/15-subscription-page.md).
const (
	SettingSubPageEnabled = "branding.sub_page.enabled"
	SettingBotUsername    = "branding.bot_username"
)

// SubPageSettings is what the page needs from the settings: whether it is
// on, and the bot's username for the renew button.
type SubPageSettings struct {
	Enabled     bool
	BotUsername string
}

// SubPage reads the page's settings. The page is on unless switched off.
func (s *Service) SubPage(ctx context.Context) (SubPageSettings, error) {
	m, err := s.Settings(ctx)
	if err != nil {
		return SubPageSettings{}, err
	}
	return SubPageFrom(m), nil
}

// SubPageFrom reads the page's settings out of a settings map already loaded.
func SubPageFrom(m map[string]string) SubPageSettings {
	return SubPageSettings{
		Enabled:     m[SettingSubPageEnabled] != "false",
		BotUsername: m[SettingBotUsername],
	}
}

// RegenerateSubscriptionPage gives a service a new page token: the old page
// URL stops working at once, the sub link itself is not touched. It returns
// the new token. The change is audited without the token (the audit log is
// read by more people than the link should reach).
func (s *Service) RegenerateSubscriptionPage(ctx context.Context, actor *store.User, id, reason string) (string, error) {
	reason, err := needReason(reason)
	if err != nil {
		return "", err
	}
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), id)
	if err != nil {
		return "", err
	}
	if sub.Status == "deleted" {
		return "", stateErr("the service is deleted")
	}
	var token string
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		t, err := s.st.RegeneratePageToken(ctx, tx, sub.ID)
		if err != nil {
			return err
		}
		token = t
		return s.audit(ctx, tx, actor, "subscription.page_regenerate", "subscription", sub.ID,
			map[string]string{"client_email": sub.ClientEmail}, reason)
	})
	return token, err
}

// SubPageURL is the address of a service's page under the store's public
// base URL: "" when either is missing.
func SubPageURL(base, token string) string {
	base = strings.TrimRight(base, "/")
	if base == "" || token == "" {
		return ""
	}
	return base + "/sub/" + token
}
