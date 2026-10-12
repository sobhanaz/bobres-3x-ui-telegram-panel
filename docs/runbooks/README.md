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
| [Rotate secrets](rotate-secrets.md) | A token, password or key leaked, or you rotate them on a schedule |
| [License](license.md) | The License tab is not "Active", or an action is refused by the license |
| [Restore a backup](restore.md) | Put the store back to a backup, or move it to a new server |
| [Update and roll back](update-rollback.md) | Install a new release, or undo the last one |

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
   bobres logs --tail 100 core          # or bot, payments, provisioner, backup, postgres, redis, caddy
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
| postgres | The database (schemas core, payments, provisioner) | Everything fails its readiness check |
| redis | The bot's conversation state and rate limits | The bot cannot answer |
| core | Users, plans, orders, wallet, delivery worker, dashboard | Nothing is sold or delivered; the dashboard is down |
| bot | Telegram: customers, staff alerts, delivering backups | Customers get no answer; no alerts, no backups in Telegram |
| payments | Zarinpal, Stars, manual receipts; checks open payments every 30 s | Online payments are not confirmed (they are once it is back) |
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
