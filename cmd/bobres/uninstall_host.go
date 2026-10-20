package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// hostSys is how the uninstall reaches the server outside Docker: files, systemd, ufw, apt. Every
// absolute path goes through p, so the tests can point the whole server at a temporary directory
// (root); a program run goes through run, which the tests answer. root is "" on a real server.
type hostSys struct {
	root     string
	run      func(ctx context.Context, name string, args ...string) ([]byte, error)
	euid     func() int
	exe      func() (string, error)
	hostname func() (string, error)
	getenv   func(string) string
	lookPath func(string) (string, error)
	stateDir string
	logPath  string
	// chdir leaves a directory that is about to be deleted (nil: stay).
	chdir func(dir string) error
}

const uninstallLogName = "/var/log/bobres-uninstall.log"

func newHostSys() *hostSys {
	h := &hostSys{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed programs, arguments built here
			// apt never asks, and the output is in English (it is parsed).
			cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C", "NEEDRESTART_MODE=a")
			cmd.WaitDelay = 5 * time.Second
			return cmd.CombinedOutput()
		},
		euid:     os.Geteuid,
		hostname: os.Hostname,
		getenv:   os.Getenv,
		chdir:    os.Chdir,
		lookPath: exec.LookPath,
		stateDir: defaultStateDir(),
		logPath:  uninstallLogName,
		exe: func() (string, error) {
			p, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(p)
		},
	}
	if p := strings.TrimSpace(os.Getenv("BOBRES_UNINSTALL_LOG")); p != "" {
		h.logPath = p
	}
	return h
}

// p maps an absolute system path into root.
func (h *hostSys) p(abs string) string {
	if h.root == "" || h.root == "/" {
		return abs
	}
	return filepath.Join(h.root, abs)
}

// has reports whether the program is installed.
func (h *hostSys) has(name string) bool {
	_, err := h.lookPath(name)
	return err == nil
}

// tryRun runs a program that only reads, with a time limit.
func (h *hostSys) tryRun(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := h.run(ctx, name, args...)
	return string(out), err
}

// do runs a program that changes the server; its output is part of the error.
func (h *hostSys) do(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	out, err := h.run(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, lastLines(string(out), 3))
	}
	return nil
}

// lastLines is the last n non-empty lines of s, on one line.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// ---- systemd units and timers -------------------------------------------------------------

// unitNameRe is every unit BOBRES may own: bobres.service, bobres-monitor.timer, bobres@x.service,
// by name. Units are found by this name and not from a list, so a unit a later release adds is
// covered without a change here.
var unitNameRe = regexp.MustCompile(`^bobres(?:[-@][A-Za-z0-9:_.@\\-]*)?\.(?:service|timer|path|socket|target|mount|automount|slice|swap|scope)$`)

// unitDirs are the directories only an administrator (or the installer) writes units in.
var unitDirs = []string{"/etc/systemd/system", "/etc/systemd/user", "/run/systemd/system"}

// unitEntry is a unit of BOBRES's and everything of it on disk.
type unitEntry struct {
	Name string
	// Paths are the unit file, its drop-in directory and its enable links (logical paths).
	Paths []string
	// Known: systemd lists it. Active is its state ("active", "activating", "inactive"...).
	Known  bool
	Active string
	// Foreign: systemd knows it but no file of it lies in the directories above (a package or the
	// vendor placed it): BOBRES does not remove what it did not place.
	Foreign bool
}

// kind orders units for stopping: what triggers others goes first.
func (u unitEntry) kind() int {
	switch {
	case strings.HasSuffix(u.Name, ".timer"), strings.HasSuffix(u.Name, ".path"), strings.HasSuffix(u.Name, ".socket"):
		return 0
	case strings.HasSuffix(u.Name, ".service"):
		return 1
	}
	return 2
}

