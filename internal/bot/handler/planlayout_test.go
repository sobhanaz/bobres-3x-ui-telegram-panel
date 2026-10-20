package handler

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// dataOf is the callback data of the button with exactly this label on the
// message the user is looking at.
func (p *person) dataOf(label string) string {
	p.w.t.Helper()
	for _, b := range buttons(p.shown()) {
		if b.Text == label {
			return b.CallbackData
		}
	}
	p.w.t.Fatalf("user %d: no button %q; the buttons are %v", p.id, label, p.shown().labels())
	return ""
}

// pressLabel taps the button with exactly this label.
func (p *person) pressLabel(label string) {
	p.w.t.Helper()
	p.press(p.dataOf(label))
}

// rowsOf are the rows of buttons of a message.
func rowsOf(m *msg) [][]tg.Button {
	if m.kb == nil {
		return nil
	}
	return m.kb.InlineKeyboard
}

// checkKeyboardLimits fails when a keyboard is one Telegram would refuse: more
// than 100 buttons, a row of more than 8, callback data over 64 bytes, or an
// empty label.
func checkKeyboardLimits(t *testing.T, who string, m *msg) {
	t.Helper()
	total := 0
	for _, row := range rowsOf(m) {
		if len(row) > 8 {
			t.Errorf("%s: a row of %d buttons", who, len(row))
		}
		for _, b := range row {
			total++
			if len(b.CallbackData) > 64 || b.Text == "" || (b.CallbackData == "" && b.URL == "" && b.WebApp == nil) {
				t.Errorf("%s: button %+v", who, b)
			}
		}
	}
	if total > 100 {
		t.Errorf("%s: %d buttons", who, total)
	}
}

// planRowsOf are the plan rows of a plan list: the rows whose buttons open a
// plan (their data starts with prefix), as each row's texts.
func planRowsOf(m *msg, prefix string) [][]string {
	var out [][]string
	for _, row := range rowsOf(m) {
		if len(row) > 0 && strings.HasPrefix(row[0].CallbackData, prefix) {
			var texts []string
			for _, b := range row {
				if !strings.HasPrefix(b.CallbackData, prefix) {
					t := fmt.Sprintf("%v", row)
					texts = append(texts, "!"+t)
				}
				texts = append(texts, b.Text)
			}
			out = append(out, texts)
		}
	}
	return out
}

// The default layout of the plan lists is a table: a header row, then each plan
// as one row of two buttons (its name and its price) that both open the plan,
// then Home. In Persian the header and the prices are Persian. Tapping the
// header does nothing at all.
func TestPlansAreATableByDefault(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/plan_add 99000 IRT 7 20 Weekly 20GB | هفتگی ۲۰ گیگ")
	owner.sees("Plan saved: Weekly 20GB")

	u := w.customer(2101, "tab")
	u.press("buy")
	m := u.shown()
	checkKeyboardLimits(t, "the plan table", m)
	rows := rowsOf(m)
	if len(rows) != 4 {
		t.Fatalf("the plan table has %d rows: %v", len(rows), m.labels())
	}
	if h := rows[0]; len(h) != 2 || h[0].Text != "🛍 Plan" || h[1].Text != "💲 Price" || h[0].CallbackData != "nop" || h[1].CallbackData != "nop" {
		t.Fatalf("header: %+v", h)
	}
	want := map[string]string{"Weekly 20GB": "99,000 Toman", "Monthly 50GB": "150,000 Toman"}
	for _, row := range rows[1:3] {
		if len(row) != 2 || row[0].CallbackData != row[1].CallbackData || !strings.HasPrefix(row[0].CallbackData, "plan:") ||
			want[row[0].Text] != row[1].Text || row[0].Style != "" || row[1].Style != "" {
			t.Fatalf("a plan row: %+v (want %v)", row, want)
		}
		delete(want, row[0].Text)
	}
	if len(want) != 0 {
		t.Fatalf("plans missing from the table: %v", want)
	}
	if last := rows[3]; len(last) != 1 || last[0].CallbackData != "home" {
		t.Fatalf("the last row: %+v", last)
	}

	// The name opens the plan, and so does the price.
	nameData, priceData := rows[1][0].CallbackData, rows[1][1].CallbackData
	u.tap(nameData)
	u.shownHas("How would you like to pay?")
	first := u.shown().text
	u.press("buy")
	u.tap(priceData)
	if got := u.shown().text; got != first || !strings.Contains(got, rows[1][0].Text) {
		t.Fatalf("the price button opened %q, the name button %q", got, first)
	}

	// The header is not a button that does anything: no new message, no
	// change, no toast.
	u.press("buy")
	before := len(w.tg.all(2101))
	text := u.shown().text
	u.press("nop")
	if len(w.tg.all(2101)) != before || u.shown().text != text {
		t.Fatalf("the header button changed the chat")
	}
	w.tg.mu.Lock()
	toasts := slices.Clone(w.tg.toasts[2101])
	w.tg.mu.Unlock()
	for _, s := range toasts {
		if strings.Contains(s, "expired") {
			t.Fatalf("the header button was answered as an expired menu: %q", s)
		}
	}

	// In Persian, the header's words and the prices in Persian digits.
	fa := w.person(2102, "")
	fa.text("/start")
	fa.press("lang:fa")
	fa.press("buy")
	fa.lastButton("🛍 محصول")
	fa.lastButton("💲 مبلغ")
	fa.lastButton("۱۵۰٬۰۰۰ تومان")
	checkKeyboardLimits(t, "the Persian plan table", fa.shown())
}

