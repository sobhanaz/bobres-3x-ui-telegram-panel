package testenv

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Option changes how Start builds the stack.
type Option func(*options)

type options struct {
	gates bool
}

// WithGates puts a Gate in front of Postgres, the provisioner and core, so a
// failure drill can switch each of them off and on (Stack.Gates).
func WithGates() Option { return func(o *options) { o.gates = true } }

// Gates are the switches a drill pulls. DB sits between every service and
// Postgres, Provisioner between core and the provisioner, and Core between
// the bot (Stack.Core and Stack.Feed) and core. Panel sits between the
// provisioner and the fake 3x-ui panel (Stack.Panel).
type Gates struct {
	DB, Provisioner, Core *Gate
	Panel                 *PanelProxy
}

// newGates returns nil unless opts ask for gates. The DB gate is in front of
// dsn from the start; the others are added once their server listens.
func newGates(t *testing.T, dsn string, opts []Option) *Gates {
	t.Helper()
	var o options
	for _, f := range opts {
		f(&o)
	}
	if !o.gates {
		return nil
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	network, addr := "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	if strings.HasPrefix(cfg.Host, "/") { // a Unix socket directory
		network, addr = "unix", cfg.Host+"/.s.PGSQL."+strconv.Itoa(int(cfg.Port))
	}
	return &Gates{DB: newGate(t, network, addr)}
}

// dsn is where the services connect to Postgres: direct without gates, else
// the same database through the DB gate.
func (g *Gates) dsn(t *testing.T, direct string) string {
	t.Helper()
	if g == nil {
		return direct
	}
	u, err := url.Parse(direct)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Del("host")
	q.Del("port")
	u.RawQuery, u.Host = q.Encode(), g.DB.Addr()
	return u.String()
}

// provisioner is the address core dials for the provisioner at addr.
func (g *Gates) provisioner(t *testing.T, addr string) string {
	t.Helper()
	if g == nil {
		return addr
	}
	g.Provisioner = newGate(t, "tcp", addr)
	return g.Provisioner.Addr()
}

// core is the address the bot dials for core at addr.
func (g *Gates) core(t *testing.T, addr string) string {
	t.Helper()
	if g == nil {
		return addr
	}
	g.Core = newGate(t, "tcp", addr)
	return g.Core.Addr()
}

// panel is the URL the provisioner uses for the fake panel at target.
func (g *Gates) panel(t *testing.T, target string) string {
	t.Helper()
	if g == nil {
		return target
	}
	g.Panel = newPanelProxy(t, target)
	return g.Panel.url
}

// PanelProxy is an HTTP proxy between the provisioner and the fake panel.
// It passes every call through, except one HoldReply picks.
type PanelProxy struct {
	url   string
	proxy *httputil.ReverseProxy
	ended chan struct{} // closed when the test ends

	mu   sync.Mutex
	path string        // the call whose reply is held next ("" = none)
	held chan struct{} // closed once the panel has run that call
}

func newPanelProxy(t *testing.T, target string) *PanelProxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	p := &PanelProxy{
		proxy: &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(u) }},
		ended: make(chan struct{}),
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(p.ended) }) // first: Close waits for a held call
	p.url = srv.URL
	return p
}

// HoldReply loses the reply to the next call to path (e.g.
// "/panel/api/clients/add") on the wire: the panel runs the call, the
// returned channel is closed, and the caller never gets an answer. Its
// connection is dropped once it gives up (the provisioner went down, or its
// timeout passed) or the test ends.
func (p *PanelProxy) HoldReply(path string) <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.path, p.held = path, make(chan struct{})
	return p.held
}

func (p *PanelProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	var held chan struct{}
	if p.path != "" && r.URL.Path == p.path {
		held, p.path = p.held, ""
	}
	p.mu.Unlock()
	if held == nil {
		p.proxy.ServeHTTP(w, r)
		return
	}
	p.proxy.ServeHTTP(httptest.NewRecorder(), r) // the panel's answer goes nowhere
	close(held)
	select {
	case <-r.Context().Done():
	case <-p.ended:
	}
	panic(http.ErrAbortHandler) // drops the connection with no reply
}

// Gate is a TCP proxy in front of a service. Down cuts every open connection
// and refuses new ones (each is closed at once), as a crashed or unreachable
// service would; Up lets connections through again on the same address, as
// the service coming back. Clients reconnect by themselves.
type Gate struct {
	lis     net.Listener
	network string
	target  string

	mu    sync.Mutex
	down  bool
	conns map[net.Conn]struct{}
}

func newGate(t *testing.T, network, target string) *Gate {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := &Gate{lis: lis, network: network, target: target, conns: map[net.Conn]struct{}{}}
	go g.serve()
	t.Cleanup(g.close)
	return g
}

// Addr is where clients connect instead of the service.
func (g *Gate) Addr() string { return g.lis.Addr().String() }

// Down cuts every connection through the gate and refuses new ones until Up.
func (g *Gate) Down() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.down = true
	g.cut()
}

// Up lets connections through again.
func (g *Gate) Up() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.down = false
}

func (g *Gate) close() {
	_ = g.lis.Close()
	g.Down()
}

// cut closes every connection; g.mu is held.
func (g *Gate) cut() {
	for c := range g.conns {
		_ = c.Close()
	}
	clear(g.conns)
}

func (g *Gate) serve() {
	for {
		c, err := g.lis.Accept()
		if err != nil {
			return // closed at the end of the test
		}
		go g.pipe(c)
	}
}

// track remembers c so Down can cut it; false (c not kept) while down.
func (g *Gate) track(c net.Conn) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.down {
		return false
	}
	g.conns[c] = struct{}{}
	return true
}

func (g *Gate) forget(cs ...net.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range cs {
		delete(g.conns, c)
	}
}

// pipe joins one client connection to the service until either side ends
// or the gate goes down.
func (g *Gate) pipe(c net.Conn) {
	if !g.track(c) {
		_ = c.Close()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var d net.Dialer
	up, err := d.DialContext(ctx, g.network, g.target)
	cancel()
	if err != nil || !g.track(up) {
		_ = c.Close()
		if up != nil {
			_ = up.Close()
		}
		g.forget(c)
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
	_ = c.Close()
	_ = up.Close()
	g.forget(c, up)
}
