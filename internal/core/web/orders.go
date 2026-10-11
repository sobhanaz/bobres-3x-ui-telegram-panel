package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// ReceiptFiles fetches the receipt photos customers sent the bot
// (botfiles.Client).
type ReceiptFiles interface {
	Fetch(ctx context.Context, fileID string) (*botfiles.File, error)
}

type orderJSON struct {
	ID             string         `json:"id"`
	User           *userBrief     `json:"user"`
	Plan           planRef        `json:"plan"`
	Type           string         `json:"type"`
	Status         string         `json:"status"`
	Amount         money          `json:"amount"`
	Discount       map[string]any `json:"discount"`
	SubscriptionID *string        `json:"subscription_id"`
	CreatedAt      int64          `json:"created_at"`
	UpdatedAt      int64          `json:"updated_at"`
	Attempts       int            `json:"attempts"`
	LastError      string         `json:"last_error"`
	NextAttemptAt  *int64         `json:"next_attempt_at"`
	Staff          string         `json:"staff"`  // the staff member who made it (an extension)
	Extend         map[string]any `json:"extend"` // {days, bytes} of an extension
	Refund         map[string]any `json:"refund"` // {amount, at} once refunded
}

func orderOf(o *store.OrderRow) orderJSON {
	out := orderJSON{
		ID: o.ID, User: briefOf(o.UserID, o.TelegramID, o.Username), Plan: planRef{o.PlanID, o.PlanName},
		Type: o.Type, Status: o.Status, Amount: money{o.Amount, o.Currency}, SubscriptionID: o.SubscriptionID,
		CreatedAt: o.CreatedAt.Unix(), UpdatedAt: o.UpdatedAt.Unix(), Attempts: o.Attempts, LastError: o.LastError,
		NextAttemptAt: unix(o.NextAttemptAt), Staff: o.CreatedByName,
	}
	if o.DiscountCode != nil {
		out.Discount = map[string]any{"code": *o.DiscountCode, "amount": money{o.DiscountAmount, o.Currency}}
	}
	if o.ExtendDays != nil || o.ExtendBytes != nil {
		out.Extend = map[string]any{"days": o.ExtendDays, "bytes": o.ExtendBytes}
	}
	if o.RefundedAt != nil && o.RefundAmount != nil {
		out.Refund = map[string]any{"amount": money{*o.RefundAmount, o.Currency}, "at": o.RefundedAt.Unix()}
	}
	return out
}

// listOrders: GET /api/v1/orders?q=&status=&type=&user=&page=&size=
func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	userID, ok := queryID(r, "user")
	if !ok {
		writeJSON(w, http.StatusOK, list{[]orderJSON{}, 0, page, size})
		return
	}
	q := r.URL.Query()
	f := store.OrderFilter{Query: q.Get("q"), Status: q.Get("status"), Type: q.Get("type"), UserID: userID}
	rows, total, err := s.cfg.Store.ListOrders(r.Context(), s.cfg.Store.Conn(), f, p)
	if err != nil {
		s.fail(w, "list orders", err)
		return
	}
	items := make([]orderJSON, 0, len(rows))
	for i := range rows {
		items = append(items, orderOf(&rows[i]))
	}
	writeJSON(w, http.StatusOK, list{items, total, page, size})
}

func (s *Server) order(r *http.Request, id string) (*store.OrderRow, error) {
	rows, _, err := s.cfg.Store.ListOrders(r.Context(), s.cfg.Store.Conn(), store.OrderFilter{ID: id}, store.Page{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, store.ErrNotFound
	}
	return &rows[0], nil
}

// getOrder: GET /api/v1/orders/{id}, with its payment attempts (when the
// payments service answers) and what staff may do with it.
func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	o, err := s.order(r, id)
	if err != nil {
		s.fail(w, "get order", err)
		return
	}
	var payments []paymentJSON
	paymentsErr := ""
	if recs, _, err := s.cfg.Payments.ListPayments(r.Context(), domain.PaymentFilter{OrderID: id}, 1, 50); err != nil {
		s.cfg.Log.Warn("dashboard api: order payments", "err", err)
		paymentsErr = "the payments service did not answer"
	} else {
		payments = s.paymentsOf(r.Context(), recs)
	}
	paid := o.Status == "paid" || o.Status == "active" || o.Status == "provision_failed"
	writeJSON(w, http.StatusOK, map[string]any{
		"order": orderOf(o), "payments": payments, "payments_error": paymentsErr,
		"can": map[string]bool{
			"retry":  o.Status == "provision_failed",
			"refund": paid && o.RefundedAt == nil && o.Amount > 0,
		},
	})
}

