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
(`BOBRES_BASE_URL` overrides the location), verifies it (Ed25519), then hands over to `bobres install`. A release
signed for the beta channel (a pre-release) is refused unless `BOBRES_UPDATE_CHANNEL=beta` is set for install.sh,
even when GitHub serves it as the latest release.
The wizard asks for what is missing; for a panel on the same server typed as `http://127.0.0.1:<port>/...`
it offers `http://host.docker.internal:<port>/...` (what the containers can reach).

The 3x-ui panel is optional (milestone 6a): a fresh install still asks for one (Enter skips it), a re-run
never asks, and a URL needs its token (and a token its URL). The provisioner registers that panel only while
it has no server; after that servers are added and changed in the dashboard (Servers), and `.env` edits of
`BOBRES_XUI_*` change nothing. Without a panel the summary says to add a server in the dashboard before selling.

## Management menu
`bobres` without a command, on a terminal, opens a numbered menu in the spirit of the x-ui script: status,
live logs, start, stop, restart, show settings (secrets masked), change the bot token, the owner's Telegram
ID or the first 3x-ui panel, back up now, show the backup key (after a confirmation), restore a backup (the newest 15
in `backups/` by number, or any file by path with an optional key file), update (the `--check` output, then
`bobres update` when a newer release exists), roll back the last update, check the server, uninstall. Restore, update
and rollback run the commands' own code, confirmation included; after an update or a rollback the menu ends, because
the `bobres` running it was replaced (run `bobres` again). Settings changes re-run the installer with the one new
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
8. **Harden** (see Hardening below): ufw (the SSH port, 80, 443), fail2ban for SSH, unattended-upgrades (security
   only), a swap file on a small server. Each step is announced and asked; `--no-harden` skips them all, and
   `bobres harden` runs them later. The services already run as non-root users in their containers, so there is no
   separate system user.
9. **Verify:** hit /readyz, send test message to admin ("BOBRES is live"), print summary with URLs and first-login link.
10. **Idempotent:** re-running detects an existing install and offers repair/upgrade instead of overwriting.

## Bot profile photo
The Bot API cannot set a bot's own profile photo, so the installer leaves it to the operator, and the summary's
first steps say so. In Telegram, from the account that created the bot: @BotFather > `/setuserpic` > choose the
bot > send the store's own logo, or `docs/brand/bot-avatar.jpg` (the BOBRES beaver, 640x640) as a photo. See
08-brand.md.

## Layout on server
`/opt/bobres/{bobres.yml, .env, docker-compose.yml, Caddyfile, data/, backups/, logs/}`, and `.restore-point/` after
an update (root only); state in named volumes. install.sh and `bobres update` record the last verified key list and
release in `/var/lib/bobres/{keys.seq,release.version}` (`$BOBRES_STATE_DIR`).

