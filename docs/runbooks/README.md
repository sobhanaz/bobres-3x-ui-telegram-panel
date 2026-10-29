# Runbooks

What to do when something goes wrong with a BOBRES store, and how to do the routine jobs that can break it. Each
runbook says how the problem shows, what to check (exact commands), how to fix it, and when to escalate.

| Runbook | Use it when |
|---|---|
| [Payment stuck](payment-stuck.md) | A customer paid but the order still waits, a receipt waits for review, or a Stars payment was not recorded |
| [Provisioning backlog](provisioning-backlog.md) | Paid orders are not delivered: "Failed deliveries" on the Overview, the "Provisioning failed" alert |
| [3x-ui down](xui-down.md) | A 3x-ui server shows "Not answering", or its token was refused |
| [Bot token revoked](bot-token-revoked.md) | The bot does not answer and its log says Telegram rejected the token |
| [Disk full](disk-full.md) | Low disk in `bobres status`, backups refused for space, the database cannot write |
| [Backups not arriving](backups-not-arriving.md) | No daily backup in the owner's Telegram, or the System page shows a failed backup |
| [Rotate secrets](rotate-secrets.md) | A token, password or key leaked, or you rotate them on a schedule; the master key has its own command, `bobres rotate-key` |
| [Dashboard sign-in approval](login-approval.md) | A staff sign-in waits for an approval in Telegram that cannot come (the bot is down, Telegram cannot be reached), or you want to know what the option does and how to switch it off |
| [License](license.md) | The License tab is not "Active", or an action is refused by the license |
| [Vendor license server](vendor-license-server.md) | For the **seller**, not an operator: deploy the license server, sell, renew and revoke licenses, back up and rotate the signing key |
| [HTTPS certificates](https.md) | Choose the domain's, the server IP address's or your own certificate; a certificate alert; browsers refuse the dashboard after a change |
| [Restore a backup](restore.md) | Put the store back to a backup, or move it to a new server |
| [Update and roll back](update-rollback.md) | Install a new release, or undo the last one |
| [Uninstall and start over](uninstall.md) | Take BOBRES off a server so that it is as on day one (with a final backup), move the store to another server, or start again from nothing |
| [Releases and update channels](releases.md) | Publish a stable or beta release, re-sign a quiet stable channel (signatures expire after 45 days), or move a store between stable and beta |
| [Monitoring](monitoring.md) | The Monitoring tab (System page) has no server figures, a monitoring alert arrived (disk, memory, a container, the collector, CI of `main`), you want host monitoring off, or you want to read the figures from Prometheus and Grafana, or to see how available each service and panel server was and publish a status page |

## System alerts in Telegram

Core checks the store every minute and the bot messages the staff who get alerts (the owner, or every owner and admin
with **Settings → Staff notifications → Recipients**; never support staff) when something goes wrong, again when it is
fixed, and about a problem still open after 6 hours, then once a day. A problem must be seen twice in a row (a minute
apart), so a service restarting during an update is not announced. It is called fixed only once it has passed for 5
minutes, and a problem back within 30 minutes of its fix is announced only if it still fails when those 30 minutes are
over, so a check that comes and goes sends one alert, not one every few minutes. The open problems also show on the dashboard's
**System** page, **Health** tab. **Settings → Staff notifications → System problems** turns the messages off (the
checks and the System page go on). The bot cannot send alerts about itself while it is down: they arrive, with the
"Fixed" message, once it is back.

Core's log names each check by its key (`bobres logs --tail 300 core | grep 'system alert'`):

