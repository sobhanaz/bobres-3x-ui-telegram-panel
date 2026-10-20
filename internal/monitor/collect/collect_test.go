package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// mapFS is a machine's files.
type mapFS map[string][]byte

func (m mapFS) ReadFile(name string) ([]byte, error) {
	if b, ok := m[name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("%s: %w", name, os.ErrNotExist)
}

// labFS is the lab server's /proc, with a server's mounts.
func labFS(t testing.TB, mountinfo string) mapFS {
	return mapFS{
		"/proc/stat":                 fixture(t, "proc/stat"),
		"/proc/meminfo":              fixture(t, "proc/meminfo"),
		"/proc/loadavg":              fixture(t, "proc/loadavg"),
		"/proc/uptime":               fixture(t, "proc/uptime"),
		"/proc/net/dev":              fixture(t, "proc/net_dev"),
		"/proc/self/mountinfo":       []byte(mountinfo),
		"/proc/sys/kernel/osrelease": fixture(t, "proc/kernel"),
		"/etc/os-release":            fixture(t, "proc/os-release"),
	}
}

// baseCollector reads the lab server's files and has no engine and no
// systemd; a test adds what it needs.
func baseCollector(t testing.TB) *Collector {
	return &Collector{
		InstallDir: "/opt/bobres", Expected: labServices, Version: "0.7.0", Interval: 30 * time.Second,
		FS: labFS(t, serverMountinfo),
		Statfs: func(path string) (Statfs, error) {
			switch path {
			case "/opt/bobres":
				return Statfs{Total: 100 << 30, Used: 40 << 30, Avail: 55 << 30, Files: 1000, FilesFree: 900}, nil
			case "/var/lib/docker":
				return Statfs{Total: 200 << 30, Used: 150 << 30, Avail: 45 << 30, Files: 5000, FilesFree: 4000}, nil
			case "/":
				return Statfs{Total: 20 << 30, Used: 10 << 30, Avail: 9 << 30}, nil
			}
			return Statfs{}, errors.New("no such filesystem")
		},
		Resolve:  func(p string) (string, error) { return p, nil },
		Hostname: func() (string, error) { return "bot-server", nil },
		Look:     func(string) (string, error) { return "", exec.ErrNotFound },
		Now:      func() time.Time { return now0 },
		GOARCH:   "amd64", DFEvery: 15 * time.Minute, JournalWindow: time.Hour,
	}
}

// systemRunner plays systemctl, journalctl and timedatectl with recorded output.
func systemRunner(t testing.TB) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "systemctl":
			return fixture(t, "system/systemctl-show.txt"), nil
		case "journalctl":
			if strings.Contains(strings.Join(args, " "), "docker.service") {
				return fixture(t, "system/journal.jsonl"), nil
			}
			return nil, nil
		case "timedatectl":
			return fixture(t, "system/timedatectl.txt"), nil
		}
		return nil, fmt.Errorf("unexpected program %s", name)
	}
}

