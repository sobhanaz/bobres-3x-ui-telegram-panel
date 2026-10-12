package licensesrv

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMigrateTwiceAndConcurrently(t *testing.T) {
	st := testStore(t) // migrated once already
	// A fresh database, migrated by three processes starting together, then
	// again.
	dsn := freshDatabase(t)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Migrate(context.Background(), dsn, nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	if err := Migrate(context.Background(), dsn, nil); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	if err := Migrate(context.Background(), testdb.DSN(t), nil); err != nil {
		t.Fatalf("migrate the shared database again: %v", err)
	}
	var n int
	if err := st.db.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_tables WHERE schemaname = 'licensesrv'
		AND tablename IN ('licenses', 'activations', 'audit', 'goose_db_version')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("licensesrv has %d of the 4 tables", n)
	}
	// Not in the customer schemas.
	if err := st.db.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_tables WHERE schemaname IN ('core', 'payments', 'provisioner') AND tablename IN ('licenses', 'activations')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("license server tables leaked into a customer schema: %d", n)
	}
}

func TestAuditIsAppendOnly(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, _ := e.newLicense()
	ctx := context.Background()
	if _, err := e.st.db.Exec(ctx, `UPDATE licensesrv.audit SET action = 'x' WHERE license_id = $1`, l.ID); err == nil {
		t.Fatal("an audit row was changed")
	}
	if _, err := e.st.db.Exec(ctx, `DELETE FROM licensesrv.audit WHERE license_id = $1`, l.ID); err == nil {
		t.Fatal("an audit row was deleted")
	}
	if _, err := e.st.db.Exec(ctx, `TRUNCATE licensesrv.audit`); err == nil {
		t.Fatal("the audit was truncated")
	}
}

func TestActivateIssuesShortLivedBoundToken(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense("-days", "365")
	install, domain := newInstall(), "panel.acme.example"
	now := e.clock.Now()

	// The key as a customer might paste it: lower case, spaces around.
	r := e.activate("  "+strings.ToLower(key)+"\n", strings.ToUpper(install), "Panel.Acme.Example.")
	if r.Status != http.StatusOK || r.Token == "" {
		t.Fatalf("activate: %d %s %s", r.Status, r.Error, r.Message)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control %q", cc)
	}
	tok := e.verify(r.Token, install, domain)
	if tok.KeyID != testKid || tok.LicenseID != l.ID || tok.Customer != "Acme VPN" || tok.Tier != license.TierStandard {
		t.Fatalf("token: %+v", tok)
	}
	if !tok.IssuedAt.Equal(now.Add(-IssuedBackdate)) {
		t.Fatalf("issued_at %s, want 5 minutes before %s", tok.IssuedAt, now)
	}
	if !tok.ExpiresAt.Equal(now.Add(TokenLifetime)) {
		t.Fatalf("expires_at %s, want now+30d", tok.ExpiresAt)
	}
	if tok.GraceDays != DefaultGraceDays {
		t.Fatalf("grace_days %d", tok.GraceDays)
	}
	if tok.LicenseEndsAt == nil || !tok.LicenseEndsAt.Equal(now.Add(365*24*time.Hour)) {
		t.Fatalf("license_ends_at %v", tok.LicenseEndsAt)
	}
	ent := tok.Entitlements
	if ent.MaxServers != 2 || ent.MaxActiveServices != license.Unlimited || !ent.Resellers || !ent.HasGateway("zarinpal") ||
		ent.DashboardStaffSeat != 3 || ent.UpdatesUntil != "2027-09-30" {
		t.Fatalf("entitlements %+v", ent)
	}
	// The install's view: active now, refused on another domain or install.
	if st, err := tok.State(now); err != nil || st != license.Active {
		t.Fatalf("state %v %v", st, err)
	}
	if _, err := license.Verify(e.ring, r.Token, license.Options{Domain: "other.example", InstallID: install, RequireBound: true}); err == nil {
		t.Fatal("token verified for another domain")
	}
	if _, err := license.Verify(e.ring, r.Token, license.Options{Domain: domain, InstallID: newInstall(), RequireBound: true}); err == nil {
		t.Fatal("token verified for another install")
	}

	act, err := e.st.ActiveActivation(context.Background(), e.st.db, l.ID)
	if err != nil || act == nil {
		t.Fatalf("active activation: %v %v", act, err)
	}
	if act.InstallID != install || act.Domain != domain || act.Counted || act.LastVersion != "0.1.0" {
		t.Fatalf("activation %+v", act)
	}
}

