package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminPassword(t *testing.T) {
	f := &fakeOps{}
	o, out, errOut := testOps("", f)
	dir := installDir(t)
	if code := o.adminCmd([]string{"password", "--dir", dir}); code != 0 {
		t.Fatalf("admin password: %d %s", code, errOut)
	}
	got := strings.Join(f.stream[0], " ")
	if !strings.HasSuffix(got, "exec -T core /app admin-password") || !strings.Contains(got, "--project-name bobres") {
		t.Fatalf("compose args: %s", got)
	}
	if !strings.Contains(out.String(), "shown once") || !strings.Contains(out.String(), "change it at first login") {
		t.Fatalf("the warning is missing: %q", out)
	}
	if code := o.adminCmd([]string{"password", "--dir", dir, "extra"}); code != 2 {
		t.Fatalf("a stray argument: %d", code)
	}
	if code := o.adminCmd([]string{"password", "--dir", t.TempDir()}); code != 2 {
		t.Fatalf("no install: %d", code)
	}
}

func TestAdminPasswordFailureAndLinkHint(t *testing.T) {
	f := &fakeOps{}
	o, _, errOut := testOps("", f)
	o.stream = func(context.Context, string, ...string) error { return errors.New("exit status 1") }
	dir := installDir(t)
	if code := o.adminCmd([]string{"password", "--dir", dir}); code != 1 || !strings.Contains(errOut.String(), "is core running") {
		t.Fatalf("password failure: %d %q", code, errOut)
	}
	errOut.Reset()
	if code := o.adminCmd([]string{"link", "--dir", dir}); code != 1 || !strings.Contains(errOut.String(), "bobres admin password") {
		t.Fatalf("link failure must point to the no-Telegram path: %d %q", code, errOut)
	}
}

func TestAdminPasswordForAChosenOwner(t *testing.T) {
	f := &fakeOps{}
	o, _, errOut := testOps("", f)
	dir := installDir(t)
	if code := o.adminCmd([]string{"password", "--dir", dir, "--id", "4243"}); code != 0 {
		t.Fatalf("admin password --id: %d %s", code, errOut)
	}
	if got := strings.Join(f.stream[0], " "); !strings.HasSuffix(got, "exec -T core /app admin-password --id 4243") {
		t.Fatalf("compose args: %s", got)
	}
	for _, bad := range []string{"abc", "0", "-3", "12 34"} {
		errOut.Reset()
		if code := o.adminCmd([]string{"password", "--dir", dir, "--id", bad}); code != 2 || !strings.Contains(errOut.String(), "positive number") {
			t.Errorf("--id %q: %d %q", bad, code, errOut)
		}
	}
	if len(f.stream) != 1 {
		t.Fatalf("a refused id reached core: %v", f.stream)
	}
}

// ownersInstall is an install that names owners (and the running stack).
func ownersInstall(t *testing.T, ids string) (dir string, o *ops, out, errOut *bytes.Buffer, f *fakeDB) {
	t.Helper()
	dir = fullInstall(t, false)
	env, _ := readEnvFile(t, dir)
	env["BOBRES_ADMIN_TELEGRAM_ID"] = ids
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	f = &fakeDB{}
	o, out, errOut = dbOps("", f)
	return dir, o, out, errOut, f
}

func TestAdminOwnersList(t *testing.T) {
	dir, o, out, errOut, f := ownersInstall(t, "4242,4243, 4244")
	if code := o.adminCmd([]string{"owners", "list", "--dir", dir}); code != 0 {
		t.Fatalf("list: %d %s", code, errOut)
	}
	got := out.String()
	for _, want := range []string{"1. 4242   (main owner: takes no staff seat", "2. 4243   (takes a staff seat)", "3. 4244   (takes a staff seat)"} {
		if !strings.Contains(got, want) {
			t.Errorf("list lacks %q:\n%s", want, got)
		}
	}
	if len(f.steps) != 0 {
		t.Fatalf("listing touched the stack: %q", f.steps)
	}
	// Unset: it says how to name one.
	dir, o, out, _, _ = ownersInstall(t, "")
	if code := o.adminCmd([]string{"owners", "list", "--dir", dir}); code != 0 || !strings.Contains(out.String(), "No owner is named") {
		t.Fatalf("no owner: %d %s", code, out)
	}
}

