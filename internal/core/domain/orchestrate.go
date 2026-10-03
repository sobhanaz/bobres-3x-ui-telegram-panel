package domain

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// maxTopupMinor bounds a single top-up so a typo cannot create an absurd
// intent (10^12 minor units: a trillion Toman, a million USDT).
const maxTopupMinor = 1_000_000_000_000

// PaymentsClient is the core-side view of the payments service.
type PaymentsClient interface {
	CreateIntent(ctx context.Context, orderID, userID, provider string, amount int64, currency, idemKey string) (intentID, status string, err error)
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

// IntentResult returns the intent id plus rendered instructions.
type IntentResult struct {
	IntentID     string
	Status       string
	OrderID      string
	Provider     string
	Amount       int64
	Currency     string
	Instructions string
}

// CreatePaymentIntent creates a manual payment intent in the payments service
// (for an order, or a wallet top-up) and renders instructions from settings.
func (s *Service) CreatePaymentIntent(ctx context.Context, pay PaymentsClient, p CreatePaymentIntentParams) (*IntentResult, error) {
	if p.IdempotencyKey == "" {
		return nil, invalid("idempotency key required")
	}
	switch p.Provider {
	case "manual_card", "manual_crypto":
	default:
		return nil, invalid("unsupported payment method %q", p.Provider)
	}
	if _, err := s.activeUser(ctx, nil, p.UserID); err != nil {
		return nil, err
	}
	amount, currency := p.Amount, p.Currency
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
	} else {
		if money.Scale(currency) < 0 {
			return nil, invalid("unsupported currency %q", currency)
		}
		if amount <= 0 || amount > maxTopupMinor {
			return nil, invalid("top-up amount out of range")
		}
	}
	id, status, err := pay.CreateIntent(ctx, p.OrderID, p.UserID, p.Provider, amount, currency, p.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return &IntentResult{
		IntentID: id, Status: status, OrderID: p.OrderID, Provider: p.Provider,
		Amount: amount, Currency: currency,
		Instructions: s.renderInstructions(ctx, p.Provider, amount, currency, id),
	}, nil
}

// renderInstructions builds user-facing text from settings (manual providers).
func (s *Service) renderInstructions(ctx context.Context, prov string, amount int64, currency, reference string) string {
	m, err := s.st.GetSettings(ctx, s.st.Conn())
	if err != nil {
		return ""
	}
	get := func(key string) string {
		raw, ok := m[key]
		if !ok {
			return ""
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return ""
		}
		return v
	}
	shown := fmt.Sprintf("%d %s", amount, currency)
	if a, err := money.New(amount, currency); err == nil {
		shown = money.Format(a)
	}
	switch prov {
	case "manual_card":
		card := get("payments.card_number")
		if card == "" {
			return ""
		}
		return fmt.Sprintf("Transfer %s to card %s (%s).\nReference: %s\nThen send the receipt photo and reference number here.",
			shown, card, get("payments.card_holder"), reference)
	case "manual_crypto":
		trc, erc := get("payments.usdt_trc20"), get("payments.usdt_erc20")
		if trc == "" && erc == "" {
			return ""
		}
		return fmt.Sprintf("Send %s to:\nTRC20: %s\nERC20: %s\nThen send the TXID and network here.", shown, trc, erc)
	default:
		return ""
	}
}
