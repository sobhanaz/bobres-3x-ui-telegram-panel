package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// --- a fake Telegram that keeps each chat's messages ---

type msg struct {
	id       int
	text     string
	kb       *tg.Keyboard
	photo    bool
	document bool
	fileID   string
}

type fakeTG struct {
	mu       sync.Mutex
	chats    map[int64][]*msg
	toasts   map[int64][]string // by callback sender
	cbFrom   map[string]int64
	invoices map[int64][]tg.Invoice
	answers  map[string]precheckAnswer // by pre-checkout query id
	docs     map[string]bool           // file ids users sent as documents

	// The join channel: each user's status (none: "left"), how often the
	// bot asked, and an error every check fails with.
	members     map[int64]string
	memberCalls int
	memberErr   error
}

type precheckAnswer struct {
	ok  bool
	msg string
}

func newFakeTG() *fakeTG {
	return &fakeTG{chats: map[int64][]*msg{}, toasts: map[int64][]string{}, cbFrom: map[string]int64{},
		invoices: map[int64][]tg.Invoice{}, answers: map[string]precheckAnswer{}, docs: map[string]bool{},
		members: map[int64]string{}}
}

func (f *fakeTG) GetChat(_ context.Context, chat string) (*tg.Chat, error) {
	return &tg.Chat{ID: -1001234567890, Type: "channel", Title: "Test channel", Username: strings.TrimPrefix(chat, "@")}, nil
}

func (f *fakeTG) GetChatMember(_ context.Context, _ string, userID int64) (*tg.ChatMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memberCalls++
	if f.memberErr != nil {
		return nil, f.memberErr
	}
	st := f.members[userID]
	if st == "" {
		st = "left"
	}
	return &tg.ChatMember{Status: st, User: tg.User{ID: userID}}, nil
}

func (f *fakeTG) setMember(userID int64, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[userID] = status
}

func (f *fakeTG) checks() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.memberCalls
}

func (f *fakeTG) SendInvoice(_ context.Context, chat int64, inv tg.Invoice) (*tg.Message, error) {
	f.mu.Lock()
	f.invoices[chat] = append(f.invoices[chat], inv)
	f.mu.Unlock()
	return f.add(chat, &msg{text: fmt.Sprintf("[invoice] %s: %d Stars", inv.Title, inv.Stars)}), nil
}

func (f *fakeTG) AnswerPreCheckoutQuery(_ context.Context, id string, ok bool, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[id] = precheckAnswer{ok: ok, msg: errMsg}
	return nil
}

func (f *fakeTG) lastInvoice(chat int64) tg.Invoice {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv := f.invoices[chat]
	if len(inv) == 0 {
		return tg.Invoice{}
	}
	return inv[len(inv)-1]
}

func (f *fakeTG) add(chat int64, m *msg) *tg.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	m.id = len(f.chats[chat]) + 1
	f.chats[chat] = append(f.chats[chat], m)
	return &tg.Message{MessageID: m.id, Chat: tg.Chat{ID: chat, Type: "private"}}
}

func (f *fakeTG) SendMessage(_ context.Context, chat int64, text string, kb *tg.Keyboard) (*tg.Message, error) {
	return f.add(chat, &msg{text: text, kb: kb}), nil
}

func (f *fakeTG) SendPhoto(_ context.Context, chat int64, p tg.Photo, caption string, kb *tg.Keyboard) (*tg.Message, error) {
	f.mu.Lock()
	isDoc := f.docs[p.FileID]
	f.mu.Unlock()
	if isDoc { // like Telegram: a document's file id is not a photo
		return nil, &tg.APIError{Code: 400, Description: "Bad Request: type of file mismatch"}
	}
	return f.add(chat, &msg{text: caption, kb: kb, photo: true, fileID: p.FileID}), nil
}

func (f *fakeTG) SendDocument(_ context.Context, chat int64, fileID, caption string, kb *tg.Keyboard) (*tg.Message, error) {
	return f.add(chat, &msg{text: caption, kb: kb, document: true, fileID: fileID}), nil
}

func (f *fakeTG) EditMessageText(_ context.Context, chat int64, id int, text string, kb *tg.Keyboard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.chats[chat] {
		if m.id == id {
			if m.photo {
				return &tg.APIError{Code: 400, Description: "Bad Request: there is no text in the message to edit"}
			}
			m.text, m.kb = text, kb
			return nil
		}
	}
	return &tg.APIError{Code: 400, Description: "message to edit not found"}
}