## Status (2026-10-12)
Implemented: `install`, the menu, `status`, `logs`, `start`, `stop`, `restart`, `uninstall [--purge]`, `doctor`,
`version`, `admin link` (2026-10-07), `license status|import` (2026-10-11), `license activate` (2026-10-12, see
License activation), `backup` (2026-10-11, see Backups),
`restore`, `update` and `rollback` (2026-10-12, see Restore, update and rollback), `support-bundle` (2026-10-12, see
Support bundle), `harden` and the install's hardening step, `config list|get|set`, `secrets rotate`, the menu's
restore/update/rollback items and the update's install time limit (2026-10-12, see Hardening, Settings and secrets).
The bootstrap `install.sh` downloads the latest GitHub release, verifies it and hands over to `bobres install`.
Operator runbooks: [runbooks/](runbooks/README.md).
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
| `bobres update [--check] [--version X] [--channel stable\|beta] [--yes]` | Verify the signed release (the newest of the update channel, or X), back up, leave a restore point, swap in the new CLI, run its install, health check, automatic rollback. `--check` only says whether a newer verified release exists, and whether the license covers it |
| `bobres rollback [--yes]` | Undo the last update with its restore point: the previous CLI, install files and version, and the database from before the update |
| `bobres backup` | Back up now: runs `/app run-now` in the backup container, which asks the running service (one backup at a time) and prints the result (see Backups) |
| `bobres restore <file> [--key-file F] [--yes] [--keep-keys]` | Replace the database with a backup's (verified first, the current data backed up first, one transaction); the backup's master keys go into `.env`. Off-server upload (S3/SFTP) is still open |
| `bobres config list / get <key> / set <key> <value>` | Show the settings (secrets masked) or change one of the safe ones: validated as install does, `.env` written in one rename (0600), only the services that read it recreated (see Settings and secrets) |
| `bobres secrets rotate <name>` | `bot-token`, `db-passwords [--role R]`, `service-tokens` or `redis-password`: new values, the affected services restarted, `.env` rolled back when they do not come back healthy. Master keys and the backup key are not rotated |
| `bobres harden [--yes] [--ssh-port N] [--dir DIR]` | The install's hardening step on its own: firewall, fail2ban, security updates, swap, each asked (see Hardening) |
| `bobres license status` / `license import < token.txt` | Show the license as core sees it (state, install id to send the vendor, entitlements, how it came) / install a token read from stdin (verified first; audited as `license.set` from the CLI). Both run `/app license ...` in the core container |
| `bobres license activate [<key>] [--server URL]` | Turn a license key from the seller into this install's license, through the vendor's license server (`BOBRES_LICENSE_SERVER` in `.env`, or `--server` for one run; https only). Run it without `<key>`: it asks for the key without echoing it, or reads it from stdin (`< key.txt`). A key given as the argument stays in the shell history and in sudo's log. Core then renews the license daily (see License activation) |
| `bobres doctor` | Diagnose: OS, RAM, disk, ports, Docker, DNS, and the hardening state (firewall, fail2ban, security updates, swap; warnings, never failures) |
| `bobres admin link [telegram id]` | One-time dashboard login link printed on the server, for when the bot is down (default: the owner). It skips the password and code, so it is also the way back after a lost authenticator |
| `bobres support-bundle [--offline]` | A redacted tar.gz for support in the install directory: versions, `compose ps`, the doctor's checks, the newest 300 log lines per service, the `.env` key names (never a value), disk and memory, the backups list (see Support bundle). Never the database, a backup or a secret value |

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

