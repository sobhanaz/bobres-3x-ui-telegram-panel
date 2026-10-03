package eventbus

import (
	"context"
	"net"
	"testing"

	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
)

func TestGRPCFeedIsolatesConsumersByCaller(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	id := publish(t, db, "payment.succeeded.v1", map[string]string{"intent_id": "i1"})

	gs, err := grpcx.NewServer(nil, grpcauth.Peer{Name: "core", Token: "core-token"}, grpcauth.Peer{Name: "audit", Token: "audit-token"})
	if err != nil {
		t.Fatal(err)
	}
	eventsv1.RegisterEventFeedServiceServer(gs, NewFeedServer(NewFeed(db, "outbox_core")))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	source := func(token string) *GRPCSource {
		conn, err := grpcx.Dial(lis.Addr().String(), token)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return NewGRPCSource(eventsv1.NewEventFeedServiceClient(conn))
	}
	core, audit, stranger := source("core-token"), source("audit-token"), source("nope")

	got, err := core.Fetch(ctx, 10)
	if err != nil || len(got) != 1 || got[0].EventID != id || string(got[0].Payload) == "" {
		t.Fatalf("core fetch: %v %+v", err, got)
	}
	if err := core.Ack(ctx, got[0].Seq); err != nil {
		t.Fatal(err)
	}
	if rest, _ := core.Fetch(ctx, 10); len(rest) != 0 {
		t.Fatalf("core still sees acked event: %+v", rest)
	}
	if theirs, _ := audit.Fetch(ctx, 10); len(theirs) != 1 {
		t.Fatalf("core's ack moved audit's cursor: %+v", theirs)
	}
	if _, err := stranger.Fetch(ctx, 10); err == nil {
		t.Fatal("unauthenticated consumer was served")
	}
}
