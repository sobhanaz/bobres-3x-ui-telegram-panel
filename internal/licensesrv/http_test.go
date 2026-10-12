package licensesrv

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRateLimitPerIP(t *testing.T) {
	e := newEnv(t, envOpts{ipRate: Rate{Max: 3, Window: time.Minute}})
	body := ActivateRequest{Key: "BOBRES-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA", InstallID: newInstall(), Domain: "a.example"}
	for i := range 3 {
		if r := e.post("/v1/activate", body, "203.0.113.7"); r.Status != http.StatusForbidden || r.Error != "invalid_key" {
			t.Fatalf("request %d: %d %s", i, r.Status, r.Error)
		}
	}
	r := e.post("/v1/activate", body, "203.0.113.7")
	if r.Status != http.StatusTooManyRequests || r.Error != "rate_limited" {
		t.Fatalf("4th request: %d %s", r.Status, r.Error)
	}
	if secs, err := strconv.Atoi(r.Header.Get("Retry-After")); err != nil || secs < 1 || secs > 60 {
		t.Fatalf("Retry-After %q", r.Header.Get("Retry-After"))
	}
	// Refresh shares the address's budget.
	if r := e.post("/v1/refresh", RefreshRequest{Token: "x.y"}, "203.0.113.7"); r.Status != http.StatusTooManyRequests {
		t.Fatalf("refresh from the limited address: %d", r.Status)
	}
	// Another client behind the same proxy has its own budget.
	if r := e.post("/v1/activate", body, "198.51.100.9"); r.Status != http.StatusForbidden {
		t.Fatalf("another address: %d %s", r.Status, r.Error)
	}
	// One IPv6 /64 is one client.
	for i := range 3 {
		if r := e.post("/v1/activate", body, fmt.Sprintf("2001:db8:1:2::%x", i+1)); r.Status != http.StatusForbidden {
			t.Fatalf("v6 request %d: %d", i, r.Status)
		}
	}
	if r := e.post("/v1/activate", body, "2001:db8:1:2::ff"); r.Status != http.StatusTooManyRequests {
		t.Fatalf("same /64: %d", r.Status)
	}
	// The window ends.
	e.clock.Advance(time.Minute)
	if r := e.post("/v1/activate", body, "203.0.113.7"); r.Status != http.StatusForbidden {
		t.Fatalf("after the window: %d %s", r.Status, r.Error)
	}
}

// A table filled by throwaway addresses (one per IPv6 /64) still lets a new
// address activate and refresh.
func TestRateLimitFullIPTable(t *testing.T) {
	e := newEnv(t, envOpts{ipRate: Rate{Max: 3, Window: time.Hour}})
	for i := range maxLimiterKeys {
		e.api.ipLimit.allow("ip:" + ipKey(fmt.Sprintf("2001:db8:%x:%x::1", i>>16, i&0xffff)))
	}
	body := ActivateRequest{Key: "BOBRES-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA", InstallID: newInstall(), Domain: "a.example"}
	if r := e.post("/v1/activate", body, "198.51.100.20"); r.Status != http.StatusForbidden || r.Error != "invalid_key" {
		t.Fatalf("activate from a new address: %d %s", r.Status, r.Error)
	}
	if r := e.post("/v1/refresh", RefreshRequest{Token: "x.y"}, "2001:db8:ffff:1::1"); r.Status == http.StatusTooManyRequests {
		t.Fatalf("refresh from a new address: %d %s", r.Status, r.Error)
	}
}

