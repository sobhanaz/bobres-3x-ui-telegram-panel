# 06 - Installer and `bobres` CLI spec (DRAFT)

Target OS: Ubuntu 22.04/24.04 and Debian 11/12, amd64 + arm64. Root or sudo required.

## One-command install
```bash
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
# or with sudo, passing install flags; the tokens are still asked with hidden input
# (never put them on the command line, where ps and the shell history keep them):
curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh | sudo bash -s -- \
  --domain panel.example.com --admin-id 123456789 --xui-url https://panel.example.com:2053/abc
```
The script is small, readable, checksum/signature verified, and only bootstraps: it installs Docker if needed,
downloads the signed `bobres` CLI binary from the latest GitHub release of this repository
(`BOBRES_BASE_URL` overrides the location), verifies it (Ed25519), then hands over to `bobres install`.
The wizard asks for what is missing; for a panel on the same server typed as `http://127.0.0.1:<port>/...`
it offers `http://host.docker.internal:<port>/...` (what the containers can reach).

The 3x-ui panel is optional (milestone 6a): a fresh install still asks for one (Enter skips it), a re-run
never asks, and a URL needs its token (and a token its URL). The provisioner registers that panel only while
it has no server; after that servers are added and changed in the dashboard (Servers), and `.env` edits of
`BOBRES_XUI_*` change nothing. Without a panel the summary says to add a server in the dashboard before selling.

## Management menu
`bobres` without a command, on a terminal, opens a numbered menu in the spirit of the x-ui script: status,
live logs, start, stop, restart, show settings (secrets masked), change the bot token, the owner's Telegram
ID or the first 3x-ui panel, back up now, show the backup key (after a confirmation), check the server, uninstall. Settings changes re-run the installer with the one new
value, so they are validated the same way (bot token and panel checked online) and only the affected
services are recreated. Scripts get the usage text instead, never a prompt. "Show settings" no longer reports
Zarinpal from `.env`: with the payments key the gateway is managed in the dashboard (Payment gateways), and
without it the menu says to re-run `bobres install`. The panel item says that the panel is used only until the
dashboard has a server.

## `bobres install` steps
1. **Preflight:** OS/arch, RAM (>=1 GB, warn <2), disk (>=10 GB), ports 80/443 free, clock sync, Docker/compose versions, outbound access to Telegram API, registry and license server.
2. **DNS check:** domain resolves to this server's public IP; explain exactly what to fix if not.
3. **Wizard** (skipped when flags given): domain, bot token (validated via getMe), admin Telegram ID, 3x-ui URL + API token (validated: reachable, version detected, compatible range), enabled gateways + keys, default language, brand name.
4. **License:** activate key (or start trial), show entitlements.
5. **Generate secrets:** DB/Redis passwords, internal service keys, session keys, one master key per service
   (`BOBRES_CORE_MASTER_KEY`, `BOBRES_PROVISIONER_MASTER_KEY`, `BOBRES_PAYMENTS_MASTER_KEY`). Write `/opt/bobres/.env`
   (chmod 600, owned by service user). A re-run keeps every secret and fills only missing ones, so an install from
   before milestone 6a gets its payments key then; until that re-run, compose passes an empty key (`:-`) and payments
   keeps running Zarinpal from `.env`. The Zarinpal merchant id, proxy and sandbox in `.env` only seed the gateway
   settings on the first start with the key; the public URL and `BOBRES_ZARINPAL_HOST` stay in `.env` (Caddy).
6. **Render** `docker-compose.yml` + Caddyfile from templates; pull signed images (verify signatures).
7. **Start** infra (Postgres, Redis), run migrations, start services, wait for health.
8. **Harden:** ufw (22/80/443 only), fail2ban, unattended-upgrades, swap if low RAM, dedicated user. Each step is asked/announced, not silent.
9. **Verify:** hit /readyz, send test message to admin ("BOBRES is live"), print summary with URLs and first-login link.
10. **Idempotent:** re-running detects an existing install and offers repair/upgrade instead of overwriting.

