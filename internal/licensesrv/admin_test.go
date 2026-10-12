package licensesrv

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestKeyCreateValidates(t *testing.T) {
	e := newEnv(t, envOpts{})
	ent := entFile(t, testEntitlements)
	for name, tc := range map[string]struct {
		args  []string
		stdin string
		code  int
		want  string
	}{
		"no args":            {nil, "", 2, "usage"},
		"unknown sub":        {[]string{"delete"}, "", 2, "usage"},
		"no entitlements":    {[]string{"create", "-customer", "A", "-tier", "pro", "-days", "30"}, "", 2, "usage"},
		"no end":             {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent}, "", 1, "exactly one of"},
		"two ends":           {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "3", "-no-end"}, "", 1, "exactly one of"},
		"bad tier":           {[]string{"create", "-customer", "A", "-tier", "gold", "-entitlements", ent, "-days", "3"}, "", 1, "-tier"},
		"no customer":        {[]string{"create", "-tier", "pro", "-entitlements", ent, "-days", "3"}, "", 1, "-customer"},
		"big grace":          {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "3", "-grace-days", "91"}, "", 1, "-grace-days"},
		"past end":           {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-ends-at", "2020-01-01"}, "", 1, "past"},
		"bad date":           {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-ends-at", "next year"}, "", 1, "-ends-at"},
		"negative days":      {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "-3"}, "", 1, "-days"},
		"endless days":       {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "200000"}, "", 1, "-days"},
		"misspelt field":     {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "-", "-days", "3"}, `{"max_server": 1}`, 1, "unknown field"},
		"limit below -1":     {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "-", "-days", "3"}, `{"max_servers": -2}`, 1, "limits"},
		"bad gateway":        {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "-", "-days", "3"}, `{"gateways": ["Zarin Pal"]}`, 1, "gateway"},
		"bad updates_until":  {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "-", "-days", "3"}, `{"updates_until": "soon"}`, 1, "updates_until"},
		"missing file":       {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "/nonexistent/ent.json", "-days", "3"}, "", 1, "entitlements"},
		"stray argument":     {[]string{"create", "extra", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "3"}, "", 2, "usage"},
		"unknown flag":       {[]string{"create", "-colour", "red"}, "", 2, "usage"},
		"show without id":    {[]string{"show"}, "", 2, "usage"},
		"show unknown":       {[]string{"show", "lic_nope"}, "", 1, "no license lic_nope"},
		"revoke unknown":     {[]string{"revoke", "lic_nope", "-reason", "x"}, "", 1, "no license lic_nope"},
		"update unknown":     {[]string{"update", "lic_nope", "-reason", "x", "-tier", "pro"}, "", 1, "no license lic_nope"},
		"update no reason":   {[]string{"update", "lic_nope", "-tier", "pro"}, "", 1, "-reason"},
		"list bad status":    {[]string{"list", "-status", "gone"}, "", 2, "usage"},
		"stdin entitlements": {[]string{"create", "-customer", "B", "-tier", "trial", "-entitlements", "-", "-days", "14"}, `{"max_servers": 1}`, 0, "Created lic_"},
	} {
		code, out, errOut := e.key(tc.stdin, tc.args...)
		if code != tc.code || !strings.Contains(out+errOut, tc.want) {
			t.Errorf("%s: exit %d, want %d; output %q does not contain %q", name, code, tc.code, out+errOut, tc.want)
		}
	}
}

func TestKeyCreatePrintsTheKeyOnceOnStdout(t *testing.T) {
	e := newEnv(t, envOpts{})
	code, out, errOut := e.key("", "create", "-customer", "Beaver Net", "-tier", "pro", "-entitlements", entFile(t, testEntitlements), "-ends-at", "2027-10-12")
	if code != 0 {
		t.Fatalf("create: %d %s", code, errOut)
	}
	key := strings.TrimSpace(out)
	if !keyRe.MatchString(key) || strings.Count(out, "\n") != 1 {
		t.Fatalf("stdout must be exactly the key: %q", out)
	}
	if strings.Contains(errOut, key) {
		t.Fatal("the key is repeated on stderr")
	}
	l, err := e.st.LicenseByKeyHash(context.Background(), e.st.db, HashKey(key), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Created "+l.ID+" for Beaver Net") {
		t.Fatalf("stderr %q", errOut)
	}
	if l.EndsAt == nil || !l.EndsAt.Equal(time.Date(2027, 10, 12, 0, 0, 0, 0, time.UTC)) || l.GraceDays != DefaultGraceDays || l.Tier != "pro" {
		t.Fatalf("stored %+v", l)
	}
	if !strings.HasPrefix(l.ID, "lic_") || len(l.ID) != 20 {
		t.Fatalf("license id %q", l.ID)
	}
	// show and list never print the key either.
	_, show, _ := e.key("", "show", l.ID)
	_, list, _ := e.key("", "list")
	if strings.Contains(show+list, strings.TrimPrefix(key, "BOBRES-")) {
		t.Fatal("show or list printed the key")
	}
}

