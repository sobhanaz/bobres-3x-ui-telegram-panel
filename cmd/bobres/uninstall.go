package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// `bobres uninstall --purge` is the day-one command: it removes everything installing BOBRES put
// on this server, so that the server is as it was before. It is written for a destructive job:
//
//   - It looks first (survey) and prints the exact plan with sizes and counts. --dry-run stops
//     there. Without --yes it asks for a token typed in full (the install's domain, else the host
//     name), and --yes only works together with --confirm <that token>.
//   - A final backup (--backup-to FILE) is written outside the install directory and verified
//     before anything is deleted; when it is asked for and fails, nothing is deleted.
//   - It removes in an order that can be resumed: units and timers first (nothing can start the
//     stack again), then the compose stack with its volumes, networks and images, then the
//     hardening and the shared-file edits, then the install directory, then BOBRES's state and
//     logs, and last the bobres command and the receipt. Every step looks at what is there now, so
//     running the command again finishes an interrupted run, and running it twice is harmless.
//   - It deletes only what it can prove is BOBRES's: Docker objects by the compose project label
//     and the bobres_ name, units by the bobres name, files by the header BOBRES wrote in them or
//     the install receipt, directories by their marker files and their real path. Never SSH
//     settings or the SSH firewall rule, DNS, other users' data or other Docker workloads.
//   - Docker and the packages BOBRES installed go only with --remove-docker and --remove-packages,
//     only when the receipt (or install.sh's marker) says BOBRES installed them, and only when
//     apt and Docker agree that nothing else needs them.
//   - It writes a log that outlives the wipe (/var/log/bobres-uninstall.log) and ends with a
//     scan of the server: "nothing of BOBRES is left", or what is, and why.

// purgeOptions are the flags of `bobres uninstall --purge`.
type purgeOptions struct {
	dir             string
	yes             bool
	confirm         string
	dryRun          bool
	backupTo        string
	noBackup        bool
	keepBackups     bool
	removeDocker    bool
	removePackages  bool
	disableFirewall bool
}

// uninstaller is the day-one uninstall with the things it touches, each replaceable in tests.
type uninstaller struct {
	*ops
	h     *hostSys
	rcpt  *receipt
	store *receiptStore
	// w receives everything printed: the screen, and the log without its colors.
	w       io.Writer
	t       termUI
	logFile *os.File
	opts    purgeOptions
	// keptNotes are the things found while removing that stay, with the reason.
	keptNotes []string
	// failures are the steps that did not finish.
	failures []string
	// imageIDs are the images the project's containers ran, seen before they were removed.
	imageIDs map[string]bool
}

func newUninstaller(o *ops) *uninstaller {
	h := o.host
	if h == nil {
		// This code deletes. A test that reaches the real server by mistake stops here, whoever
		// runs it and as whatever user.
		if testing.Testing() {
			panic("bobres uninstall: a test must set ops.host, so that the real server is never touched")
		}
		h = newHostSys()
	}
	return &uninstaller{ops: o, h: h, t: detectUI(o.out, os.Getenv), store: newReceiptStore(h.p(h.stateDir)), rcpt: &receipt{}}
}

// dualWriter prints to the screen and, without color, to the log.
type dualWriter struct {
	out io.Writer
	log io.Writer
}

func (d *dualWriter) Write(p []byte) (int, error) {
	n, err := d.out.Write(p)
	if d.log != nil {
		_, _ = d.log.Write(ansiRe.ReplaceAll(p, nil))
	}
	return n, err
}

// say prints one line with the mark of an outcome.
func (u *uninstaller) say(kind markKind, format string, args ...any) {
	if u.t.fancy {
		fmt.Fprintf(u.w, "  %s %s\n", u.t.mark(kind), fmt.Sprintf(format, args...))
		return
	}
	fmt.Fprintf(u.w, "  %-5s %s\n", u.t.mark(kind), fmt.Sprintf(format, args...))
}

// printf prints a line (or lines) as they are.
func (u *uninstaller) printf(format string, args ...any) { fmt.Fprintf(u.w, format, args...) }

// heading prints a section title.
func (u *uninstaller) heading(s string) {
	fmt.Fprintln(u.w, u.t.paint(roleBold, u.t.paint(roleTeal, s)))
}

// openLog starts the log that outlives the wipe. A log that cannot be opened is a warning, not a
// reason to stop (the screen has the same words).
func (u *uninstaller) openLog(args []string) {
	u.w = u.out
	path := u.h.p(u.h.logPath)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // the uninstall's own log
	if err != nil {
		fmt.Fprintf(u.errOut, "warning: the uninstall log %s cannot be opened (%v): this run is not recorded there.\n", u.h.logPath, err)
		return
	}
	_ = f.Chmod(0o600)
	u.logFile = f
	u.w = &dualWriter{out: u.out, log: f}
	fmt.Fprintf(f, "\n==== bobres uninstall %s, %s, %s ====\n", version.Version, u.now().UTC().Format(time.RFC3339), redactArgs(args))
}

// redactArgs is the command line without the value of --confirm.
func redactArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = a
		if i > 0 && args[i-1] == "--confirm" {
			out[i] = "<token>"
		}
		if strings.HasPrefix(a, "--confirm=") {
			out[i] = "--confirm=<token>"
		}
	}
	return strings.Join(out, " ")
}

func (u *uninstaller) closeLog() {
	if u.logFile != nil {
		_ = u.logFile.Close()
		u.logFile = nil
	}
}

// ---- the survey: everything BOBRES put on this server ---------------------------------------------

// world is what a survey finds. The plan prints it, the steps work from fresh surveys of their
// own part, and the final scan surveys again.
type world struct {
	dir     *dirState
	size    sizeInfo
	backups sizeInfo
	units   []unitEntry
	docker  dockerInventory
	fw      fwInventory
	files   []sharedFile
	swap    swapInventory
	groups  []pkgState
	cli     cliInventory
	// stateOwn and stateOther are the entries of the state directory that are BOBRES's, and not.
	stateOwn, stateOther []string
	installLog           bool
}

