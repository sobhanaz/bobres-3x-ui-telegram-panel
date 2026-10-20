package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// releasesURL is this repository's GitHub releases.
const releasesURL = "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases"

// DefaultBaseURL is where the latest (non-pre-release) GitHub release's files
// are, as in install/install.sh.
const DefaultBaseURL = releasesURL + "/latest/download"

// DownloadURL is where release v's files are (its tag is vX.Y.Z), as
// DefaultBaseURL is for the latest one.
func DownloadURL(v string) string {
	return releasesURL + "/download/v" + strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// Errors a Status reports (Detail says more).
const (
	ErrNotChecked   = "not_checked"   // the first check has not finished yet
	ErrUnreachable  = "unreachable"   // GitHub did not answer (blocked, down, timeout)
	ErrNoRelease    = "no_release"    // the repository has no published release
	ErrBadSignature = "bad_signature" // unsigned, tampered with, revoked or expired
	ErrInvalid      = "invalid"       // checksums.txt has no version header
)

// Status is the outcome of the last update check. After a check GitHub did
// not answer (Error unreachable), Latest, Issued, Newer and Verified are the
// last verified check's, if there was one.
type Status struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`  // "" when unknown
	Channel string `json:"channel"` // the channel this install follows: stable or beta
	// Issued is when Latest was signed (its header's "# issued",
	// 2006-01-02T15:04Z), "" when unknown: core checks it against the
	// license's updates_until.
	Issued    string `json:"issued,omitempty"`
	Newer     bool   `json:"newer"` // Latest is verified and newer than Current
	Verified  bool   `json:"verified"`
	CheckedAt int64  `json:"checked_at"` // unix seconds, 0 before the first check
	Error     string `json:"error"`      // "" or one of the Err* codes
	Detail    string `json:"detail"`     // a short English reason, "" when fine
}

// Checker looks for a newer release now and then and keeps the answer. The
// channel it follows can change while it runs (SetChannel: the dashboard's
// updates.channel setting), and a person can ask it to look again at once
// (CheckNow: the dashboard's "Check again").
type Checker struct {
	current string
	src     Source
	hc      *http.Client
	roots   []string
	now     func() time.Time
	log     *slog.Logger

	// Every and Retry are the pauses after a check that worked and one that
	// did not.
	Every, Retry time.Duration
	// ManualGap: a check a person asks for is answered with the last one,
	// when that worked and finished less than this long ago, so a click
	// (or two people clicking) never costs GitHub more than one look.
	// ManualTimeout bounds the check a person waits for (core's answer to the
	// dashboard must come within its 30 second write timeout).
	ManualGap, ManualTimeout time.Duration

	// run holds one check at a time (the scheduled one and the ones people
	// ask for); mu guards the state below it.
	run      sync.Mutex
	mu       sync.Mutex
	channel  string
	last     Status
	lastDone time.Time // when the last check finished
	// kick wakes Run to look again at once (the channel changed).
	kick chan struct{}
}

// maxFile bounds each downloaded metadata file.
const maxFile = 256 << 10

// NewChecker checks the newest release of channel (stable or beta, as
// ParseChannel reads it) on GitHub against the embedded Roots, for a bot at
// version current. The client's default transport is the process's normal
// egress (it honours HTTPS_PROXY).
func NewChecker(current, channel string, log *slog.Logger) *Checker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	return &Checker{
		current: current, channel: channel, src: DefaultSource(hc), roots: Roots, now: time.Now, log: log,
		hc:    hc,
		Every: 12 * time.Hour, Retry: time.Hour, ManualGap: 10 * time.Second, ManualTimeout: 22 * time.Second,
		last: Status{Current: current, Channel: channel, Error: ErrNotChecked},
		kick: make(chan struct{}, 1),
	}
}

// Status is the last check's outcome.
func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Channel is the channel the checks follow now.
func (c *Checker) Channel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channel
}

// SetChannel makes the checks follow channel (stable or beta, as ParseChannel
// reads it; anything else leaves the channel as it is) and reports whether
// that changed it. A change forgets what the other channel's check found
// (its newest release is not this one's) and makes Run look again at once,
// so the page never shows one channel's release under the other's name.
func (c *Checker) SetChannel(channel string) bool {
	ch, err := ParseChannel(channel)
	if err != nil {
		c.log.Warn("update check: the channel is not stable or beta; keeping the current one", "channel", channel)
		return false
	}
	c.mu.Lock()
	if ch == c.channel {
		c.mu.Unlock()
		return false
	}
	c.channel = ch
	c.last = Status{Current: c.current, Channel: ch, Error: ErrNotChecked}
	c.lastDone = time.Time{}
	c.mu.Unlock()
	c.log.Info("update check: following another channel", "channel", ch)
	select {
	case c.kick <- struct{}{}:
	default:
	}
	return true
}

// Run checks at once, then every c.Every (c.Retry after a failure), until
// ctx ends; a change of channel (SetChannel) checks again without waiting.
func (c *Checker) Run(ctx context.Context) error {
	for {
		wait := c.Every
		if st := c.Check(ctx); st.Error != "" {
			wait = c.Retry
			c.log.Info("update check failed", "error", st.Error, "detail", st.Detail)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-c.kick:
			t.Stop()
		case <-t.C:
		}
	}
}

// Check fetches and verifies the latest release's metadata now, and keeps
// the outcome. When GitHub does not answer, the release the last verified
// check read stays known (with the failure), so an outage does not hide an
// update.
func (c *Checker) Check(ctx context.Context) Status {
	c.run.Lock()
	defer c.run.Unlock()
	return c.look(ctx, false)
}

// CheckNow is "check again": it looks now, whatever the timers say, and
// returns the outcome. A check that worked and finished less than ManualGap
// ago is returned as it is. A caller that stops waiting (ctx) gets the last
// outcome; the check goes on, bounded by ManualTimeout, and is kept.
func (c *Checker) CheckNow(ctx context.Context) Status {
	done := make(chan Status, 1)
	go func() { done <- c.checkNow() }()
	select {
	case st := <-done:
		return st
	case <-ctx.Done():
		return c.Status()
	}
}

func (c *Checker) checkNow() Status {
	c.run.Lock()
	defer c.run.Unlock()
	c.mu.Lock()
	if c.last.Error == "" && !c.lastDone.IsZero() && c.now().Sub(c.lastDone) < c.ManualGap {
		st := c.last
		c.mu.Unlock()
		return st
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), c.ManualTimeout)
	defer cancel()
	return c.look(ctx, true)
}

// look makes one check (the caller holds c.run) and keeps its outcome. A
// check the process cut short (shutting down) keeps the last outcome; one a
// person asked for keeps even its timeout, which says GitHub did not answer
// in time.
func (c *Checker) look(ctx context.Context, manual bool) Status {
	channel := c.Channel()
	st := c.check(ctx, channel)
	st.Current, st.Channel, st.CheckedAt = c.current, channel, c.now().Unix()
	if !manual && ctx.Err() != nil && st.Error != "" {
		return st // shutting down: keep the last outcome
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channel != channel {
		// The channel changed while GitHub was being asked: this answer is
		// about the other one and is dropped; Run looks again at once.
		return c.last
	}
	if st.Error == ErrUnreachable && c.last.Verified {
		st.Latest, st.Issued, st.Newer, st.Verified = c.last.Latest, c.last.Issued, c.last.Newer, c.last.Verified
	}
	c.last, c.lastDone = st, c.now()
	return st
}

func (c *Checker) check(ctx context.Context, channel string) Status {
	fail := func(code string, err error) Status { return Status{Error: code, Detail: err.Error()} }
	base, want, err := c.src.Resolve(ctx, channel)
	switch {
	case errors.Is(err, ErrNotFound):
		return fail(ErrNoRelease, err)
	case err != nil:
		return fail(ErrUnreachable, err)
	}
	sums, err := c.fetch(ctx, base, "checksums.txt")
	switch {
	case errors.Is(err, ErrNotFound):
		return fail(ErrNoRelease, err)
	case err != nil:
		return fail(ErrUnreachable, err)
	}
	h := ParseHeader(sums)
	if !Valid(h.Version) {
		return fail(ErrInvalid, errors.New("checksums.txt has no valid '# version' header"))
	}
	// From here on a failure is about trust, not reachability: the claimed
	// version is shown, but never as an update to apply.
	claimed := func(code string, err error) Status {
		st := fail(code, err)
		st.Latest = h.Version
		return st
	}
	sig, err := c.fetch(ctx, base, "checksums.txt.sig")
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return fail(ErrUnreachable, err)
		}
		return claimed(ErrBadSignature, errors.New("the release is not signed (no checksums.txt.sig)"))
	}
	keys, err := c.fetch(ctx, base, "keys.txt")
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return fail(ErrUnreachable, err)
		}
		return claimed(ErrBadSignature, errors.New("the release has no keys.txt"))
	}
	var keySigs [][]byte
	for n := 1; n <= 4; n++ { // as many as the installer looks for
		s, err := c.fetch(ctx, base, "keys.txt.sig."+strconv.Itoa(n))
		switch {
		case err == nil:
			keySigs = append(keySigs, s)
		case !errors.Is(err, ErrNotFound):
			return fail(ErrUnreachable, err)
		}
	}
	rel, err := Verify(c.roots, keys, keySigs, sums, sig, c.now())
	if err != nil {
		return claimed(ErrBadSignature, err)
	}
	// The beta channel found the version through GitHub's API, which is
	// not signed: the signed header must name the same one.
	if want != "" && rel.Version != want {
		return claimed(ErrBadSignature, fmt.Errorf("the files of release %s are signed for %s", want, rel.Version))
	}
	if !ChannelAllows(channel, rel.Channel) {
		return claimed(ErrBadSignature, fmt.Errorf("release %s is signed for the %s channel, and this install follows %s", rel.Version, rel.Channel, channel))
	}
	return Status{
		Latest: rel.Version, Issued: rel.Issued, Verified: true,
		// "dev" and other non-release builds cannot tell.
		Newer: Valid(c.current) && Compare(rel.Version, c.current) > 0,
	}
}

func (c *Checker) fetch(ctx context.Context, base, name string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s: answered %d", name, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFile+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("%s: larger than %d bytes", name, maxFile)
	}
	return data, nil
}
