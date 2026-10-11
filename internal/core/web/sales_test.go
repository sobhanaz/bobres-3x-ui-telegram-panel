package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Fakes for the services behind the sales pages.

type fakePay struct {
	mu      sync.Mutex
	pending []domain.PendingPayment
	history []domain.PaymentRecord
	reviews []string // reviewer|intent|decision|reason
}

var errNotUsed = errors.New("not used by the dashboard")

func (p *fakePay) CreateIntent(context.Context, string, string, string, int64, string, string) (string, string, error) {
	return "", "", errNotUsed
}
func (p *fakePay) SubmitReceipt(context.Context, string, string, string, string) (string, error) {
	return "", errNotUsed
}
func (p *fakePay) SubmitTXID(context.Context, string, string, string, string) (string, error) {
	return "", errNotUsed
}
func (p *fakePay) ListPending(context.Context, int) ([]domain.PendingPayment, error) {
	return p.pending, nil
}
func (p *fakePay) Review(_ context.Context, reviewer, intent, decision, reason string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reviews = append(p.reviews, reviewer+"|"+intent+"|"+decision+"|"+reason)
	if decision == "approved" {
		return "succeeded", nil
	}
	return "failed", nil
}
func (p *fakePay) ListPayments(_ context.Context, f domain.PaymentFilter, _, _ int) ([]domain.PaymentRecord, int64, error) {
	var out []domain.PaymentRecord
	for _, r := range p.history {
		if (f.IntentID == "" || r.IntentID == f.IntentID) && (f.OrderID == "" || r.OrderID == f.OrderID) && (f.Status == "" || r.Status == f.Status) {
			out = append(out, r)
		}
	}
	return out, int64(len(out)), nil
}
func (p *fakePay) StartGateway(context.Context, domain.GatewayStart) (*domain.GatewayIntent, error) {
	return nil, errNotUsed
}
func (p *fakePay) CheckIntent(context.Context, string, string) (*domain.GatewayIntent, error) {
	return nil, errNotUsed
}
func (p *fakePay) PrecheckStars(context.Context, string, string, int64, string) (*domain.GatewayIntent, error) {
	return nil, errNotUsed
}
func (p *fakePay) ConfirmStars(context.Context, string, string, int64, string, string) (*domain.GatewayIntent, error) {
	return nil, errNotUsed
}
func (p *fakePay) ListGateways(context.Context) ([]string, error) { return nil, nil }

type fakeProv struct {
	mu     sync.Mutex
	ops    []string
	limits map[string][2]int64 // subscription -> {expires, traffic}
	usage  domain.Usage
	down   bool
}

// errPanelDown is what the provisioner client returns when the panel (or
// the provisioner) does not answer.
var errPanelDown = fmt.Errorf("provisioner.GetUsage: %w", status.Error(codes.Unavailable, "panel get usage failed"))

func (p *fakeProv) record(op string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return errPanelDown
	}
	p.ops = append(p.ops, op)
	return nil
}
func (p *fakeProv) CreateClient(context.Context, string, string, int64, int64) (domain.ProvisionedClient, error) {
	return domain.ProvisionedClient{}, errNotUsed
}
func (p *fakeProv) GetLinks(context.Context, string) (domain.Links, error) { return domain.Links{}, errNotUsed }
func (p *fakeProv) Health(context.Context) (bool, string, error)           { return true, "", nil }
func (p *fakeProv) SetLimits(_ context.Context, id string, expires, traffic int64) error {
	if err := p.record("limits:" + id); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.limits == nil {
		p.limits = map[string][2]int64{}
	}
	p.limits[id] = [2]int64{expires, traffic}
	return nil
}
func (p *fakeProv) Usage(context.Context, string) (domain.Usage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return domain.Usage{}, errPanelDown
	}
	return p.usage, nil
}
func (p *fakeProv) SetEnabled(_ context.Context, id string, on bool) error {
	if on {
		return p.record("enable:" + id)
	}
	return p.record("disable:" + id)
}
func (p *fakeProv) ResetTraffic(_ context.Context, id string) error { return p.record("reset:" + id) }
func (p *fakeProv) Delete(_ context.Context, id string) error       { return p.record("delete:" + id) }
func (p *fakeProv) done() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.ops, ",")
}

