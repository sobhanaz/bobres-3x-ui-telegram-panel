package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetTopInviters answers a page of the board with names and Telegram ids
// already masked (the bot never holds a whole one), marks the asking customer,
// and is off with the referral program or the owner's switch.
func TestGetTopInvitersOverGRPC(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	setSetting := func(key, value string) {
		t.Helper()
		raw, _ := json.Marshal(value)
		if err := f.st.SetSetting(ctx, f.st.Conn(), key, raw); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = f.st.DB().Exec(ctx, "DELETE FROM core.settings WHERE key IN ('referral.reward_percent', 'growth.leaderboard.enabled')")
	})

	up := func(tg int64, name, ref string) *corev1.User {
		u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: tg, Username: name, Language: "en", ReferredBy: ref})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	ana := up(7600000001, "ana_the_great", "")
	bob := up(7600000002, "bobby", "")
	for i := range 3 {
		up(7600001000+int64(i), "", ana.GetId())
	}
	up(7600002000, "", bob.GetId())
	viewer := up(7600000003, "viewer", "")

	// The program is off (no reward): the board is off, whatever else is set.
	if res, err := c.GetTopInviters(ctx, &corev1.GetTopInvitersRequest{UserId: viewer.GetId()}); err != nil || res.GetEnabled() || len(res.GetEntries()) != 0 {
		t.Fatalf("with the referral program off: %+v %v", res, err)
	}
	setSetting("referral.reward_percent", "10")
	res, err := c.GetTopInviters(ctx, &corev1.GetTopInvitersRequest{UserId: bob.GetId()})
	if err != nil || !res.GetEnabled() || res.GetTotal() != 2 || res.GetYourInvited() != 1 || res.GetYourRank() != 2 || res.GetPage() != 1 || res.GetPageSize() != 5 || res.GetMore() {
		t.Fatalf("the board: %+v %v", res, err)
	}
	first, second := res.GetEntries()[0], res.GetEntries()[1]
	if first.GetRank() != 1 || first.GetName() != "ana***eat" || first.GetTelegramId() != "760***001" || first.GetInvited() != 3 || first.GetYou() ||
		second.GetRank() != 2 || second.GetName() != "b***y" || second.GetTelegramId() != "760***002" || second.GetInvited() != 1 || !second.GetYou() {
		t.Fatalf("the places: %+v %+v", first, second)
	}
	// Nothing on the wire holds a whole name or id.
	wire := fmt.Sprint(res)
	for _, whole := range []string{"ana_the_great", "bobby", "7600000001", "7600000002", ana.GetId(), bob.GetId()} {
		if strings.Contains(wire, whole) {
			t.Fatalf("the answer holds %q: %s", whole, wire)
		}
	}
	// Pages: one a page, the second is the last.
	two, err := c.GetTopInviters(ctx, &corev1.GetTopInvitersRequest{UserId: viewer.GetId(), Page: 2, PageSize: 1})
	if err != nil || two.GetPage() != 2 || !two.GetEnabled() || two.GetMore() || len(two.GetEntries()) != 1 || two.GetEntries()[0].GetRank() != 2 {
		t.Fatalf("page 2 of 1: %+v %v", two, err)
	}
	if _, err := c.GetTopInviters(ctx, &corev1.GetTopInvitersRequest{UserId: "00000000-0000-0000-0000-000000000000"}); status.Code(err) != codes.NotFound {
		t.Fatalf("an unknown user: %v", err)
	}
	setSetting("growth.leaderboard.enabled", "false")
	if res, err := c.GetTopInviters(ctx, &corev1.GetTopInvitersRequest{UserId: ana.GetId()}); err != nil || res.GetEnabled() || len(res.GetEntries()) != 0 || res.GetYourInvited() != 0 {
		t.Fatalf("with the board switched off: %+v %v", res, err)
	}
}

// Settings tells the bot whether resellers are in the license in effect, so it
// offers the Become a reseller button only then: they are during the trial and
// are not on the free tier.
func TestSettingsSaysWhetherResellersAreIncluded(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	reset := func(trialDaysAgo int) {
		t.Helper()
		if _, err := f.st.DB().Exec(ctx, fmt.Sprintf(`UPDATE core.install SET trial_started_at = now() - interval '%d days'`, trialDaysAgo)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { reset(0) })

	reset(0)
	if s, err := c.GetSettings(ctx, &corev1.GetSettingsRequest{}); err != nil || !s.GetResellersEnabled() {
		t.Fatalf("during the trial: %+v %v", s, err)
	}
	reset(31) // the trial is over, no license: the free tier
	if s, err := c.GetSettings(ctx, &corev1.GetSettingsRequest{}); err != nil || s.GetResellersEnabled() {
		t.Fatalf("on the free tier: %+v %v", s, err)
	}
}
