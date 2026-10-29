package notify

import (
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// A prize won on the wheel is announced where the customer spun (the bot, or the Mini App), with the balance: the credit event sends no second
// message, as a gift code's does not.
func TestWheelPrizeSendsNoSecondMessage(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	for _, tg := range []int64{adminTG, ownerTG} { // one who reads English, one Persian
		prize := events.WalletCreditedEvent{UserID: "u1", TelegramID: tg, Amount: 5_000, Currency: "IRT", Balance: 25_000, Reason: events.CreditWheelPrize}
		if err := handleEvent(t, n, events.WalletCredited, prize); err != nil {
			t.Fatal(err)
		}
		if got := tgf.take(); len(got) != 0 {
			t.Fatalf("a wheel prize sent %+v", got)
		}
	}
	// Any other credit still does.
	other := events.WalletCreditedEvent{UserID: "u1", TelegramID: adminTG, Amount: 5_000, Currency: "IRT", Balance: 25_000, Reason: events.CreditAdminAdjust}
	if err := handleEvent(t, n, events.WalletCredited, other); err != nil {
		t.Fatal(err)
	}
	if got := tgf.take(); len(got) != 1 {
		t.Fatalf("an adjustment sent %+v", got)
	}
}
