package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BOBRES licenses sold in the owner's own bot (docs/13-license-server.md). The bot holds no rule: core prices the
// offers, takes the money from the wallet and asks the license server for the key. A buyer short of money tops the
// wallet up in USDT (automatic TRC20, or by hand) and comes back. The menu button exists only while the owner has
// `vendor.enabled` on, and core refuses everything while it is off.
//
// The callbacks are "lic" (home), "lic:b:<tier>:<months>" (confirm a new license), "lic:y:<tier>:<months>:<nonce>"
// (pay), "lic:my", "lic:k:<id>" (show the key), "lic:r:<id>:<months>" (confirm a renewal) and
// "lic:ry:<id>:<months>:<nonce>" (pay it); "lpay:<method>:<amount>:<nonce>" is the USDT top-up.

// licensesOn is the owner's switch (vendor.enabled, off unless it is true).
func (h *Handler) licensesOn() bool { return h.setting("vendor.enabled") == "true" }

// licenseTopupStep is the least a USDT top-up asks for (0.01 USDT in minor units): the shortfall is rounded up to it.
const licenseTopupStep = 10_000

func (h *Handler) onLicense(r *req, rest string) {
	if !h.licensesOn() {
		r.toast, r.alert = r.t("lic.unavailable"), true
		h.showHome(r)
		return
	}
	parts := strings.Split(rest, ":")
	switch {
	case rest == "":
		h.showLicenses(r)
	case parts[0] == "b" && len(parts) == 3:
		h.confirmLicense(r, parts[1], "", parts[2])
	case parts[0] == "y" && len(parts) == 4:
		h.buyLicense(r, parts[1], "", parts[2], parts[3])
	case parts[0] == "r" && len(parts) == 3:
		h.confirmLicense(r, "", parts[1], parts[2])
	case parts[0] == "ry" && len(parts) == 4:
		h.buyLicense(r, "", parts[1], parts[2], parts[3])
	case parts[0] == "my":
		h.showMyLicenses(r)
	case parts[0] == "k" && len(parts) == 2:
		h.showLicenseKey(r, parts[1])
	default:
		h.showLicenses(r)
	}
}

func tierName(r *req, tier string) string {
	if tier == "standard" || tier == "pro" {
		return r.t("lic.tier." + tier)
	}
	return tier
}

func termName(r *req, months int32) string {
	if months == 1 || months == 12 {
		return r.t("lic.term." + strconv.Itoa(int(months)))
	}
	return strconv.Itoa(int(months))
}

func licenseEnd(r *req, unix int64) string {
	if unix == 0 {
		return r.t("lic.no_end")
	}
	return i18n.Date(r.lang, time.Unix(unix, 0))
}

// offerFor finds the offer of tier for months.
func offerFor(offers []*corev1.VendorOffer, tier string, months int32) *corev1.VendorOffer {
	for _, o := range offers {
		if o.GetTier() == tier && o.GetMonths() == months {
			return o
		}
	}
	return nil
}

func (h *Handler) licenseOffers(r *req) (*corev1.ListVendorOffersResponse, bool) {
	resp, err := h.core.ListVendorOffers(r.ctx, &corev1.ListVendorOffersRequest{UserId: r.user.GetId()})
	if err != nil {
		r.fail(err)
		return nil, false
	}
	if !resp.GetOn() || len(resp.GetOffers()) == 0 {
		r.toast, r.alert = r.t("lic.unavailable"), true
		h.showHome(r)
		return nil, false
	}
	return resp, true
}