func TestTokenNeverOutlivesTheLicense(t *testing.T) {
	e := newEnv(t, envOpts{})
	_, key := e.newLicense("-days", "10")
	install := newInstall()
	r := e.activate(key, install, "a.example")
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d %s", r.Status, r.Message)
	}
	tok := e.verify(r.Token, install, "a.example")
	want := e.clock.Now().Add(10 * 24 * time.Hour)
	if !tok.ExpiresAt.Equal(want) || tok.LicenseEndsAt == nil || !tok.LicenseEndsAt.Equal(want) {
		t.Fatalf("expires_at %s, license_ends_at %v: want both %s", tok.ExpiresAt, tok.LicenseEndsAt, want)
	}

	// A license without an end: 30 days at a time, no license_ends_at.
	_, key = e.newLicense("-no-end", "-grace-days", "7")
	r = e.activate(key, install, "a.example")
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d %s", r.Status, r.Message)
	}
	tok = e.verify(r.Token, install, "a.example")
	if !tok.ExpiresAt.Equal(e.clock.Now().Add(TokenLifetime)) || tok.LicenseEndsAt != nil || tok.GraceDays != 7 {
		t.Fatalf("perpetual: expires %s ends %v grace %d", tok.ExpiresAt, tok.LicenseEndsAt, tok.GraceDays)
	}
}

