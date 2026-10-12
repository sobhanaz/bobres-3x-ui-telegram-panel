package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeCore answers GetUser with the languages set; other calls are not
// expected here (the nil client panics).
type fakeCore struct {
	corev1.CoreServiceClient
	langs map[int64]string
}

func (f *fakeCore) GetUser(_ context.Context, in *corev1.GetUserRequest, _ ...grpc.CallOption) (*corev1.User, error) {
	if l, ok := f.langs[in.GetTelegramId()]; ok {
		return &corev1.User{TelegramId: in.GetTelegramId(), Language: l}, nil
	}
	return nil, status.Error(codes.NotFound, "no such user")
}

type sentText struct {
	chat int64
	text string
	kb   *tg.Keyboard
}

// fakeTG records what was sent; chats in fail refuse with that error.
type fakeTG struct {
	mu   sync.Mutex
	sent []sentText
	fail map[int64]error
}

func (f *fakeTG) SendMessage(_ context.Context, chatID int64, text string, kb *tg.Keyboard) (*tg.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[chatID]; err != nil {
		return nil, err
	}
	f.sent = append(f.sent, sentText{chatID, text, kb})
	return &tg.Message{MessageID: len(f.sent)}, nil
}

func (f *fakeTG) SendPhoto(context.Context, int64, tg.Photo, string, *tg.Keyboard) (*tg.Message, error) {
	return nil, errors.New("not expected")
}