type fakeFiles struct{ files map[string][]byte }

func (f fakeFiles) Fetch(_ context.Context, id string) (*botfiles.File, error) {
	data, ok := f.files[id]
	if !ok {
		return nil, botfiles.ErrNotFound
	}
	return &botfiles.File{Data: data, ContentType: "image/jpeg"}, nil
}

// salesFixture is a fixture with the fakes plugged in and an owner logged in.
func salesFixture(t *testing.T) (*fixture, *browser, *fakePay, *fakeProv) {
	t.Helper()
	f := newFixture(t)
	pay, prov := &fakePay{}, &fakeProv{}
	f.api.cfg.Payments, f.api.cfg.Provisioner = pay, prov
	f.api.cfg.Files = fakeFiles{files: map[string][]byte{"receipt-file-1": []byte("\xff\xd8\xffjpeg")}}
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	return f, b, pay, prov
}

func (f *fixture) customer(tg int64, username string) *store.User {
	f.t.Helper()
	u, err := f.st.UpsertUser(context.Background(), f.st.Conn(), tg, username, "fa", "")
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

func (f *fixture) plan(name string, days int32, traffic, price int64) *store.Plan {
	f.t.Helper()
	p := &store.Plan{NameI18n: map[string]string{"en": name}, Kind: "both", DurationDays: &days, TrafficBytes: &traffic,
		Price: price, Currency: "IRT", Enabled: true}
	if err := f.st.UpsertPlan(context.Background(), f.st.Conn(), p); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// order makes an order in a status, with a subscription in subStatus
// ("" = none; "active" = delivered with these limits).
func (f *fixture) order(u *store.User, p *store.Plan, status, subStatus string, expires time.Time, traffic int64) (*store.Order, *store.Subscription) {
	f.t.Helper()
	ctx := context.Background()
	o, _, err := f.st.CreateOrder(ctx, f.st.Conn(), &store.Order{UserID: u.ID, PlanID: p.ID, Type: "new", Status: status,
		Amount: p.Price, Currency: p.Currency, IdempotencyKey: buuid.MustV7().String()})
	if err != nil {
		f.t.Fatal(err)
	}
	if subStatus == "" {
		return o, nil
	}
	sub, err := f.st.CreateSubscription(ctx, f.st.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID,
		ClientEmail: "u-" + buuid.MustV7().String()[:12]})
	if err != nil {
		f.t.Fatal(err)
	}
	if subStatus == "active" {
		if err := f.st.ActivateSubscription(ctx, f.st.Conn(), sub.ID, buuid.MustV7().String(), "sub123", "https://sub.example/x",
			&expires, &traffic); err != nil {
			f.t.Fatal(err)
		}
		sub, _ = f.st.GetSubscription(ctx, f.st.Conn(), sub.ID)
	}
	return o, sub
}

func key() string { return strings.ReplaceAll(buuid.MustV7().String(), "-", "") }

func items(out map[string]any) []map[string]any {
	raw, _ := out["items"].([]any)
	res := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		m, _ := it.(map[string]any)
		res = append(res, m)
	}
	return res
}

