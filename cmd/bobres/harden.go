package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The files the hardening step writes.
const (
	fail2banJail     = "/etc/fail2ban/jail.d/bobres-sshd.local"
	autoUpgradesConf = "/etc/apt/apt.conf.d/20auto-upgrades"
	// securityOnlyConf comes after the package's 50unattended-upgrades, whose
	// lists it clears.
	securityOnlyConf = "/etc/apt/apt.conf.d/51bobres-security-only"
	swapFile         = "/swapfile"
	// swapBelowMB: a server with less RAM than this and no swap gets a swap file.
	swapBelowMB = 2048
)

const hardenUsage = "usage: bobres harden [--yes] [--ssh-port N] [--dir DIR]"

// hardenOptions are the choices of `bobres harden` and of the install's
// hardening step.
type hardenOptions struct {
	yes bool // apply every step without asking
	// auto is the install's way: nothing is asked. A step that is safe is done,
	// one that is not (the SSH port not detected, a public service the firewall
	// would close, 3x-ui on this server) is skipped with the command to run.
	auto    bool
	sshPort int // --ssh-port: an SSH port to keep open (0: only the detected ones)
	// dir is the install: its .env names the services on this server the
	// containers reach through host.docker.internal ("" or no .env: none).
	dir string
}

// hardener holds the side effects of the hardening step, replaceable in
// tests: it reads and changes the system only through these.
type hardener struct {
	in     *bufio.Reader
	out    io.Writer
	errOut io.Writer
	// run runs a system program and returns its combined output.
	run       func(ctx context.Context, name string, args ...string) ([]byte, error)
	readFile  func(path string) ([]byte, error)
	writeFile func(path string, data []byte, mode os.FileMode) error
	remove    func(path string) error
	exists    func(path string) bool
	lookPath  func(file string) (string, error)
	euid      func() int
	memMB     func() int
	freeGB    func(path string) float64
	getenv    func(string) string
	goos      string
	// aptUpdated: the package lists were refreshed in this run.
	aptUpdated bool
	// notify, when set, is told each step's outcome (the install shows it as
	// one line); outcome is what the step in progress has settled on.
	notify  func(kind markKind, label, text string)
	outcome *stepOutcome
}

// stepOutcome is how a hardening step ended, in one line.
type stepOutcome struct {
	kind markKind
	text string
}

// say records the outcome of the step in progress.
func (h *hardener) say(kind markKind, text string) { h.outcome = &stepOutcome{kind, text} }

// tell passes an outcome on to notify.
func (h *hardener) tell(kind markKind, label, text string) {
	if h.notify != nil {
		h.notify(kind, label, text)
	}
}

func newHardener(in io.Reader, out, errOut io.Writer) *hardener {
	br, ok := in.(*bufio.Reader) // the installer shares its reader
	if !ok {
		br = bufio.NewReader(in)
	}
	return &hardener{
		in: br, out: out, errOut: errOut,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed programs, arguments built here
			// apt never asks, and the output is in English (ufw status is parsed).
			cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C", "NEEDRESTART_MODE=a")
			return cmd.CombinedOutput()
		},
		readFile: os.ReadFile, writeFile: writeAtomic, remove: os.Remove, exists: exists,
		lookPath: exec.LookPath, euid: os.Geteuid, memMB: memMB, freeGB: freeDiskGB, getenv: os.Getenv,
		goos: runtime.GOOS,
	}
}

// hardenCmd: `bobres harden [--yes] [--ssh-port N] [--dir DIR]` runs the install's
// hardening step on its own. Exit 0 when every step is done, declined or not
// needed, 1 when one failed or was refused, 2 for usage errors.
func hardenCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("harden", flag.ContinueOnError)
	fs.SetOutput(errOut)
	yes := fs.Bool("yes", false, "apply every step without asking")
	sshPort := fs.Int("ssh-port", 0, "an SSH port the firewall keeps open (needed when it cannot be detected)")
	dir := fs.String("dir", defaultDir(), dirUsage)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 || *sshPort < 0 || *sshPort > 65535 {
		fmt.Fprintln(errOut, hardenUsage)
		return 2
	}
	return newHardener(in, out, errOut).apply(context.Background(), hardenOptions{yes: *yes, sshPort: *sshPort, dir: *dir})
}

