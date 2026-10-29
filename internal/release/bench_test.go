package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"testing"
)

// benchKeyPair is a fixed key, so the benchmark signs the same bytes every run.
func benchKeyPair(b *testing.B, seed byte) keyPair {
	b.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	der, err := x509.MarshalPKIXPublicKey(priv.Public())
	if err != nil {
		b.Fatal(err)
	}
	return keyPair{pub: base64.StdEncoding.EncodeToString(der), priv: priv}
}

// BenchmarkVerify is `bobres update` and the bot's update check verifying a
// release's metadata: the key list against the embedded roots (an unrelated
// root first, as with two roots), then checksums.txt against the release key
// it names (docs/21-benchmark.md).
func BenchmarkVerify(b *testing.B) {
	r := &signedRelease{root: benchKeyPair(b, 1), rel: benchKeyPair(b, 2)}
	r.keys = []byte("bobres-keys v1\nseq 3\nexpires 2027-09-30T15:15Z\nmin_version 0.0.1\nkey rel-a " + r.rel.pub + "\n")
	r.sums = []byte("# bobres-release v1\n# version 1.4.0\n# channel stable\n# key_id rel-a\n" +
		"# issued 2026-10-10T10:00Z\n# expires 2026-11-24T10:00Z\nabc123  bobres_linux_amd64\n")
	r.sign()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.verify(); err != nil {
			b.Fatal(err)
		}
	}
}
