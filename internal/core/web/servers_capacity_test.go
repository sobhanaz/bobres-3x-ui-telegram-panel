package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// A server's client limit goes to the provisioner as it is typed, comes back in the list and the overview, is audited, and an edit that
// does not mention it keeps it (0 is the way to lift it).
func TestServerClientLimit(t *testing.T) {
	_, b, fs := serversFixture(t)
	body := map[string]any{"name": "Germany 1", "base_url": "https://de.example.com:2053/p", "token": panelToken, "weight": 100,
		"enabled": true, "allow_private": false, "skip_check": false, "max_clients": 50}
	code, out := b.do(http.MethodPost, "/api/v1/servers", body)
	sv := item(out)
	if code != http.StatusCreated || sv["max_clients"] != float64(50) {
		t.Fatalf("add with a limit: %d %v", code, out)
	}
	id := sv["id"].(string)
	if code, out := b.do(http.MethodGet, "/api/v1/servers", nil); code != 200 || items(out)[0]["max_clients"] != float64(50) {
		t.Fatalf("list: %d %v", code, out)
	}
	if e := lastAudit(t, b, "action=server.add"); !strings.Contains(asJSON(e["after"]), `"max_clients":50`) {
		t.Fatalf("audit of the add: %v", e)
	}

	// An edit that leaves the field out keeps the limit; a new value replaces it; 0 lifts it.
	rest := map[string]any{"name": "Germany 1", "base_url": "https://de.example.com:2053/p", "token": "", "weight": 200, "enabled": true}
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, rest); code != 200 || item(out)["max_clients"] != float64(50) || item(out)["weight"] != float64(200) {
		t.Fatalf("edit without the field: %d %v", code, out)
	}
	rest["max_clients"] = 80
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, rest); code != 200 || item(out)["max_clients"] != float64(80) {
		t.Fatalf("raise: %d %v", code, out)
	}
	e := lastAudit(t, b, "action=server.update")
	if !strings.Contains(asJSON(e["before"]), `"max_clients":50`) || !strings.Contains(asJSON(e["after"]), `"max_clients":80`) {
		t.Fatalf("audit of the change: %v", e)
	}
	rest["max_clients"] = 0
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, rest); code != 200 || item(out)["max_clients"] != float64(0) {
		t.Fatalf("lift: %d %v", code, out)
	}

	// A limit outside 0..1,000,000 is refused with its field named; a fraction is not a body core reads.
	for _, bad := range []int{-1, domain.MaxServerClients + 1} {
		rest["max_clients"] = bad
		if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, rest); code != 400 || out["field"] != "max_clients" {
			t.Errorf("a limit of %d: %d %v", bad, code, out)
		}
	}
	rest["max_clients"] = 2.5
	if code, _ := b.do(http.MethodPut, "/api/v1/servers/"+id, rest); code != 400 {
		t.Errorf("a fraction: %d", code)
	}
	rest["max_clients"] = domain.MaxServerClients
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, rest); code != 200 || item(out)["max_clients"] != float64(domain.MaxServerClients) {
		t.Errorf("the largest limit: %d %v", code, out)
	}

	// The overview carries it, for the card that warns of a server that is nearly full.
	fs.servers[0].Clients, fs.servers[0].MaxClients = 45, 50
	if code, out := b.do(http.MethodGet, "/api/v1/overview", nil); code != 200 {
		t.Fatalf("overview: %d", code)
	} else if servers, _ := out["servers"].([]any); len(servers) != 1 || servers[0].(map[string]any)["max_clients"] != float64(50) || servers[0].(map[string]any)["clients"] != float64(45) {
		t.Fatalf("overview servers: %v", out["servers"])
	}
}