func (f *fakeTG) EditMessageReplyMarkup(_ context.Context, chat int64, id int, kb *tg.Keyboard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.chats[chat] {
		if m.id == id {
			m.kb = kb
		}
	}
	return nil
}

func (f *fakeTG) AnswerCallback(_ context.Context, id, text string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if text != "" {
		f.toasts[f.cbFrom[id]] = append(f.toasts[f.cbFrom[id]], text)
	}
	return nil
}

func (f *fakeTG) last(chat int64) *msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	ms := f.chats[chat]
	if len(ms) == 0 {
		return &msg{}
	}
	return ms[len(ms)-1]
}

func (f *fakeTG) all(chat int64) []*msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*msg(nil), f.chats[chat]...)
}

func (m *msg) labels() []string {
	var out []string
	if m.kb != nil {
		for _, row := range m.kb.InlineKeyboard {
			for _, b := range row {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

// --- simulated users ---

type world struct {
	t     *testing.T
	h     *Handler
	tg    *fakeTG
	stack *testenv.Stack
	feed  *eventbus.Consumer
	next  int64
	mu    sync.Mutex
}

type person struct {
	w        *world
	id       int64
	username string
}

const ownerID = 9000

func newWorld(t *testing.T) *world {
	t.Helper()
	stack := testenv.Start(t, ownerID)
	ftg := newFakeTG()
	cat := i18n.MustLoad()
	h := New(stack.Core, ftg, state.NewMemory(), cat, nil, Config{AdminTelegramID: ownerID, BotName: "TestVPN", BotUsername: "test_vpn_bot"}, nil)
	feed, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Source:     eventbus.NewGRPCSource(stack.Feed),
		Handle:     notify.New(stack.Core, ftg, cat, h.Settings(), ownerID, nil).Handle,
		DeadLetter: func(context.Context, eventbus.Message, error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, h: h, tg: ftg, stack: stack, feed: feed}
}

func (w *world) person(id int64, username string) *person {
	return &person{w: w, id: id, username: username}
}

func (w *world) updateID() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	return w.next
}

func (p *person) from() *tg.User { return &tg.User{ID: p.id, Username: p.username, FirstName: "U"} }

func (p *person) text(s string) {
	p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), Message: &tg.Message{
		MessageID: int(p.w.updateID()), From: p.from(), Chat: tg.Chat{ID: p.id, Type: "private"}, Text: s,
	}})
}

func (p *person) photo(fileID, caption string) {
	p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), Message: &tg.Message{
		MessageID: int(p.w.updateID()), From: p.from(), Chat: tg.Chat{ID: p.id, Type: "private"},
		Caption: caption, Photo: []tg.PhotoSize{{FileID: fileID + "-small", Width: 90, Height: 90}, {FileID: fileID, Width: 1280, Height: 960}},
	}})
}

// document sends an image as a file (uncompressed screenshot).
func (p *person) document(fileID, mime, caption string) {
	p.w.tg.mu.Lock()
	p.w.tg.docs[fileID] = true
	p.w.tg.mu.Unlock()
	p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), Message: &tg.Message{
		MessageID: int(p.w.updateID()), From: p.from(), Chat: tg.Chat{ID: p.id, Type: "private"},
		Caption: caption, Document: &tg.Document{FileID: fileID, MimeType: mime, FileName: "receipt.jpg"},
	}})
}

// press taps the newest button whose callback data is exactly prefix, or
// else the newest one that starts with it.
func (p *person) press(prefix string) {
	p.w.t.Helper()
	ms := p.w.tg.all(p.id)
	for _, exact := range []bool{true, false} {
		if p.pressMatching(ms, prefix, exact) {
			return
		}
	}
	p.w.t.Fatalf("user %d: no button %q in chat; last message: %q", p.id, prefix, p.w.tg.last(p.id).text)
}

