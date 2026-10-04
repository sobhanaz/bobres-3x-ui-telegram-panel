# Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the first sellable slice of BOBRES: Telegram bot + web dashboard for selling 3x-ui VPN subscriptions with wallet + manual payments, free trial, and one-command install.

**Architecture:** Four Go microservices (`core`, `bot`, `payments`, `provisioner`) communicate via gRPC and a Postgres transactional outbox, all behind a Caddy gateway. `core` embeds a Vue 3 admin SPA. The installer generates secrets and starts Docker Compose.

**Tech Stack:** Go 1.26 (moved from 1.25 on 2026-10-04 for goose 3.28 / x/crypto 0.57), PostgreSQL 16, Redis 7, gRPC/protobuf via buf, goose migrations, sqlc, Vue 3 + Vite + TypeScript, Docker Compose, Caddy.

## Status (2026-10-03)

| Sub-phase | State |
|---|---|
| 1a Foundation | Done. Hand-written pgx stores instead of sqlc; per-service tokens instead of one shared token. |
| 1b Events + provisioner | Done. Events travel through a gRPC pull feed with per-consumer cursors (not LISTEN/NOTIFY, see PLAN.md §11). Provisioning runs as a worker in core (retries with backoff, alerts once). |
| 1c Payments | Done. Core owns wallets and applies each intent exactly once; payments publishes `payment.succeeded`/`payment.rejected`. |
| 1d Telegram bot | Done (`internal/bot`): fa/en, buy with wallet / card / USDT, receipts and TXIDs, services with QR, wallet top-ups and history, one trial, support, notifications from core's feed, and an admin panel in the bot. |
| 1e Dashboard | Deferred to Phase 4, as PLAN.md §10 always had it. Everything Phase 1 needs from it (payment review, plans, users, balances, bans, settings) is in the bot's admin panel, behind core's audited admin RPCs. |
| 1f Install | Done: `bobres install` (secrets, files, compose up, health wait, re-run safe), `bobres status`, `bobres logs`, `bobres uninstall [--purge]`. CI runs the real installer against the real stack (`docker` job): fake Telegram on the runner (`tools/tgfake`), the bot answers a `/start` through core and Postgres, then status, logs and both uninstall modes. A real-panel adapter test exists behind `-tags integration` (`XUI_TEST_URL`, `XUI_TEST_TOKEN`); it has not been run against a live panel yet. |

Tests: every package runs against a throwaway Postgres (and Redis) in CI; `internal/testenv` starts the whole backend in-process and the bot tests drive it through a fake Telegram.

Still open for Phase 1: native-speaker review of the Persian texts; running the integration test against a real 3x-ui v3 panel; dropping the GO-2026-6443 allowlist entry once grpc v1.85.0 is released.

## Global Constraints

- Go version: 1.26 (minimum; CI and the Docker image use the latest 1.26 patch).
- One Postgres database, one schema per service (`core`, `payments`, `provisioner`).
- All money amounts are `bigint` minor units; IRT scale = 0, USDT scale = 6.
- Secrets only from env or `*_FILE`; never in git/images.
- Service-token auth on gRPC; no mTLS in Phase 1.
- 3x-ui API token lives only in `provisioner`, encrypted at rest.
- Dashboard SPA embedded in `core` via `embed.FS`.
- Bot defaults to long polling; webhook optional.
- Manual payment providers only in Phase 1 (wallet, manual_card, manual_crypto).
- Every write to `audit_log` requires actor + reason for sensitive actions.
- Free trial: exactly one per Telegram ID.
- Commits are small and pushed after each finished sub-phase.

---

## Sub-Phase 1a: Foundation — DB, Domain, gRPC Contracts, Service Wiring

**Goal:** Standing services can connect to Postgres/Redis, migrations run, core domain models and gRPC contracts exist, and a simple user/plan/order flow is unit-testable without Telegram or 3x-ui.

### Task 1a.1: Add protobuf contracts and buf tooling

