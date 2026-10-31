package domain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// One wallet per customer, in Toman. A customer may top up in Toman or in USDT and spend on anything: money that arrives in USDT is converted to
// Toman at the live rate, and what is priced in USDT (a license) is charged in Toman at the live rate. The rules:
//
//   - A purchase priced in USDT costs the store's rate (UsdtRate: the live price plus the owner's margin, or the owner's own rate), rounded up to
//     a whole Toman.
//   - Money that arrives in USDT is credited at the live price without the margin (UsdtBaseRate), rounded down: the margin is the store's
//     spread, so a customer who tops up in USDT and spends on a Toman plan is never given more than the market price.
//   - The balance is shown in Toman with its equivalent in USDT at the live price (UsdtEquivalent).
//   - When there is no rate at all (no live price and none typed) nothing is converted and nothing is refused that was already paid: a USDT top-up
//     waits in the USDT wallet and the sweeper converts it as soon as a rate is known.

const (
	// tomanCurrency is the wallet's currency (WalletCurrency, for the layers above core).
	tomanCurrency = "IRT"
	// usdtScale is how many minor units a USDT has.
	usdtScale = 1_000_000
	// sweepEvery is how often the sweeper looks for money held in USDT.
	sweepEvery = 15 * time.Second
	sweepBatch = 200
)

// WalletCurrency is the currency of every customer's wallet.
const WalletCurrency = tomanCurrency

// ErrNoUsdtRate: a conversion was asked for and the store knows no rate for USDT.
var ErrNoUsdtRate = errors.New("domain: the USDT rate is not known")

// UsdtBaseRate is the Toman price of one USDT without the owner's margin: the live price while it is fresh and automatic rates are on, else the
// owner's own rate; 0 when none is known.
func (s *Service) UsdtBaseRate(ctx context.Context) int64 {
	return usdtBaseRate(s.UsdtRateInfo(ctx))
}

func usdtBaseRate(info UsdtRateInfo) int64 {
	switch info.Source {
	case "live":
		return info.Live.Rate
	case "manual":
		return info.Manual
	}
	return 0
}

// usdtBaseRateIn is UsdtBaseRate read through an open transaction: a handler that holds a connection must not ask the pool for a second one (with
// every connection held by such a handler, none would ever be free).
func (s *Service) usdtBaseRateIn(ctx context.Context, tx pgx.Tx) int64 {
	m, err := s.st.GetSettings(ctx, tx)
	if err != nil {
		return 0
	}
	return usdtBaseRate(usdtRateInfoFrom(m, time.Now()))
}

// mulDiv is a*b/c with a big intermediate, rounded up (ceil) or down, and a result that fits an int64 (false otherwise).
func mulDiv(a, b, c int64, ceil bool) (int64, bool) {
	if c <= 0 || a < 0 || b < 0 {
		return 0, false
	}
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	q, r := new(big.Int).QuoRem(n, big.NewInt(c), new(big.Int))
	if ceil && r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, false
	}
	return q.Int64(), true
}

// TomanForUsdt is what a price in USDT (minor units) costs in Toman at the store's rate, rounded up, and the rate it was worked out at.
func (s *Service) TomanForUsdt(ctx context.Context, usdt int64) (toman, rate int64, err error) {
	rate = s.UsdtRate(ctx)
	if rate <= 0 {
		return 0, 0, ErrNoUsdtRate
	}
	toman, ok := mulDiv(usdt, rate, usdtScale, true)
	if !ok {
		return 0, 0, invalid("that amount is out of range")
	}
	return toman, rate, nil
}

// UsdtEquivalent is what a sum of Toman is worth in USDT (minor units) at the live price, rounded to the nearest 0.01 USDT: for showing a
// balance. 0 when no rate is known.
func (s *Service) UsdtEquivalent(ctx context.Context, toman int64) int64 {
	return UsdtEquivalentAt(toman, s.UsdtBaseRate(ctx))
}

