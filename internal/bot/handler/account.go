package handler

import (
	"fmt"
	"strings"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Top-up bounds and presets, in Toman.
const (
	minTopup = 10_000
	maxTopup = 50_000_000
)

var topupPresets = []int64{100_000, 200_000, 500_000, 1_000_000}

// A top-up in USDT (whole USDT): the money is added to the one wallet, in Toman, at the live rate.
const (
	minTopupUsdt = 1
	maxTopupUsdt = 10_000
)

var topupUsdtPresets = []int64{5, 10, 25, 50, 100}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 6 {
		return id[len(id)-6:]
	}
	return id
}

func (h *Handler) expires(r *req, unix int64) string {
	if unix <= 0 {
		return r.t("sub.no_expiry")
	}
	return i18n.Date(r.lang, time.Unix(unix, 0))
}

func (h *Handler) traffic(r *req, b int64) string {
	if b <= 0 {
		return r.t("sub.unlimited")
	}
	return i18n.Bytes(r.lang, b)
}

func (h *Handler) subscriptions(r *req) ([]*corev1.Subscription, error) {
	resp, err := h.core.ListSubscriptions(r.ctx, &corev1.ListSubscriptionsRequest{UserId: r.user.GetId()})
	if err != nil {
		return nil, err
	}
	return resp.GetSubscriptions(), nil
}

// subscription finds one of the user's services by its id (nil when it is not
// theirs). Core searches by the whole id, so a service older than the newest
// 100 is found too; a name that happens to contain the id is skipped.
func (h *Handler) subscription(r *req, id string) (*corev1.Subscription, error) {
	resp, err := h.core.ListSubscriptions(r.ctx, &corev1.ListSubscriptionsRequest{UserId: r.user.GetId(), Query: id,
		Pagination: &commonv1.Pagination{Page: 1, PageSize: 50}})
	if err != nil {
		return nil, err
	}
	for _, s := range resp.GetSubscriptions() {
		if s.GetId() == id {
			return s, nil
		}
	}
	return nil, nil
}

// serviceButton names a service in a list: its name (a reseller's) or its
// short id, its status and when it ends.
func (h *Handler) serviceButton(r *req, s *corev1.Subscription) string {
	name := "#" + shortID(s.GetId())
	if s.GetLabel() != "" {
		name = s.GetLabel()
	}
	return name + " · " + r.t("subs.item", "status", statusText(r, s.GetStatus()), "expires", h.expires(r, s.GetExpiresAt()))
}

func (h *Handler) showServices(r *req) {
	if r.isReseller() { // their customers' services: paged, with a search
		h.showCustomerServices(r, 1, "")
		return
	}
	subs, err := h.subscriptions(r)
	if err != nil {
		r.fail(err)
		return
	}
	if len(subs) == 0 {
		r.show(r.t("subs.empty"), (&tg.Keyboard{}).Row(r.button("btn.buy", "buy")).Row(r.button("btn.find_link", "fl")).Row(r.button("btn.home", "home")))
		return
	}
	kb := &tg.Keyboard{}
	for i, s := range subs {
		if i == 20 {
			break
		}
		kb.Row(tg.CB(h.serviceButton(r, s), "sub:"+s.GetId()))
	}
	kb.Row(r.button("btn.find_link", "fl"))
	kb.Row(r.button("btn.home", "home"))
	r.show(r.t("subs.title"), kb)
}

func (h *Handler) showService(r *req, id string) {
	s, err := h.subscription(r, id)
	if err != nil {
		r.fail(err)
		return
	}
	if s == nil {
		h.showServices(r)
		return
	}
	if s.GetStatus() == "pending" || s.GetSubscriptionLink() == "" {
		kb := backHome(r, "subs")
		if r.isReseller() { // a name and a note can be given before the service is ready
			kb = (&tg.Keyboard{}).Row(r.button("btn.rename", "rs:rn:"+id), r.button("btn.rs_note", "rs:nt:"+id)).
				Row(r.button("btn.back", "subs"), r.button("btn.home", "home"))
		}
		r.show(r.labelled(s.GetLabel(), r.t("sub.not_ready")), kb)
		return
	}
	kb := &tg.Keyboard{}
	var extend []tg.Button
	if renewable(s) {
		extend = append(extend, r.button("btn.renew", "rnw:"+id))
	}
	if toppable(s) && len(h.topupPlans(r)) > 0 {
		extend = append(extend, r.button("btn.add_traffic", "tup:"+id))
	}
	kb.Row(extend...)
	if r.isReseller() {
		kb.Row(r.button("btn.qr", "qr:"+id), r.button("btn.rename", "rs:rn:"+id))
		kb.Row(manageButtons(r, s)...)
	} else {
		kb.Row(r.button("btn.qr", "qr:"+id))
	}
	// The branded page (the core fills page_url while the page is on): a URL
	// button, in the customer's language.
	if u := s.GetPageUrl(); u != "" {
		kb.Row(tg.Link(r.t("btn.open_page"), pageLink(u, r.lang)))
	}
	kb.Row(r.button("btn.back", "subs"), r.button("btn.home", "home"))
	text := r.t("sub.detail", "id", shortID(id), "status", statusText(r, s.GetStatus()),
		"expires", h.expires(r, s.GetExpiresAt()), "traffic", h.traffic(r, s.GetTrafficTotalBytes()),
		"link", esc(s.GetSubscriptionLink()))
	used, total := s.GetTrafficUsedBytes(), s.GetTrafficTotalBytes()
	switch {
	case total > 0:
		text += "\n" + r.t("sub.usage", "used", i18n.Bytes(r.lang, used), "total", i18n.Bytes(r.lang, total), "bar", usageBar(used, total))
	case used > 0:
		text += "\n" + r.t("sub.usage_unlimited", "used", i18n.Bytes(r.lang, used))
	}
	if r.isReseller() && s.GetNote() != "" { // the reseller's private note
		text += "\n\n" + r.t("sub.note_line", "note", esc(s.GetNote()))
	}
	r.show(r.labelled(s.GetLabel(), text), kb)
}

