package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// note is one outcome the hardener told the install.
type note struct {
	kind        markKind
	label, text string
}

func autoHardener(f *fakeSystem) (*hardener, *[]note, func() string) {
	h, out, _ := testHardener(f, "") // no input at all: a question would read EOF
	var notes []note
	h.notify = func(k markKind, label, text string) { notes = append(notes, note{k, label, text}) }
	return h, &notes, out.String
}

// On a fresh server the install's hardening does everything by itself: the
// firewall (SSH, 80, 443, 443/udp), fail2ban, security updates and swap, one
// outcome each, and not one question.
func TestAutoHardeningDoesEverythingOnAFreshServer(t *testing.T) {
	f := freshServer()
	h, notes, out := autoHardener(f)
	if code := h.apply(context.Background(), hardenOptions{auto: true}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out())
	}
	if len(*notes) != 4 {
		t.Fatalf("outcomes: %+v", *notes)
	}
	for i, label := range []string{"Firewall (ufw)", "SSH guard (fail2ban)", "Security updates", "Swap file"} {
		if n := (*notes)[i]; n.label != label || n.kind != markOK {
			t.Errorf("outcome %d: %+v, want OK for %q", i, n, label)
		}
	}
	if !f.ufwActive || f.index("ufw allow 22/tcp") < 0 || f.index("ufw allow 80/tcp") < 0 || f.index("ufw allow 443/tcp") < 0 ||
		f.index("ufw allow 443/udp") < 0 {
		t.Fatalf("firewall: active=%v cmds %v", f.ufwActive, f.cmds)
	}
	if f.index("ufw allow 22/tcp") > f.index("ufw --force enable") {
		t.Errorf("the SSH rule must come before the firewall is turned on: %v", f.cmds)
	}
	if _, swap := f.files[swapFile]; !f.fail2banActive || !f.packages["unattended-upgrades"] || !swap {
		t.Errorf("fail2ban %v, packages %v, swap file %v", f.fail2banActive, f.packages, swap)
	}
	if strings.Contains(out(), "[Y/n]") && !strings.Contains(out(), "automatic") {
		t.Errorf("a question was printed:\n%s", out())
	}
}

// Where it is not safe the firewall is skipped with a warning and the command
// to run, and the other steps still run: the SSH port not found, 3x-ui on this
// server, another public service.
func TestAutoHardeningSkipsTheFirewallWhenItIsNotSafe(t *testing.T) {
	for name, tune := range map[string]func(*fakeSystem){
		"ssh port not detected": func(f *fakeSystem) { f.sshdT, f.ss = "", "" },
		"3x-ui on this server":  func(f *fakeSystem) { f.ss = xuiSS },
		"unknown public service": func(f *fakeSystem) {
			f.ss = plainSS + "tcp   LISTEN 0      4096         0.0.0.0:3306       0.0.0.0:*    users:((\"mysqld\",pid=900,fd=21))\n"
		},
	} {
		f := freshServer()
		tune(f)
		h, notes, out := autoHardener(f)
		code := h.apply(context.Background(), hardenOptions{auto: true})
		if code != 0 || f.ufwActive || f.index("ufw allow") >= 0 || f.index("ufw --force") >= 0 {
			t.Fatalf("%s: exit %d, ufw active %v, cmds %v\n%s", name, code, f.ufwActive, f.cmds, out())
		}
		fw := (*notes)[0]
		if fw.kind != markWarn || !strings.Contains(fw.text, "sudo bobres harden") || !strings.HasPrefix(fw.text, "skipped") {
			t.Errorf("%s: firewall outcome %+v", name, fw)
		}
		if len(*notes) != 4 || (*notes)[1].kind != markOK {
			t.Errorf("%s: the other steps did not run: %+v", name, *notes)
		}
	}
}

// A ufw that is already on only gets the missing rules, without asking.
func TestAutoHardeningAddsMissingRulesToAFirewallThatIsOn(t *testing.T) {
	f := freshServer()
	f.ufwActive, f.ufwRules, f.packages["ufw"] = true, []string{"22/tcp", "80/tcp"}, true
	h, notes, out := autoHardener(f)
	if code := h.apply(context.Background(), hardenOptions{auto: true}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out())
	}
	if f.index("ufw allow 443/tcp") < 0 || f.index("ufw --force enable") >= 0 || (*notes)[0].kind != markOK {
		t.Fatalf("cmds %v notes %+v", f.cmds, *notes)
	}
}

// Without root, or on another system, it says so as one warning and changes nothing.
func TestAutoHardeningNeedsRoot(t *testing.T) {
	f := freshServer()
	f.root = false
	h, notes, _ := autoHardener(f)
	if code := h.apply(context.Background(), hardenOptions{auto: true}); code != 1 || len(f.changes()) != 0 ||
		len(*notes) != 1 || (*notes)[0].kind != markWarn {
		t.Fatalf("exit %d, changes %v, notes %+v", code, f.changes(), *notes)
	}
}

// The owner's rule: neither the hardening nor the installer ever changes sshd's
// settings, SSH password login, root login, or restarts sshd. Run on a fresh
// server, in every mode, no file written and no command touches them.
func TestHardeningNeverTouchesSSHSettings(t *testing.T) {
	for _, opt := range []hardenOptions{{auto: true}, {yes: true}} {
		f := freshServer()
		h, _, _ := autoHardener(f)
		h.apply(context.Background(), opt)
		for _, w := range f.writes {
			if strings.Contains(w, "sshd_config") || strings.HasPrefix(w, "/etc/ssh/") {
				t.Errorf("%+v wrote %s", opt, w)
			}
		}
		for _, c := range f.cmds {
			l := strings.ToLower(c)
			if strings.Contains(l, "sshd_config") || strings.Contains(l, "passwordauthentication") || strings.Contains(l, "permitrootlogin") ||
				regexp.MustCompile(`(systemctl|service)\s+\S*\s*(restart|reload|stop|disable|mask)\s+(ssh|sshd)\b|(service)\s+(ssh|sshd)\s`).MatchString(l) {
				t.Errorf("%+v ran %q", opt, c)
			}
		}
	}
}

// And in the source: nothing in the installer or the hardening names the
// settings it must never change, nor writes sshd_config, nor restarts sshd.
// (Reading sshd_config for the SSH port is allowed.)
func TestInstallerSourceNeverChangesSSHSettings(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "..", "install", "install.sh"))
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`(?i)PasswordAuthentication`),
		regexp.MustCompile(`(?i)PermitRootLogin`),
		regexp.MustCompile(`(?i)(writeFile|writeAtomic\w*|WriteFile|os\.Create|OpenFile|sed -i|tee)[^\n]*sshd_config`),
		regexp.MustCompile(`(?i)(>|>>)\s*\S*sshd_config`),
		regexp.MustCompile(`(?i)(systemctl|service)\s+(restart|reload|stop|disable|mask)\s+(ssh|sshd)(\.service)?\b`),
		regexp.MustCompile(`(?i)service\s+(ssh|sshd)\s+(restart|reload|stop)`),
		regexp.MustCompile(`(?i)"(restart|reload)",\s*"(ssh|sshd)(\.service)?"`),
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name) //nolint:gosec // our own source files
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range forbidden {
			if m := re.Find(b); m != nil {
				t.Errorf("%s contains %q: the installer must never change SSH settings or restart sshd", name, m)
			}
		}
	}
}