// UsdtEquivalentAt is UsdtEquivalent at a rate already read (UsdtBaseRate), for a caller that shows the rate beside it.
func UsdtEquivalentAt(toman, rate int64) int64 {
	if rate <= 0 || toman <= 0 {
		return 0
	}
	micro, ok := mulDiv(toman, usdtScale, rate, false)
	if !ok {
		return 0
	}
	const cent = usdtScale / 100
	return (micro + cent/2) / cent * cent
}

// convertToToman moves a foreign wallet's whole balance (one wallet of one customer, inside tx) into the Toman wallet at the live price, rounded
// down, as two ledger entries of kind wallet_convert. It returns the Toman credited and the Toman balance after it; 0 when there was nothing to
// convert, no rate is known, or the amount is too small to be one Toman. The idempotency key carries the ledger's last entry of the wallet, so
// two sweepers (or a replay) never convert the same money twice.
func (s *Service) convertToToman(ctx context.Context, tx pgx.Tx, userID, currency string, rate int64) (credited, balance int64, usedRate int64, err error) {
	if currency != "USDT" || rate <= 0 {
		return 0, 0, 0, nil // only USDT has a rate, and nothing is converted without one
	}
	w, err := s.st.GetWallet(ctx, tx, userID, currency)
	if err != nil {
		return 0, 0, 0, err
	}
	if w.Balance <= 0 {
		return 0, 0, 0, nil
	}
	toman, ok := mulDiv(w.Balance, rate, usdtScale, false)
	if !ok || toman <= 0 {
		return 0, 0, 0, nil
	}
	// One conversion per state of the USDT wallet: the key names the balance it converts and the ledger's newest entry of that wallet.
	var last *string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM core.ledger_entries WHERE user_id = $1 AND currency = $2 ORDER BY created_at DESC, id DESC LIMIT 1`,
		userID, currency).Scan(&last); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, 0, fmt.Errorf("convert wallet: %w", err)
	}
	key := fmt.Sprintf("wallet_convert:%s:%s:%s:%d", userID, currency, deref(last), w.Balance)
	if _, err := s.st.Debit(ctx, tx, userID, currency, w.Balance, &store.LedgerEntry{Kind: "wallet_convert", RefType: strptr("wallet"), IdempotencyKey: key + ":out"}); err != nil {
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			return 0, 0, 0, nil
		}
		return 0, 0, 0, err
	}
	in, err := s.st.Credit(ctx, tx, userID, tomanCurrency, toman, &store.LedgerEntry{Kind: "wallet_convert", RefType: strptr("wallet"), IdempotencyKey: key + ":in"})
	if err != nil {
		return 0, 0, 0, err
	}
	return toman, in.Balance, rate, nil
}

// parseWholeSetting reads a whole-number setting the way settingInt does ("271,600" and "271600" both work); 0 when it is none.
func parseWholeSetting(v string) int64 {
	v = strings.NewReplacer(",", "", "_", "", " ", "", "٬", "").Replace(latinDigits(v))
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// SweepUsdtWallets converts the money customers hold in USDT into Toman, up to a batch of wallets; it returns how many it converted. It is what
// makes a USDT top-up, a USDT gift code and the license wallet of before into Toman, and it waits (changing nothing) while no rate is known.
func (s *Service) SweepUsdtWallets(ctx context.Context) (int, error) {
	rate := s.UsdtBaseRate(ctx) // read once, before any transaction opens
	if rate <= 0 {
		return 0, nil
	}
	list, err := s.st.ForeignBalances(ctx, s.st.Conn(), sweepBatch)
	if err != nil || len(list) == 0 {
		return 0, err
	}
	n := 0
	for _, b := range list {
		var credited int64
		err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
			var err error
			credited, _, _, err = s.convertToToman(ctx, tx, b.UserID, b.Currency, rate)
			return err
		})
		if err != nil {
			s.cfg.Log.Warn("wallet sweep", "user", b.UserID, "currency", b.Currency, "err", err)
			continue
		}
		if credited > 0 {
			n++
		}
	}
	return n, nil
}

// RunWalletSweeper keeps converting USDT balances into Toman.
func (s *Service) RunWalletSweeper(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.SweepUsdtWallets(ctx); err != nil && ctx.Err() == nil {
				s.cfg.Log.Warn("wallet sweeper", "err", err)
			}
		}
	}
}
