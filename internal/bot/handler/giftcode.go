package handler

import (
	"fmt"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Gift codes (docs/03-bot-ux.md): the wallet screen's "Apply gift code" button
// asks for a code in a scene, and core says what became of it. The bot holds no
// rule: the code is checked, limited and credited in core, which also counts the
// wrong ones. A code is never logged here, and the scene keeps no copy of it.

// sceneGiftCode: the next message is a gift code.
const sceneGiftCode = "gift"

// giftCodesOn is the owner's switch (gift_codes.enabled, on unless it is false).
func (h *Handler) giftCodesOn() bool { return h.set.Bool("gift_codes.enabled", true) }

// giftButton is the wallet screen's button for a gift code: none while the
// owner has gift codes switched off.
func (h *Handler) giftButton(r *req) []tg.Button {
	if !h.giftCodesOn() {
		return nil
	}
	return []tg.Button{r.button("btn.gift_code", "gift")}
}

// askGiftCode handles "gift": the next message is the code. A button from
// before the owner switched the codes off says so and shows the wallet again.
func (h *Handler) askGiftCode(r *req) {
	if !h.giftCodesOn() {
		r.toast, r.alert = r.t("gift.bad.off"), true
		h.showWallet(r)
		return
	}
	r.setState(sceneGiftCode, nil)
	r.show(r.t("gift.ask"), cancelKeyboard(r))
}

// giftWallet is where a finished answer leads: the wallet, or home.
func giftWallet(r *req) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(r.button("btn.wallet", "wallet"), r.button("btn.home", "home"))
}

// onGiftCode takes the message the customer sent as their gift code. What a
// wrong code is told leaves the question open (Cancel is under it); a closed
// door (locked, off) and a success end the scene.
func (h *Handler) onGiftCode(r *req) {
	code := strings.TrimSpace(r.msg.Text)
	if code == "" {
		r.send(r.t("gift.bad.not_text"), cancelKeyboard(r))
		return
	}
	if !h.giftCodesOn() {
		r.clearState()
		r.send(r.t("gift.bad.off"), giftWallet(r))
		return
	}
	// The message is the request: Telegram sending the same update again, or
	// this handler retrying it, credits once.
	resp, err := h.core.RedeemGiftCode(r.ctx, &corev1.RedeemGiftCodeRequest{
		UserId: r.user.GetId(), Code: code, IdempotencyKey: fmt.Sprintf("gift:%d:%d", r.from.ID, r.msg.MessageID),
	})
	if err != nil {
		if status.Code(err) == codes.PermissionDenied { // banned: nothing more to ask
			r.clearState()
		}
		r.fail(err)
		return
	}
	switch resp.GetOutcome() {
	case corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_REDEEMED:
		r.clearState()
		c, b := resp.GetCredited(), resp.GetBalance()
		r.send(r.t("gift.applied", "amount", r.money(c.GetAmount(), c.GetCurrency()), "balance", r.balance(b.GetAmount(), b.GetCurrency())), giftWallet(r))
	case corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_INVALID:
		r.send(r.t("gift.bad.invalid"), cancelKeyboard(r))
	case corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_USED_UP:
		r.send(r.t("gift.bad.used_up"), cancelKeyboard(r))
	case corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_ALREADY_REDEEMED:
		r.send(r.t("gift.bad.already_redeemed"), cancelKeyboard(r))
	case corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_LOCKED:
		r.clearState()
		minutes := max((resp.GetRetryAfterSeconds()+59)/60, 1)
		r.send(r.t("gift.bad.locked", "minutes", i18n.Number(r.lang, minutes)), giftWallet(r))
	case corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_OFF:
		r.clearState()
		r.send(r.t("gift.bad.off"), giftWallet(r))
	default: // an outcome a newer core added: never a guess
		r.clearState()
		r.send(r.t("error.generic"), homeKeyboard(r))
	}
}
