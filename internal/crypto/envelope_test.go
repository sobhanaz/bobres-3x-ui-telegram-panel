package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	env, err := NewEnvelope(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	secret := []byte("3x-ui-api-token-abc")
	ct, err := env.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ct, secret) {
		t.Fatal("ciphertext contains plaintext secret")
	}
	pt, err := env.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(pt, secret) {
		t.Fatalf("round trip mismatch: got %q", pt)
	}
}

func TestEncryptUniqueCiphertext(t *testing.T) {
	env, _ := NewEnvelope(bytes.Repeat([]byte{2}, 32))
	a, err := env.Encrypt([]byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := env.Encrypt([]byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("identical plaintexts produced identical ciphertexts; nonce reuse")
	}
}

func TestDecryptTamperDetected(t *testing.T) {
	env, _ := NewEnvelope(bytes.Repeat([]byte{3}, 32))
	ct, err := env.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	ct[len(ct)/2] ^= 0xff
	if _, err := env.Decrypt(ct); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("want ErrDecrypt, got %v", err)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	a, _ := NewEnvelope(bytes.Repeat([]byte{4}, 32))
	b, _ := NewEnvelope(bytes.Repeat([]byte{5}, 32))
	ct, err := a.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Decrypt(ct); err == nil {
		t.Fatal("decryption with wrong master key succeeded")
	}
}

func TestNewEnvelopeRejectsShortMaster(t *testing.T) {
	if _, err := NewEnvelope([]byte("short")); err == nil {
		t.Fatal("short master key accepted")
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	env, _ := NewEnvelope(bytes.Repeat([]byte{6}, 32))
	if _, err := env.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Fatal("garbage ciphertext accepted")
	}
}

func TestTagIsStableAndKeyed(t *testing.T) {
	a, _ := NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	b, _ := NewEnvelope([]byte("fedcba9876543210fedcba9876543210"))
	x1, x2 := a.Tag([]byte("sub-1")), a.Tag([]byte("sub-1"))
	if string(x1) != string(x2) {
		t.Fatal("tag not stable")
	}
	if string(x1) == string(a.Tag([]byte("sub-2"))) || string(x1) == string(b.Tag([]byte("sub-1"))) {
		t.Fatal("tag does not depend on data and key")
	}
}
