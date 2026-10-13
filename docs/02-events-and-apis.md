# 02 - Event catalog and service APIs

Services: `bot`, `core`, `payments`, `provisioner` (+ Caddy gateway).
Sync calls: gRPC (internal network only). Async: each service's transactional outbox, pulled by consumers over gRPC (`events.v1.EventFeedService`, one cursor per consumer, kept by the producer; NATS JetStream can replace the transport if load ever needs it). Payload types live in `internal/events`. Contracts in `proto/` (buf). Service auth: one token per service, servers accept only their legitimate callers (mTLS is not built).

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
| wallet.credited.v1 / debited.v1 | core | notifier | Balance change. Reasons on credited: topup, order_not_payable, admin_adjust, referral, refund, commission (a reseller's commission on a customer's order; never name the customer), settlement (staff recorded a reseller's payment) |
| refund.requested.v1 / completed.v1 | core | payments | Refund flow |
| ticket.opened.v1 / customer_replied.v1 / replied.v1 / closed.v1 | core | notifier | Support (ids only; the bot fetches a message with GetTicketMessage and reports delivery) |
| subscription.provisioned.v1 / extended.v1 / reminder.v1 `label` | core | notifier | The name a reseller gave the service (empty for customers), for the bot's texts |
| payment.deposit_unmatched.v1 / deposit_claimed.v1 | payments, then core | notifier(staff alerts) | Phase 6a. USDT reached the store's TRON address but matched no automatic payment (`reason`: no_payment, already_paid, closed, ambiguous), so staff credit it by hand; or a manual USDT proof carries a transaction the watcher had seen, so staff check it before approving. Core passes both on to the bot |
| license.changed.v1 | core | all | Entitlements updated (feature gating) |
| system.alert.v1 | core (watchdog) | notifier(staff alerts) | A check went wrong (`state: problem`), was fixed (`resolved`, once it has passed for 5 minutes), or is still wrong after 6 h and then every 24 h (`problem`, `repeat: true`); a check back within 30 minutes of its fix is announced only if it still fails then. `{key, severity: critical\|warning, state, title_key (the bot's alert.* text), args (strings: count, days, hours, minutes are numbers, free is bytes, date is unix seconds, the rest names), detail, since, at (for resolved: when it first passed again)}`. Sent to `notify.recipients` (never support staff) unless `notify.system_alerts` is false |

## Purchase saga (happy path)
bot -> core.CreateOrder -> `order.created` -> payments creates intent -> user pays -> `payment.succeeded`
-> core marks paid, writes ledger, emits `order.paid` -> provisioner creates 3x-ui client
-> `subscription.provisioned` -> notifier sends link + QR. Wallet payment skips the payments step.

## gRPC APIs (sketch)
- core: `GetUser`, `UpsertUser`, `ListPlans`, `CreateOrder`, `ApplyDiscount`, `GetWallet`, `CreditWallet`, `DebitWallet`, `ListSubscriptions`, `OpenTicket`, entitlement checks `HasFeature`.
- core, resellers (milestone 5, built): `ListPlans.user_id` answers that user's price (a reseller's tier price) with the
  store price in `Plan.list_price`; `CreateOrder.label` and `Subscription.label`; `ListSubscriptions` pages (at most 50,
  `page_info.total`) and searches (`query`: label or id suffix) when asked, else the newest 100 as before;
  `Wallet.credit_limit`; `SetSubscriptionLabel` (the reseller's own services; PERMISSION_DENIED for others, NOT_FOUND
  for a service not theirs); `GetResellerInfo` (tier, discount, effective commission, balances with limit and
  available, customers, commissions per currency, invite code; NOT_FOUND for non-resellers). Discount codes are refused
  for resellers (`discount: reseller`, FAILED_PRECONDITION). Removing a reseller cancels their unpaid orders (paying
  one afterwards is FAILED_PRECONDITION `order not payable`); a `CreateOrder` that priced the plan for a reseller whose
  removal committed first is FAILED_PRECONDITION, and asked again it gets the store price.
- core, payment method switches (milestone 6a, built): `ListPaymentMethods` leaves out Telegram Stars while
  `payments.stars.enabled` is false, and `CreatePaymentIntent` refuses (FAILED_PRECONDITION `this payment method is
  turned off`) a manual method or Stars whose `payments.<method>.enabled` is false. Unset counts as on. Payments
  already started go on; Zarinpal is switched in payments (`ListGateways` lists only the offered ones).
