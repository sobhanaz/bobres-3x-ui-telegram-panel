package handler

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The settings editor: it is driven by core's list of settings (their kind,
// range, options, default and current value, only those the admin may
// change), so a new setting needs no change here. Groups lead to keys; a
// switch flips with one tap, a choice is one button per option, and any
// other value is typed after an explained prompt.

// settingSpecs are the settings the admin may change, with their values.
func (h *Handler) settingSpecs(r *req) ([]*corev1.AdminSettingSpec, error) {
	resp, err := h.core.AdminListSettingSpecs(r.ctx, &corev1.AdminListSettingSpecsRequest{ActorTelegramId: h.actor(r)})
	return resp.GetSpecs(), err
}

func (h *Handler) settingSpec(r *req, key string) (*corev1.AdminSettingSpec, error) {
	specs, err := h.settingSpecs(r)
	if err != nil {
		return nil, err
	}
	for _, sp := range specs {
		if sp.GetKey() == key {
			return sp, nil
		}
	}
	return nil, status.Error(codes.NotFound, "setting not found")
}

// catText is a text of the catalog when there is one, else fallback.
func (r *req) catText(key, fallback string, args ...any) string {
	if _, ok := r.h.cat.Info(key); ok {
		return r.t(key, args...)
	}
	return fallback
}

// settingLabel is the plain name of a setting (a button label).
func (r *req) settingLabel(key string) string {
	return r.catText("setting."+key+".label", key)
}

// settingHelp explains what a setting does: the bot's own text, else core's.
func (r *req) settingHelp(sp *corev1.AdminSettingSpec) string {
	return r.catText("setting."+sp.GetKey()+".help", esc(sp.GetHelp()))
}

func (r *req) groupTitle(g string) string { return r.catText("setting.group."+g, g) }

// boolOn is whether a switch is on now (its default when it was never set).
func boolOn(sp *corev1.AdminSettingSpec) bool {
	v := sp.GetValue()
	if v == "" {
		v = sp.GetDefaultValue()
	}
	return v == "true"
}

// specValue shows a setting's current value.
func (r *req) specValue(sp *corev1.AdminSettingSpec) string {
	v := sp.GetValue()
	switch sp.GetKind() {
	case "bool":
		if boolOn(sp) {
			return r.t("admin.state_on")
		}
		return r.t("admin.state_off")
	case "enum":
		if v == "" {
			v = sp.GetDefaultValue()
		}
		return esc(r.catText("setting."+sp.GetKey()+".opt."+v, v))
	}
	if v == "" {
		return r.t("admin.setting_unset")
	}
	return "<code>" + esc(v) + "</code>"
}

// showSettings lists the groups.
func (h *Handler) showSettings(r *req) {
	specs, err := h.settingSpecs(r)
	if err != nil {
		r.fail(err)
		return
	}
	var groups []string
	for _, sp := range specs {
		if !slices.Contains(groups, sp.GetGroup()) {
			groups = append(groups, sp.GetGroup())
		}
	}
	kb := &tg.Keyboard{}
	var row []tg.Button
	for _, g := range groups {
		row = append(row, tg.CB(r.groupTitle(g), "adm:sg:"+g))
		if len(row) == 2 {
			kb.Row(row...)
			row = nil
		}
	}
	kb.Row(row...)
	kb.Row(r.button("btn.back", "adm"))
	r.show(r.t("admin.settings_title"), kb)
}

// showGroup lists the settings of a group: a switch shows its state and
// flips with a tap; anything else shows its value and opens on a tap.
func (h *Handler) showGroup(r *req, group string) {
	specs, err := h.settingSpecs(r)
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	var lines []string
	for _, sp := range specs {
		if sp.GetGroup() != group {
			continue
		}
		label := r.settingLabel(sp.GetKey())
		if sp.GetKind() == "bool" {
			mark := r.t("admin.plan_off")
			if boolOn(sp) {
				mark = r.t("admin.plan_on")
			}
			kb.Row(tg.CB(mark+" "+label, "adm:st:"+sp.GetKey()))
			continue
		}
		lines = append(lines, "<b>"+label+"</b>: "+r.specValue(sp))
		kb.Row(tg.CB("✏️ "+label, "adm:sk:"+sp.GetKey()))
	}
	kb.Row(r.button("btn.back", "adm:set"), r.button("btn.adm_menu", "adm"))
	text := r.t("admin.group_title", "group", r.groupTitle(group))
	switch {
	case len(kb.InlineKeyboard) == 1:
		text += "\n\n" + r.t("admin.group_empty")
	case len(lines) > 0:
		text += "\n\n" + strings.Join(lines, "\n")
	}
	r.show(text, kb)
}

// showKey explains one setting and offers what can be done with it.
func (h *Handler) showKey(r *req, key string) {
	sp, err := h.settingSpec(r, key)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			r.toast = r.t("admin.forbidden")
			h.showSettings(r)
			return
		}
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	switch sp.GetKind() {
	case "bool":
		kb.Row(r.button("btn.adm_enable", "adm:sv:"+key+":true"), r.button("btn.adm_disable", "adm:sv:"+key+":false"))
	case "enum":
		for _, o := range sp.GetOptions() {
			kb.Row(tg.CB(r.catText("setting."+key+".opt."+o, o), "adm:sv:"+key+":"+o))
		}
	default:
		kb.Row(r.button("btn.adm_change", "adm:se:"+key))
	}
	if sp.GetValue() != "" {
		kb.Row(r.button("btn.adm_reset", "adm:sr:"+key))
	}
	kb.Row(r.button("btn.back", "adm:sg:"+sp.GetGroup()))
	def := ""
	if sp.GetDefaultValue() != "" {
		def = r.defaultLine(r.specDefault(sp))
	}
	r.show(r.t("admin.key_view", "label", r.settingLabel(key), "help", r.settingHelp(sp),
		"value", r.specValue(sp), "default", def), kb)
}

