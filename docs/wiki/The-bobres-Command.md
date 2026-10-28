# The bobres Command

`bobres` is the one command on your server. It installs the store, shows its state, starts and stops it, updates it, backs it up and repairs it. Run it as root (or with `sudo`).

```bash
sudo bobres
```

Without a command, on a terminal, it opens a numbered menu. The header is a live status board: whether the services run (for example `running (9/9 services)`), the license in effect, the HTTPS certificate and its days left, the newest backup, and your dashboard address. Every menu item is also a command. In a script or pipe you get the usage text instead of a prompt.

## Everyday commands

| Command | What it does |
|---|---|
| `bobres status` | Services, version, domain and free disk. Exit code 1 if something is down. `--json` is for scripts |
| `bobres logs [-f] [--tail N] [service...]` | Service logs. Put flags before the service names |
| `bobres start`, `stop`, `restart` | Start (and wait for health), stop (nothing is deleted) or restart the store. After `stop` the store stays stopped through a reboot until you `start` it; otherwise a boot unit starts it at every boot |
| `bobres check` | Every part of the install as PASS, WARN, FAIL or SKIP lines. Exit 1 on a FAIL. `--json` is available |
| `bobres doctor` | Is this server fit for BOBRES: RAM, disk, ports, Docker, DNS, HTTPS, hardening |
| `bobres support-bundle` | A redacted archive for support: versions, status, logs, no secrets |
| `bobres version [--images]`, `bobres about` | The version and the image digests it pins. About adds license and links |

## Update, backup and restore

| Command | What it does |
|---|---|
| `bobres update [--check]` | Update to the latest signed release. Backs up first and rolls back by itself if the new version is not healthy. `--check` only looks. Also `--version X` and `--channel stable\|beta` |
| `bobres rollback` | Undo the last update: the previous version and the database from before it |
| `bobres backup` | Make an encrypted backup now |
| `bobres restore <file>` | Replace the data with a backup. Also `s3://bucket/key`, `--key-file F`, `--yes` |

Details are in [Updates, Backups, Restore](Updates-Backups-Restore).

## Access and owners

| Command | What it does |
|---|---|
| `bobres admin link` | A one-time dashboard login link, for when the bot is down |
| `bobres admin password` | Reset the owner's dashboard password. It is shown once and must be changed at first login |
| `bobres admin owners list\|add\|remove` | The store's owners (Telegram IDs). Only the server can change them |

## Settings and secrets

| Command | What it does |
|---|---|
| `bobres config list`, `get <key>`, `set <key> <value>` | Show or change a setting. The value is checked and only the affected services are recreated |
| `bobres secrets rotate <name>` | Rotate `bot-token`, `db-passwords`, `service-tokens` or `redis-password` |
| `bobres ssl status\|auto\|ip\|manual` | The HTTPS certificate. See [HTTPS](HTTPS) |
| `bobres license status\|import\|activate` | Show the license, install a token, or activate a license key. See [Licensing](Licensing) |
| `bobres harden` | Firewall (ufw), fail2ban, automatic security updates and swap, each asked first |
| `bobres monitor status\|enable\|disable\|snapshot` | Host monitoring for the dashboard's charts |

The settings `bobres config set` accepts include `BOBRES_DOMAIN`, `BOBRES_TIMEZONE`, `BOBRES_ADMIN_TELEGRAM_ID`, `BOBRES_BACKUP_TIME` (HH:MM, default 03:30), `BOBRES_UPDATE_CHANNEL` (`stable` or `beta`), `BOBRES_LICENSE_SERVER`, `BOBRES_SELLER_CONTACT`, `BOBRES_TELEGRAM_API_URL`, the `BOBRES_BACKUP_S3_*` settings, `BOBRES_LOG_LEVEL` and `BOBRES_LOG_FORMAT`. For example:

```bash
sudo bobres config set BOBRES_TIMEZONE Asia/Tehran
```

Secrets are never shown. `bobres config list` masks them and points to the command that changes each one. The panel and gateway settings are changed in the dashboard, not here.

## Uninstall

| Command | What it does |
|---|---|
| `sudo bobres uninstall` | Remove the containers. The data, certificates and `/opt/bobres` stay, and `bobres start` brings the store back |
| `sudo bobres uninstall --purge --dry-run` | Print exactly what would be removed, with sizes. Changes nothing |
| `sudo bobres uninstall --purge` | Remove everything BOBRES put on the server. It asks for a verified final backup, then for the domain typed in full |
| `sudo bobres uninstall --verify` | Scan the server for what is left |

It only touches what it can prove belongs to BOBRES, and never SSH settings. See the [uninstall runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/uninstall.md).

## Flags that work everywhere

- `--plain` turns off colors and redrawing (also `BOBRES_PLAIN=1`, `NO_COLOR`, or a pipe).
- `--no-banner` leaves out the banner (also `BOBRES_NO_BANNER=1`).
- `--dir` selects the install directory for commands that work on an install. The default is `$BOBRES_DIR`, else `/opt/bobres`.

## Exit codes

Most commands exit 0 when done, 1 when refused or failed, and 2 for a usage error. `status` and `check` exit 1 when something is wrong, so you can use them in monitoring scripts.

## What lives where

`/opt/bobres` holds `.env` (your secrets, mode 0600), `docker-compose.yml`, `caddy/`, `tls/`, `backups/` and `logs/`. Do not edit `.env` by hand for settings that `bobres config` manages. After an update, `.restore-point/` holds what a rollback needs. It contains a copy of `.env`, so never copy it anywhere you would not copy `.env`.
