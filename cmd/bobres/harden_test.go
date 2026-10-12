package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// fakeSystem plays the server for the hardening step: its files, packages
// and services, and every program run, in order. Nothing touches this
// machine.
type fakeSystem struct {
	files          map[string]string
	packages       map[string]bool
	cmds           []string // every program run, as one line
	writes         []string
	removed        []string
	ufwActive      bool
	ufwRules       []string // as ufw status shows them: 22/tcp, or "8081/tcp from 172.16.0.0/12"
	fail2banActive bool
	sshdT          string // sshd -T's output; "" fails
	ss             string
	fail           map[string]error // a command line prefix -> its error
	mem            int
	root           bool
	env            map[string]string
	freeGB         float64
	dockerSubnets  string // docker network inspect's output
}

const procSwapsHeader = "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n"

const plainSS = `udp   UNCONN 0      0      127.0.0.53%lo:53         0.0.0.0:*    users:(("systemd-resolve",pid=511,fd=13))
udp   UNCONN 0      0      0.0.0.0%eth0:68          0.0.0.0:*    users:(("systemd-network",pid=480,fd=22))
tcp   LISTEN 0      4096         0.0.0.0:22         0.0.0.0:*    users:(("sshd",pid=723,fd=3))
tcp   LISTEN 0      4096            [::]:22            [::]:*    users:(("sshd",pid=723,fd=4))
tcp   LISTEN 0      4096         0.0.0.0:443        0.0.0.0:*    users:(("docker-proxy",pid=2201,fd=4))
tcp   LISTEN 0      4096         0.0.0.0:80         0.0.0.0:*    users:(("docker-proxy",pid=2188,fd=4))
`

// xuiSS adds a 3x-ui (its panel and Xray, whose name ss cuts to 15
// characters) and a database open to the world.
const xuiSS = plainSS + `tcp   LISTEN 0      4096               *:2053             *:*    users:(("x-ui",pid=1001,fd=7))
tcp   LISTEN 0      4096               *:8443             *:*    users:(("xray-linux-amd6",pid=1100,fd=3))
udp   UNCONN 0      0                  *:8443             *:*    users:(("xray-linux-amd6",pid=1100,fd=5))
tcp   LISTEN 0      80         127.0.0.1:62789      0.0.0.0:*    users:(("xray-linux-amd6",pid=1100,fd=9))
tcp   LISTEN 0      151          0.0.0.0:3306       0.0.0.0:*    users:(("mysqld",pid=900,fd=21))
`

// freshServer is a new Ubuntu VPS with 1 GB of RAM: nothing hardened yet.
func freshServer() *fakeSystem {
	return &fakeSystem{
		files: map[string]string{
			"/etc/os-release": "PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\nID_LIKE=debian\n",
			"/proc/swaps":     procSwapsHeader,
			"/etc/fstab":      "UUID=abcd / ext4 defaults 0 1\n",
		},
		packages: map[string]bool{},
		sshdT:    "port 22\naddressfamily any\n",
		ss:       plainSS,
		mem:      980, root: true, env: map[string]string{}, freeGB: 30,
	}
}

func (f *fakeSystem) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.cmds = append(f.cmds, line)
	for prefix, err := range f.fail {
		if strings.HasPrefix(line, prefix) {
			return []byte("failed: " + line), err
		}
	}
	switch {
	case name == "dpkg-query":
		pkg := args[len(args)-1]
		if f.packages[pkg] {
			return []byte("install ok installed"), nil
		}
		return []byte("dpkg-query: no packages found matching " + pkg), errors.New("exit status 1")
	case name == "apt-get" && args[0] == "install":
		for _, p := range args[3:] {
			f.packages[p] = true
		}
	case name == "sshd":
		if f.sshdT == "" {
			return []byte("sshd: no hostkeys available"), errors.New("exit status 1")
		}
		return []byte(f.sshdT), nil
	case name == "ss":
		return []byte(f.ss), nil
	case line == "ufw status":
		if !f.ufwActive {
			return []byte("Status: inactive\n"), nil
		}
		var b strings.Builder
		b.WriteString("Status: active\n\nTo                         Action      From\n--                         ------      ----\n")
		for _, r := range f.ufwRules {
			to, from, ok := strings.Cut(r, " from ")
			if !ok {
				from = "Anywhere"
			}
			fmt.Fprintf(&b, "%-26s ALLOW       %s\n", to, from)
		}
		return []byte(b.String()), nil
	case name == "ufw" && args[0] == "allow" && args[1] == "from": // from X to any port N proto P
		f.ufwRules = append(f.ufwRules, args[6]+"/"+args[8]+" from "+args[2])
	case name == "ufw" && args[0] == "allow":
		f.ufwRules = append(f.ufwRules, args[1])
	case name == "docker":
		return []byte(f.dockerSubnets), nil
	case line == "ufw --force enable":
		f.ufwActive = true
	case line == "systemctl is-active fail2ban":
		if f.fail2banActive {
			return []byte("active\n"), nil
		}
		return []byte("inactive\n"), errors.New("exit status 3")
	case line == "systemctl restart fail2ban":
		f.fail2banActive = true
	case name == "fallocate":
		f.files[swapFile] = ""
	case name == "swapon":
		f.files["/proc/swaps"] = procSwapsHeader + "/swapfile                               file\t\t2097148\t\t0\t\t-2\n"
	}
	return nil, nil
}

