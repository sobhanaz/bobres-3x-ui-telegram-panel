package web

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/receipt"
)

// ---- a payments service in memory ----

// payModel is the payments service as far as the Mini App's payments use it:
// intents by idempotency key (asking again for the same key gives the same
// payment), the states a manual payment goes through (pending, then
// confirming once a proof is in), and the refusals the real service gives
// (another customer's payment, a payment that cannot take a proof now, a
// transaction id used twice), with the gRPC codes the real client keeps.
type payModel struct {
	mu       sync.Mutex
	gateways []string
	byID     map[string]*modelIntent
	byKey    map[string]string
	seq      int
	down     bool
	// submitErr is what a proof submission answers with, once.
	submitErr error
	// asked counts the calls by name.
	asked map[string]int
	// filters are what ListPayments was asked for, in order; ignoreFilter makes
	// it answer every payment whatever the filter says (a payments service that
	// does not filter, which the Mini App must not trust to).
	filters      []domain.PaymentFilter
	ignoreFilter bool
}

type modelIntent struct {
	domain.GatewayIntent
	userID, key                       string
	receipt, reference, network, txid string
	created                           time.Time
}

// modelEpoch is when the first payment of a model began: each next one a second
// later, so that "newest first" is a fixed order.
const modelEpoch = 1_800_000_000

func (p *payModel) enter(call string) error {
	if p.asked == nil {
		p.asked = map[string]int{}
	}
	p.asked[call]++
	if p.down {
		return fmt.Errorf("payments.%s: %w", call, status.Error(codes.Unavailable, "payments is not reachable"))
	}
	return nil
}

func (p *payModel) add(userID, key string, g domain.GatewayIntent) *modelIntent {
	if p.byID == nil {
		p.byID, p.byKey = map[string]*modelIntent{}, map[string]string{}
	}
	if id, ok := p.byKey[key]; ok {
		return p.byID[id]
	}
	p.seq++
	g.ID = fmt.Sprintf("0000000a-0000-4000-8000-%012d", p.seq) // a letter, so that capitals are a different spelling
	g.Status = "pending"
	in := &modelIntent{GatewayIntent: g, userID: userID, key: key, created: time.Unix(modelEpoch+int64(p.seq), 0)}
	p.byID[g.ID], p.byKey[key] = in, g.ID
	return in
}

func (p *payModel) CreateIntent(_ context.Context, orderID, userID, provider string, amount int64, currency, key string) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.enter("CreateIntent"); err != nil {
		return "", "", err
	}
	in := p.add(userID, key, domain.GatewayIntent{OrderID: orderID, Provider: provider, Amount: amount, Currency: currency})
	return in.ID, in.Status, nil
}

func (p *payModel) StartGateway(_ context.Context, g domain.GatewayStart) (*domain.GatewayIntent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.enter("StartGateway"); err != nil {
		return nil, err
	}
	in := p.add(g.UserID, g.IdempotencyKey, domain.GatewayIntent{OrderID: g.OrderID, Provider: g.Provider, Amount: g.Amount, Currency: g.Currency,
		GatewayAmount: g.GatewayAmount, GatewayCurrency: g.GatewayCurrency})
	cp := in.GatewayIntent
	return &cp, nil
}

func (p *payModel) lookup(call, userID, id string) (*modelIntent, error) {
	if err := p.enter(call); err != nil {
		return nil, err
	}
	in, ok := p.byID[strings.ToLower(id)] // a uuid column takes either case
	switch {
	case !ok:
		return nil, fmt.Errorf("payments.%s: %w", call, status.Error(codes.NotFound, "payment not found"))
	case in.userID != userID:
		return nil, fmt.Errorf("payments.%s: %w", call, status.Error(codes.PermissionDenied, "this payment belongs to another user"))
	}
	return in, nil
}

func (p *payModel) CheckIntent(_ context.Context, userID, id string) (*domain.GatewayIntent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	in, err := p.lookup("CheckIntent", userID, id)
	if err != nil {
		return nil, err
	}
	cp := in.GatewayIntent
	return &cp, nil
}

func (p *payModel) confirm(call, userID, id string, allowed ...string) (*modelIntent, error) {
	in, err := p.lookup(call, userID, id)
	if err != nil {
		return nil, err
	}
	ok := false
	for _, a := range allowed {
		ok = ok || in.Provider == a
	}
	if !ok {
		return nil, fmt.Errorf("payments.%s: %w", call, status.Error(codes.InvalidArgument, "this payment does not take this kind of proof ("+in.Provider+")"))
	}
	if p.submitErr != nil {
		err := p.submitErr
		p.submitErr = nil
		return nil, fmt.Errorf("payments.%s: %w", call, err)
	}
	if in.Status != "pending" {
		return nil, fmt.Errorf("payments.%s: %w", call, status.Error(codes.FailedPrecondition, "this payment cannot change state now"))
	}
	return in, nil
}

func (p *payModel) SubmitReceipt(_ context.Context, userID, id, file, reference string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	in, err := p.confirm("SubmitReceipt", userID, id, domain.ManualCard, domain.ManualCrypto, domain.ManualZarinpal)
	if err != nil {
		return "", err
	}
	if in.Provider == domain.ManualCard && strings.TrimSpace(reference) == "" {
		return "", fmt.Errorf("payments.SubmitReceipt: %w", status.Error(codes.InvalidArgument, "the bank reference number is required for a card transfer"))
	}
	in.receipt, in.reference, in.Status = file, strings.TrimSpace(reference), "confirming"
	return in.Status, nil
}

func (p *payModel) SubmitTXID(_ context.Context, userID, id, network, txid string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	in, err := p.confirm("SubmitTXID", userID, id, domain.ManualCrypto)
	if err != nil {
		return "", err
	}
	for _, o := range p.byID {
		if o.txid != "" && strings.EqualFold(strings.TrimPrefix(o.txid, "0x"), strings.TrimPrefix(txid, "0x")) {
			return "", fmt.Errorf("payments.SubmitTXID: %w", status.Error(codes.AlreadyExists, "this transaction was already submitted"))
		}
	}
	in.network, in.txid, in.Status = strings.ToUpper(network), txid, "confirming"
	return in.Status, nil
}

func (p *payModel) ListGateways(context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.enter("ListGateways"); err != nil {
		return nil, err
	}
	return append([]string(nil), p.gateways...), nil
}

func (p *payModel) ListPending(context.Context, int) ([]domain.PendingPayment, error) {
	return nil, errNotUsed
}
func (p *payModel) Review(context.Context, string, string, string, string) (string, error) {
	return "", errNotUsed
}

// ListPayments is the payment history as the real service answers it: the
// payments that match every filter given, newest first, a page of them.
func (p *payModel) ListPayments(_ context.Context, f domain.PaymentFilter, page, size int) ([]domain.PaymentRecord, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.enter("ListPayments"); err != nil {
		return nil, 0, err
	}
	p.filters = append(p.filters, f)
	var all []*modelIntent
	for _, in := range p.byID {
		if p.ignoreFilter || ((f.UserID == "" || in.userID == f.UserID) && (f.OrderID == "" || in.OrderID == f.OrderID) &&
			(f.Status == "" || in.Status == f.Status) && (f.Provider == "" || in.Provider == f.Provider) && (f.IntentID == "" || in.ID == f.IntentID)) {
			all = append(all, in)
		}
	}
	slices.SortFunc(all, func(a, b *modelIntent) int { return cmp.Or(b.created.Compare(a.created), strings.Compare(b.ID, a.ID)) })
	if size <= 0 || size > 200 {
		size = 25
	}
	if page <= 0 {
		page = 1
	}
	from := min((page-1)*size, len(all))
	to := min(from+size, len(all))
	out := make([]domain.PaymentRecord, 0, to-from)
	for _, in := range all[from:to] {
		out = append(out, domain.PaymentRecord{IntentID: in.ID, OrderID: in.OrderID, UserID: in.userID, Provider: in.Provider, Status: in.Status,
			Amount: in.Amount, Currency: in.Currency, GatewayAmount: in.GatewayAmount, GatewayCurrency: in.GatewayCurrency, CreatedAt: in.created})
	}
	return out, int64(len(all)), nil
}
func (p *payModel) PrecheckStars(context.Context, string, string, int64, string) (*domain.GatewayIntent, error) {
	return nil, errNotUsed
}
func (p *payModel) ConfirmStars(context.Context, string, string, int64, string, string) (*domain.GatewayIntent, error) {
	return nil, errNotUsed
}

// intent is a copy of the payment with this id.
func (p *payModel) intent(id string) modelIntent {
	p.mu.Lock()
	defer p.mu.Unlock()
	if in, ok := p.byID[id]; ok {
		return *in
	}
	return modelIntent{}
}

// filterCount is how many times ListPayments has been asked, and filtersFrom
// the filters it was given after the first n of them.
func (p *payModel) filterCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.filters)
}

func (p *payModel) filtersFrom(n int) []domain.PaymentFilter {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.PaymentFilter(nil), p.filters[n:]...)
}

func (p *payModel) setStatus(id, st string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byID[id].Status = st
}

func (p *payModel) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byID)
}

func (p *payModel) calls(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked[name]
}

// ---- the bot ----

