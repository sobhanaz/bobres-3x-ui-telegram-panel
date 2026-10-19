package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// adminEnv is coreEnv for an install whose owner is Telegram id 4242 and
// whose master key is set; the tables the command writes are emptied.
func adminEnv(t *testing.T) *store.Store {
	t.Helper()
	coreEnv(t)
	t.Setenv("BOBRES_ADMIN_TELEGRAM_ID", "4242")
	t.Setenv("BOBRES_MASTER_KEY", strings.Repeat("k", 32))
	cfg, err := config.LoadCore()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := testdb.Truncate(context.Background(), st.DB(), `core.web_sessions, core.staff_credentials, core.audit_log, core.users`); err != nil {
		t.Fatal(err)
	}
	return st
}

var passwordLine = regexp.MustCompile(`(?m)^Password: ([A-Za-z0-9]{16})$`)

func runAdminPassword(args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = adminPasswordCmd(args, &o, &e)
	return code, o.String(), e.String()
}

// The owner never sent /start: the command creates the account, makes it the
// owner and prints a random password once; the stored hash matches it, the
// login must change it, and the audit entry (and stderr) never hold it.
func TestAdminPasswordWithoutPriorStart(t *testing.T) {
	st := adminEnv(t)
	code, out, errOut := runAdminPassword()
	m := passwordLine.FindStringSubmatch(out)
	if code != 0 || m == nil || !strings.Contains(out, "Username: owner\n") || !strings.Contains(out, "Shown once") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	pw := m[1]
	if strings.Count(out, pw) != 1 || errOut != "" {
		t.Fatalf("the password must be printed once, nothing on stderr:\n%s\n%s", out, errOut)
	}
	ctx := context.Background()
	u, err := st.GetUserByTelegramID(ctx, st.DB(), 4242)
	if err != nil || u.Role != "owner" {
		t.Fatalf("owner account: %+v %v", u, err)
	}
	creds, err := st.CredentialsFor(ctx, st.DB(), u.ID)
	if err != nil || creds.Username != "owner" || !creds.MustChange || creds.TOTPConfirmedAt != nil ||
		!webauth.CheckPassword(creds.PasswordHash, pw) || strings.Contains(creds.PasswordHash, pw) {
		t.Fatalf("credentials: %+v %v", creds, err)
	}
	var detail string
	if err := st.DB().QueryRow(ctx, `SELECT coalesce(string_agg(coalesce(before::text,'') || coalesce(after::text,'') || action, ' '), '')
		FROM core.audit_log WHERE action = 'dashboard.password.reset_cli'`).Scan(&detail); err != nil || detail == "" || strings.Contains(detail, pw) {
		t.Fatalf("audit: %q %v", detail, err)
	}
}

// Running it again is safe: same account and username, a new password, the
// old one and every session gone, a failed-login lock lifted.
func TestAdminPasswordIsIdempotentAndResets(t *testing.T) {
	st := adminEnv(t)
	ctx := context.Background()
	_, out1, _ := runAdminPassword()
	pw1 := passwordLine.FindStringSubmatch(out1)[1]
	u, _ := st.GetUserByTelegramID(ctx, st.DB(), 4242)

	// A live session, a lock, and a finished setup (authenticator confirmed).
	if err := st.CreateSession(ctx, st.DB(), &store.WebSession{IDHash: []byte("sess"), UserID: u.ID, CSRFToken: "c", Method: "password",
		ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := st.RecordLoginFailure(ctx, st.DB(), u.ID, 15*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB().Exec(ctx, `UPDATE core.staff_credentials SET totp_secret_enc = '\x01', totp_confirmed_at = now(), must_change = false WHERE user_id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}

	code, out2, errOut := runAdminPassword()
	m := passwordLine.FindStringSubmatch(out2)
	if code != 0 || m == nil || m[1] == pw1 {
		t.Fatalf("second run: %d %s %s", code, out2, errOut)
	}
	creds, err := st.CredentialsFor(ctx, st.DB(), u.ID)
	if err != nil || creds.Username != "owner" || !creds.MustChange || creds.TOTPConfirmedAt != nil || creds.FailedAttempts != 0 ||
		creds.LockedUntil != nil || webauth.CheckPassword(creds.PasswordHash, pw1) || !webauth.CheckPassword(creds.PasswordHash, m[1]) {
		t.Fatalf("credentials after the reset: %+v %v", creds, err)
	}
	if s, err := st.ActiveSession(ctx, st.DB(), []byte("sess"), time.Hour); err == nil && s != nil {
		t.Fatal("a session survived the reset")
	}
	var users int
	if err := st.DB().QueryRow(ctx, `SELECT count(*) FROM core.users`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("users: %d %v", users, err)
	}
}

func TestAdminPasswordRefusals(t *testing.T) {
	adminEnv(t)
	if code, _, errOut := runAdminPassword("extra"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("an argument: %d %s", code, errOut)
	}
	t.Setenv("BOBRES_MASTER_KEY", "")
	if code, out, errOut := runAdminPassword(); code != 1 || out != "" || !strings.Contains(errOut, "MASTER_KEY") {
		t.Fatalf("no master key: %d %q %s", code, out, errOut)
	}
	t.Setenv("BOBRES_MASTER_KEY", strings.Repeat("k", 32))
	t.Setenv("BOBRES_ADMIN_TELEGRAM_ID", "")
	if code, out, errOut := runAdminPassword(); code != 1 || out != "" || !strings.Contains(errOut, "BOBRES_ADMIN_TELEGRAM_ID") {
		t.Fatalf("no owner: %d %q %s", code, out, errOut)
	}
}

// A server may name several owners: with no --id the main owner (the first)
// gets the password, with --id another listed owner does, and an id the
// server does not list is refused without creating anyone.
func TestAdminPasswordForAChosenOwner(t *testing.T) {
	st := adminEnv(t)
	t.Setenv("BOBRES_ADMIN_TELEGRAM_ID", "4242, 4243")
	ctx := context.Background()

	code, out, errOut := runAdminPassword("--id", "4243")
	m := passwordLine.FindStringSubmatch(out)
	if code != 0 || m == nil || errOut != "" {
		t.Fatalf("--id 4243: %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	second, err := st.GetUserByTelegramID(ctx, st.DB(), 4243)
	if err != nil || second.Role != "owner" {
		t.Fatalf("the second owner's account: %+v %v", second, err)
	}
	creds, err := st.CredentialsFor(ctx, st.DB(), second.ID)
	if err != nil || !creds.MustChange || !webauth.CheckPassword(creds.PasswordHash, m[1]) {
		t.Fatalf("the second owner's login: %+v %v", creds, err)
	}
	if _, err := st.GetUserByTelegramID(ctx, st.DB(), 4242); err == nil {
		t.Fatal("the main owner was given an account by a command about the second")
	}

	code, out, errOut = runAdminPassword()
	if code != 0 || passwordLine.FindStringSubmatch(out) == nil {
		t.Fatalf("default: %d %s %s", code, out, errOut)
	}
	if first, err := st.GetUserByTelegramID(ctx, st.DB(), 4242); err != nil || first.Role != "owner" {
		t.Fatalf("with no --id the main owner is served: %+v %v", first, err)
	}

	for _, bad := range []string{"777", "-1", "abc"} {
		code, out, errOut = runAdminPassword("--id", bad)
		if code == 0 || out != "" {
			t.Errorf("--id %s: %d %q %s", bad, code, out, errOut)
		}
	}
	if _, err := st.GetUserByTelegramID(ctx, st.DB(), 777); err == nil {
		t.Fatal("an id the server does not list got an account")
	}
}
