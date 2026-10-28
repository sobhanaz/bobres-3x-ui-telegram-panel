# Project Plan (living document; Phases 0-7 built, updated 2026-10-19)

Product: Telegram bot + web dashboard for selling and managing 3x-ui (Xray) subscriptions.
Brand name: BOBRES, with BOBRES the beaver as the mascot (08-brand.md). Trademark, domain and handle checks are
still open (§2).
Status: Phases 0 to 7 are built (§10) and shipped as v0.1.0 (2026-10-14) to v0.17.1 (2026-10-28)
([CHANGELOG.md](../CHANGELOG.md)); what came after them is in [ROADMAP.md](ROADMAP.md). Next: the checks only a real
server gives (11-staging-run.md, the roadmap's first table). Still open: the legal review, the brand checks and the
prices (§2). How BOBRES is sold is decided: a free core and a subscription per install ([pricing.md](pricing.md)); what the
owner must still do before selling is in [launch-checklist.md](launch-checklist.md).

## 1. Decisions so far
Each item is the decision as it was taken, then what was built.
- Language: Go. Database: PostgreSQL. Cache/state: Redis. Events: Postgres outbox.
  Built: each service's outbox is pulled over gRPC, with one cursor per consumer (§11). No NATS.
- Single 3x-ui server at launch; keep a `servers` table so multi-server/nodes can be added later.
  Built: several 3x-ui servers since Phase 4 milestone 6a. A new service goes to the healthy, enabled server with the
  fewest clients per weight, among the plan's servers (any server when the plan names none).
- Bot + web dashboard. UI languages: Persian (fa) and English (en). Built: both, with right-to-left Persian.
- Payments: wallet top-up, Zarinpal (Iranian gateway), crypto (TON/USDT/TRX), Telegram Stars.
  Built: the wallet; card to card, USDT (TRC20 or ERC20) and the owner's Zarinpal link, each with a screenshot an
  admin approves; Zarinpal online (off until a merchant id is set); Telegram Stars; automatic USDT on TRON (TRC20).
  Not built: TON, TRX and third-party crypto processors (they exclude Iran).
- v1 features: plans and purchase, renew and traffic top-up, free trial, resellers, discount codes,
  referrals, subscription link + QR, expiry/usage notifications, support tickets. Built: all of them.
- Distribution: owner runs it now; later sold/licensed to others -> installer, updates and license
  checks must be designed from the start. Built: the one-command installer, `bobres update` with an automatic
  rollback, signed licenses and the vendor's license server (13-license-server.md).
- Source: planned private, under a proprietary license. The GitHub repository has been public since 2026-10-05
  (the installer downloads from its releases). Since 2026-10-14 it carries a draft [LICENSE](../LICENSE): source-available,
  all rights reserved. A lawyer has not reviewed it (§2).
- Commits/pushes are made only under the owner's own GitHub account (no tool attribution).

## 2. Open decisions
Three points are still open. The others are decided; each keeps its question with the answer.

Still open:
- **Legal review.** No lawyer has reviewed anything yet (09-legal-questions.md). Drafts exist since 2026-10-14: the
  [LICENSE](../LICENSE), [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) and the operator documents in
  [legal/](legal/README.md) (Terms of Service, Privacy Policy, Acceptable Use, Refund policy, a template for an operator's
  own store), in English and Persian. The lawyer settles who answers for the VPN service an operator resells, the payment
  providers' rules on VPN sales, the refund and tax rules, and the wording of the source license.
- **Brand checks.** BOBRES and the beaver are chosen. The domain, the Telegram bot @username, a GitHub org and
  trademark conflicts are not checked. Do it before the public launch ([launch-checklist.md](launch-checklist.md)).
- **Prices.** The numbers are the owner's: monthly and yearly, per tier, in one currency. The owner set them on 2026-10-20:
  Standard 4 and Pro 12 USDT a month, a year at ten months (40 and 120), in `internal/license/tiers.json` and
  [pricing.md](pricing.md). Only the Support rows are still "SET ME". The model is decided (below).

Decided:
- Free core, paid premium: DECIDED (2026-10-14), replacing the owner's earlier policy under which a missing or invalid
  license gated nothing. An install without a valid license runs on a free tier (1 server, 25 active services, 2 staff
  seats, no resellers, no white label, no automatic USDT, the Zarinpal, Stars and manual payment methods, security
  updates always); a license unlocks more; a 30-day implicit trial with pro-like entitlements (everything but white
  label, 5 seats) starts at an install's first start with the policy, whether fresh or upgraded. The limits refuse
  only new actions (a server, a service, a reseller, staff, a payment method); existing services keep working and
  renewing, and a customer never sees licensing text. The numbers are the owner's to tune
  (`internal/license/policy.go`). See [05-license.md](05-license.md).
- Pricing model: DECIDED (2026-10-14). **A subscription per install**, monthly or yearly, on top of the free core above.
  The owner sells to other operators and runs their own store on BOBRES too. The license token lasts 30 days at most, the
  bot renews it every day through the vendor's license server, and it lapses when unpaid: after the grace period the
  install falls back to the free tier, and nothing is deleted. The tiers (Standard, Pro) are in
  `internal/license/tiers.json`, explained in [pricing.md](pricing.md).
- Source license: DECIDED in principle (2026-10-14). **Source-available, all rights reserved**: the code may be read;
  copying, redistributing, hosting for others, reselling and white-labelling need a paid license. The text in
  [LICENSE](../LICENSE) is a draft that a lawyer must review (above).
- Where the payment gateway service runs: DECIDED. `payments` is its own deployable, so it can run in another region.
  Zarinpal calls can go through a proxy whose IP is registered (`--zarinpal-proxy`), and the pay page can live on a
  second, registered domain (`BOBRES_ZARINPAL_HOST`). Zarinpal stays off until a merchant id is set. Still
  UNVERIFIED: whether Zarinpal requires an Iranian server IP, and whether it keeps a terminal that sells VPNs. The
  owner asks their support before turning it on (Phase 2 design, "Decisions the owner must take").
- Reseller model: DECIDED, both. A tier gives a discount or a fixed price per plan, a credit limit lets a reseller buy
  now and pay later, and a reseller earns a commission on customers who joined through their link. Resellers work
  in the bot only (Phase 4 milestone 5).
- Crypto verification: DECIDED, both. Manual: a screenshot or TXID that an admin approves. Automatic: a watcher
  reads the store's TRON address and settles an exact, unique USDT amount (Phase 6a).
- Plan model: DECIDED, both. A plan is traffic-limited, time-only or both; traffic packages top up a running service.
- 3x-ui panel version and HTTPS setup: DECIDED. One adapter for 3x-ui v3.x, tested in CI against v3.8.5 and v3.9.0.
  A panel outside that range works, with a warning on the Servers page. The panel URL must be https; plain http is
  accepted only for a private address, with allow-private.
- Installer/image distribution and licensing model: DECIDED. The CLI comes from this repository's GitHub releases
  (Ed25519-signed checksums and key list; stable and beta channels). Images come from ghcr.io (amd64 and arm64,
  signed with cosign, with an SBOM). A license is a signed entitlements token, pasted by hand or activated online
  with a license key. Only the prices are open (above).

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

- Sync: gRPC. Async: Postgres outbox events, pulled over gRPC (e.g. order.paid -> provision -> notify).
- Patterns: transactional outbox, saga for purchase flow, idempotency keys on all money paths.
- Data: one Postgres, one schema per service, no cross-schema joins. Redis for bot state/locks/cache.
- Alternative to weigh: modular monolith first, split later. Decided against it: four services (§5, §11).

Built: the four services of §5 behind Caddy. The notifier and the broadcast worker run in the bot and read core's
event feed. Core runs the scheduler, the usage sync, the delivery worker, the dashboard and the system watchdog. A
fifth container, `backup`, makes the daily encrypted backups (Phase 4 milestone 6b).

## 4. 3x-ui integration (from upstream OpenAPI, v3.8.5)
- Auth: `Authorization: Bearer <API token>` (Settings -> Security). Token is effectively full-admin.
- Client key = email. Built as `u{telegramId}-{8 random characters}`; we store our own client_ref.
- Endpoints: clients/add, update/{email}, del/{email}, bulkAdjust (renew: addDays/addBytes),
  resetTraffic/{email}, traffic/{email}, links/{email}, subLinks/{subId}, get/tgId/{tgId}, onlines.
- clients/update REPLACES the row (not a patch): use per-client lock + read-modify-write. That includes the client's
  `group`: a body without one clears it, so the stored row goes back whole.
- Every client we create is in the panel's client group `BOBRES` (`xui.BotGroup`; `bobres_bot` up to v0.11.0; a reseller's in
  `BOBRES-<NAME>`; `clients/add` stores `client.group`), and the clients made before that are brought in line by a provisioner
  backfill that moves only clients we hold that are in a group we own (none, the old name, `BOBRES`, `BOBRES-...`) and leaves a group
  an operator chose (details and what the real panels do: 02-events-and-apis.md, "3x-ui adapter"). Marzban 0.8 has no groups.
