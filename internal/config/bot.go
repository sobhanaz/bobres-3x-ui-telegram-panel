package config

import "errors"

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
	return b, errors.Join(errs...)
}