// survey reads the server. Nothing is changed.
func (u *uninstaller) survey(ctx context.Context, dir *dirState, deep bool) *world {
	w := &world{dir: dir}
	if dir.Exists {
		w.size = treeSize(dir.Real)
		w.backups = treeSize(filepath.Join(dir.Real, "backups"))
	}
	w.units = u.h.unitInventory(ctx)
	w.docker = u.dockerInventory(ctx, dir)
	w.fw = u.firewallInventory(ctx)
	w.files = u.fileInventory()
	w.swap = u.swapInventory()
	w.cli = u.cliInventory(dir.Real)
	w.stateOwn, w.stateOther = u.stateInventory()
	if fi, err := os.Lstat(u.h.p(installLogName)); err == nil && fi.Mode().IsRegular() {
		w.installLog = true
	}
	// In the order they are removed in, so that the plan counts what an earlier group frees.
	going := map[string]bool{}
	for _, g := range u.removalOrder() {
		if deep && u.wantsRemoval(g.Name) {
			st := u.packageState(ctx, g, going)
			for _, p := range st.Remove {
				going[p] = true
			}
			w.groups = append(w.groups, st)
		} else {
			w.groups = append(w.groups, u.packageBrief(ctx, g))
		}
	}
	return w
}

// installLogName is where install.sh and bobres install write their log.
const installLogName = "/var/log/bobres-install.log"

// wantsRemoval says whether a flag asks for the group to go.
func (u *uninstaller) wantsRemoval(group string) bool {
	if group == "docker" {
		return u.opts.removeDocker
	}
	return u.opts.removePackages
}

// packageBrief is a package group's installed packages, without asking apt what removing them does.
func (u *uninstaller) packageBrief(ctx context.Context, g pkgGroup) pkgState {
	st := pkgState{Name: g.Name, Group: g, Keep: map[string]string{}}
	if !u.h.has("dpkg-query") {
		return st
	}
	if installed, err := u.h.installedSet(ctx); err == nil {
		for _, p := range g.Packages {
			if installed[p] {
				st.Installed = append(st.Installed, p)
			}
		}
	}
	return st
}

// actionable reports whether the survey finds anything for the purge to do.
func (w *world) actionable(u *uninstaller) bool {
	if w.dir.Exists || w.installLog || len(w.stateOwn) > 0 {
		return true
	}
	for _, e := range w.units {
		if !e.Foreign {
			return true
		}
	}
	if len(w.docker.Containers)+len(w.docker.Volumes)+len(w.docker.Networks)+len(w.docker.Images) > 0 {
		return true
	}
	for _, it := range w.fw.Items {
		if it.Delete {
			return true
		}
	}
	for _, f := range w.files {
		if f.Action == fileDelete || f.Action == fileRestore || f.Action == fileStrip {
			return true
		}
	}
	if w.swap.Ours {
		return true
	}
	if len(w.cli.Files) > 0 {
		return true
	}
	for _, g := range w.groups {
		if len(g.Installed) > 0 && u.wantsRemoval(g.Name) {
			return true
		}
	}
	return false
}

// ---- the plan ------------------------------------------------------------------------------------

// token is what must be typed to confirm: the install's domain, else this host's name, else a fixed
// phrase.
func (u *uninstaller) token(dir *dirState) string {
	if d := strings.TrimSpace(dir.Env["BOBRES_DOMAIN"]); d != "" {
		return d
	}
	if d := strings.TrimSpace(u.rcpt.Domain); d != "" {
		return d
	}
	if h, err := u.h.hostname(); err == nil && strings.TrimSpace(h) != "" {
		return strings.TrimSpace(h)
	}
	return "delete-bobres"
}

