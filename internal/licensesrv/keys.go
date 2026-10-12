package licensesrv

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// Activation keys are "BOBRES-" and eight groups of four base32 letters:
// 160 random bits. A guess is hopeless, so a fast unsalted SHA-256 is the
// right way to store them (a slow, salted hash would only stop the server
// from finding a key by its hash).
var keyRe = regexp.MustCompile(`^BOBRES(-[A-Z2-7]{4}){8}$`)

const keyBytes = 20 // 160 bits = 32 base32 letters, no padding

// NewKey makes a secret activation key.
func NewKey() (string, error) {
	b := make([]byte, keyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := base32.StdEncoding.EncodeToString(b)
	var sb strings.Builder
	sb.WriteString("BOBRES")
	for i := 0; i < len(s); i += 4 {
		sb.WriteByte('-')
		sb.WriteString(s[i : i+4])
	}
	return sb.String(), nil
}

// NormalizeKey returns a key in its canonical form (upper case, no spaces),
// or false when it is not a BOBRES activation key at all.
func NormalizeKey(raw string) (string, bool) {
	k := strings.ToUpper(strings.Join(strings.Fields(raw), ""))
	return k, keyRe.MatchString(k)
}

// HashKey is what the database keeps of a canonical key: hex SHA-256.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// newLicenseID makes a license id: "lic_" and 16 base32 letters (80 bits).
// It is not secret (installs show it), only unique.
func newLicenseID() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "lic_" + strings.ToLower(base32.StdEncoding.EncodeToString(b)), nil
}

// Signer signs tokens with the server's online key.
type Signer struct {
	KeyID string
	priv  ed25519.PrivateKey
}

// NewSigner wraps a private key (tests make one with ed25519.GenerateKey).
func NewSigner(kid string, priv ed25519.PrivateKey) (*Signer, error) {
	if !license.ValidKeyID(kid) {
		return nil, fmt.Errorf("signing key: %q is not a key id", kid)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("signing key: not an Ed25519 private key")
	}
	return &Signer{KeyID: kid, priv: priv}, nil
}

// Public is the key installs need as internal/license/keys/<kid>.pub.
func (s *Signer) Public() ed25519.PublicKey {
	pub, _ := s.priv.Public().(ed25519.PublicKey)
	return pub
}

// Sign signs t with this key; t.KeyID is set here.
func (s *Signer) Sign(t license.Token) (string, error) {
	t.KeyID = s.KeyID
	return license.Sign(s.priv, t)
}

// keyFile is the file tools/license keygen writes.
type keyFile struct {
	KeyID      string `json:"kid"`
	PrivateKey string `json:"private_key"` // standard base64 of the 64-byte key
}

// LoadSigner reads a key file made by `go run ./tools/license keygen`. It
// refuses a file inside a git checkout: the online key never lives in a
// repository. warn is told when the file can be read by others.
func LoadSigner(path string, warn func(msg string)) (*Signer, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if repo := gitCheckout(filepath.Dir(abs)); repo != "" {
		return nil, fmt.Errorf("signing key %s is inside the git checkout %s: keep it outside any repository", abs, repo)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	if st.Mode().Perm()&0o077 != 0 && warn != nil {
		warn(fmt.Sprintf("signing key %s can be read by other users (mode %04o); use chmod 600", abs, st.Mode().Perm()))
	}
	raw, err := os.ReadFile(abs) //nolint:gosec // the operator names the key file
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	var k keyFile
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, errors.New("signing key: not a key file made by tools/license keygen")
	}
	priv, err := base64.StdEncoding.DecodeString(k.PrivateKey)
	if err != nil {
		return nil, errors.New("signing key: not a key file made by tools/license keygen")
	}
	return NewSigner(k.KeyID, ed25519.PrivateKey(priv))
}

// gitCheckout is the git work tree dir lies in ("" when none), as
// tools/license keygen checks it.
func gitCheckout(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// VerifyRing is the key ring refresh tokens are checked against: every key
// built into this binary (so tokens signed before a rotation still refresh)
// with the signer's own key on top. It fails when the binary embeds a
// different key under the signer's kid: installs would refuse every token.
func VerifyRing(s *Signer, embedded license.KeyRing) (license.KeyRing, error) {
	ring := license.KeyRing{}
	for kid, pub := range embedded {
		ring[kid] = pub
	}
	if pub, ok := ring[s.KeyID]; ok && !pub.Equal(s.Public()) {
		return nil, fmt.Errorf("internal/license/keys/%s.pub is not the public key of this signing key: installs would refuse its tokens", s.KeyID)
	}
	ring[s.KeyID] = s.Public()
	return ring, nil
}
