package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// settingKeys mirrors the settings core lets admins change (core enforces it).
var settingKeys = []string{
	"payments.card_number", "payments.card_holder", "payments.usdt_trc20", "payments.usdt_erc20",
	"payments.usdt_rate", "payments.stars_rate", "payments.zarinpal_link", "referral.reward_percent",
	"branding.name", "branding.support", "texts.fa.welcome", "texts.en.welcome",
}

// rejectReasons are the preset rejection reasons; the user sees them in their
// own language (the bot stores "preset:<name>").
var rejectReasons = []string{"amount", "unreadable", "not_received"}

func (h *Handler) actor(r *req) int64 { return r.from.ID }

func (h *Handler) routeAdmin(r *req, rest string) {
	if !r.isStaff() {
		r.toast, r.alert = r.t("admin.forbidden"), true
		return
	}
	verb, arg, _ := strings.Cut(rest, ":")
	switch verb {
	case "":
		h.showAdmin(r)
	case "pend":
		h.showPending(r)
	case "ok":
		h.review(r, arg, "approved", "")
	case "no":
		h.askRejectReason(r, arg)
	case "nr":
		i, id, _ := strings.Cut(arg, ":")
		n, err := strconv.Atoi(i)
		if err != nil || n < 0 || n >= len(rejectReasons) {
			h.showPending(r)
			return
		}
		h.review(r, id, "rejected", "preset:"+rejectReasons[n])
	case "nx":
		msgID := 0
		if r.cb != nil && r.cb.Message != nil {
			msgID = r.cb.Message.MessageID
		}
		r.setState(sceneAdminReason, map[string]string{"intent": arg, "msg": strconv.Itoa(msgID)})
		r.send(r.t("admin.reject_custom"), cancelKeyboard(r))
	case "find":
		r.setState(sceneAdminFind, nil)
		r.send(r.t("admin.find_prompt"), cancelKeyboard(r))
	case "adj":
		r.setState(sceneAdminAdjust, map[string]string{"user": arg})
		r.send(r.t("admin.adjust_prompt"), cancelKeyboard(r))
	case "ban":
		h.setStatus(r, arg, "banned")
	case "unban":
		h.setStatus(r, arg, "active")
	case "plans":
		h.showAdminPlans(r)
	case "pt":
		h.togglePlan(r, arg)
	case "set":
		h.showSettings(r)
	case "web":
		h.sendDashboardLink(r)
	default:
		h.showAdmin(r)
	}
}

// sendDashboardLink sends a one-time dashboard login link (2 minutes, once).
func (h *Handler) sendDashboardLink(r *req) {
	link, err := h.core.CreateDashboardLink(r.ctx, &corev1.CreateDashboardLinkRequest{ActorTelegramId: h.actor(r)})
	if err != nil {
		r.fail(err)
		return
	}
	if link.GetUrl() == "" {
		r.send(r.t("admin.dashboard_unavailable"), nil)
		return
	}
	r.send(r.t("admin.dashboard_link"), (&tg.Keyboard{}).Row(tg.Link(r.t("btn.open_dashboard"), link.GetUrl())))
}

func (h *Handler) showAdmin(r *req) {
	st, err := h.core.AdminGetStats(r.ctx, &corev1.AdminGetStatsRequest{ActorTelegramId: h.actor(r)})
	if err != nil {
		r.fail(err)
		return
	}
	panel := r.t("admin.panel_ok")
	if !st.GetPanelHealthy() {
		panel = r.t("admin.panel_down", "detail", esc(st.GetPanelDetail()))
	}
	pending := r.t("admin.unknown")
	if st.GetPendingPayments() >= 0 {
		pending = i18n.Number(r.lang, st.GetPendingPayments())
	}
	kb := (&tg.Keyboard{}).
		Row(tg.CB(r.t("btn.adm_pending", "n", pending), "adm:pend")).
		Row(tg.CB(r.t("btn.adm_find"), "adm:find"), tg.CB(r.t("btn.adm_plans"), "adm:plans")).
		Row(tg.CB(r.t("btn.adm_settings"), "adm:set"), tg.CB(r.t("btn.adm_dashboard"), "adm:web")).
		Row(tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("admin.title",
		"users", i18n.Number(r.lang, st.GetUsersTotal()), "today", i18n.Number(r.lang, st.GetUsersToday()),
		"active", i18n.Number(r.lang, st.GetActiveSubscriptions()), "pending", pending,
		"failed", i18n.Number(r.lang, st.GetProvisionFailedOrders()), "panel", panel), kb)
}

