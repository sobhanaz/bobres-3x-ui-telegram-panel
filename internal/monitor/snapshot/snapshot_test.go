package snapshot

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Unix(1_760_000_000, 0)

// Characters that must not survive Clean, written as numbers so the source shows what they are.
var (
	rlo  = string(rune(0x202e)) // right-to-left override
	zwsp = string(rune(0x200b)) // zero-width space
	zwnj = string(rune(0x200c)) // zero-width non-joiner
	bom  = string(rune(0xfeff)) // byte order mark
	lsep = string(rune(0x2028)) // line separator
)

func ptr[T any](v T) *T { return &v }

// sample is a snapshot a healthy install would produce.
func sample() *Snapshot {
	return &Snapshot{
		Schema: Schema, GeneratedAt: t0.Unix(), GeneratedMS: t0.UnixMilli(), IntervalS: 30, DurationMS: 310, Version: "0.7.0",
		Host: &Host{
			Hostname: "bot-server", OS: "Ubuntu 24.04.5 LTS", Kernel: "6.8.0-45-generic", Arch: "amd64", UptimeS: 58698, Cores: 2,
			CPU: CPU{Pct: ptr(12.5), Busy: 400, Total: 4000}, Load: Load{0.29, 0.11, 0.07},
			Mem:   Mem{Total: 8 << 30, Available: 6 << 30},
			Swap:  Swap{Total: 1 << 30, Used: 1 << 20},
			Disks: []Disk{{Mount: "/", FS: "ext4", Total: 100 << 30, Used: 40 << 30, Avail: 55 << 30, Roles: []string{"install", "docker"}}},
			Net:   Net{Ifaces: []Iface{{Name: "eth0", RxBytes: 1000, TxBytes: 2000}}},
			Clock: &Clock{Synchronized: ptr(true), NTP: ptr(true)},
		},
		Docker: &Docker{OK: true, Version: "29.9.0", API: "1.56", Containers: []Container{
			{ID: "995876ae1421", Name: "bobres-core-1", Project: "bobres", Service: "core", Image: "ghcr.io/sobhanaz/bobres-core:0.2.0",
				State: StateRunning, Health: HealthHealthy, Restarts: 0, StartedAt: t0.Unix() - 3600, CPUNS: 5, CPUPct: ptr(1.5),
				MemBytes: 28 << 20, MemLimit: 512 << 20, NetRx: 10, NetTx: 20, PIDs: 23},
		}, DF: &DF{ImagesBytes: 1, CollectedAt: t0.Unix()}, Compose: &Compose{Expected: []string{"core", "caddy"}, Missing: []string{"caddy"}}},
		Units:   []Unit{{Name: "bobres-monitor.timer", Load: "loaded", Active: "active", Sub: "waiting", Enabled: "enabled"}},
		Journal: &Journal{WindowS: 3600, Sources: []JournalSource{{Name: "docker", Errors: 2, Lines: []JournalLine{{At: t0.Unix(), Msg: "something failed"}}}}},
	}
}

func TestWriteThenReadKeepsTheSnapshot(t *testing.T) {
	dir := t.TempDir()
	want := sample()
	if err := Write(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir, t0.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Fatalf("round trip changed it:\nwrote %s\nread  %s", a, b)
	}
	fi, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode: %v %v", fi, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestReadMissingFileIsNotExist(t *testing.T) {
	_, err := Read(t.TempDir(), t0)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
	if Exists(t.TempDir()) {
		t.Fatal("Exists on an empty directory")
	}
}

