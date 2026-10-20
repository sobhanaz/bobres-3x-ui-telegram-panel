package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// Several required channels and groups in the Mini App (miniapp_join.go): the
// customer is held to every chat the license allows, told which they still lack,
// and a chat the bot cannot check does not lock them out of the others.

// chatMembers stands in for the bot with a membership per chat.
type chatMembers struct {
	mu      sync.Mutex
	in      map[string]map[int64]bool // chat -> customers in it
	fail    map[string]error          // chat -> why Telegram cannot tell
	asked   []memberAsk
	delay   time.Duration
	running atomic.Int32
	peak    atomic.Int32
}

func newChatMembers() *chatMembers {
	return &chatMembers{in: map[string]map[int64]bool{}, fail: map[string]error{}}
}

func (c *chatMembers) join(chat string, id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.in[chat] == nil {
		c.in[chat] = map[int64]bool{}
	}
	c.in[chat][id] = true
}

func (c *chatMembers) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.asked)
}

func (c *chatMembers) ChannelMember(ctx context.Context, chat string, id int64) (bool, error) {
	n := c.running.Add(1)
	defer c.running.Add(-1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, memberAsk{chat, id})
	if err := c.fail[chat]; err != nil {
		return false, err
	}
	return c.in[chat][id], nil
}

// manyChatsFixture is a join fixture in the first days of the implicit trial (no limit on required chats) with
// a bot that knows each chat's members.
func manyChatsFixture(t *testing.T) (*joinFix, *chatMembers) {
	t.Helper()
	j := newJoinFixture(t)
	j.sql(`UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`)
	t.Cleanup(func() { j.sql(`UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`) })
	chats := newChatMembers()
	j.api.cfg.MiniAppMembers = chats
	return j, chats
}

func (j *joinFix) requireMany(list ...domain.JoinChat) {
	j.t.Helper()
	if _, err := j.dom.SetJoinChats(context.Background(), actor0(j.fixture), list); err != nil {
		j.t.Fatal(err)
	}
	j.api.mini.join.forget()
}

func chatURLs(out map[string]any) []string {
	var urls []string
	list, _ := out["join_chats"].([]any)
	for _, v := range list {
		m, _ := v.(map[string]any)
		urls = append(urls, m["url"].(string))
	}
	return urls
}

func TestMiniJoinSeveralChats(t *testing.T) {
	j, chats := manyChatsFixture(t)
	j.requireMany(
		domain.JoinChat{Chat: "@mynews"},
		domain.JoinChat{Chat: "-1001234567890", Link: "https://t.me/+AbCdEf123", Label: "VIP room"},
		domain.JoinChat{Chat: "@team_group", Label: "Team"})

	// Outside all three: every chat is named with its link and name, and not one id.
	code, out := j.auth(301)
	mustJoin(t, "a customer outside all", code, out)
	if out["join_url"] != "https://t.me/mynews" {
		t.Fatalf("join_url: %v", out)
	}
	list, _ := out["join_chats"].([]any)
	if len(list) != 3 {
		t.Fatalf("join_chats: %v", out)
	}
	second := list[1].(map[string]any)
	if second["url"] != "https://t.me/+AbCdEf123" || second["label"] != "VIP room" {
		t.Errorf("the private chat: %v", second)
	}
	if first := list[0].(map[string]any); first["label"] != "@mynews" || list[2].(map[string]any)["label"] != "Team" {
		t.Errorf("the names: %v", list)
	}
	if got := asJSON(out); strings.Contains(got, "1234567890") {
		t.Errorf("the answer has a private chat's id: %s", got)
	}

	// Joined the first and the third: only the private chat is told, and join_url is its link.
	chats.join("@mynews", 301)
	chats.join("@team_group", 301)
	code, out = j.auth(301)
	mustJoin(t, "a customer lacking one", code, out)
	if out["join_url"] != "https://t.me/+AbCdEf123" || len(chatURLs(out)) != 1 {
		t.Fatalf("one chat still missing: %v", out)
	}
	// Chats the customer is a member of are remembered; only the missing one is asked again.
	before := chats.count()
	code, out = j.auth(301)
	mustJoin(t, "again", code, out)
	if n := chats.count() - before; n != 1 {
		t.Errorf("%d chats asked about, want only the one missing", n)
	}

	// All joined: in, and not asked again for any of them.
	chats.join("-1001234567890", 301)
	tok := j.in(301)
	before = chats.count()
	for range 3 {
		if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 {
			t.Fatalf("plans: %d", code)
		}
	}
	if chats.count() != before {
		t.Errorf("a member of all was asked again: %d more", chats.count()-before)
	}

	// A session that outlives the membership in one chat stops at its next call.
	j.clock.advance(miniJoinMemberTTL + time.Minute)
	chats.mu.Lock()
	delete(chats.in["@team_group"], 301)
	chats.mu.Unlock()
	code, out = j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	mustJoin(t, "after leaving a chat", code, out)
	if out["join_url"] != "https://t.me/team_group" {
		t.Errorf("the chat left: %v", out)
	}
}

