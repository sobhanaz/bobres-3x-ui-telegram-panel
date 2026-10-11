package zpfake

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const merchant = "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b"

func post(t *testing.T, s *Server, path, body string) (int, map[string]json.RawMessage) {
	t.Helper()
	resp, err := http.Post(s.URL+path, "application/json", strings.NewReader(body)) //nolint:gosec,noctx // test server URL
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	var env map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, env
}

// unVerified lists paid, unverified payments in Zarinpal's envelope and
// changes nothing: a payment listed is still waiting to be verified.
func TestUnverifiedIsReadOnly(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	paid := s.AddPayment(1_500_000, "PAID")
	s.AddPayment(1_000_000, "IN_BANK")
	s.AddPayment(1_000_000, "VERIFIED")
	for range 2 {
		code, env := post(t, s, "/pg/v4/payment/unVerified.json", `{"merchant_id":"`+merchant+`"}`)
		var data struct {
			Code        int `json:"code"`
			Authorities []struct {
				Authority string `json:"authority"`
				Amount    int64  `json:"amount"`
			} `json:"authorities"`
		}
		if err := json.Unmarshal(env["data"], &data); err != nil || code != http.StatusOK || string(env["errors"]) != "[]" {
			t.Fatalf("envelope: %d %s %v", code, env, err)
		}
		if data.Code != 100 || len(data.Authorities) != 1 || data.Authorities[0].Authority != paid || data.Authorities[0].Amount != 1_500_000 {
			t.Fatalf("list: %+v", data)
		}
	}
	s.mu.Lock()
	st := s.payments[paid].status
	s.mu.Unlock()
	if st != "PAID" || s.Calls("unverified") != 2 || s.Calls("request") != 0 || s.Calls("verify") != 0 {
		t.Fatalf("after listing: status %s, calls %d %d %d", st, s.Calls("unverified"), s.Calls("request"), s.Calls("verify"))
	}
}

// OnlyMerchant: any other merchant id is an unknown terminal (-10) on every call.
func TestOnlyMerchant(t *testing.T) {
	s := New()
	t.Cleanup(s.Close)
	s.OnlyMerchant(merchant)
	other := "9f8e7d6c-5b4a-4321-8fed-cba987654321"
	auth := s.AddPayment(1_500_000, "IN_BANK")
	for path, body := range map[string]string{
		"/pg/v4/payment/unVerified.json": `{"merchant_id":"` + other + `"}`,
		"/pg/v4/payment/request.json":    `{"merchant_id":"` + other + `","amount":1500000,"description":"x","callback_url":"https://x.test/cb"}`,
		"/pg/v4/payment/verify.json":     `{"merchant_id":"` + other + `","amount":1500000,"authority":"` + auth + `"}`,
		"/pg/v4/payment/inquiry.json":    `{"merchant_id":"` + other + `","authority":"` + auth + `"}`,
	} {
		code, env := post(t, s, path, body)
		if code != http.StatusUnprocessableEntity || !strings.Contains(string(env["errors"]), `"code":-10`) {
			t.Errorf("%s: %d %s", path, code, env["errors"])
		}
	}
	if code, _ := post(t, s, "/pg/v4/payment/unVerified.json", `{"merchant_id":"`+merchant+`"}`); code != http.StatusOK {
		t.Fatalf("own merchant refused: %d", code)
	}
}
