// Package notify turns core's events into Telegram messages: payment
// results, wallet credits, delivered and extended services, reminders before
// a service ends, support staff's replies, and admin alerts (core's
// watchdog's system alerts, USDT deposits that matched no payment and manual
// USDT proofs that name a deposit the watcher saw among them) and the daily
// sales summary core writes for each staff member who may read the reports.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Telegram is what the notifier sends with.
type Telegram interface {
	SendMessage(ctx context.Context, chatID int64, text string, kb *tg.Keyboard) (*tg.Message, error)
	SendPhoto(ctx context.Context, chatID int64, p tg.Photo, caption string, kb *tg.Keyboard) (*tg.Message, error)
}

// Notifier handles core feed events.
type Notifier struct {
	core     corev1.CoreServiceClient
	tg       Telegram
	cat      *i18n.Catalog
	set      *settings.Store
	adminIDs []int64
	log      *slog.Logger
	sent     unreported
	now      func() time.Time // the clock (tests set it)
}

// unreported remembers staff replies that reached the customer while core
// did not hear of it (message id -> Telegram message id). The same event
// coming back (its ack was lost too) is then reported, never sent again.
// It lives in the process; the oldest are forgotten past maxUnreported.
type unreported struct {
	mu    sync.Mutex
	tg    map[string]int64
	order []string
}

const maxUnreported = 1000

func (u *unreported) put(id string, tgID int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.tg == nil {
		u.tg = map[string]int64{}
	}
	if _, ok := u.tg[id]; !ok {
		u.order = append(u.order, id)
	}
	u.tg[id] = tgID
	for len(u.order) > maxUnreported {
		delete(u.tg, u.order[0])
		u.order = u.order[1:]
	}
}

func (u *unreported) get(id string) (int64, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	v, ok := u.tg[id]
	return v, ok
}

func (u *unreported) drop(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.tg[id]; !ok {
		return
	}
	delete(u.tg, id)
	for i, v := range u.order {
		if v == id {
			u.order = append(u.order[:i], u.order[i+1:]...)
			break
		}
	}
}

