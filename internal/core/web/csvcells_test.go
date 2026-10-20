package web

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The cell rules of every CSV file (csvcells.go), without a database.

func TestTextCell(t *testing.T) {
	long := strings.Repeat("ب", maxCellRunes+500)
	for name, c := range map[string]struct{ in, want string }{
		"empty":                     {"", ""},
		"plain":                     {"sara", "sara"},
		"persian":                   {"پایه ماهانه", "پایه ماهانه"},
		"equals":                    {"=1+1", "'=1+1"},
		"plus":                      {"+98912", "'+98912"},
		"minus":                     {"-1", "'-1"},
		"at":                        {"@SUM(A1)", "'@SUM(A1)"},
		"tab":                       {"\tx", "'\tx"},
		"carriage return":           {"\rx", "'\nx"},
		"inside, not first":         {"a=b", "a=b"},
		"digits and a plus":         {"1+1", "1+1"},
		"after spaces":              {"  =1+1", "'  =1+1"},
		"after a line break":        {"\n-2", "'\n-2"},
		"after a tab and spaces":    {" \t @x", "' \t @x"},
		"only spaces":               {"   ", "   "},
		"spaces then text":          {" plain", " plain"},
		"right-to-left mark":        {"\u200f=1+1", "'\u200f=1+1"},
		"left-to-right isolate":     {"\u2066@x", "'\u2066@x"},
		"zero-width joiner":         {"\u200c\u200d-1", "'\u200c\u200d-1"},
		"byte-order mark":           {"\ufeff+1", "'\ufeff+1"},
		"no-break space":            {"\u00a0=cmd", "'\u00a0=cmd"},
		"a control character":       {"\x01=cmd", "'\x01=cmd"},
		"persian text with a dash":  {"پایه - ویژه", "پایه - ویژه"},
		"windows line breaks":       {"a\r\nb", "a\nb"},
		"old mac line breaks":       {"a\rb", "a\nb"},
		"mixed line breaks":         {"a\r\nb\nc\rd", "a\nb\nc\nd"},
		"a formula and line breaks": {"=a\r\nb", "'=a\nb"},
	} {
		if got := textCell(c.in); got != c.want {
			t.Errorf("%s: textCell(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
	// A text longer than a spreadsheet keeps is cut, and says so (and stays valid UTF-8).
	got := textCell(long)
	if n := len([]rune(got)); n != maxCellRunes+len([]rune(cellCut)) || !strings.HasSuffix(got, cellCut) {
		t.Errorf("a long text: %d runes, ends %q", n, got[len(got)-12:])
	}
	if textCell(strings.Repeat("x", maxCellRunes)) != strings.Repeat("x", maxCellRunes) {
		t.Error("a text of exactly the limit was cut")
	}
	// Idempotence is not promised (the quote is part of the cell), but a quoted cell is not quoted again.
	if once := textCell("=1"); textCell(once) != once {
		t.Errorf("quoted twice: %q", textCell(once))
	}
}

// Amounts are not text: a negative amount must stay a number a spreadsheet adds up.
func TestAmountsAreWrittenAsPlainDecimalsInTheCurrencysUnit(t *testing.T) {
	number := regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	for _, c := range []struct {
		amount   int64
		currency string
		want     string
	}{
		{0, "IRT", "0"}, {100_000, "IRT", "100000"}, {-100_000, "IRT", "-100000"}, {1_234_567_890, "IRT", "1234567890"},
		{0, "USDT", "0.000000"}, {7, "USDT", "0.000007"}, {-7, "USDT", "-0.000007"}, {2_500_000, "USDT", "2.500000"},
		{-2_500_000, "USDT", "-2.500000"}, {1_000_000, "USDT", "1.000000"}, {123_456_789_012_345, "USDT", "123456789.012345"},
		{-1, "USDT", "-0.000001"}, {999_999, "USDT", "0.999999"},
		{5000, "XTR", "5000"}, {-5000, "XTR", "-5000"}, // Stars have no minor unit, like Toman
	} {
		got := decimal(c.amount, c.currency)
		if got != c.want {
			t.Errorf("decimal(%d, %s) = %q, want %q", c.amount, c.currency, got, c.want)
		}
		if !number.MatchString(got) || strings.ContainsAny(got, ",٬ '") {
			t.Errorf("%q is not a plain number", got)
		}
		if f, err := strconv.ParseFloat(got, 64); err != nil || (c.amount < 0) != (f < 0) {
			t.Errorf("%q does not read back as a number of the right sign: %v %v", got, f, err)
		}
	}
}

// The Jalali day of a Gregorian one, around the days that are easy to get wrong.
func TestJalaliDayAtTheEdges(t *testing.T) {
	for in, want := range map[string]string{
		"2026-03-21": "1405-01-01", // Nowruz 1405
		"2026-03-20": "1404-12-29", // the last day of a common year
		"2025-03-21": "1404-01-01",
		"2025-03-20": "1403-12-30", // the leap day of 1403
		"2025-03-19": "1403-12-29",
		"2024-03-20": "1403-01-01",
		"2024-03-19": "1402-12-29",
		"2026-09-22": "1405-06-31", // Shahrivar has 31 days
		"2026-09-23": "1405-07-01",
		"2026-10-22": "1405-07-30", // Mehr has 30
		"2026-10-23": "1405-08-01",
		"2026-10-31": "1405-08-09",
		"2026-11-03": "1405-08-12",
		"2029-03-20": "1408-01-01", // the next leap year starts a day earlier
		"2000-01-01": "1378-10-11", // the first day core reports on
		"2100-03-21": "1479-01-01",
		"2100-12-31": "1479-10-10", // the last
	} {
		if got := jalaliDay(in); got != want {
			t.Errorf("jalaliDay(%s) = %s, want %s", in, got, want)
		}
	}
	for _, bad := range []string{"", "2026", "2026-13-01", "2026-02-30", "26-10-31", "garbage"} {
		if got := jalaliDay(bad); got != "" {
			t.Errorf("jalaliDay(%q) = %q, want nothing", bad, got)
		}
	}
	if got := jalaliStamp("2026-10-31 23:59:00"); got != "1405-08-09 23:59:00" {
		t.Errorf("jalaliStamp = %q", got)
	}
	if jalaliStamp("2026-10-31") != "1405-08-09" || jalaliStamp("nope") != "" || jalaliStamp("") != "" {
		t.Error("jalaliStamp of a short or bad text")
	}
	if fileDay("2026-10-31", true) != "1405-08-09" || fileDay("2026-10-31", false) != "2026-10-31" || fileDay("garbage", true) != "garbage" {
		t.Error("fileDay")
	}
}

// Instants are written once in UTC and once on the store's wall clocks, and the
// Jalali date turns over at the local midnight, not at UTC's.
func TestClockWritesInstantsInTheStoresZone(t *testing.T) {
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	c := newClock(tehran, "jalali")
	if c.zone != "Asia/Tehran" || !c.jalali {
		t.Fatalf("clock %+v", c)
	}
	before := time.Date(2026, 10, 31, 20, 29, 0, 0, time.UTC) // 23:59 in Tehran
	after := before.Add(2 * time.Minute)                      // 00:01 on the 1st
	if c.utc(before) != "2026-10-31T20:29:00Z" || c.local(before) != "2026-10-31 23:59:00" || c.jalaliTime(before) != "1405-08-09 23:59:00" {
		t.Errorf("before midnight: %s %s %s", c.utc(before), c.local(before), c.jalaliTime(before))
	}
	if c.utc(after) != "2026-10-31T20:31:00Z" || c.local(after) != "2026-11-01 00:01:00" || c.jalaliTime(after) != "1405-08-10 00:01:00" {
		t.Errorf("after midnight: %s %s %s", c.utc(after), c.local(after), c.jalaliTime(after))
	}
	// An instant given in another zone is still written in UTC.
	if got := c.utc(before.In(tehran)); got != "2026-10-31T20:29:00Z" {
		t.Errorf("utc of a local time: %s", got)
	}
	// Clocks that go back repeat an hour: the local text cannot tell the two apart, the UTC text can.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	n := newClock(ny, "gregorian")
	first, second := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	if n.local(first) != "2026-11-01 01:30:00" || n.local(second) != "2026-11-01 01:30:00" || n.utc(first) == n.utc(second) || n.jalali {
		t.Errorf("a repeated hour: %s %s | %s %s", n.local(first), n.utc(first), n.local(second), n.utc(second))
	}
	if n.zone != "America/New_York" {
		t.Errorf("zone %q", n.zone)
	}
}

func TestFileNamesHaveSafeCharactersAndTheirDateRange(t *testing.T) {
	safe := regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	for _, in := range []string{"reports-summary-2026-10-31-to-2026-11-03-by-day.csv", "a b/c\\d\";.csv", "../../etc/passwd", "نام.csv", "x\r\nSet-Cookie: a=b", ""} {
		got := safeName(in)
		if got != "" && !safe.MatchString(got) {
			t.Errorf("safeName(%q) = %q", in, got)
		}
		if strings.ContainsAny(got, "/\\\"; \r\n") {
			t.Errorf("safeName(%q) = %q keeps a risky character", in, got)
		}
	}
	if safeName("reports-summary-2026-10-31-to-2026-11-03-by-day.csv") != "reports-summary-2026-10-31-to-2026-11-03-by-day.csv" {
		t.Error("a good name was changed")
	}
	h := http.Header{}
	attachment(h, "a b.csv")
	if h.Get("Content-Type") != "text/csv; charset=utf-8" || h.Get("Content-Disposition") != `attachment; filename="a-b.csv"` {
		t.Errorf("headers: %v", h)
	}
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	greg, jal := newClock(tehran, "gregorian"), newClock(tehran, "jalali")
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 10, 30, 20, 30, 0, 0, time.UTC) // 00:00 on 31 Oct in Tehran
	to := time.Date(2026, 11, 2, 20, 30, 0, 0, time.UTC)    // 00:00 on 3 Nov: [from, to) ends on the 2nd
	for _, c := range []struct {
		clock    clock
		from, to *time.Time
		want     string
	}{
		{greg, &from, nil, "ledger-2026-10-31-to-2026-11-02.csv"},
		{greg, nil, nil, "ledger-all-to-2026-11-02.csv"},
		{greg, &from, &to, "ledger-2026-10-31-to-2026-11-02.csv"},
		{jal, &from, &to, "ledger-1405-08-09-to-1405-08-11-jalali.csv"},
		{jal, nil, nil, "ledger-all-to-1405-08-11-jalali.csv"},
	} {
		got := exportFileName("ledger", c.clock, c.from, c.to, now)
		if got != c.want || !safe.MatchString(got) {
			t.Errorf("exportFileName = %q, want %q", got, c.want)
		}
	}
}

// The file: BOM, commas, double quotes, and \r\n after every row and inside a cell.
func TestCSVFileUsesOneKindOfLineBreak(t *testing.T) {
	rec := httptest.NewRecorder()
	f := &csvFile{w: rec, name: "x.csv"}
	cells := []string{"a,b", `say "hi"`, "line one\nline two", "win\r\nbreaks", "old\rmac", "نام", ""}
	_ = f.write("h1", "h2", "h3", "h4", "h5", "h6", "h7")
	_ = f.data(cells[0], cells[1], textCell(cells[2]), textCell(cells[3]), textCell(cells[4]), cells[5], cells[6])
	_ = f.data("1", "2", "3", "4", "5", "6", "7")
	f.flush()
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("no byte-order mark: %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
	text := string(body[3:])
	// every line break, in a row end or inside a cell, is \r\n; none is alone
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			if i == 0 || text[i-1] != '\r' {
				t.Fatalf("a bare line feed at %d in %q", i, text)
			}
		case '\r':
			if i+1 >= len(text) || text[i+1] != '\n' {
				t.Fatalf("a bare carriage return at %d in %q", i, text)
			}
		}
	}
	if !strings.Contains(text, "\"line one\r\nline two\"") || !strings.Contains(text, "\"win\r\nbreaks\"") || !strings.Contains(text, "\"old\r\nmac\"") {
		t.Errorf("a line break inside a cell: %q", text)
	}
	if !strings.Contains(text, `"a,b","say ""hi"""`) {
		t.Errorf("commas and quotes: %q", text)
	}
	if !strings.HasSuffix(text, "\r\n") || strings.Count(text, "\r\n") != 3+3 { // 3 rows, 3 breaks inside cells
		t.Errorf("rows: %q", text)
	}
	// It reads back as the cells that went in (a reader folds \r\n in a cell to \n).
	rows, err := csv.NewReader(bytes.NewReader(body[3:])).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("read back: %v %v", rows, err)
	}
	want := []string{"a,b", `say "hi"`, "line one\nline two", "win\nbreaks", "old\nmac", "نام", ""}
	for i, w := range want {
		if rows[1][i] != w {
			t.Errorf("cell %d: %q, want %q", i, rows[1][i], w)
		}
	}
}

