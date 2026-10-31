package i18n

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Every language has every text, with the same placeholders (a plural
// selector {n|…} uses its placeholder), the same HTML tags and the same
// emoji as the English one: a translation may move them, never drop one.
func TestCatalogsHaveTheSameKeysAndPlaceholders(t *testing.T) {
	c := MustLoad()
	en := c.Keys("en")
	tags := regexp.MustCompile(`</?[a-z-]+[^<>]*>`)
	for _, lang := range All[1:] {
		got := c.Keys(lang)
		if strings.Join(en, ",") != strings.Join(got, ",") {
			missing := map[string]bool{}
			for _, k := range en {
				missing[k] = true
			}
			for _, k := range got {
				delete(missing, k)
			}
			t.Fatalf("catalog keys differ; missing in %s: %v (and %d extra)", lang, missing, len(got)+len(missing)-len(en))
		}
		for _, k := range en {
			a, b := c.Default("en", k), c.Default(lang, k)
			if strings.Join(placeholders(a), ",") != strings.Join(placeholders(b), ",") {
				t.Errorf("%s/%s: placeholders differ: en %v, %s %v", lang, k, placeholders(a), lang, placeholders(b))
			}
			if x, y := sorted(tags.FindAllString(a, -1)), sorted(tags.FindAllString(b, -1)); strings.Join(x, "") != strings.Join(y, "") {
				t.Errorf("%s/%s: tags differ: en %v, %s %v", lang, k, x, lang, y)
			}
		}
	}
}

// The beta languages are machine-translated: every text that counts with a
// selector has the forms of its language (Russian three, Arabic six), and
// Turkish, which keeps the noun singular after a number, has none.
func TestPluralSelectorsInTheCatalogs(t *testing.T) {
	c := MustLoad()
	used := map[string]int{}
	for _, lang := range All {
		for _, k := range c.Keys(lang) {
			for _, s := range selectors(c.Default(lang, k)) {
				used[lang]++
				if s[1] != strconv.Itoa(PluralForms(lang)) {
					t.Errorf("%s/%s: {%s|…} has %s forms, %s takes %d", lang, k, s[0], s[1], lang, PluralForms(lang))
				}
			}
		}
	}
	if used["ru"] == 0 || used["ar"] == 0 {
		t.Errorf("no plural selectors in the Russian or Arabic catalog: %v", used)
	}
	if used["tr"] != 0 || used["en"] != 0 || used["fa"] != 0 {
		t.Errorf("selectors where none are written: %v", used)
	}
}

func TestPluralRules(t *testing.T) {
	cases := []struct {
		lang string
		n    int64
		want int
	}{
		{"ru", 1, 0}, {"ru", 21, 0}, {"ru", 101, 0}, {"ru", 11, 2}, {"ru", 111, 2},
		{"ru", 2, 1}, {"ru", 4, 1}, {"ru", 22, 1}, {"ru", 12, 2}, {"ru", 14, 2},
		{"ru", 0, 2}, {"ru", 5, 2}, {"ru", 20, 2}, {"ru", 25, 2},
		{"ar", 0, 0}, {"ar", 1, 1}, {"ar", 2, 2}, {"ar", 3, 3}, {"ar", 10, 3}, {"ar", 103, 3},
		{"ar", 11, 4}, {"ar", 99, 4}, {"ar", 111, 4}, {"ar", 100, 5}, {"ar", 102, 5},
		{"en", 1, 0}, {"en", 0, 1}, {"en", 2, 1}, {"tr", 1, 0}, {"tr", 5, 1}, {"fa", 0, 0}, {"fa", 1, 0}, {"fa", 2, 1},
	}
	for _, c := range cases {
		if got := PluralIndex(c.lang, c.n, true); got != c.want {
			t.Errorf("PluralIndex(%s, %d) = %d, want %d", c.lang, c.n, got, c.want)
		}
	}
	if PluralIndex("ru", 1, false) != 1 || PluralIndex("ar", 2, false) != 5 || PluralIndex("en", 1, false) != 1 {
		t.Error("a fraction takes the fraction form")
	}
}

// T picks a selector's form from the value as the language writes it.
func TestTPicksPluralForms(t *testing.T) {
	c := MustLoad()
	ru := "{n|# день|# дня|# дней}"
	ar := "{n|لا أيام|يوم واحد|يومان|# أيام|# يومًا|# يوم}"
	c.base["ru"]["test.days"], c.base["ar"]["test.days"], c.base["en"]["test.days"] = ru, ar, "{n} days"
	for _, tc := range []struct{ lang, value, want string }{
		{"ru", "1", "1 день"}, {"ru", "3", "3 дня"}, {"ru", "11", "11 дней"}, {"ru", "1\u00a0001", "1\u00a0001 день"},
		{"ru", "1,5", "1,5 дня"}, {"ru", "5 ГБ", "5 ГБ дней"},
		{"ar", "٠", "لا أيام"}, {"ar", "١", "يوم واحد"}, {"ar", "٢", "يومان"}, {"ar", "٧", "٧ أيام"},
		{"ar", "١٥", "١٥ يومًا"}, {"ar", "١٠٠", "١٠٠ يوم"}, {"ar", "١٬٠٠٣", "١٬٠٠٣ أيام"},
		{"en", "1", "1 days"},
	} {
		if got := c.T(tc.lang, "test.days", "n", tc.value); got != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.lang, tc.value, got, tc.want)
		}
	}
}

