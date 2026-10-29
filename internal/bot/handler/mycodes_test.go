package handler

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// My codes: the Wallet screen's button and /codes list the customer's own reward codes and the gift codes they applied, newest first, five to
// a page, and nobody else's.

// reward gives the customer a reward code of pct percent (or amount Toman, when pct is 0) that ends in endsIn (0: never), made by a purchase
// of plan (empty: by none), dated at.
func (w *world) reward(tg int64, code string, pct int32, amount int64, endsIn time.Duration, plan string, at time.Time) {
	w.t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	u := w.storeUser(tg)
	d := &corestore.Discount{Code: code, UserID: &u.ID}
	if pct > 0 {
		d.Percent = &pct
	} else {
		cur := "IRT"
		d.Amount, d.Currency = &amount, &cur
	}
	if endsIn != 0 {
		end := time.Now().Add(endsIn)
		d.ExpiresAt = &end
	}
	if plan != "" {
		var planID, orderID string
		if err := st.DB().QueryRow(ctx, `INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency)
			VALUES (gen_random_uuid(), jsonb_build_object('en', $1::text, 'fa', $1::text), 'time', 30, 1000, 'IRT') RETURNING id::text`, plan).Scan(&planID); err != nil {
			w.t.Fatal(err)
		}
		if err := st.DB().QueryRow(ctx, `INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES (gen_random_uuid(), $1, $2, 'new', 'active', 1000, 'IRT', $3) RETURNING id::text`, u.ID, planID, "src-"+code).Scan(&orderID); err != nil {
			w.t.Fatal(err)
		}
		d.SourceOrderID = &orderID
	}
	if made, err := st.InsertRewardCode(ctx, st.Conn(), d); err != nil || !made {
		w.t.Fatalf("reward code %s: %v %v", code, made, err)
	}
	w.helpSQL(`UPDATE core.discount_codes SET created_at = $2 WHERE code = $1`, code, at)
}

// applyGift has the customer apply a new gift code worth amount, on the day at; it returns the last characters of the code.
func (w *world) applyGift(tg, amount int64, uses int, at time.Time) string {
	w.t.Helper()
	made, err := w.stack.Domain.CreateGiftCodes(context.Background(), w.storeUser(ownerID), coredomain.GiftCodeSpec{Amount: amount, Currency: "IRT", MaxRedemptions: uses})
	if err != nil {
		w.t.Fatal(err)
	}
	w.applySame(tg, made[0], at)
	return made[0].Tail
}

// applySame has the customer apply a gift code made before.
func (w *world) applySame(tg int64, g coredomain.NewGiftCode, at time.Time) {
	w.t.Helper()
	u := w.storeUser(tg)
	if _, err := w.stack.Domain.RedeemGiftCode(context.Background(), coredomain.RedeemGiftCodeParams{UserID: u.ID, Code: g.Code, RequestKey: fmt.Sprintf("k-%d-%s", tg, g.ID)}); err != nil {
		w.t.Fatal(err)
	}
	w.helpSQL(`UPDATE core.gift_code_redemptions SET created_at = $3 WHERE user_id = $1 AND gift_code_id = $2`, u.ID, g.ID, at)
}