func TestSamePairIsARefreshNotAMove(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense()
	install := newInstall()
	first := e.activate(key, install, "a.example")
	e.clock.Advance(time.Hour)
	again := e.activate(key, install, "a.example")
	if first.Status != http.StatusOK || again.Status != http.StatusOK {
		t.Fatalf("activate: %d %d", first.Status, again.Status)
	}
	if t1, t2 := e.verify(first.Token, install, "a.example"), e.verify(again.Token, install, "a.example"); !t2.IssuedAt.After(t1.IssuedAt) {
		t.Fatal("the second token is not newer")
	}
	acts, err := e.st.Activations(context.Background(), e.st.db, l.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || acts[0].Counted || !acts[0].LastSeenAt.Equal(e.clock.Now()) {
		t.Fatalf("activations: %+v", acts)
	}
}

func TestReactivationLimit(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense("-days", "800")
	installs := []string{newInstall(), newInstall(), newInstall(), newInstall(), newInstall()}
	tokens := make([]string, len(installs))
	start := e.clock.Now()
	// The first activation is free, then three moves are allowed.
	for i := range 4 {
		r := e.activate(key, installs[i], "panel.example")
		if r.Status != http.StatusOK {
			t.Fatalf("activation %d: %d %s %s", i, r.Status, r.Error, r.Message)
		}
		tokens[i] = r.Token
		e.clock.Advance(24 * time.Hour)
	}
	r := e.activate(key, installs[4], "panel.example")
	if r.Status != http.StatusConflict || r.Error != "reactivation_limit" {
		t.Fatalf("fourth move: %d %s", r.Status, r.Error)
	}
	// The first counted move was a day after the start; it frees up a year later.
	nextMove := start.Add(24 * time.Hour).Add(ReactivationWindow).Format(time.DateOnly)
	if !strings.Contains(r.Message, nextMove) {
		t.Fatalf("message %q does not name %s", r.Message, nextMove)
	}
	// Moving back to an earlier install is a move too.
	if r := e.activate(key, installs[0], "panel.example"); r.Status != http.StatusConflict {
		t.Fatalf("moving back: %d %s", r.Status, r.Error)
	}
	// A new domain on the same install is a new pair.
	if r := e.activate(key, installs[3], "renamed.example"); r.Status != http.StatusConflict {
		t.Fatalf("new domain: %d %s", r.Status, r.Error)
	}
	// The install that holds the license still activates and refreshes.
	if r := e.activate(key, installs[3], "panel.example"); r.Status != http.StatusOK {
		t.Fatalf("current install: %d %s", r.Status, r.Error)
	}
	if r := e.refresh(tokens[3]); r.Status != http.StatusOK {
		t.Fatalf("current install refresh: %d %s", r.Status, r.Error)
	}
	// Older installs lost the license: their tokens no longer refresh.
	for i := range 3 {
		if r := e.refresh(tokens[i]); r.Status != http.StatusConflict || r.Error != "not_active" {
			t.Fatalf("old install %d refresh: %d %s", i, r.Status, r.Error)
		}
	}

	ctx := context.Background()
	acts, err := e.st.Activations(ctx, e.st.db, l.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	active, counted := 0, 0
	for _, a := range acts {
		if a.DeactivatedAt == nil {
			active++
		}
		if a.Counted {
			counted++
		}
	}
	if len(acts) != 4 || active != 1 || counted != 3 {
		t.Fatalf("activations %d, active %d, counted %d", len(acts), active, counted)
	}
	if !hasAudit(t, e, l.ID, "activation.refused") || !hasAudit(t, e, l.ID, "activation.move") || !hasAudit(t, e, l.ID, "activation.create") {
		t.Fatal("audit is missing activation rows")
	}

	// A year after the first move, one move is free again (and only one).
	e.clock.Advance(start.Add(24*time.Hour).Add(ReactivationWindow).Sub(e.clock.Now()) + time.Second)
	if r := e.activate(key, installs[4], "panel.example"); r.Status != http.StatusOK {
		t.Fatalf("after a year: %d %s %s", r.Status, r.Error, r.Message)
	}
	if r := e.activate(key, installs[0], "panel.example"); r.Status != http.StatusConflict {
		t.Fatalf("second move after a year: %d %s", r.Status, r.Error)
	}
}

func TestConcurrentActivationsKeepOneActive(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense()
	const n = 6
	var wg sync.WaitGroup
	codes := make(chan int, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- e.activate(key, newInstall(), "race.example").Status
		}()
	}
	wg.Wait()
	close(codes)
	ok, limited := 0, 0
	for c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			limited++
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if ok != 1+MaxReactivations || limited != n-ok {
		t.Fatalf("%d activated, %d refused", ok, limited)
	}
	var active int
	if err := e.st.db.QueryRow(context.Background(), `SELECT count(*) FROM licensesrv.activations WHERE license_id = $1 AND deactivated_at IS NULL`, l.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("%d active activations", active)
	}
}

func TestRefresh(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense("-days", "365")
	install := newInstall()
	r := e.activate(key, install, "a.example")
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d", r.Status)
	}
	old := e.verify(r.Token, install, "a.example")

	e.clock.Advance(20 * 24 * time.Hour)
	r2 := e.refresh(r.Token)
	if r2.Status != http.StatusOK {
		t.Fatalf("refresh: %d %s %s", r2.Status, r2.Error, r2.Message)
	}
	fresh := e.verify(r2.Token, install, "a.example")
	if fresh.LicenseID != old.LicenseID || !fresh.IssuedAt.After(old.IssuedAt) || !fresh.ExpiresAt.Equal(e.clock.Now().Add(TokenLifetime)) {
		t.Fatalf("refreshed token: %+v", fresh)
	}
	act, err := e.st.ActiveActivation(context.Background(), e.st.db, l.ID)
	if err != nil || act.LastVersion != "0.1.1" || !act.LastSeenAt.Equal(e.clock.Now()) {
		t.Fatalf("activation after refresh: %+v %v", act, err)
	}

	// An expired token still refreshes while its binding holds (an install
	// that was offline a long time).
	e.clock.Advance(60 * 24 * time.Hour)
	if r := e.refresh(r.Token); r.Status != http.StatusOK {
		t.Fatalf("refresh with an expired token: %d %s", r.Status, r.Error)
	}

	// A renewal or an upgrade reaches the install at its next refresh.
	if code, _, errOut := e.key("", "update", l.ID, "-tier", "pro", "-ends-at", "2029-01-01", "-reason", "renewed for two years"); code != 0 {
		t.Fatalf("key update: %d %s", code, errOut)
	}
	r3 := e.refresh(r2.Token)
	if r3.Status != http.StatusOK {
		t.Fatalf("refresh after update: %d", r3.Status)
	}
	up := e.verify(r3.Token, install, "a.example")
	if up.Tier != license.TierPro || up.LicenseEndsAt == nil || up.LicenseEndsAt.Format(time.DateOnly) != "2029-01-01" {
		t.Fatalf("update not in the token: %+v", up)
	}
}