// The store chooses the languages customers may pick: any other language
// (one switched off later, an unknown Telegram language) is the store's
// default, the first of the list.
func TestEnabledLanguages(t *testing.T) {
	defer enabled.Store(nil)
	if got := ParseEnabled(""); strings.Join(got, ",") != "fa,en" {
		t.Errorf("default %v", got)
	}
	if got := ParseEnabled(" RU, en,xx,ru;ar "); strings.Join(got, ",") != "ru,en,ar" {
		t.Errorf("parse %v", got)
	}
	if got := Normalize("ar"); got != "ar" { // core never sets them: every language
		t.Errorf("unset: %q", got)
	}
	SetEnabled(ParseEnabled(DefaultEnabled))
	for in, want := range map[string]string{"ar": "fa", "ru-RU": "fa", "en-GB": "en", "": "fa", "fa": "fa"} {
		if got := Normalize(in); got != want {
			t.Errorf("fa,en: Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	SetEnabled(ParseEnabled("en,ru,tr"))
	for in, want := range map[string]string{"ru": "ru", "tr-TR": "tr", "fa": "en", "ar": "en", "xx": "en"} {
		if got := Normalize(in); got != want {
			t.Errorf("en,ru,tr: Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if Default() != "en" {
		t.Errorf("default %q", Default())
	}
	c := MustLoad()
	if got := c.T("fa", "btn.buy"); got != c.Default("en", "btn.buy") {
		t.Errorf("a language switched off reads the store's default: %q", got)
	}
	SetEnabled(ParseEnabled("ar,fa,en"))
	if !RTL("ar") || !RTL("fa") || RTL("en") {
		t.Error("RTL")
	}
	if got := ContentOrder("ar"); strings.Join(got, ",") != "ar,en,fa" {
		t.Errorf("content order %v", got)
	}
	if got := ContentOrder("fa"); strings.Join(got, ",") != "fa,en" {
		t.Errorf("content order %v", got)
	}
}

// Numbers, money, sizes and dates in the beta languages: Arabic-Indic digits
// for Arabic, the separators of Russian and Turkish, Gregorian dates, and the
// Toman in the plural form its amount takes in Russian.
func TestBetaFormatting(t *testing.T) {
	at := time.Date(2026, 11, 2, 9, 5, 0, 0, CurrentLocation())
	for got, want := range map[string]string{
		Money("ar", 150000, "IRT"):     "١٥٠٬٠٠٠ تومان",
		Money("ru", 150000, "IRT"):     "150\u00a0000 томанов",
		Money("ru", 1, "IRT"):          "1 томан",
		Money("ru", 22, "IRT"):         "22 томана",
		Money("tr", 150000, "IRT"):     "150.000 Toman",
		Money("ru", 25_500_000, "USDT"): "25,5 USDT",
		Money("tr", 25_500_000, "USDT"): "25,5 USDT",
		Money("ar", 1_250_000, "USDT"): "١٫٢٥ USDT",
		Bytes("ar", 3<<29):             "١٫٥ غيغابايت",
		Bytes("ru", 3<<29):             "1,5 ГБ",
		FileSize("tr", 5<<19):          "2,5 MB",
		Percent("ar", 12):              "١٢٪",
		Percent("ru", 12):              "12\u00a0%",
		Percent("tr", 12):              "%12",
		Date("ar", at):                 "٢ نوفمبر ٢٠٢٦",
		Date("ru", at):                 "2 ноября 2026",
		Date("tr", at):                 "2 Kasım 2026",
		DateTime("ar", at):             "٢ نوفمبر ٢٠٢٦، ٠٩:٠٥",
		CurrencyName("ru", "IRT"):      "томанов",
	} {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
	// A store that names the Toman ("thousand Toman") has it named in the beta
	// languages too, by its English name: a price must not change meaning.
	defer SetCurrencyNames(nil)
	SetCurrencyNames(map[string]string{"en": "K Toman", "fa": "هزار تومان"})
	if got := Money("ru", 150, "IRT"); got != "150 K Toman" {
		t.Errorf("store name: %q", got)
	}
	if got := MoneyNamed(map[string]string{"en": "K Toman"}, "tr", 1500, "IRT"); got != "1.500 K Toman" {
		t.Errorf("store name: %q", got)
	}
	for in, want := range map[string]int64{"150\u00a0000": 150000, "١٥٠٬٠٠٠": 150000} {
		if n, ok := ParseNumber(in); !ok || n != want {
			t.Errorf("ParseNumber(%q) = %d %v", in, n, ok)
		}
	}
}

// Every ledger kind core writes has a name in both languages: the wallet
// history and a reseller's statement show it (a missing one shows its key).
func TestLedgerKindsHaveNames(t *testing.T) {
	c := MustLoad()
	for _, kind := range []string{"topup", "purchase", "refund", "adjust", "trial_grant", "referral", "commission", "settlement", "gift_code",
		"cashback", "cashback_reversal", "target_bonus", "wheel_prize"} {
		for _, lang := range Languages {
			if k := "ledger." + kind; c.T(lang, k) == k {
				t.Errorf("%s: no text for %s", lang, k)
			}
		}
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestTAndOverrides(t *testing.T) {
	c := MustLoad()
	if got := c.T("en", "wallet.title", "balance", "5 Toman"); !strings.Contains(got, "5 Toman") {
		t.Fatalf("placeholder: %q", got)
	}
	if got := c.T("xx", "btn.buy"); got != c.T("fa", "btn.buy") {
		t.Fatalf("unknown language must fall back to Persian: %q", got)
	}
	if got := c.T("en", "no.such.key"); got != "no.such.key" {
		t.Fatalf("missing key: %q", got)
	}
	c.SetOverrides(map[string]string{"texts.fa.welcome": "سلام {brand}", "branding.name": "x"})
	if got := c.T("fa", "welcome", "brand", "VPN"); got != "سلام VPN" {
		t.Fatalf("override: %q", got)
	}
	if got := c.T("en", "welcome", "brand", "VPN"); strings.Contains(got, "سلام") {
		t.Fatalf("override leaked to another language: %q", got)
	}
}

func TestFormatting(t *testing.T) {
	cases := map[string]string{
		Money("en", 150000, "IRT"):      "150,000 Toman",
		Money("fa", 150000, "IRT"):      "۱۵۰٬۰۰۰ تومان",
		Money("en", 25_500_000, "USDT"): "25.5 USDT",
		Money("en", 3_000_000, "USDT"):  "3 USDT",
		Money("fa", 1_250_000, "USDT"):  "۱٫۲۵ تتر",
		Bytes("en", 50<<30):             "50 GB",
		Bytes("fa", 3<<29):              "۱٫۵ گیگابایت",
		FileSize("en", 48<<20+300<<10):  "48.3 MB",
		FileSize("fa", 5<<19):           "۲٫۵ مگابایت",
		FileSize("en", 3<<29):           "1.5 GB",
		Number("en", -1234567):          "-1,234,567",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
	for in, want := range map[string]int64{"۱۵۰٬۰۰۰": 150000, "150,000": 150000, "٢٠٠": 200, "-50 000": -50000} {
		if n, ok := ParseNumber(in); !ok || n != want {
			t.Errorf("ParseNumber(%q) = %d %v", in, n, ok)
		}
	}
	for _, bad := range []string{"", "12a", "1-2", "abc"} {
		if _, ok := ParseNumber(bad); ok {
			t.Errorf("ParseNumber(%q) accepted", bad)
		}
	}
}

// Exact is what a customer copies into a wallet: every decimal, Latin
// digits whatever the language, no separators, never rounded.
func TestExactAndClock(t *testing.T) {
	for _, c := range []struct {
		minor    int64
		currency string
		want     string
	}{
		{2_500_123, "USDT", "2.500123"},
		{2_500_100, "USDT", "2.500100"}, // trailing zeros kept: the amount is exact
		{1_234_000_001, "USDT", "1234.000001"},
		{7, "USDT", "0.000007"},
		{150_000, "IRT", "150000"},
		{-1_500_000, "USDT", "-1.500000"},
	} {
		if got := Exact(c.minor, c.currency); got != c.want {
			t.Errorf("Exact(%d, %s) = %q, want %q", c.minor, c.currency, got, c.want)
		}
	}
	at := time.Date(2026, 11, 2, 9, 5, 0, 0, CurrentLocation())
	for got, want := range map[string]string{
		Clock("en", at):    "09:05",
		Clock("fa", at):    "۰۹:۰۵",
		DateTime("en", at): "2 Nov 2026, 09:05",
		DateTime("fa", at): "۱۱ آبان ۱۴۰۵، ۰۹:۰۵",
	} {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestJalali(t *testing.T) {
	for _, c := range []struct {
		gy, gm, gd, jy, jm, jd int
	}{
		{2024, 3, 20, 1403, 1, 1}, // Nowruz 1403
		{2025, 3, 21, 1404, 1, 1}, // Nowruz 1404
		{2026, 3, 21, 1405, 1, 1}, // Nowruz 1405
		{2026, 10, 3, 1405, 7, 11},
		{2025, 3, 20, 1403, 12, 30}, // 1403 is a leap year
	} {
		y, m, d := GregorianToJalali(c.gy, c.gm, c.gd)
		if y != c.jy || m != c.jm || d != c.jd {
			t.Errorf("%d-%d-%d -> %d/%d/%d, want %d/%d/%d", c.gy, c.gm, c.gd, y, m, d, c.jy, c.jm, c.jd)
		}
	}
	d := time.Date(2026, 11, 2, 12, 0, 0, 0, CurrentLocation())
	if got := Date("fa", d); got != "۱۱ آبان ۱۴۰۵" {
		t.Errorf("fa date %q", got)
	}
	if got := Date("en", d); got != "2 Nov 2026" {
		t.Errorf("en date %q", got)
	}
}

// A calendar day stays the day it names whatever zone the bot shows dates in
// (the daily summary says which store-local day it is about), and a whole
// percent is written in the language.
func TestCalendarDayAndPercent(t *testing.T) {
	defer SetLocation(CurrentLocation())
	for _, zone := range []string{"Asia/Tehran", "Pacific/Kiritimati", "Pacific/Pago_Pago", "UTC"} {
		SetLocation(mustLoad(zone))
		if got := CalendarDay("en", 2026, time.October, 13); got != "13 Oct 2026" {
			t.Errorf("%s: en day %q", zone, got)
		}
		if got := CalendarDay("fa", 2026, time.October, 13); got != "۲۱ مهر ۱۴۰۵" {
			t.Errorf("%s: fa day %q", zone, got)
		}
	}
	for _, c := range []struct {
		lang string
		n    int64
		want string
	}{{"en", 12, "12%"}, {"fa", 12, "۱۲٪"}, {"fa", 100, "۱۰۰٪"}, {"en", 0, "0%"}, {"xx", 5, "۵٪"}} {
		if got := Percent(c.lang, c.n); got != c.want {
			t.Errorf("Percent(%q, %d) = %q, want %q", c.lang, c.n, got, c.want)
		}
	}
	if got, want := CurrencyName("fa", "USDT"), "تتر"; got != want {
		t.Errorf("fa USDT %q", got)
	}
	if got := CurrencyName("en", "XTR"); got != "XTR" {
		t.Errorf("an unknown currency is its code: %q", got)
	}
}
