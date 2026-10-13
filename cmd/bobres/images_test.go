package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// testDigest is a low-entropy fake image digest (never a real one).
func testDigest(c string) string { return "sha256:" + strings.Repeat(c, 64) }

// testPins is imageDigests as the release build sets it, with fake digests.
func testPins() string {
	return "core=" + testDigest("a") + ",bot=" + testDigest("b") + ",payments=" + testDigest("c") +
		",provisioner=" + testDigest("d") + ",backup=" + testDigest("e")
}

// withRelease runs the test as the release CLI v (imageDigests pins).
func withRelease(t *testing.T, v, pins string) {
	t.Helper()
	oldV, oldPins := version.Version, imageDigests
	version.Version, imageDigests = v, pins
	t.Cleanup(func() { version.Version, imageDigests = oldV, oldPins })
}

func TestReleaseDigests(t *testing.T) {
	if pins, err := releaseDigests(""); pins != nil || err != nil {
		t.Fatalf("a build without digests: %v %v", pins, err)
	}
	pins, err := releaseDigests(" " + testPins() + " ")
	if err != nil || len(pins) != 5 || pins["core"] != testDigest("a") || pins["backup"] != testDigest("e") {
		t.Fatalf("release digests: %v %v", pins, err)
	}
	for name, bad := range map[string]string{
		"one missing":     strings.TrimSuffix(testPins(), ",backup="+testDigest("e")),
		"twice":           testPins() + ",core=" + testDigest("f"),
		"unknown service": testPins() + ",caddy=" + testDigest("f"),
		"short digest":    strings.Replace(testPins(), testDigest("a"), "sha256:abc", 1),
		"upper case":      strings.Replace(testPins(), testDigest("a"), "sha256:"+strings.Repeat("A", 64), 1),
		"other algorithm": strings.Replace(testPins(), testDigest("a"), "sha512:"+strings.Repeat("a", 64), 1),
		"no service":      testPins() + "," + testDigest("f"),
	} {
		if pins, err := releaseDigests(bad); err == nil || pins != nil {
			t.Errorf("%s accepted: %v", name, pins)
		}
	}
}

// The release CLI runs each of its images by the digest it carries, not by
// the tag, which whoever can push to the registry can move: .env pins them
// and Docker's reading of the installed compose file pulls by digest. Another
// version (--version) has no digests here and runs by tag again.
func TestInstallPinsTheReleaseImages(t *testing.T) {
	withRelease(t, "v1.4.0", testPins())
	dir := t.TempDir()
	ins, _, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
		t.Fatalf("install: %d %s", code, errOut)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	want := map[string]string{"core": "a", "bot": "b", "payments": "c", "provisioner": "d", "backup": "e"}
	for svc, c := range want {
		if got := env[digestKey(svc)]; got != "@"+testDigest(c) {
			t.Errorf("%s: %q, want the release's digest", digestKey(svc), got)
		}
	}
	images := composeImages(t, dir)
	for svc, c := range want {
		if images != nil && images[svc] != imageRepo+svc+":1.4.0@"+testDigest(c) {
			t.Errorf("compose runs %s as %q, not by the release's digest", svc, images[svc])
		}
	}

	// Pinned to another release: by its tag (this CLI has no digests for it).
	ins, _, errOut = testInstaller("", nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start", "--version", "1.3.0"}); code != 0 {
		t.Fatalf("other version: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	images = composeImages(t, dir)
	for _, svc := range imageServices {
		if env[digestKey(svc)] != "" || (images != nil && images[svc] != imageRepo+svc+":1.3.0") {
			t.Errorf("--version 1.3.0: %s %q, compose %q", digestKey(svc), env[digestKey(svc)], images[svc])
		}
	}
	// Back on this CLI's own version: pinned again.
	ins, _, errOut = testInstaller("", nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start", "--version", "1.4.0"}); code != 0 {
		t.Fatalf("own version: %d %s", code, errOut)
	}
	if env, _, _ = readEnv(filepath.Join(dir, ".env")); env[digestKey("core")] != "@"+testDigest("a") {
		t.Errorf("back on 1.4.0: %q", env[digestKey("core")])
	}

	// A development build has none: by tag.
	withRelease(t, "dev", "")
	devDir := t.TempDir()
	ins, _, errOut = testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(devDir), "--no-start")); code != 0 {
		t.Fatalf("dev install: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(devDir, ".env"))
	for _, svc := range imageServices {
		if env[digestKey(svc)] != "" {
			t.Errorf("a development build pins %s: %q", svc, env[digestKey(svc)])
		}
	}

	// A release build with broken digests installs nothing.
	withRelease(t, "v1.4.0", strings.Replace(testPins(), testDigest("a"), "sha256:abc", 1))
	brokenDir := t.TempDir()
	ins, _, errOut = testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(brokenDir), "--no-start")); code != 1 || !strings.Contains(errOut.String(), "image digests are broken") {
		t.Fatalf("broken digests: %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(brokenDir, ".env")); !os.IsNotExist(err) {
		t.Errorf("broken digests still wrote .env: %v", err)
	}
}

// composeImages is each BOBRES service's image as `docker compose config`
// reads the installed files in dir; nil where docker is not installed.
func composeImages(t *testing.T, dir string) map[string]string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	cmd := exec.Command("docker", "compose", "-f", "docker-compose.yml", "--env-file", ".env", "config", "--format", "json")
	cmd.Dir = dir
	// Only the .env file counts, not this process's variables.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "BOBRES_") && !strings.HasPrefix(kv, "COMPOSE_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, got)
	}
	var project struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if err := json.Unmarshal(got, &project); err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	images := map[string]string{}
	for name, s := range project.Services {
		images[name] = s.Image
	}
	return images
}

