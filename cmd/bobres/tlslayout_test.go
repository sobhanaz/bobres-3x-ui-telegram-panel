package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// assertHTTPSLayout checks what an install of domain leaves for Caddy: its
// main Caddyfile in caddy/, and tls/ with the default snippets.
func assertHTTPSLayout(t *testing.T, dir, domain string) {
	t.Helper()
	cdir, err := os.Stat(filepath.Join(dir, "caddy"))
	if err != nil || !cdir.IsDir() || cdir.Mode().Perm() != 0o755 {
		t.Errorf("caddy/: %v %v (want a directory, mode 0755)", cdir, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "caddy", "Caddyfile"))
	if err != nil || !bytes.Equal(b, deploy.Caddyfile) {
		t.Errorf("caddy/Caddyfile is not the embedded Caddyfile (%v)", err)
	}
	if st, err := os.Stat(filepath.Join(dir, "caddy", "Caddyfile")); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("caddy/Caddyfile mode: %v %v", st, err)
	}
	tdir, err := os.Stat(filepath.Join(dir, "tls"))
	if err != nil || !tdir.IsDir() {
		t.Fatalf("tls/: %v %v", tdir, err)
	}
	if asRoot.euid() == 0 {
		if tdir.Mode().Perm() != 0o750 {
			t.Errorf("tls/ as root: mode %v (want 0750)", tdir.Mode().Perm())
		}
		if st := tdir.Sys().(*syscall.Stat_t); os.Geteuid() == 0 && (st.Uid != tlsOwner || st.Gid != tlsOwner) {
			t.Errorf("tls/ as root: owner %d:%d (want 65532:65532)", st.Uid, st.Gid)
		}
	} else if tdir.Mode().Perm() != 0o777 {
		t.Errorf("tls/ without root: mode %v (want 0777: the tls-agent, uid 65532, must be able to write)", tdir.Mode().Perm())
	}
	want, err := tlsconf.Render(tlsconf.State{Mode: tlsconf.ModeAuto, Domain: domain})
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{tlsconf.GlobalFile: want.Global, tlsconf.SitesFile: want.Sites} {
		got, err := os.ReadFile(filepath.Join(dir, "tls", name))
		if err != nil || string(got) != text {
			t.Errorf("tls/%s: %v\n%s\nwant\n%s", name, err, got, text)
		}
		if st, err := os.Stat(filepath.Join(dir, "tls", name)); err != nil || st.Mode().Perm() != 0o644 {
			t.Errorf("tls/%s mode: %v %v (the defaults the root CLI writes are 0644)", name, st, err)
		}
	}
	if !strings.Contains(want.Sites, domain+" {") || !strings.Contains(want.Sites, "import bobres_site") {
		t.Errorf("the default sites snippet has no site for %s:\n%s", domain, want.Sites)
	}
}

// A fresh install writes Caddy's file into caddy/, the default HTTPS snippets
// into tls/ and no top-level Caddyfile; it says so when tls/ had to be left
// world-writable.
func TestInstallWritesTheHTTPSLayout(t *testing.T) {
	dir := t.TempDir()
	ins, out, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	assertHTTPSLayout(t, dir, "panel.example.com")
	if lexists(filepath.Join(dir, "Caddyfile")) {
		t.Error("a top-level Caddyfile was written: nothing mounts it any more")
	}
	if asRoot.euid() != 0 && !strings.Contains(out.String(), "writable by everyone (mode 0777)") {
		t.Errorf("no note about tls/ being world-writable:\n%s", out)
	}
	// A quiet run (an update, a settings change) does not repeat the note.
	ins, out, errOut = testInstaller("", secrets, nil)
	if code := ins.install([]string{"--dir", dir, "--quiet", "--yes", "--skip-checks", "--offline", "--no-start"}); code != 0 {
		t.Fatalf("quiet run: %d: %s", code, errOut)
	}
	if strings.Contains(out.String(), "writable by everyone") {
		t.Errorf("the quiet run repeated the note:\n%s", out)
	}
}

