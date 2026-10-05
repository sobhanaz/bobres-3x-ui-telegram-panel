package domain

import (
	"context"
	"strconv"
	"strings"
)

// Automated payment methods (Phase 2). Each is charged in its own unit,
// converted from the plan price with the operator's rates when the payment
// starts; the payments service then holds the customer to that amount.
const (
	Zarinpal = "zarinpal"
	Stars    = "stars"
)

// GatewayStart asks payments to start an automated payment.
type GatewayStart struct {
	OrderID, UserID, Provider string
	Amount                    int64 // the price in its own currency (ledger amount)
	Currency                  string
	GatewayAmount             int64 // what the gateway charges
	GatewayCurrency           string
	Description               string
	IdempotencyKey            string
}

// GatewayIntent is payments' view of an automated payment.
type GatewayIntent struct {
	ID, Status, OrderID, Provider string
	Amount                        int64
	Currency                      string
	GatewayAmount                 int64
	GatewayCurrency               string
	PayURL                        string
	FailureReason                 string
}

// Zarinpal's per-payment limits (the gateway works in Rial: x10).
const (
	zarinpalMinToman = 1_000
	zarinpalMaxToman = 100_000_000
)

func isGateway(provider string) bool {
	return provider == Zarinpal || provider == Stars
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// settingInt reads a whole-number setting ("60,000" and "60000" both work);
// 0 when unset or not a positive number.
func (s *Service) settingInt(ctx context.Context, key string) int64 {
	v := strings.NewReplacer(",", "", "_", "", " ", "", "٬", "").Replace(s.setting(ctx, key))
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// gatewayCharge converts a price into what a gateway charges: Zarinpal takes
// Rial (Toman x 10), Telegram Stars a whole number of Stars (payments.stars_rate
// Toman per Star, rounded up so a payment never falls short).
func (s *Service) gatewayCharge(ctx context.Context, provider string, amount int64, currency string) (int64, string, error) {
	if amount <= 0 {
		return 0, "", invalid("amount must be positive")
	}
	switch provider {
	case Zarinpal:
		if currency != "IRT" {
			return 0, "", invalid("Zarinpal takes Toman prices only")
		}
		if amount < zarinpalMinToman || amount > zarinpalMaxToman {
			return 0, "", invalid("Zarinpal takes 1,000 to 100,000,000 Toman per payment")
		}
		return amount * 10, "IRR", nil
	case Stars:
		rate := s.settingInt(ctx, "payments.stars_rate")
		if currency != "IRT" || rate <= 0 {
			return 0, "", invalid("Telegram Stars is not set up for this price (payments.stars_rate)")
		}
		return ceilDiv(amount, rate), "XTR", nil
	}
	return 0, "", invalid("unsupported payment method %q", provider)
}

// PaymentMethods lists the automated methods usable for a price (amount in
// currency; 0 = any typical price): enabled in payments, priced (rates set)
// here, and within the method's limits.
func (s *Service) PaymentMethods(ctx context.Context, pay PaymentsClient, currency string, amount int64) ([]string, error) {
	gws, err := pay.ListGateways(ctx)
	if err != nil {
		return nil, err
	}
	probe := amount
	if probe <= 0 {
		probe = map[string]int64{"IRT": 100_000, "USDT": 1_000_000}[currency]
	}
	var out []string
	for _, g := range gws {
		if _, _, err := s.gatewayCharge(ctx, g, probe, currency); err == nil {
			out = append(out, g)
		}
	}
	return out, nil
}

// CheckPayment asks payments to check an automated payment of the user now.
func (s *Service) CheckPayment(ctx context.Context, pay PaymentsClient, userID, intentID string) (*GatewayIntent, error) {
	if intentID == "" {
		return nil, invalid("intent id required")
	}
	if _, err := s.activeUser(ctx, nil, userID); err != nil {
		return nil, err
	}
	return pay.CheckIntent(ctx, userID, intentID)
}

// StarsPreCheckout relays Telegram's pre-checkout for a Stars invoice. A banned
// user cannot start paying.
func (s *Service) StarsPreCheckout(ctx context.Context, pay PaymentsClient, userID, intentID string, total int64, currency string) (*GatewayIntent, error) {
	if _, err := s.activeUser(ctx, nil, userID); err != nil {
		return nil, err
	}
	return pay.PrecheckStars(ctx, userID, intentID, total, currency)
}

// StarsPaid relays a successful Stars payment. It deliberately does not check
// the user's status: the Stars are already taken and must be recorded.
func (s *Service) StarsPaid(ctx context.Context, pay PaymentsClient, userID, intentID string, total int64, currency, chargeID string) (*GatewayIntent, error) {
	return pay.ConfirmStars(ctx, userID, intentID, total, currency, chargeID)
}

// orderDescription is what the gateway shows for an order: the plan's English
// name (gateways' own pages are not localised by us), else a generic label.
func (s *Service) orderDescription(ctx context.Context, planID string) string {
	if planID == "" {
		return "Subscription"
	}
	p, err := s.st.GetPlan(ctx, s.st.Conn(), planID)
	if err != nil {
		return "Subscription"
	}
	if n := p.NameI18n["en"]; n != "" {
		return n
	}
	for _, n := range p.NameI18n {
		if n != "" {
			return n
		}
	}
	return "Subscription"
}