// flushRecorder counts the flushes a stream makes and the bytes it had by each.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes []int
}

func (f *flushRecorder) Flush() { f.flushes = append(f.flushes, f.Body.Len()) }

// A long file is pushed out as it is made (every 500 rows), not held back until the end.
func TestCSVFileStreamsRatherThanHoldingTheFile(t *testing.T) {
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	f := &csvFile{w: rec, name: "x.csv"}
	_ = f.write("n", "text")
	const rows = 5200
	for i := 0; i < rows; i++ {
		_ = f.data(strconv.Itoa(i), "نام کاربر")
	}
	atEnd := rec.Body.Len()
	f.flush()
	if len(rec.flushes) != rows/500 {
		t.Fatalf("%d flushes for %d rows", len(rec.flushes), rows)
	}
	// each flush had already delivered its rows to the connection
	for i, got := range rec.flushes {
		if i > 0 && got <= rec.flushes[i-1] {
			t.Errorf("flush %d delivered nothing new: %v", i, rec.flushes)
		}
	}
	if rec.flushes[0] == 0 || rec.flushes[len(rec.flushes)-1] > atEnd {
		t.Errorf("flushes %v, %d bytes at the end", rec.flushes, atEnd)
	}
	if f.rows != rows {
		t.Errorf("rows %d", f.rows)
	}
}
