package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// The cell rules of every CSV file core makes: the reports' summary and orders,
// the ledger and the audit log (docs/14-reports.md, "CSV files").
//
//   - Text a person chose (names, usernames, reasons, free text) goes through
//     textCell: a cell that a spreadsheet would run as a formula is quoted, line
//     breaks are one kind, and a cell is never longer than a spreadsheet keeps.
//   - Numbers are written by their caller and never go through textCell: a
//     negative amount ("-100000") must stay a number. Money is a plain decimal in
//     the currency's major unit (decimal), counts are integers.
//   - Dates are written by a clock (below): instants in UTC and in the store's
//     time zone, always ISO-shaped with Latin digits, and the Jalali date
//     beside them when the caller asked for that calendar.

// maxCellRunes is the longest text a cell holds. A spreadsheet keeps 32,767
// characters of a cell and moves the rest into the next row, which would break
// the shape of the file; a longer text is cut here, and says so.
const (
	maxCellRunes = 32_000
	cellCut      = " …[cut]"
)

// lineBreaks makes every line break of a text a plain \n; the CSV writer then
// writes each of them, and the end of every row, as \r\n.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// formulaStart says whether a spreadsheet could read text as a formula: it
// starts with =, +, - or @, a tab or a carriage return, or one of those comes
// after spaces, line breaks, control characters or invisible formatting
// characters (a right-to-left mark before an = is still an =).
func formulaStart(s string) bool {
	if first, _ := utf8.DecodeRuneInString(s); first == '\t' || first == '\r' {
		return true
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		return strings.ContainsRune("=+-@", r)
	}
	return false
}

// textCell is a text for a cell of a CSV file: quoted with a single quote when
// a spreadsheet could run it as a formula, with one kind of line break, and cut
// when it is longer than a spreadsheet keeps. Numbers are not texts: write
// them with decimal or strconv, so a negative amount stays a number.
func textCell(s string) string {
	if s == "" {
		return s
	}
	risky := formulaStart(s)
	s = lineBreaks.Replace(s)
	if utf8.RuneCountInString(s) > maxCellRunes {
		s = string([]rune(s)[:maxCellRunes]) + cellCut
	}
	if risky {
		return "'" + s
	}
	return s
}

// clock writes the instants and days of one file the same way everywhere.
// zone is the IANA name of the store's time zone, which is also what the
// file's own tz column says; jalali asks for the Jalali columns.
type clock struct {
	loc    *time.Location
	zone   string
	jalali bool
}

// newClock is the clock of a file in a zone, written in a calendar.
func newClock(loc *time.Location, calendar string) clock {
	return clock{loc: loc, zone: loc.String(), jalali: calendar == domain.CalendarJalali}
}

// utc is an instant in UTC, ISO 8601 to the second: 2026-10-31T20:29:00Z.
func (c clock) utc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// local is the same instant on the clocks of the store's time zone, as a date
// and a time of day that a spreadsheet reads as a date: 2026-10-31 23:59:00.
func (c clock) local(t time.Time) string { return t.In(c.loc).Format("2006-01-02 15:04:05") }

// jalaliTime is the local time with its date in the Jalali calendar and Latin
// digits: 1405-08-09 23:59:00.
func (c clock) jalaliTime(t time.Time) string { return jalaliStamp(c.local(t)) }

// jalaliDay writes a Gregorian day ("2026-10-31") as a Jalali one with Latin
// digits ("1405-08-09"); "" for text that is no day.
func jalaliDay(day string) string {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	jy, jm, jd := i18n.GregorianToJalali(d.Year(), int(d.Month()), d.Day())
	return fmt.Sprintf("%04d-%02d-%02d", jy, jm, jd)
}

// jalaliStamp is jalaliDay for a local time ("2026-10-31 23:59:00"): the date
// converted, the time of day kept.
func jalaliStamp(local string) string {
	if len(local) < len("2006-01-02") {
		return ""
	}
	j := jalaliDay(local[:len("2006-01-02")])
	if j == "" {
		return ""
	}
	return j + local[len("2006-01-02"):]
}

// fileDay writes a Gregorian day for a file name: as it is, or as the Jalali day
// when the file is Jalali.
func fileDay(day string, jalali bool) string {
	if jalali {
		if j := jalaliDay(day); j != "" {
			return j
		}
	}
	return day
}

// safeName keeps a file name to letters, digits, dot, dash and underscore, so
// nothing a request carries can end up in the Content-Disposition header.
func safeName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '-'
	}, name)
}

// attachment sets the headers of a file download: its name (safe characters
// only) and the type, with the encoding a spreadsheet needs.
func attachment(h http.Header, name string) {
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+safeName(name)+`"`)
}
