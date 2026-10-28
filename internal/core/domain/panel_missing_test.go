package domain

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// A service whose client the panel no longer has (an operator deleted it there) is marked once, with the
// time it was first seen; it is asked about every MissingEvery and not every Every; no reminder is sent for
// it, and the one that was due is not marked as sent, so it goes out when the client is back; and the mark
// goes when the panel has the client again.
func TestUsageWorkerMarksAServiceTheClientOfWhichIsGone(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 100<<30)
	u, sub, fp, _ := delivered(t, svc, s, 4601, plan)
	uw := svc.NewUsageWorker(fp, nil)
	uw.Every = -time.Hour // a service the panel has is due on every call...
	if uw.MissingEvery != 6*time.Hour {
		t.Fatalf("a service the panel lacks is asked about every %s, want 6h", uw.MissingEvery)
	}
	// ...and one it lacks only after MissingEvery.
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET expires_at = now() + interval '2 days' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}

	fp.setUsage(sub.ID, Usage{Missing: true})
	if n, err := uw.SyncDue(ctx); n != 1 || err != nil {
		t.Fatalf("first sync: %d %v", n, err)
	}
	got, _ := s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.PanelMissingSince == nil || time.Since(*got.PanelMissingSince) > time.Minute {
		t.Fatalf("not marked: %+v", got)
	}
	if got.Status != "expiring_soon" || got.NotifiedExpiringAt != nil || len(outboxEvents(t, s, events.SubscriptionReminder)) != 0 {
		t.Fatalf("a reminder was sent for (or marked on) a service the panel lacks: %+v", got)
	}
	if n, err := s.CountPanelMissing(ctx, s.Conn()); n != 1 || err != nil {
		t.Fatalf("count: %d %v", n, err)
	}

	// Asked about again only after MissingEvery; the time it was first seen stays.
	first := *got.PanelMissingSince
	if n, err := uw.SyncDue(ctx); n != 0 || err != nil {
		t.Fatalf("a service the panel lacks was asked about again at once: %d %v", n, err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET last_synced_at = now() - interval '7 hours' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := uw.SyncDue(ctx); n != 1 || err != nil {
		t.Fatalf("a service the panel lacks was not asked about after 7 hours: %d %v", n, err)
	}
	if got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID); got.PanelMissingSince == nil || !got.PanelMissingSince.Equal(first) {
		t.Fatalf("the first sighting moved: %v then %v", first, got.PanelMissingSince)
	}

	// A panel that does not answer says nothing about the client: the mark stays.
	fp.mu.Lock()
	delete(fp.usage, sub.ID)
	fp.mu.Unlock()
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET last_synced_at = now() - interval '7 hours' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := uw.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID); got.PanelMissingSince == nil {
		t.Fatal("a panel that did not answer cleared the mark")
	}

	// The client is back: the mark goes and the reminder that was held back goes out.
	exp := time.Now().Add(2 * 24 * time.Hour).Unix()
	fp.setUsage(sub.ID, Usage{UsedBytes: 1 << 30, TotalBytes: 100 << 30, ExpiresAt: exp, Enabled: true})
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET last_synced_at = now() - interval '7 hours' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := uw.SyncDue(ctx); n != 1 || err != nil {
		t.Fatalf("sync after the client is back: %d %v", n, err)
	}
	if got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID); got.PanelMissingSince != nil || got.NotifiedExpiringAt == nil {
		t.Fatalf("after the client is back: %+v", got)
	}
	evs := outboxEvents(t, s, events.SubscriptionReminder)
	var e events.SubscriptionReminderEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &e) != nil || e.Kind != events.ReminderExpiring || e.TelegramID != u.TelegramID {
		t.Fatalf("the held-back reminder: %s", evs)
	}
	if n, _ := s.CountPanelMissing(ctx, s.Conn()); n != 0 {
		t.Fatalf("count after the client is back: %d", n)
	}
}

