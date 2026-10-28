package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// The kinds of code a customer's own list holds (docs/03-bot-ux.md, "My codes").
const (
	// CodeKindReward: a reward code, a discount code a plan's purchase gave its buyer (discount_codes.user_id is the buyer).
	CodeKindReward = "reward"
	// CodeKindGift: a gift code the customer applied (a gift_code_redemptions row of theirs).
	CodeKindGift = "gift"
)

// CodeRow is one entry of a customer's list of codes. It is what is stored; the domain words the state.
//
// Only codes with a known owner are ever read. A reward code is its buyer's (discount_codes.user_id); a gift code has no owner, so
// only the customer's own redemption of it is attributable, and it is never stored in the clear (core.gift_codes keeps a keyed hash
// and the last four characters), so a gift entry has a Tail and no Code. A code staff made for everyone (discount_codes.user_id
// NULL) and a gift code nobody applied are in no one's list.
type CodeRow struct {
	Kind string
	// ID is unique in a list: a reward code's own text, or a redemption's id.
	ID string
	// Code is a reward code, whole; empty for a gift entry. Tail is the last characters of the gift code the customer applied;
	// empty for a reward entry.
	Code, Tail string
	// Percent or Amount (with Currency) is what a reward code takes off; a gift entry's Amount is what it added to the wallet.
	Percent  *int32
	Amount   *int64
	Currency *string
	// MaxUses and Used count a reward code's uses; Enabled is staff's switch.
	MaxUses *int32
	Used    int32
	Enabled bool
	// ExpiresAt is a reward code's last moment; a gift entry has none (the gift code's own expiry is not the customer's concern once
	// it is applied).
	ExpiresAt *time.Time
	// UsedAt: when a reward code was used on a paid order, or when the gift code was applied.
	UsedAt *time.Time
	// ReceivedAt: when the reward code was made for the customer, or they applied the gift code. The list is ordered by it.
	ReceivedAt time.Time
	// OrderID is the purchase that gave a reward code (nil when it is gone); PlanNames the names of that purchase's plan.
	OrderID   *string
	PlanNames map[string]string
	// OnlyPlanNames: a reward code made before 00037 works on one plan only (nil: any plan).
	OnlyPlanNames map[string]string
}

// ListUserCodes returns a page of one customer's own codes, newest first, and how many they have in all. Both halves select by the
// customer's id and nothing else, so another customer's code, a code with no owner and a gift code nobody applied cannot appear.
func (s *Store) ListUserCodes(ctx context.Context, q querier, userID string, p Page) ([]CodeRow, int64, error) {
	p = p.clamp()
	var total int64
	if err := q.QueryRow(ctx, `SELECT (SELECT count(*) FROM core.discount_codes WHERE user_id = $1)
		+ (SELECT count(*) FROM core.gift_code_redemptions WHERE user_id = $1)`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count user codes: %w", err)
	}
	rows, err := q.Query(ctx, `
		SELECT kind, id, code, tail, percent, amount, currency, max_uses, used_count, enabled, expires_at, used_at, received_at,
			order_id, plan_names, only_plan_names
		FROM (
			SELECT 'reward' AS kind, d.code AS id, d.code AS code, '' AS tail, d.percent AS percent, d.amount AS amount,
				d.currency AS currency, d.max_uses AS max_uses, d.used_count AS used_count, d.enabled AS enabled,
				d.expires_at AS expires_at, r.created_at AS used_at, d.created_at AS received_at,
				d.source_order_id::text AS order_id, p.name_i18n AS plan_names, op.name_i18n AS only_plan_names
			FROM core.discount_codes d
			LEFT JOIN core.discount_redemptions r ON r.code = d.code AND r.user_id = d.user_id
			LEFT JOIN core.orders o ON o.id = d.source_order_id
			LEFT JOIN core.plans p ON p.id = o.plan_id
			LEFT JOIN core.plans op ON op.id = d.plan_id
			WHERE d.user_id = $1
			UNION ALL
			SELECT 'gift', x.id::text, '', g.code_tail, NULL::int, x.amount, x.currency, NULL::int, 0, true, NULL::timestamptz,
				x.created_at, x.created_at, NULL::text, NULL::jsonb, NULL::jsonb
			FROM core.gift_code_redemptions x
			JOIN core.gift_codes g ON g.id = x.gift_code_id
			WHERE x.user_id = $1
		) c
		ORDER BY received_at DESC, id DESC
		LIMIT $2 OFFSET $3`, userID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list user codes: %w", err)
	}
	defer rows.Close()
	var out []CodeRow
	for rows.Next() {
		var (
			c               CodeRow
			plan, onlyPlans []byte
		)
		if err := rows.Scan(&c.Kind, &c.ID, &c.Code, &c.Tail, &c.Percent, &c.Amount, &c.Currency, &c.MaxUses, &c.Used, &c.Enabled,
			&c.ExpiresAt, &c.UsedAt, &c.ReceivedAt, &c.OrderID, &plan, &onlyPlans); err != nil {
			return nil, 0, fmt.Errorf("list user codes: %w", err)
		}
		for _, n := range []struct {
			raw  []byte
			into *map[string]string
		}{{plan, &c.PlanNames}, {onlyPlans, &c.OnlyPlanNames}} {
			if len(n.raw) == 0 {
				continue
			}
			if err := json.Unmarshal(n.raw, n.into); err != nil {
				return nil, 0, fmt.Errorf("list user codes: plan name: %w", err)
			}
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}
