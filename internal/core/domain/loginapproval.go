package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Approving dashboard sign-ins in Telegram (docs/04-dashboard.md, "Signing in"; docs/07-security.md).
//
// While the owner's switch security.login_approval is on, a staff sign-in that passed its checks (a login link, or a password with its
// authenticator code) opens no session. It becomes a waiting sign-in (store.LoginApproval): the bot messages the staff member (and the owners,
// unless security.login_approval_notify_owner is off) with the time, the address and the browser, and an Approve and a Deny button. Approve
// lets the browser that waits collect its session at its next poll; Deny, the clock (5 minutes) or the switch going off end it. Core decides
// everything; the bot only carries the messages and the taps. Nothing usable exists before the approval: an approval row is not a session.
const (
	// SettingLoginApproval is the owner's switch (off unless set), and SettingLoginApprovalNotifyOwner whether the owners are asked too for a
	// staff member who is not an owner (on unless set to false).
	SettingLoginApproval            = "security.login_approval"
	SettingLoginApprovalNotifyOwner = "security.login_approval_notify_owner"

	// LoginApprovalTTL is how long a sign-in waits for its approval.
	LoginApprovalTTL = 5 * time.Minute
	// approvalPickup is the least time the browser has to collect a session after the approval.
	approvalPickup = time.Minute
	// An account may have this many sign-ins waiting at once, and ask for this many in the window.
	approvalMaxOpen      = 3
	approvalMaxPerWindow = 6
	approvalWindow       = 15 * time.Minute
	// approvalDeliverTime bounds the bot's delivery of the messages, which the sign-in waits for.
	approvalDeliverTime = 10 * time.Second
	// approvalDecideRate bounds the answers one Telegram account may send core.
	approvalDecideRate = 20
	// approvalCloseWindow: a message is written over with the result only within this long of the end of its sign-in.
	approvalCloseWindow = 15 * time.Minute
	// approvalKeep is how long a finished sign-in is kept (the audit log keeps the record for good).
	approvalKeep = 30 * 24 * time.Hour
	// approvalTick is how often the worker looks.
	approvalTick = 3 * time.Second
	// maxApprovers bounds the owners asked for one sign-in.
	maxApprovers = 11
)

// Errors of the approval flow.
var (
	// ErrApprovalUndeliverable: no approver could be reached (the bot is not running or not configured, Telegram cannot be reached, or the
	// people asked have blocked the bot). Reason says which, as a short code.
	ErrApprovalUndeliverable = errors.New("domain: no approver could be reached")
	// ErrApprovalRateLimited: the account asked for too many sign-ins.
	ErrApprovalRateLimited = errors.New("domain: too many sign-in requests")
)

// ApprovalLimitError is ErrApprovalRateLimited with how long to wait.
type ApprovalLimitError struct{ RetryAfter time.Duration }

func (e *ApprovalLimitError) Error() string { return ErrApprovalRateLimited.Error() }
func (e *ApprovalLimitError) Unwrap() error { return ErrApprovalRateLimited }

// UndeliverableError is ErrApprovalUndeliverable with the reason.
type UndeliverableError struct{ Reason string }

func (e *UndeliverableError) Error() string {
	return ErrApprovalUndeliverable.Error() + ": " + e.Reason
}
func (e *UndeliverableError) Unwrap() error { return ErrApprovalUndeliverable }

// Why a message could not be delivered (PromptResult.Error and UndeliverableError.Reason).
const (
	DeliverNoBot       = "no_bot"      // core has no bot to ask
	DeliverBotDown     = "bot_down"    // the bot did not answer
	DeliverBlocked     = "blocked"     // the person blocked the bot, or never started it
	DeliverTelegram    = "telegram"    // Telegram refused or could not be reached
	DeliverNoAnswer    = "no_answer"   // the bot answered, but not about this person
	DeliverNoApprovers = "no_approver" // nobody to ask
)

// LoginApprovalBot is what core asks of the bot for a sign-in approval: only the bot reaches the approvers' chats (core has no Telegram token
// and no internet access).
type LoginApprovalBot interface {
	// SendLoginApproval messages each approver, and answers one result for each.
	SendLoginApproval(ctx context.Context, p LoginPrompt) ([]PromptResult, error)
	// ResolveLoginApproval writes the end of a sign-in into the messages sent for it.
	ResolveLoginApproval(ctx context.Context, r LoginResolution) error
	// NoticeLoginApproval tells the owners that the switch was changed, and answers one result for each.
	NoticeLoginApproval(ctx context.Context, n ApprovalNotice) ([]PromptResult, error)
}

// ApprovalAccount is the staff member who signs in, as the messages name them.
type ApprovalAccount struct {
	Username   string
	TelegramID int64
	Role       string
}

