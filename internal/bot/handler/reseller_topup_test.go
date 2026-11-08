package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
)

// A wallet top-up by card, end to end, for a customer, a reseller, and a reseller in debt: the receipt is taken,
// the owner is told and approves it, and the money reaches the wallet (a reseller's debt goes down by it).
func TestTopupByCardReachesTheWallet(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	for _, c := range []struct {
		name   string
		id     int64
		debt   int64 // a reseller who bought on credit first
		seller bool
	}{
		{"a customer", 5101, 0, false},
		{"a reseller", 5102, 0, true},
		{"a reseller in debt", 5103, 150_000, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			u := w.customer(c.id, "topup"+c.name[2:4])
			if c.seller {
				w.makeReseller(c.id, 0, 0, 500_000)
			}
			if c.debt > 0 {
				u.text("/menu")
				u.press("rs")
				u.press("rs:buy")
				u.press("rs:p:")
				u.press("rs:sk:")
				u.press("pay:w:")
				u.eventuallySees("Your service is ready")
			}
			before := w.balance(c.id)
			if before != -c.debt {
				t.Fatalf("balance before: %d", before)
			}
			u.text("/menu")
			u.press("wallet")
			u.press("wtop")
			u.press("wamt:100000")
			u.press("wpay:c:")
			u.sees("6037-9911-2222-3333")
			u.photo("receipt-"+c.name[2:4], "REF-778899")
			u.lastHas("Received")
			owner.eventuallySees("New payment to review")
			owner.press("adm:pi:")
			owner.press("adm:ok:")
			u.eventuallySees("was added to your wallet")
			if got := w.balance(c.id); got != before+100_000 {
				t.Fatalf("balance after the approval: %d, want %d", got, before+100_000)
			}
		})
	}
}

// startCardTopup opens a card top-up of 100,000 Toman and lets its receipt step close, as it does after
// state.TTL or another tap: the payment waits for its receipt, the bot no longer asks for it.
func startCardTopup(t *testing.T, w *world, u *person) {
	t.Helper()
	u.text("/menu")
	u.press("wallet")
	u.press("wtop")
	u.press("wamt:100000")
	u.press("wpay:c:")
	u.sees("Card-to-card payment")
	if err := w.h.state.Set(context.Background(), u.id, state.State{}); err != nil {
		t.Fatal(err)
	}
}

// A receipt sent after its step closed is no longer lost: the payments waiting for one are offered (with the
// reference their screen showed), and a tap sends it for review as the step would have, with the bank reference
// asked for when the caption had none. The owner approves it and the money reaches the wallet.
func TestLateReceiptIsOfferedItsPayment(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	rs := w.customer(5201, "late_rs")
	w.makeReseller(5201, 0, 0, 500_000)
	startCardTopup(t, w, rs)

	rs.photo("late-receipt", "BANK-4455")
	pick := rs.last()
	if !strings.Contains(pick.text, "Which payment is this receipt for") || !hasCallback(pick, "rcp:") {
		t.Fatalf("the offer: %q %v", pick.text, callbacks(pick))
	}
	labels := strings.Join(pick.labels(), "\n")
	if !strings.Contains(labels, "Top-up · 100,000") {
		t.Fatalf("the payment's button: %q", labels)
	}
	var intent string
	for _, cb := range callbacks(pick) {
		if id, ok := strings.CutPrefix(cb, "rcp:"); ok {
			intent = id
		}
	}
	if ref := paymentReference(intent); !strings.Contains(labels, ref) || rs.count(ref) == 0 {
		t.Fatalf("the reference %s on the button %q and on the payment screen", ref, labels)
	}
	rs.press("rcp:")
	rs.lastHas("Received")
	owner.eventuallySees("New payment to review")
	owner.press("adm:pi:")
	owner.sees("BANK-4455")
	owner.press("adm:ok:")
	rs.eventuallySees("was added to your wallet")
	if got := w.balance(5201); got != 100_000 {
		t.Fatalf("balance: %d", got)
	}

	// The same button again: the picture it was for is gone (a popup); with another picture kept, that payment no
	// longer waits, and nothing is sent for it.
	rs.tap("rcp:" + intent)
	rs.toast(w.h.cat.T("en", "error.expired"))
	startCardTopup(t, w, rs) // another top-up waits for its receipt
	rs.photo("another-picture", "BANK-1")
	if hasCallback(rs.last(), "rcp:"+intent) {
		t.Fatalf("a payment with its receipt sent was offered again: %v", callbacks(rs.last()))
	}
	rs.tap("rcp:" + intent)
	if !strings.Contains(rs.shown().text, "not waiting for a receipt any more") {
		t.Fatalf("a stale payment: %q", rs.shown().text)
	}

	// Without a caption the bank reference is asked for, as in the step itself.
	c := w.customer(5202, "late_c")
	startCardTopup(t, w, c)
	c.photo("late-receipt-2", "")
	c.press("rcp:")
	c.lastHas("bank tracking/reference number")
	c.text("998877")
	c.lastHas("Received")
	owner.eventuallySees("New payment to review")
}

// A picture with no payment waiting for a receipt is answered, with the way to pay, instead of the home screen.
func TestStrayPictureSaysHowToPay(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(5301, "stray")
	u.photo("not-a-receipt", "")
	m := u.last()
	if !strings.Contains(m.text, "no payment of yours is waiting for a receipt") || !hasCallback(m, "wtop") || !hasCallback(m, "support") {
		t.Fatalf("the answer: %q %v", m.text, callbacks(m))
	}
	u.press("wtop")
	u.sees("How much would you like to add?")
}

// The reseller panel leads to the wallet top-up.
func TestResellerPanelHasTopUp(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(5401, "panel_rs")
	w.makeReseller(5401, 0, 0, 500_000)
	rs.text("/menu")
	rs.press("rs")
	rs.lastButton("Top up wallet")
	if !hasCallback(rs.shown(), "wtop") {
		t.Fatalf("panel: %v", callbacks(rs.shown()))
	}
	rs.press("wtop")
	rs.sees("How much would you like to add?")
}

// In maintenance a late receipt for a payment under way still goes through (as the receipt step does); a picture
// with no payment waiting gets the maintenance text.
func TestLateReceiptInMaintenance(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(5501, "maint_late")
	startCardTopup(t, w, u)
	idle := w.customer(5502, "maint_idle")
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")

	u.photo("maint-receipt", "BANK-1")
	u.press("rcp:")
	u.lastHas("Received")
	idle.photo("maint-stray", "")
	idle.lastHas("maintenance")
}
