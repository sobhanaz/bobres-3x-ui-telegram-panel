package collect

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

var now0 = time.Unix(1_760_000_000, 0)

// labServices are the services the lab install is meant to run.
var labServices = []string{"caddy", "postgres", "redis", "core", "payments", "provisioner", "bot", "tls-agent", "backup"}

// framed wraps a log in the engine's stream framing (stdout).
func framed(lines ...string) []byte {
	var out bytes.Buffer
	for _, l := range lines {
		l += "\n"
		var h [8]byte
		h[0] = 1
		binary.BigEndian.PutUint32(h[4:], uint32(len(l))) //nolint:gosec // a short test line
		out.Write(h[:])
		out.WriteString(l)
	}
	return out.Bytes()
}

// engine plays the Docker Engine with what the lab server answered.
type engine struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	paths   []string
	queries map[string][]string
	log     []byte
	// failInspect makes these container ids' inspect answer 500.
	failInspect map[string]bool
	dfSleep     time.Duration
}

func newEngine(t *testing.T) *engine {
	t.Helper()
	e := &engine{t: t, queries: map[string][]string{}, failInspect: map[string]bool{}}
	mux := http.NewServeMux()
	serve := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			e.record(r)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixture(t, name))
		}
	}
	mux.HandleFunc("GET /version", serve("docker/version.json"))
	mux.HandleFunc("GET /info", serve("docker/info.json"))
	mux.HandleFunc("GET /containers/json", serve("docker/list.json"))
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		e.record(r)
		id := r.PathValue("id")
		if e.failInspect[id[:12]] {
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(fixture(t, "docker/inspect-"+id[:12]+".json"))
	})
	mux.HandleFunc("GET /containers/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		e.record(r)
		_, _ = w.Write(fixture(t, "docker/stats-"+r.PathValue("id")[:12]+".json"))
	})
	mux.HandleFunc("GET /containers/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		e.record(r)
		_, _ = w.Write(e.log)
	})
	mux.HandleFunc("GET /system/df", func(w http.ResponseWriter, r *http.Request) {
		e.record(r)
		time.Sleep(e.dfSleep)
		_, _ = w.Write(fixture(t, "docker/df.json"))
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *engine) record(r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paths = append(e.paths, r.URL.Path)
	e.queries[r.URL.Path] = append(e.queries[r.URL.Path], r.URL.RawQuery)
}

func (e *engine) client() *DockerClient { return NewDockerHTTP(e.srv.URL, e.srv.Client()) }

func (e *engine) called(path string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, p := range e.paths {
		if p == path {
			n++
		}
	}
	return n
}

func byName(cs []snapshot.Container) map[string]snapshot.Container {
	m := map[string]snapshot.Container{}
	for _, c := range cs {
		m[c.Name] = c
	}
	return m
}

func TestDockerSectionFromARealEngine(t *testing.T) {
	e := newEngine(t)
	e.log = framed(
		`{"level":"info","ts":1759999000.5,"logger":"tls","msg":"serving initial configuration"}`,
		`{"level":"error","ts":1759999900.25,"logger":"http.log.error","msg":"dial tcp 172.19.0.5:8080: connect: connection refused","request":{"uri":"/sub/SECRETTOKENSECRETTOKEN"}}`,
		`not json at all`,
		`{"level":"error","ts":1759999950,"logger":"tls","msg":"obtaining certificate","error":"acme: error 429 for admin@example.com"}`,
	)
	res := collectDocker(context.Background(), dockerOptions{api: e.client(), expected: labServices, now: now0, dfEvery: 15 * time.Minute,
		logWindow: time.Hour, gatewayService: "caddy"})
	d := res.section
	if !d.OK || d.Error != "" || d.Version != "29.9.0" || d.API != "1.56" || res.rootDir != "/var/lib/docker" || len(res.errs) != 0 {
		t.Fatalf("section: %+v root %q errs %v", d, res.rootDir, res.errs)
	}
	if len(d.Containers) != 9 || d.Truncated {
		t.Fatalf("%d containers", len(d.Containers))
	}
	var names []string
	for _, c := range d.Containers {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, " "); got != "bobres-backup-1 bobres-bot-1 bobres-caddy-1 bobres-core-1 bobres-payments-1 bobres-postgres-1 bobres-provisioner-1 bobres-redis-1 bobres-tls-agent-1" {
		t.Errorf("order: %s", got)
	}
	cs := byName(d.Containers)

	core := cs["bobres-core-1"]
	if core.ID != "995876ae1421" || core.Project != "bobres" || core.Service != "core" || core.Image != "ghcr.io/sobhanaz/bobres-core:0.2.0" ||
		core.State != snapshot.StateRunning || core.Health != snapshot.HealthHealthy || core.Restarts != 1 || core.StartedAt == 0 || core.ExitCode != nil {
		t.Errorf("core: %+v", core)
	}
	// 28184576 bytes used less 16384 of inactive file cache; two interfaces added up.
	if core.MemBytes != 28184576-16384 || core.MemLimit != 536870912 || core.NetRx != 42784286+58286 || core.NetTx != 56569041+568 ||
		core.BlkRead != 8192 || core.BlkWrite != 4096 || core.PIDs != 23 || core.CPUNS != 428007133000 || core.CPUPct != nil {
		t.Errorf("core figures: %+v", core)
	}

	bot := cs["bobres-bot-1"]
	if bot.State != snapshot.StateRestarting || bot.Restarts != 989 || bot.Health != snapshot.HealthUnhealthy || bot.ExitCode == nil || *bot.ExitCode != 1 ||
		bot.MemBytes != 0 || bot.CPUNS != 0 {
		t.Errorf("bot: %+v", bot)
	}
	caddy := cs["bobres-caddy-1"]
	if caddy.State != snapshot.StateExited || caddy.Health != snapshot.HealthNone || caddy.ExitCode == nil || *caddy.ExitCode != 0 || caddy.Image != "caddy:2.11.7-alpine" {
		t.Errorf("caddy: %+v", caddy)
	}
	if tls := cs["bobres-tls-agent-1"]; tls.State != snapshot.StateRunning || tls.Health != snapshot.HealthUnhealthy {
		t.Errorf("tls-agent: %+v", tls)
	}
	if pg := cs["bobres-postgres-1"]; pg.Image != "postgres:16-alpine" || pg.Health != snapshot.HealthHealthy {
		t.Errorf("postgres: %+v", pg)
	}

	// The engine has nine containers; the bot is restarting and Caddy has exited.
	if d.Compose == nil || strings.Join(d.Compose.Missing, ",") != "caddy,bot" || len(d.Compose.Expected) != 9 {
		t.Errorf("compose: %+v", d.Compose)
	}
	if df := d.DF; df == nil || df.ImagesBytes != 1754177977 || df.ImagesReclaimable != 1038814985 || df.ContainersBytes != 114688 ||
		df.VolumesBytes != 69687759 || df.BuildCacheBytes != 6844421213 || df.CollectedAt != now0.Unix() || df.AttemptedAt != now0.Unix() {
		t.Errorf("df: %+v", df)
	}

	// Stats are asked the cheap way, for the containers that run.
	if got := e.called("/containers/995876ae142103a51180ea19dec86888b9879849c027944b0d8f0f208d32f96e/stats"); got != 1 {
		t.Errorf("core stats asked %d times", got)
	}
	for path, qs := range e.queries {
		if strings.HasSuffix(path, "/stats") {
			for _, q := range qs {
				if q != "one-shot=true&stream=false" {
					t.Errorf("stats query %q", q)
				}
			}
		}
	}
	for _, id := range []string{"0a3d06e4cd8b", "cdb81141d269"} { // restarting and exited: no stats
		for p := range e.queries {
			if strings.Contains(p, id) && strings.HasSuffix(p, "/stats") {
				t.Errorf("stats asked of a container that does not run: %s", p)
			}
		}
	}

	// Caddy's own log: its two error lines, scrubbed.
	g := res.gateway
	if g == nil || g.Name != "caddy" || g.Errors != 2 || len(g.Lines) != 2 {
		t.Fatalf("gateway: %+v", g)
	}
	if g.Lines[0].At != 1759999900 || !strings.Contains(g.Lines[0].Msg, "connection refused") || strings.Contains(g.Lines[0].Msg, "172.19") ||
		strings.Contains(g.Lines[1].Msg, "admin@") || !strings.Contains(g.Lines[1].Msg, "obtaining certificate") {
		t.Errorf("gateway lines: %+v", g.Lines)
	}
	for _, l := range g.Lines {
		if strings.Contains(l.Msg, "SECRETTOKEN") {
			t.Errorf("the request in a log line leaked: %q", l.Msg)
		}
	}
}

func TestContainerCPUShareComesFromThePreviousSnapshot(t *testing.T) {
	e := newEngine(t)
	prev := &snapshot.Snapshot{GeneratedMS: now0.Add(-30 * time.Second).UnixMilli(), Docker: &snapshot.Docker{Containers: []snapshot.Container{
		// Used 15 s of CPU in the 30 s since: half a core.
		{ID: "995876ae1421", State: snapshot.StateRunning, CPUNS: 428007133000 - 15_000_000_000},
		// The same id but no CPU figure before (it was not running): no share.
		{ID: "e913e5ad7708", State: snapshot.StateExited},
		// A counter that went down (the container restarted).
		{ID: "92cb9c40664f", State: snapshot.StateRunning, CPUNS: 1 << 60},
	}}}
	res := collectDocker(context.Background(), dockerOptions{api: e.client(), prev: prev, now: now0, dfEvery: time.Hour})
	cs := byName(res.section.Containers)
	if p := cs["bobres-core-1"].CPUPct; p == nil || *p < 49.99 || *p > 50.01 {
		t.Errorf("core cpu: %v", p)
	}
	if cs["bobres-payments-1"].CPUPct != nil || cs["bobres-redis-1"].CPUPct != nil {
		t.Errorf("a share without a usable earlier reading: %v %v", cs["bobres-payments-1"].CPUPct, cs["bobres-redis-1"].CPUPct)
	}
	// Two snapshots too far apart say nothing about now.
	prev.GeneratedMS = now0.Add(-time.Hour).UnixMilli()
	res = collectDocker(context.Background(), dockerOptions{api: e.client(), prev: prev, now: now0, dfEvery: time.Hour})
	if byName(res.section.Containers)["bobres-core-1"].CPUPct != nil {
		t.Error("a share over an hour")
	}
}

func TestDockerDFIsAskedNowAndThenOnly(t *testing.T) {
	e := newEngine(t)
	carried := &snapshot.DF{ImagesBytes: 7, CollectedAt: now0.Add(-5 * time.Minute).Unix(), AttemptedAt: now0.Add(-5 * time.Minute).Unix()}
	prev := &snapshot.Snapshot{GeneratedMS: now0.Add(-30 * time.Second).UnixMilli(), Docker: &snapshot.Docker{DF: carried}}
	res := collectDocker(context.Background(), dockerOptions{api: e.client(), prev: prev, now: now0, dfEvery: 15 * time.Minute})
	if e.called("/system/df") != 0 || res.section.DF == nil || *res.section.DF != *carried {
		t.Fatalf("a recent df was asked again: %d calls, %+v", e.called("/system/df"), res.section.DF)
	}
	carried.AttemptedAt = now0.Add(-16 * time.Minute).Unix()
	res = collectDocker(context.Background(), dockerOptions{api: e.client(), prev: prev, now: now0, dfEvery: 15 * time.Minute})
	if e.called("/system/df") != 1 || res.section.DF.ImagesBytes != 1754177977 {
		t.Fatalf("an old df was kept: %d calls, %+v", e.called("/system/df"), res.section.DF)
	}
	// A df that takes too long keeps the old figures and is not retried at once.
	slow := newEngine(t)
	slow.dfSleep = 300 * time.Millisecond
	old := &snapshot.DF{ImagesBytes: 7, CollectedAt: now0.Add(-time.Hour).Unix(), AttemptedAt: now0.Add(-time.Hour).Unix()}
	prev = &snapshot.Snapshot{GeneratedMS: now0.Add(-30 * time.Second).UnixMilli(), Docker: &snapshot.Docker{DF: old}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res = collectDocker(ctx, dockerOptions{api: slow.client(), prev: prev, now: now0, dfEvery: 15 * time.Minute})
	df := res.section.DF
	if df == nil || df.ImagesBytes != 7 || df.CollectedAt != old.CollectedAt || df.AttemptedAt != now0.Unix() {
		t.Errorf("df after a failed ask: %+v", df)
	}
}

func TestOlderEnginesDFWithoutSummaries(t *testing.T) {
	raw := `{"LayersSize": 1000, "Containers": [{"SizeRw": 5}, {"SizeRw": -1}], "Volumes": [{"UsageData": {"Size": 70}}, {"UsageData": {"Size": -1}}, {"UsageData": null}],
		"BuildCache": [{"Size": 11}, {"Size": 12}], "Images": [{"Size": 99999}]}`
	var df EngineDF
	if err := json.Unmarshal([]byte(raw), &df); err != nil {
		t.Fatal(err)
	}
	got := df.summary(now0)
	if got.ImagesBytes != 1000 || got.ContainersBytes != 5 || got.VolumesBytes != 70 || got.BuildCacheBytes != 23 || got.ImagesReclaimable != 0 {
		t.Errorf("%+v", got)
	}
}

func TestHealthComesFromTheListWhenInspectIsMissing(t *testing.T) {
	for _, c := range []struct{ health, status, want string }{
		{"", "Up 2 hours (healthy)", snapshot.HealthHealthy},
		{"", "Up 2 hours (unhealthy)", snapshot.HealthUnhealthy},
		{"", "Up 3 seconds (health: starting)", snapshot.HealthStarting},
		{"", "Up 2 hours", snapshot.HealthNone},
		{"unhealthy", "Up 2 hours", snapshot.HealthUnhealthy},
		{"weird", "Up 2 hours (healthy)", snapshot.HealthHealthy},
	} {
		if got := healthFrom(c.health, c.status); got != c.want {
			t.Errorf("healthFrom(%q, %q) = %q, want %q", c.health, c.status, got, c.want)
		}
	}
}

func TestImageReferences(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io/x/y:1.2.3@sha256:abc":                 "ghcr.io/x/y:1.2.3",
		"caddy:2.11.7-alpine":                          "caddy:2.11.7-alpine",
		"sha256:3384685ce3b4c547f87f352237e845a8a0b6d": "sha256:3384685ce3b4",
		"redis@sha256:abc":                             "redis",
		"":                                             "",
	} {
		if got := imageRef(in); got != want {
			t.Errorf("imageRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnInspectThatFailsLeavesTheRestOfTheContainer(t *testing.T) {
	e := newEngine(t)
	e.failInspect["995876ae1421"] = true
	res := collectDocker(context.Background(), dockerOptions{api: e.client(), now: now0, dfEvery: time.Hour})
	core := byName(res.section.Containers)["bobres-core-1"]
	if core.State != snapshot.StateRunning || core.MemBytes == 0 || core.Restarts != 0 || core.StartedAt != 0 {
		t.Errorf("core without inspect: %+v", core)
	}
	if len(res.errs) != 1 || !strings.Contains(res.errs[0], "1 containers could not be read") {
		t.Errorf("errors: %v", res.errs)
	}
}

// Nothing of a container's environment, command, mounts, labels or addresses
// reaches the snapshot, even when the engine hands it over.
func TestNothingSecretLeavesThroughTheEngine(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"Version":"29.9.0","ApiVersion":"1.56","Secret":"engine-secret-1"}`)
	})
	mux.HandleFunc("GET /info", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"DockerRootDir":"/var/lib/docker","RegistryConfig":{"Secret":"registry-secret-2"}}`)
	})
	mux.HandleFunc("GET /containers/json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `[{"Id":%q,"Names":["/bobres-core-1"],"Image":"ghcr.io/x/core:1@sha256:%s","State":"running","Status":"Up",
			"Command":"/app --token=command-secret-3","Labels":{"com.docker.compose.project":"bobres","com.docker.compose.service":"core",
			"com.docker.compose.project.environment_file":"/opt/bobres/.env","secret":"label-secret-4"},
			"Mounts":[{"Source":"/home/owner/host-path-5","Destination":"/data"}],
			"NetworkSettings":{"Networks":{"b":{"IPAddress":"10.9.8.7","MacAddress":"02:42:ac:11:00:02"}}}}]`, id, strings.Repeat("ab", 32))
	})
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"RestartCount":2,"State":{"Status":"running","Running":true,"StartedAt":"2026-10-20T09:00:00Z","Health":{"Status":"healthy","Log":[{"Output":"health-output-secret-6"}]}},
			"Config":{"Env":["BOBRES_TOKEN_CORE=env-secret-7"],"Cmd":["--password=cmd-secret-8"],"Labels":{"x":"y"}},
			"HostConfig":{"Binds":["/root/.ssh:/ssh"]},"Mounts":[{"Source":"/root/.ssh"}]}`)
	})
	mux.HandleFunc("GET /containers/{id}/stats", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"cpu_stats":{"cpu_usage":{"total_usage":5}},"memory_stats":{"usage":100,"limit":200,"stats":{"inactive_file":10}},
			"networks":{"eth0":{"rx_bytes":1,"tx_bytes":2}},"name":"/stats-name-secret-9"}`)
	})
	mux.HandleFunc("GET /system/df", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"LayersSize":10,"BuildCache":[{"Size":3,"Description":"RUN --mount=type=secret,id=tok build-arg-secret-10"}],
			"Volumes":[{"Name":"vol-secret-11","UsageData":{"Size":4}}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res := collectDocker(context.Background(), dockerOptions{api: NewDockerHTTP(srv.URL, srv.Client()), now: now0, dfEvery: time.Hour, expected: []string{"core"}})
	b, _ := json.Marshal(res.section)
	for i := 1; i <= 11; i++ {
		if s := fmt.Sprintf("-secret-%d", i); strings.Contains(string(b), s) || strings.Contains(string(b), fmt.Sprintf("secret-%d", i)) {
			t.Errorf("secret %d reached the snapshot: %s", i, b)
		}
	}
	for _, leak := range []string{"/home/owner", "/root/.ssh", "10.9.8.7", "02:42", "environment_file", "BOBRES_TOKEN", "--password", ".env"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("%q reached the snapshot: %s", leak, b)
		}
	}
	c := res.section.Containers[0]
	if c.Service != "core" || c.Restarts != 2 || c.MemBytes != 90 || c.NetTx != 2 || strings.Contains(c.Image, "@") {
		t.Errorf("the container itself: %+v", c)
	}
	if err := (&snapshot.Snapshot{Schema: 1, GeneratedAt: now0.Unix(), GeneratedMS: now0.UnixMilli(), Docker: res.section}).Normalize(now0); err != nil {
		t.Error(err)
	}
}

func TestEngineThatIsDownOrHostile(t *testing.T) {
	// Nothing listens on the socket.
	sock := filepath.Join(shortDir(t), "none.sock")
	res := collectDocker(context.Background(), dockerOptions{api: NewDockerSocket(sock), now: now0})
	if res.section.OK || !strings.Contains(res.section.Error, "no docker socket") || len(res.errs) != 1 {
		t.Errorf("no socket: %+v %v", res.section, res.errs)
	}

	// 5000 containers with hostile names: the section holds the cap, the install's first.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"Version":"29","ApiVersion":"1.56"}`) })
	mux.HandleFunc("GET /info", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{}`) })
	mux.HandleFunc("GET /containers/json", func(w http.ResponseWriter, _ *http.Request) {
		var l []map[string]any
		for i := 0; i < 5000; i++ {
			c := map[string]any{"Id": fmt.Sprintf("%064x", i+1), "Names": []string{fmt.Sprintf("/other-%04d", i)}, "State": "exited", "Image": "x"}
			if i == 4999 {
				c["Names"] = []string{"/bobres-last-1"}
				c["Labels"] = map[string]string{"com.docker.compose.project": "bobres", "com.docker.compose.service": "last"}
			}
			l = append(l, c)
		}
		_ = json.NewEncoder(w).Encode(l)
	})
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"State":{"Status":"exited"}}`) })
	mux.HandleFunc("GET /system/df", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{}`) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res = collectDocker(context.Background(), dockerOptions{api: NewDockerHTTP(srv.URL, srv.Client()), now: now0, dfEvery: time.Hour})
	d := res.section
	if !d.OK || len(d.Containers) != snapshot.MaxContainers || !d.Truncated {
		t.Errorf("hostile list: ok %v, %d containers, truncated %v", d.OK, len(d.Containers), d.Truncated)
	}
	if d.Containers[0].Name != "bobres-last-1" || d.Containers[0].Service != "last" {
		t.Errorf("the install's own container was cut: %+v", d.Containers[0])
	}

	// A body longer than the limit is refused, not read to its end.
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("["), 1000))
	}))
	defer big.Close()
	c := NewDockerHTTP(big.URL, big.Client())
	if _, err := c.get(context.Background(), "/x", nil, 100); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, err := c.Inspect(context.Background(), "../../etc/passwd"); err == nil {
		t.Error("a bad id went into a URL")
	}
	if _, err := c.Stats(context.Background(), "ZZZ"); err == nil {
		t.Error("a bad id went into a URL")
	}
	if _, err := c.Logs(context.Background(), "x y z", now0, 10); err == nil {
		t.Error("a bad id went into a URL")
	}
}

func TestEngineAnsweringErrorsOrNonsense(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"500":     func(w http.ResponseWriter, _ *http.Request) { http.Error(w, `{"message":"no"}`, 500) },
		"html":    func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<html>proxy</html>") },
		"slow":    func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
		"emptied": func(w http.ResponseWriter, _ *http.Request) {},
	} {
		srv := httptest.NewServer(h)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		res := collectDocker(ctx, dockerOptions{api: NewDockerHTTP(srv.URL, srv.Client()), now: now0})
		cancel()
		srv.Close()
		if res.section.OK || res.section.Error == "" || len(res.errs) == 0 || len(res.section.Containers) != 0 {
			t.Errorf("%s: %+v %v", name, res.section, res.errs)
		}
	}
}

// shortDir is a directory with a short path (a unix socket's path is limited).
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "mon")
	if err != nil {
		t.Skip("no /tmp")
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestUnixSocketTransport(t *testing.T) {
	sock := filepath.Join(shortDir(t), "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets: %v", err)
	}
	var hits atomic.Int32
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"Version":"29.9.0","ApiVersion":"1.56"}`)
	})}
	go func() { _ = srv.Serve(l) }()
	defer srv.Close()
	v, err := NewDockerSocket(sock).Version(context.Background())
	if err != nil || v.Version != "29.9.0" || hits.Load() != 1 {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := NewDockerSocket(sock).Info(context.Background()); err == nil {
		t.Error("a 404 was accepted")
	}
}

func TestSocketPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	sock := filepath.Join(shortDir(t), "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets: %v", err)
	}
	defer l.Close()
	if err := os.Chmod(sock, 0); err != nil {
		t.Fatal(err)
	}
	_, err = NewDockerSocket(sock).Version(context.Background())
	if err == nil || whyDown(err) != "permission denied on the docker socket" {
		t.Errorf("%v -> %q", err, whyDown(err))
	}
}

func TestSocketFromEnv(t *testing.T) {
	for in, want := range map[string]string{
		"": DefaultSocket, "unix:///run/user/1000/docker.sock": "/run/user/1000/docker.sock",
		"tcp://10.0.0.5:2375": DefaultSocket, "ssh://root@host": DefaultSocket, "unix://relative.sock": DefaultSocket,
	} {
		if got := SocketFromEnv(in); got != want {
			t.Errorf("SocketFromEnv(%q) = %q, want %q", in, got, want)
		}
	}
	if whyDown(context.DeadlineExceeded) != "docker did not answer in time" || whyDown(errors.New("x")) != "docker did not answer" {
		t.Error("whyDown")
	}
}

func TestLogFraming(t *testing.T) {
	log := framed("line one", "line two")
	if got := string(demux(log)); got != "line one\nline two\n" {
		t.Errorf("%q", got)
	}
	// A container with a terminal has no framing.
	if got := string(demux([]byte("plain text\nsecond\n"))); got != "plain text\nsecond\n" {
		t.Errorf("%q", got)
	}
	// A frame that claims more than there is gives what there is.
	cut := append([]byte{1, 0, 0, 0, 0, 0, 1, 0}, []byte("short")...)
	if got := string(demux(cut)); got != "short" {
		t.Errorf("%q", got)
	}
	if got := demux(nil); len(got) != 0 {
		t.Errorf("%q", got)
	}
	count, capped, lines := errorLines([]byte(strings.Repeat(`{"level":"error","msg":"x"}`+"\n", 400)), now0)
	if count != 400 || !capped || len(lines) != snapshot.MaxJournalLines {
		t.Errorf("many: %d %v %d", count, capped, len(lines))
	}
}

