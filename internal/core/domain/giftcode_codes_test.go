package domain

import (
	"bytes"
	crand "crypto/rand"
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"
)

// runes builds a string from code points, so the invisible characters these tests
// are about never sit in the source file as themselves.
func runes(cps ...rune) string { return string(cps) }

func TestGiftAlphabet(t *testing.T) {
	seen := map[rune]bool{}
	for _, c := range giftAlphabet {
		if seen[c] {
			t.Errorf("%c twice in the alphabet", c)
		}
		seen[c] = true
		if strings.ContainsRune("01OIL", c) {
			t.Errorf("%c looks like another character, and must not be in the alphabet", c)
		}
		if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '9') {
			t.Errorf("%c is not a capital or a digit", c)
		}
	}
	if len(giftAlphabet) != 31 {
		t.Fatalf("%d symbols: the sampler's limit (248) assumes 31", len(giftAlphabet))
	}
	// The rule the feature was asked for: at least 50 bits.
	if bits := giftEntropyBits(); bits < 50 {
		t.Fatalf("a generated code has %.1f bits of entropy, at least 50 are needed", bits)
	}
	if giftBodyLen != 12 || giftBodyLen%giftGroupLen != 0 {
		t.Fatalf("the body is %d characters, in groups of %d", giftBodyLen, giftGroupLen)
	}
}

func TestRandomGiftBodyShapeAndUniformity(t *testing.T) {
	// 30 000 codes from the real source: every character of the alphabet, about equally often.
	counts := map[rune]int{}
	seen := map[string]bool{}
	const codes = 30_000
	for range codes {
		body, err := randomGiftBody(crand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) != giftBodyLen {
			t.Fatalf("body %q is %d characters", body, len(body))
		}
		if seen[body] {
			t.Fatalf("the body %q was drawn twice in %d draws", body, codes)
		}
		seen[body] = true
		for _, c := range body {
			counts[c]++
		}
	}
	total := codes * giftBodyLen
	want := float64(total) / float64(len(giftAlphabet))
	for _, c := range giftAlphabet {
		got := float64(counts[c])
		// 360 000 draws over 31 symbols: about 11 600 each, a standard deviation near 106; 6 of them is ample room.
		if math.Abs(got-want) > 6*math.Sqrt(want*(1-1.0/31)) {
			t.Errorf("%c came %d times, about %.0f expected: the draw is biased", c, counts[c], want)
		}
	}
	if len(counts) != len(giftAlphabet) {
		t.Errorf("%d different characters drawn, the alphabet has %d", len(counts), len(giftAlphabet))
	}
}

