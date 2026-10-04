# BOBRES 3x-ui Telegram Panel

Telegram bot and management panel for selling and managing 3x-ui (Xray) VPN subscriptions.

## Planned scope
- Telegram bot: plans, purchase flow, account and subscription status
- 3x-ui integration: create, renew and delete clients through the 3x-ui API
- Admin panel: users, orders, plans, servers, traffic and expiry management
- Payments and notifications

## Status
Phase 1 in progress (see `docs/superpowers/plans/2026-09-30-phase1-implementation.md`).
Go monorepo with four services (`core`, `bot`, `payments`, `provisioner`) behind Caddy,
PostgreSQL (one schema and one role per service) and Redis. Docs index: `docs/README.md`.

## Development
- Go 1.26 or newer (`go.mod` says `go 1.26.0`; CI and the Docker image track the latest 1.26 patch).
  `make lint` needs golangci-lint v2: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`.
- `make build`, `make test`, `make lint`, `make proto`.
- Database tests need PostgreSQL 13+ (`BOBRES_TEST_DATABASE_URL`, a URL for any database on
  a server where the role may `CREATE DATABASE`; default: the local socket in `/tmp`) and
  Redis (`BOBRES_TEST_REDIS_ADDR`, default `127.0.0.1:6379`). Each test package gets its own
  throwaway database. They skip when the servers are missing, except with
  `BOBRES_TEST_REQUIRE_DB=1` (CI), where they fail.