// miniBot is the bot as the Mini App's payments use it: it keeps what it was
// asked, and answers as it is told to.
type miniBot struct {
	mu       sync.Mutex
	links    []botfiles.InvoiceRequest
	receipts []botfiles.Receipt
	alerts   []botfiles.ProofNotice
	linkErr  error
	sendErr  error
	alertErr error
	// hold, when set, makes SendReceipt wait for it to be closed (after it
	// has said, on started, that it was called).
	hold    chan struct{}
	started chan struct{}
}

func (b *miniBot) CreateInvoiceLink(_ context.Context, in botfiles.InvoiceRequest) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.linkErr != nil {
		return "", b.linkErr
	}
	b.links = append(b.links, in)
	return fmt.Sprintf("https://t.me/$fakeInvoice%d", len(b.links)), nil
}

func (b *miniBot) SendReceipt(_ context.Context, r botfiles.Receipt) (string, error) {
	b.mu.Lock()
	hold, started, err := b.hold, b.started, b.sendErr
	b.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if hold != nil {
		<-hold
	}
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.receipts = append(b.receipts, r)
	return fmt.Sprintf("AgACAgQAAxkBAAIBreceipt_%d", len(b.receipts)), nil
}

func (b *miniBot) NotifyProof(_ context.Context, n botfiles.ProofNotice) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.alerts = append(b.alerts, n)
	return b.alertErr
}

func (b *miniBot) sent() []botfiles.Receipt {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]botfiles.Receipt(nil), b.receipts...)
}

func (b *miniBot) linksMade() []botfiles.InvoiceRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]botfiles.InvoiceRequest(nil), b.links...)
}

func (b *miniBot) alerted() []botfiles.ProofNotice {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]botfiles.ProofNotice(nil), b.alerts...)
}

// ---- the fixture ----

type payFix struct {
	*miniFix
	model *payModel
	bot   *miniBot
	logs  *bytes.Buffer
}

// newPayFix is a Mini App store that is on, with every payment method set up
// and the payments service and the bot in front of it.
func newPayFix(t *testing.T) *payFix {
	t.Helper()
	m := newMiniFixture(t, true)
	p := &payFix{miniFix: m, model: &payModel{gateways: []string{domain.Stars, domain.Zarinpal}}, bot: &miniBot{}, logs: &bytes.Buffer{}}
	m.api.cfg.Payments, m.api.cfg.MiniAppBot = p.model, p.bot
	m.api.cfg.Log = slog.New(slog.NewTextHandler(p.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for k, v := range map[string]string{
		"payments.stars_rate": "1500", "payments.card_number": "6037-9911-2222-3333", "payments.card_holder": "Ali Rezaei",
		"payments.usdt_trc20": "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r", "payments.usdt_erc20": "0x0000000000000000000000000000000000000001",
		"payments.usdt_rate": "60000", "payments.zarinpal_link": "https://zarinp.al/shop",
	} {
		m.setting(k, v)
	}
	return p
}

// loosen lifts the limits on a customer's changes and uploads, for the tests
// that make many calls and are not about those limits.
func (p *payFix) loosen() {
	p.api.mini.write = newLimiter(100_000, time.Minute)
	p.api.mini.upload = newLimiter(100_000, time.Minute)
}

// buy makes an order for the plan as the customer and returns its id.
func (p *payFix) buy(tok string, plan *store.Plan, key string) string {
	p.t.Helper()
	code, o := p.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": plan.ID, "key": key})
	if code != 200 {
		p.t.Fatalf("order: %d %v", code, o)
	}
	return o["id"].(string)
}

// pay starts the payment of an order by a method.
func (p *payFix) pay(tok, order, method string) (int, map[string]any) {
	p.t.Helper()
	return p.call(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", tok, map[string]any{"method": method})
}

// payOK is pay that must work; it returns the payment's id.
func (p *payFix) payOK(tok, order, method string) (string, map[string]any) {
	p.t.Helper()
	code, res := p.pay(tok, order, method)
	if code != 200 {
		p.t.Fatalf("pay by %s: %d %v", method, code, res)
	}
	return res["intent_id"].(string), res
}

type formFile struct {
	field, name, ctype string
	data               []byte
}

// form is a multipart body: text fields (in order) and files.
func form(t *testing.T, fields [][2]string, files ...formFile) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, f.field, f.name))
		h.Set("Content-Type", f.ctype)
		pw, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), w.FormDataContentType()
}

