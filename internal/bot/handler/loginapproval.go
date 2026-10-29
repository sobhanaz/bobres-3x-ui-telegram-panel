package handler

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/loginwire"
)

// Approving dashboard sign-ins (docs/04-dashboard.md, "Signing in"). Core decides everything and the bot carries it:
//   - core asks the bot (POST /internal/login-approval/..., runner.LoginApprovalRequest and its two siblings) to message the people who may
//     approve a sign-in, to write the end of a sign-in into those messages, and to tell the owners that the switch changed;
//   - a tap on Approve or Deny arrives as the button "lga:<y|n>:<id>:<nonce>" and goes to core over gRPC (LoginApprovalService); core says
//     what it came to, and the bot writes that into the message.
// A button the bot cannot understand, or one core refuses, only gets a short popup: the bot never decides who may approve.

// approvalMinutes is how long a sign-in waits, as the messages say it.
const approvalMinutes = 5

// SetLoginApprovals gives the handler the way to core's LoginApprovalService (set once, when the bot starts).
func (h *Handler) SetLoginApprovals(c corev1.LoginApprovalServiceClient) { h.approvals = c }

// emptyKeyboard takes the buttons off a message that is edited.
func emptyKeyboard() *tg.Keyboard { return &tg.Keyboard{InlineKeyboard: [][]tg.Button{}} }

// accountName is how a staff member is named in the messages: @username, else their id. It is escaped for HTML.
func accountName(lang string, a loginwire.Account) string {
	if a.Username != "" {
		return esc("@" + a.Username)
	}
	if a.TelegramID > 0 {
		return esc(i18n.Digits(lang, "id "+strconv.FormatInt(a.TelegramID, 10)))
	}
	return ""
}

// browserNames and systemNames are the brands core's codes stand for: the same in every language.
var (
	browserNames = map[string]string{"chrome": "Chrome", "firefox": "Firefox", "safari": "Safari", "edge": "Edge", "opera": "Opera", "samsung": "Samsung Internet"}
	systemNames  = map[string]string{"windows": "Windows", "macos": "macOS", "linux": "Linux", "android": "Android", "ios": "iOS", "chromeos": "ChromeOS"}
)

// deviceText names the browser and the system from core's codes; a code the bot does not know reads as unknown.
func (h *Handler) deviceText(lang, browser, system string) string {
	b, s := browserNames[browser], systemNames[system]
	switch {
	case b != "" && s != "":
		return h.cat.T(lang, "loginapp.device", "browser", b, "system", s)
	case b != "":
		return b
	case s != "":
		return s
	}
	return h.cat.T(lang, "loginapp.device_unknown")
}

// methodText names how the person at the browser got through the first checks.
func (h *Handler) methodText(lang, method string) string {
	switch method {
	case "link", "password", "password_initial":
		return h.cat.T(lang, "loginapp.method_"+method)
	}
	return h.cat.T(lang, "loginapp.unknown")
}

// requestText is the message an approver reads, and requestKeyboard its two buttons.
func (h *Handler) requestText(lang string, req loginwire.Request, role string) string {
	key := "loginapp.request"
	if role == "owner" {
		key = "loginapp.request_owner"
	}
	note := ""
	if req.NewAddress {
		note = "\n" + h.cat.T(lang, "loginapp.new_address")
	}
	ip := "-"
	if p := net.ParseIP(req.IP); p != nil {
		ip = p.String()
	}
	return h.cat.T(lang, key,
		"account", accountName(lang, req.Account),
		"time", esc(i18n.DateTime(lang, time.Unix(req.At, 0))),
		"ip", esc(ip),
		"new_note", note,
		"device", esc(h.deviceText(lang, req.Browser, req.System)),
		"method", esc(h.methodText(lang, req.Method)),
		"minutes", i18n.Number(lang, approvalMinutes))
}

func (h *Handler) requestKeyboard(lang, id, nonce string) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(
		h.button(lang, "btn.login_approve", loginwire.CallbackData(true, id, nonce)),
		h.button(lang, "btn.login_deny", loginwire.CallbackData(false, id, nonce)))
}

// SendLoginApproval messages each approver of a sign-in (core's call). One result for each: the message's id, or why it was not sent. A
// person who blocked the bot, or never started it, is "blocked"; any other refusal or failure is "telegram".
func (h *Handler) SendLoginApproval(ctx context.Context, req loginwire.Request) []loginwire.Result {
	out := make([]loginwire.Result, 0, len(req.Targets))
	for _, t := range req.Targets {
		lang := i18n.Normalize(t.Language)
		msg, err := h.tg.SendMessage(ctx, t.TelegramID, h.requestText(lang, req, t.Role), h.requestKeyboard(lang, req.ApprovalID, t.Nonce))
		res := loginwire.Result{TelegramID: t.TelegramID}
		switch {
		case tg.IsBlocked(err):
			res.Error = loginwire.ErrBlocked
		case err != nil:
			h.log.Warn("sign-in approval: the message was not sent", "chat", t.TelegramID, "err", err)
			res.Error = loginwire.ErrTelegram
		default:
			res.MessageID = int64(msg.MessageID)
		}
		out = append(out, res)
	}
	return out
}