func TestUsersPage(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	ali := f.customer(8001, "ali_shop")
	f.customer(8002, "sara")

	code, out := b.do(http.MethodGet, "/api/v1/users?q=@ALI", nil)
	if list := items(out); code != 200 || out["total"] != float64(1) || len(list) != 1 || list[0]["id"] != ali.ID {
		t.Fatalf("search: %d %v", code, out)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/users?q=8002", nil); code != 200 || out["total"] != float64(1) {
		t.Fatalf("by telegram id: %d %v", code, out)
	}
	code, out = b.do(http.MethodGet, "/api/v1/users/"+ali.ID, nil)
	if code != 200 || out["can_ban"] != true {
		t.Fatalf("detail: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/users/not-a-uuid", nil); code != 404 {
		t.Fatalf("bad id: %d", code)
	}

	// Balance: credit, a replay changes nothing, a debit beyond the balance fails.
	k := key()
	for i := 0; i < 2; i++ {
		code, out := b.do(http.MethodPost, "/api/v1/users/"+ali.ID+"/balance",
			map[string]string{"amount": "50,000", "currency": "IRT", "reason": "gift", "key": k})
		if code != 200 || asJSON(out["wallet"]) != `{"amount":50000,"currency":"IRT"}` {
			t.Fatalf("credit #%d: %d %v", i+1, code, out)
		}
	}
	if code, out := b.do(http.MethodPost, "/api/v1/users/"+ali.ID+"/balance",
		map[string]string{"amount": "-60000", "currency": "IRT", "reason": "fix", "key": key()}); code != 409 || out["error"] != "insufficient_funds" {
		t.Fatalf("overdraft: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/users/"+ali.ID+"/balance",
		map[string]string{"amount": "10", "currency": "IRT", "reason": "", "key": key()}); code != 400 {
		t.Fatalf("no reason: %d", code)
	}

	// Ban needs a reason; staff cannot be banned from here.
	if code, _ := b.do(http.MethodPost, "/api/v1/users/"+ali.ID+"/status", map[string]string{"status": "banned"}); code != 400 {
		t.Fatalf("ban without a reason: %d", code)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/users/"+ali.ID+"/status", map[string]string{"status": "banned", "reason": "fraud"}); code != 200 || out["status"] != "banned" {
		t.Fatalf("ban: %d %v", code, out)
	}
	admin := f.staffUser(8003, "admin")
	if code, _ := b.do(http.MethodPost, "/api/v1/users/"+admin.ID+"/status", map[string]string{"status": "banned", "reason": "x"}); code != 403 {
		t.Fatalf("banned a staff member: %d", code)
	}

	// Support reads users but cannot change balances.
	f.staffUser(8004, "support")
	sup := f.browser()
	sup.loginWithLink(8004)
	if code, _ := sup.do(http.MethodGet, "/api/v1/users", nil); code != 200 {
		t.Fatalf("support list: %d", code)
	}
	if code, _ := sup.do(http.MethodPost, "/api/v1/users/"+ali.ID+"/balance",
		map[string]string{"amount": "1", "currency": "IRT", "reason": "x", "key": key()}); code != 403 {
		t.Fatalf("support adjusted a balance: %d", code)
	}
}

func TestServicesPage(t *testing.T) {
	f, b, _, prov := salesFixture(t)
	u := f.customer(8101, "reza")
	p := f.plan("Monthly 50GB", 30, 50<<30, 120000)
	expires := time.Now().Add(5 * 24 * time.Hour).UTC().Truncate(time.Second)
	_, sub := f.order(u, p, "active", "active", expires, 50<<30)

	short := strings.ReplaceAll(sub.ID, "-", "")[26:] // the last 6 hex digits, as the bot shows them
	code, out := b.do(http.MethodGet, "/api/v1/services?q="+short, nil)
	if list := items(out); code != 200 || len(list) != 1 || list[0]["plan"].(map[string]any)["name"].(map[string]any)["en"] != "Monthly 50GB" {
		t.Fatalf("by short id: %d %v", code, out)
	}

	// Extend: a free renewal order the worker applies (10 more days, 5 GB more).
	ext := map[string]any{"days": 10, "traffic_gb": "5", "reason": "outage", "key": key()}
	code, out = b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/extend", ext)
	if code != 200 || out["order_id"] == "" {
		t.Fatalf("extend: %d %v", code, out)
	}
	if code, again := b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/extend", ext); code != 200 || again["order_id"] != out["order_id"] {
		t.Fatalf("a retried extension made another order: %v %v", again, out)
	}
	if worked, err := f.dom.NewProvisionWorker(prov, nil).ProvisionNext(context.Background()); err != nil || !worked {
		t.Fatalf("worker: %v %v", worked, err)
	}
	got := prov.limits[sub.ID]
	if got[0] != expires.Add(10*24*time.Hour).Unix() || got[1] != 55<<30 {
		t.Fatalf("limits on the panel: %v", got)
	}
	code, out = b.do(http.MethodGet, "/api/v1/services/"+sub.ID, nil)
	orders, _ := out["orders"].([]any)
	if code != 200 || len(orders) != 2 {
		t.Fatalf("detail with its orders: %d %v", code, out)
	}
	extOrder := orders[0].(map[string]any)
	if extOrder["staff"] != "@u" || extOrder["status"] != "active" || asJSON(extOrder["extend"]) != `{"bytes":5368709120,"days":10}` {
		t.Fatalf("the extension order: %v", extOrder)
	}

	// Disable, then extending is refused; enable reads the panel again.
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/enabled", map[string]any{"enabled": false, "reason": "abuse"}); code != 200 ||
		out["service"].(map[string]any)["status"] != "disabled" {
		t.Fatalf("disable: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/extend",
		map[string]any{"days": 1, "reason": "x", "key": key()}); code != 409 || out["error"] != "state" {
		t.Fatalf("extended a disabled service: %d %v", code, out)
	}
	prov.usage = domain.Usage{UsedBytes: 1 << 30, TotalBytes: 55 << 30, ExpiresAt: expires.Add(10 * 24 * time.Hour).Unix(), Enabled: true}
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/enabled", map[string]any{"enabled": true, "reason": "ok now"}); code != 200 ||
		out["service"].(map[string]any)["status"] != "active" || out["service"].(map[string]any)["traffic_used"] != float64(1<<30) {
		t.Fatalf("enable: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/reset-traffic", map[string]any{"reason": "monthly"}); code != 200 {
		t.Fatalf("reset: %d", code)
	}

	// The panel being down is a 503 with nothing changed.
	prov.down = true
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/sync", nil); code != 503 || out["error"] != "unavailable" {
		t.Fatalf("sync with the panel down: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/reset-traffic", map[string]any{"reason": "x"}); code != 503 {
		t.Fatalf("reset with the panel down: %d", code)
	}
	prov.down = false

	// Support may extend but not delete.
	f.staffUser(8102, "support")
	sup := f.browser()
	sup.loginWithLink(8102)
	if code, _ := sup.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/delete", map[string]any{"reason": "x"}); code != 403 {
		t.Fatalf("support deleted a service: %d", code)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/delete", map[string]any{"reason": "left"}); code != 200 ||
		out["service"].(map[string]any)["status"] != "deleted" {
		t.Fatalf("delete: %d %v", code, out)
	}
	if want := "limits:" + sub.ID + ",disable:" + sub.ID + ",enable:" + sub.ID + ",reset:" + sub.ID + ",delete:" + sub.ID; prov.done() != want {
		t.Fatalf("panel calls:\n%s\nwant\n%s", prov.done(), want)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/services", nil); out["total"] != float64(0) {
		t.Fatalf("a deleted service is listed by default: %v", out)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/services?status=deleted", nil); out["total"] != float64(1) {
		t.Fatalf("deleted filter: %v", out)
	}
}

func TestOrdersRetryAndRefund(t *testing.T) {
	f, b, pay, prov := salesFixture(t)
	u := f.customer(8201, "mina")
	p := f.plan("Quarterly", 90, 100<<30, 300000)
	failed, sub := f.order(u, p, "provision_failed", "pending", time.Time{}, 0)
	unpaid, _ := f.order(u, p, "awaiting_payment", "", time.Time{}, 0)
	pay.history = []domain.PaymentRecord{{IntentID: buuid.MustV7().String(), OrderID: failed.ID, UserID: u.ID, Provider: "manual_card",
		Status: "succeeded", Amount: 300000, Currency: "IRT", CreatedAt: time.Now()}}

	code, out := b.do(http.MethodGet, "/api/v1/orders/"+failed.ID, nil)
	if code != 200 || asJSON(out["can"]) != `{"refund":true,"retry":true}` || len(out["payments"].([]any)) != 1 {
		t.Fatalf("detail: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/orders/"+failed.ID+"/retry", nil); code != 200 {
		t.Fatalf("retry: %d", code)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/orders/"+unpaid.ID+"/retry", nil); code != 409 {
		t.Fatalf("retry of an unpaid order: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/orders/"+failed.ID+"/refund", map[string]string{"reason": "panel broken"})
	order, _ := out["order"].(map[string]any)
	if code != 200 || order["status"] != "cancelled" || asJSON(out["wallet"]) != `{"amount":300000,"currency":"IRT"}` || order["refund"] == nil {
		t.Fatalf("refund: %d %v", code, out)
	}
	if got, _ := f.st.GetSubscription(context.Background(), f.st.Conn(), sub.ID); got.Status != "deleted" || prov.done() != "delete:"+sub.ID {
		t.Fatalf("the unfinished service: %s, panel calls %q", got.Status, prov.done())
	}
	if code, out := b.do(http.MethodPost, "/api/v1/orders/"+failed.ID+"/refund", map[string]string{"reason": "again"}); code != 409 {
		t.Fatalf("a second refund: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/orders/"+unpaid.ID+"/refund", map[string]string{"reason": "x"}); code != 409 {
		t.Fatalf("refunded an unpaid order: %d", code)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/orders?status=cancelled", nil); out["total"] != float64(1) {
		t.Fatalf("status filter: %v", out)
	}
}

func TestPaymentsQueueAndReceipt(t *testing.T) {
	f, b, pay, _ := salesFixture(t)
	u := f.customer(8301, "nima")
	intent := buuid.MustV7().String()
	pay.pending = []domain.PendingPayment{{IntentID: intent, UserID: u.ID, Provider: "manual_card", Amount: 90000, Currency: "IRT",
		ReceiptFile: "receipt-file-1", ReferenceNumber: "778899", SubmittedAt: time.Now(), PossibleDuplicate: true}}
	pay.history = []domain.PaymentRecord{{IntentID: intent, UserID: u.ID, Provider: "manual_card", Status: "confirming",
		Amount: 90000, Currency: "IRT", CreatedAt: time.Now(), Proof: &domain.PaymentProof{ReceiptFile: "receipt-file-1"}}}

	code, out := b.do(http.MethodGet, "/api/v1/payments/pending", nil)
	if list := items(out); code != 200 || len(list) != 1 || list[0]["has_file"] != true || list[0]["possible_duplicate"] != true ||
		list[0]["user"].(map[string]any)["username"] != "nima" {
		t.Fatalf("queue: %d %v", code, out)
	}
	resp, err := b.c.Get(f.srv.URL + "/api/v1/payments/" + intent + "/receipt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || string(body) != "\xff\xd8\xffjpeg" ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("receipt: %d %v %q", resp.StatusCode, resp.Header, body)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/payments/"+intent+"/review", map[string]string{"decision": "rejected"}); code != 400 {
		t.Fatalf("a rejection without a reason: %d", code)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/payments/"+intent+"/review", map[string]string{"decision": "approved"}); code != 200 || out["status"] != "succeeded" {
		t.Fatalf("approve: %d %v", code, out)
	}
	owner, _ := f.st.GetUserByTelegramID(context.Background(), f.st.Conn(), ownerTG)
	if len(pay.reviews) != 1 || pay.reviews[0] != owner.ID+"|"+intent+"|approved|" {
		t.Fatalf("reviews: %v", pay.reviews)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/payments?status=confirming", nil); code != 200 || out["total"] != float64(1) {
		t.Fatalf("history: %d %v", code, out)
	}
}

func TestPlansLedgerDiscountsReferrals(t *testing.T) {
	f, b, _, _ := salesFixture(t)

	plan := map[string]any{"name": map[string]string{"fa": "ماهانه", "en": "Monthly"}, "kind": "time", "duration_days": 30,
		"price": "120,000", "currency": "IRT", "enabled": true}
	code, out := b.do(http.MethodPost, "/api/v1/plans", plan)
	saved, _ := out["plan"].(map[string]any)
	if code != 200 || asJSON(saved["price"]) != `{"amount":120000,"currency":"IRT"}` {
		t.Fatalf("create plan: %d %v", code, out)
	}
	plan["price"], plan["enabled"] = "99000", false
	if code, out := b.do(http.MethodPut, "/api/v1/plans/"+saved["id"].(string), plan); code != 200 || out["plan"].(map[string]any)["enabled"] != false {
		t.Fatalf("edit plan: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/plans", map[string]any{"name": map[string]string{"en": "Trial"}, "kind": "time",
		"duration_days": 1, "price": "5", "currency": "IRT", "is_trial": true}); code != 400 {
		t.Fatalf("a paid trial: %d", code)
	}

	// Ledger: list and CSV (formulas in names are defused).
	evil := f.customer(8401, "=cmd")
	if _, err := f.dom.AdjustBalance(context.Background(), f.customer(8402, "x"), evil.ID, 2500000, "USDT", "test", key()); err != nil {
		t.Fatal(err)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/ledger?user="+evil.ID, nil); code != 200 || out["total"] != float64(1) {
		t.Fatalf("ledger: %d %v", code, out)
	}
	resp, err := b.c.Get(f.srv.URL + "/api/v1/ledger.csv?currency=USDT")
	if err != nil {
		t.Fatal(err)
	}
	csv, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	lines := strings.Split(strings.TrimSpace(string(csv)), "\n")
	if resp.StatusCode != 200 || !strings.HasPrefix(string(csv), "\ufefftime_utc,") || len(lines) != 2 ||
		!strings.Contains(lines[1], ",'=cmd,adjust,2.500000,USDT,2.500000,") {
		t.Fatalf("csv: %d %q", resp.StatusCode, csv)
	}

	// Discounts.
	if code, out := b.do(http.MethodPost, "/api/v1/discounts", map[string]any{"code": "spring20", "percent": 20, "max_uses": 100, "enabled": true}); code != 200 ||
		out["discount"].(map[string]any)["code"] != "SPRING20" {
		t.Fatalf("create code: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/discounts/SPRING20/enabled", map[string]any{"enabled": false}); code != 200 ||
		out["discount"].(map[string]any)["enabled"] != false {
		t.Fatalf("turn off: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/discounts", map[string]any{"code": "BOTH", "percent": 10, "amount": "5000", "currency": "IRT"}); code != 400 {
		t.Fatalf("percent and amount: %d", code)
	}

	// Referrals.
	inviter := f.customer(8403, "host")
	if _, err := f.st.UpsertUser(context.Background(), f.st.Conn(), 8404, "guest", "fa", inviter.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/referrals", map[string]any{"reward_percent": 10}); code != 200 {
		t.Fatalf("set reward: %d", code)
	}
	code, out = b.do(http.MethodGet, "/api/v1/referrals", nil)
	top, _ := out["top"].([]any)
	if code != 200 || out["reward_percent"] != float64(10) || len(top) != 1 || top[0].(map[string]any)["invited"] != float64(1) {
		t.Fatalf("referrals: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/referrals", map[string]any{"reward_percent": 150}); code != 400 {
		t.Fatalf("150%% reward: %d", code)
	}
}
