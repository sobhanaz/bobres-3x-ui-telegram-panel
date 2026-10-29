package tonaddr

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// Addresses published in TON's documentation (public data, not keys): one account in the raw form and in
// both friendly forms.
const (
	docRaw        = "0:ed1691307050047117b998b561d8de82d31fbf84910ced6eb5fc92e7485ef8a7"
	docBounceable = "EQDtFpEwcFAEcRe5mLVh2N6C0x-_hJEM7W61_JLnSF74p4q2"
	foundationEQ  = "EQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqB2N"
	foundationRaw = "0:83dfd552e63729b472fcbcc8c45ebcc6691702558b68ec7527e1ba403a0f31a8"
)

func TestKnownAddresses(t *testing.T) {
	a, err := Parse(docBounceable)
	if err != nil {
		t.Fatal(err)
	}
	if a.Raw() != docRaw || !a.Bounceable || a.TestOnly {
		t.Fatalf("%+v %s", a, a.Raw())
	}
	if got := a.Friendly(true); got != docBounceable {
		t.Fatalf("bounceable form: %s", got)
	}
	// The non-bounceable form of the same account: UQ..., and it reads back as the same account.
	nb := a.Canonical()
	if !strings.HasPrefix(nb, "UQ") || len(nb) != 48 {
		t.Fatalf("canonical: %s", nb)
	}
	b, err := Parse(nb)
	if err != nil || b.Hash != a.Hash || b.Bounceable {
		t.Fatalf("%+v %v", b, err)
	}
	if !Same(docRaw, docBounceable) || !Same(nb, docBounceable) || !Same(docRaw, nb) {
		t.Fatal("the forms of one account are not the same account")
	}
	f, err := Parse(foundationEQ)
	if err != nil || f.Raw() != foundationRaw {
		t.Fatalf("%+v %v", f, err)
	}
	if got := f.Friendly(true); got != foundationEQ {
		t.Fatalf("foundation bounceable: %s", got)
	}
	if Same(docRaw, foundationRaw) {
		t.Fatal("two accounts are one")
	}
}

func TestRawAndAlphabets(t *testing.T) {
	a, err := Parse(docRaw)
	if err != nil || a.Friendly(true) != docBounceable {
		t.Fatalf("%+v %v", a, err)
	}
	// The standard alphabet: - becomes + and _ becomes /.
	std := strings.NewReplacer("-", "+", "_", "/").Replace(docBounceable)
	if b, err := Parse(std); err != nil || b.Hash != a.Hash {
		t.Fatalf("standard alphabet: %+v %v", b, err)
	}
	// Whatever a paste adds around it.
	for _, s := range []string{" " + docBounceable + "\n", "\u200e" + docBounceable + "\u200f", docBounceable[:10] + " " + docBounceable[10:]} {
		if b, err := Parse(s); err != nil || b.Hash != a.Hash {
			t.Errorf("%q: %+v %v", s, b, err)
		}
	}
	if n, err := Normalize(" " + docRaw + " "); err != nil || n != a.Canonical() {
		t.Fatalf("normalize: %s %v", n, err)
	}
}

func TestRefusals(t *testing.T) {
	// A friendly address written for another network or workchain, with a valid checksum.
	build := func(tag, wc byte) string {
		raw := make([]byte, 36)
		raw[0], raw[1] = tag, wc
		raw[2] = 0x42
		c := crc16(raw[:34])
		binary.BigEndian.PutUint16(raw[34:], c)
		return base64.URLEncoding.EncodeToString(raw)
	}
	cases := []struct {
		name, in string
		want     error
	}{
		{"empty", "", ErrFormat},
		{"words", "not an address", ErrFormat},
		{"too short", docBounceable[:47], ErrFormat},
		{"too long", docBounceable + "A", ErrFormat},
		{"not base64", strings.Repeat("!", 48), ErrFormat},
		{"a mistyped character", docBounceable[:20] + "Z" + docBounceable[21:], ErrChecksum},
		{"a swapped pair", docBounceable[:10] + string(docBounceable[11]) + string(docBounceable[10]) + docBounceable[12:], ErrChecksum},
		{"a TRON address", "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r", ErrFormat},
		{"an Ethereum address", "0x0000000000000000000000000000000000000001", ErrFormat},
		{"a test network address", build(0x51|0x80, 0), ErrTestnet},
		{"a test network non-bounceable", build(0x11|0x80, 0), ErrTestnet},
		{"the masterchain, friendly", build(0x11, 0xff), ErrWorkchain},
		{"the masterchain, raw", "-1:" + strings.Repeat("ab", 32), ErrWorkchain},
		{"another workchain, raw", "1:" + strings.Repeat("ab", 32), ErrWorkchain},
		{"an unknown tag", build(0x22, 0), ErrFormat},
		{"raw with a short hash", "0:" + strings.Repeat("ab", 31), ErrFormat},
		{"raw with a bad hash", "0:" + strings.Repeat("zz", 32), ErrFormat},
		{"raw with no workchain number", "x:" + strings.Repeat("ab", 32), ErrFormat},
	}
	for _, c := range cases {
		a, err := Parse(c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v (%+v)", c.name, err, c.want, a)
		}
		if err != nil && c.in != "" && strings.Contains(err.Error(), c.in) {
			t.Errorf("%s: the error repeats the input", c.name)
		}
	}
	// The good ones with the same builder parse.
	for _, tag := range []byte{0x11, 0x51} {
		if _, err := Parse(build(tag, 0)); err != nil {
			t.Errorf("tag %x: %v", tag, err)
		}
	}
}

func TestSame(t *testing.T) {
	if Same("", "") || Same("x", "x") || Same(docRaw, "") {
		t.Fatal("invalid addresses are never the same")
	}
}
