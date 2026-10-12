package domain

// Automated gateway settings at runtime: the owner switches Zarinpal on and
// off and changes its merchant id, proxy and sandbox from the dashboard, through
// core. The merchant id and the proxy URL are stored encrypted with the payments
// master key and never leave this service: staff only see whether they are set.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/provider"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

const providerZarinpal = "zarinpal"

var (
	// ErrKeyMissing: settings cannot be stored without the payments master key.
	ErrKeyMissing = errors.New("payments: the payments master key is not set")
	// ErrPrecondition: the request is valid but cannot apply in the current state.
	ErrPrecondition = errors.New("payments: failed precondition")
)

func precondition(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrPrecondition, fmt.Sprintf(format, a...))
}

// OpenIntentsError refuses a change that would strand the payments still open
// at the gateway: their verification must use the merchant id they started with.
type OpenIntentsError struct {
	Provider string
	Count    int64
}

func (e *OpenIntentsError) Error() string {
	if e.Provider == USDTAuto {
		return fmt.Sprintf("%d open %s payments: the address cannot change until they close", e.Count, e.Provider)
	}
	return fmt.Sprintf("%d open %s payments: the merchant id and the sandbox cannot change until they close", e.Count, e.Provider)
}

// Is makes the error an ErrPrecondition.
func (e *OpenIntentsError) Is(target error) bool { return target == ErrPrecondition }

// Problems that keep a gateway's settings from being used (GatewayConfig.Problem).
const (
	// ProblemUnreadable: settings are stored but the master key is missing or
	// is not the one they were saved with.
	ProblemUnreadable = "unreadable"
	// ProblemSandboxRefused: the stored settings use the sandbox in prod.
	ProblemSandboxRefused = "sandbox_refused"
	// ProblemPublicURL: switched on, but the public URL cannot take the
	// customer back from Zarinpal (BOBRES_ZARINPAL_PUBLIC_URL / BOBRES_PUBLIC_URL).
	ProblemPublicURL = "public_url"
	// ProblemInvalid: the stored settings are refused by the gateway client.
	ProblemInvalid = "invalid"
)

// ZarinpalSettings are Zarinpal's credentials and route. They are secret:
// String and LogValue print only which ones are set.
type ZarinpalSettings struct {
	MerchantID string
	Sandbox    bool
	Proxy      string // an http(s) proxy URL; it may carry a password
}

// LogValue implements slog.LogValuer without the secrets.
func (z ZarinpalSettings) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("merchant_set", z.MerchantID != ""), slog.Bool("sandbox", z.Sandbox),
		slog.Bool("proxy_set", z.Proxy != ""))
}

// String keeps %v and %+v from printing the secrets.
func (z ZarinpalSettings) String() string { return z.LogValue().String() }

// GoString keeps %#v from printing the secrets.
func (z ZarinpalSettings) GoString() string { return z.String() }

// GatewayOptions wire the gateways' runtime settings (cmd/payments).
type GatewayOptions struct {
	// Env is the deployment (dev|staging|prod); prod never uses the sandbox.
	Env string
	// PublicURLErr says why the public URL cannot take customers back from
	// Zarinpal (config.CheckZarinpalPublicURL); nil = it can.
	PublicURLErr error
	// Envelope encrypts the stored secrets. Nil = no master key: Seed is used as
	// it is and the settings cannot be changed.
	Envelope *bcrypto.Envelope
	// Seed is the environment's settings (MerchantID "" = none). They are
	// stored once, when the database has no settings yet.
	Seed ZarinpalSettings
	// Build makes the Zarinpal client and validates the settings (tests point
	// it at a fake).
	Build func(ZarinpalSettings) (gateway.Gateway, error)
	// BuildChain makes the TronGrid client of the automatic USDT watcher and
	// validates its settings (tests point it at a fake). Nil = no watcher.
	BuildChain func(ChainSettings) (gateway.ChainSource, error)
}

