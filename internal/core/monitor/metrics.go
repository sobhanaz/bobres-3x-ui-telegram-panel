package monitor

import (
	"regexp"
	"strings"
)

// The metrics core records (docs/04-dashboard.md, "Monitoring"). A metric is a name, optionally
// followed by ":" and what it is about (a mount point, a Compose service, a server's id):
//
//	host.cpu_pct host.load1 host.mem_pct host.swap_pct host.disk_pct:<mount>
//	host.net_rx_bps host.net_tx_bps
//	container.cpu_pct:<service> container.mem_bytes:<service> container.restarts:<service>
//	core.goroutines core.heap_bytes core.db_conns core.db_size_bytes core.errors_per_min core.warnings_per_min
//	queue.provisioning queue.outbox queue.pending_orders queue.pending_payments
//	bot.updates_per_min bot.errors_per_min bot.warnings_per_min bot.updates_queued bot.goroutines bot.heap_bytes
//	service.up:<name> service.latency_ms:<name> panel.up:<id> gateway.up:<provider> license.ok
const (
	HostCPU      = "host.cpu_pct"
	HostLoad1    = "host.load1"
	HostMem      = "host.mem_pct"
	HostSwap     = "host.swap_pct"
	HostDisk     = "host.disk_pct"
	HostNetRx    = "host.net_rx_bps"
	HostNetTx    = "host.net_tx_bps"
	ContainerCPU = "container.cpu_pct"
	ContainerMem = "container.mem_bytes"
	ContainerRst = "container.restarts"

	CoreGoroutines = "core.goroutines"
	CoreHeap       = "core.heap_bytes"
	CoreDBConns    = "core.db_conns"
	CoreDBSize     = "core.db_size_bytes"
	CoreErrors     = "core.errors_per_min"
	CoreWarnings   = "core.warnings_per_min"

	QueueProvisioning = "queue.provisioning"
	QueueOutbox       = "queue.outbox"
	QueueOrders       = "queue.pending_orders"
	QueuePayments     = "queue.pending_payments"

	BotUpdates    = "bot.updates_per_min"
	BotErrors     = "bot.errors_per_min"
	BotWarnings   = "bot.warnings_per_min"
	BotQueued     = "bot.updates_queued"
	BotGoroutines = "bot.goroutines"
	BotHeap       = "bot.heap_bytes"

	ServiceUp      = "service.up"
	ServiceLatency = "service.latency_ms"
	PanelUp        = "panel.up"
	GatewayUp      = "gateway.up"
	LicenseOK      = "license.ok"
)

// withSubject is a metric name with what it is about.
func withSubject(metric, subject string) string { return metric + ":" + subject }

// info is what the page needs to draw a metric.
type info struct {
	unit string
	// agg is how the samples of a bucket become one point: "avg" (default), "min" (an up/down metric
	// shows any downtime) or "max" (a counter that only grows).
	agg string
}

var infos = map[string]info{
	HostCPU: {"percent", "avg"}, HostLoad1: {"load", "avg"}, HostMem: {"percent", "avg"}, HostSwap: {"percent", "avg"},
	HostDisk: {"percent", "avg"}, HostNetRx: {"bytes_per_s", "avg"}, HostNetTx: {"bytes_per_s", "avg"},
	ContainerCPU: {"percent", "avg"}, ContainerMem: {"bytes", "avg"}, ContainerRst: {"count", "max"},
	CoreGoroutines: {"count", "avg"}, CoreHeap: {"bytes", "avg"}, CoreDBConns: {"count", "avg"}, CoreDBSize: {"bytes", "avg"},
	CoreErrors: {"per_min", "avg"}, CoreWarnings: {"per_min", "avg"},
	QueueProvisioning: {"count", "max"}, QueueOutbox: {"count", "max"}, QueueOrders: {"count", "max"}, QueuePayments: {"count", "max"},
	BotUpdates: {"per_min", "avg"}, BotErrors: {"per_min", "avg"}, BotWarnings: {"per_min", "avg"}, BotQueued: {"count", "max"},
	BotGoroutines: {"count", "avg"}, BotHeap: {"bytes", "avg"},
	ServiceUp: {"bool", "min"}, ServiceLatency: {"ms", "avg"}, PanelUp: {"bool", "min"}, GatewayUp: {"bool", "min"}, LicenseOK: {"bool", "min"},
}

// base is a metric's name without its subject.
func base(metric string) string {
	b, _, _ := strings.Cut(metric, ":")
	return b
}

// Unit is a metric's unit: percent, bytes, bytes_per_s, per_min, count, ms, load or bool. "" for a
// name this build does not know (a series of an older or newer build).
func Unit(metric string) string { return infos[base(metric)].unit }

// Agg is how a bucket of samples becomes one point: avg, min or max.
func Agg(metric string) string {
	if a := infos[base(metric)].agg; a != "" {
		return a
	}
	return "avg"
}

// Known reports whether the metric is one this build records.
func Known(metric string) bool { _, ok := infos[base(metric)]; return ok }

// subjected are the metrics that carry a subject.
var subjected = map[string]bool{
	HostDisk: true, ContainerCPU: true, ContainerMem: true, ContainerRst: true,
	ServiceUp: true, ServiceLatency: true, PanelUp: true, GatewayUp: true,
}

// reMetric is the shape of a name a request may ask for: a recorded base name, then optionally ":"
// and a subject of letters, digits and . _ / - (a mount point, a service, an id).
var reMetric = regexp.MustCompile(`^[a-z][a-z0-9_.]{1,40}(:[A-Za-z0-9_./-]{1,100})?$`)

// ValidMetric reports whether name can be asked for: a known base name, with a subject exactly when
// the metric has one.
func ValidMetric(name string) bool {
	if len(name) > 120 || !reMetric.MatchString(name) {
		return false
	}
	b, subject, has := strings.Cut(name, ":")
	if _, ok := infos[b]; !ok {
		return false
	}
	return has == subjected[b] && (!has || subject != "")
}
