package domain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// quoteClock is the clock the quotes expire by in these tests.
type quoteClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *quoteClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *quoteClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// quoteEnv is a store with a plan at 4 USDT, a live USDT price of 280,000 Toman and a 5% margin (so the store's rate is 294,000: 4 USDT cost 1,176,000
// Toman), and a clock the test moves.
type quoteEnv struct {
	svc   *Service
	s     *store.Store
	owner *store.User
	plan  *store.Plan
	clock *quoteClock
}

func seedUsdtPlan(t *testing.T, s *store.Store, price int64) *store.Plan {
	t.Helper()
	days, traffic := int32(30), int64(50<<30)
	p := &store.Plan{NameI18n: map[string]string{"en": "Pro USDT"}, Kind: "both", Price: price, Currency: "USDT", Enabled: true,
		DurationDays: &days, TrafficBytes: &traffic}
	if err := s.UpsertPlan(context.Background(), s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func newQuoteEnv(t *testing.T) *quoteEnv {
	t.Helper()
	svc, s := testService(t)
	e := &quoteEnv{svc: svc, s: s, clock: &quoteClock{t: time.Now()}}
	svc.cfg.Now = e.clock.now
	e.owner = staffMember(t, s, 9701, "owner")
	setRate(t, svc, e.owner, "0", "5", 280_000)
	e.plan = seedUsdtPlan(t, s, 4*usdt)
	return e
}

func (e *quoteEnv) order(t *testing.T, u *store.User, key string) *store.Order {
	t.Helper()
	o, err := e.svc.CreateOrder(context.Background(), CreateOrderParams{UserID: u.ID, PlanID: e.plan.ID, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// customer is a new customer with balance Toman in the wallet.
func (e *quoteEnv) customer(t *testing.T, tg int64, balance int64) *store.User {
	t.Helper()
	u := seedUser(t, e.s, tg)
	if balance > 0 {
		fund(t, e.s, u.ID, balance)
	}
	return u
}

func (e *quoteEnv) stored(t *testing.T, id string) *store.Order {
	t.Helper()
	o, err := e.s.GetOrder(context.Background(), e.s.Conn(), id)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (e *quoteEnv) ledgerCount(t *testing.T, userID, kind, currency string) (n int, sum int64) {
	t.Helper()
	if err := e.s.DB().QueryRow(context.Background(), `SELECT count(*), COALESCE(sum(amount), 0) FROM core.ledger_entries
		WHERE user_id = $1 AND kind = $2 AND currency = $3`, userID, kind, currency).Scan(&n, &sum); err != nil {
		t.Fatal(err)
	}
	return n, sum
}

// untouched checks that an order still waits for payment exactly as it was made: in USDT, with no list price.
func (e *quoteEnv) untouched(t *testing.T, id string) {
	t.Helper()
	o := e.stored(t, id)
	if o.Status != "awaiting_payment" || o.Amount != 4*usdt || o.Currency != "USDT" || o.ListAmount != nil || o.ListCurrency != nil || o.Rate != nil {
		t.Fatalf("the order changed without being paid: %+v", o)
	}
}

// A plan priced in USDT is paid from the Toman wallet at a quote: the customer is charged the plan's USDT price at the store's rate (the live price
// plus the margin, rounded up) in Toman, the order keeps the USDT price and the rate, and the books show one Toman purchase and nothing in USDT.
func TestUsdtPlanIsPaidFromTheWalletAtAQuote(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9702, 2_000_000)
	o := e.order(t, u, "q-1")
	if o.Currency != "USDT" || o.Amount != 4*usdt || o.Status != "awaiting_payment" {
		t.Fatalf("the order is listed in USDT: %+v", o)
	}

	q, err := e.svc.QuoteWalletPayment(ctx, o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q.OrderID != o.ID || q.ListAmount != 4*usdt || q.ListCurrency != "USDT" || q.Rate != 294_000 || q.Amount != 1_176_000 ||
		q.Balance != 2_000_000 || q.CreditLimit != 0 || !q.Enough || q.Token == "" {
		t.Fatalf("the quote: %+v", q)
	}
	if want := e.clock.now().Add(WalletQuoteTTL).Unix(); q.ExpiresAt.Unix() != want {
		t.Fatalf("the quote holds until %v, want %v", q.ExpiresAt, time.Unix(want, 0))
	}
	e.untouched(t, o.ID) // a quote changes nothing

	paid, err := e.svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, q.Token)
	if err != nil {
		t.Fatal(err)
	}
	if paid.Status != "paid" || paid.Amount != 1_176_000 || paid.Currency != "IRT" ||
		paid.ListAmount == nil || *paid.ListAmount != 4*usdt || paid.ListCurrency == nil || *paid.ListCurrency != "USDT" || paid.Rate == nil || *paid.Rate != 294_000 {
		t.Fatalf("the paid order: %+v", paid)
	}
	got := e.stored(t, o.ID)
	if got.Status != "paid" || got.Amount != 1_176_000 || got.Currency != "IRT" || got.ListAmount == nil || *got.ListAmount != 4*usdt ||
		got.ListCurrency == nil || *got.ListCurrency != "USDT" || got.Rate == nil || *got.Rate != 294_000 {
		t.Fatalf("the stored order: %+v", got)
	}

	if bal, sum, rows := ledger(t, e.s, u.ID); bal != 824_000 || sum != 824_000 || rows != 2 {
		t.Fatalf("Toman wallet: balance %d, ledger %d in %d entries; want 824,000 in 2 (the top-up and the purchase)", bal, sum, rows)
	}
	if n, sum := e.ledgerCount(t, u.ID, "purchase", "IRT"); n != 1 || sum != -1_176_000 {
		t.Fatalf("purchase entries: %d of %d", n, sum)
	}
	var usdtRows int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.ledger_entries WHERE user_id = $1 AND currency = 'USDT'`, u.ID).Scan(&usdtRows); err != nil || usdtRows != 0 {
		t.Fatalf("the USDT wallet moved: %d entries (%v)", usdtRows, err)
	}
	entry, err := e.s.LedgerByKey(ctx, e.s.Conn(), "purchase:"+o.ID)
	if err != nil || entry.Kind != "purchase" || entry.RefType == nil || *entry.RefType != "order" || entry.RefID == nil || *entry.RefID != o.ID || entry.Amount != -1_176_000 {
		t.Fatalf("the ledger entry: %+v %v", entry, err)
	}

	evs := outboxEvents(t, e.s, events.OrderPaid)
	var ev events.OrderPaidEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &ev) != nil || ev.OrderID != o.ID || ev.Amount != 1_176_000 || ev.Currency != "IRT" || ev.Source != events.PaidByWallet {
		t.Fatalf("order.paid.v1: %s", evs)
	}
	// The service is delivered like any other.
	if worked, err := e.svc.NewProvisionWorker(&fakeProvisioner{}, nil).ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provisioning: %v %v", worked, err)
	}
}

// The price is the license price's: one function, one rounding. A price with decimals rounds up to a whole Toman; the live price's margin is rounded up
// too; the manual rate stands when the live one is off.
func TestWalletQuoteUsesTheLicenseRateAndRounding(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9703, 0)
	tier := &VendorTier{Name: "t", Currency: "USDT"}
	for name, c := range map[string]struct {
		manual, margin string
		live           int64
		price          int64
		wantRate       int64
		wantToman      int64
	}{
		"the margin on the live price":     {"0", "5", 280_000, 4 * usdt, 294_000, 1_176_000},
		"a margin that does not divide":    {"0", "3", 281_234, 4 * usdt, 289_672, 1_158_688},
		"decimals round up":                {"271600", "0", 0, 2_500_731, 271_600, 679_199},
		"a millionth of a USDT is 1 Toman": {"271600", "0", 0, 1, 271_600, 1},
		"the manual rate when live is off": {"300000", "5", 0, 2 * usdt, 300_000, 600_000},
	} {
		t.Run(name, func(t *testing.T) {
			// Each case sets its own rates; a previous case's live price stays in the settings, so the manual cases switch the live rate off.
			auto := "true"
			if c.live == 0 {
				auto = "false"
			}
			if err := e.svc.SetSetting(ctx, e.owner, SettingUSDTAuto, auto); err != nil {
				t.Fatal(err)
			}
			setRate(t, e.svc, e.owner, c.manual, c.margin, c.live)
			plan := seedUsdtPlan(t, e.s, c.price)
			o, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "r-" + name})
			if err != nil {
				t.Fatal(err)
			}
			q, err := e.svc.QuoteWalletPayment(ctx, o.ID, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if q.Rate != c.wantRate || q.Amount != c.wantToman {
				t.Fatalf("quote: %d at %d, want %d at %d", q.Amount, q.Rate, c.wantToman, c.wantRate)
			}
			// What a license of the same price is charged, by the function the licenses use.
			toman, rate, err := e.svc.VendorToman(ctx, tier, c.price)
			if err != nil || toman != q.Amount || rate != q.Rate {
				t.Fatalf("the license rule: %d at %d (%v)", toman, rate, err)
			}
		})
	}
}

// Without a USDT rate (no live price and none typed) nothing is quoted or charged: the price is never guessed. A made-up token charges nothing either.
func TestWalletQuoteNeedsARateAndNeverGuesses(t *testing.T) {
	svc, s := testService(t)
	clock := &quoteClock{t: time.Now()}
	svc.cfg.Now = clock.now
	ctx := context.Background()
	plan := seedUsdtPlan(t, s, 4*usdt)
	u := seedUser(t, s, 9704)
	fund(t, s, u.ID, 5_000_000)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "norate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.QuoteWalletPayment(ctx, o.ID, u.ID); !errors.Is(err, ErrNoUsdtRate) {
		t.Fatalf("no rate: %v", err)
	}
	// A token nobody signed cannot stand in for a quote that was never made: signing needs the master key.
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"o":"`+o.ID+`","u":"`+u.ID+`","l":4000000,"c":"USDT","r":1,"t":4,"e":`+fmt.Sprint(clock.now().Add(time.Hour).Unix())+`}`)) + ".AAAA"
	if _, err := svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a forged quote: %v", err)
	}
	if bal, _, rows := ledger(t, s, u.ID); bal != 5_000_000 || rows != 1 {
		t.Fatalf("the wallet moved: %d in %d entries", bal, rows)
	}
	// An old rate is no rate: the live price is stale after half an hour, and without a typed rate nothing is quoted.
	owner := staffMember(t, s, 9705, "owner")
	setRate(t, svc, owner, "0", "5", 280_000)
	raw, _ := json.Marshal(LiveRate{Rate: 280_000, At: time.Now().Add(-time.Hour).Unix(), Sources: []string{"test"}})
	if err := s.SetSetting(ctx, s.Conn(), liveRateKey, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.QuoteWalletPayment(ctx, o.ID, u.ID); !errors.Is(err, ErrNoUsdtRate) {
		t.Fatalf("a stale live rate: %v", err)
	}
	if got, _ := s.GetOrder(ctx, s.Conn(), o.ID); got.Amount != 4*usdt || got.Currency != "USDT" || got.ListAmount != nil {
		t.Fatalf("the order: %+v", got)
	}
}

// A wallet that cannot cover the quote pays nothing and changes nothing; topping up and paying with the same quote (still held) works.
func TestWalletQuoteWithTooLittleMoney(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9706, 1_000_000)
	o := e.order(t, u, "short")
	q, err := e.svc.QuoteWalletPayment(ctx, o.ID, u.ID)
	if err != nil || q.Enough || q.Amount != 1_176_000 || q.Balance != 1_000_000 {
		t.Fatalf("quote: %+v %v", q, err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, q.Token); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("a short wallet: %v", err)
	}
	e.untouched(t, o.ID) // the order was not turned into a Toman charge: the failed payment left nothing behind
	if bal, _, rows := ledger(t, e.s, u.ID); bal != 1_000_000 || rows != 1 {
		t.Fatalf("the wallet: %d in %d entries", bal, rows)
	}
	if len(outboxEvents(t, e.s, events.OrderPaid)) != 0 {
		t.Fatal("an order.paid event for a payment that failed")
	}
	// One more Toman short is still short; enough pays.
	fund(t, e.s, u.ID, 175_999)
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, q.Token); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("1 Toman short: %v", err)
	}
	e.untouched(t, o.ID)
	fund(t, e.s, u.ID, 1)
	if paid, err := e.svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, q.Token); err != nil || paid.Status != "paid" {
		t.Fatalf("exactly enough: %v %+v", err, paid)
	}
	if bal, _, _ := ledger(t, e.s, u.ID); bal != 0 {
		t.Fatalf("the wallet is empty now: %d", bal)
	}
}

