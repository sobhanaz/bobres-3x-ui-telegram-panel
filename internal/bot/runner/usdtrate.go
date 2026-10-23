package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"
)

// The live price of one USDT in Toman. Core has no internet access, so it asks the bot at POST /internal/usdt-rate (core's service token) and
// the bot reads a few public price pages that are fixed here: core names no address and sends nothing. The answer is one number, the median of
// the sources that answered ({"rate": Toman per USDT, "sources": [...], "at": unix seconds}), or an error when none did. The bot keeps and
// logs nothing of what passes.

// RateSource is one public price page: where it is and how to read a Toman price of one USDT out of its answer.
type RateSource struct {
	Name  string
	URL   string
	Parse func(body []byte) (float64, error)
}

// DefaultRateSources are the pages the live rate is read from.
var DefaultRateSources = []RateSource{
	{Name: "tetherland", URL: "https://api.tetherland.com/currencies", Parse: parseTetherland},
	{Name: "wallex", URL: "https://api.wallex.ir/v1/depth?symbol=USDTTMN", Parse: parseWallexDepth},
}

const (
	// A rate outside this range is a broken page, not a price.
	minRate = 1_000
	maxRate = 100_000_000
	// maxSpread is how far apart the sources may be (the dearest over the cheapest): the real spread between exchanges is a fraction of a percent, so
	// more than this means one source is wrong, and the reading is refused rather than averaged.
	maxSpread = 1.03
	// rateSourceTimeout bounds one source; maxRateAnswer one page.
	rateSourceTimeout = 8 * time.Second
	maxRateAnswer     = 1 << 20
)

// parseTetherland reads {"data":{"currencies":{"USDT":{"price":271600}}}}.
func parseTetherland(body []byte) (float64, error) {
	var v struct {
		Data struct {
			Currencies map[string]struct {
				Price float64 `json:"price"`
			} `json:"currencies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return 0, err
	}
	return v.Data.Currencies["USDT"].Price, nil
}

// parseWallexDepth reads the order book {"result":{"ask":[{"price":271742}],"bid":[{"price":271620}]}}: the middle of the best ask and the
// best bid (or the one there is).
func parseWallexDepth(body []byte) (float64, error) {
	var v struct {
		Result struct {
			Ask []struct {
				Price float64 `json:"price"`
			} `json:"ask"`
			Bid []struct {
				Price float64 `json:"price"`
			} `json:"bid"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return 0, err
	}
	var prices []float64
	if len(v.Result.Ask) > 0 {
		prices = append(prices, v.Result.Ask[0].Price)
	}
	if len(v.Result.Bid) > 0 {
		prices = append(prices, v.Result.Bid[0].Price)
	}
	if len(prices) == 0 {
		return 0, errors.New("no order book")
	}
	sum := 0.0
	for _, p := range prices {
		sum += p
	}
	return sum / float64(len(prices)), nil
}

// RateProxy serves POST /internal/usdt-rate.
type RateProxy struct {
	// Sources are the price pages (nil: DefaultRateSources).
	Sources []RateSource
	// Client reads them (nil: one that never follows a redirect).
	Client *http.Client
	// CoreToken is core's service token.
	CoreToken string
	Log       *slog.Logger
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

// medianRate is the median of the rates (the average of the middle two when there is an even number), rounded to a whole Toman.
func medianRate(rates []float64) int64 {
	sort.Float64s(rates)
	n := len(rates)
	if n%2 == 1 {
		return int64(math.Round(rates[n/2]))
	}
	return int64(math.Round((rates[n/2-1] + rates[n/2]) / 2))
}

// Read asks every source at once and gives the median of the usable answers and the names of the sources that gave one.
func (p RateProxy) Read(ctx context.Context) (rate int64, sources []string, err error) {
	srcs := p.Sources
	if srcs == nil {
		srcs = DefaultRateSources
	}
	hc := &http.Client{Timeout: rateSourceTimeout, CheckRedirect: noRedirect}
	if p.Client != nil {
		c := *p.Client
		c.CheckRedirect = noRedirect
		hc = &c
	}
	type result struct {
		name string
		rate float64
	}
	var (
		mu  sync.Mutex
		got []result
		wg  sync.WaitGroup
	)
	for _, s := range srcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, rateSourceTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
			if err != nil {
				return
			}
			req.Header.Set("Accept", "application/json")
			resp, err := hc.Do(req)
			if err != nil {
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxRateAnswer+1))
			if err != nil || len(body) > maxRateAnswer {
				return
			}
			r, err := s.Parse(body)
			if err != nil || math.IsNaN(r) || r < minRate || r > maxRate {
				return
			}
			mu.Lock()
			got = append(got, result{s.Name, r})
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(got) == 0 {
		return 0, nil, errors.New("no price source answered")
	}
	sort.Slice(got, func(i, j int) bool { return got[i].name < got[j].name })
	rates := make([]float64, len(got))
	lo, hi := math.Inf(1), 0.0
	for i, g := range got {
		rates[i] = g.rate
		sources = append(sources, g.name)
		lo, hi = math.Min(lo, g.rate), math.Max(hi, g.rate)
	}
	if hi/lo > maxSpread {
		return 0, nil, errors.New("the price sources disagree by more than 3%")
	}
	return medianRate(rates), sources, nil
}

// Handler serves POST /internal/usdt-rate.
func (p RateProxy) Handler() http.Handler {
	log := p.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, p.CoreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		rate, sources, err := p.Read(r.Context())
		if err != nil {
			log.Warn("the live USDT rate could not be read")
			licenseError(w, http.StatusBadGateway, "unreachable", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"rate": rate, "sources": sources, "at": now().Unix()})
	})
}
