package eventbus

import (
	"context"
	"errors"
	"testing"
)

func TestDeliversToSubscribers(t *testing.T) {
	bus := NewMemory()
	got := 0
	bus.Subscribe("order.paid.v1", func(_ context.Context, e Event) error { got++; return nil })
	e, err := NewEvent("order.paid.v1", "c1", "system", map[string]string{"order_id": "o1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("handler ran %d times", got)
	}
}

func TestDuplicateEventIDIsIgnored(t *testing.T) {
	bus := NewMemory()
	n := 0
	bus.Subscribe("x.v1", func(context.Context, Event) error { n++; return nil })
	e, _ := NewEvent("x.v1", "", "", struct{}{})
	_ = bus.Publish(context.Background(), e)
	_ = bus.Publish(context.Background(), e)
	if n != 1 {
		t.Fatalf("duplicate delivered, n=%d", n)
	}
}

func TestHandlerErrorsAreReturned(t *testing.T) {
	bus := NewMemory()
	boom := errors.New("boom")
	bus.Subscribe("y.v1", func(context.Context, Event) error { return boom })
	e, _ := NewEvent("y.v1", "", "", struct{}{})
	if err := bus.Publish(context.Background(), e); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
}

func TestNewEventIDsAreUnique(t *testing.T) {
	a, _ := NewEvent("t.v1", "", "", 1)
	b, _ := NewEvent("t.v1", "", "", 1)
	if a.ID == b.ID || len(a.ID) != 32 {
		t.Fatalf("bad ids %q %q", a.ID, b.ID)
	}
}
