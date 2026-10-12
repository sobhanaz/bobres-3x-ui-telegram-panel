// Package release checks a BOBRES release's signed metadata the way
// install/install.sh verify_release does: an embedded ROOT key signs keys.txt
// (the trusted release keys, with expiry, sequence number and revocations),
// and a release key from that list signs checksums.txt, whose header carries
// the version, channel, key id and expiry. All keys are Ed25519 and the
// signatures raw (openssl pkeyutl -rawin).
//
// Unlike the installer it keeps no state (no last key list seq, no installed
// version): the bot only uses it to tell the owner whether a newer release
// exists; `bobres update` verifies again before anything is installed.
package release

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Roots are the vendor ROOT public keys (base64 SubjectPublicKeyInfo DER),
// the same two embedded in install/install.sh. Any one of them is enough to
// accept keys.txt.
var Roots = []string{
	"MCowBQYDK2VwAyEAKZqHG4ociEUeAtlxJX89TxKY7c3EEWS8JQHUKXBUKZw=",
	"MCowBQYDK2VwAyEADz7vJ+DMVvKQyotUaJSpnIOlvdbig/KAUzO49wfq3ng=",
}

// timeLayout is the installer's and tools/sign's date format (UTC, minutes).
const timeLayout = "2006-01-02T15:04Z"

// Header is checksums.txt's signed header.
type Header struct {
	Version, Channel, KeyID string
	Expires                 string
}

// ParseHeader reads checksums.txt's "# version X.Y.Z" header lines (the
// first occurrence of each, as the installer's awk does). It checks nothing.
func ParseHeader(sums []byte) Header {
	var h Header
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[0] != "#" {
			continue
		}
		set := func(p *string) {
			if *p == "" {
				*p = f[2]
			}
		}
		switch f[1] {
		case "version":
			set(&h.Version)
		case "channel":
			set(&h.Channel)
		case "key_id":
			set(&h.KeyID)
		case "expires":
			set(&h.Expires)
		}
	}
	return h
}

// Release is verified release metadata.
type Release struct {
	Header
	KeysSeq int
}

// Verify checks keys.txt against the roots (keysSigs are the keys.txt.sig.N
// files found) and checksums.txt against the release key it names, at now.
// Any failure is an error saying why; nothing is trusted then.
func Verify(roots []string, keys []byte, keysSigs [][]byte, sums, sumsSig []byte, now time.Time) (*Release, error) {
	// 1. keys.txt must carry a valid signature from ANY root.
	ok := false
	for _, sig := range keysSigs {
		for _, root := range roots {
			pub, err := parseKey(root)
			if err == nil && ed25519.Verify(pub, keys, sig) {
				ok = true
				break
			}
		}
		if ok {
			break
		}
	}
	if !ok {
		return nil, errors.New("key list signature is invalid (no embedded root verified it)")
	}

	// 2. Key list format and freshness.
	lines := strings.Split(string(keys), "\n")
	if strings.TrimRight(lines[0], "\r") != "bobres-keys v1" {
		return nil, errors.New("unknown key list format")
	}
	field := func(name string) string {
		for _, l := range lines {
			if f := strings.Fields(l); len(f) >= 2 && f[0] == name {
				return f[1]
			}
		}
		return ""
	}
	seq, err := strconv.Atoi(field("seq"))
	if err != nil || seq < 0 || strings.ContainsAny(field("seq"), "+-") {
		return nil, errors.New("bad key list seq")
	}
	kexp, minVer := field("expires"), field("min_version")
	if kexp == "" || minVer == "" {
		return nil, errors.New("key list is missing expires/min_version")
	}
	if expired, err := past(kexp, now); err != nil || expired {
		return nil, fmt.Errorf("key list expired at %s (check the system clock)", kexp)
	}

	// 3. Release header (inside the signed bytes), then its signature.
	h := ParseHeader(sums)
	if h.Version == "" || h.KeyID == "" || h.Expires == "" {
		return nil, errors.New("release header is missing version/key_id/expires")
	}
	var b64 string
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "revoked" && f[1] == h.KeyID {
			return nil, fmt.Errorf("release key %s is revoked", h.KeyID)
		}
		if len(f) >= 3 && f[0] == "key" && f[1] == h.KeyID && b64 == "" {
			b64 = f[2]
		}
	}
	if b64 == "" {
		return nil, fmt.Errorf("release key %s is not in the signed key list", h.KeyID)
	}
	pub, err := parseKey(b64)
	if err != nil {
		return nil, fmt.Errorf("release key %s: %w", h.KeyID, err)
	}
	if !ed25519.Verify(pub, sums, sumsSig) {
		return nil, errors.New("release signature is invalid (the download may have been tampered with)")
	}
	if expired, err := past(h.Expires, now); err != nil || expired {
		return nil, fmt.Errorf("release metadata expired at %s (stale or replayed release)", h.Expires)
	}
	if Compare(h.Version, minVer) < 0 {
		return nil, fmt.Errorf("release %s is below the minimum allowed %s", h.Version, minVer)
	}
	return &Release{Header: h, KeysSeq: seq}, nil
}

// past reports whether the installer-format time t is not after now.
func past(t string, now time.Time) (bool, error) {
	at, err := time.Parse(timeLayout, t)
	if err != nil {
		return true, err
	}
	return !now.Before(at), nil
}

// parseKey decodes a base64 SubjectPublicKeyInfo holding an Ed25519 key.
func parseKey(b64 string) (ed25519.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("bad key encoding: %w", err)
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("bad key: %w", err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an Ed25519 key")
	}
	return pub, nil
}

// semver is X.Y.Z with an optional -prerelease (a leading v and +build
// metadata ignored).
type semver struct {
	n   [3]int
	pre string
}

func parseSemver(s string) (semver, bool) {
	var v semver
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	s, v.pre, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || strings.ContainsAny(p, "+-") {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}

// Valid reports whether v is a version Compare understands (so not "dev").
func Valid(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

// Compare compares two versions: -1, 0 or 1. A pre-release sorts before its
// release (pre-releases between themselves by their text). A version that does
// not parse sorts before any that does.
func Compare(a, b string) int {
	va, okA := parseSemver(a)
	vb, okB := parseSemver(b)
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := range va.n {
		if va.n[i] != vb.n[i] {
			if va.n[i] < vb.n[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case va.pre == vb.pre:
		return 0
	case va.pre == "":
		return 1
	case vb.pre == "":
		return -1
	}
	return strings.Compare(va.pre, vb.pre)
}