// showPending sends the oldest payment awaiting review as a new message (a
// receipt photo when there is one), with approve/reject buttons.
func (h *Handler) showPending(r *req) {
	resp, err := h.core.AdminListPendingPayments(r.ctx, &corev1.AdminListPendingPaymentsRequest{ActorTelegramId: h.actor(r), Limit: 1})
	if err != nil {
		r.fail(err)
		return
	}
	if len(resp.GetPayments()) == 0 {
		r.send(r.t("admin.no_pending"), (&tg.Keyboard{}).Row(tg.CB(r.t("btn.back"), "adm")))
		return
	}
	p := resp.GetPayments()[0]
	method, proof := r.t("admin.method_card"), r.t("admin.proof_card", "ref", esc(p.GetReferenceNumber()))
	switch {
	case p.GetProvider() == "manual_crypto" && p.GetTxid() != "":
		method = r.t("admin.method_crypto")
		proof = r.t("admin.proof_crypto", "network", esc(p.GetNetwork()), "txid", esc(p.GetTxid()))
	case p.GetProvider() == "manual_crypto":
		method, proof = r.t("admin.method_crypto"), r.t("admin.proof_screenshot")
	case p.GetProvider() == "manual_zarinpal":
		method = r.t("admin.method_zarinpal_link")
		if p.GetReferenceNumber() == "" {
			proof = r.t("admin.proof_screenshot")
		}
	}
	dup := ""
	if p.GetPossibleDuplicate() {
		dup = r.t("admin.dup_warning")
	}
	text := r.t("admin.pending_item",
		"user", esc(displayName(p.GetUser())), "amount", r.money(p.GetAmount().GetAmount(), p.GetAmount().GetCurrency()),
		"method", method, "proof", proof, "when", i18n.Date(r.lang, time.Unix(p.GetSubmittedAt(), 0)), "dup", dup)
	kb := (&tg.Keyboard{}).
		Row(tg.CB(r.t("btn.approve"), "adm:ok:"+p.GetIntentId()), tg.CB(r.t("btn.reject"), "adm:no:"+p.GetIntentId())).
		Row(tg.CB(r.t("btn.back"), "adm"))
	if f := p.GetReceiptFile(); f != "" {
		if _, err := h.tg.SendPhoto(r.ctx, r.chatID, tg.Photo{FileID: f}, text, kb); err == nil {
			return
		}
		// A screenshot sent "as file" has a document id, which sendPhoto refuses.
		if _, err := h.tg.SendDocument(r.ctx, r.chatID, f, text, kb); err == nil {
			return
		}
	}
	r.send(text, kb)
}

func (h *Handler) askRejectReason(r *req, intentID string) {
	kb := &tg.Keyboard{}
	for i, reason := range rejectReasons {
		kb.Row(tg.CB(r.t("reason."+reason), fmt.Sprintf("adm:nr:%d:%s", i, intentID)))
	}
	kb.Row(tg.CB(r.t("btn.reject_other"), "adm:nx:"+intentID))
	kb.Row(tg.CB(r.t("btn.back"), "adm:pend"))
	if r.cb != nil && r.cb.Message != nil {
		if err := h.tg.EditMessageReplyMarkup(r.ctx, r.chatID, r.cb.Message.MessageID, kb); err == nil {
			r.toast = r.t("admin.reject_reason")
			return
		}
	}
	r.send(r.t("admin.reject_reason"), kb)
}

// review applies a decision, removes the buttons from the reviewed item and
// shows the next one.
func (h *Handler) review(r *req, intentID, decision, reason string) {
	h.reviewItem(r, intentID, decision, reason, itemMessage(r))
}

func itemMessage(r *req) int {
	if r.cb != nil && r.cb.Message != nil {
		return r.cb.Message.MessageID
	}
	return 0
}

func (h *Handler) reviewItem(r *req, intentID, decision, reason string, msgID int) {
	_, err := h.core.ReviewManualPayment(r.ctx, &corev1.ReviewManualPaymentRequest{
		ActorTelegramId: h.actor(r), IntentId: intentID, Decision: decision, Reason: reason,
	})
	done := r.t("admin.approved")
	if decision == "rejected" {
		done = r.t("admin.rejected")
	}
	if err != nil {
		if status.Code(err) != codes.FailedPrecondition {
			r.fail(err)
			return
		}
		done = r.t("admin.already_reviewed")
	}
	if msgID != 0 {
		_ = h.tg.EditMessageReplyMarkup(r.ctx, r.chatID, msgID, nil)
	}
	if r.cb != nil {
		r.toast = done
	} else {
		r.send(done, nil)
	}
	h.showPending(r)
}

