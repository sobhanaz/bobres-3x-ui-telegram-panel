package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rateServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Answers as the exchanges gave them on 2026-11-09 (Nobitex in Rial, the others in Toman).
const (
	nobitexAnswer = `{"status":"ok","stats":{"usdt-rls":{"isClosed":false,"bestSell":"2649490","bestBuy":"2649400","latest":"2649500","dayLow":"2625000"}},"global":{"binance":{}}}`
	tabdealAnswer = `{"asks":[["265000.0000000000000000","615.76667000"]],"bids":[["264950.0000000000000000","38.95451900"]]}`
	wallexAnswer  = `{"result":{"ask":[{"price":271742,"quantity":1}],"bid":[{"price":271620,"quantity":2}]}}`
)

func TestRateParsers(t *testing.T) {
	for _, c := range []struct {
		name  string
		parse func([]byte) (float64, error)
		body  string
		want  float64
	}{
		{"nobitex, Rial to Toman", parseNobitexStats, nobitexAnswer, 264944.5},
		{"nobitex, numbers", parseNobitexStats, `{"status":"ok","stats":{"usdt-rls":{"bestSell":2650000,"bestBuy":2640000}}}`, 264500},
		{"nobitex, one side", parseNobitexStats, `{"status":"ok","stats":{"usdt-rls":{"bestSell":"2650000","bestBuy":""}}}`, 265000},
		{"tabdeal", parseTabdealDepth, tabdealAnswer, 264975},
		{"tabdeal, one side", parseTabdealDepth, `{"asks":[],"bids":[["264950","1"]]}`, 264950},
		{"wallex", parseWallexDepth, wallexAnswer, 271681},
		{"wallex, one side", parseWallexDepth, `{"result":{"ask":[{"price":271742}],"bid":[]}}`, 271742},
	} {
		if r, err := c.parse([]byte(c.body)); err != nil || r != c.want {
			t.Errorf("%s: %v %v, want %v", c.name, r, err, c.want)
		}
	}
	for _, c := range []struct {
		name  string
		parse func([]byte) (float64, error)
		body  string
	}{
		{"nobitex, closed market", parseNobitexStats, `{"status":"ok","stats":{"usdt-rls":{"isClosed":true,"bestSell":"2649490","bestBuy":"2649400"}}}`},
		{"nobitex, failed", parseNobitexStats, `{"status":"failed","message":"Unavailable"}`},
		{"nobitex, another market", parseNobitexStats, `{"status":"ok","stats":{"btc-rls":{"bestSell":"1","bestBuy":"1"}}}`},
		{"nobitex, no prices", parseNobitexStats, `{"status":"ok","stats":{"usdt-rls":{"bestSell":"","bestBuy":"x"}}}`},
		{"nobitex, not numbers", parseNobitexStats, `{"status":"ok","stats":{"usdt-rls":{"bestSell":"NaN","bestBuy":null}}}`},
		{"tabdeal, empty book", parseTabdealDepth, `{"asks":[],"bids":[]}`},
		{"tabdeal, empty level", parseTabdealDepth, `{"asks":[[]],"bids":[[]]}`},
		{"wallex, empty book", parseWallexDepth, `{"result":{"ask":[],"bid":[]}}`},
		{"html", parseNobitexStats, `<html>`},
		{"html", parseTabdealDepth, `<html>`},
		{"html", parseWallexDepth, `<html>`},
	} {
		if r, err := c.parse([]byte(c.body)); err == nil {
			t.Errorf("%s was read: %v", c.name, r)
		}
	}
}

// Every default source has a parser that reads its real answer into a sensible Toman price, and the three agree.
func TestDefaultRateSources(t *testing.T) {
	answers := map[string]string{"nobitex": nobitexAnswer, "tabdeal": tabdealAnswer, "wallex": wallexAnswer}
	if len(DefaultRateSources) != len(answers) {
		t.Fatalf("%d sources, want %d", len(DefaultRateSources), len(answers))
	}
	for _, s := range DefaultRateSources {
		body, ok := answers[s.Name]
		if !ok || !strings.HasPrefix(s.URL, "https://") {
			t.Fatalf("source %s at %s", s.Name, s.URL)
		}
		if r, err := s.Parse([]byte(body)); err != nil || r < minRate || r > maxRate {
			t.Errorf("%s: %v %v", s.Name, r, err)
		}
	}
}

// The rate is the median of the sources that gave a usable price: a broken, slow-to-fail or absurd page is left out, and with none the call fails.
func TestRateProxy(t *testing.T) {
	a := rateServer(t, 200, `{"asks":[["271600","1"]],"bids":[]}`)
	b := rateServer(t, 200, `{"result":{"ask":[{"price":271800}],"bid":[{"price":271600}]}}`)
	bad := rateServer(t, 500, `oops`)
	absurd := rateServer(t, 200, `{"asks":[["3","1"]],"bids":[]}`)
	gone := rateServer(t, 200, "")
	gone.Close()
	srcs := func(servers ...*httptest.Server) []RateSource {
		names := map[*httptest.Server]string{a: "a", b: "b", bad: "bad", absurd: "absurd", gone: "gone"}
		var out []RateSource
		for _, s := range servers {
			parse := parseTabdealDepth
			if s == b {
				parse = parseWallexDepth
			}
			name, ok := names[s]
			if !ok {
				name = "far"
			}
			out = append(out, RateSource{Name: name, URL: s.URL, Parse: parse})
		}
		return out
	}
	fixed := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	handler := func(servers ...*httptest.Server) http.Handler {
		return RateProxy{Sources: srcs(servers...), CoreToken: coreTok, Now: func() time.Time { return fixed }}.Handler()
	}
	// Only core may ask.
	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if rec := post(t, handler(a), "/internal/usdt-rate", tok, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, rec.Code)
		}
	}
	var out struct {
		Rate    int64    `json:"rate"`
		Sources []string `json:"sources"`
		At      int64    `json:"at"`
	}
	rec := post(t, handler(a, b), "/internal/usdt-rate", coreTok, "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Rate != 271650 || len(out.Sources) != 2 || out.At != fixed.Unix() {
		t.Fatalf("two sources: %d %s", rec.Code, rec.Body)
	}
	// Sources that disagree by more than 3% are not averaged: the reading is refused (core keeps the last good price, then its own rate).
	far := rateServer(t, 200, `{"asks":[["300000","1"]],"bids":[]}`)
	if rec := post(t, handler(a, far), "/internal/usdt-rate", coreTok, ""); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "disagree") {
		t.Fatalf("sources that disagree: %d %s", rec.Code, rec.Body)
	}
	rec = post(t, handler(a, bad, absurd), "/internal/usdt-rate", coreTok, "")
	out = struct {
		Rate    int64    `json:"rate"`
		Sources []string `json:"sources"`
		At      int64    `json:"at"`
	}{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Rate != 271600 || len(out.Sources) != 1 || out.Sources[0] != "a" {
		t.Fatalf("one usable source: %d %s", rec.Code, rec.Body)
	}
	// None usable: the answer says which source failed and why.
	rec = post(t, handler(bad, absurd, gone), "/internal/usdt-rate", coreTok, "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "absurd: no usable price; bad: HTTP 500; gone: unreachable") {
		t.Fatalf("none usable: %d %s", rec.Code, rec.Body)
	}
	if got := medianRate([]float64{3, 1, 2}); got != 2 {
		t.Errorf("median of three: %d", got)
	}
}
