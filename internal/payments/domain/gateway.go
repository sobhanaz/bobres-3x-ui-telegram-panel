package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

// Reasons on payment.rejected.v1 for automated gateways (the bot shows them).
const (
	ReasonGatewayFailed  = "gateway_failed"
	ReasonAmountMismatch = "amount_mismatch"
	ReasonExpired        = "expired"
)

// ErrUnavailable: the gateway could not be reached or refused to create the payment.
var ErrUnavailable = errors.New("payments: payment gateway unavailable")

// gatewayPayWindow: how long a gateway payment link may be used before the
// intent expires (a payment completed later still settles, see ApplyGatewayResult).
const gatewayPayWindow = time.Hour

// checkGap: user-triggered checks ask the gateway at most this often per intent,
// across every path and replica (stored in checked_at).
const checkGap = 3 * time.Second

// expiryGrace: an open intent expires only this long after its pay window,
// longer than a gateway session, so a payment started just before the window
// closed is still seen by the reconciler (the pay page stops offering the
// link at ExpiresAt).
const expiryGrace = 45 * time.Minute

// reopenWindow: how long after creation a closed intent may still be settled
// by a confirmed payment that was missed (lost return, expiry passed).
const reopenWindow = 48 * time.Hour

// reopenable: a closed intent a confirmed payment may still settle.
func reopenable(in *store.Intent) bool {
	if in.Status != "failed" && in.Status != "expired" {
		return false
	}
	if in.FailureReason != nil && (*in.FailureReason == ReasonAmountMismatch || *in.FailureReason == "reversed") {
		return false
	}
	return time.Since(in.CreatedAt) < reopenWindow
}

// gatewayCurrency is the only currency each automated provider is charged in.
var gatewayCurrency = map[string]string{"zarinpal": "IRR", "stars": "XTR"}

// SetGateways attaches the configured automated providers by name.
func (s *Service) SetGateways(gws ...gateway.Gateway) {
	s.gws = map[string]gateway.Gateway{}
	for _, g := range gws {
		if g != nil {
			s.gws[g.Name()] = g
		}
	}
}

// SetLogger enables logging of gateway problems (no secrets are logged).
func (s *Service) SetLogger(l *slog.Logger) {
	if l != nil {
		s.log = l
	}
}

// Gateway returns a configured provider, or nil.
func (s *Service) Gateway(name string) gateway.Gateway { return s.gws[name] }

// Gateways lists the automated providers usable on this install. Telegram
// Stars needs no credentials, so it is always listed; core decides whether to
// offer it (it needs a Stars price).
func (s *Service) Gateways() []string {
	out := []string{Stars}
	for _, name := range []string{"zarinpal"} {
		if s.gws[name] != nil {
			out = append(out, name)
		}
	}
	return out
}

func chargeOf(in *store.Intent) gateway.Charge {
	c := gateway.Charge{IntentID: in.ID}
	if in.GatewayAmount != nil {
		c.Amount = *in.GatewayAmount
	}
	if in.GatewayCurrency != nil {
		c.Currency = *in.GatewayCurrency
	}
	if in.ExpiresAt != nil {
		c.ExpiresAt = *in.ExpiresAt
	}
	return c
}