func (p *person) pressMatching(ms []*msg, prefix string, exact bool) bool {
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].kb == nil {
			continue
		}
		for _, row := range ms[i].kb.InlineKeyboard {
			for _, b := range row {
				if (exact && b.CallbackData == prefix) || (!exact && strings.HasPrefix(b.CallbackData, prefix)) {
					id := strconv.FormatInt(p.w.updateID(), 10)
					p.w.tg.mu.Lock()
					p.w.tg.cbFrom[id] = p.id
					p.w.tg.mu.Unlock()
					p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), CallbackQuery: &tg.CallbackQuery{
						ID: id, From: *p.from(), Data: b.CallbackData,
						Message: &tg.Message{MessageID: ms[i].id, Chat: tg.Chat{ID: p.id, Type: "private"}},
					}})
					return true
				}
			}
		}
	}
	return false
}

// sees checks every message text and button label this user was shown.
func (p *person) sees(sub string) {
	p.w.t.Helper()
	for _, m := range p.w.tg.all(p.id) {
		if strings.Contains(m.text, sub) || strings.Contains(strings.Join(m.labels(), "\n"), sub) {
			return
		}
	}
	p.w.t.Fatalf("user %d never saw %q; last message: %q", p.id, sub, p.w.tg.last(p.id).text)
}

func (p *person) last() *msg { return p.w.tg.last(p.id) }

func (p *person) toast(sub string) {
	p.w.t.Helper()
	p.w.tg.mu.Lock()
	defer p.w.tg.mu.Unlock()
	for _, s := range p.w.tg.toasts[p.id] {
		if strings.Contains(s, sub) {
			return
		}
	}
	p.w.t.Fatalf("user %d never got toast %q (got %v)", p.id, sub, p.w.tg.toasts[p.id])
}

// preCheckout plays Telegram asking the bot to confirm a Stars payment and
// returns the bot's answer.
func (p *person) preCheckout(inv tg.Invoice, stars int64) precheckAnswer {
	p.w.t.Helper()
	id := fmt.Sprintf("pcq-%d", p.w.updateID())
	p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), PreCheckoutQuery: &tg.PreCheckoutQuery{
		ID: id, From: *p.from(), Currency: "XTR", TotalAmount: stars, InvoicePayload: inv.Payload,
	}})
	p.w.tg.mu.Lock()
	defer p.w.tg.mu.Unlock()
	a, ok := p.w.tg.answers[id]
	if !ok {
		p.w.t.Fatalf("the bot never answered pre-checkout %s", id)
	}
	return a
}

// paid plays Telegram reporting a completed Stars payment.
func (p *person) paid(inv tg.Invoice, stars int64, chargeID string) {
	p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), Message: &tg.Message{
		MessageID: int(p.w.updateID()), From: p.from(), Chat: tg.Chat{ID: p.id, Type: "private"},
		SuccessfulPayment: &tg.SuccessfulPayment{Currency: "XTR", TotalAmount: stars, InvoicePayload: inv.Payload,
			TelegramPaymentChargeID: chargeID},
	}})
}

// link returns the URL of the newest link button whose label contains label.
func (p *person) link(label string) string {
	p.w.t.Helper()
	ms := p.w.tg.all(p.id)
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].kb == nil {
			continue
		}
		for _, row := range ms[i].kb.InlineKeyboard {
			for _, b := range row {
				if b.URL != "" && strings.Contains(b.Text, label) {
					return b.URL
				}
			}
		}
	}
	p.w.t.Fatalf("user %d: no link button %q", p.id, label)
	return ""
}

