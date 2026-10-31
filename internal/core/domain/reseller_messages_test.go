package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// A text may use the six placeholders; any other word in braces is refused when it is saved, and a text is shorter than Telegram's limit by
// the room its values take.
func TestCheckResellerText(t *testing.T) {
	v, err := CheckResellerText("fa", "texts.fa", "  سلام {name}، بدهی شما <b>{debt}</b> است ({balance}، {credit_limit}، {tier}، {customers})  ")
	if err != nil || !strings.HasPrefix(v, "سلام {name}") {
		t.Fatalf("every placeholder: %q %v", v, err)
	}
	_, err = CheckResellerText("en", "texts.en", "Hi {name}, you owe {dept} and {Debt} {dept}")
	var te *TextError
	if !errors.As(err, &te) || te.Lang != "en" || te.Key != "texts.en" || len(te.Problems) != 2 {
		t.Fatalf("unknown placeholders: %v", err)
	}
	for i, want := range []string{"{dept}", "{Debt}"} {
		if p := te.Problems[i]; p.Code != i18n.ProblemUnknown || p.Arg != want {
			t.Errorf("problem %d: %+v, want %s", i, p, want)
		}
	}
	if _, err := CheckResellerText("en", "texts.en", "<b>unclosed {debt}"); !errors.As(err, &te) || te.Problems[0].Code != i18n.ProblemTagUnclosed {
		t.Errorf("HTML is checked too: %v", err)
	}
	if _, err := CheckResellerText("en", "texts.en", "  "); !errors.As(err, &te) || te.Problems[0].Code != i18n.ProblemEmpty {
		t.Errorf("an empty text: %v", err)
	}
	// Each placeholder leaves room for its value.
	long := strings.Repeat("a", i18n.MaxMessage-2*resellerValueRoom)
	if _, err := CheckResellerText("en", "texts.en", long+"{debt}{name}"); err == nil {
		t.Error("a text that leaves no room for two values")
	}
	if _, err := CheckResellerText("en", "texts.en", long[:len(long)-12]+"{debt}{name}"); err != nil {
		t.Errorf("a text that leaves the room: %v", err)
	}
	// The live check names the same problems.
	if _, probs := ResellerTextProblems("fa", "{foo}"); len(probs) != 1 || probs[0].Arg != "{foo}" {
		t.Errorf("live check: %+v", probs)
	}
}

// The values are written in the reseller's language as the bot writes money (the store's own name for the Toman), the debt is what they owe
// (0 when nothing), and what the store or the reseller typed is escaped.
func TestRenderResellerText(t *testing.T) {
	c := &store.ResellerContact{TelegramID: 77, Username: "ali", TierName: "<Gold>", Customers: 1234,
		Wallets: []store.Wallet{{Currency: "USDT", Balance: -2_500_000}, {Currency: "IRT", Balance: -125_000, CreditLimit: 500_000}}}
	text := "{name}|{debt}|{balance}|{credit_limit}|{tier}|{customers}"
	if got, want := RenderResellerText(text, "en", c, nil), "@ali|125,000 Toman + 2.5 USDT|-125,000 Toman, -2.5 USDT|500,000 Toman|&lt;Gold&gt;|1,234"; got != want {
		t.Errorf("en:\n got %q\nwant %q", got, want)
	}
	if got, want := RenderResellerText(text, "fa", c, map[string]string{"fa": "تومن"}), "@ali|۱۲۵٬۰۰۰ تومن + ۲٫۵ تتر|-۱۲۵٬۰۰۰ تومن، -۲٫۵ تتر|۵۰۰٬۰۰۰ تومن|&lt;Gold&gt;|۱٬۲۳۴"; got != want {
		t.Errorf("fa:\n got %q\nwant %q", got, want)
	}
	// No debt, no wallet, no username: 0, the IRT wallet as 0, the group name.
	none := &store.ResellerContact{TelegramID: 78, PanelGroup: "SHOP_A"}
	if got, want := RenderResellerText(text, "en", none, nil), "SHOP_A|0 Toman|0 Toman|0 Toman||0"; got != want {
		t.Errorf("nothing owed:\n got %q\nwant %q", got, want)
	}
	if got := RenderResellerText("{name}", "en", &store.ResellerContact{TelegramID: 79}, nil); got != "79" {
		t.Errorf("no username, no group: %q", got)
	}
}

