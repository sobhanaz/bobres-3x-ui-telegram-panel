// Package tlsagent is the tls-agent: the small service that applies core's
// HTTPS requests to Caddy, so core itself never writes Caddy's files or
// reaches Caddy's admin socket (core keeps no internet access and no
// control of the internet-facing gateway).
//
// It runs from core's image (`/app tls-agent`) with no network, as
// 65532:65532, and is the only writer of Caddy's TLS files. Every 2
// seconds it reads core's request (tlsconf.Request); when the request's
// generation is new, it checks the request again with tlsconf (it trusts
// nothing core sends: the names must also be this install's own, from its
// environment), renders the files, writes them atomically
// (certificates before the snippets that name them), and makes Caddy load
// its main Caddyfile again through the admin socket (a forced POST /load).
// It answers in its result file (tlsconf.Result):
//   - Caddy loaded the files: ok; they become the last known good files.
//   - Caddy refused them: the last known good files go back, Caddy loads
//     them again, and the answer carries Caddy's words.
//   - Caddy did not answer (stopped, restarting, starting): the new files
//     stay (Caddy reads them when it starts) and the agent tries the load
//     again at every round.
//
// The last known good files live in memory only: no copy of a key is kept
// on disk besides the one Caddy reads.
package tlsagent

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// Defaults: the mounts the compose file gives the agent.
const (
	DefaultRequest     = tlsconf.AgentRequestDir + "/" + tlsconf.RequestFile
	DefaultResult      = tlsconf.AgentResultDir + "/" + tlsconf.ResultFile
	DefaultTLSDir      = tlsconf.Dir
	DefaultCaddyfile   = "/etc/bobres/caddy/Caddyfile"
	DefaultSourceFile  = "/etc/caddy/Caddyfile"
	DefaultAdminSocket = "/run/caddy/admin.sock"
	DefaultHeartbeat   = "/tmp/tls-agent.alive"
	DefaultEvery       = 2 * time.Second
	// DefaultLoadTimeout keeps an answer within the 20 seconds core waits
	// for one (a round's wait plus the load; a load takes well under a
	// second).
	DefaultLoadTimeout = 15 * time.Second
)

// Config wires the agent. Empty fields take the defaults above.
type Config struct {
	// RequestPath is core's request; ResultPath the agent's answer.
	RequestPath string
	ResultPath  string
	// TLSDir is where the HTTPS files are written (Caddy reads the same
	// directory at tlsconf.Dir).
	TLSDir string
	// Caddyfile is Caddy's main Caddyfile as the agent reads it; SourceFile
	// is the same file as Caddy names it. The load says so, so a later
	// SIGUSR1 to Caddy still reloads that file.
	Caddyfile  string
	SourceFile string
	// AdminSocket is Caddy's admin endpoint.
	AdminSocket string
	// Heartbeat is touched every round, for the container's health check
	// ("" = none).
	Heartbeat   string
	Every       time.Duration
	LoadTimeout time.Duration
	// Roots are what a manual pair's chain is checked against (nil: the
	// system's); it only changes the warnings.
	Roots *x509.CertPool
	Now   func() time.Time
	Log   *slog.Logger

	// Domain and ZarinpalHost are the install's BOBRES_DOMAIN and
	// BOBRES_ZARINPAL_HOST, read from the agent's own environment: a request
	// that names any other domain or Zarinpal host is refused (invalid), so
	// a compromised core cannot have Caddy serve, or fetch certificates for,
	// names the install does not own. With no Domain, every request is
	// refused.
	Domain       string
	ZarinpalHost string
	// Unpinned turns that check off. Only tests set it; the agent command
	// never does.
	Unpinned bool
}

// Agent applies core's requests.
type Agent struct {
	cfg   Config
	admin *admin
	// last is the generation answered for good (applied, refused or
	// invalid): it is not applied again.
	last int64
	// pending is a generation whose files are written but Caddy did not
	// answer: the load is tried again.
	pending *files
	// good is what Caddy last loaded, or what was on disk when the agent
	// started (nil: nothing yet).
	good *files
	// unreadable is the last error reading the request, logged once.
	unreadable string
}

