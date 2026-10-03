package i18n

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCatalogsHaveTheSameKeysAndPlaceholders(t *testing.T) {
	c := MustLoad()
	en, fa := c.Keys("en"), c.Keys("fa")
	if strings.Join(en, ",") != strings.Join(fa, ",") {
		missing := map[string]bool{}
		for _, k := range en {
			missing[k] = true
		}
		for _, k := range fa {
			delete(missing, k)
		}
		t.Fatalf("catalog keys differ; missing in fa: %v", missing)
	}
	ph := regexp.MustCompile(`\{[a-z_]+\}`)
	for _, k := range en {
		a := ph.FindAllString(c.T("en", k), -1)
		b := ph.FindAllString(c.T("fa", k), -1)
		if strings.Join(sorted(a), ",") != strings.Join(sorted(b), ",") {
			t.Errorf("%s: placeholders differ: en %v fa %v", k, a, b)
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
	d := time.Date(2026, 11, 2, 12, 0, 0, 0, Location)
	if got := Date("fa", d); got != "۱۱ آبان ۱۴۰۵" {
		t.Errorf("fa date %q", got)
	}
	if got := Date("en", d); got != "2 Nov 2026" {
		t.Errorf("en date %q", got)
	}
}
