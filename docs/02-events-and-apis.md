# 02 - Event catalog and service APIs (DRAFT)

Services: `bot`, `core`, `payments`, `provisioner` (+ Caddy gateway).
Sync calls: gRPC (internal network only). Async: each service's transactional outbox, pulled by consumers over gRPC (`events.v1.EventFeedService`, one cursor per consumer, kept by the producer; NATS JetStream can replace the transport later). Payload types live in `internal/events`. Contracts in `proto/` (buf). Service auth: one token per service, servers accept only their legitimate callers; mTLS later.

## Rules
- Events are past-tense facts, versioned: `order.paid.v1`. Additive changes only within a version.
- Publish via transactional outbox. Consume via inbox de-dup. Handlers must be idempotent.
- Every message: `event_id`, `occurred_at`, `correlation_id`, `actor`, `payload`.
- Retries with exponential backoff, then dead-letter subject + admin alert.

## Event catalog
| Event | Producer | Consumers | Meaning |
|---|---|---|---|
| user.registered.v1 | core | notifier, core(referral) | New Telegram user |
| order.created.v1 | core | payments | Order awaiting payment |
| payment.succeeded.v1 | payments | core | Money confirmed |
| payment.failed.v1 / payment.expired.v1 | payments | core, notifier | Payment did not complete |
| order.paid.v1 | core | provisioner | Provision the service |
| subscription.provision_requested.v1 | core | provisioner | Create/renew/reset command |
| subscription.provisioned.v1 | provisioner | core, notifier | Service ready, carries links |
| subscription.provision_failed.v1 | provisioner | core, notifier(admin) | Needs retry/attention |
| subscription.usage_updated.v1 | provisioner | core | Traffic/expiry sync from 3x-ui |
| subscription.expiring.v1 / expired.v1 | core(scheduler) | notifier | Reminder triggers |
| wallet.credited.v1 / debited.v1 | core | notifier | Balance change |
| refund.requested.v1 / completed.v1 | core | payments | Refund flow |
| ticket.opened.v1 / replied.v1 | core | notifier | Support |
| license.changed.v1 | core | all | Entitlements updated (feature gating) |

## Purchase saga (happy path)
bot -> core.CreateOrder -> `order.created` -> payments creates intent -> user pays -> `payment.succeeded`
-> core marks paid, writes ledger, emits `order.paid` -> provisioner creates 3x-ui client
-> `subscription.provisioned` -> notifier sends link + QR. Wallet payment skips the payments step.

## gRPC APIs (sketch)
- core: `GetUser`, `UpsertUser`, `ListPlans`, `CreateOrder`, `ApplyDiscount`, `GetWallet`, `CreditWallet`, `DebitWallet`, `ListSubscriptions`, `OpenTicket`, entitlement checks `HasFeature`.
- payments: `CreateIntent`, `GetIntent`, `SubmitReceipt`, `ReviewReceipt`, `ListProviders`; HTTP webhooks per provider behind the gateway.
- provisioner: `CreateClient`, `RenewClient`, `ResetTraffic`, `DeleteClient`, `GetUsage`, `GetLinks`, `HealthCheck`, `ListInbounds`.

## Provider interface (payments)
`Init(order) -> redirect/instructions`, `Verify(callback) -> Result`, `Reconcile(period)`, `Capabilities()`.
Implementations: wallet, manual-card, zarinpal, stars, crypto-manual, crypto-thirdparty, crypto-watcher.
Built so far: wallet, manual-card, crypto-manual (Phase 1); zarinpal, stars (Phase 2, see
superpowers/specs/2026-10-04-phase2-payments-design.md); manual-zarinpal (the owner's Zarinpal
payment link, paid outside the bot). Every manual method takes a screenshot that an admin approves. crypto-thirdparty is on hold: the candidate
processors exclude Iran. The automated gateways share `internal/payments/gateway` (Create, Check)
and one settlement path (exactly once, amount fixed at creation, reconciler).

## 3x-ui adapter (provisioner)
Interface `PanelAdapter` (add/update/delete client, usage, links, version). First impl: 3x-ui v3.x via Bearer token.
Startup: detect version, warn outside tested range. Per-client mutex because `clients/update` replaces the row.

## Public HTTP surface (via gateway)
- `/webhooks/<provider>` (signature verified, replay-protected)
- `/sub/<token>` subscription info page (branded)
- `/admin/*` dashboard and `/api/v1/*` its JSON API (session cookie + CSRF token; password logins need TOTP), `/healthz`, `/readyz`

Internal only (never routed by Caddy): the bot's `GET /internal/files/{id}` hands core the receipt
photos customers sent (core's service token; images and PDFs, 10 MB at most).

## Open points
- DECIDED (2026-10-03): outbox + gRPC pull feed with per-consumer cursors (replaces LISTEN/NOTIFY); NATS only if load requires it.
- Implemented so far: payments publishes `payment.succeeded.v1` / `payment.rejected.v1` (core owns wallets and
  applies each intent exactly once); core publishes `order.paid.v1`, `wallet.credited.v1`, `payment.rejected.v1`
  for the bot.
- Whether bot->core is gRPC or shared library in early phases.