- Use a separate Telegram bot token from 3x-ui's built-in bot.
- Planned: generate the Go client from the OpenAPI spec (oapi-codegen). Built instead: a typed client on the
  standard library only (`internal/xui`), checked against the v3.8.5 source and run in CI against real v3.8.5 and
  v3.9.0 panels. A health loop reads each panel's version every 60 s; the Servers page flags one outside that range.
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
The plan as written, with what was built under each heading ("Built").

### Environments
- dev (docker compose on laptop, fake 3x-ui server), staging (small VPS, real 3x-ui test panel), prod.
- Same images promoted dev -> staging -> prod. Config only via env, never rebuilt per environment.
- Built: dev is the Go test suite with in-process fakes (`internal/testenv`, a fake 3x-ui, a fake Telegram) and a
  local stack. CI runs the real installer against a real 3x-ui panel. Staging is the first real-server checklist
  (11-staging-run.md). Configuration comes only from `.env`.

### Build and release
- Monorepo, multi-stage Dockerfiles, distroless/scratch images, non-root user, read-only filesystem.
- GitHub Actions: lint (golangci-lint), test, govulncheck, trivy image scan, build, push to registry.
- Release: semantic versions, git tags, goreleaser, changelog, image signing (cosign), SBOM.
- Multi-arch images (amd64 + arm64).
- Built: one multi-stage Dockerfile; `scratch` images for the four services (Alpine with pg_dump for `backup`), user
  65532, read-only root filesystem, every capability dropped. CI: race tests against Postgres and Redis,
  golangci-lint, govulncheck (with a reviewed allowlist, now empty), the dashboard (vue-tsc, vitest, npm audit),
  shellcheck and signing tests, image builds with an end-to-end install and a restore drill against a real 3x-ui
  panel, a 3x-ui integration matrix, a Marzban integration job against the real panel and gitleaks. The release is `release.yml` on a tag, not goreleaser: it verifies,
  builds amd64 and arm64 images signed with cosign (keyless) with an SBOM, builds the CLI for Linux and macOS, and
  signs `checksums.txt` with Ed25519. Not built: a trivy image scan. The changelog is CHANGELOG.md.