// The quote the customer confirmed is honoured for two minutes whatever the rate does; after that the payment is refused, nothing moves, and a new quote
// at the new rate is made and can be paid.
func TestWalletQuoteHoldsForTwoMinutes(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9707, 5_000_000)

	// 1. The rate moves up between the preview and the payment: the preview's price is charged.
	o1 := e.order(t, u, "hold-1")
	q1, err := e.svc.QuoteWalletPayment(ctx, o1.ID, u.ID)
	if err != nil || q1.Amount != 1_176_000 {
		t.Fatal(err, q1)
	}
	setRate(t, e.svc, e.owner, "0", "5", 300_000) // the store's rate is 315,000 now: 1,260,000
	e.clock.advance(WalletQuoteTTL - time.Second)
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o1.ID, u.ID, q1.Token); err != nil {
		t.Fatalf("a quote one second before it ends: %v", err)
	}
	if got := e.stored(t, o1.ID); got.Amount != 1_176_000 || *got.Rate != 294_000 {
		t.Fatalf("charged %d at %v, want the quote's 1,176,000 at 294,000", got.Amount, got.Rate)
	}

	// 2. The rate moves down: the quote is still the price shown, not the cheaper one (a quote is a promise both ways).
	setRate(t, e.svc, e.owner, "0", "5", 260_000) // 273,000: 1,092,000
	o2 := e.order(t, u, "hold-2")
	q2, err := e.svc.QuoteWalletPayment(ctx, o2.ID, u.ID)
	if err != nil || q2.Amount != 1_092_000 {
		t.Fatal(err, q2)
	}
	setRate(t, e.svc, e.owner, "0", "5", 280_000)
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o2.ID, u.ID, q2.Token); err != nil {
		t.Fatal(err)
	}
	if got := e.stored(t, o2.ID); got.Amount != 1_092_000 {
		t.Fatalf("charged %d, want 1,092,000", got.Amount)
	}

	// 3. Exactly at the end it still holds; a second later it does not.
	o3 := e.order(t, u, "hold-3")
	q3, _ := e.svc.QuoteWalletPayment(ctx, o3.ID, u.ID)
	e.clock.advance(WalletQuoteTTL)
	o4 := e.order(t, u, "hold-4")
	q4, _ := e.svc.QuoteWalletPayment(ctx, o4.ID, u.ID)
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o3.ID, u.ID, q3.Token); err != nil {
		t.Fatalf("a quote at the second it ends: %v", err)
	}
	e.clock.advance(WalletQuoteTTL + time.Second)
	before, _, _ := ledger(t, e.s, u.ID)
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o4.ID, u.ID, q4.Token); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("a quote that ran out: %v", err)
	}
	if after, _, _ := ledger(t, e.s, u.ID); after != before {
		t.Fatalf("the wallet moved on an expired quote: %d -> %d", before, after)
	}
	e.untouched(t, o4.ID)

	// 4. The customer is shown a new quote at the rate of that moment, and it can be paid.
	setRate(t, e.svc, e.owner, "0", "5", 300_000)
	again, err := e.svc.QuoteWalletPayment(ctx, o4.ID, u.ID)
	if err != nil || again.Rate != 315_000 || again.Amount != 1_260_000 || again.Token == q4.Token {
		t.Fatalf("the new quote: %+v %v", again, err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o4.ID, u.ID, again.Token); err != nil {
		t.Fatal(err)
	}
	if got := e.stored(t, o4.ID); got.Amount != 1_260_000 || *got.Rate != 315_000 || *got.ListAmount != 4*usdt {
		t.Fatalf("the order: %+v", got)
	}
	// The old token stays useless for an order that is paid: a repeat is the paid order, not an error and not a charge.
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o4.ID, u.ID, q4.Token); err != nil {
		t.Fatalf("a repeat after the payment: %v", err)
	}
	if n, sum := e.ledgerCount(t, u.ID, "purchase", "IRT"); n != 4 || sum != -(1_176_000+1_092_000+1_176_000+1_260_000) {
		t.Fatalf("purchases: %d of %d", n, sum)
	}
}