// New builds a Notifier. set holds the settings the handler keeps up to date
// (the notify.* switches and who gets staff alerts; nil: the defaults).
// Staff alerts go to every one of adminTelegramIDs (none: nobody) unless
// notify.recipients sends them to every owner and admin.
func New(core corev1.CoreServiceClient, t Telegram, cat *i18n.Catalog, set *settings.Store, adminTelegramIDs []int64, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if set == nil {
		set = settings.New()
	}
	return &Notifier{core: core, tg: t, cat: cat, set: set, adminIDs: adminTelegramIDs, log: log, now: time.Now}
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
		// The customer's confirmation first: a failed one retries the event,
		// and the staff's sale alert must not be sent again each time.
		if e.Source == events.PaidManually || e.Source == events.PaidByGateway {
			lang := n.lang(ctx, e.TelegramID)
			if err := n.send(ctx, e.TelegramID, n.cat.T(lang, "pay.approved"), n.home(lang)); err != nil {
				return err
			}
		} // wallet, trial and free orders were confirmed in the chat already
		if e.Source != events.PaidTrial && e.Source != events.PaidFree && n.set.Bool("notify.sales", false) {
			// Best effort: a lost sale alert is not worth holding the queue for.
			if err := n.saleAlert(ctx, e); err != nil {
				n.log.Warn("sale alert not sent", "order", e.OrderID, "err", err)
			}
		}
		return nil

	case events.SubscriptionProvisioned:
		var e events.SubscriptionProvisionedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		if e.BulkID != "" {
			return nil // a service of a bulk purchase: its link comes in the bulk's one message (BulkReady)
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
		// The links come first: the page address goes under the message and
		// the QR code follows it. Without them the message still goes out.
		links, lerr := n.core.GetSubscriptionLinks(ctx, &corev1.GetSubscriptionLinksRequest{UserId: e.UserID, SubscriptionId: e.SubscriptionID})
		if lerr != nil {
			n.log.Warn("links for a new subscription unavailable", "subscription", e.SubscriptionID, "err", lerr)
		}
		kb := (&tg.Keyboard{}).Row(n.button(lang, "btn.services", "sub:"+e.SubscriptionID))
		if u := links.GetPageUrl(); u != "" {
			kb.Row(tg.Link(n.cat.T(lang, "btn.open_page"), pageLink(u, lang)))
		}
		if err := n.send(ctx, e.TelegramID, n.labelled(lang, e.Label, n.cat.T(lang, "sub.ready",
			"link", html.EscapeString(e.SubscriptionLink), "expires", expires, "traffic", traffic)), kb); err != nil {
			return err
		}
		n.sendQR(ctx, lang, e, links)
		return nil

	case events.PaymentRejected:
		var e events.PaymentRejectedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		kb := (&tg.Keyboard{}).Row(n.button(lang, "btn.try_again", "buy"), n.button(lang, "btn.home", "home"))
		return n.send(ctx, e.TelegramID, n.cat.T(lang, "pay.rejected",
			"amount", i18n.Money(lang, e.Amount, e.Currency), "reason", n.reason(lang, e.Reason)), kb)

	case events.RewardCodeIssued:
		var e events.RewardCodeIssuedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		kb := (&tg.Keyboard{}).Row(n.button(lang, "btn.buy", "buy"), n.button(lang, "btn.my_codes", "cds")) // the code can be found again later
		value := n.cat.T(lang, "reward.percent", "n", i18n.Number(lang, int64(e.Percent)))
		if e.Amount > 0 {
			value = i18n.Money(lang, e.Amount, e.Currency)
		}
		days := int64(e.Days)
		if days <= 0 { // an event from before the days were in it
			days = int64(max(1, (e.ExpiresAt-time.Now().Unix()+86399)/86400))
		}
		text := ""
		if t := rewardText(e.Text, lang); t != "" {
			text = "\n\n" + html.EscapeString(t)
		}
		return n.send(ctx, e.TelegramID, n.cat.T(lang, "reward.code", "code", html.EscapeString(e.Code), "value", value,
			"days", i18n.Number(lang, days), "date", i18n.Date(lang, time.Unix(e.ExpiresAt, 0)), "text", text), kb)

	case events.VendorOrderSettled:
		var e events.VendorOrderSettledEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		return n.vendorSettled(ctx, e)

	case events.WalletCredited:
		var e events.WalletCreditedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		// A gift code was applied in the bot, which answered with the amount
		// and the balance in the same chat (as a wallet payment does): a
		// second message would only repeat it.
		if e.Reason == events.CreditGiftCode {
			return nil
		}
		// A commission names the amount only: the customer who bought is
		// never shown to the reseller.
		key := map[string]string{
			events.CreditOrderNotPayable: "wallet.credited_late",
			events.CreditDiscountUsed:    "wallet.credited_discount_used",
			events.CreditAdminAdjust:     "wallet.credited_admin",
			events.CreditReferral:        "wallet.credited_referral",
			events.CreditRefund:          "wallet.credited_refund",
			events.CreditCommission:      "wallet.credited_commission",
			events.CreditSettlement:      "wallet.credited_settlement",
		}[e.Reason]
		if key == "" {
			key = "wallet.credited"
		}
		lang := n.lang(ctx, e.TelegramID)
		if e.FromCurrency != "" && e.Reason == events.CreditTopup {
			// a USDT top-up, credited in Toman at the live rate
			return n.send(ctx, e.TelegramID, n.cat.T(lang, "wallet.credited_converted", "from", i18n.Money(lang, e.FromAmount, e.FromCurrency),
				"rate", i18n.Money(lang, e.Rate, "IRT"), "amount", i18n.Money(lang, e.Amount, e.Currency),
				"balance", n.balance(lang, e.Balance, e.Currency)), n.home(lang))
		}
		return n.send(ctx, e.TelegramID, n.cat.T(lang, key,
			"amount", i18n.Money(lang, e.Amount, e.Currency), "balance", n.balance(lang, e.Balance, e.Currency)), n.home(lang))

	case events.SubscriptionExtended:
		var e events.SubscriptionExtendedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		kb := (&tg.Keyboard{}).Row(n.button(lang, "btn.view_service", "sub:"+e.SubscriptionID))
		key := "sub.renewed"
		if e.Type == "traffic_topup" {
			key = "sub.topped_up"
		}
		return n.send(ctx, e.TelegramID, n.labelled(lang, e.Label, n.cat.T(lang, key, "id", shortID(e.SubscriptionID),
			"expires", n.expires(lang, e.ExpiresAt), "traffic", n.traffic(lang, e.TrafficTotalBytes))), kb)

	case events.SubscriptionReminder:
		var e events.SubscriptionReminderEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		row := []tg.Button{n.button(lang, "btn.renew", "rnw:"+e.SubscriptionID)}
		if e.TopupAvailable && (e.Kind == events.ReminderLowTraffic || e.Kind == events.ReminderDepleted) {
			row = append(row, n.button(lang, "btn.add_traffic", "tup:"+e.SubscriptionID))
		}
		kb := (&tg.Keyboard{}).Row(row...).Row(n.button(lang, "btn.view_service", "sub:"+e.SubscriptionID))
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
		return n.send(ctx, e.TelegramID, n.labelled(lang, e.Label, text), kb)

	case events.ProvisionFailed:
		var e events.ProvisionFailedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		if n.set.Bool("notify.provision_failed", true) {
			if err := n.alert(ctx, 0, func(lang string) string {
				return n.cat.T(lang, "admin.provision_failed", "attempts", e.Attempts, "order", e.OrderID,
					"tg", e.TelegramID, "error", html.EscapeString(truncate(e.Error, 500)))
			}); err != nil {
				return err
			}
		}
		if e.BulkID != "" {
			return nil // the customer is told by the bulk's own message, not once per service
		}
		return n.send(ctx, e.TelegramID, n.cat.T(n.lang(ctx, e.TelegramID), "sub.delayed"), nil)

	case events.BulkReady:
		var e events.BulkReadyEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		return n.deliverBulk(ctx, e)

	case events.SystemAlert:
		var e events.SystemAlertEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		if !n.set.Bool("notify.system_alerts", true) {
			return nil
		}
		return n.alert(ctx, 0, func(lang string) string { return n.systemAlert(lang, e) })

	case events.DailySummary:
		var e events.DailySummaryEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		return n.dailySummaryEvent(ctx, m, e)

	case events.SystemUpdate:
		var e events.SystemUpdateEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		return n.systemUpdateEvent(ctx, m, e)

	case events.SystemBackup:
		var e events.SystemBackupEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		return n.systemBackupEvent(ctx, m, e)

	case events.DepositUnmatched:
		var e events.DepositUnmatchedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		// Always sent: money arrived and nobody was credited.
		return n.alertTo(ctx, n.set.Recipients(n.adminIDs), 0, func(lang string) (string, *tg.Keyboard) {
			return n.unmatchedDeposit(lang, e)
		})

	case events.DepositClaimed:
		var e events.DepositClaimedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		// Always sent: a public transaction id may be sent as anyone's proof.
		return n.alertTo(ctx, n.set.Recipients(n.adminIDs), 0, func(lang string) (string, *tg.Keyboard) {
			return n.claimedDeposit(lang, e)
		})

	case events.TicketOpened:
		var e events.TicketOpenedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		return n.ticketAlert(ctx, e.MessageID, e.TelegramID, 0, "admin.ticket_opened")

	case events.TicketCustomerReplied:
		var e events.TicketCustomerRepliedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		return n.ticketAlert(ctx, e.MessageID, e.TelegramID, e.AssigneeTelegramID, "admin.ticket_followup")

	case events.TicketReplied:
		var e events.TicketRepliedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		return n.deliverReply(ctx, e)

	case events.TicketClosed:
		var e events.TicketClosedEvent
		if err := decode(m, &e); err != nil {
			return err
		}
		lang := n.lang(ctx, e.TelegramID)
		return n.send(ctx, e.TelegramID, n.cat.T(lang, "support.closed_by_staff", "id", e.Number), n.supportButton(lang))
	}
	return nil
}

