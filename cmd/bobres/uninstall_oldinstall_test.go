package main

import (
	"strings"
	"testing"
)

// A server installed by an earlier release has no receipt. The uninstall then goes by the markers the
// installer wrote (headers, names, labels) and takes the careful choice where they prove nothing.
func TestPurgeOnAnInstallWithoutAReceipt(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	l.dropReceipt()
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge without a receipt: %d\n%s\n%s", code, l.out, l.err)
	}
	// What carries BOBRES's mark goes: units, the project's objects, its firewall rules, the two files with
	// its header, the command.
	for _, p := range []string{fail2banJail, securityOnlyConf, "/usr/local/bin/bobres", "/var/log/bobres-install.log"} {
		if l.exists(p) {
			t.Errorf("%s is left", p)
		}
	}
	if l.anyUnitFile() || l.hasContainer("bobres-core-1") || l.hasVolume("bobres_pg_data") || l.hasNetwork("bobres_edge") {
		t.Errorf("units or the project's objects are left: %v", l.leftovers())
	}
	if l.hasRule("allow 80/tcp") || l.hasRule("port 8081") || !l.hasRule("bobres SSH") || !l.hasRule("bobres 3x-ui") {
		t.Errorf("firewall rules: %v", l.ufwAdded)
	}
	// The shared apt file keeps its settings (the system may have had them): only the header goes.
	got := l.read(autoUpgradesConf)
	if strings.Contains(got, "Written by bobres") || !strings.Contains(got, `Unattended-Upgrade "1"`) {
		t.Errorf("%s:\n%s", autoUpgradesConf, got)
	}
	// Nothing proves BOBRES made the swap file: it and its line stay, and the output says why.
	if !l.exists("/swapfile") || !strings.Contains(l.read("/etc/fstab"), "/swapfile none swap sw 0 0") {
		t.Error("the swap file or its fstab line was removed without a receipt")
	}
	containsAll(t, "output", l.out.String(), "kept /swapfile: no receipt says BOBRES made it", "Nothing of BOBRES is left on this server.")
	// The images the project's containers ran go (their ids were seen before the containers did); a
	// stranger's stays.
	if l.hasImage("caddy@sha256:ddd") || l.hasImage("postgres@sha256:eee") || l.hasImage("ghcr.io/sobhanaz/bobres-core:0.6.1") {
		t.Errorf("images of BOBRES's are left: %v", l.images)
	}
	l.strangersSurvive()

	// Docker and the packages are removed only on proof, and without a receipt there is none.
	l2 := newLab(t)
	l2.installBobres()
	l2.dropReceipt()
	l2.pkgs["docker-ce"], l2.pkgs["ufw"] = true, true
	if code := l2.run("", l2.yesArgs("--remove-docker", "--remove-packages")...); code == 0 {
		t.Fatalf("--remove-docker without a record succeeded:\n%s", l2.out)
	}
	containsAll(t, "output", l2.out.String()+l2.err.String(), "BOBRES cannot prove it installed it")
	if !l2.pkgs["docker-ce"] || !l2.pkgs["ufw"] || len(l2.host("apt-get purge")) != 0 {
		t.Errorf("packages were purged without a receipt: %v %v", l2.pkgs, l2.hostCmds)
	}
}

// After a plain `bobres uninstall` (the containers are gone, the volumes stay) the purge still finds
// what is BOBRES's: the volumes by label, and the images by the receipt, now that no container says
// which image it ran.
func TestPurgeAfterThePlainUninstall(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	var left []labCtr
	for _, c := range l.containers {
		if !c.Project {
			left = append(left, c)
		}
	}
	l.containers = left
	l.networks = l.networks[len(l.networks)-1:] // down removed the project's networks too
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge after the plain uninstall: %d\n%s\n%s", code, l.out, l.err)
	}
	if got := l.leftovers(); len(got) != 0 {
		t.Fatalf("left: %v", got)
	}
	// The receipt names caddy and postgres as pulled by an install, redis as there before.
	if l.hasImage("caddy@sha256:ddd") || l.hasImage("postgres@sha256:eee") {
		t.Errorf("images the receipt says an install pulled are left: %v", l.images)
	}
	if !l.hasImage("redis@sha256:ggg") {
		t.Error("an image that was on the server before BOBRES was removed")
	}
}
