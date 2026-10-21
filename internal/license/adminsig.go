package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// The signed requests of the license server's admin API (internal/licensesrv, "/admin/v1/..."), shared by the
// server that checks them and by the client that makes them, so the two cannot drift apart.
//
// A request is signed with an Ed25519 key that is NOT the license signing key: its public half is the only thing
// the server holds. The signature covers the method, the request URI (path and query), a timestamp, a one-time
// nonce and the SHA-256 of the body, so none of them can be changed, and a recorded request cannot be replayed
// (the server remembers each nonce until its timestamp is too old to be accepted anyway).
const (
	// AdminSigPrefix starts the signed text; it names the scheme and its version.
	AdminSigPrefix = "BOBRES-ADMIN-V1"
	// The request headers.
	AdminHeaderTS    = "X-Bobres-Ts"
	AdminHeaderNonce = "X-Bobres-Nonce"
	AdminHeaderSig   = "X-Bobres-Sig"
	// AdminSkew is how far a request's timestamp may be from the server's clock, either way.
	AdminSkew = 2 * time.Minute
	// AdminNonceTTL is how long the server keeps a nonce: longer than the skew can ever make it valid.
	AdminNonceTTL = 10 * time.Minute
)

// ErrAdminSignature is every way a request can fail to authenticate; the server answers them all alike.
var ErrAdminSignature = errors.New("license: admin signature does not verify")

func adminSigned(method, uri string, ts int64, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.Join([]string{AdminSigPrefix, method, uri, strconv.FormatInt(ts, 10), nonce, hex.EncodeToString(sum[:])}, "\n"))
}

// AdminSign signs a request for method and uri (path and query, as in http.Request.RequestURI) with body, at now.
// It returns the values of the three headers.
func AdminSign(priv ed25519.PrivateKey, method, uri string, body []byte, now time.Time) (ts, nonce, sig string, err error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", "", "", errors.New("license: the admin key is not an Ed25519 private key")
	}
	n := make([]byte, 18)
	if _, err := rand.Read(n); err != nil {
		return "", "", "", err
	}
	nonce = base64.RawURLEncoding.EncodeToString(n)
	unix := now.Unix()
	return strconv.FormatInt(unix, 10), nonce,
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, adminSigned(method, uri, unix, nonce, body))), nil
}

// AdminVerify checks the three header values of a request against pub, and that the timestamp is within
// AdminSkew of now. The nonce's uniqueness is the caller's to enforce (it needs state).
func AdminVerify(pub ed25519.PublicKey, method, uri, ts, nonce, sig string, body []byte, now time.Time) error {
	if len(pub) != ed25519.PublicKeySize || len(nonce) < 16 || len(nonce) > 64 || !adminNonceOK(nonce) {
		return ErrAdminSignature
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrAdminSignature
	}
	if d := now.Sub(time.Unix(unix, 0)); d > AdminSkew || d < -AdminSkew {
		return ErrAdminSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return ErrAdminSignature
	}
	if !ed25519.Verify(pub, adminSigned(method, uri, unix, nonce, body), raw) {
		return ErrAdminSignature
	}
	return nil
}

func adminNonceOK(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// ParseAdminPublicKey reads an admin public key: 32 bytes, base64 (standard or URL-safe, padded or not).
func ParseAdminPublicKey(s string) (ed25519.PublicKey, error) {
	b, err := decodeAdminKey(s, ed25519.PublicKeySize)
	if err != nil {
		return nil, errors.New("license: the admin public key is not 32 bytes of base64")
	}
	return ed25519.PublicKey(b), nil
}

// ParseAdminPrivateKey reads an admin private key: the 32-byte seed in base64.
func ParseAdminPrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := decodeAdminKey(s, ed25519.SeedSize)
	if err != nil {
		return nil, errors.New("license: the admin private key is not 32 bytes of base64")
	}
	return ed25519.NewKeyFromSeed(b), nil
}

// NewAdminKey makes an admin key pair. The private key is the 32-byte seed in base64 (what ParseAdminPrivateKey
// reads), the public key 32 bytes in base64.
func NewAdminKey() (priv, pub string, err error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return "", "", err
	}
	k := ed25519.NewKeyFromSeed(seed)
	return base64.RawURLEncoding.EncodeToString(seed), base64.RawURLEncoding.EncodeToString(k.Public().(ed25519.PublicKey)), nil
}

func decodeAdminKey(s string, size int) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == size {
			return b, nil
		}
	}
	return nil, errors.New("bad key")
}
