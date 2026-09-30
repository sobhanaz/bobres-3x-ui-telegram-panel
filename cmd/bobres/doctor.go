package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
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

const (
	minRAMMB    = 1024
	warnRAMMB   = 2048
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
		return Result{"RAM", Fail, fmt.Sprintf("%d MB, need at least %d MB", mb, minRAMMB)}
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

// runDoctor runs every check. offline skips network lookups.
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
