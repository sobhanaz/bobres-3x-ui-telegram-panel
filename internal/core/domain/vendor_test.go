package domain

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// fakeVendor is the license server's admin API as far as core uses it: it checks every signature with the real
// verifier, makes a ref work once, and can be told to fail or to lose an answer.
type fakeVendor struct {
	t   *testing.T
	srv *httptest.Server
	pub ed25519.PublicKey
	now func() time.Time

	mu       sync.Mutex
	seq      int
	byRef    map[string]VendorLicenseInfo
	licenses map[string]*VendorLicenseInfo
	calls    []string // "path ref"
	failWith int      // answer every call with this status (0: work)
	loseNext bool     // process the next create, but answer 500
}

func newFakeVendor(t *testing.T, now func() time.Time) (*fakeVendor, string) {
	t.Helper()
	priv, pub, err := license.NewAdminKey()
	if err != nil {
		t.Fatal(err)
	}
	pk, err := license.ParseAdminPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeVendor{t: t, pub: pk, now: now, byRef: map[string]VendorLicenseInfo{}, licenses: map[string]*VendorLicenseInfo{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f, priv
}

func (f *fakeVendor) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if err := license.AdminVerify(f.pub, r.Method, r.URL.RequestURI(), r.Header.Get(license.AdminHeaderTS),
		r.Header.Get(license.AdminHeaderNonce), r.Header.Get(license.AdminHeaderSig), body, f.now()); err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Ref, ID, Customer, Tier string
		Months                  int
	}
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.URL.Path+" "+req.Ref)
	if f.failWith != 0 {
		w.WriteHeader(f.failWith)
		_, _ = w.Write([]byte(`{"error":"nope","message":"refused"}`))
		return
	}
	reply := func(v VendorLicenseInfo) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	if prev, ok := f.byRef[req.Ref]; ok {
		prev.Key, prev.Replayed = "", true
		reply(prev)
		return
	}
	var v VendorLicenseInfo
	switch {
	case strings.HasSuffix(r.URL.Path, "/create"):
		f.seq++
		ends := license.AddMonths(f.now(), req.Months)
		v = VendorLicenseInfo{ID: fmt.Sprintf("lic_fake%04d", f.seq), Customer: req.Customer, Tier: req.Tier, Status: "active", EndsAt: &ends,
			Key: fmt.Sprintf("BOBRES-FAKE-%04d", f.seq)}
		f.licenses[v.ID] = &v
	case strings.HasSuffix(r.URL.Path, "/extend"):
		l := f.licenses[req.ID]
		if l == nil {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		base := *l.EndsAt
		if base.Before(f.now()) {
			base = f.now()
		}
		ends := license.AddMonths(base, req.Months)
		l.EndsAt = &ends
		v = *l
		v.Key = ""
	case strings.HasSuffix(r.URL.Path, "/rekey"):
		l := f.licenses[req.ID]
		if l == nil {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		v = *l
		v.Key = fmt.Sprintf("BOBRES-REKEY-%04d", len(f.byRef))
	default:
		http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
		return
	}
	stored := v
	stored.Key = ""
	f.byRef[req.Ref] = stored
	if f.loseNext && strings.HasSuffix(r.URL.Path, "/create") {
		f.loseNext = false
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	reply(v)
}

func (f *fakeVendor) callCount(suffix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "/admin/v1/licenses/"+suffix+" ") {
			n++
		}
	}
	return n
}

func (f *fakeVendor) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

// vendorEnv is a store that sells licenses to a fake license server.
type vendorEnv struct {
	t     *testing.T
	svc   *Service
	s     *store.Store
	fake  *fakeVendor
	owner *store.User
	clock *time.Time
}

