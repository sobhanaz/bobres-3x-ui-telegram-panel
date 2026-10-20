package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// These tests hold the code to the inventory in uninstall_inventory.go: whatever the installer, the
// hardening or a unit file puts on a server must be a thing `bobres uninstall --purge` knows. When one
// of them fails, a step was added that the uninstall cannot undo: list it in hostArtifacts, teach the
// uninstall to remove it, and describe it in docs/06-installer.md ("What installing BOBRES changes").

// repoRoot is the repository's root, from the package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the repository root is not at %s: %v", root, err)
	}
	return root
}

// hostPathLiteral matches a string that is nothing but a path on the host.
var hostPathLiteral = regexp.MustCompile(`^/(etc|var|usr|opt|lib|run|root|home|srv|boot|swapfile|proc|sys)(/[A-Za-z0-9_.*@%+-]*)*$`)

// goPathLiterals are the host path literals of the Go sources in dir (not tests), by file.
func goPathLiterals(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		base := filepath.Base(f)
		// The uninstall and the receipt are the readers of the inventory, not the makers of anything.
		if strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "uninstall") || strings.HasPrefix(base, "receipt") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && hostPathLiteral.MatchString(s) {
				out[base] = append(out[base], s)
			}
			return true
		})
	}
	return out
}

// installShPathLiterals are the host paths install.sh names outside its comments.
func installShPathLiterals(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "install", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?:^|[\s"'=(:])(/(?:etc|var|usr|opt|root|run|lib|srv|swapfile)(?:/[A-Za-z0-9_.*@+-]*)*)`)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") || strings.Contains(trim, "export PATH=") {
			continue
		}
		if i := strings.Index(line, " # "); i >= 0 {
			line = line[:i]
		}
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			seen[strings.TrimRight(m[1], "/.")] = true
		}
	}
	var out []string
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func TestHostArtifactsAreKnown(t *testing.T) {
	root := repoRoot(t)
	for file, paths := range goPathLiterals(t, filepath.Join(root, "cmd", "bobres")) {
		for _, p := range paths {
			if id, ok := artifactFor(p); !ok {
				t.Errorf("cmd/bobres/%s names the host path %s, which `bobres uninstall` does not know.\n"+
					"List it in hostArtifacts (cmd/bobres/uninstall_inventory.go), make the uninstall undo it, and document it in docs/06-installer.md.", file, p)
			} else if id == "" {
				t.Errorf("%s has no id", p)
			}
		}
	}
	for _, p := range installShPathLiterals(t) {
		if _, ok := artifactFor(p); !ok {
			t.Errorf("install/install.sh names the host path %s, which `bobres uninstall` does not know (see hostArtifacts in cmd/bobres/uninstall_inventory.go)", p)
		}
	}
}

// unitFiles are the unit files in the repository (embedded in the binary or shipped next to it).
func unitFiles(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".service", ".timer", ".path", ".socket", ".mount", ".automount", ".target", ".slice", ".swap", ".scope":
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Every unit file BOBRES ships must be one the uninstall finds by its name, and must work only inside
// places the uninstall removes or leaves alone.
func TestEmbeddedUnitFilesAreFoundByTheUninstall(t *testing.T) {
	files := unitFiles(t)
	if len(files) < 2 {
		t.Fatalf("expected the updater's units in the repository, found %v", files)
	}
	pathRe := regexp.MustCompile(`/[A-Za-z0-9_.@+-]+(?:/[A-Za-z0-9_.@+-]*)*`)
	allowed := map[string]bool{"/bin/sh": true, "/bin/rm": true, "/tmp": true}
	for _, f := range files {
		name := filepath.Base(f)
		if !unitNameRe.MatchString(name) {
			t.Errorf("%s: a unit BOBRES ships must be named bobres-<what>.<kind> (or bobres.<kind>), or the uninstall cannot find it by name", f)
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.NewReplacer("@DIR@", "/opt/bobres", "@STATE@", "/var/lib/bobres", "@EXE@", "/usr/local/bin/bobres", "@EXEDIR@", "/usr/local/bin").Replace(string(b))
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "Documentation=") || strings.HasPrefix(line, "Description=") {
				continue
			}
			for _, p := range pathRe.FindAllString(line, -1) {
				p = strings.TrimRight(p, "'\"")
				if allowed[p] || strings.HasPrefix(p, "/bin/") {
					continue
				}
				if _, ok := artifactFor(p); !ok {
					t.Errorf("%s uses the path %s, which `bobres uninstall` does not know (hostArtifacts, cmd/bobres/uninstall_inventory.go)", f, p)
				}
			}
		}
	}
}