// printPlan prints what the purge would do, section by section, in the order it does it.
func (u *uninstaller) printPlan(w *world) {
	u.heading("BOBRES uninstall --purge: the plan")
	dir := w.dir
	if dir.Exists {
		ver := orDash(dir.Env["BOBRES_VERSION"])
		u.printf("Install directory:  %s (BOBRES %s, %s)\n", dir.Real, ver, orDash(dir.Env["BOBRES_DOMAIN"]))
		u.printf("                    %s in %d file(s); backups: %d file(s), %s\n",
			humanSize(w.size.Bytes), w.size.Files, w.backups.Files, humanSize(w.backups.Bytes))
	} else {
		u.printf("Install directory:  %s (not there)\n", dir.Given)
	}
	u.printf("\nIn this order:\n")
	n := 0
	section := func(title string, lines ...string) {
		n++
		if len(lines) == 0 {
			u.printf(" %d. %s: nothing to do\n", n, title)
			return
		}
		u.printf(" %d. %s\n", n, title)
		for _, l := range lines {
			u.printf("      %s\n", l)
		}
	}

	var lines []string
	for _, e := range w.units {
		switch {
		case e.Foreign:
			lines = append(lines, e.Name+": kept, no file of it in /etc/systemd (not placed by BOBRES)")
		default:
			extra := ""
			if len(e.Paths) > 1 {
				extra = fmt.Sprintf(" (+ %d more path(s): drop-ins, enable links)", len(e.Paths)-1)
			}
			lines = append(lines, "stop, disable and delete "+e.Name+extra)
		}
	}
	section("Systemd units and timers named bobres*", lines...)

	lines = nil
	d := w.docker
	switch {
	case !d.Present:
		lines = append(lines, "Docker is not installed: nothing in Docker")
	case !d.Reachable:
		lines = append(lines, "Docker does not answer: it will be started; if it still does not, the uninstall stops before changing anything")
	default:
		if len(d.Containers) > 0 {
			lines = append(lines, fmt.Sprintf("%d container(s): %s", len(d.Containers), joinFirst(containerNames(d.Containers), 8)))
		}
		if len(d.Volumes) > 0 {
			var total int64
			known := true
			for _, v := range d.Volumes {
				total += v.Size.Bytes
				known = known && v.SizeKnown
			}
			size := ""
			if known {
				size = ", " + humanSize(total)
			}
			lines = append(lines, fmt.Sprintf("%d volume(s)%s: %s  (the database, Redis and the HTTPS certificates)", len(d.Volumes), size, joinFirst(volumeNames(d.Volumes), 8)))
		}
		if len(d.Networks) > 0 {
			lines = append(lines, fmt.Sprintf("%d network(s): %s", len(d.Networks), joinFirst(networkNames(d.Networks), 8)))
		}
		if len(d.Images) > 0 {
			var total int64
			for _, im := range d.Images {
				total += humanBytes(im.Size)
			}
			lines = append(lines, fmt.Sprintf("%d image(s), about %s: %s (an image another container uses is kept)", len(d.Images), humanSize(total), joinFirst(imageRefs(d.Images), 6)))
		}
		for _, s := range d.Skipped {
			lines = append(lines, "kept: "+s)
		}
	}
	section("Docker: the compose project \"bobres\" (the only Docker objects touched)", lines...)

	lines = nil
	switch {
	case !w.fw.Installed:
	case !w.fw.Known:
		lines = append(lines, "ufw is installed but its rules cannot be read here (needs root)")
	default:
		for _, it := range w.fw.Items {
			if it.Delete {
				lines = append(lines, fmt.Sprintf("delete the rule \"%s\" (%s)", it.spec(), it.Comment))
			} else {
				lines = append(lines, fmt.Sprintf("keep the rule \"%s\" (%s): %s", it.spec(), it.Comment, it.Why))
			}
		}
		if w.fw.Active {
			if u.opts.disableFirewall && u.rcpt.Firewall != nil && u.rcpt.Firewall.EnabledByBobres {
				lines = append(lines, "turn ufw off again (BOBRES turned it on): --disable-firewall")
			} else {
				lines = append(lines, "ufw stays on (turning it off would open every port; --disable-firewall does it where BOBRES turned it on)")
			}
		}
	}
	section("Firewall (ufw): the rules BOBRES added", lines...)

	lines = nil
	for _, f := range w.files {
		switch f.Action {
		case fileDelete:
			lines = append(lines, "delete "+f.Path)
		case fileRestore:
			lines = append(lines, "put back the original of "+f.Path)
		case fileStrip:
			lines = append(lines, "take BOBRES's header line out of "+f.Path+" ("+f.Why+")")
		case fileKeep:
			lines = append(lines, "keep "+f.Path+": "+f.Why)
		}
	}
	switch {
	case w.swap.Ours:
		lines = append(lines, fmt.Sprintf("swap off and delete %s (%d MB, made by BOBRES) and its line in /etc/fstab", swapFile, w.swap.SizeMB))
	case w.swap.Present && u.rcpt.Swap == nil:
		lines = append(lines, "keep "+swapFile+": "+w.swap.NotOursWhy)
	case w.swap.Present:
		lines = append(lines, "keep "+swapFile+": "+w.swap.NotOursWhy)
	}
	section("Server settings the hardening wrote", lines...)

	lines = nil
	for _, g := range w.groups {
		lines = append(lines, u.groupPlanLine(g)...)
	}
	section("Software BOBRES installed (only with --remove-docker / --remove-packages)", lines...)

	lines = nil
	if dir.Exists {
		if u.opts.keepBackups {
			lines = append(lines, fmt.Sprintf("delete everything in %s except backups/ (%d file(s), %s stay)", dir.Real, w.backups.Files, humanSize(w.backups.Bytes)))
		} else {
			lines = append(lines, fmt.Sprintf("delete %s with the .env (the secrets and the backup key), the certificates folder and every backup", dir.Real))
		}
	}
	section("The install directory", lines...)

	lines = nil
	if w.installLog {
		lines = append(lines, "delete "+installLogName)
	}
	for _, name := range w.stateOwn {
		if name == receiptFile || name == scriptMarkerDir {
			continue // deleted last, with the command
		}
		lines = append(lines, "delete "+filepath.Join(u.h.stateDir, name))
	}
	for _, name := range w.stateOther {
		lines = append(lines, "keep "+filepath.Join(u.h.stateDir, name)+": not a file of BOBRES's")
	}
	section("BOBRES's state and logs", lines...)

	lines = nil
	for _, f := range w.cli.Files {
		if f.Link {
			lines = append(lines, "delete the link "+f.Path+" (the file it points to is left unless it is in the install directory)")
		} else {
			lines = append(lines, "delete "+f.Path)
		}
	}
	if w.cli.Running != "" && !w.cli.RunningOwned {
		lines = append(lines, "keep "+w.cli.Running+": the copy you are running is not an installed one")
	}
	if len(w.stateOwn) > 0 {
		lines = append(lines, "delete the install receipt and install.sh's records, then "+u.h.stateDir+", last")
	}
	section("The bobres command", lines...)

	u.printf("\nNever touched: SSH settings and the SSH firewall rule, DNS, other users' data, other Docker containers, images and volumes.\n")
	u.printf("Kept: %s (the record of this run).\n", u.h.logPath)
}

// groupPlanLine describes one recorded package group in the plan.
func (u *uninstaller) groupPlanLine(g pkgState) []string {
	if len(g.Installed) == 0 {
		return nil
	}
	name := g.Name
	if g.Name == "docker" {
		name = "Docker"
	}
	if !u.wantsRemoval(g.Name) {
		flag := "--remove-packages"
		if g.Name == "docker" {
			flag = "--remove-docker"
		}
		return []string{fmt.Sprintf("keep %s (installed by %s): %s removes it", name, g.Group.By, flag)}
	}
	var out []string
	switch {
	case g.Blocked != "":
		out = append(out, fmt.Sprintf("keep %s: %s", name, g.Blocked))
	case len(g.Remove) > 0:
		out = append(out, fmt.Sprintf("purge %s: %s", name, joinFirst(g.Remove, 12)))
	}
	for p, why := range g.Keep {
		out = append(out, fmt.Sprintf("keep the package %s: %s", p, why))
	}
	return out
}

func joinFirst(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" and %d more", len(items)-n)
}

func containerNames(cs []dockerCtr) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func volumeNames(vs []dockerVol) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

func networkNames(ns []dockerNet) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

func imageRefs(ims []dockerImg) []string {
	var out []string
	for _, im := range ims {
		out = append(out, im.Ref)
	}
	return out
}

// ---- the command ---------------------------------------------------------------------------------