func TestRateLimitPerKeyAndLicense(t *testing.T) {
	e := newEnv(t, envOpts{keyRate: Rate{Max: 2, Window: time.Hour}})
	_, key := e.newLicense()
	_, otherKey := e.newLicense()
	install := newInstall()
	var tok string
	for i := range 2 {
		r := e.post("/v1/activate", ActivateRequest{Key: key, InstallID: install, Domain: "a.example"}, fmt.Sprintf("192.0.2.%d", i+1))
		if r.Status != http.StatusOK {
			t.Fatalf("activation %d: %d %s", i, r.Status, r.Error)
		}
		tok = r.Token
	}
	// From yet another address: the license's own budget is spent, for
	// activations and refreshes alike.
	r := e.post("/v1/activate", ActivateRequest{Key: key, InstallID: install, Domain: "a.example"}, "192.0.2.99")
	if r.Status != http.StatusTooManyRequests || r.Header.Get("Retry-After") == "" {
		t.Fatalf("third activation of one key: %d %s", r.Status, r.Error)
	}
	if r := e.refresh(tok); r.Status != http.StatusTooManyRequests {
		t.Fatalf("refresh with the budget spent: %d %s", r.Status, r.Error)
	}
	if r := e.activate(otherKey, install, "a.example"); r.Status != http.StatusOK {
		t.Fatalf("another key: %d %s", r.Status, r.Error)
	}
	// Unknown keys and forged tokens cost the license nothing.
	e.clock.Advance(time.Hour)
	forged := tok[:len(tok)-6] + "AAAAAA"
	for range 5 {
		if r := e.refresh(forged); r.Status != http.StatusForbidden {
			t.Fatalf("forged refresh: %d %s", r.Status, r.Error)
		}
		if r := e.activate("BOBRES-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA", install, "a.example"); r.Status != http.StatusForbidden {
			t.Fatalf("unknown key: %d %s", r.Status, r.Error)
		}
	}
	for i := range 2 {
		if r := e.refresh(tok); r.Status != http.StatusOK {
			t.Fatalf("refresh %d: %d %s", i, r.Status, r.Error)
		}
	}
	if r := e.refresh(tok); r.Status != http.StatusTooManyRequests {
		t.Fatalf("third refresh: %d", r.Status)
	}
	e.svc.keyLimit.mu.Lock()
	n := len(e.svc.keyLimit.windows)
	e.svc.keyLimit.mu.Unlock()
	if n > 2 {
		t.Fatalf("the per-license table holds %d entries: more than the 2 real licenses", n)
	}
}

