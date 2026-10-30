package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hosted"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// Hosted stores (docs/18-hosted-and-affiliate-plans.md, section 1): the customers' stores the owner installs and runs for them. tools/hosted
// installs one on a fresh server over SSH and prints a registration code; the owner pastes it into the dashboard's Hosted stores page, which
// keeps the register (customer, domain, version, license, channel, notes) and reads each store's /metrics through the bot (core has no
// internet access) to show whether it is up, the version it runs and a few figures.
//
// Custody: the register never holds a customer's bot token or backup key. The token of the store's /metrics is kept encrypted with core's
// master key and is write-only. A store's license is the "Hosted" product: a pro license made for it from this page.

// HostedTransport reads a hosted store's /metrics (botfiles.Client: the bot calls https://<domain>/metrics with the token and passes the
// answer back). status is the store's HTTP status.
type HostedTransport interface {
	FetchHostedMetrics(ctx context.Context, domain, token string) (status int, body []byte, err error)
}

// SetHostedTransport wires the bot as the way to hosted stores.
func (s *Service) SetHostedTransport(t HostedTransport) { s.cfg.HostedTransport = t }

const (
	// hostedEvery is how often the worker reads every hosted store.
	hostedEvery = 5 * time.Minute
	// hostedTimeout bounds one reading.
	hostedTimeout = 20 * time.Second
	// HostedLicenseTier is the tier of a "Hosted" license: everything, white label included.
	HostedLicenseTier = license.TierPro
)

// HostedInput is a store as the owner registers or edits it. Registration, when set, is a code from tools/hosted: it fills every field the
// input leaves empty. MetricsToken "" keeps the token there is (ClearToken forgets it).
type HostedInput struct {
	Registration string
	Customer     string
	Domain       string
	Version      string
	LicenseID    string
	Channel      string
	Notes        string
	MetricsToken string
	ClearToken   bool
}

func (in *HostedInput) merge() (*hosted.Registration, error) {
	r := &hosted.Registration{}
	if strings.TrimSpace(in.Registration) != "" {
		dec, err := hosted.Decode(in.Registration)
		if err != nil {
			return nil, &FieldError{"registration", err.Error()}
		}
		r = dec
	}
	for _, f := range []struct {
		dst *string
		v   string
	}{{&r.Customer, in.Customer}, {&r.Domain, in.Domain}, {&r.Version, in.Version}, {&r.LicenseID, in.LicenseID}, {&r.Channel, in.Channel},
		{&r.MetricsToken, in.MetricsToken}} {
		if v := strings.TrimSpace(f.v); v != "" {
			*f.dst = v
		}
	}
	if r.Channel == "" {
		r.Channel = "stable"
	}
	if err := r.Check(); err != nil {
		field, msg, _ := strings.Cut(err.Error(), ": ")
		return nil, &FieldError{field, msg}
	}
	return r, nil
}

func checkNotes(n string) (string, error) {
	n = strings.TrimSpace(n)
	if len(n) > 2000 || strings.IndexFunc(n, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		return "", &FieldError{"notes", "at most 2000 characters"}
	}
	return n, nil
}

func (s *Service) hostedToken(token string) ([]byte, error) {
	if token == "" {
		return nil, nil
	}
	if s.cfg.Secrets == nil {
		return nil, stateErr("core has no master key, so it cannot keep the metrics token")
	}
	return s.cfg.Secrets.Encrypt([]byte(token))
}

// ListHostedStores lists the register, newest first.
func (s *Service) ListHostedStores(ctx context.Context) ([]store.HostedStore, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, err
	}
	return s.st.ListHostedStores(ctx, s.st.Conn())
}

