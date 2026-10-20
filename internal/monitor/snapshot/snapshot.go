// Package snapshot is the file the host collector hands to core: what
// `bobres monitor snapshot` (a root helper on the server, run every 30
// seconds by a systemd timer) saw of the host, Docker, the BOBRES systemd
// units, the journal and, when there is one, a Kubernetes cluster.
//
// The collector writes the file; core mounts its directory read-only and
// reads it. Core has no Docker socket and no shell, so this file is the only
// thing it knows about the machine. It is written for that reader's sake:
//
//   - only allow-listed, size-bounded fields are ever written (no environment
//     variables, command lines, labels, mounts' host paths, tokens);
//   - the reader still treats every field as untrusted: Decode validates and
//     bounds it (Normalize), whoever wrote it.
//
// The format is versioned (Schema): a reader refuses a version it does not
// know instead of guessing, and ignores fields it does not know.
package snapshot

import "time"

// Schema is the format version this package reads and writes.
const Schema = 1

// FileName is the snapshot's name inside the monitor directory
// (<install>/run/monitor on the host, BOBRES_MONITOR_DIR in core).
const FileName = "snapshot.json"

// Timing. The timer runs the collector every Interval; a snapshot older than
// FreshAge has missed three rounds.
const (
	Interval = 30 * time.Second
	FreshAge = 90 * time.Second
)

// Bounds on what a snapshot may hold (Normalize drops the excess).
const (
	MaxFileBytes      = 1 << 20
	MaxContainers     = 64
	MaxDisks          = 16
	MaxIfaces         = 16
	MaxUnits          = 16
	MaxJournalSources = 8
	MaxJournalLines   = 5
	MaxErrors         = 10
	MaxNodes          = 32
	MaxPods           = 200
	MaxServices       = 32
	// MaxLine bounds one journal line, in characters.
	MaxLine = 240
)

// Snapshot is one run of the collector.
type Snapshot struct {
	Schema int `json:"schema"`
	// GeneratedAt is when the run finished (unix seconds); GeneratedMS is the
	// moment its counters were read (unix milliseconds), which rates between
	// two snapshots are divided by.
	GeneratedAt int64 `json:"generated_at"`
	GeneratedMS int64 `json:"generated_ms"`
	IntervalS   int   `json:"interval_s"`
	DurationMS  int64 `json:"duration_ms"`
	// Version is the bobres CLI that wrote it.
	Version string `json:"version"`
	// Errors names the parts the run could not read, in a few words each.
	Errors     []string `json:"errors,omitempty"`
	Host       *Host    `json:"host,omitempty"`
	Docker     *Docker  `json:"docker,omitempty"`
	Units      []Unit   `json:"units,omitempty"`
	Journal    *Journal `json:"journal,omitempty"`
	Kubernetes *Kube    `json:"kubernetes,omitempty"`
}

// Host is the machine itself.
type Host struct {
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os,omitempty"`
	Kernel   string `json:"kernel,omitempty"`
	Arch     string `json:"arch,omitempty"`
	UptimeS  int64  `json:"uptime_s"`
	Cores    int    `json:"cores"`
	CPU      CPU    `json:"cpu"`
	Load     Load   `json:"load"`
	Mem      Mem    `json:"mem"`
	Swap     Swap   `json:"swap"`
	Disks    []Disk `json:"disks"`
	Net      Net    `json:"net"`
	Clock    *Clock `json:"clock,omitempty"`
}

// CPU is processor use. Busy and Total are cumulative jiffies (Busy is
// everything but idle and iowait), kept so the next run can compute Pct over
// the time between the two.
type CPU struct {
	// Pct is the busy share since the previous snapshot, 0 to 100; nil when
	// there is no usable previous snapshot (first run, too old, a reboot).
	Pct   *float64 `json:"pct"`
	Busy  uint64   `json:"busy"`
	Total uint64   `json:"total"`
}

// Load is the 1, 5 and 15 minute load averages.
type Load [3]float64

