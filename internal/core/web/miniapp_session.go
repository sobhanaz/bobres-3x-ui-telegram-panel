package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

// Mini App sessions: a bearer token handed out after Telegram's init data is
// verified. Only the token's hash is kept, in memory (a restart of core ends
// every session; the app signs in again from its init data, which stays
// valid in the Telegram client for an hour).
const (
	miniSessionTTL  = 6 * time.Hour
	miniSessionIdle = 1 * time.Hour
	// miniMaxPerUser is how many sessions one customer may hold (a phone and
	// a desktop client, with room for a few reopened Mini Apps).
	miniMaxPerUser = 5
	// miniMaxSessions bounds the table: init data of an unknown customer
	// makes an account, so signing in is rate limited per address, and this
	// is the backstop.
	miniMaxSessions = 100_000
)

type miniSession struct {
	userID     string
	telegramID int64
	created    time.Time
	lastUsed   time.Time
}

type miniSessions struct {
	mu   sync.Mutex
	byID map[[32]byte]*miniSession
}

func newMiniSessions() *miniSessions { return &miniSessions{byID: map[[32]byte]*miniSession{}} }

func miniHash(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// open starts a session for a customer and returns its token.
func (m *miniSessions) open(userID string, telegramID int64, now time.Time) (string, bool) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", false
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.byID) >= miniMaxSessions {
		m.sweepLocked(now)
		if len(m.byID) >= miniMaxSessions {
			return "", false
		}
	}
	// The oldest session of this customer goes when they hold too many.
	var (
		count  int
		oldest [32]byte
		oldAt  time.Time
	)
	for k, s := range m.byID {
		if s.userID != userID {
			continue
		}
		count++
		if oldAt.IsZero() || s.created.Before(oldAt) {
			oldest, oldAt = k, s.created
		}
	}
	if count >= miniMaxPerUser {
		delete(m.byID, oldest)
	}
	m.byID[miniHash(token)] = &miniSession{userID: userID, telegramID: telegramID, created: now, lastUsed: now}
	return token, true
}

// get returns a live session and counts the use.
func (m *miniSessions) get(token string, now time.Time) *miniSession {
	if token == "" || len(token) > 64 {
		return nil
	}
	k := miniHash(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.byID[k]
	if s == nil {
		return nil
	}
	if now.Sub(s.created) > miniSessionTTL || now.Sub(s.lastUsed) > miniSessionIdle {
		delete(m.byID, k)
		return nil
	}
	s.lastUsed = now
	cp := *s
	return &cp
}

func (m *miniSessions) close(token string) {
	m.mu.Lock()
	delete(m.byID, miniHash(token))
	m.mu.Unlock()
}

// dropUser ends every session of a customer (banned meanwhile).
func (m *miniSessions) dropUser(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.byID {
		if s.userID == userID {
			delete(m.byID, k)
		}
	}
}

func (m *miniSessions) sweepLocked(now time.Time) {
	for k, s := range m.byID {
		if now.Sub(s.created) > miniSessionTTL || now.Sub(s.lastUsed) > miniSessionIdle {
			delete(m.byID, k)
		}
	}
}