// showLicenses is the licenses home: the offers, and the buyer's own licenses.
func (h *Handler) showLicenses(r *req) {
	resp, ok := h.licenseOffers(r)
	if !ok {
		return
	}
	kb := &tg.Keyboard{}
	for _, tier := range []string{"standard", "pro"} {
		var row []tg.Button
		for _, months := range []int32{1, 12} {
			if o := offerFor(resp.GetOffers(), tier, months); o != nil {
				label := r.t("lic.offer_btn", "tier", tierName(r, tier), "term", termName(r, months),
					"price", r.money(o.GetPrice().GetAmount(), o.GetPrice().GetCurrency()))
				row = append(row, tg.CB(label, fmt.Sprintf("lic:b:%s:%d", tier, months)))
			}
		}
		kb.Row(row...)
	}
	kb.Row(r.button("btn.lic_my", "lic:my"), r.button("btn.home", "home"))
	bal := resp.GetBalance()
	r.show(r.t("lic.title", "balance", r.balance(bal.GetAmount(), bal.GetCurrency())), kb)
}

// confirmLicense shows what a purchase costs and either pays it or offers to top up the shortfall. A new license
// has a tier; a renewal names the license (and keeps its tier).
func (h *Handler) confirmLicense(r *req, tier, licenseID, monthsArg string) {
	m, err := strconv.Atoi(monthsArg)
	if err != nil || (m != 1 && m != 12) {
		h.showLicenses(r)
		return
	}
	months := int32(m) //nolint:gosec // 1 or 12
	resp, ok := h.licenseOffers(r)
	if !ok {
		return
	}
	back := "lic"
	if licenseID != "" {
		mine, err := h.core.ListMyVendorLicenses(r.ctx, &corev1.ListMyVendorLicensesRequest{UserId: r.user.GetId()})
		if err != nil {
			r.fail(err)
			return
		}
		for _, l := range mine.GetLicenses() {
			if l.GetId() == licenseID {
				tier = l.GetTier()
			}
		}
		back = "lic:my"
	}
	o := offerFor(resp.GetOffers(), tier, months)
	if o == nil {
		h.showLicenses(r)
		return
	}
	price, cur := o.GetPrice().GetAmount(), o.GetPrice().GetCurrency()
	bal := resp.GetBalance().GetAmount()
	n := nonce()
	text := r.t("lic.confirm", "tier", tierName(r, tier), "term", termName(r, months), "price", r.money(price, cur),
		"balance", r.balance(bal, cur))
	kb := &tg.Keyboard{}
	if bal >= price {
		data := fmt.Sprintf("lic:y:%s:%d:%s", tier, months, n)
		if licenseID != "" {
			data = fmt.Sprintf("lic:ry:%s:%d:%s", licenseID, months, n)
		}
		kb.Row(tg.CB(r.t("btn.lic_pay", "price", r.money(price, cur)), data))
	} else {
		missing := (price - bal + licenseTopupStep - 1) / licenseTopupStep * licenseTopupStep
		text += "\n\n" + r.t("lic.insufficient", "missing", r.money(missing, cur))
		var methods []tg.Button
		if h.cryptoEnabled(cur) {
			methods = append(methods, r.button("btn.pay_crypto", fmt.Sprintf("lpay:x:%d:%s", missing, n)))
		}
		gw := h.gatewayButtons(r, cur, missing, "lpay", fmt.Sprint(missing), n)
		if len(gw) == 0 && len(methods) == 0 {
			text += "\n" + r.t("pay.not_configured")
		}
		rows(kb, gw, 2)
		kb.Row(methods...)
	}
	kb.Row(r.button("btn.back", back), r.button("btn.home", "home"))
	r.show(text, kb)
}

