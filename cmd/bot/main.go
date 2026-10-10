// Command bot is the BOBRES Telegram bot. It talks only to core (gRPC) and to
// the Telegram Bot API; state lives in Redis.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/handler"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/runner"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/ratelimit"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/redisx"
)

func main() {
	os.Exit(app.Run("bot", os.Args[1:], setup))
}

func setup(rt *app.Runtime) error {
	cfg, err := config.LoadBot()
	if err != nil {
		return fmt.Errorf("load bot config: %w", err)
	}
	if tz := os.Getenv("BOBRES_TIMEZONE"); tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return fmt.Errorf("BOBRES_TIMEZONE: %w", err)
		}
		i18n.SetLocation(loc)
	}

	rdb, err := redisx.New(rt.Ctx, cfg.RedisURL)
	if err != nil {
		return err
	}
	rt.OnClose(func() { _ = rdb.Close() })
	rt.Health.AddCheck("redis", func(c context.Context) error { return rdb.Ping(c).Err() })

	conn, err := grpcx.Dial(cfg.CoreGRPCAddr, cfg.ServiceToken)
	if err != nil {
		return fmt.Errorf("dial core %s: %w", cfg.CoreGRPCAddr, err)
	}
	rt.OnClose(func() { _ = conn.Close() })
	core := corev1.NewCoreServiceClient(conn)

	bot := tg.New(cfg.TelegramBotToken, tg.WithAPIRoot(cfg.TelegramAPIURL))
	if cfg.TelegramAPIURL != "" {
		rt.Log.Warn("using a custom Telegram API root", "url", cfg.TelegramAPIURL)
	}
	meCtx, cancel := context.WithTimeout(rt.Ctx, 20*time.Second)
	me, err := bot.GetMe(meCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("telegram rejected the bot token or is unreachable (check BOBRES_TELEGRAM_BOT_TOKEN): %w", err)
	}
	rt.Log.Info("telegram bot ready", "username", me.Username)

	cat := i18n.MustLoad()
	h := handler.New(core, bot, state.NewRedis(rdb), cat, ratelimit.New(rdb, ""),
		handler.Config{AdminTelegramID: cfg.AdminTelegramID, BotName: me.FirstName, BotUsername: me.Username}, rt.Log)
	if err := h.RefreshSettings(rt.Ctx); err != nil {
		rt.Log.Warn("settings not loaded yet (core unreachable?); retrying in the background", "err", err)
	}
	rt.Go("settings", func(ctx context.Context) error {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				if err := h.RefreshSettings(ctx); err != nil {
					rt.Log.Warn("settings refresh failed", "err", err)
				}
			}
		}
	})

	notifications, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Name:       "core-events",
		Source:     eventbus.NewGRPCSource(eventsv1.NewEventFeedServiceClient(conn)),
		Handle:     notify.New(core, bot, cat, cfg.AdminTelegramID, rt.Log).Handle,
		DeadLetter: notify.LogDeadLetter(rt.Log),
		Log:        rt.Log,
	})
	if err != nil {
		return err
	}
	rt.Go("notifications", notifications.Run)

	if cfg.PeerCoreToken != "" {
		rt.Mux.Handle("GET /internal/files/{id}", runner.Files(bot, cfg.PeerCoreToken, rt.Log))
	}

	d := runner.NewDispatcher(rt.Ctx, 8, h.Handle)
	rt.OnStop(d.Stop)
	if cfg.WebhookURL != "" {
		if err := bot.SetWebhook(rt.Ctx, cfg.WebhookURL, cfg.WebhookSecret); err != nil {
			return fmt.Errorf("set webhook: %w", err)
		}
		rt.Mux.Handle("/tg/webhook", runner.Webhook(cfg.WebhookSecret, d, rt.Log))
		rt.Log.Info("receiving updates by webhook")
		return nil
	}
	// A webhook left over from an earlier configuration would make getUpdates fail.
	if err := bot.DeleteWebhook(rt.Ctx); err != nil {
		rt.Log.Warn("could not delete an old webhook", "err", err)
	}
	rt.Go("polling", func(ctx context.Context) error { return runner.Poll(ctx, bot, d, rt.Log) })
	rt.Log.Info("receiving updates by long polling")
	return nil
}
