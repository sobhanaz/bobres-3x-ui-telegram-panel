package handler

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

// licenseServer is the license server's admin API as far as the bot's flow needs it: it verifies every signature,
// makes each ref work once, and can be told to be down.
type licenseServer struct {
	srv  *httptest.Server
	pub  ed25519.PublicKey
	mu   sync.Mutex
	seq  int
	refs map[string]coredomain.VendorLicenseInfo
	down bool
}

func newLicenseServer(t *testing.T) (*licenseServer, string) {
	t.Helper()
	priv, pubS, err := license.NewAdminKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := license.ParseAdminPublicKey(pubS)
	if err != nil {
		t.Fatal(err)
	}
	ls := &licenseServer{pub: pub, refs: map[string]coredomain.VendorLicenseInfo{}}
	ls.srv = httptest.NewTLSServer(http.HandlerFunc(ls.serve))
	t.Cleanup(ls.srv.Close)
	return ls, priv
}

func (ls *licenseServer) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if err := license.AdminVerify(ls.pub, r.Method, r.URL.RequestURI(), r.Header.Get(license.AdminHeaderTS),
		r.Header.Get(license.AdminHeaderNonce), r.Header.Get(license.AdminHeaderSig), body, time.Now()); err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Ref, ID, Tier string
		Months        int
	}
	_ = json.Unmarshal(body, &req)
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.down {
		http.Error(w, `{"error":"internal"}`, http.StatusServiceUnavailable)
		return
	}
	if prev, ok := ls.refs[req.Ref]; ok {
		prev.Key, prev.Replayed = "", true
		_ = json.NewEncoder(w).Encode(prev)
		return
	}
	ends := license.AddMonths(time.Now(), req.Months)
	v := coredomain.VendorLicenseInfo{ID: fmt.Sprintf("lic_bot%04d", ls.seq+1), Tier: req.Tier, Status: "active", EndsAt: &ends}
	if strings.HasSuffix(r.URL.Path, "/create") {
		ls.seq++
		v.Key = fmt.Sprintf("BOBRES-BOT-KEY-%04d", ls.seq)
	} else {
		v.ID = req.ID
	}
	stored := v
	stored.Key = ""
	ls.refs[req.Ref] = stored
	_ = json.NewEncoder(w).Encode(v)
}

func (ls *licenseServer) made() int { ls.mu.Lock(); defer ls.mu.Unlock(); return ls.seq }

// licenseWorld is a bot whose store sells licenses to a fake license server.
func licenseWorld(t *testing.T) (*world, *licenseServer, *person) {
	t.Helper()
	ls, priv := newLicenseServer(t)
	w := newWorldOpts(t, []int64{ownerID}, testenv.WithDomainConfig(func(c *coredomain.Config) {
		c.VendorTransport = coredomain.DirectVendorTransport(ls.srv.URL, ls.srv.Client())
		yes := true
		c.PrivateFeaturesOverride = &yes
	}))
	owner := w.customer(ownerID, "owner")
	ctx := context.Background()
	if err := w.stack.Domain.SetVendorConfig(ctx, w.storeUser(ownerID), priv); err != nil {
		t.Fatal(err)
	}
	// One wallet, in Toman: the licenses are priced in USDT and charged at the store's rate (the owner's own: 100,000 Toman a USDT).
	if err := w.stack.Domain.SetSetting(ctx, w.storeUser(ownerID), "payments.usdt_rate", strconv.Itoa(licenseTestRate)); err != nil {
		t.Fatal(err)
	}
	return w, ls, owner
}

// licenseTestRate is the Toman per USDT of the license tests: fundUSDT and usdtBalance write the Toman wallet in USDT units at this rate.
const licenseTestRate = 100_000

func (w *world) fundUSDT(telegramID, amount int64) {
	w.t.Helper()
	u := w.storeUser(telegramID)
	err := w.stack.Store.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := w.stack.Store.Credit(context.Background(), tx, u.ID, "IRT", amount*licenseTestRate/1_000_000,
			&corestore.LedgerEntry{Kind: "topup", IdempotencyKey: fmt.Sprintf("fundusdt-%d-%d", telegramID, time.Now().UnixNano())})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) usdtBalance(telegramID int64) int64 {
	w.t.Helper()
	u := w.storeUser(telegramID)
	wl, err := w.stack.Store.GetWallet(context.Background(), w.stack.Store.Conn(), u.ID, "IRT")
	if err != nil {
		w.t.Fatal(err)
	}
	return wl.Balance * 1_000_000 / licenseTestRate
}

func callbackWithPrefix(m *msg, prefix string) string {
	for _, c := range callbacks(m) {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	panic("no button " + prefix)
}

func hasButton(m *msg, data string) bool {
	for _, row := range m.kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == data || strings.HasPrefix(b.CallbackData, data+":") {
				return true
			}
		}
	}
	return false
}