// eventually runs the notification feed until cond holds.
func (w *world) eventually(what string, cond func() bool) {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			w.t.Fatalf("timed out waiting for %s", what)
		}
		if _, err := w.feed.Step(context.Background()); err != nil {
			w.t.Logf("feed step: %v", err)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (p *person) eventuallySees(sub string) {
	p.w.t.Helper()
	p.w.eventually("user "+strconv.FormatInt(p.id, 10)+" to see "+sub, func() bool {
		for _, m := range p.w.tg.all(p.id) {
			if strings.Contains(m.text, sub) {
				return true
			}
		}
		return false
	})
}

// trc20 is a well-formed USDT TRC20 address (T and 33 base58 characters):
// core refuses anything else.
const trc20 = "TNPeeaaFB7K9cmo4uQpcU32zGK8G1NYqeL"

// setupStore registers the owner and configures a plan, a card and a trial.
func setupStore(t *testing.T, w *world) *person {
	t.Helper()
	owner := w.person(ownerID, "boss")
	owner.text("/start")
	owner.press("lang:en")
	owner.sees("Main menu")
	owner.text("/plan_add 150000 IRT 30 50 Monthly 50GB | ماهانه ۵۰ گیگ")
	owner.sees("Plan saved: Monthly 50GB")
	owner.text("/set payments.card_number 6037-9911-2222-3333")
	owner.text("/set payments.card_holder Ali Rezaei")
	owner.sees("Saved")
	owner.text("/trial 1 1")
	owner.sees("Plan saved")
	return owner
}

func TestRegistrationAndLanguages(t *testing.T) {
	w := newWorld(t)
	en := w.person(101, "alice")
	en.text("hello")
	en.sees("Choose your language")
	en.press("lang:en")
	en.sees("Welcome to <b>TestVPN</b>")
	en.sees("Main menu")

	fa := w.person(102, "")
	fa.text("/start")
	fa.press("lang:fa")
	fa.sees("منوی اصلی")
	fa.press("lang") // the picker
	fa.press("lang:en")
	fa.sees("Main menu")
	fa.toast("Language set to English")
}

func TestCardPurchaseReviewedByAdminAndDelivered(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	buyer := w.person(201, "buyer")
	buyer.text("/start")
	buyer.press("lang:en")
	buyer.press("buy")
	buyer.sees("Monthly 50GB · 150,000 Toman")
	buyer.press("plan:")
	buyer.sees("⏳ 30 days")
	buyer.sees("📶 50 GB")
	buyer.press("pay:c:")
	buyer.sees("6037-9911-2222-3333")
	buyer.sees("Ali Rezaei")
	buyer.photo("receipt-file-1", "") // no caption: asked for the reference
	buyer.sees("bank tracking/reference number")
	buyer.text("998877")
	buyer.sees("Received")

	owner.sees("New payment to review from @buyer")
	owner.press("adm:pend")
	item := owner.last()
	if !item.photo || item.fileID != "receipt-file-1" || !strings.Contains(item.text, "998877") {
		t.Fatalf("review item: %+v", item)
	}
	owner.press("adm:ok:")
	owner.toast("Approved")
	if item.kb != nil && len(item.kb.InlineKeyboard) != 0 {
		t.Fatalf("buttons were not removed from the reviewed item: %+v", item.kb)
	}
	owner.sees("No payments are waiting")

	buyer.eventuallySees("Payment confirmed")
	buyer.eventuallySees("Your service is ready")
	buyer.sees(testenv.SubBase)
	w.eventually("the QR code", func() bool {
		for _, m := range w.tg.all(201) {
			if m.photo && strings.Contains(m.text, "Scan this QR code") {
				return true
			}
		}
		return false
	})

	buyer.press("sub:") // "My services" on the delivery message opens the service
	buyer.sees("Active ✅")
	buyer.press("qr:")
	buyer.press("subs")
	buyer.sees("#")
}

func TestTrialOnceAndWalletPurchase(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.person(301, "carol")
	u.text("/start")
	u.press("lang:en")
	u.press("trial")
	u.sees("free trial is being prepared")
	u.eventuallySees("Your service is ready")
	u.press("home")
	u.press("trial")
	u.toast("already used your free trial")

	// No balance yet: the wallet button explains and offers alternatives.
	u.press("buy")
	u.press("plan:")
	u.press("pay:w:")
	u.sees("not enough for this plan")

	owner.press("adm")
	owner.sees("Admin panel")
	owner.press("adm:find")
	owner.text("301")
	owner.sees("@carol")
	owner.press("adm:adj:")
	owner.text("200000 welcome gift")
	owner.sees("New balance: <b>200,000 Toman</b>")
	u.eventuallySees("Support added 200,000 Toman")

	u.press("home")
	u.press("buy")
	u.press("plan:")
	u.press("pay:w:")
	u.sees("Paid from your wallet")
	w.eventually("two delivered services", func() bool {
		n := 0
		for _, m := range w.tg.all(301) {
			if strings.Contains(m.text, "Your service is ready") {
				n++
			}
		}
		return n == 2
	})
	u.press("home")
	u.press("wallet")
	u.sees("50,000 Toman")
	u.press("whist")
	u.sees("Purchase")
}

func TestRejectionReasonInTheUsersLanguage(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.person(401, "dara")
	u.text("/start")
	u.press("lang:fa")
	u.press("buy")
	u.press("plan:")
	u.press("pay:c:")
	u.photo("receipt-2", "123456")
	u.sees("دریافت شد")

	owner.press("adm:pend")
	owner.press("adm:no:")
	owner.press("adm:nr:0:") // amount mismatch
	owner.toast("Rejected")
	u.eventuallySees("مبلغ واریزی مطابقت ندارد")
	u.sees("۱۵۰٬۰۰۰ تومان")
}

func TestCryptoTXIDFlow(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set payments.usdt_trc20 " + trc20)
	owner.text("/set payments.usdt_rate 60000")
	u := w.person(501, "erin")
	u.text("/start")
	u.press("lang:en")
	u.press("buy")
	u.press("plan:")
	u.press("pay:x:")
	u.sees("2.5 USDT (≈ 150,000 Toman)") // 150000 / 60000, rounded up to cents
	if strings.Contains(u.last().text, "USDT USDT") {
		t.Fatalf("currency printed twice: %q", u.last().text)
	}
	u.sees(trc20)
	u.text("not-a-hash")
	u.sees("does not look like a transaction hash")
	hash := strings.Repeat("ab", 32)
	u.text(hash)
	u.sees("Received")

	// The same transaction cannot pay for a second purchase.
	u.press("home")
	u.press("buy")
	u.press("plan:")
	u.press("pay:x:")
	u.text(strings.ToUpper(hash))
	u.sees("already been submitted")
}

func TestAccessControlAndBans(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.person(601, "mallory")
	u.text("/start")
	u.press("lang:en")
	u.text("/plan_add 1 IRT 1 1 Free stuff")
	u.sees("for admins only")
	u.text("/set payments.card_number 1111")
	if strings.Contains(u.last().text, "Saved") {
		t.Fatal("a regular user changed a setting")
	}
	for _, m := range w.tg.all(601) {
		if m.kb == nil {
			continue
		}
		for _, row := range m.kb.InlineKeyboard {
			for _, b := range row {
				if strings.HasPrefix(b.CallbackData, "adm") {
					t.Fatal("a regular user was shown an admin button")
				}
			}
		}
	}

	owner.press("adm")
	owner.press("adm:find")
	owner.text("@mallory")
	owner.press("adm:ban:")
	owner.toast("banned")

	u.press("buy")
	u.press("plan:")
	u.press("pay:w:")
	u.toast("restricted")
}

func TestDuplicateUpdatesAreHandledOnce(t *testing.T) {
	w := newWorld(t)
	u := w.person(701, "")
	upd := tg.Update{UpdateID: 424242, Message: &tg.Message{MessageID: 1, From: u.from(), Chat: tg.Chat{ID: 701, Type: "private"}, Text: "/start"}}
	w.h.Handle(context.Background(), upd)
	w.h.Handle(context.Background(), upd)
	if n := len(w.tg.all(701)); n != 1 {
		t.Fatalf("re-delivered update answered %d times", n)
	}
	group := tg.Update{UpdateID: 1, Message: &tg.Message{MessageID: 2, From: u.from(), Chat: tg.Chat{ID: -100, Type: "group"}, Text: "/start"}}
	w.h.Handle(context.Background(), group)
	if len(w.tg.all(-100)) != 0 {
		t.Fatal("the bot answered in a group")
	}
}

func TestParsePlanAndMinor(t *testing.T) {
	p, ok := parsePlan("150000 IRT 30 50 Monthly 50GB | ماهانه ۵۰ گیگ")
	if !ok || p.GetPrice().GetAmount() != 150000 || p.GetDurationDays() != 30 || p.GetTrafficBytes() != 50<<30 ||
		p.GetNameI18N()["en"] != "Monthly 50GB" || p.GetNameI18N()["fa"] != "ماهانه ۵۰ گیگ" || p.GetKind() != "both" {
		t.Fatalf("parsePlan: %v %+v", ok, p)
	}
	if p, ok := parsePlan("2.5 usdt 0 100 Big data"); !ok || p.GetPrice().GetAmount() != 2_500_000 || p.GetKind() != "traffic" {
		t.Fatalf("usdt traffic plan: %v %+v", ok, p)
	}
	for _, bad := range []string{"", "150000 IRT 30", "x IRT 30 50 n", "1 EUR 30 50 n", "1 IRT 0 0 n", "1 IRT 30 50"} {
		if _, ok := parsePlan(bad); ok {
			t.Errorf("parsePlan(%q) accepted", bad)
		}
	}
	if v, ok := parseMinor("1.234567", "USDT"); !ok || v != 1_234_567 {
		t.Errorf("parseMinor = %d %v", v, ok)
	}
	if _, ok := parseMinor("1.2345678", "USDT"); ok {
		t.Error("more decimals than the currency has accepted")
	}
}

func TestStarsPurchase(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.person(501, "dana")
	u.text("/start")
	u.press("lang:en")
	// Seen before the owner priced Stars: no Stars button yet...
	u.press("buy")
	u.press("plan:")
	for _, m := range w.tg.all(501) {
		if strings.Contains(strings.Join(m.labels(), "|"), "Telegram Stars") {
			t.Fatal("Stars offered before it was priced")
		}
	}
	owner.text("/set payments.stars_rate 1500")
	owner.sees("Saved")
	// ...and right after it is priced, the button appears (no stale cache).
	u.press("home")
	u.press("buy")
	u.press("plan:")
	u.sees("Pay with Telegram Stars")
	u.press("pay:s:")
	u.sees("invoice for <b>100 Stars</b>") // 150,000 Toman at 1,500 Toman per Star
	inv := w.tg.lastInvoice(501)
	if inv.Stars != 100 || inv.Payload == "" || inv.StartParameter == "" || len([]rune(inv.Title)) > 32 {
		t.Fatalf("invoice: %+v", inv)
	}

	// Telegram asks before charging: wrong amounts and other payers are refused.
	if a := u.preCheckout(inv, 99); a.ok || a.msg == "" {
		t.Fatalf("99 Stars accepted: %+v", a)
	}
	eve := w.person(502, "eve")
	eve.text("/start")
	eve.press("lang:en")
	if a := eve.preCheckout(inv, 100); a.ok {
		t.Fatal("another user may pay this invoice")
	}
	if a := u.preCheckout(inv, 100); !a.ok {
		t.Fatalf("valid pre-checkout refused: %+v", a)
	}
	u.paid(inv, 100, "tg-charge-1")
	u.sees("Payment received")
	u.eventuallySees("Your service is ready")

	// The same payment reported again changes nothing and is not an error.
	u.paid(inv, 100, "tg-charge-1")
	if a := u.preCheckout(inv, 100); a.ok {
		t.Fatal("a paid invoice passed pre-checkout again")
	}

	// A payment that cannot settle (a second charge for the paid invoice) is
	// never answered with "received": the payer and the admin are told.
	eve.paid(inv, 100, "tg-charge-eve")
	eve.sees("could not record it yet")
	owner.sees("A Stars payment was not recorded")
	for _, m := range w.tg.all(502) {
		if strings.Contains(m.text, "Payment received") {
			t.Fatal("an unsettled payment was confirmed to the payer")
		}
	}
}

func TestZarinpalPurchase(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.person(601, "farid")
	u.text("/start")
	u.press("lang:en")
	u.press("buy")
	u.press("plan:")
	u.press("pay:z:")
	u.sees("Turn off your VPN")
	link := u.link("Open the payment page")
	if !strings.HasPrefix(link, w.stack.PaySite.URL+"/pay/") {
		t.Fatalf("pay link must be our own page (Zarinpal checks the Referer): %s", link)
	}

	// Not paid yet: checking says so.
	u.press("chk:")
	u.toast("Not confirmed yet")

	// Our page links to Zarinpal; the customer pays and the browser comes back.
	resp, err := http.Get(link) //nolint:gosec,noctx // test server
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	authority := regexp.MustCompile(`/pg/StartPay/(S[0-9]{35})`).FindStringSubmatch(string(page))
	if authority == nil {
		t.Fatalf("pay page has no Zarinpal link: %s", page)
	}
	cb, err := w.stack.Zarinpal.Pay(authority[1], true)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(cb) //nolint:gosec,noctx // test server
	if err != nil {
		t.Fatal(err)
	}
	back, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(back), "Payment received") {
		t.Fatalf("return page: %s", back)
	}
	u.eventuallySees("Your service is ready")
	u.press("chk:")
	u.sees("Payment received")
}

