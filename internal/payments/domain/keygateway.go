package domain

// API-key gateways (IDPay, NextPay, Aqayepardakht): redirect gateways that, like
// Zarinpal, take a payment, send the customer to the bank and are verified
// through their API after the customer returns. They differ in what the owner
// enters: one API key (Aqayepardakht calls it the PIN) instead of a merchant id.
// The key and the proxy URL are stored encrypted with the payments master key
// (the key in api_key_enc) and never leave this service: staff only see whether
// they are set.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Provider names of the API-key gateways.
const (
	providerIDPay         = "idpay"
	providerNextPay       = "nextpay"
	providerAqayepardakht = "aqayepardakht"
)

// keyedProviders is the order the API-key gateways are listed in, after
// Zarinpal and automatic USDT.
var keyedProviders = []string{providerIDPay, providerNextPay, providerAqayepardakht}

func isKeyed(p string) bool {
	return p == providerIDPay || p == providerNextPay || p == providerAqayepardakht
}

func keyedTitle(p string) string {
	switch p {
	case providerIDPay:
		return "IDPay"
	case providerAqayepardakht:
		return "Aqayepardakht"
	}
	return "NextPay"
}

// keyedSecretName is what the owner's message calls the gateway's secret. It keeps the words "API key",
// which the dashboard reads to put an error under the right field.
func keyedSecretName(p string) string {
	if p == providerAqayepardakht {
		return "API key (PIN)"
	}
	return "API key"
}

// aqayepardakhtSandboxPIN is Aqayepardakht's test system: not a switch, a PIN.
const aqayepardakhtSandboxPIN = "sandbox"

// keyedSandbox says whether the settings use the gateway's test system: IDPay has a switch, Aqayepardakht's
// is the PIN "sandbox" (so the flag follows the PIN and can never disagree with it), NextPay has none.
func keyedSandbox(name string, k KeySettings) bool {
	if name == providerAqayepardakht {
		return k.APIKey == aqayepardakhtSandboxPIN
	}
	return k.Sandbox
}

// keyedKey reads a typed API key. IDPay's and NextPay's are UUIDs, whatever case they were typed in;
// Aqayepardakht's PIN is case-sensitive and stays as it was typed.
func keyedKey(name, raw string) string {
	raw = strings.TrimSpace(raw)
	if name == providerAqayepardakht {
		return raw
	}
	return strings.ToLower(raw)
}

// KeySettings are an API-key gateway's credentials and route. They are secret:
// String and LogValue print only which ones are set.
type KeySettings struct {
	APIKey  string
	Sandbox bool
	Proxy   string // an http(s) proxy URL; it may carry a password
}

// LogValue implements slog.LogValuer without the secrets.
func (k KeySettings) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("api_key_set", k.APIKey != ""), slog.Bool("sandbox", k.Sandbox),
		slog.Bool("proxy_set", k.Proxy != ""))
}

// String keeps %v and %+v from printing the secrets.
func (k KeySettings) String() string { return k.LogValue().String() }

// GoString keeps %#v from printing the secrets.
func (k KeySettings) GoString() string { return k.String() }

func normalizeKeyed(name string, k KeySettings) KeySettings {
	k.APIKey = keyedKey(name, k.APIKey)
	k.Proxy = strings.TrimSpace(k.Proxy)
	k.Sandbox = keyedSandbox(name, k)
	return k
}

// KeyedOptions wire one API-key gateway (cmd/payments). A provider without
// Build is not wired: it is not listed and cannot be configured.
type KeyedOptions struct {
	// Seed is the environment's settings (APIKey "" = none), stored once, when
	// the database has no settings for the provider yet.
	Seed KeySettings
	// Build makes the gateway client and validates the settings (tests point it
	// at a fake).
	Build func(KeySettings) (gateway.Gateway, error)
}

// keyedState is what the service knows about an API-key gateway's settings.
type keyedState struct {
	settings  KeySettings // in memory only, never returned
	enabled   bool
	fromEnv   bool
	problem   string
	updatedAt time.Time
	updatedBy string
}

// clone copies a snapshot so one provider can be replaced in it.
func (set *gatewaySet) clone() *gatewaySet {
	c := *set
	c.configured, c.offered, c.keyed = maps.Clone(set.configured), maps.Clone(set.offered), maps.Clone(set.keyed)
	if c.configured == nil {
		c.configured = map[string]gateway.Gateway{}
	}
	if c.offered == nil {
		c.offered = map[string]bool{}
	}
	if c.keyed == nil {
		c.keyed = map[string]keyedState{}
	}
	return &c
}

