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

// settingKeys are the settings the bot's settings screen shows: the switches
// worth having in Telegram (maintenance first, to turn it off when the
// dashboard is down) and the store's basics. /set takes every setting core
// knows (core checks the key and the value); the dashboard shows them all.
var settingKeys = []string{
	"maintenance.enabled", "join.channel", "join.link", "notify.recipients",
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
	case "today":
		h.showToday(r)
	case "plans":
		h.showAdminPlans(r)
	case "pl":
		h.showPlanCard(r, arg)
	case "pt":
		h.togglePlan(r, arg)
	case "pn":
		h.startWizard(r, "plan", nil)
	case "pe":
		field, id, _ := strings.Cut(arg, ":")
		h.startPlanEdit(r, field, id)
	case "tup":
		h.showAdminTopups(r)
	case "tn":
		h.startWizard(r, "topup", nil)
	case "trial":
		h.showTrial(r)
	case "tw":
		h.startWizard(r, "trial", nil)
	case "toff":
		if err := h.trialOff(r); err != nil {
			r.fail(err)
			return
		}
		h.showTrial(r)
	case "disc":
		h.showDiscountMenu(r)
	case "dn":
		h.startWizard(r, "disc", nil)
	case "dt":
		h.toggleDiscount(r, arg)
	case "bc":
		h.startWizard(r, "bc", nil)
	case "pays":
		h.showGroup(r, "payments")
	case "notif":
		h.showGroup(r, "notify")
	case "set":
		h.showSettings(r)
	case "sg":
		h.showGroup(r, arg)
	case "sk":
		h.showKey(r, arg)
	case "st":
		h.toggleSetting(r, arg)
	case "sv":
		key, val, _ := strings.Cut(arg, ":")
		h.saveSetting(r, key, val)
	case "sr":
		h.saveSetting(r, arg, "")
	case "se":
		h.startSettingEdit(r, arg)
	case "srv":
		h.showServers(r)
	case "sx":
		h.showServerCard(r, arg)
	case "sy":
		h.toggleServer(r, arg)
	case "sm":
		h.startServerWeight(r, arg)
	case "stf":
		h.showStaff(r)
	case "sa":
		h.startStaffAdd(r)
	case "sd":
		h.startStaffRemove(r, arg)
	case "w":
		h.wizButton(r, arg)
	case "wok":
		h.wizConfirm(r)
	case "wb":
		h.wizBack(r)
	case "wx":
		h.wizCancel(r)
	case "web":
		h.sendDashboardLink(r)
	default:
		h.showAdmin(r)
	}
}

// sendDashboardLink sends a one-time dashboard login link (2 minutes, once)
// to a staff member: owner, admin or support (core checks the role).
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
	r.send(r.t("admin.dashboard_link"), (&tg.Keyboard{}).Row(r.linkButton("btn.open_dashboard", link.GetUrl())))
}

func (h *Handler) showAdmin(r *req) {
	st, err := h.core.AdminGetStats(r.ctx, &corev1.AdminGetStatsRequest{ActorTelegramId: h.actor(r)})
	if err != nil {
		r.fail(err)
		return
	}
	pending := r.t("admin.unknown")
	if st.GetPendingPayments() >= 0 {
		pending = i18n.Number(r.lang, st.GetPendingPayments())
	}
	kb := (&tg.Keyboard{}).
		Row(r.button("btn.adm_pending", "adm:pend", "n", pending)).
		Row(r.button("btn.adm_today", "adm:today"), r.button("btn.adm_find", "adm:find")).
		Row(r.button("btn.adm_plans", "adm:plans"), r.button("btn.adm_discounts", "adm:disc")).
		Row(r.button("btn.adm_notify", "adm:notif"), r.button("btn.adm_broadcast", "adm:bc")).
		Row(r.button("btn.adm_settings", "adm:set"), r.button("btn.adm_servers", "adm:srv"))
	if r.isOwner() { // the payment details and the staff are the owner's (gateways.write, staff.write)
		kb.Row(r.button("btn.adm_payments", "adm:pays"), r.button("btn.adm_staff", "adm:stf"))
	}
	kb.Row(r.button("btn.adm_dashboard", "adm:web")).Row(r.button("btn.home", "home"))
	text := r.t("admin.title",
		"users", i18n.Number(r.lang, st.GetUsersTotal()), "today", i18n.Number(r.lang, st.GetUsersToday()),
		"active", i18n.Number(r.lang, st.GetActiveSubscriptions()), "pending", pending,
		"failed", i18n.Number(r.lang, st.GetProvisionFailedOrders()), "panel", serversLine(r, st))
	if h.set.Bool("maintenance.enabled", false) {
		text = r.t("admin.maintenance_on") + "\n\n" + text
	}
	r.show(text, kb)
}

