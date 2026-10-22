package handler

import (
	"regexp"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A reseller chooses, once, the prefix of the names of their configs in the store's 3x-ui panel ("sob" makes "sob-u123-…"), from
// the reseller panel. Afterwards only the owner changes it, in the dashboard. Core decides what is allowed (the shape and that no other
// reseller has it); the bot only checks the shape first so that a typo is answered at once and asked about before it is final.

// sceneResellerPrefix is the question after "Config prefix": the next message is the prefix.
const sceneResellerPrefix = "rs_prefix"

// prefixRe is the shape core takes (domain.NormalizeConfigPrefix): 2 to 10 lowercase letters or digits.
var prefixRe = regexp.MustCompile(`^[a-z0-9]{2,10}$`)

// showPrefix handles "rs:pfx": the reseller's prefix when they have one (and that only the owner changes it), else the question.
func (h *Handler) showPrefix(r *req) {
	info, ok := h.resellerInfo(r)
	if !ok {
		return
	}
	if p := info.GetConfigPrefix(); p != "" {
		r.show(r.t("rs.prefix_view", "prefix", esc(p)), backHome(r, "rs"))
		return
	}
	r.setState(sceneResellerPrefix, nil)
	r.show(r.t("rs.prefix_ask"), cancelKeyboard(r))
}

// onPrefixText is the answer to the question: a prefix of the right shape is asked about once more (it is final for the reseller).
func (h *Handler) onPrefixText(r *req) {
	p := strings.ToLower(strings.TrimSpace(r.msg.Text))
	if !prefixRe.MatchString(p) {
		r.send(r.t("rs.prefix_bad"), cancelKeyboard(r))
		return
	}
	r.clearState()
	r.show(r.t("rs.prefix_confirm", "prefix", esc(p)),
		(&tg.Keyboard{}).Row(r.button("btn.rs_prefix_yes", "rs:pfy:"+p), r.button("btn.cancel", "cancel")))
}

// setPrefix handles "rs:pfy:<prefix>": core saves it, once.
func (h *Handler) setPrefix(r *req, p string) {
	if !prefixRe.MatchString(p) {
		h.showPrefix(r)
		return
	}
	info, err := h.core.SetResellerConfigPrefix(r.ctx, &corev1.SetResellerConfigPrefixRequest{UserId: r.user.GetId(), Prefix: p})
	switch status.Code(err) {
	case codes.OK:
		r.show(r.t("rs.prefix_saved", "prefix", esc(info.GetConfigPrefix())), backHome(r, "rs"))
	case codes.InvalidArgument: // taken by another reseller, or a shape only core knows: asked again
		r.setState(sceneResellerPrefix, nil)
		r.send(r.t("rs.prefix_bad"), cancelKeyboard(r))
	case codes.FailedPrecondition: // set already (an old button)
		h.showPrefix(r)
	default:
		r.fail(err)
	}
}