func (h *Handler) setStatus(r *req, userID, st string) {
	u, err := h.core.AdminSetUserStatus(r.ctx, &corev1.AdminSetUserStatusRequest{
		ActorTelegramId: h.actor(r), UserId: userID, Status: st, Reason: r.t("admin.status_reason"),
	})
	if err != nil {
		r.fail(err)
		return
	}
	r.toast = r.t("admin.status_done", "status", u.GetStatus())
	h.showUserCard(r, u.GetTelegramId())
}

func (h *Handler) showUserCard(r *req, telegramID int64) {
	v, err := h.core.AdminFindUser(r.ctx, &corev1.AdminFindUserRequest{ActorTelegramId: h.actor(r), Query: strconv.FormatInt(telegramID, 10)})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			r.send(r.t("admin.not_found"), (&tg.Keyboard{}).Row(tg.CB(r.t("btn.back"), "adm")))
			return
		}
		r.fail(err)
		return
	}
	u := v.GetUser()
	var wallets []string
	for _, w := range v.GetWallets() {
		wallets = append(wallets, r.money(w.GetBalance(), w.GetCurrency()))
	}
	if len(wallets) == 0 {
		wallets = append(wallets, r.money(0, "IRT"))
	}
	statusBtn := tg.CB(r.t("btn.adm_ban"), "adm:ban:"+u.GetId())
	if u.GetStatus() == "banned" {
		statusBtn = tg.CB(r.t("btn.adm_unban"), "adm:unban:"+u.GetId())
	}
	kb := (&tg.Keyboard{}).Row(tg.CB(r.t("btn.adm_adjust"), "adm:adj:"+u.GetId()), statusBtn).
		Row(tg.CB(r.t("btn.back"), "adm"))
	r.show(r.t("admin.user_card", "name", esc(displayName(u)), "tg", u.GetTelegramId(), "role", u.GetRole(),
		"status", u.GetStatus(), "lang", u.GetLanguage(), "wallets", strings.Join(wallets, " · "),
		"subs", i18n.Number(r.lang, v.GetSubscriptions()), "orders", i18n.Number(r.lang, v.GetOrders())), kb)
}

func (h *Handler) onAdminScene(r *req, st state.State) {
	if !r.isStaff() {
		r.clearState()
		r.send(r.t("admin.forbidden"), homeKeyboard(r))
		return
	}
	text := strings.TrimSpace(r.msg.Text)
	switch st.Scene {
	case sceneAdminFind:
		r.clearState()
		v, err := h.core.AdminFindUser(r.ctx, &corev1.AdminFindUserRequest{ActorTelegramId: h.actor(r), Query: text})
		if status.Code(err) == codes.NotFound || status.Code(err) == codes.InvalidArgument {
			r.send(r.t("admin.not_found"), (&tg.Keyboard{}).Row(tg.CB(r.t("btn.adm_find"), "adm:find"), tg.CB(r.t("btn.back"), "adm")))
			return
		}
		if err != nil {
			r.fail(err)
			return
		}
		h.showUserCard(r, v.GetUser().GetTelegramId())
	case sceneAdminAdjust:
		amountStr, reason, _ := strings.Cut(text, " ")
		amount, ok := i18n.ParseNumber(amountStr)
		reason = strings.TrimSpace(reason)
		if !ok || amount == 0 || reason == "" {
			r.send(r.t("admin.adjust_bad"), cancelKeyboard(r))
			return
		}
		r.clearState()
		w, err := h.core.AdminAdjustBalance(r.ctx, &corev1.AdminAdjustBalanceRequest{
			ActorTelegramId: h.actor(r), UserId: st.Get("user"),
			Delta:  &commonv1.Money{Amount: amount, Currency: "IRT"},
			Reason: reason, IdempotencyKey: fmt.Sprintf("adj:%d:%d", r.from.ID, r.msg.MessageID),
		})
		if err != nil {
			r.fail(err)
			return
		}
		r.send(r.t("admin.adjust_done", "balance", r.money(w.GetBalance(), w.GetCurrency())), (&tg.Keyboard{}).Row(tg.CB(r.t("btn.back"), "adm")))
	case sceneAdminReason:
		if text == "" {
			r.send(r.t("admin.reject_custom"), cancelKeyboard(r))
			return
		}
		r.clearState()
		msgID, _ := strconv.Atoi(st.Get("msg"))
		h.reviewItem(r, st.Get("intent"), "rejected", text, msgID)
	}
}