func TestACompleteSnapshotOfTheLabServer(t *testing.T) {
	e := newEngine(t)
	e.log = framed(`{"level":"error","ts":1759999900,"msg":"boom"}`)
	c := baseCollector(t)
	c.Docker, c.Run = e.client(), systemRunner(t)
	s := c.Collect(context.Background())

	if err := s.Normalize(now0); err != nil {
		t.Fatalf("what the collector wrote does not pass the reader's checks: %v", err)
	}
	if s.Schema != snapshot.Schema || s.GeneratedAt != now0.Unix() || s.GeneratedMS != now0.UnixMilli() || s.IntervalS != 30 || s.Version != "0.7.0" {
		t.Errorf("header: %+v", s)
	}
	if len(s.Errors) != 0 {
		t.Errorf("errors: %v", s.Errors)
	}
	h := s.Host
	if h == nil || h.Hostname != "bot-server" || h.OS != "Ubuntu 24.04.5 LTS" || h.Kernel != "7.0.14-linuxkit" || h.Arch != "amd64" ||
		h.UptimeS != 58698 || h.Cores != 16 || h.Load != (snapshot.Load{0.29, 0.11, 0.07}) {
		t.Fatalf("host: %+v", h)
	}
	if h.CPU.Pct != nil || h.CPU.Total == 0 {
		t.Errorf("a first snapshot has no CPU share, only the counters: %+v", h.CPU)
	}
	if h.Mem.Total != 8125884*1024 || h.Swap.Total != 1048572*1024 || len(h.Net.Ifaces) != 1 || h.Net.Ifaces[0].Name != "eth0" {
		t.Errorf("memory and network: %+v %+v %+v", h.Mem, h.Swap, h.Net)
	}
	if h.Clock == nil || h.Clock.Synchronized == nil || *h.Clock.Synchronized {
		t.Errorf("clock: %+v", h.Clock)
	}
	// BOBRES's own filesystem and Docker's, each by its mount point.
	if len(h.Disks) != 2 || h.Disks[0].Mount != "/opt/bobres" || h.Disks[0].FS != "ext4" || h.Disks[0].Roles[0] != "install" ||
		h.Disks[0].Total != 100<<30 || h.Disks[0].Used != 40<<30 || h.Disks[0].Avail != 55<<30 || h.Disks[0].InodesUsed != 100 ||
		h.Disks[1].Mount != "/var/lib/docker" || h.Disks[1].FS != "xfs" || h.Disks[1].Roles[0] != "docker" {
		t.Errorf("disks: %+v", h.Disks)
	}
	if d := s.Docker; d == nil || !d.OK || len(d.Containers) != 9 || d.DF == nil || d.Compose == nil || len(d.Compose.Missing) != 2 {
		t.Errorf("docker: %+v", s.Docker)
	}
	if len(s.Units) != 3 || s.Units[0].Name != "bobres-monitor.timer" {
		t.Errorf("units: %+v", s.Units)
	}
	if s.Journal == nil || len(s.Journal.Sources) != 3 || s.Journal.Sources[0].Name != "docker" || s.Journal.Sources[2].Name != "caddy" ||
		s.Journal.Sources[2].Errors != 1 {
		t.Errorf("journal: %+v", s.Journal)
	}
	if k := s.Kubernetes; k == nil || k.Detected || k.Reason == "" {
		t.Errorf("kubernetes: %+v", k)
	}
	b, err := json.Marshal(s)
	if err != nil || len(b) > 24<<10 {
		t.Errorf("the snapshot of a nine-container install is %d bytes (%v)", len(b), err)
	}
}

// The second snapshot knows how much of the processor was used since the first.
func TestCPUShareBetweenTwoRuns(t *testing.T) {
	c := baseCollector(t)
	first := c.Collect(context.Background())

	later := now0.Add(30 * time.Second)
	c.Now = func() time.Time { return later }
	c.Previous = first
	fs := labFS(t, serverMountinfo)
	// Another 15 busy and 285 idle jiffies on the machine-wide line.
	stat := strings.Replace(string(fs["/proc/stat"]), "cpu  337877 6 297452 92517692 26806 0 23358 0 0 0", "cpu  337892 6 297452 92517977 26806 0 23358 0 0 0", 1)
	fs["/proc/stat"] = []byte(stat)
	c.FS = fs
	second := c.Collect(context.Background())
	if p := second.Host.CPU.Pct; p == nil || *p < 4.99 || *p > 5.01 {
		t.Errorf("cpu share: %v", p)
	}
	// Run twice in the same second, or after a long stop: no share.
	c.Now = func() time.Time { return now0.Add(500 * time.Millisecond) }
	if p := c.Collect(context.Background()).Host.CPU.Pct; p != nil {
		t.Errorf("a share over half a second: %v", *p)
	}
	c.Now = func() time.Time { return now0.Add(time.Hour) }
	if p := c.Collect(context.Background()).Host.CPU.Pct; p != nil {
		t.Errorf("a share over an hour: %v", *p)
	}
}

