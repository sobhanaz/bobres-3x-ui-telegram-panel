# Architecture

This page is for developers. It shows how the parts fit together. The split follows security, failure and scale boundaries, not features.

## The picture

```text
Telegram ⇄ bot ⇄ core ⇄ provisioner ⇄ 3x-ui / Marzban panels
              │      ⇅
              │   payments ⇄ gateways, the TRON watcher
        Mini App / dashboard (served by core)  ·  backup  ·  Caddy ⇄ tls-agent
```

BOBRES is a Go monorepo. The dashboard and the Mini App are Vue 3 apps embedded into the `core` binary. Everything runs with Docker Compose on one server.

## The nine compose services

| Service | Job | Networks |
|---|---|---|
| `caddy` | HTTPS gateway. The only service reachable from the internet | public, edge |
| `postgres` | One database, one schema and one role per service | backend |
| `redis` | The bot's conversation state and rate limits. Disposable | backend |
| `core` | Users, plans, orders, wallet and ledger, tickets, resellers, the delivery worker, the dashboard and Mini App API, the watchdog | edge, backend |
| `bot` | Telegram: customers, staff, alerts, backup delivery. Also the proxy for anything core needs from the internet | edge, backend, egress |
| `payments` | Gateway secrets, payment intents, Zarinpal, IDPay, NextPay, Stars, the TRON watcher, manual receipts | edge, backend, egress |
| `provisioner` | The only holder of panel tokens. Creates, renews and deletes clients | backend, egress |
| `tls-agent` | Applies HTTPS changes to Caddy. Core's image, with no network | none |
| `backup` | Daily encrypted `pg_dump`. Reaches the internet only when S3 copies are on | backend, offline |

`backend`, `edge` and `offline` are internal Docker networks. Only `egress` has a route out. Core has no internet access, so a compromised dashboard cannot phone home. `backup` and `tls-agent` run behind compose profiles that `bobres install` sets. The BOBRES services run read-only as user 65532, with all capabilities dropped.

## Data and money

- One PostgreSQL. Schemas `core`, `payments` and `provisioner`. No cross-schema joins or foreign keys: services refer to each other by ID only (UUID v7).
- Migrations are forward-only (goose) and additive. Each service runs its own schema's migrations at start. `bobres rollback` restores the database from the restore point taken before the update.
- Amounts are `bigint` in minor units plus a currency code, never floats. The ledger is append-only. A wallet balance is a cached sum of it. Every money-moving call carries an idempotency key with a unique index.

## Talking between services

- **Synchronous:** gRPC on the internal network. Contracts are in `proto/` (managed with buf, generated code in `gen/`).
- **Asynchronous:** each service writes events to a transactional **outbox**. Consumers pull them over gRPC (`events.v1.EventFeedService`) with one cursor per consumer, and de-duplicate with an **inbox**. Events are past-tense, versioned facts like `order.paid.v1`, and handlers are idempotent. Failures retry with back-off, then go to a dead-letter table and raise an alert.
- **Auth:** one token per service. A server accepts only its legitimate callers, and core presents a different token to each service it calls. mTLS is not built.

The purchase flow: the bot calls `core.CreateOrder`. Core emits `order.created`. Payments creates an intent and the customer pays. Payments emits `payment.succeeded`. Core marks the order paid, writes the ledger and emits `order.paid`. The provisioner creates the client and emits `subscription.provisioned`. The bot sends the link. A wallet payment skips the payments step. If the panel is down, the order stays paid and is retried.

## The browser side

- `web/dashboard` is the staff dashboard at `/admin`. `web/miniapp` is the customer store at `/app/`. Both are built into folders that core embeds. `make dashboard` and `make miniapp` build them.
- The Mini App signs a customer in with Telegram's `initData`. Only the bot holds the bot token, so core sends the data to the bot's `/internal/miniapp/verify`.
- Public pages served by core: `/sub/<token>`, `/terms`, `/privacy`, `/healthz`, `/readyz` and `/webhooks/<provider>`.

## Helpers on the host

Core has no Docker socket. Anything that needs the host goes through a file in a shared folder and a root systemd unit:

| Helper | Mechanism |
|---|---|
| Update and restore from the dashboard | Core writes `run/update/request.json`. `bobres-updater.path` starts `bobres update --from-request`, which runs the same code as the command line and writes `status.json` |
| Host monitoring | `bobres-monitor.timer` writes `run/monitor/snapshot.json` every 30 seconds. Core reads it read-only |
| HTTPS | Core writes a request file. The `tls-agent` re-checks it and talks to Caddy through its admin socket |

## Code layout

| Path | Contents |
|---|---|
| `cmd/` | One `main` per program: `core`, `bot`, `payments`, `provisioner`, `backup`, `bobres` (the CLI), `licensesrv` (the vendor's license server, never part of an install) |
| `internal/core/{domain,store,server,web}` | Business rules, SQL, gRPC and HTTP handlers |
| `internal/bot/` | The Telegram handlers, texts (`i18n`, Persian and English), notifier |
| `internal/payments/`, `internal/provisioner/` | The gateways, and the service that drives panels |
| `internal/xui`, `internal/marzban` | Panel clients and adapters. Each has a fake (`xuifake`, `marzbanfake`) for tests |
| `internal/license`, `internal/release` | License tokens, tiers and release verification |
| `proto/`, `gen/` | Protobuf contracts and generated code |
| `deploy/` | The compose file, Caddy templates, systemd units |
| `install/` | `install.sh` and the embedded public keys |
| `tools/`, `tests/`, `site/` | Tools, end-to-end tests, the public site |

## Releases and trust

Two offline root keys sign `keys.txt`, a list of release keys with expiry, sequence number and revocations. A release key signs `checksums.txt` (version, channel, expiry), and the CLI must match its signed checksum. Release images are signed with keyless cosign. The release workflow checks that signature and builds each image digest into the CLI. See [Security](Security).

The longer design notes are in [docs/PLAN.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/PLAN.md), [docs/01-database.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/01-database.md) and [docs/02-events-and-apis.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/02-events-and-apis.md).
