package collect

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// Docker is read through the Engine API on its unix socket: one HTTP request
// per fact, no `docker` process to start. The calls are the engine's cheap
// ones: stats come with one-shot=true (the raw cumulative counters at once, not
// the second-long wait for the engine's own rate), and `system df`, which
// walks every layer and takes seconds, is asked only now and then.

// Bounds on what is read from the engine.
const (
	maxList    = 4 << 20
	maxInspect = 1 << 20
	maxStats   = 256 << 10
	maxDF      = 16 << 20
	maxInfo    = 512 << 10
	maxLogs    = 512 << 10
)

// DockerAPI is what the collector asks of the engine.
type DockerAPI interface {
	Version(ctx context.Context) (EngineVersion, error)
	Info(ctx context.Context) (EngineInfo, error)
	List(ctx context.Context) ([]EngineContainer, error)
	Inspect(ctx context.Context, id string) (EngineInspect, error)
	Stats(ctx context.Context, id string) (EngineStats, error)
	DF(ctx context.Context) (EngineDF, error)
	// Logs is the container's recent log (stdout and stderr) as the engine
	// frames it, since the given time, at most tail lines.
	Logs(ctx context.Context, id string, since time.Time, tail int) ([]byte, error)
}

// EngineVersion is /version.
type EngineVersion struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
}

// EngineInfo is the part of /info the collector reads.
type EngineInfo struct {
	DockerRootDir string `json:"DockerRootDir"`
}

// EngineContainer is one entry of /containers/json. Of its labels only the
// Compose project and service are kept.
type EngineContainer struct {
	ID      string
	Names   []string
	Image   string
	State   string
	Status  string
	Health  string
	Project string
	Service string
}

// UnmarshalJSON keeps what the collector needs and drops the rest (the
// labels, ports, mounts and addresses an entry carries are never held on to).
func (c *EngineContainer) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Image  string            `json:"Image"`
		State  string            `json:"State"`
		Status string            `json:"Status"`
		Labels map[string]string `json:"Labels"`
		Health *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*c = EngineContainer{ID: raw.ID, Names: raw.Names, Image: raw.Image, State: raw.State, Status: raw.Status,
		Project: raw.Labels["com.docker.compose.project"], Service: raw.Labels["com.docker.compose.service"]}
	if raw.Health != nil {
		c.Health = raw.Health.Status
	}
	return nil
}

