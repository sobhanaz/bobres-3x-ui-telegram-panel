package domain

import (
	"context"
	"math"
	"strconv"
	"strings"
)

// Automated payment methods (Phase 2). Each is charged in its own unit,
// converted from the plan price with the operator's rates when the payment
// starts; the payments service then holds the customer to that amount.
const (
	Zarinpal = "zarinpal"
	// IDPay, NextPay and Aqayepardakht are Iranian gateways built like
	// Zarinpal: switched on in payments with an API key (Aqayepardakht's is
	// its PIN), charged in Rial (Toman x 10).
	IDPay         = "idpay"
	NextPay       = "nextpay"
	Aqayepardakht = "aqayepardakht"
	Stars         = "stars"
	// USDTAuto is automatic USDT on TRON (TRC20): the customer sends an exact
	// amount to the store's address and payments' watcher finds it on chain.
	// It is switched on in payments (the Gateways page), like Zarinpal.
	USDTAuto = "usdt_trc20_auto"
	// TONAuto is automatic TON (Toncoin): the customer sends an exact amount of TON, with the payment's comment, to
	// the store's address and payments' watcher finds it. Core prices it in USDT (as for automatic USDT) and payments
	// turns that into TON at the price it reads.
	TONAuto = "ton_auto"
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
	// Automatic USDT: the address to send GatewayAmount to, and the end of
	// the window the amount is held for (unix seconds; 0 = none). ExternalID
	// is the settling transaction's hash once paid.
	PayAddress string
	// Automatic TON: the comment the transfer must carry.
	PayMemo    string
	ExpiresAt  int64
	ExternalID string
}

// Zarinpal's per-payment limits (the gateway works in Rial: x10).
const (
	zarinpalMinToman = 1_000
	zarinpalMaxToman = 100_000_000
)

// IDPay's, NextPay's and Aqayepardakht's per-payment limits in Toman (their
// Rial limits are 1,000 to 500,000,000, 10,000 to 1,000,000,000 and 10,000 to
// 4,000,000,000: the gateway packages).
const (
	idpayMinToman         = 100
	idpayMaxToman         = 50_000_000
	nextpayMinToman       = 1_000
	nextpayMaxToman       = 100_000_000
	aqayepardakhtMinToman = 1_000
	aqayepardakhtMaxToman = 400_000_000
)

// Automatic USDT's per-payment limits, in micro-USDT (scale 6). Payments
// adds 1 to 999 micro-USDT to what core asks, so that every open payment has
// its own exact amount.
const (
	// usdtMin: 0.1 USDT. Payments treats a transfer under 0.1 USDT that
	// matches nothing as dust (address poisoning) and tells nobody, so a
	// smaller payment paid wrong would go unseen.
	usdtMin = 100_000
	// usdtMax: 1,000,000 USDT, as payments allows.
	usdtMax = 1_000_000_000_000
	// usdtCent: Toman prices are converted to whole cents of USDT.
	usdtCent = 10_000
)

func isGateway(provider string) bool {
	return provider == Zarinpal || provider == IDPay || provider == NextPay || provider == Aqayepardakht || provider == Stars || provider == USDTAuto || provider == TONAuto
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// settingInt reads a whole-number setting ("60,000" and "60000" both work);
// 0 when unset or not a positive number.
func (s *Service) settingInt(ctx context.Context, key string) int64 {
	v := strings.NewReplacer(",", "", "_", "", " ", "", "٬", "").Replace(latinDigits(s.setting(ctx, key)))
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// gatewayCharge converts a price into what a gateway charges: Zarinpal and the
// gateways built like it take Rial (Toman x 10), Telegram Stars a whole number of Stars (payments.stars_rate
// Toman per Star, rounded up so a payment never falls short), automatic USDT
// micro-USDT (see usdtCharge).
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
	case IDPay:
		if currency != "IRT" {
			return 0, "", invalid("IDPay takes Toman prices only")
		}
		if amount < idpayMinToman || amount > idpayMaxToman {
			return 0, "", invalid("IDPay takes 100 to 50,000,000 Toman per payment")
		}
		return amount * 10, "IRR", nil
	case NextPay:
		if currency != "IRT" {
			return 0, "", invalid("NextPay takes Toman prices only")
		}
		if amount < nextpayMinToman || amount > nextpayMaxToman {
			return 0, "", invalid("NextPay takes 1,000 to 100,000,000 Toman per payment")
		}
		return amount * 10, "IRR", nil
	case Aqayepardakht:
		if currency != "IRT" {
			return 0, "", invalid("Aqayepardakht takes Toman prices only")
		}
		if amount < aqayepardakhtMinToman || amount > aqayepardakhtMaxToman {
			return 0, "", invalid("Aqayepardakht takes 1,000 to 400,000,000 Toman per payment")
		}
		return amount * 10, "IRR", nil
	case Stars:
		rate := s.settingInt(ctx, "payments.stars_rate")
		if currency != "IRT" || rate <= 0 {
			return 0, "", invalid("Telegram Stars is not set up for this price (payments.stars_rate)")
		}
		return ceilDiv(amount, rate), "XTR", nil
	case USDTAuto:
		return s.usdtCharge(ctx, amount, currency)
	case TONAuto:
		return s.tonCharge(ctx, amount, currency)
	}
	return 0, "", invalid("unsupported payment method %q", provider)
}

