# 06 - Installer and `bobres` CLI spec

Target OS: Ubuntu 22.04/24.04 and Debian 11/12/13, amd64 + arm64. Root or sudo required.

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
The wizard asks only what has no safe default: the domain (unless `--domain` or `BOBRES_DOMAIN`), the owner's
Telegram id (`--admin-id` or `BOBRES_ADMIN_TELEGRAM_ID`; several owners: their ids separated by commas, the first is the
main owner), the bot token (hidden) and the optional first 3x-ui panel
with its API token (hidden). Everything else is automatic. A panel on the same server typed as
`http://127.0.0.1:<port>/...` is rewritten, without a question, to `http://host.docker.internal:<port>/...` (what the
containers can reach), and a plain-http panel on a private address is taken as a private panel. See "What you see
while it installs" below for the banner, the progress bar and the log.

The 3x-ui panel is optional (milestone 6a): a fresh install still asks for one (Enter skips it), a re-run
never asks, and a URL needs its token (and a token its URL). A panel that is new in a run is **checked before
anything is written**, with the client and calls the provisioner uses (the inbound listing, which needs the token, and
the version): reachable (the reason is said when it is not: the name does not resolve, nothing listens on the port, no
answer in time, the certificate is not trusted, plain http where https is expected, a wrong web base path), a
and the API token accepted (a version outside v3.8.5 to v3.9.0 is noted, not refused: the provisioner and the dashboard only warn about it). A refused token prints "In 3x-ui open Settings, then API
tokens (Security), create a token and paste it here." and is asked again, three more times at most; Enter skips the
panel (add the server later in the dashboard); with `--yes` or no terminal it is an error, never a silent accept, and
nothing has been written. The check runs from this server, while the provisioner runs in a container and reaches a
panel on this server as `host.docker.internal`: the check goes to `127.0.0.1` there and says so. `--offline` skips it. The provisioner registers that panel only while
it has no server; after that servers are added and changed in the dashboard (Servers), and `.env` edits of
`BOBRES_XUI_*` change nothing. Without a panel the summary says to add a server in the dashboard before selling.
Every client BOBRES makes in a 3x-ui panel is put in the panel's client group `bobres_bot` (filter on it in the panel's
Clients page to see what the bot made; clients made by an earlier version are put in it once after the update). A client
you move to another group by hand keeps it. Marzban has no groups.
Off-server copies of the backups in S3-compatible storage are not offered by the wizard (they are off by default):
the summary ends with the one-line way to turn them on (see Backups, "Off-server copies").

## Management menu
**Header: the status dashboard.** Beside the beaver the header shows, one line each, the store (`running (9/9
services)`, `partly running (7/9 services) - down: bot, payments`, `stopped` or `unknown (is Docker running?)`), the
license in effect (`pro license, 212 days left`, `trial, 9 days left`, a license in its grace period or expired, `free
tier`), HTTPS (the mode, and how many days the certificate this server serves for the domain has left; core's own
count when this host cannot reach port 443), the newest backup (`newest 5 hours ago (12.3 MB)`, yellow past 48
hours) and the dashboard address. The same facts are `bobres status --json`. The probes run side by side, each with
a 3-second limit, so the menu opens in about three seconds at worst; whatever does not answer shows `unknown`
instead of hanging the menu. Yellow is a warning (a license or certificate within 7 days of its end, an old backup),
red is a problem.

`bobres` without a command, on a terminal, opens a numbered menu in the spirit of the x-ui script: status,
live logs, start, stop, restart, show settings (secrets masked), change the bot token, the owner's Telegram
ID or the first 3x-ui panel, back up now, show the backup key (after a confirmation), restore a backup (the newest 15
in `backups/` by number, or any file by path with an optional key file), update (the `--check` output, then
`bobres update` when a newer release exists), roll back the last update, check the server (`bobres doctor`), check
everything (`bobres check`), the HTTPS certificate (a submenu, see HTTPS certificates), uninstall (a submenu: the
containers only, everything, or the leftover scan; "everything" asks what the command asks). Restore, update
and rollback run the commands' own code, confirmation included; after an update or a rollback the menu ends, because
the `bobres` running it was replaced (run `bobres` again). Settings changes re-run the installer with the one new
value, so they are validated the same way (bot token and panel checked online) and only the affected
services are recreated. Scripts get the usage text instead, never a prompt. "Show settings" no longer reports
Zarinpal from `.env`: with the payments key the gateway is managed in the dashboard (Payment gateways), and
without it the menu says to re-run `bobres install`. The panel item says that the panel is used only until the
dashboard has a server.

## `bobres install` steps
1. **Preflight:** OS/arch, RAM (a 1 GB server passes with a warning: refused under 900 MB of MemTotal, since a 1 GB server reports a little under 1024; warns under 1800 MB, below a 2 GB server), disk (>=10 GB), ports 80/443 free, clock sync, Docker/compose versions, outbound access to Telegram API, registry and license server.
2. **DNS check:** domain resolves to this server's public IP; explain exactly what to fix if not.
3. **Wizard** (skipped when flags given): domain, bot token (validated via getMe), admin Telegram ID, 3x-ui URL + API token (validated: reachable, version detected, compatible range), enabled gateways + keys, default language, brand name.
4. **License:** activate key (or start trial), show entitlements.
5. **Generate secrets:** DB/Redis passwords, internal service keys, session keys, one master key per service
   (`BOBRES_CORE_MASTER_KEY`, `BOBRES_PROVISIONER_MASTER_KEY`, `BOBRES_PAYMENTS_MASTER_KEY`). Write `/opt/bobres/.env`
   (chmod 600, owned by service user). A re-run keeps every secret and fills only missing ones, so an install from
   before milestone 6a gets its payments key then; until that re-run, compose passes an empty key (`:-`) and payments
   keeps running Zarinpal from `.env`. The Zarinpal merchant id, proxy and sandbox in `.env` only seed the gateway
   settings on the first start with the key; the public URL and `BOBRES_ZARINPAL_HOST` stay in `.env` (Caddy).
6. **Render** `docker-compose.yml`, `caddy/Caddyfile` and the default `tls/` files from templates; pull signed images
   (verify signatures).
7. **Start** infra (Postgres, Redis), run migrations, start services, wait for health.
8. **Harden** (see Hardening below): ufw (the SSH port, 80, 443), fail2ban for SSH, unattended-upgrades (security
   only), a swap file on a small server. They run by themselves where that is safe, one line each; `--no-harden`
   skips them all, `--ask-harden` asks before each step, and `bobres harden` runs them later. The services already run as non-root users in their containers, so there is no
   separate system user.
9. **Verify:** hit /readyz, send test message to admin ("BOBRES is live"), print summary with URLs and first-login link.
10. **Idempotent:** re-running detects an existing install and offers repair/upgrade instead of overwriting.

What the built installer does differently from this list:
- **Preflight** runs `bobres doctor`'s checks: OS and CPU, RAM, disk, ports 80/443, Docker and Compose, and DNS (a DNS
  failure only warns, since certificates are issued once DNS points here). There is no clock, registry or
  license-server check.
- **The wizard** asks for the domain, the owner's Telegram ID, the bot token (checked with getMe, on every run without
  `--offline`, the menu's changes included: through the Bot API server in `BOBRES_TELEGRAM_API_URL` when it is set,
  with `host.docker.internal` read as 127.0.0.1, else api.telegram.org) and an optional first 3x-ui panel (checked,
  see above). The first panel is named after its host (`panel.example.com`; `default` only when that is not a usable name) and can be renamed on the
  dashboard's Servers page. The S3 settings and the hardening are not asked (flags, and the dashboard or `bobres harden` later).
  Payment methods, the default language and the brand name are set later on the
  dashboard (Zarinpal can also come from flags).