func (h *Handler) sendQR(r *req, id string) {
	links, err := h.core.GetSubscriptionLinks(r.ctx, &corev1.GetSubscriptionLinksRequest{UserId: r.user.GetId(), SubscriptionId: id})
	if err != nil {
		r.fail(err)
		return
	}
	if _, err := h.tg.SendPhoto(r.ctx, r.chatID, tg.Photo{Data: links.GetQrPng(), Name: "subscription.png"},
		r.t("sub.qr_caption")+"\n<code>"+esc(links.GetSubscriptionLink())+"</code>", nil); err != nil {
		h.log.Warn("send QR failed", "err", err)
	}
}

func (h *Handler) showWallet(r *req) {
	w, err := h.core.GetWallet(r.ctx, &corev1.GetWalletRequest{UserId: r.user.GetId(), Currency: "IRT"})
	if err != nil {
		r.fail(err)
		return
	}
	kb := (&tg.Keyboard{}).Row(r.button("btn.topup", "wtop"), r.button("btn.history", "whist")).
		Row(h.giftButton(r)...).
		Row(r.button("btn.home", "home"))
	text := h.accountInfo(r) + "\n\n" + r.t("wallet.title", "balance", r.balance(w.GetBalance(), "IRT"))
	if w.GetUsdtRate() > 0 {
		// the same money in USDT at the live price, and the price itself: always beside the Toman balance
		text += "\n" + r.t("wallet.usdt_line", "usdt", r.money(w.GetUsdtEquivalent(), "USDT"), "rate", r.money(w.GetUsdtRate(), "IRT"))
	}
	if r.isReseller() {
		text += "\n" + r.t("rs.wallet_credit", "limit", r.money(w.GetCreditLimit(), "IRT"),
			"available", r.money(w.GetBalance()+w.GetCreditLimit(), "IRT"))
	}
	r.show(text, kb)
}

func (h *Handler) showTopupAmounts(r *req) {
	kb := &tg.Keyboard{}
	for i := 0; i < len(topupPresets); i += 2 {
		row := []tg.Button{tg.CB(r.money(topupPresets[i], "IRT"), fmt.Sprintf("wamt:%d", topupPresets[i]))}
		if i+1 < len(topupPresets) {
			row = append(row, tg.CB(r.money(topupPresets[i+1], "IRT"), fmt.Sprintf("wamt:%d", topupPresets[i+1])))
		}
		kb.Row(row...)
	}
	kb.Row(r.button("btn.custom_amount", "wcustom"))
	if h.cryptoEnabled("USDT") {
		kb.Row(r.button("btn.topup_usdt", "wusdt"))
	}
	kb.Row(r.button("btn.back", "wallet"), r.button("btn.home", "home"))
	r.show(r.t("wallet.choose_amount"), kb)
}

// showTopupUsdt asks how many USDT to top up with (presets or a typed whole number).
func (h *Handler) showTopupUsdt(r *req) {
	kb := &tg.Keyboard{}
	for i := 0; i < len(topupUsdtPresets); i += 3 {
		var row []tg.Button
		for _, a := range topupUsdtPresets[i:min(i+3, len(topupUsdtPresets))] {
			row = append(row, tg.CB(r.money(a*1_000_000, "USDT"), fmt.Sprintf("wusa:%d", a)))
		}
		kb.Row(row...)
	}
	kb.Row(r.button("btn.custom_amount", "wusc")).Row(r.button("btn.back", "wtop"), r.button("btn.home", "home"))
	r.show(r.t("wallet.usdt_choose"), kb)
}

