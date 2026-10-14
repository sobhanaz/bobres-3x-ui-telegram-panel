package notify

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc"
)

// A day with sales in two currencies: Toman up on the day before, USDT with no
// sales the day before, and staff rejections and failed gateway payments in
// Toman only.
func busyDay() events.DailySummaryEvent {
	return events.DailySummaryEvent{
		TelegramID: ownerTG, Language: "en", Date: "2026-10-13", Zone: "Asia/Tehran", NewCustomers: 3, Trials: 1,
		Currencies: []events.DailySummaryCurrency{
			{
				Currency: "IRT", Revenue: 1_200_000, PrevRevenue: 1_000_000, Orders: 5, NewOrders: 3, Renewals: 2, TrafficTopups: 0,
				Refunds: events.DailySummaryFlow{Count: 1, Amount: 100_000}, Net: 1_100_000,
				Topups:   events.DailySummaryFlow{Count: 2, Amount: 500_000},
				Rejected: events.DailySummaryFlow{Count: 1, Amount: 90_000}, GatewayFailed: events.DailySummaryFlow{Count: 2, Amount: 70_000},
			},
			{Currency: "USDT", Revenue: 2_500_000, Orders: 1, NewOrders: 1, Net: 2_500_000},
		},
	}
}

func handleSummary(t *testing.T, n *Notifier, e events.DailySummaryEvent) error {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return n.Handle(context.Background(), eventbus.Message{Topic: events.DailySummary, Payload: payload})
}

// The message in English: each currency in a block of its own with its own
// unit, the comparison with the day before, the lines for staff rejections and
// failed gateway payments only where there are some.
func TestDailySummaryInEnglish(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	if err := handleSummary(t, n, busyDay()); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"📊 <b>Sales summary</b> · 13 Oct 2026",
		"",
		"<b>Toman</b>",
		"Revenue: <b>1,200,000 Toman</b> (up 20% on the day before)",
		"Paid orders: <b>5</b> (new 3, renewals 2, traffic 0)",
		"Refunds: 1 · 100,000 Toman",
		"Net, after refunds: <b>1,100,000 Toman</b>",
		"Wallet top-ups: 2 · 500,000 Toman",
		"Rejected by staff: 1 · 90,000 Toman",
		"Failed or expired gateway payments: 2 · 70,000 Toman",
		"",
		"<b>USDT</b>",
		"Revenue: <b>2.5 USDT</b> (new: no sales the day before)",
		"Paid orders: <b>1</b> (new 1, renewals 0, traffic 0)",
		"Refunds: 0 · 0 USDT",
		"Net, after refunds: <b>2.5 USDT</b>",
		"Wallet top-ups: 0 · 0 USDT",
		"",
		"New customers: <b>3</b>",
		"Free trials started: <b>1</b>",
		"",
		"<i>Days are read in the time zone Asia/Tehran.</i>",
	}, "\n")
	got := tgf.take()
	if len(got) != 1 || got[0].chat != ownerTG || got[0].text != want || got[0].kb != nil {
		t.Fatalf("sent %+v\nwant %s", got, want)
	}
	// No currency's amount leaks into the other's block.
	toman, usdt, _ := strings.Cut(got[0].text, "<b>USDT</b>")
	if strings.Contains(usdt, "Toman") || strings.Contains(toman, "USDT") {
		t.Errorf("the currencies are mixed:\n%s", got[0].text)
	}
}

