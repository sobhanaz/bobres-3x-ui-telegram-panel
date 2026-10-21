package licensesrv

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// adminEnv is a license server with the signed admin API on, and a client that signs like the store does.
type adminEnv struct {
	*env
	t     *testing.T
	admin *AdminAPI
	srv   *httptest.Server
	priv  ed25519.PrivateKey
}

func newAdminEnv(t *testing.T, rate Rate) *adminEnv {
	t.Helper()
	e := newEnv(t, envOpts{})
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewAdminAPI(e.api, e.st, AdminAPIOptions{PublicKey: pub, Rate: rate, Now: e.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	e.api.Register(mux)
	admin.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &adminEnv{env: e, t: t, admin: admin, srv: srv, priv: priv}
}

// answer is an admin API answer, decoded loosely.
type answer struct {
	Code int `json:"-"`
	AdminLicense
	Licenses []AdminLicense `json:"licenses"`
	Error    string         `json:"error"`
}

// sign makes a request for path with body, signed by key at the env's clock.
func (a *adminEnv) sign(key ed25519.PrivateKey, path string, body any) *http.Request {
	a.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		a.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+path, bytes.NewReader(raw))
	if err != nil {
		a.t.Fatal(err)
	}
	ts, nonce, sig, err := license.AdminSign(key, http.MethodPost, req.URL.RequestURI(), raw, a.clock.Now())
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set(license.AdminHeaderTS, ts)
	req.Header.Set(license.AdminHeaderNonce, nonce)
	req.Header.Set(license.AdminHeaderSig, sig)
	return req
}

func (a *adminEnv) send(req *http.Request) answer {
	a.t.Helper()
	resp, err := a.srv.Client().Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		a.t.Fatal(err)
	}
	out := answer{Code: resp.StatusCode}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			a.t.Fatalf("answer %q: %v", data, err)
		}
	}
	return out
}

func (a *adminEnv) call(path string, body any) answer {
	a.t.Helper()
	return a.send(a.sign(a.priv, path, body))
}

var refSeq int

// ref makes a ref no other test uses (the database is shared and never cleaned).
func ref(t *testing.T) string {
	t.Helper()
	refSeq++
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("t-%s-%d-%x", t.Name(), refSeq, b)
}

func (a *adminEnv) create(customer, tier string, months int) answer {
	a.t.Helper()
	return a.call("/admin/v1/licenses/create", AdminRequest{Ref: ref(a.t), Customer: customer, Tier: tier, Months: months})
}

