package domain

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func setRate(t *testing.T, svc *Service, owner *store.User, manual string, margin string, live int64) {
	t.Helper()
	ctx := context.Background()
	if manual == "0" {
		manual = "" // empty: the owner has typed no rate
	}
	if err := svc.SetSetting(ctx, owner, "payments.usdt_rate", manual); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(ctx, owner, SettingUSDTMargin, margin); err != nil {
		t.Fatal(err)
	}
	if live > 0 {
		raw, _ := json.Marshal(LiveRate{Rate: live, At: time.Now().Unix(), Sources: []string{"test"}})
		if err := svc.st.SetSetting(ctx, svc.st.Conn(), liveRateKey, raw); err != nil {
			t.Fatal(err)
		}
	}
}

// What a price in USDT costs in Toman (the store's rate, rounded up), what a balance is worth in USDT (the live price, to the cent), and the
// refusal when no rate is known.
func TestTomanForUsdtAndEquivalent(t *testing.T) {
	svc, s := testService(t)
	owner := staffMember(t, s, 9601, "owner")
	ctx := context.Background()
	if _, _, err := svc.TomanForUsdt(ctx, 4*usdt); !errors.Is(err, ErrNoUsdtRate) {
		t.Fatalf("no rate: %v", err)
	}
	if svc.UsdtEquivalent(ctx, 1_000_000) != 0 || svc.UsdtBaseRate(ctx) != 0 {
		t.Fatal("an equivalent without a rate")
	}
	// the owner's own rate: 271,600 Toman a USDT
	setRate(t, svc, owner, "271600", "0", 0)
	toman, rate, err := svc.TomanForUsdt(ctx, 4*usdt)
	if err != nil || toman != 1_086_400 || rate != 271_600 {
		t.Fatalf("4 USDT: %d at %d, %v", toman, rate, err)
	}
	// a price with decimals rounds up to a whole Toman: 2.500731 USDT * 271,600 = 679,198.3... -> 679,199
	if toman, _, _ := svc.TomanForUsdt(ctx, 2_500_731); toman != 679_199 {
		t.Fatalf("2.500731 USDT: %d", toman)
	}
	if got, _, _ := svc.TomanForUsdt(ctx, 1); got != 1 {
		t.Fatalf("a millionth of a USDT rounds up to 1 Toman: %d", got)
	}
	if got := svc.UsdtEquivalent(ctx, 1_086_400); got != 4*usdt {
		t.Fatalf("equivalent of 1,086,400 Toman: %d", got)
	}
	if got := svc.UsdtEquivalent(ctx, 1_000_000); got != 3_680_000 { // 3.6819... rounds to the cent
		t.Fatalf("equivalent of 1,000,000 Toman: %d", got)
	}
	// the live price, with the owner's margin: licenses cost the margin's rate, a balance is shown at the live price
	setRate(t, svc, owner, "0", "5", 280_000)
	if toman, rate, _ := svc.TomanForUsdt(ctx, 4*usdt); rate != 294_000 || toman != 1_176_000 {
		t.Fatalf("with a 5%% margin: %d at %d", toman, rate)
	}
	if svc.UsdtBaseRate(ctx) != 280_000 {
		t.Fatalf("the live price without the margin: %d", svc.UsdtBaseRate(ctx))
	}
	if got := svc.UsdtEquivalent(ctx, 1_120_000); got != 4*usdt {
		t.Fatalf("equivalent at the live price: %d", got)
	}
}

