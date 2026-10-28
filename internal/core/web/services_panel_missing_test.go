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
