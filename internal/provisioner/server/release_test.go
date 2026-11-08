package server

import (
	"context"
	"errors"
	"testing"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReleaseClient forgets a reservation that was never made, so that the next create picks its server again (core
// moves an undelivered order to another server): the client a create that timed out made on the old panel is deleted
// when that panel answers, and the answer says when it could not be (leftover). A client that was made is never
// released, and a subscription without a reservation is nothing to do.
func TestReleaseClient(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	release := func(sub string) (*provisionerv1.ReleaseClientResponse, error) {
		return f.client.ReleaseClient(ctx, &provisionerv1.ReleaseClientRequest{SubscriptionId: sub})
	}
	reserve := func(sub, serverID, email string) {
		t.Helper()
		subID := f.st.SubIDFor(sub)
		if _, _, err := f.st.ReserveClient(ctx, &store.ClientMap{SubscriptionID: sub, ServerID: serverID, Email: email, XUISubID: &subID}); err != nil {
			t.Fatal(err)
		}
	}
	gone := func(sub string) bool {
		_, err := f.st.GetClientMap(ctx, sub)
		return errors.Is(err, store.ErrNotFound)
	}

	if _, err := release(""); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no id: %v", err)
	}
	if r, err := release(sub1); err != nil || r.GetReleased() || r.GetLeftover() {
		t.Fatalf("nothing reserved: %+v %v", r, err)
	}

	// A create that timed out made the client on the panel: it is deleted there.
	const half = "00000000-0000-7000-8000-0000000000c1"
	reserve(half, f.server.ID, "u1-half")
	f.fake.Seed("u1-half", f.st.SubIDFor(half), []int{1})
	r, err := release(half)
	if err != nil || !r.GetReleased() || r.GetLeftover() || r.GetServerId() != f.server.ID || f.fake.Has("u1-half") || !gone(half) {
		t.Fatalf("released with the panel up: %+v %v, on the panel %v", r, err, f.fake.Has("u1-half"))
	}
	// The panel never had it: nothing is left either.
	const never = "00000000-0000-7000-8000-0000000000c2"
	reserve(never, f.server.ID, "u2-never")
	if r, err := release(never); err != nil || !r.GetReleased() || r.GetLeftover() || !gone(never) {
		t.Fatalf("released, never made: %+v %v", r, err)
	}

	// The panel does not answer: the reservation goes all the same, and a client may be left there.
	down, err := f.st.AddServer(ctx, &store.Server{Name: "down", BaseURL: "http://127.0.0.1:1", Token: panelTok, Enabled: true,
		SubBaseURL: "https://sub.example.com:2096/sub/"})
	if err != nil {
		t.Fatal(err)
	}
	const stuck = "00000000-0000-7000-8000-0000000000c3"
	reserve(stuck, down.ID, "u3-stuck")
	if r, err := release(stuck); err != nil || !r.GetReleased() || !r.GetLeftover() || r.GetServerId() != down.ID || !gone(stuck) {
		t.Fatalf("released with the panel down: %+v %v", r, err)
	}

	// A client that was made stays where it is.
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u4-made", DurationDays: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := release(sub1); status.Code(err) != codes.FailedPrecondition || gone(sub1) || !f.fake.Has("u4-made") {
		t.Fatalf("a made client was released: %v", err)
	}
}