// send makes a request with a raw body, as a client of its own address.
func (p *payFix) send(method, path, token, ctype string, body []byte) (int, map[string]any, http.Header) {
	p.t.Helper()
	miniClients++
	req, _ := http.NewRequest(method, p.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", miniClients%250+1))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := p.srv.Client().Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, resp.Header
}

// direct calls the API in this process, for a request whose size is not one a
// network connection would carry reliably (the server answers before it has
// read it all). length is its Content-Length (-1: not given).
func (p *payFix) direct(path, token, ctype string, body io.Reader, length int64) (int, map[string]any) {
	p.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.ContentLength = length
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ctype)
	rec := httptest.NewRecorder()
	p.srv.Config.Handler.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// receipt sends a proof of an image, with the bank reference number.
func (p *payFix) receipt(tok, intent, reference string, data []byte) (int, map[string]any) {
	p.t.Helper()
	fields := [][2]string{}
	if reference != "" {
		fields = append(fields, [2]string{"reference", reference})
	}
	body, ctype := form(p.t, fields, formFile{"receipt", "receipt.png", "image/png", data})
	code, out, _ := p.send(http.MethodPost, "/api/mini/v1/payments/"+intent+"/proof", tok, ctype, body)
	return code, out
}

// txid sends a proof of a transaction id.
func (p *payFix) txid(tok, intent string, body any) (int, map[string]any) {
	p.t.Helper()
	return p.call(http.MethodPost, "/api/mini/v1/payments/"+intent+"/proof", tok, body)
}

func pngReceipt(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const (
	hash64  = "4b6c7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d"
	hash64b = "5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d"
	hash64c = "6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e"
)

// ---- the methods ----

func TestMiniAppMethodsAreWhatTheStoreOffers(t *testing.T) {
	p := newPayFix(t)
	tok := p.signIn("tg:70:ann:en")
	list := func(currency string, amount int64) []string {
		t.Helper()
		code, out := p.call(http.MethodGet, fmt.Sprintf("/api/mini/v1/methods?currency=%s&amount=%d", currency, amount), tok, nil)
		if code != 200 {
			t.Fatalf("methods: %d %v", code, out)
		}
		var got []string
		for _, m := range out["methods"].([]any) {
			got = append(got, m.(string))
		}
		return got
	}
	same := func(what string, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: %v, want %v", what, got, want)
		}
	}

	// Everything is set up: the online methods Stars first, then the manual ones in the bot's order.
	same("a Toman price", list("IRT", 150_000), "stars", "zarinpal", "manual_card", "manual_crypto", "manual_zarinpal")
	same("a USDT price", list("USDT", 5_000_000), "manual_crypto")
	same("a price Zarinpal cannot take", list("IRT", 500), "stars", "manual_card", "manual_crypto", "manual_zarinpal")

	// The owner's switches and settings decide, as they do in the bot.
	p.setting("payments.manual_card.enabled", "false")
	same("card switched off", list("IRT", 150_000), "stars", "zarinpal", "manual_crypto", "manual_zarinpal")
	p.setting("payments.manual_card.enabled", "true")
	p.setting("payments.stars.enabled", "false")
	same("Stars switched off", list("IRT", 150_000), "zarinpal", "manual_card", "manual_crypto", "manual_zarinpal")
	p.setting("payments.stars.enabled", "true")
	p.setting("payments.usdt_rate", "")
	same("no USDT rate", list("IRT", 150_000), "stars", "zarinpal", "manual_card", "manual_zarinpal")
	same("no USDT rate, a USDT price", list("USDT", 5_000_000), "manual_crypto")
	p.setting("payments.usdt_rate", "60000")
	p.setting("payments.zarinpal_link", "")
	same("no payment link", list("IRT", 150_000), "stars", "zarinpal", "manual_card", "manual_crypto")
	p.setting("payments.zarinpal_link", "https://zarinp.al/shop")

	// Without the bot there is no invoice to make and no receipt to send.
	p.api.cfg.MiniAppBot = nil
	same("no bot", list("IRT", 150_000), "zarinpal")
	p.api.cfg.MiniAppBot = p.bot
	// Without the payments service nothing can be paid.
	p.api.cfg.Payments = nil
	same("no payments service", list("IRT", 150_000))
	p.api.cfg.Payments = p.model

	for _, q := range []string{"currency=EUR&amount=1", "currency=IRT&amount=-1", "currency=IRT&amount=x", "currency=IRT", "amount=5"} {
		if code, _ := p.call(http.MethodGet, "/api/mini/v1/methods?"+q, tok, nil); code != 400 {
			t.Errorf("query %q: %d", q, code)
		}
	}
	p.model.down = true
	if code, out := p.call(http.MethodGet, "/api/mini/v1/methods?currency=IRT&amount=150000", tok, nil); code != 503 || out["error"] != "unavailable" {
		t.Errorf("payments down: %d %v", code, out)
	}
}

// ---- Telegram Stars ----

func TestMiniAppStarsMakesAnInvoiceLinkForTheOrder(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:71:bob:en")
	bob, _ := p.st.GetUserByTelegramID(context.Background(), p.st.Conn(), 71)
	order := p.buy(tok, plan, "starskey123")

	id, res := p.payOK(tok, order, "stars")
	link := "https://t.me/$fakeInvoice1"
	if res["provider"] != "stars" || res["status"] != "pending" || res["invoice_url"] != link || res["stars"] != float64(100) ||
		res["amount"] != float64(150_000) || res["currency"] != "IRT" || res["order_id"] != order ||
		res["gateway_amount"] != float64(100) || res["gateway_currency"] != "XTR" {
		t.Fatalf("pay by Stars: %v", res)
	}
	// The payment is the bot's: its key, the order's price, the Stars at the store's rate.
	in := p.model.intent(id)
	if in.key != "int:"+order+":stars" || in.userID != bob.ID || in.Amount != 150_000 || in.Currency != "IRT" || in.GatewayAmount != 100 || in.GatewayCurrency != "XTR" {
		t.Fatalf("payment: %+v", in)
	}
	// The invoice is made from the payment: its id is the payload.
	want := botfiles.InvoiceRequest{Payload: id, Stars: 100, Lang: "en", Title: "Gold", Amount: 150_000, Currency: "IRT"}
	if got := p.bot.linksMade(); len(got) != 1 || got[0] != want {
		t.Fatalf("invoice asked for: %+v, want %+v", got, want)
	}

	// Asking again (a second tap, a retry, the bot's own button) is the same payment with a link of its own.
	id2, res2 := p.payOK(tok, order, "stars")
	if id2 != id || res2["invoice_url"] == link || p.model.count() != 1 || len(p.bot.linksMade()) != 2 {
		t.Fatalf("asking again: %s %v, %d payments, %d links", id2, res2, p.model.count(), len(p.bot.linksMade()))
	}

	// The client names no price: only the method.
	if code, _ := p.call(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", tok, map[string]any{"method": "stars", "amount": 1, "stars": 1}); code != 400 {
		t.Errorf("a client-chosen amount: %d", code)
	}
	if p.model.count() != 1 || p.model.intent(id).GatewayAmount != 100 {
		t.Errorf("the payment changed")
	}

	// Paid already: nothing to open, no new link.
	p.model.setStatus(id, "succeeded")
	_, done := p.payOK(tok, order, "stars")
	if done["status"] != "succeeded" || done["invoice_url"] != nil || done["stars"] != float64(100) || len(p.bot.linksMade()) != 2 {
		t.Errorf("a paid payment: %v, %d links", done, len(p.bot.linksMade()))
	}
}

// The invoice's title is the plan's name as the bot picks it: in the
// customer's language, else Persian, else English, else the store's own text.
func TestMiniAppStarsTitleFollowsTheCustomersLanguage(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	named := func(names map[string]string) *store.Plan {
		days, traffic := int32(30), int64(50<<30)
		pl := &store.Plan{NameI18n: names, Kind: "both", DurationDays: &days, TrafficBytes: &traffic, Price: 150_000, Currency: "IRT", Enabled: true}
		if err := p.st.UpsertPlan(context.Background(), p.st.Conn(), pl); err != nil {
			t.Fatal(err)
		}
		return pl
	}
	both := named(map[string]string{"fa": "ماهانه", "en": "Monthly"})
	onlyFa := named(map[string]string{"fa": "فقط فارسی"})
	onlyDe := named(map[string]string{"de": "Monatlich"})
	for i, c := range []struct {
		lang string
		plan *store.Plan
		want string
	}{
		{"en", both, "Monthly"}, {"fa", both, "ماهانه"}, {"en", onlyFa, "فقط فارسی"}, {"en", onlyDe, ""},
	} {
		tok := p.signIn(fmt.Sprintf("tg:%d:u%d:%s", 100+i, i, c.lang))
		id, _ := p.payOK(tok, p.buy(tok, c.plan, fmt.Sprintf("titlekey%03d", i)), "stars")
		got := p.bot.linksMade()
		if last := got[len(got)-1]; last.Payload != id || last.Title != c.want || last.Lang != c.lang {
			t.Errorf("a %s customer, plan %v: %+v, want title %q", c.lang, c.plan.NameI18n, last, c.want)
		}
	}
}

func TestMiniAppStarsRefusals(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:72:cy:en")
	order := p.buy(tok, plan, "starskey124")

	// Another customer's order is not found.
	other := p.signIn("tg:73:di:en")
	if code, _ := p.pay(other, order, "stars"); code != 404 {
		t.Errorf("another customer's order: %d", code)
	}
	// Telegram trouble is told as such, and no half-made payment is shown.
	p.bot.linkErr = fmt.Errorf("botfiles: invoice link: the bot answered 502")
	if code, out := p.pay(tok, order, "stars"); code != 502 || out["error"] != "bot_unavailable" || out["invoice_url"] != nil {
		t.Errorf("bot down: %d %v", code, out)
	}
	p.bot.linkErr = fmt.Errorf("%w: AMOUNT_TOO_BIG", botfiles.ErrInvoiceRefused)
	if code, out := p.pay(tok, order, "stars"); code != 409 || out["error"] != "invoice_refused" {
		t.Errorf("Telegram refused: %d %v", code, out)
	}
	p.bot.linkErr = fmt.Errorf("%w", botfiles.ErrBotBusy)
	if code, out := p.pay(tok, order, "stars"); code != 503 || out["error"] != "busy" {
		t.Errorf("bot busy: %d %v", code, out)
	}
	p.bot.linkErr = nil
	// The method is gone: no rate, switched off, a USDT order.
	p.setting("payments.stars_rate", "")
	if code, out := p.pay(tok, order, "stars"); code != 400 || out["error"] != "method_off" {
		t.Errorf("no rate: %d %v", code, out)
	}
	p.setting("payments.stars_rate", "1500")
	p.setting("payments.stars.enabled", "false")
	if code, out := p.pay(tok, order, "stars"); code != 400 || out["error"] != "method_off" {
		t.Errorf("switched off: %d %v", code, out)
	}
	p.setting("payments.stars.enabled", "true")
	if n := len(p.bot.linksMade()); n != 0 {
		t.Errorf("%d links made for payments that were refused", n)
	}
	// And now it works.
	if code, _ := p.pay(tok, order, "stars"); code != 200 {
		t.Errorf("after all that: %d", code)
	}
}

func TestMiniAppStarsTopup(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	tok := p.signIn("tg:74:eli:fa")
	eli, _ := p.st.GetUserByTelegramID(context.Background(), p.st.Conn(), 74)
	code, res := p.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 300_000, "method": "stars", "key": "topupstars1"})
	if code != 200 || res["invoice_url"] != "https://t.me/$fakeInvoice1" || res["stars"] != float64(200) || res["amount"] != float64(300_000) || res["currency"] != "IRT" {
		t.Fatalf("top-up by Stars: %d %v", code, res)
	}
	id := res["intent_id"].(string)
	if in := p.model.intent(id); in.key != "mini:topup:"+eli.ID+":topupstars1:stars" || in.OrderID != "" || in.Amount != 300_000 {
		t.Fatalf("payment: %+v", in)
	}
	// A top-up is invoiced as a top-up, in the customer's language, with no plan name.
	if got := p.bot.linksMade(); len(got) != 1 || got[0] != (botfiles.InvoiceRequest{Payload: id, Stars: 200, Lang: "fa", Amount: 300_000, Currency: "IRT", Topup: true}) {
		t.Fatalf("invoice asked for: %+v", got)
	}
	// The same top-up again is the same payment.
	if _, again := p.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 300_000, "method": "stars", "key": "topupstars1"}); again["intent_id"] != id {
		t.Errorf("a retry made another payment: %v", again)
	}
	// The bounds are the bot's.
	for _, amount := range []int{9_999, 50_000_001} {
		if code, _ := p.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": amount, "method": "stars", "key": "topupstars2"}); code != 400 {
			t.Errorf("top-up of %d: %d", amount, code)
		}
	}
}

// ---- manual payments ----

