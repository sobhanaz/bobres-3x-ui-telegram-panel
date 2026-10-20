package collect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// fixture reads a file recorded on the lab server (testdata/).
func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // a recorded test fixture
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProcFilesOfARealServer(t *testing.T) {
	cpu, cores, err := parseStat(fixture(t, "proc/stat"))
	if err != nil {
		t.Fatal(err)
	}
	// cpu  337877 6 297452 92517692 26806 0 23358 0: busy is everything but idle and iowait.
	if cores != 16 || cpu.busy != 337877+6+297452+23358 || cpu.total != cpu.busy+92517692+26806 {
		t.Errorf("stat: %d cores, %+v", cores, cpu)
	}

	mem, swap, err := parseMeminfo(fixture(t, "proc/meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	if mem.Total != 8125884*1024 || mem.Available != 6671692*1024 || mem.Used() != (8125884-6671692)*1024 {
		t.Errorf("mem: %+v", mem)
	}
	if swap.Total != 1048572*1024 || swap.Used != 0 {
		t.Errorf("swap: %+v", swap)
	}

	load, err := parseLoadavg(fixture(t, "proc/loadavg"))
	if err != nil || load != (snapshot.Load{0.29, 0.11, 0.07}) {
		t.Errorf("load: %v %v", load, err)
	}
	if up, err := parseUptime(fixture(t, "proc/uptime")); err != nil || up != 58698 {
		t.Errorf("uptime: %v %v", up, err)
	}

	// Of twenty-odd interfaces only the real one is the server's own network.
	ifs := parseNetDev(fixture(t, "proc/net_dev"))
	if len(ifs) != 1 || ifs[0].Name != "eth0" || ifs[0].RxBytes == 0 {
		t.Errorf("interfaces: %+v", ifs)
	}
	if got := parseOSRelease(fixture(t, "proc/os-release")); got != "Ubuntu 24.04.5 LTS" {
		t.Errorf("os: %q", got)
	}
}

func TestParsersRefuseGarbage(t *testing.T) {
	for name, f := range map[string]func() error{
		"stat empty":    func() error { _, _, err := parseStat(nil); return err },
		"stat no cpu":   func() error { _, _, err := parseStat([]byte("intr 1 2 3\n")); return err },
		"stat short":    func() error { _, _, err := parseStat([]byte("cpu 1 2\n")); return err },
		"stat text":     func() error { _, _, err := parseStat([]byte("cpu a b c d e\n")); return err },
		"meminfo empty": func() error { _, _, err := parseMeminfo(nil); return err },
		"meminfo zero":  func() error { _, _, err := parseMeminfo([]byte("MemTotal: 0 kB\n")); return err },
		"loadavg short": func() error { _, err := parseLoadavg([]byte("0.1 0.2")); return err },
		"loadavg text":  func() error { _, err := parseLoadavg([]byte("a b c")); return err },
		"uptime empty":  func() error { _, err := parseUptime(nil); return err },
		"uptime text":   func() error { _, err := parseUptime([]byte("soon")); return err },
		"uptime minus":  func() error { _, err := parseUptime([]byte("-5.0 1")); return err },
	} {
		if f() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got := parseNetDev([]byte("garbage\nbroken: 1 2\n eth0: x 0 0 0 0 0 0 0 y 0\n")); len(got) != 0 {
		t.Errorf("net: %+v", got)
	}
	if len(parseMountinfo([]byte("not a mountinfo line\n149 73 0:46 / / rw - \n"))) != 0 {
		t.Error("mountinfo")
	}
}

func TestMeminfoWithoutMemAvailable(t *testing.T) {
	mem, _, err := parseMeminfo([]byte("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 150 kB\n"))
	if err != nil || mem.Available != 300*1024 {
		t.Fatalf("%+v %v", mem, err)
	}
	// A kernel that claims more available than total is capped, not believed.
	mem, _, _ = parseMeminfo([]byte("MemTotal: 1000 kB\nMemAvailable: 5000 kB\nSwapTotal: 10 kB\nSwapFree: 90 kB\n"))
	if mem.Available != 1000*1024 {
		t.Errorf("%+v", mem)
	}
}

// A server with its own disk for Docker, a path with a space, and stacked mounts.
const serverMountinfo = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw,errors=remount-ro
23 22 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc proc rw
24 22 8:16 / /var/lib/docker rw,relatime shared:30 - xfs /dev/sdb rw,attr2
25 22 0:55 / /mnt/my\040disk rw,relatime - ext4 /dev/sdc rw
26 22 8:32 / /opt rw,relatime - ext4 /dev/sdd rw
27 26 8:48 / /opt/bobres rw,relatime - ext4 /dev/sde rw
28 22 0:60 / /srv rw - tmpfs tmpfs rw
29 22 8:64 / /srv rw,relatime - ext4 /dev/sdf rw
`

func TestMountForFindsTheFilesystemHoldingAPath(t *testing.T) {
	mounts := parseMountinfo([]byte(serverMountinfo))
	if len(mounts) != 8 {
		t.Fatalf("%d mounts", len(mounts))
	}
	for path, want := range map[string]string{
		"/opt/bobres":              "/opt/bobres",
		"/opt/bobres/run/monitor":  "/opt/bobres",
		"/opt/other":               "/opt",
		"/opt":                     "/opt",
		"/optical":                 "/",
		"/var/lib/docker":          "/var/lib/docker",
		"/var/lib/docker/overlay2": "/var/lib/docker",
		"/var/lib/dockerx":         "/",
		"/mnt/my disk/x":           "/mnt/my disk",
		"/srv/data":                "/srv",
		"/home/me":                 "/",
	} {
		m, ok := mountFor(mounts, path)
		if !ok || m.point != want {
			t.Errorf("%s: mount %q (%v), want %q", path, m.point, ok, want)
		}
	}
	// Stacked mounts: the last one listed is the one in sight.
	if m, _ := mountFor(mounts, "/srv/x"); m.fstype != "ext4" {
		t.Errorf("stacked: %+v", m)
	}
	if _, ok := mountFor(nil, "/x"); ok {
		t.Error("no mounts")
	}
	if got := unescapeMount(`/mnt/a\040b\134c\9`); got != `/mnt/a b\c\9` {
		t.Errorf("unescape: %q", got)
	}
}

func TestVirtualInterfaces(t *testing.T) {
	for name, want := range map[string]bool{
		"lo": true, "docker0": true, "br-1a2b3c": true, "veth9f3a2b1": true, "virbr0": true, "cni0": true, "flannel.1": true,
		"cali123": true, "tunl0": true, "wg0": true, "tailscale0": true,
		"eth0": false, "ens3": false, "enp0s3": false, "eno1": false, "bond0": false, "wlan0": false, "lorem0": false,
	} {
		if got := virtualIface(name); got != want {
			t.Errorf("virtualIface(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestOSReleaseFallbacks(t *testing.T) {
	if got := parseOSRelease([]byte("NAME=\"Debian GNU/Linux\"\nVERSION_ID=\"12\"\n")); got != "Debian GNU/Linux 12" {
		t.Errorf("%q", got)
	}
	if parseOSRelease(nil) != "" {
		t.Error("empty os-release")
	}
}

func TestOSFSBoundsWhatItReads(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", maxProcFile+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (OSFS{}).ReadFile(big); err == nil {
		t.Error("a huge file was read")
	}
	if _, err := (OSFS{}).ReadFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file")
	}
	small := filepath.Join(dir, "small")
	_ = os.WriteFile(small, []byte("hi"), 0o600)
	if b, err := (OSFS{}).ReadFile(small); err != nil || string(b) != "hi" {
		t.Errorf("%q %v", b, err)
	}
}

func TestOSStatfsReadsARealFilesystem(t *testing.T) {
	st, err := OSStatfs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Total == 0 || st.Used > st.Total || st.Avail > st.Total || st.Files == 0 {
		t.Errorf("%+v", st)
	}
	if _, err := OSStatfs("/definitely/not/here"); err == nil {
		t.Error("a missing path")
	}
}