func TestHTTPSRequired(t *testing.T) {
	e := newEnv(t, envOpts{})
	_, key := e.newLicense()
	body := fmt.Sprintf(`{"key":%q,"install_id":%q,"domain":"a.example"}`, key, newInstall())

	plain, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/activate", strings.NewReader(body))
	if r := e.do(plain); r.Status != http.StatusForbidden || r.Error != "https_required" {
		t.Fatalf("plain http through the proxy: %d %s", r.Status, r.Error)
	}
	viaHTTP, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/activate", strings.NewReader(body))
	viaHTTP.Header.Set("X-Forwarded-Proto", "http")
	if r := e.do(viaHTTP); r.Status != http.StatusForbidden {
		t.Fatalf("X-Forwarded-Proto http: %d", r.Status)
	}

	// A peer that is not a trusted proxy cannot claim https.
	untrusted := newEnv(t, envOpts{trusted: []string{"10.9.9.9/32"}})
	req, _ := http.NewRequest(http.MethodPost, untrusted.srv.URL+"/v1/activate", strings.NewReader(body))
	req.Header.Set("X-Forwarded-Proto", "https")
	if r := untrusted.do(req); r.Status != http.StatusForbidden || r.Error != "https_required" {
		t.Fatalf("untrusted peer claiming https: %d %s", r.Status, r.Error)
	}

	dev := newEnv(t, envOpts{allowHTTP: true})
	_, devKey := dev.newLicense()
	req, _ = http.NewRequest(http.MethodPost, dev.srv.URL+"/v1/activate",
		strings.NewReader(fmt.Sprintf(`{"key":%q,"install_id":%q,"domain":"a.example"}`, devKey, newInstall())))
	if r := dev.do(req); r.Status != http.StatusOK {
		t.Fatalf("allow http: %d %s", r.Status, r.Error)
	}
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t, envOpts{})
	_, key := e.newLicense()
	install := newInstall()
	for name, tc := range map[string]struct {
		body   any
		status int
		code   string
	}{
		"not json":         {[]byte("{"), http.StatusBadRequest, "invalid"},
		"array":            {[]byte("[]"), http.StatusBadRequest, "invalid"},
		"too big":          {[]byte(`{"key":"` + strings.Repeat("A", maxBody) + `"}`), http.StatusBadRequest, "invalid"},
		"malformed key":    {ActivateRequest{Key: "ABC-123", InstallID: install, Domain: "a.example"}, http.StatusBadRequest, "invalid"},
		"bad install id":   {ActivateRequest{Key: key, InstallID: "inst_1", Domain: "a.example"}, http.StatusBadRequest, "invalid"},
		"no domain":        {ActivateRequest{Key: key, InstallID: install}, http.StatusBadRequest, "invalid"},
		"bad domain":       {ActivateRequest{Key: key, InstallID: install, Domain: "a b.example"}, http.StatusBadRequest, "invalid"},
		"dash label":       {ActivateRequest{Key: key, InstallID: install, Domain: "-a.example"}, http.StatusBadRequest, "invalid"},
		"bad version":      {ActivateRequest{Key: key, InstallID: install, Domain: "a.example", Version: "1.0 beta"}, http.StatusBadRequest, "invalid"},
		"long version":     {ActivateRequest{Key: key, InstallID: install, Domain: "a.example", Version: strings.Repeat("1", 65)}, http.StatusBadRequest, "invalid"},
		"unknown key":      {ActivateRequest{Key: "BOBRES-AAAA-BBBB-CCCC-DDDD-EEEE-FFFF-GGGG-HHHH", InstallID: install, Domain: "a.example"}, http.StatusForbidden, "invalid_key"},
		"extra fields too": {[]byte(fmt.Sprintf(`{"key":%q,"install_id":%q,"domain":"a.example","future":1}`, key, install)), http.StatusOK, ""},
	} {
		if r := e.post("/v1/activate", tc.body, ""); r.Status != tc.status || r.Error != tc.code {
			t.Errorf("%s: %d %s (%s), want %d %s", name, r.Status, r.Error, r.Message, tc.status, tc.code)
		}
	}
	get, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/v1/activate", nil)
	if r := e.do(get); r.Status != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", r.Status)
	}
}