// Mem is memory in bytes; Available is what a new program could use without
// swapping (the kernel's MemAvailable).
type Mem struct {
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"`
}

// Used is Total less Available.
func (m Mem) Used() uint64 {
	if m.Available > m.Total {
		return 0
	}
	return m.Total - m.Available
}

// Swap is swap in bytes.
type Swap struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

// Disk is one filesystem, named by its mount point. Roles say what the
// collector looked at it for: "install" (the directory BOBRES is installed
// in) and "docker" (Docker's data root); one filesystem holding both has both.
type Disk struct {
	Mount string `json:"mount"`
	FS    string `json:"fs,omitempty"`
	// Total is the filesystem's size; Used and Avail are what df shows (Avail
	// is what an ordinary user can still write, so Used+Avail can be less
	// than Total on a filesystem with reserved blocks).
	Total       uint64   `json:"total"`
	Used        uint64   `json:"used"`
	Avail       uint64   `json:"avail"`
	InodesTotal uint64   `json:"inodes_total,omitempty"`
	InodesUsed  uint64   `json:"inodes_used,omitempty"`
	Roles       []string `json:"roles,omitempty"`
}

// Pct is how full the filesystem is, as df shows it (Used / (Used+Avail)),
// 0 to 100.
func (d Disk) Pct() float64 {
	if d.Used+d.Avail == 0 {
		return 0
	}
	return float64(d.Used) / float64(d.Used+d.Avail) * 100
}

// Net holds the cumulative byte counters of the host's real network
// interfaces (virtual ones, Docker's bridges and veth pairs among them, are
// left out), one entry each so a rate is computed over the interfaces both
// snapshots have.
type Net struct {
	Ifaces []Iface `json:"ifaces"`
}