func TestRandomGiftBodyThrowsAwayTheBytesThatWouldBiasIt(t *testing.T) {
	// 248 and above are refused (256 does not divide by 31). Four chunks of them come first, all skipped; then
	// the bytes 0, 30, 31 and 61 are the first symbol, the last, the first again (31 wraps) and the last again.
	src := bytes.NewReader(append(bytes.Repeat([]byte{248, 255, 250, 254}, 32), bytes.Repeat([]byte{0, 30, 31, 61}, 8)...))
	body, err := randomGiftBody(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Repeat(string([]byte{giftAlphabet[0], giftAlphabet[30]}), giftBodyLen/2); body != want {
		t.Fatalf("body %q, want %q", body, want)
	}
	if giftAlphabet[30] != '9' {
		t.Fatalf("the last symbol is %c", giftAlphabet[30])
	}
}

func TestRandomGiftBodyRefusesABadSource(t *testing.T) {
	if _, err := randomGiftBody(bytes.NewReader(bytes.Repeat([]byte{255}, 64*32+1))); !errors.Is(err, errGiftRandom) {
		t.Fatalf("a source of only unusable bytes: %v", err)
	}
	if _, err := randomGiftBody(bytes.NewReader([]byte{1, 2, 3})); err == nil {
		t.Fatal("a source that ran out was accepted")
	}
	if _, err := randomGiftBody(errReader{}); err == nil {
		t.Fatal("a failing source was accepted")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestNormalizeGiftCode(t *testing.T) {
	const want = "ABCDEFGHJKMN"
	for name, in := range map[string]string{
		"as shown":              "ABCD-EFGH-JKMN",
		"small letters":         "abcd-efgh-jkmn",
		"no dashes":             "abcdefghjkmn",
		"spaces":                " abcd efgh jkmn ",
		"dots and underscores":  "abcd.efgh_jkmn",
		"a line break":          "ABCD-EFGH\nJKMN\r\n",
		"zero-width non-joiner": "ABCD" + runes(0x200C) + "-EFGH-JKMN",
		"direction marks":       runes(0x200F) + "ABCD-EFGH-JKMN" + runes(0x200E),
		"an embedding":          runes(0x202B) + "ABCD-EFGH-JKMN" + runes(0x202C),
		"a soft hyphen":         "ABCD" + runes(0x00AD) + "EFGHJKMN",
		"a no-break space":      "ABCD" + runes(0x00A0) + "EFGHJKMN",
		"a byte order mark":     runes(0xFEFF) + "ABCDEFGHJKMN",
		"typographic dashes":    "ABCD" + runes(0x2010) + "EFGH" + runes(0x2013) + "JKMN",
		"full-width capitals":   runes(0xFF21, 0xFF22, 0xFF23, 0xFF24) + "-EFGH-JKMN",
		"full-width small":      runes(0xFF41, 0xFF42, 0xFF43, 0xFF44) + "efghjkmn",
	} {
		if got, ok := normalizeGiftCode(in); !ok || got != want {
			t.Errorf("%s: %q gave %q %v, want %q", name, in, got, ok, want)
		}
	}
	// Persian and Arabic-Indic digits are the digits a code has.
	for in, want := range map[string]string{
		runes(0x06F2, 0x06F3, 0x06F4, 0x06F5, 0x06F6, 0x06F7, 0x06F8, 0x06F9): "23456789",
		runes(0x0662, 0x0663, 0x0664, 0x0665, 0x0666, 0x0667, 0x0668, 0x0669): "23456789",
		runes(0xFF12, 0xFF13, 0xFF14, 0xFF15, 0xFF16, 0xFF17, 0xFF18, 0xFF19): "23456789",
		"nowruz-" + runes(0x06F1, 0x06F4, 0x06F0, 0x06F5):                     "NOWRUZ1405",
	} {
		if got, ok := normalizeGiftCode(in); !ok || got != want {
			t.Errorf("digits %q gave %q %v, want %q", in, got, ok, want)
		}
	}
	// What is not a code is not guessed at.
	for name, in := range map[string]string{
		"empty":                 "",
		"only separators":       " - - ",
		"too short":             "ABC-DEF",
		"seven characters":      "ABCDEFG",
		"too long":              strings.Repeat("A", giftMaxLen+1),
		"a punctuation mark":    "ABCD-EFGH-JKM!",
		"a slash":               "ABCD/EFGH/JKMN",
		"a Persian letter":      "ABCD-EFGH-JK" + runes(0x0645) + "N",
		"a Cyrillic look-alike": "ABCD-EFGH-" + runes(0x0410) + "KMN",
		"an emoji":              "ABCD-EFGH-JKM" + runes(0x1F381),
		"a Persian sentence":    runes(0x06A9, 0x062F, 0x0020, 0x0647, 0x062F, 0x06CC, 0x0647) + " ABCD",
		"a mark in a word":      "ABCD" + runes(0x0301) + "EFGHJKMN",
		"huge input":            strings.Repeat("A", giftMaxInput+1),
		"huge input of dashes":  strings.Repeat("-", 10_000) + "ABCDEFGHJKMN",
	} {
		if got, ok := normalizeGiftCode(in); ok {
			t.Errorf("%s: %q was taken as the code %q", name, in, got)
		}
	}
	// The longest code there is, and the shortest custom one, are codes.
	if got, ok := normalizeGiftCode(strings.Repeat("a", giftMaxLen)); !ok || got != strings.Repeat("A", giftMaxLen) {
		t.Errorf("a code of %d characters: %q %v", giftMaxLen, got, ok)
	}
	if _, ok := normalizeGiftCode(strings.Repeat("a", giftMinCustomLen)); !ok {
		t.Errorf("a code of %d characters is refused", giftMinCustomLen)
	}
}

func TestFormatGiftCode(t *testing.T) {
	for _, c := range []struct{ prefix, body, want string }{
		{"", "ABCDEFGHJKMN", "ABCD-EFGH-JKMN"},
		{"NOWRUZ", "ABCDEFGHJKMN", "NOWRUZ-ABCD-EFGH-JKMN"},
		{"", "ABCDEFGHJK", "ABCD-EFGH-JK"},
		{"X", "ABCD", "X-ABCD"},
	} {
		if got := formatGiftCode(c.prefix, c.body); got != c.want {
			t.Errorf("formatGiftCode(%q, %q) = %q, want %q", c.prefix, c.body, got, c.want)
		}
	}
	// What is shown is what a customer types back: it normalises to the stored form.
	shown := formatGiftCode("SPRING", "ABCDEFGHJKMN")
	if got, ok := normalizeGiftCode(shown); !ok || got != "SPRINGABCDEFGHJKMN" {
		t.Errorf("%q normalises to %q %v", shown, got, ok)
	}
	if !regexp.MustCompile(`^([A-HJKMNP-Z2-9]{4}-){2}[A-HJKMNP-Z2-9]{4}$`).MatchString(formatGiftCode("", "ABCDEFGHJKMN")) {
		t.Error("a generated code is not three groups of four")
	}
}

func TestCheckCustomGiftCode(t *testing.T) {
	for in, want := range map[string]string{
		"nowruz-1405":      "NOWRUZ-1405",
		"  Nowruz 1405  ":  "NOWRUZ-1405",
		"NOWRUZ1405":       "NOWRUZ1405",
		"happy--new__year": "HAPPY-NEW-YEAR",
		"-BOBRES-GIFT-":    "BOBRES-GIFT",
		"bobres" + runes(0x200C) + "-" + runes(0x06F1, 0x06F4, 0x06F0, 0x06F5): "BOBRES-1405",
	} {
		canon, display, err := checkCustomGiftCode(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if display != want {
			t.Errorf("%q shows as %q, want %q", in, display, want)
		}
		if back, ok := normalizeGiftCode(display); !ok || back != canon {
			t.Errorf("%q: the shown form %q normalises to %q, not %q", in, display, back, canon)
		}
	}
	for name, in := range map[string]string{
		"too short":               "ABC12",
		"seven characters":        "ABCDEFG",
		"one character repeated":  "AAAAAAAAAAAA",
		"two characters repeated": "ABABABABABAB",
		"four different":          "ABCDABCDABCD",
		"a symbol":                "GIFT-2026!",
		"a Persian word":          runes(0x0647, 0x062F, 0x06CC, 0x0647, 0x0647, 0x062F, 0x06CC, 0x0647),
		"over 32 characters":      strings.Repeat("AB12CD34", 5),
		"empty":                   "",
		"only separators":         "----",
	} {
		if _, _, err := checkCustomGiftCode(in); err == nil {
			t.Errorf("%s: %q was accepted", name, in)
		} else if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: the refusal is not an ErrInvalid: %v", name, err)
		}
	}
	// Five different characters are enough.
	if _, _, err := checkCustomGiftCode("ABCDEABCDE"); err != nil {
		t.Errorf("five different characters: %v", err)
	}
}

func TestNormalizeGiftPrefix(t *testing.T) {
	for in, want := range map[string]string{"": "", "nowruz": "NOWRUZ", " no-wruz ": "NOWRUZ", "A1": "A1", "12345678": "12345678"} {
		if got, ok := normalizeGiftPrefix(in); !ok || got != want {
			t.Errorf("prefix %q gave %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"123456789", "AB!", "ab" + runes(0x0645), strings.Repeat("A", giftMaxInput+1)} {
		if got, ok := normalizeGiftPrefix(in); ok {
			t.Errorf("prefix %q was taken as %q", in, got)
		}
	}
}
