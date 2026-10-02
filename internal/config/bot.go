package config

import (
	"errors"
	"fmt"
)

// Bot is the bot service configuration.
type Bot struct {
	Common
	TelegramBotToken string
	CoreGRPCAddr     string
	// WebhookURL, when set, switches the bot from long polling to webhook mode.
	WebhookURL string
	// AdminTelegramID seeds owner rights in the bot UI (coincides with installer input).
	AdminTelegramID int64
}

// LoadBot reads the bot service configuration, failing closed on missing secrets.
func LoadBot() (Bot, error) {
	var errs []error
	c, err := Load("bot")
	if err != nil {
		errs = append(errs, err)
	}
	b := Bot{Common: c}
	if v, err := Secret("BOBRES_TELEGRAM_BOT_TOKEN"); err != nil || v == "" {
		errs = append(errs, err)
		errs = append(errs, errors.New("BOBRES_TELEGRAM_BOT_TOKEN is required"))
	} else {
		b.TelegramBotToken = v
	}
	b.CoreGRPCAddr = getenv("BOBRES_CORE_GRPC_ADDR", "")
	if b.CoreGRPCAddr == "" {
		errs = append(errs, errors.New("BOBRES_CORE_GRPC_ADDR is required"))
	}
	b.WebhookURL = getenv("BOBRES_TELEGRAM_WEBHOOK_URL", "")
	if _, err := fmt.Sscanf(getenv("BOBRES_ADMIN_TELEGRAM_ID", "0"), "%d", &b.AdminTelegramID); err != nil {
		errs = append(errs, fmt.Errorf("BOBRES_ADMIN_TELEGRAM_ID: %w", err))
	}
	return b, errors.Join(errs...)
}
