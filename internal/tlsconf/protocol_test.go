package tlsconf

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A request written by core is read back whole, and a result by the agent;
// the request file is 0600 (it may hold a key), and no temp file is left.
func TestExchangeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	reqDir, resDir := filepath.Join(dir, "request"), filepath.Join(dir, "result")
	for _, d := range []string{reqDir, resDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ex := NewExchange(reqDir, resDir)
	if _, err := ex.ReadRequest(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no request yet: %v", err)
	}
	if _, err := ex.ReadResult(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no result yet: %v", err)
	}
	req := Request{Generation: 7, Mode: ModeManual, Domain: "panel.example.com", ZarinpalHost: "pay.example.net",
		CertPEM: "the chain", KeyPEM: "the key"}
	if err := ex.WriteRequest(req); err != nil {
		t.Fatal(err)
	}
	got, err := ex.ReadRequest()
	if err != nil || *got != req {
		t.Fatalf("read back: %+v %v", got, err)
	}
	if fi, err := os.Stat(ex.RequestPath); err != nil || fi.Mode().Perm() != RequestMode {
		t.Fatalf("request mode: %v %v", fi.Mode(), err)
	}
	if st := got.State(); st.Mode != ModeManual || !st.HasCert || st.Domain != "panel.example.com" {
		t.Fatalf("state: %+v", st)
	}
	res := Result{Generation: 7, OK: false, Code: ResultRefused, Error: "adapting config", AppliedAt: 1760000000}
	if err := WriteResult(filepath.Join(resDir, ResultFile), res); err != nil {
		t.Fatal(err)
	}
	if r, err := ex.ReadResult(); err != nil || *r != res {
		t.Fatalf("result: %+v %v", r, err)
	}
	for _, d := range []string{reqDir, resDir} {
		entries, _ := os.ReadDir(d)
		if len(entries) != 1 {
			t.Fatalf("%s holds %d files", d, len(entries))
		}
	}
	if err := os.WriteFile(ex.RequestPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.ReadRequest(); err == nil {
		t.Fatal("a broken request was read")
	}
	if err := os.WriteFile(ex.RequestPath, bytes.Repeat([]byte(" "), maxRequest+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.ReadRequest(); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("an oversized request: %v", err)
	}
}

// The key never reaches a log or a %v.
func TestRequestNeverLogsTheKey(t *testing.T) {
	req := Request{Generation: 3, Mode: ModeManual, Domain: "panel.example.com", CertPEM: "CHAIN-TEXT", KeyPEM: "KEY-TEXT"}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("apply", "request", req)
	for _, s := range []string{buf.String(), fmt.Sprint(req), fmt.Sprintf("%+v", req), req.String()} {
		if strings.Contains(s, "KEY-TEXT") || strings.Contains(s, "CHAIN-TEXT") || !strings.Contains(s, "panel.example.com") {
			t.Fatalf("logged: %s", s)
		}
	}
}

// WriteFile replaces a file atomically with the mode asked (whatever the
// umask), over a leftover temp file, and never writes through a link
// planted at the temp name.
func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SitesFile)
	if err := os.WriteFile(filepath.Join(dir, "."+SitesFile+".tmp"), []byte("left over"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("one"), FileMode); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("two"), FileMode); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if err != nil || string(b) != "two" || fi.Mode().Perm() != FileMode {
		t.Fatalf("%q %v %v", b, fi.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "."+SitesFile+".tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file left: %v", err)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "."+GlobalFile+".tmp")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(dir, GlobalFile), []byte("new"), FileMode); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("written through the link: %q", b)
	}
	if err := WriteFile(filepath.Join(dir, "missing", "x"), []byte("x"), FileMode); err == nil {
		t.Fatal("wrote into a missing directory")
	}
}
