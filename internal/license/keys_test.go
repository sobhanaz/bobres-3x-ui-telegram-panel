package license

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// vendorKeys pins the vendor keys that this build trusts: key id and fingerprint.
// Adding or removing a key file without changing this list fails the test, so a
// key cannot arrive or leave unnoticed. To rotate (docs/runbooks/vendor-license-server.md),
// add the new key here with its fingerprint (`go run ./tools/license fingerprint
// internal/license/keys/<kid>.pub`) and, once its tokens have expired, remove the old one.
var vendorKeys = map[string]string{
	"vendor-v1-2026": "2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7",
}

// The build carries exactly the vendor's public keys, and a token signed by any
// other key is refused.
func TestEmbeddedRingHoldsTheVendorKeys(t *testing.T) {
	ring, err := Embedded()
	if err != nil {
		t.Fatalf("embedded keys: %v", err)
	}
	if len(ring) != len(vendorKeys) {
		t.Fatalf("the build embeds %d keys, the test pins %d: update vendorKeys", len(ring), len(vendorKeys))
	}
	for kid, want := range vendorKeys {
		pub, ok := ring[kid]
		if !ok {
			t.Fatalf("the key %s is not embedded", kid)
		}
		if got := Fingerprint(pub); got != want {
			t.Fatalf("fingerprint of %s is %q, want %q: was a vendor key changed?", kid, got, want)
		}
	}
	// A token from a key the build does not carry is a bad signature (the ring
	// is not empty), never accepted, even one that names a vendor kid.
	_, priv := keypair(t)
	for kid := range vendorKeys {
		tok := sample(time.Now().UTC())
		tok.KeyID = kid
		if _, err := Verify(ring, sign(t, priv, tok), okOpts); !errors.Is(err, ErrBadSignature) || Reason(err) != "bad_signature" {
			t.Fatalf("a forged token naming %s: %v (%s)", kid, err, Reason(err))
		}
	}
	if _, err := Verify(ring, sign(t, priv, sample(time.Now().UTC())), okOpts); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a token of an unknown key: %v", err)
	}
}

// With no key at all (a ring that is empty), nothing verifies and the reason
// is unknown_key.
func TestEmptyRingRefusesEverything(t *testing.T) {
	_, priv := keypair(t)
	raw := sign(t, priv, sample(time.Now().UTC()))
	if _, err := Verify(KeyRing{}, raw, okOpts); !errors.Is(err, ErrUnknownKey) || Reason(err) != "unknown_key" {
		t.Fatalf("got %v (%s)", err, Reason(err))
	}
}

// Fingerprint is the DER-based line openssl gives (verified once by hand with
// `openssl pkey -pubin -outform DER | openssl dgst -sha256`), in groups of four.
func TestFingerprintFormat(t *testing.T) {
	// An all-zero key has a fixed fingerprint: SHA-256 of the 12-byte SPKI header and 32 zero bytes.
	got := Fingerprint(make(ed25519.PublicKey, ed25519.PublicKeySize))
	groups := strings.Split(got, " ")
	if len(groups) != 8 || got != strings.ToUpper(got) {
		t.Fatalf("not eight upper-case groups of four: %q", got)
	}
	for _, g := range groups {
		if len(g) != 4 {
			t.Fatalf("group %q in %q", g, got)
		}
	}
	ring, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(ring["vendor-v1-2026"]) != vendorKeys["vendor-v1-2026"] || Fingerprint(ring["vendor-v1-2026"]) == got {
		t.Fatal("the vendor key's fingerprint is not the pinned one")
	}
}

// Rotation: a token signed by either of two keys in the ring verifies, and
// each names its own kid.
func TestRotationKeepsOldTokensWorking(t *testing.T) {
	oldPub, oldPriv := keypair(t)
	newPub, newPriv := keypair(t)
	ring := KeyRing{"v1-2026": oldPub, "v2-2027": newPub}
	now := time.Now().UTC()
	oldTok, newTok := sample(now), sample(now)
	oldTok.KeyID, newTok.KeyID = "v1-2026", "v2-2027"
	for name, raw := range map[string]string{"old": sign(t, oldPriv, oldTok), "new": sign(t, newPriv, newTok)} {
		if _, err := Verify(ring, raw, okOpts); err != nil {
			t.Errorf("%s key: %v", name, err)
		}
	}
	// Dropping the old key (after its tokens expired) refuses its tokens.
	delete(ring, "v1-2026")
	if _, err := Verify(ring, sign(t, oldPriv, oldTok), okOpts); !errors.Is(err, ErrBadSignature) {
		t.Errorf("a token of a dropped key: %v", err)
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
