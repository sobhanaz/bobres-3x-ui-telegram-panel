package server

import (
	"context"
	"testing"
	"time"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
)

// A client whose days start at its first connection: the panel gets a negative expiry (3x-ui's delayed start),
// core gets it back as negative seconds on create, retry and usage, renewals keep it waiting, and the first
// traffic starts it (the panel sets the end, which usage then reports).
func TestClientStartsOnFirstUse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	req := &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u20-wait", DurationDays: 30, TrafficBytes: 1 << 30, StartOnFirstUse: true}
	cl, err := f.client.CreateClient(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if cl.GetExpiresAt() != -30*86400 {
		t.Fatalf("created: expires_at %d, want %d", cl.GetExpiresAt(), -30*86400)
	}
	if snap, _ := f.fake.Snapshot("u20-wait"); snap.ExpiryTime != -30*dayMs || snap.TotalGB != 1<<30 {
		t.Fatalf("panel: %+v", snap)
	}
	again, err := f.client.CreateClient(ctx, req)
	if err != nil || again.GetExpiresAt() != -30*86400 {
		t.Fatalf("retry: %v %v", again, err)
	}
	u, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil || u.GetExpiresAt() != -30*86400 {
		t.Fatalf("usage while waiting: %v %v", u, err)
	}

	// A renewal before the first connection: still waiting, for longer.
	if _, err := f.client.SetClientLimits(ctx, &provisionerv1.SetClientLimitsRequest{SubscriptionId: sub1, ExpiresAt: -40 * 86400, TrafficTotalBytes: 2 << 30}); err != nil {
		t.Fatal(err)
	}
	if snap, _ := f.fake.Snapshot("u20-wait"); snap.ExpiryTime != -40*dayMs || snap.TotalGB != 2<<30 || !snap.Enable {
		t.Fatalf("panel after the renewal: %+v", snap)
	}

	// The first connection starts the 40 days.
	f.fake.AddUsage("u20-wait", 10, 20)
	u, err = f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(40 * 24 * time.Hour).Unix()
	if d := u.GetExpiresAt() - want; d < -60 || d > 60 {
		t.Fatalf("usage after the first connection: expires_at %d, want about %d", u.GetExpiresAt(), want)
	}

	// Without days there is nothing to wait for: no expiry.
	none, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: "00000000-0000-7000-8000-0000000000a2", Email: "u21-none", TrafficBytes: 1 << 30, StartOnFirstUse: true})
	if err != nil || none.GetExpiresAt() != 0 {
		t.Fatalf("no days: %v %v", none, err)
	}
}
