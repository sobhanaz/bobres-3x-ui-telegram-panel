package handler

import (
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
)

// Plan groups: staff put plans into named groups with a text. A customer who buys picks a group, reads its text, then
// picks one of its plans ("pg:<group id>"). The plans with no group are listed under "Other plans" ("pg:-"). A store
// with no group at all lists its plans flat, as before.
const (
	// planGroupOther is the callback data of the plans with no group.
	planGroupOther = "-"
	// groupButtonWidth is how many characters of a group's name a button shows (one button a row, so more than a
	// table cell; a button label is limited to 64 characters with the emoji).
	groupButtonWidth = 40
)

// planSections are the plans split by group: only the groups that have a plan on sale, in the order core gave them;
// of holds each group's plans (in the plans' order) and other the plans no group lists, a plan of a group core did not
// send included.
type planSections struct {
	groups []*corev1.PlanGroup
	of     map[string][]*corev1.Plan
	other  []*corev1.Plan
}

func sectionPlans(plans []*corev1.Plan, groups []*corev1.PlanGroup) planSections {
	known := make(map[string]bool, len(groups))
	for _, g := range groups {
		known[g.GetId()] = true
	}
	sec := planSections{of: map[string][]*corev1.Plan{}}
	for _, p := range plans {
		if g := p.GetGroupId(); g != "" && known[g] {
			sec.of[g] = append(sec.of[g], p)
		} else {
			sec.other = append(sec.other, p)
		}
	}
	for _, g := range groups {
		if len(sec.of[g.GetId()]) > 0 {
			sec.groups = append(sec.groups, g)
		}
	}
	return sec
}

// groupName is the group's name in the customer's language, else any language it has.
func groupName(r *req, g *corev1.PlanGroup) string {
	if n := localized(r, g.GetNameI18N()); n != "" {
		return n
	}
	return "Plans"
}

// groupText is the group's text in the customer's language, else any language it has ("" when it has none).
func groupText(r *req, g *corev1.PlanGroup) string {
	return localized(r, g.GetDescriptionI18N())
}

func localized(r *req, texts map[string]string) string {
	if v := strings.TrimSpace(texts[r.lang]); v != "" {
		return v
	}
	for _, l := range i18n.ContentOrder(r.lang) {
		if v := strings.TrimSpace(texts[l]); v != "" {
			return v
		}
	}
	return ""
}
