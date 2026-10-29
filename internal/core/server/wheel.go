package server

import (
	"context"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// The daily wheel of fortune for the bot (growth.proto). Core keeps every rule (who may spin, the draw, the budget, once a day); the bot words the
// answers.

// GetWheel is the wheel as this customer sees it: what could be won, the chance of each, whether they may spin and whether they have today.
func (s *Server) GetWheel(ctx context.Context, req *corev1.GetWheelRequest) (*corev1.WheelState, error) {
	st, err := s.dom.Wheel(ctx, req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	return wheelStateToProto(st), nil
}

// SpinWheel gives the customer their spin of the day. A banned customer is PERMISSION_DENIED; every other refusal is a status of the result.
func (s *Server) SpinWheel(ctx context.Context, req *corev1.SpinWheelRequest) (*corev1.WheelSpinResult, error) {
	r, err := s.dom.SpinWheel(ctx, req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.WheelSpinResult{Status: r.Status, Prize: r.Prize, Index: int32(r.Index), Balance: r.Balance, Day: r.Day, //nolint:gosec // a place on a wheel of at most 12
		Orders: r.Orders, MinOrders: r.MinOrders}
	if !r.ResetsAt.IsZero() {
		out.ResetsAt = r.ResetsAt.Unix()
	}
	return out, nil
}

func wheelStateToProto(st *domain.WheelState) *corev1.WheelState {
	out := &corev1.WheelState{On: st.On}
	if !st.On {
		return out
	}
	out.Day, out.ResetsAt, out.Reason = st.Day, st.ResetsAt.Unix(), st.Reason
	out.Orders, out.MinOrders = st.Orders, st.MinOrders
	out.Spun, out.Prize, out.Open = st.Spun, st.Prize, st.Open
	out.NoPrizeWeight, out.TotalWeight = st.NoPrizeWeight, st.TotalWeight
	for _, p := range st.Prizes {
		out.Prizes = append(out.Prizes, &corev1.WheelPrize{Amount: p.Amount, Weight: p.Weight})
	}
	return out
}
