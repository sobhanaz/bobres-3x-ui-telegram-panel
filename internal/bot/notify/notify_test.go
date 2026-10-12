package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

// fakeTG records what was sent; chats in fail refuse with that error.
type fakeTG struct {
	mu   sync.Mutex
	sent []sentText
	fail map[int64]error
}

func (f *fakeTG) SendMessage(_ context.Context, chatID int64, text string, _ *tg.Keyboard) (*tg.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[chatID]; err != nil {
		return nil, err
	}
	f.sent = append(f.sent, sentText{chatID, text})
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
