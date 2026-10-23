package handler

import (
	"strconv"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Undo in the bot's admin menu: the last change a staff member made with the menu's buttons (a setting, a plan switched on or off, a discount code
// switched on or off) is kept for an hour, and the screens of the menu show an "Undo" button that puts it back. One change is kept per person: a
// new change replaces it, and undoing it forgets it.
//
// It is kept in the checkout store under a nonce of its own: Type is "undo:<what>", PlanID the target (a setting's key, a plan's id, a code),
// Label what it was before, DiscountCode what the change wrote (Undo is refused when the value is no longer that: someone changed it again since,
// and putting the old value back would undo their change, not this one) and SubscriptionID the words the button shows.

const undoNonce = "undo"

// flipBool is "true" for "false" and the other way: what a switch holds after it was flipped from old.
func flipBool(old string) string {
	if old == "true" {
		return "false"
	}
	return "true"
}

// rememberUndo keeps the change just made. old is the value before it (a setting's old value may be "": it was unset). What the change wrote is
// read back here for a setting (core may have normalised it) and is the other position of the switch for a plan or a discount code.
func (h *Handler) rememberUndo(r *req, what, target, old, label string) {
	now := flipBool(old)
	if what == "set" {
		sp, err := h.settingSpec(r, target)
		if err != nil {
			return // without the value to compare, a later Undo could undo someone else's change: keep nothing
		}
		now = sp.GetValue()
	}
	c := state.Checkout{Type: "undo:" + what, PlanID: target, Label: old, DiscountCode: now, SubscriptionID: label}
	if err := h.state.SaveCheckout(r.ctx, r.from.ID, undoNonce, c); err != nil {
		h.log.Warn("undo not kept", "err", err)
	}
}

func (h *Handler) loadUndo(r *req) (state.Checkout, bool) {
	c, ok, err := h.state.LoadCheckout(r.ctx, r.from.ID, undoNonce)
	if err != nil || !ok || !strings.HasPrefix(c.Type, "undo:") {
		return state.Checkout{}, false
	}
	return c, true
}

// addUndo adds the Undo button to a screen of the menu when there is something to take back.
func (h *Handler) addUndo(r *req, kb *tg.Keyboard) {
	if c, ok := h.loadUndo(r); ok {
		kb.Row(tg.CB(r.t("btn.adm_undo", "what", truncRunes(c.SubscriptionID, 40)), "adm:undo"))
	}
}

// onUndo puts the last change back.
func (h *Handler) onUndo(r *req) {
	c, ok := h.loadUndo(r)
	if !ok {
		r.toast, r.alert = r.t("admin.undo_none"), true
		h.showAdmin(r)
		return
	}
	_ = h.state.SaveCheckout(r.ctx, r.from.ID, undoNonce, state.Checkout{}) // it is used up, whatever follows
	what := strings.TrimPrefix(c.Type, "undo:")
	switch what {
	case "set":
		sp, err := h.settingSpec(r, c.PlanID)
		if err != nil {
			r.fail(err)
			return
		}
		if sp.GetValue() != c.DiscountCode {
			r.toast, r.alert = r.t("admin.undo_changed"), true
			h.showGroup(r, sp.GetGroup())
			return
		}
		if bad := h.setSetting(r, c.PlanID, c.Label); bad != "" {
			r.toast, r.alert = r.t("admin.refused_short"), true
			h.showKey(r, c.PlanID)
			return
		}
		r.toast = r.t("admin.undone")
		h.showGroup(r, sp.GetGroup())
	case "plan":
		p, err := h.findPlan(r, c.PlanID, true)
		if err != nil {
			r.fail(err)
			return
		}
		if strconv.FormatBool(p.GetEnabled()) != c.DiscountCode {
			r.toast, r.alert = r.t("admin.undo_changed"), true
			h.showPlanCard(r, c.PlanID)
			return
		}
		p.Enabled = c.Label == "true"
		if _, err := h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: p}); err != nil {
			r.fail(err)
			return
		}
		r.toast = r.t("admin.undone")
		h.showPlanCard(r, c.PlanID)
	case "disc":
		list, err := h.listDiscounts(r)
		if err != nil {
			r.fail(err)
			return
		}
		for _, d := range list {
			if d.GetCode() != c.PlanID {
				continue
			}
			if strconv.FormatBool(d.GetEnabled()) != c.DiscountCode {
				break // changed since: handled below
			}
			d.Enabled = c.Label == "true"
			if _, err := h.core.AdminUpsertDiscount(r.ctx, &corev1.AdminUpsertDiscountRequest{ActorTelegramId: h.actor(r), Discount: d}); err != nil {
				r.fail(err)
				return
			}
			r.toast = r.t("admin.undone")
			h.showDiscountMenu(r)
			return
		}
		r.toast, r.alert = r.t("admin.undo_changed"), true // the code changed again or is gone
		h.showDiscountMenu(r)
	default:
		h.showAdmin(r)
	}
}