// A USDT top-up lands as Toman at the live price (no margin), as two wallet_convert entries, and the event the customer is told by says so.
func TestUsdtTopupIsCreditedInToman(t *testing.T) {
	svc, s := testService(t)
	owner := staffMember(t, s, 9611, "owner")
	buyer := seedUser(t, s, 9612)
	ctx := context.Background()
	setRate(t, svc, owner, "0", "5", 280_000) // the live price 280,000, the store's rate 294,000

	pay := func(intent string, amount int64, currency string) {
		t.Helper()
		err := s.WithTx(ctx, func(tx pgx.Tx) error {
			return svc.paymentReceived(ctx, tx, events.PaymentEvent{IntentID: intent, UserID: buyer.ID, Provider: "manual_crypto", Amount: amount, Currency: currency})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	pay("10000000-0000-7000-8000-000000000001", 10*usdt, "USDT")
	irt, err := s.GetWallet(ctx, s.Conn(), buyer.ID, "IRT")
	if err != nil || irt.Balance != 2_800_000 {
		t.Fatalf("Toman wallet after 10 USDT at 280,000: %+v %v", irt, err)
	}
	if w, _ := s.GetWallet(ctx, s.Conn(), buyer.ID, "USDT"); w.Balance != 0 {
		t.Fatalf("the USDT wallet keeps %d", w.Balance)
	}
	evs := outboxEvents(t, s, events.WalletCredited)
	var ev events.WalletCreditedEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &ev) != nil || ev.Currency != "IRT" || ev.Amount != 2_800_000 || ev.Balance != 2_800_000 ||
		ev.FromCurrency != "USDT" || ev.FromAmount != 10*usdt || ev.Rate != 280_000 {
		t.Fatalf("the event: %s", evs)
	}
	var converts int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM core.ledger_entries WHERE user_id = $1 AND kind = 'wallet_convert'`, buyer.ID).Scan(&converts); err != nil || converts != 2 {
		t.Fatalf("conversion entries: %d %v", converts, err)
	}
	// A Toman top-up is not converted, and a replay of the USDT one changes nothing.
	pay("10000000-0000-7000-8000-000000000002", 500_000, "IRT")
	pay("10000000-0000-7000-8000-000000000001", 10*usdt, "USDT")
	if irt, _ = s.GetWallet(ctx, s.Conn(), buyer.ID, "IRT"); irt.Balance != 3_300_000 {
		t.Fatalf("after a Toman top-up and a replay: %d", irt.Balance)
	}
	for _, cur := range []string{"IRT", "USDT"} {
		var bal, sum int64
		_ = s.DB().QueryRow(ctx, `SELECT COALESCE(max(balance), 0) FROM core.wallets WHERE user_id = $1 AND currency = $2`, buyer.ID, cur).Scan(&bal)
		_ = s.DB().QueryRow(ctx, `SELECT COALESCE(sum(amount), 0) FROM core.ledger_entries WHERE user_id = $1 AND currency = $2`, buyer.ID, cur).Scan(&sum)
		if bal != sum {
			t.Errorf("%s: the wallet holds %d, the ledger sums to %d", cur, bal, sum)
		}
	}
	// A license bought with it costs the store's rate: 4 USDT = 1,176,000 Toman, not the 1,120,000 the live price would make.
	if toman, _, _ := svc.TomanForUsdt(ctx, 4*usdt); toman != 1_176_000 {
		t.Fatalf("the license price: %d", toman)
	}
}

// Money held in USDT with no rate known waits; the sweeper converts it as soon as there is a rate, once however often it runs and however many run
// at the same time.
func TestSweeperConvertsUsdtWalletsOnce(t *testing.T) {
	svc, s := testService(t)
	owner := staffMember(t, s, 9621, "owner")
	ctx := context.Background()
	users := []*store.User{seedUser(t, s, 9622), seedUser(t, s, 9623)}
	for i, u := range users {
		err := s.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := s.Credit(ctx, tx, u.ID, "USDT", int64(i+1)*usdt, &store.LedgerEntry{Kind: "topup", IdempotencyKey: "sweep-seed-" + u.ID})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n, err := svc.SweepUsdtWallets(ctx); err != nil || n != 0 {
		t.Fatalf("no rate: %d %v", n, err)
	}
	if w, _ := s.GetWallet(ctx, s.Conn(), users[0].ID, "USDT"); w.Balance != usdt {
		t.Fatalf("converted without a rate: %d", w.Balance)
	}
	setRate(t, svc, owner, "100000", "0", 0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 4 {
		wg.Go(func() {
			n, err := svc.SweepUsdtWallets(ctx)
			mu.Lock()
			defer mu.Unlock()
			total += n
			if err != nil {
				t.Errorf("sweep: %v", err)
			}
		})
	}
	wg.Wait()
	if total != 2 {
		t.Fatalf("four sweepers converted %d wallets, want 2", total)
	}
	for i, u := range users {
		irt, _ := s.GetWallet(ctx, s.Conn(), u.ID, "IRT")
		usd, _ := s.GetWallet(ctx, s.Conn(), u.ID, "USDT")
		if irt.Balance != int64(i+1)*100_000 || usd.Balance != 0 {
			t.Errorf("user %d: %d Toman and %d USDT", i, irt.Balance, usd.Balance)
		}
	}
	if n, _ := svc.SweepUsdtWallets(ctx); n != 0 {
		t.Fatalf("a later sweep converted %d", n)
	}
}
