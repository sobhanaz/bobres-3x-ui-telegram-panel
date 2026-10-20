package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run `bobres uninstall` on the lab (uninstall_lab_test.go): a whole server in a
// temporary directory, with fake systemctl, ufw, apt and Docker. Nothing here touches this machine.

func containsAll(t *testing.T, what, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("%s is missing %q:\n%s", what, w, s)
		}
	}
}

func (l *lab) hasContainer(name string) bool {
	for _, c := range l.containers {
		if c.Name == name {
			return true
		}
	}
	return false
}

func (l *lab) hasVolume(name string) bool {
	for _, v := range l.volumes {
		if v.Name == name {
			return true
		}
	}
	return false
}

func (l *lab) hasNetwork(name string) bool {
	for _, n := range l.networks {
		if n.Name == name {
			return true
		}
	}
	return false
}

// hasImage looks for an image by repo:tag, or repo@digest for one pulled by digest.
func (l *lab) hasImage(ref string) bool {
	for _, im := range l.images {
		if im.Repo+":"+im.Tag == ref || im.Repo+"@"+im.Digest == ref {
			return true
		}
	}
	return false
}

func (l *lab) hasRule(substr string) bool {
	for _, r := range l.ufwAdded {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

// noStrangers takes the lab's other-people Docker objects away (a server that only ran BOBRES).
func (l *lab) noStrangers() {
	var cs []labCtr
	for _, c := range l.containers {
		if c.Name != "someone-elses-app" {
			cs = append(cs, c)
		}
	}
	l.containers = cs
	var vs []labVol
	for _, v := range l.volumes {
		if v.Name != "someone_data" {
			vs = append(vs, v)
		}
	}
	l.volumes = vs
	var ns []labNet
	for _, n := range l.networks {
		if n.Name != "someone_net" {
			ns = append(ns, n)
		}
	}
	l.networks = ns
	var is []labImg
	for _, im := range l.images {
		if ref := im.Repo + ":" + im.Tag; ref != "nginx:1.27" && im.Repo+"@"+im.Digest != "redis@sha256:ggg" { // redis was on the server before BOBRES
			is = append(is, im)
		}
	}
	l.images = is
}

// strangersSurvive checks that what is not BOBRES's is where it was.
func (l *lab) strangersSurvive() {
	l.t.Helper()
	if !l.hasContainer("someone-elses-app") || !l.hasVolume("someone_data") || !l.hasNetwork("someone_net") || !l.hasImage("nginx:1.27") {
		l.t.Errorf("a Docker object that is not BOBRES's is gone: containers=%v volumes=%v networks=%v", l.containers, l.volumes, l.networks)
	}
	for _, p := range []string{"/etc/systemd/system/ssh.service.d/keep.conf", "/etc/systemd/system/docker.service"} {
		if !l.exists(p) {
			l.t.Errorf("%s is gone", p)
		}
	}
	for _, r := range []string{"22/tcp comment 'bobres SSH'", "my own rule", "9000/tcp"} {
		if !l.hasRule(r) {
			l.t.Errorf("the ufw rule %q is gone: %v", r, l.ufwAdded)
		}
	}
	if !l.ufwActive {
		l.t.Error("ufw was turned off")
	}
	if !strings.Contains(l.read("/etc/fstab"), "UUID=abcd / ext4") || !strings.Contains(l.read("/etc/fstab"), "# my own comment") {
		l.t.Errorf("/etc/fstab lost lines of the operator's:\n%s", l.read("/etc/fstab"))
	}
}

func TestPurgeDryRunChangesNothing(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	before := l.leftovers()
	if len(before) == 0 {
		t.Fatal("the lab has no install")
	}
	// The volumes' sizes are read from where Docker keeps them: 7 volumes of 1 KiB.
	for _, v := range l.volumes {
		if v.Project {
			l.write(v.Mount+"/data", strings.Repeat("x", 1024), 0o600)
		}
	}
	code := l.purge("", "--dry-run")
	if code != 0 {
		t.Fatalf("dry run: %d\n%s\n%s", code, l.out, l.err)
	}
	containsAll(t, "volume sizes", l.out.String(), "7 volume(s), 7.0 KB:")
	containsAll(t, "plan", l.out.String(), "BOBRES uninstall --purge: the plan", "In this order:", "Dry run: nothing was changed",
		"Never touched: SSH settings and the SSH firewall rule")
	if m := l.mutating(); len(m) != 0 {
		t.Fatalf("a dry run changed the server: %v", m)
	}
	if after := l.leftovers(); len(after) != len(before) {
		t.Fatalf("a dry run removed things:\nbefore %v\nafter %v", before, after)
	}
	if l.exists(l.h.logPath) {
		t.Fatal("a dry run wrote the uninstall log")
	}
	// --backup-to is announced, not run.
	l.run("", "--purge", "--dir", l.dir, "--dry-run", "--backup-to", filepath.Join(l.root, "backups-out", "x.bkp"))
	containsAll(t, "dry run with --backup-to", l.out.String(), "a verified backup would be written")
	if l.anyBackup() {
		t.Fatal("a dry run wrote a backup")
	}
}

func TestPurgeRemovesEverythingOfBobresAndNothingElse(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	code := l.run("", l.yesArgs()...)
	if code != 0 {
		t.Fatalf("purge: %d\n%s\n%s", code, l.out, l.err)
	}
	if left := l.leftovers(); len(left) != 0 {
		t.Fatalf("left behind: %v\n%s", left, l.out)
	}
	l.strangersSurvive()
	// What the hardening replaced is put back, not deleted.
	if got := l.read(autoUpgradesConf); !strings.Contains(got, `Update-Package-Lists "1"`) || strings.Contains(got, "Written by bobres") {
		t.Errorf("%s was not put back as it was:\n%s", autoUpgradesConf, got)
	}
	if l.hasRule("allow 80/tcp") || l.hasRule("allow 443/tcp") || l.hasRule("allow 443/udp") || l.hasRule("port 8081") {
		t.Errorf("a BOBRES firewall rule is left: %v", l.ufwAdded)
	}
	if !l.exists(l.h.logPath) {
		t.Error("the uninstall log was deleted: it is the record of the run")
	}
	// The scan agrees with the test's own view.
	if code := l.run("", "--verify", "--dir", l.dir); code != 0 {
		t.Fatalf("--verify after the purge: %d\n%s\n%s", code, l.out, l.err)
	}
	// Running it again finds nothing and changes nothing.
	l.events = nil
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("second run: %d\n%s\n%s", code, l.out, l.err)
	}
	containsAll(t, "second run", l.out.String(), "Nothing to remove")
	if m := l.mutating(); len(m) != 0 {
		t.Fatalf("the second run changed the server: %v", m)
	}
	l.strangersSurvive()
}

func TestPurgeRemovesEveryUnitFileAndLink(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge: %d\n%s", code, l.out)
	}
	for _, p := range []string{"bobres-updater.path", "bobres-updater.service", "bobres-monitor.timer", "bobres-monitor.service", "bobres-monitor.service.d"} {
		if l.exists("/etc/systemd/system/" + p) {
			t.Errorf("%s is left", p)
		}
	}
	stop := l.cmdIndex("host systemctl stop bobres")
	reload := l.cmdIndex("host systemctl daemon-reload")
	if stop < 0 || reload < stop {
		t.Fatalf("units must be stopped before they are deleted and systemd reloaded: stop=%d reload=%d", stop, reload)
	}
}