func TestLicenseSalesFlow(t *testing.T) {
	w, ls, owner := licenseWorld(t)
	buyer := w.customer(7101, "buyer")

	// Off by default: no button, and a tap on an old one is told so.
	buyer.text("/menu")
	if hasButton(buyer.shown(), "lic") {
		t.Fatalf("a store that does not sell licenses shows the button: %v", buyer.shown().labels())
	}
	owner.text("/set vendor.enabled true")
	owner.sees("Saved")
	buyer.text("/menu")
	if !hasButton(buyer.shown(), "lic") {
		t.Fatalf("no licenses button once sales are on: %v", buyer.shown().labels())
	}

	// The offers: both tiers and both terms, with the owner's prices.
	buyer.tap("lic")
	for _, want := range []string{"Standard · 1 month", "Standard · 12 months", "Pro · 1 month", "Pro · 12 months"} {
		buyer.lastButton(want)
	}
	buyer.shownHas("BOBRES licenses")

	// A wallet that is short is told how much to top up, and is offered no Pay button.
	buyer.tap("lic:b:standard:1")
	buyer.shownHas("short")
	if hasButton(buyer.shown(), "lic:y") {
		t.Fatal("a Pay button for an empty wallet")
	}
	if ls.made() != 0 {
		t.Fatal("the license server was asked without payment")
	}

	// Funded, the buyer pays; the key arrives in the same message.
	w.fundUSDT(7101, 20*usdtUnit)
	buyer.tap("lic:b:standard:1")
	pay := callbackWithPrefix(buyer.shown(), "lic:y:standard:1:")
	buyer.tap(pay)
	buyer.shownHas("BOBRES-BOT-KEY-0001")
	buyer.shownHas("sudo bobres license activate")
	if got := w.usdtBalance(7101); got != 16*usdtUnit {
		t.Fatalf("balance %d, want %d", got, 16*usdtUnit)
	}
	// Tapping the same Pay button again (an old message, a retry) is the same purchase: no second license or charge.
	buyer.tap(pay)
	if got := w.usdtBalance(7101); got != 16*usdtUnit {
		t.Fatalf("a double tap charged again: %d", got)
	}
	if ls.made() != 1 {
		t.Fatalf("%d licenses made, want 1", ls.made())
	}

	// My licenses lists it, shows its key again, and renews it.
	buyer.tap("lic:my")
	buyer.shownHas("Standard")
	buyer.press("lic:k:")
	buyer.shownHas("BOBRES-BOT-KEY-0001")
	buyer.tap("lic:my")
	buyer.press("lic:r:")
	buyer.shownHas("Price")
	buyer.press("lic:ry:")
	buyer.shownHas("Renewed")
	if got := w.usdtBalance(7101); got != 12*usdtUnit {
		t.Fatalf("balance after the renewal %d, want %d", got, 12*usdtUnit)
	}

	// Another customer sees nothing of it.
	other := w.customer(7102, "other")
	other.text("/menu")
	other.tap("lic")
	other.tap("lic:my")
	other.shownHas("no licenses yet")
}

func TestLicenseSalesWhenTheServerIsDown(t *testing.T) {
	w, ls, owner := licenseWorld(t)
	owner.text("/set vendor.enabled true")
	buyer := w.customer(7111, "buyer")
	w.fundUSDT(7111, 20*usdtUnit)
	ls.mu.Lock()
	ls.down = true
	ls.mu.Unlock()

	buyer.text("/menu")
	buyer.tap("lic")
	buyer.tap("lic:b:pro:1")
	buyer.press("lic:y:pro:1:")
	// Paid, and told the key is on its way; the money is taken, the license is not yet made.
	buyer.shownHas("Payment received")
	if got := w.usdtBalance(7111); got != 8*usdtUnit {
		t.Fatalf("balance %d, want %d", got, 8*usdtUnit)
	}
	// The server comes back and core's worker settles the order: the key is in My licenses.
	ls.mu.Lock()
	ls.down = false
	ls.mu.Unlock()
	w.stack.Store.DB().Exec(context.Background(), `UPDATE core.vendor_orders SET next_attempt_at = now() - interval '1 minute'`) //nolint:errcheck // test
	if n, err := w.stack.Domain.SettleVendorOrders(context.Background()); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	buyer.tap("lic:my")
	buyer.shownHas("Pro")
	buyer.press("lic:k:")
	buyer.shownHas("BOBRES-BOT-KEY-0001")
}

func TestLicensesAreOffInAStoreThatDoesNotSellThem(t *testing.T) {
	w := newWorld(t)
	u := w.customer(7121, "buyer")
	u.tap("lic")
	u.toast("not for sale")
}

