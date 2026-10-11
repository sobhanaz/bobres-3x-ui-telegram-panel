# BOBRES 3x-ui Telegram Panel

Telegram bot and management panel for selling and managing 3x-ui (Xray) VPN subscriptions.

## Planned scope
- Telegram bot: plans, purchase flow, account and subscription status
- 3x-ui integration: create, renew and delete clients through the 3x-ui API
- Admin panel: users, orders, plans, servers, traffic and expiry management
- Payments and notifications

## Install

On a fresh Ubuntu 22.04/24.04 or Debian 11/12/13 server, as root:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
```

It installs Docker if needed, downloads the `bobres` CLI from the latest GitHub release,
verifies its signature and checksum, and asks for your domain, Telegram ID, bot token and
3x-ui panel (tokens are typed hidden). Afterwards run `bobres` for the management menu:

```text
  BOBRES manager  bobres v0.1.0
  /opt/bobres  ·  shop.example.com  ·  version 0.1.0
  Store: running (7/7 services)
   1. Status
   2. Live logs (Ctrl+C returns here)
   3. Start
   4. Stop
   5. Restart
   6. Show settings
   7. Change the bot token
   8. Change the owner (admin) Telegram ID
   9. Change the 3x-ui panel (URL, API token, subscription link)
  10. Check this server
  11. Uninstall
   0. Exit
```

Every item is also a command: `bobres status`, `bobres logs -f`, `bobres restart`, ... (`bobres help`).

## Status
Phases 1-3 are built: the sellable bot with manual payments, Phase 2 payments (Telegram Stars,
Zarinpal, payment screenshots approved by the admin), Phase 3 retention (renewals, traffic
packages, reminders, discount codes, referrals). Next: Phase 4, the web dashboard. Design notes
are in `docs/superpowers/specs/`; the first real install is `docs/11-staging-run.md`.
Go monorepo with four services (`core`, `bot`, `payments`, `provisioner`) behind Caddy,
PostgreSQL (one schema and one role per service) and Redis. Docs index: `docs/README.md`.

## Development
- Go 1.26 or newer (`go.mod` says `go 1.26.0`; CI and the Docker image track the latest 1.26 patch).
  `make` forces `GOTOOLCHAIN=local`, so it builds with the `go` on your PATH: use a 1.26.x for
  byte-for-byte parity with CI and the images. Outside `make`, an older Go auto-downloads
  go1.26.0 (the oldest patch); install a current 1.26.x instead.
  `make lint` needs golangci-lint v2: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`.
- `make build`, `make test`, `make lint`, `make proto`.
- Database tests need PostgreSQL 13+ (`BOBRES_TEST_DATABASE_URL`, a URL for any database on
  a server where the role may `CREATE DATABASE`; default: the local socket in `/tmp`) and
  Redis (`BOBRES_TEST_REDIS_ADDR`, default `127.0.0.1:6379`). Each test package gets its own
  throwaway database. They skip when the servers are missing, except with
  `BOBRES_TEST_REQUIRE_DB=1` (CI), where they fail.
