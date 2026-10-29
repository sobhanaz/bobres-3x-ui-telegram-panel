package domain

// Automatic TON settings: the owner switches the watcher on and off and sets the store's TON address, an
// optional TON Center API key and an optional proxy from the dashboard, through core. All three are stored
// encrypted with the payments master key; staff see the address (customers are shown it anyway) and whether a key
// and a proxy are set, never their values. The address is public data: nothing here ever holds a private key.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/toncenter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonaddr"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// TONAuto is the automatic TON (Toncoin) provider.
const TONAuto = "ton_auto"

// TONSettings are the TON watcher's address and route. The API key and the proxy are secret: String and
// LogValue print only which ones are set.
type TONSettings struct {
	Address string // the store's address, in the canonical (non-bounceable) form
	APIKey  string // TON Center's key, optional
	Proxy   string // an http(s) proxy URL, optional; it may carry a password
}

// LogValue implements slog.LogValuer without the secrets.
func (c TONSettings) LogValue() slog.Value {
	return slog.GroupValue(slog.Bool("address_set", c.Address != ""), slog.Bool("api_key_set", c.APIKey != ""),
		slog.Bool("proxy_set", c.Proxy != ""))
}

// String keeps %v and %+v from printing the secrets.
func (c TONSettings) String() string { return c.LogValue().String() }

// GoString keeps %#v from printing the secrets.
func (c TONSettings) GoString() string { return c.String() }

// tonState is one snapshot of the TON watcher's settings. It is never changed after it is stored: a settings
// change stores a new one.
type tonState struct {
	settings  TONSettings
	enabled   bool
	problem   string
	client    gateway.TONSource // nil = the chain cannot be read
	updatedAt time.Time
	updatedBy string
	opts      *GatewayOptions
}

// listed: this install has the TON watcher built in (cmd/payments wired it).
func (c *tonState) listed() bool { return c.opts != nil && c.opts.BuildTON != nil }

// readable: the watcher can read the chain (the payments already open keep being checked, even while switched
// off).
func (c *tonState) readable() bool { return c.client != nil && c.problem == "" }

func (s *Service) loadTON(ctx context.Context, o *GatewayOptions) (*tonState, error) {
	row, err := s.st.GetGatewayConfig(ctx, nil, TONAuto)
	if errors.Is(err, store.ErrNotFound) {
		return &tonState{opts: o}, nil
	}
	if err != nil {
		return nil, err
	}
	ts := &tonState{enabled: row.Enabled, updatedAt: row.UpdatedAt, opts: o}
	if row.UpdatedBy != nil {
		ts.updatedBy = *row.UpdatedBy
	}
	if o.Envelope == nil {
		ts.problem = ProblemUnreadable
		return ts, nil
	}
	settings, err := decryptTON(o.Envelope, row)
	if err != nil {
		ts.problem = ProblemUnreadable
		return ts, nil
	}
	ts.settings = settings
	if o.BuildTON != nil && settings.Address != "" {
		cl, err := o.BuildTON(settings)
		if err != nil {
			ts.problem = ProblemInvalid
			return ts, nil
		}
		ts.client = cl
	}
	return ts, nil
}

