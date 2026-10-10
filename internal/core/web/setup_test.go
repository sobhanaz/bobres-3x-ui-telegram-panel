package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A change made in the dashboard is in the audit log with who, why and from
// where; the log filters, exports and stays closed to support.
func TestAuditLog(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	u := f.customer(8501, "hamid")
	if code, _ := b.do(http.MethodPost, "/api/v1/users/"+u.ID+"/balance",
		map[string]string{"amount": "1000", "currency": "IRT", "reason": "=cmd gift", "key": key()}); code != 200 {
		t.Fatalf("adjust: %d", code)
	}
	code, out := b.do(http.MethodGet, "/api/v1/audit?action=wallet", nil)
	list := items(out)
	if code != 200 || out["total"] != float64(1) || len(list) != 1 {
		t.Fatalf("audit by action group: %d %v", code, out)
	}
	e := list[0]
	if e["action"] != "wallet.adjust" || e["reason"] != "=cmd gift" || e["entity_id"] != u.ID || e["ip"] != "127.0.0.1" ||
		e["actor"].(map[string]any)["username"] != "u" || e["actor_role"] != "owner" {
		t.Fatalf("entry: %v", e)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/audit?entity_id="+u.ID, nil); out["total"] != float64(1) {
		t.Fatalf("by item: %v", out)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/audit?action=plan", nil); out["total"] != float64(0) {
		t.Fatalf("another group: %v", out)
	}
	code, out = b.do(http.MethodGet, "/api/v1/audit/actions", nil)
	if code != 200 || !strings.Contains(asJSON(out["items"]), `"action":"wallet.adjust"`) {
		t.Fatalf("actions: %d %v", code, out)
	}
	resp, err := b.c.Get(f.srv.URL + "/api/v1/audit.csv?action=wallet.adjust")
	if err != nil {
		t.Fatal(err)
	}
	csv, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	lines := strings.Split(strings.TrimSpace(string(csv)), "\n")
	if resp.StatusCode != 200 || len(lines) != 2 || !strings.Contains(lines[1], ",'=cmd gift,127.0.0.1,") {
		t.Fatalf("csv: %d %q", resp.StatusCode, csv)
	}

	f.staffUser(8502, "support")
	sup := f.browser()
	sup.loginWithLink(8502)
	if code, _ := sup.do(http.MethodGet, "/api/v1/audit", nil); code != 403 {
		t.Fatalf("support read the audit log: %d", code)
	}
}
