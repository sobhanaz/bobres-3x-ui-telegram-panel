package server

import (
	"context"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// approvalServer is LoginApprovalService (proto/core/v1/loginapproval.proto): an approver's tap on Approve or Deny in the bot. Core decides
// everything (domain.DecideLoginApproval); this only carries the question and the answer.
type approvalServer struct {
	corev1.UnimplementedLoginApprovalServiceServer
	dom *domain.Service
}

// DecideLoginApproval takes one tap. A tap that does nothing (stale, repeated, forged, from someone who may no longer answer) is an answer
// with its outcome, not an error: the bot says it in the chat.
func (a *approvalServer) DecideLoginApproval(ctx context.Context, req *corev1.DecideLoginApprovalRequest) (*corev1.DecideLoginApprovalResponse, error) {
	d, err := a.dom.DecideLoginApproval(fromBot(ctx), req.GetTelegramId(), req.GetApprovalId(), req.GetNonce(), req.GetApprove())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.DecideLoginApprovalResponse{Outcome: d.Outcome, AccountUsername: d.Account.Username, AccountTelegramId: d.Account.TelegramID,
		AccountRole: d.Account.Role}
	if d.By != nil {
		out.ByUsername, out.ByTelegramId, out.ByRole = d.By.Username, d.By.TelegramID, d.By.Role
	}
	if !d.At.IsZero() {
		out.At = d.At.Unix()
	}
	return out, nil
}