// shownDown is how many servers that do not answer the admin screen names.
const shownDown = 5

// serversLine sums up the 3x-ui servers for the admin screen, from core's
// copy of the provisioner's list: how many of those taking new services
// answer, and the names of the others (the first few).
func serversLine(r *req, st *corev1.AdminStats) string {
	if !st.GetServersKnown() {
		return r.t("admin.panel_down", "detail", esc(st.GetPanelDetail()))
	}
	total, healthy := int64(st.GetServersTotal()), int64(st.GetServersHealthy())
	switch {
	case st.GetServersListed() == 0 && total == 0:
		return r.t("admin.servers_empty")
	case total == 0:
		return r.t("admin.servers_none")
	}
	down := st.GetServersDown()
	if len(down) == 0 {
		return r.t("admin.servers_ok", "healthy", i18n.Number(r.lang, healthy), "total", i18n.Number(r.lang, total))
	}
	names := make([]string, 0, shownDown+1)
	for _, n := range down[:min(len(down), shownDown)] {
		names = append(names, "<b>"+esc(n)+"</b>")
	}
	if more := len(down) - shownDown; more > 0 {
		names = append(names, r.t("admin.servers_more", "n", i18n.Number(r.lang, int64(more))))
	}
	return r.t("admin.servers_down", "healthy", i18n.Number(r.lang, healthy), "total", i18n.Number(r.lang, total),
		"names", strings.Join(names, " · "))
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
		r.send(r.t("admin.no_pending"), (&tg.Keyboard{}).Row(r.button("btn.back", "adm")))
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
		Row(r.button("btn.approve", "adm:ok:"+p.GetIntentId()), r.button("btn.reject", "adm:no:"+p.GetIntentId())).
		Row(r.button("btn.back", "adm"))
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
		kb.Row(r.button("reason."+reason, fmt.Sprintf("adm:nr:%d:%s", i, intentID)))
	}
	kb.Row(r.button("btn.reject_other", "adm:nx:"+intentID))
	kb.Row(r.button("btn.back", "adm:pend"))
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
		switch {
		case status.Code(err) == codes.AlreadyExists && strings.Contains(status.Convert(err).Message(), "already paid another payment"):
			// Its TXID paid an automatic USDT payment (or another proof): the
			// item keeps its buttons, to be rejected.
			if r.cb != nil {
				r.toast, r.alert = r.t("admin.txid_used"), true
			} else {
				r.send(r.t("admin.txid_used"), nil)
			}
			return
		case status.Code(err) != codes.FailedPrecondition:
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
			r.send(r.t("admin.not_found"), (&tg.Keyboard{}).Row(r.button("btn.back", "adm")))
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
	statusBtn := r.button("btn.adm_ban", "adm:ban:"+u.GetId())
	if u.GetStatus() == "banned" {
		statusBtn = r.button("btn.adm_unban", "adm:unban:"+u.GetId())
	}
	kb := (&tg.Keyboard{}).Row(r.button("btn.adm_adjust", "adm:adj:"+u.GetId()), statusBtn).
		Row(r.button("btn.back", "adm"))
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
	case sceneAdminInput:
		h.wizText(r, text)
	case sceneAdminFind:
		r.clearState()
		v, err := h.core.AdminFindUser(r.ctx, &corev1.AdminFindUserRequest{ActorTelegramId: h.actor(r), Query: text})
		if status.Code(err) == codes.NotFound || status.Code(err) == codes.InvalidArgument {
			r.send(r.t("admin.not_found"), (&tg.Keyboard{}).Row(r.button("btn.adm_find", "adm:find"), r.button("btn.back", "adm")))
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
		r.send(r.t("admin.adjust_done", "balance", r.money(w.GetBalance(), w.GetCurrency())), (&tg.Keyboard{}).Row(r.button("btn.back", "adm")))
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
		h.onSetCommand(r, args)
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