func testHardener(f *fakeSystem, stdin string) (*hardener, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &hardener{
		in: bufio.NewReader(strings.NewReader(stdin)), out: &out, errOut: &errOut,
		run: f.run,
		readFile: func(p string) ([]byte, error) {
			if v, ok := f.files[p]; ok {
				return []byte(v), nil
			}
			return nil, os.ErrNotExist
		},
		writeFile: func(p string, b []byte, _ os.FileMode) error {
			f.files[p] = string(b)
			f.writes = append(f.writes, p)
			return nil
		},
		remove: func(p string) error {
			delete(f.files, p)
			f.removed = append(f.removed, p)
			return nil
		},
		exists: func(p string) bool { _, ok := f.files[p]; return ok },
		lookPath: func(name string) (string, error) {
			if f.packages[name] {
				return "/usr/sbin/" + name, nil
			}
			return "", exec.ErrNotFound
		},
		euid: func() int {
			if f.root {
				return 0
			}
			return 1000
		},
		memMB:  func() int { return f.mem },
		freeGB: func(string) float64 { return f.freeGB },
		getenv: func(k string) string { return f.env[k] },
		goos:   "linux",
	}, &out, &errOut
}

// index is the position of the first command starting with prefix (-1: none).
func (f *fakeSystem) index(prefix string) int {
	for i, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func (f *fakeSystem) count(prefix string) int {
	n := 0
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// changes are the commands that change the system (everything but reads).
func (f *fakeSystem) changes() []string {
	var out []string
	for _, c := range f.cmds {
		switch {
		case strings.HasPrefix(c, "dpkg-query"), strings.HasPrefix(c, "sshd -T"), strings.HasPrefix(c, "ss "),
			strings.HasPrefix(c, "docker network inspect"), c == "ufw status", c == "systemctl is-active fail2ban":
		default:
			out = append(out, c)
		}
	}
	return out
}

func TestHardenFreshServer(t *testing.T) {
	f := freshServer()
	h, out, errOut := testHardener(f, "\n\n\n\n") // Enter: each step's default (yes)
	if code := h.apply(context.Background(), hardenOptions{}); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	// The firewall: the package, the SSH port before anything is refused, then on.
	for _, want := range []string{"apt-get update -q", "apt-get install -y -q ufw", "ufw allow 22/tcp comment bobres SSH",
		"ufw allow 80/tcp comment bobres HTTP", "ufw allow 443/tcp comment bobres HTTPS", "ufw allow 443/udp comment bobres HTTP/3",
		"ufw default deny incoming", "ufw default allow outgoing", "ufw --force enable"} {
		if f.index(want) < 0 {
			t.Errorf("missing %q in %q", want, f.cmds)
		}
	}
	if f.index("ufw allow 22/tcp") > f.index("ufw default deny incoming") || f.index("ufw default deny incoming") > f.index("ufw --force enable") {
		t.Fatalf("SSH must be allowed before incoming is refused: %q", f.cmds)
	}
	if f.count("apt-get update") != 1 {
		t.Errorf("the package lists are refreshed once: %q", f.cmds)
	}
	// fail2ban reads the journal: no /var/log/auth.log here.
	jail := f.files[fail2banJail]
	for _, want := range []string{"[sshd]", "enabled  = true", "port     = 22\n", "backend  = systemd", "maxretry = 5", "bantime  = 1h"} {
		if !strings.Contains(jail, want) {
			t.Errorf("jail missing %q:\n%s", want, jail)
		}
	}
	if f.index("apt-get install -y -q fail2ban python3-systemd") < 0 || f.index("systemctl enable fail2ban") < 0 || f.index("systemctl restart fail2ban") < 0 {
		t.Errorf("fail2ban: %q", f.cmds)
	}
	// Security updates only, from Ubuntu's security pockets.
	if f.files[autoUpgradesConf] != autoUpgrades || !strings.Contains(f.files[securityOnlyConf], "archive=${distro_codename}-security,label=Ubuntu") ||
		!strings.Contains(f.files[securityOnlyConf], "#clear Unattended-Upgrade::Origins-Pattern;") || strings.Contains(f.files[securityOnlyConf], "Debian") {
		t.Errorf("unattended-upgrades:\n%s\n%s", f.files[autoUpgradesConf], f.files[securityOnlyConf])
	}
	// 980 MB of RAM: a 2 GB swap file, also after a reboot.
	for _, want := range []string{"fallocate -l 2048M /swapfile", "chmod 600 /swapfile", "mkswap /swapfile", "swapon /swapfile"} {
		if f.index(want) < 0 {
			t.Errorf("missing %q in %q", want, f.cmds)
		}
	}
	if f.files["/etc/fstab"] != "UUID=abcd / ext4 defaults 0 1\n/swapfile none swap sw 0 0\n" {
		t.Errorf("fstab: %q", f.files["/etc/fstab"])
	}
	for _, want := range []string{"[1/4] Firewall (ufw)", "SSH listens on port 22", "> ufw --force enable", "Firewall on.", "fail2ban on.",
		"Security updates on.", "Swap on.", "Hardening done."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// Again: everything is in place, nothing is asked or changed.
	before := len(f.cmds)
	h, out, errOut = testHardener(f, "")
	if code := h.apply(context.Background(), hardenOptions{}); code != 0 {
		t.Fatalf("second run: %d %s", code, errOut)
	}
	f.cmds = f.cmds[before:]
	if c := f.changes(); len(c) != 0 || len(f.writes) != 4 {
		t.Fatalf("a second run changed %q (writes %q)", c, f.writes)
	}
	if strings.Count(out.String(), "Already on") != 4 || strings.Contains(out.String(), "[Y/n]") {
		t.Fatalf("second run:\n%s", out)
	}
}

// No terminal and no --yes: every question is no, nothing changes.
func TestHardenWithoutAnswersChangesNothing(t *testing.T) {
	f := freshServer()
	h, out, _ := testHardener(f, "")
	if code := h.apply(context.Background(), hardenOptions{}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if c := f.changes(); len(c) != 0 || len(f.writes) != 0 {
		t.Fatalf("changed %q, wrote %q", c, f.writes)
	}
	if strings.Count(out.String(), "No answer: skipped.") != 4 {
		t.Fatalf("output:\n%s", out)
	}
}

// Declined one by one, with an explicit no.
func TestHardenDeclined(t *testing.T) {
	f := freshServer()
	h, _, _ := testHardener(f, "n\nno\nN\nx\n")
	if code := h.apply(context.Background(), hardenOptions{}); code != 0 || len(f.changes()) != 0 {
		t.Fatalf("exit %d, changes %q", code, f.changes())
	}
}

func TestHardenYesOnDebian(t *testing.T) {
	f := freshServer()
	f.files["/etc/os-release"] = "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=debian\n"
	f.files["/var/log/auth.log"] = ""
	f.mem = 1800
	h, out, errOut := testHardener(f, "")
	if code := h.apply(context.Background(), hardenOptions{yes: true}); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if !strings.Contains(f.files[securityOnlyConf], "codename=${distro_codename}-security,label=Debian-Security") || strings.Contains(f.files[securityOnlyConf], "Ubuntu") {
		t.Errorf("Debian origins:\n%s", f.files[securityOnlyConf])
	}
	if !strings.Contains(f.files[fail2banJail], "backend  = auto") || f.index("apt-get install -y -q fail2ban") < 0 ||
		strings.Contains(strings.Join(f.cmds, "\n"), "python3-systemd") {
		t.Errorf("auth.log is there: the default backend: %s %q", f.files[fail2banJail], f.cmds)
	}
	if f.index("fallocate -l 1024M /swapfile") < 0 { // 1.8 GB of RAM: 1 GB
		t.Errorf("swap: %q", f.cmds)
	}
	if strings.Count(out.String(), "yes (--yes)") != 4 {
		t.Errorf("each step still announced:\n%s", out)
	}
}

// The firewall is never turned on without a known SSH port: this session
// would be cut off.
func TestHardenRefusesFirewallWithoutSSHPort(t *testing.T) {
	f := freshServer()
	f.sshdT, f.ss = "", ""
	h, _, errOut := testHardener(f, "")
	if code := h.apply(context.Background(), hardenOptions{yes: true}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "the SSH port could not be detected") || !strings.Contains(errOut.String(), "--ssh-port") {
		t.Fatalf("errors: %s", errOut)
	}
	if f.index("ufw") >= 0 || f.index("apt-get install -y -q ufw") >= 0 {
		t.Fatalf("the firewall was touched: %q", f.cmds)
	}
	// The other steps still ran; fail2ban watches the ssh service port.
	if !strings.Contains(f.files[fail2banJail], "port     = ssh\n") || f.index("swapon") < 0 {
		t.Fatalf("other steps: %q", f.cmds)
	}

	f = freshServer()
	f.sshdT, f.ss = "", ""
	h, _, errOut = testHardener(f, "")
	if code := h.apply(context.Background(), hardenOptions{yes: true, sshPort: 2222}); code != 0 {
		t.Fatalf("with --ssh-port: %d %s", code, errOut)
	}
	if f.index("ufw allow 2222/tcp comment bobres SSH") < 0 || f.index("ufw allow 22/tcp") >= 0 || f.index("ufw --force enable") < 0 {
		t.Fatalf("with --ssh-port: %q", f.cmds)
	}
}

func TestSSHPortDetection(t *testing.T) {
	f := freshServer()
	h, _, _ := testHardener(f, "")
	// sshd -T fails: sshd_config, ss (socket activation lists systemd too) and this session.
	f.sshdT = ""
	f.files["/etc/ssh/sshd_config"] = "#Port 22\nPort 2222\nPermitRootLogin no\n"
	f.env["SSH_CONNECTION"] = "203.0.113.9 51234 198.51.100.4 4422"
	ls := parseListeners(`tcp LISTEN 0 4096 0.0.0.0:2200 0.0.0.0:* users:(("sshd",pid=1,fd=3),("systemd",pid=1,fd=50))`)
	if got := h.sshPorts(context.Background(), ls); !reflect.DeepEqual(got, []int{2200, 2222, 4422}) {
		t.Fatalf("ports %v", got)
	}
	// No Port line: 22.
	f.files["/etc/ssh/sshd_config"] = "PermitRootLogin no\n"
	f.env = map[string]string{}
	if got := h.sshPorts(context.Background(), nil); !reflect.DeepEqual(got, []int{22}) {
		t.Fatalf("default port: %v", got)
	}
	// sshd -T wins over the file.
	f.sshdT = "port 2022\nport 2023\n"
	if got := h.sshPorts(context.Background(), nil); !reflect.DeepEqual(got, []int{2022, 2023}) {
		t.Fatalf("sshd -T: %v", got)
	}
}

// A 3x-ui on the same server keeps its ports, the operator is told about new
// inbounds, and the question defaults to no.
func TestHardenFirewallWith3xui(t *testing.T) {
	f := freshServer()
	f.ss = xuiSS
	h, out, _ := testHardener(f, "\n") // Enter: the default, here no
	h.apply(context.Background(), hardenOptions{})
	if f.index("ufw") >= 0 || !strings.Contains(out.String(), "Turn on the firewall? [y/N]") {
		t.Fatalf("enter must not turn the firewall on with 3x-ui here: %q\n%s", f.cmds, out)
	}
	for _, want := range []string{"2053/tcp (x-ui)", "8443/tcp (xray-linux-amd6)", "8443/udp (xray-linux-amd6)", "a new inbound",
		"would be closed to the outside and to the BOBRES containers: 3306/tcp (mysqld)",
		"for the BOBRES containers only: sudo ufw allow from 172.16.0.0/12 to any port <port> proto tcp"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "62789") || strings.Contains(out.String(), ":68") || strings.Contains(out.String(), "68/udp") {
		t.Errorf("loopback and system ports are not listed:\n%s", out)
	}

	f = freshServer()
	f.ss = xuiSS
	h, _, _ = testHardener(f, "y\n")
	h.apply(context.Background(), hardenOptions{})
	for _, want := range []string{"ufw allow 2053/tcp comment bobres 3x-ui", "ufw allow 8443/tcp comment bobres 3x-ui", "ufw allow 8443/udp comment bobres 3x-ui", "ufw --force enable"} {
		if f.index(want) < 0 {
			t.Errorf("missing %q in %q", want, f.cmds)
		}
	}
	if f.index("ufw allow 3306") >= 0 || f.index("ufw allow 62789") >= 0 || f.index("ufw allow 68") >= 0 {
		t.Fatalf("only 3x-ui's public ports are opened: %q", f.cmds)
	}
}

// An operator's own ufw keeps its defaults: only the missing rules are added.
func TestHardenFirewallAlreadyOn(t *testing.T) {
	f := freshServer()
	f.packages["ufw"], f.ufwActive, f.ufwRules = true, true, []string{"22/tcp", "80,443/tcp"}
	h, out, _ := testHardener(f, "\nn\nn\nn\n")
	h.apply(context.Background(), hardenOptions{})
	if f.index("ufw allow 443/udp") < 0 || f.index("ufw allow 22/tcp") >= 0 || f.index("ufw allow 80/tcp") >= 0 ||
		f.index("ufw default") >= 0 || f.index("ufw --force enable") >= 0 {
		t.Fatalf("commands %q", f.cmds)
	}
	if !strings.Contains(out.String(), "does not allow 443/udp (HTTP/3) yet") {
		t.Fatalf("output:\n%s", out)
	}
}

// An operator's ufw that refuses 3x-ui on purpose (a panel reached through an
// SSH tunnel) stays that way: its ports are listed, never offered, not even
// with --yes.
func TestHardenFirewallAlreadyOnKeeps3xuiClosed(t *testing.T) {
	for _, c := range []struct {
		name  string
		stdin string
		yes   bool
	}{{"Enter", "\nn\nn\nn\n", false}, {"--yes", "", true}} {
		f := freshServer()
		f.ss = xuiSS
		f.packages["ufw"], f.ufwActive, f.ufwRules = true, true, []string{"22/tcp", "80,443/tcp"}
		h, out, _ := testHardener(f, c.stdin)
		h.apply(context.Background(), hardenOptions{yes: c.yes})
		if f.index("ufw allow 2053") >= 0 || f.index("ufw allow 8443") >= 0 || f.index("ufw allow 443/udp comment bobres HTTP/3") < 0 ||
			f.index("ufw default") >= 0 {
			t.Fatalf("%s: commands %q", c.name, f.cmds)
		}
		closed := lineWith(out.String(), "does not allow 3x-ui")
		for _, want := range []string{"2053/tcp (x-ui)", "8443/tcp (xray-linux-amd6)", "8443/udp (xray-linux-amd6)"} {
			if !strings.Contains(closed, want) {
				t.Errorf("%s: %q not listed as closed:\n%s", c.name, want, out)
			}
		}
		if !strings.Contains(out.String(), "does not allow 443/udp (HTTP/3) yet") || strings.Contains(lineWith(out.String(), "yet."), "3x-ui") {
			t.Errorf("%s: only the HTTP/3 rule is offered:\n%s", c.name, out)
		}
	}

	// Everything else allowed: already on, nothing asked; a 3x-ui port the
	// operator allowed is not listed.
	f := freshServer()
	f.ss = xuiSS
	f.packages["ufw"], f.ufwActive, f.ufwRules = true, true, []string{"22/tcp", "80,443/tcp", "443/udp", "8443/tcp"}
	h, out, _ := testHardener(f, "")
	h.apply(context.Background(), hardenOptions{yes: true})
	if f.index("ufw allow") >= 0 || !strings.Contains(out.String(), "Already on, with") {
		t.Fatalf("commands %q\n%s", f.cmds, out)
	}
	if closed := lineWith(out.String(), "does not allow 3x-ui"); !strings.Contains(closed, "2053/tcp (x-ui)") || strings.Contains(closed, "8443/tcp") {
		t.Fatalf("closed: %q\n%s", closed, out)
	}
}

// lineWith is the first line of s that contains sub ("" when none).
func lineWith(s, sub string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}

// botAPISS adds a local Bot API server on the host (its name cut by ss).
const botAPISS = plainSS + `tcp   LISTEN 0      4096         0.0.0.0:8081       0.0.0.0:*    users:(("telegram-bot-ap",pid=1300,fd=5))
`

// A service on this server the containers reach through host.docker.internal
// (.env), such as a local Bot API server, stays open to them only: the
// default deny would cut them off too, and a public rule would expose it.
func TestHardenFirewallHostServices(t *testing.T) {
	const dir = "/opt/bobres"
	const botRule = "ufw allow from 172.16.0.0/12 to any port 8081 proto tcp comment bobres Bot API server"
	withEnv := func(env string) *fakeSystem {
		f := freshServer()
		f.ss = botAPISS
		f.files[dir+"/.env"] = "# BOBRES environment\nBOBRES_DOMAIN=shop.example.com\n" + env
		return f
	}

	// Turned on from off: the Docker-only rule comes before anything is refused.
	f := withEnv("BOBRES_TELEGRAM_API_URL='http://host.docker.internal:8081'\nBOBRES_XUI_URL=https://panel.example.com:2053/p\n")
	h, out, errOut := testHardener(f, "")
	if code := h.apply(context.Background(), hardenOptions{yes: true, dir: dir}); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if i := f.index(botRule); i < 0 || i > f.index("ufw default deny incoming") {
		t.Fatalf("missing %q before the default deny: %q", botRule, f.cmds)
	}
	if f.index("ufw allow 8081") >= 0 || f.index("ufw allow 2053") >= 0 || f.index("ufw allow from 172.16.0.0/12 to any port 2053") >= 0 {
		t.Fatalf("only the host.docker.internal port, never public: %q", f.cmds)
	}
	s := out.String()
	if !strings.Contains(s, "8081/tcp (Bot API server, BOBRES_TELEGRAM_API_URL)") || !strings.Contains(s, "Docker's networks only (172.16.0.0/12)") ||
		strings.Contains(lineWith(s, "would be closed"), "8081") {
		t.Fatalf("output:\n%s", s)
	}
	// Again: already on.
	before := len(f.cmds)
	h, out, _ = testHardener(f, "")
	h.apply(context.Background(), hardenOptions{dir: dir})
	if f.cmds = f.cmds[before:]; f.index("ufw allow") >= 0 || !strings.Contains(out.String(), "Already on, with") {
		t.Fatalf("second run: %q\n%s", f.cmds, out)
	}

	// An operator's ufw without it: offered (Enter: yes), still Docker only.
	f = withEnv("BOBRES_TELEGRAM_API_URL=http://host.docker.internal:8081\n")
	f.packages["ufw"], f.ufwActive, f.ufwRules = true, true, []string{"22/tcp", "80,443/tcp", "443/udp"}
	h, out, _ = testHardener(f, "\nn\nn\nn\n")
	h.apply(context.Background(), hardenOptions{dir: dir})
	if f.index(botRule) < 0 || f.index("ufw allow 8081/tcp") >= 0 || f.index("ufw default") >= 0 ||
		!strings.Contains(out.String(), "does not allow 8081/tcp (Bot API server, BOBRES_TELEGRAM_API_URL) from 172.16.0.0/12 yet") {
		t.Fatalf("commands %q\n%s", f.cmds, out)
	}

	// A custom Docker address pool: the egress network's own subnet too (IPv4 only).
	f = withEnv("BOBRES_TELEGRAM_API_URL=http://host.docker.internal:8081\n")
	f.dockerSubnets = "10.20.0.0/24 172.18.0.0/16 fd00:1::/64 junk \n"
	h, _, _ = testHardener(f, "")
	h.apply(context.Background(), hardenOptions{yes: true, dir: dir})
	if f.index("docker network inspect") < 0 || f.index(botRule) < 0 ||
		f.index("ufw allow from 10.20.0.0/24 to any port 8081 proto tcp comment bobres Bot API server") < 0 ||
		f.count("ufw allow from") != 2 {
		t.Fatalf("commands %q", f.cmds)
	}

	// The 3x-ui panel here, reached through host.docker.internal, on an
	// operator's ufw that refuses it: open to the containers, never public.
	f = withEnv("BOBRES_XUI_URL=http://host.docker.internal:2053/p\n")
	f.ss = xuiSS
	f.packages["ufw"], f.ufwActive, f.ufwRules = true, true, []string{"22/tcp", "80,443/tcp", "443/udp"}
	h, _, _ = testHardener(f, "")
	h.apply(context.Background(), hardenOptions{yes: true, dir: dir})
	if f.index("ufw allow from 172.16.0.0/12 to any port 2053 proto tcp comment bobres 3x-ui panel") < 0 || f.index("ufw allow 2053/tcp") >= 0 {
		t.Fatalf("commands %q", f.cmds)
	}
	// Turned on from off, 3x-ui keeps its public rule: no second one.
	f = withEnv("BOBRES_XUI_URL=http://host.docker.internal:2053/p\n")
	f.ss = xuiSS
	h, _, _ = testHardener(f, "")
	h.apply(context.Background(), hardenOptions{yes: true, dir: dir})
	if f.index("ufw allow 2053/tcp comment bobres 3x-ui") < 0 || f.index("ufw allow from") >= 0 {
		t.Fatalf("commands %q", f.cmds)
	}

	// No .env (or no host.docker.internal in it): no extra rule, Docker not asked.
	for _, f := range []*fakeSystem{freshServer(), withEnv("BOBRES_TELEGRAM_API_URL=https://tg.example.com:8081\n")} {
		f.ss = botAPISS
		h, _, _ = testHardener(f, "")
		h.apply(context.Background(), hardenOptions{yes: true, dir: dir})
		if f.index("ufw allow from") >= 0 || f.index("docker") >= 0 || f.index("ufw --force enable") < 0 {
			t.Fatalf("commands %q", f.cmds)
		}
	}
}

func TestHostDockerPorts(t *testing.T) {
	env := map[string]string{
		"BOBRES_TELEGRAM_API_URL": "http://HOST.docker.internal:8081",
		"BOBRES_XUI_URL":          "https://host.docker.internal/panel", // 443
		"BOBRES_ZARINPAL_PROXY":   "http://host.docker.internal",        // 80
		"BOBRES_XUI_SUB_URL":      "https://sub.example.com:2096/sub/",
		"BOBRES_OTHER":            "http://host.docker.internal:8081", // the same port: once
		"BOBRES_BAD":              "http://host.docker.internal:99999",
	}
	got := hostDockerPorts(env)
	want := []hostPort{{80, "BOBRES_ZARINPAL_PROXY"}, {443, "BOBRES_XUI_URL"}, {8081, "BOBRES_OTHER"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestHardenRefusals(t *testing.T) {
	for name, set := range map[string]func(*fakeSystem, *hardener){
		"macOS":      func(_ *fakeSystem, h *hardener) { h.goos = "darwin" },
		"CentOS":     func(f *fakeSystem, _ *hardener) { f.files["/etc/os-release"] = "ID=\"centos\"\n" },
		"no release": func(f *fakeSystem, _ *hardener) { delete(f.files, "/etc/os-release") },
		"not root":   func(f *fakeSystem, _ *hardener) { f.root = false },
	} {
		f := freshServer()
		h, _, errOut := testHardener(f, "")
		set(f, h)
		if code := h.apply(context.Background(), hardenOptions{yes: true}); code != 1 || len(f.cmds) != 0 || len(f.writes) != 0 {
			t.Errorf("%s: exit %d, ran %q, wrote %q", name, code, f.cmds, f.writes)
		}
		if !strings.Contains(errOut.String(), "Nothing was changed") {
			t.Errorf("%s: %s", name, errOut)
		}
	}
}

func TestHardenStepFailures(t *testing.T) {
	f := freshServer()
	f.fail = map[string]error{"apt-get install -y -q fail2ban": errors.New("exit status 100"), "mkswap": errors.New("exit status 1")}
	h, out, errOut := testHardener(f, "")
	if code := h.apply(context.Background(), hardenOptions{yes: true}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "apt-get install -y -q fail2ban python3-systemd: exit status 100") ||
		!strings.Contains(errOut.String(), "2 hardening step(s) did not finish") {
		t.Fatalf("errors: %s", errOut)
	}
	// The unfinished swap file goes; the steps after a failure still run.
	if !reflect.DeepEqual(f.removed, []string{swapFile}) || f.index("swapon") >= 0 || !strings.Contains(out.String(), "Security updates on.") {
		t.Fatalf("removed %q, commands %q", f.removed, f.cmds)
	}
	if _, ok := f.files[fail2banJail]; ok {
		t.Fatal("the jail was written without fail2ban")
	}
}

func TestHardenSwapEdges(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*fakeSystem)
		want string
	}{
		{"enough RAM", func(f *fakeSystem) { f.mem = 4000 }, "Not needed: 4000 MB of RAM."},
		{"swap on", func(f *fakeSystem) { f.files["/proc/swaps"] = procSwapsHeader + "/dev/vda2 partition 1048572 0 -2\n" }, "Already on (1023 MB)."},
		{"file left", func(f *fakeSystem) { f.files[swapFile] = "" }, "/swapfile exists but is not in use"},
		{"small disk", func(f *fakeSystem) { f.freeGB = 4 }, "4.0 GB free on / is too little"},
		{"unknown RAM", func(f *fakeSystem) { f.mem = 0 }, "memory size could not be read"},
	} {
		f := freshServer()
		c.set(f)
		h, out, _ := testHardener(f, "")
		if err := h.swap(context.Background(), hardenOptions{yes: true}); err != nil || !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: %v %s", c.name, err, out)
		}
		if f.index("fallocate") >= 0 || f.index("swapon") >= 0 {
			t.Errorf("%s: %q", c.name, f.cmds)
		}
	}
	// fallocate refused by the file system: written out with dd instead.
	f := freshServer()
	f.fail = map[string]error{"fallocate": errors.New("exit status 1")}
	h, _, _ := testHardener(f, "")
	if err := h.swap(context.Background(), hardenOptions{yes: true}); err != nil || f.index("dd if=/dev/zero of=/swapfile bs=1M count=2048") < 0 || f.index("swapon /swapfile") < 0 {
		t.Fatalf("dd: %v %q", err, f.cmds)
	}
	// Already in fstab: not added twice.
	f = freshServer()
	f.files["/etc/fstab"] = "/swapfile none swap sw 0 0"
	h, _, _ = testHardener(f, "")
	if err := h.swap(context.Background(), hardenOptions{yes: true}); err != nil || len(f.writes) != 0 {
		t.Fatalf("fstab: %v %q", err, f.writes)
	}
}

func TestHardenReport(t *testing.T) {
	statuses := func(rs []Result) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.Name+"="+string(r.Status))
		}
		return strings.Join(s, " ")
	}
	f := freshServer()
	h, _, _ := testHardener(f, "")
	if got := statuses(h.report(context.Background())); got != "Firewall (ufw)=WARN fail2ban (SSH)=WARN Security updates=WARN Swap=WARN" {
		t.Fatalf("fresh: %s", got)
	}
	h.apply(context.Background(), hardenOptions{yes: true})
	rs := h.report(context.Background())
	if got := statuses(rs); got != "Firewall (ufw)=PASS fail2ban (SSH)=PASS Security updates=PASS Swap=PASS" {
		t.Fatalf("hardened: %s", got)
	}
	if rs[2].Detail != "automatic, security updates only" || rs[3].Detail != "2047 MB" {
		t.Fatalf("details: %+v", rs)
	}
	// The report only reads.
	f.cmds = nil
	h.report(context.Background())
	if c := f.changes(); len(c) != 0 {
		t.Fatalf("the report changed %q", c)
	}
	f.root = false
	if rs := h.report(context.Background()); rs[0].Status != Skip {
		t.Fatalf("ufw status needs root: %+v", rs[0])
	}
	f.mem = 4000
	f.files["/proc/swaps"] = procSwapsHeader
	if rs := h.report(context.Background()); rs[3].Status != Pass || rs[3].Detail != "none needed (4000 MB of RAM)" {
		t.Fatalf("swap: %+v", rs[3])
	}
	h.goos = "darwin"
	if rs := h.report(context.Background()); len(rs) != 1 || rs[0].Status != Skip {
		t.Fatalf("off Linux: %+v", rs)
	}
}

func TestParseListeners(t *testing.T) {
	ls := parseListeners(xuiSS + "garbage line\ntcp LISTEN 0 1 nocolon *:*\n")
	byPort := map[string]listener{}
	for _, l := range ls {
		byPort[fmt.Sprintf("%d/%s", l.port, l.proto)] = l
	}
	if len(ls) != 11 {
		t.Fatalf("%d listeners: %+v", len(ls), ls)
	}
	if !byPort["53/udp"].loopback || !byPort["62789/tcp"].loopback || byPort["22/tcp"].loopback || byPort["2053/tcp"].loopback {
		t.Fatalf("loopback: %+v", byPort)
	}
	if !byPort["8443/udp"].xui() || !byPort["2053/tcp"].xui() || byPort["3306/tcp"].xui() || !byPort["80/tcp"].has("docker-proxy") {
		t.Fatalf("processes: %+v", byPort)
	}
	if !byPort["68/udp"].has(systemListeners...) {
		t.Fatalf("system: %+v", byPort["68/udp"])
	}
}

func TestUfwAllows(t *testing.T) {
	status := "Status: active\n\nTo   Action  From\n--   ------  ----\n22                         ALLOW       Anywhere\n" +
		"80,443/tcp                 ALLOW       Anywhere\n8080/tcp                   DENY        Anywhere\nOpenSSH                    ALLOW       Anywhere\n" +
		"8081/tcp                   ALLOW       172.16.0.0/12              # bobres Bot API server\n" +
		"9000/tcp                   ALLOW       203.0.113.7\n5353/udp                   ALLOW OUT   Anywhere\n" +
		"7000/tcp                   ALLOW IN    172.16.0.0/12\n"
	for _, c := range []struct {
		r    fwRule
		want bool
	}{
		{fwRule{port: 22, proto: "tcp"}, true}, {fwRule{port: 22, proto: "udp"}, true}, {fwRule{port: 443, proto: "tcp"}, true},
		{fwRule{port: 443, proto: "udp"}, false}, {fwRule{port: 8080, proto: "tcp"}, false}, {fwRule{port: 2, proto: "tcp"}, false},
		{fwRule{port: 5353, proto: "udp"}, false}, // an outgoing rule
		// A rule from a source: one from that source or from anywhere.
		{fwRule{port: 8081, proto: "tcp", from: "172.16.0.0/12"}, true}, {fwRule{port: 8081, proto: "tcp", from: "10.0.0.0/8"}, false},
		{fwRule{port: 443, proto: "tcp", from: "172.16.0.0/12"}, true}, {fwRule{port: 9000, proto: "tcp", from: "172.16.0.0/12"}, false},
		{fwRule{port: 7000, proto: "tcp", from: "172.16.0.0/12"}, true},
	} {
		if got := ufwAllows(status, c.r); got != c.want {
			t.Errorf("%s: %v", c.r.spec(), got)
		}
	}
}

func TestHardenCmdUsage(t *testing.T) {
	// Usage errors only: a valid run would change this machine.
	for _, args := range [][]string{{"extra"}, {"--ssh-port", "70000"}, {"--ssh-port", "x"}, {"--bogus"}, {"--dir"}} {
		var out, errOut bytes.Buffer
		if code := hardenCmd(args, strings.NewReader(""), &out, &errOut); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
}

// The install offers the hardening step after a good start, only where it
// can ask: never with --yes (bobres update), --no-harden or from the menu.
func TestInstallHardeningStep(t *testing.T) {
	healthy := `[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`
	for _, c := range []struct {
		name  string
		extra []string
		quiet bool
		want  bool
	}{
		{"asked", []string{"--ssh-port", "2222"}, false, true},
		{"--no-harden", []string{"--no-harden"}, false, false},
		{"--yes", []string{"--yes"}, false, false},
		{"quiet (a settings change from the menu)", nil, true, false},
		{"--no-start", []string{"--no-start"}, false, false},
	} {
		ins, out, errOut := testInstaller("", secrets, &fakeDocker{health: []string{healthy}})
		ins.quiet = c.quiet
		var got []hardenOptions
		ins.harden = func(_ context.Context, opt hardenOptions) int {
			got = append(got, opt)
			fmt.Fprintln(ins.out, "HARDENING RAN")
			return 1
		}
		dir := t.TempDir()
		if code := ins.install(append(baseArgs(dir), c.extra...)); code != 0 {
			t.Fatalf("%s: exit %d %s", c.name, code, errOut)
		}
		if (len(got) == 1) != c.want {
			t.Errorf("%s: hardening runs %v", c.name, got)
			continue
		}
		if !c.want {
			continue
		}
		// After the start, before the summary; a failure is not the install's.
		s := out.String()
		if got[0].sshPort != 2222 || got[0].yes || got[0].dir != dir || strings.Index(s, "All services are healthy") > strings.Index(s, "HARDENING RAN") ||
			strings.Index(s, "HARDENING RAN") > strings.Index(s, "Next steps") || !strings.Contains(errOut.String(), "the hardening did not finish") {
			t.Errorf("%s: %+v\n%s\n%s", c.name, got, s, errOut)
		}
	}
	ins, _, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(t.TempDir()), "--ssh-port", "-1")); code != 2 || !strings.Contains(errOut.String(), "--ssh-port") {
		t.Fatalf("bad --ssh-port: %d %s", code, errOut)
	}
}
