package xuifake_test

// The group model of the fake, against what real v3.8.5 and v3.9.0 panels do
// (adapter's integration tests, CI job "3x-ui integration", assert the same
// facts on the real thing). A tool that trusts the fake is only as good as
// these.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
)

const tok = "fake-token-not-a-secret" //gitleaks:allow test fixture

// post sends a request as an API client does and returns the envelope.
func post(t *testing.T, s *xuifake.Server, method, path string, body any) (success bool, msg string, obj json.RawMessage) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, s.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		Success bool            `json:"success"`
		Msg     string          `json:"msg"`
		Obj     json.RawMessage `json:"obj"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return env.Success, env.Msg, env.Obj
}

func groupNames(t *testing.T, s *xuifake.Server) map[string]int {
	t.Helper()
	_, _, obj := post(t, s, http.MethodGet, "/panel/api/clients/groups", nil)
	var list []struct {
		Name        string `json:"name"`
		ClientCount int    `json:"clientCount"`
	}
	if err := json.Unmarshal(obj, &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, g := range list {
		out[g.Name] = g.ClientCount
	}
	return out
}

func bulkAdd(t *testing.T, s *xuifake.Server, group string, emails ...string) int {
	t.Helper()
	ok, msg, obj := post(t, s, http.MethodPost, "/panel/api/clients/groups/bulkAdd", map[string]any{"emails": emails, "group": group})
	if !ok {
		t.Fatalf("bulkAdd refused: %s", msg)
	}
	var r struct {
		Affected int `json:"affected"`
	}
	if err := json.Unmarshal(obj, &r); err != nil {
		t.Fatal(err)
	}
	return r.Affected
}

// clients/update replaces the row: the group of the body is the group after,
// so a body without one clears it (LostFields tells).
func TestUpdateWritesTheGroupOfTheBody(t *testing.T) {
	s := xuifake.NewServer(tok)
	defer s.Close()
	s.SeedGrouped("u1", "sub-u1", "g", []int{1})
	stored := func() map[string]any {
		_, _, obj := post(t, s, http.MethodGet, "/panel/api/clients/get/u1", nil)
		var d struct {
			Client map[string]any `json:"client"`
		}
		if err := json.Unmarshal(obj, &d); err != nil {
			t.Fatal(err)
		}
		return d.Client
	}
	body := stored()
	body["id"] = body["uuid"]
	delete(body, "uuid")
	if ok, msg, _ := post(t, s, http.MethodPost, "/panel/api/clients/update/u1", body); !ok { // the group goes back with the row
		t.Fatal(msg)
	}
	if g, _ := s.Group("u1"); g != "g" || len(s.LostFields()) != 0 {
		t.Fatalf("an update that carries the group: %q, lost %v", g, s.LostFields())
	}
	delete(body, "group")
	if ok, msg, _ := post(t, s, http.MethodPost, "/panel/api/clients/update/u1", body); !ok {
		t.Fatal(msg)
	}
	if g, _ := s.Group("u1"); g != "" {
		t.Fatalf("an update without a group left %q", g)
	}
	if lost := s.LostFields(); !slices.Equal(lost, []string{"u1: group"}) {
		t.Fatalf("lost fields: %v", lost)
	}
}

// bulkAdd: counts what changed, ignores emails it does not know, moves a client
// out of the group it has, wants a group name, and takes no email as fine.
func TestBulkAddFollowsThePanel(t *testing.T) {
	s := xuifake.NewServer(tok)
	defer s.Close()
	s.Seed("a", "sub-a", []int{1})
	s.SeedGrouped("b", "sub-b", "vip", []int{1})
	s.SeedGrouped("c", "sub-c", "bobres_bot", []int{1})
	if n := bulkAdd(t, s, "bobres_bot", "a", "b", "c", "ghost"); n != 2 { // a and b changed
		t.Fatalf("affected %d", n)
	}
	if g, _ := s.Group("b"); g != "bobres_bot" {
		t.Fatalf("b is in %q: bulkAdd moves a client out of its group", g)
	}
	if n := bulkAdd(t, s, "bobres_bot", "a", "b", "c"); n != 0 {
		t.Fatalf("repeat affected %d", n)
	}
	if n := bulkAdd(t, s, "bobres_bot"); n != 0 { // no emails
		t.Fatalf("no emails affected %d", n)
	}
	ok, msg, _ := post(t, s, http.MethodPost, "/panel/api/clients/groups/bulkAdd", map[string]any{"emails": []string{"a"}, "group": "  "})
	if ok || !strings.Contains(msg, "group name is required") {
		t.Fatalf("an empty group name: %v %q", ok, msg) // an envelope of failure inside HTTP 200, as on the panel
	}
	if got := s.GroupBatches(); !slices.Equal(got, []int{4, 3, 0}) { // the refused one is not a batch
		t.Fatalf("batches %v", got)
	}
}

// The group list: a group made by a client's own label goes with its last
// client; one that bulkAdd made for a new name stays, empty. Sorted without
// regard to case.
func TestGroupListFollowsThePanel(t *testing.T) {
	s := xuifake.NewServer(tok)
	defer s.Close()
	s.SeedGrouped("a", "sub-a", "Zeta", []int{1})
	s.SeedGrouped("b", "sub-b", "alpha", []int{1})
	_, _, obj := post(t, s, http.MethodGet, "/panel/api/clients/groups", nil)
	var order []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(obj, &order); err != nil || len(order) != 2 || order[0].Name != "alpha" || order[1].Name != "Zeta" {
		t.Fatalf("order: %s %v", obj, err)
	}

	s.Seed("m", "sub-m", []int{1})
	if n := bulkAdd(t, s, "made", "m"); n != 1 {
		t.Fatal(n)
	}
	if got := groupNames(t, s); got["made"] != 1 || got["alpha"] != 1 {
		t.Fatalf("groups: %v", got)
	}
	if ok, _, _ := post(t, s, http.MethodPost, "/panel/api/clients/del/m", nil); !ok {
		t.Fatal("delete")
	}
	if listed := groupNames(t, s); !hasKey(listed, "made") || listed["made"] != 0 {
		t.Fatalf("a group made by bulkAdd should stay with 0 clients: %v", listed)
	}
	if ok, _, _ := post(t, s, http.MethodPost, "/panel/api/clients/del/b", nil); !ok {
		t.Fatal("delete")
	}
	if listed := groupNames(t, s); hasKey(listed, "alpha") {
		t.Fatalf("a group made by a client's own label stays after its last client: %v", listed)
	}

	_, _, obj = post(t, s, http.MethodGet, "/panel/api/clients/groups/Zeta/emails", nil)
	if string(obj) != `["a"]` {
		t.Fatalf("members: %s", obj)
	}
	_, _, obj = post(t, s, http.MethodGet, "/panel/api/clients/groups/nobody/emails", nil)
	if string(obj) != `[]` {
		t.Fatalf("members of nothing: %s", obj)
	}
}

func hasKey(m map[string]int, k string) bool { _, ok := m[k]; return ok }

// clients/list/paged: slim rows in the panel's row order, 25 a page unless
// asked, 200 at most, empty past the end; "group" is left out for no group.
func TestPagedListFollowsThePanel(t *testing.T) {
	s := xuifake.NewServer(tok)
	defer s.Close()
	for i := 0; i < 230; i++ {
		e := "c" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if i == 0 {
			s.SeedGrouped(e, "sub-"+e, "g", []int{1, 2})
		} else {
			s.Seed(e, "sub-"+e, []int{1})
		}
	}
	page := func(q string) (items []map[string]any, total int) {
		t.Helper()
		ok, msg, obj := post(t, s, http.MethodGet, "/panel/api/clients/list/paged"+q, nil)
		if !ok {
			t.Fatal(msg)
		}
		var p struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		if err := json.Unmarshal(obj, &p); err != nil {
			t.Fatal(err)
		}
		return p.Items, p.Total
	}
	if items, total := page(""); len(items) != 25 || total != 230 {
		t.Fatalf("default page: %d of %d", len(items), total)
	}
	items, _ := page("?page=1&pageSize=500")
	if len(items) != 200 {
		t.Fatalf("page size is capped at 200, got %d", len(items))
	}
	if items[0]["group"] != "g" || items[1]["group"] != nil {
		t.Fatalf("group key: %v / %v", items[0]["group"], items[1]["group"])
	}
	if items, _ := page("?page=2&pageSize=200"); len(items) != 30 {
		t.Fatalf("second page: %d", len(items))
	}
	if items, total := page("?page=3&pageSize=200"); len(items) != 0 || total != 230 {
		t.Fatalf("past the end: %d of %d", len(items), total)
	}
}