// Iface is one network interface's counters since boot.
type Iface struct {
	Name    string `json:"name"`
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// Clock says whether the clock is kept right by NTP; nil fields are unknown.
type Clock struct {
	Synchronized *bool `json:"synchronized,omitempty"`
	NTP          *bool `json:"ntp,omitempty"`
}

// Docker is the container engine.
type Docker struct {
	// OK is false when the engine did not answer; Error says why in a few
	// words ("permission denied", "no such socket").
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	Version    string      `json:"version,omitempty"`
	API        string      `json:"api,omitempty"`
	Containers []Container `json:"containers,omitempty"`
	// Truncated: the engine has more containers than MaxContainers.
	Truncated bool     `json:"truncated,omitempty"`
	DF        *DF      `json:"df,omitempty"`
	Compose   *Compose `json:"compose,omitempty"`
}

// Container states and health values (the engine's words).
const (
	StateRunning    = "running"
	StateRestarting = "restarting"
	StatePaused     = "paused"
	StateCreated    = "created"
	StateRemoving   = "removing"
	StateExited     = "exited"
	StateDead       = "dead"
	StateUnknown    = "unknown"

	HealthNone      = "none"
	HealthStarting  = "starting"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

// BobresProject is the Compose project every bobres command uses.
const BobresProject = "bobres"

// Container is one container. Nothing of its environment, command line,
// labels (but its Compose project and service), mounts or addresses is kept.
type Container struct {
	// ID is the engine's short id (12 hex characters).
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project,omitempty"`
	Service string `json:"service,omitempty"`
	// Image is the reference and tag, without a digest.
	Image     string `json:"image"`
	State     string `json:"state"`
	Health    string `json:"health"`
	Restarts  int    `json:"restarts"`
	StartedAt int64  `json:"started_at,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	OOMKilled bool   `json:"oom_killed,omitempty"`
	// CPUNS is the cumulative CPU time in nanoseconds; CPUPct is the share of
	// one core used since the previous snapshot (nil when unknown).
	CPUNS    uint64   `json:"cpu_ns,omitempty"`
	CPUPct   *float64 `json:"cpu_pct"`
	MemBytes uint64   `json:"mem_bytes"`
	MemLimit uint64   `json:"mem_limit,omitempty"`
	NetRx    uint64   `json:"net_rx_bytes"`
	NetTx    uint64   `json:"net_tx_bytes"`
	BlkRead  uint64   `json:"blk_read_bytes,omitempty"`
	BlkWrite uint64   `json:"blk_write_bytes,omitempty"`
	PIDs     int      `json:"pids,omitempty"`
}

// DF is `docker system df` in bytes. The engine takes seconds to compute it,
// so the collector refreshes it only now and then and carries the last values
// between (CollectedAt says when they were read).
type DF struct {
	ImagesBytes       uint64 `json:"images_bytes"`
	ImagesReclaimable uint64 `json:"images_reclaimable_bytes"`
	ContainersBytes   uint64 `json:"containers_bytes"`
	VolumesBytes      uint64 `json:"volumes_bytes"`
	BuildCacheBytes   uint64 `json:"build_cache_bytes"`
	// CollectedAt is when the figures were read; AttemptedAt when the engine
	// was last asked (a failed try is not repeated every round).
	CollectedAt int64 `json:"collected_at"`
	AttemptedAt int64 `json:"attempted_at"`
}

// Compose lists the services this install is meant to run and those of them
// with no running container.
type Compose struct {
	Expected []string `json:"expected"`
	Missing  []string `json:"missing"`
}

// Unit is a systemd unit of BOBRES (or Docker) as systemd reports it. Units
// that are not installed are left out.
type Unit struct {
	Name   string `json:"name"`
	Load   string `json:"load"`
	Active string `json:"active"`
	Sub    string `json:"sub"`
	// Enabled is systemd's UnitFileState (enabled, disabled, static...).
	Enabled  string `json:"enabled"`
	Result   string `json:"result,omitempty"`
	Since    int64  `json:"since,omitempty"`
	Restarts int    `json:"restarts,omitempty"`
}

// Journal counts the recent error-level lines of some sources and keeps a few
// of them, scrubbed.
type Journal struct {
	WindowS int             `json:"window_s"`
	Sources []JournalSource `json:"sources"`
}

// JournalSource is one source: "docker" (the engine and containerd),
// "bobres" (BOBRES's own units) or "caddy" (the gateway container).
type JournalSource struct {
	Name string `json:"name"`
	// Errors is how many error lines the window holds (capped at the number
	// the collector reads: Capped says it was).
	Errors int           `json:"errors"`
	Capped bool          `json:"capped,omitempty"`
	Lines  []JournalLine `json:"lines,omitempty"`
}

// JournalLine is one scrubbed line.
type JournalLine struct {
	At  int64  `json:"at"`
	Msg string `json:"msg"`
}

// Kube is the optional Kubernetes section: present when kubectl (or k3s)
// is on the host.
type Kube struct {
	// Detected is true when a cluster answered; Reason says why not otherwise.
	Detected bool   `json:"detected"`
	Reason   string `json:"reason,omitempty"`
	// Via is how it was asked: "kubectl" or "k3s kubectl".
	Via       string     `json:"via,omitempty"`
	Nodes     []KubeNode `json:"nodes,omitempty"`
	Pods      []KubePod  `json:"pods,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
}

// KubeNode is a node.
type KubeNode struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Roles   string `json:"roles,omitempty"`
	Version string `json:"version,omitempty"`
}

// KubePod is a pod: its phase, whether every container is ready and how often
// they restarted.
type KubePod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Ready     bool   `json:"ready"`
	Restarts  int    `json:"restarts"`
	Node      string `json:"node,omitempty"`
}

// Age is how long ago the snapshot was made (negative when it claims to be
// from the future).
func (s *Snapshot) Age(now time.Time) time.Duration {
	return now.Sub(time.Unix(s.GeneratedAt, 0))
}

// Fresh reports whether the snapshot is recent enough to say how the machine
// is now.
func (s *Snapshot) Fresh(now time.Time) bool {
	a := s.Age(now)
	return a >= -time.Minute && a <= FreshAge
}
