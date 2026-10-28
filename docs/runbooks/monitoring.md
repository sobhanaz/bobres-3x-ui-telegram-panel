# Monitoring

Use this when the dashboard's **System → Monitoring** tab shows no server figures ("the host collector is not installed" or "sent nothing for
N minutes"), when a monitoring alert arrives (disk, memory, a container, the collector, CI of `main`), when you want to turn
host monitoring off, or when you want to read the figures from Prometheus and Grafana ([Prometheus and Grafana](#prometheus-and-grafana)). How it is
built: [installer, "Host monitoring"](../06-installer.md); what it exposes:
[security, "Host monitoring"](../07-security.md); the tab and its API: [dashboard, "Monitoring API"](../04-dashboard.md).

Run the commands on the server as root. See the README's "Before you start" for the `dc` helper used below.

## What the page needs

Everything on the page except the server and the containers comes from core itself (the services, the database, the 3x-ui servers, the
gateways, the queues, the bot, the CI of `main`). The **server, the containers, Docker's disk use and the host's journal** come from a
small root timer on the host, `bobres-monitor.timer`, that writes `/opt/bobres/run/monitor/snapshot.json` every 30 seconds. Core
reads it through a read-only mount. Without a fresh snapshot (younger than 90 seconds) the page says "no data" for those parts and
the graphs have a gap; it never shows zeros.

## No server figures on the page

1. `sudo bobres monitor status`. It says which of these it is:
   - **`not installed`**: the collector is off or was never put in place. `sudo bobres monitor enable` (it also makes the folder). If
     `BOBRES_MONITOR_COLLECTOR=off` is in `.env` somebody switched it off on purpose (`sudo bobres monitor enable` turns it on again).
   - **units there, timer not active**: `sudo systemctl enable --now bobres-monitor.timer`, then `sudo bobres monitor status`. If
     systemd refuses, `systemctl status bobres-monitor.timer` says why.
   - **the last run failed**: `journalctl -u bobres-monitor.service -n 30 --no-pager`. A run that takes longer than 30 seconds is killed:
     a hung Docker engine does that (`systemctl status docker`, `docker ps`). `sudo bobres monitor snapshot --print` runs it once by hand
     and shows what it could not read (`errors`).
   - **a snapshot but `fresh: false`**: the timer is not firing, or the clock jumped. `systemctl list-timers bobres-monitor.timer`.
2. The snapshot is there and fresh, but the page still says it is missing: **the compose file is from before host monitoring**
   (`bobres monitor enable` says so). Run `sudo bobres update` (or `sudo bobres install`) once: it adds the read-only mount
   `./run/monitor:/run/bobres-monitor:ro` and `BOBRES_MONITOR_DIR` to core. Check with
   `grep -c run/bobres-monitor /opt/bobres/docker-compose.yml` (0 means the file is from before host monitoring).
3. `docker` shows `"ok": false` on the page: the collector could not talk to the engine (`error` says why: "permission denied" would
   mean it is not running as root; "no such socket" a rootless or remote Docker, which the collector does not read). Containers
   are then not listed.
4. There is no systemd on this server (a container, WSL): the collector cannot run; the page works without it.

## An alert

All of them are announced like the other alerts and listed under **System → Health → open problems** (the README explains the debounce).
The thresholds are in **Settings → Monitoring**.

| Alert | Key | What to do |
|---|---|---|
| The disk at `<mount>` is N% full | `monitor.disk:<mount>` | [Disk full](disk-full.md). `docker system df` shows what Docker holds (`docker image prune`, never `volume prune`); the page's **Server** section lists what is reclaimable |
| Memory above N% for M minutes | `monitor.memory` | The **Containers** section shows which container grows. `docker stats --no-stream`; `free -m`. A container with `oom_killed` was killed for lack of memory. Add swap or memory, or find the leak; restarting the container buys time |
| The container `<service>` is not running | `monitor.container.<service>.down` | `sudo bobres start` starts what is stopped (the alert says so). Then `dc ps -a`, `dc logs --tail 100 <service>` for why. After a **reboot** Docker can leave one container down when it has marked it "manually stopped" (Caddy did, once, until 0.17.1: the installer signalled it through `docker kill`); `bobres.service` starts the store at every boot from 0.17.1 on, and `bobres check` shows its **Start at boot** line. For `caddy`: [HTTPS certificates](https.md). For `postgres`: [Restore](restore.md) only if the data is damaged |
| The container `<service>` reports that it is unhealthy | `monitor.container.<service>.unhealthy` | `sudo bobres logs --tail 100 <service>` (the alert says so). For `tls-agent`: [HTTPS certificates](https.md#other-problems). A `tls-agent` that stays unhealthy after a reboot, with nothing wrong in its log, is the 0.17.0 heartbeat in `/tmp` that a restarted container cannot write: `docker compose up -d --force-recreate --no-deps tls-agent` in `/opt/bobres` clears it, and `sudo bobres update` to 0.17.1 fixes it for good |
| The container `<service>` keeps restarting | `monitor.container.<service>.restarting` | A crash loop. `dc logs --tail 200 <service>`; for the bot [Bot token revoked](bot-token-revoked.md); for others `bobres doctor` and the last `bobres update` ([Update and roll back](update-rollback.md)) |
| The server monitor has sent no snapshot for N minutes | `monitor.collector` | "No server figures on the page" above |
| The latest CI run on `main` failed | `monitor.ci.main` | Only raised if you turned on `monitor.ci_alert`, for a store built from `main`. Open the run (the page and the alert carry its commit) and fix or revert. It clears by itself when the next run passes |

None of these alerts is acted on by the system: nothing restarts a container or frees disk by itself.

## The CI/CD panel says "unreachable" or "rate limited"

The bot, which has the internet access, reads GitHub's public API anonymously (60 requests an hour per address, cached for
5 minutes, conditional requests that cost nothing when nothing changed). **unreachable**: the bot cannot reach `api.github.com` (an
outbound firewall, a proxy, GitHub down); the page keeps the last facts it had. **rate limited**: other programs on this server's
address used the quota; it comes back at the time the page shows (`rate_reset_at`). Nothing else depends on this panel.

## Turning it off

- **Host figures only:** `sudo bobres monitor disable`. It removes the two units and the snapshot and keeps the choice through updates
  (`BOBRES_MONITOR_COLLECTOR=off` in `.env`). `sudo bobres monitor enable` brings it back.
- **All monitoring in core** (no samples, no alerts): **Settings → Monitoring → Monitoring** off. What was recorded stays and can still
  be read; the alerts it had raised are forgotten without a message.

## The history

Core keeps 30-second samples for 7 days and 5-minute averages up to 30 days in its own database (a few megabytes) and includes
them in every backup. Nothing to maintain: core rolls them up and prunes them itself. If a graph has a gap, nothing was sampled
then (core was down, or the source was unreachable); it is a gap, not a zero.

## Prometheus and Grafana

Core can give the same figures to Prometheus at `GET /metrics` on the store's own domain (`https://shop.example.com/metrics`), in the
Prometheus text format 0.0.4. It is **off** until the owner makes a token, and then it answers only a request that carries it.

**Turn it on.** As the owner: **Settings → Monitoring → Prometheus metrics → Make a token**. The token is shown once, with the scrape
configuration below filled in for your domain. Core keeps only its SHA-256, so a token that was not copied is made again, not looked up.
You can also type a token of your own (24 to 128 characters of letters, digits and `. _ ~ + / = -`). **Make a new token** replaces it
at once and **Turn off** removes it (`/metrics` is then a plain 404 again). Making, replacing and removing it are audited
(`metrics.token.generate`, `metrics.token.set`, `metrics.token.clear`) without the token. Admins see whether it is on; only the owner changes it.

**Connect Prometheus.** Save the token in a file only Prometheus can read, for example `/etc/prometheus/bobres-metrics-token`, and add:

```yaml
scrape_configs:
  - job_name: bobres
    scheme: https
    metrics_path: /metrics
    scrape_interval: 30s
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/bobres-metrics-token
    static_configs:
      - targets: ["shop.example.com"]
```

Check by hand from the Prometheus server before reloading it:

```sh
curl -s -H "Authorization: Bearer $(cat /etc/prometheus/bobres-metrics-token)" https://shop.example.com/metrics | head -20
```

| You get | Meaning |
|---|---|
| `404` | No token is set: `/metrics` is off. Make one in Settings → Monitoring |
| `401` with `WWW-Authenticate: Bearer` | The token is missing or wrong (a token that was replaced stops working at once). More than 30 wrong tries a minute from one address get `429` |
| `200`, `Content-Type: text/plain; version=0.0.4` | Working |
| `200` but a figure is missing | By design: a figure core could not read (the collector is not installed, the backup service is off) is left out, never written as 0 |

A scrape costs what the Monitoring tab costs: core asks the other services at most once every 10 seconds however many readers there are,
so 15 to 60 seconds between scrapes is plenty. Scraping does not need the **Monitoring** setting to be on (it reads the state now, not
the history). What `/metrics` holds, all gauges:

| Metric | Labels | What |
|---|---|---|
| `bobres_build_info` | `version` | Always 1; the version of core |
| `bobres_core_start_time_seconds` | | When core started (unix seconds) |
| `bobres_service_up` | `service` (`core`, `bot`, `payments`, `provisioner`, `backup`) | 1 answers its health check and is ready, 0 down or not ready |
| `bobres_service_response_seconds` | `service` | How long its health check took, while it answers |
| `bobres_database_up`, `bobres_database_response_seconds` | | Whether core's database answers, and how fast |
| `bobres_queue_size` | `queue` (`outbox`, `pending_payments`, `provisioning`, `pending_orders`) | Events not yet read by the slowest consumer; manual payments waiting for a review; paid orders waiting for the panel; orders not yet paid |
| `bobres_orders_paid` | `window` (`1h`, `24h`, `7d`) | Orders paid in that window: VPN services, licenses and digital accounts, as the Reports page counts sales (the "start from zero" date does not apply) |
| `bobres_alerts_open` | `severity` (`critical`, `warning`) | Open system alerts |
| `bobres_panel_up` | `server` | 1 passed its last health check, 0 failed; a server that is off or never checked is left out |
| `bobres_panel_clients` | `server` | Services on the panel server, deliveries in progress included |
| `bobres_panel_clients_limit` | `server` | Its service limit; left out when it has none |
| `bobres_license_days_left` | `basis` (`license`, `trial`) | Whole days until the license or the free trial ends (negative after a license ended); nothing on the free tier |
| `bobres_backup_age_seconds`, `bobres_backup_last_run_ok` | | Age of the newest good backup; 1 if the last run worked, 0 if it failed |
| `bobres_host_collector_up` | | 1 the host collector's snapshot is fresh, 0 it is not; left out where the collector is not installed |
| `bobres_host_cpu_usage_ratio`, `bobres_host_load1`, `bobres_host_uptime_seconds` | | The server, as the collector measured it (CPU from 0 to 1) |
| `bobres_host_memory_used_bytes`, `bobres_host_memory_total_bytes` | | Memory |
| `bobres_host_disk_used_ratio`, `bobres_host_disk_free_bytes`, `bobres_host_disk_total_bytes` | `mount` | One filesystem (at most 16): how full it is as `df` shows it (from 0 to 1, what the disk alert uses), the bytes available, and its size |

**What it never holds:** a customer, a Telegram id, an order, an address, a token or a key, an error text, or a path. The labels are fixed
words (`service`, `queue`, `window`, `severity`, `basis`), the version, a mount point from the snapshot, and the panel servers' **names** as
you typed them; nothing grows with the number of customers.

**Alert rules to start from** (Prometheus `rules:` file):

```yaml
groups:
  - name: bobres
    rules:
      - alert: BobresNotScraped
        expr: up{job="bobres"} == 0
        for: 5m
      - alert: BobresServiceDown
        expr: bobres_service_up == 0
        for: 3m
        annotations: {summary: "{{ $labels.service }} is not answering"}
      - alert: BobresPanelDown
        expr: bobres_panel_up == 0
        for: 5m
        annotations: {summary: "The panel server {{ $labels.server }} failed its health check"}
      - alert: BobresServerNearlyFull
        expr: bobres_panel_clients / bobres_panel_clients_limit >= 0.9
        for: 10m
        annotations: {summary: "{{ $labels.server }} is at 90% of its service limit"}
      - alert: BobresDeliveriesWaiting
        expr: bobres_queue_size{queue="provisioning"} > 0
        for: 30m
      - alert: BobresBackupOld
        expr: bobres_backup_age_seconds > 26 * 3600
        for: 10m
      - alert: BobresLicenseEnding
        expr: bobres_license_days_left < 14
        for: 1h
      - alert: BobresDiskFull
        expr: bobres_host_disk_used_ratio > 0.9
        for: 10m
        annotations: {summary: "{{ $labels.mount }} is over 90% full"}
```

**A Grafana dashboard to import** (Dashboards → New → Import; pick your Prometheus data source). It is a start, not a promise: change it freely.

<!-- grafana-dashboard -->
```json
{
  "__inputs": [{"name": "DS_PROMETHEUS", "label": "Prometheus", "type": "datasource", "pluginId": "prometheus", "pluginName": "Prometheus"}],
  "title": "BOBRES store",
  "uid": "bobres-store",
  "tags": ["bobres"],
  "schemaVersion": 39,
  "version": 1,
  "editable": true,
  "refresh": "1m",
  "time": {"from": "now-24h", "to": "now"},
  "panels": [
    {"id": 1, "type": "stat", "title": "Services", "gridPos": {"h": 4, "w": 4, "x": 0, "y": 0}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "min(bobres_service_up)"}],
     "fieldConfig": {"defaults": {"mappings": [{"type": "value", "options": {"0": {"text": "Something is down"}, "1": {"text": "All up"}}}],
       "thresholds": {"mode": "absolute", "steps": [{"color": "red", "value": null}, {"color": "green", "value": 1}]}}, "overrides": []}},
    {"id": 2, "type": "stat", "title": "Critical alerts", "gridPos": {"h": 4, "w": 4, "x": 4, "y": 0}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_alerts_open{severity=\"critical\"}"}],
     "fieldConfig": {"defaults": {"thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": null}, {"color": "red", "value": 1}]}}, "overrides": []}},
    {"id": 3, "type": "stat", "title": "Warnings", "gridPos": {"h": 4, "w": 4, "x": 8, "y": 0}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_alerts_open{severity=\"warning\"}"}],
     "fieldConfig": {"defaults": {"thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": null}, {"color": "orange", "value": 1}]}}, "overrides": []}},
    {"id": 4, "type": "stat", "title": "License days left", "gridPos": {"h": 4, "w": 4, "x": 12, "y": 0}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_license_days_left"}],
     "fieldConfig": {"defaults": {"unit": "d", "thresholds": {"mode": "absolute", "steps": [{"color": "red", "value": null}, {"color": "orange", "value": 14}, {"color": "green", "value": 30}]}}, "overrides": []}},
    {"id": 5, "type": "stat", "title": "Newest backup", "gridPos": {"h": 4, "w": 4, "x": 16, "y": 0}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_backup_age_seconds"}],
     "fieldConfig": {"defaults": {"unit": "s", "thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": null}, {"color": "orange", "value": 93600}, {"color": "red", "value": 100800}]}}, "overrides": []}},
    {"id": 6, "type": "stat", "title": "Paid orders, 24 hours", "gridPos": {"h": 4, "w": 4, "x": 20, "y": 0}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_orders_paid{window=\"24h\"}"}], "fieldConfig": {"defaults": {}, "overrides": []}},
    {"id": 7, "type": "timeseries", "title": "Service response time", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 4}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_service_response_seconds", "legendFormat": "{{service}}"}], "fieldConfig": {"defaults": {"unit": "s"}, "overrides": []}},
    {"id": 8, "type": "timeseries", "title": "Queues", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 4}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_queue_size", "legendFormat": "{{queue}}"}], "fieldConfig": {"defaults": {}, "overrides": []}},
    {"id": 9, "type": "bargauge", "title": "Panel servers: how full", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 12}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_panel_clients / bobres_panel_clients_limit", "legendFormat": "{{server}}"}],
     "fieldConfig": {"defaults": {"unit": "percentunit", "min": 0, "max": 1, "thresholds": {"mode": "absolute", "steps": [{"color": "green", "value": null}, {"color": "orange", "value": 0.9}, {"color": "red", "value": 1}]}}, "overrides": []}},
    {"id": 10, "type": "timeseries", "title": "Panel servers answering", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 12}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_panel_up", "legendFormat": "{{server}}"}], "fieldConfig": {"defaults": {"min": 0, "max": 1}, "overrides": []}},
    {"id": 11, "type": "timeseries", "title": "Server: CPU and memory", "gridPos": {"h": 8, "w": 12, "x": 0, "y": 20}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_host_cpu_usage_ratio", "legendFormat": "CPU"}, {"refId": "B", "expr": "bobres_host_memory_used_bytes / bobres_host_memory_total_bytes", "legendFormat": "Memory"}],
     "fieldConfig": {"defaults": {"unit": "percentunit", "min": 0, "max": 1}, "overrides": []}},
    {"id": 12, "type": "timeseries", "title": "Server: disks", "gridPos": {"h": 8, "w": 12, "x": 12, "y": 20}, "datasource": {"type": "prometheus", "uid": "${DS_PROMETHEUS}"},
     "targets": [{"refId": "A", "expr": "bobres_host_disk_used_ratio", "legendFormat": "{{mount}}"}],
     "fieldConfig": {"defaults": {"unit": "percentunit", "min": 0, "max": 1}, "overrides": []}}
  ]
}
```

A test keeps this page and the code in step: every `bobres_` name used above is one `/metrics` writes, and the dashboard is valid JSON.
