package domain

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
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
	if hasAddress && (currency == "USDT" || (currency == "IRT" && s.UsdtRate(ctx) > 0)) && on(ManualCrypto) {
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
// at the store's USDT rate (UsdtRate: the live price, or payments.usdt_rate) rounded up to a whole cent, which is
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
	rate := s.UsdtRate(ctx)
	if rate <= 0 || amount > (math.MaxInt64-rate)/100 {
		return 0, false
	}
	cents := ceilDiv(amount*100, rate)
	if cents > math.MaxInt64/usdtCent {
		return 0, false
	}
	return cents * usdtCent, true
}

// PendingManualPayment is a manual payment of a customer that still waits for
// its proof (PendingManualPayments): what its screen shows, and when it began.
type PendingManualPayment struct {
	// IntentResult is the payment as CreatePaymentIntent answers it: status
	// "pending", the price, and Details (the card, the addresses or the link the
	// store has set now, and the payment reference).
	IntentResult
	CreatedAt time.Time
}

// pendingManualFetch is how many of a method's newest pending payments
// PendingManualPayments looks at: more than a customer has waiting for a proof,
// and few enough for an order to be read for each.
const pendingManualFetch = 20

// PendingManualPayments lists the customer's own manual payments that still
// wait for their proof, newest first and at most limit: what a customer needs to
// take a payment up again after leaving its screen between paying and sending
// the receipt (the Mini App reloaded, the chat closed). A payment is listed when
//
//   - it is the customer's own, made by a manual method, and still pending: no
//     proof was sent, and it was not rejected or settled;
//   - the store still takes that method for its price (ManualMethods: set up, not
//     switched off, in the license), so there is something to show; and
//   - its order, if it has one, still waits for payment: a payment for an order
//     paid another way, cancelled or gone is not one to send a proof for.
//
// Everything comes from the payment and from the store's settings. The payments
// service is asked to filter by customer, status and method, and its answer is
// checked again.
func (s *Service) PendingManualPayments(ctx context.Context, pay PaymentsClient, userID string, limit int) ([]PendingManualPayment, error) {
	if limit <= 0 {
		return nil, nil
	}
	if _, err := s.activeUser(ctx, nil, userID); err != nil {
		return nil, err
	}
	var recs []PaymentRecord
	for _, method := range []string{ManualCard, ManualCrypto, ManualZarinpal} {
		got, _, err := pay.ListPayments(ctx, PaymentFilter{UserID: userID, Status: "pending", Provider: method}, 1, pendingManualFetch)
		if err != nil {
			return nil, err
		}
		for _, r := range got {
			if r.UserID == userID && r.Provider == method && r.Status == "pending" {
				recs = append(recs, r)
			}
		}
	}
	slices.SortFunc(recs, func(a, b PaymentRecord) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), strings.Compare(b.IntentID, a.IntentID))
	})

	var (
		out     []PendingManualPayment
		offered = map[string][]string{}          // ManualMethods, by currency
		details = map[string]map[string]string{} // paymentDetails, by method
	)
	for _, r := range recs {
		if len(out) == limit {
			break
		}
		methods, ok := offered[r.Currency]
		if !ok {
			var err error
			if methods, err = s.ManualMethods(ctx, r.Currency); err != nil {
				return nil, err
			}
			offered[r.Currency] = methods
		}
		if !slices.Contains(methods, r.Provider) {
			continue
		}
		if r.OrderID != "" {
			o, err := s.st.GetOrder(ctx, s.st.Conn(), r.OrderID)
			switch {
			case errors.Is(err, store.ErrNotFound):
				continue
			case err != nil:
				return nil, err
			case o.UserID != userID || !isPayable(o.Status):
				continue
			}
		}
		d, ok := details[r.Provider]
		if !ok {
			d = s.paymentDetails(ctx, r.Provider)
			details[r.Provider] = d
		}
		shown := maps.Clone(d)
		shown["reference"] = PaymentReference(r.IntentID)
		out = append(out, PendingManualPayment{
			IntentResult: IntentResult{IntentID: r.IntentID, Status: r.Status, OrderID: r.OrderID, Provider: r.Provider, Amount: r.Amount, Currency: r.Currency, Details: shown},
			CreatedAt:    r.CreatedAt,
		})
	}
	return out, nil
}