// The top-level Caddyfile of an install from before the move is never touched:
// the compose file a rollback brings back mounts it.
func TestInstallLeavesAnExistingTopLevelCaddyfile(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(old, []byte("# the old gateway\n{$BOBRES_DOMAIN} {\n}\n"), 0o640); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}
	ins, _, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	b, err := os.ReadFile(old)
	if err != nil || string(b) != "# the old gateway\n{$BOBRES_DOMAIN} {\n}\n" {
		t.Fatalf("the old Caddyfile was changed or removed: %q %v", b, err)
	}
	if st, _ := os.Stat(old); st.Mode().Perm() != 0o640 {
		t.Errorf("the old Caddyfile's mode changed to %v", st.Mode().Perm())
	}
	assertHTTPSLayout(t, dir, "panel.example.com")
}

// The tls-agent owns what is in tls/: a second install (an update) leaves its
// files alone, even for another domain (core asks the agent for that).
func TestInstallKeepsWhatTheAgentWrote(t *testing.T) {
	dir := t.TempDir()
	ins, _, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	agent := map[string]string{tlsconf.GlobalFile: "# the agent's global\ndefault_sni 93.184.216.34\n", tlsconf.SitesFile: "# the agent's sites\n"}
	for name, text := range agent {
		path := filepath.Join(dir, "tls", name)
		if err := os.WriteFile(path, []byte(text), 0o640); err != nil { //nolint:gosec // the agent's mode
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o640); err != nil { //nolint:gosec // the agent's mode
			t.Fatal(err)
		}
	}
	ins, _, errOut = testInstaller("", secrets, nil)
	if code := ins.install([]string{"--dir", dir, "--domain", "other.example.com", "--skip-checks", "--offline", "--no-start", "--yes"}); code != 0 {
		t.Fatalf("second run: %d: %s", code, errOut)
	}
	for name, text := range agent {
		if b, _ := os.ReadFile(filepath.Join(dir, "tls", name)); string(b) != text {
			t.Errorf("tls/%s was rewritten:\n%s", name, b)
		}
		if st, _ := os.Stat(filepath.Join(dir, "tls", name)); st.Mode().Perm() != 0o640 {
			t.Errorf("tls/%s mode changed to %v", name, st.Mode().Perm())
		}
	}
	// A deleted one comes back (the first start needs both).
	if err := os.Remove(filepath.Join(dir, "tls", tlsconf.SitesFile)); err != nil {
		t.Fatal(err)
	}
	ins, _, errOut = testInstaller("", secrets, nil)
	if code := ins.install([]string{"--dir", dir, "--skip-checks", "--offline", "--no-start", "--yes"}); code != 0 {
		t.Fatalf("third run: %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "tls", tlsconf.SitesFile)); !strings.Contains(string(b), "other.example.com {") {
		t.Errorf("the missing sites file was not made again:\n%s", b)
	}
}

// tls/ belongs to the tls-agent (uid 65532): whatever it leaves there, the
// root CLI never follows a link out of it. The directories themselves (tls/,
// caddy/, and an entry that is a directory where a file goes) stop the install.
func TestInstallRefusesLinksInTheHTTPSLayout(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "victim"), []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		setup func(dir string)
		want  string
	}{
		{"tls is a link", func(dir string) { mustSymlink(t, outside, filepath.Join(dir, "tls")) }, "is a symbolic link"},
		{"caddy is a link", func(dir string) { mustSymlink(t, outside, filepath.Join(dir, "caddy")) }, "is a symbolic link"},
		{"sites.caddy is a directory", func(dir string) {
			mustMkdir(t, filepath.Join(dir, "tls"))
			mustMkdir(t, filepath.Join(dir, "tls", tlsconf.SitesFile))
		}, "is a directory"},
		{"tls is a file", func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "tls"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)
			ins, _, errOut := testInstaller("", secrets, nil)
			code := ins.install(append(baseArgs(dir), "--no-start"))
			if code != 1 || !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("exit %d, want 1 with %q: %s", code, tc.want, errOut)
			}
			entries, _ := os.ReadDir(outside)
			if len(entries) != 1 {
				t.Errorf("the install wrote through the link: %v", entries)
			}
			if b, _ := os.ReadFile(filepath.Join(outside, "victim")); string(b) != "do not touch" {
				t.Errorf("the file behind the link changed: %q", b)
			}
		})
	}
	// A directory where a file goes is refused, never removed (even empty).
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "tls"))
	mustMkdir(t, filepath.Join(dir, "tls", tlsconf.SitesFile))
	if _, err := writeIfMissing(filepath.Join(dir, "tls", tlsconf.SitesFile), []byte("x")); err == nil {
		t.Error("a directory was accepted")
	}
	if st, err := os.Lstat(filepath.Join(dir, "tls", tlsconf.SitesFile)); err != nil || !st.IsDir() {
		t.Errorf("the directory was removed: %v %v", st, err)
	}
}

