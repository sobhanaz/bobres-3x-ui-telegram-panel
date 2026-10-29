package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/runner"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/loginwire"
)

const coreToBotToken = "core-to-bot-token-0123456789abcdef" //nolint:gosec // test fixture

// approvalWorld is a store whose core reaches the bot as in production: core's botfiles client posts to the bot's internal endpoints, which
// are served here on a test server and answered by the real handler, and the taps go back over gRPC. The owner turned the approval on.
type approvalWorld struct {
	*world
	owner *person
	ctx   context.Context
}

func newApprovalWorld(t *testing.T) *approvalWorld {
	t.Helper()
	w := newWorld(t)
	owner := setupStore(t, w)
	w.h.SetLoginApprovals(w.stack.Approvals)
	mux := http.NewServeMux()
	mux.Handle("POST "+loginwire.PathRequest, runner.LoginApprovalRequest(w.h, coreToBotToken, nil))
	mux.Handle("POST "+loginwire.PathResolve, runner.LoginApprovalResolve(w.h, coreToBotToken, nil))
	mux.Handle("POST "+loginwire.PathNotice, runner.LoginApprovalNotice(w.h, coreToBotToken, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	w.stack.Domain.SetLoginApprovalBot(botfiles.New(srv.URL, coreToBotToken))
	a := &approvalWorld{world: w, owner: owner, ctx: context.Background()}
	own := w.storeUser(ownerID)
	if err := w.stack.Domain.SetLoginApproval(a.ctx, own, "dashboard", true, nil); err != nil {
		t.Fatalf("turn the approval on: %v", err)
	}
	return a
}

// signIn asks for the approval of a staff member's sign-in as the dashboard does.
func (a *approvalWorld) signIn(tgID int64, ua string) (*domain.PendingLogin, []byte, error) {
	a.t.Helper()
	_, hash, err := webauth.NewToken()
	if err != nil {
		a.t.Fatal(err)
	}
	p, err := a.stack.Domain.RequestLoginApproval(a.ctx, domain.LoginRequest{User: a.storeUser(tgID), Method: "link", IP: "198.51.100.9", UserAgent: ua, TicketHash: hash})
	return p, hash, err
}

func (a *approvalWorld) poll(ticket []byte) *domain.PollResult {
	a.t.Helper()
	r, err := a.stack.Domain.PollLoginApproval(a.ctx, ticket)
	if err != nil {
		a.t.Fatal(err)
	}
	return r
}

// buttonData is the data of the Approve (true) or Deny button under a person's newest message.
func buttonData(t *testing.T, p *person, approve bool) string {
	t.Helper()
	prefix := "lga:n:"
	if approve {
		prefix = "lga:y:"
	}
	for _, row := range p.last().kb.InlineKeyboard {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, prefix) {
				return b.CallbackData
			}
		}
	}
	t.Fatalf("user %d has no %s button under %q", p.id, prefix, p.last().text)
	return ""
}

// tapData sends a tap with these button data from a person, on their newest message (as a real client can send any data it likes).
func (p *person) tapData(data string) {
	id := "tap-" + data
	p.w.tg.mu.Lock()
	p.w.tg.cbFrom[id] = p.id
	p.w.tg.mu.Unlock()
	last := p.w.tg.last(p.id)
	p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), CallbackQuery: &tg.CallbackQuery{
		ID: id, From: *p.from(), Data: data, Message: &tg.Message{MessageID: last.id, Chat: tg.Chat{ID: p.id, Type: "private"}},
	}})
}

var approvalDataRe = regexp.MustCompile(`^lga:[yn]:[0-9a-f]{32}:[A-Za-z0-9_-]{16}$`)