**Files:**
- Create: `proto/buf.yaml`
- Create: `proto/core/v1/core.proto`
- Create: `proto/payments/v1/payments.proto`
- Create: `proto/provisioner/v1/provisioner.proto`
- Create: `proto/common/v1/types.proto`
- Modify: `Makefile` (buf generate target)
- Modify: `tools/tools.go` or `Makefile` to fetch `buf`, protoc plugins.

**Interfaces:**
- Produces: generated Go types and gRPC clients/servers under `gen/proto/...`.

- [ ] **Step 1: Write `proto/buf.yaml`**

```yaml
version: v1
name: github.com/sobhanaz/bobres-3x-ui-telegram-panel/proto
deps:
  - buf.build/googleapis/googleapis
breaking:
  use:
    - FILE
lint:
  use:
    - DEFAULT
```

- [ ] **Step 2: Write `proto/common/v1/types.proto`**

```protobuf
syntax = "proto3";
package common.v1;
option go_package = "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1;commonv1";

message Money {
  int64 amount = 1;
  string currency = 2;
}

message Pagination {
  int32 page = 1;
  int32 page_size = 2;
}
```

- [ ] **Step 3: Write `proto/core/v1/core.proto`** with messages for User, Plan, Order, Wallet, Subscription, Settings and RPCs listed in the design spec.

- [ ] **Step 4: Write `proto/payments/v1/payments.proto`** and `proto/provisioner/v1/provisioner.proto`** with all RPCs.

- [ ] **Step 5: Add Makefile targets for `buf generate` and `buf lint`.**

- [ ] **Step 6: Run `buf generate` and commit generated code.**

Run: `make proto`
Expected: `gen/proto/...` created, `go build ./...` still passes.

- [ ] **Step 7: Commit.**

```bash
git add proto gen Makefile
git commit -m "feat(proto): add core, payments, provisioner v1 contracts"
```

### Task 1a.2: Add goose migrations for core, payments, provisioner schemas

**Files:**
- Create: `migrations/core/00001_init.sql`
- Create: `migrations/payments/00001_init.sql`
- Create: `migrations/provisioner/00001_init.sql`
- Modify: `go.mod` (add goose)
- Create: `internal/migrate/migrate.go`

**Interfaces:**
- Produces: `func Up(db *sql.DB, schema string) error`.

- [ ] **Step 1: Write core schema migration from design spec.**

- [ ] **Step 2: Write payments schema migration.**

- [ ] **Step 3: Write provisioner schema migration.**

- [ ] **Step 4: Add goose dependency and migration runner.**

```go
package migrate

import (
    "database/sql"
    "embed"
    "fmt"
    "github.com/pressly/goose/v3"
)

//go:embed core/*.sql payments/*.sql provisioner/*.sql
var fs embed.FS

func Up(db *sql.DB, schema string) error {
    goose.SetBaseFS(fs)
    if err := goose.SetDialect("postgres"); err != nil {
        return err
    }
    if _, err := db.Exec(fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
        return err
    }
    return goose.Up(db, schema)
}
```

- [ ] **Step 5: Test migrations against ephemeral Postgres.**

Use: `github.com/ory/dockertest` or existing test DB setup if available.

Run: `go test ./internal/migrate/...`
Expected: PASS

- [ ] **Step 6: Commit.**

### Task 1a.3: Add crypto/encryption package for service secrets

**Files:**
- Create: `internal/crypto/envelope.go`
- Create: `internal/crypto/envelope_test.go`

**Interfaces:**
- Produces: `type Envelope struct{ master []byte }`, `func NewEnvelope(master []byte) *Envelope`, `func (e *Envelope) Encrypt(plaintext []byte) ([]byte, error)`, `func (e *Envelope) Decrypt(ciphertext []byte) ([]byte, error)`.

- [ ] **Step 1: Write envelope encryption using AES-GCM with a random per-secret nonce/key derived via HKDF-SHA256 from master.**

- [ ] **Step 2: Write tests for round-trip and tamper detection.**