// AddHostedStore registers a store, audited (never with its token).
func (s *Service) AddHostedStore(ctx context.Context, actor *store.User, in HostedInput) (*store.HostedStore, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, err
	}
	r, err := in.merge()
	if err != nil {
		return nil, err
	}
	notes, err := checkNotes(in.Notes)
	if err != nil {
		return nil, err
	}
	enc, err := s.hostedToken(r.MetricsToken)
	if err != nil {
		return nil, err
	}
	h := &store.HostedStore{Customer: r.Customer, Domain: r.Domain, Version: r.Version, LicenseID: r.LicenseID, Channel: r.Channel, Notes: notes,
		MetricsTokenEnc: enc}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.CreateHostedStore(ctx, tx, h); err != nil {
			if errors.Is(err, store.ErrHostedDomainTaken) {
				return &FieldError{"domain", "a hosted store has this domain already"}
			}
			return err
		}
		return s.audit(ctx, tx, actor, "hosted.add", "hosted_store", h.ID, map[string]any{"customer": h.Customer, "domain": h.Domain,
			"version": h.Version, "license_id": h.LicenseID, "channel": h.Channel, "metrics_token": enc != nil}, "")
	})
	if err != nil {
		return nil, err
	}
	return h, nil
}

// UpdateHostedStore changes a store's customer, version, license id, channel and notes, and its metrics token when one is given (the domain
// stays: a store that moved is registered again).
func (s *Service) UpdateHostedStore(ctx context.Context, actor *store.User, id string, in HostedInput) (*store.HostedStore, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, err
	}
	notes, err := checkNotes(in.Notes)
	if err != nil {
		return nil, err
	}
	var out *store.HostedStore
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		h, err := s.st.GetHostedStore(ctx, tx, id)
		if err != nil {
			return err
		}
		in.Domain, in.Registration = h.Domain, ""
		if strings.TrimSpace(in.Customer) == "" {
			in.Customer = h.Customer
		}
		if strings.TrimSpace(in.Channel) == "" {
			in.Channel = h.Channel
		}
		r, err := in.merge()
		if err != nil {
			return err
		}
		h.Customer, h.Version, h.LicenseID, h.Channel, h.Notes = r.Customer, strings.TrimSpace(in.Version), strings.TrimSpace(in.LicenseID), r.Channel, notes
		if err := s.st.UpdateHostedStore(ctx, tx, h); err != nil {
			return err
		}
		token := "kept"
		switch {
		case in.ClearToken:
			token = "cleared"
			h.MetricsTokenEnc = nil
			if err := s.st.SetHostedMetricsToken(ctx, tx, h.ID, nil); err != nil {
				return err
			}
		case r.MetricsToken != "":
			token = "changed"
			if h.MetricsTokenEnc, err = s.hostedToken(r.MetricsToken); err != nil {
				return err
			}
			if err := s.st.SetHostedMetricsToken(ctx, tx, h.ID, h.MetricsTokenEnc); err != nil {
				return err
			}
		}
		out = h
		return s.audit(ctx, tx, actor, "hosted.update", "hosted_store", h.ID, map[string]any{"customer": h.Customer, "version": h.Version,
			"license_id": h.LicenseID, "channel": h.Channel, "metrics_token": token}, "")
	})
	return out, err
}

// DeleteHostedStore removes a store from the register (nothing happens to the store itself).
func (s *Service) DeleteHostedStore(ctx context.Context, actor *store.User, id string) error {
	if err := s.requirePrivate(ctx); err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		h, err := s.st.GetHostedStore(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.st.DeleteHostedStore(ctx, tx, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "hosted.delete", "hosted_store", id, map[string]any{"customer": h.Customer, "domain": h.Domain}, "")
	})
}

// CreateHostedLicense makes the "Hosted" license of a store (a pro license for months, customer "Hosted: <customer> (<domain>)") on the license
// server and records its id on the store. The answer carries the activation key, the only time it is shown: tools/hosted activates it on the
// store (--license-key-file). A store that has a license keeps it (make a new one only after clearing the id).
func (s *Service) CreateHostedLicense(ctx context.Context, actor *store.User, id string, months int, requestID string) (*VendorLicenseInfo, *store.HostedStore, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, nil, err
	}
	if months < 1 || months > 120 {
		return nil, nil, &FieldError{"months", "1 to 120"}
	}
	h, err := s.st.GetHostedStore(ctx, s.st.Conn(), id)
	if err != nil {
		return nil, nil, err
	}
	if h.LicenseID != "" {
		return nil, nil, stateErr("this store has a license already (%s)", h.LicenseID)
	}
	customer := fmt.Sprintf("Hosted: %s (%s)", h.Customer, h.Domain)
	if len(customer) > 200 {
		customer = customer[:200]
	}
	info, err := s.CreateServerLicense(ctx, actor, ServerLicenseCreate{Customer: customer, Tier: HostedLicenseTier, Months: months,
		RequestID: requestID}) // the dashboard's random id: a double click is one license
	if err != nil {
		return nil, nil, err
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		cur, err := s.st.GetHostedStore(ctx, tx, id)
		if err != nil {
			return err
		}
		cur.LicenseID = info.ID
		if err := s.st.UpdateHostedStore(ctx, tx, cur); err != nil {
			return err
		}
		h = cur
		return s.audit(ctx, tx, actor, "hosted.license", "hosted_store", id, map[string]any{"license_id": info.ID, "months": months}, "")
	})
	if err != nil {
		return nil, nil, err
	}
	return info, h, nil
}

