package handler

import (
	"strconv"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Today's numbers, the 3x-ui servers and the staff. The servers are for the
// owner and the admin (as on the dashboard); the staff screens are owner only:
// core refuses anyone else, and the bot does not show them.

// isOwner: an active owner (core checks the role again on every call).
func (r *req) isOwner() bool {
	return r.user != nil && r.user.GetStatus() == "active" && r.user.GetRole() == "owner"
}

// ownerOnly refuses politely when the user is not an owner.
func (r *req) ownerOnly() bool {
	if r.isOwner() {
		return true
	}
	r.toast, r.alert = r.t("admin.owner_only"), true
	return false
}

func (r *req) dayText(d *corev1.AdminDay) string {
	var lines []string
	for _, l := range d.GetLines() {
		lines = append(lines, r.t("admin.day_line", "revenue", r.money(l.GetRevenue(), l.GetCurrency()),
			"orders", i18n.Number(r.lang, l.GetOrders()), "refunds", r.money(l.GetRefunds(), l.GetCurrency())))
	}
	if len(lines) == 0 {
		lines = append(lines, r.t("admin.day_none"))
	}
	lines = append(lines, r.t("admin.day_customers", "n", i18n.Number(r.lang, d.GetNewCustomers())))
	return strings.Join(lines, "\n")
}

// showToday: sales and new customers of today and yesterday.
func (h *Handler) showToday(r *req) {
	resp, err := h.core.AdminTodayStats(r.ctx, &corev1.AdminTodayStatsRequest{ActorTelegramId: h.actor(r)})
	if err != nil {
		r.fail(err)
		return
	}
	r.show(r.t("admin.today", "tz", esc(resp.GetTz()),
		"date", esc(resp.GetToday().GetDate()), "today", r.dayText(resp.GetToday()),
		"ydate", esc(resp.GetYesterday().GetDate()), "yesterday", r.dayText(resp.GetYesterday())),
		(&tg.Keyboard{}).Row(r.button("btn.refresh", "adm:today"), r.button("btn.back", "adm")))
}

func (h *Handler) listServers(r *req) ([]*corev1.AdminServer, error) {
	resp, err := h.core.AdminListServers(r.ctx, &corev1.AdminListServersRequest{ActorTelegramId: h.actor(r)})
	return resp.GetServers(), err
}

func (r *req) serverLine(s *corev1.AdminServer) string {
	state := r.t("admin.srv_unchecked")
	switch {
	case !s.GetEnabled():
		state = r.t("admin.srv_disabled")
	case s.GetHealthKnown() && s.GetHealthy():
		state = r.t("admin.srv_healthy")
	case s.GetHealthKnown():
		state = r.t("admin.srv_down")
	}
	line := r.t("admin.srv_line", "name", esc(s.GetName()), "state", state,
		"weight", i18n.Number(r.lang, int64(s.GetWeight())), "clients", i18n.Number(r.lang, s.GetClients()))
	if s.GetHealthKnown() && !s.GetHealthy() && s.GetHealthError() != "" {
		line += "\n   " + r.t("admin.srv_error", "error", esc(s.GetHealthError()))
	}
	return line
}

// showServers lists the servers; a tap opens one.
func (h *Handler) showServers(r *req) {
	list, err := h.listServers(r)
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	lines := []string{r.t("admin.servers_title")}
	if len(list) == 0 {
		lines = append(lines, r.t("admin.servers_list_empty"))
	}
	for _, s := range list {
		lines = append(lines, r.serverLine(s))
		kb.Row(tg.CB("⚙️ "+s.GetName(), "adm:sx:"+s.GetId()))
	}
	kb.Row(r.button("btn.back", "adm"))
	r.show(strings.Join(lines, "\n\n")+"\n\n"+r.t("admin.servers_hint"), kb)
}

func (h *Handler) findServer(r *req, id string) (*corev1.AdminServer, error) {
	list, err := h.listServers(r)
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		if s.GetId() == id {
			return s, nil
		}
	}
	return nil, status.Error(codes.NotFound, "server not found")
}

func (h *Handler) showServerCard(r *req, id string) {
	s, err := h.findServer(r, id)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			r.toast = r.t("error.expired")
			h.showServers(r)
			return
		}
		r.fail(err)
		return
	}
	toggle := r.button("btn.adm_enable", "adm:sy:"+id)
	if s.GetEnabled() {
		toggle = r.button("btn.adm_disable", "adm:sy:"+id)
	}
	r.show(r.t("admin.server_card", "line", r.serverLine(s)),
		(&tg.Keyboard{}).Row(toggle, r.button("btn.adm_weight", "adm:sm:"+id)).Row(r.button("btn.back", "adm:srv")))
}

func (h *Handler) toggleServer(r *req, id string) {
	s, err := h.findServer(r, id)
	if err != nil {
		r.fail(err)
		return
	}
	if _, err := h.core.AdminSetServer(r.ctx, &corev1.AdminSetServerRequest{ActorTelegramId: h.actor(r), ServerId: id, Enabled: !s.GetEnabled()}); err != nil {
		r.fail(err)
		return
	}
	r.toast = r.t("admin.saved")
	h.showServerCard(r, id)
}

