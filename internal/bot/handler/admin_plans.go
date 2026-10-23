package handler

import (
	"strconv"
	"strings"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Plans, traffic packages and the free trial: lists, a card with the edits
// of one plan, and the wizards that create and change them.

// planFields are the fields of a plan a button edits.
var planFields = []string{"name_en", "name_fa", "price", "days", "gb"}

// showAdminPlans lists the plans (the free trial included); a tap opens one.
func (h *Handler) showAdminPlans(r *req) {
	ps, err := h.plans(r, true)
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	for _, p := range ps {
		if p.GetIsTopup() {
			continue
		}
		kb.Row(h.planButton(r, p))
	}
	kb.Row(r.button("btn.adm_new_plan", "adm:pn"), r.button("btn.adm_trial", "adm:trial"))
	kb.Row(r.button("btn.adm_topups", "adm:tup"))
	kb.Row(r.button("btn.back", "adm"))
	r.show(r.t("admin.plans_title"), kb)
}

func (h *Handler) planButton(r *req, p *corev1.Plan) tg.Button {
	mark := r.t("admin.plan_off")
	if p.GetEnabled() {
		mark = r.t("admin.plan_on")
	}
	name := planName(r, p)
	if p.GetIsTrial() {
		name = "🎁 " + name
	}
	return tg.CB(mark+" "+name+" · "+r.money(p.GetPrice().GetAmount(), p.GetPrice().GetCurrency()), "adm:pl:"+p.GetId())
}

// showAdminTopups lists the traffic packages.
func (h *Handler) showAdminTopups(r *req) {
	ps, err := h.plans(r, true)
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	for _, p := range ps {
		if p.GetIsTopup() {
			kb.Row(h.planButton(r, p))
		}
	}
	kb.Row(r.button("btn.adm_new_topup", "adm:tn"))
	kb.Row(r.button("btn.back", "adm:plans"))
	r.show(r.t("admin.topups_title"), kb)
}

// showPlanCard shows one plan with the buttons that change it.
func (h *Handler) showPlanCard(r *req, id string) {
	p, err := h.findPlan(r, id, true)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			r.toast = r.t("error.expired")
			h.showAdminPlans(r)
			return
		}
		r.fail(err)
		return
	}
	dash := func(s string) string {
		if s == "" {
			return "—"
		}
		return esc(s)
	}
	state := r.t("admin.state_off")
	toggle := r.button("btn.adm_enable", "adm:pt:"+id)
	if p.GetEnabled() {
		state = r.t("admin.state_on")
		toggle = r.button("btn.adm_disable", "adm:pt:"+id)
	}
	kind := ""
	switch {
	case p.GetIsTrial():
		kind = r.t("admin.kind_trial")
	case p.GetIsTopup():
		kind = r.t("admin.kind_topup")
	}
	text := r.t("admin.plan_card", "name", esc(planName(r, p)), "en", dash(p.GetNameI18N()["en"]), "fa", dash(p.GetNameI18N()["fa"]),
		"price", r.money(p.GetPrice().GetAmount(), p.GetPrice().GetCurrency()), "limits", planLimits(r, p), "state", state, "kind", kind)
	kb := (&tg.Keyboard{}).
		Row(r.button("btn.adm_edit_name_en", "adm:pe:name_en:"+id), r.button("btn.adm_edit_name_fa", "adm:pe:name_fa:"+id))
	var row []tg.Button
	if !p.GetIsTrial() {
		row = append(row, r.button("btn.adm_edit_price", "adm:pe:price:"+id))
	}
	if !p.GetIsTopup() {
		row = append(row, r.button("btn.adm_edit_days", "adm:pe:days:"+id))
	}
	row = append(row, r.button("btn.adm_edit_gb", "adm:pe:gb:"+id))
	kb.Row(row...).Row(toggle)
	back := "adm:plans"
	if p.GetIsTopup() {
		back = "adm:tup"
	}
	kb.Row(r.button("btn.back", back))
	h.addUndo(r, kb)
	r.show(text, kb)
}

func (h *Handler) togglePlan(r *req, id string) {
	p, err := h.findPlan(r, id, true)
	if err != nil {
		r.fail(err)
		return
	}
	was := p.GetEnabled()
	p.Enabled = !was
	if _, err := h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: p}); err != nil {
		r.fail(err)
		return
	}
	h.rememberUndo(r, "plan", id, strconv.FormatBool(was), planName(r, p))
	h.showPlanCard(r, id)
}