// purge runs `bobres uninstall --purge`. Exit codes: 0 done (nothing of BOBRES is left, apart from
// what was kept on purpose), 1 cancelled or failed (what is left is listed), 2 refused (nothing was
// changed).
func (u *uninstaller) purge(ctx context.Context, opts purgeOptions, args []string) int {
	u.opts = opts
	u.w = u.out
	if !opts.dryRun {
		u.openLog(args)
		defer u.closeLog()
	}

	// The receipt (and what install.sh recorded) says what BOBRES did here.
	rc, err := u.store.load()
	if err != nil {
		fmt.Fprintf(u.errOut, "warning: %v: continuing without it (the markers BOBRES wrote are used instead)\n", err)
	}
	u.rcpt = rc
	importScriptMarkers(u.h.p(u.h.stateDir), u.rcpt)

	home := u.h.getenv("HOME")
	protected := []string{u.h.p(u.h.stateDir), u.h.p(filepath.Dir(u.h.logPath)), home}
	if su := u.h.getenv("SUDO_USER"); su != "" && !strings.ContainsAny(su, "/\x00") {
		protected = append(protected, "/home/"+su)
	}
	dir, err := resolveInstallDir(opts.dir, protected...)
	if err != nil {
		return u.refused(err)
	}
	for _, p := range []string{u.h.p(u.h.stateDir), u.h.p(u.h.logPath)} {
		if dir.Exists && underPath(dir.Real, p) || underPath(p, dir.Real) {
			return u.refused(refuse("refusing %s: it overlaps %s, which BOBRES keeps outside the install directory so that it survives the wipe", dir.Real, p))
		}
	}
	if u.rcpt.InstallDir != "" && dir.Exists && dir.Install && filepath.Clean(u.rcpt.InstallDir) != dir.Real && filepath.Clean(u.rcpt.InstallDir) != dir.Given {
		return u.refused(refuse("this server's install receipt says BOBRES was installed in %s, not in %s: nothing was changed. "+
			"Check --dir (or BOBRES_DIR)", u.rcpt.InstallDir, dir.Real))
	}

	world := u.survey(ctx, dir, true)
	if name, wd := foreignWorkDir(world.docker.Containers, dir); name != "" {
		return u.refused(refuse("the BOBRES containers on this server (%s) belong to the compose project in %s, not in %s: nothing was changed. "+
			"Check --dir (or BOBRES_DIR)", name, wd, dir.Real))
	}
	token := u.token(dir)
	u.printPlan(world)

	if opts.dryRun {
		if opts.backupTo != "" {
			u.printf("\nWith --backup-to a verified backup would be written to %s first.\n", opts.backupTo)
		}
		u.printf("\nDry run: nothing was changed.\n")
		return 0
	}
	if !world.actionable(u) {
		u.printf("\nNothing to remove: no BOBRES install, containers, volumes, units, rules or files were found.\n")
		return u.finish(ctx, dir, false)
	}
	u.printf("\nThis cannot be undone. To confirm you must type %q exactly.\n", token)
	if u.h.euid() != 0 {
		fmt.Fprintf(u.errOut, "error: removing BOBRES changes the system: run it as root (sudo bobres uninstall --purge). Nothing was changed.\n")
		return 1
	}

	// The two questions: a final backup, then the token.
	backupDest, ok := u.askBackup(dir)
	if !ok {
		u.printf("Cancelled. Nothing was changed.\n")
		return 1
	}
	if !u.confirmToken(token) {
		u.printf("Cancelled. Nothing was changed.\n")
		return 1
	}

	if u.h.chdir != nil {
		_ = u.h.chdir("/") // the shell may be inside the directory that goes
	}
	unlock, err := u.lock()
	if err != nil {
		fmt.Fprintf(u.errOut, "error: %v\n", err)
		return 1
	}
	defer unlock()

	if backupDest != "" {
		u.heading("\nFinal backup")
		path, err := u.finalBackup(ctx, dir, backupDest)
		if err != nil {
			fmt.Fprintf(u.errOut, "error: the final backup failed: %v\nNothing was deleted.\n", err)
			u.printf("The final backup failed: %v. Nothing was deleted.\n", err)
			return 1
		}
		_ = path
	}
	return u.run(ctx, dir, world)
}

// refused prints a refusal and returns its exit code.
func (u *uninstaller) refused(err error) int {
	var r *refusal
	if errors.As(err, &r) {
		fmt.Fprintf(u.errOut, "error: %s\n", r.msg)
		if u.logFile != nil {
			fmt.Fprintf(u.logFile, "refused: %s\n", r.msg)
		}
		return 2
	}
	fmt.Fprintf(u.errOut, "error: %v\n", err)
	return 1
}

// confirmToken asks for the token, or checks --confirm.
func (u *uninstaller) confirmToken(token string) bool {
	if u.opts.yes {
		if u.opts.confirm != token {
			fmt.Fprintf(u.errOut, "error: --confirm must be exactly %q (the install's domain, else this host's name). Nothing was changed.\n", token)
			return false
		}
		return true
	}
	fmt.Fprintf(u.out, "Type %s to delete all of it: ", token)
	line, ok := readLine(u.in)
	if !ok || line != token {
		return false
	}
	return true
}

// readLine reads one line without reading past it (the menu shares its reader).
func readLine(r io.Reader) (string, bool) {
	var sb strings.Builder
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n == 1 {
			if b[0] == '\n' {
				return strings.TrimRight(sb.String(), "\r"), true
			}
			sb.WriteByte(b[0])
			if sb.Len() > 4096 {
				return sb.String(), true
			}
		}
		if err != nil {
			return strings.TrimRight(sb.String(), "\r"), sb.Len() > 0
		}
	}
}

// lock keeps two uninstalls from running side by side.
func (u *uninstaller) lock() (func(), error) {
	path := u.h.p("/run/lock/bobres-uninstall.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // /run/lock
		return func() {}, nil //nolint:nilerr // no lock directory: go on without one
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // the uninstall's own lock
	if err != nil {
		return func() {}, nil //nolint:nilerr // as above
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // a file descriptor
		_ = f.Close()
		return nil, errors.New("another bobres uninstall is running")
	}
	return func() {
		_ = os.Remove(path)
		_ = f.Close()
	}, nil
}

// ---- the final backup --------------------------------------------------------------------------

// askBackup settles the final backup: --backup-to, --no-backup, or a question. It returns the
// destination ("" for none) and false when the answer cancels the uninstall.
func (u *uninstaller) askBackup(dir *dirState) (string, bool) {
	switch {
	case u.opts.backupTo != "":
		return u.opts.backupTo, true
	case u.opts.noBackup:
		return "", true
	case !dir.Install:
		return "", true // no database to back up
	}
	def := u.defaultBackupPath()
	fmt.Fprintf(u.out, "\nA final backup lets you bring this store back on a new server (bobres restore). Make one first?\n"+
		"Press Enter to write it to %s, type another file or directory, or type no to go without: ", def)
	line, ok := readLine(u.in)
	if !ok {
		fmt.Fprintln(u.out)
		fmt.Fprintln(u.errOut, "No answer: cancelled. Use --backup-to FILE, or --no-backup to go without one.")
		return "", false
	}
	switch strings.TrimSpace(line) {
	case "":
		return def, true
	case "no", "No", "NO", "n", "N":
		return "", true
	}
	return strings.TrimSpace(line), true
}

// defaultBackupPath is where the final backup goes unless told otherwise: the invoking user's home
// directory (root's, on a server), never inside the install directory.
func (u *uninstaller) defaultBackupPath() string {
	home := u.h.getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		home = "/root"
	}
	return filepath.Join(home, "bobres-final-"+u.now().UTC().Format("20060102T150405Z")+".bkp")
}

