package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/handler"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/runner"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/web"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgfake"
)

// The Mini App's Stars and manual payments, end to end: the Mini App's API in
// core, the bot's internal endpoints and its real handler, the real payments
// and provisioning services and a fake Telegram. Only Telegram itself is
// missing (docs/17-mini-app.md lists what a phone has to prove).

const (
	miniBotToken  = "123456:integration-test-bot-token"   //nolint:gosec // test fixture, gitleaks:allow
	miniCoreToken = "core-token-of-the-integration-tests" //nolint:gosec // test fixture, gitleaks:allow
)

type miniWorld struct {
	t     *testing.T
	stack *testenv.Stack
	fake  *tgfake.Server
	bot   *tg.Client
	app   *httptest.Server // the Mini App's API, as core serves it
	plan  *corev1.Plan
}

func newMiniWorld(t *testing.T) *miniWorld {
	t.Helper()
	stack := testenv.Start(t, ownerTG)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	quiet := slog.New(slog.DiscardHandler)

	// Telegram, and the bot as cmd/bot wires it.
	fake := tgfake.New()
	api := httptest.NewServer(fake.Handler())
	t.Cleanup(api.Close)
	bot := tg.New(miniBotToken, tg.WithAPIRoot(api.URL))
	cat := i18n.MustLoad()
	h := handler.New(stack.Core, bot, state.NewMemory(), cat, nil,
		handler.Config{AdminTelegramIDs: []int64{ownerTG}, BotName: "TestVPN", BotUsername: "test_vpn_bot"}, quiet)
	d := runner.NewDispatcher(ctx, 4, h.Handle)
	t.Cleanup(func() {
		cancel()
		stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		d.Stop(stopCtx)
	})
	go func() { _ = runner.Poll(ctx, bot, d, quiet) }()

	mux := http.NewServeMux()
	mux.Handle("POST /internal/miniapp/verify", runner.MiniAppVerify(miniBotToken, "test_vpn_bot", miniCoreToken, nil))
	mux.Handle("POST /internal/miniapp/invoice", runner.MiniAppInvoice(bot, cat, miniCoreToken, quiet))
	mux.Handle("POST /internal/miniapp/receipt", runner.MiniAppReceipt(bot, cat, miniCoreToken, quiet))
	mux.Handle("POST /internal/miniapp/proof-alert", runner.MiniAppProofAlert(func(ctx context.Context, a runner.ProofAlert) {
		h.NotifyProofSubmitted(ctx, handler.ProofNotice{TelegramID: a.TelegramID, Username: a.Username, IntentID: a.IntentID, Amount: a.Amount, Currency: a.Currency})
	}, miniCoreToken))
	botSrv := httptest.NewServer(mux)
	t.Cleanup(botSrv.Close)

	// Core's side of the Mini App.
	peer := botfiles.New(botSrv.URL, miniCoreToken)
	appMux := http.NewServeMux()
	web.New(web.Config{Store: stack.Store, Domain: stack.Domain, Payments: stack.PayClient, Provisioner: stack.Prov,
		MiniAppVerify: peer, MiniAppBot: peer, Log: quiet}).Register(appMux)
	app := httptest.NewServer(appMux)
	t.Cleanup(app.Close)

	w := &miniWorld{t: t, stack: stack, fake: fake, bot: bot, app: app}
	c := stack.Core
	if o, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: ownerTG, Language: "en"}); err != nil || o.GetRole() != "owner" {
		t.Fatalf("owner: %v %+v", err, o)
	}
	if err := h.RefreshSettings(ctx); err != nil { // the staff list, for the alerts
		t.Fatal(err)
	}
	w.set("miniapp.enabled", "true")
	w.set("payments.stars_rate", "1500")
	w.set("payments.card_number", "6037-9911-2222-3333")
	w.set("payments.card_holder", "Ali Rezaei")
	w.set("payments.usdt_trc20", "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r")
	w.set("payments.usdt_rate", "60000")
	plan, err := c.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: ownerTG, Plan: &corev1.Plan{
		NameI18N: map[string]string{"en": "Monthly 50GB", "fa": "ماهانه ۵۰ گیگ"}, Kind: "both",
		DurationDays: 30, TrafficBytes: 50 << 30, Price: &commonv1.Money{Amount: 150000, Currency: "IRT"}, Enabled: true,
	}})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	w.plan = plan
	return w
}

