// Package cicd reads the public CI/CD facts of the repository BOBRES is released from: the latest
// runs of its workflows on main, of the Release workflow, and its latest releases, from GitHub's
// public REST API. The bot does it for the dashboard's Monitoring page (core has no internet
// access), with the update check's conventions (internal/release): a time limit on every request,
// a limit on every answer, an explicit User-Agent, no credential of any kind (the API is read
// anonymously: 60 requests an hour per address), conditional requests (an unchanged answer is a
// 304 that costs no quota), a cache of about 5 minutes, a bounded number of retries, and a clear
// error when GitHub is unreachable or the quota is used up.
//
// Everything GitHub answers is untrusted text: only the fields below are kept, each checked and cut
// to length, and a link is kept only when it points into the repository.
package cicd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

// APIBase is GitHub's REST API.
const APIBase = "https://api.github.com"

// Errors a Status reports (Detail says more).
const (
	ErrNotChecked  = "not_checked"  // the first check has not finished
	ErrUnreachable = "unreachable"  // GitHub did not answer (blocked, down, timeout)
	ErrRateLimited = "rate_limited" // the quota of this address is used up
	ErrInvalid     = "invalid"      // GitHub answered something that is not what the API documents
)

// Bounds.
const (
	maxBody    = 2 << 20
	maxRuns    = 10
	maxRelRuns = 5
	maxRels    = 5
	// reqTimeout bounds each request; Retries more tries follow a network error or a 5xx answer.
	reqTimeout = 10 * time.Second
	// TTL is how long a good answer is served as it is; FailWait how long after a failed refresh the
	// next try waits.
	TTL      = 5 * time.Minute
	FailWait = time.Minute
	// maxBlock bounds how long a rate-limit answer stops the requests.
	maxBlock = time.Hour
)

// Run is one run of a workflow.
type Run struct {
	Workflow   string `json:"workflow"`
	Branch     string `json:"branch"`
	Event      string `json:"event"`
	Status     string `json:"status"`     // queued | in_progress | completed | ...
	Conclusion string `json:"conclusion"` // success | failure | cancelled | ... ("" while it runs)
	URL        string `json:"url"`
	SHA        string `json:"sha"`
	At         int64  `json:"at"` // unix seconds: when it last changed
}