- core, bot photo (P8, built): `Settings.bot_photo_version` is the photo's upload time in unix nanoseconds, `""`
  when the store has none and `"?"` when core could not read it (the bot then neither sets nor removes its photo);
  `GetBotPhoto` returns `{version, jpeg}` (a square JPEG of at most 3 MB) or NOT_FOUND. The bot reads the version
  with every settings refresh and fetches the photo only when it changes. An older core sends no version and
  answers UNIMPLEMENTED; an older bot ignores the field.
- payments: `CreateIntent`, `GetIntent`, `SubmitReceipt`, `ReviewReceipt`, `ListProviders`; HTTP webhooks per provider behind the gateway.
- payments, gateway settings (milestone 6a, built; core's token only): `ListGatewayConfigs` (provider, enabled,
  configured, offered, sandbox, merchant_hint = last 4, proxy_set, key_missing, from_env, problem, updated_at/by;
  never a secret), `SetGatewayConfig` (the whole state; an empty merchant_id or proxy keeps the stored one,
  clear_proxy, actor_id; FAILED_PRECONDITION `<n> open zarinpal payments: ...` when the merchant id or sandbox would
  change under open payments, or without the payments master key), `TestGateway` (Zarinpal's read-only
  `unVerified`, stored or candidate settings, saves nothing; a refusal is `ok=false` with a safe detail),
  `GatewayReport(provider, from, to)` (totals by status, failure reasons, open count and oldest open now, last error
  and last paid).
- provisioner: `CreateClient`, `RenewClient`, `ResetTraffic`, `DeleteClient`, `GetUsage`, `GetLinks`, `HealthCheck`, `ListInbounds`.
- provisioner, servers (milestone 6a, built; core's token only): `CreateClient.candidate_server_ids` (a plan's
  servers; empty = every enabled server; the healthy, enabled candidate with the least clients/weight is picked and
  reserved, so a retry stays on it; FAILED_PRECONDITION when none fits or no server exists), `ListServers` (settings,
  health, version with an untested flag and the tested range, clients, has_token; never the token), `AddServer`,
  `UpdateServer` (the whole state; an empty token keeps the stored one, on the same host only; the connection is
  tested first unless skip_check), `DeleteServer` (FAILED_PRECONDITION while clients exist), `TestServer` (saves
  nothing; a failed connection is `ok=false` with a detail), `ListInbounds` (with `attach`), `SetInboundAttach`.

