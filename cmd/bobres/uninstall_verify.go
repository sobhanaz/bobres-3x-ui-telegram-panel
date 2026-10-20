package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The leftover scan: after a purge, and on its own as `bobres uninstall --verify`, the server is
// searched for anything of BOBRES's that is still there. What is found is either "left" (it
// should be gone, or the operator asked for it to go) or "kept" (it stays on purpose, with the
// reason). The exit code is 0 when nothing is left.

// scanItem is one finding.
type scanItem struct{ What, Why string }

// scanDirState resolves the install directory for the scan: a directory that is no install is
// not a finding, and a refused path is reported by the caller.
func (u *uninstaller) scanDirState() *dirState {
	st, err := resolveInstallDir(u.opts.dir, u.protectedPaths()...)
	if err != nil {
		abs, _ := filepath.Abs(u.opts.dir)
		return &dirState{Given: abs, Real: abs}
	}
	return st
}

// protectedPaths are directories the install directory may not be or contain.
func (u *uninstaller) protectedPaths() []string {
	paths := []string{u.h.p(u.h.stateDir), u.h.p(filepath.Dir(u.h.logPath)), u.h.getenv("HOME")}
	if su := u.h.getenv("SUDO_USER"); su != "" && !strings.ContainsAny(su, "/\x00") {
		paths = append(paths, "/home/"+su)
	}
	return paths
}

// scan searches the server for what is left of BOBRES.
func (u *uninstaller) scan(ctx context.Context, _ *dirState) (left, kept []scanItem) {
	dir := u.scanDirState()
	w := u.survey(ctx, dir, false)
	add := func(l *[]scanItem, what, why string) { *l = append(*l, scanItem{what, why}) }

	// The install directory.
	if dir.Exists {
		entries, _ := os.ReadDir(dir.Real)
		var other []string
		backups := false
		for _, e := range entries {
			switch e.Name() {
			case keptMarker:
			case "backups":
				backups = true
			default:
				other = append(other, e.Name())
			}
		}
		switch {
		case len(other) > 0 || (!u.opts.keepBackups && backups):
			add(&left, dir.Real, fmt.Sprintf("the install directory is still there (%d entries)", len(entries)))
		case backups:
			add(&kept, filepath.Join(dir.Real, "backups"), "backups kept on request (--keep-backups); they open only with the old backup key")
		case len(entries) > 0 && dir.Kept:
			add(&kept, dir.Real, "left by an earlier --keep-backups")
		case len(entries) == 0:
			add(&left, dir.Real, "an empty install directory (rmdir it)")
		}
	}
	// Units.
	for _, e := range w.units {
		if e.Foreign {
			add(&kept, e.Name, "known to systemd but not placed by BOBRES")
		} else {
			add(&left, e.Name, "a unit of BOBRES's")
		}
	}
	// Docker.
	if w.docker.Reachable {
		for _, c := range w.docker.Containers {
			add(&left, "container "+c.Name, "a container of the bobres project")
		}
		for _, v := range w.docker.Volumes {
			add(&left, "volume "+v.Name, "a volume of the bobres project (it holds data)")
		}
		for _, n := range w.docker.Networks {
			add(&left, "network "+n.Name, "a network of the bobres project")
		}
		for _, im := range w.docker.Images {
			if why := u.keptImageReason(im.Ref); why != "" {
				add(&kept, "image "+im.Ref, why)
			} else {
				add(&left, "image "+im.Ref, im.Why+" (docker rmi "+im.Ref+")")
			}
		}
	} else if w.docker.Present {
		add(&kept, "Docker", "it does not answer, so its objects were not checked")
	}
	for _, s := range w.docker.Skipped {
		add(&kept, s, "left alone on purpose")
	}
	// The firewall.
	if w.fw.Installed && !w.fw.Known && u.h.euid() != 0 {
		add(&kept, "ufw", "its rules need root to be read, so they were not checked")
	}
	for _, it := range w.fw.Items {
		if it.Delete {
			add(&left, fmt.Sprintf("ufw rule %q", it.spec()), it.Comment)
		} else {
			add(&kept, fmt.Sprintf("ufw rule %q (%s)", it.spec(), it.Comment), it.Why)
		}
	}
	if w.fw.Active && u.rcpt.Firewall != nil && u.rcpt.Firewall.EnabledByBobres {
		if u.opts.disableFirewall {
			add(&left, "ufw", "it is still on (--disable-firewall was asked)")
		} else {
			add(&kept, "ufw is on", "BOBRES turned it on; it stays on so the server is not left open (--disable-firewall turns it off)")
		}
	}
	// Files and the swap file.
	for _, f := range w.files {
		switch f.Action {
		case fileDelete, fileRestore, fileStrip:
			add(&left, f.Path, "written by bobres harden")
		case fileKeep:
			add(&kept, f.Path, f.Why)
		}
	}
	if w.swap.Ours {
		add(&left, swapFile, "the swap file BOBRES made")
	} else if w.swap.Present && u.rcpt.Swap != nil {
		add(&kept, swapFile, w.swap.NotOursWhy)
	}
	if w.swap.FstabLine {
		add(&left, "/etc/fstab", "the line of BOBRES's swap file")
	}
	// State, logs and the command.
	if w.installLog {
		add(&left, installLogName, "the install log")
	}
	for _, n := range w.stateOwn {
		add(&left, filepath.Join(u.h.stateDir, n), "BOBRES's state")
	}
	for _, n := range w.stateOther {
		add(&kept, filepath.Join(u.h.stateDir, n), "not a file of BOBRES's")
	}
	for _, f := range w.cli.Files {
		add(&left, f.Path, "the bobres command")
	}
	// Software BOBRES installed.
	for _, g := range w.groups {
		if len(g.Installed) == 0 {
			continue
		}
		flag := "--remove-packages"
		if g.Name == "docker" {
			flag = "--remove-docker"
		}
		if u.wantsRemoval(g.Name) {
			// A package kept on purpose (an operator's own since, or one another program needs) is
			// reported with its reason below, not as a leftover.
			var still []string
			for _, p := range g.Installed {
				if !u.keptPackage(p) {
					still = append(still, p)
				}
			}
			if len(still) > 0 {
				add(&left, g.Name+": "+joinFirst(still, 6), "still installed though "+flag+" was asked (see above)")
			}
		} else {
			add(&kept, g.Name+": "+joinFirst(g.Installed, 6), "installed by BOBRES; "+flag+" removes it")
		}
	}
	for _, n := range u.keptNotes {
		if strings.HasPrefix(n, "image ") {
			continue // reported with the image
		}
		what, why, _ := strings.Cut(n, ": ")
		add(&kept, what, why)
	}
	add(&kept, u.h.logPath, "the record of the uninstall")
	return dedupeScan(left), dedupeScan(kept)
}

