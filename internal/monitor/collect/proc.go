package collect

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// ReadFS reads the files the host section comes from (/proc, /etc/os-release).
type ReadFS interface {
	ReadFile(name string) ([]byte, error)
}

// maxProcFile bounds a file read from /proc (mountinfo of a host running many
// containers is the largest, a few hundred kilobytes).
const maxProcFile = 4 << 20

// OSFS reads the real files, at most maxProcFile bytes each.
type OSFS struct{}

// ReadFile implements ReadFS.
func (OSFS) ReadFile(name string) ([]byte, error) {
	f, err := os.Open(name) //nolint:gosec // a fixed /proc or /etc path
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	b, err := io.ReadAll(io.LimitReader(f, maxProcFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxProcFile {
		return nil, fmt.Errorf("%s: larger than %d bytes", name, maxProcFile)
	}
	return b, nil
}

// cpuTimes are the cumulative jiffies of /proc/stat's first line.
type cpuTimes struct{ busy, total uint64 }

// parseStat reads the machine-wide CPU line and the number of cores.
func parseStat(b []byte) (cpuTimes, int, error) {
	var (
		t     cpuTimes
		cores int
		found bool
	)
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		if f[0] != "cpu" {
			if _, err := strconv.Atoi(f[0][3:]); err == nil {
				cores++
			}
			continue
		}
		var v [10]uint64
		if len(f) < 5 {
			return t, 0, errors.New("/proc/stat: short cpu line")
		}
		for i := 1; i < len(f) && i <= len(v); i++ {
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				return t, 0, fmt.Errorf("/proc/stat: %w", err)
			}
			v[i-1] = n
		}
		// user nice system idle iowait irq softirq steal (guest time is already in user and nice).
		idle := v[3] + v[4]
		t.busy = v[0] + v[1] + v[2] + v[5] + v[6] + v[7]
		t.total = t.busy + idle
		found = true
	}
	if !found {
		return t, 0, errors.New("/proc/stat: no cpu line")
	}
	return t, cores, nil
}

// parseMeminfo reads memory and swap, in bytes. MemAvailable is the kernel's
// estimate of what can be used without swapping; a kernel that predates it
// (before 3.14) is estimated from free, buffers and cache.
func parseMeminfo(b []byte) (snapshot.Mem, snapshot.Swap, error) {
	kb := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		kb[name] = n
	}
	total, ok := kb["MemTotal"]
	if !ok || total == 0 {
		return snapshot.Mem{}, snapshot.Swap{}, errors.New("/proc/meminfo: no MemTotal")
	}
	avail, ok := kb["MemAvailable"]
	if !ok {
		avail = kb["MemFree"] + kb["Buffers"] + kb["Cached"]
	}
	avail = min(avail, total)
	mem := snapshot.Mem{Total: total * 1024, Available: avail * 1024}
	swapTotal, swapFree := kb["SwapTotal"], min(kb["SwapFree"], kb["SwapTotal"])
	return mem, snapshot.Swap{Total: swapTotal * 1024, Used: (swapTotal - swapFree) * 1024}, nil
}

func parseLoadavg(b []byte) (snapshot.Load, error) {
	f := strings.Fields(string(b))
	var l snapshot.Load
	if len(f) < 3 {
		return l, errors.New("/proc/loadavg: short")
	}
	for i := range l {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return l, fmt.Errorf("/proc/loadavg: %w", err)
		}
		l[i] = v
	}
	return l, nil
}

func parseUptime(b []byte) (int64, error) {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, errors.New("/proc/uptime: empty")
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 {
		return 0, errors.New("/proc/uptime: not a number")
	}
	return int64(v), nil
}

// virtualIfacePrefixes name interfaces that are not the machine's own network
// (with "lo", the loopback, which is matched whole): Docker's and libvirt's
// bridges, veth pairs, tunnels, the CNI and Kubernetes overlays. Their traffic
// would count twice (once on the bridge, once on the real interface) or not be
// the server's at all.
var virtualIfacePrefixes = []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "vxlan", "kube", "tunl", "gre",
	"erspan", "ip_vti", "ip6_vti", "sit", "ip6tnl", "ip6gre", "dummy", "ifb", "lxc", "vmnet", "nodelocaldns", "cilium", "weave",
	"podman", "tun", "tap", "wg", "zt", "tailscale"}

// parseNetDev reads the byte counters of the real interfaces of /proc/net/dev.
func parseNetDev(b []byte) []snapshot.Iface {
	var out []snapshot.Iface
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || virtualIface(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, snapshot.Iface{Name: name, RxBytes: rx, TxBytes: tx})
	}
	return out
}

func virtualIface(name string) bool {
	if name == "lo" {
		return true
	}
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// mountEntry is one line of /proc/self/mountinfo.
type mountEntry struct{ point, fstype string }

// parseMountinfo reads the mount points and filesystem types.
func parseMountinfo(b []byte) []mountEntry {
	var out []mountEntry
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		sep := slices.Index(f, "-")
		if sep < 6 || sep+1 >= len(f) {
			continue
		}
		out = append(out, mountEntry{point: unescapeMount(f[4]), fstype: f[sep+1]})
	}
	return out
}

// unescapeMount turns the octal escapes of a mount point (\040 for a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mountFor finds the mount point of the filesystem holding path: the deepest
// mount point that contains it (the last one listed when mounts are stacked).
func mountFor(mounts []mountEntry, path string) (mountEntry, bool) {
	var best mountEntry
	found := false
	for _, m := range mounts {
		inside := m.point == "/" || path == m.point || strings.HasPrefix(path, strings.TrimSuffix(m.point, "/")+"/")
		if inside && (!found || len(m.point) >= len(best.point)) {
			best, found = m, true
		}
	}
	return best, found
}

// parseOSRelease is the PRETTY_NAME of /etc/os-release (else NAME and
// VERSION_ID).
func parseOSRelease(b []byte) string {
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		kv[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	if v := kv["PRETTY_NAME"]; v != "" {
		return v
	}
	return strings.TrimSpace(kv["NAME"] + " " + kv["VERSION_ID"])
}

// Statfs is a filesystem's size and use.
type Statfs struct {
	Total, Used, Avail, Files, FilesFree uint64
}

// StatfsFunc reads a filesystem's figures.
type StatfsFunc func(path string) (Statfs, error)

// OSStatfs is statfs(2).
func OSStatfs(path string) (Statfs, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Statfs{}, err
	}
	bs := uint64(st.Bsize) //nolint:gosec // a block size is positive
	blocks, free, avail := uint64(st.Blocks), uint64(st.Bfree), uint64(st.Bavail)
	free = min(free, blocks)
	return Statfs{Total: blocks * bs, Used: (blocks - free) * bs, Avail: avail * bs, Files: uint64(st.Files), FilesFree: uint64(st.Ffree)}, nil //nolint:gosec // counts
}

func defaultResolve(p string) (string, error) { return filepath.EvalSymlinks(p) }
