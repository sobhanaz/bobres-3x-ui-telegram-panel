package config

import (
	"errors"
	"net/url"
)

// Bot is the bot service configuration.
type Bot struct {
	Common
	// ServiceToken is the bot's own token, presented to core.
	ServiceToken     string
	TelegramBotToken string
	CoreGRPCAddr     string
	// WebhookURL, when set, switches the bot from long polling to webhook mode;
	// WebhookSecret is then required and checked on every delivery.
	WebhookURL    string
	WebhookSecret string
	// AdminTelegramID receives operational alerts (provisioning failures).
	AdminTelegramID int64
	// TelegramAPIURL replaces https://api.telegram.org (a local Bot API server,
	// or the fake used by the CI end-to-end run). Empty = Telegram.
	TelegramAPIURL string
	// PeerCoreToken is core's service token: core fetches receipt photos
	// through the bot with it (/internal/files). Empty = no such endpoint.
	PeerCoreToken string
}

// LoadBot reads the bot service configuration, failing closed on missing secrets.
func LoadBot() (Bot, error) {
	var errs []error
	keep := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	c, err := Load("bot")
	keep(err)
	b := Bot{Common: c}
	b.ServiceToken, err = requireToken("BOBRES_SERVICE_TOKEN", c.Env)
	keep(err)
	b.TelegramBotToken, err = Secret("BOBRES_TELEGRAM_BOT_TOKEN")
	keep(err)
	keep(requireNonEmpty("BOBRES_TELEGRAM_BOT_TOKEN", b.TelegramBotToken))
	b.CoreGRPCAddr = getenv("BOBRES_CORE_GRPC_ADDR", "")
	keep(requireNonEmpty("BOBRES_CORE_GRPC_ADDR", b.CoreGRPCAddr))
	keep(requireNonEmpty("BOBRES_REDIS_URL", b.RedisURL))
	b.WebhookURL = getenv("BOBRES_TELEGRAM_WEBHOOK_URL", "")
	if b.WebhookURL != "" {
		b.WebhookSecret, err = requireToken("BOBRES_TELEGRAM_WEBHOOK_SECRET", c.Env)
		keep(err)
	}
	b.AdminTelegramID, err = telegramID("BOBRES_ADMIN_TELEGRAM_ID")
	keep(err)
	b.PeerCoreToken, err = optionalToken("BOBRES_PEER_CORE_TOKEN", c.Env)
	keep(err)
	keep(requireDistinct(map[string]string{"BOBRES_SERVICE_TOKEN": b.ServiceToken, "BOBRES_PEER_CORE_TOKEN": b.PeerCoreToken}))
	if b.TelegramAPIURL = getenv("BOBRES_TELEGRAM_API_URL", ""); b.TelegramAPIURL != "" {
		if u, err := url.Parse(b.TelegramAPIURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			keep(errors.New("BOBRES_TELEGRAM_API_URL must be an http(s) URL"))
		}
	}
	return b, errors.Join(errs...)
}