// The same in Persian: Persian digits and separators, Toman and Tether named in
// Persian, the day in the Jalali calendar (13 Oct 2026 is 21 Mehr 1405).
func TestDailySummaryInPersian(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	e := busyDay()
	e.Language = "fa"
	if err := handleSummary(t, n, e); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"📊 <b>خلاصهٔ فروش</b> · ۲۱ مهر ۱۴۰۵",
		"",
		"<b>تومان</b>",
		"درآمد: <b>۱٬۲۰۰٬۰۰۰ تومان</b> (۲۰٪ بیشتر از روز قبل)",
		"سفارش\u200cهای پرداخت\u200cشده: <b>۵</b> (جدید ۳، تمدید ۲، ترافیک ۰)",
		"بازپرداخت\u200cها: ۱ · ۱۰۰٬۰۰۰ تومان",
		"خالص، پس از بازپرداخت: <b>۱٬۱۰۰٬۰۰۰ تومان</b>",
		"شارژ کیف پول: ۲ · ۵۰۰٬۰۰۰ تومان",
		"ردشده توسط کارکنان: ۱ · ۹۰٬۰۰۰ تومان",
		"پرداخت\u200cهای ناموفق یا منقضی\u200cشدهٔ درگاه: ۲ · ۷۰٬۰۰۰ تومان",
		"",
		"<b>تتر</b>",
		"درآمد: <b>۲٫۵ تتر</b> (جدید: روز قبل فروشی نبود)",
		"سفارش\u200cهای پرداخت\u200cشده: <b>۱</b> (جدید ۱، تمدید ۰، ترافیک ۰)",
		"بازپرداخت\u200cها: ۰ · ۰ تتر",
		"خالص، پس از بازپرداخت: <b>۲٫۵ تتر</b>",
		"شارژ کیف پول: ۰ · ۰ تتر",
		"",
		"مشتری جدید: <b>۳</b>",
		"آزمایش رایگان شروع\u200cشده: <b>۱</b>",
		"",
		"<i>روزها به وقت منطقهٔ زمانی Asia/Tehran حساب می\u200cشوند.</i>",
	}, "\n")
	got := tgf.take()
	if len(got) != 1 || got[0].text != want {
		t.Fatalf("sent %+v\nwant %s", got, want)
	}
	if m := regexp.MustCompile(`[0-9]`).FindString(strings.ReplaceAll(got[0].text, "Asia/Tehran", "")); m != "" {
		t.Errorf("an ASCII digit %q in the Persian message:\n%s", m, got[0].text)
	}
}

// A day with nothing to report is one line (the owner wants to see the job is
// alive), in each language and whatever the language field says.
func TestDailySummaryOfAQuietDayIsOneLine(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	for lang, want := range map[string]string{
		"en": "📊 No sales on 13 Oct 2026.",
		"fa": "📊 در ۲۱ مهر ۱۴۰۵ فروشی ثبت نشد.",
		"":   "📊 در ۲۱ مهر ۱۴۰۵ فروشی ثبت نشد.", // unknown: the default language, Persian
	} {
		e := events.DailySummaryEvent{TelegramID: ownerTG, Language: lang, Date: "2026-10-13", Zone: "Asia/Tehran"}
		if err := handleSummary(t, n, e); err != nil {
			t.Fatal(err)
		}
		if got := tgf.take(); len(got) != 1 || got[0].text != want {
			t.Errorf("%q: %+v, want %q", lang, got, want)
		}
	}
}