- [ ] **Step 3: Commit.**

### Task 1a.4: Add UUID v7 helper and money utilities

**Files:**
- Create: `internal/uuid/uuid.go`
- Create: `internal/money/money.go`
- Create: `internal/money/money_test.go`

**Interfaces:**
- Produces: `func NewV7() (uuid.UUID, error)`, `func MustV7() uuid.UUID`.
- Produces: `type Amount struct{ value int64; currency string }`, scales for IRT/USDT, formatting.

- [ ] **Step 1: Use `github.com/google/uuid` v7 or `github.com/gofrs/uuid/v5` for v7.**

- [ ] **Step 2: Implement money utilities.**

- [ ] **Step 3: Commit.**

### Task 1a.5: Implement core domain stores with sqlc

**Files:**
- Create: `internal/core/sqlc/queries.sql`
- Create: `internal/core/sqlc/schema.sql` (copy from migration)
- Create: `internal/core/store/user.go`
- Create: `internal/core/store/plan.go`
- Create: `internal/core/store/order.go`
- Create: `internal/core/store/wallet.go`
- Create: `internal/core/store/subscription.go`
- Create: `internal/core/store/audit.go`
- Create: `internal/core/store/store_test.go`
- Modify: `Makefile` (sqlc generate target)

**Interfaces:**
- Produces: `UserStore`, `PlanStore`, `OrderStore`, `WalletStore`, `SubscriptionStore`, `AuditStore` with CRUD + business-specific operations.

- [ ] **Step 1: Install sqlc and add config `sqlc.yaml`.**

- [ ] **Step 2: Write queries for users, plans, orders, wallets, subscriptions, audit_log.**

- [ ] **Step 3: Generate Go code with sqlc.**

- [ ] **Step 4: Wrap generated queries in domain stores with transactions.**

- [ ] **Step 5: Write tests using testcontainers or existing test DB.**

- [ ] **Step 6: Commit.**

### Task 1a.6: Implement core domain logic (orders, wallet, trial eligibility)

**Files:**
- Create: `internal/core/domain/order.go`
- Create: `internal/core/domain/wallet.go`
- Create: `internal/core/domain/trial.go`
- Create: `internal/core/domain/domain_test.go`
- Create: `internal/core/service/core.go`

**Interfaces:**
- Produces: `func (s *Service) CreateOrder(...) (*Order, error)`, `DebitWallet(...)`, `CreditWallet(...)`, `CanStartTrial(userID)`.

- [ ] **Step 1: Implement order creation with idempotency key.**

- [ ] **Step 2: Implement wallet debit with ledger entry and balance update in one transaction.**

- [ ] **Step 3: Implement trial eligibility check.**

- [ ] **Step 4: Write table-driven tests.**

- [ ] **Step 5: Commit.**

### Task 1a.7: Wire gRPC server into core

**Files:**
- Modify: `cmd/core/main.go`
- Create: `internal/core/server/server.go`
- Create: `internal/core/server/users.go`
- Create: `internal/core/server/plans.go`
- Create: `internal/core/server/orders.go`
- Create: `internal/core/server/wallet.go`

**Interfaces:**
- Produces: running gRPC server on a configurable port with service-token interceptor.

- [ ] **Step 1: Add gRPC listener address to config.**

- [ ] **Step 2: Implement unary interceptor validating `Authorization: Bearer <token>`.**

- [ ] **Step 3: Register CoreService server and implement stubs for user/plan/order/wallet.**

- [ ] **Step 4: Add health/readiness checks for DB connectivity.**

- [ ] **Step 5: Commit.**

### Task 1a.8: Add Redis-backed repository and rate limiter (prep for bot)

**Files:**
- Create: `internal/redis/redis.go`
- Create: `internal/ratelimit/ratelimit.go`
- Create: `internal/ratelimit/ratelimit_test.go`

**Interfaces:**
- Produces: `func NewPool(url string) *redis.Pool`, `func (rl *Limiter) Allow(key string, window time.Duration, max int) (bool, error)`.

