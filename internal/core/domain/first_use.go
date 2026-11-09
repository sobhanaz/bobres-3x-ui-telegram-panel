package domain

import (
	"context"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Days from the first connection (docs/03-bot-ux.md, "Days from the first connection"). With the switch for the buyer
// on (customers' or resellers'), a new service's days start when its config first connects instead of when it is
// made: the provisioner makes the panel client with a delayed start (3x-ui: a negative expiry; Marzban: on hold; S-UI:
// a delayed start; Hiddify: no start date), and the panel sets the end at the first connection. Until then the service
// is active with no end date (ExpiresAt nil) and FirstUseDays says how long it will run: it cannot expire and gets no
// reminder about its end. The usage sync reads the end once the panel has set it, and the service runs as any other.
//
// The free trial starts at once, and so does a service staff make (a gift from the dashboard or an order they enter):
// only a purchase waits. A renewal of a service still waiting adds its days to the wait; a traffic package keeps it.
// Services made before the switch was turned on keep their dates. A config nobody connects waits for ever.

// The settings that switch it on (off by default).
const (
	SettingFirstUseCustomers = "first_use.customers"
	SettingFirstUseResellers = "first_use.resellers"
)

// maxFirstUseDays bounds the days a waiting service runs once it connects (the provisioner's limit).
const maxFirstUseDays = 36500

// startsOnFirstUse says whether the new service of job waits for its first connection: the switch for its buyer is on
// (the resellers' one for a reseller), the plan has days and is not the free trial, and staff did not make it.
func (w *ProvisionWorker) startsOnFirstUse(ctx context.Context, job *provisionJob) bool {
	if job.plan.IsTrial || job.order.CreatedBy != nil || job.order.Type != OrderNew {
		return false
	}
	if days, _ := planLimits(job.plan); days <= 0 {
		return false
	}
	key := SettingFirstUseCustomers
	if isReseller(job.user) {
		key = SettingFirstUseResellers
	}
	return w.svc.setting(ctx, key) == "true"
}

// panelExpiry splits an expiry the provisioner reports (unix seconds; 0 none; negative: the seconds a client waiting for
// its first connection runs from then) into core's: an end date, or the days a waiting service will run.
func panelExpiry(expiresAt int64) (*time.Time, *int32) {
	switch {
	case expiresAt > 0:
		t := time.Unix(expiresAt, 0).UTC()
		return &t, nil
	case expiresAt < 0:
		days := min((-expiresAt+86399)/86400, maxFirstUseDays) // whole days, as the panels count them
		d := int32(days)
		return nil, &d
	}
	return nil, nil
}

// waitingExpiry is the expiry the provisioner takes for a service waiting for its first connection: the seconds it runs
// from then, negative.
func waitingExpiry(days int32) int64 { return -int64(days) * 86400 }

// isWaiting reports whether a service waits for its first connection.
func isWaiting(sub *store.Subscription) bool { return sub.FirstUseDays != nil && sub.ExpiresAt == nil }

// deref32 is *p, or 0 for nil.
func deref32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}
