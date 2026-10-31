package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// A service whose client the panel no longer has is marked when it is read from the panel, is listed by the
// filter and counted on the overview; staff with the write permission make the client again; the panel
// being down is a 503 with the mark left as it was.
func TestServicesNotOnThePanel(t *testing.T) {
	f, b, _, prov := salesFixture(t)
	p := f.plan("Monthly 50GB", 30, 50<<30, 120000)
	expires := time.Now().Add(5 * 24 * time.Hour).UTC().Truncate(time.Second)
	_, fine := f.order(f.customer(8301, "nasim"), p, "active", "active", expires, 50<<30)
	_, gone := f.order(f.customer(8302, "omid"), p, "active", "active", expires, 50<<30)

	// The panel has the first client: nothing is marked.
	prov.usage = domain.Usage{UsedBytes: 1 << 30, TotalBytes: 50 << 30, ExpiresAt: expires.Unix(), Enabled: true}
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+fine.ID+"/sync", nil); code != 200 || out["service"].(map[string]any)["panel_missing_since"] != nil {
		t.Fatalf("sync of a client the panel has: %d %v", code, out)
	}
	// It has no client for the second: the sync marks it, with the time.
	prov.usage = domain.Usage{Missing: true}
	code, out := b.do(http.MethodPost, "/api/v1/services/"+gone.ID+"/sync", nil)
	since, _ := out["service"].(map[string]any)["panel_missing_since"].(float64)
	if code != 200 || since == 0 || time.Since(time.Unix(int64(since), 0)) > time.Minute {
		t.Fatalf("sync of a client the panel lacks: %d %v", code, out)
	}

	if code, out = b.do(http.MethodGet, "/api/v1/services?panel=missing", nil); code != 200 || out["total"] != float64(1) || items(out)[0]["id"] != gone.ID {
		t.Fatalf("the filter: %d %v", code, out)
	}
	if code, out = b.do(http.MethodGet, "/api/v1/services", nil); code != 200 || out["total"] != float64(2) {
		t.Fatalf("the unfiltered list: %d %v", code, out)
	}
	if code, out = b.do(http.MethodGet, "/api/v1/overview", nil); code != 200 || out["services"].(map[string]any)["panel_missing"] != float64(1) {
		t.Fatalf("overview: %d %v", code, out)
	}

	// Support may read and extend but not make clients again (the write permission, like delete).
	f.staffUser(8303, "support")
	sup := f.browser()
	sup.loginWithLink(8303)
	if code, _ = sup.do(http.MethodPost, "/api/v1/services/"+gone.ID+"/recreate-client", map[string]any{"reason": "x"}); code != 403 {
		t.Fatalf("support made a client again: %d", code)
	}
	// A reason is required; the panel being down changes nothing.
	if code, out = b.do(http.MethodPost, "/api/v1/services/"+gone.ID+"/recreate-client", map[string]any{"reason": " "}); code != 400 || out["error"] != "invalid" {
		t.Fatalf("no reason: %d %v", code, out)
	}
	prov.down = true
	if code, _ = b.do(http.MethodPost, "/api/v1/services/"+gone.ID+"/recreate-client", map[string]any{"reason": "mistake"}); code != 503 {
		t.Fatalf("the panel being down: %d", code)
	}
	prov.down = false
	if _, out = b.do(http.MethodGet, "/api/v1/services/"+gone.ID, nil); out["service"].(map[string]any)["panel_missing_since"] == nil {
		t.Fatal("a failed attempt cleared the mark")
	}

	// The client is made again, and the read that follows (the panel has it now) clears the mark.
	prov.usage = domain.Usage{TotalBytes: 50 << 30, ExpiresAt: expires.Unix(), Enabled: true}
	if code, out = b.do(http.MethodPost, "/api/v1/services/"+gone.ID+"/recreate-client", map[string]any{"reason": "the operator deleted it by mistake"}); code != 200 ||
		out["service"].(map[string]any)["panel_missing_since"] != nil {
		t.Fatalf("recreate: %d %v", code, out)
	}
	if !strings.Contains(prov.done(), "recreate:"+gone.ID) {
		t.Fatalf("panel calls: %s", prov.done())
	}
	if _, out = b.do(http.MethodGet, "/api/v1/services?panel=missing", nil); out["total"] != float64(0) {
		t.Fatalf("the filter after: %v", out)
	}
}