// LoginPrompt is the message for the approvers of one sign-in. The browser and the system are codes (webauth.DescribeUserAgent).
type LoginPrompt struct {
	ApprovalID string // 32 hexadecimal digits
	Account    ApprovalAccount
	Method     string // link | password | password_initial
	IP         string
	Browser    string
	System     string
	NewAddress bool
	At         time.Time
	ExpiresAt  time.Time
	Targets    []PromptTarget
}

// PromptTarget is one approver: their Telegram id and language, who they are to the sign-in (store.ApproverSelf, store.ApproverOwner), and
// the nonce their buttons carry.
type PromptTarget struct {
	TelegramID int64
	Language   string
	Role       string
	Nonce      string
}

// PromptResult is what became of one message: its Telegram message id, or why it was not sent.
type PromptResult struct {
	TelegramID int64
	MessageID  int64
	Error      string // "" when sent
}

// Results of a sign-in, as the messages say them.
const (
	ResolutionApproved  = "approved"
	ResolutionDenied    = "denied"
	ResolutionExpired   = "expired"
	ResolutionCancelled = "cancelled"
)

// LoginResolution is the end of a sign-in, to be written into the messages that were sent for it.
type LoginResolution struct {
	ApprovalID string
	Result     string
	Account    ApprovalAccount
	By         *ApprovalAccount // who decided (nil: nobody did)
	At         time.Time
	Targets    []ResolveTarget
}

// ResolveTarget is a message to write the result into.
type ResolveTarget struct {
	TelegramID int64
	Language   string
	MessageID  int64
}

// Notice kinds for NoticeLoginApproval.
const (
	NoticeEnabled  = "enabled"
	NoticeDisabled = "disabled"
)

// ApprovalNotice tells an owner the switch changed. By is who changed it (nil: the server's command line).
type ApprovalNotice struct {
	Kind    string
	By      *ApprovalAccount
	Source  string // dashboard | server
	Targets []ResolveTarget
}

// loginApprovalState is what a Service keeps for the approval of sign-ins: the way to the bot, and how often each Telegram account has
// answered lately. The zero value works.
type loginApprovalState struct {
	bot  LoginApprovalBot
	mu   sync.Mutex
	seen map[int64][]time.Time
}

// SetLoginApprovalBot sets how the approvers are reached (the bot, in cmd/core).
func (s *Service) SetLoginApprovalBot(b LoginApprovalBot) { s.approvals.bot = b }

// LoginApprovalReady says whether core has a way to reach approvers at all.
func (s *Service) LoginApprovalReady() bool { return s.approvals.bot != nil }

// LoginApprovalState is the owner's two switches.
type LoginApprovalState struct {
	Enabled     bool
	NotifyOwner bool
}

// LoginApprovalState reads the switches. An error means they cannot be read: a sign-in must not open a session then.
func (s *Service) LoginApprovalState(ctx context.Context) (LoginApprovalState, error) {
	m, err := s.Settings(ctx)
	if err != nil {
		return LoginApprovalState{}, err
	}
	return LoginApprovalState{Enabled: m[SettingLoginApproval] == "true", NotifyOwner: m[SettingLoginApprovalNotifyOwner] != "false"}, nil
}

// LoginApprovalOn reports whether staff sign-ins wait for an approval.
func (s *Service) LoginApprovalOn(ctx context.Context) (bool, error) {
	st, err := s.LoginApprovalState(ctx)
	return st.Enabled, err
}

// accountOf is how a user is named in the messages and the audit log.
func accountOf(u *store.User) ApprovalAccount {
	return ApprovalAccount{Username: u.Username, TelegramID: u.TelegramID, Role: u.Role}
}

// newApprovalNonce is a nonce of 96 random bits for one approver's buttons (16 URL-safe characters), and the hash that is stored.
func newApprovalNonce() (nonce string, hash []byte, err error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	nonce = base64.RawURLEncoding.EncodeToString(b)
	return nonce, hashNonce(nonce), nil
}

func hashNonce(nonce string) []byte {
	h := sha256.Sum256([]byte(nonce))
	return h[:]
}

// ShortApprovalID writes a sign-in's id without dashes (32 characters), the form in a button's data.
func ShortApprovalID(id string) string { return strings.ReplaceAll(id, "-", "") }

// ParseApprovalID reads a sign-in's id as the buttons carry it (32 hexadecimal digits, or a UUID with dashes) and returns it in the stored
// form; false for anything else.
func ParseApprovalID(s string) (string, bool) {
	if len(s) != 32 && len(s) != 36 {
		return "", false
	}
	u, err := buuid.Parse(s)
	if err != nil {
		return "", false
	}
	return u.String(), true
}

// LoginRequest is a staff sign-in that passed its checks and now needs an approver. TicketHash is the hash of the cookie the browser waits
// with.
type LoginRequest struct {
	User       *store.User
	Method     string
	IP         string
	UserAgent  string
	TicketHash []byte
}

