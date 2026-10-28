package handler

import (
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
)

// The reseller's monthly target (docs/03-bot-ux.md, "Reseller panel"): while the owner has targets on, the reseller's panel ends with this month's
// count, each tier with its bonus (ticked once paid), what is left to the next one and when the month ends. Core works it all out
// (ResellerInfo.targets); the bot only writes it down. It is absent while the owner has targets off.

// targetDay reads a date core sends ("2026-10-01") as a time on that day, at noon UTC so that no zone moves it across the day's edge.
func targetDay(s string) (time.Time, bool) {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return d.Add(12 * time.Hour), true
}

// targetsBlock is the target's lines for the reseller's panel ("" when there is none to show).
func (r *req) targetsBlock(t *corev1.ResellerTargets) string {
	if t == nil || len(t.GetTiers()) == 0 {
		return ""
	}
	tiers := make([]string, 0, len(t.GetTiers()))
	for _, tier := range t.GetTiers() {
		mark := "⬜"
		if tier.GetPaid() {
			mark = "✅"
		}
		tiers = append(tiers, r.t("rs.target_tier", "mark", mark, "threshold", i18n.Number(r.lang, tier.GetThreshold()),
			"bonus", r.money(tier.GetBonus(), "IRT")))
	}
	next := r.t("rs.target_done")
	if t.GetRemaining() > 0 {
		for _, tier := range t.GetTiers() {
			if !tier.GetReached() {
				next = r.t("rs.target_next", "remaining", i18n.Number(r.lang, t.GetRemaining()), "bonus", r.money(tier.GetBonus(), "IRT"))
				break
			}
		}
	}
	period := esc(t.GetPeriod())
	from, ok1 := targetDay(t.GetFrom())
	to, ok2 := targetDay(t.GetTo())
	if ok1 && ok2 {
		period = r.t("rs.target_range", "from", i18n.Date(r.lang, from), "to", i18n.Date(r.lang, to))
	}
	out := r.t("rs.targets", "range", period, "sold", i18n.Number(r.lang, t.GetSold()), "tiers", strings.Join(tiers, "\n"), "next", next)
	if secs := t.GetEndsAt() - time.Now().Unix(); t.GetEndsAt() > 0 && secs > 0 {
		out += "\n" + r.t("rs.target_ends", "days", i18n.Number(r.lang, (secs+86399)/86400))
	}
	return out
}