// Free trials and new customers with no sale are still news: the day has a
// heading and the people, and says there were no sales.
func TestDailySummaryOfADayWithOnlyPeople(t *testing.T) {
	n := summaryNotifier(t)
	e := events.DailySummaryEvent{TelegramID: ownerTG, Language: "en", Date: "2026-10-13", Trials: 2}
	got := n.dailySummary("en", e)
	want := "📊 <b>Sales summary</b> · 13 Oct 2026\n\nNo sales.\n\nNew customers: <b>0</b>\nFree trials started: <b>2</b>"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func summaryNotifier(t *testing.T) *Notifier {
	t.Helper()
	n, _, _ := alertNotifier(t)
	return n
}

// The comparison with the day before: a whole percent up or down, "the same"
// only when the amounts are equal and "about the same" under half a percent, a
// fall never past 99% for a day that had sales, a rise past 999% as "more than",
// "new" when the day before had no sales, and nothing when neither day had any.
func TestDailySummaryComparesWithTheDayBefore(t *testing.T) {
	n := summaryNotifier(t)
	for _, c := range []struct {
		rev, prev int64
		en, fa    string
		what      string
	}{
		{1_200, 1_000, "up 20% on the day before", "۲۰٪ بیشتر از روز قبل", "up"},
		{250, 1_000, "down 75% on the day before", "۷۵٪ کمتر از روز قبل", "down"},
		{0, 1_000, "down 100% on the day before", "۱۰۰٪ کمتر از روز قبل", "no sales today: exactly 100% down"},
		{2_000, 1_000_000, "down 99% on the day before", "۹۹٪ کمتر از روز قبل", "99.8% down is not 100%"},
		{1, 1_000_000, "down 99% on the day before", "۹۹٪ کمتر از روز قبل", "a day with sales is never down 100%"},
		{1_000, 1_000, "same as the day before", "برابر با روز قبل", "equal"},
		{1_004, 1_000, "about the same as the day before", "تقریباً برابر با روز قبل", "0.4% up is only about the same"},
		{996, 1_000, "about the same as the day before", "تقریباً برابر با روز قبل", "0.4% down is only about the same"},
		{1_005, 1_000, "up 1% on the day before", "۱٪ بیشتر از روز قبل", "rounds half up"},
		{10_990, 1_000, "up 999% on the day before", "۹۹۹٪ بیشتر از روز قبل", "the biggest rise shown as a number"},
		{11_000, 1_000, "up more than 999% on the day before", "بیش از ۹۹۹٪ بیشتر از روز قبل", "past it"},
		{5_000_000, 1, "up more than 999% on the day before", "بیش از ۹۹۹٪ بیشتر از روز قبل", "5 USDT after 0.000001"},
		{math.MaxInt64, 1, "up more than 999% on the day before", "بیش از ۹۹۹٪ بیشتر از روز قبل", "no overflow"},
		{1_000, 0, "new: no sales the day before", "جدید: روز قبل فروشی نبود", "new"},
		{0, 0, "", "", "nothing either day"},
		{2_500_000_000_000, 1_000_000_000_000, "up 150% on the day before", "۱۵۰٪ بیشتر از روز قبل", "large amounts"},
	} {
		cur := events.DailySummaryCurrency{Currency: "IRT", Revenue: c.rev, PrevRevenue: c.prev}
		if got := n.revenueVs("en", cur); got != c.en {
			t.Errorf("%s, en: %q, want %q", c.what, got, c.en)
		}
		if got := n.revenueVs("fa", cur); got != c.fa {
			t.Errorf("%s, fa: %q, want %q", c.what, got, c.fa)
		}
	}
	// With no comparison the revenue line has no brackets.
	got := n.dailySummary("en", events.DailySummaryEvent{Date: "2026-10-13", Currencies: []events.DailySummaryCurrency{{Currency: "IRT", Refunds: events.DailySummaryFlow{Count: 1, Amount: 5}}}})
	if !strings.Contains(got, "\nRevenue: <b>0 Toman</b>\n") {
		t.Errorf("a currency with a refund and no sales:\n%s", got)
	}
}

// Nothing in the event is trusted as markup: a currency code, the zone and a
// date that is not a date reach Telegram escaped.
func TestDailySummaryEscapesWhatItShows(t *testing.T) {
	n := summaryNotifier(t)
	e := events.DailySummaryEvent{
		Date: "<b>x</b>", Zone: `<a href="x">&`, Trials: 1,
		Currencies: []events.DailySummaryCurrency{{Currency: "<i>X&", Revenue: 5, Orders: 1, Net: 5}},
	}
	for _, lang := range []string{"en", "fa"} {
		text := n.dailySummary(lang, e)
		stripped := regexp.MustCompile(`</?(b|i)>`).ReplaceAllString(text, "") // the texts' own tags
		if strings.ContainsAny(stripped, "<>") || strings.Contains(stripped, "&<") {
			t.Errorf("%s: unescaped markup:\n%s", lang, text)
		}
		for _, want := range []string{"&lt;b&gt;x&lt;/b&gt;", "&lt;a href=&#34;x&#34;&gt;&amp;", "&lt;i&gt;X&amp;"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: %q missing:\n%s", lang, want, text)
			}
		}
	}
}