// alert messages everyone who gets staff alerts about payments, sales,
// failed deliveries and system problems (notify.recipients; never support
// staff), each in their language, except the Telegram user skip (0:
// nobody). It fails only when nobody got it, so a retry does not repeat it
// to those who did.
func (n *Notifier) alert(ctx context.Context, skip int64, text func(lang string) string) error {
	return n.alertTo(ctx, n.set.Recipients(n.adminIDs), skip, func(lang string) (string, *tg.Keyboard) {
		return text(lang), nil
	})
}

// alertTo messages each of to in their language, except skip; as alert.
func (n *Notifier) alertTo(ctx context.Context, to []settings.Contact, skip int64, render func(lang string) (string, *tg.Keyboard)) error {
	var failed error
	sent := false
	for _, c := range to {
		if c.TelegramID == skip || c.TelegramID == 0 {
			continue
		}
		lang := c.Language
		if lang == "" {
			lang = n.lang(ctx, c.TelegramID)
		}
		text, kb := render(lang)
		if err := n.send(ctx, c.TelegramID, text, kb); err != nil {
			n.log.Warn("staff alert failed", "chat", c.TelegramID, "err", err)
			failed = err
			continue
		}
		sent = true
	}
	if sent {
		return nil
	}
	return failed
}

// systemAlert renders one of core's watchdog alerts in lang: a heading by
// severity (or "still not fixed after", "fixed after"), the problem in a
// sentence, and the check's own words (an error) under it. A problem this
// bot has no text for is named by its check.
func (n *Notifier) systemAlert(lang string, e events.SystemAlertEvent) string {
	title := n.cat.T(lang, "alert.check", "key", html.EscapeString(e.Key))
	if _, known := n.cat.Info(e.TitleKey); known && strings.HasPrefix(e.TitleKey, "alert.") {
		title = n.cat.T(lang, e.TitleKey, i18n.AlertArgs(lang, e.Args, html.EscapeString)...)
	}
	lasted := n.duration(lang, time.Unix(e.At, 0).Sub(time.Unix(e.Since, 0)))
	var text string
	switch {
	case e.State == events.AlertResolved:
		return n.cat.T(lang, "alert.resolved", "duration", lasted, "title", title)
	case e.Repeat:
		text = n.cat.T(lang, "alert.repeat", "duration", lasted, "title", title)
	case e.Severity == events.SeverityCritical:
		text = n.cat.T(lang, "alert.critical", "title", title)
	default:
		text = n.cat.T(lang, "alert.warning", "title", title)
	}
	if d := strings.TrimSpace(e.Detail); d != "" {
		text += "\n<code>" + html.EscapeString(truncateRunes(d, 300)) + "</code>"
	}
	return text
}

// updateNoticeMaxAge is how long a notice about an update from the dashboard
// may wait to be sent. The bot is itself restarted by the update, so the
// notices of an update are normally sent a few minutes late; one older than
// this is history, not news.
const updateNoticeMaxAge = 24 * time.Hour

// systemUpdateEvent tells one owner about an update asked for in the dashboard
// or its end (core wrote one event for each owner). It is dropped when it is
// too old, or when the staff list the bot knows no longer has the person as an
// owner (demoted or removed since: the message names who asked for what, and
// is for owners). A failure is returned for the consumer to retry; a refusal
// no retry can change (Telegram says the chat is gone, or the bot may not write
// there) is permanent, so it does not hold up the events behind it.
func (n *Notifier) systemUpdateEvent(ctx context.Context, m eventbus.Message, e events.SystemUpdateEvent) error {
	if age := n.now().Sub(m.CreatedAt); !m.CreatedAt.IsZero() && age > updateNoticeMaxAge {
		n.log.Info("update notice is too old; dropped", "state", e.State, "chat", e.TelegramID, "queued_hours_ago", int(age.Hours()))
		return nil
	}
	if len(n.set.Staff()) > 0 {
		if c, ok := n.set.Contact(e.TelegramID); !ok || c.Role != "owner" {
			n.log.Info("update notice dropped: the recipient is no longer an owner", "state", e.State, "chat", e.TelegramID)
			return nil
		}
	}
	text, known := n.systemUpdate(i18n.Normalize(e.Language), e)
	if !known {
		return nil // a state a newer core writes
	}
	err := n.send(ctx, e.TelegramID, text, nil)
	if tg.IsRefused(err) {
		n.log.Warn("update notice refused by Telegram; dropped", "state", e.State, "chat", e.TelegramID, "err", err)
		return eventbus.Permanent(err)
	}
	return err
}

// systemUpdate renders an update notice in lang: what happened, from which
// version to which, on which channel and who asked, and, for an end that is
// not a success, the server updater's own sentence under it. Every value is a
// release number, a name or text from outside, escaped. known is false for a
// state this bot has no text for.
func (n *Notifier) systemUpdate(lang string, e events.SystemUpdateEvent) (string, bool) {
	key := map[string]string{
		events.UpdateRequested:  "update.requested",
		events.UpdateDone:       "update.done",
		events.UpdateFailed:     "update.failed",
		events.UpdateRolledBack: "update.rolled_back",
		events.UpdateNotStarted: "update.not_started",
	}[e.State]
	if key == "" {
		return "", false
	}
	version := func(v string) string {
		if v = strings.TrimSpace(v); v == "" {
			return "?"
		}
		return html.EscapeString(truncateRunes(v, 40))
	}
	by := strings.TrimSpace(e.By)
	if by == "" {
		by = "?"
	}
	channel := "update.channel_stable"
	if e.Channel == "beta" {
		channel = "update.channel_beta"
	}
	text := n.cat.T(lang, key, "by", html.EscapeString(truncateRunes(by, 64)), "from", version(e.From), "to", version(e.To),
		"channel", n.cat.T(lang, channel))
	if d := strings.TrimSpace(e.Message); d != "" && e.State != events.UpdateRequested && e.State != events.UpdateDone {
		text += "\n<code>" + html.EscapeString(truncateRunes(d, 300)) + "</code>"
	}
	return text, true
}