// PendingLogin is what a browser is told about the sign-in it waits for.
type PendingLogin struct {
	ID        string
	ExpiresAt time.Time
	// Approvers are who was asked and reached: store.ApproverSelf and store.ApproverOwner.
	Approvers []string
}

// RequestLoginApproval starts a sign-in's wait: it records the sign-in and the people asked, has the bot message them, and answers once that
// is known. It refuses an account that asked too often (*ApprovalLimitError) and ends the sign-in at once when nobody could be reached
// (*UndeliverableError), so a browser never waits for an approval that cannot come.
func (s *Service) RequestLoginApproval(ctx context.Context, req LoginRequest) (*PendingLogin, error) {
	u := req.User
	if u == nil || u.Status != "active" || !IsStaffRole(u.Role) {
		return nil, ErrForbidden
	}
	if len(req.TicketHash) != sha256.Size {
		return nil, invalid("the sign-in has no ticket")
	}
	state, err := s.LoginApprovalState(ctx)
	if err != nil {
		return nil, err
	}
	targets, prompts, err := s.approvalTargets(ctx, u, state.NotifyOwner)
	if err != nil {
		return nil, err
	}
	seen, err := s.st.UserSeenAddress(ctx, s.st.Conn(), u.ID, req.IP)
	if err != nil {
		return nil, err
	}
	browser, system := webauth.DescribeUserAgent(req.UserAgent)
	a := &store.LoginApproval{ID: buuid.MustV7().String(), UserID: u.ID, Method: req.Method, TicketHash: req.TicketHash,
		IP: req.IP, UserAgent: req.UserAgent, NewAddress: !seen && req.IP != ""}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		// One account's sign-ins are counted one at a time, so that a burst cannot slip past the limits.
		if err := s.st.LockUserRow(ctx, tx, u.ID); err != nil {
			return err
		}
		recent, open, retry, err := s.st.ApprovalCounts(ctx, tx, u.ID, approvalWindow)
		if err != nil {
			return err
		}
		switch {
		case open >= approvalMaxOpen:
			return &ApprovalLimitError{RetryAfter: time.Minute}
		case recent >= approvalMaxPerWindow:
			return &ApprovalLimitError{RetryAfter: max(retry, time.Second)}
		}
		if err := s.st.CreateLoginApproval(ctx, tx, a, LoginApprovalTTL, targets); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, u, "dashboard.login_requested", "dashboard", u.ID, nil, map[string]any{
			"approval": a.ID, "method": a.Method, "ip": a.IP, "new_address": a.NewAddress, "browser": browser, "system": system,
			"approvers": len(targets)}, "")
	})
	if err != nil {
		return nil, err
	}

	prompt := LoginPrompt{ApprovalID: ShortApprovalID(a.ID), Account: accountOf(u), Method: a.Method, IP: a.IP, Browser: browser, System: system,
		NewAddress: a.NewAddress, At: a.CreatedAt, ExpiresAt: a.ExpiresAt, Targets: prompts}
	reached, reason := s.deliverApproval(ctx, a, prompt)
	if len(reached) == 0 {
		s.endUndeliverable(ctx, a, u, reason)
		return nil, &UndeliverableError{Reason: reason}
	}
	return &PendingLogin{ID: a.ID, ExpiresAt: a.ExpiresAt, Approvers: reached}, nil
}

// approvalTargets chooses the people asked to approve u's sign-in: u themself, and the owners when the switch says so and u is not one. It
// returns the rows to store (with the hash of each nonce) and the prompt targets (with the nonce itself, for the bot).
func (s *Service) approvalTargets(ctx context.Context, u *store.User, notifyOwner bool) ([]store.ApprovalTarget, []PromptTarget, error) {
	var (
		rows    []store.ApprovalTarget
		prompts []PromptTarget
	)
	add := func(who *store.User, role string) error {
		if who.TelegramID <= 0 {
			return nil
		}
		for _, t := range rows {
			if t.TelegramID == who.TelegramID {
				return nil
			}
		}
		nonce, hash, err := newApprovalNonce()
		if err != nil {
			return err
		}
		rows = append(rows, store.ApprovalTarget{TelegramID: who.TelegramID, UserID: who.ID, Role: role, NonceHash: hash})
		prompts = append(prompts, PromptTarget{TelegramID: who.TelegramID, Language: i18n.Normalize(who.Language), Role: role, Nonce: nonce})
		return nil
	}
	if err := add(u, store.ApproverSelf); err != nil {
		return nil, nil, err
	}
	if notifyOwner && u.Role != "owner" {
		staff, err := s.st.ListStaffContacts(ctx, s.st.Conn(), false)
		if err != nil {
			return nil, nil, err
		}
		for i := range staff {
			if staff[i].Role != "owner" || len(rows) >= maxApprovers {
				continue
			}
			if err := add(&staff[i], store.ApproverOwner); err != nil {
				return nil, nil, err
			}
		}
	}
	return rows, prompts, nil
}