// A quote is for one order, one customer and one price; changing it, or taking it to another order, charges nothing.
func TestWalletQuoteBelongsToOneOrderAndCustomer(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9708, 10_000_000)
	other := e.customer(t, 9709, 10_000_000)
	a, b := e.order(t, u, "own-a"), e.order(t, u, "own-b")
	qa, err := e.svc.QuoteWalletPayment(ctx, a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.QuoteWalletPayment(ctx, a.ID, other.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else's order is not quoted: %v", err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, a.ID, other.ID, qa.Token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else's order is not paid: %v", err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, b.ID, u.ID, qa.Token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("one order's quote for another: %v", err)
	}
	// A quote of the customer's own order cannot be spent by someone who learned it: the order is theirs alone.
	ob := e.order(t, other, "other-b")
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, ob.ID, other.ID, qa.Token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a quote of another customer's order: %v", err)
	}
	// Any change to the token, or none at all, is refused.
	body, sig, _ := strings.Cut(qa.Token, ".")
	flip := func(s string) string {
		c := []byte(s)
		if c[3] == 'A' {
			c[3] = 'B'
		} else {
			c[3] = 'A'
		}
		return string(c)
	}
	for name, token := range map[string]string{
		"a changed body":      flip(body) + "." + sig,
		"a changed signature": body + "." + flip(sig),
		"no signature":        body,
		"empty halves":        ".",
		"garbage":             "not a quote",
		"too long":            strings.Repeat("A", 2000) + "." + sig,
	} {
		if _, err := e.svc.PayOrderWithWalletQuote(ctx, a.ID, u.ID, token); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Signed by another store's key (a copy of the database with another master key) is no quote.
	env2, err := bcrypto.NewEnvelope([]byte("another master key of 32 bytes!!!!!"))
	if err != nil {
		t.Fatal(err)
	}
	forged, err := New(e.s, Config{Secrets: env2}).signQuote(quoteClaims{Order: a.ID, User: u.ID, List: 4 * usdt, Cur: "USDT", Rate: 1000, Toman: 4000, Exp: e.clock.now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, a.ID, u.ID, forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a quote signed with another key: %v", err)
	}
	// A signed sum that is not price x rate is refused too (a bug elsewhere must not become a discount).
	cheap, err := e.svc.signQuote(quoteClaims{Order: a.ID, User: u.ID, List: 4 * usdt, Cur: "USDT", Rate: 294_000, Toman: 1, Exp: e.clock.now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, a.ID, u.ID, cheap); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a signed quote with the wrong sum: %v", err)
	}
	e.untouched(t, a.ID)
	e.untouched(t, b.ID)
	if bal, _, rows := ledger(t, e.s, u.ID); bal != 10_000_000 || rows != 1 {
		t.Fatalf("the wallet moved: %d in %d entries", bal, rows)
	}
	// An order listed in Toman has nothing to quote, and its plain payment is unchanged.
	irtPlan := seedPlan(t, e.s, false, 500)
	io, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: irtPlan.ID, IdempotencyKey: "own-irt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.QuoteWalletPayment(ctx, io.ID, u.ID); !errors.Is(err, ErrState) {
		t.Fatalf("a quote of a Toman order: %v", err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, io.ID, u.ID, qa.Token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a USDT quote for a Toman order: %v", err)
	}
	if paid, err := e.svc.PayOrderWithWallet(ctx, io.ID, u.ID); err != nil || paid.Amount != 500 || paid.Currency != "IRT" || paid.ListAmount != nil {
		t.Fatalf("a Toman order: %v %+v", err, paid)
	}
}

// Taps that arrive together, or one after the other, charge once: one purchase entry, one order.paid event, one debit, the same answer for all.
func TestWalletQuoteDoubleTapChargesOnce(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9710, 3_000_000)
	o := e.order(t, u, "tap")
	q, err := e.svc.QuoteWalletPayment(ctx, o.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the wallet so that the payers interleave at it, as the plain double-tap test does.
	blocker, err := e.s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM core.wallets WHERE user_id = $1 AND currency = 'IRT' FOR UPDATE`, u.ID); err != nil {
		t.Fatal(err)
	}
	const taps = 6
	needConns(t, e.s, taps+1)
	var wg sync.WaitGroup
	errs := make([]error, taps)
	got := make([]*store.Order, taps)
	for i := range taps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = e.svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, q.Token)
		}()
	}
	waitLockWaiters(t, blocker, 1, "the payers")
	_ = blocker.Rollback(ctx)
	wg.Wait()
	for i := range taps {
		if errs[i] != nil || got[i].Status != "paid" || got[i].Amount != 1_176_000 || got[i].Currency != "IRT" {
			t.Errorf("tap %d: %v %+v", i, errs[i], got[i])
		}
	}
	if bal, sum, rows := ledger(t, e.s, u.ID); bal != 1_824_000 || sum != 1_824_000 || rows != 2 {
		t.Fatalf("charged more than once? balance %d, ledger %d in %d entries", bal, sum, rows)
	}
	if n := len(outboxEvents(t, e.s, events.OrderPaid)); n != 1 {
		t.Fatalf("order.paid published %d times", n)
	}
	// And later, even after the quote ran out: the order is paid, and that is the answer.
	e.clock.advance(time.Hour)
	if again, err := e.svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, q.Token); err != nil || again.Status != "paid" {
		t.Fatalf("a repeat an hour later: %v %+v", err, again)
	}
	if bal, _, _ := ledger(t, e.s, u.ID); bal != 1_824_000 {
		t.Fatalf("an hour later: %d", bal)
	}
}

// Two different USDT orders paid at once from one wallet that covers one of them: one is paid, the other is refused for lack of money, and the
// balance never goes below zero.
func TestWalletQuoteTwoOrdersOneWallet(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9711, 2_000_000)
	a, b := e.order(t, u, "two-a"), e.order(t, u, "two-b")
	qa, _ := e.svc.QuoteWalletPayment(ctx, a.ID, u.ID)
	qb, _ := e.svc.QuoteWalletPayment(ctx, b.ID, u.ID)
	needConns(t, e.s, 3)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, pair := range [][2]string{{a.ID, qa.Token}, {b.ID, qb.Token}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.svc.PayOrderWithWalletQuote(ctx, pair[0], u.ID, pair[1])
		}()
	}
	wg.Wait()
	ok, short := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, store.ErrInsufficientFunds):
			short++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || short != 1 {
		t.Fatalf("%d paid, %d short; want one of each", ok, short)
	}
	if bal, sum, _ := ledger(t, e.s, u.ID); bal != 824_000 || sum != 824_000 {
		t.Fatalf("balance %d, ledger %d", bal, sum)
	}
	paidOnes := 0
	for _, id := range []string{a.ID, b.ID} {
		if o := e.stored(t, id); o.Status == "paid" {
			paidOnes++
		} else {
			e.untouched(t, id)
		}
	}
	if paidOnes != 1 {
		t.Fatalf("%d orders are paid", paidOnes)
	}
}

// A refund of an order paid this way goes back to the wallet in Toman as charged, once; the order keeps its USDT list price and the USDT wallet is not
// touched.
func TestWalletQuotedOrderIsRefundedInToman(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	staff := owner(t, e.svc)
	u := e.customer(t, 9712, 2_000_000)
	o := e.order(t, u, "refund")
	q, _ := e.svc.QuoteWalletPayment(ctx, o.ID, u.ID)
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, q.Token); err != nil {
		t.Fatal(err)
	}
	// The rate moves a lot before the refund: the refund is what was charged, not what the plan costs now.
	setRate(t, e.svc, e.owner, "0", "5", 400_000)
	res, err := e.svc.RefundOrder(ctx, staff, &fakeProvisioner{}, o.ID, "asked for it back")
	if err != nil {
		t.Fatal(err)
	}
	if res.Wallet.Currency != "IRT" || res.Wallet.Balance != 2_000_000 {
		t.Fatalf("the wallet after the refund: %+v", res.Wallet)
	}
	got := e.stored(t, o.ID)
	if got.RefundedAt == nil || got.RefundAmount == nil || *got.RefundAmount != 1_176_000 || got.Currency != "IRT" ||
		got.ListAmount == nil || *got.ListAmount != 4*usdt || got.Status != "cancelled" {
		t.Fatalf("the refunded order: %+v", got)
	}
	if n, sum := e.ledgerCount(t, u.ID, "refund", "IRT"); n != 1 || sum != 1_176_000 {
		t.Fatalf("refund entries: %d of %d", n, sum)
	}
	if n, _ := e.ledgerCount(t, u.ID, "refund", "USDT"); n != 0 {
		t.Fatal("a refund in USDT")
	}
	if _, err := e.svc.RefundOrder(ctx, staff, &fakeProvisioner{}, o.ID, "again"); err == nil {
		t.Fatal("refunded twice")
	}
	if bal, sum, _ := ledger(t, e.s, u.ID); bal != 2_000_000 || sum != 2_000_000 {
		t.Fatalf("balance %d, ledger %d", bal, sum)
	}
	if auditCount(t, e.s, "order.refund") != 1 {
		t.Fatal("the refund is not in the audit log once")
	}
}

// A discount code takes its part off the USDT price first, and the Toman charge is worked out from what is left; the inviter's reward is in Toman.
func TestWalletQuoteAfterADiscountAndForAnInvitedCustomer(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	if err := e.svc.SetSetting(ctx, e.owner, "referral.reward_percent", "10"); err != nil {
		t.Fatal(err)
	}
	pct := int32(25)
	owner(t, e.svc) // the configured owner's account, which the dashboard's discount codes are made by
	if _, err := e.svc.AdminUpsertDiscount(ctx, ownerTG, &store.Discount{Code: "QUARTER", Percent: &pct, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	inviter := seedUser(t, e.s, 9713)
	buyer, err := e.s.UpsertUser(ctx, e.s.Conn(), 9714, "", "", inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	fund(t, e.s, buyer.ID, 2_000_000)
	o, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: buyer.ID, PlanID: e.plan.ID, IdempotencyKey: "disc", DiscountCode: "quarter"})
	if err != nil || o.Amount != 3*usdt || o.Currency != "USDT" || o.DiscountAmount != usdt {
		t.Fatalf("the order: %v %+v", err, o)
	}
	q, err := e.svc.QuoteWalletPayment(ctx, o.ID, buyer.ID)
	if err != nil || q.ListAmount != 3*usdt || q.Amount != 882_000 {
		t.Fatalf("quote: %+v %v", q, err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o.ID, buyer.ID, q.Token); err != nil {
		t.Fatal(err)
	}
	got := e.stored(t, o.ID)
	if got.Amount != 882_000 || got.Currency != "IRT" || *got.ListAmount != 3*usdt || got.DiscountAmount != usdt || got.DiscountCode == nil || *got.DiscountCode != "QUARTER" {
		t.Fatalf("the order: %+v", got)
	}
	// The code is used up for this customer, as for any order.
	var used int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.discount_redemptions WHERE order_id = $1`, o.ID).Scan(&used); err != nil || used != 1 {
		t.Fatalf("redemptions: %d %v", used, err)
	}
	// The inviter is rewarded 10% of what the buyer paid, in Toman.
	if n, sum := e.ledgerCount(t, inviter.ID, "referral", "IRT"); n != 1 || sum != 88_200 {
		t.Fatalf("the inviter's reward: %d of %d", n, sum)
	}
	if n, _ := e.ledgerCount(t, inviter.ID, "referral", "USDT"); n != 0 {
		t.Fatal("a reward in USDT")
	}
}

