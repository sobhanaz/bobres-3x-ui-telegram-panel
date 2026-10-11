// Package notify turns core's events into Telegram messages: payment
// results, wallet credits, delivered and extended services, reminders before
// a service ends, and admin alerts.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Telegram is what the notifier sends with.
type Telegram interface {
	SendMessage(ctx context.Context, chatID int64, text string, kb *tg.Keyboard) (*tg.Message, error)
	SendPhoto(ctx context.Context, chatID int64, p tg.Photo, caption string, kb *tg.Keyboard) (*tg.Message, error)
}

// Notifier handles core feed events.
type Notifier struct {
	core    corev1.CoreServiceClient
	tg      Telegram
	cat     *i18n.Catalog
	adminTG int64
	log     *slog.Logger
}

// New builds a Notifier; adminTelegramID 0 disables admin alerts.
func New(core corev1.CoreServiceClient, t Telegram, cat *i18n.Catalog, adminTelegramID int64, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Notifier{core: core, tg: t, cat: cat, adminTG: adminTelegramID, log: log}
}

func decode(m eventbus.Message, v any) error {
	if err := json.Unmarshal(m.Payload, v); err != nil {
		return eventbus.Permanent(fmt.Errorf("decode %s: %w", m.Topic, err))
	}
	return nil
}

// Handle implements eventbus.Handler. Users who blocked the bot are skipped;
// other send failures are retried by the consumer.
func (n *Notifier) Handle(ctx context.Context, m eventbus.Message) error {
	switch m.Topic {
	case events.OrderPaid:
		var e events.OrderPaidEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		if e.Source != events.PaidManually && e.Source != events.PaidByGateway {
			return nil // wallet, trial and free orders were confirmed in the chat already
		}
		lang := n.lang(ctx, e.TelegramID)
		return n.send(ctx, e.TelegramID, n.cat.T(lang, "pay.approved"), n.home(lang))

	case events.SubscriptionProvisioned:
		var e events.SubscriptionProvisionedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		expires := n.cat.T(lang, "sub.no_expiry")
		if e.ExpiresAt > 0 {
			expires = i18n.Date(lang, time.Unix(e.ExpiresAt, 0))
		}
		traffic := n.cat.T(lang, "sub.unlimited")
		if e.TrafficBytes > 0 {
			traffic = i18n.Bytes(lang, e.TrafficBytes)
		}
		kb := (&tg.Keyboard{}).Row(tg.CB(n.cat.T(lang, "btn.services"), "sub:"+e.SubscriptionID))
		if err := n.send(ctx, e.TelegramID, n.cat.T(lang, "sub.ready",
			"link", html.EscapeString(e.SubscriptionLink), "expires", expires, "traffic", traffic), kb); err != nil {
			return err
		}
		n.sendQR(ctx, lang, e)
		return nil

	case events.PaymentRejected:
		var e events.PaymentRejectedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		kb := (&tg.Keyboard{}).Row(tg.CB(n.cat.T(lang, "btn.try_again"), "buy"), tg.CB(n.cat.T(lang, "btn.home"), "home"))
		return n.send(ctx, e.TelegramID, n.cat.T(lang, "pay.rejected",
			"amount", i18n.Money(lang, e.Amount, e.Currency), "reason", n.reason(lang, e.Reason)), kb)

	case events.WalletCredited:
		var e events.WalletCreditedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		key := map[string]string{
			events.CreditOrderNotPayable: "wallet.credited_late",
			events.CreditAdminAdjust:     "wallet.credited_admin",
			events.CreditReferral:        "wallet.credited_referral",
		}[e.Reason]
		if key == "" {
			key = "wallet.credited"
		}
		lang := n.lang(ctx, e.TelegramID)
		return n.send(ctx, e.TelegramID, n.cat.T(lang, key,
			"amount", i18n.Money(lang, e.Amount, e.Currency), "balance", i18n.Money(lang, e.Balance, e.Currency)), n.home(lang))

	case events.SubscriptionExtended:
		var e events.SubscriptionExtendedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		kb := (&tg.Keyboard{}).Row(tg.CB(n.cat.T(lang, "btn.view_service"), "sub:"+e.SubscriptionID))
		key := "sub.renewed"
		if e.Type == "traffic_topup" {
			key = "sub.topped_up"
		}
		return n.send(ctx, e.TelegramID, n.cat.T(lang, key, "id", shortID(e.SubscriptionID),
			"expires", n.expires(lang, e.ExpiresAt), "traffic", n.traffic(lang, e.TrafficTotalBytes)), kb)

	case events.SubscriptionReminder:
		var e events.SubscriptionReminderEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		row := []tg.Button{tg.CB(n.cat.T(lang, "btn.renew"), "rnw:"+e.SubscriptionID)}
		if e.TopupAvailable && (e.Kind == events.ReminderLowTraffic || e.Kind == events.ReminderDepleted) {
			row = append(row, tg.CB(n.cat.T(lang, "btn.add_traffic"), "tup:"+e.SubscriptionID))
		}
		kb := (&tg.Keyboard{}).Row(row...).Row(tg.CB(n.cat.T(lang, "btn.view_service"), "sub:"+e.SubscriptionID))
		var text string
		switch e.Kind {
		case events.ReminderExpiring:
			text = n.cat.T(lang, "reminder.expiring", "id", shortID(e.SubscriptionID),
				"days", i18n.Number(lang, int64(e.Days)), "date", n.expires(lang, e.ExpiresAt))
		case events.ReminderLowTraffic:
			text = n.cat.T(lang, "reminder.low_traffic", "id", shortID(e.SubscriptionID),
				"used", i18n.Bytes(lang, e.TrafficUsedBytes), "total", i18n.Bytes(lang, e.TrafficTotalBytes))
		case events.ReminderExpired:
			text = n.cat.T(lang, "reminder.expired", "id", shortID(e.SubscriptionID))
		case events.ReminderDepleted:
			text = n.cat.T(lang, "reminder.depleted", "id", shortID(e.SubscriptionID), "total", i18n.Bytes(lang, e.TrafficTotalBytes))
		default:
			return nil // a newer core's reminder this bot does not know
		}
		return n.send(ctx, e.TelegramID, text, kb)

	case events.ProvisionFailed:
		var e events.ProvisionFailedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		if n.adminTG != 0 {
			lang := n.lang(ctx, n.adminTG)
			if err := n.send(ctx, n.adminTG, n.cat.T(lang, "admin.provision_failed",
				"attempts", e.Attempts, "order", e.OrderID, "tg", e.TelegramID, "error", html.EscapeString(truncate(e.Error, 500))), nil); err != nil {
				return err
			}
		}
		return n.send(ctx, e.TelegramID, n.cat.T(n.lang(ctx, e.TelegramID), "sub.delayed"), nil)
	}
	return nil
}