// systemBackupEvent tells one owner about a backup downloaded from the dashboard, or a restore of
// one asked for there and its end (core wrote one event for each owner). As an update's notice it
// is dropped when it is too old or when the recipient is no longer an owner, a failure is returned
// for the consumer to retry and a refusal no retry can change is permanent.
func (n *Notifier) systemBackupEvent(ctx context.Context, m eventbus.Message, e events.SystemBackupEvent) error {
	if age := n.now().Sub(m.CreatedAt); !m.CreatedAt.IsZero() && age > updateNoticeMaxAge {
		n.log.Info("backup notice is too old; dropped", "kind", e.Kind, "state", e.State, "chat", e.TelegramID, "queued_hours_ago", int(age.Hours()))
		return nil
	}
	if len(n.set.Staff()) > 0 {
		if c, ok := n.set.Contact(e.TelegramID); !ok || c.Role != "owner" {
			n.log.Info("backup notice dropped: the recipient is no longer an owner", "kind", e.Kind, "state", e.State, "chat", e.TelegramID)
			return nil
		}
	}
	text, known := n.systemBackup(i18n.Normalize(e.Language), e)
	if !known {
		return nil // a kind or state a newer core writes
	}
	err := n.send(ctx, e.TelegramID, text, nil)
	if tg.IsRefused(err) {
		n.log.Warn("backup notice refused by Telegram; dropped", "kind", e.Kind, "state", e.State, "chat", e.TelegramID, "err", err)
		return eventbus.Permanent(err)
	}
	return err
}

// systemBackup renders a backup notice in lang. Every value is a name or text from outside, escaped;
// known is false for a kind or state this bot has no text for.
func (n *Notifier) systemBackup(lang string, e events.SystemBackupEvent) (string, bool) {
	key := ""
	switch {
	case e.Kind == events.BackupDownload && e.State == events.BackupDownloaded:
		key = "backup.download"
	case e.Kind == events.BackupRestore:
		key = map[string]string{
			events.UpdateRequested:  "backup.restore.requested",
			events.UpdateDone:       "backup.restore.done",
			events.UpdateFailed:     "backup.restore.failed",
			events.UpdateRolledBack: "backup.restore.rolled_back",
			events.UpdateNotStarted: "backup.restore.not_started",
		}[e.State]
	}
	if key == "" {
		return "", false
	}
	or := func(s string) string {
		if s = strings.TrimSpace(s); s == "" {
			return "?"
		}
		return html.EscapeString(truncateRunes(s, 80))
	}
	source := ""
	if e.Source == "backup" || e.Source == "upload" {
		source = n.cat.T(lang, "backup.source_"+e.Source)
	}
	text := n.cat.T(lang, key, "by", or(e.By), "file", "<code>"+or(e.File)+"</code>", "size", i18n.FileSize(lang, e.Size), "source", source)
	if d := strings.TrimSpace(e.Message); d != "" && e.Kind == events.BackupRestore && e.State != events.UpdateRequested && e.State != events.UpdateDone {
		text += "\n<code>" + html.EscapeString(truncateRunes(d, 300)) + "</code>"
	}
	return text, true
}

// summaryMaxAge is how long a daily summary may wait to be sent. Core queues
// one for each day whether or not the bot is consuming, so a bot that was down
// for days would otherwise send every missed day back to back; one that waited
// longer than this is old news and is dropped (the newest one is the one that
// goes out).
const summaryMaxAge = 24 * time.Hour

// dailySummaryEvent sends one staff member the summary core wrote for them.
// Core chose the recipient (and their language) when it claimed the day. Two
// things are checked again here, because the event can wait: it is dropped
// when it is too old (summaryMaxAge), or when the staff list the bot knows
// no longer has the person as an owner or admin (demoted, banned or removed
// since the claim; support staff never hold reports.read). A list the bot does
// not know yet decides nothing. A member who blocked the bot is skipped like for
// any notification.
//
// A failure is returned for the consumer to retry, which applies events in order:
// the retry holds up every event behind it (customers' links and payment
// confirmations too) until it works or the event is dead-lettered after many
// failures. So a refusal that no retry can change (Telegram says 400-something
// about this chat or this message, say PEER_ID_INVALID, or that the bot has no
// right to write there) is Permanent and dead-letters at once; only a failure that
// may pass (network, 429, 5xx) is retried.
func (n *Notifier) dailySummaryEvent(ctx context.Context, m eventbus.Message, e events.DailySummaryEvent) error {
	if age := n.now().Sub(m.CreatedAt); !m.CreatedAt.IsZero() && age > summaryMaxAge {
		n.log.Info("daily summary is too old; dropped", "day", e.Date, "chat", e.TelegramID, "queued_hours_ago", int(age.Hours()))
		return nil
	}
	if len(n.set.Staff()) > 0 {
		if c, ok := n.set.Contact(e.TelegramID); !ok || c.Role == "support" {
			n.log.Info("daily summary dropped: the recipient is no longer an owner or admin", "day", e.Date, "chat", e.TelegramID)
			return nil
		}
	}
	err := n.send(ctx, e.TelegramID, n.dailySummary(i18n.Normalize(e.Language), e), nil)
	if tg.IsRefused(err) {
		n.log.Warn("daily summary refused by Telegram; dropped", "day", e.Date, "chat", e.TelegramID, "err", err)
		return eventbus.Permanent(err)
	}
	return err
}