func TestAdminLifecycle(t *testing.T) {
	a := newAdminEnv(t, Rate{})
	now := a.clock.Now()

	c := a.create("tg:1001 Acme", "standard", 3)
	if c.Code != http.StatusOK || c.Key == "" || c.Tier != "standard" || c.Status2() != "active" {
		t.Fatalf("create: %+v", c)
	}
	if want := license.AddMonths(now, 3); c.EndsAt == nil || !c.EndsAt.Equal(want) {
		t.Fatalf("ends_at %v, want %v", c.EndsAt, want)
	}
	l, err := a.st.LicenseByID(context.Background(), a.st.db, c.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if l.Tier != "standard" || l.Customer != "tg:1001 Acme" || !l.Entitlements.Resellers || l.Entitlements.MaxJoinChats != 1 {
		t.Fatalf("stored license: %+v", l)
	}
	if got, err := a.st.LicenseByKeyHash(context.Background(), a.st.db, HashKey(c.Key), false); err != nil || got.ID != c.ID {
		t.Fatalf("the key does not find the license: %v", err)
	}
	// The key works: an install activates with it.
	if r := a.activate(c.Key, newInstall(), "shop.example"); r.Status != http.StatusOK || r.Token == "" {
		t.Fatalf("activate with the issued key: %d %s", r.Status, r.Error)
	}

	// Renew: adds months to the current end.
	extRef := ref(t)
	e1 := a.call("/admin/v1/licenses/extend", AdminRequest{Ref: extRef, ID: c.ID, Months: 2})
	if want := license.AddMonths(*c.EndsAt, 2); e1.Code != http.StatusOK || !e1.EndsAt.Equal(want) || e1.Replayed {
		t.Fatalf("extend: %+v, want end %v", e1, want)
	}
	// The same ref again changes nothing and says so.
	e2 := a.call("/admin/v1/licenses/extend", AdminRequest{Ref: extRef, ID: c.ID, Months: 2})
	if e2.Code != http.StatusOK || !e2.Replayed || !e2.EndsAt.Equal(*e1.EndsAt) {
		t.Fatalf("replayed extend: %+v", e2)
	}
	if got, _ := a.st.LicenseByID(context.Background(), a.st.db, c.ID, false); !got.EndsAt.Equal(*e1.EndsAt) {
		t.Fatalf("a replay moved the end: %v", got.EndsAt)
	}

	// Upgrade: the new tier's entitlements.
	up := a.call("/admin/v1/licenses/tier", AdminRequest{Ref: ref(t), ID: c.ID, Tier: "pro"})
	if up.Code != http.StatusOK || up.Tier != "pro" {
		t.Fatalf("tier: %+v", up)
	}
	if got, _ := a.st.LicenseByID(context.Background(), a.st.db, c.ID, false); !got.Entitlements.WhiteLabel || got.Entitlements.MaxJoinChats != license.Unlimited {
		t.Fatalf("pro entitlements not applied: %+v", got.Entitlements)
	}

	// get and list.
	if g := a.call("/admin/v1/licenses/get", AdminRequest{ID: c.ID}); g.Code != http.StatusOK || g.Tier != "pro" || g.ActiveDomain != "shop.example" {
		t.Fatalf("get: %+v", g)
	}
	ls := a.call("/admin/v1/licenses/list", AdminRequest{Status: "active"})
	found := false
	for _, v := range ls.Licenses {
		found = found || v.ID == c.ID
	}
	if ls.Code != http.StatusOK || !found {
		t.Fatalf("list does not hold %s: %+v", c.ID, ls)
	}

	// Revoke: the license no longer refreshes, and nothing changes it any more.
	rv := a.call("/admin/v1/licenses/revoke", AdminRequest{Ref: ref(t), ID: c.ID, Reason: "refunded"})
	if rv.Code != http.StatusOK || rv.Status2() != "revoked" {
		t.Fatalf("revoke: %+v", rv)
	}
	if x := a.call("/admin/v1/licenses/extend", AdminRequest{Ref: ref(t), ID: c.ID, Months: 1}); x.Code != http.StatusConflict || x.Error != "revoked" {
		t.Fatalf("extend after revoke: %+v", x)
	}
	if x := a.call("/admin/v1/licenses/revoke", AdminRequest{Ref: ref(t), ID: c.ID, Reason: "again"}); x.Code != http.StatusConflict {
		t.Fatalf("second revoke: %+v", x)
	}

	// Every change is in the audit, by the API.
	au, err := a.st.Audit(context.Background(), a.st.db, c.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range au {
		if e.Actor == adminActor {
			seen[e.Action] = true
		}
	}
	for _, want := range []string{"license.create", "license.update", "license.revoke"} {
		if !seen[want] {
			t.Errorf("no %s by %s in the audit: %v", want, adminActor, au)
		}
	}
}

// Status2 is the license's status (Code is the HTTP status).
func (a answer) Status2() string { return a.Status }

func TestAdminRefusesWhatIsNotSignedByTheKey(t *testing.T) {
	a := newAdminEnv(t, Rate{})
	body := AdminRequest{Ref: ref(t), Customer: "x", Tier: "standard", Months: 1}
	_, other, _ := ed25519.GenerateKey(rand.Reader)

	cases := map[string]*http.Request{
		"another key": a.sign(other, "/admin/v1/licenses/create", body),
	}
	unsigned := a.sign(a.priv, "/admin/v1/licenses/create", body)
	unsigned.Header.Del(license.AdminHeaderSig)
	cases["no signature"] = unsigned

	stale := a.sign(a.priv, "/admin/v1/licenses/create", body)
	a.clock.Advance(license.AdminSkew + time.Second) // the request is now too old
	cases["too old"] = stale

	tampered := a.sign(a.priv, "/admin/v1/licenses/create", body)
	other2 := []byte(`{"ref":"` + body.Ref + `","customer":"y","tier":"pro","months":99}`)
	tampered.Body = io.NopCloser(bytes.NewReader(other2))
	tampered.ContentLength = int64(len(other2))
	cases["another body"] = tampered

	moved := a.sign(a.priv, "/admin/v1/licenses/create", body)
	moved.URL.Path = "/admin/v1/licenses/revoke"
	cases["another path"] = moved

	for name, req := range cases {
		if r := a.send(req); r.Code != http.StatusUnauthorized || r.Error != "unauthorized" {
			t.Errorf("%s: %d %s, want 401 unauthorized", name, r.Code, r.Error)
		}
	}

	// A good request passes once; the same bytes again are a replay and fail.
	good := a.sign(a.priv, "/admin/v1/licenses/create", body)
	raw, _ := io.ReadAll(good.Body)
	good.Body = io.NopCloser(bytes.NewReader(raw))
	good.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	if r := a.send(good); r.Code != http.StatusOK {
		t.Fatalf("a good request: %d %s", r.Code, r.Error)
	}
	again := good.Clone(context.Background())
	again.Body = io.NopCloser(bytes.NewReader(raw))
	if r := a.send(again); r.Code != http.StatusUnauthorized {
		t.Fatalf("a replayed request: %d, want 401", r.Code)
	}

	// Plain HTTP is refused (unless the server allows it for tests).
	plain := a.sign(a.priv, "/admin/v1/licenses/list", AdminRequest{})
	plain.Header.Del("X-Forwarded-Proto")
	if r := a.send(plain); r.Code != http.StatusForbidden {
		t.Fatalf("plain http: %d", r.Code)
	}
}

func TestAdminRateLimit(t *testing.T) {
	a := newAdminEnv(t, Rate{Max: 3, Window: time.Minute})
	for i := range 3 {
		if r := a.call("/admin/v1/licenses/list", AdminRequest{}); r.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, r.Code)
		}
	}
	if r := a.call("/admin/v1/licenses/list", AdminRequest{}); r.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request: %d, want 429", r.Code)
	}
	a.clock.Advance(time.Minute)
	if r := a.call("/admin/v1/licenses/list", AdminRequest{}); r.Code != http.StatusOK {
		t.Fatalf("after the window: %d", r.Code)
	}
}

