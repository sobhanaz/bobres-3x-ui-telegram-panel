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

// The daily wheel of fortune (docs/03-bot-ux.md, "Daily wheel"): a customer with enough paid orders spins once a day, free, for a chance at wallet
// credit. Core keeps every rule (who may spin, the draw, the day's budget, once a day); the bot words the answers. The button is on the home screen
// and in the bottom menu only while the owner has switched the wheel on and given it prizes and a budget, and only for customers.

// wheelOn: the settings the bot holds say the wheel can pay. Core says the same to a tap that comes later.
func (h *Handler) wheelOn() bool {
	if !h.set.Bool("wheel.enabled", false) {
		return false
	}
	budget, _ := i18n.ParseNumber(h.setting("wheel.daily_budget"))
	prizes := strings.TrimSpace(h.setting("wheel.prizes"))
	return budget > 0 && prizes != "" && prizes != "[]" && prizes != "null"
}

// wheelFor: the wheel's button is offered to this user: it is on, and they are an active customer (not staff, not a reseller).
func (h *Handler) wheelFor(r *req) bool { return h.wheelOn() && r.canApply() }

// onWheel handles "wh" (the wheel's screen) and "wh:spin" (the spin).
func (h *Handler) onWheel(r *req, rest string) {
	if !h.wheelFor(r) {
		h.wheelOff(r)
		return
	}
	if rest == "spin" {
		h.spinWheel(r)
		return
	}
	h.showWheel(r)
}

// wheelOff answers a tap on a wheel that is off: a popup, and the home screen again (without the button, once the settings say so).
func (h *Handler) wheelOff(r *req) {
	r.toast, r.alert = r.t("wheel.off"), true
	h.showHome(r)
}

// wheelChance writes a segment's chance, weight out of total, as a percent with up to two decimals ("37.5%", "۳۷٫۵٪").
func wheelChance(lang string, weight, total int64) string {
	if total <= 0 {
		return i18n.Percent(lang, 0)
	}
	hundredths := (weight*10000 + total/2) / total
	whole, frac := hundredths/100, hundredths%100
	s := strconv.FormatInt(whole, 10)
	if frac != 0 {
		f := strconv.FormatInt(100+frac, 10)[1:] // two digits
		s += "." + strings.TrimRight(f, "0")
	}
	s = i18n.Digits(lang, s)
	if i18n.Normalize(lang) == "fa" {
		return strings.Replace(s, ".", "٫", 1) + "٪"
	}
	return s + "%"
}

// wheelResult is the sentence about a spin that is done: the prize, or none.
func (r *req) wheelResult(prize int64) string {
	if prize > 0 {
		return r.t("wheel.result_won", "amount", r.money(prize, "IRT"))
	}
	return r.t("wheel.result_none")
}

// showWheel shows the wheel: what could be won and with what chance, and the Spin button; or why there is none (the paid orders still needed, the
// spin already made today, the day's prizes all given).
func (h *Handler) showWheel(r *req) {
	st, err := h.core.GetWheel(r.ctx, &corev1.GetWheelRequest{UserId: r.user.GetId()})
	if status.Code(err) == codes.Unimplemented { // a core older than this bot
		h.wheelOff(r)
		return
	}
	if err != nil {
		r.fail(err)
		return
	}
	if !st.GetOn() || st.GetReason() == "role" {
		h.wheelOff(r)
		return
	}
	home := (&tg.Keyboard{}).Row(r.button("btn.home", "home"))
	switch {
	case st.GetReason() == "orders":
		r.show(r.t("wheel.need_orders", "need", i18n.Number(r.lang, st.GetMinOrders()), "have", i18n.Number(r.lang, st.GetOrders())), home)
	case st.GetSpun():
		r.show(r.t("wheel.spun_today", "result", r.wheelResult(st.GetPrize())), home)
	case !st.GetOpen():
		r.show(r.t("wheel.closed"), home)
	default:
		lines := make([]string, 0, len(st.GetPrizes())+1)
		for _, p := range st.GetPrizes() {
			lines = append(lines, r.t("wheel.prize_line", "amount", r.money(p.GetAmount(), "IRT"), "chance", wheelChance(r.lang, p.GetWeight(), st.GetTotalWeight())))
		}
		if st.GetNoPrizeWeight() > 0 {
			lines = append(lines, r.t("wheel.no_prize_line", "chance", wheelChance(r.lang, st.GetNoPrizeWeight(), st.GetTotalWeight())))
		}
		kb := (&tg.Keyboard{}).Row(r.button("btn.wheel_spin", "wh:spin")).Row(r.button("btn.home", "home"))
		r.show(r.t("wheel.screen", "prizes", strings.Join(lines, "\n")), kb)
	}
}

// spinWheel handles "wh:spin": core draws, the bot says where the wheel stopped. Asked twice, core answers with the first spin, so a double tap
// shows the same result twice and pays once.
func (h *Handler) spinWheel(r *req) {
	res, err := h.core.SpinWheel(r.ctx, &corev1.SpinWheelRequest{UserId: r.user.GetId()})
	if status.Code(err) == codes.Unimplemented {
		h.wheelOff(r)
		return
	}
	if err != nil {
		r.fail(err)
		return
	}
	home := (&tg.Keyboard{}).Row(r.button("btn.home", "home"))
	switch res.GetStatus() {
	case "won":
		kb := (&tg.Keyboard{}).Row(r.button("btn.wallet", "wallet")).Row(r.button("btn.home", "home"))
		r.show(r.t("wheel.won", "amount", r.money(res.GetPrize(), "IRT"), "balance", r.money(res.GetBalance(), "IRT")), kb)
	case "lost":
		r.show(r.t("wheel.lost"), home)
	case "already":
		r.show(r.t("wheel.spun_today", "result", r.wheelResult(res.GetPrize())), home)
	case "closed":
		r.show(r.t("wheel.closed"), home)
	case "orders":
		r.show(r.t("wheel.need_orders", "need", i18n.Number(r.lang, res.GetMinOrders()), "have", i18n.Number(r.lang, res.GetOrders())), home)
	default: // off, or not a customer
		h.wheelOff(r)
	}
}