// planSummary lists a new plan's answers for the confirmation.
func planSummary(r *req, d map[string]string, topup bool) string {
	dash := func(s string) string {
		if s == "" {
			return "—"
		}
		return esc(s)
	}
	days := r.t("admin.unlimited")
	if n := minorOf(d["days"]); n > 0 {
		days = i18n.Number(r.lang, n)
	}
	gb := r.t("admin.unlimited")
	if n := minorOf(d["gb"]); n > 0 {
		gb = i18n.Number(r.lang, n) + " GB"
	}
	key := "admin.confirm_plan"
	if topup {
		key = "admin.confirm_topup"
	}
	return r.t(key, "en", dash(d["name_en"]), "fa", dash(d["name_fa"]),
		"price", r.money(minorOf(d["price"]), d["cur"]), "days", days, "gb", gb)
}

func planFromAnswers(d map[string]string, topup bool) *corev1.Plan {
	names := map[string]string{}
	if d["name_en"] != "" {
		names["en"] = d["name_en"]
	}
	if d["name_fa"] != "" {
		names["fa"] = d["name_fa"]
	}
	days, gb := minorOf(d["days"]), minorOf(d["gb"])
	p := &corev1.Plan{
		NameI18N: names, Enabled: true,
		Price: &commonv1.Money{Amount: minorOf(d["price"]), Currency: d["cur"]},
	}
	if topup {
		p.IsTopup, p.Kind = true, "traffic"
		p.TrafficBytes = gb << 30
		return p
	}
	p.Kind, p.DurationDays, p.TrafficBytes = planKind(days, gb), int32(days), gb<<30 //nolint:gosec // bounded by the steps
	return p
}

// saveNewPlan is the save of the plan and package wizards.
func (h *Handler) saveNewPlan(r *req, d map[string]string, topup bool) string {
	saved, err := h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: planFromAnswers(d, topup)})
	if err != nil {
		return h.refused(r, err)
	}
	back := "adm:plans"
	if topup {
		back = "adm:tup"
	}
	r.show(r.t("admin.plan_saved", "name", esc(planName(r, saved))),
		(&tg.Keyboard{}).Row(r.button("btn.adm_open_plan", "adm:pl:"+saved.GetId())).Row(r.button("btn.back", back)))
	return ""
}

func bothZero(r *req, d map[string]string, gb int64) string {
	if gb == 0 && minorOf(d["days"]) == 0 {
		return r.t("admin.err_limits")
	}
	return ""
}

func planWizard() *wizard {
	return &wizard{
		steps: []wizStep{
			nameStep("name_en", "wiz.plan.name_en", ""),
			nameStep("name_fa", "wiz.plan.name_fa", "name_en"),
			currencyStep(),
			priceStep("wiz.price"),
			intStep("days", "wiz.days", 0, 3650, "", nil),
			intStep("gb", "wiz.gb", 0, 100000, "", bothZero),
		},
		confirm: func(r *req, d map[string]string) (string, string) { return planSummary(r, d, false), "" },
		save:    func(r *req, d map[string]string) string { return (r.h).saveNewPlan(r, d, false) },
		home:    func(map[string]string) string { return "plans" },
	}
}

func topupWizard() *wizard {
	return &wizard{
		steps: []wizStep{
			nameStep("name_en", "wiz.topup.name_en", ""),
			nameStep("name_fa", "wiz.topup.name_fa", "name_en"),
			currencyStep(),
			priceStep("wiz.price"),
			intStep("gb", "wiz.topup.gb", 1, 100000, "", nil),
		},
		confirm: func(r *req, d map[string]string) (string, string) { return planSummary(r, d, true), "" },
		save:    func(r *req, d map[string]string) string { return (r.h).saveNewPlan(r, d, true) },
		home:    func(map[string]string) string { return "tup" },
	}
}

