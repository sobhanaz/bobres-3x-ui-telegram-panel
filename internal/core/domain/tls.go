package domain

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

// HTTPS (P8). The setting in core.tls_config holds only what Caddy
// accepted. Core never writes Caddy's files: a change is a proposal that
// core checks (tlsconf), writes as a request for the tls-agent (the next
// generation), and commits with its audit entry only once the agent says
// Caddy loaded the files. Any other answer, or none within 20 seconds,
// writes the committed setting back as a new request and returns why. The
// server and `/app ssl` take the same advisory lock across request, answer
// and commit, so their proposals never interleave.
//
// At start core asks for the committed setting again: the domain or the
// Zarinpal host may have changed since. A saved setting that cannot be used
// any more (a key this master key cannot unseal, a certificate that no
// longer covers the domain or has expired, an IP address that clashes with
// an IP domain) is replaced by auto in the request, the row stays, and the
// watchdog raises tls_problem with the reason. Nothing here stops core from
// starting. Every minute after that core checks that the request on the
// volume is still the saved setting (a pair that expired meanwhile, a change
// interrupted before its rollback) and asks again when it is not.
//
// A change whose commit fails after the agent took it is rolled back like
// one the agent refused: the committed setting is asked for again, so Caddy
// never keeps serving what the store does not hold.

// TLSWait is how long a change waits for the agent's answer: the agent
// reads requests every 2 seconds and gives Caddy 15 to load, and the
// dashboard's answer must fit in core's 30-second write timeout.
const TLSWait = 20 * time.Second

// TLS timing.
const (
	tlsPoll = 250 * time.Millisecond
	// tlsFailAfter: an address that has served no valid certificate this
	// long is announced (issuance takes minutes).
	tlsFailAfter = 30 * time.Minute
	// tlsAgentQuiet: a request the agent has not answered this long means
	// the agent is not running.
	tlsAgentQuiet = 5 * time.Minute
	// tlsStatusFresh: a live status this recent proves the IP address for
	// links without dialing again.
	tlsStatusFresh = 3 * time.Minute
	// tlsDetectedFresh: an IP address the bot detected this recently counts
	// for a change.
	tlsDetectedFresh = 10 * time.Minute
	// tlsEgressFresh: the bot's detection of the server's address counts as
	// proof for the links this long. Watch asks again every minute while in
	// ip mode, so it is never older than a few minutes unless the bot is
	// down.
	tlsEgressFresh = 5 * time.Minute
	// tlsEgressWait bounds one detection made for the links or the watchdog.
	tlsEgressWait = 5 * time.Second
	// tlsBusyWait: how long a change waits for another one to finish (in this
	// process or another) before it answers busy. With the agent's wait it
	// keeps a dashboard change within core's 30-second write timeout.
	tlsBusyWait = 3 * time.Second
	// tlsReconcileEvery: how often Run checks the request against the saved
	// setting.
	tlsReconcileEvery = time.Minute
	// tlsCaddyDownAfter: Caddy answering on no address is announced this soon
	// (a restart during an update is over well before).
	tlsCaddyDownAfter = 3 * time.Minute
)

// TLSExchange is core's side of the tls-agent's volumes (tlsconf.Exchange).
type TLSExchange interface {
	WriteRequest(tlsconf.Request) error
	ReadRequest() (*tlsconf.Request, error)
	ReadResult() (*tlsconf.Result, error)
}

// TLSProber reads what Caddy serves for an address (tlsprobe.Probe at
// caddy:443 over the edge network).
type TLSProber func(ctx context.Context, address string) tlsprobe.Result

// TLSConfig wires the HTTPS manager.
type TLSConfig struct {
	// Domain is BOBRES_DOMAIN; ZarinpalHost is BOBRES_ZARINPAL_HOST.
	Domain       string
	ZarinpalHost string
	// PublicURLEnv is BOBRES_PUBLIC_URL (checked): when set, every
	// dashboard link starts with it.
	PublicURLEnv string
	// Secrets seals manual mode's key (core's master key); nil refuses
	// manual mode, so the key is never stored in clear.
	Secrets *bcrypto.Envelope
	// Exchange reaches the tls-agent (nil: no agent volumes; changes are
	// refused and nothing is written).
	Exchange TLSExchange
	// Probe reads what Caddy serves (nil: no live status).
	Probe TLSProber
	// DetectIP asks the bot for the server's public IP address
	// (botfiles.Client.PublicIP; nil without a bot). In ip mode the links
	// move to the IP address only while this detection, no older than 5
	// minutes, equals the configured address: a trusted certificate for it
	// does not say the address still reaches this server (Caddy serves the
	// short-lived certificate for days after the address changed hands).
	DetectIP func(ctx context.Context) (string, error)
	// Wait and Poll: how long a change waits for the agent and how often it
	// reads the answer (TLSWait and 250 ms when 0). BusyWait is how long it
	// waits for another change to finish first (3 seconds when 0).
	Wait, Poll, BusyWait time.Duration
	// Reconcile is how often Run checks the request against the saved
	// setting (a minute when 0).
	Reconcile time.Duration
	// Roots are what a pair's chain is checked against (nil: the system's).
	Roots *x509.CertPool
	Now   func() time.Time
	Log   *slog.Logger
}

// TLS is the HTTPS manager.
type TLS struct {
	s   *Service
	cfg TLSConfig
	// mu: one proposal at a time in this process (the advisory lock covers
	// the other processes).
	mu sync.Mutex

	// What the server keeps between calls.
	state      sync.Mutex
	started    bool   // the start request was written (or nothing had to be)
	startErr   string // why it could not be
	lastStatus []TLSAddress
	lastAt     time.Time
	detected   string
	detectedAt time.Time
	// egress is the address the bot last detected for the links, apart from
	// detected (the dashboard's 10-minute cache for a change).
	egress   string
	egressAt time.Time
}