// retryOrder: POST /api/v1/orders/{id}/retry runs a failed delivery now.
func (s *Server) retryOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.RetryOrder(r.Context(), actor(r), id); err != nil {
		s.fail(w, "retry order", err)
		return
	}
	o, err := s.order(r, id)
	if err != nil {
		s.fail(w, "reload order", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": orderOf(o)})
}

// refundOrder: POST /api/v1/orders/{id}/refund {reason} refunds to the wallet.
func (s *Server) refundOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req reasonBody
	if !readJSON(w, r, &req) {
		return
	}
	res, err := s.cfg.Domain.RefundOrder(r.Context(), actor(r), s.cfg.Provisioner, id, req.Reason)
	if err != nil {
		s.fail(w, "refund order", err)
		return
	}
	o, err := s.order(r, id)
	if err != nil {
		s.fail(w, "reload order", err)
		return
	}
	out := map[string]any{"order": orderOf(o), "wallet": money{res.Wallet.Balance, res.Wallet.Currency}}
	if res.PanelCleanup != "" {
		s.cfg.Log.Warn("dashboard api: refund cleanup", "order_id", id, "err", res.PanelCleanup)
		out["warning"] = "refunded; removing the unfinished account from the 3x-ui panel failed: remove it there by hand"
	}
	writeJSON(w, http.StatusOK, out)
}

type proofJSON struct {
	HasFile     bool       `json:"has_file"`
	Reference   string     `json:"reference"`
	Network     string     `json:"network"`
	TXID        string     `json:"txid"`
	SubmittedAt int64      `json:"submitted_at"`
	Decision    string     `json:"decision"`
	Reason      string     `json:"reason"`
	ReviewedBy  *userBrief `json:"reviewed_by"`
	ReviewedAt  *int64     `json:"reviewed_at"`
}

type paymentJSON struct {
	ID            string     `json:"id"`
	OrderID       string     `json:"order_id"`
	User          *userBrief `json:"user"`
	Provider      string     `json:"provider"`
	Status        string     `json:"status"`
	Amount        money      `json:"amount"`
	GatewayAmount *money     `json:"gateway_amount"`
	ProviderRef   string     `json:"provider_ref"`
	FailureReason string     `json:"failure_reason"`
	CreatedAt     int64      `json:"created_at"`
	Proof         *proofJSON `json:"proof"`
}

func (s *Server) paymentsOf(ctx context.Context, recs []domain.PaymentRecord) []paymentJSON {
	var ids []string
	for _, rec := range recs {
		ids = append(ids, rec.UserID)
		if rec.Proof != nil && rec.Proof.ReviewedBy != "" {
			ids = append(ids, rec.Proof.ReviewedBy)
		}
	}
	users := s.usersByID(ctx, ids)
	out := make([]paymentJSON, 0, len(recs))
	for _, rec := range recs {
		p := paymentJSON{
			ID: rec.IntentID, OrderID: rec.OrderID, User: users[rec.UserID], Provider: rec.Provider, Status: rec.Status,
			Amount: money{rec.Amount, rec.Currency}, ProviderRef: rec.ProviderRef, FailureReason: rec.FailureReason,
			CreatedAt: rec.CreatedAt.Unix(),
		}
		if rec.GatewayCurrency != "" {
			p.GatewayAmount = &money{rec.GatewayAmount, rec.GatewayCurrency}
		}
		if pr := rec.Proof; pr != nil {
			p.Proof = &proofJSON{
				HasFile: pr.ReceiptFile != "", Reference: pr.ReferenceNumber, Network: pr.Network, TXID: pr.TXID,
				SubmittedAt: pr.SubmittedAt.Unix(), Decision: pr.Decision, Reason: pr.Reason,
				ReviewedBy: users[pr.ReviewedBy], ReviewedAt: unix(pr.ReviewedAt),
			}
		}
		out = append(out, p)
	}
	return out
}