func (w *miniWorld) set(key, value string) {
	w.t.Helper()
	if _, err := w.stack.Core.AdminSetSetting(context.Background(), &corev1.AdminSetSettingRequest{ActorTelegramId: ownerTG, Key: key, Value: value}); err != nil {
		w.t.Fatalf("set %s: %v", key, err)
	}
}

// customer is someone in the Mini App.
type miniCustomer struct {
	w     *miniWorld
	tg    int64
	name  string
	token string
	id    string // core's id
}

// open signs a customer in as Telegram's init data (signed with the bot's token) would.
func (w *miniWorld) open(telegramID int64, username string) *miniCustomer {
	w.t.Helper()
	initData := tgauth.Sign(miniBotToken, tgauth.User{ID: telegramID, Username: username, FirstName: username, LanguageCode: "en"}, time.Now(), nil)
	c := &miniCustomer{w: w, tg: telegramID, name: username}
	code, out := c.do(http.MethodPost, "/api/mini/v1/auth", map[string]any{"init_data": initData})
	if code != 200 || out["token"] == nil {
		w.t.Fatalf("sign in: %d %v", code, out)
	}
	c.token = out["token"].(string)
	u, err := w.stack.Core.GetUser(context.Background(), &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: telegramID}})
	if err != nil {
		w.t.Fatal(err)
	}
	c.id = u.GetId()
	return c
}

