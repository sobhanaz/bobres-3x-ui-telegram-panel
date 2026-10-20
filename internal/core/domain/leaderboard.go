package domain

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The top-inviters board (the bot's "Top users"): the customers who invited
// the most people, ranked, with their names and Telegram ids masked. Staff,
// resellers (their invites are their commission link) and banned users are
// never on it, and a name or an id never leaves core in full.

const (
	// SettingLeaderboard is the owner's switch for the board (on by default).
	// The board also needs the referral program (referral.reward_percent above
	// 0): without it nothing is invited for a reward and nothing is ranked.
	SettingLeaderboard = "growth.leaderboard.enabled"

	// TopInvitersMax is how many places the board has: the customers ranked
	// below it are not shown to anyone.
	TopInvitersMax = 50
	// TopInvitersPage is the places on a page when the caller does not say;
	// a page has at most TopInvitersPageMax.
	TopInvitersPage    = 5
	TopInvitersPageMax = 10

	// defaultBoardTTL is how long the board is kept before it is read again.
	defaultBoardTTL = 30 * time.Second
)

// LeaderboardOn: the board is on, that is the referral program is (a reward
// above 0) and the owner has not switched the board off.
func LeaderboardOn(settings map[string]string) bool {
	pct, _ := strconv.Atoi(strings.TrimSpace(settings["referral.reward_percent"]))
	return pct > 0 && settings[SettingLeaderboard] != "false"
}

// boardCache keeps the board for a short time: ranking every inviter is one
// pass over the invited users, which a store with many customers should not
// repeat for every tap of the button.
type boardCache struct {
	mu   sync.Mutex
	rows []store.InviterRow
	at   time.Time
}

// board is the ranked top customers as the store has them now, or as they were
// at most Config.LeaderboardTTL ago (a minus value: never kept).
func (s *Service) board(ctx context.Context) ([]store.InviterRow, error) {
	ttl := s.cfg.LeaderboardTTL
	if ttl < 0 {
		return s.st.TopInviters(ctx, s.st.Conn(), TopInvitersMax)
	}
	if ttl == 0 {
		ttl = defaultBoardTTL
	}
	// One read at a time: callers who arrive together wait for it rather than
	// each asking the database.
	s.boards.mu.Lock()
	defer s.boards.mu.Unlock()
	if s.boards.rows != nil && time.Since(s.boards.at) < ttl {
		return s.boards.rows, nil
	}
	rows, err := s.st.TopInviters(ctx, s.st.Conn(), TopInvitersMax)
	if err != nil {
		return nil, err
	}
	s.boards.rows, s.boards.at = rows, time.Now()
	return rows, nil
}

// TopInviter is one place on the board, as it may be shown.
type TopInviter struct {
	Rank       int
	Name       string // the @username masked (MaskName)
	TelegramID string // the Telegram id masked (MaskID)
	Invited    int64
	You        bool
}

// TopInvitersPageResult is a page of the board for the customer looking at it.
type TopInvitersPageResult struct {
	// Enabled is false while the board is off (LeaderboardOn); nothing else is
	// set then.
	Enabled bool
	Entries []TopInviter
	// Page and PageSize are what the answer is for: a page past the end is the
	// last one.
	Page, PageSize int
	// Total is the number of places on the board (at most TopInvitersMax).
	Total int
	More  bool
	// YourInvited is how many users joined through the viewer's link, on the
	// board or not; YourRank their place (0: not on it).
	YourInvited int64
	YourRank    int
}

// TopInviters returns one page of the board as userID sees it (store.ErrNotFound
// for an unknown user). page counts from 1 and size is the places per page.
// Whoever asks, the board holds only active customers; a banned viewer may look
// but is not on it.
func (s *Service) TopInviters(ctx context.Context, userID string, page, size int) (*TopInvitersPageResult, error) {
	viewer, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if !LeaderboardOn(settings) {
		return &TopInvitersPageResult{}, nil
	}
	if size <= 0 {
		size = TopInvitersPage
	}
	size = min(size, TopInvitersPageMax)
	rows, err := s.board(ctx)
	if err != nil {
		return nil, err
	}
	mine, err := s.st.InvitedCount(ctx, s.st.Conn(), viewer.ID)
	if err != nil {
		return nil, err
	}
	total := len(rows)
	pages := max((total+size-1)/size, 1)
	page = min(max(page, 1), pages)
	out := &TopInvitersPageResult{Enabled: true, Page: page, PageSize: size, Total: total, More: page < pages, YourInvited: mine}
	for i, r := range rows {
		if r.UserID == viewer.ID {
			out.YourRank = i + 1
		}
	}
	from, to := (page-1)*size, min(page*size, total)
	for i := from; i < to; i++ {
		r := rows[i]
		out.Entries = append(out.Entries, TopInviter{Rank: i + 1, Name: MaskName(r.Username),
			TelegramID: MaskID(r.TelegramID), Invited: r.Invited, You: r.UserID == viewer.ID})
	}
	return out, nil
}

// MaskName hides most of a name for the board: its first and last three
// characters around "***". A short name shows less, so that at least three
// characters always stay hidden (the stars never stand for fewer than they
// hide), and one of four characters or fewer, like no name at all, shows none:
// "***".
func MaskName(name string) string { return maskMiddle(name) }

// MaskID masks a Telegram id as MaskName masks a name: 7102607089 is
// "710***089".
func MaskID(id int64) string { return maskMiddle(strconv.FormatInt(id, 10)) }

func maskMiddle(s string) string {
	r := []rune(strings.TrimPrefix(strings.TrimSpace(s), "@"))
	keep := min(3, (len(r)-3)/2)
	if keep < 1 {
		return "***"
	}
	return string(r[:keep]) + "***" + string(r[len(r)-keep:])
}
