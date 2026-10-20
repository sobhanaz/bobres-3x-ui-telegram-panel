package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// dockerAptDirs are the directories Docker's installer puts its apt source and key in.
var dockerAptDirs = []string{"/etc/apt/sources.list.d", "/etc/apt/keyrings", "/etc/apt/trusted.gpg.d", "/usr/share/keyrings"}

// dockerAptFile reports whether p is a file Docker's installer may have added: in one of the apt
// directories above, with "docker" in its name.
func dockerAptFile(p string) bool {
	p = filepath.Clean(p)
	for _, d := range dockerAptDirs {
		if filepath.Dir(p) == d && strings.Contains(strings.ToLower(filepath.Base(p)), "docker") {
			return true
		}
	}
	return false
}

// removeDockerEngine removes Docker, the packages its installer added, the apt source and key it
// wrote, its data and the group it made. It runs only when install.sh recorded that it installed
// Docker, nothing but BOBRES's own objects is left in Docker, and apt agrees that no other package
// needs the ones to go.
func (u *uninstaller) removeDockerEngine(ctx context.Context) error {
	g, ok := u.rcpt.group("docker")
	if !ok {
		u.say(markWarn, "Docker is kept: no record says BOBRES installed it (install.sh records that when it installs Docker)")
		u.keptNotes = append(u.keptNotes, "Docker: no record says BOBRES installed it")
		return errors.New("not removing Docker: BOBRES cannot prove it installed it")
	}
	brief := u.packageBrief(ctx, g)
	if len(brief.Installed) == 0 {
		u.say(markSkip, "Docker's packages are not installed")
	} else {
		if u.h.has("docker") {
			if _, err := u.docker(ctx, "info", "--format", "{{.ServerVersion}}"); err != nil && u.h.has("systemctl") {
				_ = u.h.do(ctx, "systemctl", "start", "docker")
			}
			if why := u.dockerInUse(ctx); len(why) > 0 {
				return fmt.Errorf("keeping Docker: it still holds %s that are not BOBRES's", strings.Join(why, ", "))
			}
		}
		st := u.packageState(ctx, g, nil)
		if st.Blocked != "" {
			return fmt.Errorf("keeping Docker: %s", st.Blocked)
		}
		for p, why := range st.Keep {
			u.say(markWarn, "kept the package %s: %s", p, why)
			u.keptNotes = append(u.keptNotes, "package "+p+": "+why)
		}
		if u.h.has("systemctl") {
			for _, unit := range []string{"docker.socket", "docker.service", "containerd.service"} {
				_ = u.h.do(ctx, "systemctl", "stop", unit)
			}
		}
		if err := u.purgePackages(ctx, st); err != nil {
			return fmt.Errorf("purging Docker: %w", err)
		}
		u.say(markOK, "purged Docker: %s", strings.Join(st.Remove, ", "))
	}

	// What the installer added next to the packages.
	for _, f := range g.Files {
		if !dockerAptFile(f) {
			u.say(markWarn, "kept %s: it is not an apt source or key of Docker's", f)
			continue
		}
		if err := os.Remove(u.h.p(f)); err == nil {
			u.say(markOK, "deleted %s", f)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if matches, _ := filepath.Glob(u.h.p("/var/lib/apt/lists/download.docker.com_*")); len(matches) > 0 {
		for _, m := range matches {
			_ = os.Remove(m)
		}
		u.say(markOK, "deleted the package lists of Docker's repository")
	}
	// Its data. The directories can be mount points (a disk of their own): then they stay, empty.
	for _, d := range []string{"/var/lib/docker", "/var/lib/containerd"} {
		if _, err := os.Lstat(u.h.p(d)); err != nil {
			continue
		}
		kept, err := removeContents(u.h.p(d))
		if err != nil {
			return fmt.Errorf("empty %s: %w", d, err)
		}
		if len(kept) == 0 && !isMountPoint(u.h.p(d)) {
			_ = os.Remove(u.h.p(d))
		}
		u.say(markOK, "emptied %s", d)
	}
	for _, d := range []string{"/opt/containerd/bin", "/opt/containerd/lib", "/opt/containerd"} {
		removeDirIfEmpty(u.h.p(d))
	}
	for _, d := range []string{"/etc/docker", "/etc/containerd"} {
		_ = os.Remove(u.h.p(d + "/key.json"))
		if removeDirIfEmpty(u.h.p(d)) {
			u.say(markOK, "deleted %s", d)
		} else if _, err := os.Lstat(u.h.p(d)); err == nil {
			u.say(markWarn, "kept %s: it holds files of yours", d)
			u.keptNotes = append(u.keptNotes, d+": it holds files that are not Docker's own")
		}
	}
	for _, r := range []string{"/run/docker.sock", "/run/docker", "/run/containerd"} {
		_ = os.RemoveAll(u.h.p(r))
	}
	if g.SystemGroup != "" && u.h.has("getent") && u.h.has("groupdel") {
		out, _ := u.h.tryRun(ctx, "getent", "group", g.SystemGroup)
		if members := groupMembers(out); out != "" && members == "" {
			if err := u.h.do(ctx, "groupdel", g.SystemGroup); err != nil {
				u.say(markWarn, "the group %s was not deleted: %v", g.SystemGroup, err)
			} else {
				u.say(markOK, "deleted the system group %s", g.SystemGroup)
			}
		} else if out != "" {
			u.say(markWarn, "kept the group %s: it has members (%s)", g.SystemGroup, members)
			u.keptNotes = append(u.keptNotes, "group "+g.SystemGroup+": it has members")
		}
	}
	if u.h.has("systemctl") {
		_ = u.h.do(ctx, "systemctl", "daemon-reload")
	}
	return nil
}

// groupMembers is the member list of a `getent group` line (name:x:gid:a,b).
func groupMembers(line string) string {
	parts := strings.Split(strings.TrimSpace(line), ":")
	if len(parts) < 4 {
		return ""
	}
	return strings.TrimSpace(parts[3])
}
