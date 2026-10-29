// Package loginwire is what core and the bot say to each other about the approval of dashboard sign-ins (docs/04-dashboard.md, "Signing in"):
// the JSON of core's calls to the bot's internal endpoints, and the data of the buttons the bot puts under a message. Core has no Telegram
// token, so it asks the bot to message the approvers, to write the end of a sign-in into those messages and to tell the owners that the
// switch changed; the approver's tap goes the other way over gRPC (proto/core/v1/loginapproval.proto). Both sides import these types so that
// a field cannot drift apart.
package loginwire

import (
	"strings"
)

// The bot's endpoints for core, authenticated with core's service token like the others under /internal.
const (
	PathRequest = "/internal/login-approval/request" // message the approvers of one sign-in
	PathResolve = "/internal/login-approval/resolve" // write the end of a sign-in into those messages
	PathNotice  = "/internal/login-approval/notice"  // tell the owners the switch changed
)

// Limits both sides hold.
const (
	MaxTargets = 12
	MaxBody    = 16 << 10
)

// Why a message could not be sent (Result.Error).
const (
	ErrBlocked  = "blocked"  // the person blocked the bot, or never started it
	ErrTelegram = "telegram" // Telegram refused the message or could not be reached
)

// Account is the staff member who signs in, or who decided.
type Account struct {
	Username   string `json:"username,omitempty"`
	TelegramID int64  `json:"telegram_id"`
	Role       string `json:"role"`
}

// Target is one person: their Telegram id and language, and, as the call needs, who they are to the sign-in (self | owner), the nonce their
// buttons carry, and the Telegram message to write over.
type Target struct {
	TelegramID int64  `json:"telegram_id"`
	Language   string `json:"language"`
	Role       string `json:"role,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	MessageID  int64  `json:"message_id,omitempty"`
}

// Request asks the bot to message the approvers of one sign-in. Browser and System are the codes of webauth.DescribeUserAgent.
type Request struct {
	ApprovalID string   `json:"approval_id"` // 32 hexadecimal digits
	Account    Account  `json:"account"`
	Method     string   `json:"method"` // link | password | password_initial
	IP         string   `json:"ip,omitempty"`
	Browser    string   `json:"browser"`
	System     string   `json:"system"`
	NewAddress bool     `json:"new_address,omitempty"`
	At         int64    `json:"at"`         // unix seconds
	ExpiresAt  int64    `json:"expires_at"` // unix seconds
	Targets    []Target `json:"targets"`
}

// Result is what became of one message: the Telegram message id when it was sent, else a short code.
type Result struct {
	TelegramID int64  `json:"telegram_id"`
	MessageID  int64  `json:"message_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Response answers a Request or a Notice with one Result for each target.
type Response struct {
	Results []Result `json:"results"`
}

// Resolution asks the bot to write how a sign-in ended into the messages sent for it: Result is approved, denied, expired or cancelled.
type Resolution struct {
	ApprovalID string   `json:"approval_id"`
	Result     string   `json:"result"`
	Account    Account  `json:"account"`
	By         *Account `json:"by,omitempty"`
	At         int64    `json:"at"`
	Targets    []Target `json:"targets"`
}

// Notice asks the bot to tell the owners that the switch was turned on or off (Kind: enabled | disabled). By is who did it (absent: the
// server's command line), Source dashboard or server.
type Notice struct {
	Kind    string   `json:"kind"`
	By      *Account `json:"by,omitempty"`
	Source  string   `json:"source"`
	Targets []Target `json:"targets"`
}

// The verbs of a button's data. The data is "lga:<verb>:<approval id>:<nonce>": the approval id as 32 hexadecimal digits and the nonce of
// the person the message was sent to, 55 bytes in all (Telegram takes 64).
const (
	callbackPrefix = "lga"
	VerbApprove    = "y"
	VerbDeny       = "n"
)

// CallbackData is the data of the Approve or Deny button of one approver's message.
func CallbackData(approve bool, approvalID, nonce string) string {
	verb := VerbDeny
	if approve {
		verb = VerbApprove
	}
	return callbackPrefix + ":" + verb + ":" + approvalID + ":" + nonce
}

// ParseCallback reads the part of a button's data after "lga:" (the bot's dispatcher splits the verb off): ok is false for anything but a
// well-formed Approve or Deny.
func ParseCallback(rest string) (approve bool, approvalID, nonce string, ok bool) {
	parts := strings.Split(rest, ":")
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return false, "", "", false
	}
	switch parts[0] {
	case VerbApprove:
		approve = true
	case VerbDeny:
	default:
		return false, "", "", false
	}
	return approve, parts[1], parts[2], true
}

// CallbackVerb is the verb the bot's dispatcher routes the buttons by.
const CallbackVerb = callbackPrefix