// A reseller pays a USDT-priced plan on credit down to the limit, in Toman, like any other.
func TestWalletQuoteOnAResellersCredit(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	admin := staffMember(t, e.s, 9715, "admin")
	tier := seedTier(t, e.svc, admin, "Gold", 0, 10, nil)
	r := reseller(t, e.svc, e.s, admin, 9716, tier, 1_500_000)
	o := e.order(t, r, "credit")
	q, err := e.svc.QuoteWalletPayment(ctx, o.ID, r.ID)
	if err != nil || !q.Enough || q.Balance != 0 || q.CreditLimit != 1_500_000 {
		t.Fatalf("quote: %+v %v", q, err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o.ID, r.ID, q.Token); err != nil {
		t.Fatal(err)
	}
	if bal, _, _ := ledger(t, e.s, r.ID); bal != -1_176_000 {
		t.Fatalf("on credit: %d", bal)
	}
	// 324,000 of credit is left: the next one is not covered, and the quote says so.
	o2 := e.order(t, r, "credit-2")
	q2, err := e.svc.QuoteWalletPayment(ctx, o2.ID, r.ID)
	if err != nil || q2.Enough {
		t.Fatalf("past the limit: %+v %v", q2, err)
	}
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o2.ID, r.ID, q2.Token); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("past the limit: %v", err)
	}
	e.untouched(t, o2.ID)
}