func TestPurgeStepOrder(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge: %d\n%s", code, l.out)
	}
	order := []string{"host systemctl stop bobres", "docker compose --project-name bobres", "host ufw --force delete", "host swapoff"}
	last := -1
	for _, prefix := range order {
		i := l.cmdIndex(prefix)
		if i < 0 {
			t.Fatalf("no %q among %v", prefix, l.mutating())
		}
		if i < last {
			t.Fatalf("%q ran out of order (event %d, after %d): %v", prefix, i, last, l.events)
		}
		last = i
	}
	// The command and the receipt go last: the first docker and unit work sees them still there.
	if st, ok := l.stateAt("host systemctl stop bobres"); !ok || !st.DirExists || !st.BinExists || !st.ReceiptExists {
		t.Fatalf("at the first stop: %+v", st)
	}
	if st, ok := l.stateAt("host swapoff"); !ok || !st.BinExists || !st.ReceiptExists || st.UnitsExist {
		t.Fatalf("at swapoff: %+v", st)
	}
}

func TestPurgeConfirmation(t *testing.T) {
	// A wrong token does nothing.
	l := newLab(t)
	l.installBobres()
	before := l.leftovers()
	if code := l.purge("no\nwrong.example.com\n"); code != 1 {
		t.Fatalf("wrong token: %d\n%s", code, l.out)
	}
	containsAll(t, "wrong token", l.out.String(), "Cancelled. Nothing was changed.")
	if m := l.mutating(); len(m) != 0 {
		t.Fatalf("a wrong token changed the server: %v", m)
	}
	if len(l.leftovers()) != len(before) {
		t.Fatal("a wrong token removed things")
	}
	// No answer at all cancels too (a closed stdin).
	if code := l.purge(""); code != 1 || len(l.mutating()) != 0 {
		t.Fatalf("no answer: %d %v", code, l.mutating())
	}
	// The domain, typed in full, goes through (answering "no" to the backup question).
	if code := l.purge("no\n" + labDomain + "\n"); code != 0 {
		t.Fatalf("typed token: %d\n%s\n%s", code, l.out, l.err)
	}
	if left := l.leftovers(); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
}

