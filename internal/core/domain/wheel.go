package domain

import (
	"bytes"
	"cmp"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The daily wheel of fortune (docs/03-bot-ux.md, "Daily wheel"): a customer who has made enough paid orders spins once per store-local day, free, for a
// chance at wallet credit. It is off until the owner switches it on and gives it prizes and a daily budget.
//
// The rules the code keeps:
//   - one spin per customer per store-local day (the zone of the reports): the row (user, day) is unique, and the ledger key of a prize is
//     wheel:<user id>:<day>; a second request the same day is answered with the first spin, never a second draw;
//   - the draw is made here, from crypto/rand, over the weights the owner set; no client ever decides, and the Mini App's animation only plays the
//     result it was given;
//   - the prize and the spin are written in the one transaction, under a lock on the day, so the day's budget (what its rows add up to) cannot be
//     overspent by two spins at once; a prize the budget cannot cover is not paid and does not use the spin: the customer is asked to come back
//     tomorrow;
//   - only customers spin: a banned customer, a reseller and staff do not; nobody spins without the paid orders the owner asks for (a floor of one,
//     so an account made only to spin earns nothing).
const (
	SettingWheelEnabled   = "wheel.enabled"
	SettingWheelPrizes    = "wheel.prizes"
	SettingWheelNoPrize   = "wheel.no_prize_weight"
	SettingWheelBudget    = "wheel.daily_budget"
	SettingWheelMinOrders = "wheel.min_orders"

	// MaxWheelPrizes is the most prizes the owner may list.
	MaxWheelPrizes = 12
	// MaxWheelWeight is the largest weight of a prize, and MaxWheelNoPrizeWeight of "no prize".
	MaxWheelWeight        = 1_000_000
	MaxWheelNoPrizeWeight = 1_000_000_000
	// MaxWheelMinOrders is the most paid orders the owner may ask for.
	MaxWheelMinOrders = 1000

	// The statuses of a spin (WheelResult.Status).
	WheelWon     = "won"     // the wheel stopped on a prize, now in the wallet
	WheelLost    = "lost"    // the wheel stopped on no prize; the day's spin is used
	WheelAlready = "already" // the customer spun today: this is that spin
	WheelClosed  = "closed"  // the day's budget cannot cover the prize drawn; the spin is NOT used
	WheelOff     = "off"     // the wheel is not available
	WheelRole    = "role"    // not a customer (a reseller or staff)
	WheelOrders  = "orders"  // not enough paid orders yet
)

// WheelPrize is one segment of the wheel: Amount Toman, drawn with the chance Weight out of the total.
type WheelPrize struct {
	Amount int64 `json:"amount"`
	Weight int64 `json:"weight"`
}

var errPrizes = errors.New("a list of prizes, each with an amount and a weight")

// ParseWheelPrizes reads the owner's list (a JSON array of {"amount", "weight"}) the way it is saved: whole numbers only, at most MaxWheelPrizes
// prizes, each amount from 1 to MaxGrowthAmount and each weight from 1 to MaxWheelWeight, no amount twice. The result is sorted by amount, smallest
// first (the order of the wheel). An empty text is no prizes.
func ParseWheelPrizes(raw string) ([]WheelPrize, error) {
	if raw == "" {
		return nil, nil
	}
	var prizes []WheelPrize
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&prizes); err != nil || dec.More() {
		return nil, errPrizes
	}
	if len(prizes) > MaxWheelPrizes {
		return nil, fmt.Errorf("at most %d prizes", MaxWheelPrizes)
	}
	for _, p := range prizes {
		switch {
		case p.Amount < 1 || p.Amount > MaxGrowthAmount:
			return nil, errors.New("a prize is a whole number of Toman, at least 1")
		case p.Weight < 1 || p.Weight > MaxWheelWeight:
			return nil, fmt.Errorf("a prize's weight is a whole number from 1 to %d", MaxWheelWeight)
		}
	}
	slices.SortFunc(prizes, func(a, b WheelPrize) int { return cmp.Compare(a.Amount, b.Amount) })
	for i := 1; i < len(prizes); i++ {
		if prizes[i].Amount == prizes[i-1].Amount {
			return nil, errors.New("two prizes have the same amount")
		}
	}
	return prizes, nil
}