// Every BOBRES image line takes its digest line, and no other image does.
func TestComposeImagesTakeTheirDigest(t *testing.T) {
	re := regexp.MustCompile(`(?m)^    image: (\S+)$`)
	var bobres []string
	for _, m := range re.FindAllStringSubmatch(string(deploy.ComposeFile), -1) {
		if strings.HasPrefix(m[1], imageRepo) {
			bobres = append(bobres, m[1])
		}
	}
	var want []string
	for _, svc := range imageServices {
		want = append(want, imageRepo+svc+":${BOBRES_VERSION:-latest}${"+digestKey(svc)+":-}")
	}
	slices.Sort(bobres)
	slices.Sort(want)
	if !slices.Equal(bobres, want) {
		t.Fatalf("compose image lines\n%v\nwant\n%v", bobres, want)
	}
}

func TestVersionImages(t *testing.T) {
	withRelease(t, "v1.4.0", testPins())
	var out, errOut bytes.Buffer
	if code := run([]string{"version", "--images"}, &out, &errOut); code != 0 {
		t.Fatalf("release build: %d %s", code, errOut.String())
	}
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 5 ||
		lines[0] != imageRepo+"core:1.4.0@"+testDigest("a") {
		t.Fatalf("release build:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"version"}, &out, &errOut); code != 0 || out.String() != "bobres v1.4.0\n" {
		t.Fatalf("plain version: %d %q", code, out.String())
	}
	for _, pins := range []string{"", "core=" + testDigest("a")} {
		imageDigests = pins
		out.Reset()
		errOut.Reset()
		if code := run([]string{"version", "--images"}, &out, &errOut); code != 1 || out.Len() != 0 {
			t.Errorf("pins %q: %d %q %q", pins, code, out.String(), errOut.String())
		}
	}
}

// workflowJob is the lines of one job of a GitHub workflow file (by its
// two-space indent under jobs:), or nil.
func workflowJob(file, name string) []string {
	var job []string
	in := false
	for _, line := range strings.Split(file, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			in = strings.TrimSpace(line) == name+":"
			continue
		}
		if in {
			job = append(job, line)
		}
	}
	return job
}

// The release is published only once every image is pushed, public and
// signed, and the CLI it publishes pins those images by digest: a release
// whose images failed would be GitHub's latest (install.sh installs it) and
// every store's bobres update would fail on it.
func TestReleaseWorkflowWaitsForTheImages(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	file := string(b)
	images := strings.Join(workflowJob(file, "images"), "\n")
	if m := regexp.MustCompile(`service: \[([^\]]*)\]`).FindStringSubmatch(images); m == nil ||
		strings.ReplaceAll(m[1], " ", "") != strings.Join(imageServices, ",") {
		t.Fatalf("the images job does not build exactly %v", imageServices)
	}
	cli := workflowJob(file, "cli")
	text := strings.Join(cli, "\n")
	if !strings.Contains(text, "gh release create") {
		t.Fatal("the cli job no longer creates the release: check which job does")
	}
	var needs string
	for _, line := range cli {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "needs:"); ok {
			needs = v
		}
	}
	for _, job := range []string{"verify", "images"} {
		if !regexp.MustCompile(`\b` + job + `\b`).MatchString(needs) {
			t.Errorf("the job that creates the release needs %q, not after %s", needs, job)
		}
	}
	for _, want := range []string{
		"for svc in " + strings.Join(imageServices, " ") + "; do", // every image checked anonymously
		"cosign verify", // and signed by this workflow
		"-X main.imageDigests=${IMAGE_DIGESTS}",
		"dist/bobres_linux_amd64 version --images",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the cli job has no %q", want)
		}
	}
	// The digests are recorded before the CLI is built, which is before the release.
	if i, j, k := strings.Index(text, "cosign verify"), strings.Index(text, "go build"), strings.Index(text, "gh release create"); i > j || j > k {
		t.Error("the cli job must check the images, then build the CLI, then create the release")
	}
	if strings.Count(file, "gh release create") != 1 {
		t.Error("only the cli job creates the release")
	}
}