// usdtCharge is what automatic USDT asks before payments makes it unique: a
// USDT price as it is; a Toman price at the store's USDT rate (UsdtRate, Toman per USDT,
// the rate manual USDT uses too) in whole cents, rounded up so a payment
// never falls short: ceil(amount*100/rate) * 10,000 micro-USDT.
func (s *Service) usdtCharge(ctx context.Context, amount int64, currency string) (int64, string, error) {
	var base int64
	switch currency {
	case "USDT":
		base = amount
	case "IRT":
		rate := s.UsdtRate(ctx)
		if rate <= 0 {
			return 0, "", invalid("automatic USDT is not set up for this price (no USDT rate: the live rate is off or unreachable, and payments.usdt_rate is empty)")
		}
		if amount > (math.MaxInt64-rate)/100 { // amount*100 + rate - 1 must fit
			return 0, "", invalid("automatic USDT takes at most 1,000,000 USDT per payment")
		}
		cents := ceilDiv(amount*100, rate)
		if cents > usdtMax/usdtCent {
			return 0, "", invalid("automatic USDT takes at most 1,000,000 USDT per payment")
		}
		base = cents * usdtCent
	default:
		return 0, "", invalid("automatic USDT takes Toman and USDT prices only")
	}
	switch {
	case base < usdtMin:
		return 0, "", invalid("automatic USDT takes at least 0.1 USDT per payment")
	case base > usdtMax:
		return 0, "", invalid("automatic USDT takes at most 1,000,000 USDT per payment")
	}
	return base, "USDT", nil
}

// Automatic TON's per-payment limits in micro-USDT (scale 6), as payments allows: the TON amount is worked out by
// payments from this at the TON price it reads.
const (
	tonMin = 100_000         // 0.1 USDT
	tonMax = 100_000_000_000 // 100,000 USDT
)

// tonCharge is what automatic TON asks payments for: the price in USDT, worked out exactly as automatic USDT's (a
// USDT price as it is; a Toman price at the store's USDT rate, the live price plus the owner's margin, in whole
// cents rounded up). Payments turns it into nanoton at the TON price it reads, and offers the method only while that
// price is fresh.
func (s *Service) tonCharge(ctx context.Context, amount int64, currency string) (int64, string, error) {
	base, _, err := s.usdtCharge(ctx, amount, currency)
	if err != nil {
		return 0, "", invalid("%s", strings.ReplaceAll(err.Error(), "automatic USDT", "automatic TON"))
	}
	if base < tonMin || base > tonMax {
		return 0, "", invalid("automatic TON takes 0.1 to 100,000 USDT per payment")
	}
	return base, "USDT", nil
}

// PaymentMethods lists the automated methods usable for a price (amount in
// currency; 0 = any typical price): offered by payments, switched on and
// priced (rates set) here, within the method's limits, and in what the
// effective entitlements include (automatic USDT needs crypto_watcher, any
// gateway must be in the gateways list). A license that ended, or a trial
// that is over, so stops the OFFER of a premium method without a word to the
// customer: payments already started settle, and nothing is switched off.
func (s *Service) PaymentMethods(ctx context.Context, pay PaymentsClient, currency string, amount int64) ([]string, error) {
	gws, err := pay.ListGateways(ctx)
	if err != nil {
		return nil, err
	}
	eff, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	probe := amount
	if probe <= 0 {
		probe = map[string]int64{"IRT": 100_000, "USDT": 1_000_000}[currency]
	}
	var out []string
	for _, g := range gws {
		if !s.methodEnabled(ctx, g) || !eff.methodOffered(g) {
			continue
		}
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
	return planName(p, "Subscription")
}