## Layout on server
`/opt/bobres/{bobres.yml, .env, docker-compose.yml, Caddyfile, data/, backups/, logs/}`, and `.restore-point/` after
an update (root only); state in named volumes. install.sh and `bobres update` record the last verified key list and
release in `/var/lib/bobres/{keys.seq,release.version}` (`$BOBRES_STATE_DIR`).

## Status (2026-10-12)
Implemented: `install`, the menu, `status`, `logs`, `start`, `stop`, `restart`, `uninstall [--purge]`, `doctor`,
`version`, `admin link` (2026-10-07), `license status|import` (2026-10-11), `backup` (2026-10-11, see Backups),
`restore`, `update` and `rollback` (2026-10-12, see Restore, update and rollback). The bootstrap `install.sh` downloads
the latest GitHub release, verifies it and hands over to `bobres install`. Not yet: `config`, `secrets`, `license activate`
(needs the vendor server), `support-bundle`, and the hardening step (ufw, fail2ban).
`admin add/reset-2fa` was dropped (2026-10-10): `admin link` already gets anyone on the staff in without
a password or code, the owner manages staff on the dashboard's Staff page, and the owner themselves is
changed with `bobres menu` ("Change the owner Telegram ID"), which core applies when it starts.

## CLI commands
| Command | Purpose |
|---|---|
| `bobres` / `bobres menu` | Management menu (terminal only) |
| `bobres install / uninstall` | Set up / remove (uninstall keeps data unless `--purge`) |
| `bobres status` | Service health, versions, license, disk, backup age |
| `bobres logs [service] [-f]` | Tail logs |
| `bobres start / stop / restart` | Start (and wait for health) / stop / restart the stack |
| `bobres update [--check] [--version X] [--yes]` | Verify the signed release, back up, leave a restore point, swap in the new CLI, run its install, health check, automatic rollback. `--check` only says whether a newer verified release exists |
| `bobres rollback [--yes]` | Undo the last update with its restore point: the previous CLI, install files and version, and the database from before the update |
| `bobres backup` | Back up now: runs `/app run-now` in the backup container, which asks the running service (one backup at a time) and prints the result (see Backups) |
| `bobres restore <file> [--key-file F] [--yes] [--keep-keys]` | Replace the database with a backup's (verified first, the current data backed up first, one transaction); the backup's master keys go into `.env`. Off-server upload (S3/SFTP) is still open |
| `bobres config get/set` | Edit settings safely, validate, restart affected services |
| `bobres secrets rotate <name>` | Rotate bot token/3x-ui token/DB password |
| `bobres license status` / `license import < token.txt` | Show the license as core sees it (state, install id to send the vendor, entitlements) / install a token read from stdin (verified first; audited as `license.set` from the CLI). Both run `/app license ...` in the core container. `activate` (online, through the vendor server) comes later |
| `bobres doctor` | Diagnose: DNS, TLS, ports, bot token, 3x-ui reachability/version, queue lag, disk; print fixes |
| `bobres admin link [telegram id]` | One-time dashboard login link printed on the server, for when the bot is down (default: the owner). It skips the password and code, so it is also the way back after a lost authenticator |
| `bobres support-bundle` | Redacted logs/config for support (no secrets, no user data) |

## Backups
The `backup` service (image `bobres-backup`, a `postgres:16-alpine` target in `deploy/Dockerfile`, so its pg_dump
matches the server) backs up the whole `bobres` database every day at `BOBRES_BACKUP_TIME` (default 03:30) in
`BOBRES_TIMEZONE`, and on demand (`bobres backup`, the menu, or the dashboard's System page, which calls its HTTP API
with `BOBRES_TOKEN_CORE_BACKUP`, a token core uses for this service only). Each run:
- refuses to start when `/opt/bobres/backups` has less free space than twice the last backup (100 MiB before the
  first), and records why;
- streams `pg_dump -Fc` (as the read-only role `svc_backup`; the password goes in `PGPASSWORD`, never on a command
  line) straight into the encrypted file format of `internal/backup`, written to a temp file, synced and renamed to
  `bobres-<UTC stamp>.bkp`, with a `.json` sidecar (size, sha256, created_at, sent_at, telegram_error, error);