func TestKeyListShowUpdate(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense("-days", "100")
	install := newInstall()
	moved := newInstall()
	if r := e.activate(key, install, "panel.beaver.example"); r.Status != http.StatusOK {
		t.Fatalf("activate: %d", r.Status)
	}
	if r := e.activate(key, moved, "moved.beaver.example"); r.Status != http.StatusOK {
		t.Fatalf("move: %d", r.Status)
	}

	code, out, errOut := e.key("", "list")
	if code != 0 {
		t.Fatalf("list: %d %s", code, errOut)
	}
	var row string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, l.ID) {
			row = line
		}
	}
	for _, want := range []string{"Acme VPN", "standard", "active", "moved.beaver.example", "1/3"} {
		if !strings.Contains(row, want) {
			t.Fatalf("list row %q lacks %q", row, want)
		}
	}
	if _, out, _ := e.key("", "list", "-status", "revoked"); strings.Contains(out, l.ID) {
		t.Fatal("an active license listed as revoked")
	}

	code, out, errOut = e.key("", "show", l.ID)
	if code != 0 {
		t.Fatalf("show: %d %s", code, errOut)
	}
	for _, want := range []string{"License:      " + l.ID, "Moves (1y):   1 of 3", `"max_servers": 2`, "panel.beaver.example", "moved.beaver.example",
		install, "activation.move", "activation.create", "license.create", "first", "move", "0.1.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show lacks %q:\n%s", want, out)
		}
	}

	// update: only the flags given change; the audit keeps before and after.
	code, out, errOut = e.key(`{"max_servers": 5, "max_active_services": -1, "dashboard_staff_seats": -1}`,
		"update", l.ID, "-entitlements", "-", "-grace-days", "0", "-reason", "upgrade to five servers")
	if code != 0 || !strings.Contains(out, "Updated "+l.ID) {
		t.Fatalf("update: %d %s %s", code, out, errOut)
	}
	got, err := e.st.LicenseByID(context.Background(), e.st.db, l.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Entitlements.MaxServers != 5 || got.GraceDays != 0 || got.Customer != "Acme VPN" || got.Tier != "standard" ||
		got.EndsAt == nil || !got.EndsAt.Equal(*l.EndsAt) {
		t.Fatalf("after update: %+v", got)
	}
	rows, err := e.st.Audit(context.Background(), e.st.db, l.ID, 1)
	if err != nil || len(rows) != 1 || rows[0].Action != "license.update" || rows[0].Actor != "cli:test" {
		t.Fatalf("audit: %+v %v", rows, err)
	}
	if rows[0].Details["reason"] != "upgrade to five servers" || rows[0].Details["before"] == nil || rows[0].Details["after"] == nil {
		t.Fatalf("audit details: %+v", rows[0].Details)
	}
	// An ended license can be renewed, and its other fields changed
	// without touching the end.
	e.clock.Advance(101 * 24 * time.Hour)
	if code, _, errOut := e.key("", "update", l.ID, "-customer", "Acme VPN Ltd", "-reason", "renamed"); code != 0 {
		t.Fatalf("rename an ended license: %d %s", code, errOut)
	}
	if code, _, errOut := e.key("", "update", l.ID, "-ends-at", "2020-01-01", "-reason", "typo"); code != 1 || !strings.Contains(errOut, "past") {
		t.Fatalf("end in the past: %d %s", code, errOut)
	}
	if code, _, errOut := e.key("", "update", l.ID, "-days", "30", "-reason", "renewed"); code != 0 {
		t.Fatalf("renew: %d %s", code, errOut)
	}
	if r := e.activate(key, moved, "moved.beaver.example"); r.Status != http.StatusOK {
		t.Fatalf("activate after renewal: %d %s", r.Status, r.Error)
	}
	if r := e.activate(key, install, "panel.beaver.example"); r.Status != http.StatusOK {
		t.Fatalf("move back after renewal: %d %s", r.Status, r.Error)
	}
}
