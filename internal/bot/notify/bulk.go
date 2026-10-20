package notify

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The delivery of a bulk purchase (docs/03-bot-ux.md, "Bulk purchase"): core
// announces it once (events.BulkReady), when every service is delivered or is
// being retried in the background. The customer gets ONE message with every
// link (a few when it would be too long for Telegram's 4096 characters, with
// a text file of all the links after them), says which services are still
// being prepared, and has a button to see their services. A service that is
// ready later comes as an ordinary delivery message of its own.

// bulkMessageLimit is how long a message of the delivery may be, in the units
// Telegram counts (i18n.TextLength): under its 4096, for what a text override
// adds.
const bulkMessageLimit = 3900

// documentSender is what a Telegram client may also do: upload a file. The
// notifier needs it only to send the text file of a long delivery; a client
// without it gets the messages alone.
type documentSender interface {
	SendDocumentUpload(ctx context.Context, chatID int64, name string, r io.Reader, size int64, caption string) (*tg.Message, error)
}

// bulkItem is one service of the delivery: its name, and its link when it is
// delivered.
type bulkItem struct {
	index int32 // its place in the purchase, from 1
	name  string
	link  string // "" while it is being prepared
}

// bulkMessages lays the delivery out as messages of at most limit characters:
// the header, then the items in order, each a block of its own, then the
// footer (a message that continues another starts with the continued line).
// The footer stays with the last items when it fits.
func bulkMessages(header string, items []string, footer, continued string, limit int) []string {
	const sep = "\n\n"
	var out []string
	cur := header
	for _, it := range items {
		if i18n.TextLength(cur+sep+it) > limit {
			out = append(out, cur)
			cur = continued
		}
		cur += sep + it
	}
	if footer != "" {
		if i18n.TextLength(cur+sep+footer) > limit {
			out = append(out, cur)
			cur = footer
		} else {
			cur += sep + footer
		}
	}
	return append(out, cur)
}

// deliverBulk sends the customer a bulk purchase's links. The services in the
// announcement (e.ReadyOrders) are the delivered ones: any other is shown as
// being prepared, and is delivered on its own when it is ready.
func (n *Notifier) deliverBulk(ctx context.Context, e events.BulkReadyEvent) error {
	b, err := n.core.GetBulkOrder(ctx, &corev1.GetBulkOrderRequest{BulkId: e.BulkID, UserId: e.UserID})
	if status.Code(err) == codes.NotFound {
		n.log.Warn("bulk purchase to deliver not found", "bulk", e.BulkID)
		return nil
	}
	if err != nil {
		return err // retried by the consumer
	}
	lang := n.lang(ctx, e.TelegramID)
	t := func(key string, args ...any) string { return n.cat.T(lang, key, args...) }
	ready := make(map[string]bool, len(e.ReadyOrders))
	for _, id := range e.ReadyOrders {
		ready[id] = true
	}
	var items []bulkItem
	waiting := 0
	for _, sv := range b.GetServices() {
		switch {
		case ready[sv.GetOrderId()] && sv.GetSubscriptionLink() != "":
			items = append(items, bulkItem{index: sv.GetIndex(), name: bulkName(sv), link: sv.GetSubscriptionLink()})
		case sv.GetOrderStatus() == "cancelled":
			// refunded before it was delivered: nothing to deliver
		default:
			items = append(items, bulkItem{index: sv.GetIndex(), name: bulkName(sv)})
			waiting++
		}
	}
	delivered := len(items) - waiting
	if delivered == 0 && waiting == 0 {
		return nil
	}
	var blocks []string
	for _, it := range items {
		num := i18n.Number(lang, int64(it.index))
		if it.link == "" {
			blocks = append(blocks, t("bulk.item_waiting", "i", num, "name", html.EscapeString(it.name)))
			continue
		}
		blocks = append(blocks, t("bulk.item", "i", num, "name", html.EscapeString(it.name), "link", html.EscapeString(it.link)))
	}
	header, footer := t("bulk.ready_all", "n", i18n.Number(lang, int64(len(items)))), ""
	switch {
	case delivered == 0: // none is ready: no celebration, and the header already says what is going on
		header = t("bulk.ready_none", "n", i18n.Number(lang, int64(len(items))))
	case waiting > 0:
		header = t("bulk.ready_some", "ready", i18n.Number(lang, int64(delivered)), "n", i18n.Number(lang, int64(len(items))))
		footer = t("bulk.delayed", "m", i18n.Number(lang, int64(waiting)))
	}
	msgs := bulkMessages(header, blocks, footer, t("bulk.continued"), bulkMessageLimit)
	kb := (&tg.Keyboard{}).Row(n.button(lang, "btn.services", "subs"))
	sent, err := n.sendBulkMessages(ctx, e.TelegramID, msgs, kb)
	if err != nil || !sent {
		return err
	}
	if len(msgs) > 1 && delivered > 0 {
		n.sendLinksFile(ctx, e, lang, items, delivered)
	}
	return nil
}

// bulkName names a service of the delivery: the name it was given, else its
// number as My services shows it.
func bulkName(sv *corev1.BulkService) string {
	switch {
	case sv.GetLabel() != "":
		return sv.GetLabel()
	case sv.GetSubscriptionId() != "":
		return "#" + shortID(sv.GetSubscriptionId())
	}
	return fmt.Sprintf("#%d", sv.GetIndex())
}

// sendBulkMessages sends the messages in order, the last with the button. The
// first failing is returned, so the whole delivery is retried (nothing went
// out yet); a later one is only logged, since a retry would send the first ones
// twice and the links are in the file and in My services. sent is false when
// the customer blocked the bot.
func (n *Notifier) sendBulkMessages(ctx context.Context, chatID int64, msgs []string, kb *tg.Keyboard) (sent bool, err error) {
	for i, text := range msgs {
		var markup *tg.Keyboard
		if i == len(msgs)-1 {
			markup = kb
		}
		_, err := n.tg.SendMessage(ctx, chatID, text, markup)
		switch {
		case tg.IsBlocked(err):
			n.log.Info("user blocked the bot; bulk delivery skipped", "chat", chatID)
			n.markBlocked(ctx, chatID)
			return false, nil
		case err != nil && i == 0:
			return false, err
		case err != nil:
			n.log.Warn("a message of a bulk delivery was not sent", "chat", chatID, "part", i+1, "of", len(msgs), "err", err)
		}
	}
	return true, nil
}

// sendLinksFile sends every delivered link as a text file, one service a line
// (its name, a tab, its link). Best effort: the links went out as messages.
func (n *Notifier) sendLinksFile(ctx context.Context, e events.BulkReadyEvent, lang string, items []bulkItem, delivered int) {
	up, ok := n.tg.(documentSender)
	if !ok {
		n.log.Debug("this Telegram client cannot upload a file; the bulk delivery has no text file", "bulk", e.BulkID)
		return
	}
	var buf bytes.Buffer
	for _, it := range items {
		if it.link != "" {
			buf.WriteString(strings.Join(strings.Fields(it.name), " ") + "\t" + it.link + "\n")
		}
	}
	name := "bobres-services-" + shortID(e.BulkID) + ".txt"
	caption := n.cat.T(lang, "bulk.file_caption", "n", i18n.Number(lang, int64(delivered)))
	if _, err := up.SendDocumentUpload(ctx, e.TelegramID, name, bytes.NewReader(buf.Bytes()), int64(buf.Len()), caption); err != nil && !tg.IsBlocked(err) {
		n.log.Warn("the file of a bulk delivery was not sent", "chat", e.TelegramID, "err", err)
	}
}