// Every manual method works like card-to-card: a screenshot, approved by an admin.
func TestScreenshotForZarinpalLinkAndCrypto(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set payments.zarinpal_link http://not-https.example")
	owner.text("/set payments.zarinpal_link https://zarinp.al/teststore")
	owner.text("/set payments.usdt_trc20 " + trc20)
	owner.text("/set payments.usdt_rate 60000")
	u := w.person(701, "nima")
	u.text("/start")
	u.press("lang:en")

	// Zarinpal payment link, receipt screenshot sent as an image file.
	u.press("buy")
	u.press("plan:")
	u.press("pay:l:")
	u.sees("Pay through Zarinpal")
	if link := u.link("Open the payment page"); link != "https://zarinp.al/teststore" {
		t.Fatalf("payment link: %q (an http link must have been refused)", link)
	}
	u.document("zl-receipt-file", "image/jpeg", "TRK-55")
	u.sees("Received")
	owner.sees("New payment to review")
	owner.press("adm:pend")
	owner.sees("Zarinpal link")
	if m := w.tg.last(owner.id); !m.document || m.fileID != "zl-receipt-file" {
		t.Fatalf("the admin must get the screenshot file itself: %+v", m)
	}
	owner.press("adm:ok:")
	u.eventuallySees("Your service is ready")

	// USDT: a screenshot of the transfer instead of the transaction hash.
	u.press("home")
	u.press("buy")
	u.press("plan:")
	u.press("pay:x:")
	u.photo("usdt-transfer-shot", "")
	u.sees("Received")
	owner.press("adm:pend")
	owner.sees("Screenshot attached")
	if m := w.tg.last(owner.id); !m.photo || m.fileID != "usdt-transfer-shot" {
		t.Fatalf("the admin must get the screenshot: %+v", m)
	}
	owner.press("adm:ok:")
	w.eventually("two delivered services", func() bool {
		n := 0
		for _, m := range w.tg.all(701) {
			if strings.Contains(m.text, "Your service is ready") {
				n++
			}
		}
		return n == 2
	})
}

