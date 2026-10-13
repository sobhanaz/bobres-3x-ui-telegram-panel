package tlsagent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeCaddy is Caddy's admin endpoint on a unix socket: it records every
// load with the HTTPS files as they were on disk at that moment, and
// answers what the test says.
type fakeCaddy struct {
	t      *testing.T
	sock   string
	tlsDir string

	mu    sync.Mutex
	loads []load
	// answer decides a load (nil: 200).
	answer func(l load) (int, string)
	// delay holds every answer this long.
	delay time.Duration
	srv   *http.Server
}

// load is one POST /load as Caddy saw it.
type load struct {
	header         http.Header
	body           string
	global, sites  string
	cert, key      string
	pairOnDisk     bool
	method, target string
}

// socketDir is a short directory for a unix socket (macOS limits a socket
// path to about 100 bytes, and test temp dirs can be longer).
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ta")
	if err != nil {
		t.Fatal(err)
	}
	if len(dir) > 80 {
		_ = os.RemoveAll(dir)
		if dir, err = os.MkdirTemp("/tmp", "ta"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newFakeCaddy(t *testing.T, tlsDir string) *fakeCaddy {
	t.Helper()
	c := &fakeCaddy{t: t, sock: filepath.Join(socketDir(t), "admin.sock"), tlsDir: tlsDir}
	c.start()
	t.Cleanup(c.stop)
	return c
}

func (c *fakeCaddy) start() {
	c.t.Helper()
	lis, err := net.Listen("unix", c.sock)
	if err != nil {
		c.t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(c.serve), ReadHeaderTimeout: time.Second}
	c.srv = srv
	go func() { _ = srv.Serve(lis) }()
}

// stop takes the endpoint away (Caddy stopped or restarting).
func (c *fakeCaddy) stop() {
	if c.srv != nil {
		_ = c.srv.Close()
		c.srv = nil
		_ = os.Remove(c.sock)
	}
}

func (c *fakeCaddy) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	read := func(parts ...string) string {
		b, _ := os.ReadFile(filepath.Join(append([]string{c.tlsDir}, parts...)...))
		return string(b)
	}
	l := load{header: r.Header.Clone(), body: string(body), global: read("global.caddy"), sites: read("sites.caddy"),
		cert: read("certs", "cert.pem"), key: read("certs", "key.pem"), method: r.Method, target: r.URL.Path}
	_, err := os.Stat(filepath.Join(c.tlsDir, "certs", "cert.pem"))
	l.pairOnDisk = err == nil
	c.mu.Lock()
	c.loads = append(c.loads, l)
	answer, delay := c.answer, c.delay
	c.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	status, msg := http.StatusOK, ""
	if answer != nil {
		status, msg = answer(l)
	}
	w.WriteHeader(status)
	if msg != "" {
		_, _ = io.WriteString(w, msg)
	}
}

func (c *fakeCaddy) setAnswer(f func(l load) (int, string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answer = f
}

func (c *fakeCaddy) all() []load {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]load(nil), c.loads...)
}

// refuse answers like Caddy 2.11.6+ when a config does not load.
func refuse(msg string) func(load) (int, string) {
	return func(load) (int, string) { return http.StatusBadRequest, `{"error":"` + msg + `"}` }
}

var errNoServer = errors.New("no server")

// waitClosed waits until a stopped endpoint refuses connections.
func waitClosed(t *testing.T, sock string) {
	t.Helper()
	for range 100 {
		conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", sock)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(errNoServer)
}
