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
	// Remember sets a flag (key is the full key, e.g. bot:join:<user>) for
	// ttl; Recall reports whether it is still set.
	Remember(ctx context.Context, key string, ttl time.Duration) error
	Recall(ctx context.Context, key string) (bool, error)
	// SaveCheckout keeps what a payment menu sells, under the menu's nonce,
	// for CheckoutTTL; LoadCheckout reads it back (ok=false once gone).
	SaveCheckout(ctx context.Context, userID int64, nonce string, c Checkout) error
	LoadCheckout(ctx context.Context, userID int64, nonce string) (c Checkout, ok bool, err error)
}

// CheckoutTTL is how long a payment menu's buttons keep working.
const CheckoutTTL = time.Hour

// Checkout is what a payment menu sells: a plan, as a new service or for an
// existing subscription (renew, traffic_topup), possibly with a discount code.
// Buttons carry only the menu's nonce, so they fit Telegram's 64 bytes.
type Checkout struct {
	Type           string `json:"type"` // new | renew | traffic_topup
	PlanID         string `json:"plan,omitempty"`
	SubscriptionID string `json:"sub,omitempty"`
	DiscountCode   string `json:"code,omitempty"`
}

func checkoutKey(userID int64, nonce string) string {
	return "bot:co:" + strconv.FormatInt(userID, 10) + ":" + nonce
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

// Remember implements Store.
func (r *Redis) Remember(ctx context.Context, k string, ttl time.Duration) error {
	return r.rdb.Set(ctx, k, 1, ttl).Err()
}

// Recall implements Store.
func (r *Redis) Recall(ctx context.Context, k string) (bool, error) {
	n, err := r.rdb.Exists(ctx, k).Result()
	return n > 0, err
}

// SaveCheckout implements Store.
func (r *Redis) SaveCheckout(ctx context.Context, userID int64, nonce string, c Checkout) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return r.rdb.Set(ctx, checkoutKey(userID, nonce), raw, CheckoutTTL).Err()
}

// LoadCheckout implements Store.
func (r *Redis) LoadCheckout(ctx context.Context, userID int64, nonce string) (Checkout, bool, error) {
	raw, err := r.rdb.Get(ctx, checkoutKey(userID, nonce)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Checkout{}, false, nil
	}
	if err != nil {
		return Checkout{}, false, err
	}
	var c Checkout
	if json.Unmarshal(raw, &c) != nil {
		return Checkout{}, false, nil
	}
	return c, true, nil
}

// Memory is an in-process Store for tests and single-instance development.
type Memory struct {
	mu        sync.Mutex
	state     map[int64]State
	seen      map[string]time.Time
	flags     map[string]time.Time
	checkouts map[string]Checkout
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{state: map[int64]State{}, seen: map[string]time.Time{}, flags: map[string]time.Time{},
		checkouts: map[string]Checkout{}}
}

// SaveCheckout implements Store (no expiry in memory).
func (m *Memory) SaveCheckout(_ context.Context, userID int64, nonce string, c Checkout) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkouts[checkoutKey(userID, nonce)] = c
	return nil
}

// LoadCheckout implements Store.
func (m *Memory) LoadCheckout(_ context.Context, userID int64, nonce string) (Checkout, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.checkouts[checkoutKey(userID, nonce)]
	return c, ok, nil
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

// Remember implements Store.
func (m *Memory) Remember(_ context.Context, k string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flags[k] = time.Now().Add(ttl)
	return nil
}

// Recall implements Store.
func (m *Memory) Recall(_ context.Context, k string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.flags[k]
	return ok && time.Now().Before(exp), nil
}
