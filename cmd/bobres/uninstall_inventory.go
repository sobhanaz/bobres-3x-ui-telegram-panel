package main

import (
	"path"
	"strings"
)

// The inventory of everything installing BOBRES changes on a server, in one place. The uninstall
// is written against it (every entry says how `bobres uninstall --purge` treats the artifact),
// docs/06-installer.md prints it, and the tests hold the code to it: TestHostArtifactsAreKnown
// fails when an installer step, a unit file or a path literal appears that is not listed here,
// and TestInventoryIsDocumented fails when this list and the document differ.
//
// To add something to the server (a unit, a file, a package, a rule): add it here, teach
// `bobres uninstall` to remove it, and describe it in docs/06-installer.md.

// artifactClass is how an artifact is undone.
type artifactClass int

const (
	// classOwn is created by BOBRES alone: safe to delete once its marker proves it is ours.
	classOwn artifactClass = iota
	// classShared is a change inside a file or a service BOBRES does not own: reverted
	// precisely (a recorded original put back, a marked line taken out), never by deleting the
	// whole.
	classShared
	// classPackage is software BOBRES installed: removed only with the explicit flag, only when the
	// receipt says BOBRES installed it, and only when nothing else needs it.
	classPackage
	// classRuntime is created while BOBRES runs and goes with its owner or the next reboot.
	classRuntime
	// classRead is only read.
	classRead
)

func (c artifactClass) String() string {
	return [...]string{"created by BOBRES only", "changed in a shared file", "installed software", "runtime", "read only"}[c]
}

// hostArtifact is one thing BOBRES puts on, or looks at on, the server.
type hostArtifact struct {
	ID    string
	Class artifactClass
	// Paths are the paths or globs (path.Match; a trailing "/**" means everything below) it
	// occupies, as a literal in the sources would write them. "<install>" is the install directory.
	Paths []string
	// Made names the step that makes it; Undo says what the uninstall does with it.
	Made string
	Undo string
	// Marker says what proves it is BOBRES's.
	Marker string
}

