package domain

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func TestMasks(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", "***"},
		{"ab", "***"},
		{"abcd", "***"},
		{"abcde", "a***e"},
		{"abcdef", "a***f"},
		{"abcdefg", "ab***fg"},
		{"abcdefgh", "ab***gh"},
		{"abcdefghi", "abc***ghi"},
		{"Parisa_Ahmad", "Par***mad"},
		{"@Parisa_Ahmad", "Par***mad"}, // the @ is not part of the name
		{"  rezahil  ", "re***il"},
		{"علیرضا_احمدی", "علی***مدی"}, // whole characters, not bytes
	} {
		if got := MaskName(c.in); got != c.want {
			t.Errorf("MaskName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		id   int64
		want string
	}{
		{7102607089, "710***089"},
		{181314521, "181***521"},
		{9549, "***"},
		{12345, "1***5"},
		{1234567, "12***67"},
		{0, "***"},
	} {
		if got := MaskID(c.id); got != c.want {
			t.Errorf("MaskID(%d) = %q, want %q", c.id, got, c.want)
		}
	}
	// However short or long a name is, at least three characters stay hidden
	// (the stars never stand for fewer than they hide), at most three show at
	// each end, and what shows is the real beginning and end.
	for n := 0; n <= 40; n++ {
		s := strings.Repeat("a", n/2) + strings.Repeat("b", n-n/2)
		got := MaskName(s)
		if got == s && n > 0 {
			t.Errorf("a name of %d characters is shown whole", n)
		}
		if got == "***" {
			continue
		}
		shown := len([]rune(got)) - 3
		if hidden := n - shown; hidden < 3 || shown > 6 || shown%2 != 0 {
			t.Errorf("%d characters: %q shows %d and hides %d", n, got, shown, hidden)
		}
		keep := shown / 2
		if !strings.HasPrefix(got, s[:keep]) || !strings.HasSuffix(got, s[n-keep:]) {
			t.Errorf("%q does not keep the ends of %q", got, s)
		}
	}
}

// tgBase is where the test inviters' Telegram ids start: real ones are ten
// digits, so a masked id shows its first and last three.
const tgBase = 7_100_000_000

// inviter makes a user with the given role and status who invited n users,
// each a new user of their own.
func inviter(t *testing.T, s *store.Store, tg int64, username, role, status string, n int) *store.User {
	t.Helper()
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, s.Conn(), tg, username, "en", "")
	if err != nil {
		t.Fatal(err)
	}
	if role != "user" || status != "active" {
		if err := s.SetUserRole(ctx, s.Conn(), u.ID, role, status); err != nil {
			t.Fatal(err)
		}
	}
	for i := range n {
		if _, err := s.UpsertUser(ctx, s.Conn(), tg*100+int64(i), "", "en", u.ID); err != nil {
			t.Fatal(err)
		}
	}
	return u
}

// joinedAt moves when a user joined the bot, to make a tie decidable.
func joinedAt(t *testing.T, s *store.Store, id string, ago time.Duration) {
	t.Helper()
	if _, err := s.DB().Exec(context.Background(), `UPDATE core.users SET created_at = now() - $2::interval WHERE id = $1`,
		id, fmt.Sprintf("%d seconds", int(ago.Seconds()))); err != nil {
		t.Fatal(err)
	}
}

func placeNames(es []TopInviter) string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name)
	}
	return strings.Join(out, " ")
}