func TestAdminValidation(t *testing.T) {
	a := newAdminEnv(t, Rate{})
	bad := map[string]AdminRequest{
		"an unknown tier":     {Ref: ref(t), Customer: "x", Tier: "gold", Months: 1},
		"two ends":            {Ref: ref(t), Customer: "x", Tier: "pro", Months: 1, NoEnd: true},
		"an end and months":   {Ref: ref(t), Customer: "x", Tier: "pro", Months: 1, EndsAt: "2030-01-01"},
		"a bad date":          {Ref: ref(t), Customer: "x", Tier: "pro", EndsAt: "next year"},
		"an end in the past":  {Ref: ref(t), Customer: "x", Tier: "pro", EndsAt: "2020-01-01"},
		"an end far away":     {Ref: ref(t), Customer: "x", Tier: "pro", EndsAt: "2090-01-01"},
		"no tier":             {Ref: ref(t), Customer: "x", Months: 1},
		"zero months":         {Ref: ref(t), Customer: "x", Tier: "pro"},
		"too many months":     {Ref: ref(t), Customer: "x", Tier: "pro", Months: 121},
		"no customer":         {Ref: ref(t), Tier: "pro", Months: 1},
		"control in customer": {Ref: ref(t), Customer: "a\nb", Tier: "pro", Months: 1},
		"short ref":           {Ref: "abc", Customer: "x", Tier: "pro", Months: 1},
		"ref with a space":    {Ref: "abcdefgh ijk", Customer: "x", Tier: "pro", Months: 1},
		"customer too long":   {Ref: ref(t), Customer: string(bytes.Repeat([]byte("a"), 201)), Tier: "pro", Months: 1},
	}
	for name, req := range bad {
		if r := a.call("/admin/v1/licenses/create", req); r.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, r.Code, r.Error)
		}
	}
	// An unknown field is refused, not ignored.
	if r := a.call("/admin/v1/licenses/create", map[string]any{"ref": ref(t), "customer": "x", "tier": "pro", "months": 1, "price": 1}); r.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", r.Code)
	}
	// An unknown license: 404, and the ref is free again for the real thing.
	r1 := ref(t)
	if r := a.call("/admin/v1/licenses/extend", AdminRequest{Ref: r1, ID: "lic_nosuchlicense000", Months: 1}); r.Code != http.StatusNotFound {
		t.Errorf("unknown license: %d", r.Code)
	}
	if r := a.call("/admin/v1/licenses/create", AdminRequest{Ref: r1, Customer: "x", Tier: "pro", Months: 1}); r.Code != http.StatusOK {
		t.Errorf("a ref used by a refused request must stay free: %d %s", r.Code, r.Error)
	}
	// A ref cannot be reused for another action.
	r2 := ref(t)
	c := a.call("/admin/v1/licenses/create", AdminRequest{Ref: r2, Customer: "x", Tier: "pro", Months: 1})
	if x := a.call("/admin/v1/licenses/extend", AdminRequest{Ref: r2, ID: c.ID, Months: 1}); x.Code != http.StatusConflict || x.Error != "ref_reused" {
		t.Errorf("reused ref: %d %s", x.Code, x.Error)
	}
}

