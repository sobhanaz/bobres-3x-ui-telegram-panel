package tronfake

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

const (
	store         = "TCKR37NfLumfScqBQX6qrnJR3mZkv6xGqi"
	otherContract = "TVmTPtVpHTgWzHYnGDbih1QDTuQo6J1Eef"
)

type page struct {
	Data []struct {
		TransactionID string `json:"transaction_id"`
		TokenInfo     struct {
			Address string `json:"address"`
			Symbol  string `json:"symbol"`
		} `json:"token_info"`
		To    string `json:"to"`
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"data"`
	Success bool `json:"success"`
	Meta    struct {
		Fingerprint string `json:"fingerprint"`
	} `json:"meta"`
}

func list(t *testing.T, s *Server, q url.Values, key string) (int, page) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.URL+"/v1/accounts/"+store+"/transactions/trc20?"+q.Encode(), nil) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("TRON-PRO-API-KEY", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	var p page
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, p
}

// The fake filters like TronGrid: incoming, confirmed, one contract, from a
// time on, oldest first, paged by fingerprint; and it fails on demand.
func TestListFiltersAndPages(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	at := time.Now().Add(-time.Minute)
	paid := s.PayAt(store, 1_000_001, at)
	s.PayAt(store, 7, at.Add(-time.Hour))
	s.Add(Transfer{To: store, Value: "5", Token: otherContract, BlockTS: at})
	s.Add(Transfer{To: store, Value: "6", Unconfirmed: true, BlockTS: at})
	s.Add(Transfer{From: store, To: Sender, Value: "8", BlockTS: at})
	second := s.PayAt(store, 2_000_001, at.Add(time.Second))
	q := url.Values{"only_to": {"true"}, "only_confirmed": {"true"}, "contract_address": {USDT},
		"order_by": {"block_timestamp,asc"}, "limit": {"1"}, "min_timestamp": {strconv.FormatInt(at.UnixMilli(), 10)}}
	code, p := list(t, s, q, "")
	if code != 200 || !p.Success || len(p.Data) != 1 || p.Data[0].TransactionID != paid || p.Data[0].Value != "1000001" ||
		p.Data[0].TokenInfo.Address != USDT || p.Data[0].TokenInfo.Symbol != "USDT" || p.Meta.Fingerprint == "" {
		t.Fatalf("first page: %d %+v", code, p)
	}
	q.Set("fingerprint", p.Meta.Fingerprint)
	code, p = list(t, s, q, "")
	if code != 200 || len(p.Data) != 1 || p.Data[0].TransactionID != second || p.Meta.Fingerprint != "" {
		t.Fatalf("second page: %d %+v", code, p)
	}
	s.LeakOtherTokens(true)
	q.Del("fingerprint")
	q.Set("limit", "200")
	if _, p = list(t, s, q, ""); len(p.Data) != 3 {
		t.Fatalf("with other tokens: %+v", p)
	}
	s.RequireKey("k-123456")
	if code, _ = list(t, s, q, "wrong-key"); code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", code)
	}
	s.Fail(1, http.StatusServiceUnavailable)
	if code, _ = list(t, s, q, "k-123456"); code != http.StatusServiceUnavailable {
		t.Fatalf("failure: %d", code)
	}
	if code, _ = list(t, s, q, "k-123456"); code != http.StatusOK || s.Calls() != 6 || s.LastKey() != "k-123456" {
		t.Fatalf("after the failure: %d calls %d", code, s.Calls())
	}
	// A failure partway: two requests go through, the third fails, then all is well.
	s.FailAfter(2, 1, http.StatusTooManyRequests)
	for i, want := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests, http.StatusOK} {
		if code, _ = list(t, s, q, "k-123456"); code != want {
			t.Fatalf("request %d after FailAfter: %d, want %d", i+1, code, want)
		}
	}
}