// apply runs the hardening steps, each announced and asked: the firewall,
// fail2ban, automatic security updates and swap. A step already done is
// left as it is, so running it again is safe.
func (h *hardener) apply(ctx context.Context, opt hardenOptions) int {
	distro, why := h.distro()
	if distro == "" {
		fmt.Fprintf(h.errOut, "Hardening skipped: %s. Nothing was changed.\n", why)
		h.tell(markWarn, "Hardening", "skipped: "+why)
		return 1
	}
	if h.euid() != 0 {
		fmt.Fprintln(h.errOut, "error: hardening changes the system: run it as root (sudo bobres harden). Nothing was changed.")
		h.tell(markWarn, "Hardening", "skipped: it changes the system, so it needs root (sudo bobres harden)")
		return 1
	}
	if opt.auto {
		fmt.Fprintln(h.out, "Hardening this server by itself, where it is safe; a step already done is left as it is.")
	} else {
		fmt.Fprintln(h.out, "Hardening this server. Each step says what it changes and asks first; a step already done is left as it is.")
	}
	listeners := h.listeners(ctx)
	ssh := h.sshPorts(ctx, listeners)
	if opt.sshPort > 0 && !containsInt(ssh, opt.sshPort) {
		ssh = append(ssh, opt.sshPort)
		sort.Ints(ssh)
	}
	steps := []struct {
		title, label string
		run          func() error
	}{
		{"Firewall (ufw)", "Firewall (ufw)", func() error { return h.firewall(ctx, opt, ssh, listeners) }},
		{"Blocking SSH password guessing (fail2ban)", "SSH guard (fail2ban)", func() error { return h.fail2ban(ctx, opt, ssh) }},
		{"Automatic security updates (unattended-upgrades)", "Security updates", func() error { return h.securityUpdates(ctx, opt, distro) }},
		{"Swap", "Swap file", func() error { return h.swap(ctx, opt) }},
	}
	failed := 0
	for i, s := range steps {
		fmt.Fprintf(h.out, "\n[%d/%d] %s\n", i+1, len(steps), s.title)
		h.outcome = nil
		err := s.run()
		switch {
		case err != nil:
			fmt.Fprintf(h.errOut, "  error: %v\n", err)
			failed++
			first, _, _ := strings.Cut(err.Error(), "\n")
			h.tell(markFail, s.label, first+"; fix it and run: sudo bobres harden")
		case h.outcome != nil:
			h.tell(h.outcome.kind, s.label, h.outcome.text)
		default:
			h.tell(markOK, s.label, "done")
		}
	}
	if failed > 0 {
		fmt.Fprintf(h.errOut, "\n%d hardening step(s) did not finish (see above). Fix the cause and run sudo bobres harden again: what is done stays done.\n", failed)
		return 1
	}
	fmt.Fprintln(h.out, "\nHardening done. bobres doctor shows its state.")
	return 0
}

// distro is "debian" or "ubuntu" from /etc/os-release, else "" and why.
func (h *hardener) distro() (string, string) {
	if h.goos != "linux" {
		return "", "it is for Linux servers (Debian or Ubuntu)"
	}
	b, err := h.readFile("/etc/os-release")
	if err != nil {
		return "", "cannot read /etc/os-release to tell the distribution"
	}
	switch id := osReleaseField(b, "ID"); id {
	case "debian", "ubuntu":
		return id, ""
	default:
		return "", fmt.Sprintf("only Debian and Ubuntu are supported (this is %s)", orDash(id))
	}
}

// osReleaseField reads one KEY=value of /etc/os-release, lower case.
func osReleaseField(b []byte, key string) string {
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k == key {
			return strings.ToLower(strings.Trim(v, `"'`))
		}
	}
	return ""
}

// confirm asks a yes/no question. Enter takes def; the end of input (no
// terminal) is always no, so a script never changes the system unasked.
func (h *hardener) confirm(opt hardenOptions, question string, def bool) bool {
	suffix := " [y/N]: "
	if def {
		suffix = " [Y/n]: "
	}
	if opt.yes {
		fmt.Fprintln(h.out, "  "+question+suffix+"yes (--yes)")
		return true
	}
	if opt.auto {
		fmt.Fprintln(h.out, "  "+question+suffix+"yes (automatic: it is safe)")
		return true
	}
	fmt.Fprint(h.out, "  "+question+suffix)
	line, err := h.in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(h.out, "\n  No answer: skipped.")
		return false
	}
	switch a := strings.ToLower(strings.TrimSpace(line)); a {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}

