// Package domain holds payments business rules: intent creation, receipt
// submission, admin review with ledger credit + outbox events.
package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/provider"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

var (
	// ErrInvalid marks input that can never succeed as sent.
	ErrInvalid = errors.New("payments: invalid argument")
	// ErrForbidden: the intent belongs to someone else.
	ErrForbidden = errors.New("payments: forbidden")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

const (
	maxProofLen  = 256
	maxReasonLen = 500
)

// Service composes the payments store with business rules.
type Service struct {
	st *store.Store
	ob *eventbus.Outbox
	// gws is the current set of automated gateways, replaced whole when the
	// owner changes their settings; readers load it without locking.
	gws atomic.Pointer[gatewaySet]
	// gwMu is held shared while a gateway payment is being created and
	// exclusively while the settings change, so no payment starts with a
	// merchant id that is being replaced.
	gwMu sync.RWMutex
	// chain is the automatic USDT watcher's settings, replaced whole like gws;
	// chainMu is held shared while a payment is created and exclusively while
	// the settings change, so no payment starts at an address being replaced.
	chain   atomic.Pointer[chainState]
	chainMu sync.RWMutex
	// scanMu is held while the watcher reads the chain, and guards cutShort:
	// the addresses whose last read was cut short (its page limit, or a page
	// that failed).
	scanMu   sync.Mutex
	cutShort map[string]bool
	// hold is how long an unmatched deposit a manual proof may claim waits
	// before it is announced (nanoseconds; SetUnmatchedHold).
	hold atomic.Int64
	log  *slog.Logger
}

// New builds a Service publishing to the payments outbox.
func New(st *store.Store) *Service {
	s := &Service{st: st, ob: eventbus.NewOutbox("outbox_payments"), log: slog.New(slog.DiscardHandler), cutShort: map[string]bool{}}
	s.gws.Store(&gatewaySet{configured: map[string]gateway.Gateway{}, offered: map[string]bool{}, opts: &GatewayOptions{}})
	s.chain.Store(&chainState{})
	s.hold.Store(int64(chainUnmatchedHold))
	return s
}

// CreateIntent inserts a pending intent (idempotent per key) for the manual
// providers; automated gateways go through StartGateway.
func (s *Service) CreateIntent(ctx context.Context, in *store.Intent) (*store.Intent, error) {
	switch in.Provider {
	case provider.ManualCard, provider.ManualCrypto, provider.ManualZarinpal:
	default:
		return nil, invalid("unsupported provider %q", in.Provider)
	}
	return s.createIntent(ctx, in)
}

func (s *Service) createIntent(ctx context.Context, in *store.Intent) (*store.Intent, error) {
	if err := validIntent(in); err != nil {
		return nil, err
	}
	in.Status = "pending"
	return s.st.CreateIntent(ctx, nil, in)
}

// validIntent checks what every intent needs.
func validIntent(in *store.Intent) error {
	switch {
	case in.IdempotencyKey == "":
		return invalid("idempotency key required")
	case in.UserID == "":
		return invalid("user id required")
	case in.Amount <= 0:
		return invalid("amount must be positive")
	case money.Scale(in.Currency) < 0:
		return invalid("unsupported currency %q", in.Currency)
	}
	return nil
}

// SubmitReceipt stores a receipt screenshot for any manual payment and moves
// the intent to confirming for an admin to review. A card transfer also needs
// the bank's reference number (matched against the bank statement and flagged
// when it repeats); for a crypto transfer or a Zarinpal link payment the
// screenshot is the proof and a reference is optional.
func (s *Service) SubmitReceipt(ctx context.Context, userID, intentID, fileID, reference string) (*store.Intent, error) {
	fileID, reference = strings.TrimSpace(fileID), strings.TrimSpace(reference)
	if fileID == "" {
		return nil, invalid("receipt screenshot required")
	}
	if len(fileID) > maxProofLen || len(reference) > maxProofLen {
		return nil, invalid("receipt details too long")
	}
	return s.submit(ctx, userID, intentID, []string{provider.ManualCard, provider.ManualCrypto, provider.ManualZarinpal},
		func(in *store.Intent, r *store.Receipt) error {
			if in.Provider == provider.ManualCard && reference == "" {
				return invalid("the bank reference number is required for a card transfer")
			}
			r.ReceiptFile = &fileID
			if reference != "" {
				r.ReferenceNumber = &reference
			}
			return nil
		})
}

// SubmitTXID stores a crypto TXID and moves to confirming. A TXID can pay for
// one intent only: one manual proof (a unique index, case-insensitive, and
// the same hash with or without 0x), and never one the automatic USDT
// watcher settled a payment with or staff credited as an unmatched deposit.
// An unmatched deposit with this hash becomes this payment's proof.
func (s *Service) SubmitTXID(ctx context.Context, userID, intentID, network, txid string) (*store.Intent, error) {
	network, txid = strings.ToUpper(strings.TrimSpace(network)), strings.TrimSpace(txid)
	if txid == "" || network == "" {
		return nil, invalid("network and txid required")
	}
	if len(txid) > maxProofLen || len(network) > 32 {
		return nil, invalid("transaction details too long")
	}
	return s.submit(ctx, userID, intentID, []string{provider.ManualCrypto}, func(_ *store.Intent, r *store.Receipt) error {
		r.Network = &network
		r.TXID = &txid
		return nil
	})
}

// submit is the shared proof-submission flow: the intent must be the user's
// and use one of the allowed providers.
func (s *Service) submit(ctx context.Context, userID, intentID string, allowed []string, fill func(*store.Intent, *store.Receipt) error) (*store.Intent, error) {
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		in, err := s.st.GetIntent(ctx, tx, intentID)
		if err != nil {
			return err
		}
		if in.UserID != userID {
			return ErrForbidden
		}
		if !slices.Contains(allowed, in.Provider) {
			return invalid("this payment does not take this kind of proof (%s)", in.Provider)
		}
		r := &store.Receipt{IntentID: intentID}
		if err := fill(in, r); err != nil {
			return err
		}
		if r.TXID != nil {
			if err := s.guardSubmittedTXID(ctx, tx, intentID, *r.TXID); err != nil {
				return err
			}
		}
		if err := s.st.SaveReceipt(ctx, tx, r); err != nil {
			return err
		}
		if out, err = s.st.TransitionConfirming(ctx, tx, intentID); err != nil {
			return err
		}
		if r.TXID != nil {
			if key := txidKey(*r.TXID); key != "" {
				err = s.claimChainTransfers(ctx, tx, key, intentID)
			}
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Review applies an admin decision. Approve credits the ledger mirror and
// publishes payment.succeeded.v1; reject publishes payment.rejected.v1 with the
// reviewer's reason. Core owns wallets and applies the money exactly once per
// intent. Status guards allow exactly one committed review per intent, so
// concurrent approvals can never double-credit.
func (s *Service) Review(ctx context.Context, reviewerID, intentID, decision, reason string) (*store.Intent, error) {
	if decision != "approved" && decision != "rejected" {
		return nil, invalid("decision must be approved or rejected")
	}
	if reviewerID == "" {
		return nil, invalid("reviewer required")
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > maxReasonLen {
		return nil, invalid("reason too long")
	}
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		key, err := s.lockReceiptTXID(ctx, tx, intentID)
		if err != nil {
			return err
		}
		var in *store.Intent
		if decision == "approved" {
			if err := s.guardApprovedTXID(ctx, tx, intentID, key); err != nil {
				return err
			}
			in, err = s.st.TransitionSucceeded(ctx, tx, intentID)
			if err != nil {
				return err
			}
			if err := s.st.AppendLedgerCredit(ctx, tx, in.UserID, in.Currency, in.Amount,
				"intent", in.ID, "approve:"+in.ID); err != nil {
				return err
			}
		} else {
			in, err = s.st.TransitionFailed(ctx, tx, intentID)
			if err != nil {
				return err
			}
			if err := s.releaseChainTransfers(ctx, tx, intentID); err != nil {
				return err
			}
		}
		if err := s.st.ReviewReceipt(ctx, tx, intentID, reviewerID, decision, reason); err != nil {
			return err
		}
		out = in
		return s.publishReview(ctx, tx, in, decision, reason)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// publishReview writes the review's event inside the review transaction.
func (s *Service) publishReview(ctx context.Context, tx pgx.Tx, in *store.Intent, decision, reason string) error {
	e := events.PaymentEvent{
		IntentID: in.ID, UserID: in.UserID, Provider: in.Provider,
		Amount: in.Amount, Currency: in.Currency,
	}
	if in.OrderID != nil {
		e.OrderID = *in.OrderID
	}
	topic := events.PaymentsPaymentSucceeded
	if decision == "rejected" {
		topic, e.Reason = events.PaymentsPaymentRejected, reason
	}
	_, err := s.ob.Publish(ctx, tx, topic, e)
	return err
}
