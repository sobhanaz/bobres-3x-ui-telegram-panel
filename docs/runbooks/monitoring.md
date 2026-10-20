# Monitoring

Use this when the dashboard's **Monitoring** page shows no server figures ("the host collector is not installed" or "sent nothing for
N minutes"), when a monitoring alert arrives (disk, memory, a container, the collector, CI of `main`), or when you want to turn
host monitoring off. How it is built: [installer, "Host monitoring"](../06-installer.md); what it exposes:
[security, "Host monitoring"](../07-security.md); the page and its API: [dashboard, "Monitoring API"](../04-dashboard.md).

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
| The container `<service>` is not running | `monitor.container.<service>.down` | `dc ps -a`, `dc logs --tail 100 <service>`, then `dc up -d <service>`. For `caddy`: [HTTPS certificates](https.md). For `postgres`: [Restore](restore.md) only if the data is damaged |
| The container `<service>` reports that it is unhealthy | `monitor.container.<service>.unhealthy` | `dc logs --tail 100 <service>`. For `tls-agent`: [HTTPS certificates](https.md#other-problems) |
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
