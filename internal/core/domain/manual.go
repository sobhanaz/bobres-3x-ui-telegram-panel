package domain

import (
	"context"
	"math"
	"slices"
)

// The manual payment methods: the customer pays outside the store (to a card,
// a USDT address, or a Zarinpal payment link) and sends the proof, a receipt
// or a transaction id, which an admin reviews. The payments service makes
// their intents; what they show the customer comes from the store's settings
// (CreatePaymentIntent, paymentDetails).
const (
	ManualCard     = "manual_card"
	ManualCrypto   = "manual_crypto"
	ManualZarinpal = "manual_zarinpal"
)

// IsManual reports whether provider is a manual payment method.
func IsManual(provider string) bool {
	return provider == ManualCard || provider == ManualCrypto || provider == ManualZarinpal
}

// ManualMethods lists the manual payment methods the store offers for a price
// in currency, in the order the bot draws their buttons (card, crypto, the
// Zarinpal link). The bot's rule is the rule here: a method is offered when the
// owner has set what it shows, has not switched it off, and the price is one
// it can take:
//
//   - card to card: a Toman price and payments.card_number;
//   - crypto: a USDT address (TRC20 or ERC20), and a USDT price, or a Toman
//     price with payments.usdt_rate to turn it into USDT;
//   - the Zarinpal link: a Toman price and payments.zarinpal_link.
//
// A method the effective license does not include is left out too, as in
// PaymentMethods (the free tier includes all three).
func (s *Service) ManualMethods(ctx context.Context, currency string) ([]string, error) {
	eff, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	on := func(provider string) bool { return s.methodEnabled(ctx, provider) && eff.methodOffered(provider) }
	var out []string
	if currency == "IRT" && s.setting(ctx, "payments.card_number") != "" && on(ManualCard) {
		out = append(out, ManualCard)
	}
	hasAddress := s.setting(ctx, "payments.usdt_trc20") != "" || s.setting(ctx, "payments.usdt_erc20") != ""
	if hasAddress && (currency == "USDT" || (currency == "IRT" && s.settingInt(ctx, "payments.usdt_rate") > 0)) && on(ManualCrypto) {
		out = append(out, ManualCrypto)
	}
	if currency == "IRT" && s.setting(ctx, "payments.zarinpal_link") != "" && on(ManualZarinpal) {
		out = append(out, ManualZarinpal)
	}
	return out, nil
}

// OfferedMethods lists every payment method but the wallet that the store
// offers for a price in currency (amount in its minor units; 0: any typical
// price): the automated ones PaymentMethods lists, Telegram Stars among them,
// then the manual ones ManualMethods lists. It is what a customer is shown to
// choose from, and what a payment they chose is checked against.
func (s *Service) OfferedMethods(ctx context.Context, pay PaymentsClient, currency string, amount int64) ([]string, error) {
	out, err := s.PaymentMethods(ctx, pay, currency, amount)
	if err != nil {
		return nil, err
	}
	manual, err := s.ManualMethods(ctx, currency)
	if err != nil {
		return nil, err
	}
	return slices.Concat(out, manual), nil
}

// ManualCryptoAmount is what a customer sends for a price when they pay by
// manual crypto, in micro-USDT (scale 6): a USDT price as it is, a Toman price
// at payments.usdt_rate (Toman per USDT) rounded up to a whole cent, which is
// how the bot words it. ok is false when the price cannot be turned into
// USDT: no rate, another currency, or a price too large to compute.
func (s *Service) ManualCryptoAmount(ctx context.Context, amount int64, currency string) (micro int64, ok bool) {
	switch {
	case amount <= 0:
		return 0, false
	case currency == "USDT":
		return amount, true
	case currency != "IRT":
		return 0, false
	}
	rate := s.settingInt(ctx, "payments.usdt_rate")
	if rate <= 0 || amount > (math.MaxInt64-rate)/100 {
		return 0, false
	}
	cents := ceilDiv(amount*100, rate)
	if cents > math.MaxInt64/usdtCent {
		return 0, false
	}
	return cents * usdtCent, true
}
