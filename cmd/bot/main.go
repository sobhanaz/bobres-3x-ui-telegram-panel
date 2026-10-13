// Command bot is the BOBRES Telegram bot. It talks only to core (gRPC) and to
// the Telegram Bot API; state lives in Redis.
package main

import (
	"context"
	"fmt"
	"os"
	"time"
	_ "time/tzdata" // general.timezone works even where the image has no zoneinfo

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/broadcast"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/handler"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/runner"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/ratelimit"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/redisx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

func main() {
	os.Exit(app.Run("bot", os.Args[1:], setup))
}

func setup(rt *app.Runtime) error {
	cfg, err := config.LoadBot()
	if err != nil {
		return fmt.Errorf("load bot config: %w", err)
	}
	// The time zone of dates until (and unless) general.timezone names one.
	loc := i18n.CurrentLocation()
	if tz := os.Getenv("BOBRES_TIMEZONE"); tz != "" {
		if loc, err = time.LoadLocation(tz); err != nil {
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

	// Any 429 either client sees pauses broadcasts (flood); broadcasts take
	// only what the interactive client's messages leave (meter), on their own
	// connections, without the hidden sleep on a 429.
	flood, meter := &tg.Flood{}, &tg.Meter{}
	bot := tg.New(cfg.TelegramBotToken, tg.WithAPIRoot(cfg.TelegramAPIURL), tg.WithFlood(flood), tg.WithMeter(meter))
	bulk := tg.New(cfg.TelegramBotToken, tg.WithAPIRoot(cfg.TelegramAPIURL), tg.WithFlood(flood), tg.WithNoRetry(),
		tg.WithHTTPClient(tg.NewBulkHTTPClient(8)))
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
		handler.Config{AdminTelegramID: cfg.AdminTelegramID, BotName: me.FirstName, BotUsername: me.Username, Location: loc}, rt.Log)
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

	// The bot's own Telegram profile (menu commands, description, short
	// description, profile photo) follows its texts, the branding, the staff
	// and the photo uploaded on the Branding page (fetched from core when its
	// version changes); what was set is kept in Redis per bot, so a restart
	// does not set it all again and another bot token gets the whole profile.
	// It has a client of its own: a 429 on these calls must not pause
	// replies and broadcasts (the shared flood wait), and it waits for the
	// next refresh instead of sleeping.
	profileBot := tg.New(cfg.TelegramBotToken, tg.WithAPIRoot(cfg.TelegramAPIURL), tg.WithNoRetry())
	rt.Go("profile", func(ctx context.Context) error {
		return h.RunProfile(ctx, profileBot, me.ID, state.NewMemo(rdb, fmt.Sprintf("bot:profile:%d", me.ID)))
	})

	notifier := notify.New(core, bot, cat, h.Settings(), cfg.AdminTelegramID, rt.Log)
	notifications, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Name:       "core-events",
		Source:     eventbus.NewGRPCSource(eventsv1.NewEventFeedServiceClient(conn)),
		Handle:     notifier.Handle,
		DeadLetter: notifier.DeadLetter,
		Log:        rt.Log,
	})
	if err != nil {
		return err
	}
	rt.Go("notifications", notifications.Run)

	// Broadcasts: claimed from core in batches, sent at the store's pace;
	// at shutdown the sends in flight finish and the rest go back in line.
	rt.Go("broadcasts", broadcast.New(core, bulk, h.Settings(), flood, meter, rt.Log).Run)

	if cfg.PeerCoreToken != "" {
		rt.Mux.Handle("GET /internal/files/{id}", runner.Files(bot, cfg.PeerCoreToken, rt.Log))
		// Core asks for a reload after every settings, texts or branding change
		// (the minute ticker above stays as the fallback), and checks a join
		// channel for the dashboard: core has no bot token.
		rt.Mux.Handle("POST /internal/settings/refresh", runner.Refresh(h.RefreshSettings, cfg.PeerCoreToken, rt.Log))
		rt.Mux.Handle("GET /internal/channel-check", runner.ChannelCheck(bot, me.ID, cfg.PeerCoreToken))
		// "Send to me" from the broadcast composer.
		rt.Mux.Handle("POST /internal/broadcasts/test", runner.BroadcastTest(bot, cfg.PeerCoreToken, rt.Log))
		// The System page's update check: the bot has internet access, core
		// none. It reads the newest release of BOBRES_UPDATE_CHANNEL (stable:
		// the latest release; beta: the highest version GitHub's API lists,
		// pre-releases included) every 12 hours (an hour after a failure) and
		// verifies its signed checksums.txt with the installer's root keys;
		// applying an update stays `bobres update`.
		updates := release.NewChecker(version.Version, cfg.UpdateChannel, rt.Log)
		rt.Go("update-check", updates.Run)
		rt.Mux.Handle("GET /internal/update-check", runner.UpdateCheck(updates.Status, cfg.PeerCoreToken))
		// The System page's logs tab (owner): this process's kept warnings
		// and errors, already redacted.
		rt.Mux.Handle("GET /internal/logs", runner.Logs(logging.Recent, cfg.PeerCoreToken))
		// Activating a license key and the daily renewal: core has no
		// internet access, so the bot passes its calls to the vendor's
		// license server (BOBRES_LICENSE_SERVER) and keeps nothing.
		lic := runner.LicenseProxy{Server: cfg.LicenseServer, CoreToken: cfg.PeerCoreToken, Log: rt.Log}
		rt.Mux.Handle("POST /internal/license/activate", lic.Handler(runner.LicenseActivate))
		rt.Mux.Handle("POST /internal/license/refresh", lic.Handler(runner.LicenseRefresh))
		// The dashboard's HTTPS page detects the server's public IP address
		// (for ip mode) through the bot: core has no internet access.
		rt.Mux.Handle("GET /internal/public-ip", runner.PublicIP(runner.IPLookup{}.Lookup, cfg.PeerCoreToken, rt.Log))
	}

	if cfg.PeerBackupToken != "" {
		// The backup service hands each encrypted backup to the bot, which
		// sends it to the owner only (never a chat the caller names).
		rt.Mux.Handle("POST /internal/backups/deliver", runner.Deliver(runner.Delivery{
			Bot: bot, Token: cfg.PeerBackupToken, Owner: cfg.AdminTelegramID,
			Limit: runner.UploadLimit(cfg.TelegramAPIURL != ""), Texts: cat,
			Lang: func(ctx context.Context) string { return ownerLang(ctx, core, h, cfg.AdminTelegramID) },
			Log:  rt.Log,
		}))
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

// ownerLang is the owner's language: as staff core listed, else as a user of
// the bot, else the first language.
func ownerLang(ctx context.Context, core corev1.CoreServiceClient, h *handler.Handler, owner int64) string {
	if c, ok := h.Settings().Contact(owner); ok && c.Language != "" {
		return c.Language
	}
	u, err := core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: owner}})
	if err != nil || u.GetLanguage() == "" {
		return i18n.Languages[0]
	}
	return u.GetLanguage()
}