## Provider interface (payments)
`Init(order) -> redirect/instructions`, `Verify(callback) -> Result`, `Reconcile(period)`, `Capabilities()`.
Implementations: wallet, manual-card, zarinpal, stars, crypto-manual, crypto-thirdparty, crypto-watcher.
Built: wallet, manual-card, crypto-manual (Phase 1); zarinpal, stars (Phase 2, see
superpowers/specs/2026-10-04-phase2-payments-design.md); manual-zarinpal (the owner's Zarinpal
payment link, paid outside the bot); the crypto watcher, `usdt_trc20_auto` (Phase 6a: an exact, unique USDT amount to
the store's TRON address, found through TronGrid every 30 s). Every manual method takes a screenshot that an admin
approves. crypto-thirdparty is not built: the candidate processors exclude Iran. The automated gateways share `internal/payments/gateway` (Create, Check)
and one settlement path (exactly once, amount fixed at creation, reconciler).

## 3x-ui adapter (provisioner)
Interface `PanelAdapter` (add/update/delete client, usage, links, version). First impl: 3x-ui v3.x via Bearer token.
A health loop checks every enabled server each 60 s and reads the panel version (`/panel/api/server/status`);
`ListServers` flags versions outside the tested range (v3.8.5 to v3.9.0, the CI matrix). Per-client mutex because
`clients/update` replaces the row.

## Public HTTP surface (via gateway)
- `/webhooks/<provider>` (signature verified, replay-protected)
- `/sub/<token>` subscription info page (branded)
- `/admin/*` dashboard and `/api/v1/*` its JSON API (session cookie + CSRF token; password logins need TOTP), `/healthz`, `/readyz`.
  Milestone 6a adds `/api/v1/servers` (servers.write: owner and admin) and `/api/v1/gateways` (gateways.write: the
  owner); the table is in superpowers/specs/2026-10-07-phase4-dashboard-design.md, "Milestone 6a".
  P8 adds the HTTPS manager's `/api/v1/system/tls` (below).

## HTTPS manager (P8)
Core never writes Caddy's files or reaches its admin socket. It asks the `tls-agent` (no network) through two small
named volumes, and the agent answers through the second one (07-security.md, "HTTPS manager"):

| Volume | core | tls-agent | File |
|---|---|---|---|
| `tls_request` | `/request` read-write | `/request` read-only | `tls.json`: `{generation, mode, ip, domain, zarinpal_host, cert_pem, key_pem}` |
| `tls_result` | `/tls-result` read-only | `/result` read-write | `result.json`: `{generation, ok, code, error, applied_at}` |

- Both files are replaced atomically (a `.<name>.tmp` file, then a rename). The request is mode 0600 and holds the own
  certificate's private key in clear while manual mode is on; it never goes into a log. The result is 0644 and holds no
  secret.
- `generation` is a new number for every request. The agent reads the request every 2 seconds, applies one whose
  generation differs from the last it answered, and answers with the same number. It checks the request again, writes
  Caddy's files, and forces Caddy to load its Caddyfile.
- `code` is empty when `ok` is true, else `invalid` (refused, nothing written), `write_failed`, `caddy_refused` (`error`
  holds Caddy's words; the previous files were put back), `caddy_unreachable` (Caddy did not answer; the files stay and
  the agent tries the load again every round) or `agent_error`. Core waits up to 20 seconds for an answer, and commits
  the setting only on `ok` (an automatic-mode request is also committed on `caddy_unreachable`).
- `GET /api/v1/system/tls` (system.read) answers the saved setting, what core asks for now, the problem if the saved
  setting is not in use, a live status of every served address (issuer, expiry, trusted, error) and the agent's last
  result, the dashboard's public URL and the detected IP address. `PUT /api/v1/system/tls` (system.write, 10 a minute)
  takes `{mode, ip, cert, key, confirm_ip}`; errors carry a `code` (400 `invalid`, 409 `confirm`, `state` or `refused`,
  503 `unavailable`, which includes `busy`) that the dashboard translates. `POST /api/v1/system/tls/detect-ip` (system.write) asks the bot
  for the public IP address. None of them ever answers a key.
- Alerts (`system.alert.v1` keys): `tls.caddy` (`alert.tls_caddy_down`, critical: Caddy answered on none of the
  addresses for 3 minutes), `tls.<role>.failing` (held 30 minutes) and `tls.<role>.expiring` (not held) for the domain,
  ip and zarinpal addresses, and `tls.setting` (not held) and `tls.agent` (held 5 minutes), both with the title
  `alert.tls_problem`. A check that is not held is raised once it is seen failing twice in a row, a minute apart. While
  Caddy answers nowhere, the per-address checks are left as they were.
- The agent answers `invalid` to a request for a domain or Zarinpal host other than its own `BOBRES_DOMAIN` and
  `BOBRES_ZARINPAL_HOST` (the same values as core's, from compose), and to every request when it has no domain; core
  shows it as `agent_failed`. The status of each address has `reachable` (Caddy answered the dial), and a core that
  finds none reachable raises `tls.caddy`. A change that finds another one being applied answers 503 `busy` after 3
  seconds. The bot's detection of the server's address renews every minute in ip mode and decides, with the
  certificate, whether links use the IP address.

Internal only (never routed by Caddy): the bot's `GET /internal/files/{id}` hands core the receipt
photos customers sent (core's service token; images and PDFs, 10 MB at most).
The bot's `GET /internal/public-ip` (core's service token) tells core this server's public IP address, because core has
no internet access: the bot asks `https://api.ipify.org`, then `https://ifconfig.me/ip` (5 seconds each, no redirects,
no proxy), accepts only a 200 answer of at most 64 bytes that is a public IP address, and answers `{"ip": "..."}`.
Core's `POST /api/v1/system/tls/detect-ip` and the Detect button of the dashboard's HTTPS tab use it.

## Open points
- DECIDED (2026-10-03): outbox + gRPC pull feed with per-consumer cursors (replaces LISTEN/NOTIFY); NATS only if load requires it.
- Implemented (2026-10-12): payments publishes `payment.succeeded.v1` / `payment.rejected.v1` (core owns wallets and
  applies each intent exactly once) and `payment.deposit_unmatched.v1` / `deposit_claimed.v1`. Core publishes for the
  bot: `order.paid.v1`, `wallet.credited.v1`, `payment.rejected.v1`, `subscription.provisioned.v1`, `extended.v1`,
  `provision_failed.v1` and `reminder.v1`, the four `ticket.*.v1`, `system.alert.v1` and the two deposit events.
  The other rows of the catalog are not events: those steps are gRPC calls (orders, payment intents, provisioning,
  the usage sync) or happen inside core (licenses). Expiry and traffic reminders are `subscription.reminder.v1`.
- DECIDED: bot -> core is gRPC (core's CoreService), from Phase 1 on.