// listPayments: GET /api/v1/payments?status=&provider=&user=&page=&size=,
// the payment history.
func (s *Server) listPayments(w http.ResponseWriter, r *http.Request) {
	_, page, size := pageOf(r)
	userID, ok := queryID(r, "user")
	if !ok {
		writeJSON(w, http.StatusOK, list{[]paymentJSON{}, 0, page, size})
		return
	}
	q := r.URL.Query()
	recs, total, err := s.cfg.Payments.ListPayments(r.Context(), domain.PaymentFilter{
		UserID: userID, Status: q.Get("status"), Provider: q.Get("provider"),
	}, page, size)
	if err != nil {
		s.fail(w, "list payments", err)
		return
	}
	writeJSON(w, http.StatusOK, list{s.paymentsOf(r.Context(), recs), total, page, size})
}

// pendingPayments: GET /api/v1/payments/pending, the review queue (oldest
// first).
func (s *Server) pendingPayments(w http.ResponseWriter, r *http.Request) {
	pending, err := s.cfg.Payments.ListPending(r.Context(), 200)
	if err != nil {
		s.fail(w, "pending payments", err)
		return
	}
	ids := make([]string, 0, len(pending))
	for _, p := range pending {
		ids = append(ids, p.UserID)
	}
	users := s.usersByID(r.Context(), ids)
	items := make([]map[string]any, 0, len(pending))
	for _, p := range pending {
		items = append(items, map[string]any{
			"id": p.IntentID, "order_id": p.OrderID, "user": users[p.UserID], "provider": p.Provider,
			"amount": money{p.Amount, p.Currency}, "reference": p.ReferenceNumber, "network": p.Network, "txid": p.TXID,
			"has_file": p.ReceiptFile != "", "submitted_at": p.SubmittedAt.Unix(), "possible_duplicate": p.PossibleDuplicate,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// paymentReceipt: GET /api/v1/payments/{id}/receipt, the receipt photo the
// customer sent (fetched through the bot).
func (s *Server) paymentReceipt(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if s.cfg.Files == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "receipt photos are not set up on this install (BOBRES_BOT_URL)")
		return
	}
	recs, _, err := s.cfg.Payments.ListPayments(r.Context(), domain.PaymentFilter{IntentID: id}, 1, 1)
	if err != nil {
		s.fail(w, "receipt lookup", err)
		return
	}
	if len(recs) == 0 || recs[0].Proof == nil || recs[0].Proof.ReceiptFile == "" {
		writeError(w, http.StatusNotFound, "not_found", "this payment has no receipt photo")
		return
	}
	f, err := s.cfg.Files.Fetch(r.Context(), recs[0].Proof.ReceiptFile)
	switch {
	case errors.Is(err, botfiles.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Telegram no longer has this photo")
		return
	case errors.Is(err, botfiles.ErrRefused):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported", "the receipt is too large or not an image; open it in Telegram")
		return
	case err != nil:
		s.cfg.Log.Warn("dashboard api: receipt fetch", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot did not answer; try again in a minute")
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(f.Data)))
	h.Set("Cache-Control", "private, max-age=600") // a receipt never changes
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	if f.ContentType == "application/pdf" {
		h.Set("Content-Disposition", `attachment; filename="receipt-`+id[:8]+`.pdf"`)
	} else {
		h.Set("Content-Disposition", "inline")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.Data)
}

// reviewPayment: POST /api/v1/payments/{id}/review {decision, reason}
func (s *Server) reviewPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Decision == "rejected" && len(req.Reason) == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "say why the payment is rejected (the customer sees it)")
		return
	}
	status, err := s.cfg.Domain.ReviewPayment(r.Context(), actor(r), s.cfg.Payments, id, req.Decision, req.Reason)
	if err != nil {
		s.fail(w, "review payment", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}
