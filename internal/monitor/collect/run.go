// Package collect is the host collector: it reads the machine, Docker, the
// BOBRES systemd units and the journal, and (when kubectl or k3s is there) the
// cluster, and puts what it found into a snapshot.Snapshot. It runs as root,
// once every 30 seconds, from `bobres monitor snapshot`.
//
// Everything it reads is untrusted text it turns into a few allow-listed,
// size-bounded fields: it never copies an environment variable, a command
// line, a label (but a container's Compose project and service), a mount's
// host path, a token or a build step into the snapshot, and the journal's
// lines are scrubbed (Scrub) before they are kept. The sources are small
// interfaces, so the whole collector runs against recorded fixtures in tests.
package collect

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Runner runs a program and returns what it printed on stdout (stderr is
// dropped: it can hold anything).
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ErrTooLarge: a program printed more than the collector keeps.
var ErrTooLarge = errors.New("collect: output too large")

// toolDirs are where the collector looks for programs: a fixed list, never the
// caller's PATH. It runs as root, so a program found through a PATH someone
// else can write to would run with root's power.
var toolDirs = []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin", "/snap/bin"}

// LookTool finds a program in toolDirs (an absolute path).
func LookTool(name string) (string, error) {
	for _, d := range toolDirs {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// ExecRunner runs programs found by LookTool with a small, fixed
// environment (the kubeconfig and home directory kubectl needs, nothing
// else), and keeps at most limit bytes of stdout: more is ErrTooLarge.
func ExecRunner(limit int) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		path, err := LookTool(name)
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // a fixed program from toolDirs, arguments built here
		cmd.Env = []string{"PATH=" + joinDirs(), "LC_ALL=C", "LANG=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=", "HOME=" + homeDir()}
		if kc := os.Getenv("KUBECONFIG"); kc != "" {
			cmd.Env = append(cmd.Env, "KUBECONFIG="+kc)
		}
		out := &limited{limit: limit}
		cmd.Stdout = out
		cmd.WaitDelay = time.Second
		err = cmd.Run()
		if out.over {
			return nil, ErrTooLarge
		}
		return out.buf.Bytes(), err
	}
}

func joinDirs() string {
	var b bytes.Buffer
	for i, d := range toolDirs {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(d)
	}
	return b.String()
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/root"
}

// limited keeps the first limit bytes written and notes that there were more.
type limited struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.limit - l.buf.Len(); len(p) > room {
		l.buf.Write(p[:max(room, 0)])
		l.over = true
		return len(p), nil // keep draining the program; the answer is refused
	}
	return l.buf.Write(p)
}