// EngineInspect is the part of /containers/{id}/json the collector reads: not
// the environment, the command, the mounts or the addresses.
type EngineInspect struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		Restarting bool   `json:"Restarting"`
		OOMKilled  bool   `json:"OOMKilled"`
		Dead       bool   `json:"Dead"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

// EngineStats is one reading of /containers/{id}/stats?one-shot=true.
type EngineStats struct {
	CPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
	} `json:"cpu_stats"`
	Memory struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			InactiveFile      uint64 `json:"inactive_file"`
			TotalInactiveFile uint64 `json:"total_inactive_file"`
			Cache             uint64 `json:"cache"`
		} `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		Rx uint64 `json:"rx_bytes"`
		Tx uint64 `json:"tx_bytes"`
	} `json:"networks"`
	Blkio struct {
		Bytes []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PIDs struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

// usage is the memory a container uses the way `docker stats` shows it: the
// cgroup's usage less the file cache the kernel can take back.
func (s EngineStats) usage() uint64 {
	cache := s.Memory.Stats.InactiveFile // cgroup v2
	if cache == 0 {
		cache = s.Memory.Stats.TotalInactiveFile // cgroup v1
	}
	if cache == 0 {
		cache = s.Memory.Stats.Cache
	}
	if cache >= s.Memory.Usage {
		return s.Memory.Usage
	}
	return s.Memory.Usage - cache
}

// blk is the bytes read and written by the container's block devices.
func (s EngineStats) blk() (read, write uint64) {
	for _, b := range s.Blkio.Bytes {
		switch strings.ToLower(b.Op) {
		case "read":
			read += b.Value
		case "write":
			write += b.Value
		}
	}
	return read, write
}

// usageTotals is the summary an engine of API 1.52 or later gives of one kind
// of disk use.
type usageTotals struct {
	TotalSize   int64 `json:"TotalSize"`
	Reclaimable int64 `json:"Reclaimable"`
}

// EngineDF is /system/df. The per-item lists carry names, labels and build
// steps; only their sizes are read.
type EngineDF struct {
	LayersSize int64 `json:"LayersSize"`
	Containers []struct {
		SizeRw int64 `json:"SizeRw"`
	} `json:"Containers"`
	Volumes []struct {
		UsageData *struct {
			Size int64 `json:"Size"`
		} `json:"UsageData"`
	} `json:"Volumes"`
	BuildCache []struct {
		Size int64 `json:"Size"`
	} `json:"BuildCache"`
	ImageUsage      *usageTotals `json:"ImageUsage"`
	ContainerUsage  *usageTotals `json:"ContainerUsage"`
	VolumeUsage     *usageTotals `json:"VolumeUsage"`
	BuildCacheUsage *usageTotals `json:"BuildCacheUsage"`
}

func (d EngineDF) summary(now time.Time) *snapshot.DF {
	pos := func(v int64) uint64 {
		if v < 0 {
			return 0
		}
		return uint64(v)
	}
	out := &snapshot.DF{CollectedAt: now.Unix(), AttemptedAt: now.Unix()}
	switch {
	case d.ImageUsage != nil:
		out.ImagesBytes, out.ImagesReclaimable = pos(d.ImageUsage.TotalSize), pos(d.ImageUsage.Reclaimable)
	default:
		out.ImagesBytes = pos(d.LayersSize)
	}
	if d.ContainerUsage != nil {
		out.ContainersBytes = pos(d.ContainerUsage.TotalSize)
	} else {
		for _, c := range d.Containers {
			out.ContainersBytes += pos(c.SizeRw)
		}
	}
	if d.VolumeUsage != nil {
		out.VolumesBytes = pos(d.VolumeUsage.TotalSize)
	} else {
		for _, v := range d.Volumes {
			if v.UsageData != nil {
				out.VolumesBytes += pos(v.UsageData.Size)
			}
		}
	}
	if d.BuildCacheUsage != nil {
		out.BuildCacheBytes = pos(d.BuildCacheUsage.TotalSize)
	} else {
		for _, b := range d.BuildCache {
			out.BuildCacheBytes += pos(b.Size)
		}
	}
	return out
}

// DockerClient talks to the engine.
type DockerClient struct {
	base string
	hc   *http.Client
}

// DefaultSocket is the engine's socket.
const DefaultSocket = "/var/run/docker.sock"

// SocketFromEnv is the unix socket named by DOCKER_HOST (unix://path), else
// DefaultSocket. Anything else (tcp://, ssh://) is not used: this helper
// runs as root and talks to a local engine only.
func SocketFromEnv(host string) string {
	if p, ok := strings.CutPrefix(strings.TrimSpace(host), "unix://"); ok && strings.HasPrefix(p, "/") {
		return p
	}
	return DefaultSocket
}

// NewDockerSocket returns a client for the engine on a unix socket.
func NewDockerSocket(path string) *DockerClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
		MaxIdleConns: 4, MaxConnsPerHost: 8, IdleConnTimeout: 5 * time.Second, DisableCompression: true,
	}
	return &DockerClient{base: "http://docker", hc: &http.Client{Transport: tr}}
}

// NewDockerHTTP returns a client for an engine reached at base (tests).
func NewDockerHTTP(base string, hc *http.Client) *DockerClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &DockerClient{base: strings.TrimRight(base, "/"), hc: hc}
}

// get reads at most limit bytes of the answer to a GET.
func (c *DockerClient) get(ctx context.Context, path string, q url.Values, limit int64) ([]byte, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "bobres-monitor")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker: %s answered %d", path, resp.StatusCode)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("docker: %s: %w", path, ErrTooLarge)
	}
	return body, nil
}

func getJSON[T any](ctx context.Context, c *DockerClient, path string, q url.Values, limit int64) (T, error) {
	var v T
	b, err := c.get(ctx, path, q, limit)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("docker: %s: %w", path, err)
	}
	return v, nil
}

// Version implements DockerAPI.
func (c *DockerClient) Version(ctx context.Context) (EngineVersion, error) {
	return getJSON[EngineVersion](ctx, c, "/version", nil, maxInfo)
}

// Info implements DockerAPI.
func (c *DockerClient) Info(ctx context.Context) (EngineInfo, error) {
	return getJSON[EngineInfo](ctx, c, "/info", nil, maxInfo)
}

