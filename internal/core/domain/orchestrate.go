package domain

import (
	"context"
	"encoding/json"
	"fmt"
)

// PaymentsClient is the core-side view of the payments service.
type PaymentsClient interface {
	CreateIntent(ctx context.Context, orderID, userID, provider string, amount int64, currency, idemKey string) (intentID, status string, err error)
}

// CreatePaymentIntentParams carries-bot/orchestration inputs.
type CreatePaymentIntentParams struct {
	UserID         string
	OrderID        string // empty for top-up
	Provider       string
	Amount         int64
	Currency       string
	IdempotencyKey string
}

// IntentResult returns the intent id plus rendered instructions.
type IntentResult struct {
	IntentID     string
	Status       string
	OrderID      string
	Provider     string
	Instructions string
}

// CreatePaymentIntent orchestrates: create the payments intent and render
// instructions from core settings for manual providers.
func (s *Service) CreatePaymentIntent(ctx context.Context, pay PaymentsClient, p CreatePaymentIntentParams) (*IntentResult, error) {
	amount, currency := p.Amount, p.Currency
	if p.OrderID != "" {
		o, err := s.st.GetOrder(ctx, s.st.Conn(), p.OrderID)
		if err != nil {
			return nil, err
		}
		if o.UserID != p.UserID {
			return nil, fmt.Errorf("domain: order belongs to another user")
		}
		if o.Status != "awaiting_payment" {
			return nil, ErrOrderNotPayable
		}
		amount, currency = o.Amount, o.Currency
	}
	id, status, err := pay.CreateIntent(ctx, p.OrderID, p.UserID, p.Provider, amount, currency, p.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	res := &IntentResult{IntentID: id, Status: status, OrderID: p.OrderID, Provider: p.Provider}
	res.Instructions = s.renderInstructions(ctx, p.Provider, amount, currency, p.OrderID)
	return res, nil
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
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ""
		}
		return s
	}
	switch prov {
	case "manual_card":
		card := get("payments.card_number")
		holder := get("payments.card_holder")
		if card == "" {
			return ""
		}
		return fmt.Sprintf("Transfer %d %s to card %s (%s).\nReference: %s\nThen send the receipt photo and reference number here.",
			amount, currency, card, holder, reference)
	case "manual_crypto":
		trc := get("payments.usdt_trc20")
		erc := get("payments.usdt_erc20")
		if trc == "" && erc == "" {
			return ""
		}
		return fmt.Sprintf("Send %d %s to:\nTRC20: %s\nERC20: %s\nThen send the TXID and network here.",
			amount, currency, trc, erc)
	default:
		return ""
	}
}
