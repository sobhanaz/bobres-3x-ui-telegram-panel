package handler

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/aqayepardakhtfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/aqayepardakht"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

// enableAqayepardakht offers Aqayepardakht beside the stack's Zarinpal, pointed at a fake of the gateway, as
// the owner does by switching it on in the dashboard with its PIN.
func enableAqayepardakht(t *testing.T, w *world) *aqayepardakhtfake.Server {
	t.Helper()
	fake := aqayepardakhtfake.New()
	t.Cleanup(fake.Close)
	gw, err := aqayepardakht.New(aqayepardakht.Config{PIN: "Gm7QaV3xTz9LkR2WnD4P", BaseURL: fake.URL}) //nolint:gosec // a fake PIN for the test server
	if err != nil {
		t.Fatal(err)
	}
	zgw, err := zarinpal.New(zarinpal.Config{MerchantID: testenv.ZarinpalMerchant, BaseURL: w.stack.Zarinpal.URL})
	if err != nil {
		t.Fatal(err)
	}
	w.stack.Payments.SetGateways(zgw, gw)
	return fake
}

var aqStartPayRe = regexp.MustCompile(`/startpay/([0-9A-Za-z_-]+)`)

// aqPay is what the customer does on the pay page and at the bank: open our page, follow its link to the
// gateway (its transid is in the link), pay, and be sent back to the callback.
func aqPay(t *testing.T, fake *aqayepardakhtfake.Server, link string, ok bool) (back, callback string) {
	t.Helper()
	resp, err := http.Get(link) //nolint:gosec,noctx // test server
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m := aqStartPayRe.FindStringSubmatch(string(page))
	if m == nil || !strings.Contains(string(page), `referrerpolicy="origin"`) {
		t.Fatalf("the pay page has no link to the gateway, or one that hides the Referer: %s", page)
	}
	cb, _, err := fake.Pay(m[1], ok)
	if err != nil {
		t.Fatal(err)
	}
	return aqReturn(t, cb), cb
}

// aqReturn is the customer's browser arriving at the callback.
func aqReturn(t *testing.T, callback string) string {
	t.Helper()
	resp, err := http.Get(callback) //nolint:gosec,noctx // test server
	if err != nil {
		t.Fatal(err)
	}
	back, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(back)
}

// Aqayepardakht from the bot: offered beside the other gateways with its own button, opened from our own page
// (the gateway checks the Referer), verified when the customer comes back, and the order delivered once.
func TestAqayepardakhtPurchase(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	fake := enableAqayepardakht(t, w)
	u := w.person(611, "arash")
	u.text("/start")
	u.press("lang:en")
	u.press("buy")
	u.press("plan:")
	var found bool
	for _, row := range u.last().kb.InlineKeyboard {
		for _, b := range row {
			found = found || (b.Text == "💳 Pay online (Aqayepardakht)" && strings.HasPrefix(b.CallbackData, "pay:a:"))
			if len(b.CallbackData) > 64 {
				t.Fatalf("callback data over Telegram's 64 bytes: %q", b.CallbackData)
			}
		}
	}
	if !found {
		t.Fatalf("no Aqayepardakht button: %v", u.last().labels())
	}
	u.press("pay:a:")
	u.sees("Turn off your VPN")
	link := u.link("Open the payment page")
	if !strings.HasPrefix(link, w.stack.PaySite.URL+"/pay/") {
		t.Fatalf("pay link must be our own page (Aqayepardakht checks the Referer): %s", link)
	}
	if fake.Calls("create") != 1 {
		t.Fatalf("payments created %d transactions", fake.Calls("create"))
	}

	// Not paid yet: checking says so.
	u.press("chk:")
	u.toast("Not confirmed yet")

	back, callback := aqPay(t, fake, link, true)
	if !strings.Contains(back, "Payment received") {
		t.Fatalf("return page: %s", back)
	}
	u.eventuallySees("Your service is ready")
	u.press("chk:")
	u.sees("Payment received")
	if n := fake.Calls("verify"); n < 1 {
		t.Fatalf("the payment was never verified (%d verify calls)", n)
	}

	// The callback opened again changes nothing.
	if again := aqReturn(t, callback); !strings.Contains(again, "Payment received") {
		t.Fatalf("second return: %s", again)
	}
	w.drain()
	if n := u.count("Your service is ready"); n != 1 {
		t.Fatalf("the service was delivered %d times", n)
	}
}

// A payment the customer gave up at the bank is never delivered.
func TestAqayepardakhtCancelledAtTheBank(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	fake := enableAqayepardakht(t, w)
	u := w.person(612, "bita")
	u.text("/start")
	u.press("lang:en")
	u.press("buy")
	u.press("plan:")
	u.press("pay:a:")
	link := u.link("Open the payment page")
	back, _ := aqPay(t, fake, link, false)
	if strings.Contains(back, "Payment received") {
		t.Fatalf("a cancelled payment shown as received: %s", back)
	}
	u.press("chk:")
	if strings.Contains(u.last().text, "Your service is ready") {
		t.Fatal("a cancelled payment was delivered")
	}
	w.drain()
	if n := u.count("Your service is ready"); n != 0 {
		t.Fatalf("a cancelled payment delivered %d services", n)
	}
}

// A wallet top-up with Aqayepardakht: the same button letter, the top-up credited once.
func TestAqayepardakhtTopup(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	fake := enableAqayepardakht(t, w)
	u := w.person(613, "cyrus")
	u.text("/start")
	u.press("lang:en")
	u.press("wallet")
	u.press("wtop")
	u.press("wamt:200000")
	var found bool
	for _, row := range u.last().kb.InlineKeyboard {
		for _, b := range row {
			found = found || (b.Text == "💳 Pay online (Aqayepardakht)" && strings.HasPrefix(b.CallbackData, "wpay:a:200000:"))
			if len(b.CallbackData) > 64 {
				t.Fatalf("callback data over Telegram's 64 bytes: %q", b.CallbackData)
			}
		}
	}
	if !found {
		t.Fatalf("no Aqayepardakht top-up button: %v", u.last().labels())
	}
	u.press("wpay:a:")
	link := u.link("Open the payment page")
	if back, _ := aqPay(t, fake, link, true); !strings.Contains(back, "Payment received") {
		t.Fatalf("return page: %s", back)
	}
	u.eventuallySees("added to your wallet")
}
