package monitor

import (
	"bytes"
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// The Prometheus endpoint, GET /metrics (docs/runbooks/monitoring.md, "Prometheus and Grafana"): what the monitor
// knows now, in the text format 0.0.4, built from the same shared asking of the services, servers, gateways, queues
// and backups that the overview uses (so scraping it as often as the page polls costs nothing more), and from the
// host collector's snapshot.
//
// What it holds is a closed list (promDefs): every metric is a gauge, with a fixed set of label names whose values
// are fixed words (a service, a queue, a window, a severity), the version, a mount point, or the name of a panel
// server, so nothing grows with the number of customers. It holds no customer, order, address, token or key, and no
// text that came from a failure. A figure that is not known (the collector is not installed, the backup service is
// off) is left out, never written as 0.

// The metrics, in the order they are written.
const (
	promBuildInfo        = "bobres_build_info"
	promStartTime        = "bobres_core_start_time_seconds"
	promServiceUp        = "bobres_service_up"
	promServiceResponse  = "bobres_service_response_seconds"
	promDatabaseUp       = "bobres_database_up"
	promDatabaseResponse = "bobres_database_response_seconds"
	promQueue            = "bobres_queue_size"
	promOrdersPaid       = "bobres_orders_paid"
	promAlertsOpen       = "bobres_alerts_open"
	promPanelUp          = "bobres_panel_up"
	promPanelClients     = "bobres_panel_clients"
	promPanelLimit       = "bobres_panel_clients_limit"
	promLicenseDays      = "bobres_license_days_left"
	promBackupAge        = "bobres_backup_age_seconds"
	promBackupOK         = "bobres_backup_last_run_ok"
	promCollectorUp      = "bobres_host_collector_up"
	promHostCPU          = "bobres_host_cpu_usage_ratio"
	promHostLoad1        = "bobres_host_load1"
	promHostMemUsed      = "bobres_host_memory_used_bytes"
	promHostMemTotal     = "bobres_host_memory_total_bytes"
	promHostDiskUsed     = "bobres_host_disk_used_ratio"
	promHostDiskFree     = "bobres_host_disk_free_bytes"
	promHostDiskTotal    = "bobres_host_disk_total_bytes"
	promHostUptime       = "bobres_host_uptime_seconds"
)

// promDef is one metric of the endpoint: its name and its help line. All of them are gauges.
type promDef struct{ name, help string }

var promDefs = []promDef{
	{promBuildInfo, "Always 1; the label version is the version of core."},
	{promStartTime, "When core started, in unix seconds."},
	{promServiceUp, "1 when the service answers its health check and is ready, 0 when it is down or not ready."},
	{promServiceResponse, "How long the service took to answer its health check, while it answers."},
	{promDatabaseUp, "1 when the database answers core, 0 when it does not."},
	{promDatabaseResponse, "How long the database took to answer a ping, while it answers."},
	{promQueue, "Items waiting in a queue: outbox (events the slowest consumer has not read), pending_payments (manual payments waiting for a review), provisioning (paid orders waiting for the panel) and pending_orders (orders not yet paid)."},
	{promOrdersPaid, "Orders paid in the last window: VPN services, licenses and digital accounts, as the Reports page counts sales, gross of refunds."},
	{promAlertsOpen, "System alerts that are open, by severity."},
	{promPanelUp, "1 when the panel server passed its last health check, 0 when it failed. A server that is off or was never checked is left out."},
	{promPanelClients, "Services (clients) on the panel server, deliveries in progress included."},
	{promPanelLimit, "The most services the panel server takes. Left out when it has no limit."},
	{promLicenseDays, "Whole days until the license (basis license) or the free trial (basis trial) ends; negative once a license has ended. Left out on the free tier."},
	{promBackupAge, "Seconds since the newest good backup."},
	{promBackupOK, "1 when the last backup run worked, 0 when it failed."},
	{promCollectorUp, "1 when the host collector's snapshot is fresh, 0 when it is stale or unusable. Left out where the collector is not installed."},
	{promHostCPU, "CPU in use, from 0 to 1, as the host collector measured it."},
	{promHostLoad1, "The host's 1-minute load average."},
	{promHostMemUsed, "Memory in use on the host, in bytes."},
	{promHostMemTotal, "Memory of the host, in bytes."},
	{promHostDiskUsed, "How full the filesystem is, from 0 to 1, as df shows it."},
	{promHostDiskFree, "Bytes available to ordinary users on the filesystem."},
	{promHostDiskTotal, "Size of the filesystem, in bytes."},
	{promHostUptime, "How long the host has been up, in seconds."},
}

// Bounds, so that nothing the install holds can make the answer grow: the panel servers listed, the mount points.
const (
	maxPromServers = 200
	maxPromMounts  = 16
	// paidTTL is how long the count of paid orders is kept: a scrape every 15 seconds does not repeat the query.
	paidTTL = 30 * time.Second
)

// promSample is one line of a metric: its labels as they are written, and its value.
type promSample struct {
	labels string
	value  float64
}

// promText collects the samples of a scrape by metric.
type promText struct{ by map[string][]promSample }

func newPromText() *promText { return &promText{by: map[string][]promSample{}} }

// add records a sample; labels are name, value pairs. A value that is not a number is not a sample.
func (p *promText) add(name string, v float64, labels ...string) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	p.by[name] = append(p.by[name], promSample{labels: promLabels(labels), value: v})
}