// The tls-agent owns tls/, so a compromised one can leave a link, a pipe or a
// socket where the default snippets go. That must not stop every install and
// update (an update rolls back on a failed install, so security updates would
// be blocked): the entry is unlinked, never followed, and the default is
// written in its place, with a note.
func TestInstallReplacesHostileEntriesOfTheAgent(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		plant func(path string)
		kind  string
	}{
		{"a link to a file", func(path string) { mustSymlink(t, victim, path) }, "a symbolic link"},
		{"a dangling link", func(path string) { mustSymlink(t, filepath.Join(filepath.Dir(victim), "missing"), path) }, "a symbolic link"},
		{"a link to a directory", func(path string) { mustSymlink(t, filepath.Dir(victim), path) }, "a symbolic link"},
		{"a pipe", func(path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "a pipe"},
		{"a socket", func(path string) { mustSocket(t, path) }, "a socket"},
	} {
		for _, name := range []string{tlsconf.SitesFile, tlsconf.GlobalFile} {
			t.Run(tc.name+" as "+name, func(t *testing.T) {
				dir := t.TempDir()
				mustMkdir(t, filepath.Join(dir, "tls"))
				tc.plant(filepath.Join(dir, "tls", name))
				ins, out, errOut := testInstaller("", secrets, nil)
				ins.quiet = true // an update says it too: it is a warning, not a note
				done := make(chan int, 1)
				go func() { done <- ins.install(append(baseArgs(dir), "--no-start")) }()
				select {
				case code := <-done:
					if code != 0 {
						t.Fatalf("exit %d: %s", code, errOut)
					}
				case <-time.After(20 * time.Second):
					t.Fatal("the install hangs on the entry (it opened it)")
				}
				assertHTTPSLayout(t, dir, "panel.example.com")
				if st, err := os.Lstat(filepath.Join(dir, "tls", name)); err != nil || !st.Mode().IsRegular() {
					t.Fatalf("tls/%s afterwards: %v %v", name, st, err)
				}
				if b, _ := os.ReadFile(victim); string(b) != "do not touch" {
					t.Errorf("what the link led to was written: %q", b)
				}
				if want := "warning: " + filepath.Join(dir, "tls", name) + " was " + tc.kind; !strings.Contains(errOut.String(), want) {
					t.Errorf("no warning %q in\n%s", want, errOut)
				}
				if strings.Contains(out.String(), "was "+tc.kind) {
					t.Errorf("the warning went to the notes:\n%s", out)
				}
			})
		}
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// mustSocket leaves a unix socket (nothing listens on it) at path: bound by a
// relative name, as a socket's path is limited to about a hundred bytes.
func mustSocket(t *testing.T, path string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatal(err)
		}
	}()
	l, err := net.Listen("unix", filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil { //nolint:gosec // a test directory
		t.Fatal(err)
	}
}

// writeIfMissing creates with O_EXCL: a path that appears between the look and
// the write is not followed.
func TestWriteIfMissingNeverFollowsAFreshLink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A link is unlinked, not followed: the file behind it is not written.
	link := filepath.Join(dir, "sites.caddy")
	mustSymlink(t, victim, link)
	removed, err := writeIfMissing(link, []byte("x"))
	if err != nil || removed != "a symbolic link" {
		t.Fatalf("a link: removed %q, %v", removed, err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "do not touch" {
		t.Errorf("the link's target changed: %q", b)
	}
	if b, err := os.ReadFile(link); err != nil || string(b) != "x" {
		t.Errorf("the link was not replaced by the file: %q %v", b, err)
	}
	// A regular file stays as it is; a missing one is made, mode 0644.
	file := filepath.Join(dir, "global.caddy")
	if err := os.WriteFile(file, []byte("agent"), 0o640); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}
	if removed, err := writeIfMissing(file, []byte("default")); err != nil || removed != "" {
		t.Fatalf("a regular file: removed %q, %v", removed, err)
	}
	if b, _ := os.ReadFile(file); string(b) != "agent" {
		t.Errorf("an existing file was replaced: %q", b)
	}
	fresh := filepath.Join(dir, "fresh.caddy")
	if removed, err := writeIfMissing(fresh, []byte("default")); err != nil || removed != "" {
		t.Fatalf("a missing file: removed %q, %v", removed, err)
	}
	if st, err := os.Stat(fresh); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("a new file: %v %v", st, err)
	}
	// The creation itself never follows a link that is there at that moment.
	planted := filepath.Join(dir, "planted.caddy")
	mustSymlink(t, victim, planted)
	if err := createExclusive(planted, []byte("x")); !errors.Is(err, fs.ErrExist) {
		t.Errorf("createExclusive on a link: %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "do not touch" {
		t.Errorf("createExclusive wrote through the link: %q", b)
	}
}

// fakeRoot makes prepareTLS and writeIfMissing see root where the test runs as
// an ordinary user: euid 0; chown and chownPath are recorded (nothing changes
// hands); owners say who owns a file by its name (0 when not listed).
type fakeRoot struct {
	euid    int
	chowned []string // "<name> <uid>:<gid>"
	owners  map[string]int
	// chownErr is what the directory's chown answers.
	chownErr error
}

func withFakeRoot(t *testing.T) *fakeRoot {
	t.Helper()
	f := &fakeRoot{owners: map[string]int{}}
	old := asRoot
	asRoot = rootCalls{
		euid: func() int { return f.euid },
		chown: func(file *os.File, uid, gid int) error {
			f.chowned = append(f.chowned, fmt.Sprintf("%s %d:%d", filepath.Base(file.Name()), uid, gid))
			return f.chownErr
		},
		owner: func(fi os.FileInfo) (int, bool) { return f.owners[fi.Name()], true },
		chownPath: func(path string, uid, gid int) error {
			f.chowned = append(f.chowned, fmt.Sprintf("%s %d:%d", filepath.Base(path), uid, gid))
			return nil
		},
	}
	t.Cleanup(func() { asRoot = old })
	return f
}

// As root, tls/ is given to the agent (65532:65532) by its open descriptor and
// closed to everyone else (0750), whatever mode it had; the directory the
// agent does not have is made 0777 and said so. Each of the three is checked
// with a value that fails when the step is left out: a directory that starts
// at 0777, the exact uid and gid, and the note that must not appear.
func TestPrepareTLSAsRoot(t *testing.T) {
	f := withFakeRoot(t)
	env := map[string]string{"BOBRES_DOMAIN": "panel.example.com"}

	f.euid = 0
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "tls"))
	if err := os.Chmod(filepath.Join(dir, "tls"), 0o777); err != nil { //nolint:gosec // the mode a careless install left
		t.Fatal(err)
	}
	var notes bytes.Buffer
	if err := prepareTLS(dir, env, &notes, io.Discard); err != nil {
		t.Fatal(err)
	}
	if want := []string{"tls 65532:65532"}; !slices.Equal(f.chowned, want) {
		t.Errorf("chown calls %q, want %q: tls/ goes to the agent, through its descriptor", f.chowned, want)
	}
	if st, err := os.Stat(filepath.Join(dir, "tls")); err != nil || st.Mode().Perm() != 0o750 {
		t.Errorf("tls/ as root: %v %v (want mode 0750)", st, err)
	}
	if notes.Len() != 0 {
		t.Errorf("a root install says tls/ is world-writable:\n%s", &notes)
	}
	if _, err := os.Stat(filepath.Join(dir, "tls", tlsconf.SitesFile)); err != nil {
		t.Errorf("the default snippets are not there: %v", err)
	}

	// Not root: no chown, mode 0777 and the note.
	f.euid, f.chowned = 1000, nil
	dir = t.TempDir()
	notes.Reset()
	if err := prepareTLS(dir, env, &notes, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(f.chowned) != 0 {
		t.Errorf("an ordinary user chowned: %q", f.chowned)
	}
	if st, err := os.Stat(filepath.Join(dir, "tls")); err != nil || st.Mode().Perm() != 0o777 {
		t.Errorf("tls/ without root: %v %v (want mode 0777)", st, err)
	}
	if !strings.Contains(notes.String(), "writable by everyone (mode 0777)") {
		t.Errorf("no note about the mode:\n%s", &notes)
	}

	// A chown that fails stops the install with the path in the message.
	f.euid, f.chownErr = 0, errors.New("operation not permitted")
	err := prepareTLS(t.TempDir(), env, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "chown") || !strings.Contains(err.Error(), "operation not permitted") {
		t.Errorf("a failing chown: %v", err)
	}
}

