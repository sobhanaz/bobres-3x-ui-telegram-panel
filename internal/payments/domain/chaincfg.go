package domain

// Automatic USDT (TRC20) settings: the owner switches the watcher on and off
// and sets the store's TRON address, an optional TronGrid API key and an
// optional proxy from the dashboard, through core. All three are stored
// encrypted with the payments master key; staff see the address (customers
// are shown it anyway) and whether a key and a proxy are set, never their
// values.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/trongrid"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// USDTAuto is the automatic USDT (TRC20) provider.
const USDTAuto = "usdt_trc20_auto"

// ChainSettings are the USDT watcher's address and route. The API key and
// the proxy are secret: String and LogValue print only which ones are set.
type ChainSettings struct {
	Address string // the store's TRON address (T + 33 base58 characters)
	APIKey  string // TronGrid's TRON-PRO-API-KEY, optional
	Proxy   string // an http(s) proxy URL, optional; it may carry a password
}

// LogValue implements slog.LogValuer without the secrets.
func (c ChainSettings) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("address_set", c.Address != ""), slog.Bool("api_key_set", c.APIKey != ""),
		slog.Bool("proxy_set", c.Proxy != ""))
}

// String keeps %v and %+v from printing the secrets.
func (c ChainSettings) String() string { return c.LogValue().String() }

// GoString keeps %#v from printing the secrets.
func (c ChainSettings) GoString() string { return c.String() }

// chainState is one snapshot of the USDT watcher's settings. It is never
// changed after it is stored: a settings change stores a new one.
type chainState struct {
	settings  ChainSettings // in memory only; the address is shown, the rest never
	enabled   bool
	problem   string
	client    gateway.ChainSource // nil = the chain cannot be read
	updatedAt time.Time
	updatedBy string
	opts      *GatewayOptions
}

// readable: the watcher can read the chain (the payments already open keep
// being checked, even while switched off).
func (c *chainState) readable() bool { return c.client != nil && c.problem == "" }

// offered: new payments can use it.
func (c *chainState) offered() bool { return c.enabled && c.readable() && c.settings.Address != "" }

// loadChain reads the stored settings and builds the snapshot they describe.
func (s *Service) loadChain(ctx context.Context, o *GatewayOptions) (*chainState, error) {
	row, err := s.st.GetGatewayConfig(ctx, nil, USDTAuto)
	if errors.Is(err, store.ErrNotFound) {
		return &chainState{opts: o}, nil
	}
	if err != nil {
		return nil, err
	}
	cs := &chainState{enabled: row.Enabled, updatedAt: row.UpdatedAt, opts: o}
	if row.UpdatedBy != nil {
		cs.updatedBy = *row.UpdatedBy
	}
	if o.Envelope == nil {
		cs.problem = ProblemUnreadable
		return cs, nil
	}
	settings, err := decryptChain(o.Envelope, row)
	if err != nil {
		cs.problem = ProblemUnreadable
		return cs, nil
	}
	cs.settings = settings
	cs.build()
	return cs, nil
}

// build makes the TronGrid client for the snapshot's settings.
func (c *chainState) build() {
	if c.opts == nil || c.opts.BuildChain == nil || c.settings.Address == "" {
		return
	}
	cl, err := c.opts.BuildChain(c.settings)
	if err != nil {
		c.problem = ProblemInvalid
		return
	}
	c.client = cl
}