func newVendorEnv(t *testing.T) *vendorEnv {
	t.Helper()
	svc, s := testService(t)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	e := &vendorEnv{t: t, svc: svc, s: s, owner: staffMember(t, s, 9400, "owner"), clock: &now}
	var priv string
	e.fake, priv = newFakeVendor(t, func() time.Time { return *e.clock })
	svc.cfg.VendorHTTP = e.fake.srv.Client()
	svc.cfg.VendorNow = func() time.Time { return *e.clock }
	ctx := context.Background()
	if err := svc.SetVendorConfig(ctx, e.owner, e.fake.srv.URL, priv); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(ctx, e.owner, SettingVendorEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *vendorEnv) fund(u *store.User, amount int64) {
	e.t.Helper()
	err := e.s.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := e.s.Credit(context.Background(), tx, u.ID, "USDT", amount,
			&store.LedgerEntry{Kind: "topup", IdempotencyKey: fmt.Sprintf("fundu-%s-%d-%d", u.ID, amount, time.Now().UnixNano())})
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *vendorEnv) balance(u *store.User) int64 {
	e.t.Helper()
	w, err := e.s.GetWallet(context.Background(), e.s.Conn(), u.ID, "USDT")
	if err != nil {
		e.t.Fatal(err)
	}
	return w.Balance
}

func (e *vendorEnv) buy(u *store.User, tier string, months int, key string) (*VendorPurchase, error) {
	return e.svc.BuyVendorLicense(context.Background(), VendorBuyParams{UserID: u.ID, Tier: tier, Months: months, IdempotencyKey: key})
}

const usdt = 1_000_000

func TestVendorPricesComeFromTheTierFile(t *testing.T) {
	svc, _ := testService(t)
	tiers, err := svc.VendorTiers()
	if err != nil {
		t.Fatal(err)
	}
	if len(tiers) != 2 || tiers[0].Name != "standard" || tiers[1].Name != "pro" {
		t.Fatalf("tiers: %+v", tiers)
	}
	if tiers[0].Currency != "USDT" || tiers[0].Monthly != 4*usdt || tiers[0].Yearly != 40*usdt || tiers[1].Monthly != 12*usdt || tiers[1].Yearly != 120*usdt {
		t.Fatalf("prices: %+v", tiers)
	}
	for in, want := range map[string]int64{"4": 4 * usdt, "4.5": 4_500_000, "0.000001": 1, "12.25": 12_250_000} {
		if got, err := parseMinor(in, "USDT"); err != nil || got != want {
			t.Errorf("parseMinor(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "x", "-1", "1.0000001", "99999999999999999999"} {
		if _, err := parseMinor(in, "USDT"); err == nil {
			t.Errorf("parseMinor(%q) was accepted", in)
		}
	}
}

func TestVendorBuyNewLicense(t *testing.T) {
	e := newVendorEnv(t)
	buyer := seedUser(t, e.s, 9401)
	e.fund(buyer, 10*usdt)

	p, err := e.buy(buyer, "standard", VendorMonth, "tap-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Order.Status != "done" || p.Pending() || p.Replayed || p.License == nil || p.Key != "BOBRES-FAKE-0001" {
		t.Fatalf("purchase: %+v key=%q", p.Order, p.Key)
	}
	if want := license.AddMonths(*e.clock, 1); p.License.EndsAt == nil || !p.License.EndsAt.Equal(want) || p.License.Tier != "standard" {
		t.Fatalf("license: %+v", p.License)
	}
	if got := e.balance(buyer); got != 6*usdt {
		t.Fatalf("balance %d, want %d", got, 6*usdt)
	}
	// The key is kept encrypted and shown again to its owner only.
	if string(p.License.KeyEnc) == p.Key || strings.Contains(string(p.License.KeyEnc), "FAKE") {
		t.Fatal("the key is stored in clear")
	}
	if l, key, err := e.svc.ShowVendorKey(context.Background(), buyer.ID, p.License.ID); err != nil || key != p.Key || l.LicenseID != "lic_fake0001" {
		t.Fatalf("show: %v %q", err, key)
	}
	other := seedUser(t, e.s, 9402)
	if _, _, err := e.svc.ShowVendorKey(context.Background(), other.ID, p.License.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another user saw the key: %v", err)
	}

	// The same tap again: the first answer, no money moves, the server is not asked again.
	again, err := e.buy(buyer, "standard", VendorMonth, "tap-1")
	if err != nil || !again.Replayed || again.Key != p.Key || again.Order.ID != p.Order.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if got := e.balance(buyer); got != 6*usdt {
		t.Fatalf("a replay moved money: %d", got)
	}
	if n := e.fake.callCount("create"); n != 1 {
		t.Fatalf("%d create calls, want 1", n)
	}
	// The same key for another purchase is a conflict, not someone else's license.
	if _, err := e.buy(buyer, "pro", VendorMonth, "tap-1"); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("reused key: %v", err)
	}
	// The ledger and the wallet agree.
	if bal, sum, _ := ledger(t, e.s, buyer.ID); bal != sum {
		t.Fatalf("wallet %d, ledger %d", bal, sum)
	}
}

func TestVendorRefusesWhatItShouldNot(t *testing.T) {
	e := newVendorEnv(t)
	buyer := seedUser(t, e.s, 9411)
	ctx := context.Background()

	if _, err := e.buy(buyer, "standard", VendorMonth, "poor"); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("an empty wallet: %v", err)
	}
	if n := e.fake.callCount("create"); n != 0 {
		t.Fatalf("the server was asked without payment: %d", n)
	}
	e.fund(buyer, 100*usdt)
	for name, p := range map[string]VendorBuyParams{
		"a trial":         {UserID: buyer.ID, Tier: "trial", Months: 1, IdempotencyKey: "k1"},
		"3 months":        {UserID: buyer.ID, Tier: "pro", Months: 3, IdempotencyKey: "k2"},
		"no key":          {UserID: buyer.ID, Tier: "pro", Months: 1},
		"a stale price":   {UserID: buyer.ID, Tier: "pro", Months: 1, IdempotencyKey: "k3", ExpectedAmount: 1},
		"unknown tier":    {UserID: buyer.ID, Tier: "gold", Months: 1, IdempotencyKey: "k4"},
		"unknown renewal": {UserID: buyer.ID, Months: 1, IdempotencyKey: "k5", LicenseID: "00000000-0000-0000-0000-000000000000"},
	} {
		if _, err := e.svc.BuyVendorLicense(ctx, p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got := e.balance(buyer); got != 100*usdt {
		t.Fatalf("a refused purchase cost money: %d", got)
	}
	// Off by switch: nothing is sold.
	if err := e.svc.SetSetting(ctx, e.owner, SettingVendorEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.buy(buyer, "standard", VendorMonth, "off"); !errors.Is(err, ErrState) {
		t.Fatalf("sales off: %v", err)
	}
	// A banned buyer buys nothing.
	if err := e.svc.SetSetting(ctx, e.owner, SettingVendorEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	e.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, buyer.ID)
	if _, err := e.buy(buyer, "standard", VendorMonth, "banned"); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("banned: %v", err)
	}
}

func (e *vendorEnv) sql(q string, args ...any) {
	e.t.Helper()
	if _, err := e.s.DB().Exec(context.Background(), q, args...); err != nil {
		e.t.Fatalf("%v\n%s", err, q)
	}
}

func TestVendorServerDownThenWorkerSettles(t *testing.T) {
	e := newVendorEnv(t)
	buyer := seedUser(t, e.s, 9421)
	e.fund(buyer, 20*usdt)
	e.fake.set(func() { e.fake.failWith = http.StatusServiceUnavailable })

	p, err := e.buy(buyer, "pro", VendorMonth, "down-1")
	if err != nil {
		t.Fatal(err)
	}
	// The buyer has paid; the license is on its way.
	if !p.Pending() || p.License != nil || p.Key != "" {
		t.Fatalf("an unanswered order must be pending: %+v", p.Order)
	}
	if got := e.balance(buyer); got != 8*usdt {
		t.Fatalf("balance %d, want %d", got, 8*usdt)
	}
	o, err := e.s.GetVendorOrder(context.Background(), e.s.Conn(), p.Order.ID, false)
	if err != nil || o.Status != "paid" || o.Attempts != 1 || o.LastError == "" || !o.NextAttemptAt.After(*e.clock) {
		t.Fatalf("order after a failed attempt: %+v %v", o, err)
	}
	// Not due yet: the worker leaves it alone.
	if n, err := e.svc.SettleVendorOrders(context.Background()); err != nil || n != 0 {
		t.Fatalf("settle before the wait: %d %v", n, err)
	}
	// The server comes back and the wait passes.
	e.fake.set(func() { e.fake.failWith = 0 })
	*e.clock = e.clock.Add(vendorFirstWait * 4)
	if n, err := e.svc.SettleVendorOrders(context.Background()); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	p2, err := e.svc.vendorPurchase(context.Background(), mustOrder(t, e, p.Order.ID), true)
	if err != nil || p2.Order.Status != "done" || p2.License == nil || p2.Key == "" {
		t.Fatalf("after the worker: %+v %v", p2, err)
	}
	if n := e.fake.callCount("create"); n != 2 {
		t.Fatalf("%d create calls, want 2 (the refused one and the settled one)", n)
	}
	// Settling again does nothing more.
	if n, err := e.svc.SettleVendorOrders(context.Background()); err != nil || n != 0 {
		t.Fatalf("second settle: %d %v", n, err)
	}
	if got := e.balance(buyer); got != 8*usdt {
		t.Fatalf("balance moved: %d", got)
	}
}

func mustOrder(t *testing.T, e *vendorEnv, id string) *store.VendorOrder {
	t.Helper()
	o, err := e.s.GetVendorOrder(context.Background(), e.s.Conn(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestVendorRefusalRefundsTheBuyer(t *testing.T) {
	e := newVendorEnv(t)
	buyer := seedUser(t, e.s, 9431)
	e.fund(buyer, 10*usdt)
	e.fake.set(func() { e.fake.failWith = http.StatusForbidden })

	p, err := e.buy(buyer, "standard", VendorMonth, "refuse-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Order.Status != "refunded" || p.License != nil || p.Order.LastError == "" {
		t.Fatalf("a refused order: %+v", p.Order)
	}
	if got := e.balance(buyer); got != 10*usdt {
		t.Fatalf("balance %d, want the money back (%d)", got, 10*usdt)
	}
	if bal, sum, _ := ledger(t, e.s, buyer.ID); bal != sum {
		t.Fatalf("wallet %d, ledger %d", bal, sum)
	}
	// Two ledger entries tell the story, and a repeat refunds nothing more.
	var kinds []string
	rows, err := e.s.DB().Query(context.Background(), `SELECT kind FROM core.ledger_entries WHERE user_id = $1 AND kind LIKE 'license_%' ORDER BY created_at, id`, buyer.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds = append(kinds, k)
	}
	rows.Close()
	if strings.Join(kinds, ",") != "license_purchase,license_refund" {
		t.Fatalf("ledger kinds: %v", kinds)
	}
	if _, err := e.svc.fulfillVendorOrder(context.Background(), p.Order.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.balance(buyer); got != 10*usdt {
		t.Fatalf("a repeat refunded again: %d", got)
	}
}

func TestVendorRenewExtendsOnce(t *testing.T) {
	e := newVendorEnv(t)
	buyer := seedUser(t, e.s, 9441)
	e.fund(buyer, 100*usdt)
	first, err := e.buy(buyer, "standard", VendorMonth, "new-1")
	if err != nil {
		t.Fatal(err)
	}
	end1 := *first.License.EndsAt
	ctx := context.Background()
	renew := VendorBuyParams{UserID: buyer.ID, Months: VendorYear, IdempotencyKey: "renew-1", LicenseID: first.License.ID}
	r, err := e.svc.BuyVendorLicense(ctx, renew)
	if err != nil {
		t.Fatal(err)
	}
	if r.Order.Kind != "renew" || r.Order.Status != "done" || r.Order.Amount != 40*usdt || r.License == nil {
		t.Fatalf("renewal: %+v", r.Order)
	}
	if want := license.AddMonths(end1, 12); !r.License.EndsAt.Equal(want) {
		t.Fatalf("end %v, want %v", r.License.EndsAt, want)
	}
	// A repeat of the tap changes nothing.
	if again, err := e.svc.BuyVendorLicense(ctx, renew); err != nil || !again.Replayed {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	stored, _ := e.s.GetVendorLicense(ctx, e.s.Conn(), first.License.ID, false)
	if !stored.EndsAt.Equal(license.AddMonths(end1, 12)) {
		t.Fatalf("stored end %v", stored.EndsAt)
	}
	if got, want := e.balance(buyer), int64(100*usdt-4*usdt-40*usdt); got != want {
		t.Fatalf("balance %d, want %d", got, want)
	}
	// Someone else cannot renew it (and pay for it) by guessing its id.
	other := seedUser(t, e.s, 9442)
	e.fund(other, 100*usdt)
	renew.UserID, renew.IdempotencyKey = other.ID, "renew-2"
	if _, err := e.svc.BuyVendorLicense(ctx, renew); !errors.Is(err, ErrForbidden) {
		t.Fatalf("renewing another's license: %v", err)
	}
	if got := e.balance(other); got != 100*usdt {
		t.Fatalf("a refused renewal cost money: %d", got)
	}
	if mine, err := e.svc.MyVendorLicenses(ctx, buyer.ID); err != nil || len(mine) != 1 {
		t.Fatalf("my licenses: %v %v", mine, err)
	}
}

// The license server makes the license but the answer is lost: the retry finds it by its ref and takes a new key.
func TestVendorLostAnswerGetsANewKey(t *testing.T) {
	e := newVendorEnv(t)
	buyer := seedUser(t, e.s, 9451)
	e.fund(buyer, 10*usdt)
	e.fake.set(func() { e.fake.loseNext = true })

	p, err := e.buy(buyer, "standard", VendorMonth, "lost-1")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Pending() {
		t.Fatalf("the lost answer should leave the order pending: %+v", p.Order)
	}
	*e.clock = e.clock.Add(vendorFirstWait * 4)
	if n, err := e.svc.SettleVendorOrders(context.Background()); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	done, err := e.svc.vendorPurchase(context.Background(), mustOrder(t, e, p.Order.ID), true)
	if err != nil || done.Order.Status != "done" || !strings.HasPrefix(done.Key, "BOBRES-REKEY-") {
		t.Fatalf("after the retry: %+v key=%q %v", done.Order, done.Key, err)
	}
	if n := e.fake.callCount("rekey"); n != 1 {
		t.Fatalf("%d rekey calls, want 1", n)
	}
	e.fake.mu.Lock()
	made := e.fake.seq
	e.fake.mu.Unlock()
	if made != 1 {
		t.Fatalf("the server made %d licenses, want 1", made)
	}
}

func TestVendorDoubleTapRaceIsOnePurchase(t *testing.T) {
	e := newVendorEnv(t)
	buyer := seedUser(t, e.s, 9461)
	e.fund(buyer, 50*usdt)
	needConns(t, e.s, 4) // three purchases at once, each in a transaction
	var wg sync.WaitGroup
	ok := make([]bool, 3)
	for i := range ok {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := e.buy(buyer, "standard", VendorMonth, "race-1")
			ok[i] = err == nil && p != nil
		}()
	}
	wg.Wait()
	if got := e.balance(buyer); got != 46*usdt {
		t.Fatalf("balance %d, want one purchase (%d)", got, 46*usdt)
	}
	var n int
	if err := e.s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.vendor_orders WHERE user_id = $1`, buyer.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d orders: %v", n, err)
	}
	if made := e.fake.callCount("create"); made < 1 {
		t.Fatal("the server was never asked")
	}
	e.fake.mu.Lock()
	licenses := e.fake.seq
	e.fake.mu.Unlock()
	if licenses != 1 {
		t.Fatalf("the server made %d licenses, want 1", licenses)
	}
}

func TestVendorConfigIsWriteOnlyAndChecked(t *testing.T) {
	svc, s := testService(t)
	owner := staffMember(t, s, 9471, "owner")
	ctx := context.Background()
	priv, _, err := license.NewAdminKey()
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][2]string{
		"http address": {"http://license.example.com", priv},
		"no address":   {"", priv},
		"a space":      {"https://license example.com", priv},
		"not a key":    {"https://license.example.com", "nope"},
		"a public key": {"https://license.example.com", priv + priv},
	} {
		if err := svc.SetVendorConfig(ctx, owner, c[0], c[1]); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := svc.SetVendorConfig(ctx, owner, "https://license.example.com/", priv); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.GetVendorConfig(ctx, s.Conn())
	if err != nil || cfg.AdminURL != "https://license.example.com" {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	if strings.Contains(string(cfg.AdminKeyEnc), priv) || len(cfg.AdminKeyEnc) == 0 {
		t.Fatal("the admin key is stored in clear")
	}
	// The audit names the change and never the key.
	var details string
	if err := s.DB().QueryRow(ctx, `SELECT coalesce(before::text, '') || coalesce(after::text, '') FROM core.audit_log WHERE action = 'vendor.config' ORDER BY created_at DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, priv) {
		t.Fatalf("the audit holds the key: %s", details)
	}
}
