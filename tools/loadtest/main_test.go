package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunRefusesWithoutTargetOrOffMachine(t *testing.T) {
	env := func(token string) func(string) string {
		return func(k string) string {
			if k == tokenEnv {
				return token
			}
			return ""
		}
	}
	for _, c := range []struct {
		name  string
		args  []string
		token string
		want  string
	}{
		{"no token", []string{"-addr", "127.0.0.1:9090", "-owner", "1"}, "", "usage:"},
		{"no owner", []string{"-addr", "127.0.0.1:9090"}, "tok", "usage:"},
		{"no address", []string{"-owner", "1"}, "tok", "usage:"},
		{"not on this machine", []string{"-addr", "10.1.2.3:9090", "-owner", "1"}, "tok", "is not on this machine"},
		{"bad address", []string{"-addr", "core", "-owner", "1"}, "tok", "invalid address"},
	} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), c.args, env(c.token), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), c.want) {
			t.Errorf("%s: exit %d, stderr %q, want 2 and %q", c.name, code, errOut.String(), c.want)
		}
		if out.Len() > 0 {
			t.Errorf("%s: wrote a report: %q", c.name, out.String())
		}
	}
}
