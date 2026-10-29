package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/loginwire"
)

// The approval of dashboard sign-ins (docs/04-dashboard.md, "Signing in"): core has no Telegram token, so it asks the bot, over these
// endpoints and with its service token like every other /internal call, to message the people who may approve a sign-in, to write the end
// of a sign-in into those messages and to tell the owners the switch changed. The JSON is internal/loginwire's. Nothing here decides who may
// approve anything: core made the list of people, the nonces and the texts' facts, and the bot sends them.

// LoginApprovals is what the endpoints ask of the bot's handler.
type LoginApprovals interface {
	// SendLoginApproval messages each approver and answers one result for each.
	SendLoginApproval(ctx context.Context, r loginwire.Request) []loginwire.Result
	// ResolveLoginApproval writes the end of a sign-in into the messages; an error means a later try may work.
	ResolveLoginApproval(ctx context.Context, r loginwire.Resolution) error
	// NoticeLoginApproval tells the owners that the switch changed and answers one result for each.
	NoticeLoginApproval(ctx context.Context, n loginwire.Notice) []loginwire.Result
}

// loginApprovalCall runs one of the endpoints: core's token, a strict body of at most loginwire.MaxBody bytes, valid, then do. do answers
// the JSON to send (nil: 204). The work goes on if core stops waiting.
func loginApprovalCall[T interface{ Valid() error }](coreToken string, log *slog.Logger, what string, do func(ctx context.Context, in T) (any, error)) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in T
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, loginwire.MaxBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			problem(w, http.StatusBadRequest, ProblemBadRequest, "the body is not what this endpoint takes")
			return
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) { // one object, and nothing after it
			problem(w, http.StatusBadRequest, ProblemBadRequest, "the body is not what this endpoint takes")
			return
		}
		if err := in.Valid(); err != nil {
			problem(w, http.StatusBadRequest, ProblemBadRequest, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
		defer cancel()
		out, err := do(ctx, in)
		if err != nil {
			log.Warn("sign-in approval: "+what+" failed", "err", err)
			http.Error(w, "not done", http.StatusServiceUnavailable)
			return
		}
		if out == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// LoginApprovalRequest is POST /internal/login-approval/request: message the approvers of a sign-in. It answers 200 with a result for each.
func LoginApprovalRequest(h LoginApprovals, coreToken string, log *slog.Logger) http.Handler {
	return loginApprovalCall(coreToken, log, "request", func(ctx context.Context, in loginwire.Request) (any, error) {
		return loginwire.Response{Results: h.SendLoginApproval(ctx, in)}, nil
	})
}

// LoginApprovalResolve is POST /internal/login-approval/resolve: write the end of a sign-in into the messages. It answers 204, or 503 when
// a later try may work.
func LoginApprovalResolve(h LoginApprovals, coreToken string, log *slog.Logger) http.Handler {
	return loginApprovalCall(coreToken, log, "resolve", func(ctx context.Context, in loginwire.Resolution) (any, error) {
		return nil, h.ResolveLoginApproval(ctx, in)
	})
}

// LoginApprovalNotice is POST /internal/login-approval/notice: tell the owners the switch changed. It answers 200 with a result for each.
func LoginApprovalNotice(h LoginApprovals, coreToken string, log *slog.Logger) http.Handler {
	return loginApprovalCall(coreToken, log, "notice", func(ctx context.Context, in loginwire.Notice) (any, error) {
		return loginwire.Response{Results: h.NoticeLoginApproval(ctx, in)}, nil
	})
}
