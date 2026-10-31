package domain

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The quiet hours hold an announcement until they end, over midnight or within one day; equal ends mean none.
func TestAnnounceQuietUntil(t *testing.T) {
	loc := time.FixedZone("IRST", 3*3600+1800)
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, loc) }
	night := AnnounceConfig{QuietStart: 23 * 60, QuietEnd: 8 * 60, Location: loc}
	for _, c := range []struct {
		now, want time.Time
	}{
		{at(10, 22, 59), at(10, 22, 59)}, // before: now
		{at(10, 23, 0), at(11, 8, 0)},    // from the start: tomorrow morning
		{at(10, 23, 45), at(11, 8, 0)},
		{at(11, 2, 30), at(11, 8, 0)}, // after midnight: this morning
		{at(11, 8, 0), at(11, 8, 0)},  // the end itself: now
		{at(11, 12, 0), at(11, 12, 0)},
	} {
		if got := night.QuietUntil(c.now); !got.Equal(c.want) {
			t.Errorf("23:00-08:00 at %s: %s, want %s", c.now.Format("02 15:04"), got.Format("02 15:04"), c.want.Format("02 15:04"))
		}
	}
	day := AnnounceConfig{QuietStart: 60, QuietEnd: 7 * 60, Location: loc}
	if got := day.QuietUntil(at(10, 3, 0)); !got.Equal(at(10, 7, 0)) {
		t.Errorf("01:00-07:00 at 03:00: %s", got)
	}
	if got := day.QuietUntil(at(10, 0, 30)); !got.Equal(at(10, 0, 30)) {
		t.Errorf("01:00-07:00 at 00:30: %s", got)
	}
	none := AnnounceConfig{QuietStart: 600, QuietEnd: 600, Location: loc}
	if got := none.QuietUntil(at(10, 10, 0)); !got.Equal(at(10, 10, 0)) {
		t.Errorf("no quiet hours: %s", got)
	}
}

// A card takes the seven placeholders and nothing else; the values are written in each language, escaped, with the Toman beside a USDT
// price, and the defaults have a line for what the product has.
func TestAnnounceTexts(t *testing.T) {
	if _, err := CheckAnnounceText("en", "texts.en", "{name} {kind} {price} {days} {traffic} {stock} {store}"); err != nil {
		t.Fatalf("every placeholder: %v", err)
	}
	var te *TextError
	if _, err := CheckAnnounceText("fa", "texts.fa", "{name} {prise} {prise}"); !errors.As(err, &te) || len(te.Problems) != 1 || te.Problems[0].Arg != "{prise}" {
		t.Fatalf("an unknown placeholder: %v", err)
	}
	if _, probs := AnnounceTextProblems("en", "<b>{name}"); len(probs) == 0 || probs[0].Code != i18n.ProblemTagUnclosed {
		t.Errorf("HTML is checked: %+v", probs)
	}
	days, traffic := int32(30), int64(50<<30)
	plan := &AnnounceProduct{Kind: AnnouncePlan, Names: map[string]string{"en": "Gold <VIP>", "fa": "طلایی"}, Kinds: map[string]string{"en": "VPN plan", "fa": "سرویس VPN"},
		Currency: "IRT", Price: 120_000, Days: &days, Traffic: &traffic}
	text := "{name}|{kind}|{price}|{days}|{traffic}|{stock}|{store}"
	if got, want := RenderAnnounceText(text, "en", plan, "Shop & Co", nil), "Gold &lt;VIP&gt;|VPN plan|120,000 Toman|30 days|50 GB|—|Shop &amp; Co"; got != want {
		t.Errorf("en:\n got %q\nwant %q", got, want)
	}
	if got, want := RenderAnnounceText("{name}|{price}|{days}", "fa", plan, "", map[string]string{"fa": "تومن"}), "طلایی|۱۲۰٬۰۰۰ تومن|۳۰ روز"; got != want {
		t.Errorf("fa:\n got %q\nwant %q", got, want)
	}
	stock := int32(7)
	lic := &AnnounceProduct{Kind: AnnounceLicense, Names: map[string]string{"en": "BOBRES Pro · 12 months"}, Kinds: map[string]string{"en": "BOBRES license"},
		Currency: "USDT", Price: 40_000_000, Toman: 4_800_000, Months: 12, Stock: &stock}
	if got, want := RenderAnnounceText("{price}|{days}|{traffic}|{stock}", "en", lic, "", nil), "40 USDT ≈ 4,800,000 Toman|12 months|Unlimited|7"; got != want {
		t.Errorf("a license:\n got %q\nwant %q", got, want)
	}
	def := DefaultAnnounceTexts(plan)
	for _, l := range i18n.Languages {
		if _, err := CheckAnnounceText(l, "texts."+l, def[l]); err != nil || !strings.Contains(def[l], "{traffic}") || strings.Contains(def[l], "{stock}") {
			t.Errorf("the default %s card of a plan: %q %v", l, def[l], err)
		}
	}
	sup := DefaultAnnounceTexts(&AnnounceProduct{Kind: AnnounceSupplier, Stock: &stock})
	if !strings.Contains(sup["en"], "{stock}") || strings.Contains(sup["en"], "{days}") {
		t.Errorf("the default card of a digital account: %q", sup["en"])
	}
}

