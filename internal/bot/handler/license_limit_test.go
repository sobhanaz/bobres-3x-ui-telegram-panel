package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// fillFreeTier ends the install's trial and makes 25 delivered services
// (the free tier's limit), so a new purchase is refused.
func fillFreeTier(t *testing.T, w *world) {
	t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	// The test stack empties every table, the install's row too: it is made
	// again (with a new trial) the first time it is read.
	if _, err := st.InstallID(ctx, st.Conn()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	plan := &store.Plan{NameI18n: map[string]string{"en": "Seed"}, Kind: "time", Price: 1000, Currency: "IRT", Enabled: true}
	if err := st.UpsertPlan(ctx, st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(10 * 24 * time.Hour)
	for i := range 25 {
		u, err := st.UpsertUser(ctx, st.Conn(), int64(80000+i), "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		o, _, err := st.CreateOrder(ctx, st.Conn(), &store.Order{UserID: u.ID, PlanID: plan.ID, Type: "new", Status: "active",
			Amount: 1000, Currency: "IRT", IdempotencyKey: fmt.Sprintf("fill-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := st.CreateSubscription(ctx, st.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: fmt.Sprintf("fill%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ActivateSubscription(ctx, st.Conn(), sub.ID, "00000000-0000-7000-8000-000000000001", "x", "", &expires, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// A customer whose new purchase is refused for the active-services limit
// (a plan, or the free trial) gets the "temporarily unavailable, contact
// support" text in their language, and never a word about licensing.
func TestAPurchaseAtTheServiceLimitSaysOnlyThatBuyingIsUnavailable(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	fillFreeTier(t, w)
	for _, c := range []struct {
		id         int64
		name, lang string
		want       string
	}{
		{601, "ivy", "en", "New purchases are temporarily unavailable. Please contact support."},
		{602, "dara", "fa", "خرید جدید موقتاً در دسترس نیست. لطفاً با پشتیبانی تماس بگیرید."},
	} {
		u := w.person(c.id, c.name)
		u.text("/start")
		u.press("lang:" + c.lang)
		u.press("buy")
		u.press("plan:")
		u.press("pay:c:")
		u.toast(c.want)
		u.press("home")
		u.press("trial")
		u.toast(c.want)
		// Nothing the customer saw or was told mentions a license.
		var seen []string
		for _, m := range w.tg.all(c.id) {
			seen = append(seen, m.text)
			seen = append(seen, m.labels()...)
		}
		w.tg.mu.Lock()
		seen = append(seen, w.tg.toasts[c.id]...)
		w.tg.mu.Unlock()
		for _, s := range seen {
			low := strings.ToLower(s)
			for _, word := range []string{"licen", "free tier", "مجوز", "پروانه", "لایسنس", "سطح رایگان", "بسته رایگان"} {
				if strings.Contains(low, word) {
					t.Errorf("%s was shown %q (%q)", c.name, s, word)
				}
			}
		}
	}
}