// carryKeyed takes the API-key gateways of another snapshot, built clients and
// all (Zarinpal's settings changed; theirs did not).
func (set *gatewaySet) carryKeyed(from *gatewaySet) {
	if len(from.keyed) > 0 && set.keyed == nil {
		set.keyed = map[string]keyedState{}
	}
	for _, name := range keyedProviders {
		ks, ok := from.keyed[name]
		if !ok {
			continue
		}
		set.keyed[name] = ks
		if g := from.configured[name]; g != nil {
			set.configured[name] = g
		}
		if from.offered[name] {
			set.offered[name] = true
		}
	}
}

// applyKeyed puts a provider's state into the snapshot, deciding whether it
// checks open payments (configured) and takes new ones (offered).
func (set *gatewaySet) applyKeyed(o *GatewayOptions, name string, ks keyedState) {
	if set.keyed == nil {
		set.keyed = map[string]keyedState{}
	}
	delete(set.configured, name)
	delete(set.offered, name)
	defer func() { set.keyed[name] = ks }()
	if ks.problem != "" || ks.settings.APIKey == "" {
		return
	}
	ks.settings.Sandbox = keyedSandbox(name, ks.settings)
	if ks.settings.Sandbox && o.Env == "prod" {
		// Not even for checks: a sandbox "payment" settled here would give
		// service for free.
		ks.problem = ProblemSandboxRefused
		return
	}
	g, err := o.Keyed[name].Build(ks.settings)
	if err != nil {
		ks.problem = ProblemInvalid
		return
	}
	set.configured[name] = g
	if !ks.enabled {
		return
	}
	if o.PublicURLErr != nil {
		ks.problem = ProblemPublicURL
		return
	}
	set.offered[name] = true
}

