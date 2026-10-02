// Package grpcauth authenticates service-to-service gRPC calls with a shared
// Bearer token. Constant-time comparison thwarts timing oracles. This is the
// Phase 1 internal-auth mechanism; the interface is designed so mTLS can
// replace it without touching handlers.
package grpcauth

import (
	"context"
	"crypto/subtle"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryInterceptor returns a server interceptor enforcing
// `authorization: Bearer <token>` with constant-time comparison.
func UnaryInterceptor(token string) grpc.UnaryServerInterceptor {
	want := []byte(token)
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		var got string
		for _, h := range md.Get("authorization") {
			if len(h) > 7 && h[:7] == "Bearer " {
				got = h[7:]
				break
			}
		}
		if got == "" || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid service token")
		}
		return handler(ctx, req)
	}
}

// ClientCredentials injects the Bearer token on outgoing calls.
type ClientCredentials struct {
	Token string
}

// GetRequestMetadata implements credentials.PerRPCCredentials.
func (c ClientCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + c.Token}, nil
}

// RequireTransportSecurity returns false: Phase 1 runs plain TCP on the
// private compose network (no mTLS yet).
func (c ClientCredentials) RequireTransportSecurity() bool { return false }
