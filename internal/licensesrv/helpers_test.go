package licensesrv

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The tests run against a real Postgres (this binary's own throwaway
// database from internal/testdb): the activation rules lean on a partial
// unique index, row locks and timestamp arithmetic that a fake would only
// imitate.
func TestMain(m *testing.M) { testdb.Main(m) }

const testKid = "srv-test"

var (
	storeOnce sync.Once
	sharedSt  *Store
	storeErr  error
)

// testStore is the migrated store every test shares. Tests never clean up:
// each makes its own licenses (the audit table refuses deletes anyway).
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := testdb.DSN(t)
	storeOnce.Do(func() {
		ctx := context.Background()
		if storeErr = Migrate(ctx, dsn, nil); storeErr != nil {
			return
		}
		sharedSt, storeErr = OpenStore(ctx, dsn)
	})
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	return sharedSt
}

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	t      *testing.T
	st     *Store
	clock  *clock
	signer *Signer
	ring   license.KeyRing
	svc    *Service
	api    *API
	srv    *httptest.Server
}

type envOpts struct {
	ipRate, keyRate Rate
	allowHTTP       bool
	trusted         []string // CIDRs (default: loopback)
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	st := testStore(t)
	if o.ipRate.Max == 0 {
		o.ipRate = Rate{Max: 10000, Window: time.Hour}
	}
	if o.keyRate.Max == 0 {
		o.keyRate = Rate{Max: 10000, Window: time.Hour}
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(testKid, priv)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := VerifyRing(signer, license.KeyRing{})
	if err != nil {
		t.Fatal(err)
	}
	c := newClock()
	svc, err := NewService(Options{Store: st, Signer: signer, Ring: ring, KeyRate: o.keyRate, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	ao := APIOptions{IPRate: o.ipRate, AllowHTTP: o.allowHTTP, Now: c.Now}
	if o.trusted != nil {
		ao.TrustedProxies, err = parsePrefixes(strings.Join(o.trusted, ","))
		if err != nil {
			t.Fatal(err)
		}
	}
	api, err := NewAPI(svc, ao)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{t: t, st: st, clock: c, signer: signer, ring: ring, svc: svc, api: api, srv: srv}
}

// reply is an API answer.
type reply struct {
	Status  int
	Token   string `json:"token"`
	Error   string `json:"error"`
	Message string `json:"message"`
	Header  http.Header
}

// post sends body to path through "the proxy": from 127.0.0.1 with
// X-Forwarded-Proto https, and X-Forwarded-For when ip is not "".
func (e *env) post(path string, body any, ip string) reply {
	e.t.Helper()
	raw, ok := body.([]byte)
	if !ok {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			e.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(raw))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	return e.do(req)
}

func (e *env) do(req *http.Request) reply {
	e.t.Helper()
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	r := reply{Status: resp.StatusCode, Header: resp.Header}
	if len(data) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(data, &r); err != nil {
			e.t.Fatalf("answer %q: %v", data, err)
		}
	}
	return r
}

func (e *env) activate(key, installID, domain string) reply {
	e.t.Helper()
	return e.post("/v1/activate", ActivateRequest{Key: key, InstallID: installID, Domain: domain, Version: "0.1.0"}, "")
}

func (e *env) refresh(token string) reply {
	e.t.Helper()
	return e.post("/v1/refresh", RefreshRequest{Token: token, Version: "0.1.1"}, "")
}

// key runs `licensesrv key ...` on the env's clock.
func (e *env) key(stdin string, args ...string) (code int, stdout, stderr string) {
	e.t.Helper()
	var out, errOut bytes.Buffer
	code = RunKey(context.Background(), e.st, args, strings.NewReader(stdin), &out, &errOut, AdminOptions{Now: e.clock.Now, Actor: "cli:test"})
	return code, out.String(), errOut.String()
}

const testEntitlements = `{"white_label": false, "max_servers": 2, "max_active_services": -1, "resellers": true,
  "gateways": ["zarinpal", "manual_card"], "crypto_watcher": false, "dashboard_staff_seats": 3, "updates_until": "2027-09-30"}`

// entFile writes entitlements JSON to a temp file.
func entFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ent.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// newLicense makes a license with `key create` and returns it and its key.
func (e *env) newLicense(extra ...string) (*License, string) {
	e.t.Helper()
	args := append([]string{"create", "-customer", "Acme VPN", "-tier", "standard", "-entitlements", entFile(e.t, testEntitlements)}, extra...)
	if !hasEnd(extra) {
		args = append(args, "-days", "365")
	}
	code, out, errOut := e.key("", args...)
	if code != 0 {
		e.t.Fatalf("key create: %d %s", code, errOut)
	}
	key := strings.TrimSpace(out)
	l, err := e.st.LicenseByKeyHash(context.Background(), e.st.db, HashKey(key), false)
	if err != nil {
		e.t.Fatalf("created license not found by its key: %v", err)
	}
	return l, key
}

func hasEnd(args []string) bool {
	for _, a := range args {
		if a == "-days" || a == "-months" || a == "-ends-at" || a == "-no-end" {
			return true
		}
	}
	return false
}

// verify checks a token the way an install bound to (installID, domain) does.
func (e *env) verify(token, installID, domain string) license.Token {
	e.t.Helper()
	tok, err := license.Verify(e.ring, token, license.Options{Domain: domain, InstallID: installID, RequireBound: true})
	if err != nil {
		e.t.Fatalf("token does not verify for %s/%s: %v", installID, domain, err)
	}
	return tok
}

func newInstall() string { return uuid.MustV7().String() }