func (f *fakeTG) take() []sentText {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

const (
	ownerTG   = 100
	adminTG   = 200
	supportTG = 300
)

func alertNotifier(t *testing.T) (*Notifier, *fakeTG, *settings.Store) {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	set := settings.New()
	tgf := &fakeTG{}
	n := New(&fakeCore{langs: map[int64]string{ownerTG: "fa", adminTG: "en", supportTG: "en"}}, tgf, cat, set, ownerTG, nil)
	return n, tgf, set
}

func handleAlert(t *testing.T, n *Notifier, e events.SystemAlertEvent) error {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return n.Handle(context.Background(), eventbus.Message{Topic: events.SystemAlert, Payload: payload})
}

var botDown = events.SystemAlertEvent{Key: "service.bot", Severity: events.SeverityCritical, State: events.AlertProblem,
	TitleKey: "alert.service_down", Args: map[string]string{"service": "bot"}, Detail: "does not answer in time",
	Since: 1760000000, At: 1760000060}

// System alerts go where alerts about failed deliveries go: the configured
// owner, or every owner and admin with notify.recipients = staff, never
// support staff; notify.system_alerts switches them off.
func TestSystemAlertsGoToStaffAlertRecipients(t *testing.T) {
	n, tgf, set := alertNotifier(t)
	if err := handleAlert(t, n, botDown); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	want := "🚨 <b>هشدار سیستم</b>\nسرویس bot از کار افتاده است.\n<code>does not answer in time</code>"
	if len(got) != 1 || got[0].chat != ownerTG || got[0].text != want {
		t.Fatalf("to the owner: %+v", got)
	}

	set.SetStaff([]settings.Contact{
		{TelegramID: ownerTG, Language: "fa", Role: "owner"},
		{TelegramID: adminTG, Language: "en", Role: "admin"},
		{TelegramID: supportTG, Language: "en", Role: "support"},
	})
	set.Replace(map[string]string{"notify.recipients": "staff"})
	if err := handleAlert(t, n, botDown); err != nil {
		t.Fatal(err)
	}
	got = tgf.take()
	if len(got) != 2 || got[0].chat != ownerTG || got[1].chat != adminTG ||
		got[1].text != "🚨 <b>System alert</b>\nThe bot service is down.\n<code>does not answer in time</code>" {
		t.Fatalf("to the staff: %+v", got)
	}

	// One recipient unreachable: the others still get it, and the event is
	// not retried (they would get it twice).
	tgf.fail = map[int64]error{adminTG: errors.New("network")}
	if err := handleAlert(t, n, botDown); err != nil {
		t.Fatalf("partly delivered: %v", err)
	}
	if got = tgf.take(); len(got) != 1 || got[0].chat != ownerTG {
		t.Fatalf("partly delivered: %+v", got)
	}
	// Nobody reachable: retried.
	tgf.fail[ownerTG] = errors.New("network")
	if err := handleAlert(t, n, botDown); err == nil {
		t.Fatal("an alert nobody got was not retried")
	}
	tgf.fail = nil

	set.Replace(map[string]string{"notify.recipients": "staff", "notify.system_alerts": "false"})
	if err := handleAlert(t, n, botDown); err != nil {
		t.Fatal(err)
	}
	if got = tgf.take(); len(got) != 0 {
		t.Fatalf("switched off: %+v", got)
	}

	// A broken payload is dropped, not retried for ever.
	err := n.Handle(context.Background(), eventbus.Message{Topic: events.SystemAlert, Payload: []byte("{")})
	if !eventbus.IsPermanent(err) {
		t.Fatalf("broken payload: %v", err)
	}
}

func TestSystemAlertTexts(t *testing.T) {
	n, _, _ := alertNotifier(t)
	const since = 1760000000
	nov2 := fmt.Sprint(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC).Unix())
	for _, c := range []struct {
		name string
		lang string
		e    events.SystemAlertEvent
		want string
	}{
		{"a server, its name and error escaped", "en", events.SystemAlertEvent{Key: "server.s1", Severity: events.SeverityCritical,
			State: events.AlertProblem, TitleKey: "alert.server_down", Args: map[string]string{"server": "DE <1> & co"},
			Detail: "dial tcp: <refused>", Since: since, At: since + 60},
			"🚨 <b>System alert</b>\nThe 3x-ui server DE &lt;1&gt; &amp; co does not answer.\n<code>dial tcp: &lt;refused&gt;</code>"},
		{"a warning with a size", "fa", events.SystemAlertEvent{Key: "backup.disk", Severity: events.SeverityWarning,
			State: events.AlertProblem, TitleKey: "alert.disk_low", Args: map[string]string{"free": fmt.Sprint(1536 << 20)}, Since: since, At: since},
			"⚠️ <b>اخطار سیستم</b>\nدیسک تقریباً پر است: ۱٫۵ گیگابایت فضای آزاد."},
		{"a count in Persian digits", "fa", events.SystemAlertEvent{Key: "orders.provision_failed", Severity: events.SeverityCritical,
			State: events.AlertProblem, TitleKey: "alert.provision_failed", Args: map[string]string{"count": "12"}, Since: since, At: since},
			"🚨 <b>هشدار سیستم</b>\n۱۲ سفارش پرداخت\u200cشده در یک ساعت گذشته تحویل داده نشد."},
		{"the daily repeat", "en", events.SystemAlertEvent{Key: "license.expiry", Severity: events.SeverityWarning,
			State: events.AlertProblem, TitleKey: "alert.license_expiring", Args: map[string]string{"days": "9", "date": nov2},
			Since: since, At: since + 30*3600, Repeat: true},
			"🔁 <b>Still not fixed after 30 h</b>\nThe license expires in 9 days, on 2 Nov 2026."},
		{"the repeat in Persian, with a Jalali date", "fa", events.SystemAlertEvent{Key: "license.expiry", Severity: events.SeverityWarning,
			State: events.AlertProblem, TitleKey: "alert.license_expiring", Args: map[string]string{"days": "9", "date": nov2},
			Since: since, At: since + 3*86400, Repeat: true},
			"🔁 <b>پس از ۳ روز هنوز برطرف نشده است</b>\nمجوز ۹ روز دیگر، در ۱۱ آبان ۱۴۰۵، منقضی می\u200cشود."},
		{"fixed after minutes, without the old error", "en", events.SystemAlertEvent{Key: "service.bot", Severity: events.SeverityCritical,
			State: events.AlertResolved, TitleKey: "alert.service_down", Args: map[string]string{"service": "bot"},
			Detail: "stale", Since: since, At: since + 3*60},
			"✅ <b>Fixed after 3 min</b>\nThe bot service is down."},
		{"fixed in Persian", "fa", events.SystemAlertEvent{Key: "events.bot_lag", Severity: events.SeverityCritical,
			State: events.AlertResolved, TitleKey: "alert.bot_lag", Args: map[string]string{"minutes": "25"}, Since: since, At: since + 90*60},
			"✅ <b>پس از ۱ ساعت برطرف شد</b>\nربات ۲۵ دقیقه از پیام\u200cهایش (تحویل سرویس، رسیدها، هشدارها) عقب است."},
		{"a title this bot does not know", "en", events.SystemAlertEvent{Key: "new.<check>", Severity: "info",
			State: events.AlertProblem, TitleKey: "alert.from_a_newer_core", Since: since, At: since},
			"⚠️ <b>System warning</b>\nThe new.&lt;check&gt; check reports a problem."},
		{"a text that is not an alert", "en", events.SystemAlertEvent{Key: "x", Severity: events.SeverityCritical,
			State: events.AlertProblem, TitleKey: "welcome", Since: since, At: since},
			"🚨 <b>System alert</b>\nThe x check reports a problem."},
	} {
		if got := n.systemAlert(c.lang, c.e); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestAlertDurations(t *testing.T) {
	n, _, _ := alertNotifier(t)
	for d, want := range map[time.Duration]string{
		0:                 "1 min",
		59 * time.Second:  "1 min",
		59 * time.Minute:  "59 min",
		time.Hour:         "1 h",
		47 * time.Hour:    "47 h",
		48 * time.Hour:    "2 days",
		100 * time.Hour:   "4 days",
		-10 * time.Minute: "1 min",
	} {
		if got := n.duration("en", d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	if got := n.duration("fa", 6*time.Hour); got != "۶ ساعت" {
		t.Errorf("fa: %q", got)
	}
}

func handleDeposit(t *testing.T, n *Notifier, e events.DepositUnmatchedEvent) error {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return n.Handle(context.Background(), eventbus.Message{Topic: events.DepositUnmatched, Payload: payload})
}

const depositTx = "4f1c0e9a5b7d2c3e8f6a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e"

var overpaid = events.DepositUnmatchedEvent{TxID: depositTx, Network: "TRC20", From: "TAuD2W5vggcdy1cNpem4uH3HreRMyPQLHT",
	To: "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r", Amount: 2_600_100, Currency: "USDT",
	BlockTime: time.Date(2026, 11, 2, 9, 5, 0, 0, i18n.CurrentLocation()).Unix(), Reason: events.UnmatchedNoPayment}

// A USDT deposit that matched no payment reaches the staff who get payment
// alerts, each in their language, with the exact amount, the sender, the
// transaction (and a link to it) and why, and is always sent (money nobody
// was credited for); a broken payload is dropped, an alert nobody got is
// retried.
func TestUnmatchedDepositAlert(t *testing.T) {
	n, tgf, set := alertNotifier(t)
	set.SetStaff([]settings.Contact{
		{TelegramID: ownerTG, Language: "fa", Role: "owner"},
		{TelegramID: adminTG, Language: "en", Role: "admin"},
		{TelegramID: supportTG, Language: "en", Role: "support"},
	})
	set.Replace(map[string]string{"notify.recipients": "staff", "notify.system_alerts": "false", "notify.sales": "false"})
	if err := handleDeposit(t, n, overpaid); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 2 || got[0].chat != ownerTG || got[1].chat != adminTG {
		t.Fatalf("recipients: %+v", got)
	}
	want := "⚠️ <b>USDT arrived that matches no payment</b>\nAmount: <code>2.600100</code> USDT\n" +
		"From: <code>TAuD2W5vggcdy1cNpem4uH3HreRMyPQLHT</code>\nTo: <code>TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r</code>\n" +
		"When: 2 Nov 2026, 09:05\nTransaction: <code>" + depositTx + "</code>\n" +
		"Why: No open payment has exactly this amount: underpaid, overpaid, an exchange took its fee, or not one of your customers.\n\n" +
		"Nobody was credited. Find the customer (the amount, the sender, the time), credit their wallet by hand, then mark the deposit handled on the dashboard (Payment gateways)."
	if got[1].text != want {
		t.Fatalf("en text:\n got %q\nwant %q", got[1].text, want)
	}
	fa := got[0].text
	for _, sub := range []string{"<code>2.600100</code> USDT", "۱۱ آبان ۱۴۰۵، ۰۹:۰۵", "هیچ پرداخت بازی دقیقاً این مبلغ را ندارد", "<code>" + depositTx + "</code>"} {
		if !strings.Contains(fa, sub) {
			t.Errorf("fa text has no %q: %q", sub, fa)
		}
	}
	for _, s := range got {
		var link, dash bool
		for _, row := range s.kb.InlineKeyboard {
			for _, b := range row {
				link = link || b.URL == "https://tronscan.org/#/transaction/"+depositTx
				dash = dash || b.CallbackData == "dash"
			}
		}
		if !link || !dash {
			t.Errorf("buttons for %d: %+v", s.chat, s.kb)
		}
	}

	// A second transfer in one transaction is named; a hostile sender, an
	// unknown reason and a transaction id that is not one are escaped and
	// get no link.
	odd := overpaid
	odd.EventIndex, odd.From, odd.Reason, odd.TxID = 1, "<b>T</b>&x", "newer_reason<i>", "<script>"
	set.Replace(map[string]string{})
	if err := handleDeposit(t, n, odd); err != nil {
		t.Fatal(err)
	}
	got = tgf.take()
	if len(got) != 1 || got[0].chat != ownerTG {
		t.Fatalf("to the owner: %+v", got)
	}
	for _, sub := range []string{"<code>&lt;b&gt;T&lt;/b&gt;&amp;x</code>", "newer_reason&lt;i&gt;", "<code>&lt;script&gt;</code> (انتقال ۲ این تراکنش)"} {
		if !strings.Contains(got[0].text, sub) {
			t.Errorf("no %q in %q", sub, got[0].text)
		}
	}
	for _, row := range got[0].kb.InlineKeyboard {
		for _, b := range row {
			if b.URL != "" {
				t.Errorf("a link for a malformed transaction id: %+v", b)
			}
		}
	}
	for _, r := range []string{events.UnmatchedAlreadyPaid, events.UnmatchedClosed, events.UnmatchedAmbiguous} {
		e := overpaid
		e.Reason = r
		if text, _ := n.unmatchedDeposit("en", e); !strings.Contains(text, "Why: "+n.cat.T("en", "admin.unmatched_reason."+r)) ||
			strings.Contains(text, "admin.unmatched_reason") {
			t.Errorf("%s: %q", r, text)
		}
	}

	// Nobody reachable: retried; a broken payload: dropped.
	tgf.fail = map[int64]error{ownerTG: errors.New("network")}
	if err := handleDeposit(t, n, overpaid); err == nil {
		t.Fatal("an alert nobody got was not retried")
	}
	tgf.fail = nil
	if err := n.Handle(context.Background(), eventbus.Message{Topic: events.DepositUnmatched, Payload: []byte("[")}); !eventbus.IsPermanent(err) {
		t.Fatalf("broken payload: %v", err)
	}
}

func handleClaim(t *testing.T, n *Notifier, e events.DepositClaimedEvent) error {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return n.Handle(context.Background(), eventbus.Message{Topic: events.DepositClaimed, Payload: payload})
}

var claimedByProof = events.DepositClaimedEvent{TxID: depositTx, Network: "TRC20", From: "TAuD2W5vggcdy1cNpem4uH3HreRMyPQLHT",
	To: "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r", Amount: 2_600_100, Currency: "USDT",
	BlockTime: time.Date(2026, 11, 2, 9, 5, 0, 0, i18n.CurrentLocation()).Unix(), Reason: events.UnmatchedNoPayment,
	IntentID: "0192f0c8-7b1a-7c3d-9e4f-5a6b7c8d9e0f", Proof: "waiting"}

// A manual USDT proof that names a deposit the watcher saw reaches the staff
// who get payment alerts, each in their language, with the exact amount, the
// sender, the transaction (and a link to it) and the payment's reference,
// and is always sent (anyone may copy a public transaction id); a broken
// payload is dropped, an alert nobody got is retried.
func TestClaimedDepositAlert(t *testing.T) {
	n, tgf, set := alertNotifier(t)
	set.SetStaff([]settings.Contact{
		{TelegramID: ownerTG, Language: "fa", Role: "owner"},
		{TelegramID: adminTG, Language: "en", Role: "admin"},
		{TelegramID: supportTG, Language: "en", Role: "support"},
	})
	set.Replace(map[string]string{"notify.recipients": "staff", "notify.system_alerts": "false", "notify.sales": "false",
		"notify.new_payment": "false"})
	if err := handleClaim(t, n, claimedByProof); err != nil {
		t.Fatal(err)
	}
	got := tgf.take()
	if len(got) != 2 || got[0].chat != ownerTG || got[1].chat != adminTG {
		t.Fatalf("recipients: %+v", got)
	}
	want := "⚠️ <b>A manual USDT proof uses a deposit the watcher saw</b>\n" +
		"A customer sent this transaction as the proof of a manual USDT payment. The automatic USDT watcher had already seen it reach your address.\n" +
		"Amount: <code>2.600100</code> USDT\nFrom: <code>TAuD2W5vggcdy1cNpem4uH3HreRMyPQLHT</code>\n" +
		"Transaction: <code>" + depositTx + "</code>\nPayment reference: <code>7C8D9E0F</code>\n\n" +
		"Anyone can copy a public transaction id. Before approving the proof, check that this customer really sent it. Never credit this deposit by hand."
	if got[1].text != want {
		t.Fatalf("en text:\n got %q\nwant %q", got[1].text, want)
	}
	fa := got[0].text
	for _, sub := range []string{"<code>2.600100</code> USDT", "<code>TAuD2W5vggcdy1cNpem4uH3HreRMyPQLHT</code>",
		"<code>" + depositTx + "</code>", "کد پیگیری پرداخت: <code>7C8D9E0F</code>", "دستی شارژ نکنید"} {
		if !strings.Contains(fa, sub) {
			t.Errorf("fa text has no %q: %q", sub, fa)
		}
	}
	for _, s := range got {
		var link, review bool
		for _, row := range s.kb.InlineKeyboard {
			for _, b := range row {
				link = link || b.URL == "https://tronscan.org/#/transaction/"+depositTx
				review = review || b.CallbackData == "adm:pend"
			}
		}
		if !link || !review {
			t.Errorf("buttons for %d: %+v", s.chat, s.kb)
		}
	}

	// A proof approved already: the dashboard, not the review queue. A second
	// transfer in one transaction is named; a hostile sender and a
	// transaction id that is not one are escaped and get no link.
	odd := claimedByProof
	odd.EventIndex, odd.From, odd.TxID, odd.Proof = 1, "<b>T</b>&x", "<script>", "approved"
	set.Replace(map[string]string{})
	if err := handleClaim(t, n, odd); err != nil {
		t.Fatal(err)
	}
	got = tgf.take()
	if len(got) != 1 || got[0].chat != ownerTG {
		t.Fatalf("to the owner: %+v", got)
	}
	for _, sub := range []string{"<code>&lt;b&gt;T&lt;/b&gt;&amp;x</code>", "<code>&lt;script&gt;</code> (انتقال ۲ این تراکنش)"} {
		if !strings.Contains(got[0].text, sub) {
			t.Errorf("no %q in %q", sub, got[0].text)
		}
	}
	var dash bool
	for _, row := range got[0].kb.InlineKeyboard {
		for _, b := range row {
			if b.URL != "" || b.CallbackData == "adm:pend" {
				t.Errorf("approved proof, malformed transaction id: %+v", b)
			}
			dash = dash || b.CallbackData == "dash"
		}
	}
	if !dash {
		t.Errorf("no dashboard button: %+v", got[0].kb)
	}

	// Nobody reachable: retried; a broken payload: dropped.
	tgf.fail = map[int64]error{ownerTG: errors.New("network")}
	if err := handleClaim(t, n, claimedByProof); err == nil {
		t.Fatal("an alert nobody got was not retried")
	}
	tgf.fail = nil
	if err := n.Handle(context.Background(), eventbus.Message{Topic: events.DepositClaimed, Payload: []byte("[")}); !eventbus.IsPermanent(err) {
		t.Fatalf("broken payload: %v", err)
	}
}
