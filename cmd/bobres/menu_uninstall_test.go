package main

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// menuUninstallNumber is the number of the Uninstall item in the menu of an installed store.
func menuUninstallNumber(t *testing.T, l *lab) int {
	t.Helper()
	m := &menu{dir: l.dir, ops: l.o}
	env, _, _ := readEnv(filepath.Join(l.dir, ".env"))
	for i, it := range m.items(env, true) {
		if it.label == "Uninstall" {
			return i + 1
		}
	}
	t.Fatal("the menu has no Uninstall item")
	return 0
}

// runMenu runs the menu of the lab's install over the typed lines and returns what it printed.
func runMenu(t *testing.T, l *lab, stdin string) string {
	t.Helper()
	br := bufio.NewReader(strings.NewReader(stdin))
	var out, errOut bytes.Buffer
	l.o.in, l.o.out, l.o.errOut = br, &out, &errOut
	m := &menu{in: br, out: &out, errOut: &errOut, dir: l.dir, ops: l.o,
		install: func([]string, map[string]string, bool) int { return 0 },
		doctor:  func([]string) int { return 0 },
		shield:  func(fn func()) { fn() }}
	if code := m.loop(); code != 0 {
		t.Fatalf("menu exit %d\n%s%s", code, &out, &errOut)
	}
	return out.String() + errOut.String()
}

// destructive are the commands that remove or stop something (the menu's header also asks Docker
// questions, which are reads).
func (l *lab) destructive() []string {
	var out []string
	for _, c := range l.events {
		for _, w := range []string{" down", "systemctl stop", "ufw --force", "volume rm", "network rm", "rmi ", "docker rm", "swapoff", "apt-get purge", "groupdel"} {
			if strings.Contains(c, w) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// The menu's Uninstall item opens a submenu whose choices are the commands of bobres uninstall: the
// containers only, everything (which asks what the command asks), and the leftover scan. Choosing
// everything gives no shortcut around the question about the backup or the typed token.
func TestMenuUninstallSubmenu(t *testing.T) {
	l := newLab(t)
	l.installBobres()
	l.noStrangers()
	n := strconv.Itoa(menuUninstallNumber(t, l))

	// 3: the scan only. The install is there, so it lists what is left.
	got := runMenu(t, l, n+"\n3\n\n0\n")
	containsAll(t, "the scan from the menu", got, "Uninstall", "Remove EVERYTHING BOBRES put on this server", "Leftover scan", "still here")

	// 2 with a wrong token: nothing is removed.
	before := len(l.leftovers())
	l.events = nil
	got = runMenu(t, l, n+"\n2\nno\nnot-the-domain\n\n0\n")
	containsAll(t, "everything, wrong token", got, "Type "+labDomain+" to delete all of it", "Cancelled. Nothing was changed.")
	if len(l.leftovers()) != before || len(l.destructive()) != 0 {
		t.Fatalf("the menu removed something without the token: %v", l.destructive())
	}

	// No answer to the question about the backup cancels too.
	got = runMenu(t, l, n+"\n2\n")
	containsAll(t, "everything, no answer", got, "A final backup lets you bring this store back")
	if len(l.leftovers()) != before || len(l.destructive()) != 0 {
		t.Fatal("the menu removed something without an answer")
	}

	// 1 is the plain uninstall: containers only, the data stays.
	got = runMenu(t, l, n+"\n1\nn\n\n0\n")
	containsAll(t, "containers only, declined", got, "Cancelled.")

	// 2 with the token: everything goes.
	got = runMenu(t, l, n+"\n2\nno\n"+labDomain+"\n\n0\n")
	containsAll(t, "everything", got, "Nothing of BOBRES is left on this server.")
	if left := l.leftovers(); len(left) != 0 {
		t.Fatalf("left after the menu's purge: %v", left)
	}
}