func TestMiniAppManualPaymentsShowWhatTheBotShows(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:75:fay:en")
	order := p.buy(tok, plan, "manualkey123")

	// Card to card: the card, its holder, the reference and the exact amount.
	id, res := p.payOK(tok, order, "manual_card")
	ref := domain.PaymentReference(id)
	d, _ := res["details"].(map[string]any)
	if res["status"] != "pending" || res["provider"] != "manual_card" || res["amount"] != float64(150_000) || res["currency"] != "IRT" || res["order_id"] != order ||
		d["card_number"] != "6037-9911-2222-3333" || d["card_holder"] != "Ali Rezaei" || d["reference"] != ref || len(ref) != 8 || res["invoice_url"] != nil {
		t.Fatalf("card: %v", res)
	}
	if in := p.model.intent(id); in.key != "int:"+order+":manual_card" || in.Amount != 150_000 || in.Provider != domain.ManualCard {
		t.Fatalf("card payment: %+v", in)
	}
	if _, again := p.payOK(tok, order, "manual_card"); again["intent_id"] != id || p.model.count() != 1 {
		t.Errorf("asking again: %v", again)
	}

	// Crypto: both networks' addresses, the reference, and what to send in USDT.
	id, res = p.payOK(tok, order, "manual_crypto")
	d, _ = res["details"].(map[string]any)
	if res["provider"] != "manual_crypto" || d["usdt_trc20"] != "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r" || d["usdt_erc20"] != "0x0000000000000000000000000000000000000001" ||
		d["reference"] != domain.PaymentReference(id) || res["usdt_amount"] != float64(2_500_000) || res["amount"] != float64(150_000) || res["currency"] != "IRT" {
		t.Fatalf("crypto: %v", res)
	}
	if d["card_number"] != nil {
		t.Errorf("crypto shows the card: %v", d)
	}

	// The Zarinpal payment link.
	id, res = p.payOK(tok, order, "manual_zarinpal")
	d, _ = res["details"].(map[string]any)
	if res["provider"] != "manual_zarinpal" || d["zarinpal_link"] != "https://zarinp.al/shop" || d["reference"] != domain.PaymentReference(id) {
		t.Fatalf("payment link: %v", res)
	}
	if p.model.count() != 3 {
		t.Errorf("%d payments for three methods", p.model.count())
	}
	if len(p.bot.linksMade()) != 0 || len(p.bot.sent()) != 0 {
		t.Errorf("the bot was asked for something: %+v %+v", p.bot.linksMade(), p.bot.sent())
	}

	// A USDT order is paid in USDT as it is priced.
	days, traffic := int32(30), int64(50<<30)
	usdt := &store.Plan{NameI18n: map[string]string{"en": "Gold USDT"}, Kind: "both", DurationDays: &days, TrafficBytes: &traffic,
		Price: 5_000_000, Currency: "USDT", Enabled: true}
	if err := p.st.UpsertPlan(context.Background(), p.st.Conn(), usdt); err != nil {
		t.Fatal(err)
	}
	uorder := p.buy(tok, usdt, "manualkey124")
	if code, out := p.pay(tok, uorder, "manual_card"); code != 400 || out["error"] != "method_off" {
		t.Errorf("card for a USDT order: %d %v", code, out)
	}
	if _, res = p.payOK(tok, uorder, "manual_crypto"); res["usdt_amount"] != float64(5_000_000) || res["currency"] != "USDT" {
		t.Errorf("USDT order: %v", res)
	}
}

func TestMiniAppManualMethodsNotOffered(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:76:gus:en")
	order := p.buy(tok, plan, "manualkey125")
	for _, c := range []struct{ method, key, off string }{
		{"manual_card", "payments.card_number", ""},
		{"manual_card", "payments.manual_card.enabled", "false"},
		{"manual_crypto", "payments.usdt_rate", ""},
		{"manual_crypto", "payments.manual_crypto.enabled", "false"},
		{"manual_zarinpal", "payments.zarinpal_link", ""},
		{"manual_zarinpal", "payments.manual_zarinpal.enabled", "false"},
	} {
		old := ""
		if s, err := p.dom.Settings(context.Background()); err == nil {
			old = s[c.key]
		}
		p.setting(c.key, c.off)
		if code, out := p.pay(tok, order, c.method); code != 400 || out["error"] != "method_off" {
			t.Errorf("%s with %s=%q: %d %v", c.method, c.key, c.off, code, out)
		}
		if old == "" {
			old = "true"
		}
		p.setting(c.key, old)
	}
	if code, _ := p.pay(tok, order, "manual_bitcoin"); code != 400 {
		t.Errorf("an unknown method: %d", code)
	}
	if p.model.count() != 0 {
		t.Errorf("%d payments were made for methods that were not offered", p.model.count())
	}
	// The bot is gone: no receipt can be sent, so no manual payment is started.
	p.api.cfg.MiniAppBot = nil
	if code, out := p.pay(tok, order, "manual_card"); code != 400 || out["error"] != "method_off" {
		t.Errorf("without the bot: %d %v", code, out)
	}
	p.api.cfg.MiniAppBot = p.bot
}

func TestMiniAppTopupByManualMethods(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	tok := p.signIn("tg:77:hal:en")
	for _, method := range []string{"manual_card", "manual_crypto", "manual_zarinpal"} {
		code, res := p.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 200_000, "method": method, "key": "topupmanual1"})
		if code != 200 || res["status"] != "pending" || res["amount"] != float64(200_000) || res["currency"] != "IRT" || (res["order_id"] != nil && res["order_id"] != "") {
			t.Errorf("top-up by %s: %d %v", method, code, res)
		}
		d, _ := res["details"].(map[string]any)
		if d["reference"] != domain.PaymentReference(res["intent_id"].(string)) {
			t.Errorf("top-up by %s has no reference: %v", method, d)
		}
		if method == "manual_crypto" && res["usdt_amount"] != float64(3_340_000) { // 200,000 at 60,000 a USDT: 3.34 USDT, rounded up to the cent
			t.Errorf("crypto top-up amount: %v", res["usdt_amount"])
		}
	}
	// Each top-up method is a payment of its own; a retry is the same one.
	if p.model.count() != 3 {
		t.Errorf("%d payments", p.model.count())
	}
}

// ---- the proof: a receipt ----

func TestMiniAppReceiptIsSentToTheBotAndStoredAsTheProof(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:78:ivy:en")
	order := p.buy(tok, plan, "proofkey123")
	id, _ := p.payOK(tok, order, "manual_card")
	img := pngReceipt(t, 40, 30)

	code, out := p.receipt(tok, id, "  998877  ", img)
	if code != 200 || out["status"] != "confirming" || out["intent_id"] != id {
		t.Fatalf("proof: %d %v", code, out)
	}
	// The bot was given the image to send to the customer's own chat.
	sent := p.bot.sent()
	if len(sent) != 1 || sent[0].ChatID != 78 || sent[0].Lang != "en" || sent[0].Reference != domain.PaymentReference(id) ||
		sent[0].ContentType != "image/png" || !bytes.Equal(sent[0].Data, img) {
		t.Fatalf("sent to the bot: %+v", sent)
	}
	// What is stored is the file id Telegram gave, as the bot's flow stores one, and the bank reference.
	in := p.model.intent(id)
	if in.receipt != "AgACAgQAAxkBAAIBreceipt_1" || in.reference != "998877" || in.Status != "confirming" {
		t.Fatalf("stored proof: %+v", in)
	}
	// The staff are alerted, as for a proof sent in the bot's chat.
	if got := p.bot.alerted(); len(got) != 1 || got[0] != (botfiles.ProofNotice{TelegramID: 78, Username: "ivy", IntentID: id, Amount: 150_000, Currency: "IRT"}) {
		t.Fatalf("alerts: %+v", got)
	}
	// The payment is seen as waiting for review from the app's check too.
	if code, chk := p.call(http.MethodPost, "/api/mini/v1/payments/"+id+"/check", tok, map[string]any{}); code != 200 || chk["status"] != "confirming" || chk["provider"] != "manual_card" {
		t.Errorf("check: %d %v", code, chk)
	}

	// A second submission is turned away without sending anything.
	if code, out := p.receipt(tok, id, "112233", img); code != 409 || out["error"] != "already_submitted" {
		t.Errorf("second submission: %d %v", code, out)
	}
	if len(p.bot.sent()) != 1 || len(p.bot.alerted()) != 1 || p.model.intent(id).reference != "998877" {
		t.Errorf("a second submission changed something: %d sent", len(p.bot.sent()))
	}
	// Once approved, still not open to another proof.
	p.model.setStatus(id, "succeeded")
	if code, out := p.receipt(tok, id, "112233", img); code != 409 || out["error"] != "already_submitted" {
		t.Errorf("proof for a paid payment: %d %v", code, out)
	}
	// A rejected payment is closed.
	p.model.setStatus(id, "failed")
	if code, out := p.receipt(tok, id, "112233", img); code != 409 || out["error"] != "payment_closed" {
		t.Errorf("proof for a rejected payment: %d %v", code, out)
	}
	if len(p.bot.sent()) != 1 {
		t.Errorf("a closed payment was sent a receipt")
	}
}