| Alert | Key | Runbook |
|---|---|---|
| A service is down or not ready | `service.<name>` | First look (below), then the runbook of that service's problem |
| A 3x-ui server does not answer | `server.<id>` | [3x-ui down](xui-down.md) |
| A 3x-ui server with a service limit is at 90% of it or full (one notice a day; raising the limit ends it quietly) | `server.<id>.capacity` | [Provisioning backlog](provisioning-backlog.md) (a plan whose servers are all full waits) |
| Zarinpal payments are failing, or one has been open for over 3 hours | `gateway.zarinpal.error`, `gateway.zarinpal.open` | [Payment stuck](payment-stuck.md) |
| Automatic USDT payments are not being checked (no read of the store's TRON address from TronGrid has worked for 5 minutes), or one has been open for over 2 hours | `gateway.usdt_auto.read`, `gateway.usdt_auto.open` | [Payment stuck](payment-stuck.md) |
| The last backup failed, or no new backup for over 26 hours | `backup.failed`, `backup.stale` | [Backups not arriving](backups-not-arriving.md) |
| The latest backup was not copied to the off-site (S3) storage, or its settings cannot be used | `backup.offsite` | [Backups not arriving](backups-not-arriving.md#a-backup-is-made-but-not-copied-to-s3) |
| The disk is almost full (under 2 GB) | `backup.disk` | [Disk full](disk-full.md) |
| Paid orders could not be delivered in the last hour | `orders.provision_failed` | [Provisioning backlog](provisioning-backlog.md) |
| The bot is behind on its messages (over 10 minutes) | `events.bot_lag` | `bobres logs --tail 200 bot`; [Bot token revoked](bot-token-revoked.md) if Telegram refuses the token |
| The license expires soon, is in its grace period or has expired | `license.expiry` | [License](license.md) |
| A new purchase was refused: the install is at its limit of active services (one notice a day) | `license.limit` | [License](license.md) |
| Caddy, the HTTPS gateway, has answered on none of the store's addresses for 3 minutes | `tls.caddy` | [HTTPS certificates](https.md#alerts) |
| An HTTPS certificate is close to its end (renewal failing, or an own certificate not replaced), or an address has served no valid certificate for 30 minutes | `tls.<role>.expiring`, `tls.<role>.failing` (`<role>`: `domain`, `ip`, `zarinpal`) | [HTTPS certificates](https.md#alerts) |
| The saved HTTPS setting is not in use | `tls.setting` | [HTTPS certificates](https.md#an-own-certificate-after-a-domain-change-or-a-new-master-key) |
| The HTTPS applier (`tls-agent`) has not answered core for 5 minutes | `tls.agent` | [HTTPS certificates, other problems](https.md#other-problems) |
| The disk is more than 85% full (a monitored filesystem; the threshold is a setting), or memory has been above 90% for 10 minutes | `monitor.disk:<mount>`, `monitor.memory` | [Monitoring](monitoring.md#an-alert), [Disk full](disk-full.md) |
| A container of the stack is not running, reports unhealthy, or keeps restarting (3 restarts in 15 minutes) | `monitor.container.<service>.down`, `.unhealthy`, `.restarting` | [Monitoring](monitoring.md#an-alert) |
| The host collector has sent no snapshot for 5 minutes, or the latest CI run on `main` failed (off unless `monitor.ci_alert` is on) | `monitor.collector`, `monitor.ci.main` | [Monitoring](monitoring.md) |

## Before you start

All commands run on the server as root. The install is in `/opt/bobres` unless you installed it elsewhere: then add
`--dir <dir>` to every `bobres` command and change the paths below.

Some checks talk to Docker Compose or the database directly. Define these two helpers once per shell session (paste
them as they are):

```sh
dc() { docker compose --project-name bobres -f /opt/bobres/docker-compose.yml --env-file /opt/bobres/.env "$@"; }
bsql() { dc exec -T postgres psql -U bobres -d bobres -c "$1"; }
```

`dc` is `docker compose` for this install (`dc ps`, `dc restart core`). `bsql "SELECT ..."` runs one query as the
database's owner inside the postgres container (over its local socket, so no password is typed or shown). The
runbooks only **read** with it: never change rows by hand. Every fix goes through the dashboard, the bot or `bobres`,
which keep the ledger, the audit log and the events consistent.

Never run `docker compose config`, `docker inspect` or `env` in a container when someone is watching your screen or
recording it: they print the secrets. Never prune Docker volumes (`docker volume prune`, `docker system prune
--volumes`): depending on the Docker version, and once the containers are removed (`bobres uninstall`), they delete
the database.

## First look, whatever the problem

1. `bobres status`: every service, its state and health, the version and the free disk. Exit 1 means something
   needs attention.
2. The dashboard's **System** page, **Health** tab: each service's version, health and readiness checks (Database,
   gRPC, Redis), the 3x-ui servers and the payment methods. The **Overview** page shows failed deliveries, payments to
   review, the servers and the gateways at a glance.
3. The logs of the service involved, newest last:

   ```sh
   bobres logs --tail 100 core          # or bot, payments, provisioner, backup, postgres, redis, caddy, tls-agent
   bobres logs --tail 300 payments | grep -i -E 'warn|error'
   ```

   (Flags go before the service names.) The owner also sees the newest warnings and errors of every service on the
   System page, **Logs** tab.
4. The server itself: `bobres doctor` (RAM, disk, Docker; on a running store Caddy holds ports 80 and 443, so those
   two checks fail: that is expected).

If the bot is down, staff get into the dashboard with a one-time link printed on the server:
`bobres admin link` (or `bobres admin link <telegram id>` for another staff member).

| Service | Does | When it is down |
|---|---|---|
| caddy | HTTPS for the dashboard, payment callbacks and the webhook | Dashboard and Zarinpal returns unreachable |
| tls-agent | Applies HTTPS changes to Caddy (it has no network; releases from 0.1.0-rc.2 on) | The store keeps its current certificates; changes in the HTTPS tab are refused |
| postgres | The database (schemas core, payments, provisioner) | Everything fails its readiness check |
| redis | The bot's conversation state and rate limits | The bot cannot answer |
| core | Users, plans, orders, wallet, delivery worker, dashboard | Nothing is sold or delivered; the dashboard is down |
| bot | Telegram: customers, staff alerts, delivering backups | Customers get no answer; no alerts, no backups in Telegram |
| payments | Zarinpal, Stars, automatic USDT, manual receipts; checks open payments every 30 s | Online payments are not confirmed (they are once it is back) |
| provisioner | The only service that talks to the 3x-ui panels | Paid orders wait; they are delivered once it is back |
| backup | The daily encrypted backup (when the backup profile is on) | No backups |

## Escalate

Each runbook says when to escalate. Make a support bundle and send it with what you saw and what you did:

```sh
sudo bobres support-bundle
```

It writes `/opt/bobres/bobres-support-<time>.tar.gz` (root only): the versions, `docker compose ps`, the doctor's
checks, the newest 300 log lines of every service, the names of the `.env` keys (never their values), disk and
memory, and the list of backups. Everything in it is redacted (tokens, passwords, keys, Zarinpal merchant ids, long
hex or base64 strings), and it never holds the database, a backup or a secret value. Visitors' data is removed too:
browsers' User-Agent, and every public IP address becomes `[IP-1]`, `[IP-2]`... (the same address keeps its number;
private and Docker addresses stay). The logs can still show Telegram ids, usernames, order ids, panel host names and
which pages were opened when: read it before you send it (`tar -xzf <file> -C <folder>`), and delete it once support
is done. `--offline` skips the doctor's network checks.

Never send `.env`, a `.bkp` file, the backup key or anything from `/opt/bobres/.restore-point/files/`.
