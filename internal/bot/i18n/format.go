package i18n

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// location is the time zone dates are shown in: configuration, then the
// store's setting, which can change while the bot runs.
var location atomic.Pointer[time.Location]

func init() { location.Store(mustLoad("Asia/Tehran")) }

// SetLocation sets the time zone dates are shown in.
func SetLocation(l *time.Location) {
	if l != nil {
		location.Store(l)
	}
}

// CurrentLocation is the time zone dates are shown in.
func CurrentLocation() *time.Location { return location.Load() }

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return l
}

var persianDigits = strings.NewReplacer(
	"0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴",
	"5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹",
	",", "٬", ".", "٫",
)

// Digits renders ASCII digits (and separators) in the language's script.
func Digits(lang, s string) string {
	if Normalize(lang) == "fa" {
		return persianDigits.Replace(s)
	}
	return s
}

// ParseNumber reads an integer typed by a user in Persian, Arabic or Latin
// digits, ignoring thousands separators and spaces.
func ParseNumber(s string) (int64, bool) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r == '-' && b.Len() == 0:
			b.WriteRune(r)
		case r == ',' || r == '٬' || r == '،' || r == ' ' || r == '_':
		default:
			return 0, false
		}
	}
	n, err := strconv.ParseInt(b.String(), 10, 64)
	return n, err == nil
}