// List implements DockerAPI (every container, stopped ones too).
func (c *DockerClient) List(ctx context.Context) ([]EngineContainer, error) {
	return getJSON[[]EngineContainer](ctx, c, "/containers/json", url.Values{"all": {"true"}}, maxList)
}

// Inspect implements DockerAPI. The id comes from the engine's own list; it
// is checked anyway, since it goes into a URL.
func (c *DockerClient) Inspect(ctx context.Context, id string) (EngineInspect, error) {
	if !validID(id) {
		return EngineInspect{}, errors.New("docker: bad container id")
	}
	return getJSON[EngineInspect](ctx, c, "/containers/"+id+"/json", nil, maxInspect)
}

// Stats implements DockerAPI.
func (c *DockerClient) Stats(ctx context.Context, id string) (EngineStats, error) {
	if !validID(id) {
		return EngineStats{}, errors.New("docker: bad container id")
	}
	return getJSON[EngineStats](ctx, c, "/containers/"+id+"/stats", url.Values{"stream": {"false"}, "one-shot": {"true"}}, maxStats)
}

// DF implements DockerAPI.
func (c *DockerClient) DF(ctx context.Context) (EngineDF, error) {
	return getJSON[EngineDF](ctx, c, "/system/df", nil, maxDF)
}

// Logs implements DockerAPI.
func (c *DockerClient) Logs(ctx context.Context, id string, since time.Time, tail int) ([]byte, error) {
	if !validID(id) {
		return nil, errors.New("docker: bad container id")
	}
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "since": {strconv.FormatInt(since.Unix(), 10)}, "tail": {strconv.Itoa(tail)}}
	return c.get(ctx, "/containers/"+id+"/logs", q, maxLogs)
}

// validID: the engine's ids are hex.
func validID(id string) bool {
	if len(id) < 12 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// whyDown says in a few words why the engine did not answer.
func whyDown(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "docker did not answer in time"
	case errors.Is(err, syscall.EACCES), errors.Is(err, os.ErrPermission):
		return "permission denied on the docker socket"
	case errors.Is(err, syscall.ENOENT), errors.Is(err, os.ErrNotExist):
		return "no docker socket (docker is not installed or not running)"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "docker is not running"
	}
	return "docker did not answer"
}

// dockerOptions are the inputs of collectDocker.
type dockerOptions struct {
	api      DockerAPI
	expected []string
	prev     *snapshot.Snapshot
	now      time.Time
	// dfEvery is how long the last `system df` is kept before it is asked again.
	dfEvery time.Duration
	// logWindow is how far back Caddy's log is read for errors.
	logWindow time.Duration
	// gatewayService is the Compose service whose log is read.
	gatewayService string
}

// dockerResult is the Docker section, what else it found (Docker's data
// root, the gateway's error lines) and what could not be read.
type dockerResult struct {
	section *snapshot.Docker
	rootDir string
	gateway *snapshot.JournalSource
	errs    []string
}

const (
	inspectWorkers = 8
	callTimeout    = 3 * time.Second
	dfTimeout      = 4 * time.Second
)

