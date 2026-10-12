package licensesrv

import (
	"container/list"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate is "Max requests per Window".
type Rate struct {
	Max    int
	Window time.Duration
}

func (r Rate) String() string { return fmt.Sprintf("%d/%s", r.Max, r.Window) }

// ParseRate reads "N/duration", e.g. "120/1h".
func ParseRate(s string) (Rate, error) {
	n, w, ok := strings.Cut(strings.TrimSpace(s), "/")
	max, err := strconv.Atoi(n)
	if !ok || err != nil || max <= 0 {
		return Rate{}, fmt.Errorf("rate %q: want N/duration, e.g. 120/1h", s)
	}
	d, err := time.ParseDuration(w)
	if err != nil || d <= 0 {
		return Rate{}, fmt.Errorf("rate %q: want N/duration, e.g. 120/1h", s)
	}
	return Rate{Max: max, Window: d}, nil
}

// maxLimiterKeys bounds the limiter's memory. When that many callers are
// inside their window at once, a new caller makes the limiter forget the
// window that started first. Refusing new callers instead would let anyone
// with enough addresses (an IPv6 /48 holds 65,536 /64s) fill the table and
// lock every real install out of activating and refreshing. Forgetting a
// window only lets that caller in early; abuse stays bounded by the 160-bit
// keys, the signed tokens and the per-license limit, which only real
// licenses can fill.
const maxLimiterKeys = 100_000

// limiter is an in-memory fixed-window counter. The server runs as one
// process, so this needs no Redis (internal/ratelimit is the shared one);
// a restart forgets the counts, which only ever lets callers in early.
type limiter struct {
	rate Rate
	now  func() time.Time

	mu      sync.Mutex
	windows map[string]*list.Element // of *window
	order   *list.List               // the windows, the one that started first in front
}

type window struct {
	key   string
	start time.Time
	n     int
}

func newLimiter(r Rate, now func() time.Time) *limiter {
	return &limiter{rate: r, now: now, windows: map[string]*list.Element{}, order: list.New()}
}

// allow counts one request for key and reports whether it is inside the
// rate; when it is not, retry is how long until the window ends.
func (l *limiter) allow(key string) (ok bool, retry time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Read the clock under the lock, so windows join the list in start order.
	now := l.now()
	l.expire(now)
	e := l.windows[key]
	if e != nil && l.ended(e.Value.(*window), now) {
		// Only if the clock ever went back: the list is then out of start
		// order, and expire can stop before this window.
		l.forget(e)
		e = nil
	}
	if e == nil {
		if len(l.windows) >= maxLimiterKeys {
			l.forget(l.order.Front())
		}
		e = l.order.PushBack(&window{key: key, start: now})
		l.windows[key] = e
	}
	w := e.Value.(*window)
	w.n++
	if w.n > l.rate.Max {
		return false, w.start.Add(l.rate.Window).Sub(now)
	}
	return true, 0
}

// expire forgets the windows that have ended. They sit at the front of the
// list, so each call does work only for windows it removes.
func (l *limiter) expire(now time.Time) {
	for e := l.order.Front(); e != nil && l.ended(e.Value.(*window), now); e = l.order.Front() {
		l.forget(e)
	}
}

func (l *limiter) ended(w *window, now time.Time) bool { return now.Sub(w.start) >= l.rate.Window }

func (l *limiter) forget(e *list.Element) {
	delete(l.windows, e.Value.(*window).key)
	l.order.Remove(e)
}

// ipKey is the limiter key for a client address: the address itself for
// IPv4, its /64 for IPv6 (one host usually holds a whole /64).
func ipKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}
