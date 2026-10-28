package notify

import (
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// The customer is told what an order earned back, in their language, and the refund of such an order says what it took back.
func TestCashbackIsToldToTheCustomer(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	credit := events.WalletCreditedEvent{UserID: "u1", TelegramID: ownerTG, OrderID: "o1", Amount: 250, Currency: "IRT", Balance: 1_250, Reason: events.CreditCashback}
	if err := handleEvent(t, n, events.WalletCredited, credit); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 1 || got[0].chat != ownerTG {
		t.Fatalf("sent %+v", got)
	}
	// the owner of this fixture reads Persian
	if want := "🎁 بابت خریدتان <b>۲۵۰ تومان</b> کش\u200cبک گرفتید و به کیف پولتان اضافه شد.\nموجودی: ۱٬۲۵۰ تومان"; got[0].text != want {
		t.Errorf("in Persian:\n%s\nwant\n%s", got[0].text, want)
	}
	if got[0].kb == nil || len(got[0].kb.InlineKeyboard) != 1 {
		t.Errorf("the keyboard: %+v", got[0].kb)
	}
	credit.TelegramID = adminTG // reads English
	if err := handleEvent(t, n, events.WalletCredited, credit); err != nil {
		t.Fatal(err)
	}
	if got = tgf.take(); len(got) != 1 || got[0].text != "🎁 You earned <b>250 Toman</b> cashback on your purchase. It was added to your wallet.\nBalance: 1,250 Toman" {
		t.Errorf("in English: %+v", got)
	}
}

func TestRefundThatTookCashbackBackSaysSo(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	refund := events.WalletCreditedEvent{UserID: "u1", TelegramID: adminTG, OrderID: "o1", Amount: 10_000, Currency: "IRT", Balance: 10_000, Reason: events.CreditRefund, CashbackTaken: 250}
	if err := handleEvent(t, n, events.WalletCredited, refund); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 1 || got[0].text != "👛 An order was refunded: 10,000 Toman was added to your wallet, and the 250 Toman cashback it had earned was taken back.\nBalance: <b>10,000 Toman</b>" {
		t.Fatalf("in English: %+v", got)
	}
	refund.TelegramID = ownerTG
	if err := handleEvent(t, n, events.WalletCredited, refund); err != nil {
		t.Fatal(err)
	}
	if got = tgf.take(); len(got) != 1 || !strings.Contains(got[0].text, "۲۵۰ تومان") || !strings.Contains(got[0].text, "کسر شد") || !strings.Contains(got[0].text, "<b>۱۰٬۰۰۰ تومان</b>") {
		t.Fatalf("in Persian: %+v", got)
	}
	// A refund of an order that earned none is the message it always was.
	plain := refund
	plain.CashbackTaken = 0
	plain.TelegramID = adminTG
	if err := handleEvent(t, n, events.WalletCredited, plain); err != nil {
		t.Fatal(err)
	}
	if got = tgf.take(); len(got) != 1 || got[0].text != "👛 An order was refunded: 10,000 Toman was added to your wallet.\nBalance: <b>10,000 Toman</b>" {
		t.Fatalf("a plain refund: %+v", got)
	}
}

// The bulk's one message carries the cashback the services earned.
func TestBulkDeliveryTellsTheCashback(t *testing.T) {
	b, ready := bulkOf(3, "Family", allReady)
	e := readyEvent(b, enBuyer, ready)
	e.Cashback = 750
	n, _, tgf, _ := bulkNotifier(t, b, true)
	if err := handleEvent(t, n, events.BulkReady, e); err != nil {
		t.Fatal(err)
	}
	sent := tgf.take()
	if len(sent) != 1 || !strings.HasSuffix(sent[0].text, "\n\n🎁 You earned <b>750 Toman</b> cashback on these services. It was added to your wallet.") {
		t.Fatalf("English:\n%+v", sent)
	}
	// A bulk with something still being prepared keeps both footers.
	b2, ready2 := bulkOf(3, "Family", func(i int) bool { return i != 3 })
	e2 := readyEvent(b2, faBuyer, ready2)
	e2.Cashback = 500
	n2, _, tg2, _ := bulkNotifier(t, b2, true)
	if err := handleEvent(t, n2, events.BulkReady, e2); err != nil {
		t.Fatal(err)
	}
	sent = tg2.take()
	if len(sent) != 1 || !strings.Contains(sent[0].text, "کش\u200cبک") || !strings.Contains(sent[0].text, "<b>۵۰۰ تومان</b>") || !strings.Contains(sent[0].text, "طول") {
		t.Fatalf("Persian:\n%+v", sent)
	}
	// None earned: the message is as before.
	e.Cashback = 0
	n3, _, tg3, _ := bulkNotifier(t, b, true)
	if err := handleEvent(t, n3, events.BulkReady, e); err != nil {
		t.Fatal(err)
	}
	if sent = tg3.take(); len(sent) != 1 || strings.Contains(sent[0].text, "cashback") {
		t.Fatalf("no cashback:\n%+v", sent)
	}
}

// The daily summary has a line for the cashback given, only on a day that gave some.
func TestDailySummaryHasALineForTheCashback(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	day := busyDay()
	day.Currencies[0].Cashback = events.DailySummaryFlow{Count: 4, Amount: 12_500}
	if err := handleSummary(t, n, day); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 1 || !strings.Contains(got[0].text, "Cashback given: 4 · 12,500 Toman") {
		t.Fatalf("in English: %+v", got)
	}
	// only in the Toman block
	toman, usdt, _ := strings.Cut(got[0].text, "<b>USDT</b>")
	if !strings.Contains(toman, "Cashback given") || strings.Contains(usdt, "Cashback") {
		t.Errorf("the line is in the wrong block:\n%s", got[0].text)
	}
	day.TelegramID = ownerTG
	day.Language = "fa"
	if err := handleSummary(t, n, day); err != nil {
		t.Fatal(err)
	}
	if got = tgf.take(); len(got) != 1 || !strings.Contains(got[0].text, "کش\u200cبک پرداخت\u200cشده: ۴ · ۱۲٬۵۰۰ تومان") {
		t.Fatalf("in Persian: %+v", got)
	}
	// a day without any
	if err := handleSummary(t, n, busyDay()); err != nil {
		t.Fatal(err)
	}
	if got = tgf.take(); len(got) != 1 || strings.Contains(got[0].text, "Cashback") {
		t.Fatalf("a day without cashback: %+v", got)
	}
}
