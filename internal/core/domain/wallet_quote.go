package domain

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Paying a plan priced in USDT from the wallet. The wallet is in Toman, so the plan's USDT price is worked out in Toman at the store's USDT rate
// (TomanForUsdt: the live price plus the owner's margin, or the owner's own rate, rounded up to a whole Toman: the rule the licenses are charged by).
// The customer is shown that price first, as a quote that holds for WalletQuoteTTL: a payment that carries the quote is charged exactly what it said,
// whatever the rate did meanwhile, and one that comes after it is refused (ErrQuoteExpired) so that a new quote is made and shown again. A quote is a
// signed token (core's master key), so core keeps nothing between the two calls and a quote cannot be made up, changed or used for another order or
// customer. Without any USDT rate nothing is quoted (ErrNoUsdtRate): the price is never guessed.
//
// The charge is one transaction with the order's row locked: the order becomes a charge in Toman (amount and currency say what the customer paid, so a
// refund, the reports and the referral and commission rules read it as for any order) and keeps the USDT price it was listed at and the rate
// (list_amount, list_currency, rate), the wallet is debited under the key purchase:<order id> and the order is paid. A repeated payment finds the
// order paid and charges nothing.

// WalletQuoteTTL is how long a quote holds.
const WalletQuoteTTL = 2 * time.Minute

// walletQuoteContext separates the quote's signature from every other use of the master key's tag.
const walletQuoteContext = "wallet-quote:v1:"

// ErrQuoteExpired: the payment carried no quote that still holds (it ran out): ask for a new one and show it again.
var ErrQuoteExpired = errors.New("domain: the price quote expired")

// SetClock sets the clock the quotes expire by (nil: time.Now). It exists for tests of the layers above, which cannot reach Config once the service is built.
func (s *Service) SetClock(now func() time.Time) { s.cfg.Now = now }

// now is the clock of the quotes' expiry (Config.Now; nil: time.Now).
func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// quoteClaims is what a quote says, signed.
type quoteClaims struct {
	Order string `json:"o"`
	User  string `json:"u"`
	List  int64  `json:"l"` // the order's price in List currency, minor units
	Cur   string `json:"c"` // the currency the order is listed in (USDT)
	Rate  int64  `json:"r"` // Toman per USDT
	Toman int64  `json:"t"` // what the wallet is charged
	Exp   int64  `json:"e"` // unix seconds
}

// WalletQuote is the price of an order listed in USDT, in Toman, held until ExpiresAt.
type WalletQuote struct {
	OrderID string
	// ListAmount in ListCurrency (USDT) is the order's price; Rate is the Toman per USDT it was worked out at; Amount is what the wallet is charged
	// (Toman, rounded up).
	ListAmount   int64
	ListCurrency string
	Rate         int64
	Amount       int64
	ExpiresAt    time.Time
	// Token is sent back with the payment (PayOrderWithWalletQuote).
	Token string
	// Balance and CreditLimit are the customer's wallet (Toman) now; Enough says Balance plus CreditLimit covers Amount.
	Balance, CreditLimit int64
	Enough               bool
}

func (s *Service) signQuote(c quoteClaims) (string, error) {
	if s.cfg.Secrets == nil {
		return "", stateErr("core has no master key, so it cannot quote a price")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.cfg.Secrets.Tag([]byte(walletQuoteContext+body))), nil
}

// openQuote reads a quote's token: its signature must be core's own. Whether it still holds and is for this order is checked under the order's lock.
func (s *Service) openQuote(token string) (*quoteClaims, error) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok || s.cfg.Secrets == nil || len(token) > 1024 {
		return nil, invalid("the price quote is not valid")
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.cfg.Secrets.Tag([]byte(walletQuoteContext+body))) {
		return nil, invalid("the price quote is not valid")
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, invalid("the price quote is not valid")
	}
	var c quoteClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, invalid("the price quote is not valid")
	}
	return &c, nil
}

// QuoteWalletPayment prices an unpaid order that is listed in USDT in Toman at the store's rate now, for paying it from the wallet. userID, when set,
// must own the order. It changes nothing; calling it again makes a new quote at the rate of that moment.
func (s *Service) QuoteWalletPayment(ctx context.Context, orderID, userID string) (*WalletQuote, error) {
	if orderID == "" {
		return nil, invalid("order id required")
	}
	o, err := s.st.GetOrder(ctx, s.st.Conn(), orderID)
	if err != nil {
		return nil, err
	}
	if userID != "" && o.UserID != userID {
		return nil, ErrForbidden
	}
	if !isPayable(o.Status) {
		return nil, ErrOrderNotPayable
	}
	if _, err := s.activeUser(ctx, nil, o.UserID); err != nil {
		return nil, err
	}
	if o.Currency != "USDT" {
		return nil, stateErr("this order is not priced in USDT")
	}
	toman, rate, err := s.TomanForUsdt(ctx, o.Amount)
	if errors.Is(err, ErrNoUsdtRate) {
		return nil, fmt.Errorf("%w, so a plan priced in USDT cannot be priced in Toman now", ErrNoUsdtRate)
	}
	if err != nil {
		return nil, err
	}
	exp := s.cfg.now().Add(WalletQuoteTTL).Unix()
	token, err := s.signQuote(quoteClaims{Order: o.ID, User: o.UserID, List: o.Amount, Cur: o.Currency, Rate: rate, Toman: toman, Exp: exp})
	if err != nil {
		return nil, err
	}
	w, err := s.st.GetWallet(ctx, s.st.Conn(), o.UserID, tomanCurrency)
	if err != nil {
		return nil, err
	}
	return &WalletQuote{
		OrderID: o.ID, ListAmount: o.Amount, ListCurrency: o.Currency, Rate: rate, Amount: toman, ExpiresAt: time.Unix(exp, 0), Token: token,
		Balance: w.Balance, CreditLimit: w.CreditLimit, Enough: w.Balance+w.CreditLimit >= toman,
	}, nil
}

// chargeQuoted turns the locked, payable order o into the Toman charge the quote promised, or says why it cannot: the quote is for another order,
// customer or price (invalid), or it ran out (ErrQuoteExpired).
func (s *Service) chargeQuoted(ctx context.Context, tx pgx.Tx, o *store.Order, c *quoteClaims) error {
	switch {
	case c.Order != o.ID || c.User != o.UserID || c.Cur != o.Currency || c.List != o.Amount || o.ListAmount != nil:
		return invalid("the price quote is not for this order")
	case c.Rate <= 0 || c.Toman <= 0:
		return invalid("the price quote is not valid")
	case s.cfg.now().Unix() > c.Exp:
		return ErrQuoteExpired
	}
	// The signature says core made it; the sum is checked anyway, so that a quote can never charge other than listed price x rate, rounded up.
	if want, ok := mulDiv(c.List, c.Rate, usdtScale, true); !ok || want != c.Toman {
		return invalid("the price quote is not valid")
	}
	ok, err := s.st.PriceOrderAs(ctx, tx, o.ID, c.Toman, tomanCurrency, c.List, c.Cur, c.Rate)
	if err != nil {
		return err
	}
	if !ok {
		return ErrOrderNotPayable
	}
	list, cur, rate := c.List, c.Cur, c.Rate
	o.Amount, o.Currency, o.ListAmount, o.ListCurrency, o.Rate = c.Toman, tomanCurrency, &list, &cur, &rate
	return nil
}