func encryptChain(env *bcrypto.Envelope, c ChainSettings) (*store.GatewayConfig, error) {
	row := &store.GatewayConfig{Provider: USDTAuto}
	for _, f := range []struct {
		plain string
		enc   *[]byte
	}{{c.Address, &row.AddressEnc}, {c.APIKey, &row.APIKeyEnc}, {c.Proxy, &row.ProxyEnc}} {
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

func decryptChain(env *bcrypto.Envelope, row *store.GatewayConfig) (ChainSettings, error) {
	var c ChainSettings
	for _, f := range []struct {
		enc   []byte
		plain *string
	}{{row.AddressEnc, &c.Address}, {row.APIKeyEnc, &c.APIKey}, {row.ProxyEnc, &c.Proxy}} {
		if f.enc == nil {
			continue
		}
		b, err := env.Decrypt(f.enc)
		if err != nil {
			return ChainSettings{}, err
		}
		*f.plain = string(b)
	}
	return c, nil
}

// view is the staff view of the USDT watcher's settings.
func (c *chainState) view() GatewayConfig {
	return GatewayConfig{
		Provider: USDTAuto, Enabled: c.enabled, Configured: c.readable() && c.settings.Address != "",
		Offered: c.offered(), Address: c.settings.Address, APIKeySet: c.settings.APIKey != "",
		ProxySet: c.settings.Proxy != "", KeyMissing: c.opts == nil || c.opts.Envelope == nil,
		Problem: c.problem, UpdatedAt: c.updatedAt, UpdatedBy: c.updatedBy,
	}
}

// chainInput validates the address, API key and proxy of a request against
// the current settings and returns the settings it asks for.
func chainInput(old ChainSettings, address, apiKey string, clearKey bool, proxy string, clearProxy bool) (ChainSettings, error) {
	address, apiKey, proxy = strings.TrimSpace(address), strings.TrimSpace(apiKey), strings.TrimSpace(proxy)
	switch {
	case clearProxy && proxy != "":
		return ChainSettings{}, invalid("set a proxy or clear it, not both")
	case clearKey && apiKey != "":
		return ChainSettings{}, invalid("set an API key or clear it, not both")
	case address != "" && !trongrid.ValidAddress(address):
		return ChainSettings{}, invalid("the address must be a TRON (TRC20) address: T and 33 more letters and digits, as your wallet shows it")
	case address == trongrid.USDTContract:
		return ChainSettings{}, invalid("that is the USDT contract's address, not a wallet's")
	case apiKey != "" && !trongrid.ValidAPIKey(apiKey):
		return ChainSettings{}, invalid("the API key must be the key from the TronGrid dashboard")
	}
	next := old
	if address != "" {
		next.Address = address
	}
	switch {
	case clearKey:
		next.APIKey = ""
	case apiKey != "":
		next.APIKey = apiKey
	}
	switch {
	case clearProxy:
		next.Proxy = ""
	case proxy != "":
		next.Proxy = proxy
	}
	return next, nil
}

// setChainConfig validates, stores and applies the USDT watcher's settings.
// Switching off only stops offering it: the payments already open keep being
// watched. The address cannot change while payments to another address are
// open: their customers were told to pay there.
func (s *Service) setChainConfig(ctx context.Context, u GatewayUpdate) (GatewayConfig, error) {
	var actor *string
	if u.ActorID != "" {
		id, err := buuid.Parse(u.ActorID)
		if err != nil {
			return GatewayConfig{}, invalid("actor id must be a user id")
		}
		a := id.String()
		actor = &a
	}
	if u.Sandbox || u.MerchantID != "" {
		return GatewayConfig{}, invalid("%s has no sandbox and no merchant id", USDTAuto)
	}

	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	cur := s.chain.Load()
	o := cur.opts
	switch {
	case o == nil || o.BuildChain == nil:
		return GatewayConfig{}, precondition("the automatic USDT watcher is not available on this install")
	case o.Envelope == nil:
		return GatewayConfig{}, ErrKeyMissing
	}
	old := cur.settings
	if cur.problem == ProblemUnreadable {
		// The stored secrets are lost: start again from the request.
		old = ChainSettings{}
		if strings.TrimSpace(u.Address) == "" {
			return GatewayConfig{}, invalid("enter the address again: the stored one cannot be decrypted")
		}
	}
	next, err := chainInput(old, u.Address, u.APIKey, u.ClearAPIKey, u.Proxy, u.ClearProxy)
	if err != nil {
		return GatewayConfig{}, err
	}
	if u.Enabled && next.Address == "" {
		return GatewayConfig{}, invalid("set the TRC20 address before switching automatic USDT on")
	}
	var cl gateway.ChainSource
	if next.Address != "" {
		if cl, err = o.BuildChain(next); err != nil {
			return GatewayConfig{}, invalid("%s", err.Error())
		}
	}
	row, err := encryptChain(o.Envelope, next)
	if err != nil {
		closeIdle(cl)
		return GatewayConfig{}, err
	}
	row.Enabled, row.UpdatedBy = u.Enabled, actor
	switchedOn := u.Enabled && (!cur.enabled || next.Address != old.Address)
	var saved *store.GatewayConfig
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if next.Address != old.Address || cur.problem == ProblemUnreadable {
			n, err := s.st.OpenChainIntentsElsewhere(ctx, tx, next.Address)
			if err != nil {
				return err
			}
			if n > 0 {
				return &OpenIntentsError{Provider: USDTAuto, Count: n}
			}
		}
		if switchedOn {
			if err := s.st.SkipChainHistory(ctx, tx, next.Address, time.Now().Add(-chainSkew), reopenWindow+chainSkew); err != nil {
				return err
			}
		}
		saved, err = s.st.SaveGatewayConfig(ctx, tx, row)
		return err
	})
	if err != nil {
		closeIdle(cl)
		return GatewayConfig{}, err
	}
	cs := &chainState{settings: next, enabled: saved.Enabled, client: cl, updatedAt: saved.UpdatedAt, opts: o}
	if saved.UpdatedBy != nil {
		cs.updatedBy = *saved.UpdatedBy
	}
	s.chain.Store(cs)
	if prev := cur.client; prev != nil {
		closeIdle(prev) // reads still running on it keep their connection
	}
	s.log.Info("USDT watcher settings changed", "actor", cs.updatedBy, "enabled", cs.enabled,
		"address_changed", next.Address != old.Address, "api_key_changed", next.APIKey != old.APIKey,
		"proxy_changed", next.Proxy != old.Proxy, "settings", next)
	return cs.view(), nil
}

