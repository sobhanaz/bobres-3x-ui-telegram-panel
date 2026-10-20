package handler

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

func testEntries(n int) []planEntry {
	out := make([]planEntry, n)
	for i := range out {
		out[i] = planEntry{name: fmt.Sprintf("Plan %d", i+1), price: fmt.Sprintf("%d,000 Toman", i+1),
			data: fmt.Sprintf("rp:abcdefgh:00000000-0000-7000-8000-%012d", i)}
	}
	return out
}

func listText(e planEntry) string { return e.name + " · " + e.price }

var testHead = [2]string{"🛍 Plan", "💲 Price"}

// The table: a header of two buttons that do nothing, then each plan as ONE row of
// two buttons, its name and its price, both opening that plan, in the order given.
func TestPlanRowsTable(t *testing.T) {
	rows := planRows(planLayoutTable, testHead, testEntries(3), listText)
	if len(rows) != 4 {
		t.Fatalf("%d rows for 3 plans, want a header and 3", len(rows))
	}
	for i, b := range rows[0] {
		if b.Text != testHead[i] || b.CallbackData != "nop" || b.Style != "" {
			t.Errorf("header button %d: %+v", i, b)
		}
	}
	if len(rows[0]) != 2 {
		t.Errorf("the header has %d buttons", len(rows[0]))
	}
	for i, e := range testEntries(3) {
		row := rows[i+1]
		if len(row) != 2 || row[0].Text != e.name || row[1].Text != e.price || row[0].CallbackData != e.data || row[1].CallbackData != e.data {
			t.Errorf("row of plan %d: %+v", i+1, row)
		}
		for _, b := range row {
			if b.URL != "" || b.Style != "" {
				t.Errorf("plan %d's button %+v is not a plain callback button", i+1, b)
			}
		}
	}
}

// The list, as it always was: one button per plan, "name · price", no header.
func TestPlanRowsList(t *testing.T) {
	rows := planRows(planLayoutList, testHead, testEntries(3), listText)
	if len(rows) != 3 {
		t.Fatalf("%d rows for 3 plans", len(rows))
	}
	for i, e := range testEntries(3) {
		if len(rows[i]) != 1 || rows[i][0].Text != listText(e) || rows[i][0].CallbackData != e.data {
			t.Errorf("row of plan %d: %+v", i+1, rows[i])
		}
	}
	if rows := planRows(planLayoutList, testHead, nil, listText); len(rows) != 0 {
		t.Errorf("a list of no plans has %d rows", len(rows))
	}
	// An unknown layout is the table (the default), never a broken keyboard.
	if rows := planRows("grid", testHead, testEntries(2), listText); len(rows) != 3 || len(rows[1]) != 2 {
		t.Errorf("an unknown layout: %d rows", len(rows))
	}
}

// Whatever the number of plans the keyboard stays inside Telegram's limits
// (100 buttons with room for the rows after it, 8 to a row, 64 bytes of data);
// a table that cannot fit is a list, and a list that cannot fit is cut.
func TestPlanRowsStayInsideTelegramsLimits(t *testing.T) {
	for _, c := range []struct {
		plans      int
		layout     string
		wantLayout string
		wantPlans  int
	}{
		{0, planLayoutTable, planLayoutTable, 0},
		{1, planLayoutTable, planLayoutTable, 1},
		{2, planLayoutTable, planLayoutTable, 2},
		{8, planLayoutTable, planLayoutTable, 8},
		{23, planLayoutTable, planLayoutTable, 23},
		{46, planLayoutTable, planLayoutTable, 46}, // 2 + 92 = 94 buttons
		{47, planLayoutTable, planLayoutList, 47},  // a table would be 96
		{60, planLayoutTable, planLayoutList, 60},
		{94, planLayoutTable, planLayoutList, 94},
		{95, planLayoutTable, planLayoutList, 94},
		{300, planLayoutTable, planLayoutList, 94},
		{1, planLayoutList, planLayoutList, 1},
		{94, planLayoutList, planLayoutList, 94},
		{95, planLayoutList, planLayoutList, 94},
		{300, planLayoutList, planLayoutList, 94},
	} {
		t.Run(fmt.Sprintf("%s/%d", c.layout, c.plans), func(t *testing.T) {
			rows := planRows(c.layout, testHead, testEntries(c.plans), listText)
			total := 0
			for _, row := range rows {
				if len(row) > 8 {
					t.Fatalf("a row of %d buttons", len(row))
				}
				total += len(row)
				for _, b := range row {
					if len(b.CallbackData) > 64 || len(b.CallbackData) == 0 || b.Text == "" {
						t.Fatalf("button %+v", b)
					}
				}
			}
			// the rows a screen adds after the plans (Back and Home, 2 buttons; a pager, 2 more) still fit
			if total+6 > 100 {
				t.Fatalf("%d buttons leave no room for the other rows", total)
			}
			isTable := len(rows) > 0 && rows[0][0].CallbackData == "nop"
			if c.plans == 0 && c.wantLayout == planLayoutTable {
				isTable = true // a table of no plans is its header: not worth a keyboard, but harmless
			}
			if got := map[bool]string{true: planLayoutTable, false: planLayoutList}[isTable]; got != c.wantLayout && c.plans > 0 {
				t.Fatalf("drawn as a %s, want a %s", got, c.wantLayout)
			}
			shown := len(rows)
			if isTable {
				shown--
			}
			if c.plans > 0 && shown != c.wantPlans {
				t.Fatalf("%d plans shown, want %d", shown, c.wantPlans)
			}
		})
	}
}