- [ ] **Step 1: Add `github.com/gomodule/redigo` or `github.com/redis/go-redis/v9`.**

- [ ] **Step 2: Implement token-bucket/sliding-window rate limiter.**

- [ ] **Step 3: Commit.**

### Task 1a.9: Add service configuration for bot/payments/provisioner

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/bot.go`
- Create: `internal/config/payments.go`
- Create: `internal/config/provisioner.go`

**Interfaces:**
- Produces: `BotConfig`, `PaymentsConfig`, `ProvisionerConfig` with required secrets and gRPC target addresses.

- [ ] **Step 1: Add bot token, core/payments/provisioner gRPC addresses, encryption key, etc.**

- [ ] **Step 2: Validate required secrets fail-closed in prod.**

- [ ] **Step 3: Commit.**

### Task 1a.10: Run full test suite and close sub-phase

**Files:**
- Modify: `.github/workflows/ci.yml` if needed (add buf, sqlc, goose steps)

- [ ] **Step 1: Run `go test ./...`, `buf lint`, `go vet`, `golangci-lint run`.**

- [ ] **Step 2: Fix any failures.**

- [ ] **Step 3: Commit and push sub-phase 1a.**

---

## Sub-Phase 1b: Event Bus, Provisioner, 3x-ui Adapter

**Goal:** `provisioner` can create/renew/delete clients on fake and real 3x-ui panels; cross-service events flow through Postgres outbox.

### Task 1b.1: Implement transactional outbox library

**Files:**
- Create: `internal/eventbus/outbox.go`
- Create: `internal/eventbus/relay.go`
- Create: `internal/eventbus/eventbus_test.go`

**Interfaces:**
- Produces: `type Outbox struct{ db *sql.DB; schema string }`, `func (o *Outbox) Publish(ctx, topic, payload) error`, `func (r *Relay) Start(ctx) error`.

- [ ] **Step 1: Implement outbox insert inside caller's transaction.**

- [ ] **Step 2: Implement LISTEN/NOTIFY relay polling each schema's outbox.**

- [ ] **Step 3: Write tests for publish/relay/dedup.**

- [ ] **Step 4: Commit.**

### Task 1b.2: Add provisioner store and migrations

**Files:**
- Create: `internal/provisioner/sqlc/queries.sql`
- Create: `internal/provisioner/store/server.go`
- Create: `internal/provisioner/store/job.go`
- Create: `internal/provisioner/store/clientmap.go`
- Create: `internal/provisioner/store/store_test.go`

**Interfaces:**
- Produces: `ServerStore`, `JobStore`, `ClientMapStore`.

- [ ] **Step 1: Write sqlc queries and generated wrappers.**

- [ ] **Step 2: Write CRUD tests.**

- [ ] **Step 3: Commit.**

### Task 1b.3: Define PanelAdapter interface and 3x-ui v3.x implementation

**Files:**
- Create: `internal/provisioner/adapter/adapter.go`
- Create: `internal/provisioner/adapter/xui_v3.go`
- Create: `internal/provisioner/adapter/xui_v3_test.go`
- Create: `internal/provisioner/adapter/adapter_integration_test.go`

**Interfaces:**
- Produces: `type PanelAdapter interface{ CreateClient(...); RenewClient(...); DeleteClient(...); ResetTraffic(...); GetUsage(...); GetLinks(...); ListInbounds(...); HealthCheck(...) }`.
- Produces: `func NewXUIv3(baseURL, apiToken string, httpClient *http.Client) PanelAdapter`.

- [ ] **Step 1: Define interface and structs.**

- [ ] **Step 2: Implement 3x-ui v3.x calls using existing `internal/xui/client.go` patterns.**

- [ ] **Step 3: Write fake server tests using `internal/xuifake/server.go`.**

- [ ] **Step 4: Write integration test guarded by build tag `integration`.**

- [ ] **Step 5: Commit.**

### Task 1b.4: Implement provisioner gRPC service

**Files:**
- Modify: `cmd/provisioner/main.go`
- Create: `internal/provisioner/server/server.go`
- Create: `internal/provisioner/server/create.go`
- Create: `internal/provisioner/server/renew.go`
- Create: `internal/provisioner/server/health.go`

**Interfaces:**
- Produces: `ProvisionerService` RPC implementations.

- [ ] **Step 1: Implement `CreateClient` using adapter and store client_map.**

- [ ] **Step 2: Implement `RenewClient`, `DeleteClient`, `ResetTraffic`.**

- [ ] **Step 3: Implement `ListInbounds` and `HealthCheck`.**

- [ ] **Step 4: Add outbox publishing for `subscription.provisioned`/`provision_failed`.**

- [ ] **Step 5: Commit.**

### Task 1b.5: Extend fake 3x-ui server to support provisioner flows

**Files:**
- Modify: `internal/xuifake/server.go`
- Modify: `internal/xuifake/server_test.go`

- [ ] **Step 1: Add in-memory clients/inbounds storage.**

- [ ] **Step 2: Implement endpoints used by provisioner adapter.**

- [ ] **Step 3: Commit.**

### Task 1b.6: End-to-end core → provisioner via gRPC in tests

**Files:**
- Create: `tests/provisioner_flow_test.go`

- [ ] **Step 1: Spin up core and provisioner in process with test DB.**

- [ ] **Step 2: Send `CreateOrder` + `ProvisionRequested` and verify client created in fake panel.**

- [ ] **Step 3: Commit and close sub-phase 1b.**

---

## Sub-Phase 1c: Payments Service — Manual Receipts and Wallet

**Goal:** `payments` can create intents, accept manual receipt/TXID submissions, let admins approve/reject, and keep ledger in sync with `core`.

### Task 1c.1: Payments store and migrations

**Files:**
- Create: `internal/payments/sqlc/queries.sql`
- Create: `internal/payments/store/intent.go`
- Create: `internal/payments/store/receipt.go`
- Create: `internal/payments/store/ledger.go`
- Create: `internal/payments/store/store_test.go`

**Interfaces:**
- Produces: `IntentStore`, `ReceiptStore`, `LedgerStore`.

- [ ] **Step 1: Write sqlc queries and generated code.**

- [ ] **Step 2: Write tests.**

- [ ] **Step 3: Commit.**

### Task 1c.2: Provider interface and manual providers

**Files:**
- Create: `internal/payments/provider/provider.go`
- Create: `internal/payments/provider/manual_card.go`
- Create: `internal/payments/provider/manual_crypto.go`
- Create: `internal/payments/provider/wallet.go`
- Create: `internal/payments/provider/provider_test.go`

**Interfaces:**
- Produces: `type Provider interface{ CreateIntent(ctx, req) (*Intent, error); Instructions(ctx, intent) (string, error) }`.

- [ ] **Step 1: Define provider interface and registry.**

- [ ] **Step 2: Implement wallet provider (debit only).**

- [ ] **Step 3: Implement manual card and crypto providers with static instructions from settings.**

- [ ] **Step 4: Commit.**

### Task 1c.3: Payments domain logic (review, ledger, idempotency)

**Files:**
- Create: `internal/payments/domain/review.go`
- Create: `internal/payments/domain/ledger.go`
- Create: `internal/payments/domain/domain_test.go`
- Create: `internal/payments/service/payments.go`

**Interfaces:**
- Produces: `func (s *Service) ReviewReceipt(ctx, reviewerID, intentID, decision, reason) (*Intent, error)`.
- Produces: `func (s *Service) SubmitReceipt(ctx, userID, intentID, fileID, ref) (*Intent, error)`.
- Produces: `func (s *Service) SubmitTXID(ctx, userID, intentID, network, txid) (*Intent, error)`.

- [ ] **Step 1: Implement review with ledger credit on approve.**

- [ ] **Step 2: Implement submission validation and idempotency.**

- [ ] **Step 3: Write table-driven tests including race/double-approve.**

- [ ] **Step 4: Commit.**

### Task 1c.4: Payments gRPC server

**Files:**
- Modify: `cmd/payments/main.go`
- Create: `internal/payments/server/server.go`

- [ ] **Step 1: Register PaymentsService RPCs.**

- [ ] **Step 2: Add service-token interceptor.**

- [ ] **Step 3: Commit.**

### Task 1c.5: Core → payments integration (order created → intent)

**Files:**
- Modify: `internal/core/service/core.go`
- Create: `internal/core/service/payments_client.go`
- Create: `internal/core/service/payments_test.go`

- [ ] **Step 1: On `CreateOrder` with non-wallet provider, call `payments.CreateIntent`.**

- [ ] **Step 2: On wallet order, debit wallet directly.**

- [ ] **Step 3: Implement outbox consumer for `payment.succeeded` to mark order paid.**

- [ ] **Step 4: Commit and close sub-phase 1c.**

---

## Sub-Phase 1d: Telegram Bot

**Goal:** Bot handles `/start`, language selection, plans, wallet, buy, trial, support, and admin commands; talks to `core` via gRPC.

### Task 1d.1: Add Telegram bot library wrapper

**Files:**
- Modify: `go.mod`
- Create: `internal/bot/tg/client.go`
- Create: `internal/bot/tg/types.go`
- Create: `internal/bot/tg/client_test.go`

**Interfaces:**
- Produces: `type Client interface{ SendMessage(...); EditMessage(...); AnswerCallback(...); SendPhoto(...); GetFile(...); SetWebhook(...); GetUpdates(...) }`.

- [ ] **Step 1: Add `github.com/go-telegram-bot-api/telegram-bot-api/v5` or lightweight wrapper.**

- [ ] **Step 2: Abstract client for testability.**

- [ ] **Step 3: Commit.**

### Task 1d.2: i18n strings and branding loader

**Files:**
- Create: `internal/bot/i18n/en.yaml`
- Create: `internal/bot/i18n/fa.yaml`
- Create: `internal/bot/i18n/i18n.go`
- Create: `internal/bot/i18n/i18n_test.go`

**Interfaces:**
- Produces: `func T(lang, key string, args ...any) string`, `func SetStrings(map[string]map[string]string)`.

- [ ] **Step 1: YAML structure with placeholders.**

- [ ] **Step 2: Load from embedded defaults + override from `core.GetSettings`.**

- [ ] **Step 3: Commit.**

### Task 1d.3: Bot state machine and inline keyboard builder

**Files:**
- Create: `internal/bot/state/state.go`
- Create: `internal/bot/state/state_test.go`
- Create: `internal/bot/keyboard/keyboard.go`

**Interfaces:**
- Produces: `type State struct{ UserID int64; Scene string; Data map[string]any }`, `func (s *Store) Get(userID int64) (*State, error)`, `func (s *Store) Set(...) error`.

- [ ] **Step 1: Implement Redis-backed state store.**

- [ ] **Step 2: Implement keyboard builder with back/home buttons.**

- [ ] **Step 3: Commit.**

### Task 1d.4: Message handlers (start, plans, wallet, trial, support)

**Files:**
- Create: `internal/bot/handler/handler.go`
- Create: `internal/bot/handler/start.go`
- Create: `internal/bot/handler/plans.go`
- Create: `internal/bot/handler/wallet.go`
- Create: `internal/bot/handler/trial.go`
- Create: `internal/bot/handler/support.go`
- Create: `internal/bot/handler/admin.go`
- Create: `internal/bot/handler/handler_test.go`

**Interfaces:**
- Produces: `func (h *Handler) HandleUpdate(ctx, update) error`.

- [ ] **Step 1: Route `/start`, text messages, callbacks.**

- [ ] **Step 2: Implement language selection and user upsert.**

- [ ] **Step 3: Implement plan list, detail, order creation, payment instructions.**

- [ ] **Step 4: Implement wallet top-up flow.**

- [ ] **Step 5: Implement trial flow with eligibility check.**

- [ ] **Step 6: Implement minimal support ticket flow.**

- [ ] **Step 7: Implement admin commands/stats.**

- [ ] **Step 8: Commit.**

### Task 1d.5: Bot runner (long polling + webhook)

**Files:**
- Modify: `cmd/bot/main.go`
- Create: `internal/bot/runner/runner.go`

- [ ] **Step 1: Start long polling by default; support webhook via env.**

- [ ] **Step 2: Add graceful shutdown and health/readiness checks.**

- [ ] **Step 3: Add per-user rate limiting middleware.**

- [ ] **Step 4: Commit and close sub-phase 1d.**

---

## Sub-Phase 1e: Admin Dashboard (Vue 3 + Core API)

**Goal:** Embedded Vue 3 SPA in `core` with auth, manual payment queue, plans, users, subscriptions, servers, branding/texts, audit log.

### Task 1e.1: Scaffold Vue 3 dashboard in `dashboard/`

**Files:**
- Create: `dashboard/package.json`
- Create: `dashboard/vite.config.ts`
- Create: `dashboard/tsconfig.json`
- Create: `dashboard/index.html`
- Create: `dashboard/src/main.ts`
- Create: `dashboard/src/App.vue`
- Create: `dashboard/src/router/index.ts`
- Create: `dashboard/src/style.css`

- [ ] **Step 1: Initialize with Vue 3, Vite, TypeScript, vue-router, pinia, axios.**

- [ ] **Step 2: Build dev server works on `http://localhost:5173`.**

