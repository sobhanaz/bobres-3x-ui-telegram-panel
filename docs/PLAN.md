# Project Plan (living document; Phase 0 done, Phase 1 in progress)

Product: Telegram bot + web dashboard for selling and managing 3x-ui (Xray) subscriptions.
Brand name: BOBRES (chosen by owner; trademark/domain/handle availability not yet checked).
Status: planning. Nothing here is final until decisions below are closed.

## 1. Decisions so far
- Language: Go. Database: PostgreSQL. Cache/state: Redis. Events: Postgres outbox (NATS later, see §11).
- Single 3x-ui server at launch; keep a `servers` table so multi-server/nodes can be added later.
- Bot + web dashboard. UI languages: Persian (fa) and English (en).
- Payments: wallet top-up, Zarinpal (Iranian gateway), crypto (TON/USDT/TRX), Telegram Stars.
- v1 features: plans and purchase, renew and traffic top-up, free trial, resellers, discount codes,
  referrals, subscription link + QR, expiry/usage notifications, support tickets.
- Distribution: owner runs it now; later sold/licensed to others -> installer, updates and license
  checks must be designed from the start. Source stays private; proprietary license.
- Commits/pushes are made only under the owner's own GitHub account (no tool attribution).

## 2. Open decisions
- Brand: BOBRES chosen. Still check domain, Telegram bot @username, GitHub org and trademark conflicts.
- Where the payment gateway service runs (inside Iran vs abroad). UNVERIFIED: whether Zarinpal
  requires an Iranian server IP. Must be checked with their docs/support before infra design.
- Reseller model: discounted prices vs credit limit.
- Crypto verification: manual TXID vs automatic chain watcher.
- Plan model: traffic-limited vs time-only.
- 3x-ui panel version and HTTPS setup of the owner's panel.
- Installer/image distribution and licensing model for future customers.

## 3. Architecture (proposal: coarse microservices, one monorepo)
| Service | Responsibility |
|---|---|
| gateway | Reverse proxy, automatic TLS, rate limiting, routing |
| bot | Telegram handlers, menus, i18n. No business logic |
| core | Users, plans, orders, wallet ledger, discounts, referrals, resellers, tickets |
| payments | Gateways, webhooks, reconciliation |
| provisioner | ONLY service that talks to 3x-ui: create/renew/delete, usage sync |
| notifier | Scheduled alerts, broadcasts, retries, Telegram rate limit queue |
| dashboard | Admin web UI (Vue 3 SPA embedded in core, see §12) |

- Sync: gRPC. Async: Postgres outbox events (NATS later) (e.g. order.paid -> provision -> notify).
- Patterns: transactional outbox, saga for purchase flow, idempotency keys on all money paths.
- Data: one Postgres, one schema per service, no cross-schema joins. Redis for bot state/locks/cache.
- Alternative to weigh: modular monolith first, split later. Fewer moving parts for a small team.

## 4. 3x-ui integration (from upstream OpenAPI, v3.8.5)
- Auth: `Authorization: Bearer <API token>` (Settings -> Security). Token is effectively full-admin.
- Client key = email. Use `u{telegramId}-{orderId}`; store our own client_ref.
- Endpoints: clients/add, update/{email}, del/{email}, bulkAdjust (renew: addDays/addBytes),
  resetTraffic/{email}, traffic/{email}, links/{email}, subLinks/{subId}, get/tgId/{tgId}, onlines.
- clients/update REPLACES the row (not a patch): use per-client lock + read-modify-write.
- Use a separate Telegram bot token from 3x-ui's built-in bot.
- Generate the Go client from the OpenAPI spec (oapi-codegen); pin and check panel version at startup.
- 3x-ui is GPL-3.0: we only call it over HTTP and copy none of its code.

## 5. Service count suggestion
Split by security, failure and scale boundary, not by feature. 4 services + off-the-shelf gateway:
- gateway: Caddy (not our code)
- bot: Telegram-facing, scales on message load
- core: users, plans, orders, ledger, tickets, referrals, resellers, dashboard, scheduler/notifier
  (internal modules with strict boundaries so they can be split later)
- payments: holds gateway secrets, may need to run in a different region (Iran vs abroad)
- provisioner: only holder of the 3x-ui API token
Reason: 4 services keep the one-command install simple while isolating secrets and regional needs.

## 6. DevOps
### Environments
- dev (docker compose on laptop, fake 3x-ui server), staging (small VPS, real 3x-ui test panel), prod.
- Same images promoted dev -> staging -> prod. Config only via env, never rebuilt per environment.

