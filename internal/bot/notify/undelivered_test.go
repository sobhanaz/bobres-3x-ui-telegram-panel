package notify

import (
	"context"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc"
)

// linksCore is fakeCore that also has a service's links (no QR code: the message goes alone).
type linksCore struct{ fakeCore }

func (linksCore) GetSubscriptionLinks(context.Context, *corev1.GetSubscriptionLinksRequest, ...grpc.CallOption) (*corev1.SubscriptionLinks, error) {
	return &corev1.SubscriptionLinks{}, nil
}

func linksNotifier(t *testing.T) (*Notifier, *fakeTG) {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tgf := &fakeTG{}
	core := &linksCore{fakeCore{langs: map[int64]string{ownerTG: "fa", adminTG: "en"}}}
	return New(core, tgf, cat, settings.New(), []int64{ownerTG}, nil), tgf
}

const lateLine = "\n\n🙏 Sorry this took longer than usual: one of our servers was not answering."

// A refund staff wrote a note with carries the note, escaped, under the usual message; with or without the cashback
// it took back. A refund without one is as it always was (TestRefundThatTookCashbackBackSaysSo).
func TestRefundCarriesTheStaffNote(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	refund := events.WalletCreditedEvent{UserID: "u1", TelegramID: adminTG, OrderID: "o1", Amount: 10_000, Currency: "IRT", Balance: 10_000,
		Reason: events.CreditRefund, Note: "  The server <b>was</b> down & we moved you  "}
	if err := handleEvent(t, n, events.WalletCredited, refund); err != nil {
		t.Fatal(err)
	}
	want := "👛 An order was refunded: 10,000 Toman was added to your wallet.\nBalance: <b>10,000 Toman</b>\n\n" +
		"📝 Note from the store: The server &lt;b&gt;was&lt;/b&gt; down &amp; we moved you"
	if got := tgf.take(); len(got) != 1 || got[0].text != want || got[0].kb == nil {
		t.Fatalf("English:\n%+v\nwant\n%s", got, want)
	}

	refund.CashbackTaken = 250
	refund.TelegramID = ownerTG // reads Persian
	if err := handleEvent(t, n, events.WalletCredited, refund); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 1 || !strings.Contains(got[0].text, "کسر شد") ||
		!strings.HasSuffix(got[0].text, "\n\n📝 یادداشت فروشگاه: The server &lt;b&gt;was&lt;/b&gt; down &amp; we moved you") {
		t.Fatalf("Persian with cashback: %+v", got)
	}

	// Only a refund carries a note.
	other := refund
	other.Reason, other.CashbackTaken, other.TelegramID = events.CreditAdminAdjust, 0, adminTG
	if err := handleEvent(t, n, events.WalletCredited, other); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || strings.Contains(got[0].text, "Note from the store") {
		t.Fatalf("an adjustment: %+v", got)
	}
}

