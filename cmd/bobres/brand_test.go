package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func envMap(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// A buffer is not a terminal; BOBRES_FORCE_TTY=1 makes it look like one.
func TestDetectUI(t *testing.T) {
	tty := []string{"BOBRES_FORCE_TTY", "1", "TERM", "xterm-256color", "LANG", "en_US.UTF-8", "COLUMNS", "100"}
	with := func(extra ...string) termUI {
		return detectUI(&bytes.Buffer{}, envMap(append(append([]string{}, tty...), extra...)...))
	}
	if u := detectUI(&bytes.Buffer{}, envMap("TERM", "xterm-256color")); u.fancy || u.tty || u.unicode || u.color != colorNone {
		t.Errorf("not a terminal: %+v", u)
	}
	if u := with(); !u.fancy || !u.tty || !u.unicode || u.color != color256 || u.width != 100 {
		t.Errorf("a terminal: %+v", u)
	}
	for name, u := range map[string]termUI{
		"NO_COLOR":      with("NO_COLOR", "1"),
		"TERM=dumb":     with("TERM", "dumb"),
		"TERM unset":    with("TERM", ""),
		"BOBRES_PLAIN":  with("BOBRES_PLAIN", "1"),
		"59 columns":    with("COLUMNS", "59"),
		"plain by name": with("BOBRES_PLAIN", "true"),
	} {
		if u.fancy || u.unicode || u.color != colorNone || !u.tty {
			t.Errorf("%s must be plain on a terminal: %+v", name, u)
		}
	}
	uiOpts = uiOptions{plain: true}
	if u := with(); u.fancy {
		t.Errorf("--plain: %+v", u)
	}
	uiOpts = uiOptions{}
	// the locale: the first of LC_ALL, LC_CTYPE, LANG that is set decides
	for _, c := range []struct {
		kv   []string
		utf8 bool
	}{
		{[]string{"LANG", "en_US.UTF-8"}, true}, {[]string{"LANG", "en_US.utf8"}, true}, {[]string{"LANG", "C"}, false},
		{[]string{"LANG", "en_US.UTF-8", "LC_ALL", "C"}, false}, {[]string{"LANG", "C", "LC_CTYPE", "fa_IR.UTF-8"}, true}, {nil, false},
	} {
		if got := utf8Locale(envMap(c.kv...)); got != c.utf8 {
			t.Errorf("utf8Locale(%v) = %v", c.kv, got)
		}
	}
	if u := detectUI(&bytes.Buffer{}, envMap("BOBRES_FORCE_TTY", "1", "TERM", "xterm", "LANG", "C", "COLUMNS", "100")); u.unicode || u.color != color8 || !u.fancy {
		t.Errorf("an old terminal in the C locale: %+v", u)
	}
	if u := detectUI(&bytes.Buffer{}, envMap("BOBRES_FORCE_TTY", "1", "TERM", "xterm-256color", "COLORTERM", "truecolor", "COLUMNS", "100")); u.color != colorTrue {
		t.Errorf("truecolor: %+v", u)
	}
}

func TestStripUIFlags(t *testing.T) {
	args, o := stripUIFlags([]string{"install", "--plain", "--domain", "x", "--no-banner"})
	if strings.Join(args, " ") != "install --domain x" || !o.plain || !o.noBanner {
		t.Fatalf("%v %+v", args, o)
	}
	args, o = stripUIFlags([]string{"config", "set", "K", "--", "--plain"})
	if strings.Join(args, " ") != "config set K -- --plain" || o.plain {
		t.Fatalf("after --: %v %+v", args, o)
	}
}

var bannerBuild = buildInfo{version: "v0.2.0", commit: "06577d3", built: "2026-10-14"}

func stripSGR(s string) string { return escRe.ReplaceAllString(s, "") }

func TestBannerWidthAndAlphabet(t *testing.T) {
	cases := map[string]termUI{
		"plain":         {width: 80},
		"plain tty":     {tty: true, width: 80},
		"fancy utf-8":   {tty: true, fancy: true, unicode: true, color: color256, width: 100},
		"fancy truecol": {tty: true, fancy: true, unicode: true, color: colorTrue, width: 100},
		"fancy ascii":   {tty: true, fancy: true, color: color8, width: 100},
		"narrow":        {tty: true, width: 62},
	}
	for name, ui := range cases {
		lines := bannerLines(ui, bannerBuild)
		if len(lines) > 15 {
			t.Errorf("%s: %d lines", name, len(lines))
		}
		joined := strings.Join(lines, "\n")
		for _, l := range lines {
			if w := width(stripSGR(l)); w > bannerMaxWidth {
				t.Errorf("%s: a line of %d columns: %q", name, w, l)
			}
		}
		if !ui.fancy && strings.ContainsAny(joined, "\x1b\r") {
			t.Errorf("%s: escape bytes in a plain banner", name)
		}
		if !ui.unicode {
			for _, r := range stripSGR(joined) {
				if r > 126 {
					t.Errorf("%s: %q is not ASCII", name, r)
					break
				}
			}
			if !strings.Contains(joined, "(c) 2026 BOBRES, all rights reserved") {
				t.Errorf("%s: no ASCII copyright line", name)
			}
		}
		for _, want := range []string{brandTagline, brandMadeBy, brandLicense, brandSite, brandIssues, brandSecurity, brandRepo, "v0.2.0"} {
			if !strings.Contains(joined, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
		if name == "narrow" && strings.Contains(joined, "####") {
			t.Errorf("a 62-column terminal got the art")
		}
	}
	if joined := strings.Join(bannerLines(cases["fancy utf-8"], bannerBuild), "\n"); !strings.Contains(joined, brandCopyright) || !strings.Contains(joined, "█") {
		t.Errorf("the UTF-8 banner has the sign and the block letters: %s", joined)
	}
	// a long version still fits next to the art
	long := buildInfo{version: "v10.20.30-rc.12345", commit: "06577d3", built: "2026-10-14"}
	for _, l := range bannerLines(cases["plain"], long) {
		if width(l) > bannerMaxWidth {
			t.Errorf("a long version: %d columns: %q", width(l), l)
		}
	}
	if c := compactLine(cases["plain"], bannerBuild); strings.Contains(c, "©") || !strings.Contains(c, "bobres v0.2.0") || !strings.Contains(c, "(c) 2026 BOBRES") {
		t.Errorf("compact plain line: %q", c)
	}
	if w := width(compactLine(cases["plain"], long)); w > bannerMaxWidth {
		t.Errorf("compact line: %d columns", w)
	}
	// the credit joins the one-line form only where it fits
	if c := compactLine(cases["plain"], bannerBuild); !strings.HasSuffix(c, "|  Made by TECSO-DEV") {
		t.Errorf("compact line without the credit: %q", c)
	}
	if c := compactLine(termUI{width: 60}, bannerBuild); strings.Contains(c, "TECSO") || width(c) > 59 {
		t.Errorf("a 60-column terminal: %q", c)
	}
	if c := compactLine(cases["plain"], buildInfo{version: strings.Repeat("v", 30)}); strings.Contains(c, "TECSO") {
		t.Errorf("a long version leaves the credit out: %q", c)
	}
}

// bashOut runs a bash script and returns its output; the test is skipped without bash.
func bashOut(t *testing.T, script string, env ...string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	return string(out)
}

func installSh(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "install", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The words and links of the banner are one thing in Go and one in install.sh.
func TestInstallShBrandMatchesGo(t *testing.T) {
	out := bashOut(t, `BOBRES_INSTALL_SOURCE_ONLY=1; . `+installSh(t)+`; printf '%s\n' "$BRAND_COPYRIGHT" "$BRAND_MADE_BY" "$BRAND_TAGLINE" "$BRAND_LICENSE" "$BRAND_SITE" "$BRAND_REPO" "$BRAND_ISSUES" "$BRAND_SECURITY"`)
	want := []string{brandCopyright, brandMadeBy, brandTagline, brandLicense, brandSite, brandRepo, brandIssues, brandSecurity}
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("install.sh printed %d values: %q", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("install.sh has %q, brand.go has %q", got[i], want[i])
		}
	}
	if brandCopyright != "© 2026 BOBRES, all rights reserved" || brandMadeBy != "Made by TECSO-DEV" {
		t.Errorf("the copyright and credit lines are %q and %q", brandCopyright, brandMadeBy)
	}
}

// The step count the script assumes for `bobres install`, and the weights, add up.
func TestInstallShStepsMatchGo(t *testing.T) {
	out := bashOut(t, `BOBRES_INSTALL_SOURCE_ONLY=1; . `+installSh(t)+`; w=0; for x in "${STEP_WEIGHTS[@]}"; do w=$((w + x)); done; echo "$CLI_STEPS ${#STEP_KEYS[@]} ${#STEP_NAMES[@]} ${#STEP_WEIGHTS[@]} $w"`)
	var cli, keys, names, weights, sum int
	if _, err := fmt.Sscan(out, &cli, &keys, &names, &weights, &sum); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	if cli != len(installSteps) {
		t.Errorf("install.sh says bobres install has %d steps, installSteps has %d", cli, len(installSteps))
	}
	if keys != names || keys != weights || sum >= 100 || sum <= 0 {
		t.Errorf("install.sh steps: %d keys, %d names, %d weights, total weight %d", keys, names, weights, sum)
	}
	for _, defs := range [][]stepDef{installSteps, updateSteps} {
		total := 0
		for _, d := range defs {
			total += d.weight
		}
		if total != 100 {
			t.Errorf("the weights of %v add up to %d, not 100", defs[0].key, total)
		}
	}
}

// The art and the lines of install.sh's banner are the CLI's (the version line aside).
func TestInstallShBannerMatchesGo(t *testing.T) {
	out := bashOut(t, `BOBRES_INSTALL_SOURCE_ONLY=1; . `+installSh(t)+`; ui_init; ui_banner "`+bannerBuild.line()+`"`,
		"BOBRES_FORCE_TTY=", "TERM=xterm", "COLUMNS=100", "LC_ALL=C")
	sh := strings.Split(strings.TrimRight(out, "\n"), "\n")
	goLines := bannerLines(termUI{width: 100}, bannerBuild)
	if len(sh) != len(goLines) {
		t.Fatalf("install.sh draws %d lines, brand.go %d\n%s", len(sh), len(goLines), out)
	}
	for i := range sh {
		if strings.TrimRight(sh[i], " ") != strings.TrimRight(goLines[i], " ") {
			t.Errorf("line %d differs:\n sh: %q\n go: %q", i, sh[i], goLines[i])
		}
	}
}

func TestAboutCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("BOBRES_VERSION=1.2.3\nBOBRES_DOMAIN=panel.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"about", "--dir", dir}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	s := out.String()
	for _, want := range []string{
		"bobres", "(c) 2026 BOBRES, all rights reserved", "Made by TECSO-DEV", brandLicense, "vendor-v1-2026  2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7",
		dir + " (version 1.2.3)", brandSite, brandIssues, brandSecurity, "THIRD_PARTY_NOTICES.md", "LICENSE", "draft",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("about misses %q:\n%s", want, s)
		}
	}
	if strings.ContainsAny(s, "\x1b\r") {
		t.Errorf("escape bytes in plain about")
	}
	for _, r := range s {
		if r > 126 {
			t.Fatalf("plain about is not ASCII: %q", r)
		}
	}
	out.Reset()
	if code := run([]string{"about", "--no-banner", "--dir", t.TempDir()}, &out, &errOut); code != 0 || strings.Contains(out.String(), "####") ||
		!strings.Contains(out.String(), "none found in ") || !strings.Contains(out.String(), "Version") {
		t.Fatalf("--no-banner: %d\n%s", code, out.String())
	}
	if code := run([]string{"about", "extra"}, &out, &errOut); code != 2 {
		t.Fatalf("an argument: %d", code)
	}
	uiOpts = uiOptions{}
}

func forceTTY(t *testing.T, extra ...string) {
	t.Helper()
	kv := append([]string{"BOBRES_FORCE_TTY", "1", "TERM", "xterm-256color", "LANG", "en_US.UTF-8", "COLUMNS", "100"}, extra...)
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("BOBRES_PLAIN", "")
	t.Setenv("BOBRES_NO_BANNER", "")
}

// version and help: plain and exactly as before off a terminal; the one-line
// banner on a terminal; nothing extra with --no-banner or BOBRES_NO_BANNER.
func TestVersionAndHelpBanner(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 || out.String() != "bobres dev\n" {
		t.Fatalf("plain version: %d %q", code, out.String())
	}
	out.Reset()
	run([]string{"help"}, &out, &errOut)
	if strings.ContainsAny(out.String(), "\x1b") || strings.Contains(out.String(), "(c) 2026") || !strings.Contains(out.String(), "  about ") {
		t.Fatalf("plain help:\n%s", out.String())
	}

	forceTTY(t)
	for _, args := range [][]string{{"version"}, {"--version"}} {
		out.Reset()
		if code := run(args, &out, &errOut); code != 0 {
			t.Fatalf("%v: %d", args, code)
		}
		if got := stripSGR(out.String()); got != "bobres dev  ·  © 2026 BOBRES, all rights reserved  ·  Made by TECSO-DEV\n" || !strings.Contains(out.String(), "\x1b[") {
			t.Fatalf("%v on a terminal: %q", args, out.String())
		}
	}
	out.Reset()
	run([]string{"help"}, &out, &errOut)
	if first := strings.SplitN(stripSGR(out.String()), "\n", 2)[0]; !strings.HasPrefix(first, "bobres dev") || !strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("help on a terminal:\n%s", out.String())
	}
	out.Reset()
	run([]string{"version", "--no-banner"}, &out, &errOut)
	if out.String() != "bobres dev\n" {
		t.Fatalf("--no-banner: %q", out.String())
	}
	t.Setenv("BOBRES_NO_BANNER", "1")
	out.Reset()
	run([]string{"version"}, &out, &errOut)
	if out.String() != "bobres dev\n" {
		t.Fatalf("BOBRES_NO_BANNER: %q", out.String())
	}
	t.Setenv("BOBRES_NO_BANNER", "")
	out.Reset()
	run([]string{"version", "--plain"}, &out, &errOut)
	if strings.ContainsAny(out.String(), "\x1b") || strings.Contains(out.String(), "©") {
		t.Fatalf("--plain: %q", out.String())
	}
	uiOpts = uiOptions{}
}

// Commands whose output is parsed or pasted never get a banner or an escape
// byte, whatever the terminal looks like (and --plain / --no-banner change
// nothing for them).
func TestParsedCommandsHaveNoBannerOrEscapes(t *testing.T) {
	forceTTY(t)
	dir := fullInstall(t, true)
	o, out, errOut := dbOps("", &fakeDB{})
	if code := o.configCmd([]string{"get", "bobres_domain", "--dir", dir}); code != 0 || out.String() != "panel.example.com\n" {
		t.Fatalf("config get: %d %q %s", code, out.String(), errOut)
	}
	f := &fakeOps{}
	o2, out2, _ := testOps("", f)
	o2.stream = func(_ context.Context, _ string, _ ...string) error {
		out2.WriteString("https://panel.example.com/login?t=abc\n")
		return nil
	}
	if code := o2.adminCmd([]string{"link", "--dir", installDir(t)}); code != 0 || out2.String() != "https://panel.example.com/login?t=abc\n" {
		t.Fatalf("admin link: %d %q", code, out2.String())
	}
	var rout, rerr bytes.Buffer
	if code := run([]string{"version", "--images"}, &rout, &rerr); strings.ContainsAny(rout.String(), "\x1b") || strings.Contains(rout.String(), "####") {
		t.Fatalf("version --images (%d): %q", code, rout.String())
	}
	if code := run([]string{"config", "get", "BOBRES_DOMAIN", "--dir", dir, "--plain", "--no-banner"}, &rout, &rerr); code != 0 ||
		strings.ContainsAny(rout.String(), "\x1b") || strings.Contains(rout.String(), "####") || strings.Contains(rout.String(), "2026 BOBRES") {
		t.Fatalf("config get through run: %d %q %s", code, rout.String(), rerr.String())
	}
	uiOpts = uiOptions{}
}

func TestStatusAndDoctorPlainAreUnchangedAndFancyHasMarks(t *testing.T) {
	f := &fakeOps{ps: healthyPS}
	dir := installDir(t)
	o, out, _ := testOps("", f)
	if code := o.statusCmd([]string{"--dir", dir}); code != 0 || !strings.Contains(out.String(), "\nSERVICE       STATE        HEALTH\n") ||
		!strings.Contains(out.String(), "core          running      healthy\n") || strings.ContainsAny(out.String(), "\x1b") {
		t.Fatalf("plain status:\n%s", out.String())
	}
	forceTTY(t)
	o, out, _ = testOps("", f)
	if code := o.statusCmd([]string{"--dir", dir}); code != 0 || !strings.Contains(out.String(), "\x1b[") ||
		!strings.Contains(stripSGR(out.String()), "✔ core          running      healthy") {
		t.Fatalf("fancy status:\n%s", out.String())
	}
}

func TestMarksAndUsageColors(t *testing.T) {
	plain := termUI{width: 80}
	fancyU := termUI{tty: true, fancy: true, unicode: true, color: color256, width: 100}
	fancyA := termUI{tty: true, fancy: true, color: color8, width: 100}
	for kind, want := range map[markKind][3]string{
		markOK: {"OK", "✔", "+"}, markWarn: {"WARN", "!", "!"}, markFail: {"FAIL", "✗", "x"}, markSkip: {"SKIP", "-", "-"},
	} {
		if got := plain.mark(kind); got != want[0] {
			t.Errorf("plain mark %d = %q", kind, got)
		}
		if got := stripSGR(fancyU.mark(kind)); got != want[1] {
			t.Errorf("unicode mark %d = %q", kind, got)
		}
		if got := stripSGR(fancyA.mark(kind)); got != want[2] {
			t.Errorf("ascii mark %d = %q", kind, got)
		}
	}
	if paintUsage(plain, usageText) != usageText {
		t.Error("the plain help text changed")
	}
	if got := paintUsage(fancyU, usageText); stripSGR(got) != usageText || !strings.Contains(got, "\x1b[") {
		t.Error("the colored help text must be the plain one with colors")
	}
}