func (h *Handler) showAdminPlans(r *req) {
	ps, err := h.plans(r, true)
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	for _, p := range ps {
		mark := r.t("admin.plan_off")
		if p.GetEnabled() {
			mark = r.t("admin.plan_on")
		}
		name := planName(r, p)
		if p.GetIsTrial() {
			name = "🎁 " + name
		}
		kb.Row(tg.CB(mark+" "+name+" · "+r.money(p.GetPrice().GetAmount(), p.GetPrice().GetCurrency()), "adm:pt:"+p.GetId()))
	}
	kb.Row(tg.CB(r.t("btn.back"), "adm"))
	r.show(r.t("admin.plans_title"), kb)
}

func (h *Handler) togglePlan(r *req, id string) {
	p, err := h.findPlan(r, id, true)
	if err != nil {
		r.fail(err)
		return
	}
	p.Enabled = !p.GetEnabled()
	if _, err := h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: p}); err != nil {
		r.fail(err)
		return
	}
	h.showAdminPlans(r)
}

func (h *Handler) showSettings(r *req) {
	if err := h.RefreshSettings(r.ctx); err != nil {
		r.fail(err)
		return
	}
	lines := make([]string, 0, len(settingKeys))
	for _, k := range settingKeys {
		v := esc(h.setting(k))
		if v == "" {
			v = r.t("admin.setting_unset")
		}
		lines = append(lines, r.t("admin.setting_line", "key", k, "value", v))
	}
	r.show(r.t("admin.settings_title", "list", strings.Join(lines, "\n")), (&tg.Keyboard{}).Row(tg.CB(r.t("btn.back"), "adm")))
}

func (h *Handler) onAdminCommand(r *req, cmd, args string) {
	if !r.isStaff() {
		r.send(r.t("admin.forbidden"), homeKeyboard(r))
		return
	}
	r.clearState()
	switch cmd {
	case "/admin":
		h.showAdmin(r)
	case "/set":
		key, value, _ := strings.Cut(args, " ")
		if key == "" {
			r.send(r.t("admin.setting_usage"), nil)
			return
		}
		if _, err := h.core.AdminSetSetting(r.ctx, &corev1.AdminSetSettingRequest{
			ActorTelegramId: h.actor(r), Key: key, Value: strings.TrimSpace(value),
		}); err != nil {
			if status.Code(err) == codes.InvalidArgument {
				r.send(r.t("admin.setting_usage")+"\n"+strings.Join(settingKeys, "\n"), nil)
				return
			}
			r.fail(err)
			return
		}
		_ = h.RefreshSettings(r.ctx)
		r.send(r.t("admin.setting_saved", "key", esc(key)), nil)
	case "/plan_add":
		p, ok := parsePlan(args)
		if !ok {
			r.send(r.t("admin.plan_usage"), nil)
			return
		}
		h.savePlan(r, p)
	case "/trial":
		h.onTrialCommand(r, args)
	case "/topup_add":
		p, ok := parseTopup(args)
		if !ok {
			r.send(r.t("admin.topup_usage"), nil)
			return
		}
		h.savePlan(r, p)
	case "/discount_add":
		h.onDiscountAdd(r, args)
	case "/discount_off":
		h.onDiscountOff(r, strings.TrimSpace(args))
	case "/discounts":
		h.showDiscounts(r)
	}
}

// parseTopup reads "<price> <IRT|USDT> <GB> <English name> | <Persian name>":
// a traffic package sold from a subscription's page.
func parseTopup(args string) (*corev1.Plan, bool) {
	price, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	cur, rest, _ := strings.Cut(strings.TrimSpace(rest), " ")
	gbTok, rest, _ := strings.Cut(strings.TrimSpace(rest), " ")
	p, ok := parsePlan(price + " " + cur + " 0 " + gbTok + " " + rest)
	if !ok || p.GetTrafficBytes() <= 0 {
		return nil, false
	}
	p.IsTopup, p.Kind, p.DurationDays = true, "traffic", 0
	return p, true
}