// unitInventory finds the units named bobres* in the directories above and in what systemd lists.
func (h *hostSys) unitInventory(ctx context.Context) []unitEntry {
	by := map[string]*unitEntry{}
	get := func(name string) *unitEntry {
		if by[name] == nil {
			by[name] = &unitEntry{Name: name}
		}
		return by[name]
	}
	for _, dir := range unitDirs {
		entries, err := os.ReadDir(h.p(dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			switch {
			case unitNameRe.MatchString(name) && !e.IsDir():
				get(name).Paths = append(get(name).Paths, dir+"/"+name)
			case strings.HasSuffix(name, ".d") && e.IsDir() && unitNameRe.MatchString(strings.TrimSuffix(name, ".d")):
				un := strings.TrimSuffix(name, ".d")
				get(un).Paths = append(get(un).Paths, dir+"/"+name)
			case e.IsDir() && (strings.HasSuffix(name, ".wants") || strings.HasSuffix(name, ".requires") || strings.HasSuffix(name, ".upholds")):
				subs, err := os.ReadDir(h.p(dir + "/" + name))
				if err != nil {
					continue
				}
				for _, s := range subs {
					if unitNameRe.MatchString(s.Name()) {
						get(s.Name()).Paths = append(get(s.Name()).Paths, dir+"/"+name+"/"+s.Name())
					}
				}
			}
		}
	}
	if h.has("systemctl") {
		if out, _ := h.tryRun(ctx, "systemctl", "list-unit-files", "bobres*", "--no-legend", "--no-pager", "--plain"); out != "" {
			for _, line := range strings.Split(out, "\n") {
				if f := strings.Fields(line); len(f) > 0 && unitNameRe.MatchString(f[0]) {
					get(f[0]).Known = true
				}
			}
		}
		if out, _ := h.tryRun(ctx, "systemctl", "list-units", "bobres*", "--all", "--no-legend", "--no-pager", "--plain"); out != "" {
			for _, line := range strings.Split(out, "\n") {
				if f := strings.Fields(line); len(f) >= 3 && unitNameRe.MatchString(f[0]) {
					e := get(f[0])
					e.Known, e.Active = true, f[2]
				}
			}
		}
	}
	out := make([]unitEntry, 0, len(by))
	for _, e := range by {
		e.Paths = uniqSorted(e.Paths)
		e.Foreign = e.Known && len(e.Paths) == 0
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].kind() != out[j].kind() {
			return out[i].kind() < out[j].kind()
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ownUnitPath reports whether p is a path unitInventory may have returned: below one of the unit
// directories, named after a unit of BOBRES's (or in a .wants/.requires directory, or a drop-in
// directory of one).
func ownUnitPath(p string) bool {
	p = filepath.Clean(p)
	for _, dir := range unitDirs {
		if !strings.HasPrefix(p, dir+"/") {
			continue
		}
		rest := strings.TrimPrefix(p, dir+"/")
		parts := strings.Split(rest, "/")
		switch len(parts) {
		case 1:
			return unitNameRe.MatchString(parts[0]) || (strings.HasSuffix(parts[0], ".d") && unitNameRe.MatchString(strings.TrimSuffix(parts[0], ".d")))
		case 2:
			wants := strings.HasSuffix(parts[0], ".wants") || strings.HasSuffix(parts[0], ".requires") || strings.HasSuffix(parts[0], ".upholds")
			return wants && unitNameRe.MatchString(parts[1])
		}
	}
	return false
}

// ---- ufw ------------------------------------------------------------------------------------

// fwItem is a ufw rule that carries a comment of BOBRES's.
type fwItem struct {
	// Words are the rule in ufw's words after "ufw": allow 80/tcp.
	Words   []string
	Comment string
	// Delete: this rule is BOBRES's own to remove; otherwise Why says why it stays.
	Delete bool
	Why    string
}

func (f fwItem) spec() string { return strings.Join(f.Words, " ") }

// fwInventory is the firewall as the uninstall finds it.
type fwInventory struct {
	Installed bool
	// Known: the rules could be read (root, and ufw answered).
	Known  bool
	Active bool
	Items  []fwItem
	// Foreign counts the rules that are not BOBRES's (no comment of BOBRES's).
	Foreign int
}

var (
	ufwShowRe     = regexp.MustCompile(`^ufw\s+(.*?)(?:\s+comment\s+(?:'([^']*)'|"([^"]*)"))?\s*$`)
	ufwWordRe     = regexp.MustCompile(`^[A-Za-z0-9./:_-]+$`)
	hostRuleWords = regexp.MustCompile(`^allow from \S+ to any port \d+ proto tcp$`)
)

// deletableRules are the comments of the rules BOBRES adds for itself, with the form each takes.
// Every other comment of BOBRES's ("bobres SSH", "bobres 3x-ui", a rule a later release adds)
// is left alone.
func deletableRule(comment, spec string) bool {
	switch comment {
	case "bobres HTTP":
		return spec == "allow 80/tcp"
	case "bobres HTTPS":
		return spec == "allow 443/tcp"
	case "bobres HTTP/3":
		return spec == "allow 443/udp"
	case "bobres Bot API server", "bobres 3x-ui panel", "bobres service":
		return hostRuleWords.MatchString(spec)
	}
	return false
}

// parseUfwAdded reads `ufw show added`.
func parseUfwAdded(out string, preexisting map[string]bool) (items []fwItem, foreign int) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		m := ufwShowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		comment := m[2]
		if comment == "" {
			comment = m[3]
		}
		words := strings.Fields(m[1])
		if !strings.HasPrefix(comment, "bobres ") {
			foreign++
			continue
		}
		it := fwItem{Words: words, Comment: comment}
		bad := false
		for _, w := range words {
			if !ufwWordRe.MatchString(w) {
				bad = true
			}
		}
		switch {
		case comment == "bobres SSH":
			it.Why = "SSH: the rule that keeps this session is never touched"
		case comment == "bobres 3x-ui":
			it.Why = "it opens a port of the 3x-ui on this server"
		case bad || len(words) == 0 || words[0] != "allow":
			it.Why = "not a plain allow rule"
		case preexisting[it.spec()]:
			it.Why = "the same rule was there before BOBRES"
		case deletableRule(comment, it.spec()):
			it.Delete = true
		default:
			it.Why = "a rule this release does not know"
		}
		items = append(items, it)
	}
	return items, foreign
}

// firewallInventory reads ufw.
func (u *uninstaller) firewallInventory(ctx context.Context) fwInventory {
	var inv fwInventory
	if !u.h.has("ufw") {
		return inv
	}
	inv.Installed = true
	if u.h.euid() != 0 {
		return inv
	}
	out, err := u.h.tryRun(ctx, "ufw", "show", "added")
	if err != nil {
		return inv
	}
	inv.Known = true
	pre := map[string]bool{}
	if u.rcpt.Firewall != nil {
		for _, r := range u.rcpt.Firewall.Rules {
			if r.Preexisting {
				pre[r.Spec] = true
			}
		}
	}
	inv.Items, inv.Foreign = parseUfwAdded(out, pre)
	if st, err := u.h.tryRun(ctx, "ufw", "status"); err == nil {
		inv.Active = strings.Contains(st, "Status: active")
	}
	return inv
}

// ---- files BOBRES wrote ---------------------------------------------------------------------

// fileAction is what the uninstall does with a file.
type fileAction int

const (
	fileNone    fileAction = iota // not there, or not BOBRES's
	fileDelete                    // BOBRES's own file: delete
	fileRestore                   // put the recorded original back
	fileStrip                     // take BOBRES's header line out and keep the rest
	fileKeep                      // BOBRES's, but it is left (Why says why)
)

// sharedFile is a file the hardening wrote and how the uninstall treats it.
type sharedFile struct {
	Path   string
	Action fileAction
	Why    string
	// Data is what a restore writes.
	Data []byte
	// AfterReload: fail2ban reads this file.
	Reload string
}

const (
	hardenMarker = "Written by bobres harden"
	jailMarker   = hardenMarker
	secOnlyMark  = hardenMarker + ": unattended-upgrades installs security updates only"
	autoUpMark   = hardenMarker + ": refresh the package lists"
)

// fileInventory looks at the three files bobres harden writes.
func (u *uninstaller) fileInventory() []sharedFile {
	var out []sharedFile
	own := func(path, marker, reload string) {
		sf := sharedFile{Path: path, Reload: reload}
		data, kind := u.readOwnFile(path)
		switch {
		case kind == "absent":
			sf.Action = fileNone
		case kind != "":
			sf.Action, sf.Why = fileKeep, kind
		case !strings.Contains(string(data), marker):
			sf.Action, sf.Why = fileKeep, "it does not carry BOBRES's header: you changed it, or it is not BOBRES's"
		default:
			sf.Action = fileDelete
		}
		out = append(out, sf)
	}
	own(fail2banJail, jailMarker, "fail2ban")
	own(securityOnlyConf, secOnlyMark, "")

	sf := sharedFile{Path: autoUpgradesConf}
	data, kind := u.readOwnFile(autoUpgradesConf)
	switch {
	case kind == "absent":
		sf.Action = fileNone
	case kind != "":
		sf.Action, sf.Why = fileKeep, kind
	case !strings.Contains(string(data), autoUpMark):
		sf.Action = fileNone // the settings of the system's own: nothing of BOBRES's in it
	default:
		rec, ok := u.rcpt.file(autoUpgradesConf)
		sum := sha256.Sum256(data)
		switch {
		case !ok:
			sf.Action, sf.Why = fileStrip, "no receipt: only BOBRES's header line is taken out, the settings stay"
		case hex.EncodeToString(sum[:]) != rec.Wrote:
			sf.Action, sf.Why = fileKeep, "it was changed after BOBRES wrote it"
		case !rec.Existed:
			sf.Action = fileDelete
		default:
			if before, ok := rec.beforeContent(); ok {
				sf.Action, sf.Data = fileRestore, before
			} else {
				sf.Action, sf.Why = fileStrip, "what it held before is not recorded: only BOBRES's header line is taken out"
			}
		}
	}
	out = append(out, sf)
	return out
}

// readOwnFile reads a regular file of at most 1 MiB. kind is "absent" when there is none and a
// reason when the path is something else (a link, a directory, too big).
func (u *uninstaller) readOwnFile(path string) (data []byte, kind string) {
	fi, err := os.Lstat(u.h.p(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, "absent"
	case err != nil:
		return nil, "it cannot be read: " + err.Error()
	case !fi.Mode().IsRegular():
		return nil, "it is not a regular file"
	case fi.Size() > 1<<20:
		return nil, "it is unexpectedly large"
	}
	b, err := os.ReadFile(u.h.p(path))
	if err != nil {
		return nil, "it cannot be read: " + err.Error()
	}
	return b, ""
}

// stripHeader removes BOBRES's header line (and nothing else) from a file's text.
func stripHeader(data []byte) []byte {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, hardenMarker) {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// ---- the swap file -----------------------------------------------------------------------

// swapInventory is the swap file and its fstab line.
type swapInventory struct {
	// Ours: the receipt says BOBRES made the file, and it still looks like it.
	Ours bool
	// Present: /swapfile exists, whoever made it.
	Present    bool
	Active     bool
	FstabLine  bool
	SizeMB     int
	NotOursWhy string
}

func (u *uninstaller) swapInventory() swapInventory {
	var s swapInventory
	fi, err := os.Lstat(u.h.p(swapFile))
	s.Present = err == nil
	rec := u.rcpt.Swap
	if rec != nil && rec.Path == swapFile {
		s.FstabLine = rec.FstabAdded && u.fstabHas(swapFile)
		s.SizeMB = rec.SizeMB
	}
	if b, err := os.ReadFile(u.h.p("/proc/swaps")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == swapFile {
				s.Active = true
			}
		}
	}
	if !s.Present {
		return s
	}
	switch {
	case rec == nil || rec.Path != swapFile:
		s.NotOursWhy = "no receipt says BOBRES made it"
	case !fi.Mode().IsRegular():
		s.NotOursWhy = "it is not a regular file"
	case fi.Size() != int64(rec.SizeMB)<<20:
		s.NotOursWhy = "its size is not the one BOBRES made"
	default:
		s.Ours = true
	}
	return s
}

// fstabHas reports whether /etc/fstab has the swap file's line.
func (u *uninstaller) fstabHas(dev string) bool {
	b, err := os.ReadFile(u.h.p("/etc/fstab"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if isSwapFstabLine(line, dev) {
			return true
		}
	}
	return false
}

// isSwapFstabLine is the line BOBRES adds: "/swapfile none swap sw 0 0", whatever the spacing.
func isSwapFstabLine(line, dev string) bool {
	f := strings.Fields(line)
	return len(f) == 6 && f[0] == dev && f[1] == "none" && f[2] == "swap" && f[3] == "sw" && f[4] == "0" && f[5] == "0"
}

// ---- packages --------------------------------------------------------------------------------

// pkgState is a recorded group of packages and whether it can go.
type pkgState struct {
	Name  string
	Group pkgGroup
	// Installed lists the group's packages that are installed now.
	Installed []string
	// Remove is what would be purged: the requested packages and the dependencies that apt still
	// counts as installed automatically; Keep the ones left alone and why.
	Remove []string
	Keep   map[string]string
	// Blocked is why the group cannot be removed at all ("" when it can).
	Blocked string
}

// installedSet is the packages dpkg lists as installed.
func (h *hostSys) installedSet(ctx context.Context) (map[string]bool, error) {
	out, err := h.tryRun(ctx, "dpkg-query", "-W", "-f=${Package} ${db:Status-Abbrev}\n")
	if err != nil && out == "" {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && strings.HasPrefix(f[1], "ii") {
			set[f[0]] = true
		}
	}
	return set, nil
}

// autoSet is the packages apt counts as installed automatically (as dependencies).
func (h *hostSys) autoSet(ctx context.Context) map[string]bool {
	out, _ := h.tryRun(ctx, "apt-mark", "showauto")
	set := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			set[l] = true
		}
	}
	return set
}

var aptRemvRe = regexp.MustCompile(`^(?:Purg|Remv)\s+(\S+)`)

// simulatePurge asks apt what purging pkgs would remove.
func (h *hostSys) simulatePurge(ctx context.Context, pkgs []string) (map[string]bool, error) {
	args := append([]string{"-s", "purge"}, pkgs...)
	out, err := h.tryRun(ctx, "apt-get", args...)
	if err != nil {
		return nil, fmt.Errorf("apt-get -s purge: %w: %s", err, lastLines(out, 2))
	}
	gone := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if m := aptRemvRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			name, _, _ := strings.Cut(m[1], ":")
			gone[name] = true
		}
	}
	return gone, nil
}

