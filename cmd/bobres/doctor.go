package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

// Status is the outcome of one doctor check.
type Status string

// Check outcomes.
const (
	Pass Status = "PASS"
	Warn Status = "WARN"
	Fail Status = "FAIL"
	Skip Status = "SKIP"
)

// Result is one line of doctor output.
type Result struct {
	Name   string
	Status Status
	Detail string
}

// The RAM thresholds are in MiB of MemTotal, which is a little under the
// size a server is sold with (the kernel and firmware keep some, and a "GB"
// may be 1000 MB): a "1 GB" VPS reports roughly 920 to 1000, a "2 GB" one
// roughly 1850 to 2000. So a 1 GB server (the supported minimum) passes with
// a warning, and a 2 GB one (recommended) passes.
const (
	minRAMMB    = 900  // under this, not even a 1 GB server
	warnRAMMB   = 1800 // under this, smaller than a 2 GB server
	minDiskGB   = 10
	supportedOS = "linux"
)

func checkOS(goos, goarch string) Result {
	if goos != supportedOS {
		return Result{"OS/arch", Fail, fmt.Sprintf("%s/%s: only Linux (Ubuntu/Debian) is supported for install", goos, goarch)}
	}
	if goarch != "amd64" && goarch != "arm64" {
		return Result{"OS/arch", Fail, fmt.Sprintf("unsupported architecture %s", goarch)}
	}
	return Result{"OS/arch", Pass, goos + "/" + goarch}
}

func checkRAM(mb int) Result {
	switch {
	case mb <= 0:
		return Result{"RAM", Skip, "could not read memory size"}
	case mb < minRAMMB:
		return Result{"RAM", Fail, fmt.Sprintf("%d MB, need a server with at least 1 GB (%d MB usable)", mb, minRAMMB)}
	case mb < warnRAMMB:
		return Result{"RAM", Warn, fmt.Sprintf("%d MB works but 2 GB+ is recommended", mb)}
	default:
		return Result{"RAM", Pass, fmt.Sprintf("%d MB", mb)}
	}
}

func checkDisk(freeGB float64) Result {
	if freeGB < 0 {
		return Result{"Disk", Skip, "could not read free space"}
	}
	if freeGB < minDiskGB {
		return Result{"Disk", Fail, fmt.Sprintf("%.1f GB free, need at least %d GB", freeGB, minDiskGB)}
	}
	return Result{"Disk", Pass, fmt.Sprintf("%.1f GB free", freeGB)}
}

func checkPort(port int, inUse bool) Result {
	name := fmt.Sprintf("Port %d", port)
	if inUse {
		return Result{name, Fail, "already in use; stop the service using it (Caddy needs 80 and 443)"}
	}
	return Result{name, Pass, "free"}
}

func checkDocker(installed, daemonUp bool) Result {
	switch {
	case !installed:
		return Result{"Docker", Warn, "not installed (the installer will install it)"}
	case !daemonUp:
		return Result{"Docker", Fail, "installed but the daemon is not reachable (is it running? are you root?)"}
	default:
		return Result{"Docker", Pass, "daemon reachable"}
	}
}

func checkCompose(ok bool) Result {
	if !ok {
		return Result{"Docker Compose v2", Warn, "`docker compose` not found (installed with Docker)"}
	}
	return Result{"Docker Compose v2", Pass, "available"}
}

func checkDNS(domain string, resolved []string, publicIP string) Result {
	if domain == "" {
		return Result{"DNS", Skip, "no --domain given"}
	}
	if len(resolved) == 0 {
		return Result{"DNS", Fail, fmt.Sprintf("%s does not resolve; create an A record pointing to this server", domain)}
	}
	if publicIP == "" {
		return Result{"DNS", Warn, fmt.Sprintf("%s resolves to %s but this server's public IP is unknown", domain, strings.Join(resolved, ","))}
	}
	for _, ip := range resolved {
		if ip == publicIP {
			return Result{"DNS", Pass, fmt.Sprintf("%s -> %s", domain, ip)}
		}
	}
	return Result{"DNS", Fail, fmt.Sprintf("%s resolves to %s, but this server is %s", domain, strings.Join(resolved, ","), publicIP)}
}

