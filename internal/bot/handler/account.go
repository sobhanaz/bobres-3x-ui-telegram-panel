package handler

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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

func (h *Handler) showServices(r *req) {
	subs, err := h.subscriptions(r)
	if err != nil {
		r.fail(err)
		return
	}
	if len(subs) == 0 {
		r.show(r.t("subs.empty"), (&tg.Keyboard{}).Row(tg.CB(r.t("btn.buy"), "buy")).Row(tg.CB(r.t("btn.home"), "home")))
		return
	}
	kb := &tg.Keyboard{}
	for i, s := range subs {
		if i == 20 {
			break
		}
		label := "#" + shortID(s.GetId()) + " · " + r.t("subs.item",
			"status", r.t("sub.status."+s.GetStatus()), "expires", h.expires(r, s.GetExpiresAt()))
		kb.Row(tg.CB(label, "sub:"+s.GetId()))
	}
	kb.Row(tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("subs.title"), kb)
}

func (h *Handler) showService(r *req, id string) {
	subs, err := h.subscriptions(r)
	if err != nil {
		r.fail(err)
		return
	}
	for _, s := range subs {
		if s.GetId() != id {
			continue
		}
		if s.GetStatus() == "pending" || s.GetSubscriptionLink() == "" {
			r.show(r.t("sub.not_ready"), backHome(r, "subs"))
			return
		}
		kb := (&tg.Keyboard{}).Row(tg.CB(r.t("btn.qr"), "qr:"+id)).
			Row(tg.CB(r.t("btn.back"), "subs"), tg.CB(r.t("btn.home"), "home"))
		r.show(r.t("sub.detail", "id", shortID(id), "status", r.t("sub.status."+s.GetStatus()),
			"expires", h.expires(r, s.GetExpiresAt()), "traffic", h.traffic(r, s.GetTrafficTotalBytes()),
			"link", esc(s.GetSubscriptionLink())), kb)
		return
	}
	h.showServices(r)
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
	kb := (&tg.Keyboard{}).Row(tg.CB(r.t("btn.topup"), "wtop"), tg.CB(r.t("btn.history"), "whist")).
		Row(tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("wallet.title", "balance", r.money(w.GetBalance(), "IRT")), kb)
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
	kb.Row(tg.CB(r.t("btn.custom_amount"), "wcustom")).Row(tg.CB(r.t("btn.back"), "wallet"), tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("wallet.choose_amount"), kb)
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
		methods = append(methods, tg.CB(r.t("btn.pay_card"), fmt.Sprintf("wpay:c:%d:%s", amount, n)))
	}
	if h.cryptoEnabled("IRT") {
		methods = append(methods, tg.CB(r.t("btn.pay_crypto"), fmt.Sprintf("wpay:x:%d:%s", amount, n)))
	}
	if h.zarinpalLinkEnabled("IRT") {
		methods = append(methods, tg.CB(r.t("btn.pay_zarinpal_link"), fmt.Sprintf("wpay:l:%d:%s", amount, n)))
	}
	gw := h.gatewayButtons(r, "IRT", amount, "wpay", fmt.Sprint(amount), n)
	if len(methods) == 0 && len(gw) == 0 {
		r.show(r.t("pay.not_configured"), homeKeyboard(r))
		return
	}
	kb := &tg.Keyboard{}
	if len(gw) > 0 {
		kb.Row(gw...)
	}
	if len(methods) > 0 {
		kb.Row(methods...)
	}
	kb.Row(tg.CB(r.t("btn.back"), "wtop"), tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("wallet.choose_method", "amount", r.money(amount, "IRT")), kb)
}

// onTopupPay handles "wpay:<c|x|z|s>:<amount>:<nonce>".
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
	provider := map[string]string{"c": "manual_card", "x": "manual_crypto", "l": "manual_zarinpal", "z": "zarinpal", "s": "stars"}[parts[0]]
	if provider == "" {
		h.showWallet(r)
		return
	}
	key := fmt.Sprintf("top:%d:%d:%s:%s", r.from.ID, amount, provider, parts[2])
	if provider == "zarinpal" || provider == "stars" {
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
		amount := r.money(e.GetAmount(), e.GetCurrency())
		if e.GetAmount() > 0 {
			amount = "+" + amount
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %s",
			i18n.Date(r.lang, time.Unix(e.GetCreatedAt(), 0)), r.t("ledger."+e.GetKind()), amount))
	}
	r.show(strings.Join(lines, "\n"), kb)
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

func (h *Handler) onSupport(r *req) {
	contact := ""
	if c := h.setting("branding.support"); c != "" {
		contact = r.t("support.contact", "contact", esc(c))
	}
	r.setState(sceneTicket, nil)
	r.show(r.t("support.prompt", "contact", contact), cancelKeyboard(r))
}

const maxTicketRunes = 2000

func (h *Handler) onTicket(r *req) {
	text := strings.TrimSpace(r.msg.Text)
	if text == "" {
		text = strings.TrimSpace(r.msg.Caption)
	}
	if text == "" {
		r.send(r.t("support.need_text"), cancelKeyboard(r))
		return
	}
	if utf8.RuneCountInString(text) > maxTicketRunes {
		text = string([]rune(text)[:maxTicketRunes])
	}
	t, err := h.core.CreateSupportTicket(r.ctx, &corev1.CreateSupportTicketRequest{UserId: r.user.GetId(), Category: "general", Text: text})
	r.clearState()
	if err != nil {
		r.fail(err)
		return
	}
	r.send(r.t("support.sent", "id", shortID(t.GetId())), homeKeyboard(r))
	h.notifyAdmin(r, "admin.new_ticket", "user", esc(displayName(r.user)), "text", esc(text))
}
