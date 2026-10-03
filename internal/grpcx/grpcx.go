// Package grpcx builds the gRPC servers and client connections every service
// uses, so authentication and panic recovery cannot be left out by accident.
package grpcx

import (
	"context"
	"log/slog"
	"runtime/debug"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const maxMsgBytes = 4 << 20

// NewServer returns a server that recovers handler panics (logged, returned as
// Internal) and accepts only the given peers.
func NewServer(log *slog.Logger, peers ...grpcauth.Peer) (*grpc.Server, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	unaryAuth, err := grpcauth.UnaryInterceptor(peers...)
	if err != nil {
		return nil, err
	}
	streamAuth, err := grpcauth.StreamInterceptor(peers...)
	if err != nil {
		return nil, err
	}
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(recoverUnary(log), unaryAuth),
		grpc.ChainStreamInterceptor(recoverStream(log), streamAuth),
		grpc.MaxRecvMsgSize(maxMsgBytes),
	), nil
}

func recoverUnary(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("grpc handler panic", "method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

func recoverStream(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("grpc stream panic", "method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(srv, ss)
	}
}

// Dial connects to a peer, authenticating with this service's own token. The
// connection is lazy: it is established on first use and re-established after
// failures, so a peer that starts later is fine.
func Dial(addr, token string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(grpcauth.ClientCredentials{Token: token}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgBytes)))
}
