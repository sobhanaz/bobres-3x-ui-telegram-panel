package collect

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// Collector makes snapshots. Every source is a field, so a test replaces the
// machine with recorded files and a fake engine.
type Collector struct {
	// InstallDir is the install (BOBRES's directory): its filesystem is
	// reported. Expected are the Compose services the install is meant to run.
	InstallDir string
	Expected   []string
	// Previous is the last snapshot, if it can be read: CPU shares are the
	// change since it, and `system df` is kept from it while it is recent.
	Previous *snapshot.Snapshot
	// Version is this bobres CLI's version.
	Version  string
	Interval time.Duration

	FS       ReadFS
	Statfs   StatfsFunc
	Resolve  func(string) (string, error)
	Hostname func() (string, error)
	// Docker is nil where there is no engine to ask (the section is left out).
	Docker DockerAPI
	// Run runs systemctl, journalctl, timedatectl and kubectl; Look finds
	// programs. A nil Run leaves those sections out.
	Run  Runner
	Look func(string) (string, error)
	Now  func() time.Time
	// GOARCH names the architecture.
	GOARCH string

	// DFEvery is how long `system df` is kept; JournalWindow how far back
	// errors are counted.
	DFEvery       time.Duration
	JournalWindow time.Duration
}

// New returns a collector reading this machine.
func New(installDir, version string) *Collector {
	return &Collector{
		InstallDir: installDir, Version: version, Interval: snapshot.Interval,
		FS: OSFS{}, Statfs: OSStatfs, Resolve: defaultResolve, Hostname: os.Hostname,
		Docker: NewDockerSocket(SocketFromEnv(os.Getenv("DOCKER_HOST"))),
		Run:    ExecRunner(maxKubeOutput), Look: LookTool, Now: time.Now, GOARCH: runtime.GOARCH,
		DFEvery: 15 * time.Minute, JournalWindow: time.Hour,
	}
}

// Collect reads everything at once (each source within its own time limit) and
// puts it in a snapshot. It never fails as a whole: what could not be read is
// left out and named in Errors.
func (c *Collector) Collect(ctx context.Context) *snapshot.Snapshot {
	start := c.Now()
	s := &snapshot.Snapshot{Schema: snapshot.Schema, GeneratedAt: start.Unix(), GeneratedMS: start.UnixMilli(),
		IntervalS: int(c.Interval.Seconds()), Version: c.Version}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		dres    dockerResult
		host    *snapshot.Host
		units   []snapshot.Unit
		journal *snapshot.Journal
		clock   *snapshot.Clock
		kube    *snapshot.Kube
	)
	note := func(msgs ...string) {
		mu.Lock()
		s.Errors = append(s.Errors, msgs...)
		mu.Unlock()
	}
	wg.Go(func() {
		var errs []string
		host, errs = c.readHost(start)
		note(errs...)
	})
	if c.Docker != nil {
		wg.Go(func() {
			dres = collectDocker(ctx, dockerOptions{api: c.Docker, expected: c.Expected, prev: c.Previous, now: start, dfEvery: c.DFEvery,
				logWindow: c.JournalWindow, gatewayService: "caddy"})
			note(dres.errs...)
		})
	}
	if c.Run != nil {
		wg.Go(func() {
			var why string
			units, why = collectUnits(ctx, c.Run, c.uptime(), start)
			if why != "" {
				note(why)
			}
		})
		wg.Go(func() { journal = collectJournal(ctx, c.Run, c.JournalWindow) })
		wg.Go(func() { clock = collectClock(ctx, c.Run) })
		wg.Go(func() { kube = collectKube(ctx, KubeProbe{Look: c.Look, Run: c.Run}) })
	}
	wg.Wait()

	if host != nil {
		host.Clock = clock
		c.addDisks(host, dres.rootDir, &s.Errors)
		s.Host = host
	}
	s.Docker = dres.section
	s.Units = units
	s.Journal = journal
	if dres.gateway != nil {
		if s.Journal == nil {
			s.Journal = &snapshot.Journal{WindowS: int(c.JournalWindow.Seconds())}
		}
		s.Journal.Sources = append(s.Journal.Sources, *dres.gateway)
	}
	s.Kubernetes = kube
	s.Update = c.lastUpdate()
	slices.Sort(s.Errors)
	s.Errors = slices.Compact(s.Errors)
	s.DurationMS = c.Now().Sub(start).Milliseconds()
	return s
}

