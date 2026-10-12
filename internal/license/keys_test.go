package license

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// This build embeds no vendor key yet: the ring is empty and every token is
// refused as an unknown key.
func TestEmbeddedRingIsEmptyForNow(t *testing.T) {
	ring, err := Embedded()
	if err != nil {
		t.Fatalf("embedded keys: %v", err)
	}
	if len(ring) != 0 {
		t.Fatalf("a key was added (%d): update this test and docs/05-license.md", len(ring))
	}
	_, priv := keypair(t)
	raw := sign(t, priv, sample(time.Now().UTC()))
	if _, err := Verify(ring, raw, okOpts); !errors.Is(err, ErrUnknownKey) || Reason(err) != "unknown_key" {
		t.Fatalf("got %v (%s)", err, Reason(err))
	}
}

func TestReadKeyRing(t *testing.T) {
	pub, priv := keypair(t)
	fsys := fstest.MapFS{
		kid + ".pub": {Data: []byte("# vendor key 2026\n" + FormatPublicKey(pub))},
		"README.md":  {Data: []byte("not a key")},
		"old.txt":    {Data: []byte("ignored")},
	}
	ring, err := ReadKeyRing(fsys)
	if err != nil || len(ring) != 1 {
		t.Fatalf("ring: %v %v", ring, err)
	}
	if _, err := Verify(ring, sign(t, priv, sample(time.Now().UTC())), okOpts); err != nil {
		t.Fatalf("a token signed by the file's key: %v", err)
	}
	for name, files := range map[string]fstest.MapFS{
		"not base64":  {"k1.pub": {Data: []byte("!!!")}},
		"short key":   {"k1.pub": {Data: []byte("c2hvcnQ=")}},
		"two keys":    {"k1.pub": {Data: []byte(FormatPublicKey(pub) + FormatPublicKey(pub))}},
		"bad kid":     {"bad kid.pub": {Data: []byte(FormatPublicKey(pub))}},
		"only a note": {"k1.pub": {Data: []byte("# nothing\n")}},
	} {
		if _, err := ReadKeyRing(files); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Parse shows a correctly signed token bound to another install; Verify
// refuses it.
func TestParseSkipsBindingOnly(t *testing.T) {
	pub, priv := keypair(t)
	raw := sign(t, priv, sample(time.Now().UTC()))
	tok, err := Parse(ring(pub), raw)
	if err != nil || tok.LicenseID != "lic_1" {
		t.Fatalf("parse: %+v %v", tok, err)
	}
	other := Options{Domain: "other.example.org", InstallID: "inst_1"}
	if err := tok.CheckBinding(other); !errors.Is(err, ErrDomain) {
		t.Fatalf("binding: %v", err)
	}
	if _, err := Verify(ring(pub), raw, other); !errors.Is(err, ErrDomain) {
		t.Fatalf("verify: %v", err)
	}
	if _, err := Parse(ring(pub), raw[:len(raw)-4]+"AAAA"); !errors.Is(err, ErrBadSignature) && !errors.Is(err, ErrMalformed) {
		t.Fatalf("a changed signature parsed: %v", err)
	}
}

func TestReasonCodes(t *testing.T) {
	for err, want := range map[error]string{
		ErrUnknownKey: "unknown_key", ErrMalformed: "malformed", ErrBadSignature: "bad_signature",
		ErrDomain: "domain", ErrInstallID: "install_id", ErrUnbound: "unbound", ErrNotYetValid: "not_yet_valid",
		ErrInvalid: "invalid", errors.New("other"): "invalid",
	} {
		if got := Reason(err); got != want {
			t.Errorf("%v: %s", err, got)
		}
	}
	if got := Reason(errors.Join(errors.New("x"), ErrVersion)); got != "version" {
		t.Errorf("wrapped version: %s", got)
	}
	if !ValidKeyID("v1-2026") || ValidKeyID("") || ValidKeyID("../x") || ValidKeyID(strings.Repeat("a", 65)) {
		t.Error("key ids")
	}
}
