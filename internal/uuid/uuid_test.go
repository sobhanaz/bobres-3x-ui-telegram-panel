package uuid

import "testing"

func TestNewV7(t *testing.T) {
	a, err := NewV7()
	if err != nil {
		t.Fatalf("NewV7: %v", err)
	}
	b, err := NewV7()
	if err != nil {
		t.Fatalf("NewV7: %v", err)
	}
	if a == b {
		t.Fatal("two UUIDv7s are equal")
	}
	if a.Version() != 7 {
		t.Fatalf("version = %d, want 7", a.Version())
	}
}

func TestMustV7(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MustV7 panicked: %v", r)
		}
	}()
	MustV7()
}
