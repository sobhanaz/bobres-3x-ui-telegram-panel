package backupsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// PgDump dumps a database with pg_dump in its custom format (-Fc: compressed,
// and what pg_restore reads).
type PgDump struct {
	// Path is the pg_dump program ("": pg_dump on PATH).
	Path string
	// URL is the database, postgres://user:password@host:port/db?params.
	URL string
}

// Dump runs pg_dump with its output going to w. The password goes to pg_dump
// in PGPASSWORD, never on its command line (which other users of the host
// can read), and pg_dump gets only the environment it needs.
func (p PgDump) Dump(ctx context.Context, w io.Writer) error {
	u, err := url.Parse(p.URL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return errors.New("pg_dump: the database URL is not a postgres:// URL")
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "PGAPPNAME=bobres-backup", "PGCONNECT_TIMEOUT=30"}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
		}
		u.User = url.User(u.User.Username())
	}
	path := p.Path
	if path == "" {
		path = "pg_dump"
	}
	errOut := &tail{max: 4 << 10}
	cmd := exec.CommandContext(ctx, path, "--format=custom", "--no-password", "--dbname="+u.String()) //nolint:gosec // fixed program, arguments built here
	cmd.Env, cmd.Stdout, cmd.Stderr = env, w, errOut
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return fmt.Errorf("pg_dump: %w: %s", err, msg)
		}
		return fmt.Errorf("pg_dump: %w", err)
	}
	return nil
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