// startPlanEdit asks for the new value of one field of a plan.
func (h *Handler) startPlanEdit(r *req, field, id string) {
	if !slicesHas(planFields, field) {
		h.showAdminPlans(r)
		return
	}
	p, err := h.findPlan(r, id, true)
	if err != nil {
		r.fail(err)
		return
	}
	now := ""
	switch field {
	case "name_en", "name_fa":
		now = p.GetNameI18N()[strings.TrimPrefix(field, "name_")]
	case "price":
		now = strconv.FormatInt(p.GetPrice().GetAmount(), 10)
	case "days":
		now = strconv.Itoa(int(p.GetDurationDays()))
	case "gb":
		now = strconv.FormatInt(p.GetTrafficBytes()>>30, 10)
	}
	shown := now
	switch field {
	case "price":
		shown = r.money(p.GetPrice().GetAmount(), p.GetPrice().GetCurrency())
	case "days":
		shown = i18n.Number(r.lang, int64(p.GetDurationDays()))
		if p.GetDurationDays() == 0 {
			shown = r.t("admin.unlimited")
		}
	case "gb":
		shown = i18n.Bytes(r.lang, p.GetTrafficBytes())
		if p.GetTrafficBytes() == 0 {
			shown = r.t("admin.unlimited")
		}
	case "name_en", "name_fa":
		if now == "" {
			shown = "—"
		}
	}
	h.startWizard(r, "pe", map[string]string{
		"id": id, "field": field, "cur": p.GetPrice().GetCurrency(), "now_v": shown,
		"other": p.GetNameI18N()[map[string]string{"name_en": "fa", "name_fa": "en"}[field]],
	})
}

func slicesHas(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// planEditWizard changes one field (d["field"]) of the plan d["id"].
func planEditWizard() *wizard {
	return &wizard{
		steps: []wizStep{{
			id: "v",
			prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
				field := d["field"]
				var lines []string
				switch field {
				case "days":
					lines = append(lines, r.rangeLine(0, 3650))
				case "gb":
					lines = append(lines, r.rangeLine(0, 100000))
				}
				lines = append(lines, r.nowLine(esc(d["now_v"])))
				return promptText(r, "wiz.pe."+field, []any{"currency", d["cur"]}, lines...), nil
			},
			parse: func(r *req, d map[string]string, in string) (string, string) {
				switch d["field"] {
				case "name_en", "name_fa":
					if in == "-" {
						in = ""
					}
					if in == "" && d["other"] == "" {
						return "", r.t("admin.err_name_both")
					}
					if len([]rune(in)) > 80 {
						return "", r.t("admin.err_text_long", "max", i18n.Number(r.lang, 80))
					}
					return in, ""
				case "price":
					n, ok := parseMinor(in, d["cur"])
					if !ok {
						return "", r.t("admin.err_price", "currency", d["cur"])
					}
					return strconv.FormatInt(n, 10), ""
				case "days":
					n, bad := parseInt(r, in, 0, 3650)
					return strconv.FormatInt(n, 10), bad
				default:
					n, bad := parseInt(r, in, 0, 100000)
					return strconv.FormatInt(n, 10), bad
				}
			},
		}},
		save: func(r *req, d map[string]string) string {
			h := r.h
			p, err := h.findPlan(r, d["id"], true)
			if err != nil {
				return h.refused(r, err)
			}
			v := d["v"]
			switch d["field"] {
			case "name_en", "name_fa":
				names := map[string]string{}
				for k, n := range p.GetNameI18N() {
					names[k] = n
				}
				lang := strings.TrimPrefix(d["field"], "name_")
				if v == "" {
					delete(names, lang)
				} else {
					names[lang] = v
				}
				p.NameI18N = names
			case "price":
				p.Price = &commonv1.Money{Amount: minorOf(v), Currency: p.GetPrice().GetCurrency()}
			case "days":
				p.DurationDays = int32(minorOf(v)) //nolint:gosec // 0..3650
				if !p.GetIsTopup() {
					p.Kind = planKind(minorOf(v), p.GetTrafficBytes())
				}
			case "gb":
				p.TrafficBytes = minorOf(v) << 30
				if !p.GetIsTopup() {
					p.Kind = planKind(int64(p.GetDurationDays()), p.GetTrafficBytes())
				}
			}
			if _, err := h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: p}); err != nil {
				return h.refused(r, err)
			}
			r.toast = r.t("admin.saved")
			h.showPlanCard(r, d["id"])
			return ""
		},
		home: func(d map[string]string) string { return "pl:" + d["id"] },
	}
}

// trialPlan is the free trial plan, nil when none was set up.
func (h *Handler) trialPlan(r *req) (*corev1.Plan, error) {
	ps, err := h.plans(r, true)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.GetIsTrial() {
			return p, nil
		}
	}
	return nil, nil
}

