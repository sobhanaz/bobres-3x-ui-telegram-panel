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

func TestRateParsers(t *testing.T) {
	if r, err := parseTetherland([]byte(`{"status":200,"data":{"currencies":{"USDT":{"price":271600,"sell_price":271600}}}}`)); err != nil || r != 271600 {
		t.Fatalf("tetherland: %v %v", r, err)
	}
	if r, err := parseWallexDepth([]byte(`{"result":{"ask":[{"price":271742,"quantity":1}],"bid":[{"price":271620,"quantity":2}]}}`)); err != nil || r != 271681 {
		t.Fatalf("wallex: %v %v", r, err)
	}
	if r, err := parseWallexDepth([]byte(`{"result":{"ask":[{"price":271742}],"bid":[]}}`)); err != nil || r != 271742 {
		t.Fatalf("wallex, one side: %v %v", r, err)
	}
	if _, err := parseWallexDepth([]byte(`{"result":{"ask":[],"bid":[]}}`)); err == nil {
		t.Error("an empty book was read")
	}
	if _, err := parseTetherland([]byte(`<html>`)); err == nil {
		t.Error("html was read")
	}
}

// The rate is the median of the sources that gave a usable price: a broken, slow-to-fail or absurd page is left out, and with none the call fails.
func TestRateProxy(t *testing.T) {
	a := rateServer(t, 200, `{"data":{"currencies":{"USDT":{"price":271600}}}}`)
	b := rateServer(t, 200, `{"result":{"ask":[{"price":271800}],"bid":[{"price":271600}]}}`)
	bad := rateServer(t, 500, `oops`)
	absurd := rateServer(t, 200, `{"data":{"currencies":{"USDT":{"price":3}}}}`)
	srcs := func(servers ...*httptest.Server) []RateSource {
		names := []string{"a", "b", "bad", "absurd", "far"}
		var out []RateSource
		for i, s := range servers {
			parse := parseTetherland
			if s == b {
				parse = parseWallexDepth
			}
			out = append(out, RateSource{Name: names[i], URL: s.URL, Parse: parse})
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
	far := rateServer(t, 200, `{"data":{"currencies":{"USDT":{"price":300000}}}}`)
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
	if rec := post(t, handler(bad, absurd), "/internal/usdt-rate", coreTok, ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("none usable: %d %s", rec.Code, rec.Body)
	}
	if got := medianRate([]float64{3, 1, 2}); got != 2 {
		t.Errorf("median of three: %d", got)
	}
}