// Summarize returns counts and whether the run failed overall.
func Summarize(rs []Result) (pass, warn, fail int, failed bool) {
	for _, r := range rs {
		switch r.Status {
		case Pass:
			pass++
		case Warn:
			warn++
		case Fail:
			fail++
		}
	}
	return pass, warn, fail, fail > 0
}

// ---- real system probes (thin, not unit-tested; logic lives above) ----

func memMB() int {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			var kb int
			if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "MemTotal:")), "%d", &kb); err == nil {
				return kb / 1024
			}
		}
	}
	return 0
}

func freeDiskGB(path string) float64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1
	}
	return float64(st.Bavail) * float64(st.Bsize) / (1 << 30)
}

func portInUse(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}

func dockerInstalled() bool { _, err := exec.LookPath("docker"); return err == nil }

func dockerDaemonUp() bool { return exec.Command("docker", "info").Run() == nil } //nolint:gosec // fixed command

func composeOK() bool { return exec.Command("docker", "compose", "version").Run() == nil } //nolint:gosec // fixed command

func resolveHost(domain string) []string {
	ips, err := net.LookupHost(domain)
	if err != nil {
		return nil
	}
	return ips
}

// doctorResults is what bobres doctor reports: the server checks, the HTTPS
// certificate each served address gets (the install in dir), then the
// hardening state (bobres harden). The installer's preflight (runDoctor)
// has no TLS line: before an install there is nothing to look at.
func doctorResults(dir, domain string, offline bool) []Result {
	h := newHardener(strings.NewReader(""), io.Discard, io.Discard)
	o := newOps(strings.NewReader(""), io.Discard, io.Discard)
	rs := ownGateway(runDoctor(domain, offline), caddyRunning())
	rs = append(rs, hostTLSResults(context.Background(), o, dir, domain)...)
	rs = append(rs, h.report(context.Background())...)
	return append(rs, updaterResults(context.Background(), o, dir)...)
}

// updaterResults is doctor's line about the dashboard's updater for the install in dir: none
// without an install. .env is root's: a user who cannot read it still sees whether the units are
// there.
func updaterResults(ctx context.Context, o *ops, dir string) []Result {
	envPath := filepath.Join(dir, ".env")
	env, ok, err := readEnv(envPath)
	unreadable := err != nil && exists(envPath)
	if o.units == nil || (!ok && !unreadable) {
		return nil
	}
	return []Result{o.units.report(ctx, err == nil && dashboardUpdatesOff(env))}
}

// hostTLSResults looks at the install in dir: the state of its gateway, and
// when that runs, the addresses it serves (core's list, else .env's and the
// domain given) read through this host's port 443. An install without the
// HTTPS manager (tlsAgentOn: its images have no `ssl` command to ask) gets one
// Skip line, and nothing is run in its containers.
func hostTLSResults(ctx context.Context, o *ops, dir, domain string) []Result {
	state := caddyState
	if o.gatewayState != nil {
		state = func() string { return o.gatewayState(ctx) }
	}
	now := time.Now()
	if o.now != nil {
		now = o.now()
	}
	d := tlsDoctor{state: state(), probe: o.probeLocal, now: now}
	if d.state == "running" {
		env, _, _ := readEnv(filepath.Join(dir, ".env")) // unreadable for a user who is not root: only the domain is known then
		if !tlsAgentOn(dir, env) {
			d.noManager = "HTTPS manager not installed yet (release " + orDash(env["BOBRES_VERSION"]) + "): run `sudo bobres update`"
			return d.results(ctx)
		}
		d.addrs = o.servedAddresses(ctx, dir, env)
		if domain = strings.TrimSpace(domain); domain != "" && !slices.ContainsFunc(d.addrs, func(a served) bool { return a.Role == tlsconf.RoleDomain }) {
			d.addrs = append([]served{{domain, tlsconf.RoleDomain}}, d.addrs...)
		}
	}
	return d.results(ctx)
}