// CheckHostedStore reads a store's /metrics now and records what it found.
func (s *Service) CheckHostedStore(ctx context.Context, id string) (*store.HostedStore, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, err
	}
	h, err := s.st.GetHostedStore(ctx, s.st.Conn(), id)
	if err != nil {
		return nil, err
	}
	s.readHosted(ctx, h)
	return s.st.GetHostedStore(ctx, s.st.Conn(), id)
}

// readHosted reads one store and records the reading (a store that cannot be read is down, with the reason; nothing is returned).
func (s *Service) readHosted(ctx context.Context, h *store.HostedStore) {
	now := s.cfg.vendorNow()
	up, version, figures, why := s.fetchHosted(ctx, h)
	var raw json.RawMessage
	if figures != nil {
		raw, _ = json.Marshal(figures)
	}
	if len(why) > 300 {
		why = why[:300]
	}
	if err := s.st.RecordHostedCheck(ctx, s.st.Conn(), h.ID, now, up, version, raw, why); err != nil {
		s.cfg.Log.Warn("hosted store: recording a check", "store", h.ID, "err", err)
	}
}

func (s *Service) fetchHosted(ctx context.Context, h *store.HostedStore) (up bool, version string, f *hosted.Figures, why string) {
	switch {
	case h.MetricsTokenEnc == nil:
		return false, "", nil, "no metrics token: set the store's token to read it"
	case s.cfg.Secrets == nil:
		return false, "", nil, "core has no master key"
	case s.cfg.HostedTransport == nil:
		return false, "", nil, "core has no way to reach the stores (the bot)"
	}
	token, err := s.cfg.Secrets.Decrypt(h.MetricsTokenEnc)
	if err != nil {
		return false, "", nil, "the stored token cannot be read"
	}
	cctx, cancel := context.WithTimeout(ctx, hostedTimeout)
	defer cancel()
	status, body, err := s.cfg.HostedTransport.FetchHostedMetrics(cctx, h.Domain, string(token))
	switch {
	case err != nil:
		return false, "", nil, err.Error()
	case status == http.StatusUnauthorized:
		return false, "", nil, "the store refused the metrics token (401): set the token again"
	case status == http.StatusNotFound:
		return false, "", nil, "the store's /metrics is off (404): make a token on the store (sudo bobres admin metrics-token)"
	case status != http.StatusOK:
		return false, "", nil, fmt.Sprintf("the store answered %d", status)
	}
	f, err = hosted.ParseMetrics(body)
	if err != nil {
		return false, "", nil, err.Error()
	}
	return true, f.Version, f, ""
}

// CheckHostedStores reads every store of the register that has a token (the worker's step). It returns how many it read.
func (s *Service) CheckHostedStores(ctx context.Context) (int, error) {
	if s.cfg.HostedTransport == nil || s.cfg.Secrets == nil || !s.PrivateFeatures(ctx) {
		return 0, nil
	}
	all, err := s.st.ListHostedStores(ctx, s.st.Conn())
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range all {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if all[i].MetricsTokenEnc == nil {
			continue
		}
		s.readHosted(ctx, &all[i])
		n++
	}
	return n, nil
}

// RunHostedWorker reads the hosted stores every five minutes until ctx ends.
func (s *Service) RunHostedWorker(ctx context.Context) {
	t := time.NewTicker(hostedEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.CheckHostedStores(ctx); err != nil && ctx.Err() == nil {
				s.cfg.Log.Warn("hosted stores", "err", err)
			}
		}
	}
}
