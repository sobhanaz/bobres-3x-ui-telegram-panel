package eventbus

import (
	"context"
	"time"

	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FeedServer exposes a Feed over gRPC. The consumer is the authenticated
// caller (grpcauth), so a peer can only read and move its own cursor.
type FeedServer struct {
	eventsv1.UnimplementedEventFeedServiceServer
	feed *Feed
}

// NewFeedServer wraps feed.
func NewFeedServer(feed *Feed) *FeedServer { return &FeedServer{feed: feed} }

func consumerFrom(ctx context.Context) (string, error) {
	c := grpcauth.CallerFrom(ctx)
	if c == "" {
		return "", status.Error(codes.Unauthenticated, "unauthenticated consumer")
	}
	return c, nil
}

// PullEvents implements eventsv1.EventFeedServiceServer.
func (s *FeedServer) PullEvents(ctx context.Context, req *eventsv1.PullEventsRequest) (*eventsv1.PullEventsResponse, error) {
	consumer, err := consumerFrom(ctx)
	if err != nil {
		return nil, err
	}
	msgs, err := s.feed.Pending(ctx, consumer, int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(codes.Unavailable, "events unavailable")
	}
	out := &eventsv1.PullEventsResponse{Events: make([]*eventsv1.Event, 0, len(msgs))}
	for _, m := range msgs {
		out.Events = append(out.Events, &eventsv1.Event{
			Seq: m.Seq, EventId: m.EventID, Topic: m.Topic, Payload: m.Payload, CreatedAt: m.CreatedAt.Unix(),
		})
	}
	return out, nil
}

// AckEvents implements eventsv1.EventFeedServiceServer.
func (s *FeedServer) AckEvents(ctx context.Context, req *eventsv1.AckEventsRequest) (*eventsv1.AckEventsResponse, error) {
	consumer, err := consumerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.feed.Ack(ctx, consumer, req.GetUpToSeq()); err != nil {
		return nil, status.Error(codes.Unavailable, "ack failed")
	}
	return &eventsv1.AckEventsResponse{}, nil
}

// GRPCSource pulls a peer service's feed.
type GRPCSource struct {
	c eventsv1.EventFeedServiceClient
}

// NewGRPCSource wraps a connected client.
func NewGRPCSource(c eventsv1.EventFeedServiceClient) *GRPCSource { return &GRPCSource{c: c} }

// Fetch implements Source.
func (s *GRPCSource) Fetch(ctx context.Context, limit int) ([]Message, error) {
	resp, err := s.c.PullEvents(ctx, &eventsv1.PullEventsRequest{Limit: int32(min(limit, MaxBatch))}) //nolint:gosec // bounded by MaxBatch
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(resp.GetEvents()))
	for _, e := range resp.GetEvents() {
		out = append(out, Message{
			Seq: e.GetSeq(), EventID: e.GetEventId(), Topic: e.GetTopic(),
			Payload: e.GetPayload(), CreatedAt: time.Unix(e.GetCreatedAt(), 0),
		})
	}
	return out, nil
}

// Ack implements Source.
func (s *GRPCSource) Ack(ctx context.Context, upTo int64) error {
	_, err := s.c.AckEvents(ctx, &eventsv1.AckEventsRequest{UpToSeq: upTo})
	return err
}