### Runtime and orchestration
- Docker Compose for v1 (single VPS). Kubernetes/k3s only if scale requires it later.
- Healthcheck + readiness endpoints on every service; restart policies; resource limits.
- Zero/low-downtime deploy: start new containers, health check, switch, keep previous for rollback.
- Built: Compose on one server, health checks, restart policies, memory and process limits, log rotation (10 MB x 5
  per container). Updates are not zero-downtime: `bobres update` keeps a restore point, recreates the containers,
  waits for health and rolls back on its own when they do not come up (06-installer.md). Previous images are not
  kept: a rollback pulls them again.

### Infrastructure as code
- Server bootstrap via the installer (Docker, firewall, users, swap, time sync).
- Optional Terraform/Ansible for the owner's own fleet; installer stays the customer path.
- Hardening: SSH keys only, ufw (only 80/443/22), fail2ban, unattended security upgrades.
- Built: the installer's hardening step, also `bobres harden`: ufw with the SSH port detected, fail2ban for SSH,
  security-only unattended upgrades and swap on small servers, each asked. It does not turn off SSH passwords. No
  Terraform or Ansible. The services run as non-root users inside their containers, so there is no service user on
  the host.

### Config and secrets
- `.env` generated by installer, chmod 600, owned by a service user; secrets never in images or git.
- Rotation procedure for bot token, 3x-ui API token, DB password, internal service keys.
- Built: `.env` is 0600 and root's. `bobres secrets rotate` changes the bot token, the database passwords, the
  service tokens and the Redis password, and puts `.env` back when the services do not come back healthy. 3x-ui tokens
  are changed on the dashboard's Servers page. The three master keys and the backup key are not rotated
  (runbooks/rotate-secrets.md).