- **The license** is a separate step: `bobres license activate` or the dashboard's License tab (License activation,
  below). An install without one runs on its 30-day trial, then on the free tier (05-license.md, "Free core, paid
  premium"): it is never blocked, and the free tier's limits refuse only new actions.
- **Images:** BOBRES's own five (core, bot, payments, provisioner, backup) run by digest, not by tag. The release
  workflow reads each image's digest from ghcr.io (anonymously, as a store pulls it), checks that the digest carries
  the keyless cosign signature of that workflow for that tag, and builds the digests into the CLI
  (`bobres version --images` lists them). The CLI's checksum is in the Ed25519-signed `checksums.txt`, which
  install.sh and `bobres update` verify before they run it. The install writes the digests to `.env`
  (`BOBRES_IMAGE_DIGEST_<SERVICE>=@sha256:...`) and `docker-compose.yml` appends them to each image's tag, so Docker
  pulls exactly that content, whatever the tag points to now. The servers do not check cosign signatures
  themselves. The images run by tag when `BOBRES_VERSION` is not the CLI's own version (`--version` with another
  version, or a newer CLI from the one-liner on an install that keeps its version until `bobres update`), and with
  a development build. Postgres and Redis (`postgres:16-alpine`, `redis:7-alpine`) are pinned by digest like Caddy (the multi-arch index
  digest in `deploy/docker-compose.yml`; the backup image's `postgres` base in `deploy/Dockerfile` has the same one), so
  Docker checks what it pulls against it; `deploy/images_pinned_test.go` fails if one loses its digest. Caddy
  is pinned by version and digest (`caddy:2.11.7-alpine@sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f`,
  the multi-arch index digest), so an update that changes the reference pulls the new image, and a moved tag changes
  nothing (see HTTPS certificates).
- **Verify:** the install waits until every service is healthy and prints the first steps and the backup key. It
  sends no test message.
- **A re-run** keeps every secret, fills only missing values and recreates what changed.

## What you see while it installs
- **The banner.** install.sh, `bobres install`, `bobres update` and `bobres menu` start with the BOBRES banner: the
  beaver, the wordmark, the version (and commit and build date when known), the tagline *Your VPN store on Telegram*,
  `© 2026 BOBRES, all rights reserved`, `Made by TECSO-DEV`, the license summary, and the site, docs, issues and
  security links.
  It is drawn in the project's palette (navy, teal, brown, cream) with truecolor or 256-color escapes where the
  terminal has them, else the 8 basic colors, else none. `bobres version`, `--version` and `bobres help` show a
  one-line form on a terminal; `bobres about` shows the banner and everything else in one place. Machine-read
  commands (`config get`, `admin link`, `secrets`, `support-bundle`, `license status`, `doctor`) never get a banner.
- **One progress bar.** The install is a list of steps with weights (install.sh: checking the system, Docker, the
  download, the signature and checksum, the hand-over; `bobres install`: your settings, the checks, the files, the
  images, the stack, the health of the services, hardening, the summary). The bar,
  `[██████████░░░░░░░░░░]  42%  Step 6/13: Pulling images  ⠹ 0:38`, is the weights of the finished steps plus the
  measured part of the running one (images pulled, services healthy). It never goes down, shows 100% only when the
  install finished, and a failed step leaves it where it was with a line that says which step. install.sh hands its
  share to `bobres install` (the internal variable `BOBRES_PROGRESS=percent:share:steps done:steps in all`), so it is
  one bar from 0 to 100; run alone, `bobres install` shows 0 to 100, and `bobres update` its own five steps, with the
  install it starts inside the fourth. The bar is hidden while a question waits for input.
- **Marks and the summary.** Each step ends with a line: a check, a warning sign or a cross (`OK`, `WARN`, `FAIL`
  anywhere that is not a terminal), its name and the time it took. The install ends with the *BOBRES is ready* card:
  the domain and https address, the bot, how to log in (send `/start` to the bot first, then `/dashboard` or
  `sudo bobres admin link`), a table of the services, the license (the 30-day trial and its days left), the install
  directory and log, three next steps, the links and the copyright line.
- **The log.** What programs print (Docker's installer, apt, `docker compose`, the hardening commands) goes to
  `/var/log/bobres-install.log` (mode 600, never a secret), not to the screen. A failure shows the last 15 lines
  and what to do. `BOBRES_INSTALL_LOG` moves it.
- **If the SSH connection drops.** Once the questions are answered the install ignores the hang-up and keeps going
  (install.sh does from the start); follow it from a new session with `tail -f /var/log/bobres-install.log`, and
  run `sudo bobres install` again if it was stopped (it keeps the settings and secrets). Connect with
  `ssh -o ServerAliveInterval=30 ...` to keep the connection alive.
- **Plain mode.** When stdout is not a terminal (a pipe, `curl | sudo bash`, CI, `ssh` without a tty), `NO_COLOR` is
  set, `TERM` is `dumb` or unset, the terminal is under 60 columns, `--plain` is given or `BOBRES_PLAIN=1`: no escape
  sequences, no carriage returns, no spinner, and ASCII only (the banner is also ASCII when the locale is not
  UTF-8: `LC_ALL`, `LC_CTYPE`, `LANG`). Progress is one line per change, easy to grep:
  `[ 42%] Step 6/13: Pulling images`, `[ 55%] Step 6/13: OK Pulling images (38s)`, `... SKIP Installing Docker
  (already installed)`, `... FAIL Pulling images (exit 1)`, `[100%] Done in 2m14s`. In plain mode the banner is shown
  once for install logs.
- **Switches.** `--no-banner` or `BOBRES_NO_BANNER=1` leave the banner out; `--plain` or `BOBRES_PLAIN=1` force plain
  mode (every command takes the two flags; install.sh passes them on). `BOBRES_FORCE_TTY=1` makes output look like a
  terminal (tests only). The cursor is hidden only while a spinner runs and is shown again on every way out,
  Ctrl-C and errors included.

## Bot profile photo
The bot sets its own menu commands, description, short description and profile photo (08-brand.md, "Telegram
bot profile"). The installer sets no photo; the summary's first steps point to the upload: send `/dashboard` to
the bot for a login link, then **Branding & texts → Store → Telegram bot** (a square PNG or JPEG; 640x640 looks
best, `docs/brand/bot-avatar.jpg` is the BOBRES beaver). The bot sets it in Telegram within seconds. With a local
Bot API server (`BOBRES_TELEGRAM_API_URL`), it must be version 9.4 or newer. The bot's name stays @BotFather's
(`/setname`).

## Layout on server
`/opt/bobres/{bobres.yml, .env, docker-compose.yml, caddy/, tls/, run/update/, run/monitor/, data/, backups/, logs/}`, and
`.restore-point/` after an update (root only); state in named volumes. install.sh and `bobres update` record the last
verified key list and release in `/var/lib/bobres/{keys.seq,release.version}` (`$BOBRES_STATE_DIR`), and `bobres install` and
`bobres harden` keep the install receipt there (`install-receipt.json`; see Uninstall). `caddy/` and `tls/`
are the HTTPS layout (see HTTPS certificates). `run/update/` is where the dashboard and its updater meet (owned by uid
65532, mode 0700: `request.json` and `status.json`; see Updating from the dashboard), `.update.lock` is the lock of
updates, rollbacks and restores and `.updater-handled` the ids of the requests the updater ran (both root's, mode 0600).
`/etc/systemd/system/bobres-updater.{path,service}` are the updater's units; `run/monitor/` (root's, mode 0755, read only in core) holds the host collector's `snapshot.json` and `/etc/systemd/system/bobres-monitor.{timer,service}` are its units (see Host monitoring). An install made before P8 also keeps its
old top-level `Caddyfile`.

## Status (2026-10-12)
Implemented: `install`, the menu, `status`, `logs`, `start`, `stop`, `restart`, `uninstall [--purge]`, `doctor`,
`version`, `admin link` (2026-10-07), `license status|import` (2026-10-11), `license activate` (2026-10-12, see
License activation), `backup` (2026-10-11, see Backups),
`restore`, `update` and `rollback` (2026-10-12, see Restore, update and rollback), `support-bundle` (2026-10-12, see
Support bundle), `harden` and the install's hardening step, `config list|get|set`, `secrets rotate`, the menu's
restore/update/rollback items and the update's install time limit (2026-10-12, see Hardening, Settings and secrets),
and off-server backup copies with `restore s3://...` (2026-10-12, P7d, see Backups), and `ssl` with the menu's
HTTPS item and doctor's TLS line (P8, see HTTPS certificates).
The bootstrap `install.sh` downloads the latest GitHub release, verifies it and hands over to `bobres install`.
Operator runbooks: [runbooks/](runbooks/README.md).
`admin add/reset-2fa` was dropped (2026-10-10): `admin link` already gets anyone on the staff in without
a password or code, the owner manages staff on the dashboard's Staff page, and the owner themselves is
changed with `bobres menu` ("Owners") or `bobres admin owners`, which core applies when it starts.

## CLI commands
| Command | Purpose |
|---|---|
| `bobres` / `bobres menu` | Management menu (terminal only) |
| `bobres install` | Set up (and record what it did in the install receipt) |
| `bobres uninstall [--purge] [--dry-run] [--verify] [--backup-to F] [--no-backup] [--keep-backups] [--yes --confirm T] [--remove-docker] [--remove-packages] [--disable-firewall]` | Remove the containers (data kept); `--purge` removes everything BOBRES put on the server, as on day one, after a plan, an optional verified final backup and a typed confirmation; `--verify` scans for what is left (see Uninstall) |
| `bobres status [--json]` | Services (state and health), version, domain, free disk; exit 1 when a service is not healthy. `--json` prints the menu header's dashboard as one object instead: `installed`, `dir`, `domain`, `version`, `dashboard_url`, `services` (`state`: `running`, `partly running`, `stopped`, `unknown`; `up`, `total`, `down`), `license` (`known`, `basis`, `tier`, `state`, `days_left`), `https` (`mode`, `cert_days_left`), `backup` (`enabled`, `newest`, `age_hours`, `size_bytes`) and `disk_free_gb`; a value that could not be read is `null`/`known: false`. Same exit codes |
| `bobres check [--dir D] [--json]` | Every check of the install in one list, a line per check, `PASS`/`WARN`/`FAIL`/`SKIP`; exit 1 when one FAILs (see Check). `--json` prints `{results:[{name,status,detail}], pass, warn, fail}` |
| `bobres monitor status [--json] \| enable \| disable \| snapshot [--print] [--out DIR]` | The host collector for the dashboard's Monitoring section: whether it runs and how old its snapshot is, put it in place or take it away for good (`BOBRES_MONITOR_COLLECTOR`), or take one snapshot now (what the timer runs; see Host monitoring) |
| `bobres logs [service] [-f]` | Tail logs |
| `bobres start / stop / restart` | Start (and wait for health) / stop / restart the stack |
| `bobres update [--check] [--version X] [--channel stable\|beta] [--yes]` | Verify the signed release (the newest of the update channel, or X), back up, leave a restore point, swap in the new CLI, run its install, health check, automatic rollback. `--check` only says whether a newer verified release exists (with a note when it is newer than the license's update date: the update is never refused for it). One update, rollback or restore at a time (a lock). `bobres update --from-request` is the same update for the dashboard's request, run by `bobres-updater.service`; it is not in the help and not for people (see Updating from the dashboard) |
| `bobres rollback [--yes]` | Undo the last update with its restore point: the previous CLI, install files and version, and the database from before the update |
| `bobres backup` | Back up now: runs `/app run-now` in the backup container, which asks the running service (one backup at a time) and prints the result (see Backups) |
| `bobres restore <file \| s3://bucket/key> [--key-file F] [--yes] [--keep-keys]` | Replace the database with a backup's (verified first, the current data backed up first, one transaction); the backup's master keys go into `.env`. A backup in S3-compatible storage is downloaded into `backups/` first, with the install's S3 settings (see Off-server copies) |
| `bobres config list / get <key> / set <key> <value>` | Show the settings (secrets masked) or change one of the safe ones: validated as install does, `.env` written in one rename (0600), only the services that read it recreated (see Settings and secrets). `BOBRES_UPDATE_FROM_DASHBOARD on\|off` installs or removes the dashboard's updater instead of recreating a container |
| `bobres secrets rotate <name>` | `bot-token`, `db-passwords [--role R]`, `service-tokens` or `redis-password`: new values, the affected services restarted, `.env` rolled back when they do not come back healthy. Master keys and the backup key are not rotated |
| `bobres about [--dir D]` | The banner and everything in one place: version, commit, build date, license summary and copyright, the vendor license key's id and fingerprint (derived from the key built into this bobres), the store installed here, the links (site, docs, issues, security reports), a pointer to `THIRD_PARTY_NOTICES.md` and `LICENSE`, and the note that the license text is a draft |
| `bobres harden [--yes] [--ssh-port N] [--dir DIR]` | The install's hardening step on its own: firewall, fail2ban, security updates, swap, each asked (see Hardening) |
| `bobres license status` / `license import < token.txt` | Show the license as core sees it (state, install id to send the vendor, entitlements, how it came) / install a token read from stdin (verified first; audited as `license.set` from the CLI). Both run `/app license ...` in the core container |
| `bobres license activate [<key>] [--server URL]` | Turn a license key from the seller into this install's license, through the vendor's license server (`BOBRES_LICENSE_SERVER`, set with `bobres config set`, or `--server` for one run; https only). Run it without `<key>`: it asks for the key without echoing it, or reads it from stdin (`< key.txt`). A key given as the argument stays in the shell history and in sudo's log. Core then renews the license daily (see License activation) |
| `bobres ssl [status] / auto / ip [--ip X] [--yes] / manual --cert FILE --key FILE` | The HTTPS certificate: show it, go back to the automatic one for the domain, add a Let's Encrypt certificate for the server's IP address, or use your own certificate and key. Each runs `/app ssl ...` in the core container (see HTTPS certificates) |
| `bobres doctor [--dir D] [--domain X] [--offline]` | Diagnose: OS, RAM, disk, ports, Docker, DNS, the TLS certificate of every address the store serves (a crash-looping Caddy is a FAIL), the hardening state (firewall, fail2ban, security updates, swap; warnings, never failures) and the dashboard's updater (its units; a warning, never a failure). `--dir` is the install it reads `.env` and core's address list from; `--domain` defaults to its `BOBRES_DOMAIN` |
| `bobres admin link [telegram id]` | One-time dashboard login link printed on the server, for when the bot is down (default: the owner). It skips the password and code, so it is also the way back after a lost authenticator. It needs the account to exist (the owner started the bot once); with no Telegram, use `admin password` |
| `bobres admin owners list\|add\|remove [--dir D] [--no-restart] [telegram id]` | The store's owners (root admins), named in `BOBRES_ADMIN_TELEGRAM_ID`: list them, add one, remove one (the setting is validated like `bobres config set`, written to `.env`, and only core and the bot are recreated). Only the server can do this: the dashboard and the bot cannot make or unmake a root admin. The first is the main owner, who takes no staff seat; every other owner takes one. Removing an id never locks anyone out by itself: an account that already signed in keeps the owner role until it is demoted in the dashboard, and the command says so |
| `bobres admin password [--dir D] [--id N]` | Reset the owner's dashboard password (the main owner's, or the owner with Telegram id N): runs `/app admin-password` in the core container, prints the login `owner` and a new random password once, and says it must be changed at first login. Creates the owner's account when the owner never sent `/start`. Running it again resets the login and ends the owner's sessions (the menu has it as "Reset admin password") |
| `bobres support-bundle [--offline]` | A redacted tar.gz for support in the install directory: versions, `compose ps`, the doctor's checks, the newest 300 log lines per service, the `.env` key names (never a value), disk and memory, the backups list (see Support bundle). Never the database, a backup or a secret value |

## Check

`bobres check [--dir D] [--json]` is `bobres doctor` for an install that is already running: doctor asks whether a
server is fit to install on, check asks whether this install is healthy. The menu has it as "Check everything". It
changes nothing and takes up to a minute when the network is slow (every probe has a limit). The host's side
runs in `bobres`; core's side runs `/app check --json` in the core container (`docker compose exec -T core /app
check --json`), which prints one JSON array of `{name, status, detail}` and exits 1 when a check failed (the host
reads the array either way).

| Check | PASS | What to do otherwise |
|---|---|---|
| Config | `.env` has every generated secret (the installer's lengths), the domain, the bot token's shape, a numeric owner id and the Redis address | FAIL names the missing or malformed keys (never a value): `sudo bobres install` fills in missing secrets, `bobres config list` shows what is set |
| Config permissions | `.env` is readable by its owner only | WARN: `chmod 600 .env` |
| Services | Every service this install runs is running and healthy | FAIL names the ones that are not: `bobres status`, `bobres logs <service>` |
| Disk | At least the free space doctor asks for | FAIL: free some space before a backup or an update fills the disk |
| Database, Redis | Core is connected (checked from inside core) | FAIL: `bobres logs postgres`, `bobres logs redis` |
| License | A license with at least 14 days left, or a trial with at least 7 | WARN: renew or buy (`bobres license`); FAIL while a license is in its grace period; free tier: WARN |
| 3x-ui servers | Every enabled server passed its last health check | FAIL lists each down server with the provisioner's last error; WARN when none is added yet or the provisioner did not answer (`bobres logs provisioner`) |
| Watchdog | No open alert | WARN lists them (dashboard: Alerts) |
| HTTPS (one line per address) | The certificate this server serves on port 443 is valid and covers the address | As in doctor and `bobres ssl status` |
| Dashboard updates | `bobres-updater.path` is enabled and active, and the bobres its service runs is there (or the setting `BOBRES_UPDATE_FROM_DASHBOARD=off` is on and the units are gone) | WARN, never FAIL: not installed (update once with `sudo bobres update`), not enabled, not running or failed (`systemctl status bobres-updater.path`; `sudo bobres update` or `sudo bobres install` set it up again), or switched off but still installed |
| Backup | The newest backup is under 48 hours old | WARN when older (`bobres logs backup`, `bobres backup`); FAIL when there is none: back up and keep the backup key |
| Release signature | The release this server runs was signed with an expiry more than 14 days away | WARN/FAIL: publish or re-sign a newer release; updates and reinstalls are refused after the signed expiry. SKIP when the release manifest was not kept in the state directory (`/var/lib/bobres/checksums.txt`) |
| Bot token | Telegram accepts the token (`getMe`; shows `@username`, through the self-hosted Bot API server when `BOBRES_TELEGRAM_API_URL` is set) | FAIL: `sudo bobres secrets rotate bot-token` |
| Bot updates | Long polling or a webhook (only its host is shown) with no recent delivery error and under 100 waiting updates | WARN: is the bot running? `bobres logs bot` |

The bot token never appears in the output, not even in an error message: it is scrubbed from everything printed.
Nothing the check prints is a secret value.

## Backups
The `backup` service (image `bobres-backup`, a `postgres:16-alpine` target in `deploy/Dockerfile`, so its pg_dump
matches the server) backs up the whole `bobres` database every day at `BOBRES_BACKUP_TIME` (default 03:30) in
`BOBRES_TIMEZONE`, and on demand (`bobres backup`, the menu, or the dashboard's System page, which calls its HTTP API
with `BOBRES_TOKEN_CORE_BACKUP`, a token core uses for this service only). Each run:
- refuses to start when `/opt/bobres/backups` has less free space than twice the last backup (100 MiB before the
  first), and records why;
- streams `pg_dump -Fc` (as the read-only role `svc_backup`; the password goes in `PGPASSWORD`, never on a command
  line) straight into the encrypted file format of `internal/backup`, written to a temp file, synced and renamed to
  `bobres-<UTC stamp>.bkp`, with a `.json` sidecar (size, sha256, created_at, sent_at, telegram_error, error, and with
  the S3 upload uploaded_at, uploaded_to, upload_error);
- hands the file to the bot (`POST /internal/backups/deliver`, token `BOBRES_TOKEN_BACKUP`), which streams it to the
  owner's Telegram only, as it arrives (never kept in the bot's memory or its tmpfs `/tmp`, which share its 512 MB
  limit); over 50 MB, or 2000 MB with a local Bot API, the owner gets a text instead and the file stays
  here. Without `BOBRES_TOKEN_BACKUP` backups are kept on the server only;
- copies the file to S3-compatible storage when that is set up (below), and records how it went;
- keeps the newest 7 backups (and at most 7 records of failed runs). A failed run leaves its sidecar, no `.bkp`.
  The CLI's safety backups (`bobres-<stamp>-before-restore.bkp`, `-before-rollback.bkp`, below) are listed with
  them but neither counted nor pruned: the operator deletes them.

**Off-server copies (S3-compatible, optional; milestone P7d).** A backup that only lives on the server is lost with
the server, and Telegram takes files up to 50 MB. With `BOBRES_BACKUP_S3_ENDPOINT` set, the backup service copies
each new backup, right after the Telegram delivery, to an S3-compatible bucket (Arvan Cloud, MinIO, Backblaze B2's
S3 API, AWS S3):
- **Settings** (`.env`, passed to the backup service only): `BOBRES_BACKUP_S3_ENDPOINT` (https only, no path),
  `_BUCKET`, `_REGION` (signs the requests; empty: `us-east-1`), `_PREFIX` (a folder in the bucket; empty: none),
  `_ACCESS_KEY`, `_SECRET_KEY`, and `_KEEP` (how many backups the bucket keeps; default 30). Empty endpoint = off.
  `bobres install` never offers it (the summary prints the one-line command); with an endpoint from a flag, a bucket
  or key that is missing is asked (the secret key without echo), and a re-run never asks. Flags: `--s3-endpoint`, `--s3-bucket`,
  `--s3-region`, `--s3-prefix`; the keys come from `BOBRES_BACKUP_S3_ACCESS_KEY` / `_SECRET_KEY` in the environment
  or are asked when missing (never flags: `ps` and the shell history keep command lines), and `--yes` without them
  is an error. The plain settings are `bobres config` settings (below); to replace a key, edit `.env` and run
  `sudo bobres install --skip-checks`. The installer and `config set` check them as the service does
  (`config.ParseBackupS3`). In the backup service a setting that cannot be used never stops it: local backups and
  Telegram go on, the log says why at startup, and each backup records it as its upload error.
- **Addressing.** An endpoint that is the bucket name on a service host of three or more labels
  (`https://my-shop.s3.example.com`) is used as it is (virtual-hosted style); any other endpoint
  (`https://s3.example.com`) gets the bucket as the first path segment (path style), which every one of the services
  above accepts. The three labels keep a server that only shares its first label with the bucket (MinIO at
  `https://backups.example.com` with a bucket named `backups`) in path style.
- **Requests.** `internal/s3`: AWS Signature Version 4 with the standard library only (no SDK), each request signed
  with the SHA-256 of its payload (never `UNSIGNED-PAYLOAD`, so the storage refuses a body changed on the way). A file
  up to 100 MiB goes in one PUT; a larger one as a multipart upload in 16 MiB parts (more for a file that would need
  over 10,000), aborted when a part fails so no parts are left to pay for. A request that cannot reach the storage, or
  gets 5xx or 429, is tried three times (1 s, then 4 s apart); redirects are reported, never followed. The upload has
  two hours. The signer is tested against the AWS documentation's S3 examples, and everything against an in-memory
  S3 server (`internal/s3/s3test`) that checks each signature from the wire request and each payload hash.
- **What it uploads.** The `.bkp` file as it is on disk: already encrypted with `BOBRES_BACKUP_KEY` (which never
  leaves the server), so the bucket holds nothing readable without that key. The object is `<prefix>/<file name>`.
- **Retention.** After each successful upload the bucket keeps the newest `_KEEP` objects named
  `<prefix>/bobres-<stamp>.bkp`; older ones are deleted. Nothing else is touched: other names, other folders, the
  CLI's safety backups (which are not uploaded). A failed upload prunes nothing, and a failed prune is only logged
  (the next backup tries again).
- **Failures** never fail the local backup or the Telegram delivery: the sidecar gets `upload_error` (and the run
  stays a success), or `uploaded_at` and `uploaded_to` (`s3://bucket/key`). The System page's Backups tab shows it per
  backup; `GET /internal/backups` also answers `offsite` (`configured`, `endpoint_host`, `bucket`, `prefix`, `keep`,
  and `error` when the settings cannot be used; never the keys). `bobres backup` prints where the copy went or why
  not, and the support bundle's `backups.txt` lists it.
- **Network.** The backup service has no internet without the upload: its second network is `offline` (internal,
  no other service on it). With an endpoint the installer and `bobres config` set `BOBRES_BACKUP_NETWORK=egress`
  (it follows the endpoint: shown by `config list`, never set by hand), and compose puts the service on `egress`.
  So the container that holds the master keys reaches the internet only when the owner turns the copy on.
- **Restore** from the bucket: `sudo bobres restore s3://<bucket>/<prefix>/bobres-<stamp>.bkp` (below), or download
  the file with any S3 tool and restore it by its path.

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

**`bobres restore <file | s3://bucket/key> [--key-file F] [--yes] [--keep-keys]`** replaces the database with a backup's.
0. **From S3-compatible storage** (`s3://bucket/key`): after the key checks of step 1, the object is downloaded with
   this install's S3 settings (endpoint, region and keys; the bucket is the one named, so a new server restores from
   the old server's bucket once `bobres install` has the S3 settings) into a new folder `backups/.s3-restore-*`,
   owned by root with mode 0700 (the file 0600), so the backup service's user, who owns `backups/`, cannot write to
   the download. That user can still rename the folder (a rename within `backups/` needs write permission on
   `backups/` only), so the download is never opened again by its path: the check and the restore read the file
   through the one descriptor it was downloaded with (step 2). It is removed when the command ends, whatever the
   outcome (a killed run leaves it: delete it). Without S3 settings, or when the object is missing, nothing changes
   and the error says so. Every later message names the `s3://` source.
1. The key is `BOBRES_BACKUP_KEY` from `.env`, or the one line of `--key-file` for a backup made on another server (a
   fresh install there made its own key; a line copied from `.env`, `BOBRES_BACKUP_KEY=...`, works too). An install
   with no `BOBRES_BACKUP_KEY` of its own is refused first, with or without `--key-file`: the safety backup (step 5)
   needs it, and `sudo bobres install` creates it (keeping the settings).
2. The whole file is verified before anything is touched: a wrong key, a damaged or cut file and a short key are
   refused. It prints when the backup was made, the size of the dump and the master key names (never the values).
   The file is opened once: the restore (step 6) reads the same open file from its start, so what is restored is
   what was checked, shown and confirmed, whatever is put at its path in between (a pipe, which cannot be read
   twice, is refused).
3. It asks, unless `--yes`: the current data is replaced.
4. It stops core, bot, payments, provisioner and backup; postgres, redis, caddy and the tls-agent stay up. First, so that nothing is
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
The CLI reads `.env` as Compose does: in an unquoted value, ` #` starts a comment (`KEY=value # note` is `value`); a
quoted value keeps its `#` and ends at its closing quote. A rewrite keeps such a comment at the end of its key's line
(an empty value before one is written `''`); comment lines of their own are not kept.

**Update channel.** `BOBRES_UPDATE_CHANNEL=stable|beta` in `.env` (unset: stable; the bot gets it through the compose
file and fails to start on another value, so it is set with `bobres config set BOBRES_UPDATE_CHANNEL beta`, which
checks it and recreates the bot), overridden for one run by `bobres update --channel`. `internal/release`
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

**License update date (`updates_until`), advisory.** The signed header's `# issued` (`release.ParseHeader`) is the release's
day (UTC). When the install's license is in force (verified for it, and active or in its grace period) and carries
`updates_until`, a release issued after that day is not covered (`release.Covered`). That only adds a note, and
**never refuses, hides or withholds an update**: security updates are never withheld, and the free tier, the trial and an
ended license get every release, so a license that stops at a date must not get less than no license. `bobres update`
prints "Note: this release (issued on D) is newer than your license's update date (U). It is installed all the same" and
goes on; `--check` says "Newer: yes. Update with: sudo bobres update" followed by the same note, and the menu offers the
update (it asks before it changes anything, as always). The CLI reads the license with `compose exec -T core /app
license status` (its "State" and "Updates until" rows, which every core with a license command prints, the latter only
for a license in force); when core cannot be asked it warns that the note is skipped and the update goes on. A
missing, invalid or ended license (the free tier and the trial have no `updates_until`), a license without
`updates_until` or a release without `# issued` has no note. The dashboard's System page gets the same answer from core
(`updates.covered`: core compares the bot's `issued` with its own license; absent when there is no valid license or no
date, `false` for a release after the date): the Updates tab shows the update with the command as for any release, plus
an informational line and a link to the License tab for `covered: false`.

**`bobres update --check [--version X] [--channel C]`** fetches `checksums.txt`, its signature, `keys.txt` and
`keys.txt.sig.N` of the channel's newest release (or of `releases/download/vX`, `release.DownloadURL`), verifies them
as install.sh does (the embedded roots, `internal/release`, and the key list's `seq` against `keys.seq`, below) and
prints the current version, the channel, the release's version and whether it is newer (with the advisory note when it is
newer than the license's update date; a release older than `release.version` is "Newer: yes, but older than release X, which this server installed
before", which `bobres update` refuses, so the menu does not offer it). It needs no install and works on a development
build.

**`bobres update [--version X] [--channel C] [--yes]`**
1. Verifies the release, as install.sh's `verify_release` does. It refuses an unsigned, tampered, revoked or expired
   release; a key list whose `seq` is lower than `$BOBRES_STATE_DIR/keys.seq` (default `/var/lib/bobres`: an older
   list, still signed and unexpired, may name a release key revoked since); a release older than
   `release.version` there or than the install (going back is `rollback`); files under `vX` signed for another
   version; and on stable, a release signed for beta. A release newer than the license's update date is not refused
   (above). The same version
   is nothing to do.
2. Checks for 2 GB free in the install directory and in `/var/lib/docker` (the backup and the new images). The
   install's own checks (10 GB, RAM, ports, the Telegram API) are for a new server and are skipped in step 7.
3. Asks, unless `--yes`.
4. Downloads `bobres_<os>_<arch>` and checks it against the verified `checksums.txt`, before anything changes.
5. **Restore point**, built in `<dir>/.restore-point.new` (mode 0700, root's): the database's migration versions (each
   schema's highest goose `version_id`), a backup of the database (as restore step 5, while the store keeps running),
   copies of `.env`, `docker-compose.yml`, `Caddyfile` (the old top-level one, when the install has it),
   `caddy/Caddyfile` and `postgres-init.sh` in `files/` (only the ones that exist; the folders they sit in are made
   too), a copy of the running CLI (`bobres`), and `restore-point.json` (`from_version`, `to_version`, `backup_file`, `created_at`, `cli_path`,
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
A rollback puts back every file the restore point holds, and leaves the HTTPS layout's folders (`caddy/`, `tls/`) in
place (see HTTPS certificates).

**One update, rollback or restore at a time.** `bobres update`, `bobres rollback` and `bobres restore` (and the
dashboard's updater, below) each take an exclusive lock on `<dir>/.update.lock` (root's file, mode 0600, made when
first needed) and let go when they end, however they end (the system drops it with the process). One that finds the
lock held does not wait: it says "another bobres update, rollback or restore is running on this install. Nothing was
changed." and exits 1. `bobres update --check` takes none. `bobres install` does not either: `bobres update` runs it
while holding the lock itself.

## Updating from the dashboard

The dashboard's **System → Updates** tab can update the store without anyone logging in to the server, and lets the
owner choose between the **stable** channel (releases) and the **beta** channel (pre-releases too). Core, the container
that serves the dashboard, never gets the Docker socket or any other power over the host (it has no internet access
either), so it does not update anything itself. A small helper on the host does, with the code of `bobres update`:
two systemd units that run `bobres update` when core drops a request file.

```
dashboard  ->  core  --- request.json --->  /opt/bobres/run/update/  --- (systemd path unit) --->  bobres-updater.service
                     <--- status.json ----                                  sudo bobres update --from-request, as root
```

**The flow.**
1. An owner presses **Update** (or **Update to X**) in the dashboard. Core refuses while an update shows as running,
   and writes one `request.json` into `/opt/bobres/run/update`, which core sees as `/run/bobres-update`: a temp file,
   then a rename.
2. `bobres-updater.path` (`PathExists=/opt/bobres/run/update/request.json`) starts `bobres-updater.service`, which
   runs `/usr/local/bin/bobres update --from-request --dir /opt/bobres` as root.
3. That command reads the request (rules below), **deletes it**, takes the lock above (it answers "failed: another
   update is running" when it is held), and runs exactly what `bobres update --yes --channel C [--version V]` runs: the
   release is verified against the keys built into bobres (signed checksums, `min_version`, the key list's `seq`, the
   channel), a release older than the install or than `release.version` is refused, then the 2 GB check, the backup and
   restore point, the new bobres, its install, the health check and the **automatic rollback** if the store does not
   come back. There is no separate code path to trust: `update --from-request` calls the function `bobres update` runs.
4. It writes `status.json` at each step (and at least every 30 seconds while it runs); core reads it and the dashboard
   shows the step, one sentence, and the last lines of the output. After a successful run it keeps
   `BOBRES_UPDATE_CHANNEL` in `.env` equal to the channel the request named, with `bobres config set` (so
   `sudo bobres update` and the menu follow what the owner picked, and the bot, which checks for releases, is recreated
   with it), only when that differs.

**The first update still goes over SSH.** A store that runs a release from before the helper has no units and no mount
for core: the dashboard's Updates tab shows the command instead of a button. Run `sudo bobres update` once. The
`bobres update` of that old version swaps in the new bobres and runs *its* install, and that install is what makes
`run/update` (before the containers start, so that Docker does not make it as root), writes the new `docker-compose.yml`
with core's mount, and, once the stack is healthy, installs the two units, enables the path unit and starts it, and
writes `status.json` (`installed: true`, `state: idle`). Every later `bobres install` and `bobres update` checks the units
against the text its own release carries (they are embedded in the binary, `deploy/systemd/`) and rewrites and reloads
them only when that differs, so a release that changes them brings the change. `bobres install` skips them with
`--no-start` (nothing runs), and only warns when they cannot be set up: the store never depends on the helper.

**Protocol, version 1.** Core and the helper agree on exactly this; both ignore nothing and accept nothing else.

| File | Written by | Read by | Rules |
|---|---|---|---|
| `run/update/request.json` | core (a temp file, then a rename) | the helper, once | at most 2 KiB; a regular file owned by core's uid (65532), opened without following a link and without waiting for a pipe; deleted after it is read, **whatever it held** |
| `run/update/status.json` | the helper (a temp file, then a rename), root's file, mode 0644 | core | `installed: true` is how the dashboard knows the helper exists; a missing file means it is not installed yet |

`request.json`: `{"v":1,"id":"<uuid>","action":"update","channel":"stable"|"beta","version":"","by":"<name>","at":"<RFC 3339>"}`.
- All seven fields, no others (an unknown field, a missing one, a second JSON value or trailing text is refused); `v` is 1
  and `action` is `update`. There is no field for a URL, a base URL, an environment variable or a flag, and none can be
  added by sending more: the file is decoded into those fields only and anything else refuses the whole request.
- `id` is a UUID. An id the helper handled before is **ignored** (it keeps the ids of the last day in
  `<dir>/.updater-handled`, root's file out of core's reach), so a request file that comes back runs once.
- `channel` is `stable` or `beta`. `version` is `""` for the newest verified release of the channel, else an exact
  `X.Y.Z` or `X.Y.Z-label.N` (semver without `v` or build metadata, at most 64 characters: no `/`, `..`, space or
  quote can reach a URL or a command).
- `by` is the dashboard user's name: 1 to 64 characters, no control character, line break or right-to-left override (it
  goes into a log).
- `at` is an RFC 3339 time, **not older than 15 minutes and not more than a minute ahead** of this server's clock (core's
  is the same).

`status.json`: `{"v":1,"installed":true,"helper":"0.7.0","state":"idle","id":"","from":"0.6.1","to":"0.7.0","channel":"stable","step":"","message":"","started_at":null,"finished_at":null,"log":[]}`.
- `state` is `idle` (installed or updated, nothing asked since), `running`, `done`, `failed` or `rolled_back`. A result
  (`done`, `failed`, `rolled_back`, with its message and log) stays until the next request, or until a release with
  another helper version is installed (then `idle` again).
- `step` is `verify` (checking the signature, downloading the new bobres), `backup`, `pull` (the new bobres's install pulls
  its images), `restart` (it starts the stack; told when the install prints that step), `health`, `rollback` or `done`.
- `message` is one short sentence: "BOBRES was updated from 0.6.1 to 0.7.0.", "BOBRES already runs 0.7.0: nothing to
  update.", "The update to 0.7.0 did not go through: BOBRES runs 0.6.1 again.", or the reason of a refusal or a failure
  ("release 0.6.0 is older than this install (0.7.0)...", "The update request was refused: it is older than 15 minutes.",
  "Another update, a rollback or a restore is running on this server...", "...the automatic rollback did not finish: connect
  to the server"). `to` is known once the release is found. `helper` is the version of the bobres on disk: the new one
  after a successful update. `started_at` and `finished_at` are RFC 3339 times in UTC, `null` while not known. `log` is
  the last 40 lines of what the update printed (the parent's and the new bobres's install), each cut at 200 characters,
  with the install's secret values, bot tokens, long hex strings and public IP addresses scrubbed.
- A status that says `running` while no update runs (bobres was killed, the server went down) is told apart by whoever
  next takes the install lock (`bobres update`, `rollback`, `restore`, the helper): it becomes `failed`, "interrupted".
  While it really runs the file is rewritten at least every 30 seconds, so its age says whether the helper is alive.

**The units** (`/etc/systemd/system/bobres-updater.path` and `.service`, from `deploy/systemd/`; `@DIR@`, `@EXE@`,
`@EXEDIR@` and `@STATE@` are the install directory, the bobres binary, its folder and the release state directory;
they are written only if made of letters, digits and `. _ + - /`).
- The path unit watches the file, with `ConditionPathIsDirectory` on `run/update`; enabled by `multi-user.target`.
- The service is `Type=oneshot`, `User=root`, `ExecStart=@EXE@ update --from-request --dir @DIR@`, with
  `BOBRES_STATE_DIR` set as it is for install.sh, `TimeoutStartSec=3h` (an update has time limits of its own; this stops
  one that hangs) and `TimeoutStopSec=20min` (an install that is interrupted rolls back before systemd gives up).
  If the service ends in any way but success, `ExecStopPost` deletes `request.json`, so that a request that was
  never read (a start that failed, an older bobres that does not know the flag after a rollback) cannot make the path
  unit start the service for ever.
- Sandbox: `NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=strict` with write access only to the install directory,
  the folder bobres is replaced in, `/etc/systemd/system` (the new bobres rewrites the units), the release state
  directory and the install log, `ProtectHome=read-only`, the kernel, clock and cgroup protections, `RestrictNamespaces`,
  `RestrictSUIDSGID`, `RestrictRealtime`, `LockPersonality`, and only the Unix, IPv4, IPv6 and netlink socket families.
  Docker is reached through its socket (`docker compose` is a client), which this does not restrict: the helper is
  root, as `sudo bobres update` is.

**The directory.** `bobres install` makes `<dir>/run` (root's, mode 0755) and `<dir>/run/update` (**owned by uid 65532,
core's user, mode 0700**), by their open descriptors and never through a link, and puts both right on every run (a
directory Docker made as root when the host path was missing is given to 65532). Only core mounts it, read-write
(`deploy/docker-compose.yml`, `TestComposeUpdateRequestMount`); core's root file system stays read-only, and it
gets no other path of the host. On the host the install directory itself is root's (0750), so a process of uid 65532
that is not in that mount cannot even reach `run/update` (tried in the lab). A run that is not root (a development or CI install) cannot give it away, so it is
made world-writable (0777) with a note, and the helper, which is root's, would refuse such a directory.

**Security.** The threat is a compromised core (or a staff account that reaches this function) asking for an update at a
time the owner did not choose. The design lets it do **at most that**:
- It cannot name where to download from, what to run, a base URL, an environment variable or a flag: the request has a
  channel and a release name, both checked against a pattern. The release is the vendor's signed one, checked against the
  keys built into bobres, with every check `sudo bobres update` makes (an older release, a replayed key list, an
  expired signature, a release of another channel are all refused).
- It can cause **downtime** (the stack restarts; the update backs up first and rolls back by itself when the store does
  not come back) and one update to a signed release newer than the installed one, which may be an earlier one than the
  newest when it names `version`. It cannot roll back, change a setting other than the update channel, or run
  anything else. (The restore request is a second action with its own rules: "Restoring from the dashboard".)
- The helper reads a directory core controls, so it trusts none of it: the directory itself is checked first (`run/` and
  `update/` are real directories nobody else can write to, owned by root or core), the request is opened without
  following a link or waiting for a pipe and must be a regular file owned by uid 65532 (a hard link to a file of root's
  is root's, not core's), its size is checked on the open descriptor and the read is capped, and a link, a pipe, a
  directory or anything else put in its place is removed without being followed. `status.json` is written by a temp
  file in that directory and a rename, which replaces a link instead of writing through it.
- Core can fill the disk by writing files into `run/update` (the volume has no quota), as it already can through its
  database. It can keep the helper busy by sending requests: systemd allows a service 5 starts in 10 seconds, and a
  burst past that puts the path unit in the `failed` state (`unit-start-limit-hit`; tried in the lab with 14 requests in
  4 seconds), where requests wait until `sudo systemctl reset-failed bobres-updater.path bobres-updater.service &&
  sudo systemctl restart bobres-updater.path` or the next `bobres install` or `bobres update` (which do exactly that)
  starts it again. `bobres check` warns about it. A run that is refused, failed or rolled back ends with exit code 1, so
  `systemctl` lists the service as failed until the next run; `status.json` has the details.
- The helper is root and runs `docker compose`: a flaw in `bobres update` is a flaw here too. What this adds to it is the
  directory and its parsing, which is small (`cmd/bobres/helper_request.go`) and tested for every refusal.
- Not covered: a host where Docker runs with user namespace remapping (container uid 65532 is not host uid 65532, as for
  `backups/` and `tls/`), or where another program runs as host uid 65532 and can reach `/opt/bobres/run/update`.

**Look at it.** `sudo bobres check` and `bobres doctor` have a line, "Dashboard updates": PASS when the path unit is
enabled and active (and the bobres it runs exists), WARN when it is not installed, not enabled, failed or its bobres is
gone (never FAIL: the helper is optional). `systemctl status bobres-updater.path`,
`journalctl -u bobres-updater.service` (the whole output of each run) and `cat /opt/bobres/run/update/status.json`
(`sudo`: the directory is core's) say the rest. `sudo bobres update` over SSH works as before and takes the same lock.

**Turn it off, or remove it.** `sudo bobres config set BOBRES_UPDATE_FROM_DASHBOARD off` (unset or `on`: on) removes
the units at once, writes `installed: false` to `status.json`, and keeps them away from every later `bobres install` and
`bobres update`; `... on` installs them again (and makes `run/update`). `sudo systemctl disable --now bobres-updater.path`
alone stops it until the next `bobres install` or `bobres update`, which turns it back on unless the setting says off.
`sudo bobres uninstall --purge` removes the units with the install; `bobres uninstall` without `--purge` keeps them (the
store can be started again with `bobres start`).

**Limits.** The dashboard cannot undo an update (`sudo bobres rollback` can; the restore point it leaves is the same). The
helper needs Docker and systemd on the server, and root's `bobres` at the path the units name; a server without
systemd (a container, a non-systemd distribution) has no helper, and says so on the Updates tab. A request that finds
bobres or Docker broken ends as `failed` with the reason.

## Restoring from the dashboard

The **System → Backups** tab can **download** any backup and **restore** one: a backup the backup service keeps, or a
file the owner uploads. Both are the owner's. It is the same helper as the update (the units above), a second action
in the same protocol, and it runs the code `sudo bobres restore` runs; nothing here gives core the Docker socket or any
other power over the host.

**Download.** The page asks for a single-use address (`POST /system/backups/download-ticket`: the owner's authenticator
code is checked there when the session has one), and the browser follows it as its own download. Core fetches the file
from the backup service (`GET /internal/backups/file?name=`, core's token for that service only) and copies it through
in chunks; the file is never read into memory, on either side. The file is what is on disk: encrypted (AES-256-GCM,
`BOBRES_BACKUP_KEY`), and **the key is never sent with it** (the page says so). The backup service serves only a name
that is a backup file name (`bobres-<time>[-tag].bkp`: no path, no sidecar, no temporary file) and only a plain file,
opened without following a link. Before the first byte, core audits the download (`system.backup.download`) and writes a
Telegram notice for every owner (`system.backup.v1`) in one transaction: if that fails, nothing is sent.

**Restore: the flow.**
1. The owner picks a backup in the list, or uploads a file (`POST /system/restore/upload`, the raw body, at most 2 GiB,
   one at a time, 5 an hour). Core streams the upload into `run/update/upload/<uuid>.bkp` (a folder of its own, mode
   0700, owned by core; the file is created with `O_EXCL`, never through a link, mode 0600), counting and hashing it as
   it is written, and keeps it 30 minutes (a second upload replaces it; leftovers older than 2 hours are removed).
2. The owner confirms (`POST /system/restore`): **types the store's domain** (`BOBRES_DOMAIN`, or the host of
   `BOBRES_PUBLIC_URL` on a local test setup; core refuses to restore when it knows neither), **ticks** that everything
   after the backup's time is lost and that the dashboard and the bot are unavailable meanwhile, and gives the
   **authenticator code** when the session has one (a mistyped domain is refused before the code is checked, so it does
   not spend one). The dialog says what is lost plainly: orders, payments, new customers and services, wallet balances,
   tickets and settings go back to the backup's. 6 requests an hour.
3. Core records the request (a row in `core.system_updates` with `action = 'restore'`, the audit entry
   `system.restore.requested`, a Telegram notice to every owner) and only then writes `request.json`. At most one
   update **or** restore is open at a time (the unique index covers both; an update is refused while a restore is open
   and the other way round). For a backup of the server, the size and SHA-256 are the backup service's own record,
   never the page's.
4. `bobres-updater.service` runs `bobres update --from-request`, which reads the request (rules below), takes the same
   install lock as `update`, `rollback` and `restore`, and:
   - **verify**: copies the backup into a file of its own (`backups/.dashboard-restore-*/backup.bkp`, root's, 0600)
     **while hashing it**, and refuses a file whose SHA-256 or size is not the one core named; opens the original
     without following a link or waiting for a pipe, requires a regular file owned by the right user, checks the free
     space (twice the backup plus 512 MiB), then reads the whole copy with this install's `BOBRES_BACKUP_KEY`: the format,
     the key and every chunk. A backup of another server (its key does not open it), a newer format, a damaged or cut file
     are refused here, **before anything is stopped**, with the reason, and the command for a backup of another server
     (`sudo bobres restore FILE --key-file KEYFILE`). Whatever core does to the upload afterwards is not what is restored.
     It then waits 6 seconds, so that the notice of the request reaches the owners' Telegram before the stack that holds
     it stops;
   - **safety**, **stop**, **restore**, **start**, **health**: the code of `bobres restore --yes`: stop the services that
     use the database, a **safety backup of the current data** (`backups/bobres-<time>-before-restore.bkp`, with a
     sidecar, outside the service's retention), the dump loaded in **one transaction** (a failure leaves the database as
     it was, and the services are started again), the backup's master keys put in `.env` where they differ, start,
     wait until healthy;
   - **rollback**: if the restored data does not come up healthy (or `.env` cannot take the backup's keys), the services
     are stopped, the safety backup is loaded (which also puts back the master keys the restore replaced), and the store
     is started on it. The result is `rolled_back`; if going back does not finish either, it is `failed` at the step
     `rollback`, with the command for the safety backup. `bobres restore` over SSH has no automatic rollback (it tells the
     operator what to run); the dashboard's has.
   A stop of the service (SIGTERM) in the middle of a restore starts the services again.
5. It writes `status.json` at every step, as for an update (the steps are `verify`, `safety`, `stop`, `restore`, `start`,
   `health`, `rollback`, `done`).

**What core's record looks like after a restore.** The restore replaces core's own database, so the row and the audit
entry of the request are gone when it succeeds (the backup does not hold them), and `core.system_updates` is the
backup's. The status carries what is needed (`action`, `subject`, `source`, `by`, `requested_at`), and core's worker, when
it finds no open request, records the end of a finished restore of the last 3 hours from it: a row that is *born ended*
(`INSERT ... ON CONFLICT (id) DO NOTHING`; whichever core inserts first writes the audit entry `system.restore.<outcome>`
and the notices to every owner, once), so the dashboard shows the result and the owners are told. A restore that failed
before the database changed, or went back, keeps its row (the safety backup holds it) and ends as an update does. The
audit log after a successful restore is the backup's, plus that one entry, which names who asked and which backup.
While core is stopped and restarted the page says it is reconnecting and keeps trying; in between, it shows the restore
from the updater's status. A restored database has its own login sessions: the owner may have to log in again.

**Stale and interrupted.** The status is rewritten at least every 30 seconds while the helper runs; one that says
`running` and is older than 10 minutes is shown as failed ("interrupted"), and core ends the request as failed. A
restore the helper says it runs for over 3 hours is ended as failed (systemd stops the service at 3 hours). If the
helper died with the services stopped, `sudo bobres status` and `sudo bobres start` bring the store up; the data from
before is in `backups/…-before-restore.bkp`.

**Protocol (request.json, version 1, action `restore`).**
`{"v":1,"id":"<uuid>","action":"restore","source":"backup"|"upload","file":"<name>","upload":"<uuid>"|"","sha256":"<64 hex>","size":<bytes>,"by":"<name>","at":"<RFC 3339>"}`
- All ten fields and no others, as for an update. `source: backup` needs `file` to be a backup file name and `upload` to
  be `""`; `source: upload` needs `upload` to be a lower-case UUID (the file `run/update/upload/<uuid>.bkp`) and `file`
  to be the name it had, one plain line of at most 120 characters (it is shown, never used as a path). `sha256` is 64
  lower-case hex digits, `size` 1 to 2 GiB. `id`, `by` and `at` are as for an update (an id handled before is ignored).
- The upload folder is checked like `run/update`: a real directory (not a link) that root or core owns and nobody else
  can write to. The upload file must be a regular file owned by core's uid. The helper deletes the upload whatever
  becomes of the request, once it has read the request (also when it refuses it).
- `status.json` gains `action` (`update` | `restore`), `restore` (true: this helper can restore; absent from a helper
  from before, which refuses a restore request as "the action restore is not update", and the dashboard then says to run
  `sudo bobres update` once), `subject`, `source`, `by` and `requested_at`.

**Security.** See 07-security.md: owner only, CSRF, rate limits, the typed domain, the acknowledgement, the authenticator
code, audit and Telegram on the request and on the outcome, and the same careful reading of everything core can write
(symlinks, pipes, oversized files, concurrent requests, a file changed after it was hashed). What a hijacked owner session
can do is the worst the dashboard can already do to the data: replace it with a backup **of this server** (a backup
that this server's `BOBRES_BACKUP_KEY` opens), with a safety backup of what was there, and every owner told. The
dashboard turns restores off with the updater: `sudo bobres config set BOBRES_UPDATE_FROM_DASHBOARD off`.

## Host monitoring

The dashboard's **Monitoring** section ([04-dashboard.md](04-dashboard.md)) shows the server and its containers, which core cannot
see: it runs in a container, with no Docker socket, no shell and no access to the host. So a small **collector** on the host looks
for it. It is `bobres monitor snapshot`, run every 30 seconds by a systemd timer as root, and it writes one file that core reads.
**It is on by default** and costs about 0.2 seconds of one core every 30 seconds (the unit is limited to 25% of a core and 192 MiB).

```
bobres-monitor.timer (every 30 s)  ->  bobres-monitor.service  ->  bobres monitor snapshot  (root)
        reads: /proc, mountinfo, the Docker Engine API (read only), systemctl, journalctl, timedatectl, kubectl if there is one
        writes: /opt/bobres/run/monitor/snapshot.json   (root:root, 0644, atomic)
                        core sees it read only as /run/bobres-monitor/snapshot.json (BOBRES_MONITOR_DIR)
```

**What is installed.** `bobres install` and `bobres update` (their last step, after the updater's units) make `/opt/bobres/run/monitor`
(root, mode 0755, never through a link; `run/` is the updater's folder too, whose own subfolder stays core's) and the two units
`/etc/systemd/system/bobres-monitor.{timer,service}` from templates in the release (`@DIR@` and `@EXE@` filled in: the install directory and
the path of this bobres binary, checked for safe characters). Each step is idempotent: a unit is rewritten only if it differs, a
stopped timer is started again, a moved binary is followed, and nothing happens on a host without systemd or when not run as root
(a one-line note says so; the rest of the install is not affected). `docker-compose.yml` mounts the folder **read only** into core only
(`./run/monitor:/run/bobres-monitor:ro`, `BOBRES_MONITOR_DIR`); an install made before this feature gets the mount with its next
`sudo bobres update`, and until then the page says the collector's file is not reachable.

**The service.** `Type=oneshot`, `User=root`, `ExecCondition` that this bobres has the collector (an older binary left in the unit is
skipped, not failed), `TimeoutStartSec=30`, `Nice=10`, idle I/O, `CPUQuota=25%`, `MemoryMax=192M`, `TasksMax=64`, `UMask=0022`,
`NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=strict` with write access to `run/monitor` only, `ProtectHome=read-only`, the kernel,
clock and cgroup protections, `RestrictAddressFamilies` limited to unix, inet, inet6 and netlink, `RestrictNamespaces`, `LockPersonality`,
`SystemCallArchitectures=native`. `systemd-analyze security` rates it 6.1 MEDIUM: a root service that talks to the Docker socket
cannot be much tighter. The timer: `OnBootSec=20s`, `OnUnitActiveSec=30s`, `AccuracySec=1s`.

**What it reads, and nothing else.** `/proc/{stat,meminfo,loadavg,uptime,net/dev,self/mountinfo,sys/kernel/osrelease}`, `/etc/os-release`,
the hostname, `statfs` of the install directory's and Docker's filesystems, the Docker Engine API over its socket with `GET` only
(`/version`, `/info`, `/containers/json`, `/containers/{id}/json`, `/containers/{id}/stats?stream=false&one-shot=true`, `/system/df`
every 15 minutes, and the last lines of Caddy's log), `systemctl show` for BOBRES's three unit families and Docker, `journalctl -p err`
for Docker's and BOBRES's units, `timedatectl show`, `<dir>/.restore-point/restore-point.json` (only its versions and time) and, if
`kubectl` or `k3s` is on the host, `get nodes` and `get pods -A`. Programs are looked up in a fixed list of system directories and run
with a minimal environment. What goes into the file, and what never does, is in [02-events-and-apis.md](02-events-and-apis.md)
("The snapshot") and [07-security.md](07-security.md) ("Host monitoring").

**Commands.**

| Command | What it does |
|---|---|
| `sudo bobres monitor status [--json]` | Whether the units are in place, the timer is active and the snapshot is fresh (exit 1 when it is not working) |
| `sudo bobres monitor enable` / `disable` | Put the collector in place / take it away for good: the choice is `BOBRES_MONITOR_COLLECTOR` in `.env` (the same as `bobres config set BOBRES_MONITOR_COLLECTOR on\|off`), which install and update follow. `disable` removes the units and the snapshot, so the dashboard shows "not installed" and no alert about it |
| `sudo bobres monitor snapshot [--print] [--out DIR]` | Take one snapshot now: what the timer runs. `--print` shows it instead of writing it |

**How to switch it off.** `sudo bobres monitor disable`. Core then has no host figures: the Monitoring page still shows what core can ask
itself (the services, the database, the 3x-ui servers, the gateways, the queues, the bot, the CI) and says the host is not available. The
monitoring in core itself is `monitor.enabled` in Settings (off: nothing is recorded and no monitoring alert is raised; the history stays).

**Check and doctor.** `bobres check` and `bobres doctor` show one line, **Host monitor**: `PASS a snapshot 24s old, docker 29.9.0 with 9
containers`; `WARN` when the units are missing, the timer is not active or the snapshot is older than 90 seconds, saying what to run;
`SKIP` when it is turned off. It is never a `FAIL`: host monitoring is optional.

**Update and uninstall.** `bobres update` re-runs the install step, so the units follow the new binary and the new templates; a rollback
restores `.env` and `docker-compose.yml`, and the units are rewritten by the next update. `bobres uninstall --purge` finds the two
units by their name (`bobres-*`, as it finds the updater's), stops, disables and deletes them, and the install directory takes
`run/monitor` with it (see "What installing BOBRES changes on a server"); a plain `bobres uninstall` keeps the install, so the units
stay and the collector reports an empty server.

**When the snapshot is not there.** An older compose file without the mount, a stopped timer, a host without systemd, or a snapshot
older than 90 seconds all give the same picture on the dashboard: `host.available` is `false` with the reason, the graphs have a gap,
and after `monitor.collector_minutes` (5) without a snapshot the alert **Collector quiet** is raised, if the collector was ever in place.
[runbooks/monitoring.md](runbooks/monitoring.md) walks through each case.

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
`bobres install` runs it after the store is healthy and before the summary (step 8), **by itself**: no question, one
line per step on the screen (the commands and their output go to the install log). Never with `--yes` (the way
`bobres update` runs install), never for a settings change from the menu, never with `--no-start`; `--no-harden`
skips it, and `--ask-harden` brings back the questions below, step by step. Run by the install it does what is safe
and skips the rest with a warning and the command to run: the firewall stays off when the SSH port is not detected,
when 3x-ui runs on this server, when BOBRES reaches a service on this server (`host.docker.internal`) or when another
program listens publicly (`sudo bobres harden` then shows it and asks); a ufw that is already on only gets the
missing rules; fail2ban, the security updates and the swap file are done when needed.
**The hardening and the installer never change `sshd_config`, SSH password login or root login, and never restart
or stop sshd** (they only read the SSH port); `TestHardeningNeverTouchesSSHSettings` and
`TestInstallerSourceNeverChangesSSHSettings` fail if any code does. `bobres harden [--yes] [--ssh-port N] [--dir DIR]` runs the same step on its own (`--dir`: the
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
| `BOBRES_DOMAIN` | caddy, core, payments, tls-agent | install's host name rule (lower-cased); a reminder that DNS must point here. Core hands the new domain to the tls-agent in its request, and the agent accepts only its own `BOBRES_DOMAIN`, so both are recreated |
| `BOBRES_TIMEZONE` | core, bot, backup | a known time zone (the CLI embeds tzdata, as the images do) |
| `BOBRES_ADMIN_TELEGRAM_ID` | core, bot | one positive number, or several separated by commas (at most 10, none twice; core makes each of them an owner when it starts) |
| `BOBRES_LOG_LEVEL` / `BOBRES_LOG_FORMAT` | core, payments, provisioner, bot, backup | debug, info, warn, error / json, text |
| `BOBRES_BACKUP_TIME` | backup | HH:MM (`config.ParseClock`, the backup service's own rule) |
| `BOBRES_TELEGRAM_API_URL` | bot | an http(s) URL without credentials, or empty (api.telegram.org) |
| `BOBRES_ZARINPAL_PUBLIC_URL` | caddy, core, payments, tls-agent | install's rule (https://host), or empty; `BOBRES_ZARINPAL_HOST` follows it. The tls-agent is recreated with core for the same reason as for the domain |
| `BOBRES_UPDATE_CHANNEL` | bot | stable or beta (lower-cased); `bobres update` and the menu read it too (see Update channel); the owner's choice in the dashboard (System > Updates, the `updates.channel` setting) wins for the bot's check and the dashboard's update when it is set |
| `BOBRES_LICENSE_SERVER` | bot | an https URL without credentials, or empty (no online activation or refresh; see License activation) |
| `BOBRES_SELLER_CONTACT` | core | any text, or empty (a Telegram handle or an address the License tab and `bobres license status` show for buying or renewing a license; empty: they say "ask the seller you got BOBRES from") |
| `BOBRES_BACKUP_S3_ENDPOINT` | backup | https without a path, or empty (no copy); needs the bucket and both keys; `BOBRES_BACKUP_NETWORK` follows it |
| `BOBRES_BACKUP_S3_BUCKET` / `_REGION` / `_PREFIX` | backup | a bucket name (lower-cased) / a region name / letters, digits, `. _ - /` (`config.ParseBackupS3`) |
| `BOBRES_BACKUP_S3_KEEP` | backup | 1 to 1000, or empty (30) |

- `get` prints the value alone on its line (for scripts); `BOBRES_ZARINPAL_HOST` and `BOBRES_BACKUP_NETWORK` can be
  read, never set. The S3 access and secret keys are secrets (`bobres install --s3-endpoint <url>` asks for them while
  they are empty).
- Any other key is refused with what changes it instead: a secret (every key but the plain settings the support bundle
  keeps) is never shown, and points to `bobres secrets rotate <name>` (master keys and the backup key to the runbook);
  `BOBRES_VERSION` to `bobres update`; `BOBRES_XUI_*` and the Zarinpal merchant and proxy to the dashboard.
- `set` checks the value with the key's rule and then the whole `.env` with install's `validateEnv`, refusing only
  errors the old `.env` did not have; writes `.env` with `writeAtomic` (0600, keeping every other key and secret);
  then `compose up -d --no-deps` of the services above that this install runs (backup only where its profile is on),
  and waits until they are healthy (Caddy has no health check). A stopped store stays stopped (the change applies when
  it starts); `--no-restart` only writes. When the services are not healthy it exits 1 and prints the command that
  sets the old value back; the value itself was valid, so it is not undone on its own. The tls-agent is in the list
  only where the install has one (see HTTPS certificates, `tlsAgentOn`): on an install that runs images from before
  release 0.1.0-rc.2, or whose compose file is from then, the domain and the Zarinpal address recreate caddy, core and
  payments only; a release CLI with a tag that is no release (`ci`, `0.1.0-lab.3`) recreates it, since its images have
  it.
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

## HTTPS certificates
Runbook: [HTTPS certificates](runbooks/https.md). Trust boundaries: [07-security.md](07-security.md), "HTTPS manager".
A store's addresses get their certificates in one of three modes: `auto` (Caddy's automatic certificate for
`BOBRES_DOMAIN`, and for the Zarinpal domain when there is one; the default), `ip` (also a Let's Encrypt certificate
for the server's public IP address) and `manual` (the operator's own certificate chain and private key for the
domain). The mode is a setting in the database (`core.tls_config`, 01-database.md), changed from the dashboard's
System page, HTTPS tab, or with `bobres ssl`. It is not a `.env` setting, so `bobres config` does not touch it.

**Layout.** Caddy's main Caddyfile is static. It defines the site block once, as the snippet `(bobres_site)`, and
imports two files that the tls-agent writes: `/tls/global.caddy` inside the global options and `/tls/sites.caddy` at the
top level. Each site in `sites.caddy` imports `(bobres_site)`.

```text
/opt/bobres/
  caddy/               root:root 0755
    Caddyfile          root:root 0644, written by the installer from its embedded copy
  tls/                 65532:65532 0750 (0777 on a non-root install: development and CI only)
    global.caddy       imported in the global options (default_sni when an IP address is served)
    sites.caddy        imported at the top level: one site for each served address
    certs/             65532:65532 0750; cert.pem and key.pem of a manual pair, and nothing else
  Caddyfile            the old top-level file; only on an install made before this layout
```

- **Modes and owners.** `tls/` is `65532:65532`, mode 0750. The files the tls-agent writes (`global.caddy`,
  `sites.caddy`, `certs/cert.pem`, `certs/key.pem`) belong to 65532 and are mode 0640: group 65532 is Caddy's group, so
  Caddy can read them and other users on the host cannot. The default `global.caddy` and `sites.caddy` the installer
  writes are root's, mode 0644, and hold no secret; the agent replaces them with its own files the first time core
  asks. `caddy/` and its Caddyfile are root's, and Caddy mounts them read-only.
- **Defaults.** The installer writes the auto-mode `global.caddy` and `sites.caddy` only when they are missing, so the
  first start works before core has ever run. It never overwrites them afterwards: the tls-agent owns them. A missing
  `sites.caddy` stops Caddy loudly (a restart loop in `bobres status`) instead of serving nothing.
- **Symbolic links and other entries.** The installer runs as root and `tls/` belongs to uid 65532, so it checks every
  path under `tls/` with `lstat` before it writes, and never follows a link: it opens files with `O_NOFOLLOW`, never
  creates a directory through a link, and changes owners by descriptor. Of what it finds where a default file goes
  (`global.caddy`, `sites.caddy`): a regular file stays as it is (the agent's), unless root runs the install and it
  belongs to neither root nor 65532, which is refused; a directory is refused ("is a directory"); a symbolic link,
  a pipe, a socket or a device file is unlinked (unlinking never follows a link) and the default is created in its
  place with `O_EXCL`, with a warning that says what was removed (on stderr, also in the quiet run `bobres update`
  makes). The agent owns `tls/`, so a compromised one could plant such an entry to make every install and update fail,
  and `bobres update` roll back each time, blocking security updates; replacing it keeps them going. `tls/` and
  `caddy/` themselves must be directories, not links.
- **The old Caddyfile.** Caddy used to mount the single file `./Caddyfile`. A single-file bind mount keeps the old
  inode after the installer's atomic rename, so a changed Caddyfile never applied on update. Caddy now mounts the
  directory `./caddy`. The installer no longer writes the top-level `Caddyfile`, and never deletes it: on an upgraded
  server it stays as it was, because the compose file of an older version mounts it after `bobres rollback`. A fresh
  install never has one.
- **Compose.** A new service `tls-agent` (core's image, `command: ["tls-agent"]`, user 65532:65532, `network_mode:
  none`, read-only, all capabilities dropped, 64 MB, healthcheck `/app tls-agent --healthcheck`). Its environment is
  the log settings and the install's two names, `BOBRES_DOMAIN` and `BOBRES_ZARINPAL_HOST`, exactly as core has them
  (compose refuses to start without the domain): the agent accepts a request only for those names. Three new named
  volumes: `tls_request` (core writes, the agent reads), `tls_result` (the agent writes, core reads) and `caddy_admin`
  (the socket). Caddy runs as `0:65532`, mounts `./caddy` and `./tls` read-only, and has `caddy_admin`. The full list of
  who mounts what is in 07-security.md. The `/request`, `/result` and `/tls-result` mount points exist in the image
  (mode 0700, owned by 65532:65532), so a new volume copies that owner on its first mount.
- **The tls-agent exists from release 0.1.0-rc.2 on: the `tls` profile and one test.** The compose file always defines
  the service, but an image from before rc.2 has no `tls-agent` command, so the service sits behind the Compose profile
  `tls` (`profiles: [tls]`), turned on the way `backup` is: every `bobres install` puts `tls` in `COMPOSE_PROFILES` in
  `.env` when the images have the agent, and takes it out (other profiles stay) when they do not. An image has it
  unless its tag names a release from before 0.1.0-rc.2 (`0.1.0-rc.1`, `0.0.9`, a `-beta` or `-alpha` of 0.1.0); every
  other tag has it: a later release, `latest`, an empty tag, this CLI's own release, a development build of the CLI,
  and a tag that is no release (`ci`, `0.1.0-lab.3`), whose images are built from the source that has it
  (`tlsAgentImage` in `cmd/bobres/tlsagent.go`). So `bobres install --version 0.1.0-rc.1` (the documented downgrade),
  or a plain re-run over an rc.1 install, does not start an agent that would restart for ever, and removes the
  container an earlier run left (`compose rm --stop --force tls-agent`, best effort: with the profile off compose
  neither updates nor stops it). `bobres install --version 0.1.0-rc.2` or newer turns it on again and waits for it.

  **One test decides whether an install has a working agent** (`tlsAgentOn`): the `docker-compose.yml` on disk
  defines the service, **and** the install's images have it. The compose file is read as text (a missing file says no,
  one the user cannot read says yes), so it needs no docker, and it is what makes a rollback safe: the restored
  compose file of an older release has no `tls-agent` service, whatever `.env` says (`latest`, an empty version).
  Everything that waits for the agent, lists it, recreates it or asks core about it uses it: `bobres status` and the
  menu's count, the waits after `start`, `restart`, `install`, `update`, `restore` and a rollback, `config set` (the
  recreate list of the domain and the Zarinpal address), `bobres ssl` and the menu's HTTPS item, doctor's TLS lines,
  and the support bundle (its service logs). On an install where it says no, none of them mentions the agent, and
  nothing is run in core's container for HTTPS (see `bobres ssl`).
- **Caddy's image** is pinned: `caddy:2.11.7-alpine@sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f`
  (the digest of the multi-arch index). The floors are Caddy 2.11.3, the first version that issues IP certificates, and
  2.11.1, the first that reloads on SIGUSR1. A changed reference is "missing" to `compose up -d`, which pulls it on
  update. CI validates every mode's files with the reference it reads from the compose file.
- **Reloading Caddy.** After `compose up -d` in `bobres install` (and so in `bobres update`, which runs it) the
  installer makes Caddy read its config again with `docker compose kill -s SIGUSR1 caddy`. The signal works with or
  without the admin socket, and the tls-agent's loads keep Caddy's record of its config file, so the signal reloads the
  right file. This is best effort: a failure is a warning, never a failed install or update. The installer then waits
  up to 30 seconds for the caddy container to be `running` on two looks in a row (not restarting) and warns when it is
  not. `bobres config set` does not send the signal: it recreates Caddy.
- **Domain changes.** `bobres config set BOBRES_DOMAIN` and `BOBRES_ZARINPAL_PUBLIC_URL` recreate caddy, core, payments
  and the tls-agent. Core writes its request to the tls-agent again when it starts, and the agent, which has the new
  names in its own environment, renders the new domain or Zarinpal domain. A certificate of your own that does not
  cover the new domain is left unused (HTTPS falls back to the automatic one).
- **IP addresses.** An IPv4 address passes the installer's host name rule, so it can be `BOBRES_DOMAIN`, public or
  private (a lab or LAN install); an IPv6 address does not pass it. In `auto` mode its site gets Caddy's own
  certificate authority (`tls internal`): browsers do not trust it. In `ip` mode the address is the server's public one,
  with a Let's Encrypt `shortlived` certificate, and it must be a public address (not private, not carrier-grade NAT).
  Whenever an IP address is served, the global options get `default_sni` for it, because clients send no name to an IP
  address. In `ip` mode dashboard links move to `https://<ip>` only while two things hold: Caddy serves a publicly
  trusted certificate for that address, and the bot's own detection of the server's public address, at most 5 minutes
  old (core asks the bot every minute), is that same address. A host whose bot reaches the internet from another address
  than visitors use (a VPN, a NAT egress, a second interface, a floating IP address that moved) never satisfies the
  second part, so its links stay on the domain; `BOBRES_PUBLIC_URL` in `.env` (core's environment; recreate core)
  overrides the choice. Payment returns and Telegram webhooks use the domain, whatever the links do.

**`bobres ssl`** (exit 0 done, 1 refused or failed, 2 usage; `--dir` as for every command). Each form runs
`docker compose exec -T core /app ssl ...`, which uses the same code as the dashboard.

| Command | Does |
|---|---|
| `bobres ssl` / `bobres ssl status` | The mode, the saved setting, and for each served address (domain, IP address, Zarinpal domain) the issuer, expiry and whether it is trusted, from core's live check. It also dials this host's own `127.0.0.1:443` (SNI = the domain, none for an IP address) to show what the outside sees |
| `bobres ssl auto` | Back to the automatic certificate for the domain. This is the recovery command: it works when the dashboard does not |
| `bobres ssl ip [--ip X] [--yes]` | Also serve the store on a public IP address with a Let's Encrypt certificate. Without `--ip` it uses the server's public IP address, detected from the host (private addresses are refused). An address that differs from the detected one needs `--yes`, or an answer to the question |
| `bobres ssl manual --cert FILE --key FILE` | Use your own certificate chain and private key. The CLI reads the two files on the host and pipes them to core. The key must not be encrypted |

- The change is saved only once Caddy has accepted it. A refusal prints Caddy's own words and changes nothing.
  `auto` is the exception: it is saved even when Caddy is not running, so it can always bring the store back.
- `manual` is refused when core has no master key, because the key is never stored unsealed.
- The CLI passes the address it detected with `--detected`; without it core asks the bot, which has the internet.
  `/app ssl status --json` prints the same JSON as `GET /api/v1/system/tls`.
- **On an install that predates the HTTPS manager** (`tlsAgentOn` says no: it runs images from before 0.1.0-rc.2, or its
  `docker-compose.yml` is one from then, as after a rollback) every form prints
  `error: this install predates the HTTPS manager (release X): run `sudo bobres update``, exits 1 and runs nothing in
  core: that image's `/app` has no `ssl` command, so `docker compose exec core /app ssl status` would start a second
  core process (its migrations and workers) that dies on "address in use". Help and usage errors (exit 2) are
  unchanged. The status view's host check and doctor do not ask core on such an install either.

**Menu.** The item "HTTPS certificate" (19, in a group of its own, before 20 "Uninstall") opens a submenu: 1 Status, 2
Domain (automatic), 3 Server IP (Let's Encrypt, 6 days, renewed automatically), 4 Your own certificate files, 0 Back.
The menu's header counts the services `bobres status` lists, the tls-agent included where the install has one:
"running (9/9 services)" on an install with backups, 8 without, and one less on an install that predates the HTTPS
manager (7 without backups). On such an install the item prints the message above instead of the submenu.

**Doctor.** `bobres doctor [--dir D] [--domain X] [--offline]` looks at the install in `--dir` (it reads `.env`, and
core's own list of served addresses when core runs) and prints one TLS line for each address the store serves: `TLS`
for the domain, `TLS ip` and `TLS zarinpal` for the others. It dials this host's `127.0.0.1:443` (SNI = the address,
none for an IP address). Without `--domain` the domain is the install's `BOBRES_DOMAIN`, which only root can read.
- PASS: a publicly trusted certificate that is not close to its end.
- WARN: not publicly trusted (self-signed, from a private CA, or Caddy's own CA), or less than min(14 days, a quarter of
  its life) left, which means the renewal is failing.
- FAIL: no certificate, a certificate for other names, or an expired one. A Caddy that is crash-looping or restarting is
  one FAIL line, because HTTPS is down.
- SKIP: Caddy is not running, no domain is known, or the install predates the HTTPS manager: one line, "HTTPS manager
  not installed yet (release X): run `sudo bobres update`". Nothing is asked of core and no certificate is probed then.

The lines are part of the doctor's checks (and of the support bundle's `doctor.txt`), not of the installer's preflight:
before an install there is nothing to look at.

**Update and rollback across the layout change.** An update from a version with the old layout works like any other:
- The restore point holds the files the running CLI lists, as far as they exist: `.env`, `docker-compose.yml`,
  `Caddyfile`, `caddy/Caddyfile`, `postgres-init.sh`. Staging creates the folders they sit in. An update run by a
  `bobres` from before the layout change (no `caddy/Caddyfile` in its list) keeps the old top-level `Caddyfile` only; one
  run by a newer `bobres` keeps `caddy/Caddyfile` too.
- A rollback puts back every file in the restore point, whichever files the running CLI knows about. It leaves the new
  layout's leftovers (`caddy/`, `tls/`) in place, because the older compose file ignores them, and the old top-level
  `Caddyfile` is there for it to mount. Going back to a version without the HTTPS manager restores the database from
  before the update, which has no `core.tls_config`: the store is on Caddy's automatic certificate again, as it was.
- The tls-agent exists from release 0.1.0-rc.2 on. An update from 0.1.0-rc.1 or older adds it with the new layout
  (the new compose file, `tls` in `COMPOSE_PROFILES`). A rollback to such a release restores the older compose file and
  `.env`, and its `up -d --remove-orphans` removes the tls-agent container, which that file does not define; the wait
  after it, `bobres status`, `bobres ssl` and doctor see an install with no agent (`tlsAgentOn`: the restored compose
  file lacks the service, whatever version `.env` names), so the rollback of a healthy stack exits 0, and the HTTPS tab
  says the applier is not set up until `sudo bobres update`.
- The first start after the update runs on the defaults the installer wrote (auto mode), as the store did before. At
  every start core asks the tls-agent for the saved setting, so a restart, a restore or a reinstall brings `ip` or
  `manual` back.

**Support bundle.** It never includes `tls/` or any certificate or key, and a PEM block that reached a log is replaced
whole. The mode and the IP address are not secret and may appear in the doctor's lines and the logs.

## License activation
`BOBRES_LICENSE_SERVER` is the vendor's license server ([13-license-server.md](13-license-server.md)). The seller
gives its address with the license key. It is not in `.env` by default: set it with
`sudo bobres config set BOBRES_LICENSE_SERVER https://<the address>`, which checks it (https, no credentials), writes
`.env` and recreates the bot with it (see Settings and secrets; `bobres restart` does not apply `.env` changes). It
must be an https URL: with anything else the bot does not start, which is why `.env` is not edited by hand for it. The
compose file passes it to the bot, which makes the calls for the dashboard and for the daily refresh, because core has
no internet access. Without it (`bobres config set BOBRES_LICENSE_SERVER ""` empties it), online activation is off
and a token pasted or imported by hand still works.

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

## What installing BOBRES changes on a server

The complete list, so that nothing BOBRES does to a server is a surprise, and so that `bobres uninstall --purge`
(see Uninstall) can undo all of it. It is kept in one place in the code, `hostArtifacts` in
`cmd/bobres/uninstall_inventory.go`; `TestHostArtifactsAreKnown` fails when an installer step, a unit file or a path
literal appears that is not listed there, and `TestInventoryIsDocumented` fails when this section and that list
differ. Each row names the artifact (`id`), what makes it, what the uninstall does with it and what proves it is
BOBRES's. Where an older release left no record, the markers it wrote (a header comment, a file name, a label) are the
proof, and the uninstall takes the conservative choice wherever a marker cannot prove ownership.

### Created by BOBRES only (safe to delete)

| `id` | Path or object | Made by | Uninstall | Proof it is BOBRES's |
|---|---|---|---|---|
| `install-dir` | `/opt/bobres` (`--dir`, `$BOBRES_DIR`): `.env` (the secrets and the backup key), `docker-compose.yml`, `caddy/`, `tls/`, `backups/`, `.restore-point/`, support bundles, and the updater's `run/update/` (uid 65532), `.update.lock`, `.updater-handled` | `bobres install`, `bobres update`, `bobres support-bundle` | deleted, backups included (`--keep-backups` leaves `backups/`) | a `.env` with BOBRES's keys beside a `docker-compose.yml` that names BOBRES's images, both regular files; never a symbolic link, never a system directory |
| `cli` | `/usr/local/bin/bobres` (or `$BOBRES_BIN_DIR`), `/usr/local/bin/bobres.prev`, `/usr/local/bin/.bobres*` (half-written files) | `install.sh`, `bobres update` | deleted last; the copy that is running is deleted only when it is one of these | the name, in a bin directory the receipt or `install.sh` recorded |
| `state-dir` | `/var/lib/bobres`: `keys.seq`, `release.version`, `checksums.txt`, `install-receipt.json`, `installed/` | `install.sh`, `bobres install`, `bobres harden` | deleted after everything else, the receipt with it | the known file names |
| `install-log` | `/var/log/bobres-install.log` | `install.sh`, `bobres install` | deleted | the name |
| `uninstall-log` | `/var/log/bobres-uninstall.log` | `bobres uninstall` | **kept**: it is the record of the uninstall | the name |
| `units` | `/etc/systemd/system/bobres*` (units, `*.d` drop-ins, `*.wants/` and `*.requires/` links), `/etc/systemd/user/bobres*` and `/run/systemd/system/bobres*`: today `bobres-updater.path` and `bobres-updater.service` (templates in `deploy/systemd`) | `bobres install` | found **by name** (`bobres-*`, `bobres.*`, `bobres@*`), not from a list, so a unit a later release adds is covered: stopped, disabled, deleted | the unit name, in a directory only an administrator writes |
| `docker-objects` | project `bobres`: containers `bobres-*-1`, volumes `bobres_pg_data`, `bobres_redis_data`, `bobres_caddy_data`, `bobres_caddy_config`, `bobres_caddy_admin`, `bobres_tls_request`, `bobres_tls_result`, networks `bobres_public/edge/backend/egress/offline`, images `ghcr.io/sobhanaz/bobres-*` and the ones the install pulled (`caddy`, `postgres`, `redis`) | `docker compose` | removed; an image another container uses stays (`docker rmi` without `-f`), and so does one the receipt says was on the server before | the label `com.docker.compose.project=bobres` **and** the `bobres_` name; images by their repository, the receipt and what the project's containers run |
| `fail2ban-jail` | `/etc/fail2ban/jail.d/bobres-sshd.local` | `bobres harden` | deleted, then fail2ban reloads | the header `Written by bobres harden` |
| `security-only` | `/etc/apt/apt.conf.d/51bobres-security-only` | `bobres harden` | deleted | the header `Written by bobres harden` |
| `swap-file` | `/swapfile` | `bobres harden` (under 2 GB of RAM, no swap) | swapped off and deleted, only when the receipt says BOBRES made it and the size is the one it made | the receipt (an install made before it keeps the file: nothing proves it is BOBRES's) |

### Changed in a shared file (reverted precisely)

| `id` | Path or object | Made by | Uninstall | Proof |
|---|---|---|---|---|
| `auto-upgrades` | `/etc/apt/apt.conf.d/20auto-upgrades` (also written by the distribution's `unattended-upgrades`) | `bobres harden` | the original the receipt recorded is put back; deleted when the file did not exist; without a receipt only BOBRES's header line is taken out and the settings stay; a file changed since is kept | the header `Written by bobres harden`, and the checksum of what BOBRES wrote |
| `fstab` | `/etc/fstab`: one line, `/swapfile none swap sw 0 0` | `bobres harden` | that line taken out, nothing else, only when the receipt says BOBRES added it | the receipt |
| `ufw-rules` | ufw allow rules whose comment starts with `bobres ` | `bobres harden` | the web (`80`, `443`, `443/udp`) and container rules are deleted by their exact form (`ufw --force delete allow ...`). **The SSH rule (`bobres SSH`) and the 3x-ui rules are never touched**, nor is any rule without BOBRES's comment, nor one that was there before BOBRES. ufw stays on (turning it off could open ports a service of yours relied on it to close): `--disable-firewall` turns it off where the receipt says BOBRES turned it on | the ufw comment, the exact rule form, and the receipt |

### Installed software (removed only with a flag)

| `id` | Path or object | Made by | Uninstall | Proof |
|---|---|---|---|---|
| `docker-engine` | the packages Docker's installer added (`docker-ce`, `docker-ce-cli`, `containerd.io`, `docker-buildx-plugin`, `docker-compose-plugin` and the dependencies it brought), `/etc/apt/sources.list.d/docker.list`, `/etc/apt/keyrings/docker.asc` (or `/etc/apt/keyrings/docker.gpg`), `/var/lib/apt/lists/download.docker.com_*`, `/var/lib/docker`, `/var/lib/containerd`, `/opt/containerd`, `/etc/docker`, `/etc/containerd`, `/run/docker`, `/run/docker.sock`, `/run/containerd`, the `docker` group | `install.sh`, through https://get.docker.com, only when Docker was missing | with `--remove-docker`, and only when Docker holds nothing but BOBRES's objects, apt says no other package needs the ones to go (the `docker` group is deleted unless someone is in it) | the record `install.sh` keeps in `/var/lib/bobres/installed/docker` (what appeared between a list of packages and files before and after) |
| `openssl` | the `openssl` package | `install.sh`, only when it was missing | with `--remove-packages`, same conditions | the record `install.sh` keeps |
| `hardening-packages` | `ufw`, `fail2ban`, `python3-systemd` (on Debian 12 and other systems without `/var/log/auth.log`) and `unattended-upgrades`, with the dependencies they brought | `bobres harden` | with `--remove-packages`, same conditions; ufw is kept while it holds a rule that is not BOBRES's | the receipt |

Package removal is exact. The receipt keeps every package that was new after each `apt-get install`; the uninstall
purges the ones that were asked for by name, and the dependencies only while apt still counts them as installed
automatically (one you installed on purpose since is yours). `apt-get -s purge` must show nothing leaving but those; a
package that something else needs is kept, with the reason.

### Not changed, only read

`/etc/os-release`, `/proc/meminfo`, `/proc/swaps`, `/var/log/auth.log` and the SSH port from `sshd -T` and
`/etc/ssh/sshd_config` (read to keep the firewall from cutting the session; `TestHardeningNeverTouchesSSHSettings`
fails if any code writes it); apt's `/etc/apt/sources.list.d`, `/etc/apt/keyrings`, `/etc/apt/trusted.gpg.d` and
`/usr/share/keyrings` (listed by `install.sh` before and after Docker's installer).

### Not undone, by design

What the package manager and the kernel keep for their own reasons: the package lists apt downloaded
(`/var/lib/apt/lists`), apt's and dpkg's logs and `ucf`'s bookkeeping (`/var/lib/ucf`), a package that apt upgraded to
satisfy a dependency of Docker (it stays upgraded, with the `*.dpkg-old` file its upgrade may leave), the system's
`/etc/group-` and `/etc/gshadow-` backups, journald's records, and, until the next reboot, Docker's iptables chains and
lock files in `/run`. None of it is BOBRES's; none of it is read by anything once Docker is gone. (ufw's chains are
flushed with ufw's own script when its package goes; Docker has no such script, and a firewall chain is not something an
uninstall should guess at.)

## Uninstall
Two commands. **`bobres uninstall`** stops the services and removes the containers; the data (database, Redis,
certificates), the install directory and the updater's units stay, and `bobres start` brings the store back.
**`bobres uninstall --purge`** is the day-one command: it removes everything installing BOBRES put on this server
(What installing BOBRES changes on a server, above), data and backups included, so that the server is as it was before.
Run it as root. A runbook for the whole job, with the restore on a new server, is
[runbooks/uninstall.md](runbooks/uninstall.md).

```
bobres uninstall [--dir D] [--yes]            containers only, data kept
bobres uninstall --purge [--dir D] [--dry-run] [--backup-to FILE | --no-backup] [--keep-backups]
                         [--yes --confirm TOKEN] [--remove-docker] [--remove-packages] [--disable-firewall]
bobres uninstall --verify [--dir D]           scan the server for what is left of BOBRES
```

| Flag | Meaning |
|---|---|
| `--dry-run` | print the plan and change nothing (also works without `--purge`); needs no root |
| `--backup-to FILE` | write a verified backup of the database to `FILE` (or into that directory, named `bobres-final-<time>.bkp`) before anything is deleted; it must be a new file outside the install directory, the state directory and Docker's; when it fails, nothing is deleted |
| `--no-backup` | no final backup, and no question about one |
| `--keep-backups` | keep `<dir>/backups` (everything else in the install directory goes, `.env` and the backup key with it, so keep the key too) |
| `--yes --confirm TOKEN` | for scripts: no question, but `--confirm` must be exactly the token a person would type, and `--backup-to` or `--no-backup` must say what to do about the backup |
| `--remove-docker` | also remove Docker, when `install.sh` installed it, nothing but BOBRES's objects is in it and nothing needs its packages |
| `--remove-packages` | also remove the packages `bobres harden` and `install.sh` installed (ufw, fail2ban, unattended-upgrades, openssl), under the same conditions |
| `--disable-firewall` | turn ufw off again, when the receipt says BOBRES turned it on (by default ufw stays on) |
| `--verify` | the leftover scan alone; exit 1 when anything of BOBRES is left |

**The plan, the question, the token.** `--purge` first prints the exact plan: the install directory with its size and
file count and the backups in it, then each step with what it will do (the units, the Docker objects with counts and
sizes, the firewall rules it deletes and the ones it keeps, the files it deletes or puts back, the swap file, the
packages, the state and the command), and what is never touched. Then it asks: first, unless `--backup-to` or
`--no-backup` answered it, whether to make a final backup (Enter: into the home directory of the user who runs it; or a
path; or `no`); then it asks for the **token** typed in full, case-exact: the install's domain (the host name when the
install has none, a fixed phrase when neither is known). Anything else, or the end of input, cancels with nothing
changed (exit 1). The menu's "Uninstall" has the same questions: it runs this command.

**The final backup.** It is made with the install's own code (`pg_dump` streamed through the backup encryption, the
same file `bobres backup` makes), written beside nothing that is deleted, synced, and then every part of it is read back
and checked with the install's key. It opens only with that key, which is deleted with `.env`: on a terminal the key
is shown once after the backup, and never written to the log. Keep it with the file. On a new server: install BOBRES
(`install.sh`, the same bot token), then `sudo bobres restore FILE --key-file <file with the key>`.

**The order**, each step looking at what is there now, so that the command can be run again after an interruption and
finishes the job:
1. the units named `bobres*` (the dashboard updater, the monitor...): stopped, disabled, deleted, with their drop-ins
   and links; `systemctl daemon-reload`. One that is busy running an update stops the uninstall before anything else;
2. Docker: `docker compose --profile '*' down --remove-orphans --volumes` (every profile is on, so a container of a
   service whose profile is off now goes too), then what `down` left: containers, **every volume that carries the label
   `com.docker.compose.project=bobres` and the name prefix `bobres_`** (after a rollback to a release from before the
   HTTPS manager the compose file lacks `tls_request`, `tls_result` and `caddy_admin`, which `down --volumes` leaves),
   networks, and the images BOBRES pulled. A container, volume, network or image that is not the project's is not
   looked at; when the compose project's containers were started from another directory than `--dir`, the uninstall
   refuses (exit 2) rather than remove a stack that is not the one named;
3. the firewall rules, the fail2ban jail, the security-updates files, `/etc/apt/apt.conf.d/20auto-upgrades`, the swap
   file and its fstab line, as the inventory says;
4. with the flags, the packages and Docker;
5. the install directory, deleted from inside a root that cannot leave it (a symbolic link is removed, never followed;
   a mount point inside is kept); the marker files, `docker-compose.yml` and `.env`, go last;
6. `/var/log/bobres-install.log` and BOBRES's state files;
7. the bobres command (`/usr/local/bin/bobres`, `bobres.prev`) and last the receipt and `/var/lib/bobres`. Steps 6 and 7
   run only when everything before them succeeded, so the receipt and the command are still there to finish the job.

A step that stops the run (the units, Docker) leaves the install directory in place; the others are reported and the run
goes on. Each failure names itself and the command to run again.

**What it will not do.** It deletes only what it can prove is BOBRES's: a directory only when its real path is no
system directory (`/`, `/usr`, `/etc`, `/var`, `/opt`, `/home`, a home directory... and nothing shallower than two
levels), is no symbolic link, and holds BOBRES's marker files; Docker objects by label and name; units by name; files
by their header or the receipt. It never changes SSH settings or the SSH firewall rule (the `bobres SSH` rule), DNS, other
users' data, other Docker workloads or anything else. A path under the state directory or the log is refused as an
install directory (they must survive the wipe). Without root it prints the plan and stops with an error (a dry run needs no root).

**The receipt.** `bobres install` (so `bobres update` and the menu) and `bobres harden` write
`/var/lib/bobres/install-receipt.json` (`$BOBRES_STATE_DIR`; root only, 0600), outside the install directory so that
the uninstall can read it at the end: where the install is, the domain, the packages that were new after each apt run
(Docker's and openssl's from the record `install.sh` keeps in `/var/lib/bobres/installed/`), whether ufw was on before
and whether BOBRES turned it on, the ufw rules BOBRES added (and any that were already there), each file the hardening
wrote with the original it replaced, the swap file and its size, and the image references the install pulled and which of
them were on the server before. It is additive: a key a newer bobres writes is kept, a file that cannot be read is
treated as absent, and writing it never fails an install (a non-root run leaves none). **An install made by an older
release has no receipt.** The uninstall then uses the markers the installer wrote and takes the conservative choice: it
removes what carries BOBRES's header or label, strips only the header line from `20auto-upgrades`, keeps `/swapfile`,
refuses `--remove-docker` and `--remove-packages` (nothing proves BOBRES installed them), keeps the third-party images
no container of BOBRES's runs, and leaves ufw on. Running `sudo bobres update` or `sudo bobres install` once on such a
server writes a receipt for what can still be known.

**The record and the scan.** Everything it prints is also written, without colors, to `/var/log/bobres-uninstall.log`
(root only), which stays: it is the record of the run (the backup key is never in it, and the value of `--confirm` is replaced by `<token>`). The run ends with a
scan of the server: "Nothing of BOBRES is left on this server." (exit 0), or the list of what is left and why (exit 1),
and always what was kept on purpose (the SSH rule, ufw if it is on, the log, Docker and the packages without their
flags, a file you changed, an image another container uses). `bobres uninstall --verify` is that scan alone, for later.
Exit codes: 0 done, 1 cancelled, failed or something left, 2 refused or a wrong flag (nothing was changed).
Running the command twice is harmless: the second run finds nothing, says so, and changes nothing.

**Proven on the lab.** `deploy/lab/` is an Ubuntu 24.04 server with systemd, and `deploy/lab/uninstall-proof/` takes
the snapshots (files with their checksums, systemd units, packages, users, groups, ufw, iptables, Docker, mounts,
processes) that [deploy/lab/README.md](../deploy/lab/README.md) ("Proving the uninstall") compares. The run that proved
the command used the real `install.sh` (which installed Docker 29), `bobres install` (the whole stack, healthy),
`bobres harden --yes` (ufw with a container rule, fail2ban, unattended-upgrades), a stand-in `bobres-monitor.timer` and
a decoy container, volume, network and image. `--verify`, `--dry-run` and a wrong typed token left the snapshot identical
(only `/var/log/bobres-uninstall.log` appeared). The purge with `--remove-docker --remove-packages` removed everything
except Docker, which it refused to remove while the decoy was there (exit 1, the command and the receipt still in place);
with the decoy gone the typed run finished (exit 0); a third run found nothing and changed nothing. Against the first
snapshot the same files, units, users, groups, ufw state, fstab, swap, cron and installed package set remain; what
differs is the list under "Not undone, by design" (10 packages that apt upgraded, apt's package lists, `ucf`'s
bookkeeping, `/etc/group-`, the uninstall log, Docker's empty firewall tables and `/run` locks). Not tried there: the
swap file (the lab's host has plenty of RAM, so the hardening makes none; the unit tests cover it), Debian, Ubuntu 22.04,
and a restore of the final backup on a second server (`bobres restore` is not new). The unit tests run the same command
on a server made of a temporary directory and fakes (`cmd/bobres/uninstall_lab_test.go`), including every refusal, an
interrupted run, an install without a receipt and the second run.

## Owner login without Telegram
The owner can open the dashboard without the bot and without `/start` (the bot may be blocked, or the owner may not
have an account on it yet). Core's `admin-password` command (`/app admin-password`, run by `bobres admin password`
and by the installer) does this for the main owner (the first id in `BOBRES_ADMIN_TELEGRAM_ID`), or for the owner `--id N` names:

- It creates the owner's account when it is missing and makes it the owner, picks the login `owner` (`owner-2`,
  ... when another staff member has it) and sets a random 16-character initial password, kept as an argon2id hash
  with `must_change = true` and no authenticator. It needs `BOBRES_CORE_MASTER_KEY` (the authenticator secret is
  encrypted with it) and the owner id, and says so when either is missing.
- The password is printed once, on stdout of the terminal that ran it. It is not logged, not kept in `.env` or the
  state files, and not in the audit entry (`dashboard.password.reset_cli`, source cli).
- A fresh install runs it once the stack is healthy and prints the address `https://<domain>/admin`, the username and
  the password in its summary, with the `/start` link as an alternative. A re-run of an existing install, a quiet run
  (a settings change from the menu, `bobres update`) and `--no-start` never issue one. When core cannot be reached
  the install still succeeds and the summary points to `bobres admin password`.
- The first login with it is restricted (one hour, only the account page): the dashboard shows a forced screen to
  choose a new password (not the initial one) and scan the authenticator QR code, and a router guard keeps every other
  page closed until the first code is confirmed. See `07-security.md` for the rules.
- Running the command again is the way back after a lost phone or password, from any server shell: it replaces the
  password and the authenticator, ends the owner's sessions and lifts a lock. `bobres admin link` remains the quick
  way in when the owner already has an account and the bot works.

## Open points
- Installer and CLI binaries: GitHub releases of this (public) repository. Images: ghcr.io, which must be
  public for customers to pull without logging in (private registry auth via license is still open).
- Re-running the one-liner on an existing install updates the CLI and repairs the install, but keeps the
  running image version; moving to a new version is `bobres update` (with backup and rollback).
- Keeping the previous images for N days: not built; a rollback pulls the previous images again if they were pruned.
  (The stable/beta update channel is built: see Restore, update and rollback.)
- Release signatures expire 45 days after signing: a stable channel with no release for that long must be re-signed,
  or install.sh and `bobres update` refuse it ([releases runbook](runbooks/releases.md)). Re-signing moves the
  release's `# issued` day, which the license's advisory `updates_until` note is compared with; `tools/sign/sign.sh` has no option to keep it.
- Non-Docker (systemd binaries) mode: not planned for v1.
- Installing 3x-ui itself for customers who do not have one yet (an optional helper; not built).