func (h *Handler) startServerWeight(r *req, id string) {
	s, err := h.findServer(r, id)
	if err != nil {
		r.fail(err)
		return
	}
	h.startWizard(r, "sw", map[string]string{"id": id, "now_w": strconv.Itoa(int(s.GetWeight())), "name": s.GetName()})
}

func serverWeightWizard() *wizard {
	return &wizard{
		steps: []wizStep{intStep("w", "wiz.sw.weight", 1, 1000, "", nil)},
		save: func(r *req, d map[string]string) string {
			h := r.h
			s, err := h.findServer(r, d["id"])
			if err != nil {
				return h.refused(r, err)
			}
			if _, err := h.core.AdminSetServer(r.ctx, &corev1.AdminSetServerRequest{
				ActorTelegramId: h.actor(r), ServerId: d["id"], Enabled: s.GetEnabled(), Weight: int32(minorOf(d["w"])), //nolint:gosec // 1..1000
			}); err != nil {
				return h.refused(r, err)
			}
			r.toast = r.t("admin.saved")
			h.showServerCard(r, d["id"])
			return ""
		},
		home: func(d map[string]string) string { return "sx:" + d["id"] },
	}
}

// ---- staff ----

func (h *Handler) showStaff(r *req) {
	if !r.ownerOnly() {
		return
	}
	resp, err := h.core.AdminListStaff(r.ctx, &corev1.AdminListStaffRequest{ActorTelegramId: h.actor(r)})
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	lines := []string{r.t("admin.staff_title")}
	for _, m := range resp.GetStaff() {
		who := "<code>" + strconv.FormatInt(m.GetTelegramId(), 10) + "</code>"
		if m.GetUsername() != "" {
			who += " @" + esc(m.GetUsername())
		}
		lines = append(lines, r.t("admin.staff_line", "who", who, "role", m.GetRole()))
		if !m.GetConfiguredOwner() && m.GetTelegramId() != r.from.ID {
			kb.Row(tg.CB("✖️ "+strconv.FormatInt(m.GetTelegramId(), 10)+" · "+m.GetRole(), "adm:sd:"+strconv.FormatInt(m.GetTelegramId(), 10)))
		}
	}
	kb.Row(r.button("btn.adm_add_staff", "adm:sa"))
	kb.Row(r.button("btn.back", "adm"))
	r.show(strings.Join(lines, "\n")+"\n\n"+r.t("admin.staff_hint"), kb)
}

func (h *Handler) startStaffAdd(r *req) {
	if r.ownerOnly() {
		h.startWizard(r, "sadd", nil)
	}
}

func (h *Handler) startStaffRemove(r *req, tgID string) {
	if !r.ownerOnly() {
		return
	}
	if _, err := strconv.ParseInt(tgID, 10, 64); err != nil {
		h.showStaff(r)
		return
	}
	h.startWizard(r, "srem", map[string]string{"tg": tgID})
}

func staffAddWizard() *wizard {
	return &wizard{
		steps: []wizStep{
			intStep("tg", "wiz.staff.tg", 1, 9_999_999_999_999, "", nil),
			choiceStep("role", "wiz.staff.role",
				[2]string{"support", "btn.adm_role_support"}, [2]string{"admin", "btn.adm_role_admin"}, [2]string{"owner", "btn.adm_role_owner"}),
			reasonStep("wiz.staff.reason"),
		},
		confirm: func(r *req, d map[string]string) (string, string) {
			return r.t("admin.confirm_staff_add", "tg", esc(d["tg"]), "role", d["role"], "reason", esc(d["reason"])), ""
		},
		save: func(r *req, d map[string]string) string {
			h := r.h
			if _, err := h.core.AdminAddStaff(r.ctx, &corev1.AdminAddStaffRequest{
				ActorTelegramId: h.actor(r), TargetTelegramId: minorOf(d["tg"]), Role: d["role"], Reason: d["reason"],
			}); err != nil {
				if status.Code(err) == codes.NotFound {
					return r.t("admin.err_user_unknown") // core's "not found" says little
				}
				return h.refused(r, err)
			}
			r.clearState()
			r.toast = r.t("admin.saved")
			h.showStaff(r)
			return ""
		},
		home: func(map[string]string) string { return "stf" },
	}
}

func staffRemoveWizard() *wizard {
	return &wizard{
		steps: []wizStep{reasonStep("wiz.staff.rem_reason")},
		confirm: func(r *req, d map[string]string) (string, string) {
			return r.t("admin.confirm_staff_rem", "tg", esc(d["tg"]), "reason", esc(d["reason"])), ""
		},
		save: func(r *req, d map[string]string) string {
			h := r.h
			if _, err := h.core.AdminRemoveStaff(r.ctx, &corev1.AdminRemoveStaffRequest{
				ActorTelegramId: h.actor(r), TargetTelegramId: minorOf(d["tg"]), Reason: d["reason"],
			}); err != nil {
				return h.refused(r, err)
			}
			r.clearState()
			r.toast = r.t("admin.saved")
			h.showStaff(r)
			return ""
		},
		home: func(map[string]string) string { return "stf" },
	}
}
