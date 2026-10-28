package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// logBuf is a log sink the test can read while the server writes to it.
type logBuf struct {
	mu sync.Mutex
	sb strings.Builder
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sb.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sb.String()
}

// deleteOnPanel removes a client from the fake panel the way an operator does, behind core's back: the
// provisioner's client map still holds it.
func (f *fixture) deleteOnPanel(t *testing.T, email string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.fake.URL+"/panel/api/clients/del/"+email, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+panelTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if f.fake.Has(email) {
		t.Fatalf("%s is still on the panel", email)
	}
}

// A client the panel no longer has is an answer, not a failure of the panel: GetUsage says so in the
// reply and writes no warning (core asks about every service every ten minutes, and a warning each time
// filled the events list).
func TestGetUsageSaysWhenThePanelHasNoSuchClient(t *testing.T) {
	f := newFixture(t)
	var logs logBuf
	f.srv.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	ctx := context.Background()
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u11-gone", DurationDays: 5, TrafficBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	u, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil || u.GetClientMissing() || u.GetTrafficTotalBytes() != 1<<30 {
		t.Fatalf("a client on the panel: %+v %v", u, err)
	}

	f.deleteOnPanel(t, "u11-gone")
	u, err = f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil {
		t.Fatalf("a client the panel does not have must be an answer, not an error: %v", err)
	}
	if !u.GetClientMissing() || u.GetSubscriptionId() != sub1 || u.GetTrafficTotalBytes() != 0 || u.GetExpiresAt() != 0 {
		t.Fatalf("usage of a client the panel does not have: %+v", u)
	}
	if strings.Contains(logs.String(), "panel call failed") {
		t.Fatalf("a missing client was logged as a failed panel call:\n%s", logs.String())
	}
}

func TestRecreateClient(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	made, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u12-back", DurationDays: 5, TrafficBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	f.deleteOnPanel(t, "u12-back")

	expires := time.Now().Add(12 * 24 * time.Hour).Unix()
	req := &provisionerv1.RecreateClientRequest{SubscriptionId: sub1, ExpiresAt: expires, TrafficTotalBytes: 3 << 30}
	got, err := f.client.RecreateClient(ctx, req)
	if err != nil {
		t.Fatalf("RecreateClient: %v", err)
	}
	if !f.fake.Has("u12-back") {
		t.Fatal("the client was not made again on the panel")
	}
	// Same address for the customer: the same email, subscription id and inbounds.
	if got.GetEmail() != "u12-back" || got.GetXuiSubId() != made.GetXuiSubId() || len(got.GetInboundIds()) != len(made.GetInboundIds()) {
		t.Fatalf("the client is not the one it was: made %+v, again %+v", made, got)
	}
	snap, _ := f.fake.Snapshot("u12-back")
	if snap.ExpiryTime != expires*1000 || snap.TotalGB != 3<<30 || !snap.Enable || snap.Up+snap.Down != 0 {
		t.Fatalf("the new client's limits: %+v", snap)
	}
	u, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil || u.GetClientMissing() || u.GetTrafficTotalBytes() != 3<<30 || u.GetExpiresAt() != expires {
		t.Fatalf("usage after: %+v %v", u, err)
	}

	// A second call (a double click, a retry) finds the client there and changes nothing.
	f.fake.AddUsage("u12-back", 100, 200)
	if _, err := f.client.RecreateClient(ctx, req); err != nil {
		t.Fatalf("second RecreateClient: %v", err)
	}
	if snap, _ = f.fake.Snapshot("u12-back"); snap.Up+snap.Down != 300 {
		t.Fatalf("a client on the panel was made again: %+v", snap)
	}
	if lost := f.fake.LostFields(); len(lost) != 0 {
		t.Fatalf("the identity of the client was lost: %v", lost)
	}
}

func TestRecreateClientRefusals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.client.RecreateClient(ctx, &provisionerv1.RecreateClientRequest{SubscriptionId: "00000000-0000-4000-8000-000000000999"}); status.Code(err) != codes.NotFound {
		t.Fatalf("a service the provisioner does not hold: %v", err)
	}
	if _, err := f.client.RecreateClient(ctx, &provisionerv1.RecreateClientRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no subscription id: %v", err)
	}
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u13-taken", DurationDays: 5, TrafficBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.RecreateClient(ctx, &provisionerv1.RecreateClientRequest{SubscriptionId: sub1, ExpiresAt: -1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("a negative expiry: %v", err)
	}
	// The email is somebody else's on the panel now: nothing of ours can be made under it.
	f.deleteOnPanel(t, "u13-taken")
	f.fake.Seed("u13-taken", "someone-elses-sub-id-0000000000", []int{1})
	if _, err := f.client.RecreateClient(ctx, &provisionerv1.RecreateClientRequest{SubscriptionId: sub1}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("an email another client holds: %v", err)
	}
}