// keptPackage reports whether this run decided to keep the package on purpose.
func (u *uninstaller) keptPackage(name string) bool {
	for _, n := range u.keptNotes {
		if strings.HasPrefix(n, "package "+name+": ") {
			return true
		}
	}
	return false
}

// keptImageReason is why an image the uninstall tried to remove stays, from its own notes.
func (u *uninstaller) keptImageReason(ref string) string {
	for _, n := range u.keptNotes {
		if rest, ok := strings.CutPrefix(n, "image "+ref+": "); ok {
			return "another container uses it (" + rest + ")"
		}
	}
	return ""
}

func dedupeScan(items []scanItem) []scanItem {
	seen := map[string]bool{}
	var out []scanItem
	for _, it := range items {
		if key := it.What + "|" + it.Why; !seen[key] {
			seen[key] = true
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].What < out[j].What })
	return out
}

// reportScan prints the scan.
func (u *uninstaller) reportScan(left, kept []scanItem) {
	u.printf("\n")
	u.heading("Leftover scan")
	if len(left) == 0 {
		u.say(markOK, "Nothing of BOBRES is left on this server.")
	} else {
		u.say(markFail, "%d thing(s) of BOBRES are still here:", len(left))
		for _, it := range left {
			u.printf("        - %s: %s\n", it.What, it.Why)
		}
	}
	if len(kept) > 0 {
		u.printf("  Kept on purpose:\n")
		for _, it := range kept {
			u.printf("        - %s: %s\n", it.What, it.Why)
		}
	}
	u.printf("  Not touched, ever: SSH settings, DNS, other users' data, other Docker workloads.\n")
}

// verifyCmd is `bobres uninstall --verify`.
func (u *uninstaller) verifyCmd(ctx context.Context, opts purgeOptions) int {
	u.opts = opts
	u.w = u.out
	rc, err := u.store.load()
	if err != nil {
		fmt.Fprintf(u.errOut, "warning: %v\n", err)
	}
	u.rcpt = rc
	importScriptMarkers(u.h.p(u.h.stateDir), u.rcpt)
	if _, err := resolveInstallDir(opts.dir, u.protectedPaths()...); err != nil {
		var r *refusal
		if errors.As(err, &r) && strings.Contains(r.msg, "not a BOBRES install") {
			u.printf("Note: %s exists but is not a BOBRES install, so it is not scanned.\n", opts.dir)
		} else {
			return u.refused(err)
		}
	}
	left, kept := u.scan(ctx, nil)
	u.reportScan(left, kept)
	if len(left) > 0 {
		return 1
	}
	return 0
}