// deliverApproval has the bot message the approvers and records what became of each message. It returns who was reached (the roles) or, when
// nobody was, why.
func (s *Service) deliverApproval(ctx context.Context, a *store.LoginApproval, p LoginPrompt) (reached []string, reason string) {
	results := map[int64]PromptResult{}
	reason = DeliverNoBot
	if s.approvals.bot != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), approvalDeliverTime)
		res, err := s.approvals.bot.SendLoginApproval(cctx, p)
		cancel()
		reason = DeliverBotDown
		if err != nil {
			s.cfg.Log.Warn("sign-in approval: the bot could not be asked", "err", err)
		} else {
			reason = DeliverNoAnswer
			for _, r := range res {
				results[r.TelegramID] = r
			}
		}
	}
	for _, t := range p.Targets {
		r, ok := results[t.TelegramID]
		if !ok {
			r = PromptResult{TelegramID: t.TelegramID, Error: reason}
		}
		if err := s.st.RecordApprovalDelivery(context.WithoutCancel(ctx), s.st.Conn(), a.ID, t.TelegramID, r.MessageID, r.Error); err != nil {
			s.cfg.Log.Warn("sign-in approval: delivery not recorded", "err", err)
		}
		if r.Error == "" {
			reached = append(reached, t.Role)
			continue
		}
		reason = pickReason(reason, r.Error)
	}
	return reached, reason
}

// pickReason keeps the most telling of two reasons a delivery failed: that someone blocked the bot says more than a failed call.
func pickReason(a, b string) string {
	rank := map[string]int{DeliverNoApprovers: 0, DeliverNoAnswer: 1, DeliverTelegram: 2, DeliverBotDown: 3, DeliverNoBot: 4, DeliverBlocked: 5}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// endUndeliverable ends a sign-in nobody was asked about, with its audit entry.
func (s *Service) endUndeliverable(ctx context.Context, a *store.LoginApproval, u *store.User, reason string) {
	ctx = context.WithoutCancel(ctx)
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		ok, err := s.st.EndLoginApproval(ctx, tx, a.ID, store.ApprovalUndeliverable, store.ApprovalPending)
		if err != nil || !ok {
			return err
		}
		return s.auditChange(ctx, tx, nil, "dashboard.login_undeliverable", "dashboard", u.ID, nil,
			map[string]any{"approval": a.ID, "reason": reason}, "")
	})
	if err != nil {
		s.cfg.Log.Error("sign-in approval: could not end an undeliverable sign-in", "approval", a.ID, "err", err)
	}
}

// What a poll answers, one of the store's statuses besides these.
const (
	// PollNone: the ticket is unknown, or the session was collected already.
	PollNone = "none"
)

// PollResult is what a browser that waits is told.
type PollResult struct {
	// Status is store.ApprovalPending, store.ApprovalApproved (the session may be made: Approval is set, and the sign-in is spent),
	// ApprovalDenied, ApprovalExpired, ApprovalCancelled, ApprovalUndeliverable, or PollNone.
	Status    string
	ExpiresAt time.Time
	// Delivered: at least one approver's message was sent. Failed: none could be (so nothing will come of the wait).
	Delivered, Failed bool
	Approvers         []string
	Approval          *store.LoginApproval
}

// PollLoginApproval tells a browser how its sign-in stands, by the hash of its cookie. An approved sign-in is spent on the first poll that
// sees it: the caller makes the session then, and no other poll gets one. A sign-in whose time is up ends here too.
func (s *Service) PollLoginApproval(ctx context.Context, ticketHash []byte) (*PollResult, error) {
	a, err := s.st.LoginApprovalByTicket(ctx, s.st.Conn(), ticketHash)
	if errors.Is(err, store.ErrNotFound) {
		return &PollResult{Status: PollNone}, nil
	}
	if err != nil {
		return nil, err
	}
	out := &PollResult{Status: a.Status, ExpiresAt: a.ExpiresAt}
	if a.Status == store.ApprovalConsumed {
		out.Status = PollNone
		return out, nil
	}
	if a.Status == store.ApprovalPending || a.Status == store.ApprovalApproved {
		if a.Due {
			// The database's clock decides: nothing is approved or collected after the time is up.
			if err := s.expireOne(ctx, a.ID); err != nil {
				return nil, err
			}
			out.Status = store.ApprovalExpired
			return out, nil
		}
	}
	switch a.Status {
	case store.ApprovalPending:
		targets, err := s.st.ApprovalTargets(ctx, s.st.Conn(), a.ID)
		if err != nil {
			return nil, err
		}
		out.Failed = len(targets) > 0
		for _, t := range targets {
			if t.SentAt != nil {
				out.Delivered, out.Failed = true, false
				out.Approvers = append(out.Approvers, t.Role)
			} else if t.SendError == "" {
				out.Failed = false // not tried yet
			}
		}
	case store.ApprovalApproved:
		ok, err := s.st.ConsumeLoginApproval(ctx, s.st.Conn(), a.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			out.Status = PollNone // another poll collected it, or its time ran out in this very moment
			return out, nil
		}
		a.Status = store.ApprovalConsumed
		out.Approval = a
	}
	return out, nil
}

