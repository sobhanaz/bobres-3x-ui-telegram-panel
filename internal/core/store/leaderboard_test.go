package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// seedInviter makes a user (role and status as given) who invited n users.
func seedInviter(t *testing.T, s *Store, tg int64, username, role, status string, n int) *User {
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

// The board's query: active customers only (the plain "user" role, so no staff
// and no resellers), by users invited, a tie to whoever joined first, with a
// limit; and the viewer's own count for anyone.
func TestTopInviters(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := seedInviter(t, s, 8101, "a_inviter", "user", "active", 3)
	b := seedInviter(t, s, 8102, "b_inviter", "user", "active", 3)
	if _, err := s.DB().Exec(ctx, `UPDATE core.users SET created_at = now() - interval '2 hours' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err) // b joined the bot before a: the tie goes to b
	}
	c := seedInviter(t, s, 8103, "", "user", "active", 2)
	seedInviter(t, s, 8104, "banned_one", "user", "banned", 9)
	for i, role := range []string{"support", "admin", "owner", "reseller"} {
		seedInviter(t, s, 8110+int64(i), role+"_one", role, "active", 5)
	}
	seedInviter(t, s, 8120, "no_invites", "user", "active", 0)

	rows, err := s.TopInviters(ctx, s.Conn(), 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("the board: %+v %v", rows, err)
	}
	if rows[0].UserID != b.ID || rows[1].UserID != a.ID || rows[2].UserID != c.ID {
		t.Fatalf("the order: %+v (b, a, c wanted)", rows)
	}
	if rows[0].Invited != 3 || rows[2].Invited != 2 || rows[0].Username != "b_inviter" || rows[2].Username != "" || rows[0].TelegramID != 8102 {
		t.Fatalf("the rows: %+v", rows)
	}
	if two, err := s.TopInviters(ctx, s.Conn(), 2); err != nil || len(two) != 2 || two[1].UserID != a.ID {
		t.Fatalf("a limit of 2: %+v %v", two, err)
	}
	if none, err := s.TopInviters(ctx, s.Conn(), 0); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("a limit of 0: %+v %v", none, err)
	}
	for _, c := range []struct {
		id   string
		want int64
	}{{a.ID, 3}, {c.ID, 2}} {
		if got, err := s.InvitedCount(ctx, s.Conn(), c.id); err != nil || got != c.want {
			t.Errorf("invited count of %s: %d %v, want %d", c.id, got, err, c.want)
		}
	}
	// The count is ReferralStats' count, banned and staff inviters included.
	banned, err := s.GetUserByTelegramID(ctx, s.Conn(), 8104)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.InvitedCount(ctx, s.Conn(), banned.ID); err != nil || got != 9 {
		t.Errorf("a banned inviter's count: %d %v", got, err)
	}
	if invited, _, _, err := s.ReferralStats(ctx, s.Conn(), banned.ID); err != nil || invited != 9 {
		t.Errorf("referral stats: %d %v", invited, err)
	}
}

// TestTopInvitersAtScale times the board's query over 100,000 users, a third of
// them invited by one of thousands of inviters (a few with hundreds of
// invites), and prints its plan. It is a check to run by hand, not part of the
// suite (it seeds 100,000 rows):
//
//	BOBRES_SCALE_TEST=1 go test ./internal/core/store -run TopInvitersAtScale -v
func TestTopInvitersAtScale(t *testing.T) {
	if os.Getenv("BOBRES_SCALE_TEST") != "1" {
		t.Skip("set BOBRES_SCALE_TEST=1 to seed 100,000 users and time the top-inviters query")
	}
	s := testStore(t)
	ctx := context.Background()
	start := time.Now()
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id, username, language, role, status, created_at)
		 SELECT gen_random_uuid(), 1000000000 + g, 'user' || g, 'en',
		        CASE WHEN g % 500 = 0 THEN 'reseller' WHEN g % 997 = 0 THEN 'admin' ELSE 'user' END,
		        CASE WHEN g % 211 = 0 THEN 'banned' ELSE 'active' END,
		        now() - (g || ' seconds')::interval
		 FROM generate_series(1, 100000) g`,
		// users 5001.. are invited (a third of them) by one of the first 5000, the low
		// numbers more often
		`UPDATE core.users u SET referred_by = i.id
		 FROM (SELECT id, row_number() OVER (ORDER BY telegram_id) AS rn FROM core.users ORDER BY telegram_id LIMIT 5000) i
		 WHERE u.telegram_id > 1000005000 AND u.telegram_id % 3 = 0
		   AND i.rn = 1 + ((u.telegram_id * u.telegram_id) % 5000) / (1 + (u.telegram_id % 7))`,
		`ANALYZE core.users`,
	} {
		if _, err := s.DB().Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var invited, inviters int
	if err := s.DB().QueryRow(ctx, `SELECT count(*), count(DISTINCT referred_by) FROM core.users WHERE referred_by IS NOT NULL`).Scan(&invited, &inviters); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded 100000 users in %s: %d invited by %d inviters", time.Since(start).Round(time.Millisecond), invited, inviters)

	var times []time.Duration
	for range 10 {
		at := time.Now()
		top, err := s.TopInviters(ctx, s.Conn(), 50)
		if err != nil || len(top) != 50 {
			t.Fatalf("%d rows, %v", len(top), err)
		}
		times = append(times, time.Since(at))
	}
	t.Logf("TopInviters(50) over 100000 users, 10 runs: %v", times)

	rows, err := s.DB().Query(ctx, `EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF) SELECT u.id, u.telegram_id, COALESCE(u.username, ''), i.invited
		FROM (SELECT referred_by, count(*) AS invited FROM core.users WHERE referred_by IS NOT NULL GROUP BY referred_by) i
		JOIN core.users u ON u.id = i.referred_by
		WHERE u.status = 'active' AND u.role = 'user'
		ORDER BY i.invited DESC, u.created_at, u.id LIMIT 50`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	t.Logf("the plan:\n%s", strings.Join(plan, "\n"))
}
