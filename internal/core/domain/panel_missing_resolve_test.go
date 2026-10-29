package domain

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The bot's and the bulk actions' way to decide about a service the panel lacks: the list (live services
// only, the longest missing first, with customer and plan), making one again or deleting it as the
// dashboard does (same audit), and skipping a service that changed since it was listed.
func TestResolvePanelMissing(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 10<<30)
	_, a, fp, _ := delivered(t, svc, s, 4701, plan)
	_, b, _, _ := delivered(t, svc, s, 4702, plan)
	_, c, _, _ := delivered(t, svc, s, 4703, plan)
	_, fine, _, _ := delivered(t, svc, s, 4704, plan)
	staff := seedUser(t, s, 4705)
	if err := s.SetUserRole(ctx, s.Conn(), staff.ID, "admin", "active"); err != nil {
		t.Fatal(err)
	}
	customer := seedUser(t, s, 4706)
	for _, sub := range []*store.Subscription{a, b, c} {
		fp.setUsage(sub.ID, Usage{Missing: true})
		if err := svc.SyncSubscription(ctx, fp, sub.ID); err != nil {
			t.Fatal(err)
		}
	}
	fp.setUsage(fine.ID, Usage{UsedBytes: 1, TotalBytes: 10 << 30, Enabled: true})
	// b has been missing the longest, then a, then c.
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET panel_missing_since = now() - interval '3 days' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET panel_missing_since = now() - interval '2 days' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}

	// Only staff list them, a page at a time, with the count of all.
	if _, err := svc.AdminListPanelMissing(ctx, customer.TelegramID, 0, 5); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer listed them: %v", err)
	}
	page, err := svc.AdminListPanelMissing(ctx, staff.TelegramID, 0, 2)
	if err != nil || page.Total != 3 || len(page.Services) != 2 || page.Services[0].ID != b.ID || page.Services[1].ID != a.ID {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if r := page.Services[0]; r.TelegramID != 4702 || r.PlanName["en"] == "" || r.ClientEmail != b.ClientEmail || r.PanelMissingSince == nil {
		t.Fatalf("the row: %+v", r)
	}
	if page, err = svc.AdminListPanelMissing(ctx, staff.TelegramID, 2, 2); err != nil || page.Total != 3 || len(page.Services) != 1 || page.Services[0].ID != c.ID {
		t.Fatalf("second page: %+v %v", page, err)
	}
	if page, err = svc.AdminListPanelMissing(ctx, staff.TelegramID, 10, 2); err != nil || page.Total != 3 || len(page.Services) != 0 {
		t.Fatalf("past the end: %+v %v", page, err)
	}

	// Refused: no staff, no reason, an unknown action, not an id.
	if _, _, err := svc.AdminResolvePanelMissing(ctx, customer.TelegramID, fp, a.ID, PanelMissingRecreate, "x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer acted: %v", err)
	}
	for _, bad := range [][3]string{{a.ID, PanelMissingRecreate, " "}, {a.ID, "wipe", "x"}, {"not-an-id", PanelMissingDelete, "x"}} {
		if _, _, err := svc.AdminResolvePanelMissing(ctx, staff.TelegramID, fp, bad[0], bad[1], bad[2]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%v: %v", bad, err)
		}
	}
	if len(fp.opsDone()) != 0 {
		t.Fatalf("a refused call reached the panel: %v", fp.opsDone())
	}

	// Made again: the panel is asked, it is audited with the reason, and the mark goes.
	out, sub, err := svc.AdminResolvePanelMissing(ctx, staff.TelegramID, fp, a.ID, PanelMissingRecreate, "not on the panel: made again from the bot by @boss")
	if err != nil || out != PanelMissingDone || sub == nil || sub.ClientEmail != a.ClientEmail || sub.PanelMissingSince != nil {
		t.Fatalf("recreate: %q %+v %v", out, sub, err)
	}
	// Deleted: the panel is asked, the service is deleted and audited.
	if out, _, err = svc.AdminResolvePanelMissing(ctx, staff.TelegramID, fp, b.ID, PanelMissingDelete, "not on the panel: deleted from the bot by @boss"); err != nil || out != PanelMissingDone {
		t.Fatalf("delete: %q %v", out, err)
	}
	if got, _ := s.GetSubscription(ctx, s.Conn(), b.ID); got.Status != "deleted" {
		t.Fatalf("not deleted: %+v", got)
	}
	var audits []string
	rows, err := s.DB().Query(ctx, `SELECT action || ' ' || entity_id || ' ' || COALESCE(reason, '') FROM core.audit_log
		WHERE action IN ('subscription.recreate_client', 'subscription.delete') AND actor_id = $1 ORDER BY created_at, id`, staff.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		audits = append(audits, line)
	}
	rows.Close()
	want := []string{
		"subscription.recreate_client " + a.ID + " not on the panel: made again from the bot by @boss",
		"subscription.delete " + b.ID + " not on the panel: deleted from the bot by @boss",
	}
	if !slices.Equal(audits, want) {
		t.Fatalf("audit:\n%q\nwant\n%q", audits, want)
	}

	// Changed since it was listed: back on the panel (made again), deleted, a service the panel has, an
	// unknown id. Nothing more reaches the panel.
	before := len(fp.opsDone())
	for _, tc := range []struct {
		id, action, want string
	}{
		{a.ID, PanelMissingDelete, PanelMissingBack},
		{b.ID, PanelMissingRecreate, PanelMissingDeleted},
		{fine.ID, PanelMissingDelete, PanelMissingBack},
		{"0198a7c2-0000-7000-8000-000000000000", PanelMissingDelete, PanelMissingDeleted},
	} {
		if out, _, err := svc.AdminResolvePanelMissing(ctx, staff.TelegramID, fp, tc.id, tc.action, "x"); err != nil || out != tc.want {
			t.Fatalf("%s %s: %q %v, want %q", tc.id, tc.action, out, err, tc.want)
		}
	}
	if got := fp.opsDone(); len(got) != before {
		t.Fatalf("a skipped service reached the panel: %v", got[before:])
	}
	if got, _ := s.GetSubscription(ctx, s.Conn(), a.ID); got.Status == "deleted" {
		t.Fatal("a service back on the panel was deleted")
	}

	// The panel failing is an error, and the mark stays.
	fp.mu.Lock()
	fp.opsErr = errors.New("panel down")
	fp.mu.Unlock()
	if _, _, err := svc.AdminResolvePanelMissing(ctx, staff.TelegramID, fp, c.ID, PanelMissingRecreate, "x"); err == nil {
		t.Fatal("a failing panel was not an error")
	}
	if got, _ := s.GetSubscription(ctx, s.Conn(), c.ID); got.PanelMissingSince == nil {
		t.Fatal("the mark went though nothing was made")
	}
	if n, _ := s.CountPanelMissing(ctx, s.Conn()); n != 1 {
		t.Fatalf("left: %d", n)
	}
	st, err := svc.AdminStats(ctx, staff.TelegramID, &fakePayments{}, &fakeServers{})
	if err != nil || st.PanelMissing != 1 {
		t.Fatalf("admin stats: %+v %v", st, err)
	}
}