func TestMiniJoinOneChatAnswersAsBefore(t *testing.T) {
	j := newJoinFixture(t) // @mynews alone
	code, out := j.auth(311)
	mustJoin(t, "one chat", code, out)
	if _, has := out["join_chats"]; has || len(out) != 3 {
		t.Fatalf("one required chat must answer exactly as before: %v", out)
	}
}

// A chat the bot cannot check lets the customer in for that chat only: the others still hold.
func TestMiniJoinAChatTelegramCannotTellAbout(t *testing.T) {
	j, chats := manyChatsFixture(t)
	chats.fail["@broken_chan"] = errors.New("bot is not an admin")
	j.requireMany(domain.JoinChat{Chat: "@mynews"}, domain.JoinChat{Chat: "@broken_chan"}, domain.JoinChat{Chat: "@other_chan"})

	chats.join("@mynews", 321)
	code, out := j.auth(321)
	mustJoin(t, "a customer lacking the third", code, out)
	if urls := chatURLs(out); len(urls) != 1 || urls[0] != "https://t.me/other_chan" {
		t.Fatalf("only the chat that can be checked is asked of them: %v", out)
	}
	chats.join("@other_chan", 321)
	j.in(321) // the broken chat does not hold them
	if j.logs.String() == "" {
		t.Error("the unreadable chat was not logged")
	}
}

// The license decides how many count: with the free tier only the first, and the
// others are saved and count again when the limit allows.
func TestMiniJoinFollowsTheLicense(t *testing.T) {
	j, chats := manyChatsFixture(t)
	j.requireMany(domain.JoinChat{Chat: "@mynews"}, domain.JoinChat{Chat: "@second_chan"})
	chats.join("@mynews", 331)

	mustJoinWith := func(what string) {
		t.Helper()
		code, out := j.auth(331)
		mustJoin(t, what, code, out)
	}
	mustJoinWith("the trial holds both")
	j.sql(`UPDATE core.install SET trial_started_at = now() - interval '31 days'`)
	j.api.mini.join.forget()
	j.in(331) // the free tier holds the first only

	j.sql(`UPDATE core.install SET trial_started_at = now()`)
	j.api.mini.join.forget()
	mustJoinWith("the limit allows both again")
}

// Asking the bot about many chats is bounded: a few at a time.
func TestMiniJoinAsksAFewChatsAtOnce(t *testing.T) {
	j, chats := manyChatsFixture(t)
	chats.delay = 20 * time.Millisecond
	list := []domain.JoinChat{{Chat: "@mynews"}}
	for _, n := range []string{"aaaaa", "bbbbb", "ccccc", "ddddd", "eeeee", "fffff", "ggggg", "hhhhh"} {
		list = append(list, domain.JoinChat{Chat: "@chan_" + n})
	}
	j.requireMany(list...)
	code, out := j.auth(341)
	mustJoin(t, "outside all nine", code, out)
	if got := len(chatURLs(out)); got != 9 {
		t.Fatalf("%d chats named, want 9", got)
	}
	if p := chats.peak.Load(); p > miniJoinParallel || p < 2 {
		t.Errorf("%d asked at once, want 2 to %d", p, miniJoinParallel)
	}
}
