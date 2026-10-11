# 01 - Database schema and state machines (DRAFT)

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
| orders | id, user_id, plan_id, type (new/renew/traffic_topup), status, amount, currency, discount_id, label (a reseller's name for the new service, at most 64), idempotency_key UNIQUE, created_at, updated_at |
| subscriptions | id, user_id, order_id, server_id, client_email UNIQUE, sub_id, status, expires_at, traffic_total_bytes, traffic_used_bytes, last_synced_at, label (copied from the order that created it) |
| discounts | id, code UNIQUE, kind (percent/fixed), value, max_uses, used_count, per_user_limit, valid_from, valid_to, plan_scope |
| referrals | id, referrer_id, referred_id UNIQUE, reward_ledger_id, status |
| reseller_tiers | id, name UNIQUE (1-40), discount_percent (0-90, off the store price), commission_percent (0-50), version |
| reseller_tier_prices | tier_id, plan_id (both ON DELETE CASCADE), price (>= 0, the plan's currency): a fixed price overriding the tier's discount |
| resellers | user_id PK (role `reseller`, changed together), tier_id (RESTRICT: a tier in use cannot be deleted), commission_percent (NULL: the tier's), note (staff only, at most 500), version. Credit limits live on `wallets`; customers are the users whose `referred_by` is the reseller |
| tickets / ticket_messages / ticket_canned | tickets: number, user_id, category, subject, status (open/answered/closed, one not closed per user), assigned_to, timestamps, close reason; messages: kind (customer/staff/note), author, body, Telegram file, delivery of staff replies; saved replies per language |
| audit_log | id, actor_id, action, entity, entity_id (text: a uuid, code or key), before jsonb, after jsonb, reason, ip, source (dashboard/bot/cli/system), created_at; append-only (triggers refuse UPDATE, DELETE, TRUNCATE) |
| assets | name (logo), content_type, data, updated_at: store files kept out of settings |
| settings | key, value jsonb (branding, texts, feature flags, gateway toggles) |
| broadcasts / broadcast_recipients | broadcasts: segment, language, texts, button, schedule, status, counts; recipients: the audience snapshot with each one's status (pending, claimed, sent, blocked, failed, unknown, cancelled). users.bot_blocked_at marks users who blocked the bot |

## Schema `payments`
| Table | Key columns |
|---|---|
| payment_intents | id, order_id, user_id, provider, amount, currency, status, provider_ref, expires_at |
| provider_events | id, provider, provider_event_id, payload jsonb, signature_ok, processed_at; UNIQUE (provider, provider_event_id) |
| manual_receipts | id, intent_id, proof_file, submitted_at, reviewed_by, decision, reason |
| crypto_addresses | id, network, address, derivation_ref, intent_id, seen_txid, confirmations |
| reconciliation_runs | id, provider, period, mismatches jsonb, created_at |

## Schema `provisioner`
| Table | Key columns |
|---|---|
| xui_servers | id, name, base_url, api_token_enc (encrypted at rest), panel_version, enabled, last_health_at |
| provision_jobs | id, subscription_id, action (create/renew/reset/delete/disable), status, attempts, next_run_at, last_error |
| client_map | subscription_id, server_id, email, xui_sub_id, inbound_ids int[] |

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
- Encryption for `api_token_enc`: key from install secret (envelope) - to be specified in 07-security.
- Retention: audit and ledger kept forever; provider_events pruned after N months.
- 3x-ui is source of truth for live traffic/expiry; our `subscriptions` are a synced cache. Conflict rule: 3x-ui wins for usage, we win for entitlement/price.
