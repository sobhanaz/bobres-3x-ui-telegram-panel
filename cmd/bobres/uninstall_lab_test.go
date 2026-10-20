package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// lab is a whole server in a temporary directory, for the uninstall's tests: its files live under
// root (the hostSys maps every absolute path into it), and the programs the uninstall runs on it
// (systemctl, ufw, apt, dpkg, docker...) are answered from a state the test controls, so that the
// tests can see both what is left and what was done, in order. Nothing here touches the machine the
// tests run on.
type lab struct {
	t    *testing.T
	root string
	dir  string // the install directory, inside root
	h    *hostSys
	o    *ops
	out  *bytes.Buffer
	err  *bytes.Buffer

	mu         sync.Mutex
	hostCmds   []string
	dockerCmds []string
	events     []string // host and docker commands, in the order they ran
	fail       map[string]error
	// at is a state captured the first time a command with that prefix runs.
	at     map[string]labState
	spy    map[string]func() labState
	asRoot bool

	// systemd: unit name -> active state; vendor units exist for systemd without a file in /etc.
	units  map[string]string
	vendor map[string]bool
	// ufw
	ufwInstalled, ufwActive bool
	ufwAdded                []string
	// packages
	pkgs, auto map[string]bool
	needs      map[string][]string
	groups     map[string]string // getent group: name -> line
	// docker
	dockerInstalled, daemonUp bool
	containers                []labCtr
	volumes                   []labVol
	networks                  []labNet
	images                    []labImg
	declared                  map[string]bool // the volumes the install's compose file declares
	rmiRefused                map[string]string
	dumpErr                   error
	dump                      string
	pgRunning                 bool
	programs                  map[string]bool
	hostname                  string
	exePath                   string
}

// labState is what a test samples at the moment a command runs.
type labState struct {
	UnitsExist, DirExists, BinExists, ReceiptExists, Containers, BackupFile bool
}

type labCtr struct {
	ID, Name, Image, WorkDir string
	// ImageID is the id of the image it runs, as docker inspect says.
	ImageID string
	Project bool
}

type labVol struct {
	Name, Mount string
	Project     bool
}

type labNet struct {
	ID, Name string
	Project  bool
}

type labImg struct {
	Repo, Tag, Digest, ID, Size string
	UsedBy                      string // a container (not BOBRES's) that runs it
}

const (
	labDomain = "panel.example.com"
	labKey    = "lab-backup-key-for-the-uninstall-tests-0001"
)

// newLab builds an empty server with the programs a Debian server has and a root user. Call
// installBobres to put a BOBRES install on it.
func newLab(t *testing.T) *lab {
	t.Helper()
	l := &lab{
		t: t, root: t.TempDir(), fail: map[string]error{}, at: map[string]labState{}, spy: map[string]func() labState{},
		asRoot: true, units: map[string]string{}, vendor: map[string]bool{}, pkgs: map[string]bool{"dpkg": true, "apt": true},
		auto: map[string]bool{}, needs: map[string][]string{}, groups: map[string]string{},
		declared: map[string]bool{}, rmiRefused: map[string]string{}, dumpErr: nil, dump: "PGDMP lab dump",
		hostname: "lab-host", exePath: "/usr/local/bin/bobres",
		programs: map[string]bool{"systemctl": true, "dpkg-query": true, "apt-get": true, "apt-mark": true, "getent": true,
			"groupdel": true, "swapoff": true},
	}
	// Resolve the temporary directory: on macOS /var is a link to /private/var, and the install
	// directory's real path is what the uninstall reports.
	if r, err := filepath.EvalSymlinks(l.root); err == nil {
		l.root = r
	}
	for _, d := range []string{"etc/systemd/system", "etc/apt/apt.conf.d", "etc/fail2ban/jail.d", "usr/local/bin", "var/lib", "var/log", "run/lock", "proc", "opt"} {
		l.mkdir(d)
	}
	l.write("/proc/swaps", "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n", 0o644)
	l.write("/etc/fstab", "UUID=abcd / ext4 defaults 0 1\n", 0o644)
	l.dir = filepath.Join(l.root, "opt", "bobres")
	l.h = &hostSys{
		root: l.root, run: l.hostRun, euid: func() int {
			if l.asRoot {
				return 0
			}
			return 1000
		},
		exe:      func() (string, error) { return l.exePath, nil },
		hostname: func() (string, error) { return l.hostname, nil },
		getenv:   func(k string) string { return map[string]string{"HOME": "/root"}[k] },
		lookPath: func(name string) (string, error) {
			if l.programs[name] {
				return "/usr/bin/" + name, nil
			}
			return "", exec.ErrNotFound
		},
		stateDir: "/var/lib/bobres", logPath: "/var/log/bobres-uninstall.log",
	}
	l.out, l.err = &bytes.Buffer{}, &bytes.Buffer{}
	l.o = &ops{
		in: strings.NewReader(""), out: l.out, errOut: l.err, host: l.h,
		run: l.dockerRun, pipe: l.pipe,
		stream: func(context.Context, string, ...string) error { return errors.New("unexpected stream") },
		freeGB: func(string) float64 { return 42 }, sleep: func(time.Duration) {},
		now: func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) },
	}
	return l
}