// tlsDoctor is doctor's HTTPS check, with what it looks at given.
type tlsDoctor struct {
	// state is the gateway container's state ("running", "restarting",
	// "exited"...; "" when there is none).
	state string
	// noManager, when set, says why there is no HTTPS manager to ask about the
	// addresses (the install predates it).
	noManager string
	addrs     []served
	probe     func(ctx context.Context, address string) tlsprobe.Result
	now       time.Time
}

// results is one line per served address (the domain's is plain "TLS"), or one
// line for the gateway: Skip when it is not running (nothing to look at), Fail
// when it keeps restarting (a crash loop is a broken store, not a stopped one).
func (d tlsDoctor) results(ctx context.Context) []Result {
	switch {
	case d.state == "restarting":
		return []Result{{"TLS", Fail, "the gateway (Caddy) keeps restarting, so HTTPS is down: bobres logs caddy"}}
	case d.state != "running":
		return []Result{{"TLS", Skip, "the gateway (Caddy) is not running"}}
	case d.noManager != "":
		return []Result{{"TLS", Skip, d.noManager}}
	case len(d.addrs) == 0:
		return []Result{{"TLS", Skip, "no domain known (give --domain, or run it as root on the install)"}}
	}
	var rs []Result
	for _, a := range d.addrs {
		name := "TLS"
		if a.Role != tlsconf.RoleDomain {
			name += " " + a.Role
		}
		st, detail := tlsVerdict(d.probe(ctx, a.Host), a.Host, d.now)
		rs = append(rs, Result{name, st, a.Host + ": " + detail})
	}
	return rs
}

// caddyState is the state of the install's gateway container ("" when there
// is none, or Docker does not answer).
func caddyState() string {
	out, err := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project=bobres", //nolint:gosec // fixed command
		"--filter", "label=com.docker.compose.service=caddy", "--format", "{{.State}}").Output()
	return gatewayStateOf(out, err)
}

// gatewayStateOf reads the state out of `docker ps --format {{.State}}`: its
// first line ("" for no container or an error).
func gatewayStateOf(out []byte, err error) string {
	if err != nil {
		return ""
	}
	state, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(state)
}

// ownGateway passes a busy port 80 or 443 when this server's BOBRES gateway
// (Caddy) is running: it is the one holding them on a live install.
func ownGateway(rs []Result, caddyUp bool) []Result {
	if !caddyUp {
		return rs
	}
	for i, r := range rs {
		if r.Status == Fail && strings.HasPrefix(r.Name, "Port ") {
			rs[i] = Result{r.Name, Pass, "in use by this install's gateway (Caddy)"}
		}
	}
	return rs
}

// caddyRunning: the gateway container of the bobres compose project runs.
func caddyRunning() bool {
	out, err := exec.Command("docker", "ps", "-q", "--filter", "label=com.docker.compose.project=bobres", //nolint:gosec // fixed command
		"--filter", "label=com.docker.compose.service=caddy", "--filter", "status=running").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// runDoctor runs every check. offline skips network lookups. The installer's
// preflight runs it too, without the hardening state.
func runDoctor(domain string, offline bool) []Result {
	rs := []Result{
		checkOS(runtime.GOOS, runtime.GOARCH),
		checkRAM(memMB()),
		checkDisk(freeDiskGB("/")),
		checkPort(80, portInUse(80)),
		checkPort(443, portInUse(443)),
	}
	inst := dockerInstalled()
	rs = append(rs, checkDocker(inst, inst && dockerDaemonUp()), checkCompose(inst && composeOK()))
	if offline || domain == "" {
		rs = append(rs, Result{"DNS", Skip, "offline or no --domain"})
	} else {
		rs = append(rs, checkDNS(domain, resolveHost(domain), publicIP()))
	}
	return rs
}