// The Services page's bulk actions send only_missing: a service the panel still lacks is made again or
// deleted as usual (audited with the one reason), and one that changed since the list was loaded is
// answered 200 with what became of it ("skipped"), and nothing is done to it.
func TestBulkActionsOnlyWhileNotOnThePanel(t *testing.T) {
	f, b, _, prov := salesFixture(t)
	p := f.plan("Monthly 50GB", 30, 50<<30, 120000)
	expires := time.Now().Add(5 * 24 * time.Hour).UTC().Truncate(time.Second)
	_, one := f.order(f.customer(8311, "aria"), p, "active", "active", expires, 50<<30)
	_, two := f.order(f.customer(8312, "bahar"), p, "active", "active", expires, 50<<30)
	_, fine := f.order(f.customer(8313, "cyrus"), p, "active", "active", expires, 50<<30)
	prov.usage = domain.Usage{Missing: true}
	for _, id := range []string{one.ID, two.ID} {
		if code, _ := b.do(http.MethodPost, "/api/v1/services/"+id+"/sync", nil); code != 200 {
			t.Fatalf("sync %s: %d", id, code)
		}
	}
	body := func(reason string) map[string]any { return map[string]any{"reason": reason, "only_missing": true} }
	status := func(out map[string]any) any { return out["service"].(map[string]any)["status"] }

	// Still a reason first, and support may not.
	if code, _ := b.do(http.MethodPost, "/api/v1/services/"+one.ID+"/delete", body(" ")); code != 400 {
		t.Fatalf("no reason: %d", code)
	}
	f.staffUser(8314, "support")
	sup := f.browser()
	sup.loginWithLink(8314)
	if code, _ := sup.do(http.MethodPost, "/api/v1/services/"+one.ID+"/delete", body("x")); code != 403 {
		t.Fatalf("support deleted: %d", code)
	}

	// A service the panel has is not deleted (its client is there).
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+fine.ID+"/delete", body("bulk")); code != 200 || out["skipped"] != "back" || status(out) != "active" {
		t.Fatalf("a service on the panel: %d %v", code, out)
	}
	// Made again, then a second "make again" is a skip.
	prov.usage = domain.Usage{TotalBytes: 50 << 30, ExpiresAt: expires.Unix(), Enabled: true}
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+one.ID+"/recreate-client", body("bulk: deleted by mistake")); code != 200 || out["skipped"] != nil ||
		out["service"].(map[string]any)["panel_missing_since"] != nil {
		t.Fatalf("recreate: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+one.ID+"/recreate-client", body("again")); code != 200 || out["skipped"] != "back" {
		t.Fatalf("recreate again: %d %v", code, out)
	}
	// Deleted, then a second delete is a skip.
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+two.ID+"/delete", body("bulk: customer left")); code != 200 || out["skipped"] != nil || status(out) != "deleted" {
		t.Fatalf("delete: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+two.ID+"/delete", body("again")); code != 200 || out["skipped"] != "deleted" {
		t.Fatalf("delete again: %d %v", code, out)
	}
	if got := prov.done(); strings.Count(got, "recreate:") != 1 || strings.Count(got, "delete:") != 1 || strings.Contains(got, fine.ID) {
		t.Fatalf("panel calls: %s", got)
	}
	rows, err := f.st.DB().Query(t.Context(), `SELECT action || ' ' || reason FROM core.audit_log
		WHERE action IN ('subscription.recreate_client', 'subscription.delete') ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		reasons = append(reasons, s)
	}
	rows.Close()
	if strings.Join(reasons, "|") != "subscription.recreate_client bulk: deleted by mistake|subscription.delete bulk: customer left" {
		t.Fatalf("audit: %q", reasons)
	}
	// Without the flag, delete is what it always was: a service the panel has is deleted too.
	if code, out := b.do(http.MethodPost, "/api/v1/services/"+fine.ID+"/delete", map[string]any{"reason": "single"}); code != 200 ||
		status(out) != "deleted" || out["skipped"] != nil {
		t.Fatalf("plain delete: %d %v", code, out)
	}
}