func (n *Notifier) expires(lang string, unix int64) string {
	if unix <= 0 {
		return n.cat.T(lang, "sub.no_expiry")
	}
	return i18n.Date(lang, time.Unix(unix, 0))
}

func (n *Notifier) traffic(lang string, b int64) string {
	if b <= 0 {
		return n.cat.T(lang, "sub.unlimited")
	}
	return i18n.Bytes(lang, b)
}

// shortID is how services are named in messages: the last 6 characters.
func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

func (n *Notifier) home(lang string) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(tg.CB(n.cat.T(lang, "btn.home"), "home"))
}

// gatewayReasons are the failure reasons automated gateways report.
var gatewayReasons = map[string]bool{
	"gateway_failed": true, "amount_mismatch": true, "expired": true,
	"payer_mismatch": true, "not_found": true, "reversed": true,
}

// reason localizes a rejection reason ("preset:<name>" from the admin panel,
// or a gateway's reason code).
func (n *Notifier) reason(lang, r string) string {
	if name, ok := strings.CutPrefix(r, "preset:"); ok {
		return n.cat.T(lang, "reason."+name)
	}
	if gatewayReasons[r] {
		return n.cat.T(lang, "reason."+r)
	}
	if strings.TrimSpace(r) == "" {
		return n.cat.T(lang, "reason.none")
	}
	return html.EscapeString(r)
}

func (n *Notifier) lang(ctx context.Context, telegramID int64) string {
	u, err := n.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: telegramID}})
	if err != nil {
		return i18n.Languages[0]
	}
	return u.GetLanguage()
}

func (n *Notifier) send(ctx context.Context, chatID int64, text string, kb *tg.Keyboard) error {
	if chatID == 0 {
		return nil
	}
	_, err := n.tg.SendMessage(ctx, chatID, text, kb)
	if tg.IsBlocked(err) {
		n.log.Info("user blocked the bot; notification skipped", "chat", chatID)
		return nil
	}
	return err
}

// sendQR follows the ready message with the QR code; failures are logged
// only, since the link already went out.
func (n *Notifier) sendQR(ctx context.Context, lang string, e events.SubscriptionProvisionedEvent) {
	links, err := n.core.GetSubscriptionLinks(ctx, &corev1.GetSubscriptionLinksRequest{UserId: e.UserID, SubscriptionId: e.SubscriptionID})
	if err != nil || len(links.GetQrPng()) == 0 {
		n.log.Warn("QR for a new subscription unavailable", "subscription", e.SubscriptionID, "err", err)
		return
	}
	if _, err := n.tg.SendPhoto(ctx, e.TelegramID, tg.Photo{Data: links.GetQrPng(), Name: "subscription.png"}, n.cat.T(lang, "sub.qr_caption"), nil); err != nil && !tg.IsBlocked(err) {
		n.log.Warn("send QR failed", "err", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// LogDeadLetter is the bot's dead-letter sink: it has no database, so an
// undeliverable notification is logged for the operator.
func LogDeadLetter(log *slog.Logger) eventbus.DeadLetterFunc {
	return func(_ context.Context, m eventbus.Message, cause error) error {
		log.Error("notification dropped", "event_id", m.EventID, "topic", m.Topic, "err", cause)
		return nil
	}
}
