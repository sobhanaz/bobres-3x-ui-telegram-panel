package handler

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	paydomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/trongrid"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tronfake"
)

const (
	// usdtStore is the store's TRON address (a valid mainnet address:
	// payments checks the checksum); usdtOtherContract is a token contract
	// that is not Tether's.
	usdtStore         = "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r"
	usdtOtherContract = "TSbS3MrjbYDWGmAjvqzwTVCpiqsu7uR9Fb"
)

// enableUSDT points the stack's payments service at a fake TronGrid, as
// cmd/payments does with the real one, and switches automatic USDT on with
// the store's address, as the owner does on the dashboard. Zarinpal stays
// offered as the stack had it.
func enableUSDT(t *testing.T, w *world) *tronfake.Server {
	t.Helper()
	ctx := context.Background()
	chain := tronfake.New()
	t.Cleanup(chain.Close)
	env, err := bcrypto.NewEnvelope([]byte("bot-usdt-test-master-key-0123456789ab"))
	if err != nil {
		t.Fatal(err)
	}
	newZarinpal := func(merchant string) (gateway.Gateway, error) {
		return zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: w.stack.Zarinpal.URL})
	}
	svc := w.stack.Payments
	svc.SetUnmatchedHold(0) // unmatched deposits are announced at once, not held for a manual proof
	if err := svc.ConfigureGateways(ctx, paydomain.GatewayOptions{Env: "dev", Envelope: env,
		Build: func(z paydomain.ZarinpalSettings) (gateway.Gateway, error) { return newZarinpal(z.MerchantID) },
		BuildChain: func(c paydomain.ChainSettings) (gateway.ChainSource, error) {
			cl, err := trongrid.New(trongrid.Config{APIKey: c.APIKey, Proxy: c.Proxy, BaseURL: chain.URL})
			if err != nil {
				return nil, err
			}
			return cl, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	zgw, err := newZarinpal(testenv.ZarinpalMerchant)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetGateways(zgw)
	if _, err := svc.SetGatewayConfig(ctx, paydomain.GatewayUpdate{Provider: usdtAuto, Enabled: true, Address: usdtStore}); err != nil {
		t.Fatal(err)
	}
	return chain
}

// scan runs one pass of the payments service's chain watcher (every 30
// seconds in production).
func (w *world) scan() {
	w.t.Helper()
	if _, err := w.stack.Payments.ScanChainOnce(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

var exactRe = regexp.MustCompile(`<code>([0-9]+\.[0-9]{6})</code> USDT`)

// usdtScreen reads the exact amount (micro-USDT) and the intent of the
// automatic USDT screen the user looks at.
func (p *person) usdtScreen() (int64, string) {
	p.w.t.Helper()
	m := p.last()
	got := exactRe.FindStringSubmatch(m.text)
	if got == nil {
		p.w.t.Fatalf("no exact amount in Latin digits inside <code>: %q", m.text)
	}
	micro, err := strconv.ParseInt(strings.Replace(got[1], ".", "", 1), 10, 64)
	if err != nil {
		p.w.t.Fatal(err)
	}
	var intent string
	for _, row := range m.kb.InlineKeyboard {
		for _, b := range row {
			if id, ok := strings.CutPrefix(b.CallbackData, "chk:"); ok {
				intent = id
			}
		}
	}
	if intent == "" || !strings.Contains(m.text, "<code>"+usdtStore+"</code>") {
		p.w.t.Fatalf("no Check button or no address in <code>: %q %+v", m.text, m.kb)
	}
	return micro, intent
}

// Automatic USDT from the bot: offered once priced, the screen shows the
// exact unique amount (Latin digits, copyable) and the address with the
// window and the warning; Check rests between taps; a fake token or another
// amount pays nothing and a wrong amount reaches the staff once; the exact
// amount pays the order once, confirmed as for the other gateways; and the
// paid transaction cannot be sent again as a manual proof.
func TestUSDTAutomaticPurchase(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	chain := enableUSDT(t, w)
	owner.text("/set payments.stars_rate 1500")
	u := w.person(801, "usha")
	u.text("/start")
	u.press("lang:en")

	// A Toman price needs the rate first.
	u.press("buy")
	u.press("plan:")
	if strings.Contains(strings.Join(u.last().labels(), "|"), "USDT TRC20 (automatic)") {
		t.Fatal("automatic USDT offered before the rate was set")
	}
	owner.text("/set payments.usdt_rate 60000")
	owner.sees("Saved")
	u.press("home")
	u.press("buy")
	u.press("plan:")
	menu := u.last()
	var auto bool
	for _, row := range menu.kb.InlineKeyboard {
		if len(row) > 2 {
			t.Fatalf("a row of %d buttons: %v", len(row), menu.labels())
		}
		for _, b := range row {
			auto = auto || (b.Text == "🪙 USDT TRC20 (automatic)" && strings.HasPrefix(b.CallbackData, "pay:u:"))
		}
	}
	if !auto {
		t.Fatalf("no automatic USDT button: %v", menu.labels())
	}

	u.press("pay:u:")
	amount, intent := u.usdtScreen()
	if amount <= 2_500_000 || amount > 2_500_999 { // 150,000 / 60,000 = 2.50, plus a unique offset
		t.Fatalf("exact amount %d", amount)
	}
	screen := u.last().text
	for _, sub := range []string{"This pays <b>150,000 Toman</b>", "on the TRON (TRC20) network", "Another network or another amount is not detected"} {
		if !strings.Contains(screen, sub) {
			t.Errorf("screen has no %q: %q", sub, screen)
		}
	}
	if !regexp.MustCompile(`within <b>4[45] minutes</b> \(by [0-9]{2}:[0-9]{2}\)`).MatchString(screen) {
		t.Errorf("no window: %q", screen)
	}

	// Not paid yet; tapping again at once does not ask again.
	u.press("chk:")
	u.toast("Not on the network yet")
	u.press("chk:")
	u.toast("Checked a moment ago")

	// A token that calls itself USDT, with the exact amount: nothing.
	chain.LeakOtherTokens(true)
	chain.Add(tronfake.Transfer{To: usdtStore, Token: usdtOtherContract, Value: strconv.FormatInt(amount, 10)})
	w.scan()
	// One micro-USDT more: nothing for the customer, an alert for the staff.
	wrong := chain.Pay(usdtStore, amount+1)
	w.scan()
	owner.eventuallySees("USDT arrived that matches no payment")
	owner.sees("<code>" + i18n.Exact(amount+1, "USDT") + "</code> USDT")
	owner.sees("<code>" + wrong + "</code>")
	if link := owner.link("Tronscan"); link != "https://tronscan.org/#/transaction/"+wrong {
		t.Fatalf("Tronscan link: %s", link)
	}
	if u.count("Payment confirmed") != 0 {
		t.Fatal("a fake token or a wrong amount paid the order")
	}

	// The exact amount, read twice: paid once.
	w.h.checkWait = 0
	paid := chain.Pay(usdtStore, amount)
	w.scan()
	w.scan()
	u.eventuallySees("Payment confirmed")
	u.eventuallySees("Your service is ready")
	u.tap("chk:" + intent)
	u.sees("Payment received. Your service is being prepared")
	w.drain()
	if n := u.count("Payment confirmed"); n != 1 {
		t.Fatalf("confirmed %d times", n)
	}
	if n := owner.count("USDT arrived that matches no payment"); n != 1 {
		t.Fatalf("the staff were told %d times", n)
	}

	// The paid transaction is not a manual proof for another purchase.
	owner.text("/set payments.usdt_trc20 " + usdtStore)
	owner.sees("Saved")
	u.press("home")
	u.press("buy")
	u.press("plan:")
	u.press("pay:x:")
	u.text(paid)
	u.sees("already been submitted")

	// Switched off after the menu was shown: the tap is told so.
	u.press("home")
	u.press("buy")
	u.press("plan:")
	if _, err := w.stack.Payments.SetGatewayConfig(context.Background(), paydomain.GatewayUpdate{Provider: usdtAuto, Address: usdtStore}); err != nil {
		t.Fatal(err)
	}
	u.press("pay:u:")
	u.toast("not set up yet")
}

// A wallet top-up with automatic USDT for a Persian speaker: Persian around
// the amount, Latin digits in it; the wallet is credited once. A payment
// whose time ran out is no longer shown as payable, the watcher closes it
// and the customer is told.
func TestUSDTTopupAndExpiry(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	chain := enableUSDT(t, w)
	owner.text("/set payments.usdt_rate 60000")
	owner.sees("Saved")
	w.h.checkWait = 0
	u := w.person(802, "")
	u.text("/start")
	u.press("lang:fa")
	u.press("wallet")
	u.press("wtop")
	u.press("wamt:200000")
	u.press("wpay:u:")
	amount, intent := u.usdtScreen()
	if amount <= 3_340_000 || amount > 3_340_999 { // 200,000 / 60,000 = 3.333…, rounded up to 3.34
		t.Fatalf("exact amount %d", amount)
	}
	screen := u.last().text
	for _, sub := range []string{"<b>۲۰۰٬۰۰۰ تومان</b>", "شبکه ترون (TRC20)"} {
		if !strings.Contains(screen, sub) {
			t.Errorf("screen has no %q: %q", sub, screen)
		}
	}
	if !regexp.MustCompile(`<b>۴[۴۵] دقیقه</b> دیگر \(تا ساعت [۰-۹]{2}:[۰-۹]{2}\)`).MatchString(screen) {
		t.Errorf("no window in Persian digits: %q", screen)
	}
	chain.Pay(usdtStore, amount)
	w.scan()
	u.eventuallySees("به کیف پول شما اضافه شد")
	u.tap("chk:" + intent)
	u.sees("✅ پرداخت دریافت شد. به کیف پول شما اضافه می\u200cشود.")

	// A second top-up, left unpaid past its window.
	u.press("home")
	u.press("wallet")
	u.press("wtop")
	u.press("wamt:100000")
	tapAgain := ""
	for _, row := range u.last().kb.InlineKeyboard {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "wpay:u:") {
				tapAgain = b.CallbackData
			}
		}
	}
	u.press("wpay:u:")
	_, late := u.usdtScreen()
	if _, err := w.stack.Store.DB().Exec(context.Background(),
		`UPDATE payments.payment_intents SET expires_at = now() - interval '10 minutes' WHERE id = $1`, late); err != nil {
		t.Fatal(err)
	}
	u.tap(tapAgain) // the same top-up again: its time is up
	u.sees("این پرداخت انجام نشد. مهلت پرداخت تمام شد.")
	if strings.Contains(u.last().text, "<code>") {
		t.Fatalf("an expired payment shown as payable: %q", u.last().text)
	}
	w.scan() // a complete read past the window closes it
	u.eventuallySees("پرداخت ۱۰۰٬۰۰۰ تومان شما تأیید نشد.\nدلیل: مهلت پرداخت تمام شد.")
	u.tap("chk:" + late)
	if text := u.last().text; !strings.Contains(text, "این پرداخت انجام نشد. مهلت پرداخت تمام شد.") {
		t.Fatalf("check after expiry: %q", text)
	}
}

// The screen is shown only from complete, well-formed details: anything
// else is "not set up", never a half-filled address or amount to pay.
func TestUSDTScreenNeedsItsDetails(t *testing.T) {
	ftg := newFakeTG()
	h := New(nil, ftg, state.NewMemory(), i18n.MustLoad(), nil, Config{}, nil)
	expires := strconv.FormatInt(time.Now().Add(45*time.Minute).Unix(), 10)
	good := func() *corev1.PaymentIntentRef {
		return &corev1.PaymentIntentRef{Id: "intent-1", Status: "pending", Provider: usdtAuto, OrderId: "order-1",
			Amount:        &commonv1.Money{Amount: 150_000, Currency: "IRT"},
			GatewayAmount: &commonv1.Money{Amount: 2_500_120, Currency: "USDT"},
			Details:       map[string]string{"pay_address": usdtStore, "network": "TRC20", "expires_at": expires}}
	}
	show := func(in *corev1.PaymentIntentRef) string {
		h.showUSDT(&req{ctx: context.Background(), h: h, from: &tg.User{ID: 7}, chatID: 7, lang: "en"}, in)
		return ftg.last(7).text
	}
	if got := show(good()); !strings.Contains(got, "<code>2.500120</code> USDT") || !strings.Contains(got, "<code>"+usdtStore+"</code>") {
		t.Fatalf("good details: %q", got)
	}
	for name, spoil := range map[string]func(*corev1.PaymentIntentRef){
		"amount in Toman":  func(in *corev1.PaymentIntentRef) { in.GatewayAmount.Currency = "IRT" },
		"no amount":        func(in *corev1.PaymentIntentRef) { in.GatewayAmount = nil },
		"zero amount":      func(in *corev1.PaymentIntentRef) { in.GatewayAmount.Amount = 0 },
		"no address":       func(in *corev1.PaymentIntentRef) { delete(in.Details, "pay_address") },
		"an ERC20 address": func(in *corev1.PaymentIntentRef) { in.Details["pay_address"] = "0x" + strings.Repeat("ab", 20) },
		"markup":           func(in *corev1.PaymentIntentRef) { in.Details["pay_address"] = "T<b>" + strings.Repeat("a", 30) },
		"another network":  func(in *corev1.PaymentIntentRef) { in.Details["network"] = "ERC20" },
		"no network":       func(in *corev1.PaymentIntentRef) { delete(in.Details, "network") },
		"no window":        func(in *corev1.PaymentIntentRef) { delete(in.Details, "expires_at") },
		"a broken window":  func(in *corev1.PaymentIntentRef) { in.Details["expires_at"] = "soon" },
	} {
		in := good()
		spoil(in)
		if got := show(in); got != h.cat.T("en", "pay.not_configured") {
			t.Errorf("%s: %q", name, got)
		}
	}
	late := good()
	late.Details["expires_at"] = strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10)
	if got := show(late); !strings.Contains(got, "The time to pay ran out") || strings.Contains(got, "<code>") {
		t.Fatalf("past its window: %q", got)
	}
}

// A TXID sent as a manual proof that then paid an automatic payment on
// chain cannot be approved as well: the reviewer is told why and rejects it.
func TestUSDTTXIDUsedTwiceIsRefusedAtReview(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	chain := enableUSDT(t, w)
	owner.text("/set payments.usdt_rate 60000")
	owner.text("/set payments.usdt_trc20 " + usdtStore)
	owner.sees("Saved")
	u := w.person(803, "tara")
	u.text("/start")
	u.press("lang:en")
	u.press("buy")
	u.press("plan:")
	u.press("pay:u:")
	amount, _ := u.usdtScreen()
	txid := chain.Pay(usdtStore, amount)

	// The same transfer, also sent as the proof of a manual payment.
	u.press("home")
	u.press("buy")
	u.press("plan:")
	u.press("pay:x:")
	u.text(txid)
	u.sees("Received")
	owner.sees("New payment to review from @tara")

	w.scan() // the exact amount pays the automatic payment
	u.eventuallySees("Payment confirmed")
	owner.press("adm:pend")
	owner.sees(txid)
	owner.press("adm:ok:")
	owner.toast("already paid another payment")
	owner.press("adm:no:")
	owner.press("adm:nr:2:")
	owner.toast("Rejected")
	w.drain()
	if n := u.count("Payment confirmed"); n != 1 {
		t.Fatalf("confirmed %d times", n)
	}
}
