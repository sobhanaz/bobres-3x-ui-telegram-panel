package web

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

const (
	upServer   = "00000000-0000-7000-8000-0000000000e1"
	downServer = "00000000-0000-7000-8000-0000000000e2"
	offServer  = "00000000-0000-7000-8000-0000000000e3"
)

// The Undelivered tab: the waiting orders, the longest first, with their seller; the servers a new service may move
// to (the ones on, with their health); retry all; move one, with a reason, to a server that is on; refund one with a
// note for the buyer. Support may look but not act.
func TestUndeliveredOrdersAPI(t *testing.T) {
	f, b, _, prov := salesFixture(t)
	f.api.cfg.Servers = &fakeServers{servers: []domain.Server{
		{ID: upServer, Name: "germany", Enabled: true, Healthy: true, CheckedAt: time.Now().Unix()},
		{ID: downServer, Name: "k.noxioai.com", Enabled: true, Healthy: false, HealthError: "the panel did not answer in time", CheckedAt: time.Now().Unix()},
		{ID: offServer, Name: "old", Enabled: false},
	}}
	p := f.plan("Multi", 30, 50<<30, 300000)
	buyer := f.customer(8701, "zarei")
	first, sub := f.order(buyer, p, "provision_failed", "pending", time.Time{}, 0)
	second, _ := f.order(f.customer(8702, "ali"), p, "provision_failed", "", time.Time{}, 0)
	f.order(f.customer(8703, "done"), p, "active", "active", time.Now().Add(48*time.Hour), 50<<30)

	code, out := b.do(http.MethodGet, "/api/v1/orders/undelivered", nil)
	list := items(out)
	if code != 200 || out["total"] != float64(2) || len(list) != 2 || list[0]["id"] != first.ID || list[1]["id"] != second.ID {
		t.Fatalf("list: %d %v", code, out)
	}
	if list[0]["seller"] != nil || list[0]["server_id"] != nil || out["servers_error"] != "" {
		t.Fatalf("an order of the store: %v", list[0])
	}
	servers, _ := out["servers"].([]any)
	if len(servers) != 2 || asJSON(servers[1]) != `{"checked":true,"error":"the panel did not answer in time","full":false,"healthy":false,"id":"`+downServer+`","name":"k.noxioai.com"}` {
		t.Fatalf("servers: %v", servers)
	}

	if code, out := b.do(http.MethodPost, "/api/v1/orders/undelivered/retry", nil); code != 200 || out["count"] != float64(2) {
		t.Fatalf("retry all: %d %v", code, out)
	}

	// Move: a reason and a server that is on.
	for name, body := range map[string]map[string]string{
		"no reason":  {"server_id": upServer},
		"server off": {"server_id": offServer, "reason": "panel down"},
		"no server":  {"server_id": "germany", "reason": "panel down"},
	} {
		if code, out := b.do(http.MethodPost, "/api/v1/orders/"+first.ID+"/move", body); code < 400 {
			t.Fatalf("%s: %d %v", name, code, out)
		}
	}
	code, out = b.do(http.MethodPost, "/api/v1/orders/"+first.ID+"/move", map[string]string{"server_id": upServer, "reason": "k.noxioai.com is down"})
	order, _ := out["order"].(map[string]any)
	if code != 200 || order["server_id"] != upServer || out["leftover"] != false || out["warning"] != nil {
		t.Fatalf("move: %d %v", code, out)
	}
	if prov.done() != "release:"+sub.ID {
		t.Fatalf("the old reservation: %q", prov.done())
	}
	if _, out := b.do(http.MethodGet, "/api/v1/orders/"+first.ID, nil); asJSON(out["can"]) != `{"move":true,"refund":true,"retry":true}` {
		t.Fatalf("detail after the move: %v", out["can"])
	}

	// Refund with a note: the buyer is told; the note is audited with the refund.
	code, out = b.do(http.MethodPost, "/api/v1/orders/"+second.ID+"/refund", map[string]string{"reason": "panel down", "note": "Sorry for the wait"})
	if code != 200 || asJSON(out["commission_taken"]) != `{"amount":0,"currency":"IRT"}` {
		t.Fatalf("refund: %d %v", code, out)
	}
	var note string
	if err := f.st.DB().QueryRow(context.Background(), `SELECT after->>'note' FROM core.audit_log WHERE action = 'order.refund' AND entity_id = $1`, second.ID).Scan(&note); err != nil || note != "Sorry for the wait" {
		t.Fatalf("audit: %q %v", note, err)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/orders/undelivered", nil); out["total"] != float64(1) {
		t.Fatalf("after the refund: %v", out)
	}

	// Support reads the list but cannot act.
	f.staffUser(8710, "support")
	sb := f.browser()
	if code := sb.loginWithLink(8710); code != 200 {
		t.Fatalf("support login: %d", code)
	}
	if code, _ := sb.do(http.MethodGet, "/api/v1/orders/undelivered", nil); code != 200 {
		t.Fatalf("support list: %d", code)
	}
	if code, _ := sb.do(http.MethodPost, "/api/v1/orders/undelivered/retry", nil); code != 403 {
		t.Fatalf("support retry all: %d", code)
	}
	if code, _ := sb.do(http.MethodPost, "/api/v1/orders/"+first.ID+"/move", map[string]string{"server_id": upServer, "reason": "x"}); code != 403 {
		t.Fatalf("support move: %d", code)
	}

	// The provisioner not answering: the list still comes, without servers.
	reg := f.api.cfg.Servers.(*fakeServers)
	reg.mu.Lock()
	reg.down = true
	reg.mu.Unlock()
	if code, out := b.do(http.MethodGet, "/api/v1/orders/undelivered", nil); code != 200 || out["servers_error"] == "" || len(out["servers"].([]any)) != 0 {
		t.Fatalf("provisioner down: %d %v", code, out)
	}
}