func TestRefreshRefusesForeignTokens(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense()
	install := newInstall()
	r := e.activate(key, install, "a.example")
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d", r.Status)
	}
	good := e.verify(r.Token, install, "a.example")

	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherSigner, err := NewSigner(testKid, otherPriv)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := otherSigner.Sign(good)
	if err != nil {
		t.Fatal(err)
	}
	unbound := good
	unbound.InstallID = ""
	unboundTok, err := e.signer.Sign(unbound)
	if err != nil {
		t.Fatal(err)
	}
	ghost := good
	ghost.LicenseID = "lic_doesnotexist"
	ghostTok, err := e.signer.Sign(ghost)
	if err != nil {
		t.Fatal(err)
	}
	tampered := r.Token[:len(r.Token)-4] + "AAAA"

	for name, tc := range map[string]struct {
		token  string
		status int
		code   string
	}{
		"other key":       {forged, http.StatusForbidden, "invalid_token"},
		"tampered":        {tampered, http.StatusForbidden, "invalid_token"},
		"garbage":         {"not.a.token", http.StatusForbidden, "invalid_token"},
		"no install id":   {unboundTok, http.StatusForbidden, "invalid_token"},
		"unknown license": {ghostTok, http.StatusForbidden, "invalid_token"},
		"empty":           {"", http.StatusBadRequest, "invalid"},
		"too long":        {strings.Repeat("a", maxTokenLen+1), http.StatusBadRequest, "invalid"},
	} {
		if got := e.refresh(tc.token); got.Status != tc.status || got.Error != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, got.Status, got.Error, tc.status, tc.code)
		}
	}
	// A token for this license but another binding: the active row decides.
	other := good
	other.Domain = "elsewhere.example"
	otherTok, err := e.signer.Sign(other)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.refresh(otherTok); got.Status != http.StatusConflict || got.Error != "not_active" {
		t.Fatalf("other binding: %d %s", got.Status, got.Error)
	}
	_ = l
}

func TestRevocation(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense()
	install := newInstall()
	r := e.activate(key, install, "a.example")
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d", r.Status)
	}
	if code, _, errOut := e.key("", "revoke", l.ID); code != 1 || !strings.Contains(errOut, "-reason is required") {
		t.Fatalf("revoke without a reason: exit %d %s", code, errOut)
	}
	code, out, errOut := e.key("", "revoke", l.ID, "-reason", "chargeback")
	if code != 0 || !strings.Contains(out, "Revoked "+l.ID) {
		t.Fatalf("revoke: %d %s %s", code, out, errOut)
	}
	if code, _, errOut := e.key("", "revoke", "-reason", "again", l.ID); code != 1 || !strings.Contains(errOut, "already revoked") {
		t.Fatalf("revoke twice: %d %s", code, errOut)
	}
	if got := e.refresh(r.Token); got.Status != http.StatusForbidden || got.Error != "revoked" {
		t.Fatalf("refresh after revoke: %d %s", got.Status, got.Error)
	}
	if got := e.activate(key, install, "a.example"); got.Status != http.StatusForbidden || got.Error != "revoked" {
		t.Fatalf("activate after revoke: %d %s", got.Status, got.Error)
	}
	if got := e.activate(key, newInstall(), "b.example"); got.Status != http.StatusForbidden || got.Error != "revoked" {
		t.Fatalf("move after revoke: %d %s", got.Status, got.Error)
	}
	if code, _, errOut := e.key("", "update", l.ID, "-tier", "pro", "-reason", "x"); code != 1 || !strings.Contains(errOut, "revoked") {
		t.Fatalf("update a revoked license: %d %s", code, errOut)
	}
	got, err := e.st.LicenseByID(context.Background(), e.st.db, l.ID, false)
	if err != nil || got.Status != StatusRevoked || got.Reason != "chargeback" || got.RevokedAt == nil {
		t.Fatalf("stored: %+v %v", got, err)
	}
	if !hasAudit(t, e, l.ID, "license.revoke") {
		t.Fatal("no license.revoke audit row")
	}
}

