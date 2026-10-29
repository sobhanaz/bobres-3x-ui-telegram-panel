package notify

import (
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// A reseller who reaches a tier of the month's target is told, in their language, with the count, the tier and the balance after the bonus (a
// reseller on credit may still owe: that is a debt, never a minus).
func TestResellerIsToldTheTargetBonus(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	bonus := events.WalletCreditedEvent{UserID: "u1", TelegramID: adminTG, Amount: 50_000, Currency: "IRT", Balance: 120_000, Reason: events.CreditTargetBonus,
		TargetSales: 12, TargetMonth: "2026-10", TargetThreshold: 10}
	if err := handleEvent(t, n, events.WalletCredited, bonus); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	want := "🎯 <b>Monthly target reached!</b>\nSold this month: 12 · tier: 10\nThe bonus, <b>50,000 Toman</b>, was added to your wallet.\nBalance: 120,000 Toman"
	if len(got) != 1 || got[0].chat != adminTG || got[0].text != want {
		t.Fatalf("in English: %+v", got)
	}
	bonus.TelegramID, bonus.Balance = ownerTG, -80_000 // reads Persian, still in debt
	if err := handleEvent(t, n, events.WalletCredited, bonus); err != nil {
		t.Fatal(err)
	}
	got = tgf.take()
	if len(got) != 1 || !strings.Contains(got[0].text, "به هدف ماهانه رسیدید") || !strings.Contains(got[0].text, "فروش این ماه: ۱۲ · پله: ۱۰") ||
		!strings.Contains(got[0].text, "<b>۵۰٬۰۰۰ تومان</b>") || !strings.Contains(got[0].text, "موجودی: بدهی ۸۰٬۰۰۰ تومان") || strings.Contains(got[0].text, "-") {
		t.Fatalf("in Persian: %+v", got)
	}
	// an older core's event without the count says nothing wrong: the tier is just 0
	bonus.TargetSales, bonus.TargetThreshold, bonus.TelegramID = 0, 0, adminTG
	if err := handleEvent(t, n, events.WalletCredited, bonus); err != nil {
		t.Fatal(err)
	}
	if got = tgf.take(); len(got) != 1 || !strings.Contains(got[0].text, "<b>50,000 Toman</b>") {
		t.Fatalf("without the count: %+v", got)
	}
}
