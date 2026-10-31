package web

import (
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The Affiliates page (docs/04-dashboard.md, Affiliates; docs/18-hosted-and-affiliate-plans.md, section 2): the affiliate program of the store
// that sells BOBRES licenses. The owner switches it on and sets its percent and minimum payout, sees each affiliate's code, the licenses they
// brought, what they earned and what they were paid, gives one a percent of their own or switches them off, and records a payout made by
// hand. Owner only (license.write), and only where the license grants the vendor's own features. Money is in Toman.

type affiliateSettingsJSON struct {
	Enabled   bool  `json:"enabled"`
	On        bool  `json:"on"`
	VendorOn  bool  `json:"vendor_on"`
	Percent   int   `json:"percent"`
	MinPayout int64 `json:"min_payout"`
}

type affiliateJSON struct {
	ID        string     `json:"id"`
	Code      string     `json:"code"`
	Percent   *int       `json:"percent"`
	Status    string     `json:"status"`
	CreatedAt int64      `json:"created_at"`
	User      *userBrief `json:"user"`
	Referred  int64      `json:"referred"`
	Licenses  int64      `json:"licenses"`
	Earned    money      `json:"earned"`
	Paid      money      `json:"paid"`
	Balance   money      `json:"balance"`
}

func toman(n int64) money { return money{Amount: n, Currency: domain.WalletCurrency} }

func (s *Server) affiliateSettings(r *http.Request) (*affiliateSettingsJSON, error) {
	st, err := s.cfg.Domain.GetAffiliateSettings(r.Context())
	if err != nil {
		return nil, err
	}
	return &affiliateSettingsJSON{Enabled: st.Enabled, On: st.On, VendorOn: st.VendorOn, Percent: st.Percent, MinPayout: st.MinPayout}, nil
}

// listAffiliates: GET /api/v1/affiliates?page=&size= -> {settings, items, total, page, size}.
func (s *Server) listAffiliates(w http.ResponseWriter, r *http.Request) {
	set, err := s.affiliateSettings(r)
	if err != nil {
		s.fail(w, "affiliate settings", err)
		return
	}
	p, page, size := pageOf(r)
	rows, total, err := s.cfg.Domain.ListAffiliates(r.Context(), p)
	if err != nil {
		s.fail(w, "list affiliates", err)
		return
	}
	items := make([]affiliateJSON, 0, len(rows))
	for i := range rows {
		a := &rows[i]
		items = append(items, affiliateJSON{ID: a.ID, Code: a.Code, Percent: a.Percent, Status: a.Status, CreatedAt: a.CreatedAt.Unix(),
			User: briefOf(a.UserID, a.TelegramID, a.Username), Referred: a.Referred, Licenses: a.Licenses,
			Earned: toman(a.Earned), Paid: toman(a.Paid), Balance: toman(a.Balance)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": set, "items": items, "total": total, "page": page, "size": size})
}

// putAffiliateSettings: PUT /api/v1/affiliates/settings {enabled, percent, min_payout}.
func (s *Server) putAffiliateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled   bool  `json:"enabled"`
		Percent   int   `json:"percent"`
		MinPayout int64 `json:"min_payout"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.cfg.Domain.SetAffiliateSettings(r.Context(), actor(r), req.Enabled, req.Percent, req.MinPayout); err != nil {
		s.fail(w, "save affiliate settings", err)
		return
	}
	s.refreshBot()
	set, err := s.affiliateSettings(r)
	if err != nil {
		s.fail(w, "affiliate settings", err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

type affiliateCommissionJSON struct {
	OrderID    string     `json:"order_id"`
	Buyer      *userBrief `json:"buyer"`
	Tier       string     `json:"tier"`
	LicenseID  string     `json:"license_id"`
	Status     string     `json:"order_status"`
	Base       money      `json:"base"`
	Percent    int        `json:"percent"`
	Amount     money      `json:"amount"`
	CreatedAt  int64      `json:"created_at"`
	Reversed   *money     `json:"reversed"`
	ReversedAt *int64     `json:"reversed_at"`
}

type affiliatePayoutJSON struct {
	ID        string `json:"id"`
	Amount    money  `json:"amount"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"created_at"`
}

func payoutOf(p *store.AffiliatePayout) affiliatePayoutJSON {
	return affiliatePayoutJSON{ID: p.ID, Amount: money{p.Amount, p.Currency}, Note: p.Note, CreatedAt: p.CreatedAt.Unix()}
}

// getAffiliate: GET /api/v1/affiliates/{id}: the affiliate, their commissions (100 newest) and payouts (100 newest).
func (s *Server) getAffiliate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	d, err := s.cfg.Domain.GetAffiliateDetail(r.Context(), id)
	if err != nil {
		s.fail(w, "affiliate", err)
		return
	}
	a := d.Affiliate
	out := map[string]any{
		"affiliate": affiliateJSON{ID: a.ID, Code: a.Code, Percent: a.Percent, Status: a.Status, CreatedAt: a.CreatedAt.Unix(),
			User: briefOf(d.User.ID, d.User.TelegramID, d.User.Username), Referred: d.Stats.Referred, Licenses: d.Stats.Licenses,
			Earned: toman(d.Stats.Earned[domain.WalletCurrency]), Paid: toman(d.Stats.Paid[domain.WalletCurrency]), Balance: toman(d.Balance)},
		"effective_percent": d.Percent,
	}
	cs := make([]affiliateCommissionJSON, 0, len(d.Commissions))
	for i := range d.Commissions {
		c := &d.Commissions[i]
		j := affiliateCommissionJSON{OrderID: c.OrderID, Buyer: briefOf(c.BuyerUserID, c.BuyerTelegram, c.BuyerUsername), Tier: c.Tier,
			LicenseID: c.LicenseID, Status: c.OrderStatus, Base: money{c.BaseAmount, c.Currency}, Percent: c.Percent,
			Amount: money{c.Amount, c.Currency}, CreatedAt: c.CreatedAt.Unix(), ReversedAt: unix(c.ReversedAt)}
		if c.ReversedAmount != nil {
			j.Reversed = &money{*c.ReversedAmount, c.Currency}
		}
		cs = append(cs, j)
	}
	ps := make([]affiliatePayoutJSON, 0, len(d.Payouts))
	for i := range d.Payouts {
		ps = append(ps, payoutOf(&d.Payouts[i]))
	}
	out["commissions"], out["payouts"] = cs, ps
	writeJSON(w, http.StatusOK, out)
}

// putAffiliate: PUT /api/v1/affiliates/{id} {percent: null|0..100, status: active|disabled}.
func (s *Server) putAffiliate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Percent *int   `json:"percent"`
		Status  string `json:"status"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if _, err := s.cfg.Domain.UpdateAffiliate(r.Context(), actor(r), id, req.Percent, req.Status); err != nil {
		s.fail(w, "update affiliate", err)
		return
	}
	s.getAffiliate(w, r)
}

// postAffiliatePayout: POST /api/v1/affiliates/{id}/payouts {amount, note, request_id}: a payout made by hand leaves the affiliate's wallet.
func (s *Server) postAffiliatePayout(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Amount    int64  `json:"amount"`
		Note      string `json:"note"`
		RequestID string `json:"request_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.cfg.Domain.RecordAffiliatePayout(r.Context(), actor(r), id, req.Amount, req.Note, req.RequestID)
	if err != nil {
		s.fail(w, "record a payout", err)
		return
	}
	writeJSON(w, http.StatusOK, payoutOf(p))
}

// refundSoldLicense: POST /api/v1/vendor/licenses/{id}/refund {reason}: the buyer gets back what they paid, the license is revoked, and an
// affiliate's commission is taken back when within 30 days.
func (s *Server) refundSoldLicense(w http.ResponseWriter, r *http.Request) {
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
	o, err := s.cfg.Domain.RefundSoldLicense(r.Context(), actor(r), id, req.Reason)
	if err != nil {
		s.vendorFail(w, "refund a license", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order_id": o.ID, "status": o.Status, "amount": money{o.Amount, o.Currency}})
}
