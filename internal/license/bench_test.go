package license

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"
)

// Benchmarks for docs/21-benchmark.md: deterministic keys, a fixed clock.

var benchNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func benchKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	return priv.Public().(ed25519.PublicKey), priv //nolint:forcetypeassert // an Ed25519 key's public half
}

// BenchmarkVerify is a store checking its license token: the signature, the
// token's fields and its binding to the domain and install.
func BenchmarkVerify(b *testing.B) {
	pub, priv := benchKey()
	raw, err := Sign(priv, sample(benchNow))
	if err != nil {
		b.Fatal(err)
	}
	keys := ring(pub)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Verify(keys, raw, okOpts); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSign is the license server issuing a token.
func BenchmarkSign(b *testing.B) {
	_, priv := benchKey()
	tok := sample(benchNow)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Sign(priv, tok); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAdminVerify is the license server checking one signed admin API
// request (the bot's calls for the owner's store).
func BenchmarkAdminVerify(b *testing.B) {
	pub, priv := benchKey()
	body := []byte(`{"customer":"cust_1","tier":"standard","months":12}`)
	ts, nonce, sig, err := AdminSign(priv, "POST", "/admin/v1/licenses", body, benchNow)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := AdminVerify(pub, "POST", "/admin/v1/licenses", ts, nonce, sig, body, benchNow); err != nil {
			b.Fatal(err)
		}
	}
}