// FormatWheelPrizes writes the list the way it is stored: smallest amount first, compact JSON ("" for none).
func FormatWheelPrizes(prizes []WheelPrize) string {
	if len(prizes) == 0 {
		return ""
	}
	sorted := slices.Clone(prizes)
	slices.SortFunc(sorted, func(a, b WheelPrize) int { return cmp.Compare(a.Amount, b.Amount) })
	raw, _ := json.Marshal(sorted)
	return string(raw)
}

// WheelConfig is the owner's wheel settings, read.
type WheelConfig struct {
	Enabled bool
	// Prizes, smallest amount first.
	Prizes []WheelPrize
	// NoPrizeWeight is the weight of the segment that pays nothing.
	NoPrizeWeight int64
	// DailyBudget is the most Toman the wheel pays out in one store-local day (0: not set, so the wheel is closed).
	DailyBudget int64
	// MinOrders is how many paid orders a customer needs before they may spin (at least 1).
	MinOrders int64
}

// WheelConfigOf reads the settings. A value that is not valid reads as unset (they are checked as they are saved, and a bad one must not pay on a
// guess).
func WheelConfigOf(set map[string]string) WheelConfig {
	c := WheelConfig{Enabled: set[SettingWheelEnabled] == "true", MinOrders: 1}
	if prizes, err := ParseWheelPrizes(set[SettingWheelPrizes]); err == nil {
		c.Prizes = prizes
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(set[SettingWheelNoPrize]), 10, 64); err == nil && n >= 0 && n <= MaxWheelNoPrizeWeight {
		c.NoPrizeWeight = n
	}
	c.DailyBudget = settingAmount(set[SettingWheelBudget])
	if n, err := strconv.ParseInt(strings.TrimSpace(set[SettingWheelMinOrders]), 10, 64); err == nil && n >= 1 && n <= MaxWheelMinOrders {
		c.MinOrders = n
	}
	return c
}

// On reports whether the wheel can pay: switched on, with at least one prize and a daily budget.
func (c WheelConfig) On() bool { return c.Enabled && len(c.Prizes) > 0 && c.DailyBudget > 0 }

// TotalWeight is the weight of every segment, "no prize" included.
func (c WheelConfig) TotalWeight() int64 {
	total := c.NoPrizeWeight
	for _, p := range c.Prizes {
		total += p.Weight
	}
	return total
}

// smallestPrize is the least a spin can pay.
func (c WheelConfig) smallestPrize() int64 {
	if len(c.Prizes) == 0 {
		return 0
	}
	return c.Prizes[0].Amount // sorted
}

// pick says which segment a roll in [0, TotalWeight) lands on: the index of a prize, or -1 for no prize. The segments run in the order of the list,
// "no prize" last.
func (c WheelConfig) pick(roll int64) int {
	var upto int64
	for i, p := range c.Prizes {
		upto += p.Weight
		if roll < upto {
			return i
		}
	}
	return -1
}

// indexOfPrize is the place of a prize amount in the list (-1 when the list has no such amount any more).
func (c WheelConfig) indexOfPrize(amount int64) int {
	return slices.IndexFunc(c.Prizes, func(p WheelPrize) bool { return p.Amount == amount })
}

// drawWheel is a uniform number in [0, n): from crypto/rand, or Config.WheelRand in tests.
func (s *Service) drawWheel(n int64) (int64, error) {
	if n <= 0 {
		return 0, errors.New("wheel: nothing to draw from")
	}
	if s.cfg.WheelRand != nil {
		r, err := s.cfg.WheelRand(n)
		if err != nil {
			return 0, err
		}
		if r < 0 || r >= n {
			return 0, fmt.Errorf("wheel: the draw %d is outside 0..%d", r, n-1)
		}
		return r, nil
	}
	r, err := crand.Int(crand.Reader, big.NewInt(n))
	if err != nil {
		return 0, fmt.Errorf("wheel: draw: %w", err)
	}
	return r.Int64(), nil
}

// wheelDay is the store-local day of an instant ("2026-10-28") and the instant the next one begins.
func wheelDay(at time.Time, loc *time.Location) (string, time.Time) {
	local := at.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
	return local.Format(dateLayout), next
}

// WheelState is what a customer sees of the wheel before they spin.
type WheelState struct {
	// On is false while the wheel cannot pay (off, or no prizes, or no budget): nothing else is filled in.
	On  bool
	Day string
	// ResetsAt is the instant the next day, and the next spin, begins.
	ResetsAt time.Time
	// Reason says why this customer may not spin: WheelRole or WheelOrders (empty: they may, as far as the orders go).
	Reason            string
	Orders, MinOrders int64
	// Spun and Prize: the customer has spun today, and won this (0: no prize).
	Spun  bool
	Prize int64
	// Open: what is left of the day's budget still covers the smallest prize.
	Open          bool
	Prizes        []WheelPrize
	NoPrizeWeight int64
	TotalWeight   int64
	Currency      string
}

