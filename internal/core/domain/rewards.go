package domain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Reward codes: the owner marks a plan with a percent (Plan.RewardPercent); after each paid purchase or renewal of it a customer gets a
// discount code for their next purchase of the same plan. The code is made in the transaction that marks the order done, bound to the
// buyer and the plan, with one use and 30 days; the order is its key, so a retried delivery never makes a second one.
const (
	// RewardCodeValidity is how long a reward code can be used.
	RewardCodeValidity = 30 * 24 * time.Hour
	// MaxRewardPercent is the largest percent a plan may give.
	MaxRewardPercent = 90
	// rewardAlphabet has no look-alike characters (no 0 O 1 I).
	rewardAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	rewardTries    = 8
)

// newRewardCode is "THX-" and eight characters: 32^8 codes, and the code is the secret of its buyer.
func newRewardCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, 0, 12)
	out = append(out, "THX-"...)
	for _, c := range b {
		out = append(out, rewardAlphabet[int(c)%len(rewardAlphabet)])
	}
	return string(out)
}

// planRewardOf settles the reward percent a plan is saved with: nil keeps the stored one (the bot's plan editor never sends it), 0 takes
// it away, a free trial and a traffic package give none.
func planRewardOf(p, old *store.Plan) {
	switch {
	case p.IsTrial || p.IsTopup:
		p.RewardPercent = nil
	case p.RewardPercent == nil:
		if old != nil {
			p.RewardPercent = old.RewardPercent
		}
	case *p.RewardPercent == 0:
		p.RewardPercent = nil
	}
}

// givesReward reports whether a paid order of a plan earns its buyer a reward code: a paid purchase or renewal of a plan the owner marked,
// by a customer (a reseller buys at tier prices and uses no codes), not a free trial, not a traffic package, not one service of a bulk purchase.
func givesReward(o *store.Order, plan *store.Plan, buyer *store.User) bool {
	switch {
	case plan.RewardPercent == nil || plan.IsTrial || plan.IsTopup:
		return false
	case o.Type != "new" && o.Type != "renew":
		return false
	case o.Amount <= 0 || o.BulkID != nil:
		return false
	case buyer == nil || isReseller(buyer):
		return false
	}
	return true
}

// issueReward runs in the transaction that marks a purchase done: it makes the buyer's reward code and queues the message that tells them.
func (s *Service) issueReward(ctx context.Context, tx pgx.Tx, o *store.Order, plan *store.Plan, buyer *store.User) error {
	if !givesReward(o, plan, buyer) {
		return nil
	}
	expires := time.Now().Add(RewardCodeValidity).UTC()
	for range rewardTries {
		d := &store.Discount{Code: newRewardCode(), Percent: plan.RewardPercent, ExpiresAt: &expires, UserID: &buyer.ID, PlanID: &plan.ID, SourceOrderID: &o.ID}
		made, err := s.st.InsertRewardCode(ctx, tx, d)
		if err != nil {
			return err
		}
		if made {
			return s.publish(ctx, tx, events.RewardCodeIssued, events.RewardCodeIssuedEvent{
				OrderID: o.ID, UserID: buyer.ID, TelegramID: buyer.TelegramID, Code: d.Code, Percent: *plan.RewardPercent,
				PlanID: plan.ID, PlanNames: plan.NameI18n, ExpiresAt: expires.Unix(),
			})
		}
		// Not made: this purchase has its code already (a retry: nothing to do), or the text was taken (another try).
		if _, err := s.st.RewardCodeOfOrder(ctx, tx, o.ID); err == nil {
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return fmt.Errorf("a reward code for order %s: no free code after %d tries", o.ID, rewardTries)
}