// do runs one change, announced first: the operator sees each command as it
// runs.
func (h *hardener) do(ctx context.Context, name string, args ...string) error {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	fmt.Fprintf(h.out, "  > %s\n", line)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if out, err := h.run(ctx, name, args...); err != nil {
		return fmt.Errorf("%s: %w\n%s", line, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// query runs a program that only reads the system.
func (h *hardener) query(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := h.run(ctx, name, args...)
	return string(out), err
}

// installed reports whether a Debian package is installed.
func (h *hardener) installed(ctx context.Context, pkg string) bool {
	out, err := h.query(ctx, "dpkg-query", "-W", "-f=${Status}", pkg)
	return err == nil && strings.Contains(out, "install ok installed")
}

// install installs the packages that are missing (the package lists are
// refreshed once per run first).
func (h *hardener) install(ctx context.Context, pkgs ...string) error {
	var missing []string
	for _, p := range pkgs {
		if !h.installed(ctx, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !h.aptUpdated {
		if err := h.do(ctx, "apt-get", "update", "-q"); err != nil {
			return err
		}
		h.aptUpdated = true
	}
	return h.do(ctx, "apt-get", append([]string{"install", "-y", "-q"}, missing...)...)
}

// fileIs reports whether path holds exactly want.
func (h *hardener) fileIs(path, want string) bool {
	b, err := h.readFile(path)
	return err == nil && string(b) == want
}

// listener is a port a program listens on, as ss shows it.
type listener struct {
	proto     string // tcp or udp
	port      int
	processes []string
	loopback  bool // only on a loopback address: never reachable from outside
}

func (l listener) has(names ...string) bool {
	for _, p := range l.processes {
		for _, n := range names {
			if p == n {
				return true
			}
		}
	}
	return false
}

// xui reports whether the listener is 3x-ui (its panel, x-ui, or its Xray).
func (l listener) xui() bool {
	for _, p := range l.processes {
		if p == "x-ui" || strings.HasPrefix(p, "xray") {
			return true
		}
	}
	return false
}

func (l listener) String() string {
	s := fmt.Sprintf("%d/%s", l.port, l.proto)
	if len(l.processes) > 0 {
		s += " (" + l.processes[0] + ")"
	}
	return s
}

var ssProcessRe = regexp.MustCompile(`\("([^"]+)"`)

// listeners reads the listening sockets (ss -H -tulpn); none when ss fails.
func (h *hardener) listeners(ctx context.Context) []listener {
	out, err := h.query(ctx, "ss", "-H", "-tulpn")
	if err != nil {
		return nil
	}
	return parseListeners(out)
}

// parseListeners reads `ss -H -tulpn`: Netid State Recv-Q Send-Q Local Peer
// Process.
func parseListeners(out string) []listener {
	var ls []listener
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || (f[0] != "tcp" && f[0] != "udp") {
			continue
		}
		i := strings.LastIndex(f[4], ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(f[4][i+1:])
		if err != nil || port <= 0 {
			continue
		}
		host := strings.Trim(f[4][:i], "[]")
		if j := strings.Index(host, "%"); j >= 0 { // bound to an interface: 127.0.0.53%lo
			host = host[:j]
		}
		ip := net.ParseIP(host)
		l := listener{proto: f[0], port: port, loopback: ip != nil && ip.IsLoopback()}
		if len(f) > 6 {
			for _, m := range ssProcessRe.FindAllStringSubmatch(strings.Join(f[6:], " "), -1) {
				l.processes = append(l.processes, m[1])
			}
		}
		ls = append(ls, l)
	}
	return ls
}

// sshPorts are the ports SSH listens on: sshd's own settings (sshd -T, else
// /etc/ssh/sshd_config, where no Port line means 22), the ports ss sees sshd
// on, and this session's (SSH_CONNECTION). Empty: not detected.
func (h *hardener) sshPorts(ctx context.Context, listeners []listener) []int {
	set := map[int]bool{}
	if out, err := h.query(ctx, "sshd", "-T"); err == nil {
		for _, p := range portLines(out) {
			set[p] = true
		}
	} else if b, err := h.readFile("/etc/ssh/sshd_config"); err == nil {
		ports := portLines(string(b))
		if len(ports) == 0 {
			ports = []int{22}
		}
		for _, p := range ports {
			set[p] = true
		}
	}
	for _, l := range listeners {
		if l.proto == "tcp" && l.has("sshd") {
			set[l.port] = true
		}
	}
	// "client_ip client_port server_ip server_port"
	if f := strings.Fields(h.getenv("SSH_CONNECTION")); len(f) == 4 {
		if p, err := strconv.Atoi(f[3]); err == nil && p > 0 && p <= 65535 {
			set[p] = true
		}
	}
	ports := make([]int, 0, len(set))
	for p := range set {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// portLines reads the "Port N" lines of sshd's settings (sshd -T prints
// them in lower case; a commented one does not count).
func portLines(s string) []int {
	var ports []int
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.EqualFold(f[0], "port") {
			if p, err := strconv.Atoi(f[1]); err == nil && p > 0 && p <= 65535 {
				ports = append(ports, p)
			}
		}
	}
	return ports
}

// fwRule is a port the firewall allows: from anywhere, or only from one
// source range.
type fwRule struct {
	port  int
	proto string
	what  string // the ufw comment, after "bobres "
	env   string // the .env key that names the service, if any
	from  string // a source range (CIDR); "" is anywhere
}

func (r fwRule) spec() string { return fmt.Sprintf("%d/%s", r.port, r.proto) }

func (r fwRule) String() string {
	what := r.what
	if r.env != "" {
		what += ", " + r.env
	}
	s := r.spec() + " (" + what + ")"
	if r.from != "" {
		s += " from " + r.from
	}
	return s
}

// ufwAllows reports whether `ufw status` shows an incoming ALLOW rule for the
// port; for a rule with a source, one from that source or from anywhere.
func ufwAllows(status string, r fwRule) bool {
	for _, line := range strings.Split(status, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[1] != "ALLOW" {
			continue
		}
		from := f[2:]
		if len(from) > 0 && from[0] == "IN" {
			from = from[1:]
		}
		if len(from) > 0 && from[0] == "OUT" {
			continue // an outgoing rule
		}
		if r.from != "" && (len(from) == 0 || (from[0] != r.from && from[0] != "Anywhere")) {
			continue
		}
		ports, proto, _ := strings.Cut(f[0], "/")
		if proto != "" && proto != r.proto {
			continue
		}
		for _, p := range strings.Split(ports, ",") {
			if p == strconv.Itoa(r.port) {
				return true
			}
		}
	}
	return false
}

// systemListeners are programs whose public ports the firewall may close:
// the DHCP client and time sync, which need no incoming rule.
var systemListeners = []string{"dhclient", "systemd-network", "systemd-resolve", "systemd-timesyn", "chronyd", "ntpd", "avahi-daemon"}

// hostPort is a port on this server the BOBRES containers reach through
// host.docker.internal, and the .env key that names it.
type hostPort struct {
	port int
	key  string
}

// what names the service for the operator and the ufw comment.
func (p hostPort) what() string {
	switch p.key {
	case "BOBRES_TELEGRAM_API_URL":
		return "Bot API server"
	case "BOBRES_XUI_URL":
		return "3x-ui panel"
	}
	return "service"
}

func (p hostPort) String() string { return fmt.Sprintf("%d/tcp (%s, %s)", p.port, p.what(), p.key) }

// hostDockerPorts are the ports the .env values reach on this server through
// host.docker.internal (a local Bot API server, a 3x-ui panel here), each
// once, in port order.
func hostDockerPorts(env map[string]string) []hostPort {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	byPort := map[int]string{}
	for _, k := range keys {
		u, err := url.Parse(env[k])
		if err != nil || !strings.EqualFold(u.Hostname(), "host.docker.internal") {
			continue
		}
		port := 0
		switch p := u.Port(); {
		case p != "":
			port, _ = strconv.Atoi(p)
		case u.Scheme == "http":
			port = 80
		case u.Scheme == "https":
			port = 443
		}
		if _, ok := byPort[port]; !ok && port > 0 && port <= 65535 {
			byPort[port] = k
		}
	}
	ps := make([]hostPort, 0, len(byPort))
	for p, k := range byPort {
		ps = append(ps, hostPort{p, k})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].port < ps[j].port })
	return ps
}

// hostPorts reads the install's .env for the host.docker.internal ports;
// none without one. Only those URLs are used: nothing else of it is shown.
func (h *hardener) hostPorts(dir string) []hostPort {
	if dir == "" {
		return nil
	}
	b, err := h.readFile(filepath.Join(dir, ".env"))
	if err != nil {
		return nil
	}
	return hostDockerPorts(parseEnv(b))
}

// uncovered leaves out the ports a rule already opens to everyone.
func uncovered(ps []hostPort, rules []fwRule) []hostPort {
	var out []hostPort
	for _, p := range ps {
		if !slices.ContainsFunc(rules, func(r fwRule) bool { return r.from == "" && r.port == p.port && r.proto == "tcp" }) {
			out = append(out, p)
		}
	}
	return out
}

// dockerPool holds Docker's default address pools for bridge networks
// (172.17.0.0/16 to 172.31.0.0/16), where the containers' addresses come from.
const dockerPool = "172.16.0.0/12"

// dockerSources are the ranges the containers reach the host from: Docker's
// default pools, and the subnet of the store's egress network when Docker
// reports one outside them (a custom address pool). The containers' traffic
// to the host goes through its INPUT chain, which ufw's default deny closes.
func (h *hardener) dockerSources(ctx context.Context) []string {
	srcs := []string{dockerPool}
	_, pool, _ := net.ParseCIDR(dockerPool)
	out, err := h.query(ctx, "docker", "network", "inspect", "--format", "{{range .IPAM.Config}}{{.Subnet}} {{end}}", composeProject+"_egress")
	if err != nil {
		return srcs
	}
	for _, f := range strings.Fields(out) {
		ip, n, err := net.ParseCIDR(f)
		if err != nil || ip.To4() == nil || pool.Contains(n.IP) {
			continue
		}
		if s := n.String(); !slices.Contains(srcs, s) {
			srcs = append(srcs, s)
		}
	}
	return srcs
}

// hostRules allow each host.docker.internal port only from Docker's networks.
func hostRules(ps []hostPort, sources []string) []fwRule {
	var rules []fwRule
	for _, p := range ps {
		for _, src := range sources {
			rules = append(rules, fwRule{port: p.port, proto: "tcp", what: p.what(), env: p.key, from: src})
		}
	}
	return rules
}

// explainHosts tells the operator why those ports get a rule of their own.
func (h *hardener) explainHosts(ps []hostPort, sources []string) {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.String()
	}
	fmt.Fprintf(h.out, "  The BOBRES containers reach %s on this server through host.docker.internal:\n"+
		"  allowed from Docker's networks only (%s), closed to the outside.\n", strings.Join(parts, ", "), strings.Join(sources, ", "))
}

// firewall turns ufw on: SSH, 80 and 443 (and 443/udp for HTTP/3) allowed,
// every other incoming connection refused. It never cuts this session: with
// no SSH port detected (and no --ssh-port) it refuses. Ports Docker publishes
// do not go through ufw; a 3x-ui on this server keeps the ports it listens on,
// and a service here the containers use (host.docker.internal in .env) stays
// open to them only. On a ufw that is already on, only the missing SSH, web
// and container rules are offered: a 3x-ui port it refuses may be on purpose.
func (h *hardener) firewall(ctx context.Context, opt hardenOptions, ssh []int, listeners []listener) error {
	if len(ssh) == 0 && opt.auto {
		fmt.Fprintln(h.out, "  The SSH port could not be detected: the firewall stays off, as turning it on could cut this session.")
		h.say(markWarn, "skipped: the SSH port was not detected, so it could cut this session. Run: sudo bobres harden --ssh-port <port>")
		return nil
	}
	if len(ssh) == 0 {
		return errors.New("the SSH port could not be detected (sshd -T, sshd_config, ss and this session say nothing), " +
			"so the firewall stays off: turning it on could cut this connection. Run again with --ssh-port <port>")
	}
	var rules []fwRule
	for _, p := range ssh {
		rules = append(rules, fwRule{port: p, proto: "tcp", what: "SSH"})
	}
	rules = append(rules, fwRule{port: 80, proto: "tcp", what: "HTTP"}, fwRule{port: 443, proto: "tcp", what: "HTTPS"},
		fwRule{port: 443, proto: "udp", what: "HTTP/3"})
	hosts := uncovered(h.hostPorts(opt.dir), rules)
	onHost := map[string]bool{}
	for _, p := range hosts {
		onHost[fmt.Sprintf("%d/tcp", p.port)] = true
	}
	var xui, others []listener
	seen := map[string]bool{}
	for _, r := range rules {
		seen[r.spec()] = true
	}
	for _, l := range listeners {
		key := fmt.Sprintf("%d/%s", l.port, l.proto)
		if l.loopback || seen[key] || l.has("docker-proxy") || l.has(systemListeners...) {
			continue
		}
		seen[key] = true
		switch {
		case l.xui():
			xui = append(xui, l)
		case !onHost[key]: // a service the containers use has its own rule
			others = append(others, l)
		}
	}

	_, lookErr := h.lookPath("ufw")
	haveUfw := lookErr == nil
	status := ""
	if haveUfw {
		status, _ = h.query(ctx, "ufw", "status")
	}
	if strings.Contains(status, "Status: active") {
		return h.firewallOn(ctx, opt, status, rules, hosts, xui)
	}

	// Turned on from off by itself only where nothing can be cut off: 3x-ui
	// on this server, a service the containers reach on it, or any other
	// public service means the owner looks first.
	if opt.auto {
		var why []string
		if len(xui) > 0 {
			why = append(why, "3x-ui runs on this server ("+joinListeners(xui)+")")
		}
		if len(hosts) > 0 {
			why = append(why, "BOBRES reaches a service on this server (host.docker.internal)")
		}
		if len(others) > 0 {
			why = append(why, "other services listen publicly ("+joinListeners(others)+")")
		}
		if len(why) > 0 {
			text := strings.Join(why, "; ")
			fmt.Fprintf(h.out, "  The firewall stays off: %s.\n", text)
			h.say(markWarn, "skipped: "+text+". Review, then run: sudo bobres harden")
			return nil
		}
	}
	// Turned on from off: 3x-ui keeps the ports it listens on now.
	for _, l := range xui {
		rules = append(rules, fwRule{port: l.port, proto: l.proto, what: "3x-ui"})
	}
	var sources []string
	if hosts = uncovered(hosts, rules); len(hosts) > 0 {
		sources = h.dockerSources(ctx)
	}
	fmt.Fprintf(h.out, "  Allowed in: %s.\n  Every other incoming connection is refused; outgoing traffic stays open.\n", describeRules(rules))
	fmt.Fprintf(h.out, "  SSH listens on port %s (detected), so this session stays connected.\n", joinInts(ssh))
	if len(hosts) > 0 {
		h.explainHosts(hosts, sources)
	}
	if len(xui) > 0 {
		fmt.Fprintf(h.out, "  3x-ui runs on this server: its ports %s stay open. A port you add to 3x-ui later (a new inbound)\n"+
			"  is refused until you run: sudo ufw allow <port>/tcp\n", joinListeners(xui))
	}
	if len(others) > 0 {
		// The containers reach the host through its INPUT chain too, so the
		// default deny closes these ports to them as well.
		fmt.Fprintf(h.out, "  These ports would be closed to the outside and to the BOBRES containers: %s.\n"+
			"  To keep one open to everyone: sudo ufw allow <port>/<tcp|udp>\n"+
			"  or, for the BOBRES containers only: sudo ufw allow from %s to any port <port> proto tcp\n", joinListeners(others), dockerPool)
	}
	if !haveUfw {
		fmt.Fprintln(h.out, "  The ufw package is installed first.")
	}
	// With 3x-ui here, the operator must know about new inbounds: no default yes.
	if !h.confirm(opt, "Turn on the firewall?", len(xui) == 0) {
		fmt.Fprintln(h.out, "  Skipped.")
		return nil
	}
	if !haveUfw {
		if err := h.install(ctx, "ufw"); err != nil {
			return err
		}
	}
	// The allow rules first, then on: the SSH port is open before anything is refused.
	if err := h.allow(ctx, append(rules, hostRules(hosts, sources)...)); err != nil {
		return err
	}
	for _, args := range [][]string{{"default", "deny", "incoming"}, {"default", "allow", "outgoing"}, {"--force", "enable"}} {
		if err := h.do(ctx, "ufw", args...); err != nil {
			return err
		}
	}
	fmt.Fprintln(h.out, "  Firewall on.")
	h.say(markOK, "on: SSH "+joinInts(ssh)+", HTTP, HTTPS allowed; every other incoming connection refused")
	return nil
}

// firewallOn handles a ufw that is already on: its defaults and rules stay,
// and only the missing SSH, web and container rules are offered. 3x-ui's
// ports are never offered there: one it refuses may be refused on purpose (a
// panel reached through an SSH tunnel), so they are only listed.
func (h *hardener) firewallOn(ctx context.Context, opt hardenOptions, status string, rules []fwRule, hosts []hostPort, xui []listener) error {
	var sources []string
	if len(hosts) > 0 {
		sources = h.dockerSources(ctx)
	}
	var missing, allowed []fwRule
	for _, r := range append(rules, hostRules(hosts, sources)...) {
		if ufwAllows(status, r) {
			allowed = append(allowed, r)
		} else {
			missing = append(missing, r)
		}
	}
	var closed []listener
	for _, l := range xui {
		if r := (fwRule{port: l.port, proto: l.proto, what: "3x-ui"}); ufwAllows(status, r) {
			allowed = append(allowed, r)
		} else {
			closed = append(closed, l)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintf(h.out, "  Already on, with %s allowed.\n", describeRules(allowed))
		h.say(markOK, "already on, with "+describeRules(allowed)+" allowed")
	} else {
		fmt.Fprintf(h.out, "  ufw is already on, but does not allow %s yet.\n", describeRules(missing))
		var newHosts []hostPort
		for _, p := range hosts {
			if slices.ContainsFunc(missing, func(r fwRule) bool { return r.from != "" && r.port == p.port }) {
				newHosts = append(newHosts, p)
			}
		}
		if len(newHosts) > 0 {
			h.explainHosts(newHosts, sources)
		}
	}
	if len(closed) > 0 {
		fmt.Fprintf(h.out, "  ufw does not allow 3x-ui's %s: left closed to the outside, as that may be\n"+
			"  on purpose (a panel reached through an SSH tunnel). To open one to everyone: sudo ufw allow <port>/<tcp|udp>\n", joinListeners(closed))
	}
	if len(missing) == 0 {
		return nil
	}
	if !h.confirm(opt, "Allow them?", true) {
		fmt.Fprintln(h.out, "  Skipped.")
		return nil
	}
	if err := h.allow(ctx, missing); err != nil {
		return err
	}
	h.say(markOK, "already on; added "+describeRules(missing))
	return nil
}

func (h *hardener) allow(ctx context.Context, rules []fwRule) error {
	for _, r := range rules {
		args := []string{"allow", r.spec()}
		if r.from != "" {
			args = []string{"allow", "from", r.from, "to", "any", "port", strconv.Itoa(r.port), "proto", r.proto}
		}
		if err := h.do(ctx, "ufw", append(args, "comment", "bobres "+r.what)...); err != nil {
			return err
		}
	}
	return nil
}

func describeRules(rules []fwRule) string {
	parts := make([]string, len(rules))
	for i, r := range rules {
		parts[i] = r.String()
	}
	return strings.Join(parts, ", ")
}

func joinListeners(ls []listener) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.String()
	}
	return strings.Join(parts, ", ")
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

func containsInt(ns []int, n int) bool {
	for _, v := range ns {
		if v == n {
			return true
		}
	}
	return false
}

// jailFile is fail2ban's sshd jail as bobres writes it. Without
// /var/log/auth.log (Debian 12 logs to the journal only) it reads the journal.
func (h *hardener) jailFile(ssh []int) (content string, journal bool) {
	port := "ssh"
	if len(ssh) > 0 {
		port = strings.ReplaceAll(joinInts(ssh), " ", "")
	}
	backend := "auto"
	if !h.exists("/var/log/auth.log") {
		backend, journal = "systemd", true
	}
	return fmt.Sprintf(`# Written by bobres harden: an address that fails to log in over SSH 5 times
# within 10 minutes is blocked for an hour.
[sshd]
enabled  = true
port     = %s
backend  = %s
maxretry = 5
findtime = 10m
bantime  = 1h
`, port, backend), journal
}

// fail2ban installs fail2ban with an sshd jail.
func (h *hardener) fail2ban(ctx context.Context, opt hardenOptions, ssh []int) error {
	jail, journal := h.jailFile(ssh)
	running, _ := h.query(ctx, "systemctl", "is-active", "fail2ban")
	if h.installed(ctx, "fail2ban") && h.fileIs(fail2banJail, jail) && strings.TrimSpace(running) == "active" {
		fmt.Fprintln(h.out, "  Already on (SSH jail).")
		h.say(markOK, "already on (SSH jail)")
		return nil
	}
	fmt.Fprintf(h.out, "  This installs fail2ban (if missing) and blocks for an hour an address that fails to log in over SSH\n"+
		"  5 times within 10 minutes (%s).\n", fail2banJail)
	if !h.confirm(opt, "Set up fail2ban?", true) {
		fmt.Fprintln(h.out, "  Skipped.")
		return nil
	}
	pkgs := []string{"fail2ban"}
	if journal {
		pkgs = append(pkgs, "python3-systemd") // the journal backend
	}
	if err := h.install(ctx, pkgs...); err != nil {
		return err
	}
	fmt.Fprintf(h.out, "  > write %s\n", fail2banJail)
	if err := h.writeFile(fail2banJail, []byte(jail), 0o644); err != nil {
		return err
	}
	if err := h.do(ctx, "systemctl", "enable", "fail2ban"); err != nil {
		return err
	}
	if err := h.do(ctx, "systemctl", "restart", "fail2ban"); err != nil {
		return err
	}
	fmt.Fprintln(h.out, "  fail2ban on.")
	h.say(markOK, "on: 5 failed SSH logins in 10 minutes block an address for an hour")
	return nil
}

// autoUpgrades turns on apt's daily package lists and unattended-upgrades.
const autoUpgrades = `// Written by bobres harden: refresh the package lists and run unattended-upgrades every day.
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
`

// securityOnly limits unattended-upgrades to the distribution's security
// updates: the other updates (Docker's too) stay for the operator, and the
// server is never rebooted by it.
func securityOnly(distro string) string {
	origins := `        "origin=Debian,codename=${distro_codename}-security,label=Debian-Security";`
	if distro == "ubuntu" {
		origins = `        "origin=Ubuntu,archive=${distro_codename}-security,label=Ubuntu";
        "origin=UbuntuESMApps,archive=${distro_codename}-apps-security";
        "origin=UbuntuESM,archive=${distro_codename}-infra-security";`
	}
	return `// Written by bobres harden: unattended-upgrades installs security updates only.
#clear Unattended-Upgrade::Allowed-Origins;
#clear Unattended-Upgrade::Origins-Pattern;
Unattended-Upgrade::Origins-Pattern {
` + origins + `
};
Unattended-Upgrade::Automatic-Reboot "false";
`
}

// securityUpdates installs unattended-upgrades for security updates only.
func (h *hardener) securityUpdates(ctx context.Context, opt hardenOptions, distro string) error {
	want := securityOnly(distro)
	if h.installed(ctx, "unattended-upgrades") && h.fileIs(autoUpgradesConf, autoUpgrades) && h.fileIs(securityOnlyConf, want) {
		fmt.Fprintln(h.out, "  Already on (security updates only).")
		h.say(markOK, "already on (security updates only)")
		return nil
	}
	fmt.Fprintf(h.out, "  This installs unattended-upgrades (if missing) and installs the security updates every day. Other\n"+
		"  updates, Docker's included, stay for you to apply, and it never reboots the server (%s, %s).\n", autoUpgradesConf, securityOnlyConf)
	if !h.confirm(opt, "Turn on automatic security updates?", true) {
		fmt.Fprintln(h.out, "  Skipped.")
		return nil
	}
	if err := h.install(ctx, "unattended-upgrades"); err != nil {
		return err
	}
	for _, f := range []struct{ path, data string }{{autoUpgradesConf, autoUpgrades}, {securityOnlyConf, want}} {
		fmt.Fprintf(h.out, "  > write %s\n", f.path)
		if err := h.writeFile(f.path, []byte(f.data), 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintln(h.out, "  Security updates on.")
	h.say(markOK, "on: security updates every day, never a reboot")
	return nil
}

// swapMB is the swap in use, from /proc/swaps (sizes in kB); -1 unknown.
func (h *hardener) swapMB() int {
	b, err := h.readFile("/proc/swaps")
	if err != nil {
		return -1
	}
	kb := 0
	for i, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 3 {
			continue // the header
		}
		if n, err := strconv.Atoi(f[2]); err == nil {
			kb += n
		}
	}
	return kb / 1024
}

// swap adds a swap file on a small server: 2 GB with 1 GB of RAM or less,
// else 1 GB, when RAM is under 2 GB and there is no swap.
func (h *hardener) swap(ctx context.Context, opt hardenOptions) error {
	mb, have := h.memMB(), h.swapMB()
	switch {
	case have > 0:
		fmt.Fprintf(h.out, "  Already on (%d MB).\n", have)
		h.say(markOK, fmt.Sprintf("already on (%d MB)", have))
		return nil
	case mb <= 0 || have < 0:
		fmt.Fprintln(h.out, "  Skipped: the memory size could not be read.")
		h.say(markWarn, "skipped: the memory size could not be read")
		return nil
	case mb >= swapBelowMB:
		fmt.Fprintf(h.out, "  Not needed: %d MB of RAM.\n", mb)
		h.say(markOK, fmt.Sprintf("not needed: %d MB of RAM", mb))
		return nil
	case h.exists(swapFile):
		fmt.Fprintf(h.out, "  Skipped: %s exists but is not in use. Check it, then: sudo swapon %s\n", swapFile, swapFile)
		h.say(markWarn, fmt.Sprintf("skipped: %s exists but is not in use. Check it, then: sudo swapon %s", swapFile, swapFile))
		return nil
	}
	size := 1024
	if mb <= 1024 {
		size = 2048
	}
	if free := h.freeGB("/"); free >= 0 && free < float64(size)/1024+3 {
		fmt.Fprintf(h.out, "  Skipped: %.1f GB free on / is too little for a %d GB swap file.\n", free, size/1024)
		h.say(markWarn, fmt.Sprintf("skipped: %.1f GB free on / is too little for a %d GB swap file", free, size/1024))
		return nil
	}
	fmt.Fprintf(h.out, "  %d MB of RAM and no swap: a %d GB swap file (%s) keeps the store from being stopped when memory runs short.\n",
		mb, size/1024, swapFile)
	if !h.confirm(opt, "Add the swap file?", true) {
		fmt.Fprintln(h.out, "  Skipped.")
		return nil
	}
	if err := h.do(ctx, "fallocate", "-l", strconv.Itoa(size)+"M", swapFile); err != nil {
		// Some file systems cannot allocate: write it out instead.
		if err := h.do(ctx, "dd", "if=/dev/zero", "of="+swapFile, "bs=1M", "count="+strconv.Itoa(size), "status=none"); err != nil {
			_ = h.remove(swapFile)
			return err
		}
	}
	for _, args := range [][]string{{"chmod", "600", swapFile}, {"mkswap", swapFile}, {"swapon", swapFile}} {
		if err := h.do(ctx, args[0], args[1:]...); err != nil {
			fmt.Fprintf(h.out, "  Removing the unfinished %s.\n", swapFile)
			_ = h.remove(swapFile)
			return err
		}
	}
	fstab, err := h.readFile("/etc/fstab")
	if err != nil {
		return fmt.Errorf("the swap file is on, but /etc/fstab could not be read (%w): add  %s none swap sw 0 0  to it, or it is off after a reboot", err, swapFile)
	}
	for _, line := range strings.Split(string(fstab), "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == swapFile {
			fmt.Fprintln(h.out, "  Swap on.")
			h.say(markOK, fmt.Sprintf("%d GB swap file on (%d MB of RAM)", size/1024, mb))
			return nil
		}
	}
	text := string(fstab)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	fmt.Fprintln(h.out, "  > add the swap file to /etc/fstab (on after a reboot)")
	if err := h.writeFile("/etc/fstab", []byte(text+swapFile+" none swap sw 0 0\n"), 0o644); err != nil {
		return fmt.Errorf("the swap file is on, but /etc/fstab was not updated (%w): it is off after a reboot", err)
	}
	fmt.Fprintln(h.out, "  Swap on.")
	h.say(markOK, fmt.Sprintf("%d GB swap file on (%d MB of RAM)", size/1024, mb))
	return nil
}

// report is the hardening state for bobres doctor. Never a failure: the
// step is optional.
func (h *hardener) report(ctx context.Context) []Result {
	if distro, why := h.distro(); distro == "" {
		return []Result{{"Hardening", Skip, why}}
	}
	const fix = " (sudo bobres harden)"
	var rs []Result
	_, lookErr := h.lookPath("ufw")
	switch {
	case lookErr != nil:
		rs = append(rs, Result{"Firewall (ufw)", Warn, "not installed" + fix})
	case h.euid() != 0:
		rs = append(rs, Result{"Firewall (ufw)", Skip, "run as root to see it (sudo bobres doctor)"})
	default:
		out, err := h.query(ctx, "ufw", "status")
		switch {
		case err != nil:
			rs = append(rs, Result{"Firewall (ufw)", Warn, "ufw status failed: " + strings.TrimSpace(out)})
		case strings.Contains(out, "Status: active"):
			rs = append(rs, Result{"Firewall (ufw)", Pass, "on"})
		default:
			rs = append(rs, Result{"Firewall (ufw)", Warn, "off" + fix})
		}
	}

	if !h.installed(ctx, "fail2ban") {
		rs = append(rs, Result{"fail2ban (SSH)", Warn, "not installed" + fix})
	} else if out, _ := h.query(ctx, "systemctl", "is-active", "fail2ban"); strings.TrimSpace(out) == "active" {
		detail := "on"
		if h.exists(fail2banJail) {
			detail += ", with the SSH jail of bobres harden"
		}
		rs = append(rs, Result{"fail2ban (SSH)", Pass, detail})
	} else {
		rs = append(rs, Result{"fail2ban (SSH)", Warn, "installed but not running (systemctl status fail2ban)"})
	}

	conf, _ := h.readFile(autoUpgradesConf)
	switch {
	case !h.installed(ctx, "unattended-upgrades"):
		rs = append(rs, Result{"Security updates", Warn, "unattended-upgrades not installed" + fix})
	case !strings.Contains(string(conf), `Unattended-Upgrade "1"`):
		rs = append(rs, Result{"Security updates", Warn, "unattended-upgrades is off" + fix})
	case h.exists(securityOnlyConf):
		rs = append(rs, Result{"Security updates", Pass, "automatic, security updates only"})
	default:
		rs = append(rs, Result{"Security updates", Pass, "automatic (the distribution's selection)"})
	}

	mb, have := h.memMB(), h.swapMB()
	switch {
	case have > 0:
		rs = append(rs, Result{"Swap", Pass, fmt.Sprintf("%d MB", have)})
	case mb > 0 && mb < swapBelowMB && have == 0:
		rs = append(rs, Result{"Swap", Warn, fmt.Sprintf("none, with %d MB of RAM%s", mb, fix)})
	case mb > 0 && have == 0:
		rs = append(rs, Result{"Swap", Pass, fmt.Sprintf("none needed (%d MB of RAM)", mb)})
	default:
		rs = append(rs, Result{"Swap", Skip, "could not read the memory size"})
	}
	return rs
}