func TestAdminRekey(t *testing.T) {
	a := newAdminEnv(t, Rate{})
	c := a.create("tg:2002", "standard", 1)
	rk := a.call("/admin/v1/licenses/rekey", AdminRequest{Ref: ref(t), ID: c.ID})
	if rk.Code != http.StatusOK || rk.Key == "" || rk.Key == c.Key {
		t.Fatalf("rekey: %+v", rk)
	}
	if r := a.activate(c.Key, newInstall(), "old.example"); r.Status == http.StatusOK {
		t.Fatal("the replaced key still activates")
	}
	if r := a.activate(rk.Key, newInstall(), "new.example"); r.Status != http.StatusOK {
		t.Fatalf("the new key: %d %s", r.Status, r.Error)
	}
	// Once the license runs somewhere its key stays.
	if x := a.call("/admin/v1/licenses/rekey", AdminRequest{Ref: ref(t), ID: c.ID}); x.Code != http.StatusConflict || x.Error != "in_use" {
		t.Fatalf("rekey of an activated license: %d %s", x.Code, x.Error)
	}
}

// Two requests with one ref at the same moment make ONE license.
func TestAdminCreateIsOnceUnderRace(t *testing.T) {
	a := newAdminEnv(t, Rate{})
	r := ref(t)
	customer := "race " + r
	var wg sync.WaitGroup
	out := make([]answer, 4)
	for i := range out {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = a.call("/admin/v1/licenses/create", AdminRequest{Ref: r, Customer: customer, Tier: "standard", Months: 1})
		}()
	}
	wg.Wait()
	keys, replays := 0, 0
	ids := map[string]bool{}
	for _, o := range out {
		if o.Code != http.StatusOK {
			t.Fatalf("a racing create failed: %+v", o)
		}
		ids[o.ID] = true
		if o.Key != "" {
			keys++
		}
		if o.Replayed {
			replays++
		}
	}
	if keys != 1 || replays != 3 || len(ids) != 1 {
		t.Fatalf("keys=%d replays=%d ids=%v, want one key, three replays, one license", keys, replays, ids)
	}
}