// blockedCore records the staff members who blocked the bot.
type blockedCore struct {
	*fakeCore
	mu      sync.Mutex
	blocked []int64
}

func (b *blockedCore) SetUserBotBlocked(_ context.Context, in *corev1.SetUserBotBlockedRequest, _ ...grpc.CallOption) (*corev1.SetUserBotBlockedResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if in.GetBlocked() {
		b.blocked = append(b.blocked, in.GetTelegramId())
	}
	return &corev1.SetUserBotBlockedResponse{}, nil
}

// Core writes one event for each staff member. A recipient who blocked the bot
// is skipped (and marked in core, as for any notification) and the rest are
// delivered, each in the language the event names. A send that fails for a reason
// that may pass (the network) is an error for the consumer to retry, in order.
func TestDailySummaryReachesEveryRecipientWhoCanBeReached(t *testing.T) {
	base, tgf, set := alertNotifier(t)
	core := &blockedCore{fakeCore: &fakeCore{}}
	n := New(core, tgf, base.cat, set, ownerTG, nil)
	tgf.fail = map[int64]error{
		adminTG:   &tg.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"},
		supportTG: errors.New("network is down"),
	}
	day := func(chat int64, lang string) events.DailySummaryEvent {
		e := busyDay()
		e.TelegramID, e.Language = chat, lang
		return e
	}
	// The blocked admin's event is done (nothing to retry), the owner gets theirs in Persian.
	if err := handleSummary(t, n, day(adminTG, "en")); err != nil {
		t.Fatalf("a staff member who blocked the bot: %v", err)
	}
	if err := handleSummary(t, n, day(ownerTG, "fa")); err != nil {
		t.Fatalf("the owner: %v", err)
	}
	got := tgf.take()
	if len(got) != 1 || got[0].chat != ownerTG || !strings.Contains(got[0].text, "خلاصهٔ فروش") {
		t.Fatalf("delivered: %+v", got)
	}
	if len(core.blocked) != 1 || core.blocked[0] != adminTG {
		t.Errorf("marked as blocking the bot: %v", core.blocked)
	}
	// A message that fails for another reason is an error, so the consumer
	// retries it (and, being in order, waits for it before the events behind).
	if err := handleSummary(t, n, day(supportTG, "en")); err == nil || !strings.Contains(err.Error(), "network") {
		t.Fatalf("a failing send: %v", err)
	}
	// Back up: the retry of that one event delivers it, in English.
	delete(tgf.fail, supportTG)
	if err := handleSummary(t, n, day(supportTG, "en")); err != nil {
		t.Fatal(err)
	}
	if got = tgf.take(); len(got) != 1 || got[0].chat != supportTG || !strings.Contains(got[0].text, "Sales summary") {
		t.Fatalf("after the retry: %+v", got)
	}
	// An event with no recipient sends nothing, and an undecodable one is permanent.
	if err := handleSummary(t, n, day(0, "en")); err != nil || len(tgf.take()) != 0 {
		t.Errorf("no recipient: %v", err)
	}
	err := n.Handle(context.Background(), eventbus.Message{Topic: events.DailySummary, Payload: []byte(`{"telegram_id": "x"`)})
	if err == nil || !eventbus.IsPermanent(err) {
		t.Errorf("a broken payload: %v", err)
	}
}

// summaryAt handles a summary event queued at createdAt (zero: unknown).
func summaryAt(t *testing.T, n *Notifier, e events.DailySummaryEvent, createdAt time.Time) error {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return n.Handle(context.Background(), eventbus.Message{Topic: events.DailySummary, Payload: payload, CreatedAt: createdAt})
}