// resellerWorld is a store with an admin, two tiers and resellers in and out of debt.
type resellerWorld struct {
	t     *testing.T
	ctx   context.Context
	svc   *Service
	s     *store.Store
	admin *store.User
	gold  *store.ResellerTier
	plain *store.ResellerTier
}

func newResellerWorld(t *testing.T) *resellerWorld {
	t.Helper()
	svc, s := testService(t)
	ctx := context.Background()
	if err := testdb.Truncate(ctx, s.DB(), `core.reseller_reminder_claims, core.broadcast_recipients, core.broadcasts`); err != nil {
		t.Fatal(err)
	}
	w := &resellerWorld{t: t, ctx: ctx, svc: svc, s: s, admin: staffMember(t, s, 9500, "admin")}
	w.gold = seedTier(t, svc, w.admin, "Gold", 10, 5, nil)
	w.plain = seedTier(t, svc, w.admin, "Plain", 0, 5, nil)
	return w
}

// reseller adds a reseller on tier who reads lang, with a username, owing debt Toman (0: nothing).
func (w *resellerWorld) reseller(tg int64, tier *store.ResellerTier, lang, username string, debt int64) *store.User {
	w.t.Helper()
	u := reseller(w.t, w.svc, w.s, w.admin, tg, tier, 1_000_000)
	if _, err := w.s.DB().Exec(w.ctx, `UPDATE core.users SET language = $2, username = NULLIF($3, '') WHERE id = $1`, u.ID, lang, username); err != nil {
		w.t.Fatal(err)
	}
	if debt > 0 {
		err := w.s.WithTx(w.ctx, func(tx pgx.Tx) error {
			_, err := w.s.Debit(w.ctx, tx, u.ID, "IRT", debt, &store.LedgerEntry{Kind: "adjust", IdempotencyKey: "debt:" + u.ID})
			return err
		})
		if err != nil {
			w.t.Fatal(err)
		}
	}
	return u
}

