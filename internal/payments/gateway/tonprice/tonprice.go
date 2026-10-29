// Package tonprice reads the price of one TON in USDT from two public exchanges: Binance (the primary) and OKX
// (the second source). Both answer the price of the TON/USDT market without an account.
//
// Implemented to the exchanges' public market-data documentation and tested only against fakes of them. The price
// is kept as micro-USDT (scale 6) per TON, an integer: it is parsed from the decimal text the exchange sends and
// cut (never rounded up) beyond the sixth decimal, so a TON amount worked out from it is, if anything, a little
// more TON for the customer to send. Nothing is guessed: a source whose answer is out of range, stale or odd is no
// source, the two sources must agree within 5% when both answer, and with none left there is no price.
package tonprice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
)

const (
	binanceBase = "https://api.binance.com"
	okxBase     = "https://www.okx.com"

	// A price outside these bounds (micro-USDT per TON, 0.05 to 1,000 USDT) is a glitch, not a market.
	minPriceMicro = 50_000
	maxPriceMicro = 1_000_000_000

	// maxSpread: the largest difference between the two sources, as a fraction of the lower (percent).
	maxSpreadPercent = 5

	// okxMaxAge: the age at which OKX's own timestamp makes its ticker stale.
	okxMaxAge = 10 * time.Minute
)

// ErrDisagree: both sources answered and differ by more than 5%: something is wrong with one of them, and which
// is not known, so there is no price.
var ErrDisagree = errors.New("tonprice: the two price sources disagree: no price is used")

// ParseMicro reads a decimal price ("3.2145", "3") as micro-USDT, cutting what is beyond the sixth decimal.
func ParseMicro(s string) (int64, error) {
	s = strings.TrimSpace(s)
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(whole) > 10 || strings.ContainsAny(whole+frac, "+-eE ") {
		return 0, fmt.Errorf("tonprice: not a price: %q", s)
	}
	if len(frac) > 6 {
		frac = frac[:6]
	}
	frac += strings.Repeat("0", 6-len(frac))
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil || w < 0 || f < 0 {
		return 0, fmt.Errorf("tonprice: not a price: %q", s)
	}
	return w*1_000_000 + f, nil
}

func inRange(micro int64) bool { return micro >= minPriceMicro && micro <= maxPriceMicro }

// Source is one exchange.
type Source struct {
	name string
	get  func(ctx context.Context) (int64, error)
}

// Name is the exchange's name.
func (s Source) Name() string { return s.name }

// Price reads the price now.
func (s Source) Price(ctx context.Context) (int64, error) {
	p, err := s.get(ctx)
	if err != nil {
		return 0, err
	}
	if !inRange(p) {
		return 0, fmt.Errorf("tonprice: %s: a price out of range", s.name)
	}
	return p, nil
}

// Config configures the sources.
type Config struct {
	// BinanceURL and OKXURL override the hosts (tests).
	BinanceURL, OKXURL string
	// HTTPClient overrides the HTTP client (tests).
	HTTPClient *http.Client
	// Now is the clock (tests).
	Now func() time.Time
}

func (c Config) client() (*http.Client, error) {
	if c.HTTPClient != nil {
		return c.HTTPClient, nil
	}
	return gateway.NewHTTPClient("tonprice", "")
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func getJSON(ctx context.Context, hc *http.Client, name, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("tonprice: %s: request failed: %s", name, gateway.DescribeTransport(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tonprice: %s: HTTP %d", name, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("tonprice: %s: read response: %s", name, gateway.DescribeTransport(err))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("tonprice: %s: unexpected response", name)
	}
	return nil
}

// Binance is the TON/USDT market of Binance: GET /api/v3/ticker/price?symbol=TONUSDT answers {symbol, price}.
func Binance(c Config) (Source, error) {
	hc, err := c.client()
	if err != nil {
		return Source{}, err
	}
	base := binanceBase
	if c.BinanceURL != "" {
		base = strings.TrimRight(c.BinanceURL, "/")
	}
	return Source{name: "binance", get: func(ctx context.Context) (int64, error) {
		var a struct {
			Symbol string `json:"symbol"`
			Price  string `json:"price"`
		}
		if err := getJSON(ctx, hc, "binance", base+"/api/v3/ticker/price?symbol=TONUSDT", &a); err != nil {
			return 0, err
		}
		if a.Symbol != "TONUSDT" {
			return 0, errors.New("tonprice: binance: another market")
		}
		return ParseMicro(a.Price)
	}}, nil
}

// OKX is the TON-USDT market of OKX: GET /api/v5/market/ticker?instId=TON-USDT answers {code: "0", data: [{instId,
// last, ts}]}; ts (milliseconds) must be recent.
func OKX(c Config) (Source, error) {
	hc, err := c.client()
	if err != nil {
		return Source{}, err
	}
	base := okxBase
	if c.OKXURL != "" {
		base = strings.TrimRight(c.OKXURL, "/")
	}
	return Source{name: "okx", get: func(ctx context.Context) (int64, error) {
		var a struct {
			Code string `json:"code"`
			Data []struct {
				InstID string `json:"instId"`
				Last   string `json:"last"`
				TS     string `json:"ts"`
			} `json:"data"`
		}
		if err := getJSON(ctx, hc, "okx", base+"/api/v5/market/ticker?instId=TON-USDT", &a); err != nil {
			return 0, err
		}
		if a.Code != "0" || len(a.Data) != 1 || a.Data[0].InstID != "TON-USDT" {
			return 0, errors.New("tonprice: okx: unexpected answer")
		}
		if ms, err := strconv.ParseInt(a.Data[0].TS, 10, 64); err == nil && ms > 0 {
			if age := c.now().Sub(time.UnixMilli(ms)); age > okxMaxAge || age < -okxMaxAge {
				return 0, errors.New("tonprice: okx: a stale ticker")
			}
		}
		return ParseMicro(a.Data[0].Last)
	}}, nil
}

// Feed is the primary source with the second as its check and its fallback. It implements gateway.TONPricer.
type Feed struct {
	primary, second Source
	now             func() time.Time
}

// NewFeed makes the feed of two sources.
func NewFeed(primary, second Source, now func() time.Time) *Feed {
	if now == nil {
		now = time.Now
	}
	return &Feed{primary: primary, second: second, now: now}
}

// Default is Binance with OKX, the hosts of Config overriding them.
func Default(c Config) (*Feed, error) {
	b, err := Binance(c)
	if err != nil {
		return nil, err
	}
	o, err := OKX(c)
	if err != nil {
		return nil, err
	}
	return NewFeed(b, o, c.now), nil
}

// Quote reads both sources. Both answering: they must agree within 5% and the primary's price is used. One
// answering: its price is used. None: an error.
func (f *Feed) Quote(ctx context.Context) (gateway.TONQuote, error) {
	p, perr := f.primary.Price(ctx)
	s, serr := f.second.Price(ctx)
	now := f.now()
	switch {
	case perr == nil && serr == nil:
		lo, hi := min(p, s), max(p, s)
		if (hi-lo)*100 > lo*maxSpreadPercent {
			return gateway.TONQuote{}, ErrDisagree
		}
		return gateway.TONQuote{PriceMicro: p, Source: f.primary.name, At: now}, nil
	case perr == nil:
		return gateway.TONQuote{PriceMicro: p, Source: f.primary.name, At: now}, nil
	case serr == nil:
		return gateway.TONQuote{PriceMicro: s, Source: f.second.name, At: now}, nil
	}
	return gateway.TONQuote{}, fmt.Errorf("tonprice: no source answered (%w; %w)", perr, serr)
}