// codeBlocks are the codes of a "My codes" screen, one per paragraph after the heading.
func codeBlocks(m *msg) []string {
	parts := strings.Split(m.text, "\n\n")
	if len(parts) < 3 {
		return nil
	}
	var out []string
	for _, p := range parts[2:] {
		if strings.HasPrefix(p, "💡") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// The Wallet screen has the button beside Apply gift code, and /codes does the same: with no code yet, the screen says how they come.
func TestMyCodesButtonAndCommand(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7900000001, "empty")

	u.press("wallet")
	if cb := callbacks(u.shown()); !slices.Equal(cb, []string{"wtop", "whist", "gift", "cds", "home"}) {
		t.Fatalf("the wallet's buttons: %v", cb)
	}
	u.lastButton("🎟 My codes")
	u.press("cds")
	byButton := u.shown()
	if !strings.HasPrefix(byButton.text, "🎟 <b>My codes</b>\n\nYou have no codes yet.") || !slices.Equal(callbacks(byButton), []string{"wallet", "home"}) {
		t.Fatalf("with no code: %q %v", byButton.text, callbacks(byButton))
	}
	// The command, in every way it can be typed, is the same screen.
	for _, text := range []string{"/codes", "/CODES", "/codes@test_vpn_bot"} {
		u.text(text)
		if got := u.last(); got.text != byButton.text || !slices.Equal(callbacks(got), callbacks(byButton)) {
			t.Fatalf("%s: %q %v", text, got.text, callbacks(got))
		}
	}
	// The gift button goes when the owner switches gift codes off; My codes stays.
	w.setInCore("gift_codes.enabled", "false")
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	u.press("home")
	u.press("wallet")
	if cb := callbacks(u.shown()); !slices.Equal(cb, []string{"wtop", "whist", "cds", "home"}) {
		t.Fatalf("the wallet's buttons with gift codes off: %v", cb)
	}
}

// A customer sees their own codes and nobody else's, however many customers have codes: each list is built for the customer who sent the
// update, a reward code is whole in a code span, and a gift code is only its last characters.
func TestMyCodesAreOnlyTheirOwn(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	now := time.Now()
	ali, sara, nika := w.customer(7900000011, "ali"), w.customer(7900000012, "sara"), w.customer(7900000013, "nika")
	w.reward(7900000011, "THX-ALIAAAA2", 20, 0, 5*24*time.Hour, "Monthly 50GB", now.Add(-3*time.Hour))
	w.reward(7900000012, "THX-SARAAAA2", 30, 0, 5*24*time.Hour, "", now.Add(-2*time.Hour))
	shared := w.applyGift(7900000011, 25_000, 1, now.Add(-90*time.Minute))
	// One gift code that Ali and Sara both applied.
	made, err := w.stack.Domain.CreateGiftCodes(context.Background(), w.storeUser(ownerID), coredomain.GiftCodeSpec{Amount: 40_000, Currency: "IRT", MaxRedemptions: 2})
	if err != nil {
		t.Fatal(err)
	}
	w.applySame(7900000011, made[0], now.Add(-60*time.Minute))
	w.applySame(7900000012, made[0], now.Add(-30*time.Minute))
	// What belongs to no one: staff's own discount code, and a gift code nobody applied.
	pct := int32(25)
	if _, err := w.dom().UpsertDiscount(context.Background(), w.storeUser(ownerID), &corestore.Discount{Code: "SPRING25", Percent: &pct, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	unclaimed, err := w.stack.Domain.CreateGiftCodes(context.Background(), w.storeUser(ownerID), coredomain.GiftCodeSpec{Amount: 99_000, Currency: "IRT", MaxRedemptions: 1})
	if err != nil {
		t.Fatal(err)
	}

	ali.text("/codes")
	screen := ali.last().text
	for _, want := range []string{"<code>THX-ALIAAAA2</code>", "<b>20% off</b> any plan · <b>Not used yet</b>", "Reward for buying " + isolated("Monthly 50GB"),
		"Gift code ending in <code>" + shared + "</code>", "25,000 Toman added to your wallet · <b>Used by you</b>\nApplied on " + i18n.Date("en", now.Add(-90*time.Minute)),
		"Gift code ending in <code>" + made[0].Tail + "</code>", "40,000 Toman added to your wallet"} {
		if !strings.Contains(screen, want) {
			t.Errorf("Ali's screen lacks %q:\n%s", want, screen)
		}
	}
	for _, leak := range []string{"SARAAAA2", "SPRING25", unclaimed[0].Tail, "99,000", "30%"} {
		if strings.Contains(screen, leak) {
			t.Errorf("Ali's screen has %q:\n%s", leak, screen)
		}
	}
	// A gift code is never sent whole.
	for _, c := range []string{made[0].Code, strings.ReplaceAll(made[0].Code, "-", "")} {
		if strings.Contains(strings.ToUpper(screen), c) {
			t.Errorf("a gift code is on the screen: %s", c)
		}
	}
	if got := len(codeBlocks(ali.last())); got != 3 {
		t.Errorf("Ali has 3 codes, the screen shows %d:\n%s", got, screen)
	}

	sara.text("/codes")
	saraScreen := sara.last().text
	if !strings.Contains(saraScreen, "<code>THX-SARAAAA2</code>") || !strings.Contains(saraScreen, "<b>30% off</b> any plan") ||
		!strings.Contains(saraScreen, "40,000 Toman added") || len(codeBlocks(sara.last())) != 2 {
		t.Errorf("Sara's screen:\n%s", saraScreen)
	}
	for _, leak := range []string{"ALIAAAA2", "20%", shared, "25,000", "Monthly 50GB", "SPRING25"} {
		if strings.Contains(saraScreen, leak) {
			t.Errorf("Sara's screen has %q:\n%s", leak, saraScreen)
		}
	}

	// Nika has none; a button of Ali's, pressed by Nika, still shows Nika's own list.
	nika.text("/codes")
	nika.lastHas("You have no codes yet")
	nika.tap("cds:1")
	if strings.Contains(nika.allText(), "THX-") || strings.Contains(nika.allText(), shared) {
		t.Errorf("Nika was shown a code:\n%s", nika.allText())
	}
}

// Newest first, five to a page, with Previous and Next, the page count in the heading, and a button past the end shows the last page.
func TestMyCodesArePaged(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7900000021, "many")
	base := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	for i := range 7 {
		w.reward(7900000021, fmt.Sprintf("THX-PAGE%04d", i), 10, 0, 5*24*time.Hour, "", base.Add(time.Duration(i)*time.Minute))
	}

	u.text("/codes")
	first := u.last()
	if !strings.HasPrefix(first.text, "🎟 <b>My codes</b> · page 1 of 2\n\n") {
		t.Fatalf("the heading: %q", first.text)
	}
	var shown []string
	for _, m := range regexp.MustCompile(`<code>(THX-PAGE\d{4})</code>`).FindAllStringSubmatch(first.text, -1) {
		shown = append(shown, m[1])
	}
	if !slices.Equal(shown, []string{"THX-PAGE0006", "THX-PAGE0005", "THX-PAGE0004", "THX-PAGE0003", "THX-PAGE0002"}) {
		t.Fatalf("page 1 (newest first): %v", shown)
	}
	if cb := callbacks(first); !slices.Equal(cb, []string{"cds:2", "buy", "wallet", "home"}) {
		t.Fatalf("page 1 buttons: %v", cb)
	}
	u.lastButton("Next »")

	u.press("cds:2")
	second := u.shown()
	if !strings.HasPrefix(second.text, "🎟 <b>My codes</b> · page 2 of 2\n\n") || len(codeBlocks(second)) != 2 ||
		!strings.Contains(second.text, "THX-PAGE0001") || !strings.Contains(second.text, "THX-PAGE0000") || strings.Contains(second.text, "THX-PAGE0002") {
		t.Fatalf("page 2: %q", second.text)
	}
	if cb := callbacks(second); !slices.Equal(cb, []string{"cds:1", "buy", "wallet", "home"}) {
		t.Fatalf("page 2 buttons: %v", cb)
	}
	u.lastButton("« Previous")
	u.press("cds:1")
	if u.shown().text != first.text {
		t.Fatal("Previous did not come back to the first page")
	}
	// A stale or crafted page past the end is the last page; one that is no number is the first.
	u.tap("cds:99")
	if !strings.HasPrefix(u.shown().text, "🎟 <b>My codes</b> · page 2 of 2") {
		t.Fatalf("past the end: %q", u.shown().text)
	}
	u.tap("cds:x")
	if !strings.HasPrefix(u.shown().text, "🎟 <b>My codes</b> · page 1 of 2") {
		t.Fatalf("not a page: %q", u.shown().text)
	}

	// With five or fewer there is no page count and no Previous or Next.
	few := w.customer(7900000022, "few")
	w.reward(7900000022, "THX-FEW00001", 10, 0, 5*24*time.Hour, "", base)
	few.text("/codes")
	if strings.Contains(few.last().text, "page") || !slices.Equal(callbacks(few.last()), []string{"buy", "wallet", "home"}) {
		t.Fatalf("one page: %q %v", few.last().text, callbacks(few.last()))
	}
}

// Each state is told in words: not used yet (with its date), used (with the day), expired, switched off; an amount code says its amount; a
// code made before reward codes were for any plan names its plan; and a Buy button is there only while a code can still be used.
func TestMyCodesStates(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7900000031, "states")
	now := time.Now()
	w.reward(7900000031, "THX-USEDCODE", 10, 0, 5*24*time.Hour, "", now.Add(-5*time.Hour))
	w.reward(7900000031, "THX-OLDCODE2", 15, 0, -2*time.Hour, "", now.Add(-4*time.Hour))
	w.reward(7900000031, "THX-OFFCODE2", 20, 0, 5*24*time.Hour, "", now.Add(-3*time.Hour))
	w.reward(7900000031, "THX-AMOUNT22", 0, 40_000, 0, "Weekly 20GB", now.Add(-2*time.Hour))
	w.reward(7900000031, "THX-LEGACY22", 12, 0, 3*24*time.Hour, "", now.Add(-1*time.Hour))
	w.helpSQL(`UPDATE core.discount_codes SET enabled = false WHERE code = 'THX-OFFCODE2'`)
	w.helpSQL(`UPDATE core.discount_codes SET plan_id = (SELECT id FROM core.plans WHERE name_i18n->>'en' = 'Weekly 20GB' LIMIT 1) WHERE code = 'THX-LEGACY22'`)
	// Used: a redemption by a paid order, as the order flow records it.
	used := w.storeUser(7900000031)
	if ok, err := w.stack.Store.RecordRedemption(context.Background(), w.stack.Store.Conn(), "THX-USEDCODE", used.ID, w.someOrder(7900000031)); err != nil || !ok {
		t.Fatalf("redemption: %v %v", ok, err)
	}

	u.text("/codes")
	blocks := map[string]string{}
	for _, b := range codeBlocks(u.last()) {
		if m := regexp.MustCompile(`<code>(THX-[A-Z0-9]+)</code>`).FindStringSubmatch(b); m != nil {
			blocks[m[1]] = b
		}
	}
	day := func(d time.Duration) string { return i18n.Date("en", now.Add(d)) }
	for code, wants := range map[string][]string{
		"THX-USEDCODE": {"✅ <code>", "<b>10% off</b> any plan · <b>Used</b>", "Used on " + day(0), "Reward code · "},
		"THX-OLDCODE2": {"⌛ <code>", "<b>15% off</b> any plan · <b>Expired</b>", "Expired on " + day(-2*time.Hour)},
		"THX-OFFCODE2": {"⛔ <code>", "<b>20% off</b> any plan · <b>Switched off</b>", "Switched off by the store"},
		"THX-AMOUNT22": {"🟢 <code>", "<b>40,000 Toman off</b> any plan · <b>Not used yet</b>", "No expiry date", "Reward for buying " + isolated("Weekly 20GB")},
		"THX-LEGACY22": {"🟢 <code>", "<b>12% off</b> " + isolated("Weekly 20GB") + " only · <b>Not used yet</b>"},
	} {
		b, ok := blocks[code]
		if !ok {
			t.Errorf("no entry for %s in %q", code, u.last().text)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(b, want) {
				t.Errorf("%s lacks %q:\n%s", code, want, b)
			}
		}
	}
	// The newest is first; and there is a Buy button, since one code can be used.
	if !strings.Contains(strings.SplitN(u.last().text, "<code>", 2)[1], "THX-LEGACY22") || !hasCallback(u.last(), "buy") ||
		!strings.Contains(u.last().text, "💡 To use a discount code") {
		t.Errorf("the screen:\n%s\n%v", u.last().text, callbacks(u.last()))
	}

	// With every code spent, expired or off there is nothing to use: no Buy button, no hint.
	w.helpSQL(`UPDATE core.discount_codes SET enabled = false WHERE user_id = $1`, used.ID)
	u.text("/codes")
	if hasCallback(u.last(), "buy") || strings.Contains(u.last().text, "💡") {
		t.Errorf("a Buy button with nothing to use:\n%s\n%v", u.last().text, callbacks(u.last()))
	}
}

// someOrder is an order of the customer, for a redemption to name.
func (w *world) someOrder(tg int64) string {
	w.t.Helper()
	var id string
	if err := w.stack.Store.DB().QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	u := w.storeUser(tg)
	var planID string
	if err := w.stack.Store.DB().QueryRow(context.Background(), `SELECT id::text FROM core.plans ORDER BY created_at LIMIT 1`).Scan(&planID); err != nil {
		w.t.Fatal(err)
	}
	w.helpSQL(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key) VALUES ($1, $2, $3, 'new', 'active', 900, 'IRT', $4)`,
		id, u.ID, planID, "used-"+id)
	return id
}

// The same list in Persian: the words, the digits and the Jalali dates are Persian, a line that starts with a Latin code is still read right to
// left, and a Latin plan name sits inside its own isolate.
func TestMyCodesInPersian(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.persian(7900000041, "farsi")
	now := time.Now()
	w.reward(7900000041, "THX-FARSI222", 20, 0, 5*24*time.Hour, "Monthly 50GB", now.Add(-3*time.Hour))
	w.reward(7900000041, "THX-AMOUNT22", 0, 40_000, -time.Hour, "", now.Add(-2*time.Hour))
	tail := w.applyGift(7900000041, 25_000, 1, now.Add(-time.Hour))

	u.press("wallet")
	u.lastButton("🎟 کدهای من")
	u.press("cds")
	text := u.shown().text
	for _, want := range []string{
		"🎟 <b>کدهای من</b>",
		"\u200f🟢 <code>THX-FARSI222</code>",
		"<b>۲۰٪ تخفیف</b> برای هر پلن · <b>استفاده\u200cنشده</b>",
		"معتبر تا " + i18n.Date("fa", now.Add(5*24*time.Hour)),
		"پاداش خرید " + isolated("Monthly 50GB") + " · " + i18n.Date("fa", now.Add(-3*time.Hour)),
		"\u200f⌛ <code>THX-AMOUNT22</code>",
		"<b>۴۰٬۰۰۰ تومان تخفیف</b> برای هر پلن · <b>منقضی\u200cشده</b>",
		"پایان اعتبار: ",
		"🎁 کد هدیه · آخر کد: <code>" + tail + "</code>",
		"۲۵٬۰۰۰ تومان به کیف پول شما اضافه شد · <b>استفاده\u200cشده توسط شما</b>",
		"اعمال\u200cشده در " + i18n.Date("fa", now.Add(-time.Hour)),
		"💡 برای استفاده از کد تخفیف",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Persian screen lacks %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"{", "}", "My codes", "off any plan", "Not used", "Expired", "added to your wallet"} {
		if strings.Contains(text, bad) {
			t.Errorf("the Persian screen has %q:\n%s", bad, text)
		}
	}
	// The buttons are Persian too.
	u.lastButton("» بازگشت")
	u.lastButton("🛒")

	// And the empty list.
	other := w.persian(7900000042, "farsi2")
	other.text("/codes")
	other.lastHas("هنوز کدی ندارید")
}

// The reward message carries a My codes button beside Buy, so a customer can find the code again.
func TestRewardMessageHasMyCodesButton(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/plan_add 99000 IRT 7 20 Weekly 20GB | هفتگی")
	ctx := context.Background()
	plans, err := w.stack.Store.ListPlans(ctx, w.stack.Store.Conn(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plans {
		if p.NameI18n["en"] == "Monthly 50GB" {
			p := p
			pct := int32(15)
			p.RewardPercent, p.RewardDays = &pct, 10
			if _, err := w.dom().UpsertPlan(ctx, w.storeUser(ownerID), &p); err != nil {
				t.Fatal(err)
			}
		}
	}
	buyer := w.customer(7900000051, "rewarded")
	w.fund(7900000051, 500_000)
	buyer.tap("buy")
	buyer.pressLabel("Monthly 50GB")
	buyer.press("pay:w:")
	buyer.eventuallySees("Thank you for your purchase!")
	msg := buyer.lastWith("Thank you for your purchase!")
	m := regexp.MustCompile(`<code>(THX-[A-Z0-9]{8})</code>`).FindStringSubmatch(msg.text)
	if m == nil || !slices.Equal(callbacks(msg), []string{"buy", "cds"}) {
		t.Fatalf("the reward message: %q %v", msg.text, callbacks(msg))
	}

	// The code in the message is the code in the list, with the plan that gave it.
	buyer.press("cds")
	list := buyer.shown().text
	if !strings.Contains(list, "<code>"+m[1]+"</code>") || !strings.Contains(list, "<b>15% off</b> any plan · <b>Not used yet</b>") ||
		!strings.Contains(list, "Reward for buying "+isolated("Monthly 50GB")) {
		t.Fatalf("the list after the purchase:\n%s", list)
	}

	// Used on a later purchase, it is listed as used.
	buyer.tap("home")
	buyer.tap("buy")
	buyer.pressLabel("Weekly 20GB")
	buyer.press("dc:")
	buyer.text(m[1])
	buyer.press("pay:w:")
	buyer.eventuallySees("Your service is ready")
	buyer.text("/codes")
	if !strings.Contains(buyer.last().text, "✅ <code>"+m[1]+"</code>") || !strings.Contains(buyer.last().text, "<b>Used</b>") {
		t.Fatalf("after using it:\n%s", buyer.last().text)
	}
}

// A banned customer is told so, as everywhere else.
func TestMyCodesForABannedCustomer(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7900000061, "banned")
	w.reward(7900000061, "THX-BANNED22", 10, 0, 5*24*time.Hour, "", time.Now())
	w.setStatusOf(7900000061, "banned")
	u.text("/codes")
	if strings.Contains(u.allText(), "THX-BANNED22") {
		t.Fatalf("a banned customer was shown a code:\n%s", u.allText())
	}
	u.lastHas(w.h.cat.T("en", "error.banned"))
}

// noCodesCore is a core older than the bot: it has no list of codes.
type noCodesCore struct{ corev1.CoreServiceClient }

func (noCodesCore) ListMyCodes(context.Context, *corev1.ListMyCodesRequest, ...grpc.CallOption) (*corev1.ListMyCodesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unknown method")
}

// A bot updated before its core says the list is not there yet, and shows the wallet again.
func TestMyCodesWithACoreOlderThanTheBot(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7900000071, "older")
	w.h = New(noCodesCore{w.stack.Core}, w.tg, state.NewMemory(), i18n.MustLoad(), nil,
		Config{AdminTelegramIDs: []int64{ownerID}, BotName: "TestVPN", BotUsername: "test_vpn_bot"}, nil)
	u.press("wallet")
	u.press("cds")
	u.toast("Your list of codes is not available right now.")
	u.shownHas("Wallet")
}

// The command is in the bot's menu, with a description in each language that the owner can edit.
func TestCodesCommandIsInTheMenu(t *testing.T) {
	cat := i18n.MustLoad()
	p, on := wantedProfile(cat, settings.New(), "", true, "")
	if !on {
		t.Fatal("the profile is on by default")
	}
	for lang, want := range map[string]string{"en": "My discount and gift codes", "fa": "کدهای تخفیف و هدیه\u0654 من"} {
		var got string
		for _, c := range p.Langs[lang].Commands {
			if c.Command == "codes" {
				got = c.Description
			}
		}
		if got != want {
			t.Errorf("%s /codes: %q, want %q", lang, got, want)
		}
	}
	if dropped := cat.SetOverrides(map[string]string{"texts.en.profile.cmd.codes": "Where are my codes"}); len(dropped) > 0 {
		t.Fatalf("dropped: %v", dropped)
	}
	p, _ = wantedProfile(cat, settings.New(), "", true, "")
	for _, c := range p.Langs["en"].Commands {
		if c.Command == "codes" && c.Description != "Where are my codes" {
			t.Errorf("an edited description: %q", c.Description)
		}
	}
}
