//go:build integration

package adapter

// Client groups against a REAL 3x-ui panel, with the variables of the other
// real-panel tests (XUI_TEST_URL, XUI_TEST_TOKEN, XUI_TEST_ALLOW_PRIVATE=1,
// XUI_TEST_REQUIRE=1). CI runs them against v3.8.5, v3.9.0, v3.10.0 and v3.10.2:
//
//	go test -tags integration -run Integration ./internal/provisioner/adapter/
//
// What BOBRES relies on is asserted here as the panel does it, so a release
// that changes it fails this job instead of a store: a new client is in the
// bot group and stays in it through every call BOBRES makes on it; the panel's
// bulkAdd moves a client out of any other group (which is why the backfill
// sends only clients with no group); an update without a group clears it.
//
// Every client these tests make has a name of its own, and they delete what they
// make, so they work on a panel that holds other clients too. They count nothing
// but their own clients.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// groupPanel is a real panel and the ways these tests look at it.
type groupPanel struct {
	a        *XUIv3
	baseURL  string
	token    string
	inbounds []int
	prefix   string
	made     []string
}

func newGroupPanel(t *testing.T) *groupPanel {
	t.Helper()
	baseURL, token := os.Getenv("XUI_TEST_URL"), os.Getenv("XUI_TEST_TOKEN")
	if baseURL == "" || token == "" {
		if os.Getenv("XUI_TEST_REQUIRE") == "1" {
			t.Fatal("XUI_TEST_URL and XUI_TEST_TOKEN not set and XUI_TEST_REQUIRE=1")
		}
		t.Skip("XUI_TEST_URL and XUI_TEST_TOKEN not set")
	}
	var opts []xui.Option
	if os.Getenv("XUI_TEST_ALLOW_PRIVATE") == "1" {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	a, err := NewXUIv3(baseURL, token, opts...)
	if err != nil {
		t.Fatal(err)
	}
	p := &groupPanel{a: a, baseURL: strings.TrimRight(baseURL, "/"), token: token, prefix: fmt.Sprintf("bobres-grp-%d", time.Now().UnixNano())}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ins, err := a.Inbounds(ctx)
	if err != nil {
		t.Fatalf("inbounds: %v", err)
	}
	for _, in := range ins {
		if in.Enabled {
			p.inbounds = append(p.inbounds, in.ID)
		}
	}
	if len(p.inbounds) == 0 {
		t.Fatal("the panel has no enabled inbound; enable one for this test")
	}
	t.Cleanup(func() {
		for _, e := range p.made {
			_ = a.Delete(context.Background(), e) // already gone is fine
		}
	})
	return p
}

// email is a name for a client of this test, deleted when it ends.
func (p *groupPanel) email(label string) string {
	e := p.prefix + "-" + label
	p.made = append(p.made, e)
	return e
}

// sub is a subscription id of this test (the panel wants each one used by a single client).
func (p *groupPanel) sub(label string) string { return p.prefix + "-s-" + label }

// add makes a client the way BOBRES did before it grouped its clients (group ""),
// or the way an operator does in the panel (any group). sub is its subscription id.
func (p *groupPanel) add(t *testing.T, ctx context.Context, email, sub, group string) {
	t.Helper()
	p.addWith(t, ctx, email, sub, group, func(*xui.ClientSpec) {})
}

// addWith is add for a client in another state: change adjusts the spec first.
func (p *groupPanel) addWith(t *testing.T, ctx context.Context, email, sub, group string, change func(*xui.ClientSpec)) {
	t.Helper()
	spec := xui.ClientSpec{
		Email: email, SubID: sub, Enable: true, Group: group,
		TotalGB: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour).UnixMilli(),
	}
	change(&spec)
	if err := p.a.c.AddClient(ctx, spec, p.inbounds); err != nil {
		t.Fatalf("add %s: %v", email, err)
	}
}

// group is the group the panel holds for a client now.
func (p *groupPanel) group(t *testing.T, ctx context.Context, email string) string {
	t.Helper()
	d, err := p.a.c.GetClient(ctx, email)
	if err != nil {
		t.Fatalf("get %s: %v", email, err)
	}
	return d.Client.Group
}