// p is the path of a server path on this machine.
func (l *lab) p(abs string) string { return filepath.Join(l.root, abs) }

func (l *lab) mkdir(rel string) {
	l.t.Helper()
	if err := os.MkdirAll(filepath.Join(l.root, rel), 0o755); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) write(abs, data string, mode os.FileMode) {
	l.t.Helper()
	p := l.p(abs)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), mode); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) read(abs string) string {
	b, err := os.ReadFile(l.p(abs))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func (l *lab) exists(abs string) bool {
	_, err := os.Lstat(l.p(abs))
	return err == nil
}

// existsReal is exists for a path that is already a path on this machine.
func existsReal(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// run runs `bobres uninstall` with args and the typed lines on stdin.
func (l *lab) run(stdin string, args ...string) int {
	l.t.Helper()
	l.out.Reset()
	l.err.Reset()
	l.o.in = strings.NewReader(stdin)
	return l.o.uninstallCmd(args)
}

// purge runs `bobres uninstall --purge --dir <install>` with extra args.
func (l *lab) purge(stdin string, extra ...string) int {
	l.t.Helper()
	return l.run(stdin, append([]string{"--purge", "--dir", l.dir}, extra...)...)
}

// yesArgs are the flags of a scripted purge that makes no backup.
func (l *lab) yesArgs(extra ...string) []string {
	return append([]string{"--purge", "--dir", l.dir, "--yes", "--confirm", labDomain, "--no-backup"}, extra...)
}

func (l *lab) cmdIndex(prefix string) int {
	for i, c := range l.events {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func (l *lab) host(prefix string) []string {
	var out []string
	for _, c := range l.hostCmds {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// mutating are the commands that change the server; everything else only reads.
func (l *lab) mutating() []string {
	var out []string
	for _, c := range l.events {
		switch {
		case strings.HasPrefix(c, "host systemctl list-"), strings.HasPrefix(c, "host systemctl is-active"),
			strings.HasPrefix(c, "host dpkg-query"), strings.HasPrefix(c, "host apt-mark"), strings.HasPrefix(c, "host apt-get -s"),
			strings.HasPrefix(c, "host ufw show"), strings.HasPrefix(c, "host ufw status"), strings.HasPrefix(c, "host getent"),
			strings.HasPrefix(c, "docker info"), strings.HasPrefix(c, "docker ps"), strings.HasPrefix(c, "docker volume ls"),
			strings.HasPrefix(c, "docker volume inspect"), strings.HasPrefix(c, "docker network ls"), strings.HasPrefix(c, "docker images"),
			strings.HasPrefix(c, "docker plugin ls"), strings.HasPrefix(c, "docker image inspect"), strings.HasPrefix(c, "docker inspect"):
		default:
			out = append(out, c)
		}
	}
	return out
}

func (l *lab) sample() labState {
	return labState{
		UnitsExist: l.anyUnitFile(), DirExists: existsReal(l.dir), BinExists: l.exists("/usr/local/bin/bobres"),
		ReceiptExists: l.exists("/var/lib/bobres/" + receiptFile), Containers: len(l.containers) > 0,
		BackupFile: l.anyBackup(),
	}
}

func (l *lab) anyBackup() bool {
	m, _ := filepath.Glob(filepath.Join(l.root, "backups-out", "*.bkp"))
	return len(m) > 0
}

func (l *lab) anyUnitFile() bool {
	m, _ := filepath.Glob(l.p("/etc/systemd/system/bobres*"))
	return len(m) > 0
}

// event records a command and samples the server the first time each kind runs.
func (l *lab) event(kind, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	full := kind + " " + line
	l.events = append(l.events, full)
	if _, ok := l.at[full]; !ok {
		l.at[full] = l.sample()
	}
}

// stateAt is the server as it was when the command ran.
func (l *lab) stateAt(prefix string) (labState, bool) {
	for _, c := range l.events {
		if strings.HasPrefix(c, prefix) {
			return l.at[c], true
		}
	}
	return labState{}, false
}

func (l *lab) failFor(line string) error {
	for prefix, err := range l.fail {
		if strings.HasPrefix(line, prefix) {
			return err
		}
	}
	return nil
}

// ---- the programs run on the host ---------------------------------------------------------------

func (l *lab) hostRun(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	l.hostCmds = append(l.hostCmds, line)
	l.event("host", line)
	if err := l.failFor(line); err != nil {
		return []byte("failed: " + line), err
	}
	switch name {
	case "systemctl":
		return l.systemctl(args)
	case "ufw":
		return l.ufw(args)
	case "dpkg-query":
		var b strings.Builder
		names := make([]string, 0, len(l.pkgs))
		for p := range l.pkgs {
			names = append(names, p)
		}
		sort.Strings(names)
		for _, p := range names {
			fmt.Fprintf(&b, "%s ii \n", p)
		}
		return []byte(b.String()), nil
	case "apt-mark":
		var names []string
		for p := range l.auto {
			names = append(names, p)
		}
		sort.Strings(names)
		return []byte(strings.Join(names, "\n") + "\n"), nil
	case "apt-get":
		return l.aptGet(args)
	case "/lib/ufw/ufw-init":
		return nil, nil
	case "swapoff":
		l.write("/proc/swaps", "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n", 0o644)
		return nil, nil
	case "getent":
		if len(args) == 2 && args[0] == "group" {
			if line, ok := l.groups[args[1]]; ok {
				return []byte(line + "\n"), nil
			}
			return nil, errors.New("exit status 2")
		}
	case "groupdel":
		delete(l.groups, args[0])
		return nil, nil
	}
	return nil, fmt.Errorf("lab: unexpected program %q", line)
}

func (l *lab) systemctl(args []string) ([]byte, error) {
	switch args[0] {
	case "list-unit-files":
		names := map[string]bool{}
		for _, dir := range []string{"/etc/systemd/system", "/etc/systemd/user", "/run/systemd/system"} {
			entries, _ := os.ReadDir(l.p(dir))
			for _, e := range entries {
				if unitNameRe.MatchString(e.Name()) {
					names[e.Name()] = true
				}
			}
		}
		for v := range l.vendor {
			names[v] = true
		}
		var out []string
		for n := range names {
			out = append(out, n+" enabled enabled")
		}
		sort.Strings(out)
		if len(out) == 0 {
			return nil, errors.New("exit status 1")
		}
		return []byte(strings.Join(out, "\n") + "\n"), nil
	case "list-units":
		var out []string
		for n, st := range l.units {
			if st != "" {
				out = append(out, fmt.Sprintf("%s loaded %s running desc", n, st))
			}
		}
		sort.Strings(out)
		return []byte(strings.Join(out, "\n") + "\n"), nil
	case "is-active":
		st := l.units[args[1]]
		if st == "" {
			return []byte("inactive\n"), errors.New("exit status 3")
		}
		return []byte(st + "\n"), nil
	case "stop":
		for _, a := range args[1:] {
			if _, ok := l.units[a]; ok {
				l.units[a] = "inactive"
			}
		}
	case "daemon-reload":
		// systemd forgets a unit whose file is gone, unless it is a vendor unit.
		for n := range l.units {
			if _, err := os.Lstat(l.p("/etc/systemd/system/" + n)); err != nil && !l.vendor[n] {
				delete(l.units, n)
			}
		}
	case "reload", "start", "disable", "reset-failed":
	}
	return nil, nil
}

func (l *lab) ufw(args []string) ([]byte, error) {
	switch {
	case len(args) == 2 && args[0] == "show" && args[1] == "added":
		return []byte("Added user rules (see 'ufw status' for running firewall):\n" + strings.Join(l.ufwAdded, "\n") + "\n"), nil
	case len(args) == 1 && args[0] == "status":
		if l.ufwActive {
			return []byte("Status: active\n"), nil
		}
		return []byte("Status: inactive\n"), nil
	case len(args) >= 3 && args[0] == "--force" && args[1] == "delete":
		spec := strings.Join(args[2:], " ")
		var keep []string
		found := false
		for _, r := range l.ufwAdded {
			m := ufwShowRe.FindStringSubmatch(r)
			if m != nil && strings.Join(strings.Fields(m[1]), " ") == spec && !found {
				found = true
				continue
			}
			keep = append(keep, r)
		}
		if !found {
			return []byte("Could not delete non-existent rule"), errors.New("exit status 1")
		}
		l.ufwAdded = keep
		return nil, nil
	case len(args) == 2 && args[0] == "--force" && args[1] == "disable":
		l.ufwActive = false
		return nil, nil
	}
	return nil, fmt.Errorf("lab: unexpected ufw %v", args)
}

func (l *lab) aptGet(args []string) ([]byte, error) {
	switch {
	case len(args) >= 3 && args[0] == "-s" && args[1] == "purge":
		var b strings.Builder
		asked := map[string]bool{}
		for _, p := range args[2:] {
			asked[p] = true
			if l.pkgs[p] {
				fmt.Fprintf(&b, "Purg %s [1.0]\n", p)
			}
		}
		for p := range asked {
			for _, d := range l.needs[p] {
				if l.pkgs[d] && !asked[d] {
					fmt.Fprintf(&b, "Purg %s [1.0]\n", d)
				}
			}
		}
		return []byte(b.String()), nil
	case len(args) >= 3 && args[0] == "purge":
		for _, p := range args[3:] {
			delete(l.pkgs, p)
			delete(l.auto, p)
		}
		return nil, nil
	}
	return nil, fmt.Errorf("lab: unexpected apt-get %v", args)
}

// ---- docker -------------------------------------------------------------------------------------

func (l *lab) dockerRun(_ context.Context, _ string, args ...string) ([]byte, error) {
	line := strings.Join(args, " ")
	l.event("docker", line)
	if err := l.failFor("docker " + line); err != nil {
		return []byte("failed"), err
	}
	if len(args) > 0 && args[0] == "info" {
		if !l.daemonUp {
			return []byte("Cannot connect to the Docker daemon"), errors.New("exit status 1")
		}
		if strings.Contains(line, "Swarm") {
			return []byte("inactive\n"), nil
		}
		return []byte("29.0.0\n"), nil
	}
	if len(args) == 0 {
		return nil, errors.New("lab: empty docker command")
	}
	switch args[0] {
	case "compose":
		return l.compose(args[len(composeArgs()):])
	case "--project-name":
		return l.compose(args[len(composeArgs())-1:])
	case "ps":
		return l.dockerPS(args)
	case "volume":
		return l.dockerVolume(args)
	case "network":
		return l.dockerNetwork(args)
	case "images":
		return l.dockerImages(args)
	case "inspect":
		// docker inspect --format {{.Image}} <container ids>: the id of each one's image.
		var b strings.Builder
		for _, id := range args[3:] {
			for _, c := range l.containers {
				if c.ID == id {
					fmt.Fprintln(&b, "sha256:"+c.ImageID)
				}
			}
		}
		return []byte(b.String()), nil
	case "rmi":
		return l.dockerRmi(args[1])
	case "rm":
		for _, id := range args[2:] {
			l.removeContainer(id)
		}
		return nil, nil
	case "plugin":
		return nil, nil
	case "image":
		for _, im := range l.images {
			if im.Repo+":"+im.Tag == args[len(args)-1] {
				return []byte(im.ID + "\n"), nil
			}
		}
		return nil, errors.New("No such image")
	}
	return nil, fmt.Errorf("lab: unexpected docker %q", line)
}

func (l *lab) removeContainer(id string) {
	var keep []labCtr
	for _, c := range l.containers {
		if c.ID != id {
			keep = append(keep, c)
		}
	}
	l.containers = keep
}

func (l *lab) compose(args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("lab: compose without a verb")
	}
	// args are what follows "--env-file .env"
	verb := args[0]
	if verb == "--profile" {
		verb = args[2]
	}
	switch verb {
	case "down":
		var keepC []labCtr
		for _, c := range l.containers {
			if !c.Project {
				keepC = append(keepC, c)
			}
		}
		l.containers = keepC
		var keepN []labNet
		for _, n := range l.networks {
			if !n.Project {
				keepN = append(keepN, n)
			}
		}
		l.networks = keepN
		if contains(args, "--volumes") {
			var keepV []labVol
			for _, v := range l.volumes {
				if v.Project && l.declared[v.Name] {
					continue
				}
				keepV = append(keepV, v)
			}
			l.volumes = keepV
		}
		l.pgRunning = false
	case "ps":
		if l.pgRunning {
			return []byte(`[{"Service":"postgres","State":"running","Health":"healthy"}]`), nil
		}
		return []byte(`[]`), nil
	case "up":
		l.pgRunning = true
	}
	return nil, nil
}

func (l *lab) dockerPS(args []string) ([]byte, error) {
	var b strings.Builder
	if contains(args, "-aq") {
		for _, c := range l.containers {
			fmt.Fprintln(&b, c.ID)
		}
		return []byte(b.String()), nil
	}
	for _, c := range l.containers {
		if !c.Project {
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", c.ID, c.Name, c.Image, c.WorkDir)
	}
	return []byte(b.String()), nil
}

func (l *lab) dockerVolume(args []string) ([]byte, error) {
	switch args[1] {
	case "ls":
		var b strings.Builder
		for _, v := range l.volumes {
			if contains(args, "--filter") && !v.Project {
				continue
			}
			fmt.Fprintln(&b, v.Name)
		}
		return []byte(b.String()), nil
	case "inspect":
		var b strings.Builder
		for _, n := range args[4:] {
			for _, v := range l.volumes {
				if v.Name == n {
					fmt.Fprintf(&b, "%s|%s\n", v.Name, v.Mount)
				}
			}
		}
		return []byte(b.String()), nil
	case "rm":
		gone := map[string]bool{}
		for _, n := range args[2:] {
			gone[n] = true
		}
		var keep []labVol
		for _, v := range l.volumes {
			if !gone[v.Name] {
				keep = append(keep, v)
			}
		}
		l.volumes = keep
	}
	return nil, nil
}

func (l *lab) dockerNetwork(args []string) ([]byte, error) {
	switch args[1] {
	case "ls":
		var b strings.Builder
		if !contains(args, "--filter") {
			fmt.Fprintln(&b, "bridge\nhost\nnone")
		}
		for _, n := range l.networks {
			if contains(args, "--filter") && !n.Project {
				continue
			}
			if contains(args, "--filter") {
				fmt.Fprintf(&b, "%s\t%s\n", n.ID, n.Name)
			} else {
				fmt.Fprintln(&b, n.Name)
			}
		}
		return []byte(b.String()), nil
	case "rm":
		gone := map[string]bool{}
		for _, id := range args[2:] {
			gone[id] = true
		}
		var keep []labNet
		for _, n := range l.networks {
			if !gone[n.ID] {
				keep = append(keep, n)
			}
		}
		l.networks = keep
	}
	return nil, nil
}

func (l *lab) dockerImages(args []string) ([]byte, error) {
	var b strings.Builder
	if contains(args, "-q") {
		for _, im := range l.images {
			fmt.Fprintln(&b, im.ID)
		}
		return []byte(b.String()), nil
	}
	for _, im := range l.images {
		digest := im.Digest
		if digest == "" {
			digest = "<none>"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", im.Repo, im.Tag, digest, im.ID, im.Size)
	}
	return []byte(b.String()), nil
}

func (l *lab) dockerRmi(ref string) ([]byte, error) {
	if why, ok := l.rmiRefused[ref]; ok {
		return []byte(why), errors.New("exit status 1")
	}
	var keep []labImg
	found := false
	for _, im := range l.images {
		if im.Repo+":"+im.Tag == ref || im.Repo+"@"+im.Digest == ref {
			if im.UsedBy != "" {
				return []byte("conflict: unable to delete " + im.ID + " (cannot be forced) - image is being used by container " + im.UsedBy), errors.New("exit status 1")
			}
			found = true
			continue
		}
		keep = append(keep, im)
	}
	if !found {
		return []byte("Error response from daemon: No such image: " + ref), errors.New("exit status 1")
	}
	l.images = keep
	return nil, nil
}

// pipe answers the database dump of the final backup.
func (l *lab) pipe(_ context.Context, _ string, _ io.Reader, w io.Writer, args ...string) error {
	if len(args) == 0 || args[len(args)-1] != dumpScript {
		return fmt.Errorf("lab: unexpected pipe %v", args)
	}
	l.event("docker", "pipe pg_dump")
	if l.dumpErr != nil {
		return l.dumpErr
	}
	_, err := io.WriteString(w, l.dump)
	return err
}

// ---- a BOBRES install on the lab server -----------------------------------------------------------

// installBobres puts a full BOBRES install on the server, as `sudo bobres install` and the hardening
// leave it, and returns its receipt (written to the state directory).
func (l *lab) installBobres() *receipt {
	l.t.Helper()
	env := map[string]string{
		"BOBRES_DOMAIN": labDomain, "BOBRES_VERSION": "0.6.1", "BOBRES_BACKUP_KEY": labKey,
		"BOBRES_TOKEN_CORE": "lab-token", "POSTGRES_PASSWORD": "lab-pg-password-for-tests-only",
	}
	if err := os.MkdirAll(l.dir, 0o750); err != nil {
		l.t.Fatal(err)
	}
	put := func(rel, data string, mode os.FileMode) {
		p := filepath.Join(l.dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			l.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), mode); err != nil {
			l.t.Fatal(err)
		}
	}
	put(".env", string(renderEnv(env)), 0o600)
	put("docker-compose.yml", "services:\n  core:\n    image: ghcr.io/sobhanaz/bobres-core:${BOBRES_VERSION}\n", 0o644)
	put(".env.example", "# example\n", 0o644)
	put("postgres-init.sh", "#!/bin/sh\n", 0o755)
	put("caddy/Caddyfile", "{$BOBRES_DOMAIN}\n", 0o644)
	put("tls/global.caddy", "# tls\n", 0o644)
	put("backups/bobres-20261019T033000Z.bkp", strings.Repeat("x", 4096), 0o600)
	put("backups/bobres-20261019T033000Z.json", "{}", 0o600)
	put(".restore-point/restore-point.json", "{}", 0o600)
	put("bobres-support-20261001T000000Z.tar.gz", "support", 0o600)

	// Docker: the project's objects, and one stranger of each kind.
	l.dockerInstalled, l.daemonUp, l.programs["docker"] = true, true, true
	for _, svc := range []string{"caddy", "postgres", "redis", "core", "payments", "provisioner", "bot", "backup"} {
		l.containers = append(l.containers, labCtr{ID: "c-" + svc, Name: "bobres-" + svc + "-1", Image: "img-" + svc, WorkDir: l.dir, Project: true})
	}
	l.containers = append(l.containers, labCtr{ID: "c-other", Name: "someone-elses-app", Image: "nginx:1.27", WorkDir: "/srv/app"})
	for _, v := range []string{"pg_data", "redis_data", "caddy_data", "caddy_config", "caddy_admin", "tls_request", "tls_result"} {
		l.volumes = append(l.volumes, labVol{Name: "bobres_" + v, Mount: "/var/lib/docker/volumes/bobres_" + v + "/_data", Project: true})
		if v != "tls_request" && v != "tls_result" && v != "caddy_admin" {
			l.declared["bobres_"+v] = true // the others only an older compose file declared
		}
	}
	l.volumes = append(l.volumes, labVol{Name: "someone_data", Mount: "/var/lib/docker/volumes/someone_data/_data"})
	for _, n := range []string{"public", "edge", "backend", "egress", "offline"} {
		l.networks = append(l.networks, labNet{ID: "n-" + n, Name: "bobres_" + n, Project: true})
	}
	l.networks = append(l.networks, labNet{ID: "n-other", Name: "someone_net"})
	l.images = []labImg{
		{Repo: "ghcr.io/sobhanaz/bobres-core", Tag: "0.6.1", Digest: "sha256:aaa", ID: "i-core", Size: "25.7MB"},
		{Repo: "ghcr.io/sobhanaz/bobres-core", Tag: "0.6.0", Digest: "sha256:bbb", ID: "i-core0", Size: "25.1MB"},
		{Repo: "ghcr.io/sobhanaz/bobres-bot", Tag: "0.6.1", Digest: "sha256:ccc", ID: "i-bot", Size: "20.0MB"},
		// Docker 29 lists an image that was pulled by digest with no tag.
		{Repo: "caddy", Tag: "<none>", Digest: "sha256:ddd", ID: "i-caddy", Size: "50MB"},
		{Repo: "postgres", Tag: "<none>", Digest: "sha256:eee", ID: "i-pg", Size: "270MB"},
		{Repo: "nginx", Tag: "1.27", Digest: "sha256:fff", ID: "i-nginx", Size: "190MB", UsedBy: "someone-elses-app"},
	}
	// The containers run the images by these references.
	l.setImageRefs(map[string]string{"c-core": "ghcr.io/sobhanaz/bobres-core:0.6.1", "c-bot": "ghcr.io/sobhanaz/bobres-bot:0.6.1",
		"c-caddy": "caddy:2.11.7-alpine", "c-postgres": "postgres:16-alpine", "c-redis": "redis:7-alpine"})
	l.setImageIDs(map[string]string{"c-core": "i-core", "c-bot": "i-bot", "c-caddy": "i-caddy", "c-postgres": "i-pg", "c-redis": "i-redis"})

	// systemd units: the updater and the monitor, with a drop-in and enable links.
	unit := "[Unit]\nDescription=BOBRES\n[Service]\nExecStart=/usr/local/bin/bobres version\n"
	for _, n := range []string{"bobres-updater.path", "bobres-updater.service", "bobres-monitor.timer", "bobres-monitor.service"} {
		l.write("/etc/systemd/system/"+n, unit, 0o644)
		l.units[n] = "inactive"
	}
	l.units["bobres-updater.path"], l.units["bobres-monitor.timer"] = "active", "active"
	l.write("/etc/systemd/system/bobres-monitor.service.d/override.conf", "[Service]\nNice=10\n", 0o644)
	l.write("/etc/systemd/system/timers.target.wants/.keep", "", 0o644)
	for _, link := range []struct{ dir, unit string }{{"timers.target.wants", "bobres-monitor.timer"}, {"multi-user.target.wants", "bobres-updater.path"}} {
		if err := os.MkdirAll(l.p("/etc/systemd/system/"+link.dir), 0o755); err != nil {
			l.t.Fatal(err)
		}
		if err := os.Symlink("/etc/systemd/system/"+link.unit, l.p("/etc/systemd/system/"+link.dir+"/"+link.unit)); err != nil {
			l.t.Fatal(err)
		}
	}
	_ = os.Remove(l.p("/etc/systemd/system/timers.target.wants/.keep"))
	l.write("/etc/systemd/system/ssh.service.d/keep.conf", "[Service]\n", 0o644)
	l.write("/etc/systemd/system/docker.service", "[Unit]\n", 0o644)

	// The hardening: ufw, fail2ban, security updates, swap.
	l.programs["ufw"], l.ufwInstalled, l.ufwActive = true, true, true
	l.ufwAdded = []string{
		"ufw allow 22/tcp comment 'bobres SSH'",
		"ufw allow 80/tcp comment 'bobres HTTP'",
		"ufw allow 443/tcp comment 'bobres HTTPS'",
		"ufw allow 443/udp comment 'bobres HTTP/3'",
		"ufw allow from 172.16.0.0/12 to any port 8081 proto tcp comment 'bobres Bot API server'",
		"ufw allow 2053/tcp comment 'bobres 3x-ui'",
		"ufw allow 8080/tcp comment 'my own rule'",
		"ufw allow 9000/tcp",
	}
	jail := "# Written by bobres harden: an address that fails to log in over SSH 5 times\n[sshd]\nenabled = true\n"
	l.write(fail2banJail, jail, 0o644)
	l.write(securityOnlyConf, securityOnly("ubuntu"), 0o644)
	l.write(autoUpgradesConf, autoUpgrades, 0o644)
	l.units["fail2ban"] = "active"
	l.vendor["fail2ban"] = true
	l.write("/swapfile", strings.Repeat("\x00", 1<<20), 0o600)
	l.write("/etc/fstab", "UUID=abcd / ext4 defaults 0 1\n/swapfile none swap sw 0 0\n# my own comment\n", 0o644)
	l.write("/proc/swaps", "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n/swapfile                               file\t\t1024\t\t0\t\t-2\n", 0o644)

	// The command and the state directory.
	l.write("/usr/local/bin/bobres", "#!bobres\n", 0o755)
	l.write("/usr/local/bin/bobres.prev", "#!bobres previous\n", 0o755)
	l.write("/usr/local/bin/.bobres.new", "partial", 0o755)
	l.write("/var/lib/bobres/keys.seq", "3\n", 0o644)
	l.write("/var/lib/bobres/release.version", "0.6.1\n", 0o644)
	l.write("/var/lib/bobres/checksums.txt", "# version 0.6.1\n", 0o644)
	l.write("/var/log/bobres-install.log", "log\n", 0o600)

	// The receipt, as bobres install, install.sh and the hardening write it.
	rs := newReceiptStore(l.p("/var/lib/bobres"))
	if err := rs.update(func(r *receipt) {
		r.CLIVersion, r.InstallDir, r.Domain, r.CLIPath = "0.6.1", l.dir, labDomain, "/usr/local/bin/bobres"
		r.Firewall = &fwReceipt{WasActive: false, EnabledByBobres: true, Rules: []fwRuleReceipt{
			{Spec: "allow 22/tcp", Comment: "bobres SSH"}, {Spec: "allow 80/tcp", Comment: "bobres HTTP"},
		}}
		r.Swap = &swapReceipt{Path: "/swapfile", SizeMB: 1, FstabLine: "/swapfile none swap sw 0 0", FstabAdded: true}
		r.Images = &imgReceipt{
			Refs:        []string{"caddy:2.11.7-alpine@sha256:ddd", "postgres:16-alpine@sha256:eee", "redis:7-alpine@sha256:ggg"},
			Preexisting: []string{"redis:7-alpine@sha256:ggg"},
		}
		r.recordFile(autoUpgradesConf, []byte(`APT::Periodic::Update-Package-Lists "1";`+"\n"), true, []byte(autoUpgrades))
		r.recordFile(fail2banJail, nil, false, []byte(jail))
		r.recordFile(securityOnlyConf, nil, false, []byte(securityOnly("ubuntu")))
	}); err != nil {
		l.t.Fatal(err)
	}
	rc, err := rs.load()
	if err != nil {
		l.t.Fatal(err)
	}
	l.images = append(l.images, labImg{Repo: "redis", Tag: "<none>", Digest: "sha256:ggg", ID: "i-redis", Size: "40MB"})
	return rc
}

// setImageRefs sets the image reference each container runs.
func (l *lab) setImageRefs(refs map[string]string) {
	for i := range l.containers {
		if ref, ok := refs[l.containers[i].ID]; ok {
			l.containers[i].Image = ref
		}
	}
}

// setImageIDs sets the image id each container runs.
func (l *lab) setImageIDs(ids map[string]string) {
	for i := range l.containers {
		if id, ok := ids[l.containers[i].ID]; ok {
			l.containers[i].ImageID = id
		}
	}
}

// saveReceipt rewrites the receipt with fn.
func (l *lab) saveReceipt(fn func(r *receipt)) {
	l.t.Helper()
	if err := newReceiptStore(l.p("/var/lib/bobres")).update(fn); err != nil {
		l.t.Fatal(err)
	}
}

// dropReceipt removes the receipt, as on a server installed by an older release.
func (l *lab) dropReceipt() {
	l.t.Helper()
	if err := os.Remove(l.p("/var/lib/bobres/" + receiptFile)); err != nil {
		l.t.Fatal(err)
	}
}

// jsonOf is a helper for messages.
func jsonOf(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// leftovers lists what of BOBRES's is on the server, by looking at the files and the fake state
// (the test's own view, independent of the uninstall's scan).
func (l *lab) leftovers() []string {
	var left []string
	check := func(abs string) {
		if l.exists(abs) {
			left = append(left, abs)
		}
	}
	for _, p := range []string{fail2banJail, securityOnlyConf, "/swapfile", "/usr/local/bin/bobres", "/usr/local/bin/bobres.prev", "/usr/local/bin/.bobres.new",
		"/var/log/bobres-install.log", "/var/lib/bobres"} {
		check(p)
	}
	if existsReal(l.dir) {
		left = append(left, l.dir)
	}
	if m, _ := filepath.Glob(l.p("/etc/systemd/system/*bobres*")); len(m) > 0 {
		left = append(left, m...)
	}
	if m, _ := filepath.Glob(l.p("/etc/systemd/system/*/bobres*")); len(m) > 0 {
		left = append(left, m...)
	}
	for _, c := range l.containers {
		if c.Project {
			left = append(left, "container "+c.Name)
		}
	}
	for _, v := range l.volumes {
		if v.Project {
			left = append(left, "volume "+v.Name)
		}
	}
	for _, n := range l.networks {
		if n.Project {
			left = append(left, "network "+n.Name)
		}
	}
	for _, im := range l.images {
		if strings.HasPrefix(im.Repo, imageRepoPrefix) {
			left = append(left, "image "+im.Repo+":"+im.Tag)
		}
	}
	for _, r := range l.ufwAdded {
		if strings.Contains(r, "comment 'bobres") && !strings.Contains(r, "bobres SSH") && !strings.Contains(r, "bobres 3x-ui") {
			left = append(left, "ufw "+r)
		}
	}
	return left
}