// testChain reads the chain once with the stored settings, or a candidate
// built the same way. Nothing is stored or settled.
func (s *Service) testChain(ctx context.Context, cand *GatewayCandidate) (GatewayTest, error) {
	cur := s.chain.Load()
	client, settings := cur.client, cur.settings
	if cur.problem == ProblemUnreadable {
		client, settings = nil, ChainSettings{}
	}
	if cand != nil {
		o := cur.opts
		if o == nil || o.BuildChain == nil {
			return GatewayTest{}, precondition("the automatic USDT watcher is not available on this install")
		}
		if cand.Sandbox || cand.MerchantID != "" {
			return GatewayTest{}, invalid("%s has no sandbox and no merchant id", USDTAuto)
		}
		next, err := chainInput(settings, cand.Address, cand.APIKey, cand.ClearAPIKey, cand.Proxy, cand.ClearProxy)
		if err != nil {
			return GatewayTest{}, err
		}
		if next.Address == "" {
			return GatewayTest{}, invalid("enter the TRC20 address to test")
		}
		built, err := o.BuildChain(next)
		if err != nil {
			return GatewayTest{}, invalid("%s", err.Error())
		}
		defer closeIdle(built)
		client, settings = built, next
	}
	if client == nil || settings.Address == "" {
		return GatewayTest{}, precondition("automatic USDT has no usable address to test")
	}
	ctx, cancel := context.WithTimeout(ctx, gatewayTestTimeout)
	defer cancel()
	start := time.Now()
	_, err := client.Transfers(ctx, gateway.ChainQuery{Address: settings.Address, Token: trongrid.USDTContract,
		Since: start.Add(-time.Hour), Limit: 1})
	out := GatewayTest{Latency: time.Since(start)}
	if err != nil {
		s.log.Info("gateway test failed", "provider", USDTAuto, "err", err)
		out.Detail = err.Error()
		return out, nil
	}
	out.OK = true
	return out, nil
}