- [ ] **Step 3: Commit.**

### Task 1e.2: Core JSON API routes

**Files:**
- Create: `internal/core/http/http.go`
- Create: `internal/core/http/auth.go`
- Create: `internal/core/http/plans.go`
- Create: `internal/core/http/orders.go`
- Create: `internal/core/http/payments.go`
- Create: `internal/core/http/users.go`
- Create: `internal/core/http/subscriptions.go`
- Create: `internal/core/http/servers.go`
- Create: `internal/core/http/settings.go`
- Create: `internal/core/http/audit.go`
- Create: `internal/core/http/http_test.go`

**Interfaces:**
- Produces: `func RegisterRoutes(mux, svc, auth) error`.

- [ ] **Step 1: Add session cookie + CSRF middleware.**

- [ ] **Step 2: Add password login + argon2id + optional TOTP.**

- [ ] **Step 3: Add Telegram Login Widget callback verification.**

- [ ] **Step 4: Implement CRUD endpoints for Phase 1 pages.**

- [ ] **Step 5: Write HTTP tests.**

- [ ] **Step 6: Commit.**

### Task 1e.3: Staff store and role checks

**Files:**
- Create: `internal/core/store/staff.go`
- Create: `internal/core/store/staff_test.go`
- Create: `internal/core/authz/authz.go`

