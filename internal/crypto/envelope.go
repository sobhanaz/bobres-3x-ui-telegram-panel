// Package crypto provides envelope encryption for secrets stored in the
// database (3x-ui API tokens, TOTP secrets). A per-instance data key is
// derived from the master key with HKDF-SHA256; each Encrypt call uses AES-GCM
// with a fresh random nonce, so identical plaintexts never repeat ciphertext.
//
// Two formats exist. Version 1 (written by releases before key rotation) is
// version || nonce || sealed. Version 2 names the key that sealed the value:
// version || key id || nonce || sealed, with the version and the key id as
// authenticated data. The key id is a public fingerprint of the master key (4
// bytes of an HKDF output, nothing that helps to find the key). It is what lets
// a rotation to a new master key be interrupted at any moment without leaving a
// value that nothing can read: while a rotation runs, an Envelope holds the new
// key and the previous one, reads a value with whichever sealed it, and seals
// every new one with the new key. Version 1 values carry no id and are tried
// with each key in turn.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	versionV1    = 1
	versionV2    = 2
	keyIDLen     = 4
	nonceLen     = 12
	tagKeyLen    = 32
	// v2Header is version || key id.
	v2Header = 1 + keyIDLen
)

// Keys are the keys an Envelope works with.
type Keys struct {
	// Master is the current master key: every Encrypt uses it.
	Master string
	// Previous are older master keys that Decrypt still accepts: a rotation to
	// Master is under way, or has just finished and the old key is kept for a
	// while. A key equal to Master is ignored.
	Previous []string
	// Tag is the key of Tag(), 64 hex characters. Empty derives it from Master
	// (what every release before key rotation did). A rotation sets it to the
	// value the old master key derived, so the identifiers Tag made (gift code
	// hashes, 3x-ui subscription ids) stay the same under the new master key.
	Tag string
}

// keyset is one master key's derived material.
type keyset struct {
	id   [keyIDLen]byte
	aead cipher.AEAD
}

// Envelope encrypts and decrypts secrets with keys derived from master keys.
type Envelope struct {
	cur    keyset
	prev   []keyset
	macKey []byte // derived (or given) HMAC key, independent of the AES keys
}

// NewEnvelope derives a data key from master. master must be at least 32
// bytes and is never stored by this package.
func NewEnvelope(master []byte) (*Envelope, error) {
	return NewKeyring(Keys{Master: string(master)})
}

// NewKeyring is NewEnvelope for a set of keys: the current master key, the
// previous ones, and the key of Tag.
func NewKeyring(k Keys) (*Envelope, error) {
	cur, err := newKeyset([]byte(k.Master))
	if err != nil {
		return nil, err
	}
	e := &Envelope{cur: cur}
	for _, p := range k.Previous {
		if p == "" || p == k.Master {
			continue
		}
		ks, err := newKeyset([]byte(p))
		if err != nil {
			return nil, fmt.Errorf("previous key: %w", err)
		}
		if ks.id == cur.id {
			continue
		}
		e.prev = append(e.prev, ks)
	}
	if k.Tag != "" {
		mk, err := hex.DecodeString(k.Tag)
		if err != nil || len(mk) != tagKeyLen {
			return nil, fmt.Errorf("crypto: the tag key must be %d hex characters", 2*tagKeyLen)
		}
		e.macKey = mk
		return e, nil
	}
	if e.macKey, err = derive([]byte(k.Master), "bobres:mac:v1"); err != nil {
		return nil, err
	}
	return e, nil
}

func newKeyset(master []byte) (keyset, error) {
	if len(master) < masterMinLen {
		return keyset{}, fmt.Errorf("crypto: master key must be at least %d bytes", masterMinLen)
	}
	key, err := derive(master, "bobres:envelope:v1")
	if err != nil {
		return keyset{}, err
	}
	id, err := derive(master, "bobres:keyid:v1")
	if err != nil {
		return keyset{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return keyset{}, fmt.Errorf("crypto: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return keyset{}, fmt.Errorf("crypto: %w", err)
	}
	ks := keyset{aead: aead}
	copy(ks.id[:], id)
	return ks, nil
}

func derive(master []byte, info string) ([]byte, error) {
	k := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, master, nil, []byte(info)), k); err != nil {
		return nil, fmt.Errorf("crypto: derive key: %w", err)
	}
	return k, nil
}

// TagKeyHex is the key Tag uses under master, as hex: the value to keep as
// Keys.Tag when master is replaced.
func TagKeyHex(master []byte) (string, error) {
	if len(master) < masterMinLen {
		return "", fmt.Errorf("crypto: master key must be at least %d bytes", masterMinLen)
	}
	k, err := derive(master, "bobres:mac:v1")
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(k), nil
}

// Tag returns HMAC-SHA256(data) under a key derived from the master key. It
// gives stable identifiers that nobody without the master key can predict
// (e.g. 3x-ui subscription ids derived from our subscription ids).
func (e *Envelope) Tag(data []byte) []byte {
	m := hmac.New(sha256.New, e.macKey)
	m.Write(data)
	return m.Sum(nil)
}

