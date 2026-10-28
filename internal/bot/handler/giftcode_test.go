package handler

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// giftCodes makes gift codes as the owner would in the dashboard.
func (w *world) giftCodes(spec coredomain.GiftCodeSpec) []string {
	w.t.Helper()
	ctx := context.Background()
	owner, err := w.stack.Store.GetUserByTelegramID(ctx, w.stack.Store.Conn(), ownerID)
	if err != nil {
		w.t.Fatal(err)
	}
	made, err := w.stack.Domain.CreateGiftCodes(ctx, owner, spec)
	if err != nil {
		w.t.Fatal(err)
	}
	out := make([]string, len(made))
	for i, c := range made {
		out[i] = c.Code
	}
	return out
}

func (w *world) giftCode(amount int64, uses int) string {
	w.t.Helper()
	return w.giftCodes(coredomain.GiftCodeSpec{Amount: amount, Currency: "IRT", MaxRedemptions: uses})[0]
}

func (w *world) balanceOf(telegramID int64) int64 {
	w.t.Helper()
	u := w.storeUser(telegramID)
	wl, err := w.stack.Store.GetWallet(context.Background(), w.stack.Store.Conn(), u.ID, "IRT")
	if err != nil {
		w.t.Fatal(err)
	}
	return wl.Balance
}

func (w *world) setUserStatus(telegramID int64, status string) {
	w.t.Helper()
	u := w.storeUser(telegramID)
	if err := w.stack.Store.SetUserStatus(context.Background(), w.stack.Store.Conn(), u.ID, status); err != nil {
		w.t.Fatal(err)
	}
}

// gift opens the question: the wallet, then its gift code button.
func (p *person) gift() {
	p.w.t.Helper()
	p.press("wallet")
	p.press("gift")
}

// buttons lists the labels of the newest message that has any.
func (p *person) buttons() string {
	ms := p.w.tg.all(p.id)
	for i := len(ms) - 1; i >= 0; i-- {
		if l := ms[i].labels(); len(l) > 0 {
			return strings.Join(l, " | ")
		}
	}
	return ""
}

func TestGiftCodeFlowInEnglish(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	code := w.giftCode(25_000, 5)
	u := w.customer(901, "gina")

	// The wallet screen: the button sits in a row of its own between top up and home, with My codes beside it.
	u.press("wallet")
	if got := u.buttons(); got != "➕ Top up | 🧾 History | 🎁 Apply gift code | 🎟 My codes | 🏠 Main menu" {
		t.Fatalf("wallet buttons: %s", got)
	}
	u.press("gift")
	u.sees("Send your gift code")
	if got := u.buttons(); got != "✖️ Cancel" {
		t.Fatalf("while asking: %s", got)
	}

	// Typed as a person types it: small letters, no dashes.
	u.text(strings.ToLower(strings.ReplaceAll(code, "-", "")))
	u.lastHas("Gift code applied")
	u.lastHas("25,000 Toman was added to your wallet")
	u.lastHas("Balance: <b>25,000 Toman</b>")
	if got := u.buttons(); got != "👛 Wallet | 🏠 Main menu" {
		t.Fatalf("after: %s", got)
	}
	// The event the credit published reaches the bot's feed and says nothing more.
	w.drain()
	if n := u.count("was added to your wallet"); n != 1 {
		t.Fatalf("%d messages say the wallet was credited, want the one answer", n)
	}
	u.press("wallet")
	u.sees("Balance: <b>25,000 Toman</b>")
	u.press("whist")
	u.sees("· Gift code ·")
	u.sees("+25,000 Toman")

	// The scene is over: another message is not a code.
	u.text(w.giftCode(1_000, 5))
	if got := w.balanceOf(901); got != 25_000 {
		t.Fatalf("a message after the scene credited: %d", got)
	}
}

