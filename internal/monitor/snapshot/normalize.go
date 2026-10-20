package snapshot

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"time"
)

// Errors a reader tells apart.
var (
	// ErrInvalid: the file is not a snapshot (not JSON, no usable time).
	ErrInvalid = errors.New("snapshot: not a valid snapshot")
	// ErrSchema: a format version this build does not read.
	ErrSchema = errors.New("snapshot: unsupported schema version")
	// ErrTooLarge: the file is bigger than MaxFileBytes.
	ErrTooLarge = errors.New("snapshot: file too large")
)

// future is how far ahead of the reader's clock a snapshot may claim to be
// made. Both clocks are the host's, so a larger gap is a wrong file.
const future = 5 * time.Minute

// Normalize validates a snapshot and bounds it, in place. The reader treats
// the file as untrusted whoever wrote it: a field that is not what it should
// be is dropped (a list is cut, a name that is not shaped like a name is left
// out, a percentage that cannot be one is unknown), and free text is made
// plain (Clean). An error means nothing in the file can be used: its schema
// is not this one, or it has no sensible time.
func (s *Snapshot) Normalize(now time.Time) error {
	if s.Schema != Schema {
		return fmt.Errorf("%w %d (this build reads %d)", ErrSchema, s.Schema, Schema)
	}
	if s.GeneratedAt <= 0 || time.Unix(s.GeneratedAt, 0).After(now.Add(future)) {
		return fmt.Errorf("%w: generated_at is not a time in the past", ErrInvalid)
	}
	if ms := s.GeneratedAt * 1000; s.GeneratedMS < ms-60_000 || s.GeneratedMS > ms+60_000 {
		s.GeneratedMS = ms
	}
	s.IntervalS = clampInt(s.IntervalS, 1, 3600)
	s.DurationMS = min(max(s.DurationMS, 0), 600_000)
	if !reVersion.MatchString(s.Version) {
		s.Version = ""
	}
	s.Errors = cleanList(s.Errors, MaxErrors, 160)
	if s.Host != nil {
		s.Host.normalize()
	}
	if s.Docker != nil {
		s.Docker.normalize()
	}
	s.Units = normalizeUnits(s.Units)
	if s.Journal != nil {
		s.Journal.normalize()
	}
	if s.Kubernetes != nil {
		s.Kubernetes.normalize()
	}
	if s.Update != nil {
		u := s.Update
		u.From, u.To = ifShaped(Clean(u.From, 64), reVersion), ifShaped(Clean(u.To, 64), reVersion)
		u.At, u.RolledBackAt = min(max(u.At, 0), math.MaxInt32*4), min(max(u.RolledBackAt, 0), math.MaxInt32*4)
		if u.To == "" || u.At == 0 {
			s.Update = nil // not an update anybody can describe
		}
	}
	return nil
}

func (h *Host) normalize() {
	h.Hostname = ifShaped(Clean(h.Hostname, 63), reName)
	h.OS = Clean(h.OS, 80)
	h.Kernel = ifShaped(Clean(h.Kernel, 80), reVersion)
	h.Arch = ifShaped(Clean(h.Arch, 16), reWord)
	h.UptimeS = min(max(h.UptimeS, 0), 100*365*24*3600)
	h.Cores = clampInt(h.Cores, 0, 4096)
	h.CPU.Pct = pct(h.CPU.Pct, 100)
	for i := range h.Load {
		h.Load[i] = finite(h.Load[i], 0, 1e5)
	}
	if h.Mem.Available > h.Mem.Total {
		h.Mem = Mem{} // cannot be: unknown, not a made-up figure
	}
	if h.Swap.Used > h.Swap.Total {
		h.Swap = Swap{}
	}
	var disks []Disk
	seen := map[string]bool{}
	for _, d := range h.Disks {
		if !ValidMount(d.Mount) || seen[d.Mount] || len(disks) == MaxDisks {
			continue
		}
		seen[d.Mount] = true
		d.FS = ifShaped(Clean(d.FS, 32), reWord)
		d.Roles = keepOnly(d.Roles, "install", "docker")
		disks = append(disks, d)
	}
	h.Disks = disks
	var ifaces []Iface
	seen = map[string]bool{}
	for _, i := range h.Net.Ifaces {
		if !reIface.MatchString(i.Name) || seen[i.Name] || len(ifaces) == MaxIfaces {
			continue
		}
		seen[i.Name] = true
		ifaces = append(ifaces, i)
	}
	h.Net.Ifaces = ifaces
}

func (d *Docker) normalize() {
	d.Error = Clean(d.Error, 160)
	d.Version = ifShaped(Clean(d.Version, 40), reVersion)
	d.API = ifShaped(Clean(d.API, 16), reVersion)
	var cs []Container
	seen := map[string]bool{}
	for _, c := range d.Containers {
		if !reID12.MatchString(c.ID) || !ValidName(c.Name) || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		if len(cs) == MaxContainers {
			d.Truncated = true
			break
		}
		c.normalize()
		cs = append(cs, c)
	}
	slices.SortStableFunc(cs, func(a, b Container) int { return cmp.Compare(a.Name, b.Name) })
	d.Containers = cs
	if d.DF != nil {
		d.DF.CollectedAt = min(max(d.DF.CollectedAt, 0), math.MaxInt32*4)
		d.DF.AttemptedAt = min(max(d.DF.AttemptedAt, 0), math.MaxInt32*4)
	}
	if d.Compose != nil {
		d.Compose.Expected = services(d.Compose.Expected)
		d.Compose.Missing = services(d.Compose.Missing)
	}
}