// NewTLS builds the HTTPS manager.
func (s *Service) NewTLS(cfg TLSConfig) *TLS {
	if cfg.Wait <= 0 {
		cfg.Wait = TLSWait
	}
	if cfg.Poll <= 0 {
		cfg.Poll = tlsPoll
	}
	if cfg.BusyWait <= 0 {
		cfg.BusyWait = tlsBusyWait
	}
	if cfg.Reconcile <= 0 {
		cfg.Reconcile = tlsReconcileEvery
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = s.cfg.Log
	}
	cfg.Domain = strings.TrimSpace(cfg.Domain)
	cfg.ZarinpalHost = strings.TrimSpace(cfg.ZarinpalHost)
	return &TLS{s: s, cfg: cfg}
}

// TLSProblem is why the saved setting is not what core asks for: a code
// the dashboard translates and the same in English.
type TLSProblem struct {
	Code    string
	Message string
}

// Codes of a saved setting that is not in use (besides tlsconf's own).
const (
	TLSProblemNoMasterKey   = "no_master_key"
	TLSProblemKeyUnreadable = "key_unreadable"
	TLSProblemZarinpalHost  = "zarinpal_host_left_out"
)

// tlsTarget is what core asks the agent for.
type tlsTarget struct {
	state tlsconf.State // checked
	pair  *tlsconf.Pair // manual mode's
	// problem says why this is not the saved setting (nil: it is).
	problem *TLSProblem
}

// base is auto mode for this install's domain and Zarinpal host. A
// Zarinpal host that is not a host name is left out (problem says so); a
// domain that cannot be served is an error.
func (t *TLS) base() (tlsconf.State, *TLSProblem, error) {
	st, leftOut, err := tlsconf.Install(t.cfg.Domain, t.cfg.ZarinpalHost)
	if leftOut {
		return st, &TLSProblem{Code: TLSProblemZarinpalHost,
			Message: "BOBRES_ZARINPAL_HOST is not a host name, so the Zarinpal domain is not served"}, nil
	}
	return st, nil, err
}

// target is what core asks for given the committed row: the row itself, or
// auto when it cannot be used any more.
func (t *TLS) target(row *store.TLSRow) (*tlsTarget, error) {
	base, zp, err := t.base()
	if err != nil {
		return nil, err
	}
	fallback := func(code, msg string) (*tlsTarget, error) {
		return &tlsTarget{state: base, problem: &TLSProblem{Code: code, Message: msg}}, nil
	}
	switch tlsconf.Mode(row.Mode) {
	case tlsconf.ModeIP:
		st := base
		st.Mode, st.IP = tlsconf.ModeIP, row.IP
		checked, err := st.Check()
		if err != nil {
			return fallback(problemCode(err), "the saved server IP address cannot be used: "+err.Error())
		}
		return &tlsTarget{state: checked, problem: zp}, nil
	case tlsconf.ModeManual:
		if t.cfg.Secrets == nil {
			return fallback(TLSProblemNoMasterKey, "your certificate is not in use: core has no master key to unseal its private key")
		}
		key, err := t.cfg.Secrets.Decrypt(row.KeySealed)
		if err != nil {
			return fallback(TLSProblemKeyUnreadable, "your certificate is not in use: its private key cannot be unsealed with this master key (upload the certificate and key again)")
		}
		pair, err := tlsconf.CheckPair([]byte(row.CertPEM), key, t.pairOptions(base))
		if err != nil {
			return fallback(problemCode(err), "your certificate is not in use: "+err.Error())
		}
		st := base
		st.Mode, st.HasCert = tlsconf.ModeManual, true
		return &tlsTarget{state: st, pair: pair, problem: zp}, nil
	case tlsconf.ModeAuto:
		return &tlsTarget{state: base, problem: zp}, nil
	}
	return fallback(tlsconf.CodeBadMode, "the saved HTTPS mode "+row.Mode+" is unknown")
}

func (t *TLS) pairOptions(st tlsconf.State) tlsconf.PairOptions {
	return tlsconf.PairOptions{Name: st.Domain, ZarinpalHost: st.ZarinpalHost, Now: t.cfg.Now(), Roots: t.cfg.Roots}
}

// problemCode is a tlsconf Problem's code ("invalid" for anything else).
func problemCode(err error) string {
	var p *tlsconf.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return "invalid"
}

// nextGeneration is a generation newer than the request's and the answer's.
func (t *TLS) nextGeneration() int64 {
	var n int64
	if r, err := t.cfg.Exchange.ReadRequest(); err == nil {
		n = r.Generation
	}
	if r, err := t.cfg.Exchange.ReadResult(); err == nil && r.Generation > n {
		n = r.Generation
	}
	return n + 1
}

// requestFor is the request that asks for tg as generation gen.
func requestFor(tg *tlsTarget, gen int64) tlsconf.Request {
	req := tlsconf.Request{Generation: gen, Mode: tg.state.Mode, IP: tg.state.IP, Domain: tg.state.Domain,
		ZarinpalHost: tg.state.ZarinpalHost}
	if tg.pair != nil {
		req.CertPEM, req.KeyPEM = string(tg.pair.CertPEM), string(tg.pair.KeyPEM)
	}
	return req
}

