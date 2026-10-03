// Package grpcauth authenticates service-to-service gRPC calls. Every service
// has its own Bearer token, and a server accepts only the peers that are meant
// to call it (payments and provisioner accept core; core accepts the bot), so a
// compromised bot cannot reach payments or the 3x-ui token holder directly.
// The authenticated peer name is put in the request context (CallerFrom).
//
// This is the Phase 1 internal-auth mechanism; the interceptor is the only
// place that knows about tokens, so mTLS can replace it without touching
// handlers.
package grpcauth

import (
	"context"
	"crypto/subtle"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Peer is a caller this server accepts.
type Peer struct {
	Name  string // e.g. "core", "bot"
	Token string
}

type callerKey struct{}

// CallerFrom returns the authenticated peer name, or "" outside an
// authenticated call.
func CallerFrom(ctx context.Context) string {
	name, _ := ctx.Value(callerKey{}).(string)
	return name
}

// WithCaller returns ctx carrying caller as the authenticated peer. Tests and
// in-process callers use it; network calls get it from the interceptor.
func WithCaller(ctx context.Context, caller string) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// ErrNoPeers is returned when a server is configured without any accepted peer.
var ErrNoPeers = errors.New("grpcauth: no peers configured")

type authenticator struct{ peers []Peer }

func newAuthenticator(peers []Peer) (*authenticator, error) {
	var ok []Peer
	for _, p := range peers {
		if p.Name == "" || p.Token == "" {
			return nil, errors.New("grpcauth: peer name and token are required")
		}
		ok = append(ok, p)
	}
	if len(ok) == 0 {
		return nil, ErrNoPeers
	}
	return &authenticator{peers: ok}, nil
}

// authenticate returns ctx with the caller set, or an Unauthenticated error.
// Every peer token is compared (constant time) so the match position does not
// leak through timing.
func (a *authenticator) authenticate(ctx context.Context) (context.Context, error) {
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
	if got == "" {
		return nil, status.Error(codes.Unauthenticated, "missing service token")
	}
	caller := ""
	for _, p := range a.peers {
		if subtle.ConstantTimeCompare([]byte(got), []byte(p.Token)) == 1 {
			caller = p.Name
		}
	}
	if caller == "" {
		return nil, status.Error(codes.Unauthenticated, "invalid service token")
	}
	return WithCaller(ctx, caller), nil
}

// UnaryInterceptor enforces `authorization: Bearer <token>` against peers.
func UnaryInterceptor(peers ...Peer) (grpc.UnaryServerInterceptor, error) {
	a, err := newAuthenticator(peers)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := a.authenticate(ctx)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}, nil
}

// StreamInterceptor is the streaming counterpart, so a future streaming RPC
// can never be registered unauthenticated by accident.
func StreamInterceptor(peers ...Peer) (grpc.StreamServerInterceptor, error) {
	a, err := newAuthenticator(peers)
	if err != nil {
		return nil, err
	}
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := a.authenticate(ss.Context())
		if err != nil {
			return err
		}
		return handler(srv, &authedStream{ServerStream: ss, ctx: ctx})
	}, nil
}

type authedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authedStream) Context() context.Context { return s.ctx }

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