// resolutionText is what a finished sign-in's message says.
func (h *Handler) resolutionText(lang, result string, account loginwire.Account, by *loginwire.Account, at time.Time) string {
	key := "loginapp." + result
	byName := h.cat.T(lang, "loginapp.unknown")
	if by != nil {
		if n := accountName(lang, *by); n != "" {
			byName = n
		}
	}
	return h.cat.T(lang, key, "account", accountName(lang, account), "by", byName, "time", esc(i18n.Clock(lang, at)))
}

// ResolveLoginApproval writes the end of a sign-in into the messages sent for it, taking their buttons away (core's call). It returns an
// error only when a message could be written over later: one that cannot be (the person blocked the bot, Telegram refuses the edit) is left.
func (h *Handler) ResolveLoginApproval(ctx context.Context, res loginwire.Resolution) error {
	var retry error
	for _, t := range res.Targets {
		lang := i18n.Normalize(t.Language)
		text := h.resolutionText(lang, res.Result, res.Account, res.By, time.Unix(res.At, 0))
		err := h.tg.EditMessageText(ctx, t.TelegramID, int(t.MessageID), text, emptyKeyboard())
		if err != nil && !tg.IsBlocked(err) && !tg.IsRefused(err) {
			retry = err
		}
	}
	return retry
}

// NoticeLoginApproval tells the owners that the switch was turned on or off (core's call).
func (h *Handler) NoticeLoginApproval(ctx context.Context, n loginwire.Notice) []loginwire.Result {
	out := make([]loginwire.Result, 0, len(n.Targets))
	for _, t := range n.Targets {
		lang := i18n.Normalize(t.Language)
		by := h.cat.T(lang, "loginapp.by_server")
		if n.By != nil {
			if name := accountName(lang, *n.By); name != "" && n.Source == "dashboard" {
				by = h.cat.T(lang, "loginapp.by_dashboard", "name", name)
			} else if name != "" {
				by = name
			}
		}
		_, err := h.tg.SendMessage(ctx, t.TelegramID, h.cat.T(lang, "loginapp."+n.Kind, "by", by, "minutes", i18n.Number(lang, approvalMinutes)), nil)
		res := loginwire.Result{TelegramID: t.TelegramID}
		switch {
		case tg.IsBlocked(err):
			res.Error = loginwire.ErrBlocked
		case err != nil:
			h.log.Warn("sign-in approval: the notice was not sent", "chat", t.TelegramID, "err", err)
			res.Error = loginwire.ErrTelegram
		}
		out = append(out, res)
	}
	return out
}

// onLoginApproval is a tap on Approve or Deny under a message core had sent (the callback "lga:<y|n>:<id>:<nonce>"). The account that
// tapped is Telegram's word; everything else is core's to decide, and a refusal is a popup.
func (h *Handler) onLoginApproval(r *req, rest string) {
	approve, id, nonce, ok := loginwire.ParseCallback(rest)
	if !ok || h.approvals == nil || !loginwire.ID(id) || !loginwire.Nonce(nonce) {
		r.toast, r.alert = r.t("loginapp.toast_stale"), true
		return
	}
	resp, err := h.approvals.DecideLoginApproval(r.ctx, &corev1.DecideLoginApprovalRequest{TelegramId: r.from.ID, ApprovalId: id, Nonce: nonce, Approve: approve})
	if err != nil {
		r.fail(err)
		return
	}
	account := loginwire.Account{Username: resp.GetAccountUsername(), TelegramID: resp.GetAccountTelegramId(), Role: resp.GetAccountRole()}
	var by *loginwire.Account
	if resp.GetByTelegramId() > 0 {
		by = &loginwire.Account{Username: resp.GetByUsername(), TelegramID: resp.GetByTelegramId(), Role: resp.GetByRole()}
	}
	at := time.Now()
	if resp.GetAt() > 0 {
		at = time.Unix(resp.GetAt(), 0)
	}
	outcome, result := resp.GetOutcome(), ""
	switch outcome {
	case "approved":
		result, r.toast = "approved", r.t("loginapp.toast_approved")
	case "denied":
		result, r.toast = "denied", r.t("loginapp.toast_denied")
	case "already_approved":
		result, r.toast = "approved", r.t("loginapp.toast_stale")
	case "already_denied":
		result, r.toast = "denied", r.t("loginapp.toast_stale")
	case "expired", "cancelled":
		result, r.toast = outcome, r.t("loginapp.toast_stale")
	case "forbidden":
		r.toast, r.alert = r.t("loginapp.toast_forbidden"), true
		return
	default: // invalid, or an answer this bot does not know: nothing happened
		r.toast, r.alert = r.t("loginapp.toast_stale"), true
		return
	}
	// A popup that says the request had ended already is read, not glanced at.
	r.alert = outcome != "approved" && outcome != "denied"
	if strings.TrimSpace(account.Role) == "" {
		return // core did not say who signs in: the popup is all there is
	}
	// The request is written over in place, also when a bottom menu was sent along with this tap: a request that keeps its buttons
	// after it was answered would invite a second tap.
	r.keep = false
	r.show(h.resolutionText(r.lang, result, account, by, at), emptyKeyboard())
}