// Release is one published release.
type Release struct {
	Tag         string `json:"tag"`
	PublishedAt int64  `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	URL         string `json:"url"`
}

// Status is the bot's answer to core at GET /internal/ci.
type Status struct {
	// Available: the data below is GitHub's (possibly an earlier answer, see Error).
	Available bool   `json:"available"`
	CheckedAt int64  `json:"checked_at"` // unix seconds of the last refresh that worked; 0 before the first
	Error     string `json:"error"`      // "" or one of the Err* codes, about the last try
	Detail    string `json:"detail"`
	Repo      string `json:"repo"`
	// Runs are the latest runs of every workflow on main, newest first.
	Runs []Run `json:"runs"`
	// ReleaseRuns are the latest runs of the Release workflow (started by a tag).
	// (a run whose Branch is the tag it was started for).
	ReleaseRuns []Run     `json:"release_runs"`
	Releases    []Release `json:"releases"`
	// RateResetAt is when the quota comes back, while Error is rate_limited.
	RateResetAt int64 `json:"rate_reset_at,omitempty"`
}

// Client reads the repository's CI/CD facts.
type Client struct {
	repo, base, ua string
	hc             *http.Client
	now            func() time.Time
	// Retries is how many more tries a network error or a 5xx answer gets; Backoff the first pause
	// (doubled each time). Tests set both to 0.
	Retries int
	Backoff time.Duration

	mu      sync.Mutex // one refresh at a time
	last    Status
	okAt    time.Time
	failAt  time.Time
	blocked time.Time // no request before this (the quota)
	etags   map[string]string
	bodies  map[string][]byte
}

// New is a client for the repository BOBRES is released from, for a bot at version. The default
// client's transport is the process's normal egress.
func New(version string) *Client {
	return NewFor(release.Repo, APIBase, version, &http.Client{Timeout: 2 * reqTimeout})
}

// NewFor is New for another repository or API address (tests).
func NewFor(repo, base, version string, hc *http.Client) *Client {
	if version == "" {
		version = "dev"
	}
	return &Client{repo: repo, base: strings.TrimRight(base, "/"), hc: hc, now: time.Now, Retries: 2, Backoff: 500 * time.Millisecond,
		ua:    "bobres/" + clean(version, 32) + " (+https://github.com/" + release.Repo + ")",
		last:  Status{Repo: repo, Error: ErrNotChecked, Runs: []Run{}, ReleaseRuns: []Run{}, Releases: []Release{}},
		etags: map[string]string{}, bodies: map[string][]byte{}}
}

// Status returns the facts, refreshing them when the last good answer is older than TTL (and the
// last failed try older than FailWait). A refresh that fails keeps the earlier facts, with the
// error. It never returns before the first answer or failure, and then waits at most ctx's time.
func (c *Client) Status(ctx context.Context) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	fresh := !c.okAt.IsZero() && now.Sub(c.okAt) < TTL && c.last.Error == ""
	retryLater := !c.failAt.IsZero() && now.Sub(c.failAt) < FailWait
	if !fresh && !retryLater {
		c.refresh(ctx, now)
	}
	return c.last
}

// refresh asks GitHub for the three lists and replaces c.last.
func (c *Client) refresh(ctx context.Context, now time.Time) {
	next := Status{Repo: c.repo, Runs: []Run{}, ReleaseRuns: []Run{}, Releases: []Release{}}
	fail := func(code, detail string, reset time.Time) {
		c.failAt = now
		// The earlier facts stay (an outage must not hide them), marked with the error.
		keep := c.last
		keep.Error, keep.Detail, keep.Repo = code, detail, c.repo
		if !reset.IsZero() {
			keep.RateResetAt = reset.Unix()
		} else {
			keep.RateResetAt = 0
		}
		keep.Available = keep.CheckedAt > 0
		if keep.Runs == nil {
			keep.Runs, keep.ReleaseRuns, keep.Releases = []Run{}, []Run{}, []Release{}
		}
		c.last = keep
	}
	if now.Before(c.blocked) {
		fail(ErrRateLimited, "GitHub's limit of 60 requests an hour per address is used up; the next request is after the quota comes back", c.blocked)
		return
	}
	paths := []struct {
		key, path string
		q         url.Values
	}{
		{"runs", "/repos/" + c.repo + "/actions/runs", url.Values{"branch": {"main"}, "per_page": {strconv.Itoa(maxRuns)}, "exclude_pull_requests": {"true"}}},
		{"release", "/repos/" + c.repo + "/actions/workflows/release.yml/runs", url.Values{"per_page": {strconv.Itoa(maxRelRuns)}}},
		{"releases", "/repos/" + c.repo + "/releases", url.Values{"per_page": {strconv.Itoa(maxRels)}}},
	}
	for _, p := range paths {
		body, err := c.get(ctx, p.key, p.path, p.q)
		if err != nil {
			var rl *rateLimitError
			switch {
			case errors.As(err, &rl):
				c.blocked = rl.until
				fail(ErrRateLimited, rl.Error(), rl.until)
			case errors.Is(err, errInvalid):
				fail(ErrInvalid, err.Error(), time.Time{})
			default:
				fail(ErrUnreachable, err.Error(), time.Time{})
			}
			return
		}
		var perr error
		switch p.key {
		case "runs":
			next.Runs, perr = parseRuns(body, c.repo, maxRuns)
		case "release":
			next.ReleaseRuns, perr = parseRuns(body, c.repo, maxRelRuns)
		default:
			next.Releases, perr = parseReleases(body, c.repo)
		}
		if perr != nil {
			fail(ErrInvalid, p.path+": "+perr.Error(), time.Time{})
			return
		}
	}
	next.Available, next.CheckedAt = true, now.Unix()
	c.last, c.okAt, c.failAt = next, now, time.Time{}
}

var errInvalid = errors.New("GitHub's answer is not what its API documents")

// rateLimitError: GitHub answered that the quota is used up.
type rateLimitError struct {
	status int
	until  time.Time
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("GitHub's API answered %d: its limit is 60 requests an hour per address (back at %s)", e.status, e.until.UTC().Format("15:04 UTC"))
}

// get makes one conditional request, with bounded retries, and returns the body (the earlier one
// when GitHub says 304).
func (c *Client) get(ctx context.Context, key, path string, q url.Values) ([]byte, error) {
	var lastErr error
	pause := c.Backoff
	for try := 0; try <= c.Retries; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(pause):
			}
			pause *= 2
		}
		body, retry, err := c.once(ctx, key, path, q)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, lastErr
}

// once makes one request. retry says whether a failure is worth another try.
func (c *Client) once(ctx context.Context, key, path string, q url.Values) (body []byte, retry bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, reqTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.ua)
	if tag := c.etags[key]; tag != "" && c.bodies[key] != nil {
		req.Header.Set("If-None-Match", tag)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, true, errors.New("GitHub did not answer (offline, blocked or too slow)")
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotModified && c.bodies[key] != nil:
		return c.bodies[key], false, nil
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		if rl := rateLimited(resp, c.now()); rl != nil {
			return nil, false, rl
		}
		return nil, false, fmt.Errorf("GitHub's API answered %d", resp.StatusCode)
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("GitHub's API answered %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("GitHub's API answered %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, true, errors.New("GitHub's answer was cut short")
	}
	if len(b) > maxBody {
		return nil, false, fmt.Errorf("%w: larger than %d bytes", errInvalid, maxBody)
	}
	if tag := resp.Header.Get("ETag"); tag != "" && len(tag) <= 200 {
		c.etags[key], c.bodies[key] = tag, b
	}
	return b, false, nil
}

// rateLimited reads GitHub's rate-limit answer: 403 or 429 with no quota left or a Retry-After.
// nil when the answer is some other refusal.
func rateLimited(resp *http.Response, now time.Time) *rateLimitError {
	until := time.Time{}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			until = now.Add(time.Duration(n) * time.Second)
		}
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && n > 0 {
			until = time.Unix(n, 0)
		} else if until.IsZero() {
			until = now.Add(15 * time.Minute)
		}
	}
	if until.IsZero() && resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	if until.IsZero() {
		until = now.Add(15 * time.Minute)
	}
	if until.Before(now) {
		until = now.Add(time.Minute)
	}
	if until.After(now.Add(maxBlock)) {
		until = now.Add(maxBlock)
	}
	return &rateLimitError{status: resp.StatusCode, until: until}
}

// ---- parsing ----

var (
	branchRe     = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)
	tagRe        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	shaRe        = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	eventRe      = regexp.MustCompile(`^[a-z_]{1,40}$`)
	statuses     = []string{"queued", "in_progress", "completed", "waiting", "requested", "pending"}
	conclusions  = []string{"success", "failure", "cancelled", "skipped", "timed_out", "neutral", "action_required", "stale", "startup_failure"}
	errNotAnList = errors.New("not the JSON list the API documents")
)

func clean(s string, n int) string { return snapshot.Clean(s, n) }

// ownLink keeps a link only when it points into the repository on github.com.
func ownLink(u, repo string) string {
	if len(u) > 300 || !strings.HasPrefix(u, "https://github.com/"+repo+"/") {
		return ""
	}
	if p, err := url.Parse(u); err != nil || p.Host != "github.com" || p.User != nil || p.RawQuery != "" || p.Fragment != "" {
		return ""
	}
	return u
}

func unixOf(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.Unix() <= 0 {
		return 0
	}
	return t.Unix()
}

func parseRuns(body []byte, repo string, limit int) ([]Run, error) {
	var doc struct {
		Runs []struct {
			Name       string `json:"name"`
			Branch     string `json:"head_branch"`
			Event      string `json:"event"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			URL        string `json:"html_url"`
			SHA        string `json:"head_sha"`
			Updated    string `json:"updated_at"`
			Created    string `json:"created_at"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errNotAnList
	}
	out := []Run{}
	for _, r := range doc.Runs {
		if len(out) == limit {
			break
		}
		run := Run{Workflow: clean(r.Name, 64), URL: ownLink(r.URL, repo), At: unixOf(r.Updated)}
		if run.Workflow == "" {
			continue
		}
		if run.At == 0 {
			run.At = unixOf(r.Created)
		}
		if branchRe.MatchString(r.Branch) {
			run.Branch = r.Branch
		}
		if eventRe.MatchString(r.Event) {
			run.Event = r.Event
		}
		run.Status = "unknown"
		for _, s := range statuses {
			if r.Status == s {
				run.Status = s
			}
		}
		for _, s := range conclusions {
			if r.Conclusion == s {
				run.Conclusion = s
			}
		}
		if shaRe.MatchString(strings.ToLower(r.SHA)) {
			run.SHA = strings.ToLower(r.SHA)[:7]
		}
		out = append(out, run)
	}
	return out, nil
}

func parseReleases(body []byte, repo string) ([]Release, error) {
	var list []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Published  string `json:"published_at"`
		URL        string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, errNotAnList
	}
	out := []Release{}
	for _, r := range list {
		if len(out) == maxRels {
			break
		}
		if r.Draft || !tagRe.MatchString(r.Tag) {
			continue
		}
		out = append(out, Release{Tag: r.Tag, PublishedAt: unixOf(r.Published), Prerelease: r.Prerelease, URL: ownLink(r.URL, repo)})
	}
	return out, nil
}
