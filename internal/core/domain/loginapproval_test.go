package domain

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
)

// fakeApprovalBot stands in for the bot: it records what core asks of it and can be told to fail.
type fakeApprovalBot struct {
	mu          sync.Mutex
	prompts     []LoginPrompt
	resolutions []LoginResolution
	notices     []ApprovalNotice
	down        bool           // every call fails
	blocked     map[int64]bool // these people blocked the bot
	resolveDown bool           // writing over a message fails
	nextMsg     int64
}

func (b *fakeApprovalBot) SendLoginApproval(_ context.Context, p LoginPrompt) ([]PromptResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down {
		return nil, errors.New("bot down")
	}
	b.prompts = append(b.prompts, p)
	var out []PromptResult
	for _, t := range p.Targets {
		if b.blocked[t.TelegramID] {
			out = append(out, PromptResult{TelegramID: t.TelegramID, Error: DeliverBlocked})
			continue
		}
		b.nextMsg++
		out = append(out, PromptResult{TelegramID: t.TelegramID, MessageID: 1000 + b.nextMsg})
	}
	return out, nil
}

func (b *fakeApprovalBot) ResolveLoginApproval(_ context.Context, r LoginResolution) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down || b.resolveDown {
		return errors.New("bot down")
	}
	b.resolutions = append(b.resolutions, r)
	return nil
}

func (b *fakeApprovalBot) NoticeLoginApproval(_ context.Context, n ApprovalNotice) ([]PromptResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down {
		return nil, errors.New("bot down")
	}
	b.notices = append(b.notices, n)
	var out []PromptResult
	for _, t := range n.Targets {
		if b.blocked[t.TelegramID] {
			out = append(out, PromptResult{TelegramID: t.TelegramID, Error: DeliverBlocked})
			continue
		}
		out = append(out, PromptResult{TelegramID: t.TelegramID})
	}
	return out, nil
}

func (b *fakeApprovalBot) lastPrompt(t *testing.T) LoginPrompt {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.prompts) == 0 {
		t.Fatal("the bot was asked for no prompt")
	}
	return b.prompts[len(b.prompts)-1]
}

// nonceOf is the nonce the prompt gave the person with that Telegram id.
func nonceOf(t *testing.T, p LoginPrompt, tg int64) string {
	t.Helper()
	for _, tt := range p.Targets {
		if tt.TelegramID == tg {
			return tt.Nonce
		}
	}
	t.Fatalf("no target %d in %+v", tg, p.Targets)
	return ""
}

const (
	approverAdminTG = 910001
	otherAdminTG    = 910002
	supportTG       = 910003
)

// approvalFixture has an owner, an admin and a support member, a bot that works, and the approval switched on by the owner.
type approvalFixture struct {
	svc   *Service
	s     *store.Store
	bot   *fakeApprovalBot
	owner *store.User
	admin *store.User
}

func newApprovalFixture(t *testing.T) *approvalFixture {
	t.Helper()
	svc, s := testService(t)
	f := &approvalFixture{svc: svc, s: s, bot: &fakeApprovalBot{blocked: map[int64]bool{}}}
	svc.SetLoginApprovalBot(f.bot)
	f.owner = owner(t, svc)
	adminUser(t, svc, s, approverAdminTG, "admin")
	adminUser(t, svc, s, otherAdminTG, "admin")
	adminUser(t, svc, s, supportTG, "support")
	var err error
	if f.admin, err = s.GetUserByTelegramID(context.Background(), s.Conn(), approverAdminTG); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetLoginApproval(context.Background(), f.owner, "dashboard", true, nil); err != nil {
		t.Fatalf("turn the approval on: %v", err)
	}
	return f
}