// files are the HTTPS files of one state.
type files struct {
	gen          int64
	global       []byte
	sites        []byte
	cert, key    []byte // manual mode's pair (nil otherwise)
	manual       bool
	mode         tlsconf.Mode
	domain, ip   string
	zarinpalHost string
}

// New builds the agent.
func New(cfg Config) *Agent {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&cfg.RequestPath, DefaultRequest)
	def(&cfg.ResultPath, DefaultResult)
	def(&cfg.TLSDir, DefaultTLSDir)
	def(&cfg.Caddyfile, DefaultCaddyfile)
	def(&cfg.SourceFile, DefaultSourceFile)
	def(&cfg.AdminSocket, DefaultAdminSocket)
	if cfg.Every <= 0 {
		cfg.Every = DefaultEvery
	}
	if cfg.LoadTimeout <= 0 {
		cfg.LoadTimeout = DefaultLoadTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Agent{cfg: cfg, admin: newAdmin(cfg.AdminSocket, cfg.LoadTimeout)}
}

// Run applies requests until ctx ends: at start, then every round.
func (a *Agent) Run(ctx context.Context) error {
	a.Start()
	t := time.NewTicker(a.cfg.Every)
	defer t.Stop()
	for {
		a.Step(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Start reads the files on disk as the last known good ones (what Caddy
// loaded at its last start, or loads at its next) and removes temp files
// a crash left behind.
func (a *Agent) Start() {
	for _, dir := range []string{a.cfg.TLSDir, filepath.Join(a.cfg.TLSDir, tlsconf.CertsDir)} {
		if tmps, err := filepath.Glob(filepath.Join(dir, ".*.tmp")); err == nil {
			for _, t := range tmps {
				_ = os.Remove(t)
			}
		}
	}
	g, errG := os.ReadFile(a.path(tlsconf.GlobalFile))
	s, errS := os.ReadFile(a.path(tlsconf.SitesFile))
	if errG != nil || errS != nil {
		a.cfg.Log.Info("no HTTPS files on disk yet", "dir", a.cfg.TLSDir)
		return
	}
	f := &files{global: g, sites: s}
	c, errC := os.ReadFile(a.certPath())
	k, errK := os.ReadFile(a.keyPath())
	if errC == nil && errK == nil {
		f.cert, f.key, f.manual = c, k, true
	}
	a.good = f
}

// Step does one round: it applies a new request, or tries the load again
// for one Caddy did not answer.
func (a *Agent) Step(ctx context.Context) {
	a.beat()
	req, err := tlsconf.ReadRequest(a.cfg.RequestPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return // core has asked nothing yet: the defaults on disk stand
	case err != nil:
		if msg := err.Error(); msg != a.unreadable {
			a.cfg.Log.Warn("the HTTPS request cannot be read", "err", err)
			a.unreadable = msg
		}
		return
	}
	a.unreadable = ""
	switch {
	case a.pending != nil && req.Generation == a.pending.gen:
		a.retry(ctx)
	case req.Generation != a.last:
		a.apply(ctx, req)
	}
}

// apply handles a new request.
func (a *Agent) apply(ctx context.Context, req *tlsconf.Request) {
	log := a.cfg.Log.With("request", *req)
	a.pending = nil
	next, err := a.render(req)
	if err != nil {
		log.Warn("HTTPS request refused", "err", err)
		a.answer(req.Generation, tlsconf.ResultInvalid, err.Error())
		return
	}
	main, err := os.ReadFile(a.cfg.Caddyfile)
	if err != nil {
		log.Error("the main Caddyfile cannot be read", "path", a.cfg.Caddyfile, "err", err)
		a.answer(req.Generation, tlsconf.ResultAgentError, "the tls-agent cannot read Caddy's main Caddyfile ("+a.cfg.Caddyfile+")")
		return
	}
	if err := a.write(next); err != nil {
		log.Error("HTTPS files not written", "err", err)
		msg := "the HTTPS files could not be written: " + err.Error()
		if a.good != nil {
			if rerr := a.write(a.good); rerr != nil {
				msg += "; the previous files could not be put back either: " + rerr.Error()
			}
		}
		a.answer(req.Generation, tlsconf.ResultWriteFailed, msg)
		return
	}
	a.load(ctx, log, next, main)
}

// retry tries the load again for files Caddy did not answer for.
func (a *Agent) retry(ctx context.Context) {
	main, err := os.ReadFile(a.cfg.Caddyfile)
	if err != nil {
		return
	}
	next := a.pending
	a.load(ctx, a.cfg.Log.With("generation", next.gen, "mode", next.mode), next, main)
}

// load makes Caddy load next (already on disk) and answers.
func (a *Agent) load(ctx context.Context, log *slog.Logger, next *files, main []byte) {
	err := a.admin.load(ctx, main, a.cfg.SourceFile)
	a.beat() // a load can take up to LoadTimeout, twice when it is refused
	var refused *RefusedError
	switch {
	case err == nil:
		if !next.manual {
			a.removePair()
		}
		a.good, a.pending = next, nil
		log.Info("HTTPS files loaded by Caddy", "mode", next.mode, "domain", next.domain, "ip", next.ip)
		a.answer(next.gen, "", "")
	case errors.As(err, &refused):
		a.pending = nil
		log.Warn("Caddy refused the HTTPS files", "status", refused.Status, "err", refused.Message)
		msg := "Caddy refused the change: " + refused.Message
		switch {
		case a.good == nil:
			msg += " (there were no previous files to put back)"
		case bytes.Equal(a.good.sites, next.sites) && bytes.Equal(a.good.global, next.global) && bytes.Equal(a.good.cert, next.cert):
			// the same files as before: nothing to put back
		default:
			if werr := a.write(a.good); werr != nil {
				msg += "; the previous files could not be put back: " + werr.Error()
			} else {
				lerr := a.admin.load(ctx, main, a.cfg.SourceFile)
				a.beat()
				switch {
				case lerr != nil:
					log.Error("Caddy did not load the previous HTTPS files again", "err", lerr)
					msg += "; Caddy did not load the previous files again either: " + lerr.Error()
				case !a.good.manual:
					a.removePair()
				}
			}
		}
		a.answer(next.gen, tlsconf.ResultRefused, msg)
	default:
		// Caddy is stopped, restarting or starting: it reads these files
		// when it starts. A pair no snippet names any more goes now.
		if !next.manual {
			a.removePair()
		}
		if a.pending == nil {
			log.Warn("Caddy did not answer; the HTTPS files are written and Caddy reads them when it starts", "err", err)
			a.answer(next.gen, tlsconf.ResultUnreachable, "Caddy did not answer ("+err.Error()+"); it reads the new files when it starts")
		}
		a.pending = next
	}
	a.last = next.gen
}

// render checks the request and renders its files.
func (a *Agent) render(req *tlsconf.Request) (*files, error) {
	st, err := req.State().Check()
	if err != nil {
		return nil, err
	}
	if err := a.checkNames(st); err != nil {
		return nil, err
	}
	out, err := tlsconf.Render(st)
	if err != nil {
		return nil, err
	}
	f := &files{gen: req.Generation, global: []byte(out.Global), sites: []byte(out.Sites), manual: out.Manual,
		mode: st.Mode, domain: st.Domain, ip: st.IP, zarinpalHost: st.ZarinpalHost}
	if out.Manual {
		p, err := tlsconf.CheckPair([]byte(req.CertPEM), []byte(req.KeyPEM),
			tlsconf.PairOptions{Name: st.Domain, ZarinpalHost: st.ZarinpalHost, Now: a.cfg.Now(), Roots: a.cfg.Roots})
		if err != nil {
			return nil, err
		}
		f.cert, f.key = p.CertPEM, p.KeyPEM
	}
	return f, nil
}

// checkNames refuses a request for another domain or Zarinpal host than
// the install's own (Config.Domain, Config.ZarinpalHost), as both are
// canonical. st is the checked request.
func (a *Agent) checkNames(st tlsconf.State) error {
	if a.cfg.Unpinned {
		return nil
	}
	own, _, err := tlsconf.Install(a.cfg.Domain, a.cfg.ZarinpalHost)
	if err != nil {
		return fmt.Errorf("the tls-agent has no usable BOBRES_DOMAIN of its own to check the request against: %w", err)
	}
	if st.Domain != own.Domain {
		return fmt.Errorf("the request names the domain %s, not this install's domain (BOBRES_DOMAIN)", st.Domain)
	}
	if st.ZarinpalHost != own.ZarinpalHost {
		return fmt.Errorf("the request names the Zarinpal domain %q, not this install's (BOBRES_ZARINPAL_HOST)", st.ZarinpalHost)
	}
	return nil
}

// write puts f on disk: the pair first, then the global options, then the
// sites (each atomically, mode 0640).
func (a *Agent) write(f *files) error {
	if f.manual {
		if err := os.MkdirAll(filepath.Join(a.cfg.TLSDir, tlsconf.CertsDir), 0o750); err != nil {
			return err
		}
		if err := tlsconf.WriteFile(a.certPath(), f.cert, tlsconf.FileMode); err != nil {
			return err
		}
		if err := tlsconf.WriteFile(a.keyPath(), f.key, tlsconf.FileMode); err != nil {
			return err
		}
	}
	if err := tlsconf.WriteFile(a.path(tlsconf.GlobalFile), f.global, tlsconf.FileMode); err != nil {
		return err
	}
	return tlsconf.WriteFile(a.path(tlsconf.SitesFile), f.sites, tlsconf.FileMode)
}

// removePair deletes manual mode's pair once no snippet names it.
func (a *Agent) removePair() {
	for _, p := range []string{a.certPath(), a.keyPath()} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			a.cfg.Log.Warn("an old certificate file could not be removed", "path", p, "err", err)
		}
	}
}

// answer writes the result for gen (code "" means ok).
func (a *Agent) answer(gen int64, code, msg string) {
	a.last = gen
	r := tlsconf.Result{Generation: gen, OK: code == "", Code: code, Error: clip(msg, 2000), AppliedAt: a.cfg.Now().Unix()}
	if err := tlsconf.WriteResult(a.cfg.ResultPath, r); err != nil {
		a.cfg.Log.Error("the HTTPS result could not be written", "path", a.cfg.ResultPath, "err", err)
	}
}

// beat touches the heartbeat file.
func (a *Agent) beat() {
	if a.cfg.Heartbeat == "" {
		return
	}
	if err := os.WriteFile(a.cfg.Heartbeat, []byte(strconv.FormatInt(a.cfg.Now().Unix(), 10)), 0o600); err != nil {
		a.cfg.Log.Debug("heartbeat", "err", err)
	}
}

func (a *Agent) path(name string) string { return filepath.Join(a.cfg.TLSDir, name) }
func (a *Agent) certPath() string {
	return filepath.Join(a.cfg.TLSDir, tlsconf.CertsDir, tlsconf.CertFile)
}
func (a *Agent) keyPath() string {
	return filepath.Join(a.cfg.TLSDir, tlsconf.CertsDir, tlsconf.KeyFile)
}

// HealthyAge is how fresh the heartbeat must be for Healthcheck.
const HealthyAge = 30 * time.Second

// Healthcheck is `tls-agent --healthcheck`: 0 when the agent's loop
// touched its heartbeat file within HealthyAge, else 1.
func Healthcheck(path string, now time.Time) int {
	fi, err := os.Stat(path)
	if err != nil || now.Sub(fi.ModTime()) > HealthyAge {
		return 1
	}
	return 0
}

// clip keeps the first n bytes of s on a rune boundary, on one line.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