func TestPurgeYesNeedsTheTokenAndABackupChoice(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"yes alone", []string{"--purge", "--dir", l.dir, "--yes"}, 2, "needs --confirm"},
		{"yes and token, no backup choice", []string{"--purge", "--dir", l.dir, "--yes", "--confirm", labDomain}, 2, "--backup-to FILE"},
		{"confirm without yes", []string{"--purge", "--dir", l.dir, "--confirm", labDomain}, 2, "--confirm goes with --yes"},
		{"both backup flags", []string{"--purge", "--dir", l.dir, "--backup-to", "/x/a.bkp", "--no-backup"}, 2, "contradict"},
		{"purge-only flag without purge", []string{"--dir", l.dir, "--no-backup"}, 2, "only work with --purge"},
		{"wrong token", []string{"--purge", "--dir", l.dir, "--yes", "--confirm", "other.example.com", "--no-backup"}, 1, "--confirm must be exactly"},
		{"verify takes nothing else", []string{"--verify", "--yes", "--dir", l.dir}, 2, "--verify only scans"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := l.run("", c.args...); code != c.code {
				t.Fatalf("exit %d, want %d\n%s\n%s", code, c.code, l.out, l.err)
			}
			containsAll(t, "stderr", l.err.String(), c.want)
			if m := l.mutating(); len(m) != 0 {
				t.Fatalf("changed the server: %v", m)
			}
		})
	}
}

func TestPurgeNeedsRoot(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	l.asRoot = false
	if code := l.run("", l.yesArgs()...); code != 1 {
		t.Fatalf("not root: %d\n%s\n%s", code, l.out, l.err)
	}
	containsAll(t, "stderr", l.err.String(), "run it as root")
	if m := l.mutating(); len(m) != 0 {
		t.Fatalf("changed the server without root: %v", m)
	}
	// A dry run needs no root.
	if code := l.purge("", "--dry-run"); code != 0 {
		t.Fatalf("dry run as a user: %d\n%s", code, l.err)
	}
}

func TestPurgeFinalBackupIsVerifiedBeforeAnythingIsDeleted(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	dest := filepath.Join(l.root, "backups-out", "final.bkp")
	l.mkdir("backups-out")
	args := []string{"--purge", "--dir", l.dir, "--yes", "--confirm", labDomain, "--backup-to", dest}
	if code := l.run("", args...); code != 0 {
		t.Fatalf("purge with a backup: %d\n%s\n%s", code, l.out, l.err)
	}
	fi, err := os.Stat(dest)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("the backup is not there: %v", err)
	}
	if _, err := verifyBackupFile(dest, []byte(labKey)); err != nil {
		t.Fatalf("the backup does not verify with the install's key: %v", err)
	}
	containsAll(t, "output", l.out.String(), "Final backup:", "every part checked", "BOBRES_BACKUP_KEY")
	if strings.Contains(l.read(l.h.logPath), labKey) {
		t.Error("the backup key is in the uninstall log")
	}
	// The database is up for the dump, and the dump is taken before the first deletion.
	up, dump, down := l.cmdIndex("docker compose --project-name bobres -f docker-compose.yml --env-file .env up -d postgres"), l.cmdIndex("docker pipe pg_dump"), l.cmdIndex("host systemctl stop bobres")
	if up < 0 || dump < up || down < dump {
		t.Fatalf("up=%d dump=%d first deletion=%d: %v", up, dump, down, l.events)
	}
	if st, ok := l.stateAt("host systemctl stop bobres"); !ok || !st.BackupFile {
		t.Fatalf("the verified backup did not exist when the removal began: %+v", st)
	}
	if left := l.leftovers(); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
}

