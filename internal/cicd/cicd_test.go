package cicd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

const (
	runsJSON = `{"workflow_runs":[
	 {"name":"CI","head_branch":"main","event":"push","status":"completed","conclusion":"failure",
	  "html_url":"https://github.com/o/r/actions/runs/11","head_sha":"ABCDEF0123456789abcdef0123456789abcdef01","updated_at":"2026-10-20T10:00:00Z"},
	 {"name":"Docs","head_branch":"main","event":"push","status":"in_progress","conclusion":null,
	  "html_url":"https://github.com/o/r/actions/runs/12","head_sha":"abcdef0123456789abcdef0123456789abcdef01","updated_at":"2026-10-20T10:05:00Z"}]}`
	releaseRunsJSON = `{"workflow_runs":[
	 {"name":"Release","head_branch":"v0.7.0","event":"push","status":"completed","conclusion":"success",
	  "html_url":"https://github.com/o/r/actions/runs/9","head_sha":"1234567890abcdef1234567890abcdef12345678","updated_at":"2026-10-19T08:00:00Z"}]}`
	releasesJSON = `[
	 {"tag_name":"v0.7.0","draft":false,"prerelease":false,"published_at":"2026-10-19T08:10:00Z","html_url":"https://github.com/o/r/releases/tag/v0.7.0"},
	 {"tag_name":"v0.8.0-rc.1","draft":true,"prerelease":true,"published_at":"2026-10-20T08:10:00Z","html_url":"https://github.com/o/r/releases/tag/v0.8.0-rc.1"},
	 {"tag_name":"v0.6.1","draft":false,"prerelease":true,"published_at":"2026-10-10T08:10:00Z","html_url":"https://evil.example/phish"}]`
)

// github is a fake of the three endpoints, with a request log.
type github struct {
	mu       sync.Mutex
	hits     map[string]int // by path
	cond     map[string]int // conditional requests that got a 304
	agents   []string
	auth     []string
	etag     string
	status   int // when not 0, answers every request with it
	headers  map[string]string
	body     map[string]string
	oversize bool
}

func newGithub() *github {
	return &github{hits: map[string]int{}, cond: map[string]int{}, etag: `W/"v1"`, body: map[string]string{
		"/repos/o/r/actions/runs":                       runsJSON,
		"/repos/o/r/actions/workflows/release.yml/runs": releaseRunsJSON,
		"/repos/o/r/releases":                           releasesJSON,
	}}
}

func (g *github) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hits[r.URL.Path]++
	g.agents = append(g.agents, r.Header.Get("User-Agent"))
	g.auth = append(g.auth, r.Header.Get("Authorization")+r.Header.Get("Cookie"))
	if g.status != 0 {
		for k, v := range g.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(g.status)
		return
	}
	if g.oversize {
		_, _ = w.Write([]byte(strings.Repeat("x", maxBody+10)))
		return
	}
	body, ok := g.body[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("If-None-Match") == g.etag {
		g.cond[r.URL.Path]++
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", g.etag)
	_, _ = w.Write([]byte(body))
}

func (g *github) total() (n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, h := range g.hits {
		n += h
	}
	return n
}

// clock is a movable time source.
type clock struct{ t atomic.Int64 }

func (c *clock) now() time.Time      { return time.Unix(c.t.Load(), 0) }
func (c *clock) add(d time.Duration) { c.t.Add(int64(d / time.Second)) }

func newClient(srv *httptest.Server) (*Client, *clock) {
	c := NewFor("o/r", srv.URL, "0.7.0", srv.Client())
	c.Retries, c.Backoff = 0, 0
	ck := &clock{}
	ck.t.Store(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC).Unix())
	c.now = ck.now
	return c, ck
}