// Wheel is the wheel as a customer sees it now: what could be won, the chance of each, whether they may spin, and whether they have today.
func (s *Service) Wheel(ctx context.Context, userID string) (*WheelState, error) {
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	cfg := WheelConfigOf(set)
	if !cfg.On() {
		return &WheelState{}, nil
	}
	day, next := wheelDay(s.now(), s.storeLocation(set))
	st := &WheelState{On: true, Day: day, ResetsAt: next, MinOrders: cfg.MinOrders, Prizes: cfg.Prizes, NoPrizeWeight: cfg.NoPrizeWeight,
		TotalWeight: cfg.TotalWeight(), Currency: WalletCurrency}
	if u.Role != "user" {
		st.Reason = WheelRole
		return st, nil
	}
	if st.Orders, err = s.st.CountPaidOrders(ctx, s.st.Conn(), u.ID); err != nil {
		return nil, err
	}
	if st.Orders < cfg.MinOrders {
		st.Reason = WheelOrders
	}
	spin, err := s.st.WheelSpinOf(ctx, s.st.Conn(), u.ID, day)
	switch {
	case err == nil:
		st.Spun, st.Prize = true, spin.Prize
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	paid, err := s.st.WheelPaidOn(ctx, s.st.Conn(), day)
	if err != nil {
		return nil, err
	}
	st.Open = cfg.DailyBudget-paid >= cfg.smallestPrize()
	return st, nil
}

// WheelResult is the answer to a spin.
type WheelResult struct {
	// Status is one of the Wheel* statuses.
	Status string
	// Prize is the Toman won (WheelWon), or the earlier spin's (WheelAlready); 0 otherwise.
	Prize int64
	// Index is the place of the prize on the wheel (WheelState.Prizes), -1 for no prize or a prize the list no longer holds.
	Index int
	// Balance is the customer's wallet now (WheelWon, WheelLost, WheelAlready, WheelClosed).
	Balance int64
	Day     string
	// ResetsAt is when the next spin is possible.
	ResetsAt time.Time
	// Orders and MinOrders: for WheelOrders, what the customer has and what is asked.
	Orders, MinOrders int64
}

// SpinWheel gives the customer their spin of the day. See the rules at the top of this file. A customer who spun already is answered with that spin
// (WheelAlready); a banned customer is refused with ErrUserBanned; every other refusal is a status of the result, and writes nothing.
func (s *Service) SpinWheel(ctx context.Context, userID string) (*WheelResult, error) {
	var out *WheelResult
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		out = nil
		// A ban that fell while the request waited counts: the customer is read again inside the transaction.
		u, err := s.activeUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		set, err := s.settingsIn(ctx, tx)
		if err != nil {
			return err
		}
		cfg := WheelConfigOf(set)
		if !cfg.On() {
			out = &WheelResult{Status: WheelOff, Index: -1}
			return nil
		}
		day, next := wheelDay(s.now(), s.storeLocation(set))
		res := &WheelResult{Index: -1, Day: day, ResetsAt: next, MinOrders: cfg.MinOrders}
		if u.Role != "user" {
			res.Status = WheelRole
			out = res
			return nil
		}

		// From here on the spins of the day are decided one at a time.
		if err := s.st.LockWheelDay(ctx, tx, day); err != nil {
			return err
		}
		balance := func() (int64, error) {
			w, err := s.st.GetWallet(ctx, tx, u.ID, WalletCurrency) // makes the wallet when the customer has none yet
			if err != nil {
				return 0, err
			}
			return w.Balance, nil
		}

		// Spun today already: the same answer again, whatever was asked in between (a double tap, a retry, the bot and the app at once).
		prev, err := s.st.WheelSpinOf(ctx, tx, u.ID, day)
		switch {
		case err == nil:
			res.Status, res.Prize, res.Index = WheelAlready, prev.Prize, -1
			if prev.Prize > 0 {
				res.Index = cfg.indexOfPrize(prev.Prize)
			}
			if res.Balance, err = balance(); err != nil {
				return err
			}
			out = res
			return nil
		case !errors.Is(err, store.ErrNotFound):
			return err
		}

		if res.Orders, err = s.st.CountPaidOrders(ctx, tx, u.ID); err != nil {
			return err
		}
		if res.Orders < cfg.MinOrders {
			res.Status = WheelOrders
			out = res
			return nil
		}

		paid, err := s.st.WheelPaidOn(ctx, tx, day)
		if err != nil {
			return err
		}
		remaining := cfg.DailyBudget - paid
		closed := func() error {
			res.Status = WheelClosed
			var err error
			res.Balance, err = balance()
			out = res
			return err
		}
		if remaining < cfg.smallestPrize() {
			return closed() // nothing the wheel can pay is left today
		}

		total := cfg.TotalWeight()
		roll, err := s.drawWheel(total)
		if err != nil {
			return err
		}
		index := cfg.pick(roll)
		var prize int64
		if index >= 0 {
			prize = cfg.Prizes[index].Amount
		}
		if prize > remaining {
			return closed() // the budget cannot cover what came up: no prize is paid and the spin is not used
		}

		id, err := buuid.NewV7()
		if err != nil {
			return err
		}
		spin := &store.WheelSpin{ID: id.String(), UserID: u.ID, Day: day, Prize: prize, Roll: roll, TotalWeight: total}
		if prize > 0 {
			entry := &store.LedgerEntry{Kind: "wheel_prize", RefType: strptr("wheel_spin"), RefID: strptr(spin.ID),
				IdempotencyKey: "wheel:" + u.ID + ":" + day}
			w, err := s.st.Credit(ctx, tx, u.ID, WalletCurrency, prize, entry)
			if errors.Is(err, store.ErrDuplicateIdempotency) || errors.Is(err, store.ErrIdempotencyConflict) {
				// The ledger holds a prize for this customer and day but no spin does. Nothing is paid again; the books are for a person to look at.
				s.cfg.Log.Error("wheel: the ledger key of a prize is taken but no spin is recorded; nothing was paid", "user", u.ID, "day", day)
				return fmt.Errorf("wheel: the prize of %s for %s is in the ledger without a spin", u.ID, day)
			}
			if err != nil {
				return err
			}
			spin.LedgerEntryID = &entry.ID
			res.Balance = w.Balance
		}
		if err := s.st.InsertWheelSpin(ctx, tx, spin); err != nil {
			return err
		}
		if prize > 0 {
			err = s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{UserID: u.ID, TelegramID: u.TelegramID, Amount: prize,
				Currency: WalletCurrency, Balance: res.Balance, Reason: events.CreditWheelPrize})
			if err != nil {
				return err
			}
			res.Status, res.Prize, res.Index = WheelWon, prize, index
		} else {
			res.Status = WheelLost
			if res.Balance, err = balance(); err != nil {
				return err
			}
		}
		out = res
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// WheelReport is the owner's small report of the wheel: today against the budget, and the days of the last month.
type WheelReport struct {
	// On: the wheel can pay now.
	On       bool
	Currency string
	Day      string
	Budget   int64
	// Today's spins; Remaining is what is left of the budget.
	Today     store.WheelDay
	Remaining int64
	// Days with at least one spin in the last 30 days (today included), newest first, and their sum.
	Days  []store.WheelDay
	Total store.WheelDay
}

// WheelReportDays is how many days back the report goes.
const WheelReportDays = 30

// WheelReport reads the wheel's figures; it writes nothing.
func (s *Service) WheelReport(ctx context.Context) (*WheelReport, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	cfg := WheelConfigOf(set)
	loc := s.storeLocation(set)
	day, _ := wheelDay(s.now(), loc)
	today, err := time.Parse(dateLayout, day)
	if err != nil {
		return nil, err
	}
	days, err := s.st.WheelDays(ctx, s.st.Conn(), today.AddDate(0, 0, -(WheelReportDays-1)).Format(dateLayout), day)
	if err != nil {
		return nil, err
	}
	r := &WheelReport{On: cfg.On(), Currency: WalletCurrency, Day: day, Budget: cfg.DailyBudget, Days: days, Today: store.WheelDay{Day: day}}
	for _, d := range days {
		r.Total.Spins += d.Spins
		r.Total.Winners += d.Winners
		r.Total.Paid += d.Paid
		if d.Day == day {
			r.Today = d
		}
	}
	r.Remaining = max(cfg.DailyBudget-r.Today.Paid, 0)
	return r, nil
}