// A long name is cut with an ellipsis in a table button, never a name that
// fits; the price is never cut; the list keeps its text as it was.
func TestPlanRowsCutALongNameInTheTable(t *testing.T) {
	long := planEntry{name: "Unlimited traffic for a whole year, all servers, with support", price: "5,990,000 Toman", data: "plan:x"}
	rows := planRows(planLayoutTable, testHead, []planEntry{long, {name: "Short", price: "1 Toman", data: "plan:y"}}, listText)
	name := rows[1][0].Text
	if utf8.RuneCountInString(name) > tableNameWidth || !strings.HasSuffix(name, "…") || !strings.HasPrefix(long.name, strings.TrimSuffix(name, "…")) {
		t.Errorf("a long name: %q", name)
	}
	if rows[1][1].Text != long.price || rows[1][0].CallbackData != "plan:x" || rows[1][1].CallbackData != "plan:x" {
		t.Errorf("its price or data changed: %+v", rows[1])
	}
	if rows[2][0].Text != "Short" {
		t.Errorf("a short name: %q", rows[2][0].Text)
	}
	if rows := planRows(planLayoutList, testHead, []planEntry{long}, listText); rows[0][0].Text != listText(long) {
		t.Errorf("the list cut a name: %q", rows[0][0].Text)
	}
}

// The invisible characters the cuts are tested with, written as code points so
// the source shows them.
var (
	zwnj   = string(rune(0x200c)) // the half-space of Persian
	zwj    = string(rune(0x200d)) // binds an emoji sequence
	rlm    = string(rune(0x200f)) // a right-to-left mark
	shadda = string(rune(0x0651)) // an Arabic vowel sign (a combining mark)
	tanwin = string(rune(0x064b))
	vs16   = string(rune(0xfe0f)) // an emoji's variation selector (a combining mark)
)

func TestEllipsize(t *testing.T) {
	const w = 10
	for _, c := range []struct {
		name, in, want string
	}{
		{"short", "VIP 20 GB", "VIP 20 GB"},
		{"exactly the width", "0123456789", "0123456789"},
		{"one more", "0123456789a", "012345678…"},
		{"empty", "", ""},
		{"blanks collapse", "  VIP   20  GB  ", "VIP 20 GB"},
		{"a line break is a space", "VIP\n20\nGB", "VIP 20 GB"},
		{"a space before the cut is dropped", "01234567 89abc", "01234567…"},
		// Persian: the half-space before the cut would hang at the end of the name.
		{"a half-space at the cut", "یکدوسهچا" + zwnj + "رهفت", "یکدوسهچا…"},
		{"RTL with Latin digits", "پلن 20 گیگ VIP یک ماهه", "پلن 20 گی…"},
		// The vowel signs (combining marks) of the letters kept stay with them.
		{"marks inside the cut", "ab" + shadda + tanwin + "cdefghijkl", "ab" + shadda + tanwin + "cdefg…"},
		{"a mark at the end of the cut", "abcdefgh" + tanwin + "ijkl", "abcdefgh" + tanwin + "…"},
		{"only marks", strings.Repeat(tanwin, 12), strings.Repeat(tanwin, 9) + "…"},
		{"a joiner at the cut", "01234567" + zwj + "89abc", "01234567…"},
		{"a direction mark at the cut", "01234567" + rlm + "89abc", "01234567…"},
		{"an emoji and its selector", strings.Repeat("📦", 8) + vs16 + "📦📦", strings.Repeat("📦", 8) + vs16 + "…"},
		{"an emoji sequence cut at its joiner", strings.Repeat("📦", 8) + zwj + "👩👩", strings.Repeat("📦", 8) + "…"},
	} {
		got := ellipsize(c.in, w)
		if got != c.want {
			t.Errorf("%s: ellipsize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > w {
			t.Errorf("%s: %q is %d characters", c.name, got, utf8.RuneCountInString(got))
		}
	}
}

// Cutting never produces broken UTF-8 or more than the width, for any text
// and any width: no name can make Telegram refuse a keyboard.
func TestEllipsizeNeverBreaksAText(t *testing.T) {
	texts := []string{
		"", "a", "سلام دنیا", "سرویس" + zwnj + "های" + zwnj + "ویژه" + zwnj + "ی" + zwnj + "من", "🇮🇷🇮🇷🇮🇷🇮🇷🇮🇷🇮🇷",
		strings.Repeat("e"+string(rune(0x0301)), 12), strings.Repeat("ب"+shadda, 40), strings.Repeat("🧺", 30),
		"👨" + zwj + "👩" + zwj + "👧" + zwj + "👦 family plan with a long name",
	}
	for _, s := range texts {
		for width := 1; width <= 40; width++ {
			got := ellipsize(s, width)
			if !utf8.ValidString(got) {
				t.Fatalf("ellipsize(%q, %d) = %q: broken UTF-8", s, width, got)
			}
			if n := utf8.RuneCountInString(got); n > width {
				t.Fatalf("ellipsize(%q, %d) = %q: %d characters", s, width, got, n)
			}
		}
	}
}

// Both layouts open the plan from every button of it, with the data the list
// always carried.
func TestPlanRowsCarryTheCallersData(t *testing.T) {
	for _, layout := range []string{planLayoutTable, planLayoutList} {
		rows := planRows(layout, testHead, testEntries(5), listText)
		var data []string
		for _, row := range rows {
			for _, b := range row {
				if b.CallbackData != "nop" {
					data = append(data, b.CallbackData)
				}
			}
		}
		for _, d := range data {
			if !strings.HasPrefix(d, "rp:abcdefgh:") {
				t.Errorf("%s: data %q", layout, d)
			}
		}
		want := 5
		if layout == planLayoutTable {
			want = 10 // the name and the price of each
		}
		if len(data) != want {
			t.Errorf("%s: %d plan buttons, want %d", layout, len(data), want)
		}
	}
	var _ tg.Button // the rows are the Telegram client's buttons
}