// The whole way in Telegram: the staff member and the owner get the facts and two buttons, in their own languages; an Approve lets the
// sign-in through and is written into both messages; a second tap, an old button and a foreign one do nothing.
func TestSignInApprovalInTheBot(t *testing.T) {
	a := newApprovalWorld(t)
	adam := a.customer(871, "adam")
	a.role(871, "admin")

	p, ticket, err := a.signIn(871, "Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0")
	if err != nil || len(p.Approvers) != 2 {
		t.Fatalf("sign-in: %+v %v", p, err)
	}
	prompt, ownerPrompt := adam.last(), a.owner.last() // the messages themselves: later taps edit them, and the bot may add others after
	for _, want := range []string{"Dashboard sign-in", "@adam", "198.51.100.9", "Firefox on Linux", "a login link", "5 minutes", "has not been seen"} {
		if !strings.Contains(prompt.text, want) {
			t.Errorf("the staff member's message lacks %q:\n%s", want, prompt.text)
		}
	}
	if labels := prompt.labels(); len(labels) != 2 || labels[0] != "✅ Approve" || labels[1] != "⛔ Deny" {
		t.Fatalf("buttons: %v", labels)
	}
	approve, deny := buttonData(t, adam, true), buttonData(t, adam, false)
	if !approvalDataRe.MatchString(approve) || !approvalDataRe.MatchString(deny) || len(approve) > 64 {
		t.Fatalf("button data: %q %q", approve, deny)
	}
	if prompt.kb.InlineKeyboard[0][0].Style != "success" || prompt.kb.InlineKeyboard[0][1].Style != "danger" {
		t.Fatalf("button colors: %+v", prompt.kb.InlineKeyboard[0])
	}
	if !strings.Contains(ownerPrompt.text, "you are asked as an owner") || !strings.Contains(ownerPrompt.text, "@adam") || len(ownerPrompt.labels()) != 2 {
		t.Fatalf("the owner's message:\n%s", ownerPrompt.text)
	}
	ownerApprove := buttonData(t, a.owner, true)
	if ownerApprove == approve {
		t.Fatal("two approvers share one button")
	}

	// A tap on a button the sign-in gave someone else does nothing: the other admin cannot approve adam's sign-in.
	eve := a.customer(872, "eve")
	a.role(872, "admin")
	eve.tapData(approve)
	eve.toast("This request has ended already.")
	if r := a.poll(ticket); r.Status != corestore.ApprovalPending {
		t.Fatalf("another admin's tap changed the sign-in: %+v", r)
	}
	// So does a forged one, and one the bot cannot even read.
	for _, data := range []string{
		"lga:y:" + strings.Repeat("0", 32) + ":" + strings.Repeat("A", 16),
		strings.Replace(approve, ":y:", ":x:", 1),
		approve[:len(approve)-1] + "!",
		"lga:y:short:short",
		"lga",
		"lga:y:",
	} {
		adam.tapData(data)
	}
	if r := a.poll(ticket); r.Status != corestore.ApprovalPending {
		t.Fatalf("a forged tap changed the sign-in: %+v", r)
	}
	if !strings.Contains(prompt.text, "Dashboard sign-in") || len(prompt.labels()) != 2 || len(ownerPrompt.labels()) != 2 {
		t.Fatalf("a refused tap changed the message:\n%s", prompt.text)
	}

	// The real Approve: the toast, the message written over without its buttons, the sign-in let through.
	adam.press("lga:y:")
	adam.toast("Approved.")
	if !strings.Contains(prompt.text, "Sign-in approved") || !strings.Contains(prompt.text, "@adam can continue") || len(prompt.labels()) != 0 {
		t.Fatalf("after Approve:\n%s", prompt.text)
	}
	if r := a.poll(ticket); r.Status != corestore.ApprovalApproved || r.Approval == nil {
		t.Fatalf("the approved sign-in: %+v", r)
	}
	// The owner's copy is written over by core's worker.
	if err := a.stack.Domain.SweepLoginApprovals(a.ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ownerPrompt.text, "Sign-in approved") || !strings.Contains(ownerPrompt.text, "@adam approved it") || len(ownerPrompt.labels()) != 0 {
		t.Fatalf("the owner's copy after the sweep:\n%s", ownerPrompt.text)
	}

	// A second tap on the same data is told it ended already, and nothing is approved twice.
	adam.tapData(approve)
	adam.toast("This request has ended already.")
	a.owner.tapData(ownerApprove)
	a.owner.toast("This request has ended already.")
	if n := a.auditCount("dashboard.login_approved"); n != 1 {
		t.Fatalf("approvals in the audit log: %d", n)
	}
}

// Deny is written into the message with advice for a sign-in the person did not make; the owner may deny too.
func TestSignInDenyInTheBot(t *testing.T) {
	a := newApprovalWorld(t)
	adam := a.customer(871, "adam")
	a.role(871, "admin")
	_, ticket, err := a.signIn(871, "Mozilla/5.0 (Windows NT 10.0) Chrome/126.0 Safari/537.36")
	if err != nil {
		t.Fatal(err)
	}
	prompt, ownerPrompt := adam.last(), a.owner.last()
	a.owner.press("lga:n:")
	a.owner.toast("Denied.")
	if !strings.Contains(ownerPrompt.text, "Sign-in denied") || !strings.Contains(ownerPrompt.text, "change your dashboard password") || len(ownerPrompt.labels()) != 0 {
		t.Fatalf("after Deny:\n%s", ownerPrompt.text)
	}
	if r := a.poll(ticket); r.Status != corestore.ApprovalDenied || r.Approval != nil {
		t.Fatalf("the denied sign-in: %+v", r)
	}
	if err := a.stack.Domain.SweepLoginApprovals(a.ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.text, "Sign-in denied") || len(prompt.labels()) != 0 {
		t.Fatalf("the staff member's copy:\n%s", prompt.text)
	}
	adam.tapData("lga:y:" + strings.Repeat("a", 32) + ":" + strings.Repeat("b", 16))
	adam.toast("This request has ended already.")
}

