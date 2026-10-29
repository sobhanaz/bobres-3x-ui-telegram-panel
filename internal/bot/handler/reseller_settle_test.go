package handler

import (
	"strings"
	"testing"
)

// The Settle / Top up button under a message to resellers ("wallet:m") opens the reseller's wallet in a new message: the reminder above it,
// with their debt, stays as it was. A plain Wallet button still turns the screen it is on into the wallet.
func TestSettleButtonKeepsTheMessage(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7300000001, "shop")
	w.makeReseller(7300000001, 10, 5, 500_000)
	u.text("/menu")
	reminder := u.last()
	text := reminder.text
	n := len(w.tg.all(u.id))
	u.tap("wallet:m")
	if len(w.tg.all(u.id)) != n+1 || reminder.text != text {
		t.Fatalf("the Settle button replaced the message above it: %d messages, %q", len(w.tg.all(u.id))-n, reminder.text)
	}
	u.lastHas("👛 <b>Wallet</b>")
	if !strings.Contains(u.last().text, "500,000 Toman") {
		t.Fatalf("the reseller's credit is not on the wallet: %q", u.last().text)
	}
	if cb := callbacks(u.last()); len(cb) == 0 || cb[0] != "wtop" {
		t.Fatalf("no top-up under the wallet: %v", cb)
	}

	n = len(w.tg.all(u.id))
	u.press("home")
	u.press("wallet")
	if len(w.tg.all(u.id)) != n {
		t.Fatalf("the Wallet button sent a new message: %d", len(w.tg.all(u.id))-n)
	}
}