func TestGiftCodeFlowInPersian(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	code := w.giftCode(1_250_000, 2)
	u := w.person(902, "")
	u.text("/start")
	u.press("lang:fa")
	u.press("wallet")
	u.sees("🎁 اعمال کد هدیه")
	u.press("gift")
	u.sees("کد هدیهٔ خود را بفرستید")
	u.text(code)
	u.lastHas("کد هدیه اعمال شد")
	u.lastHas("۱٬۲۵۰٬۰۰۰ تومان")
	u.lastHas("موجودی: <b>۱٬۲۵۰٬۰۰۰ تومان</b>")

	// A wrong one, and the lock, in Persian.
	v := w.person(903, "")
	v.text("/start")
	v.press("lang:fa")
	v.gift()
	v.text("ABCD-EFGH-JKMN")
	v.lastHas("این کد هدیه معتبر نیست")
	for range 3 {
		v.text("ABCD-EFGH-JKMP")
	}
	v.text("ABCD-EFGH-JKMQ")
	v.lastHas("۶۰ دقیقهٔ دیگر")
}

func TestGiftCodeWrongCodesAndTheLock(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	good := w.giftCode(5_000, 5)
	u := w.customer(911, "wren")
	u.gift()

	// A wrong code leaves the question open, Cancel beneath it.
	u.text("ABCD-EFGH-JKMN")
	u.lastHas("not valid, or it is no longer active")
	if got := u.buttons(); got != "✖️ Cancel" {
		t.Fatalf("after a wrong code: %s", got)
	}
	// Words, symbols and a wall of text are no code either, and are told the same.
	for _, s := range []string{"hello", "???", strings.Repeat("A", 300)} {
		u.text(s)
		u.lastHas("not valid, or it is no longer active")
	}
	// The fifth closes it, says for how long, and ends the scene.
	u.text("ABCD-EFGH-JKMP")
	u.lastHas("closed for you for now")
	u.lastHas("60 min")
	// Locked, the right code waits too, and nothing is credited.
	u.gift()
	u.text(good)
	u.lastHas("closed for you for now")
	if got := w.balanceOf(911); got != 0 {
		t.Fatalf("a locked customer was credited %d", got)
	}
	// Another customer is not affected by it.
	v := w.customer(912, "val")
	v.gift()
	v.text(good)
	v.lastHas("Gift code applied")
}

func TestGiftCodeUsedUpAndAlreadyRedeemed(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	code := w.giftCode(700, 1)
	a, b := w.customer(921, "ann"), w.customer(922, "ben")
	a.gift()
	a.text(code)
	a.lastHas("Gift code applied")
	// The customer who has it is told so.
	a.gift()
	a.text(code)
	a.lastHas("You have already used this gift code")
	// Another customer is told it is used up, having sent the whole code; the question stays open for another.
	b.gift()
	b.text(code)
	b.lastHas("has been used up")
	if got := b.buttons(); got != "✖️ Cancel" {
		t.Fatalf("after: %s", got)
	}
	b.text(w.giftCode(300, 1))
	b.lastHas("Gift code applied")
	if got := w.balanceOf(922); got != 300 {
		t.Fatalf("balance %d", got)
	}
}

func TestGiftCodeCancel(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	code := w.giftCode(900, 5)
	u := w.customer(931, "cal")
	u.gift()
	u.press("cancel")
	u.toast("Cancelled")
	// Cancelled: the next message is not taken as a code.
	u.text(code)
	if got := w.balanceOf(931); got != 0 {
		t.Fatalf("a code sent after Cancel was redeemed: %d", got)
	}
	// /cancel leaves it too.
	u.gift()
	u.text("/cancel")
	u.text(code)
	if got := w.balanceOf(931); got != 0 {
		t.Fatalf("a code sent after /cancel was redeemed: %d", got)
	}
	// A tap on the bottom menu is never a code, and ends the question.
	u.gift()
	u.text("👛 Wallet")
	u.text(code)
	if got := w.balanceOf(931); got != 0 {
		t.Fatalf("a code sent after the menu: %d", got)
	}
	// A photo is not a code; the question stays open for the text.
	u.gift()
	u.photo("some-photo", "")
	u.lastHas("send the gift code as text")
	u.text(code)
	u.lastHas("Gift code applied")
}