// packageState works out what removing a recorded group would do. Nothing is changed.
func (u *uninstaller) packageState(ctx context.Context, g pkgGroup, going map[string]bool) pkgState {
	st := pkgState{Name: g.Name, Group: g, Keep: map[string]string{}}
	if !u.h.has("dpkg-query") {
		st.Blocked = "this server has no dpkg"
		return st
	}
	installed, err := u.h.installedSet(ctx)
	if err != nil {
		st.Blocked = "the package list could not be read: " + err.Error()
		return st
	}
	for _, p := range g.Packages {
		if installed[p] {
			st.Installed = append(st.Installed, p)
		}
	}
	if len(st.Installed) == 0 {
		return st
	}
	if u.h.euid() != 0 {
		st.Blocked = "run as root to see what apt would remove"
		return st
	}
	auto := u.h.autoSet(ctx)
	requested := map[string]bool{}
	for _, p := range g.Requested {
		requested[p] = true
	}
	var primary, deps []string
	for _, p := range st.Installed {
		switch {
		case requested[p]:
			primary = append(primary, p)
		case auto[p]:
			deps = append(deps, p)
		default:
			st.Keep[p] = "installed on purpose since (apt counts it as manually installed)"
		}
	}
	set := append(append([]string(nil), primary...), deps...)
	// What other groups, removed before this one, take with them is free to go here too.
	inSet := map[string]bool{}
	for p := range going {
		inSet[p] = true
	}
	for _, p := range set {
		inSet[p] = true
	}
	if len(primary) == 0 && len(deps) == 0 {
		return st
	}
	// A dependency that something else needs would drag that program out with it: try each
	// alone, keep the ones that take only themselves (or other members of the set).
	var okDeps []string
	for _, d := range deps {
		gone, err := u.h.simulatePurge(ctx, []string{d})
		if err != nil {
			st.Keep[d] = "apt could not tell whether anything needs it"
			continue
		}
		if extra := outside(gone, inSet); len(extra) > 0 {
			st.Keep[d] = "needed by " + joinFirst(extra, 3)
			continue
		}
		okDeps = append(okDeps, d)
	}
	if len(primary) > 0 {
		gone, err := u.h.simulatePurge(ctx, primary)
		if err != nil {
			st.Blocked = err.Error()
			return st
		}
		allowed := map[string]bool{}
		for p := range going {
			allowed[p] = true
		}
		for _, p := range append(append([]string(nil), primary...), okDeps...) {
			allowed[p] = true
		}
		if extra := outside(gone, allowed); len(extra) > 0 {
			st.Blocked = "other packages need it: " + joinFirst(extra, 5)
			return st
		}
	}
	st.Remove = append(primary, okDeps...)
	sort.Strings(st.Remove)
	return st
}

