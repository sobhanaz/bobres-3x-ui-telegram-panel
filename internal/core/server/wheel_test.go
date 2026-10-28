package server

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The wheel over gRPC (what the bot calls): off until the owner gives it prizes and a budget; one spin a day that is the same spin when asked again;
// a customer without a paid order is told so; a banned one is refused; an unknown one is not found.
func TestWheelOverGRPC(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	set := func(values map[string]string) {
		t.Helper()
		for k, v := range values {
			raw, _ := json.Marshal(v)
			if err := f.st.SetSetting(ctx, f.st.Conn(), k, raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		_, _ = f.st.DB().Exec(ctx, "DELETE FROM core.settings WHERE key LIKE 'wheel.%'")
		_, _ = f.st.DB().Exec(ctx, "DELETE FROM core.wheel_spins")
	})

	up := func(tg int64, name string) *corev1.User {
		u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: tg, Username: name, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	const plan = "00000000-0000-7000-8000-000000000e01"
	if _, err := f.st.DB().Exec(ctx, `INSERT INTO core.plans (id, name_i18n, kind, price, currency) VALUES ($1, '{}', 'time', 10000, 'IRT') ON CONFLICT DO NOTHING`, plan); err != nil {
		t.Fatal(err)
	}
	order := func(u *corev1.User) {
		t.Helper()
		_, err := f.st.DB().Exec(ctx, `INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES (gen_random_uuid(), $1, $2, 'new', 'active', 10000, 'IRT', $3)`, u.GetId(), plan, "wheel-grpc-"+u.GetId())
		if err != nil {
			t.Fatal(err)
		}
	}
	ana, newcomer, banned := up(7700000001, "ana"), up(7700000002, "newcomer"), up(7700000003, "banned_one")
	order(ana)
	order(banned)

	// Off by default.
	if st, err := c.GetWheel(ctx, &corev1.GetWheelRequest{UserId: ana.GetId()}); err != nil || st.GetOn() {
		t.Fatalf("the wheel while off: %+v %v", st, err)
	}
	if r, err := c.SpinWheel(ctx, &corev1.SpinWheelRequest{UserId: ana.GetId()}); err != nil || r.GetStatus() != "off" {
		t.Fatalf("a spin while off: %+v %v", r, err)
	}

	set(map[string]string{"wheel.enabled": "true", "wheel.prizes": `[{"amount":2500,"weight":1}]`, "wheel.no_prize_weight": "0", "wheel.daily_budget": "10000"})
	st, err := c.GetWheel(ctx, &corev1.GetWheelRequest{UserId: ana.GetId()})
	if err != nil || !st.GetOn() || st.GetSpun() || !st.GetOpen() || st.GetReason() != "" || st.GetOrders() != 1 || st.GetMinOrders() != 1 || st.GetTotalWeight() != 1 ||
		len(st.GetPrizes()) != 1 || st.GetPrizes()[0].GetAmount() != 2500 || st.GetPrizes()[0].GetWeight() != 1 || st.GetDay() == "" || st.GetResetsAt() == 0 {
		t.Fatalf("the wheel: %+v %v", st, err)
	}
	r, err := c.SpinWheel(ctx, &corev1.SpinWheelRequest{UserId: ana.GetId()})
	if err != nil || r.GetStatus() != "won" || r.GetPrize() != 2500 || r.GetIndex() != 0 || r.GetBalance() != 2500 || r.GetDay() != st.GetDay() || r.GetResetsAt() != st.GetResetsAt() {
		t.Fatalf("the spin: %+v %v", r, err)
	}
	again, err := c.SpinWheel(ctx, &corev1.SpinWheelRequest{UserId: ana.GetId()})
	if err != nil || again.GetStatus() != "already" || again.GetPrize() != 2500 || again.GetBalance() != 2500 {
		t.Fatalf("asked again: %+v %v", again, err)
	}
	if st, err = c.GetWheel(ctx, &corev1.GetWheelRequest{UserId: ana.GetId()}); err != nil || !st.GetSpun() || st.GetPrize() != 2500 {
		t.Fatalf("the wheel after the spin: %+v %v", st, err)
	}

	// No paid order yet: told how many it takes, nothing written.
	if st, err = c.GetWheel(ctx, &corev1.GetWheelRequest{UserId: newcomer.GetId()}); err != nil || st.GetReason() != "orders" || st.GetOrders() != 0 {
		t.Fatalf("a newcomer's wheel: %+v %v", st, err)
	}
	if r, err = c.SpinWheel(ctx, &corev1.SpinWheelRequest{UserId: newcomer.GetId()}); err != nil || r.GetStatus() != "orders" || r.GetOrders() != 0 || r.GetMinOrders() != 1 || r.GetPrize() != 0 {
		t.Fatalf("a newcomer's spin: %+v %v", r, err)
	}

	// A banned customer is refused, an unknown one is not found.
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.users SET status = 'banned' WHERE id = $1`, banned.GetId()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SpinWheel(ctx, &corev1.SpinWheelRequest{UserId: banned.GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a banned spin: %v", err)
	}
	if _, err := c.GetWheel(ctx, &corev1.GetWheelRequest{UserId: banned.GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a banned look: %v", err)
	}
	if _, err := c.SpinWheel(ctx, &corev1.SpinWheelRequest{UserId: "00000000-0000-0000-0000-000000000000"}); status.Code(err) != codes.NotFound {
		t.Fatalf("an unknown customer: %v", err)
	}
}