func TestClientIP(t *testing.T) {
	a := &API{trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.0/8")}}
	for name, tc := range map[string]struct {
		remote string
		xff    []string
		want   string
	}{
		"direct":                {"198.51.100.1:5000", nil, "198.51.100.1"},
		"direct ignores xff":    {"198.51.100.1:5000", []string{"1.2.3.4"}, "198.51.100.1"},
		"proxy":                 {"127.0.0.1:5000", []string{"203.0.113.5"}, "203.0.113.5"},
		"proxy chain":           {"127.0.0.1:5000", []string{"203.0.113.5, 10.1.1.1"}, "203.0.113.5"},
		"forged left part":      {"127.0.0.1:5000", []string{"6.6.6.6, 203.0.113.5"}, "203.0.113.5"},
		"two headers":           {"127.0.0.1:5000", []string{"6.6.6.6", "203.0.113.5"}, "203.0.113.5"},
		"proxy without xff":     {"127.0.0.1:5000", nil, "127.0.0.1"},
		"only proxies":          {"127.0.0.1:5000", []string{"10.2.2.2"}, "127.0.0.1"},
		"garbage at the right":  {"127.0.0.1:5000", []string{"203.0.113.5, junk"}, "127.0.0.1"},
		"mapped v4":             {"[::ffff:127.0.0.1]:5000", []string{"::ffff:203.0.113.5"}, "203.0.113.5"},
		"v6 client via proxy":   {"127.0.0.1:5000", []string{"2001:db8::1"}, "2001:db8::1"},
		"unparsable remoteaddr": {"pipe", nil, "pipe"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/activate", nil)
		r.RemoteAddr = tc.remote
		for _, h := range tc.xff {
			r.Header.Add("X-Forwarded-For", h)
		}
		peer := peerAddr(r)
		if got := a.clientIP(r, peer, a.isTrusted(peer)); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if k := ipKey("2001:db8:1:2:aaaa::1"); k != "2001:db8:1:2::/64" {
		t.Fatalf("ipKey v6: %q", k)
	}
	if k := ipKey("::ffff:192.0.2.1"); k != "192.0.2.1" {
		t.Fatalf("ipKey mapped: %q", k)
	}
}

func TestLimiter(t *testing.T) {
	c := newClock()
	l := newLimiter(Rate{Max: 2, Window: 10 * time.Second}, c.Now)
	for i := range 2 {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("request %d refused", i)
		}
	}
	c.Advance(4 * time.Second)
	ok, retry := l.allow("a")
	if ok || retry != 6*time.Second {
		t.Fatalf("over the rate: %v %s", ok, retry)
	}
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("another key refused")
	}
	c.Advance(6 * time.Second)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("refused after the window")
	}
	// Ended windows are swept.
	c.Advance(time.Minute)
	l.allow("c")
	l.mu.Lock()
	n := len(l.windows)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d windows kept after a sweep", n)
	}
}

// A full table must not lock new callers out: anyone with enough IPv6 /64s
// could fill it and keep every real install from activating or refreshing.
// The window that started first is forgotten instead, and recent callers
// keep their counts.
func TestLimiterFullTable(t *testing.T) {
	c := newClock()
	l := newLimiter(Rate{Max: 2, Window: time.Hour}, c.Now)
	l.allow("first")
	c.Advance(time.Second)
	for i := range maxLimiterKeys - 2 {
		l.allow("filler-" + strconv.Itoa(i))
	}
	for range 2 {
		l.allow("hot")
	}
	if n := len(l.windows); n != maxLimiterKeys {
		t.Fatalf("%d windows, want a full table", n)
	}

	if ok, retry := l.allow("newcomer"); !ok {
		t.Fatalf("a full table refused a new caller (retry %s)", retry)
	}
	if n := len(l.windows); n != maxLimiterKeys {
		t.Fatalf("%d windows after a new caller, want %d", n, maxLimiterKeys)
	}
	if _, kept := l.windows["first"]; kept {
		t.Fatal("the oldest window was not the one forgotten")
	}
	if ok, _ := l.allow("hot"); ok {
		t.Fatal("a recent caller lost its count to a new one")
	}
	// A steady stream of new callers all get in, and the table stays bounded.
	for i := range 1000 {
		if ok, _ := l.allow("flood-" + strconv.Itoa(i)); !ok {
			t.Fatalf("new caller %d refused", i)
		}
	}
	if n := len(l.windows); n != maxLimiterKeys {
		t.Fatalf("%d windows after a flood, want %d", n, maxLimiterKeys)
	}
}

func TestParseRate(t *testing.T) {
	if r, err := ParseRate("120/1h"); err != nil || r.Max != 120 || r.Window != time.Hour {
		t.Fatalf("120/1h: %+v %v", r, err)
	}
	for _, bad := range []string{"", "120", "0/1h", "-1/1h", "x/1h", "10/", "10/0s", "10/-1m", "10/forever"} {
		if _, err := ParseRate(bad); err == nil {
			t.Errorf("ParseRate(%q) passed", bad)
		}
	}
}

func TestErrorShape(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, rateLimited(1500*time.Millisecond))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("%d Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"error":"rate_limited"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"message"`)) {
		t.Fatalf("body %s", w.Body)
	}
}