// gatewaySet is one snapshot of the automated gateways. It is never changed
// after it is stored: a settings change stores a new one.
type gatewaySet struct {
	// configured: gateways with credentials. They check the payments already
	// open (the reconciler, the return page), even while not offered.
	configured map[string]gateway.Gateway
	// offered: gateways taking new payments.
	offered map[string]bool
	zp      zarinpalState
	opts    *GatewayOptions
}

// zarinpalState is what the service knows about Zarinpal's settings.
type zarinpalState struct {
	settings  ZarinpalSettings // in memory only, never returned
	enabled   bool
	fromEnv   bool // no master key: the environment's settings, read-only
	problem   string
	updatedAt time.Time
	updatedBy string
}

// GatewayConfig is an automated gateway's settings as staff see them: whether
// the secrets are set, never the secrets.
type GatewayConfig struct {
	Provider string
	Enabled  bool // switched on by the owner
	// Configured: it has credentials, so open payments keep being checked.
	Configured bool
	// Offered: new payments can use it now (enabled, configured, no problem).
	Offered      bool
	Sandbox      bool
	MerchantHint string // the merchant id's last 4 characters, "" when none
	Address      string // usdt_trc20_auto: the store's TRC20 address (public)
	APIKeySet    bool   // usdt_trc20_auto: a TronGrid API key is set
	ProxySet     bool
	KeyMissing   bool // no payments master key: read-only
	FromEnv      bool // the settings come from the environment
	Problem      string
	UpdatedAt    time.Time // zero for the environment's settings
	UpdatedBy    string
}

// ConfigureGateways loads the automated gateways' settings at startup: from
// the database, seeded once from the environment when it has none; from the
// environment alone, as before, when there is no master key.
func (s *Service) ConfigureGateways(ctx context.Context, o GatewayOptions) error {
	if o.Build == nil {
		return errors.New("payments: no gateway client builder")
	}
	o.Seed = normalize(o.Seed)
	s.gwMu.Lock()
	defer s.gwMu.Unlock()
	if o.Envelope != nil && o.Seed.MerchantID != "" {
		if err := s.seedZarinpal(ctx, &o); err != nil {
			return err
		}
	}
	set, err := s.loadGateways(ctx, &o)
	if err != nil {
		return err
	}
	zp := set.zp
	if zp.fromEnv && zp.problem != "" {
		// config refuses these already; never run on them.
		return fmt.Errorf("zarinpal settings from the environment: %s", problemText(zp.problem))
	}
	s.gws.Store(set)
	s.log.Info("Zarinpal settings", "configured", set.configured[providerZarinpal] != nil,
		"offered", set.offered[providerZarinpal], "from_env", zp.fromEnv, "settings", zp.settings)
	if zp.problem != "" {
		s.log.Warn("Zarinpal settings cannot be used", "problem", zp.problem, "why", problemText(zp.problem))
	}
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	cs, err := s.loadChain(ctx, &o)
	if err != nil {
		return err
	}
	s.chain.Store(cs)
	if o.BuildChain != nil {
		s.log.Info("automatic USDT settings", "readable", cs.readable(), "offered", cs.offered(), "settings", cs.settings)
		if cs.problem != "" {
			s.log.Warn("automatic USDT settings cannot be used", "problem", cs.problem, "why", problemText(cs.problem))
		}
	}
	return nil
}

func normalize(z ZarinpalSettings) ZarinpalSettings {
	z.MerchantID = strings.ToLower(strings.TrimSpace(z.MerchantID))
	z.Proxy = strings.TrimSpace(z.Proxy)
	return z
}