**Interfaces:**
- Produces: `func (a *Authorizer) HasPermission(role, perm string) bool`.

- [ ] **Step 1: Add staff table and role-permission matrix.**

- [ ] **Step 2: Implement middleware enforcing permissions per route.**

- [ ] **Step 3: Commit.**

### Task 1e.4: Dashboard pages

**Files:**
- Create: `dashboard/src/views/Login.vue`
- Create: `dashboard/src/views/Overview.vue`
- Create: `dashboard/src/views/Plans.vue`
- Create: `dashboard/src/views/Orders.vue`
- Create: `dashboard/src/views/PaymentQueue.vue`
- Create: `dashboard/src/views/Users.vue`
- Create: `dashboard/src/views/Subscriptions.vue`
- Create: `dashboard/src/views/Servers.vue`
- Create: `dashboard/src/views/Branding.vue`
- Create: `dashboard/src/views/AuditLog.vue`
- Create: `dashboard/src/stores/auth.ts`
- Create: `dashboard/src/stores/api.ts`
- Create: `dashboard/src/api/client.ts`

- [ ] **Step 1: Implement login flow with CSRF.**

- [ ] **Step 2: Implement each page with API calls.**

- [ ] **Step 3: Add RTL support and CSS variables for branding.**

- [ ] **Step 4: Commit.**

