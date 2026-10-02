package grpcauth

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func handler(ctx context.Context, req any) (any, error) { return "ok", nil }

func call(token, header string) error {
	it := UnaryInterceptor(token)
	md := metadata.MD{}
	if header != "" {
		md.Set("authorization", header)
	}
	ctx := metadata.NewIncomingContext(context.Background(), md)
	_, err := it(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/core.v1.CoreService/GetUser"}, handler)
	return err
}

func TestValidToken(t *testing.T) {
	if err := call("s3cret", "Bearer s3cret"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}

func TestMissingHeader(t *testing.T) {
	err := call("s3cret", "")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}

func TestWrongToken(t *testing.T) {
	err := call("s3cret", "Bearer wrong")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}

func TestWrongScheme(t *testing.T) {
	err := call("s3cret", "Basic s3cret")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}