// A refusal no retry can change (Telegram answers 400 about this chat or this
// message: PEER_ID_INVALID, no right to write there, a message too long) is
// Permanent, so it is dead-lettered at once; a failure that may pass (the
// network, a flood limit, a server error, a refused token, a reply that is not
// Telegram's) is an ordinary error and is retried.
func TestDailySummaryRefusalsArePermanentAndTransientFailuresAreNot(t *testing.T) {
	base, tgf, set := alertNotifier(t)
	n := New(&blockedCore{fakeCore: &fakeCore{}}, tgf, base.cat, set, ownerTG, nil)
	for _, c := range []struct {
		err       error
		permanent bool
	}{
		{&tg.APIError{Code: 400, Description: "Bad Request: PEER_ID_INVALID"}, true},
		{&tg.APIError{Code: 400, Description: "Bad Request: have no rights to send a message"}, true},
		{&tg.APIError{Code: 400, Description: "Bad Request: message is too long"}, true},
		{&tg.APIError{Code: 429, Description: "Too Many Requests: retry after 5", RetryAfter: 5}, false},
		{&tg.APIError{Code: 500, Description: "Internal Server Error"}, false},
		{&tg.APIError{Code: 502, Description: "Bad Gateway"}, false},
		{&tg.APIError{Code: 401, Description: "Unauthorized"}, false},
		{errors.New("network is down"), false},
	} {
		tgf.fail = map[int64]error{ownerTG: c.err}
		err := handleSummary(t, n, busyDay())
		if err == nil {
			t.Errorf("%v: no error", c.err)
			continue
		}
		if got := eventbus.IsPermanent(err); got != c.permanent {
			t.Errorf("%v: permanent=%v, want %v", c.err, got, c.permanent)
		}
		var ae *tg.APIError
		if c.permanent && !errors.As(err, &ae) {
			t.Errorf("%v: the cause is lost: %v", c.err, err)
		}
	}
	// Blocking the bot is still no error at all.
	tgf.fail = map[int64]error{ownerTG: &tg.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}}
	if err := handleSummary(t, n, busyDay()); err != nil {
		t.Errorf("blocked: %v", err)
	}
}

// memSource is a feed of events in memory for a real consumer.
type memSource struct {
	mu    sync.Mutex
	msgs  []eventbus.Message
	acked int64
}