func TestAdminOwnersAddAndRemove(t *testing.T) {
	dir, o, out, errOut, f := ownersInstall(t, "4242")
	f.ps = func() string { return healthyPS }
	if code := o.adminCmd([]string{"owners", "add", "4243", "--dir", dir}); code != 0 {
		t.Fatalf("add: %d %s", code, errOut)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_ADMIN_TELEGRAM_ID"] != "4242,4243" {
		t.Fatalf("env after add: %q", env["BOBRES_ADMIN_TELEGRAM_ID"])
	}
	// Only core and the bot read it, and they are the ones recreated.
	if strings.Join(f.steps, "|") != "up -d --no-deps core bot" {
		t.Fatalf("compose: %q", f.steps)
	}
	for _, want := range []string{"Telegram id 4243 is named as an owner", "bobres admin password --id 4243", "Only the main owner (4242) takes no staff seat"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("add output lacks %q:\n%s", want, out)
		}
	}

	// Adding one again changes nothing and is not an error.
	f.steps = nil
	out.Reset()
	if code := o.adminCmd([]string{"owners", "add", "4243", "--dir", dir}); code != 0 || len(f.steps) != 0 || !strings.Contains(out.String(), "an owner already") {
		t.Fatalf("add twice: %d %q %s", code, f.steps, out)
	}
	if code := o.adminCmd([]string{"owners", "add", "4244", "--dir", dir, "--no-restart"}); code != 0 || len(f.steps) != 0 {
		t.Fatalf("add --no-restart: %d %q", code, f.steps)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_ADMIN_TELEGRAM_ID"] != "4242,4243,4244" {
		t.Fatalf("env: %q", env["BOBRES_ADMIN_TELEGRAM_ID"])
	}

	// Removing one leaves the others in their order, and warns that the
	// account keeps its role until the dashboard demotes it.
	out.Reset()
	if code := o.adminCmd([]string{"owners", "remove", "4243", "--dir", dir, "--no-restart"}); code != 0 {
		t.Fatalf("remove: %d %s", code, errOut)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_ADMIN_TELEGRAM_ID"] != "4242,4244" {
		t.Fatalf("env after remove: %q", env["BOBRES_ADMIN_TELEGRAM_ID"])
	}
	if !strings.Contains(out.String(), "no longer named in the server settings") || !strings.Contains(out.String(), "demote or remove them in the dashboard") {
		t.Fatalf("remove warning:\n%s", out)
	}
	// Removing the main owner makes the next one the main owner.
	if code := o.adminCmd([]string{"owners", "remove", "4242", "--dir", dir, "--no-restart"}); code != 0 {
		t.Fatalf("remove the main owner: %d %s", code, errOut)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_ADMIN_TELEGRAM_ID"] != "4244" {
		t.Fatalf("env: %q", env["BOBRES_ADMIN_TELEGRAM_ID"])
	}
}

func TestAdminOwnersRefusals(t *testing.T) {
	dir, o, _, errOut, f := ownersInstall(t, "4242,4243")
	_, before := readEnvFile(t, dir)
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"owners", "add", "abc", "--dir", dir}, 2, "positive number"},
		{[]string{"owners", "add", "0", "--dir", dir}, 2, "positive number"},
		{[]string{"owners", "add", "-4", "--dir", dir}, 2, "flag provided but not defined"}, // a minus sign is a flag to Go: refused either way
		{[]string{"owners", "add", "4,5", "--dir", dir}, 2, "positive number"},
		{[]string{"owners", "remove", "999", "--dir", dir}, 1, "is not an owner named in the server settings"},
		{[]string{"owners", "add", "--dir", dir}, 2, "usage:"},
		{[]string{"owners", "add", "1", "2", "--dir", dir}, 2, "usage:"},
		{[]string{"owners", "list", "1", "--dir", dir}, 2, "usage:"},
		{[]string{"owners", "promote", "1", "--dir", dir}, 2, "usage:"},
		{[]string{"owners", "--dir", dir}, 2, "usage:"},
		{[]string{"owners", "list", "--dir", t.TempDir()}, 2, "no install found"},
	} {
		errOut.Reset()
		if code := o.adminCmd(c.args); code != c.code || !strings.Contains(errOut.String(), c.want) {
			t.Errorf("%v: %d %q, want %d %q", c.args[:min(len(c.args), 3)], code, errOut, c.code, c.want)
		}
	}
	if _, after := readEnvFile(t, dir); after != before || len(f.steps) != 0 {
		t.Fatalf("a refused command changed something: %q", f.steps)
	}

	// The last owner stays: a store needs one.
	solo, o, _, errOut, _ := ownersInstall(t, "4242")
	if code := o.adminCmd([]string{"owners", "remove", "4242", "--dir", solo}); code != 1 || !strings.Contains(errOut.String(), "the only owner") {
		t.Fatalf("remove the only owner: %d %s", code, errOut)
	}
	// At most 10 owners.
	full, o, _, errOut, _ := ownersInstall(t, "1,2,3,4,5,6,7,8,9,10")
	if code := o.adminCmd([]string{"owners", "add", "11", "--dir", full}); code != 1 || !strings.Contains(errOut.String(), "at most 10 owners") {
		t.Fatalf("an eleventh owner: %d %s", code, errOut)
	}
}
