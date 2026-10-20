package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// prepareUpdateDir makes <dir>/run/update, the one directory of the host that core (the dashboard)
// can write: it drops request.json there and reads status.json (helper_request.go). It is mounted
// into core's container at /run/bobres-update (deploy/docker-compose.yml) and must exist before the
// stack starts, or Docker makes it as root and core could not write to it.
//
// run/ is root's (mode 0755), so core cannot swap update/ for something else; update/ belongs to
// core's user (65532), mode 0700: nobody else on the host can put a file in it, and the helper,
// which is root, checks every file it reads from it anyway. Both are looked at without following
// links, and put right on every install. A run that is not root cannot give update/ away, so the
// directory gets mode 0777 and a note says so (a development or CI install; the helper, which is
// root's, would refuse such a directory).
func prepareUpdateDir(dir string, notes io.Writer) error {
	run := filepath.Join(dir, runDirName)
	d, err := openDir(run, 0o755)
	if err != nil {
		return err
	}
	if asRoot.euid() == 0 {
		if err = asRoot.chown(d, 0, 0); err != nil {
			_ = d.Close()
			return fmt.Errorf("chown %s: %w", run, err)
		}
	}
	err = d.Chmod(0o755)
	_ = d.Close()
	if err != nil {
		return fmt.Errorf("chmod %s: %w", run, err)
	}
	path := updateDirPath(dir)
	u, err := openDir(path, 0o700)
	if err != nil {
		return err
	}
	defer u.Close() //nolint:errcheck // read-only descriptor
	if asRoot.euid() == 0 {
		if err := asRoot.chown(u, coreUID, coreUID); err != nil {
			return fmt.Errorf("chown %s: %w", path, err)
		}
		err = u.Chmod(0o700)
	} else {
		fmt.Fprintf(notes, "Note: %s is writable by everyone (mode 0777) because this install is not run as root: core (uid %d) "+
			"could not write to it otherwise. Fine for development and CI; install a server with sudo.\n", path, coreUID)
		err = u.Chmod(0o777)
	}
	if err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// ensureUpdater installs the dashboard's updater (helper_units.go) and says so in status.json, once
// the store is up. `bobres update` runs the new bobres's install, so every update brings the
// units of the release it installs. A problem is a warning, never a failed install: the store does
// not depend on the updater, and the next install or update tries again.
func (ins *installer) ensureUpdater(ctx context.Context, dir string, env map[string]string) {
	if ins.units == nil {
		return
	}
	exe := ""
	if ins.exe != nil {
		var err error
		if exe, err = ins.exe(); err != nil {
			fmt.Fprintf(ins.errOut, "warning: the dashboard's updater was not set up: cannot find the bobres binary: %v\n", err)
			return
		}
	}
	off := dashboardUpdatesOff(env)
	res := ins.units.ensure(ctx, unitOptions{dir: dir, exe: exe, state: ins.stateDir, off: off})
	if res.kind == markOK {
		// The dashboard learns from status.json whether the helper is there.
		if _, err := ensureStatus(dir, helperVersion(), !off); err != nil {
			res = unitWarn("the units are in place, but status.json was not written: %v", err)
		}
	}
	switch {
	case res.kind == markWarn:
		fmt.Fprintf(ins.errOut, "warning: the dashboard's updater: %s\n", res.text)
	case res.kind == markSkip:
		if !ins.quiet {
			ins.prog.Note(markSkip, dashboardUpdatesLabel, res.text)
		}
	case res.text != "" && res.changed:
		ins.prog.Note(markOK, dashboardUpdatesLabel, res.text)
	}
}

// applyDashboardUpdates puts BOBRES_UPDATE_FROM_DASHBOARD into effect once `bobres config set` has
// saved it: on installs the updater (and the directory it needs), off removes it. env is the
// install's new .env.
func applyDashboardUpdates(o *ops, dir string, env map[string]string) int {
	off := dashboardUpdatesOff(env)
	if o.units == nil {
		fmt.Fprintln(o.out, "Saved. The updater's units are set up by the next sudo bobres install or sudo bobres update.")
		return 0
	}
	exeOf := o.exe
	if exeOf == nil {
		exeOf = selfExe
	}
	exe, err := exeOf()
	if err != nil {
		fmt.Fprintf(o.errOut, "The setting is saved, but the updater was not set up: cannot find the bobres binary: %v\n", err)
		return 1
	}
	if !off {
		if err := prepareUpdateDir(dir, o.out); err != nil {
			fmt.Fprintf(o.errOut, "The setting is saved, but the updater was not set up: %v\n", err)
			return 1
		}
	}
	res := o.units.ensure(context.Background(), unitOptions{dir: dir, exe: exe, state: defaultStateDir(), off: off})
	if res.kind == markOK {
		if _, err := ensureStatus(dir, helperVersion(), !off); err != nil {
			res = unitWarn("the units are in place, but status.json was not written: %v", err)
		}
	}
	switch res.kind {
	case markWarn:
		fmt.Fprintf(o.errOut, "The setting is saved, but the updater was not set up: %s\n", res.text)
		return 1
	case markSkip:
		fmt.Fprintf(o.out, "The setting is saved; the updater is not set up here: %s.\n", res.text)
		return 0
	}
	if off {
		fmt.Fprintln(o.out, "The dashboard can no longer update this server: only sudo bobres update does. Turn it on again with: sudo bobres config set "+dashboardUpdatesKey+" on")
		return 0
	}
	fmt.Fprintln(o.out, "The dashboard can update this server (System > Updates): "+updaterPathUnit+" is on.")
	if b, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml")); err == nil && !strings.Contains(string(b), "/run/bobres-update") {
		fmt.Fprintln(o.out, "This install's docker-compose.yml is from before the updater: run sudo bobres update (or sudo bobres install) once, so that the dashboard can reach it.")
	}
	return 0
}