func TestPurgeFinalBackupFailureDeletesNothing(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	l.mkdir("backups-out")
	dest := filepath.Join(l.root, "backups-out", "final.bkp")
	before := l.leftovers()
	args := []string{"--purge", "--dir", l.dir, "--yes", "--confirm", labDomain, "--backup-to", dest}

	// The dump fails.
	l.dumpErr = errors.New("pg_dump: connection refused")
	if code := l.run("", args...); code != 1 {
		t.Fatalf("failed dump: %d\n%s\n%s", code, l.out, l.err)
	}
	containsAll(t, "stderr", l.err.String(), "the final backup failed", "Nothing was deleted")
	if l.anyBackup() {
		t.Fatal("a half-written backup was left behind")
	}
	// A dump that is not a database dump does not verify (a truncated or empty stream).
	l.dumpErr, l.dump = nil, ""
	if code := l.run("", args...); code != 1 {
		t.Fatalf("empty dump: %d\n%s\n%s", code, l.out, l.err)
	}
	if l.anyBackup() {
		t.Fatal("an unverified backup was left behind")
	}
	// The database cannot be started.
	l.dump = "PGDMP lab dump"
	l.pgRunning = false
	l.fail["docker compose --project-name bobres -f docker-compose.yml --env-file .env up -d postgres"] = errors.New("exit status 1")
	if code := l.run("", args...); code != 1 {
		t.Fatalf("db down: %d\n%s\n%s", code, l.out, l.err)
	}
	delete(l.fail, "docker compose --project-name bobres -f docker-compose.yml --env-file .env up -d postgres")

	for _, c := range l.mutating() {
		if strings.Contains(c, "systemctl stop") || strings.Contains(c, " down ") || strings.Contains(c, "rmi") || strings.Contains(c, "ufw --force") ||
			strings.Contains(c, "volume rm") || strings.Contains(c, "swapoff") {
			t.Fatalf("a deletion ran although the backup failed: %s", c)
		}
	}
	if after := l.leftovers(); len(after) != len(before) {
		t.Fatalf("things were deleted although the backup failed:\nbefore %v\nafter %v", before, after)
	}
	if !l.exists("/var/lib/bobres/" + receiptFile) {
		t.Fatal("the receipt is gone")
	}
}

func TestPurgeFinalBackupTargetRules(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	l.mkdir("backups-out")
	good := filepath.Join(l.root, "backups-out")
	l.write("/backups-out/taken.bkp", "keep me", 0o600)
	cases := []struct {
		name, dest, want string
	}{
		{"inside the install directory", filepath.Join(l.dir, "backups", "final.bkp"), "inside"},
		{"inside the state directory", l.p("/var/lib/bobres/final.bkp"), "inside"},
		{"not a .bkp name", filepath.Join(good, "final.tar"), "must end in .bkp"},
		{"a directory that does not exist", filepath.Join(good, "nope", "final.bkp"), "does not exist"},
		{"an existing file is never overwritten", filepath.Join(good, "taken.bkp"), "already exists"},
	}
	before := l.leftovers()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code := l.run("", "--purge", "--dir", l.dir, "--yes", "--confirm", labDomain, "--backup-to", c.dest)
			if code != 1 {
				t.Fatalf("exit %d\n%s\n%s", code, l.out, l.err)
			}
			containsAll(t, "stderr", l.err.String(), c.want, "Nothing was deleted")
		})
	}
	if got := l.read("/backups-out/taken.bkp"); got != "keep me" {
		t.Fatalf("an existing file was overwritten: %q", got)
	}
	if len(l.leftovers()) != len(before) {
		t.Fatal("something was deleted")
	}
	// Not enough room.
	l.o.freeGB = func(string) float64 { return 0.1 }
	if code := l.run("", "--purge", "--dir", l.dir, "--yes", "--confirm", labDomain, "--backup-to", filepath.Join(good, "new.bkp")); code != 1 {
		t.Fatalf("no room: %d", code)
	}
	containsAll(t, "stderr", l.err.String(), "GB free")
	// An existing directory gets a file named after the time.
	l.o.freeGB = func(string) float64 { return 42 }
	if code := l.run("", "--purge", "--dir", l.dir, "--yes", "--confirm", labDomain, "--backup-to", good); code != 0 {
		t.Fatalf("directory target: %d\n%s\n%s", code, l.out, l.err)
	}
	if m, _ := filepath.Glob(filepath.Join(good, "bobres-final-*.bkp")); len(m) != 1 {
		t.Fatalf("no timestamped backup in %s: %v", good, m)
	}
}

func TestPurgeResumesAfterAFailure(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	// Docker refuses to delete a volume: the run stops there, with the install directory,
	// the command and the receipt still in place.
	l.fail["docker volume rm"] = errors.New("exit status 1")
	code := l.run("", l.yesArgs()...)
	if code != 1 {
		t.Fatalf("failing run: %d\n%s\n%s", code, l.out, l.err)
	}
	containsAll(t, "output", l.out.String(), "did not finish", "running the same command again", "sudo bobres uninstall --purge --dir "+l.dir)
	for _, p := range []string{"/usr/local/bin/bobres", "/var/lib/bobres/" + receiptFile} {
		if !l.exists(p) {
			t.Errorf("%s was deleted although a step failed: the run cannot be resumed", p)
		}
	}
	if !existsReal(l.dir) {
		t.Error("the install directory was deleted although the Docker step failed")
	}
	if !l.hasRule("allow 80/tcp") {
		t.Error("the firewall step ran after a failed stopping step")
	}
	// The fault is gone: the same command finishes.
	delete(l.fail, "docker volume rm")
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("resumed run: %d\n%s\n%s", code, l.out, l.err)
	}
	if left := l.leftovers(); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
	l.strangersSurvive()
}

