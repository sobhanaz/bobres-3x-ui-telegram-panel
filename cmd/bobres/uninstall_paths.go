package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// This file holds the rules for what `bobres uninstall --purge` may delete: a directory is
// deleted only when it is provably BOBRES's own (its real path is no system directory, the
// marker files of an install are in it, it is no symbolic link), and a tree is deleted without
// following a link and without crossing into another file system.

// refusal is an error that means "this input is not acceptable": the command exits 2 and
// nothing was changed.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) error { return &refusal{fmt.Sprintf(format, args...)} }

// protectedDirs are the directories no install may be, whatever is in them: system directories,
// the places a home directory or a disk is mounted, and the ones BOBRES itself keeps state in.
var protectedDirs = []string{
	"/", "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib32", "/lib64", "/libx32", "/media", "/mnt", "/opt",
	"/proc", "/root", "/run", "/sbin", "/snap", "/srv", "/sys", "/tmp", "/usr", "/usr/bin", "/usr/lib",
	"/usr/lib64", "/usr/libexec", "/usr/local", "/usr/local/bin", "/usr/local/lib", "/usr/local/sbin",
	"/usr/local/share", "/usr/sbin", "/usr/share", "/var", "/var/backups", "/var/cache", "/var/lib",
	"/var/lib/docker", "/var/lib/containerd", "/var/lib/bobres", "/var/log", "/var/mail", "/var/opt",
	"/var/run", "/var/spool", "/var/tmp", "/Users", "/Library", "/System", "/Applications", "/private",
}

// dangerousDir reports why p (already absolute and clean) may not be an install directory, "" when
// it may. extra are more protected paths (the invoking user's home directory, the state
// directory).
func dangerousDir(p string, extra ...string) string {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return "it is not an absolute path"
	}
	for _, d := range append(append([]string(nil), protectedDirs...), extra...) {
		if d != "" && p == filepath.Clean(d) {
			return "it is a system directory"
		}
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case len(parts) == 1:
		return "it is a top-level directory, which is too broad to be an install"
	case len(parts) == 2 && (parts[0] == "home" || parts[0] == "Users"):
		return "it is a user's home directory"
	}
	return ""
}

// installMarkers are what makes a directory BOBRES's: the .env of an install (with BOBRES's
// keys) and a docker-compose.yml that names BOBRES's images, both regular files and no links.
const (
	keptMarker     = ".bobres-uninstall-kept"
	composeMarkerS = "ghcr.io/sobhanaz/bobres-"
)

// dirState is what resolveInstallDir finds.
type dirState struct {
	Given, Real string
	Exists      bool
	// Empty: nothing in it (an uninstall that stopped at the last step).
	Empty bool
	// Install: it holds an install's marker files. Kept: it holds only what an earlier
	// `--keep-backups` uninstall left (its marker file).
	Install, Kept bool
	Env           map[string]string
}

// resolveInstallDir checks dir against the rules above. extraProtected are more directories it may
// not be or contain. It returns a refusal for anything it will not delete.
func resolveInstallDir(dir string, extraProtected ...string) (*dirState, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, refuse("no install directory given (--dir)")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, refuse("cannot resolve %q: %v", dir, err)
	}
	st := &dirState{Given: abs, Real: abs}
	if why := dangerousDir(abs, extraProtected...); why != "" {
		return nil, refuse("refusing %s: %s", abs, why)
	}
	lst, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return st, nil
	case err != nil:
		return nil, refuse("cannot look at %s: %v", abs, err)
	case lst.Mode()&os.ModeSymlink != 0:
		return nil, refuse("refusing %s: it is a symbolic link (point --dir at the directory itself)", abs)
	case !lst.IsDir():
		return nil, refuse("refusing %s: it is not a directory", abs)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, refuse("cannot resolve %s: %v", abs, err)
	}
	st.Real, st.Exists = real, true
	if why := dangerousDir(real, extraProtected...); why != "" {
		return nil, refuse("refusing %s (really %s): %s", abs, real, why)
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return nil, refuse("cannot read %s: %v", real, err)
	}
	if len(entries) == 0 {
		st.Empty = true
		return st, nil
	}
	env, envOK := readMarkerFile(filepath.Join(real, ".env"))
	compose, composeOK := readMarkerFile(filepath.Join(real, "docker-compose.yml"))
	st.Install = envOK && composeOK && hasBobresKeys(env) && strings.Contains(compose, composeMarkerS)
	_, st.Kept = readMarkerFile(filepath.Join(real, keptMarker))
	if st.Install {
		st.Env = parseEnv([]byte(env))
	}
	if !st.Install && !st.Kept {
		return nil, refuse("refusing %s: it is not a BOBRES install (it has no .env of BOBRES together with its docker-compose.yml). "+
			"Nothing was changed; point --dir at the install directory", real)
	}
	return st, nil
}