// As root, a file in tls/ that belongs to neither root nor the agent is not
// trusted (someone else made it); as an ordinary user nobody is judged.
func TestWriteIfMissingChecksTheOwnerAsRoot(t *testing.T) {
	f := withFakeRoot(t)
	for _, tc := range []struct {
		name  string
		euid  int
		owner int
		ok    bool
	}{
		{"root's file", 0, 0, true},
		{"the agent's file", 0, tlsOwner, true},
		{"another user's file", 0, 1000, false},
		{"the backup user is not the agent's uid by accident", 0, tlsOwner + 1, false},
		{"another user's file, not root", 1000, 1000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sites.caddy")
			if err := os.WriteFile(path, []byte("theirs"), 0o640); err != nil { //nolint:gosec // a fixture
				t.Fatal(err)
			}
			f.euid, f.owners["sites.caddy"] = tc.euid, tc.owner
			_, err := writeIfMissing(path, []byte("default"))
			switch {
			case tc.ok && err != nil:
				t.Errorf("refused: %v", err)
			case !tc.ok && (err == nil || !strings.Contains(err.Error(), fmt.Sprintf("belongs to uid %d", tc.owner))):
				t.Errorf("a file of uid %d: %v", tc.owner, err)
			}
			if b, _ := os.ReadFile(path); string(b) != "theirs" {
				t.Errorf("the file was rewritten: %q", b)
			}
		})
	}
}