// dailySummary renders yesterday's sales in lang: a heading with the day (Jalali
// in Persian), then each currency in a block of its own (amounts of different
// currencies are never added), then the people (new customers, free trials). A
// day with nothing to report is one line, so the staff see the job is alive.
// Every value is a number or escaped text.
func (n *Notifier) dailySummary(lang string, e events.DailySummaryEvent) string {
	date := html.EscapeString(e.Date)
	if d, err := time.Parse("2006-01-02", e.Date); err == nil {
		date = i18n.CalendarDay(lang, d.Year(), d.Month(), d.Day())
	}
	if len(e.Currencies) == 0 && e.NewCustomers == 0 && e.Trials == 0 {
		return n.cat.T(lang, "report.daily_none", "date", date)
	}
	t := func(key string, args ...any) string { return n.cat.T(lang, key, args...) }
	num := func(v int64) string { return i18n.Number(lang, v) }
	money := func(v int64, cur string) string { return html.EscapeString(i18n.Money(lang, v, cur)) }
	out := []string{t("report.daily_title", "date", date)}
	if len(e.Currencies) == 0 {
		out = append(out, "", t("report.daily_no_sales"))
	}
	for _, c := range e.Currencies {
		revenue := t("report.daily_revenue", "amount", money(c.Revenue, c.Currency))
		if vs := n.revenueVs(lang, c); vs != "" {
			revenue += " (" + vs + ")"
		}
		out = append(out, "", t("report.daily_currency", "currency", html.EscapeString(i18n.CurrencyName(lang, c.Currency))), revenue,
			t("report.daily_orders", "n", num(c.Orders-c.Licenses.Count-c.Digital.Count), "new", num(c.NewOrders), "renew", num(c.Renewals), "traffic", num(c.TrafficTopups)),
			t("report.daily_refunds", "n", num(c.Refunds.Count), "amount", money(c.Refunds.Amount, c.Currency)),
			t("report.daily_net", "amount", money(c.Net, c.Currency)),
			t("report.daily_topups", "n", num(c.Topups.Count), "amount", money(c.Topups.Amount, c.Currency)))
		if c.Licenses.Count > 0 {
			out = append(out, t("report.daily_licenses", "n", num(c.Licenses.Count), "amount", money(c.Licenses.Amount, c.Currency)))
		}
		if c.Digital.Count > 0 {
			out = append(out, t("report.daily_digital", "n", num(c.Digital.Count), "amount", money(c.Digital.Amount, c.Currency)))
		}
		if c.Rejected.Count > 0 {
			out = append(out, t("report.daily_rejected", "n", num(c.Rejected.Count), "amount", money(c.Rejected.Amount, c.Currency)))
		}
		if c.GatewayFailed.Count > 0 {
			out = append(out, t("report.daily_gateway_failed", "n", num(c.GatewayFailed.Count), "amount", money(c.GatewayFailed.Amount, c.Currency)))
		}
	}
	out = append(out, "", t("report.daily_new_customers", "n", num(e.NewCustomers)), t("report.daily_trials", "n", num(e.Trials)))
	if e.Zone != "" {
		out = append(out, "", t("report.daily_zone", "zone", html.EscapeString(e.Zone)))
	}
	return strings.Join(out, "\n")
}

// maxVsPercent is the biggest rise shown as a number; a bigger one is "more
// than" it (a day after almost no sales would otherwise read "up 500000000%").
const maxVsPercent = 999

// revenueVs compares a currency's revenue with the day before, in words: up or
// down by a whole percent, the same (to the unit), about the same (under half a
// percent either way), or "new" when the day before had none ("": no sales
// either day). The words never say more than the amounts do: a day that had
// sales is never "down 100%" (that is a day with none, at most 99%), and a rise
// past maxVsPercent is "more than" it.
func (n *Notifier) revenueVs(lang string, c events.DailySummaryCurrency) string {
	switch {
	case c.PrevRevenue <= 0 && c.Revenue <= 0:
		return ""
	case c.PrevRevenue <= 0:
		return n.cat.T(lang, "report.daily_vs_new")
	case c.Revenue == c.PrevRevenue:
		return n.cat.T(lang, "report.daily_vs_same")
	}
	change := math.Round(float64(c.Revenue-c.PrevRevenue) * 100 / float64(c.PrevRevenue))
	switch {
	case change > maxVsPercent:
		return n.cat.T(lang, "report.daily_vs_up_lots", "pct", i18n.Percent(lang, maxVsPercent))
	case change > 0:
		return n.cat.T(lang, "report.daily_vs_up", "pct", i18n.Percent(lang, int64(change)))
	case change < 0:
		down := -change
		if c.Revenue > 0 {
			down = min(down, 99)
		}
		return n.cat.T(lang, "report.daily_vs_down", "pct", i18n.Percent(lang, int64(min(down, 100))))
	}
	return n.cat.T(lang, "report.daily_vs_about_same")
}

// unmatchedReasons are the reasons payments gives for an unmatched deposit.
var unmatchedReasons = map[string]bool{
	events.UnmatchedNoPayment: true, events.UnmatchedAlreadyPaid: true,
	events.UnmatchedClosed: true, events.UnmatchedAmbiguous: true,
	events.UnmatchedAfterRestore: true,
}

