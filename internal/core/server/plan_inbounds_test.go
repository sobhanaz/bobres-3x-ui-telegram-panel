package server

import (
	"context"
	"testing"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The plan's inbound choice crosses the gRPC boundary: it comes back with the
// plan, and a save that sends none (the bot's plan editor) keeps it.
func TestPlanInboundsOverGRPC(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7101})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.users SET role = 'owner' WHERE id = $1`, u.GetId()); err != nil {
		t.Fatal(err)
	}
	const a, b = "00000000-0000-7000-8000-0000000000a1", "00000000-0000-7000-8000-0000000000b2"
	plan := &corev1.Plan{NameI18N: map[string]string{"en": "I"}, Kind: "time", DurationDays: 30,
		Price: &commonv1.Money{Amount: 1000, Currency: "IRT"}, Enabled: true,
		Inbounds: []*corev1.InboundSelection{{ServerId: b, InboundIds: []int32{3, 1}}, {ServerId: a, InboundIds: []int32{2}}}}
	got, err := c.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: 7101, Plan: plan})
	if err != nil || len(got.GetInbounds()) != 2 || got.GetInbounds()[0].GetServerId() != a ||
		len(got.GetInbounds()[1].GetInboundIds()) != 2 || got.GetInbounds()[1].GetInboundIds()[0] != 1 {
		t.Fatalf("create: %v %+v", err, got)
	}
	// The bot's edit: no inbounds in the request, the choice stays.
	edit := &corev1.Plan{Id: got.GetId(), NameI18N: plan.GetNameI18N(), Kind: "time", DurationDays: 30,
		Price: &commonv1.Money{Amount: 900, Currency: "IRT"}, Enabled: true}
	again, err := c.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: 7101, Plan: edit})
	if err != nil || again.GetPrice().GetAmount() != 900 || len(again.GetInbounds()) != 2 {
		t.Fatalf("edit: %v %+v", err, again)
	}
	// A bad choice is an invalid argument.
	edit.Inbounds = []*corev1.InboundSelection{{ServerId: "default", InboundIds: []int32{1}}}
	if _, err := c.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: 7101, Plan: edit}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad choice: %v", err)
	}
}