// start runs the broadcast worker once: due broadcasts start and their recipients are written.
func (w *resellerWorld) start() {
	w.t.Helper()
	if err := w.svc.NewBroadcastWorker(nil).Step(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

// texts are the recipients of a broadcast: Telegram id -> the text written for them.
func (w *resellerWorld) texts(id string) map[int64]string {
	w.t.Helper()
	rows, err := w.s.DB().Query(w.ctx, `SELECT telegram_id, COALESCE(text, '') FROM core.broadcast_recipients WHERE broadcast_id = $1`, id)
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[int64]string{}
	for rows.Next() {
		var (
			tg   int64
			text string
		)
		if err := rows.Scan(&tg, &text); err != nil {
			w.t.Fatal(err)
		}
		out[tg] = text
	}
	return out
}

// The audience: active resellers who have not blocked the bot, all of them, only those in debt, or one tier.
func TestResellerAudience(t *testing.T) {
	w := newResellerWorld(t)
	w.reseller(9501, w.gold, "fa", "a", 125_000)
	w.reseller(9502, w.plain, "en", "b", 0)
	banned := w.reseller(9503, w.gold, "fa", "c", 50_000)
	blocked := w.reseller(9504, w.gold, "fa", "d", 50_000)
	seedUser(t, w.s, 9505) // a customer
	if err := w.s.SetUserStatus(w.ctx, w.s.Conn(), banned.ID, "banned"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.SetBotBlocked(w.ctx, w.s.Conn(), blocked.TelegramID, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		debt          bool
		tier, lang    string
		total, fa, en int64
		name          string
	}{
		{false, "", "", 2, 1, 1, "all"},
		{true, "", "", 1, 1, 0, "in debt"},
		{false, w.plain.ID, "", 1, 0, 1, "one tier"},
		{true, w.plain.ID, "", 0, 0, 0, "in debt on that tier"},
		{false, "", "en", 1, 0, 1, "English only"},
	} {
		total, fa, en, err := w.svc.ResellerAudience(w.ctx, c.debt, c.tier, c.lang)
		if err != nil || total != c.total || fa != c.fa || en != c.en {
			t.Errorf("%s: %d (fa %d, en %d) %v, want %d (%d, %d)", c.name, total, fa, en, err, c.total, c.fa, c.en)
		}
	}
	if _, _, _, err := w.svc.ResellerAudience(w.ctx, false, "not-a-tier", ""); fieldOf(err) != "tier_id" {
		t.Errorf("a bad tier: %v", err)
	}
}

// A message to resellers goes out through the broadcasts: each reseller of the audience gets the text in their language with their own
// figures, the bot is told about the Settle button, and the send is audited. An unknown placeholder or tier is refused and nothing is saved.
func TestSendResellerMessage(t *testing.T) {
	w := newResellerWorld(t)
	w.reseller(9511, w.gold, "fa", "ali", 125_000)
	w.reseller(9512, w.plain, "en", "bob", 40_000)
	w.reseller(9513, w.gold, "en", "cat", 0)

	in := ResellerMessageInput{DebtOnly: true, Texts: map[string]string{"fa": "سلام {name}، بدهی: {debt}", "en": "Hi {name}, you owe {debt} ({tier})"},
		SettleButton: true}
	bad := in
	bad.Texts = map[string]string{"fa": "{debt}", "en": "{owed}"}
	var te *TextError
	if _, err := w.svc.SendResellerMessage(w.ctx, w.admin, bad); !errors.As(err, &te) || te.Problems[0].Arg != "{owed}" {
		t.Fatalf("an unknown placeholder: %v", err)
	}
	bad = in
	bad.TierID = "0190a000-0000-7000-8000-000000000000"
	if _, err := w.svc.SendResellerMessage(w.ctx, w.admin, bad); fieldOf(err) != "tier_id" {
		t.Fatalf("a tier that does not exist: %v", err)
	}
	if n := auditCount(t, w.s, "reseller_message.send"); n != 0 {
		t.Fatalf("refused messages were audited: %d", n)
	}

	b, err := w.svc.SendResellerMessage(w.ctx, w.admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != "scheduled" || b.Segment != store.SegmentResellers || !b.ResellerDebtOnly || !b.SettleButton || b.Kind != "manual" || b.CreatedBy != w.admin.ID {
		t.Fatalf("saved as %+v", b)
	}
	w.start()
	got := w.texts(b.ID)
	want := map[int64]string{9511: "سلام @ali، بدهی: ۱۲۵٬۰۰۰ تومان", 9512: "Hi @bob, you owe 40,000 Toman (Plain)"}
	if len(got) != len(want) || got[9511] != want[9511] || got[9512] != want[9512] {
		t.Fatalf("recipients' texts: %v, want %v", got, want)
	}
	batch, err := w.svc.ClaimBroadcastBatch(w.ctx, 10)
	if err != nil || batch == nil || batch.Broadcast.ID != b.ID || !batch.Broadcast.SettleButton || len(batch.Recipients) != 2 {
		t.Fatalf("claimed: %+v %v", batch, err)
	}
	for _, r := range batch.Recipients {
		if r.Text != want[r.TelegramID] {
			t.Errorf("claimed text of %d: %q", r.TelegramID, r.Text)
		}
	}
	if n := auditCount(t, w.s, "reseller_message.send"); n != 1 {
		t.Errorf("audited sends: %d", n)
	}
	// It is not a broadcast the composer may change.
	if _, err := w.svc.UpdateBroadcast(w.ctx, w.admin, b.ID, BroadcastInput{Segment: "all", Texts: map[string]string{"fa": "x", "en": "x"}}, b.Version); !errors.Is(err, ErrState) {
		t.Errorf("edited in the composer: %v", err)
	}
}

// One tier, and a message in one language: the others of the tier who read the other language get it in the language it was written in.
func TestResellerMessageTierAndLanguage(t *testing.T) {
	w := newResellerWorld(t)
	w.reseller(9521, w.gold, "fa", "", 0)
	w.reseller(9522, w.gold, "en", "", 0)
	w.reseller(9523, w.plain, "fa", "", 0)
	b, err := w.svc.SendResellerMessage(w.ctx, w.admin, ResellerMessageInput{TierID: w.gold.ID, Language: "fa", Texts: map[string]string{"fa": "{tier}: {customers}"}})
	if err != nil {
		t.Fatal(err)
	}
	w.start()
	if got := w.texts(b.ID); len(got) != 1 || got[9521] != "Gold: ۰" {
		t.Fatalf("Persian readers of Gold: %v", got)
	}
}

// The preview is written for the chosen reseller, the first of the audience, or sample values when the store has no reseller.
func TestPreviewResellerText(t *testing.T) {
	w := newResellerWorld(t)
	p, err := w.svc.PreviewResellerText(w.ctx, "", "en", "{name} owes {debt}", false, "")
	if err != nil || p.Reseller != nil || p.Text != "@reseller owes 1,250,000 Toman" {
		t.Fatalf("sample: %+v %v", p, err)
	}
	a := w.reseller(9531, w.gold, "fa", "zed", 0)
	w.reseller(9532, w.gold, "fa", "amy", 70_000)
	if p, err = w.svc.PreviewResellerText(w.ctx, "", "en", "{name} owes {debt}", true, ""); err != nil || p.Text != "@amy owes 70,000 Toman" {
		t.Fatalf("first in debt: %+v %v", p, err)
	}
	if p, err = w.svc.PreviewResellerText(w.ctx, a.ID, "fa", "{name}: {debt}", true, ""); err != nil || p.Text != "@zed: ۰ تومان" || p.Reseller.UserID != a.ID {
		t.Fatalf("chosen: %+v %v", p, err)
	}
	if _, err := w.svc.PreviewResellerText(w.ctx, w.admin.ID, "fa", "{name}", false, ""); fieldOf(err) != "reseller_id" {
		t.Errorf("not a reseller: %v", err)
	}
	var te *TextError
	if _, err := w.svc.PreviewResellerText(w.ctx, "", "fa", "{nme}", false, ""); !errors.As(err, &te) {
		t.Errorf("an unknown placeholder: %v", err)
	}
}

// The reminder's settings: off by default, a text with an unknown placeholder refused, and every change audited.
func TestReminderSettings(t *testing.T) {
	w := newResellerWorld(t)
	st, err := w.svc.ResellerReminderStatus(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := st.Config
	if c.Enabled || c.Weekday != "thursday" || c.Time != "21:00" || !c.DebtOnly || !c.Settle || c.Texts["fa"] != DefaultReminderTexts["fa"] || st.NextDue != nil || st.Last != nil {
		t.Fatalf("defaults: %+v", st)
	}
	for _, l := range []string{"fa", "en"} {
		if _, err := CheckResellerText(l, "x", DefaultReminderTexts[l]); err != nil {
			t.Errorf("the default %s text: %v", l, err)
		}
	}
	var te *TextError
	if _, err := w.svc.SetSettings(w.ctx, w.admin, map[string]string{SettingReminderText + "en": "You owe {amount}"}); !errors.As(err, &te) || te.Problems[0].Arg != "{amount}" {
		t.Fatalf("an unknown placeholder: %v", err)
	}
	if _, err := w.svc.SetSettings(w.ctx, w.admin, map[string]string{SettingReminderWeekday: "someday"}); fieldOf(err) != SettingReminderWeekday {
		t.Fatalf("a weekday: %v", err)
	}
	if _, err := w.svc.SetSettings(w.ctx, w.admin, map[string]string{SettingReminderEnabled: "true", SettingReminderWeekday: "friday",
		SettingReminderTime: "۸:۳۰", SettingReminderText + "en": "Pay {debt}\n\n\n\nplease"}); err != nil {
		t.Fatal(err)
	}
	if st, err = w.svc.ResellerReminderStatus(w.ctx); err != nil || !st.Config.Enabled || st.Config.Weekday != "friday" || st.Config.Time != "08:30" ||
		st.Config.Texts["en"] != "Pay {debt}\n\nplease" || !st.Config.Custom["en"] || st.Config.Custom["fa"] || st.NextDue == nil {
		t.Fatalf("saved: %+v %v", st, err)
	}
	if n := auditCount(t, w.s, "setting.set"); n != 4 {
		t.Errorf("audited changes: %d", n)
	}
}

// reminderAudit is the newest reminder.queue entry's details.
func reminderAudit(t *testing.T, s *store.Store) map[string]any {
	t.Helper()
	var raw []byte
	if err := s.DB().QueryRow(context.Background(), `SELECT after FROM core.audit_log WHERE action = 'reseller_reminder.queue' ORDER BY created_at DESC LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