// fund credits a customer's wallet directly (the admin flow is tested above).
func (w *world) fund(tg, amount int64) {
	w.t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	u, err := st.GetUserByTelegramID(ctx, st.Conn(), tg)
	if err != nil {
		w.t.Fatal(err)
	}
	err = st.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := st.Credit(ctx, tx, u.ID, "IRT", amount, &corestore.LedgerEntry{Kind: "adjust",
			IdempotencyKey: fmt.Sprintf("test-fund-%d-%d", tg, time.Now().UnixNano())})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// onlySub returns a customer's one subscription.
func (w *world) onlySub(tg int64) corestore.Subscription {
	w.t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	u, err := st.GetUserByTelegramID(ctx, st.Conn(), tg)
	if err != nil {
		w.t.Fatal(err)
	}
	subs, err := st.ListSubscriptions(ctx, st.Conn(), u.ID, 10)
	if err != nil || len(subs) != 1 {
		w.t.Fatalf("subscriptions of %d: %d %v", tg, len(subs), err)
	}
	return subs[0]
}

func TestRenewAndTopupFromTheServicePage(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/topup_add 30000 IRT 10 +10 GB | ۱۰ گیگ اضافه")
	owner.sees("Plan saved: +10 GB")
	u := w.person(701, "rena")
	u.text("/start")
	u.press("lang:en")
	w.fund(701, 1_000_000)

	u.press("buy")
	for _, l := range u.last().labels() {
		if strings.Contains(l, "+10 GB") {
			t.Fatalf("a traffic package is offered as a new service: %v", u.last().labels())
		}
	}
	u.press("plan:")
	u.press("pay:w:")
	u.sees("Paid from your wallet")
	u.eventuallySees("Your service is ready")
	sub := w.onlySub(701)
	before, _ := w.stack.Panel.Snapshot(sub.ClientEmail)

	u.press("home")
	u.press("subs")
	u.press("sub:")
	u.sees("of 50 GB")
	u.press("rnw:")
	u.sees("Renew service #")
	u.press("rp:")
	u.sees("Added to the time and traffic you have left")
	u.press("pay:w:")
	u.sees("Paid from your wallet")
	u.eventuallySees("renewed")
	after, _ := w.stack.Panel.Snapshot(sub.ClientEmail)
	// Core keeps whole seconds, the panel milliseconds: up to 1 s may go.
	if moved := after.ExpiryTime - before.ExpiryTime; after.TotalGB != 100<<30 || moved > 30*24*3600*1000 || moved < 30*24*3600*1000-1000 {
		t.Fatalf("panel after renewal: total %d (want 100 GB), expiry moved %d ms (want 30 days)", after.TotalGB, moved)
	}

	u.press("sub:") // "View service" under the renewal message
	u.press("tup:")
	u.sees("Add traffic to service")
	u.press("rp:")
	u.sees("more traffic for service")
	u.press("pay:w:")
	u.eventuallySees("Traffic added to service")
	if top, _ := w.stack.Panel.Snapshot(sub.ClientEmail); top.TotalGB != 110<<30 || top.ExpiryTime != after.ExpiryTime {
		t.Fatalf("panel after top-up: %+v", top)
	}
	if lost := w.stack.Panel.LostFields(); len(lost) != 0 {
		t.Fatalf("an update lost the client's identity: %v", lost)
	}
}

func TestDiscountCodeAtCheckout(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/discount_add SPRING20 20% 5 30")
	owner.sees("Saved: <code>SPRING20</code> · 20%")
	owner.text("/discounts")
	owner.sees("0/5 used")
	u := w.person(711, "dina")
	u.text("/start")
	u.press("lang:en")
	w.fund(711, 120_000)

	u.press("buy")
	u.press("plan:")
	u.press("dc:")
	u.sees("Send your discount code")
	u.text("NOPE")
	u.sees("not valid")
	u.text("spring20")
	u.sees("Code <b>SPRING20</b>: 30,000 Toman off")
	u.sees("Price: <b>120,000 Toman</b>")
	u.press("pay:w:")
	u.sees("Paid from your wallet")
	u.eventuallySees("Your service is ready")

	u.press("home")
	u.press("buy")
	u.press("plan:")
	u.press("dc:")
	u.text("SPRING20")
	u.sees("already used this discount code")
	owner.text("/discount_off spring20")
	owner.sees("1/5 used")
	owner.sees("off")
}

func TestInviteLinkRewardsTheInviter(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	inviter := w.person(721, "ina")
	inviter.text("/start")
	inviter.press("lang:en")
	inviter.press("ref")
	inviter.sees("Invite friends")
	m := regexp.MustCompile(`https://t\.me/test_vpn_bot\?start=r_([a-z0-9]+)`).FindStringSubmatch(inviter.last().text)
	if m == nil {
		t.Fatalf("no invite link: %q", inviter.last().text)
	}

	friend := w.person(722, "fred")
	friend.text("/start r_" + m[1])
	friend.press("lang:en")
	friend.sees("Main menu")
	w.fund(722, 150_000)
	friend.press("buy")
	friend.press("plan:")
	friend.press("pay:w:")
	friend.sees("Paid from your wallet")
	inviter.eventuallySees("A friend you invited made a purchase: <b>15,000 Toman</b>")
	inviter.press("home")
	inviter.press("ref")
	inviter.sees("Invited: 1")
}

func TestRemindersReachTheCustomer(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/topup_add 30000 IRT 10 +10 GB | ۱۰ گیگ اضافه")
	owner.sees("Plan saved: +10 GB")
	u := w.person(731, "remy")
	u.text("/start")
	u.press("lang:en")
	w.fund(731, 150_000)
	u.press("buy")
	u.press("plan:")
	u.press("pay:w:")
	u.eventuallySees("Your service is ready")
	sub := w.onlySub(731)
	usage := w.stack.Usage
	usage.Every = -time.Hour // everything is due

	w.stack.Panel.AddUsage(sub.ClientEmail, 0, 45<<30)
	if _, err := usage.SyncDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	u.eventuallySees("has used 45 GB of 50 GB")
	labels := strings.Join(u.last().labels(), " | ")
	if !strings.Contains(labels, "Renew") || !strings.Contains(labels, "Add traffic") {
		t.Fatalf("reminder buttons: %s", labels)
	}

	w.stack.Panel.AddUsage(sub.ClientEmail, 0, 6<<30)
	if _, err := usage.SyncDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	u.eventuallySees("has used all of its 50 GB")
}
