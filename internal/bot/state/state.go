// Package state keeps the bot's per-user conversation state (what the user is
// expected to send next) and de-duplicates Telegram updates. Redis is the
// production store; Memory serves tests.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// TTL is how long a pending conversation step stays valid.
const TTL = 30 * time.Minute

// State is what a user is in the middle of. Scene "" means nothing.
type State struct {
	Scene string            `json:"scene"`
	Data  map[string]string `json:"data,omitempty"`
}

// Get returns a value from Data ("" when unset).
func (s State) Get(k string) string { return s.Data[k] }

// Store persists State per Telegram user.
type Store interface {
	Get(ctx context.Context, userID int64) (State, error)
	Set(ctx context.Context, userID int64, s State) error
	Clear(ctx context.Context, userID int64) error
	// Once reports true the first time key is seen within ttl.
	Once(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// Redis stores state as JSON under bot:state:<user>.
type Redis struct{ rdb *redis.Client }

// NewRedis wraps a connected client.
func NewRedis(rdb *redis.Client) *Redis { return &Redis{rdb: rdb} }

func key(userID int64) string { return "bot:state:" + strconv.FormatInt(userID, 10) }

// Get implements Store.
func (r *Redis) Get(ctx context.Context, userID int64) (State, error) {
	raw, err := r.rdb.Get(ctx, key(userID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, nil // unreadable state is just forgotten
	}
	return s, nil
}

// Set implements Store.
func (r *Redis) Set(ctx context.Context, userID int64, s State) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.rdb.Set(ctx, key(userID), raw, TTL).Err()
}

// Clear implements Store.
func (r *Redis) Clear(ctx context.Context, userID int64) error {
	return r.rdb.Del(ctx, key(userID)).Err()
}

// Once implements Store with SET NX.
func (r *Redis) Once(ctx context.Context, k string, ttl time.Duration) (bool, error) {
	return r.rdb.SetNX(ctx, "bot:once:"+k, 1, ttl).Result()
}

// Memory is an in-process Store for tests and single-instance development.
type Memory struct {
	mu    sync.Mutex
	state map[int64]State
	seen  map[string]time.Time
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{state: map[int64]State{}, seen: map[string]time.Time{}}
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, userID int64) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state[userID], nil
}

// Set implements Store.
func (m *Memory) Set(_ context.Context, userID int64, s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state[userID] = s
	return nil
}

// Clear implements Store.
func (m *Memory) Clear(_ context.Context, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state, userID)
	return nil
}

// Once implements Store.
func (m *Memory) Once(_ context.Context, k string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if exp, ok := m.seen[k]; ok && time.Now().Before(exp) {
		return false, nil
	}
	m.seen[k] = time.Now().Add(ttl)
	return true, nil
}
