package i18n

import (
	"regexp"
	"strconv"
	"strings"
)

// A text that counts something by a placeholder picks the word the number
// takes in the language with a plural selector:
//
//	{n|# день|# дня|# дней}
//
// The forms follow the language's plural categories (CLDR), in this order:
//
//	ru: one | few | many                      (1, 21 день; 2-4, 22 дня; 5-20, 11, 25 дней)
//	ar: zero | one | two | few | many | other (0; 1; 2; 3-10; 11-99; 100+)
//	en, fa, tr: one | other
//
// '#' in a form is the value as the text shows it. The value is read back as
// a number (Persian, Arabic-Indic or Latin digits, with the language's
// separators); a fraction takes the form fractions take (Russian "few",
// Arabic "other", "other" elsewhere), and a value that is not a number the
// last form. Turkish keeps the noun singular after a number, so its texts
// need no selector, and the Persian and English texts have none.

// pluralRe finds a selector: a placeholder name, then forms after '|'.
var pluralRe = regexp.MustCompile(`\{([a-z_]+)((?:\|[^{}|\n]*)+)\}`)

// PluralForms is the number of forms a selector takes in the language.
func PluralForms(lang string) int {
	switch lang {
	case "ru":
		return 3
	case "ar":
		return 6
	}
	return 2
}

// PluralIndex is the form (0-based, PluralForms of them) a count takes in the
// language; integer is false for a fraction.
func PluralIndex(lang string, n int64, integer bool) int {
	if n < 0 {
		n = -n
	}
	switch lang {
	case "ru":
		switch {
		case !integer:
			return 1
		case n%10 == 1 && n%100 != 11:
			return 0
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			return 1
		}
		return 2
	case "ar":
		switch {
		case !integer:
			return 5
		case n == 0:
			return 0
		case n == 1:
			return 1
		case n == 2:
			return 2
		case n%100 >= 3 && n%100 <= 10:
			return 3
		case n%100 >= 11:
			return 4
		}
		return 5
	case "fa": // CLDR: one is 0 and 1
		if integer && n <= 1 {
			return 0
		}
		return 1
	}
	if integer && n == 1 {
		return 0
	}
	return 1
}

// pluralNumber reads a value as the language writes numbers: "1,234" (en),
// "۱٬۲۳۴" (fa), "١٬٢٣٤" (ar), "1 234,5" (ru), "1.234,5" (tr). ok is false for
// anything else (an amount with its unit, a name).
func pluralNumber(lang, s string) (n int64, integer, ok bool) {
	group, decimal := ",", "."
	switch lang {
	case "fa", "ar":
		group, decimal = "٬,", "٫."
	case "ru":
		group, decimal = " \u00a0\u202f", ","
	case "tr":
		group, decimal = ".", ","
	}
	var whole strings.Builder
	inFrac, fraction, seenDigit := false, false, false
	for i, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9', r >= '۰' && r <= '۹', r >= '٠' && r <= '٩':
			seenDigit = true
			if inFrac {
				fraction = fraction || digitValue(r) != 0 // "2.0" is still 2
				continue
			}
			whole.WriteRune('0' + digitValue(r))
		case r == '-' && i == 0:
		case strings.ContainsRune(decimal, r) && seenDigit && !inFrac:
			inFrac = true
		case strings.ContainsRune(group, r) && seenDigit && !inFrac:
		default:
			return 0, false, false
		}
	}
	if !seenDigit {
		return 0, false, false
	}
	v, err := strconv.ParseInt(whole.String(), 10, 64)
	if err != nil {
		return 0, false, false
	}
	return v, !fraction, true
}

func digitValue(r rune) rune {
	switch {
	case r >= '۰' && r <= '۹':
		return r - '۰'
	case r >= '٠' && r <= '٩':
		return r - '٠'
	}
	return r - '0'
}

// selectPlurals replaces every selector of s with the form its value takes.
func selectPlurals(lang, s string, values map[string]string) string {
	if !strings.Contains(s, "|") {
		return s
	}
	return pluralRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := pluralRe.FindStringSubmatch(m)
		forms := strings.Split(sub[2][1:], "|")
		value := values[sub[1]]
		idx := len(forms) - 1
		if n, integer, ok := pluralNumber(lang, value); ok {
			idx = min(PluralIndex(lang, n, integer), len(forms)-1)
		}
		return strings.ReplaceAll(forms[idx], "#", value)
	})
}

// selectors lists the selectors of a text: name and number of forms.
func selectors(s string) [][2]string {
	var out [][2]string
	for _, m := range pluralRe.FindAllStringSubmatch(s, -1) {
		out = append(out, [2]string{m[1], strconv.Itoa(strings.Count(m[2], "|"))})
	}
	return out
}