// promLabels writes label pairs as {name="value",...}, with the values escaped.
func promLabels(kv []string) string {
	if len(kv) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i])
		b.WriteString(`="`)
		b.WriteString(promEscape(kv[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// promEscape makes a label value safe for the text format: a backslash, a quote and a line break are escaped,
// any other control character is dropped, and what is not UTF-8 is replaced.
func promEscape(s string) string {
	s = strings.ToValidUTF8(s, "�")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\n':
			b.WriteString(`\n`)
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// promHelp makes a help line safe: a backslash and a line break are escaped.
func promHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

// text writes the metrics that have samples, in promDefs order, each with its help and type lines, the samples sorted
// by their labels so that two scrapes of the same state are the same bytes.
func (p *promText) text() []byte {
	var b bytes.Buffer
	for _, d := range promDefs {
		samples := p.by[d.name]
		if len(samples) == 0 {
			continue
		}
		sort.SliceStable(samples, func(i, j int) bool { return samples[i].labels < samples[j].labels })
		b.WriteString("# HELP " + d.name + " " + promHelp(d.help) + "\n")
		b.WriteString("# TYPE " + d.name + " gauge\n")
		for i, s := range samples {
			if i > 0 && s.labels == samples[i-1].labels {
				continue // a series is written once, whatever the source said twice
			}
			b.WriteString(d.name + s.labels + " " + strconv.FormatFloat(s.value, 'f', -1, 64) + "\n")
		}
	}
	return b.Bytes()
}

func promBool(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// promState is the monitor's memory for the endpoint.
type promState struct {
	mu     sync.Mutex
	paid   *store.PaidOrders
	paidAt time.Time
}

// paidOrders reads the count of orders paid, at most every paidTTL; nil when it cannot be read (the metric is then left out).
func (m *Monitor) paidOrders(ctx context.Context, now time.Time) *store.PaidOrders {
	if m.cfg.Store == nil {
		return nil
	}
	m.prom.mu.Lock()
	defer m.prom.mu.Unlock()
	if m.prom.paid != nil && now.Sub(m.prom.paidAt) < paidTTL && !now.Before(m.prom.paidAt) {
		return m.prom.paid
	}
	c, err := m.cfg.Store.PaidOrderCounts(ctx, m.cfg.Store.Conn(), now)
	if err != nil {
		m.log.Warn("monitor: paid orders", "err", err)
		return nil
	}
	m.prom.paid, m.prom.paidAt = c, now
	return c
}

// Exposition is the body of GET /metrics.
func (m *Monitor) Exposition(ctx context.Context) []byte {
	now := m.now()
	p := m.probe(ctx)
	cv := m.collector(now)
	out := newPromText()

	out.add(promBuildInfo, 1, "version", m.cfg.Version)
	out.add(promStartTime, float64(m.start.Unix()))

	// The services core asks, and its database.
	if p.report != nil {
		for _, sv := range p.report.Services {
			switch sv.Status {
			case syshealth.StatusOK:
				out.add(promServiceUp, 1, "service", sv.Name)
				out.add(promServiceResponse, float64(sv.LatencyMS)/1000, "service", sv.Name)
			case syshealth.StatusNotReady, syshealth.StatusDown:
				out.add(promServiceUp, 0, "service", sv.Name)
			}
		}
	}
	if m.cfg.Store != nil {
		out.add(promDatabaseUp, promBool(p.dbOK))
		if p.dbOK {
			out.add(promDatabaseResponse, p.dbLatency.Seconds())
		}
	}

	// The queues and the sales.
	if c := p.counters; c != nil {
		out.add(promQueue, float64(c.OutboxBacklog), "queue", "outbox")
		out.add(promQueue, float64(c.ProvisionQueue), "queue", "provisioning")
		out.add(promQueue, float64(c.OrdersPending), "queue", "pending_orders")
	}
	if p.pending >= 0 {
		out.add(promQueue, float64(p.pending), "queue", "pending_payments")
	}
	if paid := m.paidOrders(ctx, now); paid != nil {
		out.add(promOrdersPaid, float64(paid.Hour), "window", "1h")
		out.add(promOrdersPaid, float64(paid.Day), "window", "24h")
		out.add(promOrdersPaid, float64(paid.Week), "window", "7d")
	}
	if p.alertsOK {
		var critical, warning int
		for _, a := range p.alerts {
			if a.Severity == "critical" {
				critical++
			} else {
				warning++
			}
		}
		out.add(promAlertsOpen, float64(critical), "severity", "critical")
		out.add(promAlertsOpen, float64(warning), "severity", "warning")
	}

	// The panel servers: whether they answer, how many services they hold and their limit.
	if p.servers != nil {
		for i, sv := range p.servers.Servers {
			if i >= maxPromServers {
				break
			}
			if sv.Enabled && sv.CheckedAt > 0 {
				out.add(promPanelUp, promBool(sv.Healthy), "server", sv.Name)
			}
			out.add(promPanelClients, float64(sv.Clients), "server", sv.Name)
			if sv.MaxClients > 0 {
				out.add(promPanelLimit, float64(sv.MaxClients), "server", sv.Name)
			}
		}
	}

	// The license, as whole days to its end.
	if l := p.license; l != nil {
		switch {
		case (l.Basis == license.BasisLicense || l.Basis == license.BasisGrace) && l.License != nil && l.License.Token != nil:
			out.add(promLicenseDays, daysLeft(l.License.Token.ExpiresAt, now), "basis", "license")
		case l.Basis == license.BasisTrial && !l.TrialEndsAt.IsZero():
			out.add(promLicenseDays, daysLeft(l.TrialEndsAt, now), "basis", "trial")
		}
	}

	// The newest backup.
	if b := p.backups; b != nil {
		if b.LastCreatedAt != nil {
			out.add(promBackupAge, math.Max(now.Sub(*b.LastCreatedAt).Seconds(), 0))
		}
		if b.LastCreatedAt != nil || b.LastError != "" {
			out.add(promBackupOK, promBool(b.LastError == ""))
		}
	}

	// The server, as the host collector measured it (only while its snapshot is fresh).
	if cv.Configured && cv.Installed {
		out.add(promCollectorUp, promBool(cv.Fresh))
	}
	if h := hostOf(cv.Snap); h != nil {
		if h.CPU.Pct != nil {
			out.add(promHostCPU, *h.CPU.Pct/100)
		}
		out.add(promHostLoad1, h.Load[0])
		if h.Mem.Total > 0 {
			out.add(promHostMemUsed, float64(h.Mem.Used()))
			out.add(promHostMemTotal, float64(h.Mem.Total))
		}
		for i, d := range h.Disks {
			if i >= maxPromMounts {
				break
			}
			out.add(promHostDiskUsed, d.Pct()/100, "mount", d.Mount)
			out.add(promHostDiskFree, float64(d.Avail), "mount", d.Mount)
			out.add(promHostDiskTotal, float64(d.Total), "mount", d.Mount)
		}
		if h.UptimeS > 0 {
			out.add(promHostUptime, float64(h.UptimeS))
		}
	}
	return out.text()
}

// daysLeft is the whole days from now to t, rounded up: 0.2 days left is 1, and a license that ended 2.5 days ago is -2.
func daysLeft(t, now time.Time) float64 { return math.Ceil(t.Sub(now).Hours() / 24) }