// A sign-in nobody answers ends, and both messages say so.
func TestSignInExpiryInTheBot(t *testing.T) {
	a := newApprovalWorld(t)
	adam := a.customer(871, "adam")
	a.role(871, "admin")
	_, ticket, err := a.signIn(871, "curl/8")
	if err != nil {
		t.Fatal(err)
	}
	approve := buttonData(t, adam, true)
	prompts := map[int64]*msg{adam.id: adam.last(), a.owner.id: a.owner.last()}
	if _, err := a.stack.Store.DB().Exec(a.ctx, `UPDATE core.login_approvals
		SET created_at = created_at - interval '10 minutes', expires_at = now() - interval '1 second' WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
	if err := a.stack.Domain.SweepLoginApprovals(a.ctx); err != nil {
		t.Fatal(err)
	}
	for id, m := range prompts {
		if !strings.Contains(m.text, "Sign-in request expired") || len(m.labels()) != 0 {
			t.Fatalf("user %d after the time was up:\n%s", id, m.text)
		}
	}
	// An approval on the old button is too late.
	adam.tapData(approve)
	adam.toast("This request has ended already.")
	if r := a.poll(ticket); r.Status != corestore.ApprovalExpired || r.Approval != nil {
		t.Fatalf("the sign-in: %+v", r)
	}
}

// The texts are Persian for a Persian reader, and the owner's copy is the owner variant.
func TestSignInApprovalIsInThePersonsLanguage(t *testing.T) {
	a := newApprovalWorld(t)
	sara := a.customerNamed(881, "sara", "fa")
	a.role(881, "support")
	if _, _, err := a.signIn(881, "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Version/17.5 Mobile/15E148 Safari/604.1"); err != nil {
		t.Fatal(err)
	}
	m := sara.last()
	for _, want := range []string{"ورود به داشبورد", "@sara", "Safari روی iOS", "لینک ورود", "۵ دقیقه", "فقط اگر خودتان هستید"} {
		if !strings.Contains(m.text, want) {
			t.Errorf("the Persian message lacks %q:\n%s", want, m.text)
		}
	}
	if labels := m.labels(); len(labels) != 2 || labels[0] != "✅ تأیید" || labels[1] != "⛔ رد" {
		t.Fatalf("Persian buttons: %v", labels)
	}
	sara.press("lga:y:")
	sara.toast("تأیید شد.")
	if !strings.Contains(m.text, "ورود تأیید شد") {
		t.Fatalf("Persian result:\n%s", m.text)
	}
}

// A person who blocked the bot is left out; with nobody left to ask, the sign-in ends at once.
func TestSignInApprovalWhenAPersonBlockedTheBot(t *testing.T) {
	a := newApprovalWorld(t)
	adam := a.customer(871, "adam")
	a.role(871, "admin")
	a.tg.setBlocked(871, true)
	p, ticket, err := a.signIn(871, "curl/8")
	if err != nil || len(p.Approvers) != 1 || p.Approvers[0] != corestore.ApproverOwner {
		t.Fatalf("one approver is left: %+v %v", p, err)
	}
	if r := a.poll(ticket); r.Status != corestore.ApprovalPending || !r.Delivered {
		t.Fatalf("poll: %+v", r)
	}
	a.tg.setBlocked(ownerID, true)
	var und *domain.UndeliverableError
	_, _, err = a.signIn(871, "curl/8")
	if !errors.As(err, &und) || und.Reason != domain.DeliverBlocked {
		t.Fatalf("nobody can be reached: %v", err)
	}
	if adam.count("Dashboard sign-in") != 0 {
		t.Fatal("a person who blocked the bot was shown a message")
	}
}

// Turning the approval on tells the owner who does it; core's off, from the server, tells every owner.
func TestSignInApprovalSwitchNotices(t *testing.T) {
	a := newApprovalWorld(t)
	if m := a.owner.last(); !strings.Contains(m.text, "Sign-in approval is on") || !strings.Contains(m.text, "sudo bobres admin approval off") || !strings.Contains(m.text, "@boss") {
		t.Fatalf("the notice for turning it on:\n%s", m.text)
	}
	if err := a.stack.Domain.SetLoginApproval(a.ctx, nil, "server", false, nil); err != nil {
		t.Fatal(err)
	}
	if m := a.owner.last(); !strings.Contains(m.text, "Sign-in approval is off") || !strings.Contains(m.text, "The server's command line") {
		t.Fatalf("the notice for turning it off:\n%s", m.text)
	}
}

// How staff sign in is not changed from the bot, not even by the owner.
func TestSignInApprovalIsNotASettingOfTheBot(t *testing.T) {
	a := newApprovalWorld(t)
	a.owner.text("/set security.login_approval false")
	a.owner.lastHas("change it in the dashboard")
	if on, _ := a.stack.Domain.LoginApprovalOn(a.ctx); !on {
		t.Fatal("the bot switched the approval off")
	}
}

func (a *approvalWorld) auditCount(action string) int {
	a.t.Helper()
	var n int
	if err := a.stack.Store.DB().QueryRow(a.ctx, `SELECT count(*) FROM core.audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}