// A whole install as root (the real one runs as root on a server): tls/ goes to
// the agent, backups/ to the backup service's user and run/update/ (where the
// dashboard drops its update requests) to core's, each by a chown of its own
// (run/ stays root's), and the files and modes of the layout are the ones a
// server gets.
func TestInstallAsRootGivesTheDirectoriesAway(t *testing.T) {
	f := withFakeRoot(t)
	f.euid = 0
	dir := t.TempDir()
	ins, out, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if want := []string{"backups 65532:65532", "run 0:0", "tls 65532:65532", "update 65532:65532"}; !slices.Equal(sortedCopy(f.chowned), want) {
		t.Errorf("chown calls %q, want %q", f.chowned, want)
	}
	for path, mode := range map[string]os.FileMode{"run": 0o755, filepath.Join("run", "update"): 0o700} {
		if st, err := os.Stat(filepath.Join(dir, path)); err != nil || !st.IsDir() || st.Mode().Perm() != mode {
			t.Errorf("%s: %v %v, want a directory of mode %04o", path, st, err, mode)
		}
	}
	assertHTTPSLayout(t, dir, "panel.example.com")
	if strings.Contains(out.String(), "writable by everyone") {
		t.Errorf("a root install made tls/ world-writable:\n%s", out)
	}
}

func sortedCopy(list []string) []string {
	out := slices.Clone(list)
	slices.Sort(out)
	return out
}

