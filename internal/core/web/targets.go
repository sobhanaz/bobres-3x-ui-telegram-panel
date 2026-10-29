package web

import (
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// targetsJSON is a reseller's month against the owner's monthly targets, as the Mini App's reseller panel and the dashboard's reseller page show it:
// the month (its label in the owner's calendar, the Gregorian dates it covers in the store's zone, and when it ends), the count, the tiers with their
// bonus and whether each is reached and paid, and what is left to the next one. Amounts are Toman.
func targetsJSON(t *domain.TargetProgress) map[string]any {
	tiers := make([]map[string]any, 0, len(t.Tiers))
	for _, v := range t.Tiers {
		tiers = append(tiers, map[string]any{"threshold": v.Threshold, "bonus": v.Bonus, "reached": v.Reached, "paid": v.Paid})
	}
	return map[string]any{
		"period": t.Period, "calendar": t.Calendar, "from": t.From, "to": t.To, "ends_at": t.EndsAt.Unix(), "sold": t.Sold, "tiers": tiers,
		"remaining": t.Remaining, "bonus_paid": t.BonusPaid, "currency": domain.WalletCurrency, "scale": bmoney.Scale(domain.WalletCurrency),
	}
}