func (c *miniCustomer) raw(method, path, ctype string, body []byte) (int, map[string]any) {
	c.w.t.Helper()
	req, _ := http.NewRequest(method, c.w.app.URL+path, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.w.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func (c *miniCustomer) do(method, path string, body any) (int, map[string]any) {
	c.w.t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	return c.raw(method, path, "application/json", raw)
}

func (c *miniCustomer) must(method, path string, body any) map[string]any {
	c.w.t.Helper()
	code, out := c.do(method, path, body)
	if code != 200 {
		c.w.t.Fatalf("%s %s: %d %v", method, path, code, out)
	}
	return out
}

// order makes an order for the plan in the app.
func (c *miniCustomer) order(key string) string {
	c.w.t.Helper()
	return c.must(http.MethodPost, "/api/mini/v1/orders", map[string]any{"plan_id": c.w.plan.GetId(), "key": key})["id"].(string)
}

// waitOrder polls the order the way the app does until it has the status.
func (c *miniCustomer) waitOrder(id, want string) {
	c.w.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		out := c.must(http.MethodGet, "/api/mini/v1/orders/"+id, nil)
		if out["status"] == want {
			return
		}
		if time.Now().After(deadline) {
			c.w.t.Fatalf("order %s is %v, want %s", id, out["status"], want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// sentTo is what the bot sent to a chat, oldest first.
func (w *miniWorld) sentTo(chat int64) []tgfake.Sent {
	var out []tgfake.Sent
	for _, s := range w.fake.Sent() {
		if s.ChatID == chat {
			out = append(out, s)
		}
	}
	return out
}

func (w *miniWorld) eventually(what string, ok func() bool) {
	w.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			w.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pending is the review list as the staff see it.
func (w *miniWorld) pending() []*corev1.PendingPayment {
	w.t.Helper()
	q, err := w.stack.Core.AdminListPendingPayments(context.Background(), &corev1.AdminListPendingPaymentsRequest{ActorTelegramId: ownerTG})
	if err != nil {
		w.t.Fatal(err)
	}
	return q.GetPayments()
}

func (w *miniWorld) approve(intent string) {
	w.t.Helper()
	if _, err := w.stack.Core.ReviewManualPayment(context.Background(), &corev1.ReviewManualPaymentRequest{ActorTelegramId: ownerTG, IntentId: intent, Decision: "approved"}); err != nil {
		w.t.Fatal(err)
	}
}

func receiptPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 90, 60))
	img.Pix[7] = 200
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// upload sends the customer's proof: the bank's number and the receipt image.
func (c *miniCustomer) upload(intent, reference string, img []byte) (int, map[string]any) {
	c.w.t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if reference != "" {
		_ = mw.WriteField("reference", reference)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="receipt"; filename="receipt.png"`)
	h.Set("Content-Type", "image/png")
	pw, _ := mw.CreatePart(h)
	_, _ = pw.Write(img)
	_ = mw.Close()
	return c.raw(http.MethodPost, "/api/mini/v1/payments/"+intent+"/proof", mw.FormDataContentType(), b.Bytes())
}

// ---- Telegram Stars ----

// A Stars payment started in the app is an invoice link; Telegram asks the
// bot about it and reports the payment to it as it does for an invoice message,
// and the bot's own handlers settle it. Nothing of the bot's was ever in the
// customer's chat for it to belong to.
func TestMiniAppStarsSettlesThroughTheBotsHandlers(t *testing.T) {
	w := newMiniWorld(t)
	sam := w.open(4001, "sam")
	order := sam.order("starsflowkey1")

	pay := sam.must(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", map[string]any{"method": "stars"})
	intent, link := pay["intent_id"].(string), pay["invoice_url"].(string)
	if pay["stars"] != float64(100) || pay["amount"] != float64(150000) || pay["status"] != "pending" || !strings.HasPrefix(link, "https://t.me/") {
		t.Fatalf("pay by Stars: %v", pay)
	}
	// The invoice the bot made is the bot's own: the payment's id as the payload, XTR, no provider token.
	links := w.fake.InvoiceLinks()
	if len(links) != 1 || links[0].URL != link || links[0].Payload != intent || links[0].Stars != 100 || links[0].Currency != "XTR" ||
		links[0].ProviderToken != "" || links[0].Title != "Monthly 50GB" || links[0].Description != "Payment of 150,000 Toman" {
		t.Fatalf("invoice links: %+v", links)
	}
	// There is no invoice message and no chat with the customer at all.
	if got := w.sentTo(4001); len(got) != 0 {
		t.Fatalf("the bot wrote to the customer before the payment: %+v", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Someone else cannot pay the customer's invoice: the bot refuses them in the pre-checkout.
	stranger := tg.User{ID: 4099, Username: "eve", FirstName: "Eve"}
	if _, err := w.fake.PayInvoiceLink(ctx, link, stranger); err == nil {
		t.Fatal("another user paid the invoice")
	}
	// The customer pays it.
	charge, err := w.fake.PayInvoiceLink(ctx, link, tg.User{ID: 4001, Username: "sam", FirstName: "sam", LanguageCode: "en"})
	if err != nil || charge == "" {
		t.Fatalf("paying the invoice: %q %v", charge, err)
	}
	// The bot's handlers did their part: yes to the pre-checkout and a message in the customer's chat.
	ok := 0
	for _, s := range w.fake.Sent() {
		if s.Method == "answerPreCheckoutQuery" && s.OK != nil && *s.OK {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d pre-checkouts were accepted", ok)
	}
	w.eventually("the payment received message", func() bool {
		for _, s := range w.sentTo(4001) {
			if s.Method == "sendMessage" && strings.Contains(s.Text, "Payment received") {
				return true
			}
		}
		return false
	})
	// And the app sees it: the payment succeeded, and the service is delivered.
	w.eventually("the payment to succeed", func() bool {
		out := sam.must(http.MethodPost, "/api/mini/v1/payments/"+intent+"/check", map[string]any{})
		return out["status"] == "succeeded"
	})
	sam.waitOrder(order, "active")

	// The same invoice cannot be paid twice: Telegram's pre-checkout is refused.
	if _, err := w.fake.PayInvoiceLink(ctx, link, tg.User{ID: 4001, Username: "sam", LanguageCode: "en"}); err == nil {
		t.Error("a paid invoice was paid again")
	}
	if got := len(w.pending()); got != 0 {
		t.Errorf("%d payments wait for review after a Stars payment", got)
	}
}

// A Stars payment in flight finishes when the store goes into maintenance: the
// app refuses new payments then, but the bot settles what Telegram reports and
// the app can still read the order and the payment.
func TestMiniAppStarsInFlightFinishesInMaintenance(t *testing.T) {
	w := newMiniWorld(t)
	max := w.open(4011, "max")
	order := max.order("maintflowkey1")
	pay := max.must(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", map[string]any{"method": "stars"})
	intent, link := pay["intent_id"].(string), pay["invoice_url"].(string)

	w.set("maintenance.enabled", "true")
	if code, out := max.do(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", map[string]any{"method": "stars"}); code != 503 || out["error"] != "maintenance" {
		t.Fatalf("a new payment in maintenance: %d %v", code, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := w.fake.PayInvoiceLink(ctx, link, tg.User{ID: 4011, Username: "max", LanguageCode: "en"}); err != nil {
		t.Fatalf("paying the invoice in maintenance: %v", err)
	}
	w.eventually("the payment to succeed", func() bool {
		return max.must(http.MethodPost, "/api/mini/v1/payments/"+intent+"/check", map[string]any{})["status"] == "succeeded"
	})
	max.waitOrder(order, "active")
}

// A Stars payment started in one place is the same payment in the other: the
// payment's key is the order's, in the bot and in the app.
func TestMiniAppAndBotShareTheStarsPayment(t *testing.T) {
	w := newMiniWorld(t)
	ctx := context.Background()
	zoe := w.open(4002, "zoe")
	order := zoe.order("sharekey1234")

	// The bot's call for "pay with Stars" on this order, then the app's.
	fromBot, err := w.stack.Core.CreatePaymentIntent(ctx, &corev1.CreatePaymentIntentRequest{UserId: zoe.id, OrderId: order, Provider: "stars", IdempotencyKey: "int:" + order + ":stars"})
	if err != nil {
		t.Fatal(err)
	}
	pay := zoe.must(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", map[string]any{"method": "stars"})
	if pay["intent_id"] != fromBot.GetId() || pay["stars"] != float64(fromBot.GetGatewayAmount().GetAmount()) {
		t.Fatalf("the app made another payment: %v, the bot's is %s", pay, fromBot.GetId())
	}
	// And the other way round, for a method the app started first.
	card := zoe.must(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", map[string]any{"method": "manual_card"})
	fromBotCard, err := w.stack.Core.CreatePaymentIntent(ctx, &corev1.CreatePaymentIntentRequest{UserId: zoe.id, OrderId: order, Provider: "manual_card", IdempotencyKey: "int:" + order + ":manual_card"})
	if err != nil || fromBotCard.GetId() != card["intent_id"] {
		t.Fatalf("the bot made another payment: %v %v, the app's is %v", err, fromBotCard.GetId(), card["intent_id"])
	}
}

// ---- card to card ----

func TestMiniAppCardReceiptReachesTheReviewQueueAndTheAdminCard(t *testing.T) {
	w := newMiniWorld(t)
	tia := w.open(4003, "tia")
	order := tia.order("cardflowkey1")
	pay := tia.must(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", map[string]any{"method": "manual_card"})
	intent := pay["intent_id"].(string)
	d := pay["details"].(map[string]any)
	if d["card_number"] != "6037-9911-2222-3333" || d["card_holder"] != "Ali Rezaei" || pay["amount"] != float64(150000) || d["reference"] == "" {
		t.Fatalf("card instructions: %v", pay)
	}
	ref := d["reference"].(string)
	img := receiptPNG(t)

	// A customer who has not started the bot cannot be written to: the app is told to send them there.
	w.fake.FailChat(4003, tgfake.Failure{Code: 403, Description: "Forbidden: bot can't initiate conversation with a user"})
	if code, out := tia.upload(intent, "998877", img); code != 409 || out["error"] != "chat_closed" {
		t.Fatalf("a customer the bot cannot write to: %d %v", code, out)
	}
	w.fake.ClearChat(4003)
	if got := w.pending(); len(got) != 0 {
		t.Fatalf("a failed upload reached the review queue: %d", len(got))
	}

	// The proof: the bot sends the picture to the customer's chat, with the payment's reference on it.
	code, out := tia.upload(intent, "998877", img)
	if code != 200 || out["status"] != "confirming" {
		t.Fatalf("upload: %d %v", code, out)
	}
	sum := sha256.Sum256(img)
	var photo tgfake.Sent
	for _, s := range w.sentTo(4003) {
		if s.Method == "sendPhoto" {
			photo = s
		}
	}
	if photo.FileSHA256 != hex.EncodeToString(sum[:]) || photo.FileSize != int64(len(img)) || !strings.Contains(photo.Text, ref) {
		t.Fatalf("the photo in the customer's chat: %+v", photo)
	}

	// The review queue has it, as one the bot's own flow would have put there: the receipt is a Telegram file id.
	list := w.pending()
	if len(list) != 1 || list[0].GetIntentId() != intent || list[0].GetProvider() != "manual_card" || list[0].GetReferenceNumber() != "998877" ||
		list[0].GetUser().GetTelegramId() != 4003 || list[0].GetAmount().GetAmount() != 150000 || !strings.HasPrefix(list[0].GetReceiptFile(), "fake-photo-") {
		t.Fatalf("review queue: %+v", list)
	}
	// Every owner is alerted, with the Review button of that very payment.
	w.eventually("the staff alert", func() bool {
		for _, s := range w.sentTo(ownerTG) {
			if s.Method == "sendMessage" && strings.Contains(string(s.Markup), "adm:pi:"+intent) && strings.Contains(s.Text, "tia") {
				return true
			}
		}
		return false
	})
	// The admin opens the card from the alert: the receipt photo, the reference, approve and reject.
	w.fake.Inject(tg.Update{CallbackQuery: &tg.CallbackQuery{ID: "cb-1", From: tg.User{ID: ownerTG, Username: "boss"}, Data: "adm:pi:" + intent,
		Message: &tg.Message{MessageID: 1, Chat: tg.Chat{ID: ownerTG, Type: "private"}}}})
	w.eventually("the admin card", func() bool {
		for _, s := range w.sentTo(ownerTG) {
			if s.Method == "sendPhoto" && strings.Contains(s.Text, "998877") && strings.Contains(string(s.Markup), "adm:ok:"+intent) {
				return true
			}
		}
		return false
	})

	// Approved, the order is delivered; the proof cannot be sent again.
	w.approve(intent)
	tia.waitOrder(order, "active")
	if code, out := tia.upload(intent, "112233", img); code != 409 {
		t.Errorf("a proof after approval: %d %v", code, out)
	}
}

// ---- crypto, the payment link, a top-up ----

func TestMiniAppCryptoTransactionIDAndPaymentLink(t *testing.T) {
	w := newMiniWorld(t)
	w.set("payments.zarinpal_link", "https://zarinp.al/shop")
	uma := w.open(4004, "uma")

	// Crypto: a transaction hash is the proof.
	order := uma.order("cryptoflowk1")
	pay := uma.must(http.MethodPost, "/api/mini/v1/orders/"+order+"/pay", map[string]any{"method": "manual_crypto"})
	intent := pay["intent_id"].(string)
	d := pay["details"].(map[string]any)
	if d["usdt_trc20"] != "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r" || pay["usdt_amount"] != float64(2_500_000) {
		t.Fatalf("crypto instructions: %v", pay)
	}
	hash := strings.Repeat("ab12", 16)
	if out := uma.must(http.MethodPost, "/api/mini/v1/payments/"+intent+"/proof", map[string]any{"txid": hash, "network": "TRC20"}); out["status"] != "confirming" {
		t.Fatalf("proof: %v", out)
	}
	list := w.pending()
	if len(list) != 1 || list[0].GetTxid() != hash || list[0].GetNetwork() != "TRC20" || list[0].GetReceiptFile() != "" {
		t.Fatalf("review queue: %+v", list)
	}
	// The same transaction cannot pay a second payment.
	order2 := uma.order("cryptoflowk2")
	second := uma.must(http.MethodPost, "/api/mini/v1/orders/"+order2+"/pay", map[string]any{"method": "manual_crypto"})["intent_id"].(string)
	if code, out := uma.do(http.MethodPost, "/api/mini/v1/payments/"+second+"/proof", map[string]any{"txid": hash, "network": "TRC20"}); code != 409 || out["error"] != "duplicate" {
		t.Fatalf("the same hash twice: %d %v", code, out)
	}
	w.approve(intent)
	uma.waitOrder(order, "active")

	// The Zarinpal payment link: the receipt screenshot alone is the proof.
	order3 := uma.order("cryptoflowk3")
	link := uma.must(http.MethodPost, "/api/mini/v1/orders/"+order3+"/pay", map[string]any{"method": "manual_zarinpal"})
	if link["details"].(map[string]any)["zarinpal_link"] != "https://zarinp.al/shop" {
		t.Fatalf("payment link: %v", link)
	}
	if code, out := uma.upload(link["intent_id"].(string), "", receiptPNG(t)); code != 200 || out["status"] != "confirming" {
		t.Fatalf("a receipt for the payment link: %d %v", code, out)
	}
}

func TestMiniAppTopupByCardIsCreditedAfterReview(t *testing.T) {
	w := newMiniWorld(t)
	ned := w.open(4005, "ned")
	top := ned.must(http.MethodPost, "/api/mini/v1/topup", map[string]any{"amount": 200000, "method": "manual_card", "key": "topupflowk1"})
	intent := top["intent_id"].(string)
	if top["amount"] != float64(200000) || top["currency"] != "IRT" {
		t.Fatalf("top-up: %v", top)
	}
	if code, out := ned.upload(intent, "445566", receiptPNG(t)); code != 200 {
		t.Fatalf("receipt: %d %v", code, out)
	}
	w.approve(intent)
	w.eventually("the wallet to be credited", func() bool {
		out := ned.must(http.MethodGet, "/api/mini/v1/wallet", nil)
		ws, _ := out["wallets"].([]any)
		return len(ws) > 0 && ws[0].(map[string]any)["balance"] == float64(200000)
	})
}

// A top-up by Stars is the same kind of payment as an order's: the invoice
// says it is a wallet top-up, and what the bot's handlers settle lands in the
// wallet.
func TestMiniAppTopupByStarsIsCreditedByTheBotsHandlers(t *testing.T) {
	w := newMiniWorld(t)
	kim := w.open(4012, "kim")
	top := kim.must(http.MethodPost, "/api/mini/v1/topup", map[string]any{"amount": 150000, "method": "stars", "key": "starstopupk1"})
	intent, link := top["intent_id"].(string), top["invoice_url"].(string)
	if top["stars"] != float64(100) || top["amount"] != float64(150000) || top["currency"] != "IRT" {
		t.Fatalf("top-up by Stars: %v", top)
	}
	links := w.fake.InvoiceLinks()
	if len(links) != 1 || links[0].Payload != intent || links[0].Stars != 100 || links[0].Title != "Wallet top-up" || links[0].Description != "Payment of 150,000 Toman" {
		t.Fatalf("invoice links: %+v", links)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := w.fake.PayInvoiceLink(ctx, link, tg.User{ID: 4012, Username: "kim", LanguageCode: "en"}); err != nil {
		t.Fatalf("paying the invoice: %v", err)
	}
	w.eventually("the wallet to be credited", func() bool {
		out := kim.must(http.MethodGet, "/api/mini/v1/wallet", nil)
		ws, _ := out["wallets"].([]any)
		return len(ws) > 0 && ws[0].(map[string]any)["balance"] == float64(150000)
	})
	if out := kim.must(http.MethodPost, "/api/mini/v1/payments/"+intent+"/check", map[string]any{}); out["status"] != "succeeded" {
		t.Errorf("the payment after settling: %v", out)
	}
	// The bot's message says the wallet was credited, not that a service is being prepared.
	w.eventually("the bot to confirm the top-up", func() bool {
		for _, s := range w.sentTo(4012) {
			if s.Method == "sendMessage" && strings.Contains(s.Text, "added to your wallet") {
				return true
			}
		}
		return false
	})
	for _, s := range w.sentTo(4012) {
		if strings.Contains(s.Text, "being prepared") {
			t.Errorf("a top-up was told a service is being prepared: %q", s.Text)
		}
	}
}
