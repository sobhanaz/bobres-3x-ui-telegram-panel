package server

import (
	"context"
	"errors"
	"math"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// giftOutcomes maps why a code was not redeemed to what the bot is told.
var giftOutcomes = map[string]corev1.GiftCodeOutcome{
	domain.GiftInvalid:         corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_INVALID,
	domain.GiftUsedUp:          corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_USED_UP,
	domain.GiftAlreadyRedeemed: corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_ALREADY_REDEEMED,
	domain.GiftLocked:          corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_LOCKED,
	domain.GiftOff:             corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_OFF,
}

// RedeemGiftCode applies a gift code to a customer's wallet. What the customer
// can get wrong is an outcome of the answer; a broken request, a banned
// customer and a failing backend are errors. The code is never logged: it is
// not in an error, and the interceptors log the method, not the message.
func (s *Server) RedeemGiftCode(ctx context.Context, req *corev1.RedeemGiftCodeRequest) (*corev1.RedeemGiftCodeResponse, error) {
	ctx = fromBot(ctx)
	got, err := s.dom.RedeemGiftCode(ctx, domain.RedeemGiftCodeParams{
		UserID: req.GetUserId(), Code: req.GetCode(), RequestKey: req.GetIdempotencyKey(),
	})
	var ge *domain.GiftCodeError
	if errors.As(err, &ge) {
		out := giftOutcomes[ge.Reason]
		if out == corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_UNSPECIFIED {
			return nil, fail(err) // a reason this server does not know: never a guess
		}
		resp := &corev1.RedeemGiftCodeResponse{Outcome: out}
		if ge.Reason == domain.GiftLocked {
			// Rounded up, so "0 seconds" is never said of a lock that has not ended.
			resp.RetryAfterSeconds = int64(math.Ceil(ge.RetryAfter.Seconds()))
		}
		return resp, nil
	}
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.RedeemGiftCodeResponse{
		Outcome:  corev1.GiftCodeOutcome_GIFT_CODE_OUTCOME_REDEEMED,
		Credited: &commonv1.Money{Amount: got.Amount, Currency: got.Currency},
		Balance:  &commonv1.Money{Amount: got.Balance, Currency: got.Currency},
		Replayed: got.Replayed,
	}, nil
}