// bot.plan_layout chooses the layout, from the chat and from the admin menu:
// the list is what the bot had before (one button, "name · price"); anything
// else core refuses; clearing it is the table again.
func TestPlanLayoutIsASetting(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(2111, "lay")

	owner.text("/set bot.plan_layout list")
	owner.sees("Saved")
	u.press("buy")
	rows := planRowsOf(u.shown(), "plan:")
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != "Monthly 50GB · 150,000 Toman" {
		t.Fatalf("the list: %v (%v)", rows, u.shown().labels())
	}
	if slices.Contains(callbacks(u.shown()), "nop") {
		t.Fatalf("a list has no header")
	}

	owner.text("/set bot.plan_layout grid")
	owner.lastHas("one of table, list")
	u.press("home")
	u.press("buy")
	if rows := planRowsOf(u.shown(), "plan:"); len(rows[0]) != 1 {
		t.Fatalf("a refused value changed the layout: %v", rows)
	}

	owner.text("/set bot.plan_layout table")
	u.press("home")
	u.press("buy")
	if rows := planRowsOf(u.shown(), "plan:"); len(rows) != 1 || !slices.Equal(rows[0], []string{"Monthly 50GB", "150,000 Toman"}) {
		t.Fatalf("the table: %v", rows)
	}

	// Cleared, it is the table; set from the admin menu's buttons, it is the list.
	owner.text("/set bot.plan_layout list")
	owner.text("/set bot.plan_layout")
	u.press("home")
	u.press("buy")
	if rows := planRowsOf(u.shown(), "plan:"); len(rows[0]) != 2 {
		t.Fatalf("cleared, the layout is not the table: %v", rows)
	}
	owner.text("/admin")
	owner.press("adm:set")
	owner.sees("Plans and bulk purchase")
	owner.press("adm:sg:shop")
	owner.shownHas("Plan list layout")
	owner.shownHas("Table")
	owner.press("adm:sk:bot.plan_layout")
	owner.shownHas("How the bot lists plans")
	owner.press("adm:sv:bot.plan_layout:list")
	u.press("home")
	u.press("buy")
	if rows := planRowsOf(u.shown(), "plan:"); len(rows[0]) != 1 {
		t.Fatalf("chosen from the admin menu, the list was not drawn: %v", rows)
	}
}

// The renewal and traffic package pickers and a reseller's Buy for a customer use
// the same layout, with their own data on every button.
func TestPickersFollowThePlanLayout(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/topup_add 30000 IRT 10 +10 GB | ۱۰ گیگ اضافه")
	owner.sees("Plan saved: +10 GB")
	u := w.customer(2121, "pick")
	w.fund(2121, 1_000_000)
	sub := w.seedService(2121, "")
	rs := w.customer(2122, "shop")
	w.makeReseller(2122, 20, 10, 200_000)

	check := func(layout string) {
		t.Helper()
		wantCells := 2
		if layout == "list" {
			wantCells = 1
		}
		for _, c := range []struct {
			who    *person
			press  string
			prefix string
			name   string
			price  string
		}{
			{u, "rnw:" + sub, "rp:", "Monthly 50GB", "150,000 Toman"},
			{u, "tup:" + sub, "rp:", "+10 GB", "30,000 Toman"},
			{rs, "rs:buy", "rs:p:", "Monthly 50GB", "120,000 Toman"},
		} {
			c.who.tap(c.press)
			checkKeyboardLimits(t, c.press+" ("+layout+")", c.who.shown())
			rows := planRowsOf(c.who.shown(), c.prefix)
			if len(rows) != 1 || len(rows[0]) != wantCells {
				t.Fatalf("%s as a %s: %v (%v)", c.press, layout, rows, c.who.shown().labels())
			}
			if layout == "table" && (rows[0][0] != c.name || rows[0][1] != c.price) {
				t.Fatalf("%s: row %v", c.press, rows[0])
			}
			if layout == "list" && rows[0][0] != c.name+" · "+c.price {
				t.Fatalf("%s: row %v", c.press, rows[0])
			}
			if layout == "table" && !slices.Contains(callbacks(c.who.shown()), "nop") {
				t.Fatalf("%s: no header", c.press)
			}
			if layout == "list" && slices.Contains(callbacks(c.who.shown()), "nop") {
				t.Fatalf("%s: a header in a list", c.press)
			}
		}
	}
	check("table")
	// Both buttons of a picker's plan work: the price opens the same checkout.
	u.tap("rnw:" + sub)
	row := rowsOf(u.shown())[1]
	u.tap(row[1].CallbackData)
	u.shownHas("Renew service")
	u.shownHas("Price: <b>150,000 Toman</b>")

	owner.text("/set bot.plan_layout list")
	check("list")
}