// An order that was paid another way (a TRC20 payment, a staff member's approval) is not quoted or charged again; a payment that arrives for an order
// the wallet paid stays in the wallet.
func TestWalletQuoteAndAnotherPayment(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9717, 5_000_000)
	pay := func(id, intent string, o *store.Order) {
		t.Helper()
		if err := e.svc.HandlePaymentEvent(ctx, payEvent(t, id, events.PaymentsPaymentSucceeded, events.PaymentEvent{
			IntentID: intent, OrderID: o.ID, UserID: u.ID, Provider: "usdt_trc20_auto", Amount: 4 * usdt, Currency: "USDT"})); err != nil {
			t.Fatal(err)
		}
	}
	// 1. Paid by crypto first: the direct path keeps working, the order stays a USDT charge, and the wallet quote is refused.
	o1 := e.order(t, u, "other-1")
	q1, _ := e.svc.QuoteWalletPayment(ctx, o1.ID, u.ID)
	pay("ev1", "00000000-0000-7000-8000-0000000000a1", o1)
	got := e.stored(t, o1.ID)
	if got.Status != "paid" || got.Amount != 4*usdt || got.Currency != "USDT" || got.ListAmount != nil {
		t.Fatalf("paid by crypto: %+v", got)
	}
	if _, err := e.svc.QuoteWalletPayment(ctx, o1.ID, u.ID); !errors.Is(err, ErrOrderNotPayable) {
		t.Fatalf("a quote of a paid order: %v", err)
	}
	if again, err := e.svc.PayOrderWithWalletQuote(ctx, o1.ID, u.ID, q1.Token); err != nil || again.Currency != "USDT" {
		t.Fatalf("the wallet payment after the crypto one: %v %+v", err, again)
	}
	if bal, _, _ := ledger(t, e.s, u.ID); bal != 5_000_000 {
		t.Fatalf("the Toman wallet was charged for an order crypto paid: %d", bal)
	}
	// 2. Paid by the wallet first; the TRC20 payment that arrives later stays in the wallet as money, and is converted like any USDT top-up.
	o2 := e.order(t, u, "other-2")
	q2, _ := e.svc.QuoteWalletPayment(ctx, o2.ID, u.ID)
	if _, err := e.svc.PayOrderWithWalletQuote(ctx, o2.ID, u.ID, q2.Token); err != nil {
		t.Fatal(err)
	}
	pay("ev2", "00000000-0000-7000-8000-0000000000a2", o2)
	if n, _ := e.ledgerCount(t, u.ID, "purchase", "IRT"); n != 1 {
		t.Fatalf("the order was charged %d times in Toman", n)
	}
	var usdtWallet int64
	_ = e.s.DB().QueryRow(ctx, `SELECT COALESCE(max(balance), 0) FROM core.wallets WHERE user_id = $1 AND currency = 'USDT'`, u.ID).Scan(&usdtWallet)
	if usdtWallet != 4*usdt {
		t.Fatalf("the late payment: %d micro-USDT in the USDT wallet, want 4,000,000 waiting to be converted", usdtWallet)
	}
	if swept, err := e.svc.SweepUsdtWallets(ctx); err != nil || swept != 1 {
		t.Fatalf("sweep: %d %v", swept, err)
	}
	if bal, sum, _ := ledger(t, e.s, u.ID); bal != 5_000_000-1_176_000+1_120_000 || sum != bal {
		t.Fatalf("after converting the late payment at the live price (280,000): balance %d, ledger %d", bal, sum)
	}
}