func (r *req) specDefault(sp *corev1.AdminSettingSpec) string {
	d := sp.GetDefaultValue()
	switch sp.GetKind() {
	case "bool":
		if d == "true" {
			return r.t("admin.state_on")
		}
		return r.t("admin.state_off")
	case "enum":
		return esc(r.catText("setting."+sp.GetKey()+".opt."+d, d))
	}
	return "<code>" + esc(d) + "</code>"
}

// setSetting saves a value ("" removes the setting); bad is core's reason
// when it refuses it.
func (h *Handler) setSetting(r *req, key, value string) (bad string) {
	if _, err := h.core.AdminSetSetting(r.ctx, &corev1.AdminSetSettingRequest{ActorTelegramId: h.actor(r), Key: key, Value: value}); err != nil {
		return h.refused(r, err)
	}
	_ = h.RefreshSettings(r.ctx)
	return ""
}

// saveSetting saves a value from a button and shows the group again.
func (h *Handler) saveSetting(r *req, key, value string) {
	sp, err := h.settingSpec(r, key)
	if err != nil {
		r.fail(err)
		return
	}
	if bad := h.setSetting(r, key, value); bad != "" {
		r.toast, r.alert = r.t("admin.refused_short"), true
		h.showKey(r, key)
		return
	}
	r.toast = r.t("admin.saved")
	h.showGroup(r, sp.GetGroup())
}

// toggleSetting flips a switch.
func (h *Handler) toggleSetting(r *req, key string) {
	sp, err := h.settingSpec(r, key)
	if err != nil {
		r.fail(err)
		return
	}
	if sp.GetKind() != "bool" {
		h.showKey(r, key)
		return
	}
	h.saveSetting(r, key, strconv.FormatBool(!boolOn(sp)))
}

// startSettingEdit asks for a new value of a setting.
func (h *Handler) startSettingEdit(r *req, key string) {
	sp, err := h.settingSpec(r, key)
	if err != nil {
		r.fail(err)
		return
	}
	h.startWizard(r, "set", map[string]string{"key": key, "group": sp.GetGroup()})
}

// settingWizard types a value for the setting d["key"].
func settingWizard() *wizard {
	return &wizard{
		steps: []wizStep{{
			id: "v",
			prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
				sp, err := r.h.settingSpec(r, d["key"])
				if err != nil {
					return r.t("error.generic"), nil
				}
				lines := []string{r.t("admin.wiz_kind."+sp.GetKind(), "max", sp.GetMaxLen())}
				if sp.GetKind() == "int" {
					lines = append(lines, r.rangeLine(sp.GetMin(), sp.GetMax()))
				}
				lines = append(lines, r.nowLine(r.specValue(sp)))
				if sp.GetDefaultValue() != "" {
					lines = append(lines, r.defaultLine(r.specDefault(sp)))
				}
				lines = append(lines, r.t("admin.wiz_clear"))
				text := r.t("admin.wiz_setting", "label", r.settingLabel(sp.GetKey()), "help", r.settingHelp(sp)) +
					"\n\n" + strings.Join(lines, "\n")
				return text, nil
			},
			parse: func(r *req, d map[string]string, in string) (string, string) {
				if in == "-" {
					return "", "" // removes the setting
				}
				sp, err := r.h.settingSpec(r, d["key"])
				if err == nil {
					if sp.GetKind() == "int" {
						n, bad := parseInt(r, in, sp.GetMin(), sp.GetMax())
						return strconv.FormatInt(n, 10), bad
					}
					if sp.GetMaxLen() > 0 && utf8.RuneCountInString(in) > int(sp.GetMaxLen()) {
						return "", r.t("admin.err_text_long", "max", int(sp.GetMaxLen()))
					}
				}
				return in, ""
			},
		}},
		save: func(r *req, d map[string]string) string {
			if bad := r.h.setSetting(r, d["key"], d["v"]); bad != "" {
				return bad
			}
			r.toast = r.t("admin.saved")
			r.h.showKey(r, d["key"])
			return ""
		},
		home: func(d map[string]string) string { return "sk:" + d["key"] },
	}
}

// onSetCommand: "/set <key> <value>" takes every setting core knows (core
// checks the key and the value).
func (h *Handler) onSetCommand(r *req, args string) {
	key, value, _ := strings.Cut(args, " ")
	if key == "" {
		h.showSettings(r) // no arguments: the buttons
		return
	}
	if _, err := h.core.AdminSetSetting(r.ctx, &corev1.AdminSetSettingRequest{
		ActorTelegramId: h.actor(r), Key: key, Value: strings.TrimSpace(value),
	}); err != nil {
		if status.Code(err) == codes.InvalidArgument {
			// Core's reason (a bad value, an unknown key, owner only); the
			// key list helps with a mistyped key.
			reason := status.Convert(err).Message()
			reason = strings.TrimPrefix(reason, "domain: invalid argument: ") // core's error kind
			msg := r.t("admin.setting_usage") + "\n" + esc(reason)
			if !slices.Contains(settingKeys, key) && !strings.HasPrefix(key, "texts.") {
				msg += "\n\n" + strings.Join(settingKeys, "\n")
			}
			r.send(msg, nil)
			return
		}
		r.fail(err)
		return
	}
	_ = h.RefreshSettings(r.ctx)
	r.send(r.t("admin.setting_saved", "key", esc(key)), nil)
}