// Whatever is in the file, Read does not trust it.
func TestDecodeRefusesWhatIsNotASnapshot(t *testing.T) {
	good := sample()
	future := *good
	future.GeneratedAt = t0.Add(time.Hour).Unix()
	zero := *good
	zero.GeneratedAt = 0
	other := *good
	other.Schema = 2
	enc := func(s *Snapshot) string { b, _ := json.Marshal(s); return string(b) }
	cases := map[string]struct {
		in   string
		want error
	}{
		"empty":                  {"", ErrInvalid},
		"not json":               {"<html>", ErrInvalid},
		"an array":               {"[1,2]", ErrInvalid},
		"null":                   {"null", ErrSchema},
		"another schema":         {enc(&other), ErrSchema},
		"no schema":              {`{"generated_at": 1760000000}`, ErrSchema},
		"no time":                {enc(&zero), ErrInvalid},
		"a time from the future": {enc(&future), ErrInvalid},
		"a negative counter":     {strings.Replace(enc(good), `"rx_bytes":1000`, `"rx_bytes":-1`, 1), ErrInvalid},
		"a number as text":       {strings.Replace(enc(good), `"cores":2`, `"cores":"2"`, 1), ErrInvalid},
		"trailing garbage":       {enc(good) + "{}", ErrInvalid},
		"too large":              {strings.Repeat(" ", MaxFileBytes+1), ErrTooLarge},
	}
	for name, c := range cases {
		if _, err := Decode([]byte(c.in), t0); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
	// Fields a newer collector adds are ignored.
	if _, err := Decode([]byte(strings.Replace(enc(good), `"host":`, `"later":{"x":1},"host":`, 1)), t0); err != nil {
		t.Errorf("an unknown field: %v", err)
	}
}

func TestNormalizeBoundsAndCleansEverything(t *testing.T) {
	s := sample()
	huge := strings.Repeat("x", 5000)
	bidi := "boo" + rlo + "k" + zwsp
	s.Version = "0.7.0 && rm -rf /"
	s.Errors = []string{"docker: <script>alert(1)</script>", bidi, "", huge}
	s.GeneratedMS = 12 // nonsense: replaced by the time in seconds
	s.IntervalS, s.DurationMS = 0, -4
	s.Host.Hostname = "bad host name"
	s.Host.OS = "Ubuntu\n24.04" + bidi
	s.Host.Kernel = "6.8; curl evil"
	s.Host.Cores = -3
	s.Host.CPU.Pct = ptr(math.NaN())
	s.Host.Load = Load{math.Inf(1), -1, 1e9}
	s.Host.Mem = Mem{Total: 10, Available: 20}
	s.Host.Swap = Swap{Total: 1, Used: 2}
	s.Host.Disks = []Disk{
		{Mount: "/", Total: 1, Roles: []string{"install", "docker", "evil", "install"}},
		{Mount: "/", Total: 2},                      // a repeat
		{Mount: "relative/path"},                    // not a mount point
		{Mount: "/data/../etc"},                     // climbs
		{Mount: "/with space"},                      // not shaped like a mount
		{Mount: "/mnt/" + strings.Repeat("a", 200)}, // too long
		{Mount: "/var/lib/docker", FS: "ext4; drop"},
	}
	s.Host.Net.Ifaces = []Iface{{Name: "eth0"}, {Name: "eth0"}, {Name: "bad name"}, {Name: ""}}
	var cs []Container
	for i := 0; i < MaxContainers+10; i++ {
		id := []byte("000000000000")
		id[11] = "0123456789abcdef"[i%16]
		id[10] = "0123456789abcdef"[(i/16)%16]
		cs = append(cs, Container{ID: string(id), Name: "c" + string(id), State: "running", Health: "healthy"})
	}
	cs = append(cs,
		Container{ID: "NOT-AN-ID", Name: "x"},
		Container{ID: "aaaaaaaaaaaa", Name: "bad name with spaces"},
		Container{ID: "bbbbbbbbbbbb", Name: "ok", Project: "p q", Service: "s/t", Image: "img with space", State: "weird", Health: "very",
			Restarts: -5, CPUPct: ptr(-1.0), ExitCode: ptr(9999)},
	)
	s.Docker.Containers = cs
	s.Docker.Version = "29.9.0\n"
	s.Docker.Error = huge
	s.Docker.Compose = &Compose{Expected: []string{"core", "core", "bad name", "caddy"}, Missing: []string{"x y"}}
	s.Units = []Unit{{Name: "ok.service", Load: "loaded", Active: "active; evil"}, {Name: "bad unit"}}
	s.Journal = &Journal{WindowS: -1, Sources: []JournalSource{{Name: "docker", Errors: -4, Lines: []JournalLine{
		{At: 1, Msg: "<b>one</b>"}, {Msg: ""}, {Msg: huge}, {Msg: "3"}, {Msg: "4"}, {Msg: "5"}, {Msg: "6"}, {Msg: "7"}}}, {Name: "bad name"}}}
	s.Kubernetes = &Kube{Detected: true, Via: "sudo kubectl", Nodes: []KubeNode{{Name: "n1", Roles: "control-plane", Version: "v1.30"}, {Name: "BAD NODE"}},
		Pods: []KubePod{{Namespace: "kube-system", Name: "coredns-abc", Phase: "Running"}, {Namespace: "x", Name: "Bad_Name"}, {Namespace: "a", Name: "b", Phase: "Hacked", Restarts: -1}}}

	if err := s.Normalize(t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	check := func(name string, ok bool) {
		t.Helper()
		if !ok {
			t.Errorf("%s", name)
		}
	}
	check("version dropped", s.Version == "")
	check("errors cleaned and bounded", len(s.Errors) == 3 && !strings.ContainsAny(strings.Join(s.Errors, ""), "<>"+rlo+zwsp) && len([]rune(s.Errors[2])) <= 161)
	check("time in ms repaired", s.GeneratedMS == s.GeneratedAt*1000)
	check("interval and duration clamped", s.IntervalS == 1 && s.DurationMS == 0)
	h := s.Host
	check("hostname", h.Hostname == "")
	check("os line", h.OS == "Ubuntu 24.04book")
	check("kernel", h.Kernel == "")
	check("cores", h.Cores == 0)
	check("cpu pct unknown", h.CPU.Pct == nil)
	check("load clamped", h.Load == Load{0, 0, 1e5})
	check("memory unknown when impossible", h.Mem == Mem{})
	check("swap unknown when impossible", h.Swap == Swap{})
	check("disks: root once with its roles, docker root", len(h.Disks) == 2 && h.Disks[0].Mount == "/" && len(h.Disks[0].Roles) == 2 &&
		h.Disks[1].Mount == "/var/lib/docker" && h.Disks[1].FS == "")
	check("ifaces", len(h.Net.Ifaces) == 1)
	d := s.Docker
	check("container count capped and flagged", len(d.Containers) <= MaxContainers && d.Truncated)
	check("docker version", d.Version == "29.9.0")
	check("docker error bounded", len([]rune(d.Error)) <= 161)
	check("compose services", strings.Join(d.Compose.Expected, ",") == "core,caddy" && len(d.Compose.Missing) == 0)
	check("units", len(s.Units) == 1 && s.Units[0].Active == "")
	check("journal", s.Journal.WindowS == 0 && len(s.Journal.Sources) == 1 && s.Journal.Sources[0].Errors == 0 &&
		len(s.Journal.Sources[0].Lines) == MaxJournalLines && s.Journal.Sources[0].Lines[0].Msg == "‹b›one‹/b›")
	for _, l := range s.Journal.Sources[0].Lines {
		check("journal line bounded", len([]rune(l.Msg)) <= MaxLine+1)
	}
	k := s.Kubernetes
	check("kube", k.Via == "" && len(k.Nodes) == 1 && len(k.Pods) == 2 && k.Pods[1].Phase == "Unknown" && k.Pods[1].Restarts == 0)
}

func TestNormalizeContainerFields(t *testing.T) {
	s := sample()
	s.Docker.Containers = []Container{{ID: "bbbbbbbbbbbb", Name: "ok", Project: "p q", Service: "s/t", Image: "img with space", State: "weird", Health: "very",
		Restarts: -5, CPUPct: ptr(-1.0), ExitCode: ptr(9999), PIDs: -2}}
	if err := s.Normalize(t0); err != nil {
		t.Fatal(err)
	}
	c := s.Docker.Containers[0]
	if c.Project != "" || c.Service != "" || c.Image != "" || c.State != StateUnknown || c.Health != HealthNone || c.Restarts != 0 ||
		c.CPUPct != nil || *c.ExitCode != 255 || c.PIDs != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestReadRefusesLinksDirectoriesAndHugeFiles(t *testing.T) {
	good, _ := json.Marshal(sample())

	linked := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(elsewhere, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(linked, FileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(linked, t0); err == nil {
		t.Error("a symbolic link was followed")
	}

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir, t0); !errors.Is(err, ErrInvalid) {
		t.Errorf("a directory: %v", err)
	}

	big := t.TempDir()
	if err := os.WriteFile(filepath.Join(big, FileName), []byte(strings.Repeat("x", MaxFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(big, t0); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a huge file: %v", err)
	}
}

func TestWriteRefusesWhatIsTooLarge(t *testing.T) {
	s := sample()
	s.Errors = nil
	// Bounded fields cannot make a snapshot that large, so make it past Normalize.
	s.Host.OS = strings.Repeat("x", MaxFileBytes)
	if err := Write(t.TempDir(), s); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
}

// A reader that opens the file while the collector replaces it sees a whole
// snapshot every time.
func TestReadersNeverSeeAHalfWrittenSnapshot(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, sample()); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad error
	var mu sync.Mutex
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := Read(dir, t0); err != nil {
					mu.Lock()
					bad = err
					mu.Unlock()
					return
				}
			}
		}()
	}
	for i := range 60 {
		s := sample()
		s.DurationMS = int64(i)
		if err := Write(dir, s); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if bad != nil {
		t.Fatalf("a reader failed: %v", bad)
	}
}

func TestWriteClearsOldLeftoversOnly(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, tmpPrefix+"aaaa")
	recent := filepath.Join(dir, tmpPrefix+"bbbb")
	other := filepath.Join(dir, "notes.txt")
	for _, p := range []string{old, recent, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, sample()); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{old: false, recent: true, other: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists: %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
	if err := Remove(dir); err != nil || Exists(dir) {
		t.Fatalf("remove: %v", err)
	}
	if err := Remove(dir); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestFreshness(t *testing.T) {
	s := sample()
	for age, want := range map[time.Duration]bool{0: true, 30 * time.Second: true, FreshAge: true, FreshAge + time.Second: false,
		time.Hour: false, -30 * time.Second: true, -10 * time.Minute: false} {
		if got := s.Fresh(t0.Add(age)); got != want {
			t.Errorf("age %v: fresh %v, want %v", age, got, want)
		}
	}
	if s.Age(t0.Add(time.Minute)) != time.Minute {
		t.Error("age")
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"":                              "",
		"  hello \n\t world  ":          "hello world",
		"a\x00b\x07c":                   "a b c",
		"<b>bold</b> & \"quoted\"":      "‹b›bold‹/b› & \"quoted\"",
		"evil" + rlo + "exe.txt":        "evilexe.txt",
		"zero" + zwsp + "width" + bom:   "zerowidth",
		"bad \xff\xfe bytes":            "bad ? bytes",
		"line\r\nbreak" + lsep + "here": "line break here",
		zwsp + zwnj + "  \t":            "",
		"مرحبا بالعالم 🌍":               "مرحبا بالعالم 🌍",
	} {
		if got := Clean(in, 100); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Clean("abcdefghij", 5); got != "abcde…" {
		t.Errorf("cut: %q", got)
	}
	if got := Clean("abcde", 5); got != "abcde" {
		t.Errorf("exactly the limit: %q", got)
	}
	if got := Clean("abcde   ", 5); got != "abcde" {
		t.Errorf("trailing space is not a cut: %q", got)
	}
	if got := Clean("abc def", 5); got != "abc d…" {
		t.Errorf("cut at a space: %q", got)
	}
	if got := Clean("x", 0); got != "" {
		t.Errorf("max 0: %q", got)
	}
	if got := Clean(strings.Repeat("é", 1_000_000), 10); len([]rune(got)) != 11 {
		t.Errorf("long input: %d", len([]rune(got)))
	}
}

func TestLabels(t *testing.T) {
	for in, want := range map[string]bool{
		"core": true, "tls-agent": true, "/": true, "/var/lib/docker": true, "": false, "a:b": false, "/a:b": false,
		"with space": false, "/data/../x": false, strings.Repeat("a", 65): false, "-lead": false,
	} {
		if got := ValidLabel(in); got != want {
			t.Errorf("ValidLabel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDiskPctIsWhatDfShows(t *testing.T) {
	d := Disk{Total: 1000, Used: 450, Avail: 450} // 10% reserved
	if got := d.Pct(); got != 50 {
		t.Errorf("pct = %v", got)
	}
	if (Disk{}).Pct() != 0 {
		t.Error("an empty disk")
	}
	if (Mem{Total: 10, Available: 25}).Used() != 0 {
		t.Error("used when available exceeds total")
	}
}

func TestNetRates(t *testing.T) {
	mk := func(ms int64, ifs ...Iface) *Snapshot {
		return &Snapshot{GeneratedMS: ms, Host: &Host{Net: Net{Ifaces: ifs}}}
	}
	prev := mk(1_000_000, Iface{"eth0", 1000, 2000}, Iface{"eth1", 500, 500})
	rx, tx := NetRates(prev, mk(1_030_000, Iface{"eth0", 4000, 5000}, Iface{"eth1", 500, 500}, Iface{"new0", 9e9, 9e9}))
	if rx == nil || tx == nil || *rx != 100 || *tx != 100 {
		t.Fatalf("rates: %v %v", rx, tx)
	}
	if rx, tx := NetRates(prev, mk(1_030_000, Iface{"eth0", 10, 10})); rx != nil || tx != nil {
		t.Errorf("a reset counter is not a rate: %v %v", rx, tx)
	}
	if rx, _ := NetRates(prev, mk(1_030_000, Iface{"other", 1, 1})); rx != nil {
		t.Error("no interface in common")
	}
	for name, cur := range map[string]*Snapshot{"same time": mk(1_000_000, Iface{"eth0", 2000, 3000}),
		"backwards": mk(900_000, Iface{"eth0", 2000, 3000}), "too far apart": mk(1_000_000+11*60*1000, Iface{"eth0", 2000, 3000})} {
		if rx, tx := NetRates(prev, cur); rx != nil || tx != nil {
			t.Errorf("%s gave a rate", name)
		}
	}
	if rx, _ := NetRates(nil, prev); rx != nil {
		t.Error("no previous snapshot")
	}
	if rx, _ := NetRates(prev, &Snapshot{GeneratedMS: 1_030_000}); rx != nil {
		t.Error("no host section")
	}
}

func TestCPUPercentages(t *testing.T) {
	if p := CPUPct(100, 1000, 150, 1200); p == nil || *p != 25 {
		t.Errorf("host: %v", p)
	}
	for _, c := range [][4]uint64{{100, 1000, 150, 1000}, {100, 1000, 90, 1100}, {100, 1000, 50, 900}} {
		if CPUPct(c[0], c[1], c[2], c[3]) != nil {
			t.Errorf("%v gave a percentage", c)
		}
	}
	if p := ContainerCPUPct(0, uint64(15*time.Second), 30*time.Second); p == nil || *p != 50 {
		t.Errorf("container: %v", p)
	}
	if p := ContainerCPUPct(0, uint64(60*time.Second), 30*time.Second); p == nil || *p != 200 {
		t.Errorf("two cores: %v", p)
	}
	if ContainerCPUPct(10, 5, 30*time.Second) != nil || ContainerCPUPct(0, 5, 0) != nil || ContainerCPUPct(0, 5, time.Hour) != nil {
		t.Error("an unusable span gave a percentage")
	}
	a, b := &Snapshot{GeneratedMS: 1000}, &Snapshot{GeneratedMS: 31000}
	if Gap(a, b) != 30*time.Second || Gap(b, a) != 0 || Gap(nil, b) != 0 {
		t.Error("gap")
	}
	if len(PrevContainers(nil)) != 0 || len(PrevContainers(sample())) != 1 {
		t.Error("PrevContainers")
	}
}
