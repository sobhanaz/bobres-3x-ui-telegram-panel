// Command license is the vendor's license tool: it makes a signing key and
// signs license tokens (docs/05-license.md).
//
//	go run ./tools/license keygen -kid v1-2026 -out ~/bobres-license-v1-2026.key
//	go run ./tools/license sign -key ~/bobres-license-v1-2026.key -in payload.json
//
// keygen writes the private key to a new file OUTSIDE any git checkout (it
// refuses a path inside one) and prints the public key to save as
// internal/license/keys/<kid>.pub. sign reads the payload JSON (a
// license.Token without kid), fills in kid, v and issued_at, checks it and
// prints the token. The private key never belongs in the repository.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

const usage = `usage:
  license keygen -kid ID -out FILE   make a signing key (FILE outside any git checkout)
  license sign -key FILE [-in payload.json] [-now RFC3339]   sign a payload (stdin without -in)
`

func run(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "keygen":
		err = keygen(args[1:], out, errOut)
	case "sign":
		err = sign(args[1:], in, out, errOut)
	default:
		fmt.Fprint(errOut, usage)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) || errors.Is(err, errUsage) {
		return 2
	}
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

// keyFile is the private key file's content.
type keyFile struct {
	KeyID      string `json:"kid"`
	PrivateKey string `json:"private_key"` // standard base64 of the 64-byte Ed25519 key
}

func keygen(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(errOut)
	kid := fs.String("kid", "", "key id, e.g. v1-2026 (letters, digits, '.', '_', '-')")
	path := fs.String("out", "", "new file for the private key, outside any git checkout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !license.ValidKeyID(*kid) || *path == "" || fs.NArg() > 0 {
		fmt.Fprint(errOut, usage)
		return errUsage
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	if repo := gitCheckout(filepath.Dir(abs)); repo != "" {
		return fmt.Errorf("%s is inside the git checkout %s: keep the private key outside any repository", abs, repo)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(keyFile{KeyID: *kid, PrivateKey: base64.StdEncoding.EncodeToString(priv)}, "", "  ")
	if err != nil {
		return err
	}
	// O_EXCL: never overwrite a key (tokens signed with it would die).
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(errOut, "Private key written to %s (mode 0600). Keep it offline; never commit it.\n", abs)
	fmt.Fprintf(errOut, "Save the next line as internal/license/keys/%s.pub:\n", *kid)
	fmt.Fprint(out, license.FormatPublicKey(pub))
	return nil
}

// gitCheckout is the git work tree dir lies in ("" when none): a directory
// on the way up holding .git (a directory, or a file in a worktree).
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

func sign(args []string, in io.Reader, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(errOut)
	keyPath := fs.String("key", "", "the private key file from keygen")
	inPath := fs.String("in", "", "the payload JSON file (default: stdin)")
	nowFlag := fs.String("now", "", "issued_at when the payload has none (RFC3339; default: now)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || fs.NArg() > 0 {
		fmt.Fprint(errOut, usage)
		return errUsage
	}
	kid, priv, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	src := in
	if *inPath != "" {
		f, err := os.Open(*inPath)
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	raw, err := io.ReadAll(io.LimitReader(src, 1<<20))
	if err != nil {
		return err
	}
	var t license.Token
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return fmt.Errorf("payload: %w", err)
	}
	switch {
	case t.KeyID != "" && t.KeyID != kid:
		return fmt.Errorf("payload kid %q is not this key's (%q): leave kid out", t.KeyID, kid)
	case t.V != 0 && t.V != license.Version:
		return fmt.Errorf("payload v must be %d (or left out)", license.Version)
	case t.Domain == "":
		// Core accepts only tokens bound to the store's domain.
		return errors.New("payload domain is required (the store's BOBRES_DOMAIN)")
	}
	t.KeyID, t.V = kid, license.Version
	if t.IssuedAt.IsZero() {
		t.IssuedAt = time.Now().UTC().Truncate(time.Second)
		if *nowFlag != "" {
			if t.IssuedAt, err = time.Parse(time.RFC3339, *nowFlag); err != nil {
				return fmt.Errorf("-now: %w", err)
			}
		}
	}
	if err := t.Validate(); err != nil {
		return err
	}
	token, err := license.Sign(priv, t)
	if err != nil {
		return err
	}
	// Check the result the way core will (signature and contents).
	pub, _ := priv.Public().(ed25519.PublicKey)
	if _, err := license.Parse(license.KeyRing{kid: pub}, token); err != nil {
		return fmt.Errorf("the signed token does not verify: %w", err)
	}
	fmt.Fprintf(errOut, "Signed %s for %s (%s), expires %s.\n", t.LicenseID, t.Domain, t.Tier, t.ExpiresAt.UTC().Format(time.DateOnly))
	fmt.Fprintln(out, token)
	return nil
}

func readKey(path string) (string, ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var k keyFile
	if err := json.Unmarshal(raw, &k); err != nil {
		return "", nil, fmt.Errorf("key file: %w", err)
	}
	priv, err := base64.StdEncoding.DecodeString(k.PrivateKey)
	if err != nil || len(priv) != ed25519.PrivateKeySize || !license.ValidKeyID(k.KeyID) {
		return "", nil, errors.New("key file: not a key made by license keygen")
	}
	return k.KeyID, ed25519.PrivateKey(priv), nil
}