// collectDocker asks the engine for its containers: the list, then each
// one's inspect and (when running) stats at the same time.
func collectDocker(ctx context.Context, o dockerOptions) dockerResult {
	res := dockerResult{section: &snapshot.Docker{}}
	d := res.section
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	ver, err := o.api.Version(cctx)
	cancel()
	if err != nil {
		d.Error = whyDown(err)
		res.errs = append(res.errs, "docker: "+d.Error)
		return res
	}
	d.OK, d.Version, d.API = true, ver.Version, ver.APIVersion
	cctx, cancel = context.WithTimeout(ctx, callTimeout)
	if info, err := o.api.Info(cctx); err == nil {
		res.rootDir = info.DockerRootDir
	}
	cancel()
	cctx, cancel = context.WithTimeout(ctx, callTimeout)
	list, err := o.api.List(cctx)
	cancel()
	if err != nil {
		d.OK, d.Error = false, whyDown(err)
		res.errs = append(res.errs, "docker: containers: "+d.Error)
		return res
	}
	// This install's containers first, then the others, each by name; the
	// excess is cut (and said so).
	slices.SortStableFunc(list, func(a, b EngineContainer) int {
		ak, bk := a.Project == snapshot.BobresProject, b.Project == snapshot.BobresProject
		switch {
		case ak != bk && ak:
			return -1
		case ak != bk:
			return 1
		}
		return strings.Compare(firstName(a), firstName(b))
	})
	if len(list) > snapshot.MaxContainers {
		list, d.Truncated = list[:snapshot.MaxContainers], true
	}

	cs := make([]snapshot.Container, len(list))
	failed := make([]bool, len(list))
	prev := snapshot.PrevContainers(o.prev)
	var gap time.Duration
	if o.prev != nil {
		gap = o.now.Sub(time.UnixMilli(o.prev.GeneratedMS))
	}
	sem := make(chan struct{}, inspectWorkers)
	var wg sync.WaitGroup
	for i := range list {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			cs[i], failed[i] = readContainer(ctx, o.api, list[i], prev, gap)
		}()
	}
	wg.Wait()
	nfail := 0
	for _, f := range failed {
		if f {
			nfail++
		}
	}
	if nfail > 0 {
		res.errs = append(res.errs, fmt.Sprintf("docker: %d containers could not be read in full", nfail))
	}
	d.Containers = cs

	// What this install is meant to run, and what of it does.
	if len(o.expected) > 0 {
		running := map[string]bool{}
		for _, c := range cs {
			if c.Project == snapshot.BobresProject && c.State == snapshot.StateRunning {
				running[c.Service] = true
			}
		}
		comp := &snapshot.Compose{Expected: slices.Clone(o.expected)}
		for _, s := range o.expected {
			if !running[s] {
				comp.Missing = append(comp.Missing, s)
			}
		}
		d.Compose = comp
	}

	d.DF, res.errs = dockerDF(ctx, o, res.errs)

	// The gateway's error lines: Caddy runs in a container, so its log is the
	// engine's, not the journal's.
	if o.gatewayService != "" && o.logWindow > 0 {
		for i, l := range list {
			if l.Project == snapshot.BobresProject && l.Service == o.gatewayService {
				res.gateway = gatewayErrors(ctx, o.api, cs[i].ID, o.now, o.logWindow)
				break
			}
		}
	}
	return res
}