// fakeChannel is the bot's side of channel posts.
type fakeChannel struct {
	mu    sync.Mutex
	posts []ChannelPost
	res   *ChannelPostResult
	err   error
}

func (f *fakeChannel) PostChannel(_ context.Context, p ChannelPost) (*ChannelPostResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, p)
	if f.err != nil {
		return nil, f.err
	}
	if f.res != nil {
		return f.res, nil
	}
	return &ChannelPostResult{OK: true, MessageID: 501}, nil
}

func announceTexts() map[string]string {
	return map[string]string{"fa": "🆕 {name} — {price}", "en": "🆕 {name} — {price} ({days}, {traffic})"}
}

// An announcement goes out through the broadcasts to the customers who said yes (active, not blocking the bot), each in their language with
// a Buy button that opens the plan; the channel post goes through the bot with a start link. The limits hold: off by default, the same
// product not twice in 24 hours unless forced, a few a day, and the quiet hours queue it until they end. Sends and channel posts are audited.
func TestSendAnnouncement(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	staff := owner(t, svc)
	ch := &fakeChannel{}
	svc.SetAnnounceTransport(ch)
	clock := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) // 15:30 in Tehran
	svc.SetClock(func() time.Time { return clock })
	plan := seedPlan(t, s, false, 120_000)
	in := AnnounceInput{Kind: AnnouncePlan, ProductID: plan.ID, Texts: announceTexts(), Private: true}

	if _, err := svc.SendAnnouncement(ctx, staff, in); !errors.Is(err, ErrState) {
		t.Fatalf("while announcements are off: %v", err)
	}
	setSetting(t, s, SettingAnnounceEnabled, "true")

	// Who gets it: yes in either language; not the ones who said no, never answered, are banned or block the bot.
	yesFa, yesEn := seedUser(t, s, 7001), seedUser(t, s, 7002)
	if _, err := s.DB().Exec(ctx, `UPDATE core.users SET language = 'en' WHERE id = $1`, yesEn.ID); err != nil {
		t.Fatal(err)
	}
	no, silent, banned, blocking := seedUser(t, s, 7003), seedUser(t, s, 7004), seedUser(t, s, 7005), seedUser(t, s, 7006)
	_ = silent
	for _, u := range []*store.User{yesFa, yesEn, banned, blocking} {
		if _, err := svc.SetAnnounceOptIn(ctx, u.ID, 0, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SetAnnounceOptIn(ctx, "", no.TelegramID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.users SET status = 'banned' WHERE id = $1`, banned.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.users SET bot_blocked_at = now() WHERE id = $1`, blocking.ID); err != nil {
		t.Fatal(err)
	}
	if total, _, _, err := svc.AnnounceAudience(ctx); err != nil || total != 2 {
		t.Fatalf("audience: %d %v", total, err)
	}

	// A card with a placeholder core does not know, or no text in a language, is refused.
	bad := in
	bad.Texts = map[string]string{"fa": "{name}", "en": "{nme}"}
	var te *TextError
	if _, err := svc.SendAnnouncement(ctx, staff, bad); !errors.As(err, &te) || te.Lang != "en" {
		t.Fatalf("an unknown placeholder: %v", err)
	}
	bad.Texts = map[string]string{"fa": "{name}"}
	if _, err := svc.SendAnnouncement(ctx, staff, bad); !errors.As(err, &te) || te.Lang != "en" {
		t.Fatalf("no English card: %v", err)
	}
	if _, err := svc.SendAnnouncement(ctx, staff, AnnounceInput{Kind: AnnouncePlan, ProductID: plan.ID, Texts: announceTexts()}); err == nil {
		t.Fatal("neither the channel nor private messages")
	}
	if _, err := svc.SendAnnouncement(ctx, staff, AnnounceInput{Kind: AnnouncePlan, ProductID: plan.ID, Texts: announceTexts(), Channel: true}); err == nil {
		t.Fatal("the channel while none is set")
	}

	res, err := svc.SendAnnouncement(ctx, staff, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued || res.Audience != 2 || res.Announcement.Status != "queued" || !res.Announcement.DueAt.Equal(clock) {
		t.Fatalf("sent at 15:30: %+v %+v", res, res.Announcement)
	}
	w := svc.NewAnnouncementWorker(nil)
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAnnouncement(ctx, s.Conn(), res.Announcement.ID, false)
	if err != nil || a.Status != "sent" || a.BroadcastID == "" || a.ChannelStatus != "none" {
		t.Fatalf("after the worker: %+v %v", a, err)
	}
	b, err := s.GetBroadcast(ctx, s.Conn(), a.BroadcastID, false)
	if err != nil || b.Segment != store.SegmentAnnounce || b.Status != "scheduled" || b.BuyCallback != "an:b:p:"+strings.ReplaceAll(plan.ID, "-", "") {
		t.Fatalf("the private messages: %+v %v", b, err)
	}
	if b.Texts["fa"] != "🆕 P — ۱۲۰٬۰۰۰ تومان" || !strings.HasPrefix(b.Texts["en"], "🆕 P — 120,000 Toman (No time limit, Unlimited)") {
		t.Fatalf("the cards as written: %q", b.Texts)
	}
	if err := svc.NewBroadcastWorker(nil).Step(ctx); err != nil {
		t.Fatal(err)
	}
	batch, err := svc.ClaimBroadcastBatch(ctx, 10)
	if err != nil || batch == nil || len(batch.Recipients) != 2 || batch.Broadcast.BuyCallback == "" {
		t.Fatalf("the batch: %+v %v", batch, err)
	}
	got := map[int64]string{}
	for _, r := range batch.Recipients {
		got[r.TelegramID] = r.Language
	}
	if got[7001] != "fa" || got[7002] != "en" {
		t.Fatalf("recipients: %v", got)
	}

	// The same product again within 24 hours: refused, unless forced.
	clock = clock.Add(time.Hour)
	var rep *AnnounceRepeatError
	if _, err := svc.SendAnnouncement(ctx, staff, in); !errors.As(err, &rep) || !rep.Last.Equal(clock.Add(-time.Hour)) {
		t.Fatalf("again within 24 hours: %v", err)
	}
	forced := in
	forced.Force = true
	if r, err := svc.SendAnnouncement(ctx, staff, forced); err != nil || !r.Announcement.Forced {
		t.Fatalf("forced: %+v %v", r, err)
	}

	// The channel (the required one, as nothing else is set): posted by the bot with a start link to the plan, in the default language.
	setSetting(t, s, "join.channel", "@mystore")
	other := seedPlan(t, s, false, 50_000)
	res, err = svc.SendAnnouncement(ctx, staff, AnnounceInput{Kind: AnnouncePlan, ProductID: other.ID, Texts: announceTexts(), Channel: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ch.posts) != 1 || ch.posts[0].Chat != "@mystore" || ch.posts[0].Start != "p_"+strings.ReplaceAll(other.ID, "-", "") ||
		ch.posts[0].Text != "🆕 P — ۵۰٬۰۰۰ تومان" || ch.posts[0].Label != "🛒 خرید" {
		t.Fatalf("the channel post: %+v", ch.posts)
	}
	if a, _ := s.GetAnnouncement(ctx, s.Conn(), res.Announcement.ID, false); a.ChannelStatus != "sent" || a.ChannelMessageID != 501 || a.BroadcastID != "" {
		t.Fatalf("after the post: %+v", a)
	}

	// The daily limit (3 by default) is reached: a fourth one today is refused.
	third := seedPlan(t, s, false, 1)
	var lim *AnnounceLimitError
	if _, err := svc.SendAnnouncement(ctx, staff, AnnounceInput{Kind: AnnouncePlan, ProductID: third.ID, Texts: announceTexts(), Private: true}); !errors.As(err, &lim) || lim.Max != 3 {
		t.Fatalf("a fourth one today: %v", err)
	}

	// In the quiet hours (23:00 to 08:00 Tehran): queued until 08:00, which is tomorrow's count; a refusal of the bot is recorded.
	clock = time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC) // 23:30 in Tehran
	ch.res = &ChannelPostResult{Problem: "not_admin", Detail: "Forbidden: bot is not a member of the channel chat"}
	res, err = svc.SendAnnouncement(ctx, staff, AnnounceInput{Kind: AnnouncePlan, ProductID: third.ID, Texts: announceTexts(), Private: true, Channel: true})
	if err != nil {
		t.Fatal(err)
	}
	morning := time.Date(2026, 10, 31, 4, 30, 0, 0, time.UTC) // 08:00 in Tehran
	if !res.Queued || !res.Announcement.DueAt.Equal(morning) {
		t.Fatalf("in the quiet hours: queued %v until %s", res.Queued, res.Announcement.DueAt)
	}
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.GetAnnouncement(ctx, s.Conn(), res.Announcement.ID, false); a.Status != "queued" || len(ch.posts) != 1 {
		t.Fatalf("before the morning: %+v", a)
	}
	clock = morning
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAnnouncement(ctx, s.Conn(), res.Announcement.ID, false)
	if a.Status != "sent" || a.BroadcastID == "" || a.ChannelStatus != "failed" || !strings.Contains(a.ChannelError, "not_admin") {
		t.Fatalf("in the morning: %+v", a)
	}
	for action, want := range map[string]int{"announcement.send": 4, "announcement.start": 4, "announcement.channel": 2} {
		if n := auditCount(t, s, action); n != want {
			t.Errorf("%s: %d audit entries, want %d", action, n, want)
		}
	}

	// A queued one can be cancelled; a sent one not.
	clock = time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	fourth := seedPlan(t, s, false, 2)
	res, err = svc.SendAnnouncement(ctx, staff, AnnounceInput{Kind: AnnouncePlan, ProductID: fourth.ID, Texts: announceTexts(), Private: true})
	if err != nil || !res.Queued {
		t.Fatalf("queued: %+v %v", res, err)
	}
	if c, err := svc.CancelAnnouncement(ctx, staff, res.Announcement.ID); err != nil || c.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", c, err)
	}
	if _, err := svc.CancelAnnouncement(ctx, staff, a.ID); !errors.Is(err, ErrState) {
		t.Fatalf("cancel a sent one: %v", err)
	}
	list, total, err := svc.Announcements(ctx, AnnouncePlan, "", store.Page{Limit: 50})
	if err != nil || total != 5 || len(list) != 5 {
		t.Fatalf("the list: %d %v", total, err)
	}
}

// What cannot be bought is not announced: a plan switched off, a trial, a traffic package, an unknown plan; digital accounts and licenses
// while their sales are off.
func TestAnnounceProductOf(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	trial := seedPlan(t, s, true, 0)
	off := seedPlan(t, s, false, 10)
	off.Enabled = false
	if err := s.UpsertPlan(ctx, s.Conn(), off); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"trial": trial.ID, "off": off.ID, "unknown": "00000000-0000-7000-8000-000000000001", "garbage": "x"} {
		var fe *FieldError
		if _, err := svc.AnnounceProductOf(ctx, AnnouncePlan, id); !errors.As(err, &fe) {
			t.Errorf("%s plan: %v", name, err)
		}
	}
	var fe *FieldError
	if _, err := svc.AnnounceProductOf(ctx, AnnounceSupplier, "abc"); !errors.As(err, &fe) {
		t.Errorf("digital accounts off: %v", err)
	}
	if _, err := svc.AnnounceProductOf(ctx, AnnounceLicense, "pro:12"); !errors.As(err, &fe) {
		t.Errorf("license sales off: %v", err)
	}
	if _, err := svc.AnnounceProductOf(ctx, AnnounceLicense, "gold:7"); !errors.As(err, &fe) {
		t.Errorf("not a license offer: %v", err)
	}
	if _, err := svc.AnnounceProductOf(ctx, "car", "1"); !errors.As(err, &fe) || fe.Field != "kind" {
		t.Errorf("an unknown kind: %v", err)
	}
}