- hands the file to the bot (`POST /internal/backups/deliver`, token `BOBRES_TOKEN_BACKUP`), which streams it to the
  owner's Telegram only, as it arrives (never kept in the bot's memory or its tmpfs `/tmp`, which share its 512 MB
  limit); over 50 MB, or 2000 MB with a local Bot API, the owner gets a text instead and the file stays
  here. Without `BOBRES_TOKEN_BACKUP` backups are kept on the server only;
- keeps the newest 7 backups (and at most 7 records of failed runs). A failed run leaves its sidecar, no `.bkp`.
  The CLI's safety backups (`bobres-<stamp>-before-restore.bkp`, `-before-rollback.bkp`, below) are listed with
  them but neither counted nor pruned: the operator deletes them.

**The backup key.** `bobres install` generates `BOBRES_BACKUP_KEY` and prints it once, at the end of the run that
saved it to `.env`, however that run ends (after the summary, after a failed start, with `--no-start`, or a quiet
settings change from the menu); `bobres menu` > "Backup key" shows it again after a confirmation, and every summary
says so. It is never sent to Telegram. Store
it away from the server: without it the backups cannot be read. Each backup also carries `BOBRES_CORE_MASTER_KEY`,
`BOBRES_PROVISIONER_MASTER_KEY` and `BOBRES_PAYMENTS_MASTER_KEY` (encrypted, inside the file), so a backup plus the
backup key is enough to restore on a new server, with `bobres restore` (below).

**Older installs and pinned versions.** The service runs only with the Compose profile `backup` on
(`COMPOSE_PROFILES=backup` in `.env`, with `BOBRES_BACKUP_URL=http://backup:8080` for core). Every `bobres install`
sets it from the version it runs: on for images that have the service (`latest`, any release from the first that
ships `bobres-backup`, `firstBackupRelease` in `cmd/bobres/install.go`, its pre-releases included, this CLI's own
version, or anything with a development build of the CLI), off for an older one, which has no `bobres-backup` image
to pull. So an install kept on an older version starts as before, `bobres install --version <new>` turns backups on,
and a rollback to an older version (`bobres install --version <old>`) turns them off again (other profiles stay)
instead of failing in `compose up`. Status, the menu and the health wait count `backup` only where the profile is on.

**The database role.** New databases get `svc_backup` (LOGIN, `pg_read_all_data`, CONNECT; PostgreSQL 14+) from
`postgres-init.sh`. Init scripts run only on a new database, so `bobres install` also creates or updates the role in
the running database after `compose up` (idempotent; it runs psql in the postgres container, which reads
`DB_BACKUP_PASSWORD` from its environment with `\getenv`). If that step fails (postgres not running) it is a
warning; re-run `bobres install`, or by hand in `/opt/bobres`:

```sh
docker compose --project-name bobres -f docker-compose.yml --env-file .env exec -T postgres sh -c \
  'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
\getenv pw DB_BACKUP_PASSWORD
\getenv db POSTGRES_DB
SELECT 'CREATE ROLE svc_backup LOGIN' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'svc_backup') \gexec
ALTER ROLE svc_backup WITH LOGIN PASSWORD :'pw';
GRANT pg_read_all_data TO svc_backup;
GRANT CONNECT ON DATABASE :"db" TO svc_backup;
SQL
```