func TestUnitNamesTheCodeUsesMatchTheDiscovery(t *testing.T) {
	for _, n := range []string{updaterPathUnit, updaterServiceUnit, "bobres-monitor.timer", "bobres-monitor.service", "bobres.service", "bobres@x.service", "bobres-a-b@c.timer"} {
		if !unitNameRe.MatchString(n) {
			t.Errorf("%s is not found by the uninstall's unit discovery", n)
		}
	}
	for _, n := range []string{"docker.service", "ssh.service", "bobresque.service", "xbobres-x.service", "bobres_x.service", "bobres-x.conf", "bobres-.service/../ssh.service", "bobres-x.service.d"} {
		if unitNameRe.MatchString(n) {
			t.Errorf("%s must not be taken for a unit of BOBRES's", n)
		}
	}
	for _, p := range []string{"/etc/systemd/system/ssh.service", "/etc/systemd/system/../ssh.service", "/etc/systemd/system/bobres-x.service/../../ssh.service",
		"/etc/systemd/system/multi-user.target.wants/ssh.service", "/etc/systemd/system/ssh.service.d", "/etc/systemd/system", "/etc/systemd/system/a/b/bobres-x.service"} {
		if ownUnitPath(p) {
			t.Errorf("ownUnitPath(%q) = true", p)
		}
	}
	for _, p := range []string{"/etc/systemd/system/bobres-x.service", "/etc/systemd/system/bobres-x.service.d", "/etc/systemd/system/timers.target.wants/bobres-x.timer",
		"/run/systemd/system/bobres-x.service"} {
		if !ownUnitPath(p) {
			t.Errorf("ownUnitPath(%q) = false", p)
		}
	}
}

// The hardening writes only what the inventory lists, and records every file, package and rule it
// adds in the receipt: a step that changed the server without a record would be one the uninstall
// could not undo precisely.
func TestHardeningOnlyChangesWhatTheInventoryAndTheReceiptKnow(t *testing.T) {
	for _, opt := range []hardenOptions{{auto: true}, {yes: true}} {
		f := freshServer()
		f.packages["dpkg"] = true
		h, _, _ := autoHardener(f)
		h.rec = newReceiptStore(t.TempDir())
		if code := h.apply(context.Background(), opt); code != 0 {
			t.Fatalf("%+v: exit %d", opt, code)
		}
		r, err := h.rec.load()
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range f.writes {
			if _, ok := artifactFor(w); !ok {
				t.Errorf("%+v wrote %s, which the inventory does not list", opt, w)
			}
			if w == "/etc/fstab" {
				if r.Swap == nil || !r.Swap.FstabAdded {
					t.Errorf("%+v changed /etc/fstab without a swap record in the receipt", opt)
				}
				continue
			}
			if _, ok := r.file(w); !ok {
				t.Errorf("%+v wrote %s without recording it in the receipt", opt, w)
			}
		}
		for _, c := range f.changes() {
			if strings.HasPrefix(c, "ufw show ") { // a read
				continue
			}
			known := false
			for _, p := range knownCommandPrefixes {
				if strings.HasPrefix(c, p) {
					known = true
				}
			}
			if !known {
				t.Errorf("%+v ran %q, which is not among knownCommandPrefixes (cmd/bobres/uninstall_inventory.go): the uninstall must know how to undo it", opt, c)
			}
			if rest, ok := strings.CutPrefix(c, "apt-get install -y -q "); ok {
				for _, p := range strings.Fields(rest) {
					if !packageRecorded(r, p) {
						t.Errorf("%+v installed %s without recording it in the receipt", opt, p)
					}
				}
			}
			if strings.HasPrefix(c, "ufw allow ") {
				if len(r.Firewall.Rules) == 0 {
					t.Errorf("%+v added a ufw rule with no rule in the receipt: %s", opt, c)
				}
			}
		}
		if r.Firewall == nil || !r.Firewall.EnabledByBobres || r.Firewall.WasActive {
			t.Errorf("%+v: the receipt does not say BOBRES turned ufw on: %+v", opt, r.Firewall)
		}
		if r.Swap == nil || r.Swap.Path != swapFile || r.Swap.SizeMB == 0 {
			t.Errorf("%+v: no swap record: %+v", opt, r.Swap)
		}
	}
}

