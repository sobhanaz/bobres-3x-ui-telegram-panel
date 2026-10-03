package grpcauth

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var peers = []Peer{{Name: "core", Token: "core-token"}, {Name: "bot", Token: "bot-token"}}

// call runs the interceptor and returns the caller the handler saw.
func call(t *testing.T, header string) (string, error) {
	t.Helper()
	it, err := UnaryInterceptor(peers...)
	if err != nil {
		t.Fatal(err)
	}
	md := metadata.MD{}
	if header != "" {
		md.Set("authorization", header)
	}
	ctx := metadata.NewIncomingContext(context.Background(), md)
	var seen string
	_, err = it(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/core.v1.CoreService/GetUser"},
		func(ctx context.Context, _ any) (any, error) { seen = CallerFrom(ctx); return "ok", nil })
	return seen, err
}

func TestEachPeerIsIdentified(t *testing.T) {
	for _, p := range peers {
		got, err := call(t, "Bearer "+p.Token)
		if err != nil || got != p.Name {
			t.Fatalf("token of %s: caller=%q err=%v", p.Name, got, err)
		}
	}
}

func TestRejected(t *testing.T) {
	for name, h := range map[string]string{
		"missing":      "",
		"wrong token":  "Bearer nope",
		"wrong scheme": "Basic core-token",
		"empty bearer": "Bearer ",
	} {
		if _, err := call(t, h); status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: want Unauthenticated, got %v", name, err)
		}
	}
}

func TestMisconfiguredPeersRefused(t *testing.T) {
	if _, err := UnaryInterceptor(); err == nil {
		t.Fatal("server without peers accepted")
	}
	if _, err := UnaryInterceptor(Peer{Name: "core"}); err == nil {
		t.Fatal("peer with empty token accepted")
	}
}

func TestStreamCarriesCaller(t *testing.T) {
	it, err := StreamInterceptor(peers...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer bot-token"))
	var seen string
	err = it(nil, fakeStream{ctx: ctx}, &grpc.StreamServerInfo{}, func(_ any, ss grpc.ServerStream) error {
		seen = CallerFrom(ss.Context())
		return nil
	})
	if err != nil || seen != "bot" {
		t.Fatalf("stream caller=%q err=%v", seen, err)
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f fakeStream) Context() context.Context { return f.ctx }
