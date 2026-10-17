package web

import (
	"testing"
	"time"
)

func TestMiniSessions(t *testing.T) {
	s, now := newMiniSessions(), time.Unix(1_700_000_000, 0)
	tok, ok := s.open("u1", 11, now)
	if !ok || len(tok) < 40 {
		t.Fatalf("open: %q %v", tok, ok)
	}
	// Only a hash is kept.
	for k := range s.byID {
		if string(k[:]) == tok {
			t.Fatal("the token itself is stored")
		}
	}
	if got := s.get(tok, now.Add(time.Minute)); got == nil || got.userID != "u1" || got.telegramID != 11 {
		t.Fatalf("get: %+v", got)
	}
	if s.get("", now) != nil || s.get("nope", now) != nil || s.get(tok+"x", now) != nil {
		t.Fatal("a wrong token opened a session")
	}
	// Idle for more than an hour: gone. Use renews the idle clock.
	if s.get(tok, now.Add(miniSessionIdle+time.Minute+time.Minute)) != nil {
		t.Fatal("an idle session lived on")
	}
	tok, _ = s.open("u1", 11, now)
	for i := 1; i <= 5; i++ { // active every 50 minutes
		if s.get(tok, now.Add(time.Duration(i)*50*time.Minute)) == nil {
			t.Fatalf("an active session ended at %d", i)
		}
	}
	// But never past the total lifetime.
	if s.get(tok, now.Add(miniSessionTTL+time.Minute)) != nil {
		t.Fatal("a session outlived its lifetime")
	}
	// Logout and drop-user.
	a, _ := s.open("u2", 12, now)
	b, _ := s.open("u2", 12, now)
	s.close(a)
	if s.get(a, now) != nil || s.get(b, now) == nil {
		t.Fatal("close ended the wrong session")
	}
	s.dropUser("u2")
	if s.get(b, now) != nil {
		t.Fatal("dropUser left a session")
	}
}

func TestMiniSessionsPerUserCap(t *testing.T) {
	s, now := newMiniSessions(), time.Unix(1_700_000_000, 0)
	var first string
	for i := 0; i < miniMaxPerUser+2; i++ {
		tok, _ := s.open("u", 1, now.Add(time.Duration(i)*time.Second))
		if i == 0 {
			first = tok
		}
	}
	if len(s.byID) != miniMaxPerUser {
		t.Fatalf("%d sessions held", len(s.byID))
	}
	if s.get(first, now.Add(time.Minute)) != nil {
		t.Fatal("the oldest session was kept")
	}
}