// expireOne ends one sign-in whose time is up, if it is still open, with its audit entry.
func (s *Service) expireOne(ctx context.Context, id string) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		a, err := s.st.LockLoginApproval(ctx, tx, id)
		if err != nil {
			return err
		}
		return s.expireLocked(ctx, tx, a)
	})
}

// expireLocked ends a sign-in whose time is up (a is locked in tx); one that ended otherwise meanwhile stays as it is.
func (s *Service) expireLocked(ctx context.Context, tx pgx.Tx, a *store.LoginApproval) error {
	was := a.Status
	ok, err := s.st.EndLoginApproval(ctx, tx, a.ID, store.ApprovalExpired, store.ApprovalPending, store.ApprovalApproved)
	if err != nil || !ok {
		return err
	}
	return s.auditChange(ctx, tx, nil, "dashboard.login_expired", "dashboard", a.UserID, nil,
		map[string]any{"approval": a.ID, "was": was, "method": a.Method, "ip": a.IP}, "")
}

// CancelLoginApproval ends the sign-in a browser waits for because the person at it gave up. It reports whether one was waiting.
func (s *Service) CancelLoginApproval(ctx context.Context, ticketHash []byte) (bool, error) {
	a, err := s.st.LoginApprovalByTicket(ctx, s.st.Conn(), ticketHash)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var done bool
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		done = false
		ok, err := s.st.EndLoginApproval(ctx, tx, a.ID, store.ApprovalCancelled, store.ApprovalPending)
		if err != nil || !ok {
			return err
		}
		done = true
		return s.auditChange(ctx, tx, nil, "dashboard.login_cancelled", "dashboard", a.UserID, nil,
			map[string]any{"approval": a.ID, "by": "the person signing in", "method": a.Method, "ip": a.IP}, "")
	})
	return done, err
}

// The outcomes of an approver's answer (ApprovalDecision.Outcome).
const (
	OutcomeApproved        = "approved"         // this answer approved the sign-in
	OutcomeDenied          = "denied"           // this answer denied it
	OutcomeAlreadyApproved = "already_approved" // it was approved before (a second tap, or the other approver's)
	OutcomeAlreadyDenied   = "already_denied"
	OutcomeExpired         = "expired"
	OutcomeCancelled       = "cancelled"
	OutcomeInvalid         = "invalid"   // not a sign-in this person was asked about, or a wrong nonce: nothing happened
	OutcomeForbidden       = "forbidden" // this person may no longer answer for it: nothing happened
)

// ApprovalDecision is what an approver is told about their answer.
type ApprovalDecision struct {
	Outcome string
	Account ApprovalAccount // who signs in
	By      *ApprovalAccount
	At      time.Time
}