// signIn asks for the approval of u's sign-in as the web layer does, with a ticket of its own.
func (f *approvalFixture) signIn(t *testing.T, u *store.User) (*PendingLogin, []byte) {
	t.Helper()
	_, hash, err := webauth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.RequestLoginApproval(context.Background(), LoginRequest{User: u, Method: "link", IP: "203.0.113.7",
		UserAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/126.0 Safari/537.36", TicketHash: hash})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return p, hash
}

func (f *approvalFixture) poll(t *testing.T, ticket []byte) *PollResult {
	t.Helper()
	r, err := f.svc.PollLoginApproval(context.Background(), ticket)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// decide is a tap of tg's on the buttons of the prompt.
func (f *approvalFixture) decide(t *testing.T, tg int64, p LoginPrompt, approve bool) *ApprovalDecision {
	t.Helper()
	return f.decideWith(t, tg, p.ApprovalID, nonceOf(t, p, tg), approve)
}

func (f *approvalFixture) decideWith(t *testing.T, tg int64, id, nonce string, approve bool) *ApprovalDecision {
	t.Helper()
	d, err := f.svc.DecideLoginApproval(context.Background(), tg, id, nonce, approve)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// age makes every waiting or approved sign-in's time be up, as the database sees it.
func (f *approvalFixture) age(t *testing.T) {
	t.Helper()
	if _, err := f.s.DB().Exec(context.Background(), `UPDATE core.login_approvals
		SET created_at = created_at - interval '10 minutes', expires_at = now() - interval '1 second' WHERE status IN ('pending', 'approved')`); err != nil {
		t.Fatal(err)
	}
}

func approvalStatus(t *testing.T, s *store.Store, id string) string {
	t.Helper()
	u, ok := ParseApprovalID(id)
	if !ok {
		t.Fatalf("bad id %q", id)
	}
	a, err := s.GetLoginApproval(context.Background(), s.Conn(), u)
	if err != nil {
		t.Fatal(err)
	}
	return a.Status
}

// The approval is off until the owner turns it on, and the two switches read as documented.
func TestLoginApprovalDefaults(t *testing.T) {
	svc, _ := testService(t)
	st, err := svc.LoginApprovalState(context.Background())
	if err != nil || st.Enabled || !st.NotifyOwner {
		t.Fatalf("defaults: %+v %v", st, err)
	}
	if on, err := svc.LoginApprovalOn(context.Background()); err != nil || on {
		t.Fatalf("on by default: %v %v", on, err)
	}
	spec, ok := SpecOf(SettingLoginApproval)
	if !ok || spec.Default != "false" || spec.Perm != permSecurity || !ownerOnlyPerm(spec.Perm) {
		t.Fatalf("spec: %+v", spec)
	}
	spec, ok = SpecOf(SettingLoginApprovalNotifyOwner)
	if !ok || spec.Default != "true" || spec.Perm != permSecurity {
		t.Fatalf("notify spec: %+v", spec)
	}
}

// The happy path: the staff member is asked (and the owner), an approval lets the browser collect its session once, and everything is audited.
func TestLoginApprovalHappyPath(t *testing.T) {
	f := newApprovalFixture(t)
	p, ticket := f.signIn(t, f.admin)
	if len(p.Approvers) != 2 || p.Approvers[0] != store.ApproverSelf || p.Approvers[1] != store.ApproverOwner {
		t.Fatalf("approvers: %v", p.Approvers)
	}
	prompt := f.bot.lastPrompt(t)
	if len(prompt.Targets) != 2 || prompt.Account.Username != f.admin.Username || prompt.Account.Role != "admin" || prompt.IP != "203.0.113.7" ||
		prompt.Browser != "chrome" || prompt.System != "windows" || !prompt.NewAddress || len(prompt.ApprovalID) != 32 || prompt.Method != "link" {
		t.Fatalf("prompt: %+v", prompt)
	}
	if prompt.ExpiresAt.Sub(prompt.At) != LoginApprovalTTL {
		t.Fatalf("the wait is %v", prompt.ExpiresAt.Sub(prompt.At))
	}
	if self, owner := nonceOf(t, prompt, approverAdminTG), nonceOf(t, prompt, ownerTG); self == owner || len(self) != 16 {
		t.Fatalf("nonces: %q %q", self, owner)
	}

	r := f.poll(t, ticket)
	if r.Status != store.ApprovalPending || !r.Delivered || r.Failed || r.Approval != nil || len(r.Approvers) != 2 {
		t.Fatalf("waiting: %+v", r)
	}
	d := f.decide(t, approverAdminTG, prompt, true)
	if d.Outcome != OutcomeApproved || d.By == nil || d.By.TelegramID != approverAdminTG || d.Account.TelegramID != approverAdminTG {
		t.Fatalf("decision: %+v", d)
	}
	r = f.poll(t, ticket)
	if r.Status != store.ApprovalApproved || r.Approval == nil || r.Approval.UserID != f.admin.ID || r.Approval.DecidedVia != store.ApproverSelf || r.Approval.IP != "203.0.113.7" {
		t.Fatalf("approved poll: %+v", r)
	}
	// The sign-in is spent: nobody collects a second session.
	if r := f.poll(t, ticket); r.Status != PollNone || r.Approval != nil {
		t.Fatalf("second poll: %+v", r)
	}
	if st := approvalStatus(t, f.s, prompt.ApprovalID); st != store.ApprovalConsumed {
		t.Fatalf("status %q", st)
	}
	for action, want := range map[string]int{"dashboard.login_requested": 1, "dashboard.login_approved": 1, "dashboard.login_denied": 0, "dashboard.login_expired": 0} {
		if n := auditCount(t, f.s, action); n != want {
			t.Errorf("%s: %d entries, want %d", action, n, want)
		}
	}

	// A second tap of the same button, and the owner's tap on theirs, do nothing: it was approved already.
	if d := f.decide(t, approverAdminTG, prompt, true); d.Outcome != OutcomeAlreadyApproved || d.By == nil {
		t.Fatalf("replay: %+v", d)
	}
	if d := f.decide(t, ownerTG, prompt, false); d.Outcome != OutcomeAlreadyApproved {
		t.Fatalf("the owner's late deny: %+v", d)
	}
	if n := auditCount(t, f.s, "dashboard.login_approved"); n != 1 {
		t.Fatalf("a replay was audited as an approval: %d", n)
	}
	if n := auditCount(t, f.s, "dashboard.login_denied"); n != 0 {
		t.Fatalf("a late deny was audited: %d", n)
	}

	// The owner's copy of the message is written over with the result (the admin's own tap did theirs).
	if err := f.svc.SweepLoginApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.bot.mu.Lock()
	defer f.bot.mu.Unlock()
	if len(f.bot.resolutions) != 1 || f.bot.resolutions[0].Result != ResolutionApproved || len(f.bot.resolutions[0].Targets) != 1 ||
		f.bot.resolutions[0].Targets[0].TelegramID != ownerTG || f.bot.resolutions[0].By == nil || f.bot.resolutions[0].By.TelegramID != approverAdminTG {
		t.Fatalf("resolutions: %+v", f.bot.resolutions)
	}
}

// The owner may approve another staff member's sign-in, with the nonce of their own message.
func TestLoginApprovalByTheOwner(t *testing.T) {
	f := newApprovalFixture(t)
	p, ticket := f.signIn(t, f.admin)
	prompt := f.bot.lastPrompt(t)
	_ = p
	if d := f.decide(t, ownerTG, prompt, true); d.Outcome != OutcomeApproved || d.By.Role != "owner" {
		t.Fatalf("owner approves: %+v", d)
	}
	if r := f.poll(t, ticket); r.Status != store.ApprovalApproved || r.Approval.DecidedVia != store.ApproverOwner {
		t.Fatalf("poll: %+v", r)
	}
	// The staff member's own copy is written over by the sweep: their tap did not do it.
	if err := f.svc.SweepLoginApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.bot.mu.Lock()
	defer f.bot.mu.Unlock()
	if len(f.bot.resolutions) != 1 || f.bot.resolutions[0].Targets[0].TelegramID != approverAdminTG {
		t.Fatalf("resolutions: %+v", f.bot.resolutions)
	}
}

// An owner signing in is asked alone, and the notify-owner switch off asks the staff member alone.
func TestLoginApprovalWhoIsAsked(t *testing.T) {
	f := newApprovalFixture(t)
	f.signIn(t, f.owner)
	if p := f.bot.lastPrompt(t); len(p.Targets) != 1 || p.Targets[0].TelegramID != ownerTG || p.Targets[0].Role != store.ApproverSelf {
		t.Fatalf("an owner signs in: %+v", p.Targets)
	}
	off := false
	if err := f.svc.SetLoginApproval(context.Background(), f.owner, "dashboard", true, &off); err != nil {
		t.Fatal(err)
	}
	f.signIn(t, f.admin)
	if p := f.bot.lastPrompt(t); len(p.Targets) != 1 || p.Targets[0].TelegramID != approverAdminTG {
		t.Fatalf("notify-owner off: %+v", p.Targets)
	}
	// Support staff sign in with the same approval.
	sup, err := f.s.GetUserByTelegramID(context.Background(), f.s.Conn(), supportTG)
	if err != nil {
		t.Fatal(err)
	}
	on := true
	if err := f.svc.SetLoginApproval(context.Background(), f.owner, "dashboard", true, &on); err != nil {
		t.Fatal(err)
	}
	f.signIn(t, sup)
	if p := f.bot.lastPrompt(t); len(p.Targets) != 2 || p.Targets[0].TelegramID != supportTG {
		t.Fatalf("support signs in: %+v", p.Targets)
	}
}

// Deny ends the sign-in: nothing opens, and the end is audited.
func TestLoginApprovalDeny(t *testing.T) {
	f := newApprovalFixture(t)
	_, ticket := f.signIn(t, f.admin)
	prompt := f.bot.lastPrompt(t)
	if d := f.decide(t, approverAdminTG, prompt, false); d.Outcome != OutcomeDenied {
		t.Fatalf("deny: %+v", d)
	}
	if r := f.poll(t, ticket); r.Status != store.ApprovalDenied || r.Approval != nil {
		t.Fatalf("poll after deny: %+v", r)
	}
	// Approving afterwards, by anyone asked, changes nothing.
	for _, tg := range []int64{approverAdminTG, ownerTG} {
		if d := f.decide(t, tg, prompt, true); d.Outcome != OutcomeAlreadyDenied {
			t.Fatalf("approve after deny by %d: %+v", tg, d)
		}
	}
	if r := f.poll(t, ticket); r.Status != store.ApprovalDenied || r.Approval != nil {
		t.Fatalf("poll after a late approve: %+v", r)
	}
	if n := auditCount(t, f.s, "dashboard.login_denied"); n != 1 {
		t.Fatalf("denied entries: %d", n)
	}
	if n := auditCount(t, f.s, "dashboard.login_approved"); n != 0 {
		t.Fatalf("approved entries: %d", n)
	}
}

// A sign-in nobody answers ends after 5 minutes, by the database's clock: it cannot be approved late, and it is audited once.
func TestLoginApprovalExpires(t *testing.T) {
	f := newApprovalFixture(t)
	_, ticket := f.signIn(t, f.admin)
	prompt := f.bot.lastPrompt(t)
	f.age(t)
	if d := f.decide(t, approverAdminTG, prompt, true); d.Outcome != OutcomeExpired {
		t.Fatalf("a late approval: %+v", d)
	}
	if r := f.poll(t, ticket); r.Status != store.ApprovalExpired || r.Approval != nil {
		t.Fatalf("poll: %+v", r)
	}
	if n := auditCount(t, f.s, "dashboard.login_expired"); n != 1 {
		t.Fatalf("expired entries: %d", n)
	}
	if d := f.decide(t, ownerTG, prompt, true); d.Outcome != OutcomeExpired {
		t.Fatalf("the owner's late approval: %+v", d)
	}
	// Both messages are written over with the end.
	if err := f.svc.SweepLoginApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.bot.mu.Lock()
	defer f.bot.mu.Unlock()
	if len(f.bot.resolutions) == 0 || f.bot.resolutions[0].Result != ResolutionExpired {
		t.Fatalf("resolutions: %+v", f.bot.resolutions)
	}
	n := 0
	for _, r := range f.bot.resolutions {
		n += len(r.Targets)
	}
	if n != 2 {
		t.Fatalf("messages written over: %d", n)
	}
}

// The worker ends the sign-ins whose time is up without anyone asking, including an approval nobody collected.
func TestLoginApprovalSweepExpiresWithoutAPoll(t *testing.T) {
	f := newApprovalFixture(t)
	p1, _ := f.signIn(t, f.admin)
	p2, _ := f.signIn(t, f.admin)
	prompt2 := f.bot.lastPrompt(t)
	if d := f.decide(t, approverAdminTG, prompt2, true); d.Outcome != OutcomeApproved {
		t.Fatal(d)
	}
	f.age(t)
	if err := f.svc.SweepLoginApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{p1.ID, p2.ID} {
		if st := approvalStatus(t, f.s, id); st != store.ApprovalExpired {
			t.Fatalf("%s: %q", id, st)
		}
	}
	if n := auditCount(t, f.s, "dashboard.login_expired"); n != 2 {
		t.Fatalf("expired entries: %d", n)
	}
	// Nothing is left to end, and a second sweep audits nothing more.
	if err := f.svc.SweepLoginApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := auditCount(t, f.s, "dashboard.login_expired"); n != 2 {
		t.Fatalf("expired entries after a second sweep: %d", n)
	}
}

// A tap from anyone the sign-in was not asked of, with a wrong or another person's nonce, or about an unknown sign-in, does nothing.
func TestLoginApprovalWrongApprover(t *testing.T) {
	f := newApprovalFixture(t)
	_, ticket := f.signIn(t, f.admin)
	prompt := f.bot.lastPrompt(t)
	good := nonceOf(t, prompt, approverAdminTG)
	ownerNonce := nonceOf(t, prompt, ownerTG)

	for name, d := range map[string]*ApprovalDecision{
		"another admin with the right nonce":      f.decideWith(t, otherAdminTG, prompt.ApprovalID, good, true),
		"a support member":                        f.decideWith(t, supportTG, prompt.ApprovalID, good, true),
		"a stranger":                              f.decideWith(t, 555, prompt.ApprovalID, good, true),
		"the admin with the owner's nonce":        f.decideWith(t, approverAdminTG, prompt.ApprovalID, ownerNonce, true),
		"the owner with the admin's nonce":        f.decideWith(t, ownerTG, prompt.ApprovalID, good, true),
		"a wrong nonce":                           f.decideWith(t, approverAdminTG, prompt.ApprovalID, "AAAAAAAAAAAAAAAA", true),
		"an empty nonce":                          f.decideWith(t, approverAdminTG, prompt.ApprovalID, "", true),
		"an unknown sign-in":                      f.decideWith(t, approverAdminTG, "00000000000070008000000000000001", good, true),
		"an id that is no id":                     f.decideWith(t, approverAdminTG, "not-an-id", good, true),
		"no Telegram account":                     f.decideWith(t, 0, prompt.ApprovalID, good, true),
		"a nonce made longer than any real nonce": f.decideWith(t, approverAdminTG, prompt.ApprovalID, good+good+good+good+good, true),
	} {
		if d.Outcome != OutcomeInvalid || d.By != nil {
			t.Errorf("%s: %+v", name, d)
		}
	}
	if r := f.poll(t, ticket); r.Status != store.ApprovalPending {
		t.Fatalf("a refused tap changed the sign-in: %+v", r)
	}
	// Only a wrong nonce from someone who was asked is worth a line in the audit log.
	if n := auditCount(t, f.s, "dashboard.login_approval_invalid"); n != 3 {
		t.Fatalf("invalid-tap entries: %d", n)
	}
	for _, action := range []string{"dashboard.login_approved", "dashboard.login_denied"} {
		if n := auditCount(t, f.s, action); n != 0 {
			t.Fatalf("%s: %d", action, n)
		}
	}
	// The person who was asked can still answer.
	if d := f.decide(t, approverAdminTG, prompt, true); d.Outcome != OutcomeApproved {
		t.Fatalf("the real approval: %+v", d)
	}
}

// A person asked loses the right to answer when their role goes, and a sign-in of an account that lost its access ends.
func TestLoginApprovalAuthorityIsCheckedWhenTheyAnswer(t *testing.T) {
	f := newApprovalFixture(t)
	ctx := context.Background()

	// The owner is demoted before they answer: their button no longer works.
	_, ticket := f.signIn(t, f.admin)
	prompt := f.bot.lastPrompt(t)
	if _, err := f.s.DB().Exec(ctx, `UPDATE core.users SET role = 'admin' WHERE id = $1`, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	if d := f.decide(t, ownerTG, prompt, true); d.Outcome != OutcomeForbidden {
		t.Fatalf("a demoted owner: %+v", d)
	}
	if r := f.poll(t, ticket); r.Status != store.ApprovalPending {
		t.Fatalf("the sign-in changed: %+v", r)
	}
	if _, err := f.s.DB().Exec(ctx, `UPDATE core.users SET role = 'owner' WHERE id = $1`, f.owner.ID); err != nil {
		t.Fatal(err)
	}

	// The staff member is banned before they answer: nothing is approved, the sign-in ends.
	if _, err := f.s.DB().Exec(ctx, `UPDATE core.users SET status = 'banned' WHERE id = $1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	if d := f.decide(t, approverAdminTG, prompt, true); d.Outcome != OutcomeForbidden {
		t.Fatalf("a banned staff member: %+v", d)
	}
	if d := f.decide(t, ownerTG, prompt, true); d.Outcome != OutcomeCancelled && d.Outcome != OutcomeForbidden {
		t.Fatalf("the owner answers for a banned account: %+v", d)
	}
	if r := f.poll(t, ticket); r.Status == store.ApprovalApproved || r.Approval != nil {
		t.Fatalf("a banned account was approved: %+v", r)
	}
}

// Switching the approval off ends the sign-ins that wait, tells the owners, and writes the end into the messages.
func TestLoginApprovalSwitchedOff(t *testing.T) {
	f := newApprovalFixture(t)
	_, ticket := f.signIn(t, f.admin)
	prompt := f.bot.lastPrompt(t)
	if err := f.svc.SetLoginApproval(context.Background(), nil, "server", false, nil); err != nil {
		t.Fatal(err)
	}
	if on, _ := f.svc.LoginApprovalOn(context.Background()); on {
		t.Fatal("still on")
	}
	if r := f.poll(t, ticket); r.Status != store.ApprovalCancelled || r.Approval != nil {
		t.Fatalf("poll: %+v", r)
	}
	if d := f.decide(t, approverAdminTG, prompt, true); d.Outcome != OutcomeCancelled {
		t.Fatalf("approve after it was switched off: %+v", d)
	}
	if n := auditCount(t, f.s, "dashboard.login_cancelled"); n != 1 {
		t.Fatalf("cancelled entries: %d", n)
	}
	if err := f.svc.SweepLoginApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.bot.mu.Lock()
	defer f.bot.mu.Unlock()
	if len(f.bot.resolutions) == 0 || f.bot.resolutions[0].Result != ResolutionCancelled {
		t.Fatalf("resolutions: %+v", f.bot.resolutions)
	}
	// The owner who turned it on was told before the switch; the server's command line turning it off tells every owner.
	if len(f.bot.notices) != 2 || f.bot.notices[0].Kind != NoticeEnabled {
		t.Fatalf("notices: %+v", f.bot.notices)
	}
	last := f.bot.notices[1]
	if last.Kind != NoticeDisabled || last.By != nil || last.Source != "server" || len(last.Targets) != 1 || last.Targets[0].TelegramID != ownerTG {
		t.Fatalf("last notice: %+v", last)
	}
}

// With the bot down, not configured, or the person blocked, the sign-in ends at once as undeliverable: a browser never waits for nothing.
func TestLoginApprovalUndeliverable(t *testing.T) {
	f := newApprovalFixture(t)
	ctx := context.Background()
	reason := func() string {
		t.Helper()
		_, hash, _ := webauth.NewToken()
		_, err := f.svc.RequestLoginApproval(ctx, LoginRequest{User: f.admin, Method: "password", IP: "203.0.113.8", TicketHash: hash})
		var und *UndeliverableError
		if !errors.As(err, &und) || !errors.Is(err, ErrApprovalUndeliverable) {
			t.Fatalf("expected an undeliverable sign-in, got %v", err)
		}
		if a, err := f.s.LoginApprovalByTicket(ctx, f.s.Conn(), hash); err != nil || a.Status != store.ApprovalUndeliverable {
			t.Fatalf("the row: %+v %v", a, err)
		}
		r, err := f.svc.PollLoginApproval(ctx, hash)
		if err != nil || r.Status != store.ApprovalUndeliverable || r.Approval != nil {
			t.Fatalf("poll: %+v %v", r, err)
		}
		return und.Reason
	}

	f.bot.down = true
	if got := reason(); got != DeliverBotDown {
		t.Fatalf("bot down: %q", got)
	}
	f.bot.down = false

	// Both the admin and the owner blocked the bot.
	f.bot.blocked[approverAdminTG], f.bot.blocked[ownerTG] = true, true
	if got := reason(); got != DeliverBlocked {
		t.Fatalf("blocked: %q", got)
	}

	// Only the admin blocked it: the owner still gets the message, so the sign-in waits.
	f.bot.blocked[ownerTG] = false
	_, hash, _ := webauth.NewToken()
	p, err := f.svc.RequestLoginApproval(ctx, LoginRequest{User: f.admin, Method: "link", IP: "203.0.113.9", TicketHash: hash})
	if err != nil || len(p.Approvers) != 1 || p.Approvers[0] != store.ApproverOwner {
		t.Fatalf("one approver reached: %+v %v", p, err)
	}
	if r := f.poll(t, hash); r.Status != store.ApprovalPending || !r.Delivered || r.Failed {
		t.Fatalf("poll: %+v", r)
	}

	// No bot at all.
	f.svc.SetLoginApprovalBot(nil)
	f.bot.blocked = map[int64]bool{}
	if f.svc.LoginApprovalReady() {
		t.Fatal("ready without a bot")
	}
	if got := reason(); got != DeliverNoBot {
		t.Fatalf("no bot: %q", got)
	}
	if n := auditCount(t, f.s, "dashboard.login_undeliverable"); n != 3 {
		t.Fatalf("undeliverable entries: %d", n)
	}
}

// An account that asks too often is refused, not the account next to it: at most 3 wait at once and 6 are asked for in 15 minutes.
func TestLoginApprovalRateLimit(t *testing.T) {
	f := newApprovalFixture(t)
	ctx := context.Background()
	ask := func(u *store.User) error {
		_, hash, _ := webauth.NewToken()
		_, err := f.svc.RequestLoginApproval(ctx, LoginRequest{User: u, Method: "link", IP: "203.0.113.20", TicketHash: hash})
		return err
	}
	for i := 0; i < approvalMaxOpen; i++ {
		if err := ask(f.admin); err != nil {
			t.Fatalf("sign-in %d: %v", i+1, err)
		}
	}
	var lim *ApprovalLimitError
	if err := ask(f.admin); !errors.As(err, &lim) || !errors.Is(err, ErrApprovalRateLimited) || lim.RetryAfter <= 0 {
		t.Fatalf("a fourth waiting sign-in: %v", err)
	}
	// Another account is not held back by it.
	other, err := f.s.GetUserByTelegramID(ctx, f.s.Conn(), otherAdminTG)
	if err != nil {
		t.Fatal(err)
	}
	if err := ask(other); err != nil {
		t.Fatalf("another account: %v", err)
	}
	// The three end (the person at the browser gave up); the window still counts them.
	if _, err := f.s.DB().Exec(ctx, `UPDATE core.login_approvals SET status = 'cancelled' WHERE user_id = $1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	for i := approvalMaxOpen; i < approvalMaxPerWindow; i++ {
		if err := ask(f.admin); err != nil {
			t.Fatalf("sign-in %d: %v", i+1, err)
		}
		if _, err := f.s.DB().Exec(ctx, `UPDATE core.login_approvals SET status = 'cancelled' WHERE user_id = $1`, f.admin.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := ask(f.admin); !errors.As(err, &lim) || lim.RetryAfter <= 0 || lim.RetryAfter > approvalWindow {
		t.Fatalf("the seventh in the window: %v (%+v)", err, lim)
	}
	// Once the window has passed they may ask again.
	if _, err := f.s.DB().Exec(ctx, `UPDATE core.login_approvals SET created_at = created_at - interval '20 minutes', expires_at = expires_at - interval '20 minutes' WHERE user_id = $1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := ask(f.admin); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

// Sign-ins of one account, and answers to one sign-in, at the same time: the limits hold and exactly one answer wins.
func TestLoginApprovalConcurrency(t *testing.T) {
	f := newApprovalFixture(t)
	needConns(t, f.s, 6)
	ctx := context.Background()

	// Twelve sign-ins of one account at once: only the open limit gets through.
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, hash, _ := webauth.NewToken()
			_, err := f.svc.RequestLoginApproval(ctx, LoginRequest{User: f.admin, Method: "link", IP: "203.0.113.30", TicketHash: hash})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, limited := 0, 0
	for err := range results {
		var lim *ApprovalLimitError
		switch {
		case err == nil:
			ok++
		case errors.As(err, &lim):
			limited++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != approvalMaxOpen || limited != 12-approvalMaxOpen {
		t.Fatalf("%d got through, %d were limited", ok, limited)
	}

	// Two approvers answer one sign-in at once, one approving and one denying: exactly one answer counts.
	prompt := f.bot.lastPrompt(t)
	outcomes := make(chan string, 2)
	for _, tc := range []struct {
		tg      int64
		approve bool
	}{{approverAdminTG, true}, {ownerTG, false}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := f.svc.DecideLoginApproval(ctx, tc.tg, prompt.ApprovalID, nonceOf(t, prompt, tc.tg), tc.approve)
			if err != nil {
				outcomes <- "error: " + err.Error()
				return
			}
			outcomes <- d.Outcome
		}()
	}
	wg.Wait()
	close(outcomes)
	var got []string
	for o := range outcomes {
		got = append(got, o)
	}
	decided := 0
	for _, o := range got {
		switch o {
		case OutcomeApproved, OutcomeDenied:
			decided++
		case OutcomeAlreadyApproved, OutcomeAlreadyDenied:
		default:
			t.Fatalf("outcomes: %v", got)
		}
	}
	if decided != 1 {
		t.Fatalf("outcomes: %v", got)
	}
	if n := auditCount(t, f.s, "dashboard.login_approved") + auditCount(t, f.s, "dashboard.login_denied"); n != 1 {
		t.Fatalf("decisions in the audit log: %d", n)
	}
}

// Many polls at once for one approved sign-in: one session is made, once.
func TestLoginApprovalConcurrentPolls(t *testing.T) {
	f := newApprovalFixture(t)
	needConns(t, f.s, 6)
	ctx := context.Background()
	_, ticket := f.signIn(t, f.admin)
	if d := f.decide(t, approverAdminTG, f.bot.lastPrompt(t), true); d.Outcome != OutcomeApproved {
		t.Fatal(d)
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		sessions int
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.svc.PollLoginApproval(ctx, ticket)
			if err != nil {
				t.Error(err)
				return
			}
			if r.Approval != nil {
				mu.Lock()
				sessions++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if sessions != 1 {
		t.Fatalf("%d sessions were collected from one approval", sessions)
	}
}

// Turning the approval on needs a bot that reaches the owner who does it: otherwise nothing changes. The notice goes to them first.
func TestLoginApprovalPreflight(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	o := owner(t, svc)
	bot := &fakeApprovalBot{blocked: map[int64]bool{}}

	// No bot at all.
	var und *UndeliverableError
	if err := svc.SetLoginApproval(ctx, o, "dashboard", true, nil); !errors.As(err, &und) || und.Reason != DeliverNoBot {
		t.Fatalf("no bot: %v", err)
	}
	svc.SetLoginApprovalBot(bot)

	// The bot is down.
	bot.down = true
	if err := svc.SetLoginApproval(ctx, o, "dashboard", true, nil); !errors.As(err, &und) || und.Reason != DeliverBotDown {
		t.Fatalf("bot down: %v", err)
	}
	bot.down = false

	// The owner blocked the bot.
	bot.blocked[ownerTG] = true
	if err := svc.SetLoginApproval(ctx, o, "dashboard", true, nil); !errors.As(err, &und) || und.Reason != DeliverBlocked {
		t.Fatalf("blocked: %v", err)
	}
	bot.blocked[ownerTG] = false

	// The server's command line cannot turn it on.
	if err := svc.SetLoginApproval(ctx, nil, "server", true, nil); !errors.As(err, &und) {
		t.Fatalf("the command line turned it on: %v", err)
	}
	if on, _ := svc.LoginApprovalOn(ctx); on {
		t.Fatal("it was switched on by a refused request")
	}
	if n := auditCount(t, s, "setting.set"); n != 0 {
		t.Fatalf("refused requests were audited as changes: %d", n)
	}

	// Now it works: the owner is told before, once, and the change is audited with who made it.
	bot.notices = nil // the attempt that failed because the owner blocked the bot was offered to the bot
	if err := svc.SetLoginApproval(ctx, o, "dashboard", true, nil); err != nil {
		t.Fatal(err)
	}
	if on, _ := svc.LoginApprovalOn(ctx); !on {
		t.Fatal("not on")
	}
	if len(bot.notices) != 1 || bot.notices[0].Kind != NoticeEnabled || bot.notices[0].By == nil || bot.notices[0].By.TelegramID != ownerTG ||
		bot.notices[0].Source != "dashboard" || len(bot.notices[0].Targets) != 1 {
		t.Fatalf("notices: %+v", bot.notices)
	}
	if n := auditCount(t, s, "setting.set"); n != 1 {
		t.Fatalf("setting.set entries: %d", n)
	}
	// Turning it on again changes nothing and tells nobody.
	if err := svc.SetLoginApproval(ctx, o, "dashboard", true, nil); err != nil || len(bot.notices) != 1 {
		t.Fatalf("again: %v %d", err, len(bot.notices))
	}
	// Off needs no bot to answer: a bot that is down does not keep it on.
	bot.down = true
	if err := svc.SetLoginApproval(ctx, o, "dashboard", false, nil); err != nil {
		t.Fatal(err)
	}
	if on, _ := svc.LoginApprovalOn(ctx); on {
		t.Fatal("still on")
	}
}

// How staff sign in is changed in the dashboard, never in the bot, and no admin can reach it.
func TestLoginApprovalSettingsAreNotForTheBot(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner(t, svc)
	adminUser(t, svc, s, approverAdminTG, "admin")
	for _, tg := range []int64{ownerTG, approverAdminTG} {
		err := svc.AdminSetSetting(ctx, tg, SettingLoginApproval, "true")
		var fe *FieldError
		if !errors.As(err, &fe) {
			t.Fatalf("/set by %d: %v", tg, err)
		}
		views, err := svc.AdminSettingSpecs(ctx, tg)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range views {
			if v.Key == SettingLoginApproval || v.Key == SettingLoginApprovalNotifyOwner {
				t.Fatalf("the bot offers %s to %d", v.Key, tg)
			}
		}
	}
	if on, _ := svc.LoginApprovalOn(ctx); on {
		t.Fatal("the bot switched it on")
	}
}

func TestApprovalIDsAndNonces(t *testing.T) {
	id := "0192f3a4-1b2c-7d3e-8f40-516273849abc"
	short := ShortApprovalID(id)
	if len(short) != 32 {
		t.Fatalf("short id %q", short)
	}
	for _, in := range []string{short, id} {
		if got, ok := ParseApprovalID(in); !ok || got != id {
			t.Errorf("ParseApprovalID(%q) = %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "x", short[:31], short + "0", "zz92f3a41b2c7d3e8f40516273849abc", id + "0", "0192f3a4-1b2c-7d3e-8f40-516273849ab"} {
		if _, ok := ParseApprovalID(bad); ok {
			t.Errorf("ParseApprovalID(%q) accepted", bad)
		}
	}
	n1, h1, err := newApprovalNonce()
	n2, _, _ := newApprovalNonce()
	if err != nil || len(n1) != 16 || n1 == n2 {
		t.Fatalf("nonces: %q %q %v", n1, n2, err)
	}
	if want := sha256.Sum256([]byte(n1)); fmt.Sprintf("%x", h1) != fmt.Sprintf("%x", want[:]) {
		t.Fatal("the stored form of a nonce is its SHA-256")
	}
}

// A Telegram account may only send core so many answers a minute.
func TestLoginApprovalAnswersAreRateLimited(t *testing.T) {
	var s loginApprovalState
	now := time.Now()
	for i := 0; i < approvalDecideRate; i++ {
		if !s.allowAnswer(7, now) {
			t.Fatalf("answer %d refused", i+1)
		}
	}
	if s.allowAnswer(7, now) {
		t.Fatal("answer past the limit accepted")
	}
	if !s.allowAnswer(8, now) {
		t.Fatal("another account was held back")
	}
	if !s.allowAnswer(7, now.Add(61*time.Second)) {
		t.Fatal("not allowed again after a minute")
	}
}