// KeyID is the public fingerprint of the current master key, as hex: the
// number a version 2 value carries. It tells which key is meant, not what it is.
func (e *Envelope) KeyID() string { return hex.EncodeToString(e.cur.id[:]) }

// HasPrevious reports whether older keys are still accepted.
func (e *Envelope) HasPrevious() bool { return len(e.prev) > 0 }

// CurrentOnly is an Envelope with the current key alone: what a store reads
// with once the previous keys are gone. A rotation proves each value readable
// with it before the old ciphertext is replaced.
func (e *Envelope) CurrentOnly() *Envelope {
	return &Envelope{cur: e.cur, macKey: e.macKey}
}

// Encrypt returns version || key id || nonce || AES-GCM(plaintext), sealed with
// the current key.
func (e *Envelope) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceLen)
	if _, err := crand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	out := make([]byte, 0, v2Header+nonceLen+len(plaintext)+e.cur.aead.Overhead())
	out = append(out, versionV2)
	out = append(out, e.cur.id[:]...)
	out = append(out, nonce...)
	// The version and the key id are authenticated data: a value cannot be
	// passed off as sealed by another key, or as another version.
	aad := append([]byte(nil), out[:v2Header]...)
	out = e.cur.aead.Seal(out, nonce, plaintext, aad)
	return out, nil
}

// Decrypt reverses Encrypt, with the current key or a previous one. Any
// failure returns ErrDecrypt.
func (e *Envelope) Decrypt(ciphertext []byte) ([]byte, error) {
	pt, _, ok := e.open(ciphertext)
	if !ok {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// open decrypts with the key that fits, and says whether that was the current one.
func (e *Envelope) open(ct []byte) (pt []byte, current, ok bool) {
	if len(ct) == 0 {
		return nil, false, false
	}
	overhead := e.cur.aead.Overhead()
	switch ct[0] {
	case versionV1:
		if len(ct) < 1+nonceLen+overhead {
			return nil, false, false
		}
		nonce, sealed := ct[1:1+nonceLen], ct[1+nonceLen:]
		if pt, err := e.cur.aead.Open(nil, nonce, sealed, []byte{versionV1}); err == nil {
			return pt, true, true
		}
		for _, k := range e.prev {
			if pt, err := k.aead.Open(nil, nonce, sealed, []byte{versionV1}); err == nil {
				return pt, false, true
			}
		}
	case versionV2:
		if len(ct) < v2Header+nonceLen+overhead {
			return nil, false, false
		}
		nonce, sealed := ct[v2Header:v2Header+nonceLen], ct[v2Header+nonceLen:]
		var id [keyIDLen]byte
		copy(id[:], ct[1:v2Header])
		if id == e.cur.id {
			if pt, err := e.cur.aead.Open(nil, nonce, sealed, ct[:v2Header]); err == nil {
				return pt, true, true
			}
			return nil, false, false
		}
		for _, k := range e.prev {
			if k.id != id {
				continue
			}
			if pt, err := k.aead.Open(nil, nonce, sealed, ct[:v2Header]); err == nil {
				return pt, false, true
			}
		}
	}
	return nil, false, false
}

// Status says how a stored value stands against this Envelope's keys.
type Status int

const (
	// Current: the current key reads it. It is where a rotation wants it.
	Current Status = iota
	// Previous: only a previous key reads it; a rotation has to seal it again.
	Previous
	// Unreadable: no key reads it (the wrong key, a damaged value).
	Unreadable
)

// Check reports which key reads ciphertext. It never returns the plaintext.
func (e *Envelope) Check(ciphertext []byte) Status {
	pt, current, ok := e.open(ciphertext)
	for i := range pt {
		pt[i] = 0
	}
	switch {
	case !ok:
		return Unreadable
	case current:
		return Current
	}
	return Previous
}

// Reseal reads ciphertext with whichever key fits and seals it with the current
// one. Before it answers, the new value is read back with the current key alone
// and compared with the plaintext, so a value that cannot be read after the
// rotation is never handed out. A value the current key already reads comes back
// unchanged.
func (e *Envelope) Reseal(ciphertext []byte) ([]byte, error) {
	pt, current, ok := e.open(ciphertext)
	if !ok {
		return nil, ErrDecrypt
	}
	defer func() {
		for i := range pt {
			pt[i] = 0
		}
	}()
	if current {
		return ciphertext, nil
	}
	sealed, err := e.Encrypt(pt)
	if err != nil {
		return nil, err
	}
	back, err := e.CurrentOnly().Decrypt(sealed)
	if err != nil || !hmac.Equal(back, pt) {
		return nil, errors.New("crypto: the value did not read back under the current key")
	}
	for i := range back {
		back[i] = 0
	}
	return sealed, nil
}