func TestPurgeHalfRemovedServerStillFinishes(t *testing.T) {
	// Somebody already deleted the install directory and stopped Docker: the rest still goes.
	l := newLab(t)
	l.installBobres()
	if err := os.RemoveAll(l.dir); err != nil {
		t.Fatal(err)
	}
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge: %d\n%s\n%s", code, l.out, l.err)
	}
	if left := l.leftovers(); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
	l.strangersSurvive()
}

func TestPurgeKeepsAnImageAnotherContainerUses(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	// A stranger's container runs the caddy image BOBRES pulled too.
	l.images[3].UsedBy = "someone-elses-proxy"
	l.rmiRefused["caddy@sha256:ddd"] = "image is being used by running container"
	if code := l.run("", l.yesArgs()...); code != 0 && code != 1 {
		t.Fatalf("purge: %d\n%s\n%s", code, l.out, l.err)
	}
	if !l.hasImage("caddy@sha256:ddd") {
		t.Fatal("an image another container uses was force-removed")
	}
	for _, c := range l.dockerCmds {
		if strings.Contains(c, "rmi -f") || strings.Contains(c, "--force") && strings.Contains(c, "rmi") {
			t.Fatalf("docker rmi was forced: %s", c)
		}
	}
	if l.hasImage("ghcr.io/sobhanaz/bobres-core:0.6.1") {
		t.Fatal("BOBRES's own image is left")
	}
}

func TestPurgeNeverTouchesForeignDockerObjects(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	// Look-alikes: a container of a different project, a volume with a bobres-like name.
	l.volumes = append(l.volumes, labVol{Name: "mybobres_data", Mount: "/var/lib/docker/volumes/mybobres_data/_data"})
	l.networks = append(l.networks, labNet{ID: "n-x", Name: "notbobres_net"})
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge: %d\n%s\n%s", code, l.out, l.err)
	}
	if !l.hasVolume("mybobres_data") || !l.hasNetwork("notbobres_net") {
		t.Fatalf("look-alike objects were deleted: %v %v", l.volumes, l.networks)
	}
	l.strangersSurvive()
}

func TestPurgeRefusals(t *testing.T) {
	t.Run("a protected directory", func(t *testing.T) {
		l := newLab(t)
		l.installBobres()
		for _, d := range []string{"/", "/etc", "/usr", "/var", "/var/lib", "/root", "/home", "/opt"} {
			if code := l.run("", "--purge", "--dir", d, "--dry-run"); code != 2 {
				t.Errorf("--dir %s: exit %d, want 2 (refused)\n%s", d, code, l.err)
			}
		}
		if m := l.mutating(); len(m) != 0 {
			t.Fatalf("changed the server: %v", m)
		}
	})
	t.Run("the state directory", func(t *testing.T) {
		l := newLab(t)
		l.installBobres()
		if code := l.run("", "--purge", "--dir", l.p("/var/lib/bobres"), "--dry-run"); code != 2 {
			t.Fatalf("exit %d\n%s", code, l.err)
		}
	})
	t.Run("a directory that is not a BOBRES install", func(t *testing.T) {
		l := newLab(t)
		l.installBobres()
		l.write("/srv/mysite/index.html", "hello", 0o644)
		l.write("/srv/mysite/.env", "SECRET=1\n", 0o600)
		if code := l.run("", "--purge", "--dir", l.p("/srv/mysite"), "--yes", "--confirm", "x", "--no-backup"); code == 0 {
			t.Fatalf("a foreign directory was purged\n%s", l.out)
		}
		if !l.exists("/srv/mysite/index.html") || !l.exists("/srv/mysite/.env") {
			t.Fatal("a foreign directory lost files")
		}
		if len(l.leftovers()) == 0 {
			t.Fatal("BOBRES's own things were removed for the wrong directory")
		}
	})
	t.Run("a receipt that names another directory", func(t *testing.T) {
		l := newLab(t)
		l.installBobres()
		l.saveReceipt(func(r *receipt) { r.InstallDir = "/opt/elsewhere" })
		if code := l.purge("", "--dry-run"); code != 2 {
			t.Fatalf("exit %d\n%s\n%s", code, l.out, l.err)
		}
		containsAll(t, "stderr", l.err.String(), "install receipt says BOBRES was installed in /opt/elsewhere", "nothing was changed")
	})
	t.Run("containers of another compose project directory", func(t *testing.T) {
		l := newLab(t)
		l.installBobres()
		for i := range l.containers {
			if l.containers[i].Project {
				l.containers[i].WorkDir = "/srv/other-bobres"
			}
		}
		if code := l.purge("", "--dry-run"); code != 2 {
			t.Fatalf("exit %d\n%s\n%s", code, l.out, l.err)
		}
		containsAll(t, "stderr", l.err.String(), "belong to the compose project in /srv/other-bobres", "nothing was changed")
	})
	t.Run("an install directory that overlaps the state directory", func(t *testing.T) {
		l := newLab(t)
		l.installBobres()
		l.write("/var/lib/bobres/.env", string(renderEnv(map[string]string{"BOBRES_DOMAIN": labDomain, "BOBRES_VERSION": "1"})), 0o600)
		if code := l.run("", "--purge", "--dir", l.p("/var/lib"), "--dry-run"); code != 2 {
			t.Fatalf("exit %d\n%s", code, l.err)
		}
	})
}