// finalBackup writes a backup of the database outside the install directory, checks the whole
// file, and returns its path. Any failure removes what was written.
func (u *uninstaller) finalBackup(ctx context.Context, dir *dirState, dest string) (string, error) {
	if !dir.Install {
		return "", errors.New("there is no install here to back up")
	}
	env := dir.Env
	key := strings.TrimSpace(env["BOBRES_BACKUP_KEY"])
	if len(key) < backup.MinKeyLen {
		return "", errors.New("this install has no backup key (BOBRES_BACKUP_KEY in .env), so a backup cannot be made")
	}
	destDir, name, err := u.backupTarget(dir, dest)
	if err != nil {
		return "", err
	}
	// The database must run.
	if err := u.ensurePostgres(ctx, dir); err != nil {
		return "", err
	}
	u.printf("Backing up the database to %s ...\n", filepath.Join(destDir, name))
	path := filepath.Join(destDir, name)
	if _, _, err := u.backupTo(ctx, dir.Real, destDir, name, env, -1); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	sum, err := verifyBackupFile(path, []byte(key))
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("the backup written to %s did not verify: %w", path, err)
	}
	if sum.DumpSize <= 0 {
		// A valid container around no database: the "backup" would hold nothing to restore.
		_ = os.Remove(path)
		return "", fmt.Errorf("the backup written to %s holds an empty database dump", path)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		_ = os.Remove(path)
		return "", fmt.Errorf("the backup written to %s is empty", path)
	}
	describeBackup(u.w, path, sum)
	u.printf("Final backup: %s (%s, every part checked).\n", path, humanSize(fi.Size()))
	u.printf("It opens only with this install's backup key (BOBRES_BACKUP_KEY), which is deleted with the install: keep the key with the file.\n"+
		"On a new server: install BOBRES, then  sudo bobres restore %s --key-file <file holding the key>  (docs/runbooks/uninstall.md).\n", path)
	if u.tty || interactive(u.in, u.out) {
		fmt.Fprintf(u.out, "\nThe backup key (shown only now, and not written to the log):\n  %s\n", key)
	}
	return path, nil
}

// backupTarget resolves where the final backup goes: a new file in an existing directory outside
// the install, the state directory and Docker's. An existing directory gets a file named after the
// time.
func (u *uninstaller) backupTarget(dir *dirState, dest string) (destDir, name string, err error) {
	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", "", err
	}
	name = "bobres-final-" + u.now().UTC().Format("20060102T150405Z") + ".bkp"
	if fi, err := os.Stat(abs); err == nil {
		if !fi.IsDir() {
			return "", "", fmt.Errorf("%s already exists: it is not overwritten (give a new file name)", abs)
		}
		destDir = abs
	} else {
		destDir, name = filepath.Dir(abs), filepath.Base(abs)
	}
	real, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		return "", "", fmt.Errorf("the directory %s for the backup does not exist: %w", destDir, err)
	}
	if !strings.HasSuffix(name, ".bkp") {
		return "", "", fmt.Errorf("the backup's file name must end in .bkp (got %q), as bobres restore expects", name)
	}
	if !safeFileRe.MatchString(name) {
		return "", "", fmt.Errorf("%q is not a plain file name", name)
	}
	for _, bad := range []string{dir.Real, u.h.p(u.h.stateDir), u.h.p("/var/lib/docker"), u.h.p("/var/lib/containerd")} {
		if underPath(bad, real) {
			return "", "", fmt.Errorf("%s is inside %s, which the uninstall deletes: choose a directory elsewhere", real, bad)
		}
	}
	if gb := u.freeGB(real); gb >= 0 && gb < 0.5 {
		return "", "", fmt.Errorf("only %.1f GB free in %s", gb, real)
	}
	if _, err := os.Lstat(filepath.Join(real, name)); err == nil {
		return "", "", fmt.Errorf("%s already exists: it is not overwritten", filepath.Join(real, name))
	}
	if strings.HasPrefix(real, "/tmp") || strings.HasPrefix(real, "/run") || strings.HasPrefix(real, "/dev/shm") {
		u.printf("Warning: %s is on temporary storage; copy the backup somewhere safe before you restart this server.\n", real)
	}
	return real, name, nil
}

// ensurePostgres makes sure the database runs for the dump (a stopped stack has its containers
// and volumes; only the database is started).
func (u *uninstaller) ensurePostgres(ctx context.Context, dir *dirState) error {
	ps, err := u.ops.run(ctx, dir.Real, composeArgs("ps", "--format", "json", "postgres")...)
	if err == nil && parseHealth(ps)["postgres"] == "healthy" {
		return nil
	}
	u.printf("Starting the database for the backup...\n")
	if out, err := u.ops.run(ctx, dir.Real, composeArgs("up", "-d", "postgres")...); err != nil {
		return fmt.Errorf("the database could not be started for the backup: %w: %s", err, lastLines(string(out), 3))
	}
	if code := waitHealthy(ctx, u.ops.run, u.sleep, dir.Real, []string{"postgres"}, io.Discard, u.errOut); code != 0 {
		return errors.New("the database did not become healthy for the backup")
	}
	return nil
}

// ---- the removal ---------------------------------------------------------------------------------

// step is one part of the removal.
type step struct {
	title string
	run   func(ctx context.Context) error
	// stops: a failure ends the run here (nothing after it may depend on this part being undone).
	stops bool
	// last: only runs when every step before it succeeded.
	last bool
}