func TestNoTwoSourcesWaitForEachOther(t *testing.T) {
	// Docker and the host sources are asked at the same time, so a slow one
	// does not delay the others.
	var wg sync.WaitGroup
	wg.Add(2)
	meet := func() {
		wg.Done()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("the sources were asked one after the other")
		}
	}
	c := baseCollector(t)
	c.Docker = &rendezvousDocker{meet: meet}
	c.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "systemctl" {
			meet()
		}
		return nil, errors.New("no")
	}
	s := c.Collect(context.Background())
	if s.Docker == nil || s.Host == nil {
		t.Fatalf("%+v", s)
	}
}

// rendezvousDocker blocks its first call until the other source has called too.
type rendezvousDocker struct{ meet func() }

func (r *rendezvousDocker) Version(context.Context) (EngineVersion, error) {
	r.meet()
	return EngineVersion{}, errors.New("stop here")
}
func (r *rendezvousDocker) Info(context.Context) (EngineInfo, error) {
	return EngineInfo{}, errors.New("no")
}
func (r *rendezvousDocker) List(context.Context) ([]EngineContainer, error) {
	return nil, errors.New("no")
}
func (r *rendezvousDocker) Inspect(context.Context, string) (EngineInspect, error) {
	return EngineInspect{}, errors.New("no")
}
func (r *rendezvousDocker) Stats(context.Context, string) (EngineStats, error) {
	return EngineStats{}, errors.New("no")
}
func (r *rendezvousDocker) DF(context.Context) (EngineDF, error) { return EngineDF{}, errors.New("no") }
func (r *rendezvousDocker) Logs(context.Context, string, time.Time, int) ([]byte, error) {
	return nil, errors.New("no")
}