func TestPurgeDoesNotFollowSymlinks(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	// A file outside the install, reachable through a link inside it, must survive.
	outside := l.p("/srv/precious")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	l.write("/srv/precious/data.txt", "precious", 0o644)
	if err := os.Symlink(outside, filepath.Join(l.dir, "backups", "link-out")); err != nil {
		t.Fatal(err)
	}
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge: %d\n%s\n%s", code, l.out, l.err)
	}
	if got := l.read("/srv/precious/data.txt"); got != "precious" {
		t.Fatalf("a symlink inside the install led the deletion out of it: %q", got)
	}
	if existsReal(l.dir) {
		t.Fatal("the install directory is left")
	}
}

func TestPurgeInstallDirIsASymlinkToAProtectedPlace(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	link := l.p("/opt/sneaky")
	if err := os.Symlink(l.p("/etc"), link); err != nil {
		t.Fatal(err)
	}
	if code := l.run("", "--purge", "--dir", link, "--dry-run"); code != 2 {
		t.Fatalf("exit %d\n%s\n%s", code, l.out, l.err)
	}
}

func TestPurgeTimerAndUpdaterOnlyTheirOwnUnits(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	// A unit systemd knows (a package's) but with no file in /etc/systemd/system is not BOBRES's.
	l.units["bobres-packaged.service"] = "inactive"
	l.vendor["bobres-packaged.service"] = true
	// A busy unit is not interrupted halfway.
	l.units["bobres-updater.service"] = "activating"
	before := l.leftovers()
	code := l.run("", l.yesArgs()...)
	if code != 1 {
		t.Fatalf("busy unit: %d\n%s\n%s", code, l.out, l.err)
	}
	containsAll(t, "output", l.out.String(), "bobres-updater.service is busy right now")
	if len(l.leftovers()) != len(before) {
		t.Fatalf("a busy updater did not stop the purge:\n%v", l.leftovers())
	}
	for _, c := range l.mutating() {
		if strings.Contains(c, "bobres-packaged") {
			t.Fatalf("touched a unit that is not BOBRES's: %s", c)
		}
	}
	l.units["bobres-updater.service"] = "inactive"
	if code := l.run("", l.yesArgs()...); code != 1 && code != 0 {
		t.Fatalf("resumed: %d", code)
	}
	if l.exists("/etc/systemd/system/bobres-updater.service") {
		t.Fatal("the updater unit is left")
	}
	if _, ok := l.units["bobres-packaged.service"]; !ok {
		t.Fatal("a vendor unit was forgotten")
	}
}

func TestPurgeFirewallOnlyBobresRules(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge: %d\n%s", code, l.out)
	}
	for _, c := range l.host("ufw") {
		if strings.Contains(c, "22/tcp") || strings.Contains(c, "2053") || strings.Contains(c, "8080") || strings.Contains(c, "9000") ||
			strings.Contains(c, "disable") {
			t.Errorf("a ufw command touched a rule that stays: %s", c)
		}
	}
	// Even when asked to disable the firewall, it stays on unless the receipt says BOBRES turned it on.
	l2 := newLab(t)
	l2.installBobres()
	l2.saveReceipt(func(r *receipt) { r.Firewall.EnabledByBobres = false })
	if code := l2.run("", l2.yesArgs("--disable-firewall")...); code != 0 {
		t.Fatalf("purge: %d\n%s", code, l2.out)
	}
	if !l2.ufwActive || len(l2.host("ufw --force disable")) != 0 {
		t.Fatal("ufw was disabled although no receipt says BOBRES enabled it")
	}
	// And it is disabled when the receipt does say so.
	l3 := newLab(t)
	l3.installBobres()
	if code := l3.run("", l3.yesArgs("--disable-firewall")...); code != 0 {
		t.Fatalf("purge: %d\n%s", code, l3.out)
	}
	if l3.ufwActive {
		t.Fatal("ufw is still on although BOBRES turned it on and --disable-firewall was given")
	}
	if !l3.hasRule("22/tcp comment 'bobres SSH'") {
		t.Fatal("the SSH rule was deleted")
	}
}