func (c *Container) normalize() {
	if !ValidName(c.Project) {
		c.Project = ""
	}
	if !ValidName(c.Service) {
		c.Service = ""
	}
	if !reImage.MatchString(c.Image) {
		c.Image = ""
	}
	switch c.State {
	case StateRunning, StateRestarting, StatePaused, StateCreated, StateRemoving, StateExited, StateDead:
	default:
		c.State = StateUnknown
	}
	switch c.Health {
	case HealthNone, HealthStarting, HealthHealthy, HealthUnhealthy:
	default:
		c.Health = HealthNone
	}
	c.Restarts = clampInt(c.Restarts, 0, 1_000_000_000)
	c.StartedAt = min(max(c.StartedAt, 0), math.MaxInt32*4)
	if c.ExitCode != nil {
		v := clampInt(*c.ExitCode, -1, 255)
		c.ExitCode = &v
	}
	c.CPUPct = pct(c.CPUPct, 100*4096)
	c.PIDs = clampInt(c.PIDs, 0, 10_000_000)
}

func normalizeUnits(units []Unit) []Unit {
	var out []Unit
	seen := map[string]bool{}
	for _, u := range units {
		if !reUnit.MatchString(u.Name) || seen[u.Name] || len(out) == MaxUnits {
			continue
		}
		seen[u.Name] = true
		u.Load, u.Active, u.Sub = word(u.Load), word(u.Active), word(u.Sub)
		u.Enabled, u.Result = word(u.Enabled), word(u.Result)
		u.Since = min(max(u.Since, 0), math.MaxInt32*4)
		u.Restarts = clampInt(u.Restarts, 0, 1_000_000_000)
		out = append(out, u)
	}
	return out
}

func (j *Journal) normalize() {
	j.WindowS = clampInt(j.WindowS, 0, 7*24*3600)
	var out []JournalSource
	seen := map[string]bool{}
	for _, s := range j.Sources {
		if !ValidName(s.Name) || seen[s.Name] || len(out) == MaxJournalSources {
			continue
		}
		seen[s.Name] = true
		s.Errors = clampInt(s.Errors, 0, 1_000_000)
		var lines []JournalLine
		for _, l := range s.Lines {
			if len(lines) == MaxJournalLines {
				break
			}
			if msg := Clean(l.Msg, MaxLine); msg != "" {
				lines = append(lines, JournalLine{At: min(max(l.At, 0), math.MaxInt32*4), Msg: msg})
			}
		}
		s.Lines = lines
		out = append(out, s)
	}
	j.Sources = out
}

func (k *Kube) normalize() {
	k.Reason = Clean(k.Reason, 120)
	if k.Via != "kubectl" && k.Via != "k3s kubectl" {
		k.Via = ""
	}
	var nodes []KubeNode
	for _, n := range k.Nodes {
		if !reDNS.MatchString(n.Name) || len(nodes) == MaxNodes {
			continue
		}
		n.Roles = ifShaped(Clean(n.Roles, 60), reRoles)
		n.Version = ifShaped(Clean(n.Version, 40), reVersion)
		nodes = append(nodes, n)
	}
	k.Nodes = nodes
	var pods []KubePod
	for _, p := range k.Pods {
		if !reDNS.MatchString(p.Name) || !reDNS.MatchString(p.Namespace) {
			continue
		}
		if len(pods) == MaxPods {
			k.Truncated = true
			break
		}
		switch p.Phase {
		case "Pending", "Running", "Succeeded", "Failed", "Unknown":
		default:
			p.Phase = "Unknown"
		}
		p.Restarts = clampInt(p.Restarts, 0, 1_000_000_000)
		if !reDNS.MatchString(p.Node) {
			p.Node = ""
		}
		pods = append(pods, p)
	}
	k.Pods = pods
}

// services keeps the names shaped like Compose services, once each.
func services(in []string) []string {
	var out []string
	for _, s := range in {
		if ValidName(s) && !slices.Contains(out, s) && len(out) < MaxServices {
			out = append(out, s)
		}
	}
	return out
}

// keepOnly keeps the allowed values, once each.
func keepOnly(in []string, allowed ...string) []string {
	var out []string
	for _, s := range in {
		if slices.Contains(allowed, s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// cleanList cleans each text, drops the empty ones and keeps at most n.
func cleanList(in []string, n, maxLen int) []string {
	var out []string
	for _, s := range in {
		if s = Clean(s, maxLen); s != "" && len(out) < n {
			out = append(out, s)
		}
	}
	return out
}

// word keeps a state word (systemd's "active", "enabled"...) or drops it.
func word(s string) string {
	if reWord.MatchString(s) {
		return s
	}
	return ""
}

// ifShaped returns s when it matches re, else "".
func ifShaped(s string, re *regexp.Regexp) string {
	if re.MatchString(s) {
		return s
	}
	return ""
}

func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }

// finite clamps v to [lo, hi]; a value that is not a number becomes lo.
func finite(v, lo, hi float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return lo
	}
	return min(max(v, lo), hi)
}

// pct keeps a percentage that makes sense (0 to limit), else nil: unknown.
func pct(v *float64, limit float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > limit {
		return nil
	}
	out := *v
	return &out
}
