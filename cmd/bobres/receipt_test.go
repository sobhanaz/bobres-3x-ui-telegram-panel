package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReceiptMissingFileIsEmptyNotAnError(t *testing.T) {
	s := newReceiptStore(t.TempDir())
	r, err := s.load()
	if err != nil || r == nil || r.Schema != 0 || len(r.Packages) != 0 {
		t.Fatalf("a missing receipt: %+v %v", r, err)
	}
	// A nil store (the tests that do not care) does nothing and fails nothing.
	var none *receiptStore
	if err := none.update(func(*receipt) { t.Fatal("a nil store ran its update") }); err != nil {
		t.Fatal(err)
	}
	if r, err := none.load(); err != nil || r == nil {
		t.Fatalf("nil store load: %v %v", r, err)
	}
	if newReceiptStore("") != nil {
		t.Fatal("a store without a directory should be nil")
	}
}

// A receipt written by a newer bobres keeps the keys this one does not know, and what this one
// writes is read back whole.
func TestReceiptKeepsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	s := newReceiptStore(dir)
	newer := `{"schema":7,"created":"2026-10-20T10:00:00Z","install_dir":"/opt/bobres","future_thing":{"a":[1,2,3]},"units":["bobres-monitor.timer"]}`
	if err := os.WriteFile(s.path(), []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.update(func(r *receipt) { r.Domain = "panel.example.com" }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.path())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	compact := func(b json.RawMessage) string {
		var buf bytes.Buffer
		if err := json.Compact(&buf, b); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	if compact(m["future_thing"]) != `{"a":[1,2,3]}` || compact(m["units"]) != `["bobres-monitor.timer"]` {
		t.Fatalf("unknown keys lost:\n%s", raw)
	}
	r, err := s.load()
	if err != nil || r.Domain != "panel.example.com" || r.InstallDir != "/opt/bobres" || r.Schema != 7 {
		t.Fatalf("known keys: %+v %v", r, err)
	}
	if fi, _ := os.Stat(s.path()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode %v", fi.Mode())
	}
}

// An unreadable receipt is treated as absent: the install goes on, the next write replaces it.
func TestReceiptCorruptIsReplaced(t *testing.T) {
	s := newReceiptStore(t.TempDir())
	if err := os.WriteFile(s.path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := s.load(); err == nil || r == nil {
		t.Fatalf("a corrupt receipt: %v %v", r, err)
	}
	if err := s.update(func(r *receipt) { r.Domain = "x.example.com" }); err != nil {
		t.Fatal(err)
	}
	if r, err := s.load(); err != nil || r.Domain != "x.example.com" || r.Schema != receiptSchema || r.Created.IsZero() {
		t.Fatalf("after the replacement: %+v %v", r, err)
	}
}

// The first record of a file is the one that counts: a second run of the hardening sees BOBRES's own
// earlier content and must not take it for the original.
func TestReceiptFileRecordKeepsTheOriginal(t *testing.T) {
	var r receipt
	r.recordFile("/etc/apt/apt.conf.d/20auto-upgrades", []byte("ORIGINAL\n"), true, []byte("BOBRES v1\n"))
	r.recordFile("/etc/apt/apt.conf.d/20auto-upgrades", []byte("BOBRES v1\n"), true, []byte("BOBRES v2\n"))
	f, ok := r.file("/etc/apt/apt.conf.d/20auto-upgrades")
	if !ok || len(r.Files) != 1 {
		t.Fatalf("records: %+v", r.Files)
	}
	if b, ok := f.beforeContent(); !ok || string(b) != "ORIGINAL\n" {
		t.Fatalf("the original is lost: %q %v", b, ok)
	}
	if !strings.HasPrefix(f.Wrote, "") || f.Wrote == "" {
		t.Fatal("no checksum of what BOBRES wrote")
	}
	// A file that did not exist has no before; one BOBRES wrote earlier (an older release) has an unknown one.
	r.recordFile("/etc/new", nil, false, []byte("x"))
	if f, _ := r.file("/etc/new"); f.Existed {
		t.Fatalf("%+v", f)
	}
	r.recordUnknownBefore("/etc/old", []byte("# Written by bobres harden"))
	if f, _ := r.file("/etc/old"); !f.BeforeUnknown {
		t.Fatalf("%+v", f)
	} else if _, ok := f.beforeContent(); ok {
		t.Fatal("an unknown original was claimed to be known")
	}
	// A large original is not kept in the receipt.
	r.recordFile("/etc/big", []byte(strings.Repeat("x", maxReceiptBefore+1)), true, []byte("y"))
	if f, _ := r.file("/etc/big"); !f.BeforeUnknown || f.Before != "" {
		t.Fatalf("a big original was stored: %d bytes", len(f.Before))
	}
}

func TestReceiptPackageGroupsAccumulate(t *testing.T) {
	var r receipt
	r.setPackages(pkgGroup{Name: "fail2ban", By: "bobres harden", Requested: []string{"fail2ban"}, Packages: []string{"fail2ban", "whois"}})
	r.setPackages(pkgGroup{Name: "fail2ban", By: "bobres harden", Requested: []string{"python3-systemd"}, Packages: []string{"python3-systemd", "whois"}})
	g, ok := r.group("fail2ban")
	if !ok || len(r.Packages) != 1 {
		t.Fatalf("%+v", r.Packages)
	}
	if strings.Join(g.Packages, ",") != "fail2ban,python3-systemd,whois" || strings.Join(g.Requested, ",") != "fail2ban,python3-systemd" {
		t.Fatalf("a second run forgot what the first installed: %+v", g)
	}
}

func TestReceiptNoteImages(t *testing.T) {
	var r receipt
	have := func(ref string) bool { return ref != "ghcr.io/x/new:1" }
	inUse := func(ref string) bool { return ref == "caddy:2" }
	r.noteImages([]string{"ghcr.io/x/new:1", "redis:7", "caddy:2"}, have, inUse)
	// redis was there before and runs nothing of BOBRES's: not BOBRES's. caddy was there and is run
	// by the project (an install first receipted later): BOBRES's. The new image is BOBRES's.
	if got := strings.Join(r.Images.Preexisting, ","); got != "redis:7" {
		t.Fatalf("preexisting %q", got)
	}
	// A later install does not change what was decided.
	r.noteImages([]string{"redis:7"}, func(string) bool { return false }, func(string) bool { return false })
	if got := strings.Join(r.Images.Preexisting, ","); got != "redis:7" {
		t.Fatalf("preexisting after a second look: %q", got)
	}
	if len(r.Images.Refs) != 3 {
		t.Fatalf("refs %v", r.Images.Refs)
	}
}

func TestScriptMarkersAreParsedStrictly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, scriptMarkerDir), 0o755); err != nil {
		t.Fatal(err)
	}
	docker := "# written by install.sh 0.2.0\npackage docker-ce\npackage containerd.io\npackage git\npackage ; rm -rf /\npackage \n" +
		"file /etc/apt/sources.list.d/docker.list\nfile relative/path\ngroup docker\nbogus line\n"
	if err := os.WriteFile(filepath.Join(dir, scriptMarkerDir, "docker"), []byte(docker), 0o600); err != nil {
		t.Fatal(err)
	}
	// A marker whose name is not a plain word, and a directory, are ignored.
	_ = os.WriteFile(filepath.Join(dir, scriptMarkerDir, "../evil"), []byte("package x\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, scriptMarkerDir, "Bad Name"), []byte("package y\n"), 0o600)
	var r receipt
	importScriptMarkers(dir, &r)
	g, ok := r.group("docker")
	if !ok || len(r.Packages) != 1 {
		t.Fatalf("groups %+v", r.Packages)
	}
	if strings.Join(g.Packages, ",") != "containerd.io,docker-ce,git" || strings.Join(g.Files, ",") != "/etc/apt/sources.list.d/docker.list" || g.SystemGroup != "docker" {
		t.Fatalf("parsed %+v", g)
	}
	if strings.Join(g.Requested, ",") != "containerd.io,docker-ce" {
		t.Fatalf("requested %v: git came with Docker, it was not asked for", g.Requested)
	}
	// A group the hardening recorded is not replaced by a marker.
	var r2 receipt
	r2.setPackages(pkgGroup{Name: "docker", By: "bobres harden", Packages: []string{"mine"}})
	importScriptMarkers(dir, &r2)
	if g, _ := r2.group("docker"); g.By != "bobres harden" {
		t.Fatalf("%+v", g)
	}
}

func TestInstalledCLIPathOnlyAcceptsInstallLocations(t *testing.T) {
	t.Setenv("BOBRES_BIN_DIR", "/srv/bin")
	for p, want := range map[string]bool{
		"/usr/local/bin/bobres": true, "/srv/bin/bobres": true, "/usr/bin/bobres": true,
		"/tmp/bobres": false, "/home/me/bobres": false, "/usr/local/bin/other": false, "bobres": false, "": false,
		"/usr/local/bin/sub/bobres": false,
	} {
		if got := isInstalledCLIPath(p); got != want {
			t.Errorf("isInstalledCLIPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// install records the install, and the images, in the receipt it is given (and writes nothing without one).
func TestInstallWritesTheReceipt(t *testing.T) {
	dir := t.TempDir()
	state := t.TempDir()
	docker := &fakeDocker{health: []string{allHealthy}}
	ins, _, _ := testInstaller("", secrets, docker)
	ins.rcpt = newReceiptStore(state)
	// Compose names the images; caddy was on the server before and runs nothing of BOBRES's.
	base := ins.run
	ins.run = func(ctx context.Context, d string, args ...string) ([]byte, error) {
		switch {
		case contains(args, "config") && contains(args, "--images"):
			return []byte("ghcr.io/sobhanaz/bobres-core:1.2.3@sha256:abc\ncaddy:2.11.7-alpine@sha256:def\n"), nil
		case args[0] == "ps" && contains(args, "-a"):
			return nil, nil
		case args[0] == "image" && args[1] == "inspect":
			if args[len(args)-1] == "caddy:2.11.7-alpine@sha256:def" { // Docker 29 finds it by its digest only
				return []byte("sha256:caddy"), nil
			}
			return nil, os.ErrNotExist
		}
		return base(ctx, d, args...)
	}
	if code := ins.install(append(baseArgs(dir), "--no-harden", "--offline", "--skip-checks")); code != 0 {
		t.Fatalf("install: %d", code)
	}
	r, err := newReceiptStore(state).load()
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if r.InstallDir != real || r.Domain != "panel.example.com" || r.Schema != receiptSchema || r.Created.IsZero() {
		t.Fatalf("receipt %s", jsonOf(r))
	}
	if r.Images == nil || strings.Join(r.Images.Refs, ",") != "caddy:2.11.7-alpine@sha256:def,ghcr.io/sobhanaz/bobres-core:1.2.3@sha256:abc" ||
		strings.Join(r.Images.Preexisting, ",") != "caddy:2.11.7-alpine@sha256:def" {
		t.Fatalf("images %+v", r.Images)
	}
	// The test binary is no installed CLI, so it is not recorded.
	if r.CLIPath != "" {
		t.Fatalf("cli_path %q", r.CLIPath)
	}
	// Without a receipt store the install writes nothing and asks Docker for nothing extra.
	ins2, _, _ := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	if code := ins2.install(append(baseArgs(t.TempDir()), "--no-harden", "--offline", "--skip-checks")); code != 0 {
		t.Fatalf("install without a receipt: %d", code)
	}
}