### Data operations
- Postgres: automated encrypted dumps + WAL/PITR if needed, off-server storage (S3-compatible).
- Redis is disposable (state can be rebuilt). Outbox rows are pruned after publish + retention window.
- Restore drill documented and tested regularly; `bobres restore` command.
- Migrations: forward-only, tested, run by a one-shot job before new version starts; rollback plan.
- Built: a daily encrypted backup (`pg_dump -Fc`, AES-256-GCM chunks under `BOBRES_BACKUP_KEY`), the newest 7 kept on
  the server, each sent to the owner on Telegram (up to 50 MB) and, when set up, copied to S3-compatible storage
  (Phase 7d). The backup carries the master keys, so a backup and the backup key restore everything. `bobres restore`
  (also from `s3://`), and a restore drill in CI. No WAL/PITR. Migrations: goose, forward-only, run by each service
  for its own schema when it starts; `bobres rollback` puts back the database from before an update. Outbox rows are
  not pruned yet (12-load-test.md, limit 4).

### Observability
- Metrics: Prometheus + Grafana (payment success, provisioning failures, queue lag, 3x-ui health).
- Logs: structured JSON -> Loki, request/trace IDs (OpenTelemetry).
- Errors: Sentry or GlitchTip.
- Alerts: private admin Telegram chat + uptime checks; optional public status page.
- Built instead (decided in Phase 7c): structured JSON logs with secrets redacted, `/healthz` and `/readyz` on every
  service, the dashboard's System page (versions, health, open problems, and the newest warnings and errors of every
  service), and a core watchdog that checks the store every minute and alerts staff on Telegram
  (runbooks/README.md). No metrics, tracing or error-tracking stack, and no public status page.
- Added after v0.7 (the Monitoring section): a host collector (a root timer writing a snapshot core reads read only; core still has no Docker socket),
  core's own time series (30 s samples for 7 days, 5-minute averages to 30 days, in Postgres), alerts for disk, memory, containers and the
  collector, and the repository's CI/CD facts through the bot ([04-dashboard.md](04-dashboard.md) "Monitoring API",
  [06-installer.md](06-installer.md) "Host monitoring"). Still no Prometheus, Grafana, Loki or tracing.

### Update and license (for selling later)
- `bobres update` pulls signed images, runs migrations, health checks, auto-rollback on failure.
- License/activation server, offline grace period, telemetry strictly opt-in.
- Version compatibility check with the connected 3x-ui panel.
- Built: `bobres update` verifies the signed release (checksums, key list, channel and license coverage), backs up,
  keeps a restore point, swaps in the new CLI, installs the new version and rolls back on its own. Images are pulled
  by tag: their cosign signatures are not checked on the server. The license server and online activation are built
  (13-license-server.md). The vendor key `vendor-v1-2026` exists and its public key is built into BOBRES; the server
  has run at `license.bobresai.cv` since 2026-10-21 (its kit is in `deploy/vendor/`). Telemetry: none. Only
  activation and the daily license refresh call the vendor, and only when `BOBRES_LICENSE_SERVER` is set. Each
  panel's version is read and flagged outside the tested range.