// seedZarinpal stores the environment's settings when there are none yet.
func (s *Service) seedZarinpal(ctx context.Context, o *GatewayOptions) error {
	if _, err := s.st.GetGatewayConfig(ctx, nil, providerZarinpal); err == nil {
		return nil // stored settings win; the environment is only the first install's input
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if o.Seed.Sandbox && o.Env == "prod" {
		return errors.New("zarinpal settings from the environment: the sandbox is refused when BOBRES_ENV=prod")
	}
	if _, err := o.Build(o.Seed); err != nil {
		return fmt.Errorf("zarinpal settings from the environment: %w", err)
	}
	row, err := encryptSettings(o.Envelope, o.Seed)
	if err != nil {
		return err
	}
	row.Enabled = true
	ok, err := s.st.SeedGatewayConfig(ctx, row)
	if err != nil {
		return err
	}
	if ok {
		s.log.Info("Zarinpal settings stored from the environment; change them from the dashboard from now on")
	}
	return nil
}

// encryptSettings is the row for settings (enabled left to the caller).
func encryptSettings(env *bcrypto.Envelope, z ZarinpalSettings) (*store.GatewayConfig, error) {
	row := &store.GatewayConfig{Provider: providerZarinpal, Sandbox: z.Sandbox}
	for _, f := range []struct {
		plain string
		enc   *[]byte
	}{{z.MerchantID, &row.MerchantEnc}, {z.Proxy, &row.ProxyEnc}} {
		if f.plain == "" {
			continue
		}
		b, err := env.Encrypt([]byte(f.plain))
		if err != nil {
			return nil, err
		}
		*f.enc = b
	}
	return row, nil
}

// decryptSettings reverses encryptSettings.
func decryptSettings(env *bcrypto.Envelope, row *store.GatewayConfig) (ZarinpalSettings, error) {
	z := ZarinpalSettings{Sandbox: row.Sandbox}
	for _, f := range []struct {
		enc   []byte
		plain *string
	}{{row.MerchantEnc, &z.MerchantID}, {row.ProxyEnc, &z.Proxy}} {
		if f.enc == nil {
			continue
		}
		b, err := env.Decrypt(f.enc)
		if err != nil {
			return ZarinpalSettings{}, err
		}
		*f.plain = string(b)
	}
	return z, nil
}

// loadGateways reads the settings and builds the snapshot they describe.
func (s *Service) loadGateways(ctx context.Context, o *GatewayOptions) (*gatewaySet, error) {
	row, err := s.st.GetGatewayConfig(ctx, nil, providerZarinpal)
	if errors.Is(err, store.ErrNotFound) {
		row, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	var zp zarinpalState
	switch {
	case row == nil && o.Envelope == nil:
		if o.Seed.MerchantID != "" {
			// Refused before anything starts, as before the settings were stored.
			if _, err := o.Build(o.Seed); err != nil {
				return nil, fmt.Errorf("zarinpal settings from the environment: %w", err)
			}
		}
		zp = zarinpalState{settings: o.Seed, enabled: o.Seed.MerchantID != "", fromEnv: true}
	case row == nil:
		// Nothing stored and nothing in the environment: not configured.
	default:
		zp = zarinpalState{enabled: row.Enabled, updatedAt: row.UpdatedAt}
		zp.settings.Sandbox = row.Sandbox
		if row.UpdatedBy != nil {
			zp.updatedBy = *row.UpdatedBy
		}
		if o.Envelope == nil {
			zp.problem = ProblemUnreadable
			break
		}
		z, err := decryptSettings(o.Envelope, row)
		if err != nil {
			zp.problem = ProblemUnreadable
			break
		}
		zp.settings = z
	}
	return newGatewaySet(o, zp), nil
}

// newGatewaySet builds the snapshot for Zarinpal's state, deciding whether it
// checks open payments (configured) and takes new ones (offered).
func newGatewaySet(o *GatewayOptions, zp zarinpalState) *gatewaySet {
	set := &gatewaySet{configured: map[string]gateway.Gateway{}, offered: map[string]bool{}, zp: zp, opts: o}
	z := &set.zp
	if z.problem != "" || z.settings.MerchantID == "" {
		return set
	}
	if z.settings.Sandbox && o.Env == "prod" {
		// Not even for checks: a sandbox "payment" settled here would give
		// service for free.
		z.problem = ProblemSandboxRefused
		return set
	}
	g, err := o.Build(z.settings)
	if err != nil {
		z.problem = ProblemInvalid
		return set
	}
	set.configured[providerZarinpal] = g
	if !z.enabled {
		return set
	}
	if o.PublicURLErr != nil {
		z.problem = ProblemPublicURL
		return set
	}
	set.offered[providerZarinpal] = true
	return set
}

// problemText explains a problem code in the log.
func problemText(p string) string {
	switch p {
	case ProblemUnreadable:
		return "the stored settings need the payments master key they were saved with (BOBRES_PAYMENTS_MASTER_KEY)"
	case ProblemSandboxRefused:
		return "the Zarinpal sandbox is refused when BOBRES_ENV=prod"
	case ProblemPublicURL:
		return "BOBRES_ZARINPAL_PUBLIC_URL (or BOBRES_PUBLIC_URL) must be https://host[:port]"
	case ProblemInvalid:
		return "the stored settings are refused by the gateway's client"
	}
	return p
}

// view is the staff view of Zarinpal's settings in this snapshot.
func (set *gatewaySet) view() GatewayConfig {
	zp := set.zp
	c := GatewayConfig{
		Provider: providerZarinpal, Enabled: zp.enabled,
		Configured: set.configured[providerZarinpal] != nil, Offered: set.offered[providerZarinpal],
		Sandbox: zp.settings.Sandbox, ProxySet: zp.settings.Proxy != "",
		KeyMissing: set.opts == nil || set.opts.Envelope == nil, FromEnv: zp.fromEnv,
		Problem: zp.problem, UpdatedAt: zp.updatedAt, UpdatedBy: zp.updatedBy,
	}
	if m := zp.settings.MerchantID; len(m) >= 4 {
		c.MerchantHint = m[len(m)-4:]
	}
	return c
}

// GatewayConfigs lists the automated gateways' settings (staff view):
// Zarinpal, then automatic USDT.
func (s *Service) GatewayConfigs() []GatewayConfig {
	return []GatewayConfig{s.gws.Load().view(), s.chain.Load().view()}
}

// GatewayUpdate changes a gateway's settings. Enabled and Sandbox are the new
// values; an empty merchant id, address, API key or proxy keeps the stored one.
type GatewayUpdate struct {
	Provider   string
	Enabled    bool
	Sandbox    bool
	MerchantID string
	Proxy      string
	ClearProxy bool
	// usdt_trc20_auto: the store's TRC20 address and the TronGrid API key.
	Address     string
	APIKey      string
	ClearAPIKey bool
	ActorID     string // the core staff user making the change
}

// SetGatewayConfig validates, stores and applies new settings. Switching off
// only stops offering the gateway: the payments already open keep being
// checked. The merchant id and the sandbox cannot change while payments are
// open and being checked, or they could never be verified.
func (s *Service) SetGatewayConfig(ctx context.Context, u GatewayUpdate) (GatewayConfig, error) {
	if u.Provider == USDTAuto {
		return s.setChainConfig(ctx, u)
	}
	if u.Provider != providerZarinpal {
		return GatewayConfig{}, invalid("unknown gateway %q", u.Provider)
	}
	var actor *string
	if u.ActorID != "" {
		id, err := buuid.Parse(u.ActorID)
		if err != nil {
			return GatewayConfig{}, invalid("actor id must be a user id")
		}
		a := id.String()
		actor = &a
	}
	merchant, proxy := strings.ToLower(strings.TrimSpace(u.MerchantID)), strings.TrimSpace(u.Proxy)
	if u.ClearProxy && proxy != "" {
		return GatewayConfig{}, invalid("set a proxy or clear it, not both")
	}
	if u.Address != "" || u.APIKey != "" || u.ClearAPIKey {
		return GatewayConfig{}, invalid("Zarinpal has no address and no API key")
	}

	s.gwMu.Lock()
	defer s.gwMu.Unlock()
	cur := s.gws.Load()
	o := cur.opts
	if o == nil || o.Envelope == nil || o.Build == nil {
		return GatewayConfig{}, ErrKeyMissing
	}
	old := cur.zp.settings
	if cur.zp.problem == ProblemUnreadable {
		// The stored secrets are lost: start again from the request.
		old = ZarinpalSettings{Sandbox: old.Sandbox}
		if merchant == "" {
			return GatewayConfig{}, invalid("enter the merchant id again: the stored one cannot be decrypted")
		}
	}
	next := old
	if merchant != "" {
		next.MerchantID = merchant
	}
	switch {
	case u.ClearProxy:
		next.Proxy = ""
	case proxy != "":
		next.Proxy = proxy
	}
	next.Sandbox = u.Sandbox
	switch {
	case next.Sandbox && o.Env == "prod":
		return GatewayConfig{}, invalid("the Zarinpal sandbox is refused when BOBRES_ENV=prod")
	case u.Enabled && next.MerchantID == "":
		return GatewayConfig{}, invalid("set the merchant id before switching Zarinpal on")
	case u.Enabled && o.PublicURLErr != nil:
		return GatewayConfig{}, precondition("%s", o.PublicURLErr.Error())
	}
	if next.MerchantID != "" {
		if _, err := o.Build(next); err != nil {
			return GatewayConfig{}, invalid("%s", err.Error())
		}
	}
	row, err := encryptSettings(o.Envelope, next)
	if err != nil {
		return GatewayConfig{}, err
	}
	row.Enabled, row.UpdatedBy = u.Enabled, actor
	// Open payments pin the merchant id and the sandbox while a client can
	// check them. With none (secrets the key cannot decrypt, a stored sandbox in
	// prod, settings the client refuses), nothing checks them, and entering the
	// settings again is the only way they ever can be. Another merchant id
	// cannot settle them: their checks fail, which closes them in time.
	routeChanged := next.MerchantID != old.MerchantID || next.Sandbox != old.Sandbox
	pinned := routeChanged && cur.configured[providerZarinpal] != nil
	var saved *store.GatewayConfig
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if pinned {
			n, _, err := s.st.OpenIntents(ctx, tx, providerZarinpal)
			if err != nil {
				return err
			}
			if n > 0 {
				return &OpenIntentsError{Provider: providerZarinpal, Count: n}
			}
		}
		saved, err = s.st.SaveGatewayConfig(ctx, tx, row)
		return err
	})
	if err != nil {
		return GatewayConfig{}, err
	}
	zp := zarinpalState{settings: next, enabled: saved.Enabled, updatedAt: saved.UpdatedAt}
	if saved.UpdatedBy != nil {
		zp.updatedBy = *saved.UpdatedBy
	}
	set := newGatewaySet(o, zp)
	s.gws.Store(set)
	if prev := cur.configured[providerZarinpal]; prev != nil && prev != set.configured[providerZarinpal] {
		closeIdle(prev) // checks still running on it keep their connection
	}
	s.log.Info("Zarinpal settings changed", "actor", zp.updatedBy, "enabled", zp.enabled,
		"merchant_changed", next.MerchantID != old.MerchantID, "proxy_changed", next.Proxy != old.Proxy,
		"settings", next)
	return set.view(), nil
}

