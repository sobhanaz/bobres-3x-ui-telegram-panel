package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDangerousDirs(t *testing.T) {
	for _, d := range []string{"/", "/usr", "/usr/local/bin", "/etc", "/var", "/var/lib", "/var/lib/docker", "/var/lib/bobres", "/home", "/home/alice",
		"/Users/alice", "/root", "/opt", "/srv", "/mnt", "/boot", "/tmp", "/bin", "/sbin", "/lib", "/run", "/sys", "/proc", "/dev", "/usr/share",
		"/var/log", "/media", "/usr/local", "/snap", "relative/path", "", "/x", "/opt/../usr", "/opt/bobres/../.."} {
		// dangerousDir cleans first: a path that climbs out to / or /usr is refused as that directory.
		if why := dangerousDir(d); why == "" {
			t.Errorf("dangerousDir(%q) allows it", d)
		}
	}
	for _, d := range []string{"/opt/bobres", "/srv/bobres", "/home/alice/bobres", "/data/bobres-store", "/mnt/disk/bobres"} {
		if why := dangerousDir(d); why != "" {
			t.Errorf("dangerousDir(%q) = %q", d, why)
		}
	}
	// The invoking user's own home directory and the state directory are protected too.
	if why := dangerousDir("/data/me", "/data/me"); why == "" {
		t.Error("an extra protected directory was allowed")
	}
}

