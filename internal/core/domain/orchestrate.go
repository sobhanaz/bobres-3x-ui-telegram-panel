package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// maxTopupMinor bounds a single top-up so a typo cannot create an absurd
// intent (10^12 minor units: a trillion Toman, a million USDT).
const maxTopupMinor = 1_000_000_000_000

// PaymentsClient is the core-side view of the payments service.
type PaymentsClient interface {
	CreateIntent(ctx context.Context, orderID, userID, provider string, amount int64, currency, idemKey string) (intentID, status string, err error)
	SubmitReceipt(ctx context.Context, userID, intentID, fileID, reference string) (status string, err error)
	SubmitTXID(ctx context.Context, userID, intentID, network, txid string) (status string, err error)
	ListPending(ctx context.Context, limit int) ([]PendingPayment, error)
	Review(ctx context.Context, reviewerID, intentID, decision, reason string) (status string, err error)
	// Automated gateways (Phase 2).
	StartGateway(ctx context.Context, p GatewayStart) (*GatewayIntent, error)
	CheckIntent(ctx context.Context, userID, intentID string) (*GatewayIntent, error)
	PrecheckStars(ctx context.Context, userID, intentID string, total int64, currency string) (*GatewayIntent, error)
	ConfirmStars(ctx context.Context, userID, intentID string, total int64, currency, chargeID string) (*GatewayIntent, error)
	ListGateways(ctx context.Context) ([]string, error)
}

// PendingPayment is one manual payment waiting for review.
type PendingPayment struct {
	IntentID, OrderID, UserID, Provider string
	Amount                              int64
	Currency                            string
	ReceiptFile, ReferenceNumber        string
	Network, TXID                       string
	SubmittedAt                         time.Time
	PossibleDuplicate                   bool
}

// CreatePaymentIntentParams carries bot/orchestration inputs.
type CreatePaymentIntentParams struct {
	UserID         string
	OrderID        string // empty for a wallet top-up
	Provider       string // manual_card | manual_crypto
	Amount         int64  // top-ups only; an order's amount comes from the order
	Currency       string
	IdempotencyKey string
}

// IntentResult returns the intent id plus what the user needs to pay.
type IntentResult struct {
	IntentID     string
	Status       string
	OrderID      string
	Provider     string
	Amount       int64
	Currency     string
	Instructions string
	// Details holds card_number, card_holder, usdt_trc20, usdt_erc20 and
	// reference, so the bot can render instructions in the user's language.
	Details map[string]string
	// Automated gateways: where to pay and what is charged there.
	PayURL          string
	GatewayAmount   int64
	GatewayCurrency string
	Description     string
}

// PaymentReference is the short code a user can put in a transfer note and an
// admin can match against the review queue (the random tail of the intent id).
func PaymentReference(intentID string) string {
	ref := strings.ReplaceAll(intentID, "-", "")
	if len(ref) > 8 {
		ref = ref[len(ref)-8:]
	}
	return strings.ToUpper(ref)
}