func packageRecorded(r *receipt, pkg string) bool {
	for _, g := range r.Packages {
		for _, p := range g.Packages {
			if p == pkg {
				return true
			}
		}
	}
	return false
}

// The inventory and docs/06-installer.md say the same things.
func TestInventoryIsDocumented(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "06-installer.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	i := strings.Index(doc, "## What installing BOBRES changes on a server")
	if i < 0 {
		t.Fatal("docs/06-installer.md has no section \"What installing BOBRES changes on a server\"")
	}
	section := doc[i:]
	if j := strings.Index(section[3:], "\n## "); j >= 0 {
		section = section[:j+3]
	}
	for _, a := range hostArtifacts {
		if a.Class == classRead || a.Class == classRuntime {
			continue
		}
		if !strings.Contains(section, "`"+a.ID+"`") {
			t.Errorf("the artifact %q is not in the inventory section of docs/06-installer.md (name it as `%s`)", a.ID, a.ID)
		}
		for _, p := range a.Paths {
			needle := strings.TrimSuffix(p, "/**")
			if k := strings.IndexAny(needle, "*?["); k >= 0 {
				needle = needle[:k]
			}
			if strings.Contains(needle, ":") && !strings.HasPrefix(needle, "/") {
				continue // docker:..., ufw:..., pkg:...
			}
			if !strings.Contains(section, needle) {
				t.Errorf("the path %s of %q is not in the inventory section of docs/06-installer.md", needle, a.ID)
			}
		}
	}
}

// install.sh writes down what it installs, in the form the Go side reads.
func TestInstallShRecordsWhatItInstalls(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	state := t.TempDir()
	before := filepath.Join(state, "before.txt")
	after := filepath.Join(state, "after.txt")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(before, []byte("f /etc/apt/sources.list.d/ubuntu.sources\np bash\np curl\n"), 0o600))
	must(os.WriteFile(after, []byte("f /etc/apt/keyrings/docker.asc\nf /etc/apt/sources.list.d/docker.list\nf /etc/apt/sources.list.d/ubuntu.sources\n"+
		"g docker\np bash\np containerd.io\np curl\np docker-ce\np git\n"), 0o600))
	script := `BOBRES_INSTALL_SOURCE_ONLY=1; . "$1"; record_installed docker "$2" "$3" "$4"`
	cmd := exec.Command("bash", "-c", script, "bash", filepath.Join(repoRoot(t), "install", "install.sh"), before, after, state) //nolint:gosec // a fixed script and this test's own files
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("record_installed: %v\n%s", err, out)
	}
	groups := readScriptMarkers(state)
	if len(groups) != 1 {
		t.Fatalf("groups %+v", groups)
	}
	g := groups[0]
	if g.Name != "docker" || strings.Join(g.Packages, ",") != "containerd.io,docker-ce,git" ||
		strings.Join(g.Files, ",") != "/etc/apt/keyrings/docker.asc,/etc/apt/sources.list.d/docker.list" || g.SystemGroup != "docker" {
		t.Fatalf("what install.sh wrote was read as %+v", g)
	}
	if strings.Join(g.Requested, ",") != "containerd.io,docker-ce" {
		t.Fatalf("requested %v", g.Requested)
	}
	// Nothing new: an empty record, and no failure.
	cmd = exec.Command("bash", "-c", script, "bash", filepath.Join(repoRoot(t), "install", "install.sh"), before, before, state) //nolint:gosec // as above
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("record_installed with nothing new: %v\n%s", err, out)
	}
	if got := readScriptMarkers(state); len(got) != 0 {
		t.Fatalf("a record with no package was kept: %+v", got)
	}
}