// txidRe is a TRON transaction id as payments records it.
var txidRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// unmatchedDeposit renders a USDT deposit that matched no payment for the
// staff in lang: the exact amount, who sent it, where, when, the transaction
// and why, with a link to the transaction on Tronscan and the dashboard,
// where it is marked handled. Every value is escaped: the sender is anyone
// on the chain.
func (n *Notifier) unmatchedDeposit(lang string, e events.DepositUnmatchedEvent) (string, *tg.Keyboard) {
	reason := html.EscapeString(e.Reason)
	if unmatchedReasons[e.Reason] {
		reason = n.cat.T(lang, "admin.unmatched_reason."+e.Reason)
	}
	part := ""
	if e.EventIndex > 0 {
		part = " " + n.cat.T(lang, "admin.unmatched_part", "n", i18n.Number(lang, int64(e.EventIndex)+1))
	}
	txid := strings.ToLower(strings.TrimPrefix(e.TxID, "0x"))
	text := n.cat.T(lang, "admin.unmatched_deposit",
		"amount", i18n.Exact(e.Amount, e.Currency), "currency", html.EscapeString(e.Currency),
		"from", html.EscapeString(e.From), "to", html.EscapeString(e.To),
		"when", i18n.DateTime(lang, time.Unix(e.BlockTime, 0)), "txid", html.EscapeString(e.TxID),
		"part", part, "reason", reason)
	kb := &tg.Keyboard{}
	if txidRe.MatchString(txid) {
		kb.Row(n.linkButton(lang, "btn.tronscan", "https://tronscan.org/#/transaction/"+txid))
	}
	kb.Row(n.button(lang, "btn.dashboard", "dash"))
	return text, kb
}

// claimedDeposit renders, for the staff in lang, a manual USDT proof that
// carries a transaction the watcher had seen reach the store's address: the
// exact amount, who sent it, the transaction and the payment's reference (as
// the customer was shown it), with a link to the transaction on Tronscan and
// the payments to review while the proof waits. Every value is escaped: the
// sender is anyone on the chain.
func (n *Notifier) claimedDeposit(lang string, e events.DepositClaimedEvent) (string, *tg.Keyboard) {
	part := ""
	if e.EventIndex > 0 {
		part = " " + n.cat.T(lang, "admin.unmatched_part", "n", i18n.Number(lang, int64(e.EventIndex)+1))
	}
	txid := strings.ToLower(strings.TrimPrefix(e.TxID, "0x"))
	text := n.cat.T(lang, "admin.deposit_claimed",
		"amount", i18n.Exact(e.Amount, e.Currency), "currency", html.EscapeString(e.Currency),
		"from", html.EscapeString(e.From), "txid", html.EscapeString(e.TxID), "part", part,
		"ref", html.EscapeString(paymentReference(e.IntentID)))
	kb := &tg.Keyboard{}
	if txidRe.MatchString(txid) {
		kb.Row(n.linkButton(lang, "btn.tronscan", "https://tronscan.org/#/transaction/"+txid))
	}
	if e.Proof == "waiting" {
		kb.Row(n.button(lang, "btn.review_now", "adm:pend"))
	} else {
		kb.Row(n.button(lang, "btn.dashboard", "dash"))
	}
	return text, kb
}

// paymentReference is the reference a customer is shown for a payment (core's
// domain.PaymentReference): the last 8 hex digits of its id, upper case.
func paymentReference(intentID string) string {
	ref := strings.ReplaceAll(intentID, "-", "")
	if len(ref) > 8 {
		ref = ref[len(ref)-8:]
	}
	return strings.ToUpper(ref)
}

// duration names how long a problem lasted: minutes under an hour, hours
// under two days, days from there.
func (n *Notifier) duration(lang string, d time.Duration) string {
	switch {
	case d < time.Hour:
		return n.cat.T(lang, "alert.duration_minutes", "n", i18n.Number(lang, max(int64(d/time.Minute), 1)))
	case d < 48*time.Hour:
		return n.cat.T(lang, "alert.duration_hours", "n", i18n.Number(lang, int64(d/time.Hour)))
	}
	return n.cat.T(lang, "alert.duration_days", "n", i18n.Number(lang, int64(d/(24*time.Hour))))
}

// ticketPreview is the start of a customer's message for a staff alert:
// at most 300 characters, escaped, with 📎 when a file came with it.
const ticketPreview = 300

// ticketAlert tells the staff about a new ticket or a customer's answer
// (notify.new_ticket): everyone who gets ticket alerts, support staff
// included (notify.recipients), or only the ticket's assignee. The customer
// is never alerted about their own ticket (staff writing to their store).
func (n *Notifier) ticketAlert(ctx context.Context, messageID string, customer, assignee int64, key string) error {
	if !n.set.Bool("notify.new_ticket", true) {
		return nil
	}
	v, err := n.core.GetTicketMessage(ctx, &corev1.GetTicketMessageRequest{MessageId: messageID})
	if status.Code(err) == codes.NotFound {
		n.log.Warn("ticket message for an alert not found", "message", messageID)
		return nil
	}
	if err != nil {
		return err
	}
	user := fmt.Sprintf("id %d", v.GetTelegramId())
	if v.GetUsername() != "" {
		user = "@" + v.GetUsername()
	}
	preview := html.EscapeString(truncateRunes(v.GetBody(), ticketPreview))
	if v.GetHasFile() {
		preview = strings.TrimSpace("📎 " + preview)
	}
	to := n.set.TicketRecipients(n.adminIDs)
	if assignee != 0 {
		c, ok := n.set.Contact(assignee)
		if !ok {
			c = settings.Contact{TelegramID: assignee}
		}
		to = []settings.Contact{c}
	}
	return n.alertTo(ctx, to, customer, func(lang string) (string, *tg.Keyboard) {
		var kb *tg.Keyboard
		if u := v.GetUrl(); strings.HasPrefix(u, "https://") {
			kb = (&tg.Keyboard{}).Row(n.linkButton(lang, "btn.ticket_open", u))
		}
		k := key
		if k == "admin.ticket_opened" && v.GetCategory() == "reseller" {
			k = "admin.reseller_application" // an application has words of its own: staff must not take it for a support question
		}
		args := []any{"user", html.EscapeString(user), "id", v.GetTicketNumber(), "text", preview}
		if k == "admin.ticket_opened" {
			args = append(args, "category", n.category(lang, v.GetCategory()))
		}
		return n.cat.T(lang, k, args...), kb
	})
}

