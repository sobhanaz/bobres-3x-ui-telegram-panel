package main

import (
	"strings"
	"testing"
)

// The hardening's package groups are removed latest first, so that a dependency an earlier group
// shares is free when its turn comes; the plan counts what an earlier group frees; a service is
// stopped before its package goes, and ufw's own script flushes the chains ufw leaves in the kernel.
func TestPurgePackagesInOrderWithTheirServicesAndChains(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	l.noStrangers()
	// No rule of the operator's: ufw may go.
	l.ufwAdded = []string{"ufw allow 22/tcp comment 'bobres SSH'", "ufw allow 80/tcp comment 'bobres HTTP'"}
	l.write("/lib/ufw/ufw-init", "#!/bin/sh\n", 0o755)
	l.pkgs = map[string]bool{"dpkg": true, "ufw": true, "python3": true, "fail2ban": true, "python3-systemd": true, "unattended-upgrades": true, "whois": true}
	l.auto = map[string]bool{"python3": true, "whois": true}
	// fail2ban needs the python3 that came with ufw (and unattended-upgrades would too).
	l.needs["python3"] = []string{"fail2ban", "python3-systemd", "unattended-upgrades"}
	l.saveReceipt(func(r *receipt) {
		r.setPackages(pkgGroup{Name: "ufw", By: "bobres harden", Requested: []string{"ufw"}, Packages: []string{"ufw", "python3"}})
		r.setPackages(pkgGroup{Name: "fail2ban", By: "bobres harden", Requested: []string{"fail2ban", "python3-systemd"}, Packages: []string{"fail2ban", "python3-systemd", "whois"}})
		r.setPackages(pkgGroup{Name: "unattended-upgrades", By: "bobres harden", Requested: []string{"unattended-upgrades"}, Packages: []string{"unattended-upgrades"}})
	})

	// The plan counts what the groups removed before ufw free: python3 is not "kept" in it.
	if code := l.purge("", "--dry-run", "--remove-packages"); code != 0 {
		t.Fatalf("dry run: %d\n%s%s", code, l.out, l.err)
	}
	plan := l.out.String()
	containsAll(t, "the plan", plan, "purge ufw: python3, ufw", "purge fail2ban: fail2ban, python3-systemd, whois", "purge unattended-upgrades: unattended-upgrades")
	if strings.Contains(plan, "keep the package python3") {
		t.Errorf("the plan keeps python3 although the groups that need it go first:\n%s", plan)
	}

	if code := l.run("", l.yesArgs("--remove-packages")...); code != 0 {
		t.Fatalf("purge: %d\n%s%s", code, l.out, l.err)
	}
	if len(l.pkgs) != 1 || !l.pkgs["dpkg"] {
		t.Errorf("packages left: %v", l.pkgs)
	}
	uu, f2b, ufw := l.cmdIndex("host apt-get purge -y -q unattended-upgrades"), l.cmdIndex("host apt-get purge -y -q fail2ban"), l.cmdIndex("host apt-get purge -y -q python3 ufw")
	if uu < 0 || f2b < 0 || ufw < 0 || uu >= f2b || f2b >= ufw {
		t.Errorf("groups must be removed latest first (unattended-upgrades, fail2ban, ufw): %d %d %d\n%v", uu, f2b, ufw, l.events)
	}
	if stop := l.cmdIndex("host systemctl stop fail2ban"); stop < 0 || stop > f2b {
		t.Errorf("fail2ban must be stopped before its package is purged: %d %d", stop, f2b)
	}
	if stop := l.cmdIndex("host systemctl stop unattended-upgrades"); stop < 0 || stop > uu {
		t.Errorf("unattended-upgrades must be stopped before its package is purged: %d %d", stop, uu)
	}
	disable, flush := l.cmdIndex("host ufw --force disable"), l.cmdIndex("host /lib/ufw/ufw-init flush-all")
	if disable < 0 || flush < disable || flush > ufw {
		t.Errorf("ufw is turned off, its chains flushed, then the package goes: %d %d %d", disable, flush, ufw)
	}
}