func writeInstallMarkers(t *testing.T, dir string, env, compose string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{".env": env, "docker-compose.yml": compose} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveInstallDir(t *testing.T) {
	root := t.TempDir()
	good := "BOBRES_DOMAIN=panel.example.com\nBOBRES_VERSION=1.0.0\n"
	compose := "services:\n  core:\n    image: ghcr.io/sobhanaz/bobres-core:1\n"
	refused := func(name, dir, want string) {
		t.Helper()
		_, err := resolveInstallDir(dir)
		var r *refusal
		if !errors.As(err, &r) || !strings.Contains(r.msg, want) {
			t.Errorf("%s: %v (want a refusal mentioning %q)", name, err, want)
		}
	}
	// Not an install: the env without the compose file, the compose file without BOBRES's image, an env
	// without BOBRES's keys, a stranger's directory.
	d1 := filepath.Join(root, "only-env")
	writeInstallMarkers(t, d1, good, "")
	refused("env only", d1, "not a BOBRES install")
	d2 := filepath.Join(root, "foreign-compose")
	writeInstallMarkers(t, d2, good, "services:\n  web:\n    image: nginx\n")
	refused("compose of someone else", d2, "not a BOBRES install")
	d3 := filepath.Join(root, "foreign-env")
	writeInstallMarkers(t, d3, "FOO=bar\n", compose)
	refused("env of someone else", d3, "not a BOBRES install")
	d4 := filepath.Join(root, "stranger")
	if err := os.MkdirAll(d4, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d4, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused("a directory with other files", d4, "not a BOBRES install")

	// The marker files must be files, not links to somewhere else.
	d5 := filepath.Join(root, "linked-env")
	writeInstallMarkers(t, d5, "", compose)
	other := filepath.Join(root, "elsewhere.env")
	if err := os.WriteFile(other, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(d5, ".env")); err != nil {
		t.Fatal(err)
	}
	refused(".env is a link", d5, "not a BOBRES install")

	// The directory itself is no link, and no file.
	real := filepath.Join(root, "real")
	writeInstallMarkers(t, real, good, compose)
	link := filepath.Join(root, "link-to-real")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	refused("a symbolic link to an install", link, "symbolic link")
	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused("a file", file, "not a directory")
	refused("nothing given", "", "no install directory")
	refused("a system directory", "/usr", "system directory")

	// What is accepted.
	st, err := resolveInstallDir(real)
	if err != nil || !st.Install || !st.Exists || st.Env["BOBRES_DOMAIN"] != "panel.example.com" {
		t.Fatalf("a real install: %+v %v", st, err)
	}
	st, err = resolveInstallDir(filepath.Join(root, "not-there"))
	if err != nil || st.Exists || st.Install {
		t.Fatalf("a directory that is not there: %+v %v", st, err)
	}
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if st, err = resolveInstallDir(empty); err != nil || !st.Empty {
		t.Fatalf("an empty directory (an uninstall that stopped at its last step): %+v %v", st, err)
	}
	kept := filepath.Join(root, "kept")
	if err := os.MkdirAll(filepath.Join(kept, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, keptMarker), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err = resolveInstallDir(kept); err != nil || !st.Kept || st.Install {
		t.Fatalf("what --keep-backups leaves: %+v %v", st, err)
	}
	// An extra protected path (the state directory) is refused even when it looks like an install.
	if _, err := resolveInstallDir(real, real); err == nil {
		t.Error("a protected path was accepted as the install directory")
	}
}

// removeInside deletes a link without following it, and leaves a mount point alone.
func TestRemoveInsideNeverFollowsLinksOrCrossesMounts(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(outside, "deep", "precious.txt")
	if err := os.WriteFile(precious, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "install")
	for _, d := range []string{"tls/certs", "caddy", "backups"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A link the tls-agent's user could have planted: to a directory, and a nested one to a file.
	if err := os.Symlink(outside, filepath.Join(dir, "tls", "certs", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(precious, filepath.Join(dir, "caddy", "filelink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "toplink")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backups", "a.bkp"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := removeInside(dir, []string{"tls", "caddy", "toplink", "backups"})
	if err != nil || len(kept) != 0 {
		t.Fatalf("removeInside: kept %v, %v", kept, err)
	}
	if b, err := os.ReadFile(precious); err != nil || string(b) != "keep" {
		t.Fatalf("a link was followed: %v", err)
	}
	for _, n := range []string{"tls", "caddy", "toplink", "backups"} {
		if existsReal(filepath.Join(dir, n)) {
			t.Errorf("%s is still there", n)
		}
	}
	// Names that climb or nest are refused.
	for _, bad := range []string{"..", ".", "", "a/b", "../outside"} {
		if _, err := removeInside(dir, []string{bad}); err == nil {
			t.Errorf("removeInside accepted the name %q", bad)
		}
	}
	if !existsReal(precious) {
		t.Fatal("something outside was deleted")
	}
}

func TestRemoveAroundKeepsMountPoints(t *testing.T) {
	dir := t.TempDir()
	mount := filepath.Join(dir, "data", "share") // stands for another file system mounted here
	for _, d := range []string{"data/share", "data/own"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"data/share/theirs.txt", "data/own/mine.txt", "data/top.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	kept, err := removeAround(root, dir, "data", []string{mount})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != mount {
		t.Fatalf("kept %v", kept)
	}
	if !existsReal(filepath.Join(mount, "theirs.txt")) {
		t.Fatal("the mounted file system was entered")
	}
	if existsReal(filepath.Join(dir, "data", "own")) || existsReal(filepath.Join(dir, "data", "top.txt")) {
		t.Fatal("what is on the install's own file system was not removed")
	}
}

func TestParseUfwAddedDecisions(t *testing.T) {
	out := `Added user rules (see 'ufw status' for running firewall):
ufw allow 22/tcp comment 'bobres SSH'
ufw allow 80/tcp comment 'bobres HTTP'
ufw allow 443/tcp comment 'bobres HTTPS'
ufw allow 443/udp comment 'bobres HTTP/3'
ufw allow from 172.16.0.0/12 to any port 8081 proto tcp comment 'bobres Bot API server'
ufw allow from 10.0.0.0/8 to any port 9090 proto tcp comment 'bobres 3x-ui panel'
ufw allow 2053/tcp comment 'bobres 3x-ui'
ufw allow 8080/tcp comment 'my web'
ufw allow 9000/tcp
ufw deny 23/tcp comment 'bobres HTTP'
ufw allow 81/tcp comment 'bobres HTTP'
ufw allow 7777/tcp comment 'bobres Something New'
ufw allow 25/tcp comment "bobres HTTPS"; rm -rf /"
`
	items, foreign := parseUfwAdded(out, map[string]bool{"allow 443/tcp": true})
	got := map[string]string{}
	for _, it := range items {
		verdict := "keep"
		if it.Delete {
			verdict = "delete"
		}
		got[it.spec()+" | "+it.Comment] = verdict
	}
	want := map[string]string{
		"allow 22/tcp | bobres SSH":     "keep",
		"allow 80/tcp | bobres HTTP":    "delete",
		"allow 443/tcp | bobres HTTPS":  "keep", // it was there before BOBRES
		"allow 443/udp | bobres HTTP/3": "delete",
		"allow from 172.16.0.0/12 to any port 8081 proto tcp | bobres Bot API server": "delete",
		"allow from 10.0.0.0/8 to any port 9090 proto tcp | bobres 3x-ui panel":       "delete",
		"allow 2053/tcp | bobres 3x-ui":                                               "keep",
		"deny 23/tcp | bobres HTTP":                                                   "keep", // not a plain allow rule
		"allow 81/tcp | bobres HTTP":                                                  "keep", // not the form BOBRES adds
		"allow 7777/tcp | bobres Something New":                                       "keep", // a rule this release does not know
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if foreign != 3 { // my web, no comment, and the line with a quote trick (a comment that is not BOBRES's)
		t.Errorf("foreign rules: %d", foreign)
	}
	for _, it := range items {
		if strings.ContainsAny(it.spec(), ";'\"") && it.Delete {
			t.Errorf("a rule with odd characters would be deleted: %q", it.spec())
		}
	}
}
