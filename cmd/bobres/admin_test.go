package main

import (
	"context"
	"errors"
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