// StartGateway creates the intent (idempotent per key) and the payment at the
// gateway, and returns the intent with its external id and pay URL. A retry with
// the same key returns the payment already created.
func (s *Service) StartGateway(ctx context.Context, in *store.Intent, description, callbackURL string) (*store.Intent, error) {
	g := s.gws[in.Provider]
	if g == nil {
		return nil, invalid("payment method %q is not enabled", in.Provider)
	}
	if in.GatewayAmount == nil || *in.GatewayAmount <= 0 || in.GatewayCurrency == nil || *in.GatewayCurrency != gatewayCurrency[in.Provider] {
		return nil, invalid("%s needs a positive amount in %s", in.Provider, gatewayCurrency[in.Provider])
	}
	if in.ExpiresAt == nil {
		exp := time.Now().Add(gatewayPayWindow)
		in.ExpiresAt = &exp
	}
	created, err := s.createIntent(ctx, in)
	if err != nil {
		return nil, err
	}
	if created.ExternalID != nil || created.Status != "pending" {
		return created, nil
	}
	c := chargeOf(created)
	c.Description, c.CallbackURL = description, callbackURL
	started, err := g.Create(ctx, c)
	if err != nil {
		s.log.Warn("gateway create failed", "provider", in.Provider, "intent", created.ID, "err", err)
		_ = s.st.RecordGatewayEvent(ctx, nil, created.ID, in.Provider, "create", "error", map[string]any{"error": err.Error()})
		return nil, fmt.Errorf("%w: %s: %w", ErrUnavailable, in.Provider, err)
	}
	var out *store.Intent
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.st.SetGatewayStart(ctx, tx, created.ID, started.ExternalID, started.PayURL)
		if errors.Is(err, store.ErrInvalidTransition) {
			// A concurrent retry started it first; the payment created here is
			// never shown to anyone and expires at the gateway.
			got, err = s.st.GetIntent(ctx, tx, created.ID)
		}
		if err != nil {
			return err
		}
		out = got
		return s.st.RecordGatewayEvent(ctx, tx, created.ID, in.Provider, "create", "pending",
			map[string]any{"external_id": started.ExternalID})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ApplyGatewayResult settles an intent from a gateway's report. It is
// idempotent: a settled intent is returned as is, and the compare-and-set on the
// status means concurrent reports (callback and reconciler) settle it once.
func (s *Service) ApplyGatewayResult(ctx context.Context, in *store.Intent, r gateway.Result, kind string) (*store.Intent, error) {
	return s.applyResult(ctx, in, r, kind, false)
}

func (s *Service) applyResult(ctx context.Context, in *store.Intent, r gateway.Result, kind string, countAttempt bool) (*store.Intent, error) {
	switch in.Status {
	case "succeeded":
		return in, nil
	case "failed", "expired":
		// Only a confirmed payment changes a closed intent (a lost return, our
		// expiry passed, a concurrent check failed it first): the gateway took
		// the money. Amount mismatches and reversed payments stay failed.
		if r.State != gateway.Paid || !reopenable(in) {
			return in, nil
		}
	}
	switch r.State {
	case gateway.Paid:
		want := chargeOf(in)
		if (r.Amount != 0 && r.Amount != want.Amount) || (r.Currency != "" && r.Currency != want.Currency) {
			s.log.Warn("gateway amount mismatch", "provider", in.Provider, "intent", in.ID,
				"want", want.Amount, "want_currency", want.Currency, "got", r.Amount, "got_currency", r.Currency)
			return s.failGateway(ctx, in, "failed", ReasonAmountMismatch, kind,
				map[string]any{"want": want.Amount, "got": r.Amount, "currency": r.Currency, "reference": r.Reference})
		}
		return s.settlePaid(ctx, in, r, kind)
	case gateway.Failed:
		reason := r.Reason
		if reason == "" {
			reason = ReasonGatewayFailed
		}
		return s.failGateway(ctx, in, "failed", reason, kind, map[string]any{"reason": reason})
	default:
		if in.ExpiresAt != nil && time.Now().After(in.ExpiresAt.Add(expiryGrace)) {
			return s.failGateway(ctx, in, "expired", ReasonExpired, kind, nil)
		}
		return in, nil
	}
}

func (s *Service) settlePaid(ctx context.Context, in *store.Intent, r gateway.Result, kind string) (*store.Intent, error) {
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		ext := ""
		if in.ExternalID != nil {
			ext = *in.ExternalID
		}
		got, err := s.st.SettleGatewayPaid(ctx, tx, in.ID, ext, r.Reference)
		if errors.Is(err, store.ErrInvalidTransition) {
			out, err = s.st.GetIntent(ctx, tx, in.ID) // settled concurrently
			return err
		}
		if err != nil {
			return err
		}
		if err := s.st.AppendLedgerCredit(ctx, tx, got.UserID, got.Currency, got.Amount,
			"intent", got.ID, "gateway:"+got.ID); err != nil {
			return err
		}
		if err := s.st.RecordGatewayEvent(ctx, tx, got.ID, got.Provider, kind, "paid",
			map[string]any{"reference": r.Reference, "amount": r.Amount}); err != nil {
			return err
		}
		out = got
		return s.publishOutcome(ctx, tx, got, events.PaymentsPaymentSucceeded, "")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) failGateway(ctx context.Context, in *store.Intent, status, reason, kind string, detail map[string]any) (*store.Intent, error) {
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.st.FailGateway(ctx, tx, in.ID, status, reason)
		if errors.Is(err, store.ErrInvalidTransition) {
			out, err = s.st.GetIntent(ctx, tx, in.ID)
			return err
		}
		if err != nil {
			return err
		}
		outcome := "failed"
		if reason == ReasonAmountMismatch {
			outcome = "rejected"
		}
		if err := s.st.RecordGatewayEvent(ctx, tx, got.ID, got.Provider, kind, outcome, detail); err != nil {
			return err
		}
		out = got
		return s.publishOutcome(ctx, tx, got, events.PaymentsPaymentRejected, reason)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// publishOutcome writes payment.succeeded / payment.rejected in the settling
// transaction (core applies each intent exactly once).
func (s *Service) publishOutcome(ctx context.Context, tx pgx.Tx, in *store.Intent, topic, reason string) error {
	e := events.PaymentEvent{
		IntentID: in.ID, UserID: in.UserID, Provider: in.Provider,
		Amount: in.Amount, Currency: in.Currency, Reason: reason,
	}
	if in.OrderID != nil {
		e.OrderID = *in.OrderID
	}
	_, err := s.ob.Publish(ctx, tx, topic, e)
	return err
}

type checkOpts struct {
	reconciler bool // the reconciler: open intents only, no gap, counts towards its backoff
	fromReturn bool // the gateway's return URL: an open intent skips the gap (a one-off signal)
}

// CheckIntent asks the gateway about an intent now (the customer tapped "check
// payment") and applies the answer: an open intent, or a closed one a missed
// payment may still settle (reopenable). The user id, when given, must own it.
func (s *Service) CheckIntent(ctx context.Context, userID, intentID, kind string) (*store.Intent, error) {
	return s.check(ctx, userID, intentID, kind, checkOpts{})
}

// CheckAfterReturn is the gateway's return URL. Nothing in the query is
// trusted: the gateway is asked. An open intent is verified even right after a
// "check payment" press; a closed (reopenable) one only behind the gap.
func (s *Service) CheckAfterReturn(ctx context.Context, intentID string) (*store.Intent, error) {
	return s.check(ctx, "", intentID, "callback", checkOpts{fromReturn: true})
}

func (s *Service) check(ctx context.Context, userID, intentID, kind string, o checkOpts) (*store.Intent, error) {
	in, err := s.st.GetIntent(ctx, nil, intentID)
	if err != nil {
		return nil, err
	}
	if userID != "" && in.UserID != userID {
		return nil, ErrForbidden
	}
	open := in.Status == "pending" || in.Status == "confirming"
	switch {
	case in.Status == "succeeded":
		return in, nil
	case !open && (o.reconciler || !reopenable(in)):
		return in, nil
	}
	g := s.gws[in.Provider]
	if g == nil || in.ExternalID == nil {
		return in, nil
	}
	gap := !o.reconciler && (!o.fromReturn || !open)
	if gap && in.CheckedAt != nil && time.Since(*in.CheckedAt) < checkGap {
		return in, nil // asked a moment ago: answer from what we know
	}
	// Every gateway call is recorded first, so the gap and the backoff see it.
	if err := s.st.TouchCheck(ctx, in.ID, o.reconciler); err != nil {
		return nil, err
	}
	r, err := g.Check(ctx, *in.ExternalID, chargeOf(in))
	if err != nil {
		s.log.Warn("gateway check failed", "provider", in.Provider, "intent", in.ID, "err", err)
		return in, nil
	}
	return s.applyResult(ctx, in, r, kind, o.reconciler)
}

// ReconcileOnce checks the open gateway intents that are due: a fresh intent
// every interval, backing off exponentially (up to 64 intervals) as checks
// accumulate. It returns how many intents it looked at.
func (s *Service) ReconcileOnce(ctx context.Context, interval time.Duration) (int, error) {
	due, err := s.st.DueGatewayIntents(ctx, interval, 50)
	if err != nil {
		return 0, err
	}
	for _, in := range due {
		if ctx.Err() != nil {
			break
		}
		if _, err := s.check(ctx, "", in.ID, "check", checkOpts{reconciler: true}); err != nil {
			s.log.Warn("reconcile intent", "intent", in.ID, "err", err)
		}
	}
	return len(due), nil
}

// RunReconciler runs ReconcileOnce every interval until ctx ends. It covers
// lost callbacks, customers who close the browser before returning, and
// installs whose gateway cannot reach a public webhook URL.
func (s *Service) RunReconciler(ctx context.Context, interval time.Duration) error {
	if len(s.gws) == 0 {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.ReconcileOnce(ctx, interval); err != nil && ctx.Err() == nil {
			s.log.Warn("reconciler", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