// CreatePaymentIntent creates a manual payment intent in the payments service
// (for an order, or a wallet top-up) and renders instructions from settings.
func (s *Service) CreatePaymentIntent(ctx context.Context, pay PaymentsClient, p CreatePaymentIntentParams) (*IntentResult, error) {
	if p.IdempotencyKey == "" {
		return nil, invalid("idempotency key required")
	}
	switch {
	case p.Provider == "manual_card", p.Provider == "manual_crypto", isGateway(p.Provider):
	default:
		return nil, invalid("unsupported payment method %q", p.Provider)
	}
	if _, err := s.activeUser(ctx, nil, p.UserID); err != nil {
		return nil, err
	}
	amount, currency := p.Amount, p.Currency
	description := "Wallet top-up"
	if p.OrderID != "" {
		o, err := s.st.GetOrder(ctx, s.st.Conn(), p.OrderID)
		if err != nil {
			return nil, err
		}
		if o.UserID != p.UserID {
			return nil, ErrForbidden
		}
		if !isPayable(o.Status) {
			return nil, ErrOrderNotPayable
		}
		amount, currency = o.Amount, o.Currency
		description = s.orderDescription(ctx, o.PlanID)
	} else {
		if money.Scale(currency) < 0 {
			return nil, invalid("unsupported currency %q", currency)
		}
		if amount <= 0 || amount > maxTopupMinor {
			return nil, invalid("top-up amount out of range")
		}
	}
	if isGateway(p.Provider) {
		ga, gc, err := s.gatewayCharge(ctx, p.Provider, amount, currency)
		if err != nil {
			return nil, err
		}
		gi, err := pay.StartGateway(ctx, GatewayStart{
			OrderID: p.OrderID, UserID: p.UserID, Provider: p.Provider, Amount: amount, Currency: currency,
			GatewayAmount: ga, GatewayCurrency: gc, Description: description, IdempotencyKey: p.IdempotencyKey,
		})
		if err != nil {
			return nil, err
		}
		return &IntentResult{
			IntentID: gi.ID, Status: gi.Status, OrderID: p.OrderID, Provider: p.Provider,
			Amount: amount, Currency: currency, Details: map[string]string{"reference": PaymentReference(gi.ID)},
			PayURL: gi.PayURL, GatewayAmount: gi.GatewayAmount, GatewayCurrency: gi.GatewayCurrency,
			Description: description,
		}, nil
	}
	id, status, err := pay.CreateIntent(ctx, p.OrderID, p.UserID, p.Provider, amount, currency, p.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	details := s.paymentDetails(ctx, p.Provider)
	details["reference"] = PaymentReference(id)
	return &IntentResult{
		IntentID: id, Status: status, OrderID: p.OrderID, Provider: p.Provider,
		Amount: amount, Currency: currency, Details: details,
		Instructions: renderInstructions(p.Provider, amount, currency, details),
	}, nil
}

// paymentDetails reads the operator's payment settings for a provider.
func (s *Service) paymentDetails(ctx context.Context, prov string) map[string]string {
	out := map[string]string{}
	keys := map[string][]string{
		"manual_card":   {"card_number", "card_holder"},
		"manual_crypto": {"usdt_trc20", "usdt_erc20"},
	}[prov]
	for _, k := range keys {
		if v := s.setting(ctx, "payments."+k); v != "" {
			out[k] = v
		}
	}
	return out
}

// setting returns a string setting, or "" when unset.
func (s *Service) setting(ctx context.Context, key string) string {
	m, err := s.st.GetSettings(ctx, s.st.Conn())
	if err != nil {
		return ""
	}
	var v string
	if raw, ok := m[key]; ok && json.Unmarshal(raw, &v) == nil {
		return v
	}
	return ""
}

// renderInstructions is an English fallback; the bot renders localized text
// from the structured details.
func renderInstructions(prov string, amount int64, currency string, d map[string]string) string {
	shown := fmt.Sprintf("%d %s", amount, currency)
	if a, err := money.New(amount, currency); err == nil {
		shown = money.Format(a)
	}
	switch prov {
	case "manual_card":
		if d["card_number"] == "" {
			return ""
		}
		return fmt.Sprintf("Transfer %s to card %s (%s).\nReference: %s\nThen send the receipt photo and reference number here.",
			shown, d["card_number"], d["card_holder"], d["reference"])
	case "manual_crypto":
		if d["usdt_trc20"] == "" && d["usdt_erc20"] == "" {
			return ""
		}
		return fmt.Sprintf("Send %s to:\nTRC20: %s\nERC20: %s\nThen send the TXID and network here.", shown, d["usdt_trc20"], d["usdt_erc20"])
	default:
		return ""
	}
}

// ProofParams is a user's payment proof: a receipt (card) or a TXID (crypto).
type ProofParams struct {
	UserID, IntentID             string
	ReceiptFile, ReferenceNumber string
	Network, TXID                string
}

// SubmitPaymentProof forwards a proof to payments (which checks ownership).
func (s *Service) SubmitPaymentProof(ctx context.Context, pay PaymentsClient, p ProofParams) (string, error) {
	if p.IntentID == "" {
		return "", invalid("intent id required")
	}
	if _, err := s.activeUser(ctx, nil, p.UserID); err != nil {
		return "", err
	}
	switch {
	case p.TXID != "":
		return pay.SubmitTXID(ctx, p.UserID, p.IntentID, p.Network, p.TXID)
	case p.ReceiptFile != "":
		return pay.SubmitReceipt(ctx, p.UserID, p.IntentID, p.ReceiptFile, p.ReferenceNumber)
	default:
		return "", invalid("a receipt photo or a transaction id is required")
	}
}

// ErrNotReady: the subscription is still being prepared.
var ErrNotReady = errors.New("domain: subscription is still being prepared")

// SubscriptionLinks returns how to connect to one of the user's subscriptions.
func (s *Service) SubscriptionLinks(ctx context.Context, prov Provisioner, userID, subscriptionID string) (Links, error) {
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), subscriptionID)
	if err != nil {
		return Links{}, err
	}
	if sub.UserID != userID {
		return Links{}, ErrForbidden
	}
	if sub.Status == "pending" {
		return Links{}, ErrNotReady
	}
	return prov.GetLinks(ctx, subscriptionID)
}
