// Package eventbus defines the async messaging contract between services.
// v1 backs it with a Postgres transactional outbox (LISTEN/NOTIFY); NATS
// JetStream can replace the implementation without touching handlers.
//
// Contract: at-least-once delivery. Handlers MUST be idempotent (consumers
// de-duplicate on Event.ID via an inbox table).
package eventbus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Event is an immutable fact that already happened. Type is versioned,
// e.g. "order.paid.v1". Additive payload changes only within one version.
type Event struct {
	ID            string          `json:"event_id"`
	Type          string          `json:"type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	Actor         string          `json:"actor,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// NewEvent builds an event with a fresh ID and timestamp.
func NewEvent(typ, correlationID, actor string, payload any) (Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{ID: newID(), Type: typ, OccurredAt: time.Now().UTC(), CorrelationID: correlationID, Actor: actor, Payload: b}, nil
}

// Handler processes one event. Returning an error triggers a retry with
// backoff; after the retry budget the event goes to the dead-letter store.
type Handler func(ctx context.Context, e Event) error

// Publisher emits events.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

// Subscriber registers handlers for an event type.
type Subscriber interface {
	Subscribe(eventType string, h Handler)
}

// Bus is both.
type Bus interface {
	Publisher
	Subscriber
}

// ErrNoHandler is returned by test buses when nothing listens (informational).
var ErrNoHandler = errors.New("eventbus: no handler registered")

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Memory is an in-process Bus for tests and single-binary development.
// It delivers synchronously and de-duplicates by Event.ID like the real inbox.
type Memory struct {
	mu   sync.Mutex
	subs map[string][]Handler
	seen map[string]struct{}
}

// NewMemory returns an empty in-memory bus.
func NewMemory() *Memory {
	return &Memory{subs: map[string][]Handler{}, seen: map[string]struct{}{}}
}

// Subscribe implements Subscriber.
func (m *Memory) Subscribe(eventType string, h Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[eventType] = append(m.subs[eventType], h)
}

// Publish implements Publisher. Duplicate event IDs are ignored.
func (m *Memory) Publish(ctx context.Context, e Event) error {
	m.mu.Lock()
	if _, dup := m.seen[e.ID]; dup {
		m.mu.Unlock()
		return nil
	}
	m.seen[e.ID] = struct{}{}
	hs := append([]Handler(nil), m.subs[e.Type]...)
	m.mu.Unlock()

	var errs []error
	for _, h := range hs {
		if err := h(ctx, e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