func TestStatusParsesAndKeepsOnlyAllowListedFields(t *testing.T) {
	g := newGithub()
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, ck := newClient(srv)

	st := c.Status(context.Background())
	if !st.Available || st.Error != "" || st.CheckedAt != ck.now().Unix() || st.Repo != "o/r" {
		t.Fatalf("status: %+v", st)
	}
	if len(st.Runs) != 2 || st.Runs[0].Workflow != "CI" || st.Runs[0].Conclusion != "failure" || st.Runs[0].Branch != "main" ||
		st.Runs[0].SHA != "abcdef0" || st.Runs[0].At != time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).Unix() ||
		st.Runs[0].URL != "https://github.com/o/r/actions/runs/11" {
		t.Fatalf("runs: %+v", st.Runs)
	}
	if st.Runs[1].Status != "in_progress" || st.Runs[1].Conclusion != "" {
		t.Fatalf("running run: %+v", st.Runs[1])
	}
	if len(st.ReleaseRuns) != 1 || st.ReleaseRuns[0].Branch != "v0.7.0" || st.ReleaseRuns[0].Conclusion != "success" {
		t.Fatalf("release runs: %+v", st.ReleaseRuns)
	}
	// The draft is left out, and a link that leaves the repository is dropped.
	if len(st.Releases) != 2 || st.Releases[0].Tag != "v0.7.0" || st.Releases[0].Prerelease || st.Releases[1].URL != "" || !st.Releases[1].Prerelease {
		t.Fatalf("releases: %+v", st.Releases)
	}
	// Every request says who asks and carries no credential.
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, ua := range g.agents {
		if !strings.HasPrefix(ua, "bobres/0.7.0 ") || g.auth[i] != "" {
			t.Fatalf("request %d: agent %q, credentials %q", i, ua, g.auth[i])
		}
	}
}

func TestStatusIsCachedThenRevalidatedWithETag(t *testing.T) {
	g := newGithub()
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, ck := newClient(srv)

	first := c.Status(context.Background())
	if g.total() != 3 {
		t.Fatalf("first check made %d requests", g.total())
	}
	// Within the cache time nothing is asked.
	ck.add(4 * time.Minute)
	if again := c.Status(context.Background()); again.CheckedAt != first.CheckedAt || g.total() != 3 {
		t.Fatalf("not served from the cache: %d requests", g.total())
	}
	// After it, a conditional request; the 304 costs no quota and keeps the data.
	ck.add(2 * time.Minute)
	st := c.Status(context.Background())
	if g.total() != 6 || len(g.cond) != 3 {
		t.Fatalf("revalidation: %d requests, %v not modified", g.total(), g.cond)
	}
	if !st.Available || st.Error != "" || st.CheckedAt != ck.now().Unix() || len(st.Runs) != 2 || len(st.Releases) != 2 {
		t.Fatalf("after 304: %+v", st)
	}
}

func TestRateLimitIsSaidClearlyAndStopsRequests(t *testing.T) {
	g := newGithub()
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, ck := newClient(srv)
	good := c.Status(context.Background())

	reset := ck.now().Add(20 * time.Minute)
	g.mu.Lock()
	g.status = http.StatusForbidden
	g.headers = map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": fmt.Sprint(reset.Unix())}
	g.mu.Unlock()
	ck.add(6 * time.Minute)
	st := c.Status(context.Background())
	if st.Error != ErrRateLimited || st.RateResetAt != reset.Unix() || !strings.Contains(st.Detail, "60 requests an hour") {
		t.Fatalf("rate limited: %+v", st)
	}
	// The earlier facts stay readable, marked with the error.
	if !st.Available || st.CheckedAt != good.CheckedAt || len(st.Runs) != 2 {
		t.Fatalf("lost the earlier data: %+v", st)
	}
	// Until the quota is back, not one more request is made (a quota already used up must not be hit).
	n := g.total()
	ck.add(2 * time.Minute)
	if st := c.Status(context.Background()); st.Error != ErrRateLimited || g.total() != n {
		t.Fatalf("asked GitHub during the block: %d -> %d, %+v", n, g.total(), st)
	}
	// When it is back, the next check works again.
	g.mu.Lock()
	g.status = 0
	g.mu.Unlock()
	ck.add(25 * time.Minute)
	if st := c.Status(context.Background()); st.Error != "" || !st.Available || st.RateResetAt != 0 {
		t.Fatalf("after the quota came back: %+v", st)
	}
}

func TestRateLimitWithRetryAfter(t *testing.T) {
	g := newGithub()
	g.status = http.StatusTooManyRequests
	g.headers = map[string]string{"Retry-After": "120"}
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, ck := newClient(srv)
	st := c.Status(context.Background())
	if st.Error != ErrRateLimited || st.Available || st.RateResetAt != ck.now().Add(2*time.Minute).Unix() {
		t.Fatalf("429: %+v", st)
	}
	// Nothing was ever fetched: empty lists, never null.
	if st.Runs == nil || st.ReleaseRuns == nil || st.Releases == nil {
		t.Fatalf("null lists: %+v", st)
	}
}