// The default snippets are the engine's own rendering of automatic mode (a
// private or LAN IP address is rendered too, with Caddy's own CA); for a
// domain the engine refuses they are put together from the installer's checked
// values, and a value that is not a host name gives no site at all.
func TestDefaultTLSFiles(t *testing.T) {
	render := func(domain, zp string) (string, string) {
		f, err := tlsconf.Render(tlsconf.State{Mode: tlsconf.ModeAuto, Domain: domain, ZarinpalHost: zp})
		if err != nil {
			t.Fatalf("%s: %v", domain, err)
		}
		return f.Global, f.Sites
	}
	for _, tc := range []struct{ domain, zp string }{
		{"panel.example.com", ""},
		{"Panel.Example.com", "pay.example.net"},
		{"93.184.216.34", ""},
		{"10.0.0.5", "pay.example.net"},
		{"bobres.localhost", ""},
	} {
		g, s := defaultTLSFiles(map[string]string{"BOBRES_DOMAIN": tc.domain, "BOBRES_ZARINPAL_HOST": tc.zp})
		wg, ws := render(tc.domain, tc.zp)
		if g != wg || s != ws {
			t.Errorf("%s: not the engine's rendering\n%s\n%s", tc.domain, g, s)
		}
	}

	// A private IP address is the engine's too: Caddy's own CA signs it.
	if _, s := render("10.0.0.5", ""); !strings.Contains(s, "\n10.0.0.5 {\n\ttls internal\n\timport bobres_site\n}\n") {
		t.Errorf("a private IP address as the domain:\n%s", s)
	}
	// Refused by the engine (a multicast address is no server's): the fallback.
	g, s := defaultTLSFiles(map[string]string{"BOBRES_DOMAIN": "224.0.0.1", "BOBRES_ZARINPAL_HOST": "pay.example.net"})
	if !strings.Contains(g, "default_sni 224.0.0.1\n") || !strings.Contains(s, "\n224.0.0.1 {\n\timport bobres_site\n}\n") ||
		!strings.Contains(s, "\npay.example.net {\n") || !strings.Contains(s, "handle @bobres_not_zarinpal") {
		t.Errorf("fallback for an address the engine refuses:\n%s\n%s", g, s)
	}
	_, s = defaultTLSFiles(map[string]string{"BOBRES_DOMAIN": "10.0.0.5", "BOBRES_ZARINPAL_HOST": "10.0.0.5"})
	if strings.Count(s, "import bobres_site") != 1 {
		t.Errorf("a Zarinpal host equal to the domain is no second site:\n%s", s)
	}
	// Nothing but text a host name can be goes into a file.
	for _, bad := range []string{"panel.example.com {\n\trespond \"x\"\n}\nevil.com", "a b.example.com", "panel.example.com;", ""} {
		_, s := defaultTLSFiles(map[string]string{"BOBRES_DOMAIN": bad})
		if strings.Contains(s, "import bobres_site") || strings.Contains(s, "respond") || strings.Contains(s, "evil") {
			t.Errorf("domain %q produced a site:\n%s", bad, s)
		}
	}
	_, s = defaultTLSFiles(map[string]string{"BOBRES_DOMAIN": "10.0.0.5", "BOBRES_ZARINPAL_HOST": "x {\n}"})
	if strings.Contains(s, "x {") || strings.Count(s, "import bobres_site") != 1 {
		t.Errorf("a bad Zarinpal host got in:\n%s", s)
	}
}