// showTopupUsdtMethods offers the ways to pay an amount of USDT (USDT only: automatic TRC20, or by hand).
func (h *Handler) showTopupUsdtMethods(r *req, raw string) {
	amount, ok := i18n.ParseNumber(raw)
	if !ok || amount < minTopupUsdt || amount > maxTopupUsdt || !h.cryptoEnabled("USDT") {
		h.showTopupAmounts(r)
		return
	}
	n := nonce()
	var methods []tg.Button
	methods = append(methods, r.button("btn.pay_crypto", fmt.Sprintf("wpu:x:%d:%s", amount, n)))
	gw := h.gatewayButtons(r, "USDT", amount*1_000_000, "wpu", fmt.Sprint(amount), n)
	kb := &tg.Keyboard{}
	rows(kb, gw, 2)
	kb.Row(methods...)
	kb.Row(r.button("btn.back", "wusdt"), r.button("btn.home", "home"))
	text := r.t("wallet.usdt_method", "usdt", r.money(amount*1_000_000, "USDT"))
	if rate := h.usdtRate(); rate > 0 {
		text += "\n" + r.t("wallet.usdt_method_rate", "toman", r.money(amount*rate, "IRT"), "rate", r.money(rate, "IRT"))
	}
	r.show(text, kb)
}

// onTopupUsdtPay handles "wpu:<x|u>:<usdt>:<nonce>": the payment of a top-up in USDT.
func (h *Handler) onTopupUsdtPay(r *req, rest string) {
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		h.showTopupAmounts(r)
		return
	}
	amount, ok := i18n.ParseNumber(parts[1])
	if !ok || amount < minTopupUsdt || amount > maxTopupUsdt {
		h.showTopupAmounts(r)
		return
	}
	provider := map[string]string{"x": "manual_crypto", "u": usdtAuto}[parts[0]]
	if provider == "" {
		h.showWallet(r)
		return
	}
	micro := amount * 1_000_000
	key := fmt.Sprintf("topu:%d:%d:%s:%s", r.from.ID, amount, provider, parts[2])
	if provider == usdtAuto {
		h.startGatewayIn(r, nil, provider, r.t("pay.topup_title"), key, micro, "USDT")
		return
	}
	h.startManualIn(r, nil, provider, key, micro, "USDT")
}

func (h *Handler) onTopupUsdtAmount(r *req) {
	amount, ok := i18n.ParseNumber(r.msg.Text)
	if !ok || amount < minTopupUsdt || amount > maxTopupUsdt {
		r.send(r.t("wallet.bad_usdt", "min", fmt.Sprint(minTopupUsdt), "max", i18n.Number(r.lang, maxTopupUsdt)), cancelKeyboard(r))
		return
	}
	r.clearState()
	h.showTopupUsdtMethods(r, fmt.Sprint(amount))
}

func (h *Handler) showTopupMethods(r *req, raw string) {
	amount, ok := i18n.ParseNumber(raw)
	if !ok || amount < minTopup || amount > maxTopup {
		h.showTopupAmounts(r)
		return
	}
	n := nonce()
	var methods []tg.Button
	if h.cardEnabled("IRT") {
		methods = append(methods, r.button("btn.pay_card", fmt.Sprintf("wpay:c:%d:%s", amount, n)))
	}
	if h.cryptoEnabled("IRT") {
		methods = append(methods, r.button("btn.pay_crypto", fmt.Sprintf("wpay:x:%d:%s", amount, n)))
	}
	if h.zarinpalLinkEnabled("IRT") {
		methods = append(methods, r.button("btn.pay_zarinpal_link", fmt.Sprintf("wpay:l:%d:%s", amount, n)))
	}
	gw := h.gatewayButtons(r, "IRT", amount, "wpay", fmt.Sprint(amount), n)
	if len(methods) == 0 && len(gw) == 0 {
		r.show(r.t("pay.not_configured"), homeKeyboard(r))
		return
	}
	kb := &tg.Keyboard{}
	rows(kb, gw, 2)
	kb.Row(methods...)
	kb.Row(r.button("btn.back", "wtop"), r.button("btn.home", "home"))
	r.show(r.t("wallet.choose_method", "amount", r.money(amount, "IRT")), kb)
}