**Update channel.** `BOBRES_UPDATE_CHANNEL=stable|beta` in `.env` (unset: stable; the bot gets it through the compose
file and fails to start on another value), overridden for one run by `bobres update --channel`. `internal/release`
resolves the release (`Source.Resolve`): **stable** is `releases/latest/download`, which GitHub points at the latest
release that is not a pre-release, and a release whose signed header says `channel beta` is refused there; **beta** asks
GitHub's API for the newest 30 releases (`api.github.com/repos/.../releases`; drafts and tags that are not
`vX.Y.Z[-pre]` left out), takes the highest by `release.Compare`, pre-releases included, and verifies
`releases/download/v<it>` with that version required (the API's answer is not signed). `release.Compare` and
install.sh's `semver_lt` order pre-releases as semver 2.0 does (`rc.9` < `rc.10` < the release; numeric parts of any
length by their value; `TestCompareMatchesInstaller` runs both on the same pairs). release.yml signs every tag with a
`-` with `--channel beta` and marks it a pre-release. install.sh refuses a release signed for beta unless it runs
with `BOBRES_UPDATE_CHANNEL=beta` (`verify_release`'s channel argument). A beta install that goes back to stable stays
on its pre-release (`bobres update` says so and exits 0) until a stable release is newer. A beta that was tried and
rolled back does not hold anything back: `release.version` (the floor) is written only once an update holds, and
`bobres rollback` puts back the one from before the update. Runbook: [releases](runbooks/releases.md).

**License coverage (`updates_until`).** The signed header's `# issued` (`release.ParseHeader`) is the release's day
(UTC). When the install's license verifies for it (active, grace or expired) and carries `updates_until`, a release
issued after that day is not covered (`release.Covered`): `bobres update` refuses it, and `--check` and the menu say
"Newer: yes, but not covered by your license" (the menu then offers no update). The CLI reads the license with
`compose exec -T core /app license status` (its "State" and "Updates until" rows, which every core with a license
command prints); when core cannot be asked it warns and does not refuse. A missing or invalid license, a license
without `updates_until` or a release without `# issued` gates nothing. The dashboard's System page gets the same
answer from core (`updates.covered`: core compares the bot's `issued` with its own license; absent when there is no
valid license or no date).

**`bobres update --check [--version X] [--channel C]`** fetches `checksums.txt`, its signature, `keys.txt` and
`keys.txt.sig.N` of the channel's newest release (or of `releases/download/vX`, `release.DownloadURL`), verifies them
as install.sh does (the embedded roots, `internal/release`, and the key list's `seq` against `keys.seq`, below) and
prints the current version, the channel, the release's version and whether it is newer (and covered by the license;
a release older than `release.version` is "Newer: yes, but older than release X, which this server installed
before", which `bobres update` refuses, so the menu does not offer it). It needs no install and works on a development
build.

**`bobres update [--version X] [--channel C] [--yes]`**
1. Verifies the release, as install.sh's `verify_release` does. It refuses an unsigned, tampered, revoked or expired
   release; a key list whose `seq` is lower than `$BOBRES_STATE_DIR/keys.seq` (default `/var/lib/bobres`: an older
   list, still signed and unexpired, may name a release key revoked since); a release older than
   `release.version` there or than the install (going back is `rollback`); files under `vX` signed for another
   version; on stable, a release signed for beta; and a release the license does not cover (above). The same version
   is nothing to do.
2. Checks for 2 GB free in the install directory and in `/var/lib/docker` (the backup and the new images). The
   install's own checks (10 GB, RAM, ports, the Telegram API) are for a new server and are skipped in step 7.
3. Asks, unless `--yes`.
4. Downloads `bobres_<os>_<arch>` and checks it against the verified `checksums.txt`, before anything changes.
5. **Restore point**, built in `<dir>/.restore-point.new` (mode 0700, root's): the database's migration versions (each
   schema's highest goose `version_id`), a backup of the database (as restore step 5, while the store keeps running),
   copies of `.env`, `docker-compose.yml`, `Caddyfile` and `postgres-init.sh` in `files/`, a copy of the running CLI
   (`bobres`), and `restore-point.json` (`from_version`, `to_version`, `backup_file`, `created_at`, `cli_path`,
   `cli_backup_path`, `files_dir`, `schema_versions`, `release_floor` (`release.version` before the update, "" for
   none), later `used_at`). It is outside `backups/` on purpose: uid 65532
   controls that folder (it could swap in its own compose file for root to start), and operators copy `backups/` off
   the server, which must never carry `.env` and its `BOBRES_BACKUP_KEY` in the clear. The restore point's backup is
   not pruned, so it lasts as long as the restore point. `bobres rollback` trusts the folder only when root owns it
   and no one else can write to it, and reads the backup and the files from it whatever the record says.
6. Swaps the CLI: the running binary is hard-linked to `bobres.prev` next to it, and the new one is written beside it
   and renamed over it. Only then does the new restore point replace the current one, which is set aside as
   `.restore-point.prev` until the update holds: a failed attempt never loses the previous update's restore point.
   `keys.seq` is written then, as install.sh writes it after installing a CLI (never lowered): the key list was
   verified whatever becomes of the update.
7. Runs the new binary's `install --version X --dir D --quiet --yes --skip-checks --offline`: it re-renders compose
   from its embedded copy, pulls the images, starts and waits. `--quiet` leaves out the first-steps summary; `--yes`
   never asks (a missing value is an error).
8. Waits until healthy; then `release.version` is written (never lowered), as install.sh writes it, and
   `.restore-point.prev` goes. An update that rolls back leaves `release.version` as it was, so a tried release (a
   beta above all) never becomes the floor that refuses the releases below it.

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
(`dev`) refuses to replace itself; `--check` still works.

**The install's time limit.** The new CLI's install run (pulling the images, starting, its own five-minute health
wait) has 20 minutes. It runs in a process group of its own (`Setpgid`), and at the limit the whole group is killed
(`SIGKILL` to the group), the `docker compose` it started included, so nothing is left that could recreate containers
after the rollback; then the automatic rollback above runs. Its own process group does not get the terminal's Ctrl+C,
so `bobres update` catches Ctrl+C, SIGTERM and SIGHUP (a dropped SSH session) while the install runs and does the same:
kills the group and rolls back. A second Ctrl+C during the rollback ends `bobres` itself; `sudo bobres rollback`
finishes the job then.

**`bobres rollback [--yes]`** undoes the last update with its restore point: it checks the restore point's backup,
asks, stops the app services, saves the current data in `backups/bobres-<stamp>-before-rollback.bkp` (orders since the
update; when that fails nothing changes and the services start again), then puts back the previous CLI (from the
restore point) and install files, restores the database as above and starts. No restore point, or one already used:
it explains and exits 1. Only the last update can be rolled back; to go further back, `bobres install --version <old>`
moves the images and `bobres restore` the data. `release.version` goes back to the restore point's `release_floor`
(removed when that is ""): the update undone no longer counts as installed, so a later update may take a release
below it (a stable patch after an undone beta). It is only lowered, and not when it is above the release rolled back
from (install.sh installed a newer one since); a restore point without `release_floor` leaves it as it is.

## Support bundle
`bobres support-bundle [--dir D] [--offline]` writes `<dir>/bobres-support-<UTC stamp>.tar.gz` (mode 0600, through
`writeAtomic`) and prints its path. It holds `README.txt` (what is inside, and what could not be collected),
`versions.txt` (the CLI, `BOBRES_VERSION`, the OS, Docker, Compose and `compose images`), `compose-ps.txt`
(`compose ps --all`), `doctor.txt` (the doctor's checks; `--offline` skips DNS and the public IP), `logs/<service>.log`
(`compose logs --timestamps --tail 300` for every service status lists, at most 2 MB each), `env-keys.txt` (each `.env`
key as set, empty or placeholder, and the file's mode), `system.txt` (disk of the install directory and
`/var/lib/docker`, the backups folder's size, RAM and swap, `docker system df`) and `backups.txt` (the backups folder's
names and sizes, what each sidecar says, and the restore point's versions). It never runs `compose config`,
`inspect` or anything in a container, never opens a `.bkp` file or the database, and never follows a link in
`backups/` (the backup service's user owns it); a sidecar is opened non-blocking and read only when the open file is
regular, so one swapped for a named pipe cannot hang it. Each docker command has 30 seconds; a part that fails is described in
its file and the rest is still written (exit 0), so it works when Docker is the problem. Exit 1 only when the file
cannot be written, 2 for usage errors.

Every text is redacted before it is packed, line by line: first every `.env` value of 6 characters or more wherever it
appears (all keys but plain settings such as the domain, version, time zone and admin id), then `scheme://user:pass@`
(internal/logging's pattern), Telegram bot tokens, `Bearer`/`Basic` credentials, credential headers (Authorization,
Cookie, X-Api-Key, the webhook secret header), JSON fields, JSON lists and `name=value` pairs whose name holds one of
internal/logging's secret words (token, secret, password, key, auth, dsn, license, signature...) or `merchant`, a UUID
within a few characters after "merchant" (a Zarinpal merchant id), hex strings of 32+ characters, and base64 or
base64url runs of 32+ characters that mix letters and digits. Booleans stay (`database_url_set=true`), and so do order,
user and service UUIDs, backup stamps and file paths (a run with a lower-case path segment is judged segment by
segment), because support needs them. Visitors' data goes last: Caddy logs every request with the visitor's address and
browser, so `User-Agent` headers and fields (the word `agent` joins the secret words) lose their value, and every
public IPv4 or IPv6 address (customers on `/pay/*`, staff on `/admin`, the server's own) becomes `[IP-1]`, `[IP-2]`...,
numbered in the order met so lines from one address can still be matched. Private, loopback, link-local and unspecified
addresses (Docker's networks) stay; a candidate must parse as an address and stand alone, so times and versions such as
`1.2.3.4.5` are kept. The logs can still show Telegram ids, usernames, order ids, panel host names and which pages were
opened when, so the operator reads the bundle before sending it.

## Hardening
`bobres install` runs it after the store is healthy and before the summary (step 8), only when it can ask: never with
`--yes` (the way `bobres update` runs install), never for a settings change from the menu, never with `--no-start`;
`--no-harden` skips it. `bobres harden [--yes] [--ssh-port N] [--dir DIR]` runs the same step on its own (`--dir`: the
install whose `.env` it reads, default `$BOBRES_DIR`, else `/opt/bobres`; exit 0 when every step is
done, declined or not needed; 1 when one failed or was refused; 2 for usage errors). Debian and Ubuntu only (`ID` in
`/etc/os-release`), as root; anything else is refused before a command runs. Each step first reads the system and
says "Already on" when it has nothing to do, so a second run changes nothing; otherwise it says what it changes, asks
(Enter takes the shown default; the end of input, a script without a terminal, is always no; `--yes` says yes to every
question), and prints each command before it runs it (`> ufw allow 22/tcp ...`). A failed step does not stop the
others; the install stands whatever the hardening does.

1. **Firewall (ufw).** The SSH ports are detected from `sshd -T` (else `/etc/ssh/sshd_config`, where no `Port` line
   means 22), the ports `ss -tulpn` shows sshd listening on, and this session's port (`SSH_CONNECTION`), all together,
   plus `--ssh-port`. When none is found the step is refused (the firewall stays off, so this session is never cut
   off) until `--ssh-port` is given. It allows the SSH ports (tcp), 80/tcp, 443/tcp and 443/udp (HTTP/3), each with
   a `bobres ...` comment, then `ufw default deny incoming`, `default allow outgoing` and `--force enable`: the allow
   rules always come first. Ports Docker publishes do not pass through ufw, so Caddy is reachable either way.
   - **3x-ui on the same server** (an `x-ui` or `xray...` process listening on a public address): when the firewall is
     turned on from off, its ports stay open (`bobres 3x-ui` rules), the operator is told that a port added to 3x-ui
     later (a new inbound) is refused until `sudo ufw allow <port>/tcp`, and the question's default becomes no.
   - **Services on this server the containers use.** The containers reach the host (`host.docker.internal`, the
     Docker bridge address) through its INPUT chain, so the default deny would cut them off too: a local Bot API
     server in `BOBRES_TELEGRAM_API_URL` would stop the bot. Every `.env` URL on `host.docker.internal` (read from the
     install, `--dir`; the install passes its own) gets its port allowed only from Docker's networks:
     `ufw allow from 172.16.0.0/12 to any port N proto tcp` (Docker's default pools), plus the store's egress network
     subnet when `docker network inspect bobres_egress` shows one outside them (a custom address pool). It is never
     opened to the outside. A port already open to everyone (80, 443, a 3x-ui port kept open) needs no rule of its own.
   - Other programs listening on a public address (a database, rpcbind) are listed as closed to the outside and to the
     BOBRES containers, with the command to open one to everyone and the Docker-only form. Loopback listeners,
     `docker-proxy` and the DHCP and time services are left out.
   - **ufw already on:** its defaults and rules stay; only the missing SSH, 80/443 and container (Docker-only) rules are
     offered. 3x-ui's ports are never offered there, not even with `--yes`: a firewall that refuses them may do so on
     purpose (a panel reached through an SSH tunnel). They are only listed, with the command to open one.
2. **fail2ban.** Installs `fail2ban` (and `python3-systemd` where there is no `/var/log/auth.log`, as on Debian 12,
   whose jail then reads the journal) and writes `/etc/fail2ban/jail.d/bobres-sshd.local`: the sshd jail on the
   detected ports, 5 failures within 10 minutes block the address for an hour. Then `systemctl enable` and `restart`.
3. **Security updates.** Installs `unattended-upgrades`, writes `/etc/apt/apt.conf.d/20auto-upgrades` (daily lists and
   upgrades) and `/etc/apt/apt.conf.d/51bobres-security-only`, which clears the package's origin lists (`#clear`) and
   allows only the security pockets: `${distro_codename}-security` of Debian, or Ubuntu's (with ESM apps and infra),
   with `Automatic-Reboot "false"`. Docker's own packages are never upgraded by it (they come from Docker's
   repository): the operator updates Docker in a quiet hour.
4. **Swap.** With less than 2 GB of RAM and no swap: a swap file, 2 GB with 1 GB of RAM or less, else 1 GB, when the
   disk has its size plus 3 GB free (`fallocate`, or `dd` where the file system cannot allocate; `chmod 600`, `mkswap`,
   `swapon`, then a line in `/etc/fstab`, written in one rename). An unfinished file is removed; an existing unused
   `/swapfile` is left alone and reported.

`apt-get update` runs once per run, before the first package install (`DEBIAN_FRONTEND=noninteractive`, `LC_ALL=C`).
**`bobres doctor`** (and the support bundle's `doctor.txt`) adds four lines: `Firewall (ufw)` (on, off or not installed;
needs root to read), `fail2ban (SSH)`, `Security updates` (security only when the bobres file is there) and `Swap`.
They are PASS or WARN, never FAIL, and SKIP off Debian/Ubuntu. The installer's preflight leaves them out. The code
reaches the system only through the injectable runner and file functions of `cmd/bobres/harden.go`; the tests use a
fake server.

## Settings and secrets
**`bobres config list | get <key> | set <key> <value> [--no-restart]`** (exit 0 done, 1 refused or failed, 2 usage).
The settings it changes, and the services each one recreates:

| Key | Recreates | Checked as |
|---|---|---|
| `BOBRES_DOMAIN` | caddy, core, payments | install's host name rule (lower-cased); a reminder that DNS must point here |
| `BOBRES_TIMEZONE` | bot, backup | a known time zone (the CLI embeds tzdata, as the images do) |
| `BOBRES_ADMIN_TELEGRAM_ID` | core, bot | a positive number (core makes them the owner when it starts) |
| `BOBRES_LOG_LEVEL` / `BOBRES_LOG_FORMAT` | core, payments, provisioner, bot, backup | debug, info, warn, error / json, text |
| `BOBRES_BACKUP_TIME` | backup | HH:MM (`config.ParseClock`, the backup service's own rule) |
| `BOBRES_TELEGRAM_API_URL` | bot | an http(s) URL without credentials, or empty (api.telegram.org) |
| `BOBRES_ZARINPAL_PUBLIC_URL` | caddy, payments | install's rule (https://host), or empty; `BOBRES_ZARINPAL_HOST` follows it |

- `get` prints the value alone on its line (for scripts); `BOBRES_ZARINPAL_HOST` can be read, never set.
- Any other key is refused with what changes it instead: a secret (every key but the plain settings the support bundle
  keeps) is never shown, and points to `bobres secrets rotate <name>` (master keys and the backup key to the runbook);
  `BOBRES_VERSION` to `bobres update`; `BOBRES_XUI_*` and the Zarinpal merchant and proxy to the dashboard.
- `set` checks the value with the key's rule and then the whole `.env` with install's `validateEnv`, refusing only
  errors the old `.env` did not have; writes `.env` with `writeAtomic` (0600, keeping every other key and secret);
  then `compose up -d --no-deps` of the services above that this install runs (backup only where its profile is on),
  and waits until they are healthy (Caddy has no health check). A stopped store stays stopped (the change applies when
  it starts); `--no-restart` only writes. When the services are not healthy it exits 1 and prints the command that
  sets the old value back; the value itself was valid, so it is not undone on its own.
- `list` shows the settings, then every other key with secrets masked (`••••`; a bot token keeps its public bot id)
  and the way it is changed.

**`bobres secrets rotate <name> [--yes]`** (exit 0 done, 1 refused or failed, 2 usage):
- `bot-token`: asked without echo (or one line of stdin), checked with getMe (through `BOBRES_TELEGRAM_API_URL` when
  set, with `host.docker.internal` read as 127.0.0.1 from the host; `--offline` skips it), then the bot restarts.
- `service-tokens`: new random `BOBRES_TOKEN_CORE`, `_BOT`, `_BACKUP` and `_CORE_BACKUP` together, so no service keeps
  a stale peer token; core, payments, the provisioner, the bot and the backup service restart at once.
- `redis-password`: a new `REDIS_PASSWORD` and `BOBRES_REDIS_URL`; Redis and the bot are recreated. Redis takes its
  password from its environment when it starts (`--requirepass`), so the new container is the configuration change
  (no `CONFIG SET`, which would put the password in a command); its data is on its volume (append-only file).
- `db-passwords [--role core|payments|provisioner|backup|postgres]` (default all; `svc_backup` only where the backup
  service runs): the database must be running. First a safety backup (`backups/bobres-<stamp>-before-rotation.bkp`,
  as restore's); then one transaction in the postgres container: the shell reads `NAME=value` lines from stdin and
  exports them, and psql reads the SQL after them, `\getenv` and `ALTER ROLE ... WITH PASSWORD :'pw'` (the superuser
  as `CURRENT_USER`), so no password is on a command line. Then `.env`, and only the services whose role changed
  restart (none for the superuser's: nothing logs in with it). The postgres container's own copies of these values
  are used only to create a new database, so it is not restarted; it takes them at its next recreation.
- Every rotation asks (unless `--yes`; the bot token's question is the token itself), keeps a copy of the old `.env`
  as `.env.before-rotation` (0600) until it ends well, prints which services restart and waits until they are
  healthy. When they are not, it puts the old values back (the database's too) and restarts them again; if the
  database cannot take the old passwords back, `.env` keeps the new ones (which match it) and the copy stays. A
  stopped store gets the new `.env` and starts with it (except `db-passwords`, refused). Once it holds, the last
  update's restore point (`.restore-point/files/.env`) takes the new values too, so `bobres rollback` keeps them: a
  restore does not change the roles' passwords (pg_dump of the database does not carry roles), and a revoked token
  must not come back.
- **Master keys are not rotated** (they encrypt the 3x-ui tokens, staff authenticators and gateway settings at rest;
  re-encryption with a new key is future work), nor the backup key (older backups open only with it): the command
  explains and points to the [rotate secrets](runbooks/rotate-secrets.md) runbook.

## License activation
`BOBRES_LICENSE_SERVER` is the vendor's license server ([13-license-server.md](13-license-server.md)). The seller
gives its address with the license key. It is not in `.env` by default: add the line (`sudo nano /opt/bobres/.env`),
then run `sudo bobres start`, which recreates the bot with it (`bobres restart` does not apply `.env` changes, and
`bobres config` does not set this key). It must be an https URL: with anything else the bot does not start. The compose file
passes it to the bot, which makes the calls for the dashboard and for the daily refresh, because core has no internet
access. Without it, online activation is off and a token pasted or imported by hand still works.

**`bobres license activate [<key>] [--server URL] [--dir D]`** (exit 0 installed, 1 refused or failed, 2 usage or no
server):
- **The key.** Leave `<key>` out. The CLI then asks for the key without echoing it on a terminal, or reads the first
  line of stdin (`sudo bobres license activate < key.txt`, then delete the file). A key given as the argument also
  works, but it stays in the shell history and in sudo's log (`COMMAND=...` in `auth.log` or the journal), and other
  users can see it in `ps` while the command runs. The key alone is enough to move the license to another install.
- **The server.** `--server`, else `BOBRES_LICENSE_SERVER` from the environment, else from `.env`. Only https is
  accepted, and redirects are not followed.
- **The steps.** The host makes the call. The CLI reads the install id, domain and version from core
  (`compose exec -T core /app license info --json`) and POSTs them with the key to `<server>/v1/activate`. It pipes
  the token from the answer into `/app license import --activated`. That command verifies the token (signature,
  domain, install id, dates) and installs it with source `activation` (audited as `license.set` from the CLI).
- **Refusals.** A wrong key, a revoked license or too many moves prints the server's message and code, and nothing
  changes. The key is never printed, logged or kept.

The dashboard does the same without the host: System > License, "Activate with a license key" (the owner; a reason
is required). That is `POST /api/v1/license/activate {key, reason}` (license.write, at most 10 a minute). Core sends
it to the bot (`POST /internal/license/activate`, with core's token), and the bot sends it to the license server.
Without `BOBRES_LICENSE_SERVER` on the bot, the form answers "Online activation is not set up" (503
`not_configured`).

An activated license is then renewed once a day through the bot (`/internal/license/refresh`). After a failure, core
asks again after 3 hours. A failed refresh changes nothing: the token simply runs into its grace and expiry. See
05-license.md, "Activation and daily refresh".

## Uninstall
Stops services, removes containers/units; data kept unless `--purge`; prints what remains. `--purge` deletes the
install directory, and with it every backup in `/opt/bobres/backups`: it warns first (copy them elsewhere).

## Open points
- Installer and CLI binaries: GitHub releases of this (public) repository. Images: ghcr.io, which must be
  public for customers to pull without logging in (private registry auth via license is still open).
- Re-running the one-liner on an existing install updates the CLI and repairs the install, but keeps the
  running image version; moving to a new version is `bobres update` (with backup and rollback).
- Keeping the previous images for N days: not built; a rollback pulls the previous images again if they were pruned.
  (The stable/beta update channel is built: see Restore, update and rollback.)
- Release signatures expire 45 days after signing: a stable channel with no release for that long must be re-signed,
  or install.sh and `bobres update` refuse it ([releases runbook](runbooks/releases.md)). Re-signing moves the
  release's `# issued` day, which `updates_until` is compared with; `tools/sign/sign.sh` has no option to keep it.
- Non-Docker (systemd binaries) mode: not planned for v1.
- Installing 3x-ui itself for customers who do not have one yet (optional helper, later).
