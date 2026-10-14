package licensesrv

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
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
		"no args":                {nil, "", 2, "usage"},
		"unknown sub":            {[]string{"delete"}, "", 2, "usage"},
		"no entitlements":        {[]string{"create", "-customer", "A", "-tier", "gold", "-days", "30"}, "", 2, "usage"},
		"no tier at all":         {[]string{"create", "-customer", "A", "-days", "30"}, "", 2, "usage"},
		"preset needs end":       {[]string{"create", "-customer", "A", "-tier", "standard"}, "", 1, "exactly one of"},
		"two ends with months":   {[]string{"create", "-customer", "A", "-tier", "standard", "-months", "1", "-days", "3"}, "", 1, "exactly one of"},
		"months too long":        {[]string{"create", "-customer", "A", "-tier", "standard", "-months", "121"}, "", 1, "-months"},
		"negative months":        {[]string{"create", "-customer", "A", "-tier", "standard", "-months", "-1"}, "", 1, "-months"},
		"custom trial needs end": {[]string{"create", "-customer", "A", "-tier", "trial", "-entitlements", ent}, "", 1, "exactly one of"},
		"no end":                 {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent}, "", 1, "exactly one of"},
		"two ends":               {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "3", "-no-end"}, "", 1, "exactly one of"},
		"bad tier":               {[]string{"create", "-customer", "A", "-tier", "gold", "-entitlements", ent, "-days", "3"}, "", 1, "-tier"},
		"no customer":            {[]string{"create", "-tier", "pro", "-entitlements", ent, "-days", "3"}, "", 1, "-customer"},
		"big grace":              {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "3", "-grace-days", "91"}, "", 1, "-grace-days"},
		"past end":               {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-ends-at", "2020-01-01"}, "", 1, "past"},
		"bad date":               {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-ends-at", "next year"}, "", 1, "-ends-at"},
		"negative days":          {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "-3"}, "", 1, "-days"},
		"endless days":           {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "200000"}, "", 1, "-days"},
		"misspelt field":         {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "-", "-days", "3"}, `{"max_server": 1}`, 1, "unknown field"},
		"limit below -1":         {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "-", "-days", "3"}, `{"max_servers": -2}`, 1, "limits"},
		"bad gateway":            {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "-", "-days", "3"}, `{"gateways": ["Zarin Pal"]}`, 1, "gateway"},
		"bad updates_until":      {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "-", "-days", "3"}, `{"updates_until": "soon"}`, 1, "updates_until"},
		"missing file":           {[]string{"create", "-customer", "A", "-tier", "pro", "-entitlements", "/nonexistent/ent.json", "-days", "3"}, "", 1, "entitlements"},
		"stray argument":         {[]string{"create", "extra", "-customer", "A", "-tier", "pro", "-entitlements", ent, "-days", "3"}, "", 2, "usage"},
		"unknown flag":           {[]string{"create", "-colour", "red"}, "", 2, "usage"},
		"show without id":        {[]string{"show"}, "", 2, "usage"},
		"show unknown":           {[]string{"show", "lic_nope"}, "", 1, "no license lic_nope"},
		"revoke unknown":         {[]string{"revoke", "lic_nope", "-reason", "x"}, "", 1, "no license lic_nope"},
		"update unknown":         {[]string{"update", "lic_nope", "-reason", "x", "-tier", "pro"}, "", 1, "no license lic_nope"},
		"update no reason":       {[]string{"update", "lic_nope", "-tier", "pro"}, "", 1, "-reason"},
		"list bad status":        {[]string{"list", "-status", "gone"}, "", 2, "usage"},
		"stdin entitlements":     {[]string{"create", "-customer", "B", "-tier", "trial", "-entitlements", "-", "-days", "14"}, `{"max_servers": 1}`, 0, "Created lic_"},
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

