// Package crypto provides envelope encryption for secrets stored in the
// database (3x-ui API tokens, TOTP secrets). A per-instance data key is
// derived from the master key with HKDF-SHA256; each Encrypt call uses AES-GCM
// with a fresh random nonce, so identical plaintexts never repeat ciphertext.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// ErrDecrypt marks any decryption failure (tampering, wrong key, truncation).
// Callers must not distinguish between failure causes to avoid oracles.
var ErrDecrypt = errors.New("crypto: decryption failed")

const (
	masterMinLen = 32
	versionByte  = 1
	headerLen    = 1 // version
)

// Envelope encrypts and decrypts secrets with keys derived from master.
type Envelope struct {
	key []byte // derived AES key
}

// NewEnvelope derives a data key from master. master must be at least 32
// bytes and is never stored by this package.
func NewEnvelope(master []byte) (*Envelope, error) {
	if len(master) < masterMinLen {
		return nil, fmt.Errorf("crypto: master key must be at least %d bytes", masterMinLen)
	}
	r := hkdf.New(sha256.New, master, nil, []byte("bobres:envelope:v1"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("crypto: derive key: %w", err)
	}
	return &Envelope{key: key}, nil
}

// Encrypt returns version || nonce || AES-GCM(plaintext).
func (e *Envelope) Encrypt(plaintext []byte) ([]byte, error) {
	gcm, err := e.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := crand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	out := make([]byte, 0, headerLen+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, versionByte)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, []byte{versionByte}) // version is AAD
	return out, nil
}

// Decrypt reverses Encrypt. Any failure returns ErrDecrypt.
func (e *Envelope) Decrypt(ciphertext []byte) ([]byte, error) {
	gcm, err := e.gcm()
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < headerLen+gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrDecrypt
	}
	if ciphertext[0] != versionByte {
		return nil, ErrDecrypt
	}
	nonce := ciphertext[headerLen : headerLen+gcm.NonceSize()]
	pt, err := gcm.Open(nil, nonce, ciphertext[headerLen+gcm.NonceSize():], []byte{versionByte})
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

func (e *Envelope) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	return cipher.NewGCM(block)
}