// category names a ticket's topic in lang (older tickets have "general").
func (n *Notifier) category(lang, c string) string {
	switch c {
	case "payment", "service", "reseller":
	default:
		c = "other"
	}
	return n.cat.T(lang, "support.cat."+c)
}

// deliverReply sends a staff reply to the customer and reports how it went.
// A reply already reported (a repeated event) is not sent again.
func (n *Notifier) deliverReply(ctx context.Context, e events.TicketRepliedEvent) error {
	v, err := n.core.GetTicketMessage(ctx, &corev1.GetTicketMessageRequest{MessageId: e.MessageID})
	if status.Code(err) == codes.NotFound {
		n.log.Warn("ticket reply to deliver not found", "message", e.MessageID)
		return nil
	}
	if err != nil {
		return err
	}
	if v.GetDelivery() != "pending" {
		n.sent.drop(e.MessageID)
		return nil // sent (or given up) before
	}
	if tgID, ok := n.sent.get(e.MessageID); ok {
		// Sent already; only the report was lost.
		if _, err := n.core.ReportTicketDelivery(ctx, &corev1.ReportTicketDeliveryRequest{MessageId: e.MessageID, Status: "sent",
			TgMessageId: tgID}); err != nil {
			n.log.Warn("ticket reply delivered but not reported", "message", e.MessageID, "err", err)
			return nil
		}
		n.sent.drop(e.MessageID)
		return nil
	}
	lang := i18n.Normalize(v.GetLanguage())
	body := html.EscapeString(v.GetBody())
	var (
		text string
		kb   *tg.Keyboard
	)
	if v.GetClosed() || e.Closed {
		text = n.cat.T(lang, "support.reply_closed", "id", v.GetTicketNumber(), "text", body)
		kb = n.supportButton(lang)
	} else {
		text = n.cat.T(lang, "support.reply", "id", v.GetTicketNumber(), "text", body)
		kb = (&tg.Keyboard{}).Row(n.button(lang, "btn.ticket_reply", "tk:r:"+v.GetTicketId()),
			n.button(lang, "btn.ticket_close", "tk:c:"+v.GetTicketId()))
	}
	msg, err := n.tg.SendMessage(ctx, v.GetTelegramId(), text, kb)
	if tg.IsBlocked(err) {
		n.log.Info("user blocked the bot; ticket reply not delivered", "chat", v.GetTelegramId())
		// Core marks the user as blocking the bot too. Nothing was sent, so a
		// failed report is safe to retry.
		_, err := n.core.ReportTicketDelivery(ctx, &corev1.ReportTicketDeliveryRequest{MessageId: e.MessageID, Status: "blocked"})
		return err
	}
	if tg.IsRefused(err) {
		// Telegram will never take it (say, too long with a text override):
		// failed at once, so later notifications do not wait behind it.
		n.log.Warn("ticket reply refused by Telegram", "message", e.MessageID, "err", err)
		_, err := n.core.ReportTicketDelivery(ctx, &corev1.ReportTicketDeliveryRequest{MessageId: e.MessageID, Status: "failed"})
		return err
	}
	if err != nil {
		return err // retried by the consumer
	}
	if _, err := n.core.ReportTicketDelivery(ctx, &corev1.ReportTicketDeliveryRequest{MessageId: e.MessageID, Status: "sent",
		TgMessageId: int64(msg.MessageID)}); err != nil {
		// Not retried: the reply would be sent twice. Remembered, so the
		// event coming back again is only reported.
		n.log.Warn("ticket reply delivered but not reported", "message", e.MessageID, "err", err)
		n.sent.put(e.MessageID, int64(msg.MessageID))
	}
	return nil
}

// supportButton opens the Support screen as a new message (the one it is
// under stays as it is).
func (n *Notifier) supportButton(lang string) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(n.button(lang, "btn.support", "support:m"))
}

// DeadLetter is the bot's dead-letter sink (an eventbus.DeadLetterFunc): it
// has no database, so an undeliverable notification is logged for the
// operator; a staff reply that could not be delivered is reported failed,
// so the dashboard shows it.
func (n *Notifier) DeadLetter(ctx context.Context, m eventbus.Message, cause error) error {
	n.log.Error("notification dropped", "event_id", m.EventID, "topic", m.Topic, "err", cause)
	if m.Topic != events.TicketReplied {
		return nil
	}
	var e events.TicketRepliedEvent
	if json.Unmarshal(m.Payload, &e) != nil || e.MessageID == "" {
		return nil
	}
	_, err := n.core.ReportTicketDelivery(ctx, &corev1.ReportTicketDeliveryRequest{MessageId: e.MessageID, Status: "failed"})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err // retried later: the dashboard must not keep showing "sending"
}

// saleAlert tells the staff about a paid order (notify.sales); a staff
// member's own purchase is not announced to them.
func (n *Notifier) saleAlert(ctx context.Context, e events.OrderPaidEvent) error {
	// A bulk purchase is one sale: announced with its first order, for the
	// whole quantity (its orders cost the same).
	if e.BulkID != "" && e.BulkIndex != 1 {
		return nil
	}
	buyer := fmt.Sprintf("id %d", e.TelegramID)
	if u, err := n.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: e.TelegramID}}); err == nil && u.GetUsername() != "" {
		buyer = "@" + u.GetUsername()
	}
	var names map[string]string
	if ps, err := n.core.ListPlans(ctx, &corev1.ListPlansRequest{IncludeDisabled: true}); err == nil {
		for _, p := range ps.GetPlans() {
			if p.GetId() == e.PlanID {
				names = p.GetNameI18N()
			}
		}
	}
	if e.BulkID != "" && e.BulkQuantity > 1 {
		return n.alert(ctx, e.TelegramID, func(lang string) string {
			return n.cat.T(lang, "admin.new_bulk_sale", "user", html.EscapeString(buyer), "n", i18n.Number(lang, int64(e.BulkQuantity)),
				"plan", html.EscapeString(planName(names, lang, e.PlanID)), "amount", i18n.Money(lang, e.Amount*int64(e.BulkQuantity), e.Currency))
		})
	}
	return n.alert(ctx, e.TelegramID, func(lang string) string {
		return n.cat.T(lang, "admin.new_sale", "user", html.EscapeString(buyer),
			"plan", html.EscapeString(planName(names, lang, e.PlanID)), "amount", i18n.Money(lang, e.Amount, e.Currency))
	})
}