`/opt/bobres/backups` is created by the installer, mode 0700, owned by uid 65532 (the image's user). Adding
`DB_BACKUP_PASSWORD` to the postgres service recreates the postgres container once on the upgrade.

## Restore, update and rollback
Runbooks: [restore](runbooks/restore.md) and [update and rollback](runbooks/update-rollback.md). All three exit 0 when
done (or nothing was to do), 1 when refused or failed, 2 for usage errors, and take `--dir` like every other command.

**`bobres restore <file> [--key-file F] [--yes] [--keep-keys]`** replaces the database with a backup's.
1. The key is `BOBRES_BACKUP_KEY` from `.env`, or the one line of `--key-file` for a backup made on another server (a
   fresh install there made its own key; a line copied from `.env`, `BOBRES_BACKUP_KEY=...`, works too). An install
   with no `BOBRES_BACKUP_KEY` of its own is refused first, with or without `--key-file`: the safety backup (step 5)
   needs it, and `sudo bobres install` creates it (keeping the settings).
2. The whole file is verified before anything is touched: a wrong key, a damaged or cut file and a short key are
   refused. It prints when the backup was made, the size of the dump and the master key names (never the values).
3. It asks, unless `--yes`: the current data is replaced.
4. It stops core, bot, payments, provisioner and backup; postgres, redis and caddy stay up. First, so that nothing is
   written after the safety backup (it would be in neither that backup nor the restored data).
5. **Safety backup:** `pg_dump -Fc` of the current database (in the postgres container, as its superuser
   `POSTGRES_USER` over the local socket: no password anywhere) streamed into the backup format with this install's
   key and master keys, saved as `backups/bobres-<stamp>-before-restore.bkp` with its sidecar, so the System page
   lists it. The name is outside the backup service's retention: a restore tried again and again never pushes out
   the 7 daily backups, and the file stays until the operator deletes it. The file and its sidecar are written as
   temp files, given to uid 65532 through their open descriptors (never by path: that user controls `backups/`),
   synced and renamed, and the directory is synced before anything destructive follows. If it fails, nothing
   changes and the services start again.
6. **One transaction:** in the postgres container, `psql -v ON_ERROR_STOP=1` gets `BEGIN`, `DROP SCHEMA core,
   payments, provisioner CASCADE`, the archive as SQL from `pg_restore --clean --if-exists`, and `COMMIT` only once
   pg_restore has read the whole archive. Any failure (pg_restore, SQL, a cut stream) rolls everything back and the
   services start again on the old data. Owners and grants come from the archive; the roles exist on every install
   (postgres-init.sh). Why not `pg_restore --single-transaction --clean` straight into the database: `--clean` drops
   only what the archive holds, so a table a later version added (any backup from before an update) keeps its schema
   from being dropped and the whole restore fails.
7. **Master keys:** where the backup's differ from `.env`, `.env` takes the backup's (names printed, mode 0600 kept):
   without them the restored 3x-ui tokens, staff 2FA and gateway settings cannot be read. `--keep-keys` leaves `.env`
   alone and says so. `BOBRES_BACKUP_KEY` itself is never changed: new backups use this install's key.
8. `compose up -d`, the usual five-minute health wait, then a summary with the safety backup to go back to
   (`bobres restore <it> --yes`) and a disk hint; when unhealthy, where to look.

Every file the CLI rewrites (`.env`, the restore point, the swapped CLI) goes through `writeAtomic`: a temp file,
synced, renamed, then the directory synced, so a power cut leaves the old file or the new one, never an empty `.env`.

**`bobres update --check [--version X]`** fetches `checksums.txt`, its signature, `keys.txt` and `keys.txt.sig.N` of
the latest release (or of `releases/download/vX`, `release.DownloadURL`), verifies them as install.sh does (the
embedded roots, `internal/release`, and the key list's `seq` against `keys.seq`, below) and prints the current version,
the release's and whether it is newer. It needs no install and works on a development build.

**`bobres update [--version X] [--yes]`**
1. Verifies the release, as install.sh's `verify_release` does. It refuses an unsigned, tampered, revoked or expired
   release; a key list whose `seq` is lower than `$BOBRES_STATE_DIR/keys.seq` (default `/var/lib/bobres`: an older
   list, still signed and unexpired, may name a release key revoked since); a release older than
   `release.version` there or than the install (going back is `rollback`); and files under `vX` signed for another
   version. The same version is nothing to do.
2. Checks for 2 GB free in the install directory and in `/var/lib/docker` (the backup and the new images). The
   install's own checks (10 GB, RAM, ports, the Telegram API) are for a new server and are skipped in step 7.
3. Asks, unless `--yes`.
4. Downloads `bobres_<os>_<arch>` and checks it against the verified `checksums.txt`, before anything changes.
5. **Restore point**, built in `<dir>/.restore-point.new` (mode 0700, root's): the database's migration versions (each
   schema's highest goose `version_id`), a backup of the database (as restore step 5, while the store keeps running),
   copies of `.env`, `docker-compose.yml`, `Caddyfile` and `postgres-init.sh` in `files/`, a copy of the running CLI
   (`bobres`), and `restore-point.json` (`from_version`, `to_version`, `backup_file`, `created_at`, `cli_path`,
   `cli_backup_path`, `files_dir`, `schema_versions`, later `used_at`). It is outside `backups/` on purpose: uid 65532
   controls that folder (it could swap in its own compose file for root to start), and operators copy `backups/` off
   the server, which must never carry `.env` and its `BOBRES_BACKUP_KEY` in the clear. The restore point's backup is
   not pruned, so it lasts as long as the restore point. `bobres rollback` trusts the folder only when root owns it
   and no one else can write to it, and reads the backup and the files from it whatever the record says.
6. Swaps the CLI: the running binary is hard-linked to `bobres.prev` next to it, and the new one is written beside it
   and renamed over it. Only then does the new restore point replace the current one, which is set aside as
   `.restore-point.prev` until the update holds: a failed attempt never loses the previous update's restore point.
   `keys.seq` and `release.version` are written then, as install.sh writes them after installing a CLI (never
   lowered).
7. Runs the new binary's `install --version X --dir D --quiet --yes --skip-checks --offline`: it re-renders compose
   from its embedded copy, pulls the images, starts and waits. `--quiet` leaves out the first-steps summary; `--yes`
   never asks (a missing value is an error).
8. Waits until healthy; then `.restore-point.prev` goes.

When the install fails or the store is not healthy within five minutes, it **rolls back on its own**. It reads the
migration versions again:
- **No migration ran** (an image pull that failed, the install's checks, a service that never got far): the database
  stays as it is, with every order the old version took during the pull. The old CLI and the install files (and
  `BOBRES_VERSION` with them) come back, and `compose up -d --remove-orphans` starts the old version; nothing at all
  when the install never wrote its files.
- **Migrations ran, or the versions are not known:** it stops the app services, backs up the current data as
  `backups/bobres-<stamp>-before-rollback.bkp` (what the new version wrote), puts back the old CLI and install files,
  starts postgres, restores the database from the restore point (as restore step 6), puts back the master keys, runs
  `compose up -d --remove-orphans` and waits. If that backup fails, nothing else changes: the services start again as
  they were, and `sudo bobres rollback` finishes the job once the cause is fixed.

Either way the previous update's restore point is the current one again (the failed attempt's goes; with none, a
restore point whose backup was restored stays, marked used). It says what happened and exits 1. A development build
(`dev`) refuses to replace itself; `--check` still works. The install run has no time limit: killing it on a timeout
would leave its `docker compose` running, which could recreate containers after the rollback.

**`bobres rollback [--yes]`** undoes the last update with its restore point: it checks the restore point's backup,
asks, stops the app services, saves the current data in `backups/bobres-<stamp>-before-rollback.bkp` (orders since the
update; when that fails nothing changes and the services start again), then puts back the previous CLI (from the
restore point) and install files, restores the database as above and starts. No restore point, or one already used:
it explains and exits 1. Only the last update can be rolled back; to go further back, `bobres install --version <old>`
moves the images and `bobres restore` the data. `release.version` is not lowered by a rollback: a later update goes to
the release rolled back from, or newer.

## Uninstall
Stops services, removes containers/units; data kept unless `--purge`; prints what remains. `--purge` deletes the
install directory, and with it every backup in `/opt/bobres/backups`: it warns first (copy them elsewhere).

## Open points
- Installer and CLI binaries: GitHub releases of this (public) repository. Images: ghcr.io, which must be
  public for customers to pull without logging in (private registry auth via license is still open).
- Re-running the one-liner on an existing install updates the CLI and repairs the install, but keeps the
  running image version; moving to a new version is `bobres update` (with backup and rollback).
- Update channel (stable/beta) and keeping the previous images for N days: not built; `bobres update` follows the
  latest non-pre-release (or `--version X`), and a rollback pulls the previous images again if they were pruned.
- Non-Docker (systemd binaries) mode: not planned for v1.
- Installing 3x-ui itself for customers who do not have one yet (optional helper, later).