func TestAdminManualCreateAndUpdate(t *testing.T) {
	a := newAdminEnv(t, Rate{})
	now := a.clock.Now()
	call := func(path string, req AdminRequest) answer {
		a.t.Helper()
		if req.Ref == "" && path != "/admin/v1/licenses/get" && path != "/admin/v1/licenses/list" {
			req.Ref = ref(a.t)
		}
		return a.call(path, req)
	}

	// An end date, no end, and a trial's own days: three ways to make a license by hand.
	dated := call("/admin/v1/licenses/create", AdminRequest{Customer: "Gift Co", Tier: "pro", EndsAt: "2027-03-01"})
	if dated.Code != http.StatusOK || dated.EndsAt == nil || !dated.EndsAt.Equal(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)) || dated.Key == "" {
		t.Fatalf("dated: %+v", dated)
	}
	forever := call("/admin/v1/licenses/create", AdminRequest{Customer: "Forever Co", Tier: "standard", NoEnd: true})
	if forever.Code != http.StatusOK || forever.EndsAt != nil {
		t.Fatalf("no end: %+v", forever)
	}
	trial := call("/admin/v1/licenses/create", AdminRequest{Customer: "Trial Co", Tier: "trial"})
	if trial.Code != http.StatusOK || trial.EndsAt == nil || !trial.EndsAt.Equal(now.AddDate(0, 0, 30)) {
		t.Fatalf("trial: %+v", trial)
	}
	// What was made by hand activates like any other.
	if r := a.activate(dated.Key, newInstall(), "gift.example"); r.Status != http.StatusOK {
		t.Fatalf("activate: %d %s", r.Status, r.Error)
	}

	// update: the customer, the tier (with its preset) and the end, each alone or together; an empty update is refused.
	up := call("/admin/v1/licenses/update", AdminRequest{ID: forever.ID, Customer: "  Renamed Co  ", Tier: "pro", EndsAt: "2028-01-01"})
	if up.Code != http.StatusOK || up.Customer != "Renamed Co" || up.Tier != "pro" || up.EndsAt == nil || up.EndsAt.Year() != 2028 {
		t.Fatalf("update: %+v", up)
	}
	got, _ := a.st.LicenseByID(context.Background(), a.st.db, forever.ID, false)
	if !got.Entitlements.WhiteLabel || got.Entitlements.MaxJoinChats != license.Unlimited {
		t.Fatalf("the new tier's preset was not applied: %+v", got.Entitlements)
	}
	noEnd := call("/admin/v1/licenses/update", AdminRequest{ID: forever.ID, NoEnd: true})
	if noEnd.Code != http.StatusOK || noEnd.EndsAt != nil {
		t.Fatalf("no end again: %+v", noEnd)
	}
	for name, bad := range map[string]AdminRequest{
		"nothing to change": {ID: forever.ID},
		"two ends":          {ID: forever.ID, EndsAt: "2028-01-01", NoEnd: true},
		"a past end":        {ID: forever.ID, EndsAt: "2020-01-01"},
		"a bad tier":        {ID: forever.ID, Tier: "gold"},
		"a control char":    {ID: forever.ID, Customer: "a\nb"},
	} {
		if r := call("/admin/v1/licenses/update", bad); r.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.Code, r.Error)
		}
	}
	if r := call("/admin/v1/licenses/update", AdminRequest{ID: "lic_nosuchlicense000", Customer: "x"}); r.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", r.Code)
	}
	// A repeat of the same ref changes nothing.
	r1 := ref(t)
	first := a.call("/admin/v1/licenses/update", AdminRequest{Ref: r1, ID: dated.ID, Customer: "Once"})
	second := a.call("/admin/v1/licenses/update", AdminRequest{Ref: r1, ID: dated.ID, Customer: "Once"})
	if first.Code != http.StatusOK || second.Code != http.StatusOK || !second.Replayed {
		t.Fatalf("repeat: %+v %+v", first, second)
	}
	// A revoked license does not change.
	if r := call("/admin/v1/licenses/revoke", AdminRequest{ID: trial.ID, Reason: "test"}); r.Code != http.StatusOK {
		t.Fatalf("revoke: %+v", r)
	}
	if r := call("/admin/v1/licenses/update", AdminRequest{ID: trial.ID, Customer: "x"}); r.Code != http.StatusConflict {
		t.Fatalf("update after revoke: %d", r.Code)
	}

	// get: entitlements, where it ran, and its audit.
	g := call("/admin/v1/licenses/get", AdminRequest{ID: dated.ID})
	if g.Code != http.StatusOK || g.Entitlements == nil || !g.Entitlements.WhiteLabel || len(g.Activations) != 1 || g.Activations[0].Domain != "gift.example" ||
		g.ActiveDomain != "gift.example" || g.LastSeenAt == nil || g.GraceDays == 0 {
		t.Fatalf("get: %+v", g)
	}
	actions := map[string]bool{}
	for _, e := range g.Audit {
		actions[e.Action] = true
	}
	if !actions["license.create"] || !actions["license.update"] || !actions["activation.create"] {
		t.Fatalf("audit actions: %v", actions)
	}
}
