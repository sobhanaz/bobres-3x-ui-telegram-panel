package license

import (
	"crypto/ed25519"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// keyFiles are the vendor public keys built into every binary
// (keys/<kid>.pub).
//
//go:embed keys
var keyFiles embed.FS

// kidRe is what a key id (and its file name) may be.
var kidRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidKeyID reports whether id can name a key file.
func ValidKeyID(id string) bool { return kidRe.MatchString(id) }

// Embedded is the key ring built into this binary. It is empty until the
// vendor adds a key, and then every token is refused as ErrUnknownKey.
func Embedded() (KeyRing, error) {
	sub, err := fs.Sub(keyFiles, "keys")
	if err != nil {
		return nil, err
	}
	return ReadKeyRing(sub)
}

// ReadKeyRing reads every <kid>.pub file at the top of fsys; other files are
// ignored. A file that is not one base64 Ed25519 public key is an error, so a
// broken key fails the build's tests instead of silently refusing tokens.
func ReadKeyRing(fsys fs.FS) (KeyRing, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	ring := KeyRing{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || path.Ext(name) != ".pub" {
			continue
		}
		kid := strings.TrimSuffix(name, ".pub")
		if !ValidKeyID(kid) {
			return nil, fmt.Errorf("license key %s: the file name is not a key id", name)
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		pub, err := ParsePublicKey(string(raw))
		if err != nil {
			return nil, fmt.Errorf("license key %s: %w", name, err)
		}
		ring[kid] = pub
	}
	return ring, nil
}

// ParsePublicKey reads a key file's text: one standard base64 line holding a
// 32-byte Ed25519 public key; blank lines and # comments are skipped.
func ParsePublicKey(text string) (ed25519.PublicKey, error) {
	var b64 string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if b64 != "" {
			return nil, errors.New("more than one key line")
		}
		b64 = line
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("not a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

// FormatPublicKey is the text of a key file for pub.
func FormatPublicKey(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub) + "\n"
}