// DecideLoginApproval takes an approver's tap. telegramID is the person who tapped (Telegram vouches for it), approvalID and nonce are what
// the button carried. It changes nothing unless the sign-in is still waiting, the nonce is the one given to that person for it, and they may
// still answer for the account: the staff member themself while they are active staff, or an owner while they are an active owner. A
// second tap, a tap on an old or foreign button, or a forged one, does nothing. The first of two answers at once wins.
func (s *Service) DecideLoginApproval(ctx context.Context, telegramID int64, approvalID, nonce string, approve bool) (*ApprovalDecision, error) {
	id, ok := ParseApprovalID(approvalID)
	if !ok || telegramID <= 0 || nonce == "" || len(nonce) > 64 {
		return &ApprovalDecision{Outcome: OutcomeInvalid}, nil
	}
	if !s.approvals.allowAnswer(telegramID, time.Now()) {
		return &ApprovalDecision{Outcome: OutcomeInvalid}, nil
	}
	var out *ApprovalDecision
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		out = nil
		a, err := s.st.LockLoginApproval(ctx, tx, id)
		if errors.Is(err, store.ErrNotFound) {
			out = &ApprovalDecision{Outcome: OutcomeInvalid}
			return nil
		}
		if err != nil {
			return err
		}
		t, err := s.st.GetApprovalTarget(ctx, tx, id, telegramID)
		if errors.Is(err, store.ErrNotFound) {
			out = &ApprovalDecision{Outcome: OutcomeInvalid}
			return nil
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(hashNonce(nonce), t.NonceHash) != 1 {
			out = &ApprovalDecision{Outcome: OutcomeInvalid}
			return s.auditChange(ctx, tx, nil, "dashboard.login_approval_invalid", "dashboard", a.UserID, nil,
				map[string]any{"approval": a.ID, "telegram_id": telegramID, "reason": "nonce"}, "")
		}
		staffer, err := s.st.GetUser(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		out = &ApprovalDecision{Account: accountOf(staffer)}
		switch a.Status {
		case store.ApprovalDenied:
			out.Outcome = OutcomeAlreadyDenied
			out.At, out.By = s.decidedBy(ctx, tx, a)
			return nil
		case store.ApprovalApproved, store.ApprovalConsumed:
			out.Outcome = OutcomeAlreadyApproved
			out.At, out.By = s.decidedBy(ctx, tx, a)
			return nil
		case store.ApprovalExpired:
			out.Outcome = OutcomeExpired
			return nil
		case store.ApprovalCancelled, store.ApprovalUndeliverable:
			out.Outcome = OutcomeCancelled
			return nil
		}
		// Still waiting. Its time may be up.
		if a.Due {
			out.Outcome = OutcomeExpired
			return s.expireLocked(ctx, tx, a)
		}
		approver, err := s.st.GetUser(ctx, tx, t.UserID)
		if err != nil {
			return err
		}
		if !s.mayAnswer(approver, t, a, telegramID) {
			out.Outcome = OutcomeForbidden
			return nil
		}
		if staffer.Status != "active" || !IsStaffRole(staffer.Role) {
			// The account that signs in lost its access since: there is nothing left to approve.
			out.Outcome = OutcomeForbidden
			if _, err := s.st.EndLoginApproval(ctx, tx, a.ID, store.ApprovalCancelled, store.ApprovalPending); err != nil {
				return err
			}
			return s.auditChange(ctx, tx, nil, "dashboard.login_cancelled", "dashboard", a.UserID, nil,
				map[string]any{"approval": a.ID, "by": "the account may no longer sign in"}, "")
		}
		decided, err := s.st.DecideLoginApproval(ctx, tx, a.ID, approve, approver.ID, t.Role, approvalPickup)
		if err != nil {
			return err
		}
		if !decided { // its time ran out in this moment
			out.Outcome = OutcomeExpired
			return nil
		}
		action, outcome := "dashboard.login_denied", OutcomeDenied
		if approve {
			action, outcome = "dashboard.login_approved", OutcomeApproved
		}
		by := accountOf(approver)
		out.Outcome, out.By, out.At = outcome, &by, time.Now()
		if err := s.st.MarkApprovalMessagesClosed(ctx, tx, a.ID, telegramID); err != nil {
			return err // the tapped message is written over by the bot's answer
		}
		return s.auditChange(ctx, tx, approver, action, "dashboard", a.UserID, nil,
			map[string]any{"approval": a.ID, "via": t.Role, "method": a.Method, "ip": a.IP, "account": staffer.Username}, "")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mayAnswer: the staff member themself while they are active staff, or an owner while they are an active owner.
func (s *Service) mayAnswer(approver *store.User, t *store.ApprovalTarget, a *store.LoginApproval, telegramID int64) bool {
	if approver.TelegramID != telegramID || approver.Status != "active" {
		return false
	}
	switch t.Role {
	case store.ApproverSelf:
		return approver.ID == a.UserID && IsStaffRole(approver.Role)
	case store.ApproverOwner:
		return approver.Role == "owner"
	}
	return false
}

// decidedBy names who decided a sign-in, and when (zero values when nobody did).
func (s *Service) decidedBy(ctx context.Context, tx pgx.Tx, a *store.LoginApproval) (time.Time, *ApprovalAccount) {
	var at time.Time
	if a.DecidedAt != nil {
		at = *a.DecidedAt
	}
	if a.DecidedBy == nil {
		return at, nil
	}
	u, err := s.st.GetUser(ctx, tx, *a.DecidedBy)
	if err != nil {
		return at, nil
	}
	acc := accountOf(u)
	return at, &acc
}

// SetLoginApproval changes the owner's switches. actor is the owner who changed them in the dashboard, or nil for the server's command line
// (via says which: "dashboard" or "server"). notifyOwner nil leaves that switch as it is.
//
// Turning approval on is refused (*UndeliverableError) unless the bot can message the owner who turns it on, so that it cannot be switched on
// with no way to answer; the owner gets the notice first. Turning it off ends the sign-ins that wait (their browsers are told, nobody is let
// in). The owners are told in Telegram of either change, so a switch changed by a stolen session is noticed.
func (s *Service) SetLoginApproval(ctx context.Context, actor *store.User, via string, enabled bool, notifyOwner *bool) error {
	before, err := s.LoginApprovalState(ctx)
	if err != nil {
		return err
	}
	turningOn := enabled && !before.Enabled
	if turningOn {
		if err := s.preflightApproval(ctx, actor, via); err != nil {
			return err
		}
	}
	values := map[string]string{SettingLoginApproval: boolString(enabled)}
	if notifyOwner != nil {
		values[SettingLoginApprovalNotifyOwner] = boolString(*notifyOwner)
	}
	if _, err := s.SetSettings(ctx, actor, values); err != nil {
		return err
	}
	if before.Enabled == enabled {
		return nil
	}
	if !enabled {
		if _, err := s.CancelWaitingLoginApprovals(ctx, "approval was switched off"); err != nil {
			s.cfg.Log.Error("sign-in approval: waiting sign-ins were not ended", "err", err)
		}
	}
	// The owner who turned it on was told by the pre-flight.
	s.noticeOwners(ctx, actor, via, enabled, turningOn)
	return nil
}

// preflightApproval proves the bot can message the owner who turns approval on, by telling them it is being turned on. Without a way to
// answer, the switch would lock the dashboard.
func (s *Service) preflightApproval(ctx context.Context, actor *store.User, via string) error {
	if s.approvals.bot == nil {
		return &UndeliverableError{Reason: DeliverNoBot}
	}
	if actor == nil || actor.TelegramID <= 0 {
		return &UndeliverableError{Reason: DeliverNoApprovers}
	}
	acc := accountOf(actor)
	n := ApprovalNotice{Kind: NoticeEnabled, By: &acc, Source: via,
		Targets: []ResolveTarget{{TelegramID: actor.TelegramID, Language: i18n.Normalize(actor.Language)}}}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), approvalDeliverTime)
	defer cancel()
	res, err := s.approvals.bot.NoticeLoginApproval(cctx, n)
	if err != nil {
		s.cfg.Log.Warn("sign-in approval: the bot could not be asked", "err", err)
		return &UndeliverableError{Reason: DeliverBotDown}
	}
	for _, r := range res {
		if r.TelegramID == actor.TelegramID {
			if r.Error != "" {
				return &UndeliverableError{Reason: r.Error}
			}
			return nil
		}
	}
	return &UndeliverableError{Reason: DeliverNoAnswer}
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// noticeOwners tells every owner that the switch was changed, best effort (not the actor, when skipActor says they were told already).
func (s *Service) noticeOwners(ctx context.Context, actor *store.User, via string, enabled, skipActor bool) {
	if s.approvals.bot == nil {
		return
	}
	staff, err := s.st.ListStaffContacts(ctx, s.st.Conn(), false)
	if err != nil {
		s.cfg.Log.Warn("sign-in approval: owners not listed for the notice", "err", err)
		return
	}
	n := ApprovalNotice{Kind: NoticeDisabled, Source: via}
	if enabled {
		n.Kind = NoticeEnabled
	}
	if actor != nil {
		acc := accountOf(actor)
		n.By = &acc
	}
	for _, o := range staff {
		if o.Role == "owner" && o.TelegramID > 0 && (!skipActor || actor == nil || o.ID != actor.ID) {
			n.Targets = append(n.Targets, ResolveTarget{TelegramID: o.TelegramID, Language: i18n.Normalize(o.Language)})
		}
	}
	if len(n.Targets) == 0 {
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), approvalDeliverTime)
	defer cancel()
	if _, err := s.approvals.bot.NoticeLoginApproval(cctx, n); err != nil {
		s.cfg.Log.Warn("sign-in approval: the owners were not told of the change", "err", err)
	}
}

// CancelWaitingLoginApprovals ends every sign-in that waits, with the reason in the audit log. It says how many it ended.
func (s *Service) CancelWaitingLoginApprovals(ctx context.Context, reason string) (int, error) {
	n := 0
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		n = 0
		waiting, err := s.st.WaitingLoginApprovals(ctx, tx, "")
		if err != nil {
			return err
		}
		for i := range waiting {
			a := &waiting[i]
			ok, err := s.st.EndLoginApproval(ctx, tx, a.ID, store.ApprovalCancelled, store.ApprovalPending)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			n++
			if err := s.auditChange(ctx, tx, nil, "dashboard.login_cancelled", "dashboard", a.UserID, nil,
				map[string]any{"approval": a.ID, "by": reason}, ""); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

// WaitingLoginApprovals says how many sign-ins wait for an answer now.
func (s *Service) WaitingLoginApprovals(ctx context.Context) (int, error) {
	return s.st.CountWaitingLoginApprovals(ctx, s.st.Conn())
}

// RunLoginApprovalWorker ends the sign-ins whose time is up, writes the result into the messages of those that ended, and forgets old ones,
// until ctx ends.
func (s *Service) RunLoginApprovalWorker(ctx context.Context, log *slog.Logger) {
	if log == nil {
		log = s.cfg.Log
	}
	tick := time.NewTicker(approvalTick)
	defer tick.Stop()
	prune := time.NewTicker(6 * time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := s.SweepLoginApprovals(ctx); err != nil && ctx.Err() == nil {
				log.Warn("sign-in approvals: sweep failed", "err", err)
			}
		case <-prune.C:
			if n, err := s.st.PruneLoginApprovals(ctx, s.st.Conn(), time.Now().Add(-approvalKeep)); err != nil && ctx.Err() == nil {
				log.Warn("sign-in approvals: prune failed", "err", err)
			} else if n > 0 {
				log.Info("sign-in approvals: old ones forgotten", "count", n)
			}
		}
	}
}

// SweepLoginApprovals does one round of the worker: it ends the sign-ins whose time is up (the audit log says so), then writes the result
// into the messages of the sign-ins that ended, within a while of their end.
func (s *Service) SweepLoginApprovals(ctx context.Context) error {
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		due, err := s.st.DueLoginApprovals(ctx, tx, 100)
		if err != nil {
			return err
		}
		for i := range due {
			if err := s.expireLocked(ctx, tx, &due[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.closeApprovalMessages(ctx)
}

// closeApprovalMessages writes the result into the messages of sign-ins that ended. A bot that cannot be reached leaves them for the next
// round (until they are too old).
func (s *Service) closeApprovalMessages(ctx context.Context) error {
	if s.approvals.bot == nil {
		return nil
	}
	msgs, err := s.st.ApprovalMessagesToClose(ctx, s.st.Conn(), approvalCloseWindow, 100)
	if err != nil || len(msgs) == 0 {
		return err
	}
	byApproval := map[string][]store.ApprovalMessage{}
	var order []string
	for _, m := range msgs {
		if _, ok := byApproval[m.Approval.ID]; !ok {
			order = append(order, m.Approval.ID)
		}
		byApproval[m.Approval.ID] = append(byApproval[m.Approval.ID], m)
	}
	for _, id := range order {
		group := byApproval[id]
		a := &group[0].Approval
		res, ok := s.resolutionFor(ctx, a, group)
		if !ok {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, approvalDeliverTime)
		err := s.approvals.bot.ResolveLoginApproval(cctx, res)
		cancel()
		if err != nil {
			s.cfg.Log.Debug("sign-in approval: messages not written over yet", "approval", id, "err", err)
			continue
		}
		var tgs []int64
		for _, m := range group {
			tgs = append(tgs, m.Target.TelegramID)
		}
		if err := s.st.MarkApprovalMessagesClosed(ctx, s.st.Conn(), id, tgs...); err != nil {
			return err
		}
	}
	return nil
}

// resolutionFor builds the text-free description of how a sign-in ended, for the messages in group.
func (s *Service) resolutionFor(ctx context.Context, a *store.LoginApproval, group []store.ApprovalMessage) (LoginResolution, bool) {
	var result string
	switch a.Status {
	case store.ApprovalApproved, store.ApprovalConsumed:
		result = ResolutionApproved
	case store.ApprovalDenied:
		result = ResolutionDenied
	case store.ApprovalExpired:
		result = ResolutionExpired
	case store.ApprovalCancelled, store.ApprovalUndeliverable:
		result = ResolutionCancelled
	default:
		return LoginResolution{}, false
	}
	ids := []string{a.UserID}
	if a.DecidedBy != nil {
		ids = append(ids, *a.DecidedBy)
	}
	for _, m := range group {
		ids = append(ids, m.Target.UserID)
	}
	users, err := s.st.UsersByID(ctx, s.st.Conn(), ids)
	if err != nil {
		return LoginResolution{}, false
	}
	staffer, ok := users[a.UserID]
	if !ok {
		return LoginResolution{}, false
	}
	res := LoginResolution{ApprovalID: ShortApprovalID(a.ID), Result: result, Account: accountOf(&staffer)}
	if a.DecidedAt != nil {
		res.At = *a.DecidedAt
	} else {
		res.At = a.ExpiresAt
	}
	if a.DecidedBy != nil {
		if by, ok := users[*a.DecidedBy]; ok {
			acc := accountOf(&by)
			res.By = &acc
		}
	}
	for _, m := range group {
		lang := ""
		if u, ok := users[m.Target.UserID]; ok {
			lang = u.Language
		}
		res.Targets = append(res.Targets, ResolveTarget{TelegramID: m.Target.TelegramID, Language: i18n.Normalize(lang), MessageID: *m.Target.MessageID})
	}
	return res, true
}

// allowAnswer limits the answers one Telegram account may send in a minute.
func (a *loginApprovalState) allowAnswer(id int64, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.seen == nil {
		a.seen = map[int64][]time.Time{}
	}
	cut := now.Add(-time.Minute)
	kept := a.seen[id][:0]
	for _, t := range a.seen[id] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= approvalDecideRate {
		a.seen[id] = kept
		return false
	}
	a.seen[id] = append(kept, now)
	if len(a.seen) > 1000 { // a long-running core forgets the accounts that stopped
		for k, v := range a.seen {
			if len(v) == 0 || !v[len(v)-1].After(cut) {
				delete(a.seen, k)
			}
		}
	}
	return true
}