// outside lists the packages of gone that are not in allowed.
func outside(gone, allowed map[string]bool) []string {
	var out []string
	for p := range gone {
		if !allowed[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// purgePackages purges st.Remove (requested first, so a dependency is free to go with them).
func (u *uninstaller) purgePackages(ctx context.Context, st pkgState) error {
	if len(st.Remove) == 0 {
		return nil
	}
	args := append([]string{"purge", "-y", "-q"}, st.Remove...)
	return u.h.do(ctx, "apt-get", args...)
}

// ---- the bobres command and the state directory ----------------------------------------------

// binFiles are the files of the bobres command in a bin directory.
func binFileNames(dir string) []string {
	return []string{dir + "/bobres", dir + "/bobres.prev"}
}

// standardBinDirs are the directories the CLI may be installed in, besides $BOBRES_BIN_DIR.
var standardBinDirs = []string{"/usr/local/bin", "/usr/local/sbin", "/usr/bin", "/usr/sbin", "/opt/bobres"}

// cliFile is a file of the bobres command.
type cliFile struct {
	Path string
	// Link: it is a symbolic link; Target is where it points (real path).
	Link   bool
	Target string
}

// cliInventory lists the bobres command's files and says whether the running one is an installed
// copy.
type cliInventory struct {
	Files []cliFile
	// Running is the real path of the running binary; RunningOwned: it is one of the files, or
	// lies inside the install directory (so it goes with it).
	Running      string
	RunningOwned bool
}

// binDirs are the directories to look in, in logical form.
func (u *uninstaller) binDirs() []string {
	dirs := []string{}
	if d := strings.TrimSpace(u.h.getenv("BOBRES_BIN_DIR")); d != "" && filepath.IsAbs(d) {
		dirs = append(dirs, filepath.Clean(d))
	}
	if u.rcpt.CLIPath != "" && filepath.IsAbs(u.rcpt.CLIPath) && filepath.Base(u.rcpt.CLIPath) == "bobres" {
		dir := filepath.Dir(u.rcpt.CLIPath)
		for _, s := range standardBinDirs {
			if dir == s {
				dirs = append(dirs, dir)
			}
		}
	}
	dirs = append(dirs, "/usr/local/bin")
	return uniqSorted(dirs)
}

// cliInventory looks for the command in the places it is installed.
func (u *uninstaller) cliInventory(installDir string) cliInventory {
	var inv cliInventory
	seen := map[string]bool{}
	add := func(logical string) {
		if seen[logical] {
			return
		}
		seen[logical] = true
		fi, err := os.Lstat(u.h.p(logical))
		if err != nil {
			return
		}
		cf := cliFile{Path: logical}
		if fi.Mode()&os.ModeSymlink != 0 {
			cf.Link = true
			if t, err := filepath.EvalSymlinks(u.h.p(logical)); err == nil {
				cf.Target = t
			}
		}
		inv.Files = append(inv.Files, cf)
	}
	for _, dir := range u.binDirs() {
		for _, f := range binFileNames(dir) {
			add(f)
		}
		if matches, err := filepath.Glob(u.h.p(dir + "/.bobres*")); err == nil {
			for _, m := range matches {
				add(dir + "/" + filepath.Base(m))
			}
		}
	}
	if exe, err := u.h.exe(); err == nil {
		inv.Running = exe
		for _, f := range inv.Files {
			if f.Path == exe || f.Target == exe {
				inv.RunningOwned = true
			}
		}
		if installDir != "" && underPath(installDir, exe) {
			inv.RunningOwned = true
		}
	}
	return inv
}

// stateFiles are the names install.sh and the CLI keep in the state directory.
var stateFileRes = []*regexp.Regexp{
	regexp.MustCompile(`^(keys\.seq|release\.version|checksums\.txt|` + regexp.QuoteMeta(receiptFile) + `)$`),
	regexp.MustCompile(`^\.(keys\.seq|release\.version|checksums\.txt|` + regexp.QuoteMeta(receiptFile) + `)\.tmp-\d+$`),
}

// stateInventory lists what the state directory holds: files of BOBRES's, and anything else.
func (u *uninstaller) stateInventory() (own, other []string) {
	entries, err := os.ReadDir(u.h.p(u.h.stateDir))
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		known := false
		for _, re := range stateFileRes {
			if re.MatchString(e.Name()) && !e.IsDir() {
				known = true
			}
		}
		if e.Name() == scriptMarkerDir && e.IsDir() {
			known = true
		}
		if known {
			own = append(own, e.Name())
		} else {
			other = append(other, e.Name())
		}
	}
	return own, other
}
