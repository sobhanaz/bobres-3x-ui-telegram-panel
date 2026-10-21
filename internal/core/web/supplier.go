package web

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Digital accounts resold from a supplier, in the dashboard (docs/supplier.md): the owner sets the supplier's API key
// (write-only: no answer ever carries it), reads the catalog, writes the price in Toman and the texts of each product and
// switches it on, and watches the orders (retry one, refund one the supplier never delivers). Owner only
// (gateways.write). What was delivered (an account) is never in an answer.

var supplierProductRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)

func supplierProductID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !supplierProductRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "no such product")
		return "", false
	}
	return id, true
}

// supplierFail answers a failure of a call to the supplier: its refusal keeps its meaning, one that did not answer is a
// 502 the owner can retry.
func (s *Server) supplierFail(w http.ResponseWriter, what string, err error) {
	var se *domain.SupplierError
	switch {
	case errors.As(err, &se):
		switch {
		case se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden:
			writeError(w, http.StatusConflict, "state", "the supplier does not accept this API key: check it")
		case se.Status >= 400 && se.Status < 500:
			writeError(w, http.StatusConflict, "state", "the supplier refused: "+se.Message)
		default:
			writeError(w, http.StatusBadGateway, "unavailable", "the supplier did not answer; try again in a minute")
		}
	case errors.Is(err, domain.ErrState) || errors.Is(err, domain.ErrInvalid) || errors.Is(err, store.ErrNotFound):
		s.fail(w, what, err)
	case strings.Contains(err.Error(), "supplier") || strings.Contains(err.Error(), "the bot"):
		writeError(w, http.StatusBadGateway, "unavailable", "the supplier did not answer; try again in a minute")
	default:
		s.fail(w, what, err)
	}
}

// getSupplier: GET /api/v1/supplier.
func (s *Server) getSupplier(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeJSON(w, http.StatusOK, map[string]any{
		"available": s.cfg.Domain.SupplierAvailable(), "configured": s.cfg.Domain.SupplierConfigured(ctx), "on": s.cfg.Domain.SupplierOn(ctx),
	})
}

// putSupplierConfig: PUT /api/v1/supplier/config {api_key}.
func (s *Server) putSupplierConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.cfg.Domain.SetSupplierKey(r.Context(), actor(r), req.APIKey); err != nil {
		s.fail(w, "set the supplier key", err)
		return
	}
	s.getSupplier(w, r)
}

// getSupplierBalance: GET /api/v1/supplier/balance: the balance of the API account at the supplier.
func (s *Server) getSupplierBalance(w http.ResponseWriter, r *http.Request) {
	amount, cur, err := s.cfg.Domain.SupplierBalance(r.Context())
	if err != nil {
		s.supplierFail(w, "read the supplier balance", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"amount": amount, "currency": cur})
}

// syncSupplier: POST /api/v1/supplier/sync: read the catalog now.
func (s *Server) syncSupplier(w http.ResponseWriter, r *http.Request) {
	n, err := s.cfg.Domain.SyncSupplierCatalog(r.Context())
	if err != nil {
		s.supplierFail(w, "read the supplier catalog", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

type supplierProductJSON struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	CostMicro    *int64            `json:"cost_micro"`
	CostCurrency string            `json:"cost_currency"`
	Stock        *int32            `json:"stock"`
	Listed       bool              `json:"listed"`
	Enabled      bool              `json:"enabled"`
	SellPrice    *int64            `json:"sell_price"`
	Title        map[string]string `json:"title"`
	Desc         map[string]string `json:"desc"`
	Sort         int32             `json:"sort"`
	SyncedAt     *int64            `json:"synced_at"`
}

func supplierProductOf(p *store.SupplierProduct) supplierProductJSON {
	return supplierProductJSON{ID: p.ID, Name: p.Name, Description: p.Description, CostMicro: p.CostMicro, CostCurrency: p.CostCurrency, Stock: p.Stock,
		Listed: p.Listed, Enabled: p.Enabled, SellPrice: p.SellPrice, Title: p.Title, Desc: p.Desc, Sort: p.Sort, SyncedAt: unix(p.SyncedAt)}
}

// listSupplierProducts: GET /api/v1/supplier/products.
func (s *Server) listSupplierProducts(w http.ResponseWriter, r *http.Request) {
	ps, err := s.cfg.Domain.ListSupplierProducts(r.Context())
	if err != nil {
		s.fail(w, "list the supplier products", err)
		return
	}
	items := make([]supplierProductJSON, 0, len(ps))
	for i := range ps {
		items = append(items, supplierProductOf(&ps[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

// updateSupplierProduct: PUT /api/v1/supplier/products/{id} {enabled, sell_price, title, desc, sort}: any of them.
func (s *Server) updateSupplierProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := supplierProductID(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled   *bool             `json:"enabled"`
		SellPrice *int64            `json:"sell_price"`
		Title     map[string]string `json:"title"`
		Desc      map[string]string `json:"desc"`
		Sort      *int32            `json:"sort"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.cfg.Domain.UpdateSupplierProduct(r.Context(), actor(r), id, domain.SupplierEdit{Enabled: req.Enabled, SellPrice: req.SellPrice, Title: req.Title, Desc: req.Desc, Sort: req.Sort})
	if err != nil {
		s.fail(w, "change a supplier product", err)
		return
	}
	writeJSON(w, http.StatusOK, supplierProductOf(p))
}

type supplierOrderJSON struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Product     string     `json:"product"`
	ProductID   string     `json:"product_id"`
	Quantity    int        `json:"quantity"`
	Amount      money      `json:"amount"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error"`
	CreatedAt   int64      `json:"created_at"`
	DeliveredAt *int64     `json:"delivered_at"`
	Buyer       *userBrief `json:"buyer"`
}

// listSupplierOrders: GET /api/v1/supplier/orders?status=&page=&size= (newest first).
func (s *Server) listSupplierOrders(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	rows, total, err := s.cfg.Domain.ListSupplierOrdersAdmin(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")), p)
	if err != nil {
		s.fail(w, "list the supplier orders", err)
		return
	}
	items := make([]supplierOrderJSON, 0, len(rows))
	for i := range rows {
		o := &rows[i]
		items = append(items, supplierOrderJSON{ID: o.ID, Status: o.Status, Product: o.ProductName, ProductID: o.ProductID, Quantity: o.Quantity,
			Amount: money{o.Amount, o.Currency}, Attempts: o.Attempts, LastError: o.LastError, CreatedAt: o.CreatedAt.Unix(), DeliveredAt: unix(o.DeliveredAt),
			Buyer: briefOf(o.UserID, o.TelegramID, o.Username)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "size": size})
}

// retrySupplierOrder: POST /api/v1/supplier/orders/{id}/retry: ask the supplier again now.
func (s *Server) retrySupplierOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	o, err := s.cfg.Domain.RetrySupplierOrder(r.Context(), id)
	if err != nil && o == nil {
		s.supplierFail(w, "retry a supplier order", err)
		return
	}
	// A retry that did not deliver is not an error here: the order's state is the answer.
	writeJSON(w, http.StatusOK, map[string]any{"id": o.ID, "status": o.Status, "last_error": o.LastError})
}

// refundSupplierOrder: POST /api/v1/supplier/orders/{id}/refund {reason}: the money back for an order the supplier never delivers.
func (s *Server) refundSupplierOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	o, err := s.cfg.Domain.RefundSupplierOrder(r.Context(), actor(r), id, req.Reason)
	if err != nil {
		s.fail(w, "refund a supplier order", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": o.ID, "status": o.Status})
}