### Operations
- Runbooks: payment stuck, 3x-ui down, provisioning backlog, restore from backup, rotate secrets.
- Incident process, post-mortems, on-call = owner + alert channel.
- Capacity and cost notes per VPS size; log/metric retention limits so disks do not fill.
- Load tests and failure drills (kill 3x-ui, kill Postgres, replay webhooks) before launch.
- Built: 11 runbooks (runbooks/README.md) and `bobres support-bundle`. Failure drills run in the test suite:
  provisioner down while delivering, a replayed payment event, the notifier restarting, core down during a
  broadcast, the Postgres connection lost. A load test with its numbers (12-load-test.md). Not written: an
  incident process and a post-mortem template. Not done: the load test on a real VPS.

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
- No hardcoded "BOBRES" in user-facing text; "Powered by BOBRES" footer is a license-tier option (shown on the free
  tier and during the trial).
- Per-install secrets generated locally; the vendor (us) never sees customer bot tokens, keys or user data.
- Each install has its own DB, backups, and payment gateway accounts (customer's own Zarinpal/crypto keys).
- Built: all of the above, with two differences. The bot gives customers the 3x-ui panel's own subscription link;
  there is no branded subscription page. Long polling is the default and webhook mode is optional
  (`BOBRES_TELEGRAM_WEBHOOK_URL`). Branding, every bot text in both languages, the legal links and the currency name
  are edited on the dashboard. "Powered by BOBRES" shows only on the dashboard and its login page, on every install
  except one whose license includes `white_label` (the free tier and the trial show it); never in the bot.

### Vendor side (what we run)
- License server: issue keys, bind to install ID/domain, tiers (features, reseller limits, branding removal).
- Release channel: signed images + update manifest; customers pull with `bobres update`.
- Customer docs site, changelog, support channel.
- Optional opt-in telemetry (version, health only, never user data).
- Built: the license server (13-license-server.md) with its deployment kit (`deploy/vendor/`) and runbook
  (runbooks/vendor-license-server.md); deployed at `license.bobresai.cv` since 2026-10-21. The release channel is this repository's GitHub
  releases (signed CLI, checksums and key list; stable and beta). The docs live in `docs/` and the changelog in
  CHANGELOG.md; no docs site or support channel yet. No telemetry.

### Open questions
- Pricing/licensing: DECIDED (2026-10-14), a subscription per install; the prices are still the owner's (§2). The source is
  public (since 2026-10-05) and source-available under a draft license.
- Do customers get shell access to source? (Assumed no: binary images only.) The source is readable on GitHub
  since 2026-10-05; what customers may do with it is for the license text (a draft; legal review open).
- Support scope and SLA for customers. Still open (placeholders in pricing.md).
- Legal: ToS for operators, and who is responsible for the VPN service they resell (drafts in legal/; needs a lawyer). Still open.
- Anti-piracy level: license check strictness vs offline grace period. DECIDED: tokens last 30 days at most and
  are refreshed daily, with 0 to 90 grace days and 3 moves a year. A missing or invalid license is not blocked: the
  install runs on the trial, then the free tier, which refuses only new actions (decided 2026-10-14, §2); a stricter
  "vendor mode" is a later policy decision (13-license-server.md).

## 8. Decisions from Q&A round
- Licensing: flexible. Model it as ENTITLEMENTS (feature flags + limits + expiry) in the license key,
  so one-time / subscription / per-server / add-ons are just different entitlement sets. Pricing decided later.
  Built (05-license.md). The model is decided (2026-10-14): a subscription per install, as entitlement sets (the tiers
  in `internal/license/tiers.json`). The prices are still open (§2).
- Delivery: signed Docker images only, closed source. Built: signed images. The repository became public on
  2026-10-05 (§1).
- "Powered by BOBRES" footer: removable on higher tier only (entitlement `white_label`). Built.
- Trial: every install has an implicit 30-day trial from its first start with the license policy (2026-10-14), with
  everything but white label and 5 staff seats. A vendor can still issue a trial license (an entitlement with a short
  expiry).
- Crypto: manual TXID approval, automatic chain watcher, and third-party gateway. All behind ONE
  payment-provider interface; each is a separate provider, enabled per install. Built: manual and the TRC20 watcher;
  third-party gateways were dropped (they exclude Iran).
- Resellers: both discount tiers and credit limit (pay later). Built.
- Plans: both traffic-limited and time-only; customer-configurable. Built.
- 3x-ui compatibility: any version, via a compatibility layer (see risk below). Built: the v3.x adapter (§4).
- Inbounds: new clients auto-attach to all ENABLED inbounds (customer can disable inbounds). Built, with a
  per-server attach switch for each inbound on the Servers page.
- Installer OS: Ubuntu + Debian. Built: Ubuntu 22.04/24.04 and Debian 11/12/13, amd64 and arm64.
- Hosting: customer chooses (installer must not assume region; payment service deployable separately).
- Legal: not reviewed, needs professional guidance. Still open (§2).
- Delivery approach: phased, step by step. Target: first working install as soon as possible.
- Team: owner + AI only.

## 9. Risks raised by these decisions
- "Any 3x-ui version": costliest choice. Upstream API changes fast. Proposal: detect version at
  startup, support a tested range via adapter interface, warn outside it. Start with latest v3.x adapter only,
  add older adapters when a real customer needs one.
  Outcome: as proposed. The tested range is v3.8.5 to v3.9.0 (the CI matrix). No older adapter was needed yet.
- "All crypto modes": three providers = three sets of failure modes. Ship manual first, then third-party,
  then the chain watcher. Outcome: manual first (Phase 1), then the chain watcher (Phase 6a); third-party dropped.
- Solo + ASAP + microservices: keep services at 4 and use a shared Go module for common code. Outcome: as proposed.
- Auto-attach to all inbounds: one client per inbound multiplies traffic rows; verify with 3x-ui that
  one client can attach to many inbounds (API has clients/attach and bulkAttach). Outcome: one client is attached
  to every enabled inbound whose attach switch is on.
- Support load grows per customer: invest early in `bobres doctor`, clear preflight errors, docs.
  Outcome: `bobres doctor`, `bobres support-bundle`, the runbooks, and system alerts in Telegram.

## 10. Phases (each ends with something installable)
Each phase has a status line and what shipped, with the pull requests. "#N" is a pull request on GitHub.

0. Foundation: repo layout, CI, installer skeleton, config/branding model, license format.
   **Done (2026-09-30).** The monorepo and shared packages, CI (tests, lint, vulnerability and secret scans, Docker),
   the release workflow with Ed25519 signing and a signed key list, `install.sh` with signature checks, the signed
   license token with entitlements, and the typed 3x-ui client. Dependency bumps #3, #4, #6, #7; CI fix #8.
1. First sellable slice: provisioner + core + bot: plans, wallet, purchase, subscription link/QR, trial,
   manual payment approval, admin in bot. One-command install on clean Ubuntu.
   **Done in code (2026-10-03 to 10-05).** #13 the purchase loop, the Telegram bot and `bobres install`; #14
   `bobres status`, `logs` and `uninstall`, and the installer end to end in CI; #18 Go 1.26 and golangci-lint v2; #19
   real 3x-ui panels in CI and a purchase through the bot end to end; #23 the `bobres` menu and the one-command install
   from GitHub releases. The dashboard part moved to Phase 4. Left: the first real-server run (11-staging-run.md).
2. Payments: Zarinpal, Telegram Stars, third-party crypto.
   **Done, except third-party crypto (2026-10-05 to 10-11).** #20 Telegram Stars and Zarinpal (off by default); #22
   payment screenshots approved by an admin for card, USDT and the Zarinpal link; #24 Zarinpal aligned with its
   official docs and sandbox. Third-party crypto was not built: the processors exclude Iran (Phase 2 design). The
   automatic USDT watcher of Phase 6 took its place.
3. Retention: renew, traffic top-up, discounts, referrals, expiry/usage notifications.
   **Done (2026-10-11).** #25 renewals, traffic packages, reminders from the usage sync, discount codes and referrals
   (superpowers/specs/2026-10-06-phase3-retention-design.md).
4. Dashboard (web) + branding editor.
   **Done (2026-10-11 to 10-12).** All 17 sections at `https://<domain>/admin`, in six milestones
   (superpowers/specs/2026-10-07-phase4-dashboard-design.md): #26 foundation (link and password + TOTP logins,
   Persian/English, dark/light, the Overview); #27 customers and sales (users, services, plans, orders, the receipt
   queue, the ledger with CSV export, discounts and referrals); #28 store setup (branding and every bot text,
   settings, staff, the audit log); #29 support tickets and broadcasts; #30 resellers; #31 several 3x-ui servers and
   the payment gateways; #32 the System page (health, versions, update check, logs, backups, the license).