// Many plans and long names cannot break the keyboard: a long name is cut with an
// ellipsis in the table (its price and its data are not), a table that would not
// fit Telegram's 100 buttons is a list, and a list that would not fit is cut.
func TestPlanListsStayInsideTelegramsLimits(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	ctx := context.Background()
	st := w.stack.Store
	addPlans := func(n int, namePrefix string) {
		t.Helper()
		for i := range n {
			p := &corestore.Plan{NameI18n: map[string]string{"en": fmt.Sprintf("%s %03d", namePrefix, i), "fa": fmt.Sprintf("%s %03d", namePrefix, i)},
				Kind: "time", Price: int64(1000 + i), Currency: "IRT", Enabled: true, Sort: int32(i + 10)}
			if err := st.UpsertPlan(ctx, st.Conn(), p); err != nil {
				t.Fatal(err)
			}
		}
	}
	u := w.customer(2131, "many")

	// One plan with a long name, in both languages.
	long := &corestore.Plan{NameI18n: map[string]string{"en": "Unlimited traffic for a whole year on every server with priority support", "fa": "ترافیک نامحدود یک ساله روی همهٔ سرورها با پشتیبانی ویژه و اولویت دار"},
		Kind: "time", Price: 5_990_000, Currency: "IRT", Enabled: true, Sort: 5}
	if err := st.UpsertPlan(ctx, st.Conn(), long); err != nil {
		t.Fatal(err)
	}
	u.press("buy")
	var cut []string
	for _, row := range planRowsOf(u.shown(), "plan:") {
		if strings.HasPrefix(row[0], "Unlimited") {
			cut = row
		}
	}
	if len(cut) != 2 || !strings.HasSuffix(cut[0], "…") || utf8.RuneCountInString(cut[0]) > tableNameWidth || cut[1] != "5,990,000 Toman" {
		t.Fatalf("a long name: %v", cut)
	}
	checkKeyboardLimits(t, "a table with a long name", u.shown())
	u.press("home")
	u.press("lang") // the picker
	u.press("lang:fa")
	u.press("buy")
	for _, row := range planRowsOf(u.shown(), "plan:") {
		if strings.HasPrefix(row[0], "ترافیک") && (!strings.HasSuffix(row[0], "…") || utf8.RuneCountInString(row[0]) > tableNameWidth) {
			t.Fatalf("a long Persian name: %v", row)
		}
	}
	u.press("home")
	u.press("lang")
	u.press("lang:en")

	count := func(m *msg) (plans, header int) {
		for _, row := range rowsOf(m) {
			if len(row) > 0 && strings.HasPrefix(row[0].CallbackData, "plan:") {
				plans++
			}
			if len(row) > 0 && row[0].CallbackData == "nop" {
				header++
			}
		}
		return plans, header
	}
	// 46 plans fill a table (a header, 46 rows of two buttons, and Home); 60 are a
	// list; 120 are cut to the 94 that fit.
	for _, c := range []struct {
		have, plans, header int
	}{
		{46, 46, 1},
		{60, 60, 0},
		{120, 94, 0},
	} {
		for len(regularPlans(t, st)) < c.have {
			addPlans(1, "Extra")
		}
		u.text("/menu")
		u.press("buy")
		m := u.shown()
		checkKeyboardLimits(t, fmt.Sprintf("%d plans", c.have), m)
		if plans, header := count(m); plans != c.plans || header != c.header {
			t.Fatalf("%d plans: %d plan rows and %d headers, want %d and %d", c.have, plans, header, c.plans, c.header)
		}
	}
}

// regularPlans are the plans a customer can buy a service with.
func regularPlans(t *testing.T, st *corestore.Store) []corestore.Plan {
	t.Helper()
	ps, err := st.ListPlans(context.Background(), st.Conn(), false)
	if err != nil {
		t.Fatal(err)
	}
	var out []corestore.Plan
	for _, p := range ps {
		if !p.IsTrial && !p.IsTopup {
			out = append(out, p)
		}
	}
	return out
}
