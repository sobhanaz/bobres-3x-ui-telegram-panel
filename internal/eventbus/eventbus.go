// Package eventbus moves domain events between services with at-least-once
// delivery:
//
//   - Outbox.Publish appends an event inside the caller's transaction, so a
//     state change and its event commit (or roll back) together.
//   - Feed serves one schema's outbox to named consumers and keeps one cursor
//     per consumer in the producer's schema; FeedServer exposes it over gRPC,
//     so no service ever reads another service's database.
//   - Consumer pulls from a Source (GRPCSource for a peer's feed), applies each
//     event with retries and backoff, dead-letters events that cannot be
//     applied, and acknowledges progress.
//
// Handlers must be idempotent and de-duplicate on Message.EventID (an inbox
// table): a crash between "applied" and "acknowledged" re-delivers the event.
// Another transport (e.g. NATS JetStream) can replace Feed/Source without
// touching handlers.
package eventbus

import (
	"errors"
	"time"
)

// Message is one event as seen by a consumer.
type Message struct {
	Seq       int64  // position in the producer's feed
	EventID   string // globally unique; the de-duplication key
	Topic     string // versioned fact name, e.g. "payment.succeeded.v1"
	Payload   []byte // JSON
	CreatedAt time.Time
}

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks a handler error that retrying cannot fix (malformed payload,
// reference to something that does not exist). The consumer dead-letters such
// an event immediately instead of retrying it.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err (or anything it wraps) was marked Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}