func TestPurgeKeepsBackupsOnRequest(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	code := l.run("", l.yesArgs("--keep-backups")...)
	if code != 0 && code != 1 {
		t.Fatalf("purge: %d\n%s\n%s", code, l.out, l.err)
	}
	if !l.exists(strings.TrimPrefix(l.dir, l.root) + "/backups/bobres-20261019T033000Z.bkp") {
		t.Fatalf("--keep-backups deleted a backup:\n%s", l.out)
	}
	if l.exists(strings.TrimPrefix(l.dir, l.root) + "/.env") {
		t.Fatal(".env (the secrets) is left next to the kept backups")
	}
}

func TestPurgeDockerAndPackagesOnlyWithProof(t *testing.T) {
	setup := func(t *testing.T) *lab {
		l := newLab(t)
		l.installBobres()
		l.pkgs["docker-ce"], l.pkgs["docker-ce-cli"], l.pkgs["containerd.io"] = true, true, true
		l.pkgs["ufw"], l.pkgs["fail2ban"] = true, true
		l.auto["containerd.io"] = true
		l.programs["docker"] = true
		l.groups["docker"] = "docker:x:999:"
		l.write("/etc/apt/sources.list.d/docker.list", "deb https://download.docker.com\n", 0o644)
		l.write("/etc/apt/keyrings/docker.asc", "key", 0o644)
		l.write("/etc/apt/sources.list.d/mine.list", "deb http://example.com\n", 0o644)
		l.write("/var/lib/docker/overlay2/x", "layer", 0o644)
		return l
	}
	t.Run("without a record nothing is removed", func(t *testing.T) {
		l := setup(t)
		code := l.run("", l.yesArgs("--remove-docker", "--remove-packages")...)
		if code == 0 {
			t.Fatalf("expected a non-zero exit, Docker cannot be proven to be BOBRES's\n%s", l.out)
		}
		for _, p := range []string{"docker-ce", "ufw", "fail2ban"} {
			if !l.pkgs[p] {
				t.Errorf("%s was purged without a record", p)
			}
		}
		if !l.exists("/etc/apt/sources.list.d/docker.list") || !l.exists("/var/lib/docker/overlay2/x") {
			t.Error("Docker's files were removed without a record")
		}
	})
	t.Run("with a record only what was recorded goes", func(t *testing.T) {
		l := setup(t)
		l.noStrangers()
		l.saveReceipt(func(r *receipt) {
			r.setPackages(pkgGroup{Name: "docker", By: "install.sh", Requested: []string{"docker-ce", "docker-ce-cli"},
				Packages: []string{"docker-ce", "docker-ce-cli", "containerd.io"}, SystemGroup: "docker",
				Files: []string{"/etc/apt/sources.list.d/docker.list", "/etc/apt/keyrings/docker.asc"}})
		})
		code := l.run("", l.yesArgs("--remove-docker")...)
		if code != 0 {
			t.Fatalf("exit %d\n%s\n%s", code, l.out, l.err)
		}
		for _, p := range []string{"docker-ce", "docker-ce-cli", "containerd.io"} {
			if l.pkgs[p] {
				t.Errorf("%s is left", p)
			}
		}
		if l.exists("/etc/apt/sources.list.d/docker.list") || l.exists("/etc/apt/keyrings/docker.asc") || l.exists("/var/lib/docker/overlay2/x") {
			t.Error("Docker's apt source, key or data is left")
		}
		if !l.exists("/etc/apt/sources.list.d/mine.list") {
			t.Error("an apt source that is not Docker's was deleted")
		}
		if !l.pkgs["ufw"] || !l.pkgs["fail2ban"] {
			t.Error("--remove-docker removed ufw or fail2ban")
		}
		if _, ok := l.groups["docker"]; ok {
			t.Error("the docker group is left")
		}
	})
	t.Run("another container blocks removing Docker", func(t *testing.T) {
		l := setup(t)
		l.saveReceipt(func(r *receipt) {
			r.setPackages(pkgGroup{Name: "docker", By: "install.sh", Requested: []string{"docker-ce"}, Packages: []string{"docker-ce"}})
		})
		code := l.run("", l.yesArgs("--remove-docker")...)
		if code == 0 {
			t.Fatalf("Docker was removed while a stranger's container lives in it\n%s", l.out)
		}
		if !l.pkgs["docker-ce"] || !l.hasContainer("someone-elses-app") {
			t.Fatal("Docker or the stranger's container is gone")
		}
	})
	t.Run("a package the operator installed since stays", func(t *testing.T) {
		l := setup(t)
		l.noStrangers()
		l.saveReceipt(func(r *receipt) {
			r.setPackages(pkgGroup{Name: "fail2ban", By: "bobres harden", Requested: []string{"fail2ban"}, Packages: []string{"fail2ban", "python3-pyinotify"}})
		})
		l.pkgs["python3-pyinotify"] = true // manual (not in l.auto): the operator's now
		l.needs["fail2ban"] = nil
		code := l.run("", l.yesArgs("--remove-packages")...)
		if code != 0 {
			t.Fatalf("exit %d\n%s\n%s", code, l.out, l.err)
		}
		if l.pkgs["fail2ban"] {
			t.Error("fail2ban was recorded and requested but is left")
		}
		if !l.pkgs["python3-pyinotify"] {
			t.Error("a package apt counts as manually installed was purged")
		}
		if !l.pkgs["ufw"] {
			t.Error("ufw was purged without a record")
		}
	})
	t.Run("a package something else needs stays", func(t *testing.T) {
		l := setup(t)
		l.saveReceipt(func(r *receipt) {
			r.setPackages(pkgGroup{Name: "fail2ban", By: "bobres harden", Requested: []string{"fail2ban"}, Packages: []string{"fail2ban"}})
		})
		l.pkgs["important-app"] = true
		l.needs["fail2ban"] = []string{"important-app"}
		code := l.run("", l.yesArgs("--remove-packages")...)
		if code == 0 {
			t.Logf("exit 0, acceptable only if fail2ban was kept\n%s", l.out)
		}
		if !l.pkgs["important-app"] {
			t.Fatal("a package that is not BOBRES's was removed with it")
		}
		if !l.pkgs["fail2ban"] {
			t.Fatal("fail2ban was purged although apt would remove another program with it")
		}
	})
}

