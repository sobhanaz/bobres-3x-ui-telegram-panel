# 01 - Database schema and state machines

One Postgres per install. One schema per service; no cross-schema joins or foreign keys.
Services reference each other by ID only (UUID v7). Migrations: goose, forward-only.

## Money rules (non-negotiable)
- Amounts are `bigint` in minor units + `currency` code. Never float.
- Each currency has a scale (IRT/IRR 0, USDT 6, TON 9, XTR 0). Scale table lives in code + `core.currencies`.
- Wallet balance = SUM of ledger. `wallets.balance` is a cache updated in the same transaction.
- `ledger_entries` is append-only (no UPDATE/DELETE, enforced by trigger + revoked privileges).
- Every money-moving call carries an `idempotency_key` with a UNIQUE index.
- Balance can never go below 0, except a reseller's: `wallets.credit_limit` (per currency, 0 for everyone else) lets
  it go down to `-credit_limit` (CHECK `balance >= -credit_limit`). Debits are refused past the limit under the
  wallet's row lock; credits always go through and lower the debt. Only resellers get a limit (domain rule).

## Schema `core`
| Table | Key columns |
|---|---|
| users | id, telegram_id UNIQUE, username, lang (fa/en), role, status (active/banned), referred_by, created_at |
| wallets | user_id, currency, balance (cache), credit_limit (resellers; balance >= -credit_limit), PRIMARY KEY (user_id, currency) |
| ledger_entries | id, user_id, currency, amount (signed), kind (topup/purchase/refund/adjust/trial_grant/referral/commission/settlement), ref_type, ref_id, idempotency_key UNIQUE, balance_after, created_at. Reseller keys: `commission:<order id>` (ref order), `settlement:<request key>` (ref admin) |
| plans | id, name_i18n jsonb, kind (traffic/time/both), duration_days, traffic_bytes, price, currency, enabled, sort, is_trial |
| plan_servers | plan_id (ON DELETE CASCADE), server_id, PRIMARY KEY (plan_id, server_id): the 3x-ui servers a plan's new services may go to; no row = any server. server_id is an opaque reference to `provisioner.xui_servers` (core never reads that schema). Core refuses to delete a server a plan names; one removed another way stays named until staff take it out |
| orders | id, user_id, plan_id, type (new/renew/traffic_topup), status, amount, currency, discount_id, label (a reseller's name for the new service, at most 64), idempotency_key UNIQUE, created_at, updated_at |
| subscriptions | id, user_id, order_id, server_id, client_email UNIQUE, sub_id, status, expires_at, traffic_total_bytes, traffic_used_bytes, last_synced_at, sub_link (stored link the bot shows), sub_link_stale_at (set when the server's address changed: a worker fetches the link again; cleared only if not marked again meanwhile; a fetch the panel did not answer moves it to the next try), sub_link_attempts (those fetches; after 8 the stored link is kept and the mark cleared; a new mark starts over), label (copied from the order that created it), page_token (32 lowercase hex = 128 random bits, UNIQUE: the secret of the branded page `/sub/<token>`; never derived from another id; regenerated on demand) |
| discounts | id, code UNIQUE, kind (percent/fixed), value, max_uses, used_count, per_user_limit, valid_from, valid_to, plan_scope |
| referrals | id, referrer_id, referred_id UNIQUE, reward_ledger_id, status |
| reseller_tiers | id, name UNIQUE (1-40), discount_percent (0-90, off the store price), commission_percent (0-50), version |
| reseller_tier_prices | tier_id, plan_id (both ON DELETE CASCADE), price (>= 0, the plan's currency): a fixed price overriding the tier's discount |
| resellers | user_id PK (role `reseller`, changed together), tier_id (RESTRICT: a tier in use cannot be deleted), commission_percent (NULL: the tier's), note (staff only, at most 500), version. Credit limits live on `wallets`; customers are the users whose `referred_by` is the reseller |
| tickets / ticket_messages / ticket_canned | tickets: number, user_id, category, subject, status (open/answered/closed, one not closed per user), assigned_to, timestamps, close reason; messages: kind (customer/staff/note), author, body, Telegram file, delivery of staff replies; saved replies per language |
| audit_log | id, actor_id, action, entity, entity_id (text: a uuid, code or key), before jsonb, after jsonb, reason, ip, source (dashboard/bot/cli/system), created_at; append-only (triggers refuse UPDATE, DELETE, TRUNCATE) |
| assets | name (logo, bot_photo), content_type, data, updated_at: store files kept out of settings; bot_photo is the bot's Telegram profile photo, always a square JPEG |
| settings | key, value jsonb (branding, texts, feature flags, payment details and the on/off switches `payments.manual_card.enabled`, `payments.manual_crypto.enabled`, `payments.manual_zarinpal.enabled`, `payments.stars.enabled`; no secret ever, since the bot reads every setting) |
| install / license | install: one random install id, made with the schema (a license may be bound to it), `trial_started_at` (when the implicit trial began: written once, by migration 00017) and `limit_hit_at` (when a new purchase was last refused for the active-services limit); license: one row, the installed token, when and by whom, `source` (manual, activation, refresh) and `refreshed_at` (05-license.md) |
| alert_state | key (one per system check), state (ok/pending/problem), since, last_sent_at, passing_since, detail, severity, title_key, args: the core watchdog's memory, so an alert is sent once and its fix is announced (runbooks/README.md) |
| tls_config | One row (id smallint PRIMARY KEY, CHECK id = 1), seeded as `auto`: mode (auto/ip/manual), ip (ip mode's public address, set only in ip mode), cert_pem (manual mode's certificate chain, at most 64 KiB), key_sealed (its private key, sealed with core's master key, set together with cert_pem and only in manual mode; never stored in clear), updated_at. It holds only a state Caddy accepted: core writes it after the tls-agent says Caddy loaded the change (07-security.md, "HTTPS manager"). Leaving manual mode deletes the pair |
| broadcasts / broadcast_recipients | broadcasts: segment, language, texts, button, schedule, status, counts; recipients: the audience snapshot with each one's status (pending, claimed, sent, blocked, failed, unknown, cancelled). users.bot_blocked_at marks users who blocked the bot |

## Schema `payments`
| Table | Key columns |
|---|---|
| payment_intents | id, order_id, user_id, provider, amount, currency, status, provider_ref, expires_at |
| provider_events | id, provider, provider_event_id, payload jsonb, signature_ok, processed_at; UNIQUE (provider, provider_event_id) |
| manual_receipts | id, intent_id, proof_file, submitted_at, reviewed_by, decision, reason |
| crypto_addresses | id, network, address, derivation_ref, intent_id, seen_txid, confirmations |
| reconciliation_runs | id, provider, period, mismatches jsonb, created_at |
| gateway_configs | provider PK (zarinpal, usdt_trc20_auto, idpay, nextpay: the last two keep their API key in api_key_enc), enabled (offered for new payments), sandbox, merchant_enc, proxy_enc, address_enc (the store's TRC20 address), api_key_enc (TronGrid, optional), all encrypted with the payments master key (the proxy URL can carry a password), updated_at, updated_by (core staff user). Zarinpal is seeded once from the environment when empty, then the table is the source of truth; CHECK: enabled needs a merchant id (Zarinpal) or an address (USDT) |
| gateway_events | id, intent_id, provider, kind (create, callback, webhook, check, precheckout, confirm, scan, match), outcome, detail jsonb, created_at: what each gateway answered, for the report and the runbooks (never card numbers or tokens) |
| chain_transfers | Every incoming USDT transfer the watcher saw, once: txid + event_index UNIQUE, network (TRC20), token, from/to address, value (micro-USDT), block_ts, intent_id, outcome (matched, unmatched, manual, resolved, ignored), reason, resolved_by/at, note, announce_at. A transfer settles at most one intent and an intent at most one transfer. `payment_intents.pay_address` keeps the address an automatic payment was told to use |
| chain_cursors | address PK, last_ts (reads restart 2 minutes before it), scanned_at, ok_at, last_error: how far the watcher read each address and how its last read went |

## Schema `provisioner`
| Table | Key columns |
|---|---|
| xui_servers | id, name UNIQUE, base_url, api_token_enc (encrypted at rest), sub_base_url, allow_private, panel_version, enabled (takes new clients; existing ones keep working), weight (1-1000: the least clients/weight gets a new client), managed (env: seeded from the installer while the table was empty / dashboard), last_health_ok, last_error, last_checked_at (the 60 s health loop; it never moves updated_at, which keys the cached panel clients), last_health_at |
| server_inbounds | server_id (ON DELETE CASCADE), inbound_id, attach: whether new clients join an inbound (no row = attach; the panel's own switch still applies) |
| provision_jobs | id, subscription_id, action (create/renew/reset/delete/disable), status, attempts, next_run_at, last_error |
| client_map | subscription_id, server_id (a server with clients cannot be deleted), email, xui_sub_id, inbound_ids int[], pending (a reservation written before the panel is called, so a retry returns to the same server) |

## Shared plumbing (each schema)
- `outbox` (id, topic, payload jsonb, created_at, published_at) for reliable event publishing.
- `inbox` (message_id UNIQUE) for consumer de-duplication.

## State machines
Order: `created -> awaiting_payment -> paid -> provisioning -> active`
- Failure paths: `awaiting_payment -> expired | cancelled`; `provisioning -> provision_failed -> provisioning` (retry with backoff, alert after N); `paid|active -> refunded`.
- Removing a reseller moves their `created`/`awaiting_payment` orders to `cancelled` (they carry tier prices); a payment that still arrives for one stays in the wallet (`order_not_payable`).
- Pay first, provision after: if 3x-ui is down the customer keeps a paid order and gets it delivered on retry.

Payment intent: `pending -> confirming -> succeeded | failed | expired`; manual: `pending -> review -> succeeded | rejected`.

Subscription: `pending -> active -> expiring_soon -> expired -> deleted`; also `disabled` (admin/abuse), `depleted` (quota used).

## Open points
- UUID v7 vs bigserial (UUID chosen for mergeability across installs).
- Encryption at rest: each service has its own master key (envelope, 07-security): the provisioner's for `api_token_enc`, payments' for `merchant_enc`/`proxy_enc`, core's for dashboard authenticator secrets and for the private key of an own HTTPS certificate (`tls_config.key_sealed`).
- Retention: audit and ledger kept forever; provider_events pruned after N months.
- 3x-ui is source of truth for live traffic/expiry; our `subscriptions` are a synced cache. Conflict rule: 3x-ui wins for usage, we win for entitlement/price.