// A 403 that is not about the quota (a blocked address, for instance) is a plain refusal, never a block.
func TestOtherRefusalIsNotARateLimit(t *testing.T) {
	g := newGithub()
	g.status = http.StatusForbidden
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, _ := newClient(srv)
	st := c.Status(context.Background())
	if st.Error != ErrUnreachable || !strings.Contains(st.Detail, "403") || st.RateResetAt != 0 {
		t.Fatalf("403: %+v", st)
	}
}

func TestOfflineKeepsEarlierDataAndRetriesLater(t *testing.T) {
	g := newGithub()
	srv := httptest.NewServer(g)
	c, ck := newClient(srv)
	if st := c.Status(context.Background()); !st.Available {
		t.Fatalf("setup: %+v", st)
	}
	srv.Close() // GitHub is unreachable now
	ck.add(6 * time.Minute)
	st := c.Status(context.Background())
	if st.Error != ErrUnreachable || !st.Available || len(st.Runs) != 2 || !strings.Contains(st.Detail, "did not answer") {
		t.Fatalf("offline: %+v", st)
	}
	// A failed try is not repeated on every call.
	failed := st.CheckedAt
	ck.add(10 * time.Second)
	if again := c.Status(context.Background()); again.Error != ErrUnreachable || again.CheckedAt != failed {
		t.Fatalf("again: %+v", again)
	}
}

func TestOfflineFromTheStart(t *testing.T) {
	srv := httptest.NewServer(newGithub())
	srv.Close()
	c, _ := newClient(srv)
	st := c.Status(context.Background())
	if st.Available || st.Error != ErrUnreachable || st.CheckedAt != 0 || st.Runs == nil {
		t.Fatalf("offline: %+v", st)
	}
}

func TestNotCheckedYet(t *testing.T) {
	c := NewFor("o/r", "http://127.0.0.1:1", "dev", http.DefaultClient)
	if st := c.last; st.Error != ErrNotChecked || st.Available {
		t.Fatalf("initial: %+v", st)
	}
}

func TestServerErrorsAreRetriedAFewTimes(t *testing.T) {
	var calls atomic.Int32
	g := newGithub()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/actions/runs" && calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		g.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, _ := newClient(srv)
	c.Retries = 2
	if st := c.Status(context.Background()); !st.Available || st.Error != "" || calls.Load() != 3 {
		t.Fatalf("retries: %d calls, %+v", calls.Load(), st)
	}
	// The retries are bounded.
	calls.Store(-100)
	c2, _ := newClient(srv)
	c2.Retries = 1
	st := c2.Status(context.Background())
	if st.Error != ErrUnreachable || !strings.Contains(st.Detail, "502") {
		t.Fatalf("exhausted: %+v", st)
	}
}

func TestOversizedAndMalformedAnswersAreRefused(t *testing.T) {
	g := newGithub()
	g.oversize = true
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, _ := newClient(srv)
	if st := c.Status(context.Background()); st.Error != ErrInvalid || st.Available {
		t.Fatalf("oversized: %+v", st)
	}

	g2 := newGithub()
	g2.body["/repos/o/r/actions/runs"] = `<html>a captive portal</html>`
	srv2 := httptest.NewServer(g2)
	defer srv2.Close()
	c2, _ := newClient(srv2)
	if st := c2.Status(context.Background()); st.Error != ErrInvalid || st.Available {
		t.Fatalf("malformed: %+v", st)
	}
}

// What GitHub says is untrusted text: hostile names, branches, hashes and links are cleaned or dropped.
func TestHostileFieldsAreCleanedOrDropped(t *testing.T) {
	g := newGithub()
	g.body["/repos/o/r/actions/runs"] = `{"workflow_runs":[
	 {"name":"CI\u202e<script>alert(1)</script>","head_branch":"main; rm -rf /","event":"push\n","status":"weird","conclusion":"owned",
	  "html_url":"https://github.com/o/r/actions/runs/1?token=x","head_sha":"not-a-sha","updated_at":"yesterday"},
	 {"name":"","head_branch":"main","status":"completed"},
	 {"name":"` + strings.Repeat("W", 500) + `","head_branch":"main","status":"completed","conclusion":"success"}]}`
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, _ := newClient(srv)
	st := c.Status(context.Background())
	if !st.Available || len(st.Runs) != 2 {
		t.Fatalf("status: %+v", st)
	}
	r := st.Runs[0]
	if strings.ContainsAny(r.Workflow, "<>\u202e") || r.Branch != "" || r.Event != "" || r.Status != "unknown" || r.Conclusion != "" ||
		r.URL != "" || r.SHA != "" || r.At != 0 {
		t.Fatalf("hostile run kept: %+v", r)
	}
	if len(st.Runs[1].Workflow) > 70 {
		t.Fatalf("long name kept: %d", len(st.Runs[1].Workflow))
	}
}

