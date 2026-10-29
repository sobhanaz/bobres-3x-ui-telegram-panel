package toncenter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/toncenter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonaddr"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonfake"
)

const apiKey = "test-key-test-key-test-key" //nolint:gosec // a fake key for the test server

func store() string { return tonaddr.Address{Hash: [32]byte{1, 2, 3}}.Canonical() }

func newClient(t *testing.T, key string) (*toncenter.Client, *tonfake.Server) {
	t.Helper()
	fake := tonfake.New()
	t.Cleanup(fake.Close)
	c, err := toncenter.New(toncenter.Config{APIKey: key, BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c, fake
}

func TestConfig(t *testing.T) {
	for _, c := range []toncenter.Config{{APIKey: "short"}, {APIKey: "has space in the key"}, {APIKey: strings.Repeat("a", 129)},
		{Proxy: "socks5://1.2.3.4:1080"}, {Proxy: "::::"}} {
		if _, err := toncenter.New(c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	for _, c := range []toncenter.Config{{}, {APIKey: apiKey}, {Proxy: "http://u:p@10.0.0.2:3128"}} { //nolint:gosec // test fixture
		if _, err := toncenter.New(c); err != nil {
			t.Errorf("refused %+v: %v", c, err)
		}
	}
}

func TestReadsAnIncomingTransfer(t *testing.T) {
	c, fake := newClient(t, "")
	hash := fake.Pay(store(), 2_531_000_000, "BOB-3F9A1C20")
	got, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store(), Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	x := got[0]
	if x.Hash != hash || x.LT <= 0 || !x.HasMsg || x.Source == "" || x.Value != "2531000000" || x.Comment != "BOB-3F9A1C20" ||
		!tonaddr.Same(x.Destination, store()) || time.Since(x.Time) < time.Minute || len(x.Hash) != 64 {
		t.Fatalf("%+v", x)
	}
	if x.RawHash == "" || x.RawHash == x.Hash {
		t.Fatalf("the hash as reported (base64) is kept for paging: %q", x.RawHash)
	}
}

func TestExternalBinaryAndBouncedTransactions(t *testing.T) {
	c, fake := newClient(t, "")
	fake.Add(tonfake.Tx{To: store(), Nano: 0, Comment: ""})                                        // an external message
	fake.Add(tonfake.Tx{To: store(), From: tonfake.Customer(), Nano: 1_000_000_000, Binary: true}) // not a text
	fake.Add(tonfake.Tx{To: store(), From: tonfake.Customer(), Nano: 3_000_000_000, Comment: "x", Bounce: true})
	got, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store()})
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	bounced, binary, external := got[0], got[1], got[2]
	if external.Source != "" || binary.Comment != "" || binary.Value != "1000000000" || len(bounced.OutMsgs) != 1 ||
		!tonaddr.Same(bounced.OutMsgs[0].Destination, tonfake.Customer()) {
		t.Fatalf("%+v / %+v / %+v", external, binary, bounced)
	}
}

func TestPagingContinuesFromAHash(t *testing.T) {
	c, fake := newClient(t, "")
	for i := range 5 {
		fake.Pay(store(), int64(1000+i), "c")
	}
	ctx := context.Background()
	first, err := c.Transactions(ctx, gateway.TONQuery{Address: store(), Limit: 2})
	if err != nil || len(first) != 2 || first[0].LT < first[1].LT {
		t.Fatalf("newest first: %+v %v", first, err)
	}
	last := first[len(first)-1]
	next, err := c.Transactions(ctx, gateway.TONQuery{Address: store(), Limit: 2, BeforeLT: last.LT, BeforeHash: last.RawHash})
	if err != nil || len(next) != 2 || next[0].LT != last.LT || next[1].LT >= last.LT {
		t.Fatalf("the page starts at the named transaction: %+v %v", next, err)
	}
}

func TestOnlyTheAskedAccount(t *testing.T) {
	c, fake := newClient(t, "")
	other := tonaddr.Address{Hash: [32]byte{9}}.Canonical()
	fake.Pay(other, 5, "x")
	got, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store()})
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := c.Transactions(context.Background(), gateway.TONQuery{Address: "nope"}); err == nil {
		t.Fatal("a bad address was answered")
	}
}

func TestAPIKeyGoesInTheHeader(t *testing.T) {
	var gotHeader, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader, gotQuery = r.Header.Get("X-API-Key"), r.URL.RawQuery
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()
	c, _ := toncenter.New(toncenter.Config{APIKey: apiKey, BaseURL: srv.URL})
	if _, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store()}); err != nil {
		t.Fatal(err)
	}
	if gotHeader != apiKey || strings.Contains(gotQuery, apiKey) {
		t.Fatalf("header %q, query %q", gotHeader, gotQuery)
	}
}

func TestErrorsNeverCarryTheKeyOrTheAddress(t *testing.T) {
	c, fake := newClient(t, apiKey)
	fake.OnlyAPIKey("another-key-0123456789")
	_, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store()})
	if err == nil || !strings.Contains(err.Error(), "API key") || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("%v", err)
	}
	fake.OnlyAPIKey("")
	fake.RateLimit(1)
	if _, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store()}); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("%v", err)
	}
	fake.Down(1)
	if _, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store()}); err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("%v", err)
	}
}

func TestOddAnswersAreErrorsNotEmptyLists(t *testing.T) {
	for _, body := range []string{`<html>oops</html>`, ``, `{"ok":false,"error":"bad","code":500}`, `{"result":[]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		c, _ := toncenter.New(toncenter.Config{BaseURL: srv.URL})
		if got, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store()}); err == nil {
			t.Errorf("%q: %v", body, got)
		}
		srv.Close()
	}
}

func TestHostileFieldsDoNotBreakTheRead(t *testing.T) {
	// A transaction the client cannot read is skipped, the others stay; a comment that is not text is none.
	body := `{"ok":true,"result":[
	  {"utime":1700000000,"transaction_id":{"lt":"5","hash":"` + strings.Repeat("a", 64) + `"},"in_msg":{"source":"s","destination":"d","value":42,"message":"` + "ok\\u0000bad" + `"}},
	  {"utime":"x","transaction_id":7},
	  {"utime":1700000001,"transaction_id":{"lt":"6","hash":"not-a-hash"},"in_msg":{"source":"s","destination":"d","value":"7","msg_data":{"@type":"msg.dataText","text":"!!!"}}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer srv.Close()
	c, _ := toncenter.New(toncenter.Config{BaseURL: srv.URL})
	got, err := c.Transactions(context.Background(), gateway.TONQuery{Address: store()})
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Hash != strings.Repeat("a", 64) || got[0].Value != "42" || got[0].Comment != "" {
		t.Fatalf("a numeric value and a comment with a NUL: %+v", got[0])
	}
	if got[1].Hash != "" || got[1].LT != 6 {
		t.Fatalf("a hash that is none is empty and the domain refuses it: %+v", got[1])
	}
}

func TestTimeout(t *testing.T) {
	c, fake := newClient(t, "")
	fake.Delay(600 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Transactions(ctx, gateway.TONQuery{Address: store()}); err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}