// Staff syncing one service by hand records the mark too, and the service filter and the overview count it.
func TestSyncOneMarksAndTheListsFindIt(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 10<<30)
	_, gone, fp, _ := delivered(t, svc, s, 4611, plan)
	_, fine, _, _ := delivered(t, svc, s, 4612, plan)
	fp.setUsage(gone.ID, Usage{Missing: true})
	fp.setUsage(fine.ID, Usage{UsedBytes: 1, TotalBytes: 10 << 30, Enabled: true})
	for _, id := range []string{gone.ID, fine.ID} {
		if err := svc.SyncSubscription(ctx, fp, id); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := s.ListAllSubscriptions(ctx, s.Conn(), store.SubscriptionFilter{PanelMissing: true}, store.Page{Limit: 50})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != gone.ID || rows[0].PanelMissingSince == nil {
		t.Fatalf("the filter: %d %v %+v", total, err, rows)
	}
	if _, total, _ = s.ListAllSubscriptions(ctx, s.Conn(), store.SubscriptionFilter{}, store.Page{Limit: 50}); total != 2 {
		t.Fatalf("the unfiltered list: %d", total)
	}
	ov, err := s.OverviewStats(ctx, s.Conn(), time.Now())
	if err != nil || ov.ServicesPanelMissing != 1 {
		t.Fatalf("overview: %+v %v", ov, err)
	}
}

// Staff make the client again: the panel is asked with the limits core holds, the action is audited, and
// the mark is cleared by the read that follows; a service that is deleted or being delivered is refused.
func TestRecreateSubscriptionClient(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 10<<30)
	_, sub, fp, _ := delivered(t, svc, s, 4621, plan)
	staff := seedUser(t, s, 4622)
	exp := time.Now().Add(9 * 24 * time.Hour).UTC().Truncate(time.Second)
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET expires_at = $2, traffic_total_bytes = $3 WHERE id = $1`, sub.ID, exp, int64(7<<30)); err != nil {
		t.Fatal(err)
	}
	fp.setUsage(sub.ID, Usage{Missing: true})
	if err := svc.SyncSubscription(ctx, fp, sub.ID); err != nil {
		t.Fatal(err)
	}

	if err := svc.RecreateSubscriptionClient(ctx, staff, fp, sub.ID, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	// The panel has the client again after RecreateClient (the fake flips its answer), so the read that
	// follows clears the mark.
	if err := svc.RecreateSubscriptionClient(ctx, staff, fp, sub.ID, "the operator deleted it by mistake"); err != nil {
		t.Fatal(err)
	}
	if len(fp.recreated) != 1 || fp.recreated[0].expires != exp.Unix() || fp.recreated[0].traffic != 7<<30 {
		t.Fatalf("limits sent to the panel: %+v, want %d and %d", fp.recreated, exp.Unix(), int64(7<<30))
	}
	got, _ := s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.PanelMissingSince != nil {
		t.Fatalf("still marked after the client was made again: %+v", got)
	}
	var audited int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action = 'subscription.recreate_client' AND entity_id = $1`, sub.ID).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit entries: %d %v", audited, err)
	}

	// A deleted service is not brought back.
	if err := svc.DeleteSubscription(ctx, staff, fp, sub.ID, "gone for good"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecreateSubscriptionClient(ctx, staff, fp, sub.ID, "again"); !errors.Is(err, ErrState) {
		t.Fatalf("a deleted service: %v", err)
	}
}

// One daily notice names the services the panel lacks; it ends without a message when none is left.
func TestWatchdogNoticeAboutServicesNotOnThePanel(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	ctx := context.Background()
	plan := planWithLimits(t, a.st, 30, 10<<30)
	_, sub, fp, _ := delivered(t, a.svc, a.st, 4631, plan)
	fp.setUsage(sub.ID, Usage{Missing: true})
	if err := a.svc.SyncSubscription(ctx, fp, sub.ID); err != nil {
		t.Fatal(err)
	}

	for range 2 { // seen once: quiet; seen again: announced
		a.step()
	}
	sent := a.expectSent(1)
	got := sent[0]
	if got.Key != "subscriptions.panel_missing" || got.TitleKey != "alert.panel_missing" || got.Severity != events.SeverityWarning || got.Args["count"] != "1" {
		t.Fatalf("the notice: %+v", got)
	}

	// Not repeated within a day, and the end of a notice is not announced.
	a.after(10 * time.Hour)
	a.step()
	a.expectSent(1)
	if err := a.st.ClearPanelMissing(ctx, a.st.Conn(), sub.ID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		a.after(10 * time.Minute)
		a.step()
	}
	a.expectSent(1)
	if open, _ := a.svc.OpenAlerts(ctx); len(open) != 0 {
		t.Fatalf("the notice stayed open with nothing left to say: %+v", open)
	}
}