// onTopupPay handles "wpay:<c|x|l|z|i|n|s|u>:<amount>:<nonce>".
func (h *Handler) onTopupPay(r *req, rest string) {
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		h.showWallet(r)
		return
	}
	amount, ok := i18n.ParseNumber(parts[1])
	if !ok || amount < minTopup || amount > maxTopup {
		h.showTopupAmounts(r)
		return
	}
	provider := map[string]string{"c": "manual_card", "x": "manual_crypto", "l": "manual_zarinpal", "z": "zarinpal", "i": "idpay", "n": "nextpay", "s": "stars",
		"u": usdtAuto}[parts[0]]
	if provider == "" {
		h.showWallet(r)
		return
	}
	key := fmt.Sprintf("top:%d:%d:%s:%s", r.from.ID, amount, provider, parts[2])
	if provider == "zarinpal" || provider == "idpay" || provider == "nextpay" || provider == "stars" || provider == usdtAuto {
		h.startGateway(r, nil, provider, r.t("pay.topup_title"), key, amount)
		return
	}
	h.startManual(r, nil, provider, key, amount)
}

func (h *Handler) onTopupAmount(r *req) {
	amount, ok := i18n.ParseNumber(r.msg.Text)
	if !ok || amount < minTopup || amount > maxTopup {
		r.send(r.t("wallet.bad_amount", "min", r.money(minTopup, "IRT"), "max", r.money(maxTopup, "IRT")), cancelKeyboard(r))
		return
	}
	r.clearState()
	h.showTopupMethods(r, fmt.Sprint(amount))
}

func (h *Handler) showHistory(r *req) {
	if r.isReseller() { // their statement: every page
		h.showStatement(r, 1)
		return
	}
	resp, err := h.core.ListLedgerEntries(r.ctx, &corev1.ListLedgerEntriesRequest{
		UserId: r.user.GetId(), Pagination: &commonv1.Pagination{Page: 1, PageSize: 10},
	})
	if err != nil {
		r.fail(err)
		return
	}
	kb := backHome(r, "wallet")
	if len(resp.GetEntries()) == 0 {
		r.show(r.t("wallet.history_empty"), kb)
		return
	}
	lines := []string{r.t("wallet.history_title"), ""}
	for _, e := range resp.GetEntries() {
		lines = append(lines, ledgerLine(r, e))
	}
	r.show(strings.Join(lines, "\n"), kb)
}

// ledgerLine is one wallet transaction: its day, kind and signed amount.
func ledgerLine(r *req, e *corev1.LedgerEntry) string {
	amount := r.money(e.GetAmount(), e.GetCurrency())
	if e.GetAmount() > 0 {
		amount = "+" + amount
	}
	return fmt.Sprintf("%s · %s · %s", i18n.Date(r.lang, time.Unix(e.GetCreatedAt(), 0)), r.t("ledger."+e.GetKind()), amount)
}

func (h *Handler) onTrial(r *req) {
	resp, err := h.core.ListPlans(r.ctx, &corev1.ListPlansRequest{})
	if err != nil {
		r.fail(err)
		return
	}
	var trial *corev1.Plan
	for _, p := range resp.GetPlans() {
		if p.GetIsTrial() && p.GetEnabled() {
			trial = p
			break
		}
	}
	if trial == nil {
		r.toast, r.alert = r.t("trial.unavailable"), true
		return
	}
	ok, err := h.core.CanStartTrial(r.ctx, &corev1.CanStartTrialRequest{UserId: r.user.GetId()})
	if err != nil {
		r.fail(err)
		return
	}
	if !ok.GetEligible() {
		r.toast, r.alert = r.t("trial.used"), true
		return
	}
	if _, err := h.core.StartTrial(r.ctx, &corev1.StartTrialRequest{
		UserId: r.user.GetId(), PlanId: trial.GetId(), IdempotencyKey: fmt.Sprintf("trial:%d", r.from.ID),
	}); err != nil {
		r.fail(err)
		return
	}
	r.show(r.t("trial.started"), homeKeyboard(r))
}

// legalButtons link to the store's terms and privacy policy, when set
// (https links only: Telegram refuses a message with a bad link button).
func (h *Handler) legalButtons(r *req) []tg.Button {
	var out []tg.Button
	if u := h.setting("branding.terms_url"); httpsURL(u) {
		out = append(out, r.linkButton("btn.terms", u))
	}
	if u := h.setting("branding.privacy_url"); httpsURL(u) {
		out = append(out, r.linkButton("btn.privacy", u))
	}
	return out
}

// onTerms answers /terms with the store's terms and privacy policy.
func (h *Handler) onTerms(r *req) {
	links := h.legalButtons(r)
	if len(links) == 0 {
		r.send(r.t("terms.none"), homeKeyboard(r))
		return
	}
	r.send(r.t("terms.title"), (&tg.Keyboard{}).Row(links...).Row(r.button("btn.home", "home")))
}

// pageLink adds the customer's language to a service page address.
func pageLink(u, lang string) string {
	if strings.Contains(u, "?") {
		return u
	}
	return u + "?l=" + lang
}