// hostArtifacts is the complete list. The order is the order of docs/06-installer.md.
var hostArtifacts = []hostArtifact{
	// ---- created by BOBRES only
	{ID: "install-dir", Class: classOwn, Paths: []string{"/opt/bobres", "/opt/bobres/**"},
		Made:   "bobres install (the .env with the secrets, docker-compose.yml, caddy/, tls/, backups/, .restore-point/, support bundles)",
		Undo:   "deleted, backups included (--keep-backups leaves backups/)",
		Marker: "a .env with BOBRES's keys together with a docker-compose.yml that names BOBRES's images, both regular files"},
	{ID: "cli", Class: classOwn, Paths: []string{"/usr/local/bin", "/usr/local/bin/bobres", "/usr/local/bin/bobres.prev", "/usr/local/bin/.bobres*"},
		Made:   "install.sh, bobres update (bobres.prev is the previous one)",
		Undo:   "deleted last; the copy that is running is deleted only when it lies in a BOBRES location",
		Marker: "the file name, in the bin directory the receipt or install.sh recorded"},
	{ID: "state-dir", Class: classOwn, Paths: []string{"/var/lib/bobres", "/var/lib/bobres/**"},
		Made:   "install.sh, bobres update, bobres install, bobres harden (keys.seq, release.version, checksums.txt, install-receipt.json, installed/)",
		Undo:   "deleted after everything else, the receipt with it",
		Marker: "the known file names"},
	{ID: "install-log", Class: classOwn, Paths: []string{"/var/log/bobres-install.log"},
		Made: "install.sh, bobres install", Undo: "deleted", Marker: "the file name"},
	{ID: "uninstall-log", Class: classOwn, Paths: []string{"/var/log/bobres-uninstall.log"},
		Made: "bobres uninstall", Undo: "kept: it is the record of the uninstall", Marker: "the file name"},
	{ID: "units", Class: classOwn, Paths: []string{"/etc/systemd/system", "/run/systemd/system", "/etc/systemd/system/bobres*", "/etc/systemd/system/*.wants/bobres*",
		"/etc/systemd/system/*.requires/bobres*", "/etc/systemd/user/bobres*", "/run/systemd/system/bobres*"},
		Made:   "the units and timers BOBRES ships (for example bobres-updater.path and bobres-monitor.timer)",
		Undo:   "found by name (bobres-*, bobres.* and bobres@*) and not from a list: stopped, disabled, deleted, drop-ins and enable links included",
		Marker: "the unit name, in a directory only an administrator writes"},
	{ID: "docker-objects", Class: classOwn, Paths: []string{"docker:containers", "docker:volumes", "docker:networks", "docker:images"},
		Made:   "docker compose, project \"bobres\": containers, volumes (pg_data, redis_data, caddy_data, caddy_config, caddy_admin, tls_request, tls_result), networks (public, edge, backend, egress, offline) and the images it pulled",
		Undo:   "removed by the project label and the bobres_ name, the images by their references (never an image another container uses)",
		Marker: "the label com.docker.compose.project=bobres and the name prefix bobres_"},
	{ID: "fail2ban-jail", Class: classOwn, Paths: []string{"/etc/fail2ban/jail.d/bobres-sshd.local"},
		Made: "bobres harden", Undo: "deleted, then fail2ban reloads", Marker: "the header \"Written by bobres harden\""},
	{ID: "security-only", Class: classOwn, Paths: []string{"/etc/apt/apt.conf.d/51bobres-security-only"},
		Made: "bobres harden", Undo: "deleted", Marker: "the header \"Written by bobres harden\""},
	{ID: "swap-file", Class: classOwn, Paths: []string{"/swapfile"},
		Made: "bobres harden (a small server with no swap)", Undo: "swapped off and deleted, only when the receipt says BOBRES made it",
		Marker: "the receipt (an older install has none, so the file is kept)"},
	// ---- changed in a shared file
	{ID: "auto-upgrades", Class: classShared, Paths: []string{"/etc/apt/apt.conf.d/20auto-upgrades"},
		Made: "bobres harden", Undo: "the recorded original put back (the file removed when there was none); without a receipt only BOBRES's header line is taken out",
		Marker: "the header \"Written by bobres harden\""},
	{ID: "fstab", Class: classShared, Paths: []string{"/etc/fstab"},
		Made: "bobres harden (the swap file's line)", Undo: "that one line taken out, only when the receipt says BOBRES added it",
		Marker: "the receipt"},
	{ID: "ufw-rules", Class: classShared, Paths: []string{"ufw:rules"},
		Made:   "bobres harden (allow rules with the comment \"bobres ...\")",
		Undo:   "the web (80, 443) and container rules deleted; the SSH rule and the 3x-ui rules are never touched, and ufw stays on unless --disable-firewall asks",
		Marker: "the ufw comment \"bobres ...\" and the receipt"},
	// ---- installed software
	{ID: "docker-engine", Class: classPackage, Paths: []string{"/etc/apt/sources.list.d/docker.list", "/etc/apt/keyrings/docker.asc",
		"/etc/apt/keyrings/docker.gpg", "/var/lib/docker", "/var/lib/containerd", "/opt/containerd", "/etc/docker", "/etc/containerd",
		"/run/docker", "/run/docker.sock", "/run/containerd", "/var/lib/apt/lists/download.docker.com_*"},
		Made:   "install.sh, through Docker's own installer (https://get.docker.com), when Docker is missing",
		Undo:   "with --remove-docker, only when install.sh recorded it, nothing but BOBRES uses Docker and apt agrees that nothing needs the packages",
		Marker: "the record install.sh keeps in /var/lib/bobres/installed/docker"},
	{ID: "openssl", Class: classPackage, Paths: []string{"pkg:openssl"},
		Made: "install.sh, when openssl is missing", Undo: "with --remove-packages, under the same conditions", Marker: "the record install.sh keeps"},
	{ID: "hardening-packages", Class: classPackage, Paths: []string{"pkg:ufw", "pkg:fail2ban", "pkg:python3-systemd", "pkg:unattended-upgrades"},
		Made: "bobres harden", Undo: "with --remove-packages, under the same conditions", Marker: "the receipt"},
	// ---- runtime
	{ID: "update-requests", Class: classRuntime, Paths: []string{"/run/bobres-update"},
		Made: "docker compose: <install>/run/update, mounted into core's container at this path (the dashboard's update requests)",
		Undo: "inside the install directory, deleted with it", Marker: "-"},
	{ID: "monitor-snapshot", Class: classRuntime, Paths: []string{"/run/bobres-monitor"},
		Made: "docker compose: <install>/run/monitor, mounted read only into core's container at this path (the host collector's snapshot.json; its units are bobres-monitor.timer and bobres-monitor.service)",
		Undo: "inside the install directory, deleted with it; the units are found by name like the updater's", Marker: "-"},
	{ID: "uninstall-lock", Class: classRuntime, Paths: []string{"/run/lock/bobres-uninstall.lock"},
		Made: "bobres uninstall", Undo: "deleted when the run ends", Marker: "the file name"},
	// ---- read only
	{ID: "reads", Class: classRead, Paths: []string{"/etc/apt/sources.list.d", "/etc/apt/keyrings", "/etc/apt/trusted.gpg.d", "/usr/share/keyrings", "/etc/os-release", "/proc/meminfo", "/proc/swaps", "/etc/ssh/sshd_config",
		"/var/log/auth.log", "/sys/**", "/proc/**"},
		Made: "bobres doctor, bobres harden (the SSH port is read, never changed), bobres support-bundle", Undo: "nothing to undo", Marker: "-"},
}

// artifactFor returns the artifact a path literal belongs to ("" when the registry does not
// know it). A pattern matches the path itself, a glob with path.Match, and "/**" at its end
// everything below.
func artifactFor(p string) (string, bool) {
	p = path.Clean(p)
	for _, a := range hostArtifacts {
		for _, pat := range a.Paths {
			if matchArtifactPath(pat, p) {
				return a.ID, true
			}
		}
	}
	return "", false
}

func matchArtifactPath(pat, p string) bool {
	if base, ok := strings.CutSuffix(pat, "/**"); ok {
		return p == base || strings.HasPrefix(p, base+"/")
	}
	if ok, _ := path.Match(pat, p); ok {
		return true
	}
	// A pattern for a directory also covers the files below it.
	return !strings.ContainsAny(pat, "*?[") && strings.HasPrefix(p, pat+"/") && !strings.HasPrefix(pat, "docker:")
}

// knownCommandPrefixes are the program lines the installer and the hardening are allowed to run
// that change the server, each with what undoes it. The tests compare the commands the code runs
// against it.
var knownCommandPrefixes = []string{
	"apt-get update", "apt-get install",
	"ufw allow", "ufw default", "ufw --force enable",
	"systemctl enable fail2ban", "systemctl restart fail2ban",
	"fallocate", "dd", "chmod 600 /swapfile", "mkswap", "swapon",
	"systemctl enable --now docker",
}