func encryptTON(env *bcrypto.Envelope, c TONSettings) (*store.GatewayConfig, error) {
	row := &store.GatewayConfig{Provider: TONAuto}
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

func decryptTON(env *bcrypto.Envelope, row *store.GatewayConfig) (TONSettings, error) {
	var c TONSettings
	for _, f := range []struct {
		enc   []byte
		plain *string
	}{{row.AddressEnc, &c.Address}, {row.APIKeyEnc, &c.APIKey}, {row.ProxyEnc, &c.Proxy}} {
		if f.enc == nil {
			continue
		}
		b, err := env.Decrypt(f.enc)
		if err != nil {
			return TONSettings{}, err
		}
		*f.plain = string(b)
	}
	return c, nil
}

// view is the staff view of the TON watcher's settings, with the price that decides whether customers are
// offered it.
func (s *Service) tonView() GatewayConfig {
	c := s.ton.Load()
	q, fresh := s.freshTONQuote()
	last := s.tonQ.Load()
	v := GatewayConfig{
		Provider: TONAuto, Enabled: c.enabled, Configured: c.readable() && c.settings.Address != "",
		Offered: s.tonOffered(), Address: c.settings.Address, APIKeySet: c.settings.APIKey != "",
		ProxySet: c.settings.Proxy != "", KeyMissing: c.opts == nil || c.opts.Envelope == nil,
		Problem: c.problem, UpdatedAt: c.updatedAt, UpdatedBy: c.updatedBy, PriceFresh: fresh,
	}
	if fresh {
		last = &q
	}
	if last != nil {
		v.PriceMicro, v.PriceSource, v.PriceAt = last.PriceMicro, last.Source, last.At
	}
	return v
}

// tonInput validates the address, API key and proxy of a request against the current settings and returns the
// settings it asks for. The address comes back in the canonical form.
func tonInput(old TONSettings, address, apiKey string, clearKey bool, proxy string, clearProxy bool) (TONSettings, error) {
	address, apiKey, proxy = strings.TrimSpace(address), strings.TrimSpace(apiKey), strings.TrimSpace(proxy)
	switch {
	case clearProxy && proxy != "":
		return TONSettings{}, invalid("set a proxy or clear it, not both")
	case clearKey && apiKey != "":
		return TONSettings{}, invalid("set an API key or clear it, not both")
	case apiKey != "" && !toncenter.ValidAPIKey(apiKey):
		return TONSettings{}, invalid("the API key must be the key from TON Center (letters, digits, - and _)")
	}
	next := old
	if address != "" {
		a, err := tonaddr.Normalize(address)
		if err != nil {
			return TONSettings{}, invalid("%s", strings.TrimPrefix(err.Error(), "tonaddr: "))
		}
		next.Address = a
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

// setTONConfig validates, stores and applies the TON watcher's settings. Switching off only stops offering it:
// the payments already open keep being watched. The address cannot change while payments to another address
// are open: their customers were told to pay there.
func (s *Service) setTONConfig(ctx context.Context, u GatewayUpdate) (GatewayConfig, error) {
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
		return GatewayConfig{}, invalid("%s has no sandbox and no merchant id", TONAuto)
	}

	s.tonMu.Lock()
	defer s.tonMu.Unlock()
	cur := s.ton.Load()
	o := cur.opts
	switch {
	case o == nil || o.BuildTON == nil:
		return GatewayConfig{}, precondition("automatic TON is not available on this install")
	case o.Envelope == nil:
		return GatewayConfig{}, ErrKeyMissing
	}
	old := cur.settings
	if cur.problem == ProblemUnreadable {
		// The stored secrets are lost: start again from the request.
		old = TONSettings{}
		if strings.TrimSpace(u.Address) == "" {
			return GatewayConfig{}, invalid("enter the address again: the stored one cannot be decrypted")
		}
	}
	next, err := tonInput(old, u.Address, u.APIKey, u.ClearAPIKey, u.Proxy, u.ClearProxy)
	if err != nil {
		return GatewayConfig{}, err
	}
	if u.Enabled && next.Address == "" {
		return GatewayConfig{}, invalid("set the TON address before switching automatic TON on")
	}
	var cl gateway.TONSource
	if next.Address != "" {
		if cl, err = o.BuildTON(next); err != nil {
			return GatewayConfig{}, invalid("%s", err.Error())
		}
	}
	row, err := encryptTON(o.Envelope, next)
	if err != nil {
		closeIdle(cl)
		return GatewayConfig{}, err
	}
	row.Enabled, row.UpdatedBy = u.Enabled, actor
	switchedOn := u.Enabled && (!cur.enabled || next.Address != old.Address)
	var saved *store.GatewayConfig
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if next.Address != old.Address || cur.problem == ProblemUnreadable {
			n, err := s.st.OpenTONIntentsElsewhere(ctx, tx, next.Address)
			if err != nil {
				return err
			}
			if n > 0 {
				return &OpenIntentsError{Provider: TONAuto, Count: n}
			}
		}
		if switchedOn {
			if err := s.st.SkipTONHistory(ctx, tx, next.Address, time.Now().Add(-tonSkew), reopenWindow+tonSkew); err != nil {
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
	ts := &tonState{settings: next, enabled: saved.Enabled, client: cl, updatedAt: saved.UpdatedAt, opts: o}
	if saved.UpdatedBy != nil {
		ts.updatedBy = *saved.UpdatedBy
	}
	s.ton.Store(ts)
	if prev := cur.client; prev != nil {
		closeIdle(prev) // reads still running on it keep their connection
	}
	s.log.Info("TON watcher settings changed", "actor", ts.updatedBy, "enabled", ts.enabled,
		"address_changed", next.Address != old.Address, "api_key_changed", next.APIKey != old.APIKey,
		"proxy_changed", next.Proxy != old.Proxy, "settings", next)
	return s.tonView(), nil
}

// testTON reads the address's newest transaction once with the stored settings, or a candidate built the same
// way, and says when it happened. Nothing is stored or settled.
func (s *Service) testTON(ctx context.Context, cand *GatewayCandidate) (GatewayTest, error) {
	cur := s.ton.Load()
	client, settings := cur.client, cur.settings
	if cur.problem == ProblemUnreadable {
		client, settings = nil, TONSettings{}
	}
	if cand != nil {
		o := cur.opts
		if o == nil || o.BuildTON == nil {
			return GatewayTest{}, precondition("automatic TON is not available on this install")
		}
		if cand.Sandbox || cand.MerchantID != "" {
			return GatewayTest{}, invalid("%s has no sandbox and no merchant id", TONAuto)
		}
		next, err := tonInput(settings, cand.Address, cand.APIKey, cand.ClearAPIKey, cand.Proxy, cand.ClearProxy)
		if err != nil {
			return GatewayTest{}, err
		}
		if next.Address == "" {
			return GatewayTest{}, invalid("enter the TON address to test")
		}
		built, err := o.BuildTON(next)
		if err != nil {
			return GatewayTest{}, invalid("%s", err.Error())
		}
		defer closeIdle(built)
		client, settings = built, next
	}
	if client == nil || settings.Address == "" {
		return GatewayTest{}, precondition("automatic TON has no usable address to test")
	}
	ctx, cancel := context.WithTimeout(ctx, gatewayTestTimeout)
	defer cancel()
	start := time.Now()
	txs, err := client.Transactions(ctx, gateway.TONQuery{Address: settings.Address, Limit: 1})
	out := GatewayTest{Latency: time.Since(start)}
	if err != nil {
		s.log.Info("gateway test failed", "provider", TONAuto, "err", err)
		out.Detail = err.Error()
		return out, nil
	}
	out.OK = true
	if len(txs) > 0 && txs[0].Time.Unix() > 0 {
		out.LatestTxAt = txs[0].Time.Unix()
	}
	return out, nil
}