### Task 1e.5: Embed built SPA into core binary

**Files:**
- Modify: `internal/core/server/server.go`
- Create: `internal/core/http/spa.go`
- Modify: `Makefile`
- Modify: `Dockerfile`

- [ ] **Step 1: Add `//go:embed dist` for dashboard build output.**

- [ ] **Step 2: Serve `/admin/*` from embedded `dist/index.html` with API fallback.**

- [ ] **Step 3: Add dashboard build to Makefile and Dockerfile.**

- [ ] **Step 4: Commit and close sub-phase 1e.**

---

## Sub-Phase 1f: Installer, Compose, and End-to-End Integration

**Goal:** One-command install on Ubuntu/Debian produces a working stack; CI verifies it.

### Task 1f.1: Update docker-compose for 4 services + Caddy + Redis

**Files:**
- Modify: `deploy/docker-compose.yml`
- Modify: `deploy/Caddyfile`

- [ ] **Step 1: Add Redis service.**

- [ ] **Step 2: Add Caddy routes for `/admin/*`, `/api/*`, bot webhook, gRPC internal only.**

- [ ] **Step 3: Add inter-service env for gRPC targets and tokens.**

- [ ] **Step 4: Commit.**

### Task 1f.2: Extend install.sh

**Files:**
- Modify: `install/install.sh`