func TestGiftCodesSwitchedOff(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	code := w.giftCode(100, 5)
	u := w.customer(941, "oli")
	u.gift()
	u.sees("Send your gift code")

	owner.text("/set gift_codes.enabled false")
	owner.sees("Saved")
	// In the middle of the question: the code is not taken.
	u.text(code)
	u.lastHas("not available right now")
	if got := w.balanceOf(941); got != 0 {
		t.Fatalf("credited while off: %d", got)
	}
	// The wallet screen has no button; a button from before says so and shows the wallet.
	u.press("wallet")
	if got := u.buttons(); got != "➕ Top up | 🧾 History | 🎟 My codes | 🏠 Main menu" { // My codes stays: it lists what was applied before
		t.Fatalf("wallet buttons while off: %s", got)
	}
	w.tg.add(941, &msg{text: "an old message", kb: (&tg.Keyboard{}).Row(tg.CB("🎁", "gift"))})
	u.press("gift")
	u.toast("not available right now")
	u.sees("Balance")

	owner.text("/set gift_codes.enabled")
	owner.sees("Saved")
	u.press("home")
	u.press("wallet")
	if got := u.buttons(); !strings.Contains(got, "🎁 Apply gift code") {
		t.Fatalf("wallet buttons after switching on: %s", got)
	}
	u.press("gift")
	u.text(code)
	u.lastHas("Gift code applied")
}

// The existing gates hold: maintenance and the required channel answer what a customer sends in the middle of the
// question, and the code is not redeemed; a banned account is told so, and its question ends.
func TestGiftCodesAndTheGates(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	code := w.giftCode(100, 10)
	u := w.customer(951, "gus")
	u.gift()

	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	u.text(code)
	u.lastHas("under maintenance")
	if got := w.balanceOf(951); got != 0 {
		t.Fatalf("redeemed during maintenance: %d", got)
	}
	owner.text("/set maintenance.enabled false")
	owner.text("/set join.channel @testchan")
	u.text(code)
	u.lastHas("Join our channel")
	if got := w.balanceOf(951); got != 0 {
		t.Fatalf("redeemed without joining: %d", got)
	}
	w.tg.setMember(951, "member")
	u.press("jn")
	u.lastHas("Main menu")
	u.gift()
	u.text(code)
	u.lastHas("Gift code applied")
	owner.text("/set join.channel")

	// Banned in the middle of the question.
	v := w.customer(952, "bob")
	v.gift()
	w.setUserStatus(952, "banned")
	v.text(code)
	v.lastHas("account is restricted")
	if got := w.balanceOf(952); got != 0 {
		t.Fatalf("a banned account was credited: %d", got)
	}
	// The question is over for them: it is not asked again by itself.
	w.setUserStatus(952, "active")
	v.text(code)
	if got := w.balanceOf(952); got != 0 {
		t.Fatalf("a question outlived the ban: %d", got)
	}
}

// A reseller has the button too, and the credit counts.
func TestGiftCodeForAReseller(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	code := w.giftCode(40_000, 5)
	r := w.customer(961, "rex")
	w.role(961, "reseller")
	r.press("wallet")
	r.sees("Apply gift code")
	r.press("gift")
	r.text(code)
	r.lastHas("Gift code applied")
	if got := w.balanceOf(961); got != 40_000 {
		t.Fatalf("a reseller's balance %d", got)
	}
}

// giftSpy stands in for core where a test needs to say what core answers, and to see what the bot asked.
type giftSpy struct {
	corev1.CoreServiceClient
	mu    sync.Mutex
	reqs  []*corev1.RedeemGiftCodeRequest
	reply func() (*corev1.RedeemGiftCodeResponse, error)
}

func (s *giftSpy) RedeemGiftCode(_ context.Context, in *corev1.RedeemGiftCodeRequest, _ ...grpc.CallOption) (*corev1.RedeemGiftCodeResponse, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, in)
	reply := s.reply
	s.mu.Unlock()
	return reply()
}