func (s *memSource) Fetch(_ context.Context, limit int) ([]eventbus.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []eventbus.Message
	for _, m := range s.msgs {
		if m.Seq > s.acked && len(out) < limit {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *memSource) Ack(_ context.Context, upTo int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acked = max(s.acked, upTo)
	return nil
}

// The consumer applies events in order and stops at the first that fails, so a
// summary that cannot be sent holds up every event behind it (a customer's
// subscription link, a payment confirmation) until it works or is dead-lettered.
// With a refusal that no retry changes the admin's summary is dead-lettered at
// once and the owner's, behind it, goes out in the same step; with a failure that
// may pass it is retried and the owner waits.
func TestDailySummaryRefusalDoesNotHoldUpTheEventsBehindIt(t *testing.T) {
	run := func(t *testing.T, failure error) (steps int, dead []string, tgf *fakeTG, handled int, stepErr error) {
		t.Helper()
		n, tgf, _ := alertNotifier(t)
		tgf.fail = map[int64]error{adminTG: failure}
		mk := func(seq int64, id string, chat int64) eventbus.Message {
			e := busyDay()
			e.TelegramID = chat
			payload, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			return eventbus.Message{Seq: seq, EventID: id, Topic: events.DailySummary, Payload: payload, CreatedAt: time.Now()}
		}
		src := &memSource{msgs: []eventbus.Message{mk(1, "admin", adminTG), mk(2, "owner", ownerTG)}}
		c, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
			Name: "test", Source: src, Handle: n.Handle,
			DeadLetter: func(_ context.Context, m eventbus.Message, _ error) error { dead = append(dead, m.EventID); return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		handled, stepErr = c.Step(context.Background())
		return 1, dead, tgf, handled, stepErr
	}
	t.Run("a refusal is dead-lettered at once", func(t *testing.T) {
		_, dead, tgf, handled, err := run(t, &tg.APIError{Code: 400, Description: "Bad Request: PEER_ID_INVALID"})
		if err != nil || handled != 2 {
			t.Fatalf("step: handled %d, err %v", handled, err)
		}
		if len(dead) != 1 || dead[0] != "admin" {
			t.Errorf("dead-lettered: %v", dead)
		}
		if got := tgf.take(); len(got) != 1 || got[0].chat != ownerTG {
			t.Errorf("the owner's summary behind it: %+v", got)
		}
	})
	t.Run("a failure that may pass is retried, in order", func(t *testing.T) {
		_, dead, tgf, handled, err := run(t, &tg.APIError{Code: 500, Description: "Internal Server Error"})
		if err == nil || handled != 0 {
			t.Fatalf("step: handled %d, err %v", handled, err)
		}
		if len(dead) != 0 {
			t.Errorf("dead-lettered at once: %v", dead)
		}
		if got := tgf.take(); len(got) != 0 {
			t.Errorf("the owner's summary went out ahead of the retry: %+v", got)
		}
	})
}

// Core chose the recipient when it claimed the day, but the event can wait (the
// bot or the consumer down, a retry). When the bot knows the staff list, a person
// who is no longer an owner or admin there (demoted to support, banned or removed
// since) is not sent the revenue figures when delivery resumes; a list it does not
// know yet decides nothing.
func TestDailySummaryIsNotSentToWhoIsNoLongerStaff(t *testing.T) {
	n, tgf, set := alertNotifier(t)
	send := func(chat int64) []sentText {
		t.Helper()
		e := busyDay()
		e.TelegramID = chat
		if err := handleSummary(t, n, e); err != nil {
			t.Fatal(err)
		}
		return tgf.take()
	}
	// No staff list known: delivered.
	if got := send(adminTG); len(got) != 1 {
		t.Fatalf("with no list: %+v", got)
	}
	set.SetStaff([]settings.Contact{
		{TelegramID: ownerTG, Language: "fa", Role: "owner"},
		{TelegramID: adminTG, Language: "en", Role: "admin"},
		{TelegramID: supportTG, Language: "en", Role: "support"},
	})
	for chat, want := range map[int64]bool{ownerTG: true, adminTG: true, supportTG: false, 999: false} {
		if got := send(chat); (len(got) == 1) != want {
			t.Errorf("chat %d: delivered %v, want %v (%+v)", chat, len(got) == 1, want, got)
		}
	}
	// An admin who was demoted after the claim.
	set.SetStaff([]settings.Contact{{TelegramID: ownerTG, Role: "owner"}, {TelegramID: adminTG, Role: "support"}})
	if got := send(adminTG); len(got) != 0 {
		t.Errorf("a demoted admin: %+v", got)
	}
}

// Core queues a summary for each day whether or not the bot is consuming. One
// that waited more than a day to be sent (the bot was down for days) is dropped,
// not delivered late, so a bot that comes back sends the newest and not every
// missed day back to back. An event with no queue time is delivered.
func TestDailySummaryThatWaitedTooLongIsDropped(t *testing.T) {
	n, tgf, _ := alertNotifier(t)
	now := time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return now }
	for _, c := range []struct {
		queued time.Time
		sent   bool
		what   string
	}{
		{now.Add(-time.Minute), true, "just queued"},
		{now.Add(-23 * time.Hour), true, "23 hours"},
		{now.Add(-24 * time.Hour), true, "exactly a day"},
		{now.Add(-24*time.Hour - time.Second), false, "just over a day"},
		{now.Add(-35 * time.Hour), false, "yesterday's, queued a day before"},
		{now.Add(-3 * 24 * time.Hour), false, "three days"},
		{time.Time{}, true, "queue time unknown"},
		{now.Add(time.Hour), true, "queued in the future (clocks differ)"},
	} {
		if err := summaryAt(t, n, busyDay(), c.queued); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if got := tgf.take(); (len(got) == 1) != c.sent {
			t.Errorf("%s: delivered %v, want %v", c.what, len(got) == 1, c.sent)
		}
	}
}