// write asks the agent for tg as a new request; it returns its generation.
func (t *TLS) write(tg *tlsTarget) (int64, error) {
	req := requestFor(tg, t.nextGeneration())
	if err := t.cfg.Exchange.WriteRequest(req); err != nil {
		return 0, err
	}
	return req.Generation, nil
}

var errTLSBusy = errors.New("another HTTPS change is being applied")

// Start asks the agent for the committed setting (or its fallback). It
// never takes long (the lock is only tried) and never fails: what goes
// wrong is logged, and the status and the watchdog show it. false: the
// lock was busy and Run tries again.
func (t *TLS) Start(ctx context.Context) bool {
	if t.cfg.Exchange == nil || t.cfg.Domain == "" {
		t.setStarted("")
		return true
	}
	var tg *tlsTarget
	err := t.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		ok, err := t.s.st.TryLockTLS(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return errTLSBusy
		}
		row, err := t.s.st.GetTLS(ctx, tx)
		if err != nil {
			return err
		}
		if tg, err = t.target(row); err != nil {
			return err
		}
		_, err = t.write(tg)
		return err
	})
	switch {
	case errors.Is(err, errTLSBusy):
		return false
	case err != nil:
		t.cfg.Log.Error("the HTTPS setting could not be handed to the tls-agent", "err", err)
		t.setStarted("the HTTPS setting could not be handed to the tls-agent: " + err.Error())
		return true
	}
	if tg.problem != nil {
		t.cfg.Log.Warn("the saved HTTPS setting is not in use", "code", tg.problem.Code, "reason", tg.problem.Message,
			"mode", tg.state.Mode)
	}
	t.cfg.Log.Info("HTTPS setting handed to the tls-agent", "mode", tg.state.Mode, "domain", tg.state.Domain, "ip", tg.state.IP)
	t.setStarted("")
	return true
}

func (t *TLS) setStarted(errText string) {
	t.state.Lock()
	defer t.state.Unlock()
	t.started, t.startErr = true, errText
}

// Run tries the start request again until the lock was free (another
// process was applying a change when core started), then keeps the request
// on the volume in step with the saved setting until ctx ends (reconcile).
func (t *TLS) Run(ctx context.Context) error {
	if t.cfg.Exchange == nil || t.cfg.Domain == "" {
		return nil // nothing is asked of any agent
	}
	retry := time.NewTicker(5 * time.Second)
	defer retry.Stop()
	for {
		t.state.Lock()
		done := t.started
		t.state.Unlock()
		if done || t.Start(ctx) {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-retry.C:
		}
	}
	tick := time.NewTicker(t.cfg.Reconcile)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			t.reconcile(ctx)
		}
	}
}

// reconcile asks for the saved setting again when the request on the volume
// is not it: a manual pair that expired meanwhile (the view says "not in
// use", so Caddy must stop serving it), or a change that was interrupted
// before its rollback (a `bobres ssl` that was killed while it waited). It
// never waits: while a change is being applied here or in another process it
// leaves the request alone and looks again at the next tick.
func (t *TLS) reconcile(ctx context.Context) {
	if !t.mu.TryLock() {
		return
	}
	defer t.mu.Unlock()
	var tg *tlsTarget
	err := t.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		ok, err := t.s.st.TryLockTLS(ctx, tx)
		if err != nil || !ok {
			return err
		}
		row, err := t.s.st.GetTLS(ctx, tx)
		if err != nil {
			return err
		}
		want, err := t.target(row)
		if err != nil {
			return nil // a domain that cannot be served: nothing to ask for (the view says so)
		}
		if cur, err := t.cfg.Exchange.ReadRequest(); err == nil && sameAsked(*cur, requestFor(want, 0)) {
			return nil
		}
		if _, err := t.write(want); err != nil {
			return err
		}
		tg = want
		return nil
	})
	switch {
	case err != nil:
		if ctx.Err() == nil {
			t.cfg.Log.Warn("the HTTPS request could not be brought back in step with the saved setting", "err", err)
		}
	case tg != nil:
		t.cfg.Log.Warn("the HTTPS request was not the saved setting: asked for it again", "mode", tg.state.Mode)
		if tg.problem != nil {
			t.cfg.Log.Warn("the saved HTTPS setting is not in use", "code", tg.problem.Code, "reason", tg.problem.Message)
		}
		t.state.Lock()
		t.lastStatus = nil
		t.state.Unlock()
	}
}

// sameAsked reports whether two requests ask for the same thing, whatever
// their generations.
func sameAsked(a, b tlsconf.Request) bool {
	a.Generation, b.Generation = 0, 0
	return a == b
}

// TLSChange is a requested HTTPS setting.
type TLSChange struct {
	Mode tlsconf.Mode
	// IP is ip mode's address.
	IP string
	// CertPEM and KeyPEM are manual mode's chain (leaf first) and key.
	CertPEM, KeyPEM []byte
	// DetectedIP is the server's public address as detected ("" when it
	// could not be): an IP address that differs needs ConfirmIP.
	DetectedIP string
	ConfirmIP  bool
}

// TLSError is why a change was not made: Code is one of tlsconf's problem
// codes (what was sent), or a TLSErr* code; Message is for the operator
// (Caddy's own words when it refused).
type TLSError struct {
	Code    string
	Message string
	// Field is the part of the change at fault: mode, ip, cert or key.
	Field string
	// DetectedIP is the address detected, for ip_unconfirmed ("" when it
	// could not be).
	DetectedIP string
}

func (e *TLSError) Error() string { return "https: " + e.Message }