// usdtUnit is one USDT in minor units.
const usdtUnit = 1_000_000

// A buyer short of money taps a payment method for the shortfall: the bot tells them the license will be bought by itself when the payment
// lands, core records the intent, and when the money arrives the license is bought and its key sent, with no second visit.
func TestLicenseIsBoughtByItselfAfterTheTopup(t *testing.T) {
	w, ls, owner := licenseWorld(t)
	owner.text("/set vendor.enabled true")
	owner.text("/set payments.usdt_trc20 TNPeeaaFB7K9cmo4uQpcU32zGK8G1NYqeL")
	buyer := w.customer(7111, "intent_buyer")
	ctx := context.Background()

	buyer.tap("lic:b:standard:1")
	buyer.shownHas("short")
	buyer.tap(callbackWithPrefix(buyer.shown(), "lpay:x:"))
	buyer.sees("I will buy")
	buyer.sees("TNPeeaaFB7K9cmo4uQpcU32zGK8G1NYqeL") // the payment instructions follow
	u := w.storeUser(7111)
	var n int
	if err := w.stack.Store.DB().QueryRow(ctx, `SELECT count(*) FROM core.vendor_intents WHERE user_id = $1 AND tier = 'standard' AND months = 1`, u.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the intent: %d %v", n, err)
	}
	if ls.made() != 0 {
		t.Fatal("bought before the payment landed")
	}

	// The payment lands (a staff approval credits the wallet): the next look of the worker buys, and the key arrives in the chat.
	w.fundUSDT(7111, 4*usdtUnit)
	if made, err := w.stack.Domain.SettleVendorIntents(ctx); err != nil || made != 1 {
		t.Fatalf("settle: %d %v", made, err)
	}
	buyer.eventuallySees("Your Standard license is ready")
	if !strings.Contains(buyer.lastWith("Your Standard license is ready").text, "BOBRES-BOT-KEY-0001") {
		t.Fatalf("no key in %q", buyer.lastWith("Your Standard license is ready").text)
	}
	if got := w.usdtBalance(7111); got != 0 || ls.made() != 1 {
		t.Fatalf("balance %d, licenses %d", got, ls.made())
	}
}

// One wallet, in Toman: the wallet screen shows the balance with its USDT equivalent and the live rate, a top-up can be asked in USDT (it is
// added in Toman at the rate), the license screens show Toman with the USDT beside it, and a USDT payment that arrives is credited as Toman.
func TestOneWalletInTomanWithUsdtBesideIt(t *testing.T) {
	w, _, owner := licenseWorld(t)
	owner.text("/set vendor.enabled true")
	owner.text("/set payments.usdt_trc20 TNPeeaaFB7K9cmo4uQpcU32zGK8G1NYqeL")
	buyer := w.customer(7131, "one_wallet")
	ctx := context.Background()

	// 4 USDT at 100,000 Toman: 400,000 Toman; the wallet shows the Toman balance, its USDT and the rate.
	w.fundUSDT(7131, 4*usdtUnit)
	buyer.tap("wallet")
	buyer.shownHas("400,000")
	buyer.shownHas("USDT rate now")
	buyer.shownHas("100,000")
	buyer.shownHas("4 USDT")

	// The licenses are priced in Toman at today's rate, with the USDT price beside it and the rate in the title.
	buyer.tap("lic")
	buyer.shownHas("100,000")
	buyer.tap("lic:b:pro:1")
	buyer.shownHas("1,200,000") // 12 USDT
	buyer.shownHas("≈ 12 USDT")
	buyer.shownHas("short")

	// A top-up in USDT: amounts, then the ways to pay in USDT; the Toman it makes is told before paying.
	buyer.tap("wtop")
	buyer.tap("wusdt")
	buyer.shownHas("Top up in USDT")
	buyer.tap("wusa:10")
	buyer.shownHas("1,000,000") // 10 USDT at 100,000
	buyer.tap(callbackWithPrefix(buyer.shown(), "wpu:x:10:"))
	buyer.sees("TNPeeaaFB7K9cmo4uQpcU32zGK8G1NYqeL")
	var amount, cur string
	if err := w.stack.Store.DB().QueryRow(ctx, `SELECT amount::text, currency FROM payments.intents ORDER BY created_at DESC LIMIT 1`).Scan(&amount, &cur); err == nil {
		if amount != "10000000" || cur != "USDT" {
			t.Fatalf("the payment: %s %s", amount, cur)
		}
	}
	// A custom amount, refused out of range, accepted inside it.
	buyer.tap("wtop")
	buyer.tap("wusdt")
	buyer.tap("wusc")
	buyer.text("0")
	buyer.sees("whole number of USDT")
	buyer.text("25")
	buyer.shownHas("25 USDT")
}
