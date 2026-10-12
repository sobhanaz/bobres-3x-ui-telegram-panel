package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// updatesBot is the fake bot with a given update check.
type updatesBot struct {
	*fakeBot
	check botfiles.UpdateCheck
}

func (b *updatesBot) CheckUpdates(context.Context) (*botfiles.UpdateCheck, error) {
	c := b.check
	return &c, nil
}

// The Updates tab: the channel the bot follows and the release's issue time
// pass through, and core (never the bot) says whether the installed license
// covers the release: its updates_until against the day the release was
// issued. No valid license, no known release or no issue time: nothing to
// say (covered absent), and the release is installable.
func TestSystemUpdatesLicense(t *testing.T) {
	f, b, sign := licenseFixture(t)
	ctx := context.Background()
	if _, err := f.st.DB().Exec(ctx, `DELETE FROM core.license`); err != nil {
		t.Fatal(err)
	}
	claimed := true // what a bot says about the license is not taken
	bot := &updatesBot{fakeBot: &fakeBot{}, check: botfiles.UpdateCheck{Current: "1.4.0", Latest: "1.5.0-rc.10", Channel: "beta",
		Issued: "2027-10-01T10:00Z", Newer: true, Verified: true, CheckedAt: 1760000000, Covered: &claimed}}
	f.api.cfg.Bot = bot
	updates := func(t *testing.T) map[string]any {
		t.Helper()
		code, out := b.do(http.MethodGet, "/api/v1/system", nil)
		if code != http.StatusOK {
			t.Fatalf("system: %d %v", code, out)
		}
		return obj(out["updates"])
	}
	covered := func(t *testing.T, want any) {
		t.Helper()
		u := updates(t)
		got, ok := u["covered"]
		if want == nil && ok || want != nil && got != want {
			t.Fatalf("covered: %v (present %v), want %v; %v", got, ok, want, u)
		}
	}
	install := func(t *testing.T, raw string) {
		t.Helper()
		if code, out := b.do(http.MethodPut, "/api/v1/license", map[string]any{"token": raw, "reason": "test"}); code != http.StatusOK {
			t.Fatalf("install: %d %v", code, out)
		}
	}

	u := updates(t)
	if u["channel"] != "beta" || u["issued"] != "2027-10-01T10:00Z" || u["latest"] != "1.5.0-rc.10" || u["newer"] != true {
		t.Fatalf("passed through: %v", u)
	}
	covered(t, nil) // unlicensed: everything is covered, nothing is said

	// The fixture's license has updates until 2027-09-30.
	install(t, sign(nil))
	covered(t, false)
	bot.check.Issued = "2027-09-30T23:59Z" // issued on its last day
	covered(t, true)
	bot.check.Issued = "2027-10-01T10:00Z"

	// Unlimited updates.
	install(t, sign(func(tok *license.Token) { tok.Entitlements.UpdatesUntil = "" }))
	covered(t, true)
	install(t, sign(nil))

	// The bot does not say when the release was issued (an older bot), or
	// knows no release: nothing to say.
	bot.check.Issued = ""
	covered(t, nil)
	bot.check.Issued, bot.check.Latest = "2027-10-01T10:00Z", ""
	covered(t, nil)
	bot.check.Latest = "1.5.0-rc.10"
	covered(t, false)

	// A license that does not verify here gates nothing.
	other := sign(func(tok *license.Token) { tok.Domain = "other.example.com" })
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.license SET token = $1`, other); err != nil {
		t.Fatal(err)
	}
	covered(t, nil)
}
