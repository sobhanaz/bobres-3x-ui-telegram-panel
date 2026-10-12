// Package syshealth asks every BOBRES service for its /healthz and /readyz,
// for the dashboard's System page. Core reaches them over the internal
// network without a token: these endpoints say only ok or fail and the
// version, never why (the reasons stay in each service's logs).
package syshealth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Timeout bounds each request to a service.
const Timeout = 3 * time.Second

// maxBody bounds what is read of an answer.
const maxBody = 64 << 10

// Service statuses.
const (
	StatusOK            = "ok"            // alive and ready
	StatusNotReady      = "not_ready"     // alive, but a dependency failed
	StatusDown          = "down"          // does not answer
	StatusNotInstalled  = "not_installed" // an optional service this install does not run
	StatusNotConfigured = "not_configured"
)

// Peer is a service to ask.
type Peer struct {
	Name string
	// URL is its internal address (http://bot:8080); "" when not configured.
	URL string
	// Optional services whose name the network cannot resolve are not
	// installed rather than down.
	Optional bool
}

// Service is what one service answered.
type Service struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version string `json:"version"`
	// Checks are the readiness checks by name ("ok" or "fail").
	Checks map[string]string `json:"checks"`
	// Detail says why a service is down or not ready, in a few words.
	Detail    string `json:"detail"`
	LatencyMS int64  `json:"latency_ms"`
	// VersionMismatch: it runs another version than core.
	VersionMismatch bool `json:"version_mismatch"`
}

// Report is every service's answer.
type Report struct {
	// Version is core's own version.
	Version  string    `json:"version"`
	Services []Service `json:"services"`
	// VersionMismatch: a service answered with another version than core.
	VersionMismatch bool `json:"version_mismatch"`
}

// Checker asks a fixed set of services.
type Checker struct {
	version string
	peers   []Peer
	// Client sends the requests (replaced in tests). It never follows a
	// redirect.
	Client *http.Client
}

// New builds a Checker for core at version asking peers (core itself among
// them, through its own HTTP address).
func New(version string, peers []Peer) *Checker {
	return &Checker{version: version, peers: peers, Client: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Check asks every service at once, each request within Timeout.
func (c *Checker) Check(ctx context.Context) Report {
	out := Report{Version: c.version, Services: make([]Service, len(c.peers))}
	var wg sync.WaitGroup
	for i, p := range c.peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out.Services[i] = c.probe(ctx, p)
		}()
	}
	wg.Wait()
	want := NormalizeVersion(c.version)
	for i := range out.Services {
		sv := &out.Services[i]
		if sv.Version != "" && NormalizeVersion(sv.Version) != want {
			sv.VersionMismatch, out.VersionMismatch = true, true
		}
	}
	return out
}

// NormalizeVersion drops a leading "v" (release builds say "v1.2.3", the
// installer's .env says "1.2.3").
func NormalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// probe asks one service's /healthz and /readyz at the same time.
func (c *Checker) probe(ctx context.Context, p Peer) Service {
	sv := Service{Name: p.Name, Checks: map[string]string{}}
	if p.URL == "" {
		sv.Status = StatusNotConfigured
		if p.Optional {
			sv.Status = StatusNotInstalled
		}
		return sv
	}
	var (
		live, ready       answer
		liveErr, readyErr error
		wg                sync.WaitGroup
	)
	start := time.Now()
	wg.Add(2)
	go func() { defer wg.Done(); live, liveErr = c.get(ctx, p.URL+"/healthz") }()
	go func() { defer wg.Done(); ready, readyErr = c.get(ctx, p.URL+"/readyz") }()
	wg.Wait()
	sv.LatencyMS = time.Since(start).Milliseconds()
	switch {
	case liveErr != nil && p.Optional && unresolved(liveErr):
		sv.Status, sv.LatencyMS = StatusNotInstalled, 0
		return sv
	case liveErr != nil:
		sv.Status, sv.Detail = StatusDown, failure(liveErr)
		return sv
	case live.code != http.StatusOK:
		sv.Status, sv.Detail = StatusDown, "answers HTTP "+strconv.Itoa(live.code)
		return sv
	}
	sv.Version = live.body.Version
	for k, v := range ready.body.Checks {
		sv.Checks[k] = v
	}
	switch {
	case readyErr != nil:
		sv.Status, sv.Detail = StatusNotReady, "readiness: "+failure(readyErr)
	case ready.code != http.StatusOK:
		sv.Status = StatusNotReady
		var failed []string
		for k, v := range ready.body.Checks {
			if v != "ok" {
				failed = append(failed, k)
			}
		}
		sort.Strings(failed)
		sv.Detail = "not ready"
		if len(failed) > 0 {
			sv.Detail = "failing: " + strings.Join(failed, ", ")
		}
	default:
		sv.Status = StatusOK
	}
	return sv
}

// answer is a health endpoint's reply (both endpoints share the shape).
type answer struct {
	code int
	body struct {
		Status  string            `json:"status"`
		Version string            `json:"version"`
		Checks  map[string]string `json:"checks"`
	}
}

func (c *Checker) get(ctx context.Context, url string) (answer, error) {
	var a answer
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return a, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return a, err
	}
	defer resp.Body.Close()
	a.code = resp.StatusCode
	// A body that is not the expected JSON leaves the fields empty: the
	// status code still says alive or not.
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&a.body)
	return a, nil
}

// unresolved: the name could not be looked up at all. Any lookup failure
// counts, not only "no such host": on Docker's internal-only networks the
// embedded DNS answers SERVFAIL (a temporary error) for a service that does
// not exist, since it has no upstream to forward the name to.
func unresolved(err error) bool {
	var dns *net.DNSError
	return errors.As(err, &dns)
}

// failure names why a request failed, without addresses.
func failure(err error) string {
	var dns *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "does not answer in time"
	case errors.As(err, &dns):
		return "unknown host"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return "does not answer"
}