// onDiscountAdd: "/discount_add <CODE> <20%|50000 IRT> [max uses] [days valid]".
func (h *Handler) onDiscountAdd(r *req, args string) {
	f := strings.Fields(args)
	if len(f) < 2 {
		r.send(r.t("admin.discount_usage"), nil)
		return
	}
	d := &corev1.Discount{Code: strings.ToUpper(f[0]), Enabled: true}
	rest := f[2:]
	if pct, isPct := strings.CutSuffix(f[1], "%"); isPct {
		n, ok := i18n.ParseNumber(pct)
		if !ok || n < 1 || n > 100 {
			r.send(r.t("admin.discount_usage"), nil)
			return
		}
		d.Percent = int32(n) //nolint:gosec // 1..100
	} else {
		if len(f) < 3 {
			r.send(r.t("admin.discount_usage"), nil)
			return
		}
		cur := strings.ToUpper(f[2])
		amount, ok := parseMinor(f[1], cur)
		if !ok || amount <= 0 {
			r.send(r.t("admin.discount_usage"), nil)
			return
		}
		d.Amount = &commonv1.Money{Amount: amount, Currency: cur}
		rest = f[3:]
	}
	for i, tok := range rest {
		n, ok := i18n.ParseNumber(tok)
		if !ok || n <= 0 || i > 1 {
			r.send(r.t("admin.discount_usage"), nil)
			return
		}
		if i == 0 {
			d.MaxUses = int32(min(n, 1_000_000)) //nolint:gosec // bounded
		} else {
			d.ExpiresAt = time.Now().Add(time.Duration(min(n, 3650)) * 24 * time.Hour).Unix()
		}
	}
	h.saveDiscount(r, d)
}

func (h *Handler) saveDiscount(r *req, d *corev1.Discount) {
	saved, err := h.core.AdminUpsertDiscount(r.ctx, &corev1.AdminUpsertDiscountRequest{ActorTelegramId: h.actor(r), Discount: d})
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			r.send(esc(status.Convert(err).Message())+"\n"+r.t("admin.discount_usage"), nil)
			return
		}
		r.fail(err)
		return
	}
	r.send(r.t("admin.discount_saved", "line", h.discountLine(r, saved)), nil)
}

// onDiscountOff: "/discount_off <CODE>" stops a code (its history stays).
func (h *Handler) onDiscountOff(r *req, code string) {
	list, err := h.core.AdminListDiscounts(r.ctx, &corev1.AdminListDiscountsRequest{ActorTelegramId: h.actor(r)})
	if err != nil {
		r.fail(err)
		return
	}
	for _, d := range list.GetDiscounts() {
		if strings.EqualFold(d.GetCode(), code) && code != "" {
			d.Enabled = false
			h.saveDiscount(r, d)
			return
		}
	}
	r.send(r.t("admin.discount_unknown", "code", esc(code)), nil)
}

func (h *Handler) showDiscounts(r *req) {
	list, err := h.core.AdminListDiscounts(r.ctx, &corev1.AdminListDiscountsRequest{ActorTelegramId: h.actor(r)})
	if err != nil {
		r.fail(err)
		return
	}
	if len(list.GetDiscounts()) == 0 {
		r.send(r.t("admin.discounts_empty")+"\n"+r.t("admin.discount_usage"), nil)
		return
	}
	lines := []string{r.t("admin.discounts_title")}
	for _, d := range list.GetDiscounts() {
		lines = append(lines, h.discountLine(r, d))
	}
	r.send(strings.Join(lines, "\n"), nil)
}

// discountLine shows a code's terms: "SPRING20 · 20% · 3/100 used · until … · off".
func (h *Handler) discountLine(r *req, d *corev1.Discount) string {
	parts := []string{"<code>" + esc(d.GetCode()) + "</code>"}
	if d.GetPercent() > 0 {
		parts = append(parts, i18n.Number(r.lang, int64(d.GetPercent()))+"%")
	} else {
		parts = append(parts, r.money(d.GetAmount().GetAmount(), d.GetAmount().GetCurrency()))
	}
	used := i18n.Number(r.lang, int64(d.GetUsed()))
	if d.GetMaxUses() > 0 {
		used += "/" + i18n.Number(r.lang, int64(d.GetMaxUses()))
	}
	parts = append(parts, r.t("admin.discount_used", "n", used))
	if d.GetExpiresAt() > 0 {
		parts = append(parts, r.t("admin.discount_until", "date", i18n.Date(r.lang, time.Unix(d.GetExpiresAt(), 0))))
	}
	if !d.GetEnabled() {
		parts = append(parts, r.t("admin.discount_off"))
	}
	return strings.Join(parts, " · ")
}