// group inserts thousands separators into a non-negative integer string.
func group(s string) string {
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// Number formats an integer with thousands separators in the language.
func Number(lang string, n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := group(strconv.FormatInt(n, 10))
	if neg {
		s = "-" + s
	}
	return Digits(lang, s)
}

var currencyNames = map[string]map[string]string{
	"fa": {"IRT": "تومان", "USDT": "تتر"},
	"en": {"IRT": "Toman", "USDT": "USDT"},
}

var currencyScale = map[string]int{"IRT": 0, "USDT": 6}

// irtNames are the store's own names for the Toman (per language), from
// the branding settings; empty means the built-in name.
var irtNames atomic.Pointer[map[string]string]

// SetCurrencyNames sets the store's names for the Toman, per language.
func SetCurrencyNames(names map[string]string) {
	m := map[string]string{}
	for lang, n := range names {
		// Prices go into HTML messages unescaped: a name with markup is skipped.
		if n = strings.TrimSpace(n); n != "" && !strings.ContainsAny(n, "<>&") {
			m[Normalize(lang)] = n
		}
	}
	irtNames.Store(&m)
}

func currencyName(lang, currency string) string {
	if currency == "IRT" {
		if m := irtNames.Load(); m != nil && (*m)[lang] != "" {
			return (*m)[lang]
		}
	}
	if name := currencyNames[lang][currency]; name != "" {
		return name
	}
	return currency
}

// CurrencyName is the currency's name in the language (the store's own name
// for the Toman, when it set one; an unknown currency is its code).
func CurrencyName(lang, currency string) string { return currencyName(Normalize(lang), currency) }

// Money formats minor units in the currency's major unit, e.g. "150,000 Toman",
// "۱۵۰٬۰۰۰ تومان", "25.5 USDT".
func Money(lang string, minor int64, currency string) string {
	lang = Normalize(lang)
	name := currencyName(lang, currency)
	scale := currencyScale[currency]
	if scale == 0 {
		return Number(lang, minor) + " " + name
	}
	neg := minor < 0
	if neg {
		minor = -minor
	}
	div := int64(1)
	for i := 0; i < scale; i++ {
		div *= 10
	}
	whole := group(strconv.FormatInt(minor/div, 10))
	frac := strings.TrimRight(fmt.Sprintf("%0*d", scale, minor%div), "0")
	s := whole
	if frac != "" {
		s += "." + frac
	}
	if neg {
		s = "-" + s
	}
	return Digits(lang, s) + " " + name
}

// Exact writes minor units with every decimal of the currency, in Latin
// digits and without separators, e.g. "2.500123" USDT: the amount a customer
// copies into a wallet, in every language, never rounded or trimmed.
func Exact(minor int64, currency string) string {
	scale := currencyScale[currency]
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	if scale == 0 {
		return sign + strconv.FormatInt(minor, 10)
	}
	div := int64(1)
	for i := 0; i < scale; i++ {
		div *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", sign, minor/div, scale, minor%div)
}

// Bytes formats a traffic amount in GB (binary), e.g. "50 GB", "۱٫۵ گیگابایت".
func Bytes(lang string, b int64) string {
	gb := float64(b) / float64(1<<30)
	var s string
	if gb == float64(int64(gb)) {
		s = strconv.FormatInt(int64(gb), 10)
	} else {
		s = strconv.FormatFloat(gb, 'f', 1, 64)
	}
	if Normalize(lang) == "fa" {
		return Digits(lang, s) + " گیگابایت"
	}
	return s + " GB"
}

// FileSize formats a file's size in MB (binary, one decimal) below 1 GB and
// in GB (as Bytes) from there, e.g. "48.3 MB", "۴۸٫۳ مگابایت".
func FileSize(lang string, b int64) string {
	if b >= 1<<30 {
		return Bytes(lang, b)
	}
	s := strconv.FormatFloat(float64(b)/float64(1<<20), 'f', 1, 64)
	if Normalize(lang) == "fa" {
		return Digits(lang, s) + " مگابایت"
	}
	return s + " MB"
}

var persianMonths = [...]string{"فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور", "مهر", "آبان", "آذر", "دی", "بهمن", "اسفند"}

// Date formats a day: Jalali in Persian ("۱۱ آبان ۱۴۰۵"), "2 Nov 2026" in English.
func Date(lang string, t time.Time) string {
	t = t.In(CurrentLocation())
	return CalendarDay(lang, t.Year(), t.Month(), t.Day())
}

// CalendarDay formats a calendar date as Date does, with no clock to move it
// across a day's edge: the day a report names stays that day.
func CalendarDay(lang string, y int, m time.Month, d int) string {
	if Normalize(lang) == "fa" {
		jy, jm, jd := GregorianToJalali(y, int(m), d)
		return Digits(lang, strconv.Itoa(jd)) + " " + persianMonths[jm-1] + " " + Digits(lang, strconv.Itoa(jy))
	}
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Format("2 Jan 2006")
}

// Percent formats a whole percent: "12%", "۱۲٪".
func Percent(lang string, n int64) string {
	if Normalize(lang) == "fa" {
		return Digits(lang, strconv.FormatInt(n, 10)) + "٪"
	}
	return strconv.FormatInt(n, 10) + "%"
}

// Clock formats a time of day in the store's time zone, 24-hour: "14:05",
// "۱۴:۰۵".
func Clock(lang string, t time.Time) string {
	return Digits(lang, t.In(CurrentLocation()).Format("15:04"))
}

// DateTime is a day and its time: "2 Nov 2026, 14:05", "۱۱ آبان ۱۴۰۵، ۱۴:۰۵".
func DateTime(lang string, t time.Time) string {
	sep := ", "
	if Normalize(lang) == "fa" {
		sep = "، "
	}
	return Date(lang, t) + sep + Clock(lang, t)
}

// GregorianToJalali converts a Gregorian date to the Persian (Solar Hijri)
// calendar (the widely used arithmetic algorithm, exact for 1300-1500 SH).
func GregorianToJalali(gy, gm, gd int) (jy, jm, jd int) {
	gdm := [12]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 355666 + 365*gy + (gy2+3)/4 - (gy2+99)/100 + (gy2+399)/400 + gd + gdm[gm-1]
	jy = -1595 + 33*(days/12053)
	days %= 12053
	jy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}
	if days < 186 {
		return jy, 1 + days/31, 1 + days%31
	}
	return jy, 7 + (days-186)/30, 1 + (days-186)%30
}