// closeIdle lets a client that will not be used again drop its connections.
func closeIdle(g any) {
	if c, ok := g.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// GatewayCandidate is settings to test before saving them; an empty merchant
// id, address, API key or proxy means the stored one.
type GatewayCandidate struct {
	Sandbox     bool
	MerchantID  string
	Proxy       string
	ClearProxy  bool
	Address     string // usdt_trc20_auto
	APIKey      string // usdt_trc20_auto
	ClearAPIKey bool   // usdt_trc20_auto
}

// GatewayTest is a connection test's outcome. A gateway that cannot be
// reached or refuses the credentials is a failed test, not an error.
type GatewayTest struct {
	OK         bool
	Detail     string // why it failed (no secret or address in it)
	Unverified int    // paid payments the gateway still waits for us to verify
	Latency    time.Duration
}

// gatewayTestTimeout bounds a test, under the dashboard's request timeout.
const gatewayTestTimeout = 25 * time.Second

// TestGateway checks credentials and their route with a read-only call:
// the stored settings (through the very client in use), or a candidate built
// the same way. Nothing is stored and no payment is created or verified.
func (s *Service) TestGateway(ctx context.Context, providerName string, cand *GatewayCandidate) (GatewayTest, error) {
	if providerName == USDTAuto {
		return s.testChain(ctx, cand)
	}
	if providerName != providerZarinpal {
		return GatewayTest{}, invalid("unknown gateway %q", providerName)
	}
	set := s.gws.Load()
	g := set.configured[providerZarinpal]
	if cand != nil {
		o := set.opts
		if o == nil || o.Build == nil {
			return GatewayTest{}, precondition("gateway settings are not loaded")
		}
		z := set.zp.settings
		if set.zp.problem == ProblemUnreadable {
			z = ZarinpalSettings{}
		}
		if m := strings.ToLower(strings.TrimSpace(cand.MerchantID)); m != "" {
			z.MerchantID = m
		}
		proxy := strings.TrimSpace(cand.Proxy)
		switch {
		case cand.ClearProxy && proxy != "":
			return GatewayTest{}, invalid("set a proxy or clear it, not both")
		case cand.ClearProxy:
			z.Proxy = ""
		case proxy != "":
			z.Proxy = proxy
		}
		z.Sandbox = cand.Sandbox
		switch {
		case z.Sandbox && o.Env == "prod":
			return GatewayTest{}, invalid("the Zarinpal sandbox is refused when BOBRES_ENV=prod")
		case z.MerchantID == "":
			return GatewayTest{}, invalid("enter the merchant id to test")
		}
		built, err := o.Build(z)
		if err != nil {
			return GatewayTest{}, invalid("%s", err.Error())
		}
		defer closeIdle(built)
		g = built
	}
	if g == nil {
		return GatewayTest{}, precondition("Zarinpal has no usable merchant id to test")
	}
	t, ok := g.(gateway.Tester)
	if !ok {
		return GatewayTest{}, precondition("%s cannot be tested", providerName)
	}
	ctx, cancel := context.WithTimeout(ctx, gatewayTestTimeout)
	defer cancel()
	start := time.Now()
	r, err := t.Test(ctx)
	out := GatewayTest{Latency: time.Since(start)}
	if err != nil {
		s.log.Info("gateway test failed", "provider", providerName, "err", err)
		out.Detail = err.Error()
		return out, nil
	}
	out.OK, out.Unverified = true, r.Unverified
	return out, nil
}

// reportProviders are the providers a report can be asked for.
var reportProviders = []string{providerZarinpal, Stars, USDTAuto, provider.ManualCard, provider.ManualCrypto, provider.ManualZarinpal}

// maxReportSpan bounds a report's period.
const maxReportSpan = 366 * 24 * time.Hour

// GatewayReport summarises a provider's payments created in [from, to)
// (default: the 30 days up to now), what is open now and the failed payment
// checks kept in the period (Zarinpal); for automatic USDT also the unmatched
// deposits staff must still credit by hand and how the watcher's reads go.
func (s *Service) GatewayReport(ctx context.Context, providerName string, from, to time.Time) (*store.GatewayReport, error) {
	if !slices.Contains(reportProviders, providerName) {
		return nil, invalid("unknown payment method %q", providerName)
	}
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-30 * 24 * time.Hour)
	}
	if !from.Before(to) {
		return nil, invalid("the report must start before it ends")
	}
	if to.Sub(from) > maxReportSpan {
		return nil, invalid("a report covers at most 366 days")
	}
	r, err := s.st.GatewayReport(ctx, providerName, from, to)
	if err != nil || providerName != USDTAuto {
		return r, err
	}
	if r.Chain, err = s.st.ChainReport(ctx, s.chain.Load().settings.Address); err != nil {
		return nil, err
	}
	return r, nil
}