func (p *groupPanel) wantGroup(t *testing.T, ctx context.Context, email, want, when string) {
	t.Helper()
	if got := p.group(t, ctx, email); got != want {
		t.Fatalf("%s: group of %s is %q, want %q", when, email, got, want)
	}
}

// call sends one request the way an operator's tool would, for what the typed
// client has no call for (an update without a group). It fails the test when
// the panel refuses, and returns the object of the answer.
func (p *groupPanel) call(t *testing.T, ctx context.Context, method, path string, body any) json.RawMessage {
	t.Helper()
	obj, err := p.try(ctx, method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

// try is call without the test: the error says what went wrong (the token is never in it).
func (p *groupPanel) try(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		Success bool            `json:"success"`
		Msg     string          `json:"msg"`
		Obj     json.RawMessage `json:"obj"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&env); err != nil {
		return nil, fmt.Errorf("%s %s: http %d, not the panel's JSON: %w", method, path, resp.StatusCode, err)
	}
	if !env.Success {
		return nil, fmt.Errorf("%s %s: refused: %s", method, path, env.Msg)
	}
	return env.Obj, nil
}

// A client BOBRES creates is in the bot group, the panel lists it there, and
// every call BOBRES makes on it afterwards leaves it there.
func TestIntegrationGroupNewClient(t *testing.T) {
	p := newGroupPanel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	email, sub := p.email("new"), p.prefix+"-sub-new"

	if err := p.a.CreateClient(ctx, email, sub, time.Now().Add(24*time.Hour).UnixMilli(), 1<<30, p.inbounds); err != nil {
		t.Fatalf("create: %v", err)
	}
	p.wantGroup(t, ctx, email, xui.BotGroup, "after create")

	// The panel's own views of the group: its members, its list, its client listing.
	members, err := p.a.c.GroupEmails(ctx, xui.BotGroup)
	if err != nil || !slices.Contains(members, email) {
		t.Fatalf("members of %s: %v %v", xui.BotGroup, members, err)
	}
	groups, err := p.a.c.Groups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range groups {
		if g.Name == xui.BotGroup {
			found = g.ClientCount >= 1
		}
	}
	if !found {
		t.Fatalf("the group list does not show %s with a client: %+v", xui.BotGroup, groups)
	}
	rows, err := p.a.c.ListClients(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var row *xui.ClientRow
	for i := range rows {
		if rows[i].Email == email {
			row = &rows[i]
		}
	}
	if row == nil || row.Group != xui.BotGroup || row.SubID != sub || len(row.InboundIDs) != len(p.inbounds) {
		t.Fatalf("the listing shows %+v for the new client", row)
	}

	// Renewals, top-ups, staff switching it off and on, a traffic reset: the group stays.
	exp := time.Now().Add(72 * time.Hour).Truncate(time.Second).UnixMilli()
	for _, step := range []struct {
		name string
		do   func() error
	}{
		{"renew", func() error { return p.a.Renew(ctx, email, 1, 1<<30) }},
		{"reset traffic", func() error { return p.a.ResetTraffic(ctx, email) }},
		{"set limits", func() error { return p.a.SetLimits(ctx, email, exp, 3<<30) }},
		{"set limits again", func() error { return p.a.SetLimits(ctx, email, exp, 3<<30) }},
		{"switch off", func() error { return p.a.SetEnabled(ctx, email, false) }},
		{"switch on", func() error { return p.a.SetEnabled(ctx, email, true) }},
	} {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		p.wantGroup(t, ctx, email, xui.BotGroup, "after "+step.name)
	}
	if st, err := p.a.Status(ctx, email); err != nil || !st.Enabled || st.ExpiryMs != exp || st.TotalBytes != 3<<30 {
		t.Fatalf("status after all that: %+v %v", st, err)
	}

	// Deleting the client takes it out of the group's members.
	if err := p.a.Delete(ctx, email); err != nil {
		t.Fatal(err)
	}
	if members, err := p.a.c.GroupEmails(ctx, xui.BotGroup); err != nil || slices.Contains(members, email) {
		t.Fatalf("a deleted client is still a member: %v %v", members, err)
	}
}

// The backfill's panel side: clients made before the group existed are tagged,
// a client an operator put in a group keeps it, a client that is not in the map
// of the clients BOBRES holds is never touched, and a repeat changes nothing.
func TestIntegrationGroupTagUngrouped(t *testing.T) {
	p := newGroupPanel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	operator := p.prefix + "-operator"
	t.Cleanup(func() { p.dropGroup(t, operator) })

	oldA, oldB, oldC := p.email("old-a"), p.email("old-b"), p.email("old-c")
	stranger, taken := p.email("stranger"), p.email("taken")
	p.add(t, ctx, oldA, p.sub("a"), "")
	p.add(t, ctx, oldB, p.sub("b"), "")
	p.add(t, ctx, oldC, p.sub("c"), operator) // an operator's group
	p.add(t, ctx, stranger, p.sub("stranger"), "")
	p.add(t, ctx, taken, p.sub("someone-elses"), "") // the email BOBRES holds, but another client's
	// Past a page and a batch of the panel's own limits (200 each).
	const many = 230
	managed := map[string]string{
		oldA: p.sub("a"), oldB: p.sub("b"), oldC: p.sub("c"), taken: p.sub("taken"), p.prefix + "-gone": p.sub("gone"),
	}
	bulk := make([]string, 0, many)
	for i := 0; i < many; i++ {
		e, sub := p.email(fmt.Sprintf("bulk-%03d", i)), p.sub(fmt.Sprintf("bulk-%03d", i))
		p.add(t, ctx, e, sub, "")
		managed[e] = sub
		bulk = append(bulk, e)
	}
	// The states a store's customers end up in are listed and tagged like any other:
	// switched off, expired, not used yet (a negative expiry), and in no inbound at all.
	for label, change := range map[string]func(*xui.ClientSpec){
		"off":       func(s *xui.ClientSpec) { s.Enable = false },
		"expired":   func(s *xui.ClientSpec) { s.ExpiryTime = 1000 },
		"first-use": func(s *xui.ClientSpec) { s.ExpiryTime = -86400000 },
		"orphan":    func(*xui.ClientSpec) {},
	} {
		e, sub := p.email("state-"+label), p.sub("state-"+label)
		p.addWith(t, ctx, e, sub, "", change)
		if label == "orphan" {
			p.call(t, ctx, http.MethodPost, "/panel/api/clients/"+e+"/detach", map[string]any{"inboundIds": p.inbounds})
		}
		managed[e] = sub
		bulk = append(bulk, e)
	}
	tagged := 2 + len(bulk)

	res, err := p.a.TagUngrouped(ctx, managed)
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	want := TagResult{Managed: len(managed), Tagged: tagged, Grouped: 1, Missing: 2, Converged: true}
	if res != want {
		t.Fatalf("result %+v, want %+v", res, want)
	}
	for _, e := range append([]string{oldA, oldB}, bulk...) {
		p.wantGroup(t, ctx, e, xui.BotGroup, "after the backfill")
	}
	p.wantGroup(t, ctx, oldC, operator, "after the backfill (an operator's group is theirs)")
	p.wantGroup(t, ctx, stranger, "", "after the backfill (not held by BOBRES)")
	p.wantGroup(t, ctx, taken, "", "after the backfill (the email is another client's)")
	// A tagged client of any of those states keeps the group through the calls that send its row back.
	for _, label := range []string{"off", "expired", "first-use", "orphan"} {
		e := p.prefix + "-state-" + label
		for _, on := range []bool{false, true} {
			if err := p.a.SetEnabled(ctx, e, on); err != nil {
				t.Fatalf("switch %s %v: %v", label, on, err)
			}
			p.wantGroup(t, ctx, e, xui.BotGroup, fmt.Sprintf("after switching the %s client %v", label, on))
		}
	}

	// Again: nothing to do, nothing changes.
	res, err = p.a.TagUngrouped(ctx, managed)
	if err != nil {
		t.Fatal(err)
	}
	if want := (TagResult{Managed: len(managed), Grouped: tagged + 1, Missing: 2, Converged: true}); res != want {
		t.Fatalf("second run %+v, want %+v", res, want)
	}

	// An operator moves a tagged client to another group: later runs respect it.
	if n, err := p.a.c.AddToGroup(ctx, []string{oldA}, operator); err != nil || n != 1 {
		t.Fatalf("operator moves %s: %d %v", oldA, n, err)
	}
	if _, err := p.a.TagUngrouped(ctx, managed); err != nil {
		t.Fatal(err)
	}
	p.wantGroup(t, ctx, oldA, operator, "after the operator moved it")

	// One client: tagged when it has no group, left when it has one.
	one := p.email("one")
	p.add(t, ctx, one, p.sub("one"), "")
	for i := 0; i < 2; i++ {
		if err := p.a.EnsureTagged(ctx, one); err != nil {
			t.Fatal(err)
		}
		p.wantGroup(t, ctx, one, xui.BotGroup, "after EnsureTagged")
	}
	if n, err := p.a.c.AddToGroup(ctx, []string{one}, operator); err != nil || n != 1 {
		t.Fatalf("move %s: %d %v", one, n, err)
	}
	if err := p.a.EnsureTagged(ctx, one); err != nil {
		t.Fatal(err)
	}
	p.wantGroup(t, ctx, one, operator, "after EnsureTagged on a client in an operator's group")
}

// The panel's behaviour the design rests on, as the pinned versions have it.
func TestIntegrationGroupPanelBehaviour(t *testing.T) {
	p := newGroupPanel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	other := p.prefix + "-other"
	t.Cleanup(func() { p.dropGroup(t, other) })
	email := p.email("behaviour")
	p.add(t, ctx, email, p.prefix+"-sub-behaviour", "")

	// bulkAdd: counts only what changed, ignores an email it does not know, and
	// MOVES a client out of the group it has (so the backfill must not send one).
	n, err := p.a.c.AddToGroup(ctx, []string{email, p.prefix + "-nobody"}, xui.BotGroup)
	if err != nil || n != 1 {
		t.Fatalf("bulkAdd of a client and a stranger: %d %v", n, err)
	}
	if n, err := p.a.c.AddToGroup(ctx, []string{email}, xui.BotGroup); err != nil || n != 0 {
		t.Fatalf("bulkAdd again: %d %v", n, err)
	}
	if n, err := p.a.c.AddToGroup(ctx, []string{email}, other); err != nil || n != 1 {
		t.Fatalf("bulkAdd into another group: %d %v", n, err)
	}
	p.wantGroup(t, ctx, email, other, "after bulkAdd into another group (it overrides)")
	if n, err := p.a.c.AddToGroup(ctx, nil, xui.BotGroup); err != nil || n != 0 {
		t.Fatalf("bulkAdd of nobody: %d %v", n, err)
	}

	// clients/update replaces the row: the group of the body is the group after.
	// BOBRES sends the stored row back (SetLimits, SetEnabled) and so keeps it;
	// a body without a group clears it.
	stored := map[string]json.RawMessage{}
	var d struct {
		Client map[string]json.RawMessage `json:"client"`
	}
	if err := json.Unmarshal(p.call(t, ctx, http.MethodGet, "/panel/api/clients/get/"+email, nil), &d); err != nil {
		t.Fatal(err)
	}
	for k, v := range d.Client {
		switch k {
		case "id", "uuid", "createdAt", "updatedAt", "reverse", "allowedIPs": // as xui.asUpdate drops them
		default:
			stored[k] = v
		}
	}
	stored["id"] = d.Client["uuid"]
	delete(stored, "group")
	p.call(t, ctx, http.MethodPost, "/panel/api/clients/update/"+email, stored)
	p.wantGroup(t, ctx, email, "", "after an update without a group")

	if _, err := p.a.c.AddToGroup(ctx, []string{email}, other); err != nil {
		t.Fatal(err)
	}
	members, err := p.a.c.GroupEmails(ctx, other)
	if err != nil || !slices.Contains(members, email) {
		t.Fatalf("members of the operator's group: %v %v", members, err)
	}

	// A second inbound: attaching a client to it and detaching it again, even
	// from the last inbound it has, leaves its group alone.
	port := 20000 + int(time.Now().UnixNano()%20000)
	var ib struct {
		ID int `json:"id"`
	}
	obj := p.call(t, ctx, http.MethodPost, "/panel/api/inbounds/add", map[string]any{
		"enable": true, "remark": p.prefix, "listen": "", "port": port, "protocol": "vless", "expiryTime": 0, "total": 0,
		"settings":       map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}},
		"streamSettings": map[string]any{"network": "tcp", "security": "none", "tcpSettings": map[string]any{"header": map[string]any{"type": "none"}}},
		"sniffing":       map[string]any{"enabled": false, "destOverride": []string{"http", "tls"}},
	})
	if err := json.Unmarshal(obj, &ib); err != nil || ib.ID == 0 {
		t.Fatalf("second inbound: %s %v", obj, err)
	}
	t.Cleanup(func() {
		_, _ = p.try(context.Background(), http.MethodPost, fmt.Sprintf("/panel/api/inbounds/del/%d", ib.ID), nil)
	})
	for _, step := range []struct {
		name, path string
		ids        []int
	}{
		{"attach to a second inbound", "attach", []int{ib.ID}},
		{"detach from it", "detach", []int{ib.ID}},
		{"detach from all inbounds", "detach", p.inbounds},
		{"attach to the first ones again", "attach", p.inbounds},
	} {
		p.call(t, ctx, http.MethodPost, "/panel/api/clients/"+email+"/"+step.path, map[string]any{"inboundIds": step.ids})
		p.wantGroup(t, ctx, email, other, "after "+step.name)
	}

	// What an emptied group does. One that bulkAdd made for a new name stays in the
	// panel's list with 0 clients; one that only a client's own "group" made goes
	// with its last client.
	p.dropGroup(t, other)
	own := p.prefix + "-own"
	t.Cleanup(func() { p.dropGroup(t, own) })
	delGroup := p.email("emptied")
	p.add(t, ctx, delGroup, p.prefix+"-sub-emptied", own)
	if g := p.listed(t, ctx, own); g == nil || g.ClientCount != 1 {
		t.Fatalf("a group made by a client's own group label: %+v", g)
	}
	if err := p.a.Delete(ctx, delGroup); err != nil {
		t.Fatal(err)
	}
	if g := p.listed(t, ctx, own); g != nil {
		t.Fatalf("a group made by a client's own label stays after its last client is deleted: %+v", g)
	}
	made := p.email("made")
	p.add(t, ctx, made, p.prefix+"-sub-made", "")
	if _, err := p.a.c.AddToGroup(ctx, []string{made}, own); err != nil {
		t.Fatal(err)
	}
	if err := p.a.Delete(ctx, made); err != nil {
		t.Fatal(err)
	}
	if g := p.listed(t, ctx, own); g == nil || g.ClientCount != 0 {
		t.Fatalf("a group that bulkAdd made for a new name should stay, empty, after its last client is deleted: %+v", g)
	}
}

// listed is the panel's group list entry of that name, nil when it is not listed.
func (p *groupPanel) listed(t *testing.T, ctx context.Context, name string) *xui.GroupInfo {
	t.Helper()
	groups, err := p.a.c.Groups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i]
		}
	}
	return nil
}

// dropGroup removes a group the tests made from the panel's list (its members, if any, are kept).
func (p *groupPanel) dropGroup(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.try(ctx, http.MethodPost, "/panel/api/clients/groups/delete", map[string]string{"name": name}); err != nil {
		t.Logf("dropping group %s: %v", name, err)
	}
}