// The wallet and the TRC20 payment race for one order: whoever locks the order first pays it, the other changes nothing about it, and no money is
// lost or made: the books of both wallets add up to their balances, and the order was charged once, in the currency of the one that won.
func TestWalletQuoteRacesACryptoPayment(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	needConns(t, e.s, 4)
	for i := range 6 {
		u := e.customer(t, int64(9720+i), 2_000_000)
		o := e.order(t, u, fmt.Sprintf("race-%d", i))
		q, err := e.svc.QuoteWalletPayment(ctx, o.ID, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var payErr, eventErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, payErr = e.svc.PayOrderWithWalletQuote(ctx, o.ID, u.ID, q.Token)
		}()
		go func() {
			defer wg.Done()
			eventErr = e.svc.HandlePaymentEvent(ctx, payEvent(t, fmt.Sprintf("race-ev-%d", i), events.PaymentsPaymentSucceeded, events.PaymentEvent{
				IntentID: fmt.Sprintf("00000000-0000-7000-8000-00000000b%03d", i), OrderID: o.ID, UserID: u.ID, Provider: "usdt_trc20_auto", Amount: 4 * usdt, Currency: "USDT"}))
		}()
		wg.Wait()
		if payErr != nil || eventErr != nil {
			t.Fatalf("round %d: the wallet payment: %v, the crypto payment: %v", i, payErr, eventErr)
		}
		got := e.stored(t, o.ID)
		if got.Status != "paid" {
			t.Fatalf("round %d: the order is %s", i, got.Status)
		}
		tomanN, _ := e.ledgerCount(t, u.ID, "purchase", "IRT")
		usdtN, _ := e.ledgerCount(t, u.ID, "purchase", "USDT")
		if tomanN+usdtN != 1 {
			t.Fatalf("round %d: the order was charged %d times in Toman and %d in USDT", i, tomanN, usdtN)
		}
		if tomanN == 1 && (got.Currency != "IRT" || got.Amount != 1_176_000 || got.ListAmount == nil) || usdtN == 1 && (got.Currency != "USDT" || got.Amount != 4*usdt || got.ListAmount != nil) {
			t.Fatalf("round %d: the order does not say how it was paid: %+v", i, got)
		}
		for _, cur := range []string{"IRT", "USDT"} {
			var bal, sum int64
			_ = e.s.DB().QueryRow(ctx, `SELECT COALESCE(max(balance), 0) FROM core.wallets WHERE user_id = $1 AND currency = $2`, u.ID, cur).Scan(&bal)
			_ = e.s.DB().QueryRow(ctx, `SELECT COALESCE(sum(amount), 0) FROM core.ledger_entries WHERE user_id = $1 AND currency = $2`, u.ID, cur).Scan(&sum)
			if bal != sum {
				t.Fatalf("round %d, %s: the wallet holds %d, the ledger sums to %d", i, cur, bal, sum)
			}
		}
	}
}