func TestVerify(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	if code := l.run("", "--verify", "--dir", l.dir); code != 1 {
		t.Fatalf("verify on a full install: %d\n%s\n%s", code, l.out, l.err)
	}
	containsAll(t, "verify", l.out.String()+l.err.String(), "left")
	if m := l.mutating(); len(m) != 0 {
		t.Fatalf("--verify changed the server: %v", m)
	}
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge: %d", code)
	}
	if code := l.run("", "--verify", "--dir", l.dir); code != 0 {
		t.Fatalf("verify after the purge: %d\n%s\n%s", code, l.out, l.err)
	}
	// A file that comes back is found.
	l.write("/etc/systemd/system/bobres-monitor.timer", "[Timer]\n", 0o644)
	if code := l.run("", "--verify", "--dir", l.dir); code != 1 {
		t.Fatalf("verify missed a unit file: %d\n%s", code, l.out)
	}
	if !strings.Contains(l.out.String()+l.err.String(), "bobres-monitor.timer") {
		t.Fatalf("verify does not name the leftover:\n%s", l.out)
	}
}

func TestPurgeOnAServerWithoutAnInstall(t *testing.T) {
	l := newLab(t)
	code := l.run("", "--purge", "--dir", l.dir, "--yes", "--confirm", "lab-host", "--no-backup")
	if code != 0 {
		t.Fatalf("empty server: %d\n%s\n%s", code, l.out, l.err)
	}
	containsAll(t, "output", l.out.String(), "Nothing to remove")
	if m := l.mutating(); len(m) != 0 {
		t.Fatalf("changed an empty server: %v", m)
	}
}

func TestPurgeTokenFallsBackToTheHostName(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	// An install without a domain in .env or the receipt is confirmed with the host's name.
	l.saveReceipt(func(r *receipt) { r.Domain = "" })
	envPath := filepath.Join(l.dir, ".env")
	env := renderEnv(map[string]string{"BOBRES_VERSION": "0.6.1", "BOBRES_BACKUP_KEY": labKey, "POSTGRES_PASSWORD": "x-lab-pg-password"}) //nolint:gosec // test fixture
	if err := os.WriteFile(envPath, env, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := l.run("", "--purge", "--dir", l.dir, "--yes", "--confirm", labDomain, "--no-backup"); code != 1 {
		t.Fatalf("the old domain was accepted: %d\n%s", code, l.err)
	}
	if code := l.run("", "--purge", "--dir", l.dir, "--yes", "--confirm", "lab-host", "--no-backup"); code != 0 {
		t.Fatalf("host name token: %d\n%s\n%s", code, l.out, l.err)
	}
}