// Without -entitlements a license takes its tier's preset, and the trial its
// fixed length; -months makes a subscription period.
func TestKeyCreateFromAPreset(t *testing.T) {
	e := newEnv(t, envOpts{})
	presets, err := license.LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	issue := func(args ...string) (*License, string) {
		t.Helper()
		code, out, errOut := e.key("", append([]string{"create", "-customer", "Preset Co"}, args...)...)
		if code != 0 || !strings.Contains(errOut, "preset") {
			t.Fatalf("create %v: %d %s", args, code, errOut)
		}
		l, err := e.st.LicenseByKeyHash(context.Background(), e.st.db, HashKey(strings.TrimSpace(out)), false)
		if err != nil {
			t.Fatal(err)
		}
		return l, errOut
	}

	std, _ := presets.Get(license.TierStandard)
	l, _ := issue("-tier", "standard", "-months", "1")
	if !reflect.DeepEqual(l.Entitlements, std.Entitlements) || l.Tier != "standard" || l.GraceDays != std.GraceDays ||
		l.EndsAt == nil || !l.EndsAt.Equal(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("standard: %+v", l)
	}
	pro, _ := presets.Get(license.TierPro)
	l, _ = issue("-tier", "pro", "-months", "12", "-grace-days", "30")
	if !reflect.DeepEqual(l.Entitlements, pro.Entitlements) || !l.Entitlements.WhiteLabel || l.GraceDays != 30 ||
		!l.EndsAt.Equal(time.Date(2027, 10, 12, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("pro: %+v", l)
	}
	// The trial: 30 days from now, no grace, without white label.
	l, _ = issue("-tier", "trial")
	if l.Entitlements.WhiteLabel || l.GraceDays != 0 || l.EndsAt == nil || !l.EndsAt.Equal(time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("trial: %+v", l)
	}
	// An end the vendor gives wins over the trial's default.
	if l, _ = issue("-tier", "trial", "-days", "7"); !l.EndsAt.Equal(time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("short trial: %+v", l)
	}
}

// A new tier takes its preset's entitlements; the same tier, or a tier
// given with -entitlements, does not.
func TestKeyUpdateTierTakesItsPreset(t *testing.T) {
	e := newEnv(t, envOpts{})
	presets, _ := license.LoadPresets()
	pro, _ := presets.Get(license.TierPro)
	l, _ := e.newLicense("-months", "1") // standard, with custom entitlements (two servers)

	// The same tier again changes nothing about the custom entitlements.
	if code, _, errOut := e.key("", "update", l.ID, "-tier", "standard", "-reason", "same tier"); code != 0 {
		t.Fatalf("same tier: %d %s", code, errOut)
	}
	got, _ := e.st.LicenseByID(context.Background(), e.st.db, l.ID, false)
	if got.Entitlements.MaxServers != 2 {
		t.Fatalf("custom entitlements were reset: %+v", got.Entitlements)
	}
	// A new tier takes the preset.
	code, _, errOut := e.key("", "update", l.ID, "-tier", "pro", "-months", "12", "-reason", "upgrade to pro")
	if code != 0 || !strings.Contains(errOut, "pro preset") {
		t.Fatalf("upgrade: %d %s", code, errOut)
	}
	if got, _ = e.st.LicenseByID(context.Background(), e.st.db, l.ID, false); got.Tier != "pro" ||
		!reflect.DeepEqual(got.Entitlements, pro.Entitlements) || !got.EndsAt.Equal(time.Date(2027, 10, 12, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("after the upgrade: %+v", got)
	}
	// -entitlements wins over the preset.
	if code, _, errOut := e.key(`{"max_servers": 7, "max_active_services": -1, "dashboard_staff_seats": -1}`,
		"update", l.ID, "-tier", "standard", "-entitlements", "-", "-reason", "custom deal"); code != 0 {
		t.Fatalf("custom: %d %s", code, errOut)
	}
	if got, _ = e.st.LicenseByID(context.Background(), e.st.db, l.ID, false); got.Tier != "standard" || got.Entitlements.MaxServers != 7 {
		t.Fatalf("custom deal: %+v", got)
	}
}

// -extend-months renews from the current end (a customer who pays early loses
// nothing) or from now (one who pays late gets a full period).
func TestKeyUpdateExtendMonths(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, _ := e.newLicense("-ends-at", "2026-10-31") // the clock is 2026-10-12 09:00
	ends := func() time.Time {
		t.Helper()
		got, err := e.st.LicenseByID(context.Background(), e.st.db, l.ID, false)
		if err != nil || got.EndsAt == nil {
			t.Fatalf("license: %+v %v", got, err)
		}
		return *got.EndsAt
	}
	// Early: from the end, clamped to the end of a shorter month.
	if code, out, errOut := e.key("", "update", l.ID, "-extend-months", "1", "-reason", "paid for November"); code != 0 {
		t.Fatalf("extend: %d %s %s", code, out, errOut)
	}
	if got := ends(); !got.Equal(time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("early renewal ends %s", got)
	}
	// Late: the license ended 40 days ago, so the new period starts now.
	e.clock.Advance(80 * 24 * time.Hour) // 2026-12-31 09:00
	if code, _, errOut := e.key("", "update", l.ID, "-extend-months", "1", "-reason", "paid late"); code != 0 {
		t.Fatalf("late renewal: %d %s", code, errOut)
	}
	if got := ends(); !got.Equal(time.Date(2027, 1, 31, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("late renewal ends %s", got)
	}
	// Refusals: with another end flag, out of range, and a license without an end.
	for name, args := range map[string][]string{
		"with -days":   {"-extend-months", "1", "-days", "5"},
		"too long":     {"-extend-months", "121"},
		"negative":     {"-extend-months", "-1"},
		"with -no-end": {"-extend-months", "1", "-no-end"},
	} {
		if code, _, errOut := e.key("", append([]string{"update", l.ID, "-reason", "x"}, args...)...); code != 1 || !strings.Contains(errOut, "-extend-months") {
			t.Errorf("%s: %d %s", name, code, errOut)
		}
	}
	forever, _ := e.newLicense("-no-end")
	if code, _, errOut := e.key("", "update", forever.ID, "-extend-months", "1", "-reason", "x"); code != 1 || !strings.Contains(errOut, "no end date") {
		t.Errorf("no end: %d %s", code, errOut)
	}
}

// A license whose max_active_services is 0 or missing is made all the same,
// and the vendor is warned that the install will floor it at the free tier's
// 25 (key create and key update; a preset never warns).
func TestKeyWarnsWhenTheFloorApplies(t *testing.T) {
	e := newEnv(t, envOpts{})
	const warning = "WARNING: max_active_services is 0 or missing: the install will floor it at the free tier's 25"
	// Left out of the file: created, with the warning on stderr, never stdout.
	code, out, errOut := e.key(`{"max_servers": 2, "dashboard_staff_seats": 3}`,
		"create", "-customer", "Forgetful", "-tier", "standard", "-entitlements", "-", "-months", "1")
	if code != 0 || !strings.Contains(errOut, "Created lic_") || !strings.Contains(errOut, warning) || strings.Contains(out, "WARNING") {
		t.Fatalf("create without the limit: %d out %q err %q", code, out, errOut)
	}
	if !keyRe.MatchString(strings.TrimSpace(out)) {
		t.Fatalf("stdout must still be the key: %q", out)
	}
	l, err := e.st.LicenseByKeyHash(context.Background(), e.st.db, HashKey(strings.TrimSpace(out)), false)
	if err != nil || l.Entitlements.MaxActiveServices != 0 {
		t.Fatalf("stored: %+v %v", l, err)
	}
	// Set to a count or to unlimited: no warning. A preset: none either.
	for name, ent := range map[string]string{
		"a count":   `{"max_servers": 2, "max_active_services": 100, "dashboard_staff_seats": 3}`,
		"unlimited": `{"max_servers": 2, "max_active_services": -1, "dashboard_staff_seats": 3}`,
	} {
		if code, _, errOut := e.key(ent, "create", "-customer", "Careful", "-tier", "standard", "-entitlements", "-", "-months", "1"); code != 0 || strings.Contains(errOut, "WARNING") {
			t.Errorf("%s: %d %q", name, code, errOut)
		}
	}
	if code, _, errOut := e.key("", "create", "-customer", "Preset Co", "-tier", "standard", "-months", "1"); code != 0 || strings.Contains(errOut, "WARNING") {
		t.Errorf("preset: %d %q", code, errOut)
	}

	// update: the warning follows the entitlements the license has after it.
	good, _ := e.newLicense("-months", "1")
	code, out, errOut = e.key(`{"max_servers": 5, "dashboard_staff_seats": 3}`,
		"update", good.ID, "-entitlements", "-", "-reason", "dropped the limit by mistake")
	if code != 0 || !strings.Contains(out, "Updated "+good.ID) || !strings.Contains(errOut, warning) {
		t.Fatalf("update to zero: %d out %q err %q", code, out, errOut)
	}
	code, _, errOut = e.key(`{"max_servers": 5, "max_active_services": 200, "dashboard_staff_seats": 3}`,
		"update", good.ID, "-entitlements", "-", "-reason", "fixed")
	if code != 0 || strings.Contains(errOut, "WARNING") {
		t.Fatalf("update with the limit: %d %q", code, errOut)
	}
}
