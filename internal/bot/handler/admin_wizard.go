package handler

import (
	"maps"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The admin menu asks for input one step at a time: a wizard is a list of
// steps, each with an explained prompt (what the field is, an example, the
// range, the current value), a parser, and a final save. The answers wait in
// the user's state (scene sceneAdminInput) under the step ids; "_w" is the
// wizard and "_i" the step. Every step has Back and Cancel buttons.

// sceneAdminInput is the scene of every wizard of the admin menu.
const sceneAdminInput = "adm_in"

type wizStep struct {
	id string
	// when skips the step when it returns false (nil: always asked).
	when func(d map[string]string) bool
	// prompt is the explained question and the extra button rows (choices).
	prompt func(r *req, d map[string]string) (text string, rows [][]tg.Button)
	// parse checks an answer (typed, or the value of a choice button) and
	// returns what to keep, or the reason it is not accepted.
	parse func(r *req, d map[string]string, in string) (val, bad string)
}

type wizard struct {
	steps []wizStep
	// confirm shows what will be done after the last step; nil saves at once.
	confirm func(r *req, d map[string]string) (text, bad string)
	// save does it; a non-empty result is shown as the reason it was refused,
	// and the user stays where they were.
	save func(r *req, d map[string]string) (bad string)
	// home is the admin screen (callback data after "adm:") to go back to.
	home func(d map[string]string) string
}

// wizardFor builds the wizard with the id; nil for an unknown one.
func wizardFor(id string) *wizard {
	switch id {
	case "plan":
		return planWizard()
	case "topup":
		return topupWizard()
	case "pe":
		return planEditWizard()
	case "trial":
		return trialWizard()
	case "disc":
		return discountWizard()
	case "set":
		return settingWizard()
	case "sw":
		return serverWeightWizard()
	case "sadd":
		return staffAddWizard()
	case "srem":
		return staffRemoveWizard()
	case "bc":
		return broadcastWizard()
	}
	return nil
}

func (w *wizard) applicable(i int, d map[string]string) bool {
	return i >= 0 && i < len(w.steps) && (w.steps[i].when == nil || w.steps[i].when(d))
}

// next is the step after i that is asked (len(steps) when none is left).
func (w *wizard) next(i int, d map[string]string) int {
	for j := i + 1; j < len(w.steps); j++ {
		if w.applicable(j, d) {
			return j
		}
	}
	return len(w.steps)
}

// prev is the step before i that is asked (-1 when none).
func (w *wizard) prev(i int, d map[string]string) int {
	for j := min(i, len(w.steps)) - 1; j >= 0; j-- {
		if w.applicable(j, d) {
			return j
		}
	}
	return -1
}

// wizCurrent loads the wizard the user is in; ok is false when they are in
// none (the state expired, or a button from an old message).
func (h *Handler) wizCurrent(r *req) (w *wizard, d map[string]string, ok bool) {
	st, err := h.state.Get(r.ctx, r.from.ID)
	if err != nil || st.Scene != sceneAdminInput {
		return nil, nil, false
	}
	w = wizardFor(st.Get("_w"))
	if w == nil {
		return nil, nil, false
	}
	d = maps.Clone(st.Data)
	return w, d, true
}

// startWizard begins a wizard with some answers already known (seed).
func (h *Handler) startWizard(r *req, id string, seed map[string]string) {
	w := wizardFor(id)
	d := maps.Clone(seed)
	if d == nil {
		d = map[string]string{}
	}
	d["_w"] = id
	d["_i"] = strconv.Itoa(w.next(-1, d))
	r.setState(sceneAdminInput, d)
	h.wizRender(r, w, d, "")
}

func stepIndex(d map[string]string) int {
	i, err := strconv.Atoi(d["_i"])
	if err != nil {
		return 0
	}
	return i
}

// wizRender shows the current step (or the confirmation), after a reason the
// last answer was refused.
func (h *Handler) wizRender(r *req, w *wizard, d map[string]string, bad string) {
	i := stepIndex(d)
	var text string
	kb := &tg.Keyboard{}
	if i >= len(w.steps) {
		if w.confirm == nil {
			r.clearState()
			h.showAdmin(r)
			return
		}
		t, problem := w.confirm(r, d)
		if t == "" && problem == "" { // the confirmation left the wizard (an error was shown)
			return
		}
		if problem != "" {
			bad = problem
		}
		text = t
		kb.Row(r.button("btn.adm_confirm", "adm:wok"))
	} else {
		t, rows := w.steps[i].prompt(r, d)
		pos, total := 0, 0
		for j := range w.steps {
			if w.applicable(j, d) {
				total++
				if j <= i {
					pos++
				}
			}
		}
		text = r.t("admin.step", "n", pos, "total", total) + "\n" + t
		for _, row := range rows {
			kb.Row(row...)
		}
	}
	if bad != "" {
		text = bad + "\n\n" + text
	}
	nav := []tg.Button{}
	if w.prev(i, d) >= 0 {
		nav = append(nav, r.button("btn.back", "adm:wb"))
	}
	nav = append(nav, r.button("btn.cancel", "adm:wx"))
	kb.Row(nav...)
	r.show(text, kb)
}

// wizText takes what the user typed for the current step.
func (h *Handler) wizText(r *req, in string) {
	w, d, ok := h.wizCurrent(r)
	if !ok {
		r.clearState()
		h.showAdmin(r)
		return
	}
	if strings.TrimSpace(in) == "" {
		h.wizRender(r, w, d, r.t("admin.err_empty"))
		return
	}
	h.wizAnswer(r, w, d, "", in)
}

// wizButton takes a tap on a choice of step: "<step>:<value>".
func (h *Handler) wizButton(r *req, arg string) {
	stepID, val, _ := strings.Cut(arg, ":")
	w, d, ok := h.wizCurrent(r)
	if !ok {
		r.toast = r.t("error.expired")
		h.showAdmin(r)
		return
	}
	h.wizAnswer(r, w, d, stepID, val)
}

func (h *Handler) wizAnswer(r *req, w *wizard, d map[string]string, stepID, in string) {
	i := stepIndex(d)
	if i >= len(w.steps) { // at the confirmation: only its buttons count
		h.wizRender(r, w, d, "")
		return
	}
	step := w.steps[i]
	if stepID != "" && stepID != step.id {
		r.toast = r.t("error.expired")
		h.wizRender(r, w, d, "")
		return
	}
	val, bad := step.parse(r, d, strings.TrimSpace(in))
	if bad != "" {
		h.wizRender(r, w, d, bad)
		return
	}
	d[step.id] = val
	d["_i"] = strconv.Itoa(w.next(i, d))
	if stepIndex(d) >= len(w.steps) && w.confirm == nil {
		h.wizSave(r, w, d)
		return
	}
	r.setState(sceneAdminInput, d)
	h.wizRender(r, w, d, "")
}

// wizSave runs the wizard's save; when refused the user stays and sees why.
func (h *Handler) wizSave(r *req, w *wizard, d map[string]string) {
	if bad := w.save(r, d); bad != "" {
		if w.confirm == nil {
			d["_i"] = strconv.Itoa(w.prev(len(w.steps), d))
		}
		r.setState(sceneAdminInput, d)
		h.wizRender(r, w, d, bad)
		return
	}
	r.clearState()
}

// wizConfirm is a tap on the confirmation's button.
func (h *Handler) wizConfirm(r *req) {
	w, d, ok := h.wizCurrent(r)
	if !ok || stepIndex(d) < len(w.steps) {
		r.toast = r.t("error.expired")
		h.showAdmin(r)
		return
	}
	h.wizSave(r, w, d)
}

// wizBack goes one step back.
func (h *Handler) wizBack(r *req) {
	w, d, ok := h.wizCurrent(r)
	if !ok {
		r.toast = r.t("error.expired")
		h.showAdmin(r)
		return
	}
	p := w.prev(stepIndex(d), d)
	if p < 0 {
		h.wizCancel(r)
		return
	}
	d["_i"] = strconv.Itoa(p)
	r.setState(sceneAdminInput, d)
	h.wizRender(r, w, d, "")
}

// wizCancel leaves the wizard for the screen it was opened from.
func (h *Handler) wizCancel(r *req) {
	home := ""
	if w, d, ok := h.wizCurrent(r); ok && w.home != nil {
		home = w.home(d)
	}
	r.clearState()
	r.toast = r.t("cancelled")
	h.routeAdmin(r, home)
}

// choice is a row of buttons for the answers of a step: value, label key.
func choiceRow(r *req, step string, labels ...[2]string) []tg.Button {
	row := make([]tg.Button, 0, len(labels))
	for _, l := range labels {
		row = append(row, r.button(l[1], "adm:w:"+step+":"+l[0]))
	}
	return row
}

// refused turns an error of a core call into what a wizard shows: a refused
// value is a reason (core's words, escaped) shown above the step; a missing
// permission is told politely; anything else is a failure shown as such. In
// the last two the wizard is left, and "" is returned.
func (h *Handler) refused(r *req, err error) string {
	st := status.Convert(err)
	switch st.Code() {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.NotFound, codes.AlreadyExists:
		msg := st.Message()
		for _, p := range []string{"domain: invalid argument: ", "domain: not possible in the current state: "} {
			msg = strings.TrimPrefix(msg, p)
		}
		return r.t("admin.refused", "reason", esc(msg))
	case codes.PermissionDenied:
		r.clearState()
		r.send(r.t("admin.forbidden"), (&tg.Keyboard{}).Row(r.button("btn.back", "adm")))
		return ""
	}
	r.clearState()
	r.fail(err)
	return ""
}

// promptText is a step's explanation with the lines that follow it (a range,
// the current value, the default; "" lines are left out).
func promptText(r *req, key string, args []any, lines ...string) string {
	out := []string{r.t(key, args...)}
	var extra []string
	for _, l := range lines {
		if l != "" {
			extra = append(extra, l)
		}
	}
	if len(extra) > 0 {
		out = append(out, "", strings.Join(extra, "\n"))
	}
	return strings.Join(out, "\n")
}

func (r *req) rangeLine(lo, hi int64) string {
	return r.t("admin.wiz_range", "range", i18n.Number(r.lang, lo)+" – "+i18n.Number(r.lang, hi))
}

// nowLine and defaultLine show a current value and a default ("" shows nothing).
func (r *req) nowLine(v string) string {
	if v == "" {
		return ""
	}
	return r.t("admin.wiz_current", "value", v)
}

func (r *req) defaultLine(v string) string {
	if v == "" {
		return ""
	}
	return r.t("admin.wiz_default", "value", v)
}

// parseInt reads a whole number in [lo, hi]; bad is the reason when not.
func parseInt(r *req, in string, lo, hi int64) (n int64, bad string) {
	n, ok := i18n.ParseNumber(in)
	if !ok {
		return 0, r.t("admin.err_number")
	}
	if n < lo || n > hi {
		return 0, r.t("admin.err_range", "range", i18n.Number(r.lang, lo)+" – "+i18n.Number(r.lang, hi))
	}
	return n, ""
}

// intStep asks for a whole number in [lo, hi]. check may refuse it given the
// earlier answers; def is the default shown ("" = none).
func intStep(id, key string, lo, hi int64, def string, check func(r *req, d map[string]string, v int64) string) wizStep {
	return wizStep{
		id: id,
		prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
			text := promptText(r, key, nil, r.rangeLine(lo, hi), r.nowLine(d["now_"+id]), r.defaultLine(def))
			var rows [][]tg.Button
			if def != "" {
				rows = append(rows, choiceRow(r, id, [2]string{def, "btn.adm_default"}))
			}
			return text, rows
		},
		parse: func(r *req, d map[string]string, in string) (string, string) {
			n, bad := parseInt(r, in, lo, hi)
			if bad == "" && check != nil {
				bad = check(r, d, n)
			}
			return strconv.FormatInt(n, 10), bad
		},
	}
}