func TestMiniAppReceiptReference(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:79:jon:en")
	img := pngReceipt(t, 40, 30)

	// A card transfer needs the bank's number; a bad one is refused before anything is sent.
	card, _ := p.payOK(tok, p.buy(tok, plan, "refkey1234"), "manual_card")
	for _, ref := range []string{"", "   "} {
		if code, out := p.receipt(tok, card, ref, img); code != 400 || out["error"] != "reference_required" {
			t.Errorf("reference %q: %d %v", ref, code, out)
		}
	}
	for name, ref := range map[string]string{"a new line": "12\n34", "a control character": "12\x0034", "too long": strings.Repeat("9", 129)} {
		if code, out := p.receipt(tok, card, ref, img); code != 400 || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if len(p.bot.sent()) != 0 || p.model.intent(card).Status != "pending" {
		t.Fatalf("a refused reference sent a receipt: %d", len(p.bot.sent()))
	}
	// Persian digits and the longest reference are fine.
	if code, out := p.receipt(tok, card, strings.Repeat("۹", 128), img); code != 200 {
		t.Errorf("a long Persian reference: %d %v", code, out)
	}

	// For the Zarinpal link and for crypto the screenshot is the proof.
	link, _ := p.payOK(tok, p.buy(tok, plan, "refkey1235"), "manual_zarinpal")
	if code, out := p.receipt(tok, link, "", img); code != 200 {
		t.Errorf("payment link without a number: %d %v", code, out)
	}
	crypto, _ := p.payOK(tok, p.buy(tok, plan, "refkey1236"), "manual_crypto")
	if code, out := p.receipt(tok, crypto, "", img); code != 200 {
		t.Errorf("crypto screenshot: %d %v", code, out)
	}
	if in := p.model.intent(crypto); in.receipt == "" || in.txid != "" || in.reference != "" {
		t.Errorf("crypto proof: %+v", in)
	}
}

func TestMiniAppReceiptUploadChecks(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:80:kim:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "upkey12345"), "manual_card")
	path := "/api/mini/v1/payments/" + id + "/proof"
	img := pngReceipt(t, 40, 30)
	ref := [][2]string{{"reference", "998877"}}
	send := func(fields [][2]string, files ...formFile) (int, map[string]any) {
		t.Helper()
		body, ctype := form(t, fields, files...)
		code, out, _ := p.send(http.MethodPost, path, tok, ctype, body)
		return code, out
	}
	file := func(name, ctype string, data []byte) formFile { return formFile{"receipt", name, ctype, data} }

	// What is not a JPEG, PNG or WebP image is refused, whatever its name and type claim.
	for name, data := range map[string][]byte{
		"html":       []byte("<!doctype html><script>alert(1)</script>"),
		"svg":        []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`),
		"pdf":        []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n"),
		"gif":        []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"text":       []byte("a receipt, honest"),
		"png header": []byte("\x89PNG\r\n\x1a\ntruncated"),
	} {
		for _, claim := range []struct{ name, ctype string }{{"receipt.png", "image/png"}, {"receipt.jpg", "image/jpeg"}, {"receipt.html", "text/html"}, {"x", ""}} {
			if code, out := send(ref, file(claim.name, claim.ctype, data)); code != 415 || out["error"] != "bad_type" {
				t.Errorf("%s as %q (%s): %d %v", name, claim.name, claim.ctype, code, out)
			}
		}
	}
	// A real image under a wrong name and type is fine: the bytes decide.
	if code, out := send(ref, file("notes.txt", "text/plain", img)); code != 200 {
		t.Errorf("a PNG called notes.txt: %d %v", code, out)
	}
	if got := p.bot.sent(); len(got) != 1 || got[0].ContentType != "image/png" {
		t.Errorf("the bot was told the type %v", got)
	}
}

func TestMiniAppReceiptSizeAndShape(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:81:lee:en")
	newCard := func(key string) string {
		id, _ := p.payOK(tok, p.buy(tok, plan, key), "manual_card")
		return id
	}
	post := func(id string, body []byte, ctype string) (int, map[string]any) {
		t.Helper()
		code, out, _ := p.send(http.MethodPost, "/api/mini/v1/payments/"+id+"/proof", tok, ctype, body)
		return code, out
	}
	img := pngReceipt(t, 40, 30)
	ref := [][2]string{{"reference", "998877"}}
	id := newCard("shapekey01")

	// Empty, missing, doubled, or with something the call does not take.
	body, ctype := form(t, ref, formFile{"receipt", "r.png", "image/png", nil})
	if code, out := post(id, body, ctype); code != 400 || out["error"] != "empty" {
		t.Errorf("an empty file: %d %v", code, out)
	}
	body, ctype = form(t, ref)
	if code, out := post(id, body, ctype); code != 400 || out["error"] != "no_receipt" {
		t.Errorf("no file: %d %v", code, out)
	}
	body, ctype = form(t, ref, formFile{"receipt", "a.png", "image/png", img}, formFile{"receipt", "b.png", "image/png", img})
	if code, out := post(id, body, ctype); code != 400 || out["error"] != "one_receipt" {
		t.Errorf("two receipts: %d %v", code, out)
	}
	body, ctype = form(t, [][2]string{{"reference", "1"}, {"reference", "2"}}, formFile{"receipt", "a.png", "image/png", img})
	if code, out := post(id, body, ctype); code != 400 || out["error"] != "invalid" {
		t.Errorf("two references: %d %v", code, out)
	}
	body, ctype = form(t, [][2]string{{"reference", "1"}, {"amount", "1"}}, formFile{"receipt", "a.png", "image/png", img})
	if code, out := post(id, body, ctype); code != 400 || out["error"] != "invalid" {
		t.Errorf("an amount in the form: %d %v", code, out)
	}
	body, ctype = form(t, ref, formFile{"photo", "a.png", "image/png", img})
	if code, out := post(id, body, ctype); code != 400 || out["error"] != "invalid" {
		t.Errorf("a file of another name: %d %v", code, out)
	}
	if code, out := post(id, []byte("not a form"), "text/plain"); code != 400 || out["error"] != "invalid" {
		t.Errorf("a body of another type: %d %v", code, out)
	}
	if code, out := post(id, []byte("not a form"), "multipart/form-data; boundary=zz"); code != 400 {
		t.Errorf("a broken form: %d %v", code, out)
	}
	if len(p.bot.sent()) != 0 {
		t.Fatalf("something was sent for a refused upload: %d", len(p.bot.sent()))
	}

	// The limit: exactly 8 MB is taken; one byte more is not.
	pad := func(n int) []byte {
		w := webpHeader()
		return append(w, make([]byte, n-len(w))...)
	}
	body, ctype = form(t, ref, formFile{"receipt", "r.webp", "image/webp", pad(receipt.MaxBytes + 1)})
	if code, out := post(id, body, ctype); code != 413 || out["error"] != "too_large" {
		t.Errorf("one byte over: %d %v", code, out)
	}
	// A request larger than any the call reads is refused by its Content-Length at
	// once, and one that hides its size is cut off at the limit: both without a network.
	body, ctype = form(t, ref, formFile{"receipt", "r.webp", "image/webp", pad(receipt.MaxBytes + 1<<20)})
	if code, out := p.direct("/api/mini/v1/payments/"+id+"/proof", tok, ctype, bytes.NewReader(body), int64(len(body))); code != 413 || out["error"] != "too_large" {
		t.Errorf("a megabyte over, with its length: %d %v", code, out)
	}
	body, ctype = form(t, ref, formFile{"receipt", "r.webp", "image/webp", pad(receipt.MaxBytes + 3*miniUploadBody)})
	if code, out := p.direct("/api/mini/v1/payments/"+id+"/proof", tok, ctype, bytes.NewReader(body), -1); code != 413 || out["error"] != "too_large" {
		t.Errorf("far over, hiding its length: %d %v", code, out)
	}
	if len(p.bot.sent()) != 0 {
		t.Fatalf("a receipt over the limit was sent: %d", len(p.bot.sent()))
	}
	body, ctype = form(t, ref, formFile{"receipt", "r.webp", "image/webp", pad(receipt.MaxBytes)})
	if code, out := post(id, body, ctype); code != 200 {
		t.Errorf("exactly the limit: %d %v", code, out)
	}
	if got := p.bot.sent(); len(got) != 1 || len(got[0].Data) != receipt.MaxBytes || got[0].ContentType != "image/webp" {
		t.Errorf("the bot was given %d receipts", len(got))
	}
}

// webpHeader is the first 36 bytes of a lossy WebP of 320 x 240 pixels: padded
// with zeros to any length it is still taken for a WebP (its RIFF size says the
// file is 44 bytes long, which no longer file is shorter than).
func webpHeader() []byte {
	b := []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x10\x00\x00\x00")
	b = append(b, 0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0x40, 0x01, 0xf0, 0x00) // the frame tag, the start code, 320, 240
	return append(b, make([]byte, 6)...)
}

func TestMiniAppReceiptWithoutABotOrTrouble(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:82:max:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "troublekey1"), "manual_card")
	img := pngReceipt(t, 40, 30)

	// The bot says what is wrong, and the payment waits for a proof still.
	for name, c := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"customer has not started the bot": {botfiles.ErrChatClosed, 409, "chat_closed"},
		"bot found it too large":           {botfiles.ErrReceiptTooLarge, 413, "too_large"},
		"bot found it is not an image":     {botfiles.ErrReceiptType, 415, "bad_type"},
		"bot busy":                         {botfiles.ErrBotBusy, 503, "busy"},
		"bot down":                         {fmt.Errorf("botfiles: receipt: the bot answered 502"), 502, "bot_unavailable"},
	} {
		p.bot.sendErr = c.err
		if code, out := p.receipt(tok, id, "998877", img); code != c.status || out["error"] != c.code {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	p.bot.sendErr = nil
	if in := p.model.intent(id); in.Status != "pending" || in.receipt != "" {
		t.Fatalf("a failed upload changed the payment: %+v", in)
	}
	if len(p.bot.alerted()) != 0 {
		t.Fatalf("staff alerted to a proof that was never recorded")
	}

	// No bot at all.
	p.api.cfg.MiniAppBot = nil
	if code, out := p.receipt(tok, id, "998877", img); code != 502 || out["error"] != "bot_unavailable" {
		t.Errorf("no bot: %d %v", code, out)
	}
	p.api.cfg.MiniAppBot = p.bot

	// The alert failing does not undo the proof: it waits in the review list.
	p.bot.alertErr = fmt.Errorf("botfiles: proof alert: the bot answered 500")
	if code, out := p.receipt(tok, id, "998877", img); code != 200 || out["status"] != "confirming" {
		t.Fatalf("proof with the alert failing: %d %v", code, out)
	}
	if !strings.Contains(p.logs.String(), "review list") {
		t.Errorf("the failed alert was not logged:\n%s", p.logs)
	}
}

// ---- the proof: a transaction id ----

func TestMiniAppTransactionIDProof(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:83:nia:en")
	crypto := func(key string) string {
		id, _ := p.payOK(tok, p.buy(tok, plan, key), "manual_crypto")
		return id
	}

	// A TRON hash, named or not; spaces typed around it do not matter.
	a := crypto("txkey123456")
	if code, out := p.txid(tok, a, map[string]any{"txid": " " + hash64[:32] + " " + hash64[32:] + " ", "network": "trc20"}); code != 200 || out["status"] != "confirming" {
		t.Fatalf("TRC20: %d %v", code, out)
	}
	if in := p.model.intent(a); in.txid != hash64 || in.network != "TRC20" || in.receipt != "" {
		t.Fatalf("stored: %+v", in)
	}
	if got := p.bot.alerted(); len(got) != 1 || got[0].IntentID != a || got[0].Amount != 150_000 {
		t.Fatalf("alerts: %+v", got)
	}
	if len(p.bot.sent()) != 0 {
		t.Fatalf("a transaction id sends no receipt")
	}
	// An Ethereum hash with its 0x is ERC20 when no network is named.
	b := crypto("txkey123457")
	if code, out := p.txid(tok, b, map[string]any{"txid": "0x" + hash64b}); code != 200 {
		t.Fatalf("ERC20: %d %v", code, out)
	}
	if in := p.model.intent(b); in.txid != "0x"+hash64b || in.network != "ERC20" {
		t.Fatalf("stored: %+v", in)
	}
	// The same transaction cannot pay twice (with or without the 0x).
	c := crypto("txkey123458")
	if code, out := p.txid(tok, c, map[string]any{"txid": hash64b, "network": "ERC20"}); code != 409 || out["error"] != "duplicate" {
		t.Errorf("a transaction used twice: %d %v", code, out)
	}
	// Not a hash, a network the store does not take, or a hash of another network.
	for name, body := range map[string]map[string]any{
		"short":                   {"txid": hash64[:63]},
		"long":                    {"txid": hash64 + "0"},
		"not hex":                 {"txid": strings.Repeat("g", 64)},
		"empty":                   {"txid": ""},
		"two words":               {"txid": hash64 + " " + hash64},
		"a 0x hash on TRC20":      {"txid": "0x" + hash64, "network": "TRC20"},
		"a network we do not use": {"txid": hash64, "network": "BTC"},
	} {
		if code, out := p.txid(tok, c, body); code != 400 {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, _ := p.txid(tok, c, map[string]any{"txid": hash64, "amount": 1}); code != 400 {
		t.Errorf("an unknown field: %d", code)
	}
	if p.model.intent(c).Status != "pending" {
		t.Errorf("a refused hash changed the payment")
	}
	// The store has no TRC20 address: the hash is ERC20 whatever it looks like, and TRC20 is refused.
	p.setting("payments.usdt_trc20", "")
	if code, out := p.txid(tok, c, map[string]any{"txid": hash64c, "network": "TRC20"}); code != 400 || out["error"] != "invalid" {
		t.Errorf("TRC20 without an address: %d %v", code, out)
	}
	if code, out := p.txid(tok, c, map[string]any{"txid": hash64c}); code != 200 {
		t.Errorf("a hash with only an ERC20 address: %d %v", code, out)
	}
	if in := p.model.intent(c); in.network != "ERC20" {
		t.Errorf("network %q", in.network)
	}

	// A transaction id is the proof of a crypto payment only.
	card, _ := p.payOK(tok, p.buy(tok, plan, "txkey123459"), "manual_card")
	if code, out := p.txid(tok, card, map[string]any{"txid": hash64}); code != 400 || out["error"] != "invalid" {
		t.Errorf("a transaction id for a card payment: %d %v", code, out)
	}
	// Large JSON is not read.
	d := crypto("txkey123460")
	big := map[string]any{"txid": hash64, "network": strings.Repeat("x", 20<<10)}
	if code, _ := p.txid(tok, d, big); code != 400 && code != 413 {
		t.Errorf("a large JSON body: %d", code)
	}
}

// ---- whose payment it is ----

func TestMiniAppProofOnlyForTheCustomersOwnPayment(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	alice, bob := p.signIn("tg:84:alice:en"), p.signIn("tg:85:bob:en")
	card, _ := p.payOK(alice, p.buy(alice, plan, "ownkey12345"), "manual_card")
	crypto, _ := p.payOK(alice, p.buy(alice, plan, "ownkey12346"), "manual_crypto")
	img := pngReceipt(t, 40, 30)

	for _, c := range []struct{ name, id string }{
		{"alice's card payment", card}, {"alice's crypto payment", crypto},
		{"no such payment", "00000000-0000-4000-8000-00000000ffff"}, {"not an id", "not-an-id"}, {"an id of the wrong shape", "123"},
	} {
		if code, out := p.receipt(bob, c.id, "998877", img); code != 404 || out["error"] != "not_found" {
			t.Errorf("a receipt for %s: %d %v", c.name, code, out)
		}
		if code, out := p.txid(bob, c.id, map[string]any{"txid": hash64}); code != 404 {
			t.Errorf("a transaction id for %s: %d %v", c.name, code, out)
		}
		if code, out := p.call(http.MethodPost, "/api/mini/v1/payments/"+c.id+"/check", bob, map[string]any{}); code != 404 {
			t.Errorf("a check of %s: %d %v", c.name, code, out)
		}
	}
	// Nothing was sent for them, no proof reached the payments service, and alice's payments are as they were.
	if len(p.bot.sent()) != 0 || len(p.bot.alerted()) != 0 {
		t.Fatalf("something was sent for payments that are not bob's: %d %d", len(p.bot.sent()), len(p.bot.alerted()))
	}
	if n := p.model.calls("SubmitReceipt") + p.model.calls("SubmitTXID"); n != 0 {
		t.Fatalf("%d proofs of payments that are not bob's reached the payments service", n)
	}
	for _, id := range []string{card, crypto} {
		if in := p.model.intent(id); in.Status != "pending" || in.receipt != "" || in.txid != "" {
			t.Errorf("alice's payment changed: %+v", in)
		}
	}
	// Alice can send hers.
	if code, out := p.receipt(alice, card, "998877", img); code != 200 {
		t.Errorf("alice's own: %d %v", code, out)
	}
	// And a payment that takes no receipt (one paid online) is not a place for one.
	zp, _ := p.payOK(alice, p.buy(alice, plan, "ownkey12347"), "zarinpal")
	if code, out := p.receipt(alice, zp, "998877", img); code != 400 || out["error"] != "invalid" {
		t.Errorf("a receipt for an online payment: %d %v", code, out)
	}
	stars, _ := p.payOK(alice, p.buy(alice, plan, "ownkey12348"), "stars")
	if code, out := p.receipt(alice, stars, "998877", img); code != 400 || out["error"] != "invalid" {
		t.Errorf("a receipt for a Stars payment: %d %v", code, out)
	}
}

func TestMiniAppProofNeedsASession(t *testing.T) {
	p := newPayFix(t)
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:86:cat:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "sessionkey1"), "manual_card")
	body, ctype := form(t, [][2]string{{"reference", "1"}}, formFile{"receipt", "r.png", "image/png", pngReceipt(t, 4, 4)})
	for _, token := range []string{"", "not-a-token"} {
		if code, _, _ := p.send(http.MethodPost, "/api/mini/v1/payments/"+id+"/proof", token, ctype, body); code != 401 {
			t.Errorf("token %q: %d", token, code)
		}
	}
	if code, _, _ := p.send(http.MethodGet, "/api/mini/v1/payments/"+id+"/proof", tok, "", nil); code != 404 && code != 405 {
		t.Errorf("GET: %d", code)
	}
	// Banned meanwhile.
	u, _ := p.st.GetUserByTelegramID(context.Background(), p.st.Conn(), 86)
	p.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, u.ID)
	if code, out, _ := p.send(http.MethodPost, "/api/mini/v1/payments/"+id+"/proof", tok, ctype, body); code != 403 || out["error"] != "banned" {
		t.Errorf("banned: %d %v", code, out)
	}
	if len(p.bot.sent()) != 0 {
		t.Errorf("a receipt was sent without a session")
	}
}

// ---- the payments service and the store being away ----

func TestMiniAppPaymentsServiceUnavailable(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:87:dan:en")
	order := p.buy(tok, plan, "awaykey1234")
	id, _ := p.payOK(tok, order, "manual_card")
	img := pngReceipt(t, 40, 30)

	p.model.down = true
	for _, method := range []string{"stars", "manual_card", "manual_crypto", "manual_zarinpal"} {
		if code, out := p.pay(tok, order, method); code != 503 || out["error"] != "unavailable" {
			t.Errorf("pay by %s: %d %v", method, code, out)
		}
	}
	if code, out := p.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 200_000, "method": "manual_card", "key": "awaytopup1"}); code != 503 || out["error"] != "unavailable" {
		t.Errorf("top-up: %d %v", code, out)
	}
	if code, out := p.receipt(tok, id, "998877", img); code != 503 || out["error"] != "unavailable" {
		t.Errorf("receipt: %d %v", code, out)
	}
	if code, out := p.txid(tok, id, map[string]any{"txid": hash64}); code != 503 {
		t.Errorf("transaction id: %d %v", code, out)
	}
	if len(p.bot.sent()) != 0 || len(p.bot.linksMade()) != 0 {
		t.Errorf("the bot was asked for something while payments was away")
	}

	// It answers the pre-check and goes away before the proof is stored: the receipt
	// was sent, the proof is not stored, and the customer may try again.
	p.model.down = false
	p.model.submitErr = status.Error(codes.Unavailable, "payments is not reachable")
	if code, out := p.receipt(tok, id, "998877", img); code != 503 || out["error"] != "unavailable" {
		t.Errorf("payments away at the last step: %d %v", code, out)
	}
	if in := p.model.intent(id); in.Status != "pending" || len(p.bot.alerted()) != 0 {
		t.Errorf("a proof that failed is stored or alerted: %+v", in)
	}
	if code, out := p.receipt(tok, id, "998877", img); code != 200 {
		t.Errorf("trying again: %d %v", code, out)
	}

	// No payments service connected at all.
	p.api.cfg.Payments = nil
	if code, out := p.pay(tok, order, "manual_card"); code != 503 || out["error"] != "unavailable" {
		t.Errorf("pay without payments: %d %v", code, out)
	}
	if code, out := p.receipt(tok, id, "998877", img); code != 503 || out["error"] != "unavailable" {
		t.Errorf("receipt without payments: %d %v", code, out)
	}
}

func TestMiniAppPaymentsInMaintenance(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:88:eve:en")
	order := p.buy(tok, plan, "maintkey123")
	card, _ := p.payOK(tok, order, "manual_card")
	crypto, _ := p.payOK(tok, p.buy(tok, plan, "maintkey124"), "manual_crypto")
	stars, _ := p.payOK(tok, order, "stars")
	img := pngReceipt(t, 40, 30)

	p.setting("maintenance.enabled", "true")
	// Nothing new starts.
	if code, out := p.call(http.MethodGet, "/api/mini/v1/methods?currency=IRT&amount=150000", tok, nil); code != 503 || out["error"] != "maintenance" {
		t.Errorf("methods: %d %v", code, out)
	}
	for _, method := range []string{"stars", "manual_card", "manual_crypto", "manual_zarinpal"} {
		if code, out := p.pay(tok, order, method); code != 503 || out["error"] != "maintenance" {
			t.Errorf("pay by %s: %d %v", method, code, out)
		}
	}
	if code, out := p.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 200_000, "method": "stars", "key": "maint-topup-key"}); code != 503 || out["error"] != "maintenance" {
		t.Errorf("top-up: %d %v", code, out)
	}
	// A payment already started is still seen (a Stars payment in flight finishes).
	for _, id := range []string{card, stars} {
		if code, out := p.call(http.MethodPost, "/api/mini/v1/payments/"+id+"/check", tok, map[string]any{}); code != 200 {
			t.Errorf("check in maintenance: %d %v", code, out)
		}
	}
	// And so is its proof: a customer who has paid by hand must be able to say so, as the bot lets a
	// payment under way finish. The bot is asked, and the staff alerted, as ever.
	if code, out := p.receipt(tok, card, "998877", img); code != 200 || out["status"] != "confirming" {
		t.Errorf("receipt in maintenance: %d %v", code, out)
	}
	if code, out := p.txid(tok, crypto, map[string]any{"txid": hash64}); code != 200 || out["status"] != "confirming" {
		t.Errorf("transaction id in maintenance: %d %v", code, out)
	}
	if len(p.bot.sent()) != 1 || len(p.bot.alerted()) != 2 {
		t.Errorf("%d receipts sent and %d alerts in maintenance", len(p.bot.sent()), len(p.bot.alerted()))
	}
	// Still once.
	if code, out := p.receipt(tok, card, "998877", img); code != 409 || out["error"] != "already_submitted" {
		t.Errorf("a second receipt in maintenance: %d %v", code, out)
	}
	// The store opens again.
	p.setting("maintenance.enabled", "false")
	if code, out := p.pay(tok, order, "manual_zarinpal"); code != 200 {
		t.Errorf("after maintenance: %d %v", code, out)
	}
}

// ---- limits ----

func TestMiniAppReceiptsHaveALimitOfTheirOwn(t *testing.T) {
	p := newPayFix(t)
	p.api.mini.write = newLimiter(100_000, time.Minute) // only the receipt limit is in question
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:89:fox:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "limitkey123"), "manual_card")
	// A tidy customer sends one receipt; a script sends many. Refused uploads count too.
	bad := []byte("not an image")
	var last int
	var n int
	for n = 1; n <= miniUploadRate+3; n++ {
		body, ctype := form(t, [][2]string{{"reference", "1"}}, formFile{"receipt", "r.png", "image/png", bad})
		code, _, hdr := p.send(http.MethodPost, "/api/mini/v1/payments/"+id+"/proof", tok, ctype, body)
		if code == 429 {
			if hdr.Get("Retry-After") == "" {
				t.Error("no Retry-After on the limit")
			}
			last = n
			break
		}
		if code != 415 {
			t.Fatalf("upload %d: %d", n, code)
		}
	}
	if last != miniUploadRate+1 {
		t.Fatalf("limited at upload %d, want %d", last, miniUploadRate+1)
	}
	// Another customer is not held up by it, and a JSON proof is not a receipt.
	other := p.signIn("tg:90:gil:en")
	plan2 := p.plan("Gold", 30, 50<<30, 150_000)
	id2, _ := p.payOK(other, p.buy(other, plan2, "limitkey124"), "manual_card")
	body, ctype := form(t, [][2]string{{"reference", "1"}}, formFile{"receipt", "r.png", "image/png", pngReceipt(t, 4, 4)})
	if code, _, _ := p.send(http.MethodPost, "/api/mini/v1/payments/"+id2+"/proof", other, ctype, body); code != 200 {
		t.Errorf("another customer: %d", code)
	}
	crypto, _ := p.payOK(tok, p.buy(tok, plan, "limitkey125"), "manual_crypto")
	if code, out := p.txid(tok, crypto, map[string]any{"txid": hash64}); code != 200 {
		t.Errorf("a transaction id while the receipt limit is reached: %d %v", code, out)
	}
}

func TestMiniAppProofsAreSharedWithTheWriteLimit(t *testing.T) {
	p := newPayFix(t)
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:91:hue:en")
	order := p.buy(tok, plan, "writekey123")
	id, _ := p.payOK(tok, order, "manual_crypto")
	var limited bool
	for i := 0; i < miniWriteRate+5 && !limited; i++ {
		code, _ := p.txid(tok, id, map[string]any{"txid": "nothex"})
		limited = code == 429
	}
	if !limited {
		t.Error("the shared write limit does not apply to proofs")
	}
}

func TestMiniAppOneProofAtATime(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:92:ian:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "oncekey1234"), "manual_card")
	img := pngReceipt(t, 40, 30)
	p.bot.hold, p.bot.started = make(chan struct{}), make(chan struct{}, 2)

	first := make(chan [2]any, 1)
	go func() {
		code, out := p.receipt(tok, id, "998877", img)
		first <- [2]any{code, out}
	}()
	select {
	case <-p.bot.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first receipt never reached the bot")
	}
	// A double tap while the first is on its way, also with the id in capitals
	// (the database takes either, so it is the same payment).
	if code, out := p.receipt(tok, id, "998877", img); code != 409 || out["error"] != "busy" {
		t.Errorf("the second at once: %d %v", code, out)
	}
	caps := make(chan [2]any, 1)
	go func() {
		code, out := p.receipt(tok, strings.ToUpper(id), "998877", img)
		caps <- [2]any{code, out}
	}()
	select {
	case r := <-caps:
		if out, _ := r[1].(map[string]any); r[0] != 409 || out["error"] != "busy" {
			t.Errorf("the second at once, the id in capitals: %v", r)
		}
	case <-p.bot.started:
		t.Error("the same payment with its id in capitals got past the one-at-a-time guard to the bot")
	case <-time.After(10 * time.Second):
		t.Error("the second, the id in capitals, was neither turned away nor sent")
	}
	close(p.bot.hold)
	select {
	case r := <-first:
		if r[0] != 200 {
			t.Errorf("the first: %v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first receipt never finished")
	}
	if len(p.bot.sent()) != 1 || len(p.bot.alerted()) != 1 {
		t.Errorf("%d receipts sent, %d alerts", len(p.bot.sent()), len(p.bot.alerted()))
	}
	if code, out := p.receipt(tok, id, "998877", img); code != 409 || out["error"] != "already_submitted" {
		t.Errorf("after: %d %v", code, out)
	}
}

func TestMiniAppReceiptsAreCheckedFewAtATime(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	p.api.mini.uploads = make(chan struct{}, 1)
	p.api.mini.upload = newLimiter(1, miniUploadWindow) // one receipt each: a turn-away that counted would be seen
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	jay, kit := p.signIn("tg:93:jay:en"), p.signIn("tg:95:kit:en")
	a, _ := p.payOK(jay, p.buy(jay, plan, "fewkey12345"), "manual_card")
	b, _ := p.payOK(kit, p.buy(kit, plan, "fewkey12346"), "manual_card")
	img := pngReceipt(t, 40, 30)
	p.bot.hold, p.bot.started = make(chan struct{}), make(chan struct{}, 2)

	done := make(chan int, 1)
	go func() {
		code, _ := p.receipt(jay, a, "998877", img)
		done <- code
	}()
	select {
	case <-p.bot.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first receipt never reached the bot")
	}
	// No room for kit's, however often they try: being turned away for lack of room uses none of their allowance.
	for i := 0; i < 4; i++ {
		if code, out := p.receipt(kit, b, "998877", img); code != 503 || out["error"] != "busy" {
			t.Errorf("one too many (try %d): %d %v", i+1, code, out)
		}
	}
	close(p.bot.hold)
	if code := <-done; code != 200 {
		t.Errorf("the first: %d", code)
	}
	if code, out := p.receipt(kit, b, "998877", img); code != 200 {
		t.Errorf("when there is room: %d %v", code, out)
	}
	// Their one receipt is used now, and that is the allowance speaking.
	c, _ := p.payOK(kit, p.buy(kit, plan, "fewkey12347"), "manual_card")
	if code, out := p.receipt(kit, c, "998877", img); code != 429 || out["error"] != "rate_limited" {
		t.Errorf("past the allowance: %d %v", code, out)
	}
}

// One account must not be able to hold every place: a customer has one receipt
// on its way at a time, and another customer is not held up by it.
func TestMiniAppACustomerHasOneReceiptOnItsWayAtATime(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	rae, sol := p.signIn("tg:96:rae:en"), p.signIn("tg:97:sol:en")
	a, _ := p.payOK(rae, p.buy(rae, plan, "onekey12345"), "manual_card")
	b, _ := p.payOK(rae, p.buy(rae, plan, "onekey12346"), "manual_card")
	c, _ := p.payOK(sol, p.buy(sol, plan, "onekey12347"), "manual_card")
	img := pngReceipt(t, 40, 30)
	p.bot.hold, p.bot.started = make(chan struct{}), make(chan struct{}, 4)

	first := make(chan int, 1)
	go func() {
		code, _ := p.receipt(rae, a, "998877", img)
		first <- code
	}()
	select {
	case <-p.bot.started:
	case <-time.After(10 * time.Second):
		t.Fatal("rae's first receipt never reached the bot")
	}
	// Rae's second, for another payment, waits for the first: it does not even reach the bot. (Asked on the
	// side: where there is no such rule it would sit at the bot with the first, and the test must say so.)
	second := make(chan [2]any, 1)
	go func() {
		code, out := p.receipt(rae, b, "998877", img)
		second <- [2]any{code, out}
	}()
	select {
	case r := <-second:
		if out, _ := r[1].(map[string]any); r[0] != 409 || out["error"] != "busy" {
			t.Errorf("rae's second at once: %v", r)
		}
	case <-p.bot.started:
		t.Error("rae's second receipt got to the bot while the first was still on its way")
	case <-time.After(10 * time.Second):
		t.Error("rae's second was neither turned away nor sent")
	}
	// Sol is not held up.
	other := make(chan int, 1)
	go func() {
		code, _ := p.receipt(sol, c, "998877", img)
		other <- code
	}()
	select {
	case <-p.bot.started:
	case <-time.After(10 * time.Second):
		t.Fatal("sol's receipt never reached the bot")
	}
	close(p.bot.hold)
	for name, ch := range map[string]chan int{"rae's first": first, "sol's": other} {
		select {
		case code := <-ch:
			if code != 200 {
				t.Errorf("%s: %d", name, code)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s never finished", name)
		}
	}
	if len(p.bot.sent()) != 2 {
		t.Errorf("%d receipts reached the bot, want rae's first and sol's", len(p.bot.sent()))
	}
	if code, out := p.receipt(rae, b, "998877", img); code != 200 {
		t.Errorf("rae's second once the first is done: %d %v", code, out)
	}
}

// Payments takes a bank reference of 256 bytes, whatever the characters; core
// says no before the receipt has gone to Telegram, not after.
func TestMiniAppReferenceIsLimitedInBytesAsPaymentsDoes(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:99:ash:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "refkey123456"), "manual_card")
	img := pngReceipt(t, 40, 30)

	tooLong := strings.Repeat("😀", 65) // 65 characters, 260 bytes
	if code, out := p.receipt(tok, id, tooLong, img); code != 400 || out["error"] != "invalid" {
		t.Errorf("a reference of 260 bytes: %d %v", code, out)
	}
	if len(p.bot.sent()) != 0 || p.model.calls("SubmitReceipt") != 0 {
		t.Fatalf("a receipt was sent for a reference that payments would have refused: %d %d", len(p.bot.sent()), p.model.calls("SubmitReceipt"))
	}
	if code, out := p.receipt(tok, id, strings.Repeat("😀", 64), img); code != 200 { // exactly 256 bytes
		t.Errorf("a reference of 256 bytes: %d %v", code, out)
	}
}

// What payments finds wrong with a proof is its own wording; the customer gets ours.
func TestMiniAppProofRefusedByPaymentsIsNotRepeated(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:100:bea:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "wordkey12345"), "manual_card")
	p.model.submitErr = status.Error(codes.InvalidArgument, "payments: invalid argument: receipt details too long")
	code, out := p.receipt(tok, id, "998877", pngReceipt(t, 40, 30))
	if code != 400 || out["error"] != "invalid" || strings.Contains(fmt.Sprint(out["message"]), "payments") {
		t.Errorf("a proof payments refused: %d %v", code, out)
	}
}

// A payment id is a UUID, in either case; anything else is no payment of the
// customer's and payments is not asked (it answers an internal error to one).
func TestMiniAppCheckRefusesWhatIsNotAPaymentID(t *testing.T) {
	p := newPayFix(t)
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:101:cyd:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "malkey123456"), "manual_card")
	before := p.model.calls("CheckIntent")
	for _, bad := range []string{"not-a-uuid", "1", "%00", strings.Repeat("a", 65), "0000000a-0000-4000-8000-00000000000g", "0000000a-0000-4000-8000-0000000000011"} {
		if code, out := p.call(http.MethodPost, "/api/mini/v1/payments/"+bad+"/check", tok, map[string]any{}); code != 404 || out["error"] != "not_found" {
			t.Errorf("a check of %q: %d %v", bad, code, out)
		}
	}
	if got := p.model.calls("CheckIntent"); got != before {
		t.Errorf("payments was asked about %d ids that are not payment ids", got-before)
	}
	if code, out := p.call(http.MethodPost, "/api/mini/v1/payments/"+strings.ToUpper(id)+"/check", tok, map[string]any{}); code != 200 || out["intent_id"] != id {
		t.Errorf("the id in capitals: %d %v", code, out)
	}
}

// ---- what is kept and what is logged ----

func TestMiniAppReceiptNeverReachesLogsOrAudit(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:94:kay:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "privkey1234"), "manual_card")
	img := pngReceipt(t, 40, 30)

	// A receipt that succeeds, one that fails at the bot, one the payments service refuses, an alert that fails.
	p.bot.sendErr = fmt.Errorf("botfiles: receipt: the bot answered 502")
	p.receipt(tok, id, "ref-secret-1", img)
	p.bot.sendErr = nil
	p.model.submitErr = status.Error(codes.Internal, "boom")
	p.receipt(tok, id, "ref-secret-2", img)
	p.bot.alertErr = fmt.Errorf("botfiles: proof alert: the bot answered 500")
	if code, _ := p.receipt(tok, id, "ref-secret-3", img); code != 200 {
		t.Fatalf("the proof: %d", code)
	}
	fileID := p.model.intent(id).receipt
	if fileID == "" {
		t.Fatal("no file id was stored")
	}
	logs := p.logs.String()
	for _, secret := range []string{fileID, "AgACAgQ", "PNG", "ref-secret", string(img[:16])} {
		if strings.Contains(logs, secret) {
			t.Errorf("the logs hold %q:\n%s", secret, logs)
		}
	}
	// Nor does the audit log say anything about a receipt or a proof.
	var n int
	if err := p.st.DB().QueryRow(context.Background(),
		`SELECT count(*) FROM core.audit_log a WHERE to_jsonb(a)::text ILIKE '%' || $1 || '%' OR to_jsonb(a)::text ILIKE '%receipt%' OR to_jsonb(a)::text ILIKE '%ref-secret%'`, fileID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d audit entries mention the receipt", n)
	}
}

// The payment check is the customer's own, also for payments the app now takes.
func TestMiniAppCheckReportsTheManualPayment(t *testing.T) {
	p := newPayFix(t)
	p.loosen()
	plan := p.plan("Gold", 30, 50<<30, 150_000)
	tok := p.signIn("tg:95:lou:en")
	id, _ := p.payOK(tok, p.buy(tok, plan, "checkkey123"), "manual_card")
	for _, st := range []string{"pending", "confirming", "succeeded", "failed"} {
		p.model.setStatus(id, st)
		if code, out := p.call(http.MethodPost, "/api/mini/v1/payments/"+id+"/check", tok, map[string]any{}); code != 200 || out["status"] != st || out["provider"] != "manual_card" {
			t.Errorf("status %s: %d %v", st, code, out)
		}
	}
}
