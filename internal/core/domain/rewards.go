package domain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Reward codes: the owner marks a plan with a reward, a percent or an amount in the plan's currency (Plan.RewardPercent, RewardAmount), a number
// of days it lasts (RewardDays) and a text the buyer reads with it (RewardTextI18n). After each paid purchase or renewal of the plan a customer
// gets a discount code for ANY plan they buy next. The code is made in the transaction that marks the order done, bound to the buyer, with one
// use; the order is its key, so a retried delivery never makes a second one.
const (
	// DefaultRewardDays is how long a reward code lasts when the owner sets nothing else.
	DefaultRewardDays = 10
	// MaxRewardDays is the longest a reward code may last.
	MaxRewardDays = 365
	// MaxRewardPercent is the largest percent a plan may give.
	MaxRewardPercent = 90
	// MaxRewardText is the longest text (characters, per language) the owner may write for the buyer.
	MaxRewardText = 500
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

// planRewardOf settles the reward a plan is saved with. KeepReward keeps the stored one (the bot's plan editor and the on/off switch never
// send one); otherwise what is given is saved, a zero percent or amount meaning none. A free trial and a traffic package give none, and a plan
// that gives none keeps no days or text.
func planRewardOf(p, old *store.Plan) {
	if p.KeepReward {
		p.RewardPercent, p.RewardAmount, p.RewardDays, p.RewardTextI18n = nil, nil, 0, nil
		if old != nil {
			p.RewardPercent, p.RewardAmount, p.RewardDays, p.RewardTextI18n = old.RewardPercent, old.RewardAmount, old.RewardDays, old.RewardTextI18n
		}
	}
	if p.RewardPercent != nil && *p.RewardPercent == 0 {
		p.RewardPercent = nil
	}
	if p.RewardAmount != nil && *p.RewardAmount == 0 {
		p.RewardAmount = nil
	}
	if p.IsTrial || p.IsTopup || (p.RewardPercent == nil && p.RewardAmount == nil) {
		p.RewardPercent, p.RewardAmount, p.RewardTextI18n = nil, nil, nil
		p.RewardDays = DefaultRewardDays
		return
	}
	if p.RewardDays == 0 {
		p.RewardDays = DefaultRewardDays
	}
}

// validateReward checks the reward a caller gives (it is not looked at when the caller keeps the stored one).
func validateReward(p *store.Plan) error {
	if p.KeepReward {
		return nil
	}
	if r := p.RewardPercent; r != nil && (*r < 0 || *r > MaxRewardPercent) {
		return &FieldError{"reward_percent", fmt.Sprintf("0 (none) to %d", MaxRewardPercent)}
	}
	if a := p.RewardAmount; a != nil && (*a < 0 || *a > maxTopupMinor) {
		return &FieldError{"reward_amount", "out of range"}
	}
	if p.RewardPercent != nil && *p.RewardPercent > 0 && p.RewardAmount != nil && *p.RewardAmount > 0 {
		return &FieldError{"reward_amount", "a reward is a percent or an amount, not both"}
	}
	if p.RewardDays < 0 || p.RewardDays > MaxRewardDays {
		return &FieldError{"reward_days", fmt.Sprintf("1 to %d days", MaxRewardDays)}
	}
	text := map[string]string{}
	for _, lang := range []string{"fa", "en"} {
		if v := strings.TrimSpace(p.RewardTextI18n[lang]); v != "" {
			if utf8.RuneCountInString(v) > MaxRewardText {
				return &FieldError{"reward_text", fmt.Sprintf("a reward text is at most %d characters", MaxRewardText)}
			}
			text[lang] = v
		}
	}
	p.RewardTextI18n = text
	return nil
}

// givesReward reports whether a paid order of a plan earns its buyer a reward code: a paid purchase or renewal of a plan the owner marked,
// by a customer (a reseller buys at tier prices and uses no codes), not a free trial, not a traffic package, not one service of a bulk purchase.
func givesReward(o *store.Order, plan *store.Plan, buyer *store.User) bool {
	switch {
	case (plan.RewardPercent == nil && plan.RewardAmount == nil) || plan.IsTrial || plan.IsTopup:
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
	days := plan.RewardDays
	if days <= 0 {
		days = DefaultRewardDays
	}
	expires := time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC()
	for range rewardTries {
		d := &store.Discount{Code: newRewardCode(), Percent: plan.RewardPercent, ExpiresAt: &expires, UserID: &buyer.ID, SourceOrderID: &o.ID}
		ev := events.RewardCodeIssuedEvent{OrderID: o.ID, UserID: buyer.ID, TelegramID: buyer.TelegramID, PlanID: plan.ID, PlanNames: plan.NameI18n,
			ExpiresAt: expires.Unix(), Days: days, Text: plan.RewardTextI18n}
		if plan.RewardPercent != nil {
			ev.Percent = *plan.RewardPercent
		} else {
			cur := plan.Currency
			d.Amount, d.Currency = plan.RewardAmount, &cur
			ev.Amount, ev.Currency = *plan.RewardAmount, cur
		}
		made, err := s.st.InsertRewardCode(ctx, tx, d)
		if err != nil {
			return err
		}
		if made {
			ev.Code = d.Code
			return s.publish(ctx, tx, events.RewardCodeIssued, ev)
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
