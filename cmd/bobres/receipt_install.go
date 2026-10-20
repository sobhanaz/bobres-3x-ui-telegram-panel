package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// isInstalledCLIPath reports whether p is where the bobres command is installed: named bobres, in
// $BOBRES_BIN_DIR or one of the usual bin directories. A copy anywhere else (a build in /tmp, one in
// a home directory) is not recorded, so the uninstall never takes it for the installed one.
func isInstalledCLIPath(p string) bool {
	if p == "" || !filepath.IsAbs(p) || filepath.Base(p) != "bobres" {
		return false
	}
	dir := filepath.Dir(p)
	if d := strings.TrimSpace(os.Getenv("BOBRES_BIN_DIR")); d != "" && filepath.Clean(d) == dir {
		return true
	}
	for _, s := range standardBinDirs {
		if dir == s {
			return true
		}
	}
	return false
}

// recordInstall notes this install in the receipt: where it is, its domain, the command that ran it
// and what install.sh recorded of its own steps (Docker, openssl). A failure goes to the install log
// only: the install does not depend on the receipt.
func (ins *installer) recordInstall(dir string, env map[string]string) {
	if ins.rcpt == nil {
		return
	}
	real, err := filepath.Abs(dir)
	if err == nil {
		if r, err := filepath.EvalSymlinks(real); err == nil {
			real = r
		}
	}
	exe := ""
	if p, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(p); err == nil && isInstalledCLIPath(r) {
			exe = r
		}
	}
	if err := ins.rcpt.update(func(r *receipt) {
		r.CLIVersion = version.Version
		r.InstallDir = real
		r.Domain = env["BOBRES_DOMAIN"]
		if exe != "" {
			r.CLIPath = exe
		}
		importScriptMarkers(ins.rcpt.dir, r)
	}); err != nil {
		fmt.Fprintf(ins.logOut(), "note: the install receipt was not written: %v\n", err)
	}
}

// recordImages notes the images this install is about to pull: every reference its compose file
// names, and which of them were on the server already, running nothing of BOBRES's (those are not
// BOBRES's to remove). Best effort, and only where a receipt is kept.
func (ins *installer) recordImages(ctx context.Context, dir string) {
	if ins.rcpt == nil {
		return
	}
	out, err := ins.run(ctx, dir, composeArgs("config", "--images")...)
	if err != nil {
		return
	}
	var refs []string
	for _, line := range strings.Split(string(out), "\n") {
		// The reference as compose has it, digest included: Docker 29 lists an image pulled by digest
		// with no tag, and only the digest finds it again.
		if ref := strings.TrimSpace(line); ref != "" && !strings.ContainsAny(ref, " \t") && strings.Contains(ref, ":") {
			refs = append(refs, ref)
		}
	}
	used := map[string]bool{}
	if ps, err := ins.run(ctx, dir, "ps", "-a", "--filter", "label="+projectLabel, "--format", "{{.Image}}"); err == nil {
		for _, l := range strings.Split(string(ps), "\n") {
			if ref := normRef(l); ref != "" {
				used[ref] = true
			}
		}
	}
	have := func(ref string) bool {
		_, err := ins.run(ctx, dir, "image", "inspect", "--format", "{{.Id}}", ref)
		return err == nil
	}
	if err := ins.rcpt.update(func(r *receipt) {
		r.noteImages(refs, have, func(ref string) bool { return used[normRef(ref)] })
	}); err != nil {
		fmt.Fprintf(ins.logOut(), "note: the install receipt was not updated with the images: %v\n", err)
	}
}