- [ ] **Step 1: Prompt for bot token, admin Telegram ID, 3x-ui URL/token, domain, DB password.**

- [ ] **Step 2: Generate master encryption key and service tokens.**

- [ ] **Step 3: Seed first owner from admin Telegram ID.**

- [ ] **Step 4: Write `.env` with all service envs and chmod 600.**

- [ ] **Step 5: Run compose and health checks.**

- [ ] **Step 6: Commit.**

### Task 1f.3: Add CLI `bobres` commands for Phase 1

**Files:**
- Modify: `cmd/bobres/main.go`
- Create: `cmd/bobres/migrate.go`
- Create: `cmd/bobres/createstaff.go`
- Create: `cmd/bobres/doctor.go`

- [ ] **Step 1: Add `bobres migrate` to run goose migrations.**

- [ ] **Step 2: Add `bobres create-staff` for password-based staff accounts.**

- [ ] **Step 3: Add `bobres doctor` checks for Phase 1 (DB, Redis, 3x-ui, bot token).**

- [ ] **Step 4: Commit.**

### Task 1f.4: End-to-end install test

**Files:**
- Create: `tests/e2e/install_test.go`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Run `install.sh` in CI with test env and fake 3x-ui.**

- [ ] **Step 2: Verify all services healthy and bot responds to mocked Telegram updates.**

- [ ] **Step 3: Commit and close sub-phase 1f.**

---

## Self-Review

### Spec coverage check

| Spec Section | Plan Tasks |
|---|---|
| Four services | 1a.7, 1b.4, 1c.4, 1d.5 |
| gRPC contracts | 1a.1 |
| Outbox events | 1b.1 |
| Core domain | 1a.5, 1a.6 |
| 3x-ui adapter | 1b.3, 1b.5 |
| Manual payments | 1c.2, 1c.3, 1c.5 |
| Telegram bot | 1d.1–1d.5 |
| Dashboard | 1e.1–1e.5 |
| Installer | 1f.1–1f.4 |

### Placeholder scan

No `TBD`, `TODO`, or "implement later" placeholders. Every task has concrete deliverables.

### Type consistency

- gRPC message/field names match across tasks.
- Store method names follow sqlc-generated patterns.
- Money amounts are always `int64` + currency `string`.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-09-30-phase1-implementation.md`.

Two execution options:

1. **Subagent-Driven** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Given the scope, I recommend **subagent-driven with one sub-phase at a time**. Start with sub-phase 1a.
