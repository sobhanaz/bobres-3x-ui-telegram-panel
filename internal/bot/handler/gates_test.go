package handler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// memLimiter counts the keys with one prefix and ignores the window (tests
// are short); other keys always pass.
type memLimiter struct {
	mu     sync.Mutex
	prefix string
	n      map[string]int
}

func newMemLimiter(prefix string) *memLimiter {
	return &memLimiter{prefix: prefix, n: map[string]int{}}
}

func (l *memLimiter) Allow(_ context.Context, key string, _ time.Duration, max int) (bool, error) {
	if !strings.HasPrefix(key, l.prefix) {
		return true, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n[key]++
	return l.n[key] <= max, nil
}

func (l *memLimiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	clear(l.n)
}

// role makes a registered user staff (or a customer again) in core, and
// reloads the bot's staff list.
func (w *world) role(telegramID int64, role string) {
	w.t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	u, err := st.GetUserByTelegramID(ctx, st.Conn(), telegramID)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := st.SetUserRole(ctx, st.Conn(), u.ID, role, "active"); err != nil {
		w.t.Fatal(err)
	}
	if err := w.h.RefreshSettings(ctx); err != nil {
		w.t.Fatal(err)
	}
}

// setInCore changes a setting in core as the owner, outside the chat (the
// bot only learns about it at its next refresh).
func (w *world) setInCore(key, value string) {
	w.t.Helper()
	if _, err := w.stack.Core.AdminSetSetting(context.Background(), &corev1.AdminSetSettingRequest{
		ActorTelegramId: ownerID, Key: key, Value: value,
	}); err != nil {
		w.t.Fatalf("set %s: %v", key, err)
	}
}

// customer registers a user who picked English.
func (w *world) customer(id int64, username string) *person {
	p := w.person(id, username)
	p.text("/start")
	p.press("lang:en")
	return p
}

// count is how many messages shown to the user contain sub.
func (p *person) count(sub string) int {
	n := 0
	for _, m := range p.w.tg.all(p.id) {
		if strings.Contains(m.text, sub) {
			n++
		}
	}
	return n
}

func (p *person) lastHas(sub string) {
	p.w.t.Helper()
	if m := p.last(); !strings.Contains(m.text, sub) {
		p.w.t.Fatalf("user %d: last message %q, want %q", p.id, m.text, sub)
	}
}

func TestMaintenanceMode(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(801, "mona")
	sup := w.customer(802, "sam")
	w.role(802, "support")
	// A card payment started before maintenance can still be finished.
	u.press("buy")
	u.press("plan:")
	u.press("pay:c:")

	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	owner.text("/admin")
	owner.lastHas("Maintenance mode is on")

	u.photo("receipt-mnt", "REF-77")
	u.lastHas("Received")
	u.text("hello")
	u.lastHas("under maintenance")
	shown := len(w.tg.all(801))
	u.text("hello again") // within the minute: not answered again
	u.text("/menu")
	if n := len(w.tg.all(801)); n != shown {
		t.Fatalf("maintenance message repeated: %d messages, want %d", n, shown)
	}
	u.press("home")
	u.toast("under maintenance")
	u.text("/paysupport") // Telegram requires it to work
	u.lastHas("Payment support")

	// Staff work as usual, support too; the review goes on, and its result
	// reaches the customer (notifications are never gated).
	sup.text("/menu")
	sup.lastHas("Main menu")
	owner.press("adm:pend")
	owner.press("adm:ok:")
	u.eventuallySees("Payment confirmed")

	// A newcomer can pick a language (an invite code survives), then waits.
	nu := w.person(803, "nora")
	nu.text("/start")
	nu.lastHas("Choose your language")
	nu.press("lang:en")
	nu.lastHas("under maintenance")
	if nu.count("Main menu") != 0 {
		t.Fatal("a newcomer got the menu during maintenance")
	}

	owner.text("/set maintenance.enabled false")
	u.text("hi")
	u.lastHas("Main menu")
}

func TestForcedJoin(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(811, "jack")
	sup := w.customer(812, "sue")
	w.role(812, "support")

	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	sup.text("/menu")
	sup.lastHas("Main menu")
	if n := w.tg.checks(); n != 0 {
		t.Fatalf("staff were checked against the channel %d times", n)
	}

	menu := u.last()
	u.press("buy")
	u.lastHas("Join our channel")
	if !strings.Contains(menu.text, "Main menu") {
		t.Fatalf("the pressed message was replaced: %q", menu.text)
	}
	if link := u.link("Join the channel"); link != "https://t.me/testchan" {
		t.Fatalf("join button: %q", link)
	}
	u.press("jn")
	u.toast("haven't joined")
	w.tg.setMember(811, "member")
	u.press("jn")
	u.lastHas("Main menu")

	// A member is remembered for a while: not asked again on every tap.
	checks := w.tg.checks()
	w.tg.setMember(811, "left")
	u.press("buy")
	u.lastHas("Choose a plan")
	u.text("/menu")
	if w.tg.checks() != checks {
		t.Fatal("a member was checked again within the cache time")
	}

	// A private channel's invite link; a newcomer is asked right after
	// picking a language. Paying support stays open.
	owner.text("/set join.link https://t.me/+AbCdEf123")
	nu := w.person(813, "nils")
	nu.text("/start")
	nu.press("lang:en")
	nu.lastHas("Join our channel")
	if link := nu.link("Join the channel"); link != "https://t.me/+AbCdEf123" {
		t.Fatalf("join link: %q", link)
	}
	nu.text("/paysupport")
	nu.lastHas("Payment support")

	// When Telegram cannot tell (the bot is not the channel's admin), users
	// are let in rather than locked out.
	w.tg.mu.Lock()
	w.tg.memberErr = &tg.APIError{Code: 400, Description: "Bad Request: member list is inaccessible"}
	w.tg.mu.Unlock()
	nu.text("hello")
	nu.lastHas("Main menu")

	owner.text("/set join.channel")
	w.tg.mu.Lock()
	w.tg.memberErr = nil
	w.tg.mu.Unlock()
	checks = w.tg.checks()
	w.customer(814, "olga").lastHas("Main menu")
	if w.tg.checks() != checks {
		t.Fatal("checked a channel that is no longer required")
	}
}

func TestTicketsPerDay(t *testing.T) {
	w := newWorld(t)
	w.h.limiter = newMemLimiter("bot:tk:")
	owner := setupStore(t, w)
	owner.text("/set limits.tickets_per_day 2")
	u := w.customer(821, "tina")
	ticket := func(text string) {
		u.text("/menu")
		u.press("support")
		u.text(text)
	}
	for i := range 3 {
		ticket(fmt.Sprintf("help number %d", i))
	}
	u.lastHas("today's limit of support messages")
	if n := owner.count("Support message from @tina"); n != 2 {
		t.Fatalf("%d tickets reached the admin, want 2", n)
	}
	owner.text("/set limits.tickets_per_day 0") // no limit
	ticket("help number 3")
	u.lastHas("Your message was sent")
}

func TestConfigurableRateLimit(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	lim := newMemLimiter("bot:rl:")
	w.h.limiter = lim
	u := w.customer(831, "rita")
	burst := func(n int) int {
		lim.reset()
		before := len(w.tg.all(831))
		for range n {
			u.text("hi")
		}
		return len(w.tg.all(831)) - before
	}
	if got := burst(25); got != 20 {
		t.Fatalf("default limit: %d of 25 messages answered, want 20", got)
	}
	owner.text("/set limits.bot_per_10s 5")
	owner.sees("Saved")
	if got := burst(8); got != 5 {
		t.Fatalf("limit 5: %d of 8 messages answered", got)
	}
	u.press("buy")
	u.toast("Too many requests")
}

func TestHTTPSURL(t *testing.T) {
	for s, want := range map[string]bool{
		"https://example.com/terms": true, "https://t.me/+AbC": true,
		"": false, "http://example.com": false, "https://": false, "tg://resolve?domain=x": false,
		"https://user:pw@example.com": false, "javascript:alert(1)": false, "example.com/terms": false,
	} {
		if httpsURL(s) != want {
			t.Errorf("httpsURL(%q) = %v", s, !want)
		}
	}
}

// A payment already under way can be finished whatever the gates say: a
// TXID, a receipt and then its bank reference, a check of a gateway payment.
func TestGatesLetPaymentsUnderWayFinish(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set payments.usdt_trc20 " + trc20)
	owner.text("/set payments.usdt_rate 60000")
	start := func(id int64, name, method string) *person {
		p := w.customer(id, name)
		p.press("buy")
		p.press("plan:")
		p.press(method)
		return p
	}
	crypto := start(821, "cara", "pay:x:")
	card := start(822, "carl", "pay:c:")
	gateway := start(823, "gina", "pay:z:")
	late := start(824, "leo", "pay:x:")

	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	crypto.text(strings.Repeat("ab", 32))
	crypto.lastHas("Received")
	card.photo("receipt-gate", "") // no caption: the bank reference is asked
	card.lastHas("bank tracking/reference number")
	card.text("554433")
	card.lastHas("Received")
	gateway.press("chk:")
	gateway.toast("Not confirmed yet")
	gateway.press("home") // anything else waits
	gateway.toast("under maintenance")

	owner.text("/set maintenance.enabled false")
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	late.text(strings.Repeat("cd", 32))
	late.lastHas("Received")
	late.press("home")
	late.lastHas("Join our channel")
}
