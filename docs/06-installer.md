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
ID or the first 3x-ui panel, check the server, uninstall. Settings changes re-run the installer with the one new
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
`/opt/bobres/{bobres.yml, .env, docker-compose.yml, Caddyfile, data/, backups/, logs/}`; state in named volumes.

## Status (2026-10-03)
Implemented: `install`, the menu, `status`, `logs`, `start`, `stop`, `restart`, `uninstall [--purge]`, `doctor`,
`version`, `admin link` (2026-10-07), `license status|import` (2026-10-11). The bootstrap `install.sh` downloads the latest GitHub release, verifies it and hands over to `bobres install`. Not yet: `update`, `rollback`, `backup`,
`restore`, `config`, `secrets`, `license activate` (needs the vendor server), `support-bundle`, and the hardening step (ufw, fail2ban).
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
| `bobres update [--check]` | Verify signed release, backup, migrate, roll out, health check, auto-rollback |
| `bobres rollback` | Return to previous version (+ DB restore point) |
| `bobres backup [--now]` / `restore <file>` | Encrypted dump, optional off-server upload (S3/SFTP); tested restore |
| `bobres config get/set` | Edit settings safely, validate, restart affected services |
| `bobres secrets rotate <name>` | Rotate bot token/3x-ui token/DB password |
| `bobres license status` / `license import < token.txt` | Show the license as core sees it (state, install id to send the vendor, entitlements) / install a token read from stdin (verified first; audited as `license.set` from the CLI). Both run `/app license ...` in the core container. `activate` (online, through the vendor server) comes later |
| `bobres doctor` | Diagnose: DNS, TLS, ports, bot token, 3x-ui reachability/version, queue lag, disk; print fixes |
| `bobres admin link [telegram id]` | One-time dashboard login link printed on the server, for when the bot is down (default: the owner). It skips the password and code, so it is also the way back after a lost authenticator |
| `bobres support-bundle` | Redacted logs/config for support (no secrets, no user data) |

## Update safety
Pre-update backup -> pull + verify -> run migrations (forward-only, guarded) -> start new -> health gate -> switch -> keep previous images N days.
Health gate fails -> automatic rollback and alert. Update channel: stable/beta.

## Uninstall
Stops services, removes containers/units; data kept unless `--purge`; prints what remains.

## Open points
- Installer and CLI binaries: GitHub releases of this (public) repository. Images: ghcr.io, which must be
  public for customers to pull without logging in (private registry auth via license is still open).
- Re-running the one-liner on an existing install updates the CLI and repairs the install, but keeps the
  running image version; moving to a new version is `bobres update` (Phase 6, with backup and rollback).
- Non-Docker (systemd binaries) mode: not planned for v1.
- Installing 3x-ui itself for customers who do not have one yet (optional helper, later).