5. Resellers (tiers + credit), tickets.
   **Done (2026-10-11), as part of Phase 4.** #29 support tickets (bot and dashboard) and #30 resellers (tiers,
   credit limits, commission, settlements; the reseller panel in the bot).
6. Auto crypto watcher, older 3x-ui adapters, license server + update channel.
   **Done, except older 3x-ui adapters (2026-10-12).** #37 automatic USDT (TRC20) payments through TronGrid, with
   unmatched deposits reported to staff; #38 the vendor license server (`licensesrv`), online activation with a daily
   refresh, and the stable/beta update channel (the license's `updates_until` is advisory: a note, never a refusal). Older 3x-ui adapters are built
   only when a customer needs one (§9). Several servers already came with #31.
7. Hardening: backups/restore drills, monitoring, load tests, docs, legal review.
   **Done, except the legal review (2026-10-12).** #33 `bobres restore`, `bobres update` with an automatic rollback,
   `bobres rollback` and a restore drill in CI; #34 failure drills, the load test, runbooks and `bobres
   support-bundle`; #36 system alerts on Telegram from a core watchdog, server hardening, `bobres config` and `bobres
   secrets rotate`; #39 backup copies to S3-compatible storage and recorded Zarinpal check errors. Brand: #35 BOBRES
   the beaver. Docs: this update and CHANGELOG.md. Monitoring is the watchdog and the System page, not Prometheus
   (§6). The legal review needs a lawyer (§2).

## 11. Weak-spot decisions (closed)
- Internal auth: one service token PER SERVICE (v1); mTLS is not built. A server accepts only its legitimate
  callers: core accepts the bot; payments and provisioner accept only core (so a compromised bot cannot reach
  payment approvals or the 3x-ui token holder). The auth layer is one interceptor (`internal/grpcauth`).
- Queue/events: Postgres transactional outbox per service + a pull feed over gRPC (`events.v1.EventFeedService`):
  the producer keeps one cursor per consumer, consumers de-duplicate on a UUID event id and dead-letter events
  they cannot apply. No service reads another service's schema, so payments can run in another region.
  NO NATS yet; a NATS transport can replace Feed/Source without touching handlers. (Decided 2026-10-03 after
  the shared-table relay proved incompatible with per-service DB roles; supersedes LISTEN/NOTIFY.)
- License binding: domain + soft install ID; up to 3 re-activations per year. Built (13-license-server.md).
- Services: 4 separate deployables (bot, core, payments, provisioner) in one Go monorepo, shared module, plus Caddy.
  The `backup` container joined in Phase 4.
- Zarinpal: payments stays a separate deployable so it can run in any region. Verify Zarinpal's server-IP rules
  with their support BEFORE Phase 2. Still unverified: Zarinpal ships off, and the owner asks before turning it on.
- 3x-ui compatibility: adapter for latest v3.x first, version detection with warning; older adapters only on demand.
- Brand: keep BOBRES. Domain, bot username and trademark checks deferred (do before public launch). Still open (§2).

## 12. Dashboard stack (closed)
- Vue 3 + Vite + TypeScript, built to static files and embedded in the `core` binary via `embed.FS`
  (same approach 3x-ui uses). No Node runtime in production, no separate container.
- Needs a JSON API on `core` (versioned, session + CSRF auth). This supersedes the templ/HTMX plan in 04-dashboard.md.
- Theming through CSS variables for white-label; RTL via CSS logical properties; Vazirmatn + Inter fonts; vue-i18n for fa/en.
- UI library: DECIDED in Phase 4: PrimeVue 4.5 (MIT) with the Aura theme. PrimeVue 5 needs a commercial license.