// planName is a plan's name in lang, else in another language, else its id.
func planName(names map[string]string, lang, id string) string {
	if n := names[lang]; n != "" {
		return n
	}
	for _, l := range i18n.Languages {
		if n := names[l]; n != "" {
			return n
		}
	}
	return "#" + shortID(id)
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

// balance shows a wallet balance after a credit. A reseller's negative
// balance is what they owe: shown as a debt, never with a bare minus.
func (n *Notifier) balance(lang string, amount int64, currency string) string {
	if amount < 0 {
		return n.cat.T(lang, "wallet.debt", "amount", i18n.Money(lang, -amount, currency))
	}
	return i18n.Money(lang, amount, currency)
}

// labelled puts the name a reseller gave a service above a message about it
// (they get the messages of the services they buy for customers).
func (n *Notifier) labelled(lang, label, text string) string {
	if label == "" {
		return text
	}
	return n.cat.T(lang, "sub.label_line", "label", html.EscapeString(label)) + "\n" + text
}

func (n *Notifier) home(lang string) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(n.button(lang, "btn.home", "home"))
}

// button is a callback button labelled with key's text in lang, in the color
// of what it does (i18n.ButtonStyle), as on the bot's own screens.
func (n *Notifier) button(lang, key, data string) tg.Button {
	b := tg.CB(n.cat.T(lang, key), data)
	b.Style = i18n.ButtonStyle(key)
	return b
}

// linkButton is a URL button labelled with key's text, colored the same way.
func (n *Notifier) linkButton(lang, key, url string) tg.Button {
	b := tg.Link(n.cat.T(lang, key), url)
	b.Style = i18n.ButtonStyle(key)
	return b
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
		n.markBlocked(ctx, chatID)
		return nil
	}
	return err
}

// markBlocked records in core that a user blocked the bot (best effort: the
// next broadcast finds out again).
func (n *Notifier) markBlocked(ctx context.Context, chatID int64) {
	if _, err := n.core.SetUserBotBlocked(ctx, &corev1.SetUserBotBlockedRequest{TelegramId: chatID, Blocked: true}); err != nil &&
		status.Code(err) != codes.NotFound {
		n.log.Warn("bot blocked flag not saved", "chat", chatID, "err", err)
	}
}

// sendQR follows the ready message with the QR code; failures are logged
// only, since the link already went out.
func (n *Notifier) sendQR(ctx context.Context, lang string, e events.SubscriptionProvisionedEvent, links *corev1.SubscriptionLinks) {
	if len(links.GetQrPng()) == 0 {
		n.log.Warn("QR for a new subscription unavailable", "subscription", e.SubscriptionID)
		return
	}
	if _, err := n.tg.SendPhoto(ctx, e.TelegramID, tg.Photo{Data: links.GetQrPng(), Name: "subscription.png"}, n.cat.T(lang, "sub.qr_caption"), nil); err != nil && !tg.IsBlocked(err) {
		n.log.Warn("send QR failed", "err", err)
	}
}

// pageLink adds the customer's language to a page address, as the bot's
// service screen does.
func pageLink(u, lang string) string {
	if strings.Contains(u, "?") {
		return u
	}
	return u + "?l=" + lang
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// truncateRunes shortens s to n runes (never splitting a character).
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// rewardText is the owner's text for a reward in the customer's language, else English, else Persian ("" when there is none).
func rewardText(texts map[string]string, lang string) string {
	for _, l := range []string{lang, "en", "fa"} {
		if t := strings.TrimSpace(texts[l]); t != "" {
			return t
		}
	}
	return ""
}

// vendorSettled tells a customer that the license they asked for before topping up was bought (with its key, or the new end of a renewal) or
// refunded. The key is fetched here, never carried by the event.
func (n *Notifier) vendorSettled(ctx context.Context, e events.VendorOrderSettledEvent) error {
	lang := n.lang(ctx, e.TelegramID)
	kb := (&tg.Keyboard{}).Row(n.button(lang, "btn.lic_my", "lic:my"), n.button(lang, "btn.home", "home"))
	tier := e.Tier
	if tier == "standard" || tier == "pro" {
		tier = n.cat.T(lang, "lic.tier."+tier)
	}
	end := n.cat.T(lang, "lic.no_end")
	if e.EndsAt != 0 {
		end = i18n.Date(lang, time.Unix(e.EndsAt, 0))
	}
	switch {
	case e.Status != "done":
		return n.send(ctx, e.TelegramID, n.cat.T(lang, "lic.refunded"), kb)
	case e.Kind == "renew":
		return n.send(ctx, e.TelegramID, n.cat.T(lang, "lic.renewed", "tier", tier, "end", end), kb)
	}
	key, err := n.core.GetVendorLicenseKey(ctx, &corev1.GetVendorLicenseKeyRequest{UserId: e.UserID, LicenseId: e.LicenseID})
	if err != nil {
		// The license is bought; the key is in My licenses. Say so instead of failing the event for ever.
		n.log.Warn("license key for a settled intent", "err", err)
		return n.send(ctx, e.TelegramID, n.cat.T(lang, "lic.pending"), kb)
	}
	return n.send(ctx, e.TelegramID, n.cat.T(lang, "lic.bought", "tier", tier, "end", end, "key", html.EscapeString(key.GetKey())), kb)
}
