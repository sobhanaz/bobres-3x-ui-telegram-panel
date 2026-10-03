package grpcx

import (
	"context"
	"log/slog"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPanicBecomesInternalError(t *testing.T) {
	it := recoverUnary(slog.New(slog.DiscardHandler))
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic escaped the interceptor: %v", r)
		}
	}()
	_, err := it(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/x.v1.X/Boom"},
		func(context.Context, any) (any, error) { panic("boom") })
	if status.Code(err) != codes.Internal {
		t.Fatalf("want Internal, got %v", err)
	}
}

func TestNewServerRequiresPeers(t *testing.T) {
	if _, err := NewServer(nil); err == nil {
		t.Fatal("server without peers accepted")
	}
}