// Codes of a change that was not made (besides tlsconf's).
const (
	// TLSErrIPUnconfirmed: the IP address differs from the detected one, or
	// none could be detected; send it again confirmed.
	TLSErrIPUnconfirmed = "ip_unconfirmed"
	// TLSErrNoMasterKey: manual mode needs core's master key.
	TLSErrNoMasterKey = "no_master_key"
	// TLSErrNoAgent: there is no tls-agent volume, or its request could not
	// be written.
	TLSErrNoAgent = "agent_unavailable"
	// TLSErrAgentTimeout: the tls-agent did not answer within TLSWait.
	TLSErrAgentTimeout = "agent_timeout"
	// TLSErrAgentFailed: the agent refused the request or could not write
	// the files.
	TLSErrAgentFailed = "agent_failed"
	// TLSErrRefused: Caddy refused the new files (Message holds its words).
	TLSErrRefused = "caddy_refused"
	// TLSErrUnreachable: Caddy is not running (ip and manual need it to
	// load them now).
	TLSErrUnreachable = "caddy_unreachable"
	// TLSErrBusy: another change is being applied (here or in the CLI) and
	// did not finish within a few seconds; try again.
	TLSErrBusy = "busy"
)

// changeError turns a tlsconf problem into a TLSError naming its field.
func changeError(err error) error {
	var p *tlsconf.Problem
	if !errors.As(err, &p) {
		return err
	}
	field := ""
	switch {
	case p.Code == tlsconf.CodeBadMode:
		field = "mode"
	case strings.HasPrefix(p.Code, "ip_") || p.Code == tlsconf.CodeBadIP:
		field = "ip"
	case strings.HasPrefix(p.Code, "key_"):
		field = "key"
	case strings.HasPrefix(p.Code, "cert_") || strings.HasPrefix(p.Code, "chain_"):
		field = "cert"
	}
	return &TLSError{Code: p.Code, Message: p.Message, Field: field}
}

// TLSSetResult is a change Caddy took.
type TLSSetResult struct {
	// Warnings are what was accepted but worth knowing (a pair's).
	Warnings []tlsconf.Problem
	// Pending: Caddy is not running; the files are written and Caddy loads
	// them when it starts (only auto is accepted that way: it is the way
	// back when Caddy cannot start).
	Pending bool
}

// Set applies a change (actor nil: the CLI) and commits it once Caddy took
// it, audited as system.tls.set (mode, IP, the certificate's names and
// dates; never the key). It waits a few seconds for another change to
// finish (TLSErrBusy after that), then at most TLSWait for the agent.
//
// Whatever goes wrong after the agent was asked, the committed setting is
// asked for again before Set returns: Caddy refused it, the agent did not
// answer, the caller gave up, or the row, its audit entry or the commit
// failed after the agent took the change.
func (t *TLS) Set(ctx context.Context, actor *store.User, c TLSChange) (*TLSSetResult, error) {
	mode, err := tlsconf.ParseMode(string(c.Mode))
	if err != nil {
		return nil, changeError(err)
	}
	if t.cfg.Exchange == nil {
		return nil, &TLSError{Code: TLSErrNoAgent, Message: "the HTTPS applier (tls-agent) is not set up on this server: run sudo bobres update"}
	}
	base, _, err := t.base()
	if err != nil {
		return nil, changeError(err)
	}
	next := &tlsTarget{state: base}
	row := store.TLSRow{Mode: string(mode)}
	out := &TLSSetResult{}
	switch mode {
	case tlsconf.ModeIP:
		st := base
		st.Mode, st.IP = mode, c.IP
		if next.state, err = st.Check(); err != nil {
			return nil, changeError(err)
		}
		row.IP = next.state.IP
		detected, _ := tlsconf.PublicIP(c.DetectedIP)
		if !c.ConfirmIP && detected != next.state.IP {
			msg := "this server's public IP address could not be detected: confirm that " + next.state.IP + " is this server's address"
			if detected != "" {
				msg = "this server's public IP address looks like " + detected + ", not " + next.state.IP + ": confirm to use " + next.state.IP
			}
			return nil, &TLSError{Code: TLSErrIPUnconfirmed, Field: "ip", Message: msg, DetectedIP: detected}
		}
	case tlsconf.ModeManual:
		if t.cfg.Secrets == nil {
			return nil, &TLSError{Code: TLSErrNoMasterKey, Message: "your own certificate needs core's master key (BOBRES_CORE_MASTER_KEY), which seals the private key"}
		}
		pair, err := tlsconf.CheckPair(c.CertPEM, c.KeyPEM, t.pairOptions(base))
		if err != nil {
			return nil, changeError(err)
		}
		sealed, err := t.cfg.Secrets.Encrypt(pair.KeyPEM)
		if err != nil {
			return nil, err
		}
		next.state.Mode, next.state.HasCert, next.pair = mode, true, pair
		row.CertPEM, row.KeySealed = string(pair.CertPEM), sealed
		out.Warnings = pair.Warnings
	}
	if _, err := tlsconf.Render(next.state); err != nil {
		return nil, changeError(err)
	}

	busyUntil := time.Now().Add(t.cfg.BusyWait)
	if err := t.lockWithin(ctx, busyUntil); err != nil {
		return nil, err
	}
	defer t.mu.Unlock()
	var (
		old        *store.TLSRow
		wrote      bool // the agent was asked
		rolledBack bool // and the committed setting was asked for again
		committed  bool
	)
	// back asks for the committed setting again, once, while the advisory
	// lock is still held when the failure is inside the transaction.
	back := func() {
		if wrote && !rolledBack {
			t.rollback(old)
			rolledBack = true
		}
	}
	err = t.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := t.lockTx(ctx, tx, busyUntil); err != nil {
			return err
		}
		var err error
		if old, err = t.s.st.GetTLS(ctx, tx); err != nil {
			return err
		}
		gen, err := t.write(next)
		if err != nil {
			t.cfg.Log.Error("HTTPS request not written", "err", err)
			return &TLSError{Code: TLSErrNoAgent, Message: "the request for the HTTPS applier (tls-agent) could not be written: " + err.Error()}
		}
		wrote = true
		// Caddy took it, or Caddy is not running and the change is auto:
		// the way back when Caddy cannot start, read when it starts.
		res := t.await(ctx, gen)
		taken := res != nil && (res.OK || res.Code == tlsconf.ResultUnreachable && mode == tlsconf.ModeAuto)
		if !taken {
			back()
			if ctx.Err() != nil {
				return fmt.Errorf("the change was interrupted before Caddy took it; the saved setting was asked for again: %w", ctx.Err())
			}
			return answerError(res)
		}
		out.Pending = !res.OK
		if err := t.s.st.SetTLS(ctx, tx, &row); err != nil {
			back()
			return err
		}
		if err := t.s.auditChange(ctx, tx, actor, "system.tls.set", "tls", "", tlsAudit(old), tlsAudit(&row), ""); err != nil {
			back()
			return err
		}
		return nil
	})
	if err == nil {
		committed = true
	}
	if wrote && !committed {
		back() // the commit itself failed: the agent took what the store does not hold
	}
	if err != nil {
		return nil, err
	}
	t.state.Lock()
	t.lastStatus, t.startErr = nil, ""
	t.state.Unlock()
	return out, nil
}