### Build and release
- Monorepo, multi-stage Dockerfiles, distroless/scratch images, non-root user, read-only filesystem.
- GitHub Actions: lint (golangci-lint), test, govulncheck, trivy image scan, build, push to registry.
- Release: semantic versions, git tags, goreleaser, changelog, image signing (cosign), SBOM.
- Multi-arch images (amd64 + arm64).

### Runtime and orchestration
- Docker Compose for v1 (single VPS). Kubernetes/k3s only if scale requires it later.
- Healthcheck + readiness endpoints on every service; restart policies; resource limits.
- Zero/low-downtime deploy: start new containers, health check, switch, keep previous for rollback.

### Infrastructure as code
- Server bootstrap via the installer (Docker, firewall, users, swap, time sync).
- Optional Terraform/Ansible for the owner's own fleet; installer stays the customer path.
- Hardening: SSH keys only, ufw (only 80/443/22), fail2ban, unattended security upgrades.

### Config and secrets
- `.env` generated by installer, chmod 600, owned by a service user; secrets never in images or git.
- Rotation procedure for bot token, 3x-ui API token, DB password, internal service keys.

### Data operations
- Postgres: automated encrypted dumps + WAL/PITR if needed, off-server storage (S3-compatible).
- Redis is disposable (state can be rebuilt). Outbox rows are pruned after publish + retention window.
- Restore drill documented and tested regularly; `bobres restore` command.
- Migrations: forward-only, tested, run by a one-shot job before new version starts; rollback plan.

### Observability
- Metrics: Prometheus + Grafana (payment success, provisioning failures, queue lag, 3x-ui health).
- Logs: structured JSON -> Loki, request/trace IDs (OpenTelemetry).
- Errors: Sentry or GlitchTip.
- Alerts: private admin Telegram chat + uptime checks; optional public status page.

### Update and license (for selling later)
- `bobres update` pulls signed images, runs migrations, health checks, auto-rollback on failure.
- License/activation server, offline grace period, telemetry strictly opt-in.
- Version compatibility check with the connected 3x-ui panel.

### Operations
- Runbooks: payment stuck, 3x-ui down, provisioning backlog, restore from backup, rotate secrets.
- Incident process, post-mortems, on-call = owner + alert channel.
- Capacity and cost notes per VPS size; log/metric retention limits so disks do not fill.
- Load tests and failure drills (kill 3x-ui, kill Postgres, replay webhooks) before launch.

## 7. Distribution model: white-label, single-tenant per install
Decision: BOBRES is the product sold to operators. Each customer (operator) runs their OWN
install on their OWN server with their OWN domain, bot token, 3x-ui panel and branding.
No shared multi-tenant SaaS in v1 (simpler security, no cross-customer data risk).

### Consequences
- Domain: customer buys it. Installer asks for it, checks DNS points to the server, Caddy gets TLS.
- Domain is needed for: dashboard HTTPS, payment webhooks/callbacks, subscription-link page.
  Bot itself can use long polling (no domain); webhook mode is optional.
- Brand is data, not code: bot name, logo, colors, welcome text, support contact, currency, ToS/privacy
  links, all in a `branding` config + editable from the dashboard. fa/en strings overridable per install.