// run removes everything, step by step. A step looks at what is there now.
func (u *uninstaller) run(ctx context.Context, dir *dirState, w *world) int {
	u.printf("\n")
	steps := []step{
		{title: "Systemd units and timers", stops: true, run: u.stepUnits},
		{title: "Docker: containers, volumes, networks, images", stops: true, run: func(ctx context.Context) error { return u.stepDocker(ctx, dir) }},
		{title: "Firewall rules", run: u.stepFirewall},
		{title: "Server settings", run: u.stepSettings},
		{title: "Software BOBRES installed", run: u.stepSoftware},
		{title: "The install directory", run: func(ctx context.Context) error { return u.stepDir(ctx, dir) }},
		{title: "State and logs", last: true, run: u.stepState},
		{title: "The bobres command and the receipt", last: true, run: func(ctx context.Context) error { return u.stepCommand(ctx, dir) }},
	}
	stopped := false
	for i, s := range steps {
		if stopped || (s.last && len(u.failures) > 0) {
			continue
		}
		u.heading(fmt.Sprintf("[%d/%d] %s", i+1, len(steps), s.title))
		if err := s.run(ctx); err != nil {
			u.say(markFail, "%v", err)
			u.failures = append(u.failures, s.title)
			if s.stops {
				stopped = true
			}
		}
	}
	if stopped || len(u.failures) > 0 {
		u.printf("\nThe uninstall did not finish (%s). Nothing was left half-done that running the same command again cannot finish:\n"+
			"  sudo bobres uninstall --purge --dir %s\n", strings.Join(u.failures, "; "), dir.Real)
	}
	return u.finish(ctx, dir, true)
}

// finish scans the server and reports. After a run (ran) a failure makes the exit code 1 on its own.
func (u *uninstaller) finish(ctx context.Context, dir *dirState, ran bool) int {
	left, kept := u.scan(ctx, dir)
	u.reportScan(left, kept)
	if len(left) > 0 || (ran && len(u.failures) > 0) {
		return 1
	}
	return 0
}

func (u *uninstaller) stepUnits(ctx context.Context) error {
	// An update that is running now is not cut off by its own removal.
	for _, e := range u.h.unitInventory(ctx) {
		if !e.Foreign && (e.Active == "activating" || e.Active == "deactivating") {
			return fmt.Errorf("%s is busy right now (%s; an update or a check may be running): wait for it to finish, then run this again", e.Name, e.Active)
		}
	}
	// The dashboard updater's units have a remover of their own (bobres config set
	// BOBRES_UPDATE_FROM_DASHBOARD off uses it): it goes first, and what it leaves, or any other unit
	// named bobres*, is found and removed below.
	if u.units != nil {
		if res := u.units.uninstall(ctx); res.text != "" {
			u.say(res.kind, "Dashboard updater: %s.", res.text)
		}
	}
	units := u.h.unitInventory(ctx)
	var own []unitEntry
	for _, e := range units {
		if e.Foreign {
			u.say(markWarn, "%s is known to systemd but was not placed by BOBRES (no file of it in /etc/systemd): kept", e.Name)
			u.keptNotes = append(u.keptNotes, e.Name+": known to systemd, but no file of it lies in /etc/systemd")
			continue
		}
		own = append(own, e)
	}
	if len(own) == 0 {
		u.say(markSkip, "no unit named bobres* found")
		return nil
	}
	for _, e := range own {
		if e.Active == "activating" || e.Active == "deactivating" {
			return fmt.Errorf("%s is busy right now (%s; an update or a check may be running): wait for it to finish, then run this again", e.Name, e.Active)
		}
	}
	if u.h.has("systemctl") {
		for _, e := range own { // timers and paths first: the order of unitInventory
			if !e.Known {
				continue
			}
			if err := u.h.do(ctx, "systemctl", "stop", e.Name); err != nil {
				if st, _ := u.h.tryRun(ctx, "systemctl", "is-active", e.Name); strings.TrimSpace(st) == "active" || strings.TrimSpace(st) == "activating" {
					return fmt.Errorf("%s could not be stopped: %w", e.Name, err)
				}
			}
			if err := u.h.do(ctx, "systemctl", "disable", e.Name); err != nil {
				u.say(markWarn, "systemctl disable %s: %v", e.Name, err)
			}
			u.say(markOK, "stopped and disabled %s", e.Name)
		}
	}
	for _, e := range own {
		for _, p := range e.Paths {
			if !ownUnitPath(p) {
				return fmt.Errorf("refusing to delete %s: it is not a unit file of BOBRES's", p)
			}
			if err := os.RemoveAll(u.h.p(p)); err != nil {
				return fmt.Errorf("delete %s: %w", p, err)
			}
		}
		u.say(markOK, "deleted %s (%d path(s))", e.Name, len(e.Paths))
	}
	if u.h.has("systemctl") {
		if err := u.h.do(ctx, "systemctl", "daemon-reload"); err != nil {
			u.say(markWarn, "%v", err)
		}
		args := []string{"reset-failed"}
		for _, e := range own {
			args = append(args, e.Name)
		}
		_ = u.h.do(ctx, "systemctl", args...)
	}
	for _, e := range u.h.unitInventory(ctx) {
		if !e.Foreign {
			return fmt.Errorf("%s is still there after it was deleted", e.Name)
		}
	}
	return nil
}

func (u *uninstaller) stepDocker(ctx context.Context, dir *dirState) error {
	kept, err := u.removeDocker(ctx, dir)
	u.keptNotes = append(u.keptNotes, kept...)
	return err
}

func (u *uninstaller) stepFirewall(ctx context.Context) error {
	inv := u.firewallInventory(ctx)
	switch {
	case !inv.Installed:
		u.say(markSkip, "ufw is not installed")
		return nil
	case !inv.Known:
		return errors.New("the ufw rules cannot be read")
	}
	var failed []string
	removed := 0
	for _, it := range inv.Items {
		if !it.Delete {
			u.say(markSkip, "kept the rule %q (%s): %s", it.spec(), it.Comment, it.Why)
			continue
		}
		if err := u.h.do(ctx, "ufw", append([]string{"--force", "delete"}, it.Words...)...); err != nil {
			failed = append(failed, it.spec())
			u.say(markFail, "%v", err)
			continue
		}
		removed++
		u.say(markOK, "deleted the rule %q (%s)", it.spec(), it.Comment)
	}
	if u.opts.disableFirewall {
		switch {
		case u.rcpt.Firewall == nil || !u.rcpt.Firewall.EnabledByBobres:
			u.say(markWarn, "ufw stays on: the receipt does not say BOBRES turned it on")
			u.keptNotes = append(u.keptNotes, "ufw is on and stays on: no receipt says BOBRES turned it on")
		case inv.Active:
			if err := u.h.do(ctx, "ufw", "--force", "disable"); err != nil {
				failed = append(failed, "ufw disable")
				u.say(markFail, "%v", err)
			} else {
				u.say(markOK, "ufw is off again (BOBRES had turned it on)")
			}
		}
	}
	if removed == 0 && len(failed) == 0 {
		u.say(markSkip, "no rule of BOBRES's to delete")
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not delete: %s", strings.Join(failed, "; "))
	}
	return nil
}