// lockWithin takes the process-wide change lock, waiting until the
// deadline (TLSErrBusy after that).
func (t *TLS) lockWithin(ctx context.Context, deadline time.Time) error {
	for !t.mu.TryLock() {
		if err := sleepUntil(ctx, deadline, 10*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

// lockTx takes the advisory lock the server and `/app ssl` share, until tx
// ends, waiting until the deadline (TLSErrBusy after that).
func (t *TLS) lockTx(ctx context.Context, tx pgx.Tx, deadline time.Time) error {
	for {
		ok, err := t.s.st.TryLockTLS(ctx, tx)
		if err != nil || ok {
			return err
		}
		if err := sleepUntil(ctx, deadline, 50*time.Millisecond); err != nil {
			return err
		}
	}
}

// sleepUntil waits d (ending at the deadline at the latest): it returns
// TLSErrBusy once the deadline has passed, or ctx's error.
func sleepUntil(ctx context.Context, deadline time.Time, d time.Duration) error {
	left := time.Until(deadline)
	if left <= 0 {
		return &TLSError{Code: TLSErrBusy, Message: "another HTTPS change is being applied: try again in a moment"}
	}
	timer := time.NewTimer(min(d, left))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// await reads the agent's answer for gen until it comes, the wait is over
// or ctx ends (nil: no answer).
func (t *TLS) await(ctx context.Context, gen int64) *tlsconf.Result {
	deadline := time.NewTimer(t.cfg.Wait)
	defer deadline.Stop()
	tick := time.NewTicker(t.cfg.Poll)
	defer tick.Stop()
	for {
		if r, err := t.cfg.Exchange.ReadResult(); err == nil && r.Generation == gen {
			return r
		}
		select {
		case <-ctx.Done():
			return nil
		case <-deadline.C:
			return nil
		case <-tick.C:
		}
	}
}

// rollback asks the agent for the committed setting again (or its
// fallback). The answer is not awaited: the change has failed already.
func (t *TLS) rollback(old *store.TLSRow) {
	tg, err := t.target(old)
	if err == nil {
		_, err = t.write(tg)
	}
	if err != nil {
		t.cfg.Log.Error("the committed HTTPS setting could not be asked for again", "err", err)
	}
}

// answerError is the error for an agent's answer that is not a commit.
func answerError(r *tlsconf.Result) error {
	switch {
	case r == nil:
		return &TLSError{Code: TLSErrAgentTimeout, Message: "the HTTPS applier (tls-agent) did not answer: check it runs (sudo bobres status)"}
	case r.Code == tlsconf.ResultRefused:
		return &TLSError{Code: TLSErrRefused, Message: r.Error}
	case r.Code == tlsconf.ResultUnreachable:
		return &TLSError{Code: TLSErrUnreachable, Message: "Caddy is not running, so it cannot load the new setting now: start it (sudo bobres start) and try again"}
	}
	return &TLSError{Code: TLSErrAgentFailed, Message: "the HTTPS applier could not apply the change: " + r.Error}
}

// tlsAudit is what an audit entry keeps of a setting: never the key.
func tlsAudit(r *store.TLSRow) map[string]any {
	out := map[string]any{"mode": r.Mode}
	if r.IP != "" {
		out["ip"] = r.IP
	}
	if c := parseLeaf(r.CertPEM); c != nil {
		info := tlsconf.Info(c)
		out["cert"] = map[string]any{"subject": info.Subject, "issuer": info.Issuer, "dns_names": info.DNSNames, "ips": info.IPs,
			"not_after": info.NotAfter.Format(time.RFC3339)}
	}
	return out
}

// parseLeaf is the first certificate of a chain (nil when none).
func parseLeaf(chain string) *x509.Certificate {
	b, _ := pem.Decode([]byte(chain))
	if b == nil || b.Type != "CERTIFICATE" {
		return nil
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return nil
	}
	return c
}

// TLSAddress is what one served address serves now.
type TLSAddress struct {
	Host string
	// Role is domain, ip or zarinpal; Expect is public, internal or manual
	// (tlsconf).
	Role   string
	Expect string
	// Reachable: Caddy answered the dial, whatever it then served. False on
	// every address means Caddy is down, stopped or restarting.
	Reachable bool
	// Served: a certificate came back; Cert describes it (nil otherwise).
	Served bool
	Cert   *tlsconf.CertInfo
	NameOK bool
	// Trusted: publicly trusted for the address (nil: unknown).
	Trusted *bool
	// Valid: what this address should serve, and good now (a public
	// address's certificate trusted, any other covering the address and in
	// date).
	Valid bool
	// Expiring: less than min(14 days, a quarter of its life) left.
	Expiring bool
	DaysLeft int
	Error    string
}

// TLSAgentView is the tls-agent's state.
type TLSAgentView struct {
	// Generation is the current request's (0: none written yet).
	Generation int64
	// Answered: the agent answered the current request (OK, Code, Error and
	// AppliedAt are that answer); false while it applies it, or when it
	// does not run.
	Answered  bool
	OK        bool
	Code      string
	Error     string
	AppliedAt *time.Time
}

// TLSView is the HTTPS state for the dashboard and `/app ssl status`.
type TLSView struct {
	// The saved setting.
	Mode         tlsconf.Mode
	IP           string
	Cert         *tlsconf.CertInfo
	UpdatedAt    *time.Time
	Domain       string
	ZarinpalHost string
	// EffectiveMode and EffectiveIP are what core asks for: the saved
	// setting, or auto when Problem says it cannot be used.
	EffectiveMode tlsconf.Mode
	EffectiveIP   string
	Problem       *TLSProblem
	// ManualAllowed: core has a master key, so manual mode can be chosen.
	ManualAllowed bool
	// Agent is nil when there is no tls-agent.
	Agent     *TLSAgentView
	Addresses []TLSAddress
	CheckedAt *time.Time
	PublicURL string
	// DetectedIP is the last public address the bot detected ("" none).
	DetectedIP string
}

// View is the HTTPS state, with a live status of every served address when
// probe (and a prober) is set. Without probe it dials nothing at all (nor
// does it ask the bot): the public URL then comes from the last status and
// detection when they are fresh, else it stays on the domain. A dashboard
// answer to a change uses that, to stay within core's write timeout.
func (t *TLS) View(ctx context.Context, probe bool) (*TLSView, error) {
	row, err := t.s.st.GetTLS(ctx, t.s.st.Conn())
	if err != nil {
		return nil, err
	}
	v := &TLSView{Mode: tlsconf.Mode(row.Mode), IP: row.IP, Domain: t.cfg.Domain, ZarinpalHost: t.cfg.ZarinpalHost,
		ManualAllowed: t.cfg.Secrets != nil, Addresses: []TLSAddress{}}
	if !row.UpdatedAt.IsZero() {
		at := row.UpdatedAt
		v.UpdatedAt = &at
	}
	if c := parseLeaf(row.CertPEM); c != nil {
		info := tlsconf.Info(c)
		v.Cert = &info
	}
	tg, err := t.target(row)
	if err != nil {
		v.Problem = &TLSProblem{Code: problemCode(err), Message: "nothing can be served: " + err.Error()}
	} else {
		v.EffectiveMode, v.EffectiveIP, v.Problem = tg.state.Mode, tg.state.IP, tg.problem
		v.Domain, v.ZarinpalHost = tg.state.Domain, tg.state.ZarinpalHost // as served
	}
	t.state.Lock()
	if t.startErr != "" && v.Problem == nil {
		v.Problem = &TLSProblem{Code: TLSErrNoAgent, Message: t.startErr}
	}
	if t.detected != "" {
		v.DetectedIP = t.detected
	}
	t.state.Unlock()
	if t.cfg.Exchange != nil {
		v.Agent = t.agentView()
	}
	if tg != nil && probe && t.cfg.Probe != nil {
		v.Addresses = t.probe(ctx, tg)
		now := t.cfg.Now()
		v.CheckedAt = &now
	}
	v.PublicURL = t.publicURL(ctx, row, v.Addresses, v.CheckedAt != nil, probe)
	return v, nil
}

// agentView reads the request and the agent's answer.
func (t *TLS) agentView() *TLSAgentView {
	a := &TLSAgentView{}
	if r, err := t.cfg.Exchange.ReadRequest(); err == nil {
		a.Generation = r.Generation
	}
	if r, err := t.cfg.Exchange.ReadResult(); err == nil && a.Generation != 0 && r.Generation == a.Generation {
		at := time.Unix(r.AppliedAt, 0)
		a.Answered, a.OK, a.Code, a.Error, a.AppliedAt = true, r.OK, r.Code, r.Error, &at
	}
	return a
}

// probe reads every address tg serves, at once, and keeps the result for
// the links.
func (t *TLS) probe(ctx context.Context, tg *tlsTarget) []TLSAddress {
	addrs := tg.state.Addresses()
	out := make([]TLSAddress, len(addrs))
	now := t.cfg.Now()
	var wg sync.WaitGroup
	for i, a := range addrs {
		if a.Role == tlsconf.RoleZarinpal && tg.pair != nil && tg.pair.CoversZarinpal {
			a.Expect = tlsconf.ExpectManual // Caddy serves the loaded pair there too
		}
		wg.Go(func() { out[i] = addressStatus(a, t.cfg.Probe(ctx, a.Host), now) })
	}
	wg.Wait()
	t.state.Lock()
	t.lastStatus, t.lastAt = out, now
	t.state.Unlock()
	return out
}

// addressStatus judges what an address serves.
func addressStatus(a tlsconf.Address, r tlsprobe.Result, now time.Time) TLSAddress {
	out := TLSAddress{Host: a.Host, Role: a.Role, Expect: a.Expect, Reachable: r.Reachable, Served: r.Served, NameOK: r.NameOK,
		Trusted: r.Trusted, Error: r.Error}
	if !r.Served {
		return out
	}
	info := r.Info
	out.Cert = &info
	inDate := !now.Before(info.NotBefore) && now.Before(info.NotAfter)
	out.DaysLeft = int(math.Ceil(info.NotAfter.Sub(now).Hours() / 24))
	out.Expiring = inDate && tlsconf.ExpiringSoon(info.NotBefore, info.NotAfter, now)
	switch a.Expect {
	case tlsconf.ExpectPublic:
		out.Valid = r.NameOK && inDate && (r.Trusted == nil || *r.Trusted)
	default:
		// Caddy's own CA, or the operator's certificate: browsers may not
		// trust it, but it is what this address is meant to serve.
		out.Valid = r.NameOK && inDate
		if out.Valid {
			out.Error = ""
		}
	}
	return out
}

// PublicURL is where dashboard links point now (tlsconf.PublicURL). In ip
// mode the links use the IP address only while two things hold: this Caddy
// serves a publicly trusted certificate for it (from a live status at most 3
// minutes old; it dials when there is none), and the bot has detected the
// server's public address within the last 5 minutes and it is that same
// address.
//
// The certificate alone proves nothing after the address changed hands:
// Caddy keeps serving the short-lived certificate for days, and whoever
// holds the recycled address could read the one-time login token in a link
// that still pointed at it. The trade-off: a host whose bot reaches the
// internet from another address than the one clients reach it on (a NAT
// egress, a second interface) never has the two agree, so its links stay on
// the domain; BOBRES_PUBLIC_URL is the manual override. Without the bot, or
// when it cannot answer, they stay on the domain too ("" with no domain).
func (t *TLS) PublicURL(ctx context.Context) string {
	if t.cfg.PublicURLEnv != "" {
		return tlsconf.PublicURL(tlsconf.State{}, t.cfg.PublicURLEnv, false)
	}
	row, err := t.s.st.GetTLS(ctx, t.s.st.Conn())
	if err != nil {
		t.cfg.Log.Warn("public URL: the HTTPS setting", "err", err)
		row = &store.TLSRow{Mode: string(tlsconf.ModeAuto)}
	}
	return t.publicURL(ctx, row, nil, false, true)
}

// publicURL is PublicURL for row. status is a live status just read when
// fresh; otherwise the last one is used if it is recent, else a new one is
// taken when dial is set (the bot is asked only when dial is set too).
func (t *TLS) publicURL(ctx context.Context, row *store.TLSRow, status []TLSAddress, fresh, dial bool) string {
	if t.cfg.PublicURLEnv != "" {
		return tlsconf.PublicURL(tlsconf.State{}, t.cfg.PublicURLEnv, false)
	}
	base, _, err := t.base()
	if err != nil {
		// A domain Caddy cannot serve: links say it as it is, as before.
		if t.cfg.Domain == "" {
			return ""
		}
		return "https://" + tlsconf.HostPort(t.cfg.Domain)
	}
	st := base
	if row.Mode == string(tlsconf.ModeIP) {
		st.Mode, st.IP = tlsconf.ModeIP, row.IP
		if checked, err := st.Check(); err == nil {
			st = checked
		} else {
			st = base
		}
	}
	if st.Mode != tlsconf.ModeIP {
		return tlsconf.PublicURL(st, "", false)
	}
	if !fresh {
		status = t.recentStatus(ctx, st, dial)
	}
	trusted := false
	for _, a := range status {
		if a.Host == st.IP && a.Served && a.NameOK && a.Trusted != nil && *a.Trusted {
			trusted = true
		}
	}
	// Only a certificate that checks out is worth asking the bot about.
	return tlsconf.PublicURL(st, "", trusted && t.egressIP(ctx, dial) == st.IP)
}

// recentStatus is the last live status when it is fresh, else (with dial) a
// new one for st's addresses (none without a prober).
func (t *TLS) recentStatus(ctx context.Context, st tlsconf.State, dial bool) []TLSAddress {
	t.state.Lock()
	status, at := t.lastStatus, t.lastAt
	t.state.Unlock()
	if status != nil && t.cfg.Now().Sub(at) < tlsStatusFresh {
		return status
	}
	if !dial || t.cfg.Probe == nil {
		return nil
	}
	return t.probe(ctx, &tlsTarget{state: st})
}

// egressIP is the public address the bot detected within the last 5
// minutes. When the last detection is older (or there is none) and ask is
// set, it asks the bot now. "" when there is none.
func (t *TLS) egressIP(ctx context.Context, ask bool) string {
	t.state.Lock()
	ip, at := t.egress, t.egressAt
	t.state.Unlock()
	if ip != "" && t.cfg.Now().Sub(at) <= tlsEgressFresh {
		return ip
	}
	if !ask {
		return ""
	}
	return t.detectEgress(ctx)
}

// detectEgress asks the bot for the server's public address and keeps the
// answer. When the bot cannot answer, the detection already kept stays until
// it is 5 minutes old; "" when none is left.
func (t *TLS) detectEgress(ctx context.Context) string {
	if t.cfg.DetectIP == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, tlsEgressWait)
	defer cancel()
	raw, err := t.cfg.DetectIP(ctx)
	ip, perr := tlsconf.PublicIP(raw)
	now := t.cfg.Now()
	t.state.Lock()
	defer t.state.Unlock()
	if err == nil && perr == nil {
		t.egress, t.egressAt = ip, now
		return ip
	}
	t.cfg.Log.Debug("https: public IP address not detected for the links", "err", errors.Join(err, perr))
	if t.egress != "" && now.Sub(t.egressAt) <= tlsEgressFresh {
		return t.egress
	}
	return ""
}

// NoteDetectedIP keeps the public address the bot just detected, for the
// view and for a change made soon after.
func (t *TLS) NoteDetectedIP(ip string) {
	t.state.Lock()
	defer t.state.Unlock()
	t.detected, t.detectedAt = ip, t.cfg.Now()
}

// DetectedIP is the address the bot detected within the last 10 minutes
// ("" when none).
func (t *TLS) DetectedIP() string {
	t.state.Lock()
	defer t.state.Unlock()
	if t.detected == "" || t.cfg.Now().Sub(t.detectedAt) > tlsDetectedFresh {
		return ""
	}
	return t.detected
}

// TLSWatchReport is what the watchdog reads every minute.
type TLSWatchReport struct {
	// Addresses are every served address as it serves now.
	Addresses []TLSAddress
	// Problem says the saved setting is not working as saved ("" when it
	// is): not in use, or not applied by the agent.
	Problem string
	// AgentWaiting: the agent has not answered the current request yet.
	AgentWaiting bool
}

// Watch reads the live status for the watchdog (an empty report when there
// is no domain to watch).
func (t *TLS) Watch(ctx context.Context) (*TLSWatchReport, error) {
	if t.cfg.Domain == "" {
		return &TLSWatchReport{}, nil
	}
	// In ip mode the links depend on the bot's detection of the server's
	// address (PublicURL): it is renewed every minute here, beside the
	// probes, so it is never old when a link is made.
	var wg sync.WaitGroup
	if row, err := t.s.st.GetTLS(ctx, t.s.st.Conn()); err == nil && row.Mode == string(tlsconf.ModeIP) && t.cfg.DetectIP != nil {
		wg.Go(func() { t.detectEgress(ctx) })
	}
	defer wg.Wait()
	v, err := t.View(ctx, true)
	if err != nil {
		return nil, err
	}
	rep := &TLSWatchReport{Addresses: v.Addresses}
	switch a := v.Agent; {
	case v.Problem != nil:
		rep.Problem = v.Problem.Message
	case a != nil && a.Answered && !a.OK && a.Code != tlsconf.ResultUnreachable:
		// Caddy restarting is not the setting's fault (tls_failing tells
		// when it lasts).
		rep.Problem = "the HTTPS applier could not apply the saved setting: " + a.Error
	}
	if a := v.Agent; a != nil && a.Generation != 0 && !a.Answered {
		rep.AgentWaiting = true
	}
	return rep, nil
}

// JSON is the view as GET /api/v1/system/tls and `/app ssl status --json`
// give it (times in unix seconds, null when unknown; never a key):
//
//	{"state": {"mode", "ip", "cert", "updated_at", "domain", "zarinpal_host", "manual_allowed"},
//	 "effective": {"mode", "ip"} (null when nothing can be served),
//	 "problem": {"code", "message"} or null,
//	 "status": {"checked_at", "addresses": [{"address", "role", "expect", "reachable", "served",
//	   "valid", "name_ok", "trusted", "expiring", "days_left", "cert", "error"}], "agent"},
//	 "public_url", "detected_ip"}
//
// A cert is {"subject", "issuer", "dns_names", "ips", "not_before",
// "not_after"}; the agent {"generation", "answered", "ok", "code", "error",
// "applied_at"} (null without a tls-agent).
func (v *TLSView) JSON() map[string]any {
	addrs := make([]map[string]any, 0, len(v.Addresses))
	for _, a := range v.Addresses {
		addrs = append(addrs, map[string]any{"address": a.Host, "role": a.Role, "expect": a.Expect, "reachable": a.Reachable,
			"served": a.Served, "valid": a.Valid, "name_ok": a.NameOK, "trusted": a.Trusted, "expiring": a.Expiring, "days_left": a.DaysLeft,
			"cert": certJSON(a.Cert), "error": a.Error})
	}
	var agent map[string]any
	if a := v.Agent; a != nil {
		agent = map[string]any{"generation": a.Generation, "answered": a.Answered, "ok": a.OK, "code": a.Code,
			"error": a.Error, "applied_at": unixOrNil(a.AppliedAt)}
	}
	var effective, problem map[string]any
	if v.EffectiveMode != "" {
		effective = map[string]any{"mode": v.EffectiveMode, "ip": v.EffectiveIP}
	}
	if v.Problem != nil {
		problem = map[string]any{"code": v.Problem.Code, "message": v.Problem.Message}
	}
	var detected any
	if v.DetectedIP != "" {
		detected = v.DetectedIP
	}
	return map[string]any{
		"state": map[string]any{"mode": v.Mode, "ip": v.IP, "cert": certJSON(v.Cert), "updated_at": unixOrNil(v.UpdatedAt),
			"domain": v.Domain, "zarinpal_host": v.ZarinpalHost, "manual_allowed": v.ManualAllowed},
		"effective": effective, "problem": problem,
		"status":     map[string]any{"checked_at": unixOrNil(v.CheckedAt), "addresses": addrs, "agent": agent},
		"public_url": v.PublicURL, "detected_ip": detected,
	}
}

// certJSON describes a certificate (nil: null).
func certJSON(c *tlsconf.CertInfo) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{"subject": c.Subject, "issuer": c.Issuer, "dns_names": c.DNSNames, "ips": c.IPs,
		"not_before": c.NotBefore.Unix(), "not_after": c.NotAfter.Unix()}
}

func unixOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}