func TestDisksOfInstallAndDocker(t *testing.T) {
	oneFS := "22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw\n"
	for name, tc := range map[string]struct {
		mountinfo string
		dockerErr bool
		want      []string // mount:roles
	}{
		"both on one filesystem": {oneFS, false, []string{"/:install,docker"}},
		"each on its own":        {serverMountinfo, false, []string{"/opt/bobres:install", "/var/lib/docker:docker"}},
		"docker root missing":    {oneFS, true, []string{"/:install"}},
	} {
		c := baseCollector(t)
		c.FS = labFS(t, tc.mountinfo)
		if tc.dockerErr {
			c.Resolve = func(p string) (string, error) {
				if p == "/var/lib/docker" {
					return "", os.ErrNotExist
				}
				return p, nil
			}
		}
		var got []string
		for _, d := range c.Collect(context.Background()).Host.Disks {
			got = append(got, d.Mount+":"+strings.Join(d.Roles, ","))
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}

	// The filesystem cannot be read, or the mounts: the host is still reported.
	c := baseCollector(t)
	c.Statfs = func(string) (Statfs, error) { return Statfs{}, errors.New("EIO") }
	s := c.Collect(context.Background())
	if s.Host == nil || len(s.Host.Disks) != 0 || len(s.Errors) == 0 {
		t.Errorf("statfs failing: %+v %v", s.Host, s.Errors)
	}
	c = baseCollector(t)
	delete(c.FS.(mapFS), "/proc/self/mountinfo")
	if s := c.Collect(context.Background()); s.Host == nil || len(s.Host.Disks) != 0 || !strings.Contains(strings.Join(s.Errors, "|"), "mountinfo") {
		t.Errorf("no mountinfo: %v", s.Errors)
	}
}

func TestHostWithoutProc(t *testing.T) {
	c := baseCollector(t)
	c.FS = mapFS{}
	s := c.Collect(context.Background())
	if s.Host != nil || !strings.Contains(strings.Join(s.Errors, "|"), "/proc is not readable here") {
		t.Errorf("host: %+v errors: %v", s.Host, s.Errors)
	}
	if err := s.Normalize(now0); err != nil {
		t.Errorf("a snapshot without a host section is still a snapshot: %v", err)
	}
}

func TestHalfTheSourcesMissing(t *testing.T) {
	c := baseCollector(t)
	c.Docker = &rendezvousDownDocker{}
	c.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound }
	s := c.Collect(context.Background())
	if s.Docker == nil || s.Docker.OK || s.Docker.Error == "" || s.Docker.Compose != nil {
		t.Errorf("docker: %+v", s.Docker)
	}
	if len(s.Units) != 0 || s.Journal != nil || s.Host.Clock != nil {
		t.Errorf("units %v journal %v clock %v", s.Units, s.Journal, s.Host.Clock)
	}
	if got := strings.Join(s.Errors, "|"); !strings.Contains(got, "docker: docker did not answer") || !strings.Contains(got, "systemd: not available") {
		t.Errorf("errors: %q", got)
	}
}

type rendezvousDownDocker struct{ rendezvousDocker }

func (r *rendezvousDownDocker) Version(context.Context) (EngineVersion, error) {
	return EngineVersion{}, errors.New("down")
}

// A hung engine or systemd costs its own section, not the run: the outer
// limit bounds everything.
func TestAHungSourceCannotHoldTheRun(t *testing.T) {
	c := baseCollector(t)
	var asked atomic.Int32
	hang := func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		asked.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	c.Run = hang
	c.Docker = &hangingDocker{}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	s := c.Collect(ctx)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the run took %v", took)
	}
	if s.Host == nil || s.Docker == nil || s.Docker.OK || asked.Load() == 0 {
		t.Errorf("%+v asked %d", s.Docker, asked.Load())
	}
	if !strings.Contains(strings.Join(s.Errors, "|"), "did not answer in time") {
		t.Errorf("errors: %v", s.Errors)
	}
}

type hangingDocker struct{ rendezvousDocker }

func (*hangingDocker) Version(ctx context.Context) (EngineVersion, error) {
	<-ctx.Done()
	return EngineVersion{}, ctx.Err()
}

func TestRealConstructorWiresRealSources(t *testing.T) {
	c := New("/opt/bobres", "1.2.3")
	if c.FS == nil || c.Statfs == nil || c.Resolve == nil || c.Docker == nil || c.Run == nil || c.Look == nil || c.Now == nil ||
		c.Interval != 30*time.Second || c.DFEvery != 15*time.Minute || c.Version != "1.2.3" {
		t.Errorf("%+v", c)
	}
}

func TestLookToolOnlySearchesTheFixedDirectories(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if p, err := LookTool("sh"); err != nil || !strings.HasPrefix(p, "/bin/") && !strings.HasPrefix(p, "/usr/bin/") {
		t.Errorf("sh: %q %v", p, err)
	}
	if _, err := LookTool("definitely-not-a-program-xyz"); err == nil {
		t.Error("found a program that does not exist")
	}
}

func TestExecRunnerKeepsOutputBoundedAndEnvironmentSmall(t *testing.T) {
	t.Setenv("SECRET_FOR_TEST", "must-not-reach-the-child")
	run := ExecRunner(64)
	out, err := run(context.Background(), "sh", "-c", "env")
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	if strings.Contains(string(out), "must-not-reach-the-child") {
		t.Errorf("the child saw the caller's environment:\n%s", out)
	}
	if _, err := ExecRunner(10)(context.Background(), "sh", "-c", "echo 0123456789abcdef"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too much output: %v", err)
	}
	out, err = ExecRunner(1024)(context.Background(), "sh", "-c", "echo hi; echo hidden >&2")
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Errorf("stdout only: %q %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = ExecRunner(1024)(ctx, "sleep", "5")
	if time.Since(start) > 3*time.Second {
		t.Error("a program that sleeps was not stopped")
	}
}
