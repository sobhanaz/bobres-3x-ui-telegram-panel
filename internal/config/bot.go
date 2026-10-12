package config

import (
	"errors"
	"net/url"
	"strings"
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
	// LicenseServer is the vendor's license server (BOBRES_LICENSE_SERVER,
	// https only, no trailing slash): core activates and renews its license
	// through the bot (/internal/license/*), which keeps nothing. Empty =
	// those answer 503.
	LicenseServer string
	// PeerCoreToken is core's service token: core fetches receipt photos
	// through the bot with it (/internal/files). Empty = no such endpoint.
	PeerCoreToken string
	// PeerBackupToken is the backup service's token: it hands the bot each
	// encrypted backup for the owner (/internal/backups/deliver). Empty = no
	// such endpoint.
	PeerBackupToken string

	// UpdateChannel is the release channel the update check follows
	// (BOBRES_UPDATE_CHANNEL): stable (the default) or beta.
	UpdateChannel string
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
	b.PeerBackupToken, err = optionalToken("BOBRES_PEER_BACKUP_TOKEN", c.Env)
	keep(err)
	keep(requireDistinct(map[string]string{"BOBRES_SERVICE_TOKEN": b.ServiceToken, "BOBRES_PEER_CORE_TOKEN": b.PeerCoreToken,
		"BOBRES_PEER_BACKUP_TOKEN": b.PeerBackupToken}))
	if b.TelegramAPIURL = getenv("BOBRES_TELEGRAM_API_URL", ""); b.TelegramAPIURL != "" {
		if u, err := url.Parse(b.TelegramAPIURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			keep(errors.New("BOBRES_TELEGRAM_API_URL must be an http(s) URL"))
		}
	}
	b.LicenseServer, err = LicenseServerURL(getenv("BOBRES_LICENSE_SERVER", ""))
	keep(err)

	b.UpdateChannel, err = updateChannel()
	keep(err)

	return b, errors.Join(errs...)
}

// updateChannel reads BOBRES_UPDATE_CHANNEL: stable when unset.
func updateChannel() (string, error) {
	switch c := strings.ToLower(strings.TrimSpace(getenv("BOBRES_UPDATE_CHANNEL", ""))); c {
	case "", "stable":
		return "stable", nil
	case "beta":
		return c, nil
	}
	return "", errors.New("BOBRES_UPDATE_CHANNEL must be stable or beta")
}
