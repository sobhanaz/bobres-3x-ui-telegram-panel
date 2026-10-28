package domain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// wheelEnv is a store with an owner, a plan of 10,000 Toman and the wheel switched on: a prize of 1,000 (weight 3), a prize of 5,000 (weight 1) and
// no prize (weight 4), so out of 8 rolls 0-2 win 1,000, 3 wins 5,000 and 4-7 win nothing; a budget of 20,000 a day and one paid order asked for. The
// clock stands at 09:00 UTC on 15 October 2026 (12:30 in Tehran) and the draw is whatever e.roll says.
type wheelEnv struct {
	*namingEnv
	fp   *fakeProvisioner
	at   time.Time
	keys int
	tg   int64
	// roll is the draw; a test sets it to choose where the wheel stops.
	roll func(n int64) (int64, error)
}

func newWheelEnv(t *testing.T) *wheelEnv {
	t.Helper()
	e := &wheelEnv{namingEnv: newNamingEnv(t), fp: &fakeProvisioner{}, at: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC), tg: 7_100_000}
	e.plan.Price = 10_000
	if err := e.s.UpsertPlan(context.Background(), e.s.Conn(), e.plan); err != nil {
		t.Fatal(err)
	}
	e.svc.cfg.Now = func() time.Time { return e.at }
	e.rollAt(0)
	e.svc.cfg.WheelRand = func(n int64) (int64, error) { return e.roll(n) }
	e.set(map[string]string{
		SettingWheelEnabled: "true", SettingWheelPrizes: `[{"amount":5000,"weight":1},{"amount":1000,"weight":3}]`,
		SettingWheelNoPrize: "4", SettingWheelBudget: "20000",
	})
	return e
}

// rollAt makes every draw come up v.
func (e *wheelEnv) rollAt(v int64) { e.roll = func(int64) (int64, error) { return v, nil } }

func (e *wheelEnv) set(values map[string]string) {
	e.t.Helper()
	if _, err := e.svc.SetSettings(context.Background(), e.owner, values); err != nil {
		e.t.Fatal(err)
	}
}

func (e *wheelEnv) key() string {
	e.keys++
	return fmt.Sprintf("wh-%d", e.keys)
}

// customer is a new customer with one paid order (paid for from the wallet, not delivered).
func (e *wheelEnv) customer() *store.User {
	e.t.Helper()
	e.tg++
	u := seedUser(e.t, e.s, e.tg)
	e.payOrder(u)
	return u
}

// payOrder has u pay for one more order of the plan.
func (e *wheelEnv) payOrder(u *store.User) *store.Order {
	e.t.Helper()
	ctx := context.Background()
	err := e.s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := e.s.Credit(ctx, tx, u.ID, "IRT", 10_000, &store.LedgerEntry{Kind: "topup", IdempotencyKey: "fund-" + e.key()})
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	o, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: e.plan.ID, IdempotencyKey: e.key()})
	if err != nil {
		e.t.Fatal(err)
	}
	if o, err = e.svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		e.t.Fatal(err)
	}
	return o
}

// deliver has the worker deliver the next paid order.
func (e *wheelEnv) deliver() {
	e.t.Helper()
	if worked, err := e.svc.NewProvisionWorker(e.fp, nil).ProvisionNext(context.Background()); err != nil || !worked {
		e.t.Fatalf("provisioning: %v %v", worked, err)
	}
}

func (e *wheelEnv) spin(u *store.User) *WheelResult {
	e.t.Helper()
	r, err := e.svc.SpinWheel(context.Background(), u.ID)
	if err != nil {
		e.t.Fatalf("spin: %v", err)
	}
	return r
}

func (e *wheelEnv) balance(u *store.User) int64 {
	e.t.Helper()
	w, err := e.s.GetWallet(context.Background(), e.s.Conn(), u.ID, "IRT")
	if err != nil {
		e.t.Fatal(err)
	}
	return w.Balance
}

