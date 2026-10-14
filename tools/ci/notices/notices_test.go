// Package notices checks THIRD_PARTY_NOTICES.md against what ships: a
// dependency that changes without the notices changing fails here, so the
// file a lawyer reviews is the one the release carries.
package notices

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func notices(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Every Go module linked into a release binary is listed with its version.
func TestEveryShippedGoModuleIsListed(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go tool is not on PATH")
	}
	cmd := exec.Command("go", "list", "-deps", "-f",
		"{{if .Module}}{{if not .Module.Main}}{{.Module.Path}} {{.Module.Version}}{{end}}{{end}}",
		"./cmd/bobres", "./cmd/bot", "./cmd/core", "./cmd/payments", "./cmd/provisioner", "./cmd/backup")
	cmd.Dir = filepath.Join("..", "..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list: %v", err) // no module cache (offline): CI has one
	}
	text := notices(t)
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path, ver, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || seen[line] {
			continue
		}
		seen[line] = true
		if !strings.Contains(text, "| "+path+" | "+ver+" |") {
			t.Errorf("THIRD_PARTY_NOTICES.md does not list %s %s (a dependency changed: update the file, see its last section)", path, ver)
		}
	}
	if len(seen) < 10 {
		t.Errorf("only %d modules found: did go list change?", len(seen))
	}
}

// Every direct dashboard dependency is listed at its locked version.
func TestEveryDashboardDependencyIsListed(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "web", "dashboard")
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	for file, into := range map[string]any{"package.json": &pkg, "package-lock.json": &lock} {
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	text := notices(t)
	if len(pkg.Dependencies) == 0 {
		t.Fatal("no dependencies in package.json")
	}
	for name := range pkg.Dependencies {
		ver := lock.Packages["node_modules/"+name].Version
		if ver == "" || !strings.Contains(text, "| "+name+" | "+ver+" |") {
			t.Errorf("THIRD_PARTY_NOTICES.md does not list %s %s (a dashboard dependency changed: update the file)", name, ver)
		}
	}
}

// The file says it is a draft until a lawyer has reviewed it.
func TestNoticesCarryTheDraftBanner(t *testing.T) {
	if !strings.Contains(notices(t), "DRAFT") {
		t.Error("THIRD_PARTY_NOTICES.md lost its draft banner")
	}
}