func firstName(c EngineContainer) string {
	if len(c.Names) == 0 {
		return c.ID
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// readContainer builds one container's entry. failed: something about it could
// not be read (the entry holds what could).
func readContainer(ctx context.Context, api DockerAPI, l EngineContainer, prev map[string]snapshot.Container, gap time.Duration) (snapshot.Container, bool) {
	c := snapshot.Container{ID: shortID(l.ID), Name: firstName(l), Project: l.Project, Service: l.Service, Image: imageRef(l.Image),
		State: strings.ToLower(l.State), Health: healthFrom(l.Health, l.Status)}
	failed := false
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	in, err := api.Inspect(cctx, l.ID)
	cancel()
	if err != nil {
		failed = true
	} else {
		c.Restarts = in.RestartCount
		c.OOMKilled = in.State.OOMKilled
		if in.State.Restarting {
			c.State = snapshot.StateRestarting
		}
		if in.State.Health != nil && in.State.Health.Status != "" {
			c.Health = in.State.Health.Status
		}
		if t, err := time.Parse(time.RFC3339Nano, in.State.StartedAt); err == nil && t.Unix() > 0 {
			c.StartedAt = t.Unix()
		}
		if c.State != snapshot.StateRunning {
			code := in.State.ExitCode
			c.ExitCode = &code
		}
	}
	if c.State != snapshot.StateRunning {
		return c, failed
	}
	cctx, cancel = context.WithTimeout(ctx, callTimeout)
	st, err := api.Stats(cctx, l.ID)
	cancel()
	if err != nil {
		return c, true
	}
	c.CPUNS = st.CPU.Usage.Total
	c.MemBytes, c.MemLimit = st.usage(), st.Memory.Limit
	for _, n := range st.Networks {
		c.NetRx += n.Rx
		c.NetTx += n.Tx
	}
	c.BlkRead, c.BlkWrite = st.blk()
	c.PIDs = int(min(st.PIDs.Current, 10_000_000)) //nolint:gosec // bounded just above
	// CPU share since the previous snapshot, when that one saw it running too.
	if p, ok := prev[c.ID]; ok && p.State == snapshot.StateRunning && p.CPUNS > 0 && c.CPUNS > 0 {
		c.CPUPct = snapshot.ContainerCPUPct(p.CPUNS, c.CPUNS, gap)
	}
	return c, failed
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// imageRef is an image reference without its digest ("repo:tag"); an image
// known by id only is shown as a short id.
func imageRef(image string) string {
	ref, _, _ := strings.Cut(image, "@")
	if rest, ok := strings.CutPrefix(ref, "sha256:"); ok {
		return "sha256:" + shortID(rest)
	}
	return ref
}

// healthFrom reads a container's health from the list entry: its Health field
// (engines of API 1.52 and later), else the words in its Status line.
func healthFrom(health, status string) string {
	switch health {
	case snapshot.HealthHealthy, snapshot.HealthUnhealthy, snapshot.HealthStarting, snapshot.HealthNone:
		return health
	}
	switch {
	case strings.Contains(status, "(unhealthy)"):
		return snapshot.HealthUnhealthy
	case strings.Contains(status, "(healthy)"):
		return snapshot.HealthHealthy
	case strings.Contains(status, "(health: starting)"):
		return snapshot.HealthStarting
	}
	return snapshot.HealthNone
}

// dockerDF returns the `system df` figures: the previous ones while they are
// recent, else asked for again (at most once per dfEvery, a failed ask too).
func dockerDF(ctx context.Context, o dockerOptions, errs []string) (*snapshot.DF, []string) {
	var last *snapshot.DF
	if o.prev != nil && o.prev.Docker != nil {
		last = o.prev.Docker.DF
	}
	if last != nil && o.now.Sub(time.Unix(last.AttemptedAt, 0)) < o.dfEvery {
		keep := *last
		return &keep, errs
	}
	cctx, cancel := context.WithTimeout(ctx, dfTimeout)
	defer cancel()
	df, err := o.api.DF(cctx)
	if err != nil {
		errs = append(errs, "docker df: "+whyDown(err))
		if last == nil {
			return nil, errs
		}
		keep := *last
		keep.AttemptedAt = o.now.Unix()
		return &keep, errs
	}
	return df.summary(o.now), errs
}

// gatewayErrors reads the gateway container's recent log for error lines.
func gatewayErrors(ctx context.Context, api DockerAPI, id string, now time.Time, window time.Duration) *snapshot.JournalSource {
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	raw, err := api.Logs(cctx, id, now.Add(-window), 300)
	if err != nil {
		return nil
	}
	count, capped, lines := errorLines(demux(raw), now)
	return &snapshot.JournalSource{Name: "caddy", Errors: count, Capped: capped, Lines: lines}
}

// demux strips the engine's stream framing from a container's log: eight bytes
// (stream, three zeros, a big-endian length) before each chunk. A log of a
// container with a terminal has no framing and is returned as it is.
func demux(raw []byte) []byte {
	if len(raw) < 8 || raw[0] > 2 || raw[1] != 0 || raw[2] != 0 || raw[3] != 0 {
		return raw
	}
	var out bytes.Buffer
	for len(raw) >= 8 {
		n := int(binary.BigEndian.Uint32(raw[4:8]))
		raw = raw[8:]
		if n < 0 || n > len(raw) {
			n = len(raw)
		}
		out.Write(raw[:n])
		raw = raw[n:]
	}
	return out.Bytes()
}

// errorLines picks the error-level lines of a JSON log (Caddy's): the count,
// whether the log was cut short at the lines the collector asked for, and the
// last few lines, scrubbed.
func errorLines(log []byte, now time.Time) (count int, capped bool, lines []snapshot.JournalLine) {
	total := 0
	for _, line := range bytes.Split(log, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		total++
		var l struct {
			Level string          `json:"level"`
			TS    json.RawMessage `json:"ts"`
			Msg   string          `json:"msg"`
			Error string          `json:"error"`
		}
		if json.Unmarshal(line, &l) != nil {
			continue
		}
		switch strings.ToLower(l.Level) {
		case "error", "fatal", "panic", "critical":
		default:
			continue
		}
		count++
		msg := l.Msg
		if l.Error != "" {
			msg += ": " + l.Error
		}
		at := now.Unix()
		if f, err := strconv.ParseFloat(strings.Trim(string(l.TS), `"`), 64); err == nil && f > 0 {
			at = int64(f)
		}
		lines = append(lines, snapshot.JournalLine{At: at, Msg: Scrub(msg)})
		if len(lines) > snapshot.MaxJournalLines {
			lines = lines[1:]
		}
	}
	return count, total >= 300, lines
}