func (s *giftSpy) taken() []*corev1.RedeemGiftCodeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*corev1.RedeemGiftCodeRequest(nil), s.reqs...)
}

func TestGiftCodeRequestsAndWhatCoreSays(t *testing.T) {
	w := newWorld(t)
	logs := &bytes.Buffer{}
	w.h.log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	setupStore(t, w)
	u := w.customer(971, "ida")
	uid := w.storeUser(971).ID
	spy := &giftSpy{CoreServiceClient: w.stack.Core}
	w.h.core = spy
	say := func(r *corev1.RedeemGiftCodeResponse, err error) {
		spy.mu.Lock()
		spy.reply = func() (*corev1.RedeemGiftCodeResponse, error) { return r, err }
		spy.mu.Unlock()
	}
	outcome := func(o corev1.GiftCodeOutcome) *corev1.RedeemGiftCodeResponse {
		return &corev1.RedeemGiftCodeResponse{Outcome: o}
	}
	const secret = "QWER-TYUP-ASDF" //nolint:gosec // a made-up code for the test, not a credential

	// What is sent: the customer's id in core, the text as typed (trimmed), and a key made of the customer and
	// the message, so the same message retried is the same request and another message another.
	say(outcome(corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_INVALID), nil)
	u.gift()
	u.text("  " + secret + "  ")
	reqs := spy.taken()
	if len(reqs) != 1 || reqs[0].GetUserId() != uid || reqs[0].GetCode() != secret {
		t.Fatalf("request: %+v", reqs)
	}
	msgID, ok := strings.CutPrefix(reqs[0].GetIdempotencyKey(), "gift:971:")
	if _, err := strconv.Atoi(msgID); !ok || err != nil {
		t.Fatalf("key %q is not gift:<telegram id>:<message id>", reqs[0].GetIdempotencyKey())
	}
	u.text(secret)
	if k := spy.taken()[1].GetIdempotencyKey(); k == reqs[0].GetIdempotencyKey() {
		t.Fatalf("two messages made one request key %q", k)
	}

	// A lock's wait is said in whole minutes, rounded up, at least one.
	u.press("cancel")
	for seconds, want := range map[int64]string{0: "1 min", 1: "1 min", 59: "1 min", 60: "1 min", 61: "2 min", 3600: "60 min"} {
		say(&corev1.RedeemGiftCodeResponse{Outcome: corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_LOCKED, RetryAfterSeconds: seconds}, nil)
		u.gift()
		u.text(secret)
		u.lastHas("try again in " + want)
	}
	// Core out of reach: the customer is told, the question stays open, and nothing is credited.
	say(nil, status.Error(codes.Unavailable, "down"))
	u.gift()
	u.text(secret)
	u.lastHas("temporarily unavailable")
	say(outcome(corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_INVALID), nil)
	u.text(secret) // still a code: the question survived
	u.lastHas("not valid")
	// The owner's switch seen by core first: the same words as the bot's own.
	say(outcome(corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_OFF), nil)
	u.text(secret)
	u.lastHas("not available right now")
	// An outcome this bot does not know (a newer core), or none, is an error and not a guess.
	for _, o := range []corev1.GiftCodeOutcome{corev1.GiftCodeOutcome(99), corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_UNSPECIFIED} {
		say(outcome(o), nil)
		u.gift()
		u.text(secret)
		u.lastHas("Something went wrong")
		u.press("home")
	}
	// A banned account hears it, and the question ends.
	say(nil, status.Error(codes.PermissionDenied, "domain: user is banned"))
	u.gift()
	u.text(secret)
	u.lastHas("account is restricted")
	n := len(spy.taken())
	u.text(secret)
	if len(spy.taken()) != n {
		t.Fatal("a banned customer's next message was sent as a code")
	}

	// The code is nowhere in what the bot logged.
	if strings.Contains(strings.ToUpper(logs.String()), strings.ReplaceAll(secret, "-", "")) || strings.Contains(logs.String(), secret) {
		t.Fatalf("a gift code is in the log:\n%s", logs.String())
	}
}
