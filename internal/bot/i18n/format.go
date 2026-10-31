package i18n

import (
	"fmt"
	"slices"
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

var arabicDigits = strings.NewReplacer(
	"0", "٠", "1", "١", "2", "٢", "3", "٣", "4", "٤",
	"5", "٥", "6", "٦", "7", "٧", "8", "٨", "9", "٩",
	",", "٬", ".", "٫",
)

// Russian groups thousands with a no-break space and writes a decimal comma;
// Turkish groups with a dot and writes a decimal comma.
var (
	russianSeparators = strings.NewReplacer(",", "\u00a0", ".", ",")
	turkishSeparators = strings.NewReplacer(",", ".", ".", ",")
)

// Digits renders ASCII digits (and the "," and "." separators of "1,234.5")
// as the language writes them: Persian or Arabic-Indic digits, and the
// separators of Russian and Turkish.
func Digits(lang, s string) string {
	switch Normalize(lang) {
	case "fa":
		return persianDigits.Replace(s)
	case "ar":
		return arabicDigits.Replace(s)
	case "ru":
		return russianSeparators.Replace(s)
	case "tr":
		return turkishSeparators.Replace(s)
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
		case r == ',' || r == '٬' || r == '،' || r == ' ' || r == '_' || r == '\u00a0' || r == '\u202f':
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

// currencyNames are the built-in names; "a|b|c" are the plural forms of a
// name (PluralForms), chosen by the amount's whole number.
var currencyNames = map[string]map[string]string{
	"fa": {"IRT": "تومان", "USDT": "تتر", "TON": "TON"},
	"en": {"IRT": "Toman", "USDT": "USDT", "TON": "TON"},
	"ar": {"IRT": "تومان", "USDT": "USDT", "TON": "TON"},
	"ru": {"IRT": "томан|томана|томанов", "USDT": "USDT", "TON": "TON"},
	"tr": {"IRT": "Toman", "USDT": "USDT", "TON": "TON"},
}

// pluralName picks the form of a currency name for an amount.
func pluralName(lang, name string, whole int64, integer bool) string {
	forms := strings.Split(name, "|")
	if len(forms) == 1 {
		return name
	}
	return forms[min(PluralIndex(lang, whole, integer), len(forms)-1)]
}

var currencyScale = map[string]int{"IRT": 0, "USDT": 6, "TON": 9}

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

// storeIRTName is the store's own name for the Toman in lang from names (its
// branding settings, written in Persian and English): a beta language takes
// the English one, since a name like "thousand Toman" changes what a price
// means. "" when the store set none.
func storeIRTName(names map[string]string, lang string) string {
	if n := strings.TrimSpace(names[lang]); n != "" {
		return n
	}
	if slices.Contains(Beta, lang) {
		return strings.TrimSpace(names["en"])
	}
	return ""
}

// currencyName is the name of the currency in lang, with its plural forms
// still joined by '|' (pluralName picks one).
func currencyName(lang, currency string) string {
	if currency == "IRT" {
		if m := irtNames.Load(); m != nil {
			if n := storeIRTName(*m, lang); n != "" {
				return n
			}
		}
	}
	if name := currencyNames[lang][currency]; name != "" {
		return name
	}
	return currency
}

// CurrencyName is the currency's name in the language (the store's own name
// for the Toman, when it set one; an unknown currency is its code), in the
// form that goes with "many" where the language has plural forms.
func CurrencyName(lang, currency string) string {
	forms := strings.Split(currencyName(Normalize(lang), currency), "|")
	return forms[len(forms)-1]
}

// Money formats minor units in the currency's major unit, e.g. "150,000 Toman",
// "۱۵۰٬۰۰۰ تومان", "25.5 USDT".
func Money(lang string, minor int64, currency string) string {
	lang = Normalize(lang)
	return money(lang, minor, currency, currencyName(lang, currency))
}

// MoneyNamed is Money with the store's names for the Toman given (per language;
// an empty or missing one is the built-in name): what core writes into a
// message the bot sends as it is, since only the bot process holds the names
// SetCurrencyNames set.
func MoneyNamed(irtNames map[string]string, lang string, minor int64, currency string) string {
	lang = Normalize(lang)
	name := currencyNames[lang][currency]
	if name == "" {
		name = currency
	}
	if n := storeIRTName(irtNames, lang); currency == "IRT" && n != "" && !strings.ContainsAny(n, "<>&") {
		name = n
	}
	return money(lang, minor, currency, name)
}

func money(lang string, minor int64, currency, name string) string {
	scale := currencyScale[currency]
	if scale == 0 {
		w := minor
		if w < 0 {
			w = -w
		}
		return Number(lang, minor) + " " + pluralName(lang, name, w, true)
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
	return Digits(lang, s) + " " + pluralName(lang, name, minor/div, frac == "")
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
	return Digits(lang, s) + " " + sizeUnits[Normalize(lang)][1]
}

// sizeUnits are MB and GB in each language.
var sizeUnits = map[string][2]string{
	"fa": {"مگابایت", "گیگابایت"},
	"en": {"MB", "GB"},
	"ar": {"ميغابايت", "غيغابايت"},
	"ru": {"МБ", "ГБ"},
	"tr": {"MB", "GB"},
}

// FileSize formats a file's size in MB (binary, one decimal) below 1 GB and
// in GB (as Bytes) from there, e.g. "48.3 MB", "۴۸٫۳ مگابایت".
func FileSize(lang string, b int64) string {
	if b >= 1<<30 {
		return Bytes(lang, b)
	}
	s := strconv.FormatFloat(float64(b)/float64(1<<20), 'f', 1, 64)
	return Digits(lang, s) + " " + sizeUnits[Normalize(lang)][0]
}

var persianMonths = [...]string{"فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور", "مهر", "آبان", "آذر", "دی", "بهمن", "اسفند"}

// gregorianMonths are the months of the Gregorian calendar the beta languages
// use, as a date writes them ("2 ноября 2026" takes the genitive).
var gregorianMonths = map[string][12]string{
	"ar": {"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو", "يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"},
	"ru": {"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"},
	"tr": {"Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"},
}

// Date formats a day: Jalali in Persian ("۱۱ آبان ۱۴۰۵"), Gregorian in the
// others: "2 Nov 2026", "٢ نوفمبر ٢٠٢٦", "2 ноября 2026", "2 Kasım 2026".
func Date(lang string, t time.Time) string {
	t = t.In(CurrentLocation())
	return CalendarDay(lang, t.Year(), t.Month(), t.Day())
}

// CalendarDay formats a calendar date as Date does, with no clock to move it
// across a day's edge: the day a report names stays that day.
func CalendarDay(lang string, y int, m time.Month, d int) string {
	switch lang = Normalize(lang); lang {
	case "fa":
		jy, jm, jd := GregorianToJalali(y, int(m), d)
		return Digits(lang, strconv.Itoa(jd)) + " " + persianMonths[jm-1] + " " + Digits(lang, strconv.Itoa(jy))
	case "ar", "ru", "tr":
		return Digits(lang, strconv.Itoa(d)) + " " + gregorianMonths[lang][m-1] + " " + Digits(lang, strconv.Itoa(y))
	}
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Format("2 Jan 2006")
}

// Percent formats a whole percent: "12%", "۱۲٪", "١٢٪", "12 %", "%12".
func Percent(lang string, n int64) string {
	return PercentText(lang, Digits(lang, strconv.FormatInt(n, 10)))
}

// PercentText puts the percent sign where the language writes it, round a
// number already written in the language (Digits).
func PercentText(lang, number string) string {
	switch Normalize(lang) {
	case "fa", "ar":
		return number + "٪"
	case "ru":
		return number + "\u00a0%"
	case "tr":
		return "%" + number
	}
	return number + "%"
}

// Clock formats a time of day in the store's time zone, 24-hour: "14:05",
// "۱۴:۰۵".
func Clock(lang string, t time.Time) string {
	return Digits(lang, t.In(CurrentLocation()).Format("15:04"))
}

// DateTime is a day and its time: "2 Nov 2026, 14:05", "۱۱ آبان ۱۴۰۵، ۱۴:۰۵".
func DateTime(lang string, t time.Time) string {
	sep := ", "
	if l := Normalize(lang); l == "fa" || l == "ar" {
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