- No hardcoded "BOBRES" in user-facing text; "Powered by BOBRES" footer is a license-tier option.
- Per-install secrets generated locally; the vendor (us) never sees customer bot tokens, keys or user data.
- Each install has its own DB, backups, and payment gateway accounts (customer's own Zarinpal/crypto keys).

### Vendor side (what we run)
- License server: issue keys, bind to install ID/domain, tiers (features, reseller limits, branding removal).
- Release channel: signed images + update manifest; customers pull with `bobres update`.
- Customer docs site, changelog, support channel.
- Optional opt-in telemetry (version, health only, never user data).

### Open questions
- Pricing/licensing: one-time, subscription, per-server? Source stays closed; images are signed.
- Do customers get shell access to source? (Assumed no: binary images only.)
- Support scope and SLA for customers.
- Legal: ToS for operators, and who is responsible for the VPN service they resell (needs a lawyer).
- Anti-piracy level: license check strictness vs offline grace period.

## 8. Decisions from Q&A round
- Licensing: flexible. Model it as ENTITLEMENTS (feature flags + limits + expiry) in the license key,
  so one-time / subscription / per-server / add-ons are just different entitlement sets. Pricing decided later.
- Delivery: signed Docker images only, closed source.
- "Powered by BOBRES" footer: removable on higher tier only (entitlement `white_label`).
- Trial: time-limited trial license (entitlement with short expiry).
- Crypto: manual TXID approval, automatic chain watcher, and third-party gateway. All behind ONE
  payment-provider interface; each is a separate provider, enabled per install.
- Resellers: both discount tiers and credit limit (pay later).
- Plans: both traffic-limited and time-only; customer-configurable.
- 3x-ui compatibility: any version, via a compatibility layer (see risk below).
- Inbounds: new clients auto-attach to all ENABLED inbounds (customer can disable inbounds).
- Installer OS: Ubuntu + Debian.
- Hosting: customer chooses (installer must not assume region; payment service deployable separately).
- Legal: not yet reviewed, needs professional guidance.
- Delivery approach: phased, step by step. Target: first working install as soon as possible.
- Team: owner + AI only.

## 9. Risks raised by these decisions
- "Any 3x-ui version": costliest choice. Upstream API changes fast. Proposal: detect version at
  startup, support a tested range via adapter interface, warn outside it. Start with latest v3.x adapter only,
  add older adapters when a real customer needs one.
- "All crypto modes": three providers = three sets of failure modes. Ship manual first, then third-party,
  then the chain watcher.
- Solo + ASAP + microservices: keep services at 4 and use a shared Go module for common code.
- Auto-attach to all inbounds: one client per inbound multiplies traffic rows; verify with 3x-ui that
  one client can attach to many inbounds (API has clients/attach and bulkAttach).
- Support load grows per customer: invest early in `bobres doctor`, clear preflight errors, docs.

## 10. Phases (each ends with something installable)
0. Foundation: repo layout, CI, installer skeleton, config/branding model, license format.
1. First sellable slice: provisioner + core + bot: plans, wallet, purchase, subscription link/QR, trial,
   manual payment approval, admin in bot. One-command install on clean Ubuntu.
2. Payments: Zarinpal, Telegram Stars, third-party crypto.
3. Retention: renew, traffic top-up, discounts, referrals, expiry/usage notifications.
4. Dashboard (web) + branding editor.
5. Resellers (tiers + credit), tickets.
6. Auto crypto watcher, older 3x-ui adapters, license server + update channel.
7. Hardening: backups/restore drills, monitoring, load tests, docs, legal review.

## 11. Weak-spot decisions (closed)
- Internal auth: one service token PER SERVICE (v1), mTLS added later. A server accepts only its legitimate
  callers: core accepts the bot; payments and provisioner accept only core (so a compromised bot cannot reach
  payment approvals or the 3x-ui token holder). The auth layer is one interceptor (`internal/grpcauth`).
- Queue/events: Postgres transactional outbox per service + a pull feed over gRPC (`events.v1.EventFeedService`):
  the producer keeps one cursor per consumer, consumers de-duplicate on a UUID event id and dead-letter events
  they cannot apply. No service reads another service's schema, so payments can run in another region.
  NO NATS yet; a NATS transport can replace Feed/Source without touching handlers. (Decided 2026-10-03 after
  the shared-table relay proved incompatible with per-service DB roles; supersedes LISTEN/NOTIFY.)
- License binding: domain + soft install ID; up to 3 re-activations per year.
- Services: 4 separate deployables (bot, core, payments, provisioner) in one Go monorepo, shared module, plus Caddy.
- Zarinpal: payments stays a separate deployable so it can run in any region. Verify Zarinpal's server-IP rules
  with their support BEFORE Phase 2.
- 3x-ui compatibility: adapter for latest v3.x first, version detection with warning; older adapters only on demand.
- Brand: keep BOBRES. Domain, bot username and trademark checks deferred (do before public launch).

## 12. Dashboard stack (closed)
- Vue 3 + Vite + TypeScript, built to static files and embedded in the `core` binary via `embed.FS`
  (same approach 3x-ui uses). No Node runtime in production, no separate container.
- Needs a JSON API on `core` (versioned, session + CSRF auth). This supersedes the templ/HTMX plan in 04-dashboard.md.
- Theming through CSS variables for white-label; RTL via CSS logical properties; Vazirmatn + Inter fonts; vue-i18n for fa/en.
- Keep the UI library decision open (e.g. Naive UI / PrimeVue / shadcn-vue) until Phase 4.