// Paying a USDT-priced order with no quote is the old way: it takes the customer's USDT wallet at the list price (a balance left there), never the
// Toman wallet at a rate nobody was shown.
func TestUsdtOrderWithoutAQuoteTakesTheUsdtWallet(t *testing.T) {
	e := newQuoteEnv(t)
	ctx := context.Background()
	u := e.customer(t, 9718, 5_000_000)
	o := e.order(t, u, "legacy")
	if _, err := e.svc.PayOrderWithWallet(ctx, o.ID, u.ID); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("no USDT in the USDT wallet: %v", err)
	}
	e.untouched(t, o.ID)
	err := e.s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := e.s.Credit(ctx, tx, u.ID, "USDT", 10*usdt, &store.LedgerEntry{Kind: "adjust", IdempotencyKey: "usdt-left"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	paid, err := e.svc.PayOrderWithWallet(ctx, o.ID, u.ID)
	if err != nil || paid.Currency != "USDT" || paid.Amount != 4*usdt || paid.ListAmount != nil {
		t.Fatalf("paid from the USDT wallet: %v %+v", err, paid)
	}
	if bal, _, _ := ledger(t, e.s, u.ID); bal != 5_000_000 {
		t.Fatalf("the Toman wallet moved: %d", bal)
	}
}