// After `up -d` the installer tells Caddy to read its Caddyfile again and
// waits for the gateway; neither can fail the install.
func TestInstallReloadsCaddyBestEffort(t *testing.T) {
	flap := `[{"Service":"caddy","State":"restarting"}]`
	run := `[{"Service":"caddy","State":"running"}]`
	for _, tc := range []struct {
		name     string
		docker   *fakeDocker
		warn     string
		wait     time.Duration // how long the installer waits for Caddy
		waitFull bool          // all of caddyWait
	}{
		{"fine", &fakeDocker{}, "", 2 * time.Second, false},
		{"the signal fails", &fakeDocker{killErr: errors.New("service \"caddy\" is not running")}, "Caddy was not told to read its Caddyfile again", 2 * time.Second, false},
		{"a crash loop", &fakeDocker{caddy: []string{flap}}, "not running steadily (state: restarting)", caddyWait, true},
		{"running now and then", &fakeDocker{caddy: []string{run, flap, run, flap}}, "not running steadily (state: restarting)", caddyWait, true},
		{"slow to start", &fakeDocker{caddy: []string{`[]`, flap, run, run}}, "", 6 * time.Second, false},
		{"no container", &fakeDocker{caddy: []string{`[]`}}, "not running steadily (state: no container)", caddyWait, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.docker.health = []string{`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`}
			ins, _, errOut := testInstaller("", secrets, tc.docker)
			var waited time.Duration
			ins.sleep = func(d time.Duration) { waited += d }
			if code := ins.install(baseArgs(dir)); code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			// waitHealthy sleeps too, but only while services are pending.
			if tc.warn == "" && strings.Contains(errOut.String(), "warning") {
				t.Errorf("unexpected warning: %s", errOut)
			}
			if tc.warn != "" && !strings.Contains(errOut.String(), tc.warn) {
				t.Errorf("want a warning with %q, got: %s", tc.warn, errOut)
			}
			if waited > tc.wait || (tc.waitFull && waited < tc.wait) {
				t.Errorf("waited %s for Caddy, want %s (full: %v)", waited, tc.wait, tc.waitFull)
			}
		})
	}
}

// publicIP trusts a service's answer only when it is a plain 200 of an IP
// address of at most 64 bytes, and the address is a public one.
func TestPublicIPIsStrict(t *testing.T) {
	big := strings.Repeat("1", 65)
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"an address", 200, "93.184.216.34\n", "93.184.216.34"},
		{"spaces around", 200, "  93.184.216.34  ", "93.184.216.34"},
		{"IPv6", 200, "2606:2800:220:1:248:1893:25c8:1946", "2606:2800:220:1:248:1893:25c8:1946"},
		{"not found", 404, "93.184.216.34", ""},
		{"server error", 500, "93.184.216.34", ""},
		{"a redirect", 302, "93.184.216.34", ""},
		{"an html page", 200, "<html>captcha</html>", ""},
		{"too long", 200, big, ""},
		{"empty", 200, "", ""},
		{"two addresses", 200, "93.184.216.34 8.8.8.8", ""},
		{"a host name", 200, "example.com", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 302 {
					http.Redirect(w, r, "/elsewhere", http.StatusFound)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			text, ok := fetchIPText(context.Background(), &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, srv.URL)
			if (tc.want != "") != ok || (ok && text != tc.want) {
				t.Errorf("fetchIPText = %q, %v; want %q", text, ok, tc.want)
			}
			defer func(old []string) { ipServices = old }(ipServices)
			ipServices = []string{srv.URL}
			if got := publicIP(); got != tc.want {
				t.Errorf("publicIP = %q, want %q", got, tc.want)
			}
		})
	}

	// An address that is not public is no answer, and the next service is asked.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("10.0.0.7")) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("8.8.4.4")) }))
	defer good.Close()
	defer func(old []string) { ipServices = old }(ipServices)
	ipServices = []string{bad.URL, good.URL}
	if got := publicIP(); got != "8.8.4.4" {
		t.Errorf("a private answer, then a good one: %q", got)
	}
	for _, private := range []string{"10.0.0.7", "192.168.1.9", "100.64.3.3", "127.0.0.1", "169.254.1.1", "0.0.0.0", "::1", "fe80::1", "fd00::5"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(private)) }))
		t.Cleanup(srv.Close)
		ipServices = []string{srv.URL}
		if got := publicIP(); got != "" {
			t.Errorf("publicIP accepted %s", private)
		}
	}
}

// doctor reads the install's domain from its .env when none is given, and
// says nothing when it cannot.
func TestInstallDomainOfDoctor(t *testing.T) {
	dir := installDir(t)
	env, ok, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil || !ok || env["BOBRES_DOMAIN"] != "panel.example.com" {
		t.Fatalf("fixture: %v %v %v", env, ok, err)
	}
	if got := domainOfInstall(dir); got != "panel.example.com" {
		t.Errorf("domainOfInstall = %q", got)
	}
	if got := domainOfInstall(t.TempDir()); got != "" {
		t.Errorf("no install: %q", got)
	}
}