// The bot asks a customer once, only while announcements are on; an answer (in the Mini App too) counts as asked.
func TestAskAnnounceOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 7101)
	if ask, err := svc.AskAnnounce(ctx, u.TelegramID); err != nil || ask {
		t.Fatalf("while off: %v %v", ask, err)
	}
	setSetting(t, s, SettingAnnounceEnabled, "true")
	if ask, err := svc.AskAnnounce(ctx, u.TelegramID); err != nil || !ask {
		t.Fatalf("the first time: %v %v", ask, err)
	}
	if ask, _ := svc.AskAnnounce(ctx, u.TelegramID); ask {
		t.Fatal("asked twice")
	}
	got, err := s.GetUser(ctx, s.Conn(), u.ID)
	if err != nil || got.AnnounceAskedAt == nil || got.AnnounceOptIn != nil {
		t.Fatalf("after the question: %+v %v", got, err)
	}
	answered := seedUser(t, s, 7102)
	if u, err := svc.SetAnnounceOptIn(ctx, answered.ID, 0, true); err != nil || u.AnnounceOptIn == nil || !*u.AnnounceOptIn || u.AnnounceAskedAt == nil {
		t.Fatalf("an answer in the Mini App: %+v %v", u, err)
	}
	if ask, _ := svc.AskAnnounce(ctx, answered.TelegramID); ask {
		t.Fatal("asked after answering")
	}
	if _, err := svc.SetAnnounceOptIn(ctx, "", 999999, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown user: %v", err)
	}
}