func (h *Handler) savePlan(r *req, p *corev1.Plan) {
	saved, err := h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: p})
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			r.send(esc(status.Convert(err).Message())+"\n"+r.t("admin.plan_usage"), nil)
			return
		}
		r.fail(err)
		return
	}
	r.send(r.t("admin.plan_saved", "name", esc(planName(r, saved))), (&tg.Keyboard{}).Row(tg.CB(r.t("btn.adm_plans"), "adm:plans")))
}

// onTrialCommand: "/trial <days> <GB>" sets up the free trial plan, "/trial
// off" disables it.
func (h *Handler) onTrialCommand(r *req, args string) {
	ps, err := h.plans(r, true)
	if err != nil {
		r.fail(err)
		return
	}
	var trial *corev1.Plan
	for _, p := range ps {
		if p.GetIsTrial() {
			trial = p
			break
		}
	}
	if strings.EqualFold(strings.TrimSpace(args), "off") {
		if trial == nil || !trial.GetEnabled() {
			r.send(r.t("admin.trial_off"), nil)
			return
		}
		trial.Enabled = false
		if _, err := h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: trial}); err != nil {
			r.fail(err)
			return
		}
		r.send(r.t("admin.trial_off"), nil)
		return
	}
	f := strings.Fields(args)
	if len(f) != 2 {
		r.send(r.t("admin.trial_usage"), nil)
		return
	}
	days, ok1 := i18n.ParseNumber(f[0])
	gb, ok2 := i18n.ParseNumber(f[1])
	if !ok1 || !ok2 || days < 0 || gb < 0 || (days == 0 && gb == 0) || days > 365 || gb > 1000 {
		r.send(r.t("admin.trial_usage"), nil)
		return
	}
	if trial == nil {
		trial = &corev1.Plan{NameI18N: map[string]string{"en": "Free trial", "fa": "تست رایگان"}, IsTrial: true}
	}
	trial.DurationDays, trial.TrafficBytes = int32(days), gb<<30 //nolint:gosec // bounded above
	trial.Kind, trial.Enabled = planKind(days, gb), true
	trial.Price = &commonv1.Money{Amount: 0, Currency: "IRT"}
	h.savePlan(r, trial)
}

func planKind(days, gb int64) string {
	switch {
	case days > 0 && gb > 0:
		return "both"
	case days > 0:
		return "time"
	default:
		return "traffic"
	}
}

// parsePlan reads "<price> <IRT|USDT> <days> <GB> <English name> | <Persian name>".
func parsePlan(args string) (*corev1.Plan, bool) {
	var tok [4]string
	rest := strings.TrimSpace(args)
	for i := range tok {
		idx := strings.IndexAny(rest, " \t")
		if idx < 0 {
			return nil, false
		}
		tok[i], rest = rest[:idx], strings.TrimSpace(rest[idx:])
	}
	currency := strings.ToUpper(tok[1])
	price, ok := parseMinor(tok[0], currency)
	days, okD := i18n.ParseNumber(tok[2])
	gb, okG := i18n.ParseNumber(tok[3])
	if !ok || !okD || !okG || days < 0 || gb < 0 || (days == 0 && gb == 0) || days > 3650 || gb > 100_000 || rest == "" {
		return nil, false
	}
	en, fa, _ := strings.Cut(rest, "|")
	names := map[string]string{}
	if v := strings.TrimSpace(en); v != "" {
		names["en"] = v
	}
	if v := strings.TrimSpace(fa); v != "" {
		names["fa"] = v
	}
	return &corev1.Plan{
		NameI18N: names, Kind: planKind(days, gb), Enabled: true,
		DurationDays: int32(days), TrafficBytes: gb << 30, //nolint:gosec // bounded above
		Price: &commonv1.Money{Amount: price, Currency: currency},
	}, true
}

// parseMinor reads a price in major units ("150000", "2.5") into minor units.
func parseMinor(s, currency string) (int64, bool) {
	scale := map[string]int{"IRT": 0, "USDT": 6}
	sc, known := scale[currency]
	if !known {
		return 0, false
	}
	s = strings.NewReplacer("٫", ".", "/", ".").Replace(s)
	whole, frac, hasFrac := strings.Cut(s, ".")
	w, ok := i18n.ParseNumber(whole)
	if !ok || w < 0 {
		return 0, false
	}
	for i := 0; i < sc; i++ {
		w *= 10
	}
	if !hasFrac || frac == "" {
		return w, true
	}
	if len(frac) > sc {
		return 0, false
	}
	f, ok := i18n.ParseNumber(frac + strings.Repeat("0", sc-len(frac)))
	if !ok {
		return 0, false
	}
	return w + f, true
}