// buyLicense pays a purchase from the wallet. The nonce in the button makes a double tap one purchase.
func (h *Handler) buyLicense(r *req, tier, licenseID, monthsArg, n string) {
	m, err := strconv.Atoi(monthsArg)
	if err != nil || (m != 1 && m != 12) || n == "" {
		h.showLicenses(r)
		return
	}
	months := int32(m) //nolint:gosec // 1 or 12
	subject := tier
	if licenseID != "" {
		subject = licenseID
	}
	resp, err := h.core.BuyVendorLicense(r.ctx, &corev1.BuyVendorLicenseRequest{
		UserId: r.user.GetId(), Tier: tier, Months: months, LicenseId: licenseID,
		IdempotencyKey: fmt.Sprintf("lic:%d:%s:%d:%s", r.from.ID, subject, months, n),
	})
	if err != nil {
		st, _ := status.FromError(err)
		switch {
		case st.Code() == codes.FailedPrecondition && strings.Contains(st.Message(), "insufficient"):
			h.confirmLicense(r, tier, licenseID, monthsArg) // the shortfall and the top-up buttons
		case st.Code() == codes.FailedPrecondition:
			h.showLicenses(r)
		default:
			r.fail(err)
		}
		return
	}
	home := (&tg.Keyboard{}).Row(r.button("btn.lic_my", "lic:my"), r.button("btn.home", "home"))
	switch resp.GetOrderStatus() {
	case "done":
		l := resp.GetLicense()
		if licenseID != "" {
			r.show(r.t("lic.renewed", "tier", tierName(r, l.GetTier()), "end", licenseEnd(r, l.GetEndsAt())), home)
			return
		}
		r.show(r.t("lic.bought", "tier", tierName(r, l.GetTier()), "end", licenseEnd(r, l.GetEndsAt()), "key", esc(resp.GetKey())), home)
	case "paid":
		r.show(r.t("lic.pending"), home)
	default: // refunded
		r.show(r.t("lic.refunded"), home)
	}
}

// showMyLicenses lists the buyer's licenses with a Key and a Renew button each.
func (h *Handler) showMyLicenses(r *req) {
	resp, err := h.core.ListMyVendorLicenses(r.ctx, &corev1.ListMyVendorLicensesRequest{UserId: r.user.GetId()})
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	if len(resp.GetLicenses()) == 0 {
		kb.Row(r.button("btn.lic_home", "lic"), r.button("btn.home", "home"))
		r.show(r.t("lic.my.empty"), kb)
		return
	}
	lines := []string{r.t("lic.my.title"), ""}
	for _, l := range resp.GetLicenses() {
		lines = append(lines, r.t("lic.my.line", "tier", tierName(r, l.GetTier()), "end", licenseEnd(r, l.GetEndsAt()),
			"status", r.t("lic.status."+l.GetStatus())))
		kb.Row(tg.CB(r.t("btn.lic_key", "tier", tierName(r, l.GetTier())), "lic:k:"+l.GetId()),
			tg.CB(r.t("btn.lic_renew"), "lic:r:"+l.GetId()+":1"),
			tg.CB(r.t("btn.lic_renew_year"), "lic:r:"+l.GetId()+":12"))
	}
	kb.Row(r.button("btn.lic_home", "lic"), r.button("btn.home", "home"))
	r.show(strings.Join(lines, "\n"), kb)
}

// showLicenseKey sends a license's activation key to its buyer.
func (h *Handler) showLicenseKey(r *req, id string) {
	resp, err := h.core.GetVendorLicenseKey(r.ctx, &corev1.GetVendorLicenseKeyRequest{UserId: r.user.GetId(), LicenseId: id})
	if err != nil {
		r.fail(err)
		return
	}
	kb := backHome(r, "lic:my")
	r.show(r.t("lic.key", "tier", tierName(r, resp.GetLicense().GetTier()), "key", esc(resp.GetKey())), kb)
}

// onLicenseTopup handles "lpay:<x|u>:<amount>:<nonce>": a USDT top-up for the shortfall of a license.
func (h *Handler) onLicenseTopup(r *req, rest string) {
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		h.showLicenses(r)
		return
	}
	amount, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || amount <= 0 || amount > 100_000_000_000 {
		h.showLicenses(r)
		return
	}
	provider := map[string]string{"x": "manual_crypto", "u": usdtAuto}[parts[0]]
	if provider == "" {
		h.showLicenses(r)
		return
	}
	key := fmt.Sprintf("ltop:%d:%d:%s:%s", r.from.ID, amount, provider, parts[2])
	if provider == usdtAuto {
		h.startGatewayIn(r, nil, provider, r.t("pay.topup_title"), key, amount, "USDT")
		return
	}
	h.startManualIn(r, nil, provider, key, amount, "USDT")
}