func encryptKeyed(env *bcrypto.Envelope, name string, k KeySettings) (*store.GatewayConfig, error) {
	row := &store.GatewayConfig{Provider: name, Sandbox: k.Sandbox}
	for _, f := range []struct {
		plain string
		enc   *[]byte
	}{{k.APIKey, &row.APIKeyEnc}, {k.Proxy, &row.ProxyEnc}} {
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

func decryptKeyed(env *bcrypto.Envelope, row *store.GatewayConfig) (KeySettings, error) {
	k := KeySettings{Sandbox: row.Sandbox}
	for _, f := range []struct {
		enc   []byte
		plain *string
	}{{row.APIKeyEnc, &k.APIKey}, {row.ProxyEnc, &k.Proxy}} {
		if f.enc == nil {
			continue
		}
		b, err := env.Decrypt(f.enc)
		if err != nil {
			return KeySettings{}, err
		}
		*f.plain = string(b)
	}
	return k, nil
}

// wiredKeyed lists the API-key gateways this service was given a builder for.
func wiredKeyed(o *GatewayOptions) []string {
	var out []string
	for _, name := range keyedProviders {
		if o.Keyed[name].Build != nil {
			out = append(out, name)
		}
	}
	return out
}

// seedKeyed stores the environment's settings when there are none yet.
func (s *Service) seedKeyed(ctx context.Context, o *GatewayOptions, name string) error {
	seed := normalizeKeyed(name, o.Keyed[name].Seed)
	if _, err := s.st.GetGatewayConfig(ctx, nil, name); err == nil {
		return nil // stored settings win
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if seed.Sandbox && o.Env == "prod" {
		return fmt.Errorf("%s settings from the environment: the sandbox is refused when BOBRES_ENV=prod", name)
	}
	if _, err := o.Keyed[name].Build(seed); err != nil {
		return fmt.Errorf("%s settings from the environment: %w", name, err)
	}
	row, err := encryptKeyed(o.Envelope, name, seed)
	if err != nil {
		return err
	}
	row.Enabled = true
	ok, err := s.st.SeedGatewayConfig(ctx, row)
	if err != nil {
		return err
	}
	if ok {
		s.log.Info(keyedTitle(name) + " settings stored from the environment; change them from the dashboard from now on")
	}
	return nil
}

// loadKeyed reads one API-key gateway's settings.
func (s *Service) loadKeyed(ctx context.Context, o *GatewayOptions, name string) (keyedState, error) {
	row, err := s.st.GetGatewayConfig(ctx, nil, name)
	if errors.Is(err, store.ErrNotFound) {
		row, err = nil, nil
	}
	if err != nil {
		return keyedState{}, err
	}
	seed := normalizeKeyed(name, o.Keyed[name].Seed)
	switch {
	case row == nil && o.Envelope == nil:
		if seed.APIKey != "" {
			// Refused before anything starts.
			if _, err := o.Keyed[name].Build(seed); err != nil {
				return keyedState{}, fmt.Errorf("%s settings from the environment: %w", name, err)
			}
		}
		return keyedState{settings: seed, enabled: seed.APIKey != "", fromEnv: true}, nil
	case row == nil:
		return keyedState{}, nil // nothing stored and nothing in the environment
	}
	ks := keyedState{enabled: row.Enabled, updatedAt: row.UpdatedAt}
	ks.settings.Sandbox = row.Sandbox
	if row.UpdatedBy != nil {
		ks.updatedBy = *row.UpdatedBy
	}
	if o.Envelope == nil {
		ks.problem = ProblemUnreadable
		return ks, nil
	}
	k, err := decryptKeyed(o.Envelope, row)
	if err != nil {
		ks.problem = ProblemUnreadable
		return ks, nil
	}
	k.Sandbox = keyedSandbox(name, k)
	ks.settings = k
	return ks, nil
}

// keyedView is the staff view of an API-key gateway's settings in this snapshot.
func (set *gatewaySet) keyedView(name string) GatewayConfig {
	ks := set.keyed[name]
	c := GatewayConfig{
		Provider: name, Enabled: ks.enabled,
		Configured: set.configured[name] != nil, Offered: set.offered[name],
		Sandbox: ks.settings.Sandbox, ProxySet: ks.settings.Proxy != "", APIKeySet: ks.settings.APIKey != "",
		KeyMissing: set.opts == nil || set.opts.Envelope == nil, FromEnv: ks.fromEnv,
		Problem: ks.problem, UpdatedAt: ks.updatedAt, UpdatedBy: ks.updatedBy,
	}
	return c
}

func parseActor(id string) (*string, error) {
	if id == "" {
		return nil, nil
	}
	u, err := buuid.Parse(id)
	if err != nil {
		return nil, invalid("actor id must be a user id")
	}
	a := u.String()
	return &a, nil
}

// setKeyedConfig is SetGatewayConfig for IDPay, NextPay and Aqayepardakht.
func (s *Service) setKeyedConfig(ctx context.Context, u GatewayUpdate) (GatewayConfig, error) {
	name, title, secret := u.Provider, keyedTitle(u.Provider), keyedSecretName(u.Provider)
	actor, err := parseActor(u.ActorID)
	if err != nil {
		return GatewayConfig{}, err
	}
	apiKey, proxy := keyedKey(name, u.APIKey), strings.TrimSpace(u.Proxy)
	switch {
	case u.ClearProxy && proxy != "":
		return GatewayConfig{}, invalid("set a proxy or clear it, not both")
	case u.ClearAPIKey && apiKey != "":
		return GatewayConfig{}, invalid("set the %s or clear it, not both", secret)
	case u.MerchantID != "" || u.Address != "":
		return GatewayConfig{}, invalid("%s has an API key only: no merchant id and no address", title)
	case u.Sandbox && name == providerAqayepardakht:
		return GatewayConfig{}, invalid("%s has no sandbox switch: its test system is the %s %q", title, secret, aqayepardakhtSandboxPIN)
	}

	s.gwMu.Lock()
	defer s.gwMu.Unlock()
	cur := s.gws.Load()
	o := cur.opts
	if o == nil || o.Envelope == nil || o.Keyed[name].Build == nil {
		return GatewayConfig{}, ErrKeyMissing
	}
	prev := cur.keyed[name]
	old := prev.settings
	if prev.problem == ProblemUnreadable {
		// The stored secrets are lost: start again from the request.
		old = KeySettings{Sandbox: old.Sandbox}
		if apiKey == "" {
			return GatewayConfig{}, invalid("enter the %s again: the stored one cannot be decrypted", secret)
		}
	}
	next := old
	switch {
	case u.ClearAPIKey:
		next.APIKey = ""
	case apiKey != "":
		next.APIKey = apiKey
	}
	switch {
	case u.ClearProxy:
		next.Proxy = ""
	case proxy != "":
		next.Proxy = proxy
	}
	next.Sandbox = u.Sandbox
	if name == providerAqayepardakht {
		next.Sandbox = keyedSandbox(name, next)
	}
	switch {
	case next.Sandbox && o.Env == "prod":
		if name == providerAqayepardakht {
			return GatewayConfig{}, invalid("the %s sandbox (the %s %q) is refused when BOBRES_ENV=prod", title, secret, aqayepardakhtSandboxPIN)
		}
		return GatewayConfig{}, invalid("the %s sandbox is refused when BOBRES_ENV=prod", title)
	case u.Enabled && next.APIKey == "":
		return GatewayConfig{}, invalid("set the %s before switching %s on", secret, title)
	case u.Enabled && o.PublicURLErr != nil:
		return GatewayConfig{}, precondition("%s", o.PublicURLErr.Error())
	}
	if next.APIKey != "" {
		if _, err := o.Keyed[name].Build(next); err != nil {
			return GatewayConfig{}, invalid("%s", err.Error())
		}
	}
	row, err := encryptKeyed(o.Envelope, name, next)
	if err != nil {
		return GatewayConfig{}, err
	}
	row.Enabled, row.UpdatedBy = u.Enabled, actor
	// Open payments pin the key and the sandbox while a client can check them
	// (see SetGatewayConfig for Zarinpal).
	routeChanged := next.APIKey != old.APIKey || next.Sandbox != old.Sandbox
	pinned := routeChanged && cur.configured[name] != nil
	var saved *store.GatewayConfig
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if pinned {
			n, _, err := s.st.OpenIntents(ctx, tx, name)
			if err != nil {
				return err
			}
			if n > 0 {
				return &OpenIntentsError{Provider: name, Count: n}
			}
		}
		saved, err = s.st.SaveGatewayConfig(ctx, tx, row)
		return err
	})
	if err != nil {
		return GatewayConfig{}, err
	}
	ks := keyedState{settings: next, enabled: saved.Enabled, updatedAt: saved.UpdatedAt}
	if saved.UpdatedBy != nil {
		ks.updatedBy = *saved.UpdatedBy
	}
	set := cur.clone()
	set.applyKeyed(o, name, ks)
	s.gws.Store(set)
	if p := cur.configured[name]; p != nil && p != set.configured[name] {
		closeIdle(p) // checks still running on it keep their connection
	}
	s.log.Info(title+" settings changed", "actor", ks.updatedBy, "enabled", ks.enabled,
		"api_key_changed", next.APIKey != old.APIKey, "proxy_changed", next.Proxy != old.Proxy, "settings", next)
	return set.keyedView(name), nil
}

// testKeyed is TestGateway for IDPay, NextPay and Aqayepardakht.
func (s *Service) testKeyed(ctx context.Context, name string, cand *GatewayCandidate) (GatewayTest, error) {
	title, secret := keyedTitle(name), keyedSecretName(name)
	set := s.gws.Load()
	g := set.configured[name]
	if cand != nil {
		o := set.opts
		if o == nil || o.Keyed[name].Build == nil {
			return GatewayTest{}, precondition("gateway settings are not loaded")
		}
		ks := set.keyed[name]
		k := ks.settings
		if ks.problem == ProblemUnreadable {
			k = KeySettings{}
		}
		if a := keyedKey(name, cand.APIKey); a != "" {
			k.APIKey = a
		}
		proxy := strings.TrimSpace(cand.Proxy)
		switch {
		case cand.ClearProxy && proxy != "":
			return GatewayTest{}, invalid("set a proxy or clear it, not both")
		case cand.ClearProxy:
			k.Proxy = ""
		case proxy != "":
			k.Proxy = proxy
		}
		k.Sandbox = cand.Sandbox
		if name == providerAqayepardakht {
			if cand.Sandbox {
				return GatewayTest{}, invalid("%s has no sandbox switch: its test system is the %s %q", title, secret, aqayepardakhtSandboxPIN)
			}
			k.Sandbox = keyedSandbox(name, k)
		}
		switch {
		case k.Sandbox && o.Env == "prod":
			if name == providerAqayepardakht {
				return GatewayTest{}, invalid("the %s sandbox (the %s %q) is refused when BOBRES_ENV=prod", title, secret, aqayepardakhtSandboxPIN)
			}
			return GatewayTest{}, invalid("the %s sandbox is refused when BOBRES_ENV=prod", title)
		case k.APIKey == "":
			return GatewayTest{}, invalid("enter the %s to test", secret)
		}
		built, err := o.Keyed[name].Build(k)
		if err != nil {
			return GatewayTest{}, invalid("%s", err.Error())
		}
		defer closeIdle(built)
		g = built
	}
	if g == nil {
		return GatewayTest{}, precondition("%s has no usable %s to test", title, secret)
	}
	return s.runGatewayTest(ctx, name, g)
}