// showTrial shows the free trial and the buttons that set it up or stop it.
func (h *Handler) showTrial(r *req) {
	t, err := h.trialPlan(r)
	if err != nil {
		r.fail(err)
		return
	}
	state := r.t("admin.trial_none")
	kb := &tg.Keyboard{}
	if t != nil {
		if t.GetEnabled() {
			state = r.t("admin.trial_on", "limits", planLimits(r, t))
			kb.Row(r.button("btn.adm_trial_off", "adm:toff"))
		} else {
			state = r.t("admin.trial_stopped", "limits", planLimits(r, t))
		}
	}
	kb.Row(r.button("btn.adm_trial_set", "adm:tw"))
	kb.Row(r.button("btn.back", "adm:plans"))
	r.show(r.t("admin.trial_title", "state", state), kb)
}

func (h *Handler) trialOff(r *req) error {
	t, err := h.trialPlan(r)
	if err != nil || t == nil || !t.GetEnabled() {
		return err
	}
	t.Enabled = false
	_, err = h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: t})
	return err
}

// applyTrial sets up the free trial (a free plan, turned on).
func (h *Handler) applyTrial(r *req, days, gb int64) (*corev1.Plan, error) {
	trial, err := h.trialPlan(r)
	if err != nil {
		return nil, err
	}
	if trial == nil {
		trial = &corev1.Plan{NameI18N: map[string]string{"en": "Free trial", "fa": "تست رایگان"}, IsTrial: true}
	}
	trial.DurationDays, trial.TrafficBytes = int32(days), gb<<30 //nolint:gosec // bounded by the callers
	trial.Kind, trial.Enabled = planKind(days, gb), true
	trial.Price = &commonv1.Money{Amount: 0, Currency: "IRT"}
	return h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: trial})
}

func trialWizard() *wizard {
	return &wizard{
		steps: []wizStep{
			intStep("days", "wiz.trial.days", 0, 365, "", nil),
			intStep("gb", "wiz.trial.gb", 0, 1000, "", bothZero),
		},
		confirm: func(r *req, d map[string]string) (string, string) {
			days, gb := r.t("admin.unlimited"), r.t("admin.unlimited")
			if n := minorOf(d["days"]); n > 0 {
				days = i18n.Number(r.lang, n)
			}
			if n := minorOf(d["gb"]); n > 0 {
				gb = i18n.Number(r.lang, n) + " GB"
			}
			return r.t("admin.confirm_trial", "days", days, "gb", gb), ""
		},
		save: func(r *req, d map[string]string) string {
			saved, err := r.h.applyTrial(r, minorOf(d["days"]), minorOf(d["gb"]))
			if err != nil {
				return r.h.refused(r, err)
			}
			r.show(r.t("admin.plan_saved", "name", esc(planName(r, saved))), (&tg.Keyboard{}).Row(r.button("btn.back", "adm:trial")))
			return ""
		},
		home: func(map[string]string) string { return "trial" },
	}
}

// onTrialCommand: "/trial <days> <GB>" sets up the free trial plan, "/trial
// off" disables it.
func (h *Handler) onTrialCommand(r *req, args string) {
	if strings.EqualFold(strings.TrimSpace(args), "off") {
		if err := h.trialOff(r); err != nil {
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
	saved, err := h.applyTrial(r, days, gb)
	if err != nil {
		h.failPlanSave(r, err)
		return
	}
	r.send(r.t("admin.plan_saved", "name", esc(planName(r, saved))), (&tg.Keyboard{}).Row(r.button("btn.adm_plans", "adm:plans")))
}

func planKind(days, traffic int64) string {
	switch {
	case days > 0 && traffic > 0:
		return "both"
	case days > 0:
		return "time"
	default:
		return "traffic"
	}
}

// failPlanSave answers a refused plan of a command with core's reason.
func (h *Handler) failPlanSave(r *req, err error) {
	if status.Code(err) == codes.InvalidArgument {
		r.send(esc(status.Convert(err).Message())+"\n"+r.t("admin.plan_usage"), nil)
		return
	}
	r.fail(err)
}

func (h *Handler) savePlan(r *req, p *corev1.Plan) {
	saved, err := h.core.AdminUpsertPlan(r.ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: h.actor(r), Plan: p})
	if err != nil {
		h.failPlanSave(r, err)
		return
	}
	r.send(r.t("admin.plan_saved", "name", esc(planName(r, saved))), (&tg.Keyboard{}).Row(r.button("btn.adm_plans", "adm:plans")))
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