// The board ranks only active customers (no staff, resellers or banned users),
// by how many users joined through their link, a tie to whoever joined first;
// it pages, marks the viewer, shows every viewer their own count whether they
// are ranked or not, and masks every name and id.
func TestTopInviters(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	setSetting(t, s, "referral.reward_percent", "10")

	a := inviter(t, s, tgBase+1, "alexander_the_great", "user", "active", 5)
	b := inviter(t, s, tgBase+2, "bobby_tables", "user", "active", 5)
	joinedAt(t, s, a.ID, 2*time.Hour) // a joined the bot before b: the tie goes to a
	joinedAt(t, s, b.ID, time.Hour)
	c := inviter(t, s, tgBase+3, "", "user", "active", 4) // no username
	staffUser := inviter(t, s, tgBase+4, "the_boss_user", "admin", "active", 9)
	banned := inviter(t, s, tgBase+5, "banned_inviter", "user", "banned", 8)
	reseller := inviter(t, s, tgBase+6, "resellerman", "reseller", "active", 7)
	g := inviter(t, s, tgBase+7, "gwenstacey99", "user", "active", 3)
	h := inviter(t, s, tgBase+8, "hal_jordan_1", "user", "active", 2)
	i := inviter(t, s, tgBase+9, "ivy_pepper_22", "user", "active", 1)
	j := inviter(t, s, tgBase+10, "joker_wild_33", "user", "active", 1)
	joinedAt(t, s, i.ID, 3*time.Hour) // i before j
	joinedAt(t, s, j.ID, 2*time.Hour)
	inviter(t, s, tgBase+11, "nobody_invited", "user", "active", 0) // invited nobody: not ranked

	page1, err := svc.TopInviters(ctx, c.ID, 1, 5)
	if err != nil || !page1.Enabled {
		t.Fatalf("page 1: %+v %v", page1, err)
	}
	// Ranked: a and b (5), c (4), g (3), h (2), i and j (1). Not ranked: the
	// admin, the banned user and the reseller, whatever their invites.
	if got := placeNames(page1.Entries); got != "ale***eat bob***les *** gwe***y99 hal***n_1" {
		t.Fatalf("page 1 names: %q", got)
	}
	for k, e := range page1.Entries {
		if e.Rank != k+1 {
			t.Errorf("place %d has rank %d", k+1, e.Rank)
		}
	}
	if page1.Total != 7 || !page1.More || page1.Page != 1 || page1.PageSize != 5 {
		t.Fatalf("page 1 totals: %+v", page1)
	}
	if first := page1.Entries[0]; first.Invited != 5 || first.TelegramID != "710***001" {
		t.Fatalf("first place: %+v", first)
	}
	// c is the viewer: marked, rank 3, 4 invites.
	if page1.YourRank != 3 || page1.YourInvited != 4 {
		t.Fatalf("the viewer: rank %d, invited %d", page1.YourRank, page1.YourInvited)
	}
	for _, e := range page1.Entries {
		if e.You != (e.Rank == 3) {
			t.Errorf("place %d marked as the viewer: %v", e.Rank, e.You)
		}
	}

	page2, err := svc.TopInviters(ctx, c.ID, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := placeNames(page2.Entries); got != "ivy***_22 jok***_33" || page2.More || page2.Page != 2 || page2.Entries[0].Rank != 6 {
		t.Fatalf("page 2: %q %+v", got, page2)
	}
	// A page past the end is the last one; a page size is at most ten.
	if past, err := svc.TopInviters(ctx, c.ID, 9, 5); err != nil || past.Page != 2 || len(past.Entries) != 2 {
		t.Fatalf("past the end: %+v %v", past, err)
	}
	if wide, err := svc.TopInviters(ctx, c.ID, 1, 99); err != nil || wide.PageSize != TopInvitersPageMax || len(wide.Entries) != 7 {
		t.Fatalf("a wide page: %+v %v", wide, err)
	}
	if first, err := svc.TopInviters(ctx, c.ID, 0, 0); err != nil || first.Page != 1 || first.PageSize != TopInvitersPage || len(first.Entries) != 5 {
		t.Fatalf("defaults: %+v %v", first, err)
	}

	// Nothing a viewer receives holds a whole username or Telegram id.
	for _, e := range append(page1.Entries, page2.Entries...) {
		for _, u := range []*store.User{a, b, c, g, h, i, j} {
			if (u.Username != "" && strings.Contains(e.Name, u.Username)) || strings.Contains(e.TelegramID, strconv.FormatInt(u.TelegramID, 10)) {
				t.Errorf("place %d shows a whole name or id: %+v", e.Rank, e)
			}
		}
		if !strings.Contains(e.TelegramID, "***") || len(e.TelegramID) != 9 {
			t.Errorf("place %d: an id that is not masked: %q", e.Rank, e.TelegramID)
		}
	}

	// Someone who is not on the board still sees their own count: staff, a
	// reseller and a banned user.
	for _, c := range []struct {
		u    *store.User
		want int64
	}{{staffUser, 9}, {banned, 8}, {reseller, 7}} {
		res, err := svc.TopInviters(ctx, c.u.ID, 1, 5)
		if err != nil || res.YourRank != 0 || res.YourInvited != c.want || len(res.Entries) != 5 {
			t.Errorf("%s: %+v %v", c.u.Role+"/"+c.u.Status, res, err)
			continue
		}
		for _, e := range res.Entries {
			if e.You {
				t.Errorf("%s is marked on the board", c.u.Role)
			}
		}
	}
	nobody, err := svc.TopInviters(ctx, seedUser(t, s, tgBase+12).ID, 1, 5)
	if err != nil || nobody.YourInvited != 0 || nobody.YourRank != 0 {
		t.Fatalf("a customer with no invites: %+v %v", nobody, err)
	}

	// An unknown viewer is not found.
	if _, err := svc.TopInviters(ctx, "00000000-0000-0000-0000-000000000000", 1, 5); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown user: %v", err)
	}

	// A user who becomes staff or is banned leaves the board at once (the
	// service here keeps nothing), and the others move up.
	if err := s.SetUserRole(ctx, s.Conn(), a.ID, "support", "active"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserStatus(ctx, s.Conn(), b.ID, "banned"); err != nil {
		t.Fatal(err)
	}
	after, err := svc.TopInviters(ctx, c.ID, 1, 5)
	if err != nil || after.Entries[0].Rank != 1 || !after.Entries[0].You || after.Total != 5 {
		t.Fatalf("after two left: %+v %v", after, err)
	}
}

// With no one invited the board is empty; it is off while the referral program
// is off or the owner switched it off, and answers nothing else then.
func TestTopInvitersSwitches(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	viewer := inviter(t, s, tgBase+101, "viewer_one", "user", "active", 2)

	if res, err := svc.TopInviters(ctx, viewer.ID, 1, 5); err != nil || res.Enabled {
		t.Fatalf("with the referral program off (no reward): %+v %v", res, err)
	}
	setSetting(t, s, "referral.reward_percent", "10")
	res, err := svc.TopInviters(ctx, viewer.ID, 1, 5)
	if err != nil || !res.Enabled || res.Total != 1 || res.YourInvited != 2 || res.YourRank != 1 {
		t.Fatalf("on: %+v %v", res, err)
	}
	setSetting(t, s, SettingLeaderboard, "false")
	if res, err := svc.TopInviters(ctx, viewer.ID, 1, 5); err != nil || res.Enabled || len(res.Entries) != 0 || res.YourInvited != 0 {
		t.Fatalf("switched off: %+v %v", res, err)
	}
	setSetting(t, s, SettingLeaderboard, "true")
	if res, err := svc.TopInviters(ctx, viewer.ID, 1, 5); err != nil || !res.Enabled {
		t.Fatalf("switched on: %+v %v", res, err)
	}
	setSetting(t, s, "referral.reward_percent", "0")
	if res, err := svc.TopInviters(ctx, viewer.ID, 1, 5); err != nil || res.Enabled {
		t.Fatalf("a reward of 0: %+v %v", res, err)
	}

	// No one has invited anyone: an empty board, the viewer's count 0.
	setSetting(t, s, "referral.reward_percent", "10")
	if _, err := s.DB().Exec(ctx, `UPDATE core.users SET referred_by = NULL`); err != nil {
		t.Fatal(err)
	}
	if res, err := svc.TopInviters(ctx, viewer.ID, 1, 5); err != nil || !res.Enabled || res.Total != 0 || len(res.Entries) != 0 || res.More || res.Page != 1 || res.YourInvited != 0 {
		t.Fatalf("an empty board: %+v %v", res, err)
	}
}

// The board is kept for a short time (a store with many customers should not
// rank them all for every tap), but a viewer's own count is always read fresh;
// a negative time keeps nothing.
func TestTopInvitersBoardIsKeptForAWhile(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	setSetting(t, s, "referral.reward_percent", "10")
	kept := New(s, Config{OwnerTelegramIDs: []int64{ownerTG}, LeaderboardTTL: time.Hour})
	fresh := New(s, Config{OwnerTelegramIDs: []int64{ownerTG}, LeaderboardTTL: -1})
	first := inviter(t, s, tgBase+201, "first_inviter", "user", "active", 1)

	res, err := kept.TopInviters(ctx, first.ID, 1, 5)
	if err != nil || res.Total != 1 {
		t.Fatalf("the first read: %+v %v", res, err)
	}
	second := inviter(t, s, tgBase+202, "second_inviter", "user", "active", 3)
	res, err = kept.TopInviters(ctx, first.ID, 1, 5)
	if err != nil || res.Total != 1 || res.Entries[0].Name != "fir***ter" {
		t.Fatalf("a read within the time: %+v %v", res, err)
	}
	if _, err := s.UpsertUser(ctx, s.Conn(), tgBase+203, "", "en", first.ID); err != nil {
		t.Fatal(err)
	}
	if res, err = kept.TopInviters(ctx, first.ID, 1, 5); err != nil || res.YourInvited != 2 {
		t.Fatalf("the viewer's own count is read fresh: %+v %v", res, err)
	}
	if res, err = fresh.TopInviters(ctx, first.ID, 1, 5); err != nil || res.Total != 2 || res.Entries[0].Name != "sec***ter" || res.YourRank != 2 {
		t.Fatalf("without keeping: %+v %v", res, err)
	}
	if res, err = fresh.TopInviters(ctx, second.ID, 1, 5); err != nil || res.YourRank != 1 {
		t.Fatalf("the second: %+v %v", res, err)
	}
}