func (u *uninstaller) stepSettings(ctx context.Context) error {
	var errs []string
	reload := ""
	for _, f := range u.fileInventory() {
		switch f.Action {
		case fileNone:
		case fileKeep:
			u.say(markWarn, "kept %s: %s", f.Path, f.Why)
			u.keptNotes = append(u.keptNotes, f.Path+": "+f.Why)
		case fileDelete:
			if err := os.Remove(u.h.p(f.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Sprintf("delete %s: %v", f.Path, err))
				continue
			}
			u.say(markOK, "deleted %s", f.Path)
			if f.Reload != "" {
				reload = f.Reload
			}
		case fileRestore, fileStrip:
			data := f.Data
			if f.Action == fileStrip {
				cur, _ := u.readOwnFile(f.Path)
				data = stripHeader(cur)
			}
			mode := os.FileMode(0o644)
			if fi, err := os.Stat(u.h.p(f.Path)); err == nil {
				mode = fi.Mode().Perm()
			}
			if err := writeAtomic(u.h.p(f.Path), data, mode); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if f.Action == fileRestore {
				u.say(markOK, "put back the original of %s", f.Path)
			} else {
				u.say(markOK, "took BOBRES's header line out of %s (the settings stay)", f.Path)
			}
		}
	}
	if reload == "fail2ban" && u.h.has("systemctl") {
		if st, _ := u.h.tryRun(ctx, "systemctl", "is-active", "fail2ban"); strings.TrimSpace(st) == "active" {
			if err := u.h.do(ctx, "systemctl", "reload", "fail2ban"); err != nil {
				u.say(markWarn, "fail2ban was not reloaded: %v (the SSH jail of BOBRES's is gone after its next start)", err)
			} else {
				u.say(markOK, "fail2ban reloaded without BOBRES's jail")
			}
		}
	}
	if err := u.removeSwap(ctx); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// removeSwap takes the swap file BOBRES made out of use and removes it with its fstab line.
func (u *uninstaller) removeSwap(ctx context.Context) error {
	s := u.swapInventory()
	if !s.Present && !s.FstabLine {
		return nil
	}
	if !s.Ours {
		if s.Present {
			u.say(markWarn, "kept %s: %s", swapFile, s.NotOursWhy)
			u.keptNotes = append(u.keptNotes, swapFile+": "+s.NotOursWhy)
		}
		return nil
	}
	if s.Active {
		if err := u.h.do(ctx, "swapoff", swapFile); err != nil {
			return fmt.Errorf("the swap file is in use and could not be swapped off: %w", err)
		}
	}
	if s.FstabLine {
		if err := u.dropFstabLine(); err != nil {
			return err
		}
		u.say(markOK, "took the swap file's line out of /etc/fstab")
	}
	if err := os.Remove(u.h.p(swapFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", swapFile, err)
	}
	u.say(markOK, "swapped off and deleted %s", swapFile)
	return nil
}

// dropFstabLine removes the swap file's line from /etc/fstab, in one rename, keeping the rest.
func (u *uninstaller) dropFstabLine() error {
	path := u.h.p("/etc/fstab")
	b, err := os.ReadFile(path) //nolint:gosec // the system's fstab
	if err != nil {
		return err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if !isSwapFstabLine(line, swapFile) {
			out = append(out, line)
		}
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return writeAtomic(path, []byte(strings.Join(out, "\n")), mode)
}

// groupOrder is the order package groups are removed in: the latest installed first, so that a
// dependency an earlier group shares is free when its turn comes.
var groupOrder = []string{"unattended-upgrades", "fail2ban", "ufw", "openssl"}

func (u *uninstaller) stepSoftware(ctx context.Context) error {
	if !u.opts.removePackages && !u.opts.removeDocker {
		for _, g := range u.rcpt.Packages {
			if st := u.packageBrief(ctx, g); len(st.Installed) > 0 {
				u.say(markSkip, "kept %s (installed by BOBRES): %s", g.Name, map[bool]string{true: "--remove-docker removes it", false: "--remove-packages removes it"}[g.Name == "docker"])
			}
		}
		return nil
	}
	var errs []string
	if u.opts.removePackages {
		for _, g := range u.removalOrder() {
			if g.Name == "docker" {
				continue
			}
			if err := u.removeGroup(ctx, g); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if u.opts.removeDocker {
		if err := u.removeDockerEngine(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// removeGroup purges a recorded package group when apt agrees that nothing else needs it.
func (u *uninstaller) removeGroup(ctx context.Context, g pkgGroup) error {
	st := u.packageState(ctx, g, nil)
	if len(st.Installed) == 0 {
		u.say(markSkip, "%s: none of its packages is installed", g.Name)
		return nil
	}
	if g.Name == "ufw" {
		inv := u.firewallInventory(ctx)
		if inv.Foreign > 0 {
			return fmt.Errorf("ufw is kept: it holds %d rule(s) that are not BOBRES's", inv.Foreign)
		}
		if inv.Active {
			if err := u.h.do(ctx, "ufw", "--force", "disable"); err != nil {
				return fmt.Errorf("ufw could not be turned off before its removal: %w", err)
			}
		}
		// ufw's own "disable" leaves the skeleton of its chains in the kernel; with the package going
		// nothing would ever remove them (until a reboot), so its own script does.
		if fi, err := os.Stat(u.h.p("/lib/ufw/ufw-init")); err == nil && fi.Mode().IsRegular() {
			if err := u.h.do(ctx, "/lib/ufw/ufw-init", "flush-all"); err != nil {
				u.say(markWarn, "ufw's chains were not flushed: %v", err)
			}
		}
	}
	if st.Blocked != "" {
		return fmt.Errorf("%s is kept: %s", g.Name, st.Blocked)
	}
	for p, why := range st.Keep {
		u.say(markWarn, "kept the package %s: %s", p, why)
		u.keptNotes = append(u.keptNotes, "package "+p+": "+why)
	}
	// The service goes first: where apt is told not to touch services (policy-rc.d in a container),
	// its prerm would leave the daemon running without its package.
	if unit := groupService[g.Name]; unit != "" && u.h.has("systemctl") {
		_ = u.h.do(ctx, "systemctl", "stop", unit)
	}
	if err := u.purgePackages(ctx, st); err != nil {
		return fmt.Errorf("%s: %w", g.Name, err)
	}
	u.say(markOK, "purged %s: %s", g.Name, strings.Join(st.Remove, ", "))
	return nil
}

// groupService is the service a hardening package group runs, stopped before the package goes.
var groupService = map[string]string{"fail2ban": "fail2ban", "unattended-upgrades": "unattended-upgrades"}

// removalOrder is the recorded package groups in the order they are removed in: the latest installed
// first, so that a dependency an earlier group shares is free when its turn comes; Docker's last.
func (u *uninstaller) removalOrder() []pkgGroup {
	var out []pkgGroup
	seen := map[string]bool{}
	for _, name := range groupOrder {
		if g, ok := u.rcpt.group(name); ok {
			out, seen[name] = append(out, g), true
		}
	}
	for _, g := range u.rcpt.Packages {
		if !seen[g.Name] && g.Name != "docker" {
			out, seen[g.Name] = append(out, g), true
		}
	}
	if g, ok := u.rcpt.group("docker"); ok {
		out = append(out, g)
	}
	return out
}

// stepDir deletes the install directory (or all but backups/ with --keep-backups). The marker
// files go last, so that an interrupted run is still recognised for what it is.
func (u *uninstaller) stepDir(ctx context.Context, dir *dirState) error {
	if !dir.Exists {
		u.say(markSkip, "%s is not there", dir.Given)
		return nil
	}
	if !dir.Install && !dir.Kept && !dir.Empty {
		return fmt.Errorf("refusing to delete %s: it is not recognised as an install", dir.Real)
	}
	entries, err := os.ReadDir(dir.Real)
	if err != nil {
		return err
	}
	if u.opts.keepBackups {
		if err := writeAtomic(filepath.Join(dir.Real, keptMarker),
			[]byte("BOBRES was uninstalled from this server; the backups in backups/ were kept on request (bobres uninstall --purge --keep-backups).\n"+
				"They open only with the old install's backup key. Delete this directory when you no longer need them.\n"), 0o600); err != nil {
			return err
		}
	}
	var names []string
	for _, e := range entries {
		switch n := e.Name(); {
		case n == ".env" || n == "docker-compose.yml":
		case n == keptMarker:
		case u.opts.keepBackups && n == "backups":
		default:
			names = append(names, n)
		}
	}
	// A BOBRES container's last act may have left a file only root can remove: report, do not stop.
	kept, err := removeInside(dir.Real, names)
	if err != nil {
		return err
	}
	for _, k := range kept {
		u.say(markWarn, "kept %s: it is a mount point (another file system)", k)
		u.keptNotes = append(u.keptNotes, k+": a mount point inside the install directory")
	}
	for _, marker := range []string{"docker-compose.yml", ".env"} {
		if _, err := removeInside(dir.Real, []string{marker}); err != nil {
			return err
		}
	}
	if u.opts.keepBackups {
		u.say(markOK, "deleted the install except backups/ (kept on request)")
		return nil
	}
	if len(kept) == 0 {
		if err := os.Remove(dir.Real); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if mounted := isMountPoint(dir.Real); mounted {
				u.say(markOK, "emptied %s (it is a mount point, so the directory itself stays)", dir.Real)
				return nil
			}
			return fmt.Errorf("remove %s: %w", dir.Real, err)
		}
	}
	u.say(markOK, "deleted %s", dir.Real)
	return nil
}

// isMountPoint reports whether dir is on another file system than its parent.
func isMountPoint(dir string) bool {
	a, ok1 := deviceOf(dir)
	b, ok2 := deviceOf(filepath.Dir(dir))
	return ok1 && ok2 && a != b
}

func (u *uninstaller) stepState(ctx context.Context) error {
	if fi, err := os.Lstat(u.h.p(installLogName)); err == nil && fi.Mode().IsRegular() {
		if err := os.Remove(u.h.p(installLogName)); err != nil {
			return err
		}
		u.say(markOK, "deleted %s", installLogName)
	}
	own, _ := u.stateInventory()
	for _, name := range own {
		if name == receiptFile || name == scriptMarkerDir {
			continue // the receipt and the markers go in the last step
		}
		if err := os.Remove(u.h.p(filepath.Join(u.h.stateDir, name))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		u.say(markOK, "deleted %s", filepath.Join(u.h.stateDir, name))
	}
	return nil
}

func (u *uninstaller) stepCommand(ctx context.Context, dir *dirState) error {
	inv := u.cliInventory(dir.Real)
	for _, f := range inv.Files {
		if err := os.Remove(u.h.p(f.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete %s: %w", f.Path, err)
		}
		u.say(markOK, "deleted %s", f.Path)
	}
	if inv.Running != "" && !inv.RunningOwned {
		u.say(markSkip, "kept %s: the copy you are running is not an installed one", inv.Running)
		u.keptNotes = append(u.keptNotes, inv.Running+": the bobres you ran is not an installed copy")
	}
	state := u.h.p(u.h.stateDir)
	if err := os.RemoveAll(filepath.Join(state, scriptMarkerDir)); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(state, receiptFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	u.say(markOK, "deleted the install receipt")
	if removeDirIfEmpty(state) {
		u.say(markOK, "deleted %s", u.h.stateDir)
	}
	return nil
}

// ---- the plain uninstall (containers only) and the entry point ---------------------------------------

const uninstallUsage = "usage: bobres uninstall [--dir D] [--yes]            stop and remove the containers, keep the data\n" +
	"       bobres uninstall --purge [--dir D] [--dry-run] [--backup-to FILE | --no-backup] [--keep-backups]\n" +
	"                        [--yes --confirm TOKEN] [--remove-docker] [--remove-packages] [--disable-firewall]\n" +
	"       bobres uninstall --verify [--dir D]              scan the server for what is left of BOBRES"