func (c *Collector) uptime() time.Duration {
	b, err := c.FS.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	n, err := parseUptime(b)
	if err != nil {
		return 0
	}
	return time.Duration(n) * time.Second
}

// readHost reads the machine's own figures. The filesystems are added once
// Docker's data root is known (addDisks).
func (c *Collector) readHost(now time.Time) (*snapshot.Host, []string) {
	var errs []string
	h := &snapshot.Host{Arch: c.GOARCH}
	read := func(name string) []byte {
		b, err := c.FS.ReadFile(name)
		if err != nil {
			errs = append(errs, "host: "+filepath.Base(name)+" could not be read")
			return nil
		}
		return b
	}
	if b := read("/proc/stat"); b != nil {
		t, cores, err := parseStat(b)
		if err != nil {
			errs = append(errs, "host: cpu could not be read")
		} else {
			h.Cores = cores
			h.CPU = snapshot.CPU{Busy: t.busy, Total: t.total}
			if p := c.Previous; p != nil && p.Host != nil {
				if gap := now.Sub(time.UnixMilli(p.GeneratedMS)); gap >= time.Second && gap <= snapshot.MaxRateGap {
					h.CPU.Pct = snapshot.CPUPct(p.Host.CPU.Busy, p.Host.CPU.Total, t.busy, t.total)
				}
			}
		}
	}
	if h.Cores == 0 {
		h.Cores = runtime.NumCPU()
	}
	if b := read("/proc/meminfo"); b != nil {
		mem, swap, err := parseMeminfo(b)
		if err != nil {
			errs = append(errs, "host: memory could not be read")
		}
		h.Mem, h.Swap = mem, swap
	}
	if b := read("/proc/loadavg"); b != nil {
		if l, err := parseLoadavg(b); err == nil {
			h.Load = l
		}
	}
	if b := read("/proc/uptime"); b != nil {
		h.UptimeS, _ = parseUptime(b)
	}
	if b := read("/proc/net/dev"); b != nil {
		h.Net.Ifaces = parseNetDev(b)
	}
	if b, err := c.FS.ReadFile("/etc/os-release"); err == nil {
		h.OS = parseOSRelease(b)
	}
	if b, err := c.FS.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		h.Kernel = strings.TrimSpace(string(b))
	}
	if name, err := c.Hostname(); err == nil {
		h.Hostname = name
	}
	if h.Mem.Total == 0 && h.CPU.Total == 0 {
		// Neither the processor nor the memory could be read: this is not a
		// Linux host with /proc, and a section of zeros would pass for figures.
		return nil, append(errs, "host: /proc is not readable here")
	}
	return h, errs
}

// addDisks reports the filesystems of the install directory and of Docker's
// data root, each by its mount point, one entry (with both roles) when they are
// the same filesystem.
func (c *Collector) addDisks(h *snapshot.Host, dockerRoot string, errs *[]string) {
	b, err := c.FS.ReadFile("/proc/self/mountinfo")
	if err != nil {
		*errs = append(*errs, "host: mountinfo could not be read")
		return
	}
	mounts := parseMountinfo(b)
	targets := []struct{ path, role string }{{c.InstallDir, "install"}}
	if dockerRoot == "" {
		dockerRoot = "/var/lib/docker"
	}
	targets = append(targets, struct{ path, role string }{dockerRoot, "docker"})
	for _, t := range targets {
		if t.path == "" {
			continue
		}
		p, err := c.Resolve(t.path)
		if err != nil {
			continue // the directory is not there (Docker's, on a host without Docker)
		}
		m, ok := mountFor(mounts, p)
		if !ok {
			continue
		}
		if i := slices.IndexFunc(h.Disks, func(d snapshot.Disk) bool { return d.Mount == m.point }); i >= 0 {
			if !slices.Contains(h.Disks[i].Roles, t.role) {
				h.Disks[i].Roles = append(h.Disks[i].Roles, t.role)
			}
			continue
		}
		st, err := c.Statfs(m.point)
		if err != nil {
			*errs = append(*errs, "host: the disk of the "+t.role+" directory could not be read")
			continue
		}
		h.Disks = append(h.Disks, snapshot.Disk{Mount: m.point, FS: m.fstype, Total: st.Total, Used: st.Used, Avail: st.Avail,
			InodesTotal: st.Files, InodesUsed: st.Files - min(st.FilesFree, st.Files), Roles: []string{t.role}})
	}
}
