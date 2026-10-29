package domain

import (
	"strconv"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Per-server capacity (docs/04-dashboard.md, "Servers"). A 3x-ui or Marzban server may be given a limit on
// its services (Server.MaxClients; 0 is none). The provisioner keeps to it: it passes over a full server when
// it picks one for a new service and refuses one that is named. Core only shows how full each server is and,
// through the watchdog, tells the staff when one is nearly or entirely full, before an order has to wait.

// CapacityWarnPct is the share of its limit at which a server counts as nearly full: the Servers page's
// warning and the watchdog's notice start here.
const CapacityWarnPct = 90

// Alert texts of the two states (the bot's alert.server_capacity and alert.server_full).
const (
	alertServerCapacity = "alert.server_capacity"
	alertServerFull     = "alert.server_full"
)

// ServerFill says how full a server is: its services against its limit. ok is false for a server with no
// limit. pct is the whole percent it is at (more than 100 for a server whose limit was set below what it holds).
func ServerFill(sv *Server) (pct int, full, ok bool) {
	if sv == nil || sv.MaxClients <= 0 {
		return 0, false, false
	}
	pct = int(min(sv.Clients*100/int64(sv.MaxClients), 1_000_000))
	return pct, sv.Clients >= int64(sv.MaxClients), true
}

// capacityCheck is the watchdog's notice about a server: for a server that takes new services and has a
// limit, a notice that is open from CapacityWarnPct on, and a different text once it is full. It is a daily
// notice: announced when it starts and then once a day while it lasts, and its end is not announced (a server
// that has room again, or no limit, is only forgotten). A server with no limit, or one switched off, has
// no check.
func capacityCheck(sv *Server) (alertCheck, bool) {
	pct, full, limited := ServerFill(sv)
	if !limited || !sv.Enabled {
		return alertCheck{}, false
	}
	c := alertCheck{key: "server." + sv.ID + ".capacity", problem: pct >= CapacityWarnPct, severity: events.SeverityWarning, daily: true,
		title: alertServerCapacity, args: map[string]string{"server": sv.Name, "percent": strconv.Itoa(pct),
			"count": strconv.FormatInt(sv.Clients, 10), "limit": strconv.FormatInt(int64(sv.MaxClients), 10)}}
	if full {
		c.title = alertServerFull
	}
	return c, true
}
