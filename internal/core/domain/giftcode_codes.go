package domain

import (
	"errors"
	"io"
	"math"
	"strings"
)

// The shape of a gift code. A generated code is 12 characters of a 31-symbol
// alphabet (no 0, O, 1, I or L: nothing that is read or typed as another
// character), shown in groups of four: ABCD-EFGH-JKMN. That is 12 x log2(31)
// = 59.4 bits from the operating system's random source, far beyond what a
// guesser who is allowed five wrong codes an hour can ever reach.
const (
	giftAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	giftBodyLen  = 12
	giftGroupLen = 4
	// A custom code (a campaign's word, typed by staff) is at least this long
	// and made of at least giftMinDistinct different characters, so it is not
	// a short word, a run of one character or a sequence a guesser tries first.
	giftMinCustomLen = 8
	giftMinDistinct  = 5
	// No code, with its prefix, is longer than this; a longer string a
	// customer sends is not a code.
	giftMaxLen = 32
	// A prefix of a batch of generated codes ("NOWRUZ-...") is at most this long.
	giftMaxPrefixLen = 8
	// What a person may type or paste (bytes); a longer text is not a code and
	// is not looked at any further.
	giftMaxInput = 128
	// A gift code's hash is keyed and domain-separated, so it can never be a
	// hash another part of core made of the same bytes.
	giftHashContext = "bobres:gift-code:v1\x00"
)

// giftEntropyBits is the entropy of a generated code's random part.
func giftEntropyBits() float64 { return float64(giftBodyLen) * math.Log2(float64(len(giftAlphabet))) }

type giftRune int

const (
	giftChar giftRune = iota // a letter or digit that counts
	giftSep                  // a separator or an invisible mark: dropped
	giftBad                  // anything else: not a code
)

// giftRuneOf reads one character of what a person typed: capitals and small
// letters, Latin, Persian, Arabic-Indic and full-width digits and letters
// all become the one character a code is made of; spaces, dashes, dots,
// underscores and the invisible marks Persian keyboards and chat apps add
// (the zero-width non-joiner, direction marks, a soft hyphen) are dropped;
// every other character means this is not a code. Code points are written
// as numbers, so no invisible character sits in this file.
func giftRuneOf(r rune) (rune, giftRune) {
	switch {
	case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return r, giftChar
	case r >= 'a' && r <= 'z':
		return r - 'a' + 'A', giftChar
	case r >= 0x0660 && r <= 0x0669: // Arabic-Indic digits
		return '0' + (r - 0x0660), giftChar
	case r >= 0x06F0 && r <= 0x06F9: // Persian digits
		return '0' + (r - 0x06F0), giftChar
	case r >= 0xFF10 && r <= 0xFF19: // full-width digits
		return '0' + (r - 0xFF10), giftChar
	case r >= 0xFF21 && r <= 0xFF3A: // full-width capitals
		return 'A' + (r - 0xFF21), giftChar
	case r >= 0xFF41 && r <= 0xFF5A: // full-width small letters
		return 'A' + (r - 0xFF41), giftChar
	case r == ' ', r == '\t', r == '\n', r == '\r', r == '-', r == '_', r == '.',
		r == 0x00A0,                // no-break space
		r == 0x00AD,                // soft hyphen
		r == 0x061C,                // Arabic letter mark
		r >= 0x2010 && r <= 0x2015, // hyphens and dashes
		r == 0x2212,                // minus sign
		r >= 0x200B && r <= 0x200F, // zero-width space, joiners and direction marks
		r >= 0x202A && r <= 0x202E, // direction embeddings and overrides
		r == 0x2060,                // word joiner
		r >= 0x2066 && r <= 0x2069, // direction isolates
		r == 0xFEFF:                // zero-width no-break space
		return 0, giftSep
	}
	return 0, giftBad
}

// normalizeGiftCode is the one form a code is compared in: capitals and
// digits only, no separators. It reports false for what cannot be a code
// (a character that is not a letter or a digit, or a length no code has),
// without a guess at what was meant.
func normalizeGiftCode(raw string) (string, bool) {
	if len(raw) > giftMaxInput {
		return "", false
	}
	var b strings.Builder
	for _, r := range raw {
		c, kind := giftRuneOf(r)
		switch kind {
		case giftChar:
			b.WriteRune(c)
		case giftBad:
			return "", false
		}
	}
	s := b.String()
	if len(s) < giftMinCustomLen || len(s) > giftMaxLen {
		return "", false
	}
	return s, true
}

// normalizeGiftPrefix is the prefix of a batch of generated codes: up to
// giftMaxPrefixLen capitals and digits (empty: none).
func normalizeGiftPrefix(raw string) (string, bool) {
	if len(raw) > giftMaxInput {
		return "", false
	}
	var b strings.Builder
	for _, r := range raw {
		c, kind := giftRuneOf(r)
		switch kind {
		case giftChar:
			b.WriteRune(c)
		case giftBad:
			return "", false
		}
	}
	return b.String(), b.Len() <= giftMaxPrefixLen
}

// checkCustomGiftCode accepts a code staff chose themselves: it returns its
// normalised form and how it is shown (the staff member's own dashes kept,
// one dash for any run of separators), or says what is wrong with it.
func checkCustomGiftCode(raw string) (canon, display string, err error) {
	canon, ok := normalizeGiftCode(raw)
	if !ok {
		return "", "", &FieldError{"code", "letters and digits only (dashes and spaces are fine), 8 to 32 of them"}
	}
	seen := map[rune]bool{}
	for _, r := range canon {
		seen[r] = true
	}
	if len(seen) < giftMinDistinct {
		return "", "", &FieldError{"code", "too easy to guess: use at least 5 different characters"}
	}
	var b strings.Builder
	dash := false
	for _, r := range raw {
		c, kind := giftRuneOf(r)
		switch kind {
		case giftChar:
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(c)
		case giftSep:
			dash = true
		}
	}
	return canon, b.String(), nil
}

// errGiftRandom: the random source gave nothing usable (it is the operating
// system's, which does not fail; a test's reader can).
var errGiftRandom = errors.New("domain: no random bytes for a gift code")

// randomGiftBody draws the random part of a code from r (the operating
// system's random source): 12 symbols, each uniform over the alphabet. Bytes
// at or above 248 are thrown away, because 256 does not divide by 31 and
// taking them modulo 31 would make the first symbols likelier.
func randomGiftBody(r io.Reader) (string, error) {
	const limit = 256 - 256%len(giftAlphabet)
	out := make([]byte, 0, giftBodyLen)
	buf := make([]byte, 32)
	for reads := 0; len(out) < giftBodyLen; reads++ {
		if reads == 64 { // 64 x 32 bytes: a source that never gives a usable one
			return "", errGiftRandom
		}
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, giftAlphabet[int(b)%len(giftAlphabet)])
			if len(out) == giftBodyLen {
				break
			}
		}
	}
	return string(out), nil
}

// formatGiftCode shows a code in groups of four, after the batch's prefix:
// NOWRUZ-ABCD-EFGH-JKMN.
func formatGiftCode(prefix, body string) string {
	parts := make([]string, 0, 1+len(body)/giftGroupLen)
	if prefix != "" {
		parts = append(parts, prefix)
	}
	for len(body) > giftGroupLen {
		parts = append(parts, body[:giftGroupLen])
		body = body[giftGroupLen:]
	}
	if body != "" {
		parts = append(parts, body)
	}
	return strings.Join(parts, "-")
}