func TestListsAreBounded(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"workflow_runs":[`)
	for i := range 40 {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"name":"W%d","head_branch":"main","status":"completed","conclusion":"success","updated_at":"2026-10-20T10:00:00Z"}`, i)
	}
	sb.WriteString(`]}`)
	g := newGithub()
	g.body["/repos/o/r/actions/runs"] = sb.String()
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, _ := newClient(srv)
	if st := c.Status(context.Background()); len(st.Runs) != maxRuns {
		t.Fatalf("runs: %d", len(st.Runs))
	}
}

// The repository is the build's constant: the default client asks no other.
func TestDefaultClientAsksTheReleaseRepository(t *testing.T) {
	c := New("0.7.0")
	if c.repo != release.Repo || c.base != APIBase || !strings.HasSuffix(release.APIURL, release.Repo+"/releases") {
		t.Fatalf("client: %+v", c)
	}
	if !strings.Contains(c.ua, "bobres/0.7.0") {
		t.Fatalf("user agent %q", c.ua)
	}
}

// Core checks the bot's answer again before it reaches a page.
func TestSanitizeBoundsAndCleansWhatCameOverTheWire(t *testing.T) {
	good := "https://github.com/" + release.Repo + "/actions/runs/5"
	in := Status{Available: true, CheckedAt: -5, Error: "something else", Detail: "<b>x</b>\u202e" + strings.Repeat("d", 900), Repo: "evil/repo",
		RateResetAt: -1,
		Runs: []Run{
			{Workflow: "CI", Branch: "main", Status: "completed", Conclusion: "success", URL: good, SHA: "abcdef0", At: 100},
			{Workflow: "Bad<>", Branch: "a b", Event: "x y", Status: "weird", Conclusion: "owned", URL: "https://evil.example/x", SHA: "zzz", At: -3},
			{Workflow: ""},
		},
		Releases: []Release{{Tag: "v1.0.0", URL: good}, {Tag: "bad tag"}, {Tag: "v1.0.1", PublishedAt: 7, URL: "javascript:alert(1)"}}}
	for range 30 {
		in.Runs = append(in.Runs, Run{Workflow: "W"})
		in.Releases = append(in.Releases, Release{Tag: "v9"})
	}
	out := Sanitize(in)
	if out.Repo != release.Repo || out.CheckedAt != 0 || out.RateResetAt != 0 || out.Error != ErrInvalid || len(out.Detail) > 320 ||
		strings.ContainsAny(out.Detail, "<>\u202e") {
		t.Fatalf("status: %+v", out)
	}
	if len(out.Runs) != maxRuns || out.Runs[0].URL != good || out.Runs[0].SHA != "abcdef0" || out.Runs[0].Conclusion != "success" {
		t.Fatalf("runs: %+v", out.Runs)
	}
	bad := out.Runs[1]
	if strings.ContainsAny(bad.Workflow, "<>") || bad.Branch != "" || bad.Event != "" || bad.Status != "unknown" || bad.Conclusion != "" || bad.URL != "" ||
		bad.SHA != "" || bad.At != 0 {
		t.Fatalf("hostile run: %+v", bad)
	}
	if len(out.Releases) != maxRels || out.Releases[0].URL != good || out.Releases[1].Tag != "v1.0.1" || out.Releases[1].URL != "" {
		t.Fatalf("releases: %+v", out.Releases)
	}
	// An empty status keeps its lists as lists.
	if e := Sanitize(Status{}); e.Runs == nil || e.ReleaseRuns == nil || e.Releases == nil {
		t.Fatalf("null lists: %+v", e)
	}
}

func TestConcurrentCallsShareOneRefresh(t *testing.T) {
	g := newGithub()
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, _ := newClient(srv)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = c.Status(context.Background()) })
	}
	wg.Wait()
	if g.total() != 3 {
		t.Fatalf("%d requests for 8 callers", g.total())
	}
}
