package handler

import (
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Config names (docs/03-bot-ux.md, "Config names"): while the owner has them on for the buyer's role, the menu of a new
// service (a customer's, or a reseller's for a customer) offers "Choose a name" beside the automatic one, and a service
// that can be renamed has a "Change config name" button. Core checks every name (the rules, and that no config on any
// panel has it) and answers with a reason the bot puts in words; renewals and top-ups never ask.
//
// Buttons: "cn:c:<nonce>" asks for a name for the payment menu saved under nonce, "cn:a:<nonce>" goes back to the
// automatic name, "cn:r:<service id>" asks for a service's new name.

const (
	sceneConfigName   = "cfg_name"   // a config name for a payment menu (co: its nonce)
	sceneConfigRename = "cfg_rename" // a service's new config name (sub: its id)
)

// The settings that switch config names on (core's domain.SettingConfigNames*).
const (
	settingConfigNamesCustomers = "config_names.customers"
	settingConfigNamesResellers = "config_names.resellers"
)

// configNamesOn: the buyer may choose config names (the setting of their role).
func (h *Handler) configNamesOn(r *req) bool {
	if r.isReseller() {
		return h.set.Bool(settingConfigNamesResellers, false)
	}
	return h.set.Bool(settingConfigNamesCustomers, false)
}

// configNameReasons are the reasons core gives (ConfigNameCheck.reason) that have a text of their own.
var configNameReasons = map[string]bool{
	"off": true, "length": true, "chars": true, "mix": true, "automatic": true, "taken": true, "unchecked": true, "not_renamable": true,
}

// configNameText is why a name cannot be used, in the user's language.
func configNameText(r *req, reason string) string {
	if !configNameReasons[reason] {
		reason = "unchecked"
	}
	return r.t("cfg.bad." + reason)
}

// orderNameReason is the reason core refused an order's config name for ("" when err is something else): core answers
// InvalidArgument "... config name: <reason>".
func orderNameReason(err error) string {
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		return ""
	}
	_, reason, found := strings.Cut(st.Message(), "config name: ")
	if !found {
		return ""
	}
	return reason
}

// checkoutNameLine is the payment menu's line about the config name.
func checkoutNameLine(r *req, c state.Checkout) string {
	if c.ClientFull == "" {
		return r.t("cfg.checkout_auto")
	}
	return r.t("cfg.checkout_name", "name", esc(c.ClientFull))
}

// checkoutNameButtons are the payment menu's buttons about the config name.
func checkoutNameButtons(r *req, c state.Checkout, n string) []tg.Button {
	if c.ClientName == "" {
		return []tg.Button{r.button("btn.cfg_choose", "cn:c:"+n)}
	}
	return []tg.Button{r.button("btn.cfg_change", "cn:c:"+n), r.button("btn.cfg_auto", "cn:a:"+n)}
}

// onConfigName routes the "cn:" buttons.
func (h *Handler) onConfigName(r *req, rest string) {
	what, arg, _ := strings.Cut(rest, ":")
	switch what {
	case "c":
		h.askConfigName(r, arg)
	case "a":
		c, ok := h.loadCheckout(r, arg)
		if !ok {
			return
		}
		r.clearState()
		c.ClientName, c.ClientFull = "", ""
		h.showCheckout(r, c)
	case "r":
		h.askConfigRename(r, arg)
	default:
		r.toast = r.t("error.expired")
		h.showHome(r)
	}
}

// askConfigName asks for the config name of the new service a payment menu sells.
func (h *Handler) askConfigName(r *req, n string) {
	c, ok := h.loadCheckout(r, n)
	if !ok {
		return
	}
	if c.Type != "new" || !h.configNamesOn(r) { // a menu from before the owner switched them off
		c.ClientName, c.ClientFull = "", ""
		h.showCheckout(r, c)
		return
	}
	r.setState(sceneConfigName, map[string]string{"co": n})
	r.show(r.t("cfg.ask"), (&tg.Keyboard{}).Row(r.button("btn.cfg_auto", "cn:a:"+n)).Row(r.button("btn.cancel", "cancel")))
}

// onConfigNameText checks the name the buyer sent: a name core takes goes on the payment menu (shown again); one it does
// not take is explained and the question stays open (or, config names switched off meanwhile, the menu comes back with
// the automatic name).
func (h *Handler) onConfigNameText(r *req, st state.State) {
	n := st.Get("co")
	c, ok := h.loadCheckout(r, n)
	if !ok {
		r.clearState()
		return
	}
	name := strings.TrimSpace(r.msg.Text)
	res, err := h.core.CheckConfigName(r.ctx, &corev1.CheckConfigNameRequest{UserId: r.user.GetId(), Name: name})
	if err != nil {
		r.clearState()
		r.fail(err)
		return
	}
	switch {
	case res.GetOk():
		r.clearState()
		c.ClientName, c.ClientFull = name, res.GetName()
		h.showCheckout(r, c)
	case res.GetReason() == "off":
		r.clearState()
		r.send(configNameText(r, res.GetReason()), nil)
		c.ClientName, c.ClientFull = "", ""
		h.showCheckout(r, c)
	default:
		r.send(configNameText(r, res.GetReason()), (&tg.Keyboard{}).Row(r.button("btn.cfg_auto", "cn:a:"+n)).Row(r.button("btn.cancel", "cancel")))
	}
}

// askConfigRename asks for a new name for one of the user's services.
func (h *Handler) askConfigRename(r *req, id string) {
	s, err := h.subscription(r, id)
	if err != nil {
		r.fail(err)
		return
	}
	if s == nil {
		h.showServices(r)
		return
	}
	if !s.GetRenamable() {
		r.toast, r.alert = r.t("cfg.bad.not_renamable"), true
		h.showService(r, id)
		return
	}
	r.setState(sceneConfigRename, map[string]string{"sub": id})
	r.show(r.t("cfg.ask_rename", "name", esc(s.GetClientEmail()))+"\n\n"+r.t("cfg.ask"), backHome(r, "sub:"+id))
}

// onConfigRenameText renames the service to the name the user sent; a name core does not take is explained and the
// question stays open, unless the service cannot be renamed any more.
func (h *Handler) onConfigRenameText(r *req, st state.State) {
	id := st.Get("sub")
	res, err := h.core.RenameService(r.ctx, &corev1.RenameServiceRequest{UserId: r.user.GetId(), SubscriptionId: id, Name: strings.TrimSpace(r.msg.Text)})
	if err != nil {
		r.clearState()
		r.fail(err)
		return
	}
	switch {
	case res.GetOk():
		r.clearState()
		r.send(r.t("cfg.renamed", "name", esc(res.GetName())), nil)
		h.showService(r, id)
	case res.GetReason() == "off" || res.GetReason() == "not_renamable":
		r.clearState()
		r.send(configNameText(r, res.GetReason()), nil)
		h.showService(r, id)
	default:
		r.send(configNameText(r, res.GetReason()), backHome(r, "sub:"+id))
	}
}