// readMarkerFile reads a marker file that must be a regular file (never a link), at most 1 MiB.
func readMarkerFile(path string) (string, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return "", false
	}
	b, err := os.ReadFile(path) //nolint:gosec // a marker file of the directory being checked
	if err != nil {
		return "", false
	}
	return string(b), true
}

// hasBobresKeys reports whether an .env holds keys of BOBRES.
func hasBobresKeys(envText string) bool {
	for _, k := range []string{"BOBRES_DOMAIN", "BOBRES_VERSION", "BOBRES_TOKEN_CORE", "BOBRES_BACKUP_KEY"} {
		if _, ok := parseEnv([]byte(envText))[k]; ok {
			return true
		}
	}
	return false
}

// underPath reports whether child is path or lies below it.
func underPath(path, child string) bool {
	path, child = filepath.Clean(path), filepath.Clean(child)
	if path == child {
		return true
	}
	if path == "/" {
		return true
	}
	return strings.HasPrefix(child, path+string(filepath.Separator))
}

// sizeInfo is how much a tree holds.
type sizeInfo struct {
	Bytes int64
	Files int
}

// treeSize adds up the regular files below root without following a link and without leaving root's
// file system. A tree that cannot be read counts as far as it can.
func treeSize(root string) sizeInfo {
	var s sizeInfo
	rootDev, _ := deviceOf(root)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // a part that cannot be read is skipped
		}
		if d.IsDir() {
			if dev, ok := deviceOf(path); ok && path != root && dev != rootDev {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				s.Bytes += fi.Size()
				s.Files++
			}
		}
		return nil
	})
	return s
}

// deviceOf is the device number of the file system path is on (without following a link).
func deviceOf(path string) (uint64, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert,gosec // the field's width differs between systems
}

// mountPointsBelow lists the directories below root that are on another file system (a disk or
// a share mounted inside the tree), which a removal must not enter.
func mountPointsBelow(root string) []string {
	var out []string
	rootDev, ok := deviceOf(root)
	if !ok {
		return nil
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == root {
			return nil //nolint:nilerr // a part that cannot be read is skipped
		}
		if dev, ok := deviceOf(path); ok && dev != rootDev {
			out = append(out, path)
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

// removeInside deletes the entries named (relative names, no slash) of dir, inside a root that
// confines the operation to dir: a link is removed, never followed, and a swapped directory
// cannot lead out of it. Entries that are mount points below are kept and returned.
func removeInside(dir string, names []string) (kept []string, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close() //nolint:errcheck // nothing was written through it
	mounts := mountPointsBelow(dir)
	for _, name := range names {
		if name == "" || name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) {
			return kept, fmt.Errorf("refusing to remove %q inside %s", name, dir)
		}
		full := filepath.Join(dir, name)
		var inner []string
		for _, m := range mounts {
			if underPath(full, m) {
				inner = append(inner, m)
			}
		}
		if len(inner) == 0 {
			if err := root.RemoveAll(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return kept, fmt.Errorf("remove %s: %w", full, err)
			}
			continue
		}
		// A mount point inside: delete around it, one entry at a time.
		k, err := removeAround(root, dir, name, inner)
		kept = append(kept, k...)
		if err != nil {
			return kept, err
		}
	}
	return kept, nil
}

// removeAround removes the entry rel of root, except the mount points (absolute paths) below it.
func removeAround(root *os.Root, base, rel string, mounts []string) (kept []string, err error) {
	full := filepath.Join(base, rel)
	for _, m := range mounts {
		if m == full {
			return []string{m}, nil
		}
	}
	fi, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, root.Remove(rel)
	}
	d, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	names, err := d.Readdirnames(-1)
	_ = d.Close()
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		k, err := removeAround(root, base, filepath.Join(rel, n), mounts)
		kept = append(kept, k...)
		if err != nil {
			return kept, err
		}
	}
	if len(kept) > 0 {
		return kept, nil
	}
	return nil, root.Remove(rel)
}

// removeContents empties dir (a directory that is a mount point itself stays, as an empty one),
// keeping nothing but mount points below it. It returns those.
func removeContents(dir string) (kept []string, err error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return removeInside(dir, names)
}

// removeDirIfEmpty removes dir when it holds nothing (a mount point or a busy directory stays).
func removeDirIfEmpty(dir string) bool {
	return os.Remove(dir) == nil
}

var (
	ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")
)