// A delivery the customer had been told is late says sorry under the usual message, after the config name; one on
// time does not.
func TestLateDeliverySaysSorry(t *testing.T) {
	n, tgf := linksNotifier(t)
	prov := events.SubscriptionProvisionedEvent{SubscriptionID: "sub-1", OrderID: "order-1", UserID: "u1", TelegramID: adminTG,
		SubscriptionLink: "https://sub.example.com/sub/x", ClientName: "ali1234", Late: true}
	if err := handleEvent(t, n, events.SubscriptionProvisioned, prov); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 1 || !strings.HasPrefix(got[0].text, "🎉 <b>Your service is ready!</b>") ||
		!strings.HasSuffix(got[0].text, "\n🔤 Config name: <code>ali1234</code>"+lateLine) {
		t.Fatalf("a late delivery: %+v", got)
	}
	prov.Late = false
	if err := handleEvent(t, n, events.SubscriptionProvisioned, prov); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || strings.Contains(got[0].text, "Sorry") {
		t.Fatalf("a delivery on time: %+v", got)
	}
	// A late service of a bulk is still not announced on its own.
	prov.Late, prov.BulkID = true, "bulk-1"
	if err := handleEvent(t, n, events.SubscriptionProvisioned, prov); err != nil || len(tgf.take()) != 0 {
		t.Fatalf("a bulk's service was announced: %v", err)
	}

	// A renewal and a traffic package, in English and in Persian; the reseller's name stays on top.
	ext := events.SubscriptionExtendedEvent{SubscriptionID: "00000000-0000-0000-0000-00000000abcd", OrderID: "o2", UserID: "u1", TelegramID: adminTG,
		Type: "renew", ExpiresAt: 1_800_000_000, TrafficTotalBytes: 50 << 30, Label: "Ali", Late: true}
	if err := handleEvent(t, n, events.SubscriptionExtended, ext); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || !strings.HasPrefix(got[0].text, "🏷 <b>Ali</b>\n✅ <b>Service #00abcd renewed.</b>") ||
		!strings.HasSuffix(got[0].text, lateLine) {
		t.Fatalf("a late renewal: %+v", got)
	}
	ext.Type, ext.TelegramID = "traffic_topup", ownerTG
	if err := handleEvent(t, n, events.SubscriptionExtended, ext); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || !strings.Contains(got[0].text, "حجم سرویس #00abcd افزایش یافت") ||
		!strings.HasSuffix(got[0].text, "\n\n🙏 بابت تأخیر عذر می\u200cخواهیم: یکی از سرورهای ما پاسخ نمی\u200cداد.") {
		t.Fatalf("a late traffic package in Persian: %+v", got)
	}
	ext.Late = false
	if err := handleEvent(t, n, events.SubscriptionExtended, ext); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || strings.Contains(got[0].text, "🙏") {
		t.Fatalf("a traffic package on time: %+v", got)
	}
}

// A reseller whose commission on a refunded undelivered order was taken back is told what and the balance after
// (a debt as such), never who the customer was; nothing taken, nothing said.
func TestCommissionTakenBackIsToldToTheReseller(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	e := events.CommissionReversedEvent{UserID: "r1", TelegramID: adminTG, OrderID: "o1", Credited: 15_000, Amount: 15_000, Currency: "IRT", Balance: 5_000}
	if err := handleEvent(t, n, events.CommissionReversed, e); err != nil {
		t.Fatal(err)
	}
	want := "↩️ An order of one of your customers was refunded before its service could be delivered, so the commission you earned on it was taken back: <b>15,000 Toman</b>.\nBalance: 5,000 Toman"
	got := tgf.take()
	if len(got) != 1 || got[0].chat != adminTG || got[0].text != want {
		t.Fatalf("English:\n%+v\nwant\n%s", got, want)
	}
	if got[0].kb == nil || len(got[0].kb.InlineKeyboard) != 1 || got[0].kb.InlineKeyboard[0][0].CallbackData != "home" {
		t.Fatalf("the keyboard: %+v", got[0].kb)
	}
	e.TelegramID, e.Balance = ownerTG, -20_000
	if err := handleEvent(t, n, events.CommissionReversed, e); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 || !strings.Contains(got[0].text, "پس گرفته شد: <b>۱۵٬۰۰۰ تومان</b>") ||
		!strings.Contains(got[0].text, "بدهی ۲۰٬۰۰۰ تومان") || strings.Contains(got[0].text, "-") {
		t.Fatalf("Persian, in debt: %+v", got)
	}
	e.Amount = 0
	if err := handleEvent(t, n, events.CommissionReversed, e); err != nil || len(tgf.take()) != 0 {
		t.Fatalf("nothing taken was announced: %v", err)
	}
}

// The wallet history and a reseller's statement name the entry of a commission taken back.
func TestCommissionReversalHasALedgerName(t *testing.T) {
	cat := i18n.MustLoad()
	for lang, want := range map[string]string{"en": "Commission taken back (refund)", "fa": "کسر پورسانت (برگشت سفارش)"} {
		if got := cat.T(lang, "ledger.commission_reversal"); got != want {
			t.Errorf("%s: %q, want %q", lang, got, want)
		}
	}
}