// nameStep asks for a plan's name; "-" leaves it out (when optional).
func nameStep(id, key string, other string) wizStep {
	return wizStep{
		id: id,
		prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
			return promptText(r, key, nil, r.nowLine(esc(d["now_"+id]))), nil
		},
		parse: func(r *req, d map[string]string, in string) (string, string) {
			if in == "-" {
				in = ""
			}
			if utf8.RuneCountInString(in) > 80 {
				return "", r.t("admin.err_text_long", "max", i18n.Number(r.lang, 80))
			}
			if in == "" && other != "" && d[other] == "" {
				return "", r.t("admin.err_name_both")
			}
			return in, ""
		},
	}
}

// choiceStep asks to pick one of the options (value, button text key).
func choiceStep(id, key string, opts ...[2]string) wizStep {
	return wizStep{
		id: id,
		prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
			return promptText(r, key, nil, r.nowLine(d["now_"+id])), [][]tg.Button{choiceRow(r, id, opts...)}
		},
		parse: func(r *req, d map[string]string, in string) (string, string) {
			for _, o := range opts {
				if strings.EqualFold(in, o[0]) {
					return o[0], ""
				}
			}
			return "", r.t("admin.err_choice")
		},
	}
}

// currencyStep and priceStep ask for a price in IRT or USDT.
var currencyOpts = [][2]string{{"IRT", "btn.adm_cur_irt"}, {"USDT", "btn.adm_cur_usdt"}}

func currencyStep() wizStep { return choiceStep("cur", "wiz.cur", currencyOpts...) }

func priceStep(key string) wizStep {
	return wizStep{
		id: "price",
		prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
			return promptText(r, key, []any{"currency", d["cur"]}, r.nowLine(d["now_price"])), nil
		},
		parse: func(r *req, d map[string]string, in string) (string, string) {
			n, ok := parseMinor(in, d["cur"])
			if !ok || n < 0 {
				return "", r.t("admin.err_price", "currency", d["cur"])
			}
			return strconv.FormatInt(n, 10), ""
		},
	}
}

// minorOf reads a stored answer back.
func minorOf(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

// reasonStep asks for the reason of a staff change (kept in the audit log).
func reasonStep(key string) wizStep {
	return wizStep{
		id: "reason",
		prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
			return promptText(r, key, nil, r.t("admin.wiz_len", "max", i18n.Number(r.lang, 500))), nil
		},
		parse: func(r *req, d map[string]string, in string) (string, string) {
			if utf8.RuneCountInString(in) > 500 {
				return "", r.t("admin.err_text_long", "max", i18n.Number(r.lang, 500))
			}
			return in, ""
		},
	}
}