func TestEndedLicense(t *testing.T) {
	e := newEnv(t, envOpts{})
	_, key := e.newLicense("-days", "3")
	install := newInstall()
	r := e.activate(key, install, "a.example")
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d", r.Status)
	}
	e.clock.Advance(3 * 24 * time.Hour)
	if got := e.refresh(r.Token); got.Status != http.StatusForbidden || got.Error != "expired" {
		t.Fatalf("refresh after the end: %d %s", got.Status, got.Error)
	}
	if got := e.activate(key, install, "a.example"); got.Status != http.StatusForbidden || got.Error != "expired" {
		t.Fatalf("activate after the end: %d %s", got.Status, got.Error)
	}
	// The install's last token decays through grace, as for revocation.
	tok := e.verify(r.Token, install, "a.example")
	if st, _ := tok.State(e.clock.Now()); st != license.Grace {
		t.Fatalf("state after the end: %v", st)
	}
}

func TestKeysAreStoredOnlyAsHashes(t *testing.T) {
	e := newEnv(t, envOpts{})
	l, key := e.newLicense()
	if !keyRe.MatchString(key) {
		t.Fatalf("key %q is not BOBRES-XXXX-...", key)
	}
	if r := e.activate(key, newInstall(), "a.example"); r.Status != http.StatusOK {
		t.Fatalf("activate: %d", r.Status)
	}
	ctx := context.Background()
	var hash string
	var leaks int
	if err := e.st.db.QueryRow(ctx, `SELECT key_hash FROM licensesrv.licenses WHERE id = $1`, l.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != HashKey(key) || len(hash) != 64 {
		t.Fatalf("key_hash %q is not the key's SHA-256", hash)
	}
	secret := strings.TrimPrefix(key, "BOBRES-")
	if err := e.st.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM licensesrv.licenses x WHERE row_to_json(x)::text LIKE '%' || $1 || '%')
		     + (SELECT count(*) FROM licensesrv.audit x WHERE row_to_json(x)::text LIKE '%' || $1 || '%')
		     + (SELECT count(*) FROM licensesrv.activations x WHERE row_to_json(x)::text LIKE '%' || $1 || '%')`, secret).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	if leaks != 0 {
		t.Fatalf("the key is stored in %d rows", leaks)
	}
	// Two keys never collide, and normalizing is stable.
	k2, err := NewKey()
	if err != nil || k2 == key || HashKey(k2) == HashKey(key) {
		t.Fatalf("NewKey: %v", err)
	}
	if n, ok := NormalizeKey(" " + strings.ToLower(key) + " \n"); !ok || n != key {
		t.Fatalf("NormalizeKey: %q %v", n, ok)
	}
	for _, bad := range []string{"", "BOBRES", "BOBRES-AAAA", key + "-AAAA", strings.Replace(key, "BOBRES", "XOBRES", 1), key[:len(key)-1] + "1"} {
		if _, ok := NormalizeKey(bad); ok {
			t.Errorf("NormalizeKey accepted %q", bad)
		}
	}
}

func hasAudit(t *testing.T, e *env, licenseID, action string) bool {
	t.Helper()
	rows, err := e.st.Audit(context.Background(), e.st.db, licenseID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Action == action {
			return true
		}
	}
	return false
}

// freshDatabase makes an empty database next to the test database and
// drops it when the test ends.
func freshDatabase(t *testing.T) string {
	t.Helper()
	base := testdb.DSN(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx) //nolint:errcheck // test connection
	name := fmt.Sprintf("bobres_test_licsrv_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			t.Logf("drop %s: %v", name, err)
			return
		}
		defer c.Close(context.Background()) //nolint:errcheck // test connection
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