func (e *wheelEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.s.DB().QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// spins counts the rows of the wheel; prizes the ledger entries it wrote.
func (e *wheelEnv) spins() int { return e.count(`SELECT count(*) FROM core.wheel_spins`) }
func (e *wheelEnv) prizes() int {
	return e.count(`SELECT count(*) FROM core.ledger_entries WHERE kind = 'wheel_prize'`)
}

func (e *wheelEnv) consistent(u *store.User) {
	e.t.Helper()
	if bal, sum, _ := ledger(e.t, e.s, u.ID); bal != sum {
		e.t.Fatalf("the wallet holds %d but its ledger adds up to %d", bal, sum)
	}
}

func TestWheelPrizesAreRead(t *testing.T) {
	got, err := ParseWheelPrizes(` [ {"weight": 2, "amount": 5000}, {"amount":1000,"weight":10} ] `)
	if err != nil || len(got) != 2 || got[0] != (WheelPrize{1000, 10}) || got[1] != (WheelPrize{5000, 2}) {
		t.Errorf("sorted by amount: %v %v", got, err)
	}
	for _, empty := range []string{``, `null`, `[]`} {
		if got, err := ParseWheelPrizes(empty); err != nil || len(got) != 0 {
			t.Errorf("ParseWheelPrizes(%q) = %v, %v", empty, got, err)
		}
	}
	for _, bad := range []string{
		`nope`, `{}`, `7`, `[1]`, `[{}]`, `[{"amount":0,"weight":1}]`, `[{"amount":1,"weight":0}]`, `[{"amount":-5,"weight":1}]`, `[{"amount":1,"weight":-1}]`,
		`[{"amount":1.5,"weight":1}]`, `[{"amount":"5","weight":1}]`, `[{"amount":1,"weight":1,"extra":2}]`, `[{"amount":1,"weight":1}] x`,
		`[{"amount":100000000001,"weight":1}]`, `[{"amount":1,"weight":1000001}]`, `[{"amount":7,"weight":1},{"amount":7,"weight":2}]`,
	} {
		if got, err := ParseWheelPrizes(bad); err == nil {
			t.Errorf("ParseWheelPrizes(%q) = %v: accepted", bad, got)
		}
	}
	many := "["
	for i := 1; i <= MaxWheelPrizes+1; i++ {
		if i > 1 {
			many += ","
		}
		many += fmt.Sprintf(`{"amount":%d,"weight":1}`, i)
	}
	if _, err := ParseWheelPrizes(many + "]"); err == nil {
		t.Error("more prizes than the wheel holds")
	}
	if _, err := ParseWheelPrizes(many[:len(many)-len(`,{"amount":13,"weight":1}`)] + "]"); err != nil {
		t.Errorf("a full wheel: %v", err)
	}
	if s := FormatWheelPrizes([]WheelPrize{{5000, 2}, {1000, 10}}); s != `[{"amount":1000,"weight":10},{"amount":5000,"weight":2}]` {
		t.Errorf("format: %s", s)
	}
	if FormatWheelPrizes(nil) != "" {
		t.Error("an empty wheel is written empty")
	}
}

func TestWheelSettingsAreChecked(t *testing.T) {
	if got, err := NormalizeSetting(SettingWheelPrizes, ` [ {"weight":1,"amount":5000}, {"amount":100,"weight":9} ] `); err != nil ||
		got != `[{"amount":100,"weight":9},{"amount":5000,"weight":1}]` {
		t.Errorf("prizes: %q %v", got, err)
	}
	var fe *FieldError
	if _, err := NormalizeSetting(SettingWheelPrizes, `[{"amount":0,"weight":1}]`); !errors.As(err, &fe) || fe.Field != SettingWheelPrizes {
		t.Errorf("a prize of 0: %v", err)
	}
	for key, bad := range map[string][]string{
		SettingWheelMinOrders: {"0", "-1", "1001", "x"},
		SettingWheelBudget:    {"-5", "100000000001", "1.5"},
		SettingWheelNoPrize:   {"-1", "1000000001"},
	} {
		for _, v := range bad {
			if got, err := NormalizeSetting(key, v); err == nil {
				t.Errorf("%s = %q accepted as %q", key, v, got)
			}
		}
	}
	for key, good := range map[string]string{SettingWheelMinOrders: "3", SettingWheelBudget: "50000", SettingWheelNoPrize: "0", SettingWheelEnabled: "true"} {
		if _, err := NormalizeSetting(key, good); err != nil {
			t.Errorf("%s = %q: %v", key, good, err)
		}
	}
	// Off by default; it pays only with prizes and a budget; a value that is not valid never pays.
	if c := WheelConfigOf(map[string]string{}); c.Enabled || c.On() || c.MinOrders != 1 {
		t.Errorf("defaults: %+v", c)
	}
	on := map[string]string{SettingWheelEnabled: "true", SettingWheelPrizes: `[{"amount":10,"weight":1}]`, SettingWheelBudget: "100"}
	if c := WheelConfigOf(on); !c.On() || c.TotalWeight() != 1 {
		t.Errorf("a wheel with a prize and a budget: %+v", c)
	}
	for _, drop := range []string{SettingWheelEnabled, SettingWheelPrizes, SettingWheelBudget} {
		set := map[string]string{}
		for k, v := range on {
			if k != drop {
				set[k] = v
			}
		}
		if c := WheelConfigOf(set); c.On() {
			t.Errorf("a wheel without %s pays: %+v", drop, c)
		}
	}
	if c := WheelConfigOf(map[string]string{SettingWheelEnabled: "true", SettingWheelPrizes: "garbage", SettingWheelBudget: "100"}); c.On() {
		t.Errorf("a bad list pays: %+v", c)
	}
	if c := WheelConfigOf(map[string]string{SettingWheelMinOrders: "0"}); c.MinOrders != 1 {
		t.Errorf("a floor of one paid order: %+v", c)
	}
}

// The segments run in the order of the list, "no prize" last, and a roll lands on exactly one of them.
func TestWheelRollsLandOnTheRightSegment(t *testing.T) {
	c := WheelConfigOf(map[string]string{SettingWheelPrizes: `[{"amount":1000,"weight":3},{"amount":5000,"weight":1}]`, SettingWheelNoPrize: "4"})
	want := []int{0, 0, 0, 1, -1, -1, -1, -1}
	if c.TotalWeight() != 8 {
		t.Fatalf("total %d", c.TotalWeight())
	}
	for roll, w := range want {
		if got := c.pick(int64(roll)); got != w {
			t.Errorf("roll %d landed on %d, want %d", roll, got, w)
		}
	}
	// No "no prize" segment: every roll wins.
	every := WheelConfigOf(map[string]string{SettingWheelPrizes: `[{"amount":1000,"weight":1},{"amount":5000,"weight":1}]`})
	for roll := range int64(2) {
		if every.pick(roll) < 0 {
			t.Errorf("roll %d won nothing on a wheel with no empty segment", roll)
		}
	}
}

// The draw is uniform over the weights: a large sample from the real generator lands on each segment as often as its weight says, within a
// generous margin (5 standard deviations), and the generator stays inside its range.
func TestWheelDrawFollowsTheWeights(t *testing.T) {
	svc := &Service{}
	c := WheelConfigOf(map[string]string{SettingWheelPrizes: `[{"amount":1000,"weight":50},{"amount":5000,"weight":10},{"amount":20000,"weight":1}]`, SettingWheelNoPrize: "39"})
	total := c.TotalWeight() // 100
	const n = 200_000
	hits := map[int]int{}
	for range n {
		roll, err := svc.drawWheel(total)
		if err != nil || roll < 0 || roll >= total {
			t.Fatalf("draw: %d %v", roll, err)
		}
		hits[c.pick(roll)]++
	}
	expect := map[int]float64{0: 0.50, 1: 0.10, 2: 0.01, -1: 0.39}
	for seg, p := range expect {
		mean, sd := p*n, math.Sqrt(n*p*(1-p))
		if got := float64(hits[seg]); math.Abs(got-mean) > 5*sd {
			t.Errorf("segment %d came up %v times, expected %v +- %v", seg, got, mean, 5*sd)
		}
	}
	if _, err := svc.drawWheel(0); err == nil {
		t.Error("a draw from nothing")
	}
}

// Every roll of the wheel is played by one customer each: the wheel pays as many of each prize as that prize has weight, and nothing for the empty
// segment, so the whole path from the roll to the wallet follows the weights exactly.
func TestWheelPaysByTheWeights(t *testing.T) {
	e := newWheelEnv(t)
	e.set(map[string]string{SettingWheelBudget: "1000000"})
	counts := map[int64]int{}
	var next int64
	e.roll = func(n int64) (int64, error) {
		if n != 8 {
			return 0, fmt.Errorf("the wheel has %d weights, not 8", n)
		}
		return next, nil
	}
	var paid int64
	for next = 0; next < 8; next++ {
		u := e.customer()
		r := e.spin(u)
		counts[r.Prize]++
		paid += r.Prize
		wantBalance := int64(0)
		switch {
		case next < 3:
			wantBalance = 1000
		case next == 3:
			wantBalance = 5000
		}
		if r.Prize != wantBalance || e.balance(u) != wantBalance {
			t.Errorf("roll %d paid %d, wallet %d, want %d", next, r.Prize, e.balance(u), wantBalance)
		}
		e.consistent(u)
	}
	if counts[1000] != 3 || counts[5000] != 1 || counts[0] != 4 {
		t.Errorf("prizes by roll: %v", counts)
	}
	if paid != 8000 || e.prizes() != 4 || e.spins() != 8 {
		t.Errorf("paid %d in %d ledger entries over %d spins", paid, e.prizes(), e.spins())
	}
}

func TestWheelWhoMaySpin(t *testing.T) {
	ctx := context.Background()

	t.Run("off until switched on, and closed until it has prizes and a budget", func(t *testing.T) {
		e := newWheelEnv(t)
		u := e.customer()
		for _, off := range []map[string]string{{SettingWheelEnabled: "false"}, {SettingWheelBudget: ""}, {SettingWheelPrizes: ""}} {
			e.set(map[string]string{SettingWheelEnabled: "true", SettingWheelBudget: "20000", SettingWheelPrizes: `[{"amount":1000,"weight":1}]`})
			e.set(off)
			if r := e.spin(u); r.Status != WheelOff {
				t.Errorf("%v: %s", off, r.Status)
			}
			if st, err := e.svc.Wheel(ctx, u.ID); err != nil || st.On {
				t.Errorf("%v: state %+v %v", off, st, err)
			}
		}
		if e.spins() != 0 || e.prizes() != 0 || e.balance(u) != 0 {
			t.Error("a wheel that is off wrote something")
		}
	})

	t.Run("a customer needs the paid orders the owner asks for", func(t *testing.T) {
		e := newWheelEnv(t)
		e.tg++
		u := seedUser(t, e.s, e.tg)
		if r := e.spin(u); r.Status != WheelOrders || r.Orders != 0 || r.MinOrders != 1 {
			t.Errorf("no order: %+v", r)
		}
		// An order that is not paid for does not count.
		if _, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: e.plan.ID, IdempotencyKey: e.key()}); err != nil {
			t.Fatal(err)
		}
		if r := e.spin(u); r.Status != WheelOrders {
			t.Errorf("an unpaid order: %+v", r)
		}
		e.payOrder(u)
		e.rollAt(7)
		if r := e.spin(u); r.Status != WheelLost {
			t.Errorf("one paid order: %+v", r)
		}
		// Two asked for.
		e.set(map[string]string{SettingWheelMinOrders: "2"})
		v := e.customer()
		if r := e.spin(v); r.Status != WheelOrders || r.Orders != 1 || r.MinOrders != 2 {
			t.Errorf("one of two orders: %+v", r)
		}
		if st, err := e.svc.Wheel(ctx, v.ID); err != nil || st.Reason != WheelOrders || st.Orders != 1 || st.MinOrders != 2 {
			t.Errorf("state: %+v %v", st, err)
		}
		e.payOrder(v)
		if r := e.spin(v); r.Status != WheelLost {
			t.Errorf("two paid orders: %+v", r)
		}
		// A refunded order does not count.
		w := e.customer()
		second := e.payOrder(w)
		e.deliver()
		e.deliver()
		if _, err := e.svc.RefundOrder(ctx, e.owner, e.fp, second.ID, "asked for it back"); err != nil {
			t.Fatal(err)
		}
		if r := e.spin(w); r.Status != WheelOrders || r.Orders != 1 {
			t.Errorf("one of two orders refunded: %+v", r)
		}
	})

	t.Run("a banned customer cannot spin or look", func(t *testing.T) {
		e := newWheelEnv(t)
		u := e.customer()
		if _, err := e.svc.SetUserStatus(ctx, e.owner, u.ID, "banned", "abuse"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.SpinWheel(ctx, u.ID); !errors.Is(err, ErrUserBanned) {
			t.Errorf("a banned spin: %v", err)
		}
		if _, err := e.svc.Wheel(ctx, u.ID); !errors.Is(err, ErrUserBanned) {
			t.Errorf("a banned look: %v", err)
		}
		if e.spins() != 0 || e.prizes() != 0 || e.balance(u) != 0 {
			t.Error("a banned customer got a spin")
		}
		// Unbanned, they may.
		if _, err := e.svc.SetUserStatus(ctx, e.owner, u.ID, "active", "sorry"); err != nil {
			t.Fatal(err)
		}
		if r := e.spin(u); r.Status != WheelWon {
			t.Errorf("after the ban was lifted: %+v", r)
		}
	})

	t.Run("a customer who spun and is banned afterwards keeps the prize and cannot ask again", func(t *testing.T) {
		e := newWheelEnv(t)
		u := e.customer()
		e.spin(u)
		if _, err := e.svc.SetUserStatus(ctx, e.owner, u.ID, "banned", "abuse"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.SpinWheel(ctx, u.ID); !errors.Is(err, ErrUserBanned) {
			t.Errorf("banned: %v", err)
		}
		if e.balance(u) != 1000 || e.prizes() != 1 {
			t.Errorf("balance %d, prizes %d", e.balance(u), e.prizes())
		}
	})

	t.Run("only customers spin: not a reseller, not staff", func(t *testing.T) {
		e := newWheelEnv(t)
		rs := e.reseller(7_200_001)
		e.payOrder(rs)
		for _, who := range []*store.User{rs, e.owner} {
			if r := e.spin(who); r.Status != WheelRole {
				t.Errorf("%s: %+v", who.Role, r)
			}
			if st, err := e.svc.Wheel(ctx, who.ID); err != nil || st.Reason != WheelRole {
				t.Errorf("%s state: %+v %v", who.Role, st, err)
			}
		}
		if e.spins() != 0 || e.prizes() != 0 {
			t.Error("a reseller or staff member got a spin")
		}
	})
}

func TestWheelPaysOncePerDay(t *testing.T) {
	ctx := context.Background()
	e := newWheelEnv(t)
	u := e.customer()
	e.rollAt(0) // 1,000

	first := e.spin(u)
	if first.Status != WheelWon || first.Prize != 1000 || first.Index != 0 || first.Balance != 1000 || first.Day != "2026-10-15" {
		t.Fatalf("first spin: %+v", first)
	}
	// Whatever is asked again the same day gets the same spin, never another draw or another credit.
	e.rollAt(3) // would be 5,000
	for range 3 {
		again := e.spin(u)
		if again.Status != WheelAlready || again.Prize != 1000 || again.Index != 0 || again.Balance != 1000 {
			t.Errorf("again: %+v", again)
		}
	}
	if e.balance(u) != 1000 || e.prizes() != 1 || e.spins() != 1 {
		t.Errorf("balance %d, prizes %d, spins %d", e.balance(u), e.prizes(), e.spins())
	}
	e.consistent(u)

	// What the ledger and the row say.
	var key, kind, refType string
	var amount int64
	if err := e.s.DB().QueryRow(ctx, `SELECT idempotency_key, kind, amount, ref_type FROM core.ledger_entries WHERE user_id = $1 AND kind = 'wheel_prize'`, u.ID).
		Scan(&key, &kind, &amount, &refType); err != nil {
		t.Fatal(err)
	}
	if key != "wheel:"+u.ID+":2026-10-15" || kind != "wheel_prize" || amount != 1000 || refType != "wheel_spin" {
		t.Errorf("the ledger entry: %s %s %d %s", key, kind, amount, refType)
	}
	var day string
	var prize, roll, total int64
	var entry *string
	if err := e.s.DB().QueryRow(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), prize, roll, total_weight, ledger_entry_id FROM core.wheel_spins WHERE user_id = $1`, u.ID).
		Scan(&day, &prize, &roll, &total, &entry); err != nil {
		t.Fatal(err)
	}
	if day != "2026-10-15" || prize != 1000 || roll != 0 || total != 8 || entry == nil {
		t.Errorf("the spin row: %s %d %d %d %v", day, prize, roll, total, entry)
	}
	// The event that carries the credit.
	evs := outboxEvents(t, e.s, events.WalletCredited)
	if len(evs) != 1 {
		t.Fatalf("%d wallet events", len(evs))
	}

	// The next day she may spin again, and the key is the new day's.
	e.at = e.at.Add(24 * time.Hour)
	e.rollAt(3)
	second := e.spin(u)
	if second.Status != WheelWon || second.Prize != 5000 || second.Index != 1 || second.Balance != 6000 || second.Day != "2026-10-16" {
		t.Errorf("the next day: %+v", second)
	}
	if e.prizes() != 2 || e.spins() != 2 || e.balance(u) != 6000 {
		t.Errorf("after two days: prizes %d, spins %d, balance %d", e.prizes(), e.spins(), e.balance(u))
	}
	e.consistent(u)
}

// A spin that lands on no prize still uses the day's spin, and writes no credit.
func TestWheelNoPrizeUsesTheSpin(t *testing.T) {
	e := newWheelEnv(t)
	u := e.customer()
	e.rollAt(6)
	r := e.spin(u)
	if r.Status != WheelLost || r.Prize != 0 || r.Index != -1 || r.Balance != 0 {
		t.Fatalf("no prize: %+v", r)
	}
	e.rollAt(0)
	if again := e.spin(u); again.Status != WheelAlready || again.Prize != 0 || again.Index != -1 {
		t.Errorf("asked again: %+v", again)
	}
	if e.prizes() != 0 || e.spins() != 1 || e.balance(u) != 0 {
		t.Errorf("prizes %d, spins %d, balance %d", e.prizes(), e.spins(), e.balance(u))
	}
	if st, err := e.svc.Wheel(context.Background(), u.ID); err != nil || !st.Spun || st.Prize != 0 {
		t.Errorf("state: %+v %v", st, err)
	}
}

// The day is the store's day (Tehran by default), not UTC's: 23:30 and 00:10 Tehran time are 20:00 and 20:40 UTC, two days though one UTC day.
func TestWheelDayIsTheStoreDay(t *testing.T) {
	e := newWheelEnv(t)
	u := e.customer()
	e.rollAt(0)
	e.at = time.Date(2026, 10, 15, 20, 0, 0, 0, time.UTC)
	a := e.spin(u)
	if a.Status != WheelWon || a.Day != "2026-10-15" {
		t.Fatalf("23:30 in Tehran: %+v", a)
	}
	if wantReset := time.Date(2026, 10, 15, 20, 30, 0, 0, time.UTC); !a.ResetsAt.Equal(wantReset) {
		t.Errorf("the next day begins at %v, want %v", a.ResetsAt.UTC(), wantReset)
	}
	e.at = time.Date(2026, 10, 15, 20, 29, 59, 0, time.UTC)
	if again := e.spin(u); again.Status != WheelAlready {
		t.Errorf("one second before midnight: %+v", again)
	}
	e.at = time.Date(2026, 10, 15, 20, 30, 0, 0, time.UTC)
	b := e.spin(u)
	if b.Status != WheelWon || b.Day != "2026-10-16" {
		t.Errorf("midnight in Tehran: %+v", b)
	}
	// In another zone the same instants fall on other days.
	e.set(map[string]string{"general.timezone": "America/Los_Angeles"})
	e.at = time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC) // 20:00 on the 15th in Los Angeles
	if c := e.spin(u); c.Status != WheelAlready || c.Day != "2026-10-15" {
		t.Errorf("Los Angeles: %+v", c)
	}
}

// Twelve requests for one customer's spin at once: one draw, one credit, and the same answer for every other.
func TestWheelConcurrentSpinsPayOnce(t *testing.T) {
	e := newWheelEnv(t)
	needConns(t, e.s, 14)
	u := e.customer()
	e.rollAt(0)
	const n = 12
	var wg sync.WaitGroup
	results := make([]*WheelResult, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = e.svc.SpinWheel(context.Background(), u.ID)
		}()
	}
	wg.Wait()
	won, already := 0, 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		switch results[i].Status {
		case WheelWon:
			won++
		case WheelAlready:
			already++
		default:
			t.Errorf("request %d: %+v", i, results[i])
		}
		if results[i].Prize != 1000 {
			t.Errorf("request %d saw a prize of %d", i, results[i].Prize)
		}
	}
	if won != 1 || already != n-1 {
		t.Errorf("%d won and %d got the earlier spin", won, already)
	}
	if e.balance(u) != 1000 || e.prizes() != 1 || e.spins() != 1 {
		t.Errorf("balance %d, prizes %d, spins %d", e.balance(u), e.prizes(), e.spins())
	}
	e.consistent(u)
}

// Many customers at once cannot overspend the day's budget: the spins are decided one after the other, each seeing what the earlier ones paid.
func TestWheelBudgetIsNeverOverspent(t *testing.T) {
	e := newWheelEnv(t)
	needConns(t, e.s, 14)
	e.set(map[string]string{SettingWheelPrizes: `[{"amount":1000,"weight":1},{"amount":5000,"weight":1}]`, SettingWheelNoPrize: "0", SettingWheelBudget: "12000"})
	const n = 16
	users := make([]*store.User, n)
	for i := range users {
		users[i] = e.customer()
	}
	e.rollAt(1) // 5,000 every time: at most two fit in 12,000
	var wg sync.WaitGroup
	results := make([]*WheelResult, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.svc.SpinWheel(context.Background(), users[i].ID)
			if err != nil {
				t.Errorf("spin %d: %v", i, err)
				return
			}
			results[i] = r
		}()
	}
	wg.Wait()
	won, closed := 0, 0
	for _, r := range results {
		if r == nil {
			continue
		}
		switch r.Status {
		case WheelWon:
			won++
		case WheelClosed:
			closed++
		default:
			t.Errorf("unexpected: %+v", r)
		}
	}
	if won != 2 || closed != n-2 {
		t.Errorf("%d won and %d were told to come back tomorrow", won, closed)
	}
	paid := e.count(`SELECT coalesce(sum(prize), 0) FROM core.wheel_spins`)
	if paid != 10_000 || e.prizes() != 2 || e.spins() != 2 {
		t.Errorf("paid %d in %d entries over %d spins", paid, e.prizes(), e.spins())
	}
	for _, u := range users {
		e.consistent(u)
	}
}

// A prize the day's budget cannot cover is not paid and does not use the spin; the customer tries again tomorrow, and meanwhile a smaller prize that
// fits may still come up.
func TestWheelBudgetCap(t *testing.T) {
	ctx := context.Background()
	e := newWheelEnv(t)
	e.set(map[string]string{SettingWheelPrizes: `[{"amount":1000,"weight":1},{"amount":5000,"weight":1}]`, SettingWheelNoPrize: "0", SettingWheelBudget: "6500"})
	a, b, c := e.customer(), e.customer(), e.customer()

	e.rollAt(1) // 5,000: fits
	if r := e.spin(a); r.Status != WheelWon || r.Prize != 5000 {
		t.Fatalf("a: %+v", r)
	}
	// 1,500 left. The wheel stops on 5,000: it cannot pay it.
	r := e.spin(b)
	if r.Status != WheelClosed || r.Prize != 0 || r.Index != -1 || r.Balance != 0 || !r.ResetsAt.After(e.at) {
		t.Fatalf("b over budget: %+v", r)
	}
	if e.balance(b) != 0 || e.spins() != 1 || e.prizes() != 1 {
		t.Fatalf("the refused spin wrote something: balance %d, spins %d, prizes %d", e.balance(b), e.spins(), e.prizes())
	}
	if st, err := e.svc.Wheel(ctx, b.ID); err != nil || st.Spun || !st.Open {
		t.Errorf("b's state, the spin not used and 1,500 left: %+v %v", st, err)
	}
	// The spin was not used: the next try may stop on a prize that fits.
	e.rollAt(0) // 1,000
	if r := e.spin(b); r.Status != WheelWon || r.Prize != 1000 || r.Index != 0 {
		t.Errorf("b again: %+v", r)
	}
	// 500 left: not even the smallest prize fits, whatever the draw.
	for _, v := range []int64{0, 1} {
		e.rollAt(v)
		if r := e.spin(c); r.Status != WheelClosed {
			t.Errorf("c, roll %d: %+v", v, r)
		}
	}
	if st, err := e.svc.Wheel(ctx, c.ID); err != nil || st.Open || st.Spun {
		t.Errorf("c's state, the budget gone: %+v %v", st, err)
	}
	if paid := e.count(`SELECT coalesce(sum(prize), 0) FROM core.wheel_spins`); paid != 6000 {
		t.Errorf("paid %d of 6,500", paid)
	}
	// Tomorrow the budget is new, and c's spin was never used.
	e.at = e.at.Add(24 * time.Hour)
	e.rollAt(1)
	if r := e.spin(c); r.Status != WheelWon || r.Prize != 5000 {
		t.Errorf("c tomorrow: %+v", r)
	}
	for _, u := range []*store.User{a, b, c} {
		e.consistent(u)
	}
}

// The budget is what the day's rows add up to, so it follows a change of the owner's mind: raising it opens the wheel again the same day.
func TestWheelBudgetFollowsTheOwner(t *testing.T) {
	e := newWheelEnv(t)
	e.set(map[string]string{SettingWheelPrizes: `[{"amount":5000,"weight":1}]`, SettingWheelNoPrize: "0", SettingWheelBudget: "5000"})
	a, b := e.customer(), e.customer()
	e.rollAt(0)
	if r := e.spin(a); r.Status != WheelWon {
		t.Fatal(r)
	}
	if r := e.spin(b); r.Status != WheelClosed {
		t.Fatal(r)
	}
	e.set(map[string]string{SettingWheelBudget: "10000"})
	if r := e.spin(b); r.Status != WheelWon || r.Prize != 5000 {
		t.Errorf("after the budget was raised: %+v", r)
	}
	// Lowered below what was paid: nothing more, and what was paid stays.
	e.set(map[string]string{SettingWheelBudget: "1"})
	c := e.customer()
	if r := e.spin(c); r.Status != WheelClosed {
		t.Errorf("after the budget was lowered: %+v", r)
	}
}

// A customer who spun keeps the answer when the owner changes the prizes: the prize is shown, and its place only if the wheel still has it.
func TestWheelEarlierSpinSurvivesTheOwnerEditingThePrizes(t *testing.T) {
	e := newWheelEnv(t)
	u := e.customer()
	e.rollAt(3) // 5,000
	if r := e.spin(u); r.Prize != 5000 || r.Index != 1 {
		t.Fatal(r)
	}
	e.set(map[string]string{SettingWheelPrizes: `[{"amount":2000,"weight":1},{"amount":5000,"weight":9}]`})
	if r := e.spin(u); r.Status != WheelAlready || r.Prize != 5000 || r.Index != 1 {
		t.Errorf("a prize still on the wheel: %+v", r)
	}
	e.set(map[string]string{SettingWheelPrizes: `[{"amount":2000,"weight":1}]`})
	if r := e.spin(u); r.Status != WheelAlready || r.Prize != 5000 || r.Index != -1 {
		t.Errorf("a prize no longer on the wheel: %+v", r)
	}
	// Switched off: the wheel is gone, and what was won stays in the wallet.
	e.set(map[string]string{SettingWheelEnabled: "false"})
	if r := e.spin(u); r.Status != WheelOff {
		t.Errorf("off: %+v", r)
	}
	if e.balance(u) != 5000 {
		t.Errorf("balance %d", e.balance(u))
	}
}

// A failure anywhere writes nothing: the draw, the credit and the spin are one transaction.
func TestWheelFailureWritesNothing(t *testing.T) {
	ctx := context.Background()
	e := newWheelEnv(t)
	u := e.customer()

	e.roll = func(int64) (int64, error) { return 0, errors.New("no randomness today") }
	if _, err := e.svc.SpinWheel(ctx, u.ID); err == nil {
		t.Error("a failed draw spun")
	}
	e.roll = func(n int64) (int64, error) { return n, nil } // outside the range
	if _, err := e.svc.SpinWheel(ctx, u.ID); err == nil {
		t.Error("a draw outside the range spun")
	}
	e.roll = func(int64) (int64, error) { return -1, nil }
	if _, err := e.svc.SpinWheel(ctx, u.ID); err == nil {
		t.Error("a negative draw spun")
	}
	if e.spins() != 0 || e.prizes() != 0 || e.balance(u) != 0 {
		t.Fatalf("a failed spin wrote something: spins %d, prizes %d, balance %d", e.spins(), e.prizes(), e.balance(u))
	}

	// The ledger already holds this customer's prize for the day but no spin does (the books disagree): nothing is paid again, and the spin fails.
	err := e.s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := e.s.Credit(ctx, tx, u.ID, "IRT", 1000, &store.LedgerEntry{Kind: "wheel_prize", IdempotencyKey: "wheel:" + u.ID + ":2026-10-15"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.rollAt(0)
	before := e.balance(u)
	if _, err := e.svc.SpinWheel(ctx, u.ID); err == nil {
		t.Error("a prize already in the ledger was paid again")
	}
	if e.balance(u) != before || e.spins() != 0 || e.prizes() != 1 {
		t.Errorf("balance %d (was %d), spins %d, prizes %d", e.balance(u), before, e.spins(), e.prizes())
	}
	e.consistent(u)
}

// The spin row cannot disagree with the ledger even when written by hand: a prize without its credit, a second spin on a day, a roll outside the
// weights are all refused by the table.
func TestWheelTableRefusesWhatTheRulesForbid(t *testing.T) {
	ctx := context.Background()
	e := newWheelEnv(t)
	u := e.customer()
	insert := func(day string, prize int64, entry *string, roll, total int64) error {
		_, err := e.s.DB().Exec(ctx, `INSERT INTO core.wheel_spins (user_id, day, prize, ledger_entry_id, roll, total_weight) VALUES ($1, $2::date, $3, $4, $5, $6)`,
			u.ID, day, prize, entry, roll, total)
		return err
	}
	if err := insert("2026-10-15", 1000, nil, 0, 8); err == nil {
		t.Error("a prize without a credit")
	}
	var entry string
	if err := e.s.DB().QueryRow(ctx, `SELECT id FROM core.ledger_entries WHERE user_id = $1 LIMIT 1`, u.ID).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	if err := insert("2026-10-15", 0, &entry, 5, 8); err == nil {
		t.Error("a credit for no prize")
	}
	if err := insert("2026-10-15", 0, nil, 8, 8); err == nil {
		t.Error("a roll outside the weights")
	}
	if err := insert("2026-10-15", 0, nil, 0, 0); err == nil {
		t.Error("no weights")
	}
	if err := insert("2026-10-15", -1, nil, 0, 8); err == nil {
		t.Error("a negative prize")
	}
	if err := insert("2026-10-15", 0, nil, 3, 8); err != nil {
		t.Fatalf("a spin: %v", err)
	}
	if err := insert("2026-10-15", 0, nil, 3, 8); err == nil {
		t.Error("two spins on a day")
	}
	if err := insert("2026-10-16", 0, nil, 3, 8); err != nil {
		t.Errorf("the next day: %v", err)
	}
}

func TestWheelStateAndReport(t *testing.T) {
	ctx := context.Background()
	e := newWheelEnv(t)
	a, b := e.customer(), e.customer()

	st, err := e.svc.Wheel(ctx, a.ID)
	if err != nil || !st.On || st.Reason != "" || st.Spun || !st.Open || st.Day != "2026-10-15" || st.TotalWeight != 8 || st.NoPrizeWeight != 4 ||
		len(st.Prizes) != 2 || st.Prizes[0] != (WheelPrize{1000, 3}) || st.Prizes[1] != (WheelPrize{5000, 1}) || st.Currency != "IRT" || st.Orders != 1 || st.MinOrders != 1 {
		t.Fatalf("state: %+v %v", st, err)
	}
	e.rollAt(0)
	e.spin(a) // 1,000
	e.rollAt(3)
	e.spin(b) // 5,000
	e.at = e.at.Add(-48 * time.Hour)
	e.rollAt(0)
	e.spin(a) // two days earlier: 1,000
	e.at = e.at.Add(48 * time.Hour)

	st, err = e.svc.Wheel(ctx, a.ID)
	if err != nil || !st.Spun || st.Prize != 1000 {
		t.Errorf("a after spinning: %+v %v", st, err)
	}
	rep, err := e.svc.WheelReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.On || rep.Day != "2026-10-15" || rep.Budget != 20000 || rep.Today.Spins != 2 || rep.Today.Winners != 2 || rep.Today.Paid != 6000 || rep.Remaining != 14000 ||
		len(rep.Days) != 2 || rep.Total.Spins != 3 || rep.Total.Paid != 7000 {
		t.Errorf("report: %+v", rep)
	}
	if rep.Days[0].Day != "2026-10-15" || rep.Days[1].Day != "2026-10-13" || rep.Days[1].Paid != 1000 {
		t.Errorf("days: %+v", rep.Days)
	}
	// The wheel turned off still reports what it paid.
	e.set(map[string]string{SettingWheelEnabled: "false"})
	if rep, err = e.svc.WheelReport(ctx); err != nil || rep.On || rep.Total.Paid != 7000 {
		t.Errorf("report with the wheel off: %+v %v", rep, err)
	}
}
