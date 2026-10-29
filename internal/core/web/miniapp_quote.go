package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// Paying a plan priced in USDT from the wallet (docs/17-mini-app.md, "Plans priced in USDT"): the app asks for a quote, shows the USDT price, the rate and
// the Toman the wallet is charged, and pays with the quote's token; core charges exactly what the quote said while it holds (two minutes). The rules
// are the domain's (domain.QuoteWalletPayment, PayOrderWithWalletQuote), the bot's too.

// miniQuoteBody is a quote as the app draws it. expires_in is seconds from the moment of the answer, so that a phone with a wrong clock still counts
// the right time; expires_at is the same moment in unix seconds.
func miniQuoteBody(q *domain.WalletQuote) map[string]any {
	return map[string]any{
		"order_id": q.OrderID, "list_amount": q.ListAmount, "list_currency": q.ListCurrency, "list_scale": bmoney.Scale(q.ListCurrency),
		"rate": q.Rate, "amount": q.Amount, "currency": domain.WalletCurrency, "scale": bmoney.Scale(domain.WalletCurrency),
		"quote": q.Token, "expires_at": q.ExpiresAt.Unix(), "expires_in": int(domain.WalletQuoteTTL.Seconds()),
		"balance": q.Balance, "credit_limit": q.CreditLimit, "enough": q.Enough,
	}
}

// miniWalletQuote: GET /api/mini/v1/orders/{id}/wallet-quote prices the customer's own unpaid order, listed in USDT, in Toman at the store's rate now.
// It changes nothing, and each call makes a new quote at the rate of that moment. 409 no_rate when the store knows no USDT rate (nothing is guessed),
// not_payable for an order that is paid or closed, state for an order not priced in USDT.
func (s *Server) miniWalletQuote(w http.ResponseWriter, r *http.Request) {
	o, err := s.ownOrder(r)
	if err != nil {
		s.miniFail(w, "mini app wallet quote", err)
		return
	}
	q, err := s.cfg.Domain.QuoteWalletPayment(r.Context(), o.ID, o.UserID)
	if err != nil {
		s.miniFail(w, "mini app wallet quote", err)
		return
	}
	writeJSON(w, http.StatusOK, miniQuoteBody(q))
}

// readOptionalJSON reads a JSON body that may be empty (the call takes optional fields only): no body is the zero value, a body that is not JSON for
// this endpoint (unknown fields included) is a 400.
func readOptionalJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid", "the request body is not valid JSON for this endpoint")
		return false
	}
	return true
}

// The bounds of a top-up in USDT, as the bot's: a whole number of USDT from 1 to 10,000 (micro-USDT here, the unit every amount of the API is in).
const (
	miniMinTopupUsdt  = 1_000_000
	miniMaxTopupUsdt  = 10_000_000_000
	miniStepTopupUsdt = 1_000_000
)

// usdtAddressQR is the QR code of a TRON address as an image the app can show (a PNG as a data URI, which the app's content security policy allows);
// "" when it cannot be made. The code holds the plain address, which every wallet reads.
func usdtAddressQR(address string) string {
	png, err := qrcode.Encode(address, qrcode.Medium, 256)
	if err != nil || len(png) == 0 {
		return ""
	}
	var b bytes.Buffer
	b.WriteString("data:image/png;base64,")
	b.WriteString(base64.StdEncoding.EncodeToString(png))
	return b.String()
}
