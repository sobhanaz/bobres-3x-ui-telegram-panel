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
| order.paid.v1 | core | provisioner, notifier, sales reports | Provision the service. `{order_id, user_id, telegram_id, plan_id, amount, currency, source, provider}`: `source` is wallet, manual, gateway, trial or free; `provider` (optional, additive) is the payments provider that took the money (`zarinpal`, `stars`, `usdt_trc20_auto`, `manual_card`, ...), absent for wallet, trial and free orders and for events from before it was recorded `bulk_id`, `bulk_index` and `bulk_quantity` (optional, additive) are set when the order is one of a bulk purchase: the bulk, its place in it (from 1) and how many orders it has; the bot tells the staff about such a sale once, with the first order |
| subscription.provision_requested.v1 | core | provisioner | Create/renew/reset command |
| subscription.provisioned.v1 | provisioner | core, notifier | Service ready, carries links. `bulk_id` (optional, additive) is set while the service belongs to a bulk purchase whose one message is still to come: the bot sends nothing for it on its own. A service that is ready after that message carries none |
| subscription.provision_failed.v1 | provisioner | core, notifier(admin) | Needs retry/attention. `bulk_id` (optional, additive) names the bulk the order belongs to: the staff are alerted as for any order, the customer is told by the bulk's own message |
| order.bulk_ready.v1 | core (the provisioning worker's loop) | notifier | Every order of a bulk purchase is delivered, or has failed 3 times and is retried in the background. `{bulk_id, user_id, telegram_id, plan_id, quantity, ready_orders (the delivered orders, in their order in the bulk), delayed (the others still retried), amount (the whole bulk), currency, cashback (what the delivered services earned the buyer; absent: none)}`. Announced once per bulk (`core.bulk_orders.delivered_at`, set in the same transaction); a bulk with nothing left to tell (every order refunded) is closed without an event. The bot asks `GetBulkOrder` and sends the links of `ready_orders` in one message (a few, with a text file, when it would be too long) and says which services are still being prepared |
| subscription.usage_updated.v1 | provisioner | core | Traffic/expiry sync from 3x-ui |
| subscription.expiring.v1 / expired.v1 | core(scheduler) | notifier | Reminder triggers |
| wallet.credited.v1 / debited.v1 | core | notifier | Balance change. Reasons on credited: topup, order_not_payable, admin_adjust, referral, refund (with `cashback_taken` when the refund also took back the cashback the order had earned; `balance` is then the balance after both), cashback (a share of a delivered order given back, `order_id`; written in the delivery's transaction, so it follows `subscription.provisioned.v1`/`extended.v1`; the services of a bulk purchase send none, their cashback is in `order.bulk_ready.v1`), commission (a reseller's commission on a customer's order; never name the customer), settlement (staff recorded a reseller's payment), gift_code (the customer applied a gift code: the bot answers in the same chat, so the notifier sends no second message) |
| refund.requested.v1 / completed.v1 | core | payments | Refund flow |
| ticket.opened.v1 / customer_replied.v1 / replied.v1 / closed.v1 | core | notifier | Support (ids only; the bot fetches a message with GetTicketMessage and reports delivery) |
| subscription.provisioned.v1 / extended.v1 / reminder.v1 `label` | core | notifier | The name a reseller gave the service (empty for customers), for the bot's texts |
| payment.deposit_unmatched.v1 / deposit_claimed.v1 | payments, then core | notifier(staff alerts) | Phase 6a. USDT reached the store's TRON address but matched no automatic payment (`reason`: no_payment, already_paid, closed, ambiguous), so staff credit it by hand; or a manual USDT proof carries a transaction the watcher had seen, so staff check it before approving. Core passes both on to the bot |
| license.changed.v1 | core | all | Entitlements updated (feature gating) |
| license.order_settled.v1 | core (the vendor workers) | notifier | A license the owner's store sells by itself is settled: bought after the buyer's top-up landed (`status: done`) or refused and refunded (`status: refunded`). `{order_id, user_id, telegram_id, kind (new\|renew), tier, months, status, license_id, ends_at}`, no key inside: the bot fetches the key with `GetVendorLicenseKey`. Only on a store with `private_features` |
| reward.code.v1 | core (provisioning, in the transaction that marks a purchase done) | notifier | A reward code for the buyer's next purchase of any plan: `{order_id, user_id, telegram_id, code, percent, amount, currency, days, text (lang -> the owner's text), plan_id, plan_names (lang -> name of the plan that earned it), expires_at}`; `percent` or `amount` (minor units of `currency`) is set, not both. Queued with the code, once per purchase |
| report.daily_summary.v1 | core (daily summary worker) | notifier(staff) | Yesterday's sales (one store-local day) for **one** staff member who holds `reports.read` and has a Telegram account; core writes one event per such member, in the transaction that claims the day (`core.daily_summary_claims`), so each recipient's blocking, refusal or failure is their own (a blocked bot is skipped; a refusal no retry changes, a 4xx other than a blocked bot, a flood limit or a refused token, is dead-lettered at once; any other failure is retried, and since a feed is applied in order the events behind it wait for it until it is sent or dead-lettered), and a member who lost the permission before the claim gets none (the bot also drops the event when it knows the person is no longer an owner or admin, and when it is sent more than a day after it was queued). Queued exactly once, delivered at least once. Only when the store turned `reports.daily_summary` on. `{telegram_id, language (fa\|en, the member's own), date ("2026-10-13"), zone (IANA), new_customers, trials, currencies: [{currency, revenue, prev_revenue (the day before, for the comparison), orders (all sales), new_orders, renewals, traffic_topups, licenses: {count, amount}, digital: {count, amount} (the part of orders and revenue the licenses and digital accounts made; absent in an older event), refunds: {count, amount}, net, topups: {count, amount}, rejected: {count, amount} (by staff), gateway_failed: {count, amount}, cashback: {count, amount} (the cashback given that day less what refunds took back; absent in an older event)}]}`. Amounts are integers in the currency's smallest unit and are never added across currencies; `currencies` lists only those with something to report (none: a quiet day, which the bot says in one line). No names, customers, plans or order ids ([14-reports.md](14-reports.md#the-daily-summary-in-telegram)) |
| system.alert.v1 | core (watchdog) | notifier(staff alerts) | A check went wrong (`state: problem`), was fixed (`resolved`, once it has passed for 5 minutes), or is still wrong after 6 h and then every 24 h (`problem`, `repeat: true`; the notice `license.limit` is sent at most every 24 h, has no 6-hour reminder and no `resolved`); a check back within 30 minutes of its fix is announced only if it still fails then. `{key, severity: critical\|warning, state, title_key (the bot's alert.* text), args (strings: count, days, hours, minutes, limit are numbers, free is bytes, date is unix seconds, the rest names; name of `license.limit` is the limited thing, `active_services`), detail, since, at (for resolved: when it first passed again)}`. Sent to `notify.recipients` (never support staff) unless `notify.system_alerts` is false |
| system.update.v1 | core | notifier(owners) | An update asked for in the dashboard (System > Updates > Update now) or its end, one event per owner with a Telegram account, written in the transaction that records the request or its end. `{telegram_id, language, state: requested\|done\|failed\|rolled_back\|not_started, from, to, channel, by, at (unix seconds), message?}`; `message` is the updater's own short sentence for an end that is not `done` (outside text: the bot escapes it) |
| system.backup.v1 | core | notifier(owners) | A backup downloaded from the dashboard (System > Backups), or a restore of one asked for there and its end; one event per owner with a Telegram account, written in the transaction that records it (and, for the end of a restore that replaced the database that held the request, in the one that records the end again). `{telegram_id, language, kind: download\|restore, state: downloaded (download) \| requested\|done\|failed\|rolled_back\|not_started (restore), file, source?: backup\|upload, by, at (unix seconds), size? (a download's), message?}`; `file` and `message` are outside text (the bot escapes them) |
| sales.daily_summary.v1 | core (daily-summary worker) | notifier(staff alerts) | Yesterday's sales, once a day after `notify.daily_summary_hour` when `notify.daily_summary` is on ([14-reports.md](14-reports.md)). `{date (the local day), tz, new_customers, trials, currencies: [{currency, revenue, net, orders, new_orders, renewals, refunds (count), refund_amount, topups (count), topup_amount}]}`, money in minor units, one entry per currency, never added together; an empty day has no currencies. Sent to `notify.recipients` (never support staff) |

## Purchase saga (happy path)
bot -> core.CreateOrder -> `order.created` -> payments creates intent -> user pays -> `payment.succeeded`
-> core marks paid, writes ledger, emits `order.paid` -> provisioner creates 3x-ui client
-> `subscription.provisioned` -> notifier sends link + QR. Wallet payment skips the payments step.

## gRPC APIs (sketch)
- core: `GetUser`, `UpsertUser`, `ListPlans`, `CreateOrder`, `ApplyDiscount`, `GetWallet`, `CreditWallet`, `DebitWallet`, `ListSubscriptions`, `OpenTicket`, entitlement checks `HasFeature`.
- core, gift codes (additive; `buf breaking` stays clean): `RedeemGiftCode(user_id, code, idempotency_key)`, called by the
  bot only. Everything a customer can get wrong is an **outcome** of the answer, not an error: `REDEEMED` (with
  `credited`, `balance` and `replayed`), `INVALID` (unknown, switched off, expired or not a code: one answer for all, so
  nothing tells a guesser which codes exist), `USED_UP` and `ALREADY_REDEEMED` (told only to someone who sent the whole
  code; the customer's own count is checked first), `LOCKED` (`retry_after_seconds`) and `OFF` (the owner's switch
  `gift_codes.enabled`, or no master key). Errors stay for a broken request, a banned customer (PERMISSION_DENIED) and
  a key that already redeemed another code (`idempotency conflict`). It is one transaction that locks the code's row,
  checks it, credits the wallet (ledger kind `gift_code`, key `gift:<code id>:<user id>:<n>`), records the redemption,
  counts it, writes the audit entry `gift_code.redeem` and the `wallet.credited.v1` event (reason `gift_code`). Sent
  again with the same `idempotency_key` it credits nothing more and answers the same with `replayed`. Five wrong codes
  in an hour close entering codes for an hour (`gift_code.lock` in the audit log); a code is never logged or audited,
  only its last four characters.
- core, bulk purchase (built 2026-10-20, additive; [03-bot-ux.md](03-bot-ux.md), "Bulk purchase"): `QuoteBulkOrder`
  (user, plan, quantity: the unit price, the total and the most the store allows now; it fails as `CreateBulkOrder` would for what
  can be known beforehand, the license's places included), `CreateBulkOrder` (user, plan, quantity, `idempotency_key`,
  optional `label_prefix` and `expected_total`) and `GetBulkOrder` (bulk id, optional user id: the buyer's own bulk or
  NOT_FOUND). `CreateBulkOrder` makes N ordinary orders and pays each from the wallet in ONE transaction (all or nothing,
  audited as `order.bulk` made in the bot) and answers the `BulkOrder` with a `BulkService` per order (order status, then
  service id, status, name and link once delivered); a repeat with the same key answers the first purchase with
  `replayed = true` and charges nothing. Errors: FAILED_PRECONDITION with "insufficient" (the wallet, or a reseller's balance plus
  credit limit, cannot pay for all), "price changed" (`expected_total` is not what the price came to), "turned off"
  (`bulk.enabled` is false), the neutral "new purchases are paused" (the license's limit for N services at once, or a reseller
  without the feature) or "plan unavailable" (a free, switched-off or trial plan); INVALID_ARGUMENT for a quantity outside 2 to
  `bulk.max_quantity`, a name too long for "<name> <n>" or a missing key; PERMISSION_DENIED for a banned buyer;
  ALREADY_EXISTS when the key belongs to another purchase; NOT_FOUND for an unknown plan, buyer or bulk.
- core, resellers (milestone 5, built): `ListPlans.user_id` answers that user's price (a reseller's tier price) with the
  store price in `Plan.list_price`; `CreateOrder.label` and `Subscription.label`; `ListSubscriptions` pages (at most 50,
  `page_info.total`) and searches (`query`: label or id suffix) when asked, else the newest 100 as before;
  `Wallet.credit_limit`; `SetSubscriptionLabel` (the reseller's own services; PERMISSION_DENIED for others, NOT_FOUND
  for a service not theirs); `GetResellerInfo` (tier, discount, effective commission, balances with limit and
  available, customers, commissions per currency, invite code; NOT_FOUND for non-resellers). Discount codes are refused
  for resellers (`discount: reseller`, FAILED_PRECONDITION). Removing a reseller cancels their unpaid orders (paying
  one afterwards is FAILED_PRECONDITION `order not payable`); a `CreateOrder` that priced the plan for a reseller whose
  removal committed first is FAILED_PRECONDITION, and asked again it gets the store price.
- core, reseller management (built, additive; the proto stays compatible): a reseller's own services and customers.
  `ListResellerServices` (the reseller's services, newest first, paged at most 50 and searched like `ListSubscriptions`,
  narrowed by `status` = active, expiring_soon, expired (time or traffic ran out), paused or pending, with the counts
  of every group in `counts`, whatever the filter, and each service's usage, link and `note`);
  `SetResellerServiceEnabled` (pause or resume: the same panel call, checks and audit entry as staff's, the reseller
  as the actor, source `bot`; repeating it changes nothing; a service support paused is FAILED_PRECONDITION "paused by
  support"); `DeleteResellerService` (like staff's delete, no refund, idempotent, FAILED_PRECONDITION while the service
  is being delivered or has a renewal being applied); `SetSubscriptionNote` (one line, at most 200 characters, empty
  clears); `ListInvitedCustomers` (the users who joined through the reseller's link, newest first, paged at most 50:
  `telegram_id`, `username`, `joined_at`, `paid_orders`, `spent` and `commission` per currency, nothing else);
  `Subscription.note`. Each call is the reseller's own: PERMISSION_DENIED for a customer, staff, a former reseller, a
  reseller after `resellers` left the license ("domain: forbidden") and a banned one ("user is banned"); NOT_FOUND for
  an unknown user or a service that is not theirs; INVALID_ARGUMENT for an unknown status or a note or search that is
  too long. Over the Mini App the same functions answer `/api/mini/v1/reseller/...` ([17-mini-app.md](17-mini-app.md)).
- core, growth tools (built after v0.6.1, additive; the proto stays compatible): `GetTopInviters` (a page of the board of
  the customers who invited the most people: request `user_id` (the viewer), `page` from 1, `page_size` (0 = 5, at most 10);
  answer `enabled` (false while `referral.reward_percent` is 0 or `growth.leaderboard.enabled` is false, nothing else
  set), `entries` of `{rank, name, telegram_id, invited, you}`, `page`, `page_size`, `total` (at most 50), `more`,
  `your_invited` (every user who joined through the viewer's link, as `GetReferralInfo.invited`) and `your_rank` (0: not on
  the board); NOT_FOUND for an unknown user). Core masks `name` and `telegram_id` itself (the first and last three
  characters around `***`; at least three always hidden; `***` for no username), so no answer holds a whole one. Only
  active users with the plain `user` role are ranked, a tie goes to whoever joined first, and the board is kept for 30
  seconds (`domain.Config.LeaderboardTTL`). `Settings.resellers_enabled` is true while the license in effect includes
  resellers (a license, its grace, the trial; the free tier does not), for the bot's Become a reseller button; an older
  core never sets it, so the button stays hidden. `AddTicketMessage` with `category` `reseller` files a reseller
  application: a new ticket only (never an answer in another: `ticket_id` or `reply_to_tg_message_id` is INVALID_ARGUMENT),
  from an active customer (a reseller, staff: FAILED_PRECONDITION "only a customer can apply to become a reseller"; banned:
  PERMISSION_DENIED), while resellers are in the license (FAILED_PRECONDITION, the staff-facing refusal), and with no other
  ticket open (FAILED_PRECONDITION "a reseller application is open already" or "another support conversation is open": the
  bot reads them). `Ticket.category` can be `reseller`; the topics a customer picks from stay payment, service and other
  (`domain.SupportCategories`, which is also what the Mini App lists).
- core, required chats (built, additive; the proto stays compatible): `Settings.join_chats` (field 8, `repeated JoinChat
  {chat, link, label}`) are the **extra** channels and groups customers must join, after the first one (the settings
  `join.channel`, `join.link` and the new `join.label`, which stay in `Settings.values`), in the order they are asked and
  already limited to what the license in effect allows (`max_join_chats`: the free tier and Standard 1, so none here;
  Pro and the trial any number). It is empty while `join.channel` is empty (the requirement is off) and for an older
  core; an older bot ignores it and holds customers to the first chat only. `AdminSetSetting` takes `join.label` like
  the other settings. The extras are saved by the dashboard (`GET` and `PUT /api/v1/join-chats`, [04-dashboard.md](04-dashboard.md))
  and live in the `core.join_chats` table (migration 00029); nothing else of the bot-core contract changed, and the
  bot's `POST /internal/miniapp/channel-member` and `GET /internal/channel-check` take any channel or group, a call each
  ([17-mini-app.md](17-mini-app.md)). The channel check accepts a group or supergroup as well as a channel. The
  license's token has a new field `max_join_chats` ([05-license.md](05-license.md)), and `GET /api/v1/license` lists
  it among the entitlements with the chats saved as its use.
- core, payment method switches (milestone 6a, built): `ListPaymentMethods` leaves out Telegram Stars while
  `payments.stars.enabled` is false, and `CreatePaymentIntent` refuses (FAILED_PRECONDITION `this payment method is
  turned off`) a manual method or Stars whose `payments.<method>.enabled` is false. Unset counts as on. Payments
  already started go on; Zarinpal is switched in payments (`ListGateways` lists only the offered ones).
- core, bot photo (P8, built): `Settings.bot_photo_version` is the photo's upload time in unix nanoseconds, `""`
  when the store has none and `"?"` when core could not read it (the bot then neither sets nor removes its photo);
  `GetBotPhoto` returns `{version, jpeg}` (a square JPEG of at most 3 MB) or NOT_FOUND. The bot reads the version
  with every settings refresh and fetches the photo only when it changes. An older core sends no version and
  answers UNIMPLEMENTED; an older bot ignores the field.
- core, help content (built, additive): `ListHelpEntries(user_id, kind, language)` returns the enabled FAQ questions or
  tutorials in the owner's order, each in the customer's language (the other one where that translation is missing,
  `HelpEntry.language` says which), and `GetHelpEntry(user_id, id, language)` one of them; a banned customer is
  PERMISSION_DENIED, an unknown one NOT_FOUND, and an entry that is gone or switched off NOT_FOUND. Titles are plain
  text, bodies Telegram's HTML subset checked by core, tutorial buttons https only. `Settings.help_faq` and
  `help_tutorials` say whether there is anything to show (false from an older core, which answers UNIMPLEMENTED: the
  bot then shows no help buttons). The owner writes the entries over the dashboard's `/api/v1/help` endpoints
  ([04-dashboard.md](04-dashboard.md)); each change is audited and tells the bot to reload its settings.
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
superpowers/specs/2026-10-04-phase2-payments-design.md); idpay, nextpay (see
[16-idpay-nextpay.md](16-idpay-nextpay.md)); manual-zarinpal (the owner's Zarinpal
payment link, paid outside the bot); the crypto watcher, `usdt_trc20_auto` (Phase 6a: an exact, unique USDT amount to
the store's TRON address, found through TronGrid every 30 s). Every manual method takes a screenshot that an admin
approves. crypto-thirdparty is not built: the candidate processors exclude Iran. The automated gateways share `internal/payments/gateway` (Create, Check)
and one settlement path (exactly once, amount fixed at creation, reconciler).

## 3x-ui adapter (provisioner)
Interface `PanelAdapter` (add/update/delete client, usage, links, version). First impl: 3x-ui v3.x via Bearer token.
A health loop checks every enabled server each 60 s and reads the panel version (`/panel/api/server/status`);
`ListServers` flags versions outside the tested range (v3.8.5 to v3.9.0, the CI matrix). Per-client mutex because
`clients/update` replaces the row.

**Client groups `BOBRES` and `BOBRES-<NAME>`.** 3x-ui (v3.8.5 to v3.10.2) has client groups, and every client the 3x-ui adapter
creates is put in a group, so an operator can filter on what the bot made in the panel's Clients page: `xui.BotGroup`, `BOBRES`
(called `bobres_bot` up to v0.11.0, `xui.LegacyBotGroup`), for a customer's client, and `BOBRES-<NAME>` for a reseller who has a
group name (the owner writes NAME when making the reseller: capitals, digits and `_`, 2 to 24; unique). A client is in ONE group,
so a reseller's clients are in their own group and not in `BOBRES`. Core sends the group with `CreateClient` (`group`, "" is the
default); the provisioner keeps it in `client_map.panel_group` and makes the client in it. It is a label only: links, limits and
the subscription do not depend on it. Marzban 0.8 has no groups; its adapter ignores them. A reseller's configs are also **named**
`<prefix>-u<telegram id>-<8 random>` when the reseller has a config prefix (chosen once by the reseller in the bot or the Mini App,
2 to 10 of a-z and 0-9, unique, not the look of `u123`; afterwards only the owner changes it): the name is made once, when the
service is created, and stays through a later change. The group backfill (`RunGroupBackfill`, at start, when a server is added or
answers again, and whenever core calls `SetClientGroups`) converges a 3x-ui panel to each client's group: it moves a client only when
its panel group is one BOBRES owns (none, `BOBRES`, `bobres_bot` or any `BOBRES-...`, `xui.IsBotGroup`), so a group an operator chose
is never touched, and a pass that began before a `SetClientGroups` never records the server as done (`group_reset_at`). Core hands the
provisioner a reseller's existing clients (`ResellerGroupSync`, a worker that looks every minute) after the owner gives or changes the
group name, or asks "Move the existing configs". What the code relies on, as the real panels do it (the adapter's integration tests assert
each on both versions, in CI's "3x-ui integration" job):
- `clients/add` stores `client.group` (the OpenAPI text for it does not list the field). A create of a client the panel
  already has under the same subscription id, on the inbounds it is on, succeeds and changes nothing, the group
  included; under another subscription id it is refused.
- `clients/update` replaces the row and writes the group of its body, so a body without one **clears it**. `SetLimits` and
  `SetEnabled` read the stored row and send it back whole, group included. `clients/bulkAdjust` (renewal) and
  `resetTraffic` do not touch it. Attach, detach and delete leave the other clients' groups alone.
- `clients/groups/bulkAdd` sets the group of the listed clients **whatever group they have**, ignores an email it does not
  know and counts only the clients that changed. `xui.Client.AddToGroup` sends at most 200 emails a call and holds the
  clients' locks, so a tagging cannot fall between the read and the write of an update. Callers choose whom to send: the
  backfill sends only clients that have no group, so a group an operator chose is respected.
- `clients/list/paged` (200 rows a page at most) is the listing used (`xui.Client.ListClients`); `clients/list` is about 750
  bytes a client and would pass the 8 MiB response limit at around 11,000 clients.

**Backfill.** The clients made before the group existed are put in it by the provisioner (`server.RunGroupBackfill`, started
by `cmd/provisioner`). For each 3x-ui server (`kind = 'xui'`, switched on or off) whose `group_backfilled_at` is NULL it
reads the client map of that server (reservations included), lists the panel's clients, and calls `bulkAdd` with the ones
that are held, carry the derived subscription id the map has for them (it is the proof that the client is ours and not a
stranger's under the same email) and have **no** group. It lists again after tagging and records the server only when
a listing finds nothing left to tag (the paged listing can skip a client when one is deleted meanwhile). It runs at
start, when a server is added or its connection is edited, when the health loop sees a server answer again, and every
5 minutes for a server that is not recorded yet (a panel that is down, or too old to have groups: the failure is logged
once per distinct reason). A server that holds no client of ours is recorded without calling the panel. The log line
`3x-ui groups: backfill finished` has `managed`, `tagged`, `already_in_a_group` and `missing`. A retried create (the
reservation was written by an earlier attempt) checks the client's group once more, for an attempt made before the group
existed.

## Public HTTP surface (via gateway)
- `/webhooks/<provider>` (signature verified, replay-protected)
- `/sub/<token>` subscription info page (branded; server-rendered, no script, one fixed 404 for anything unknown; see 15-subscription-page.md). `POST /api/v1/services/{id}/page-token/regenerate` (services.write) gives a service a new address
- `/admin/*` dashboard and `/api/v1/*` its JSON API (session cookie + CSRF token; password logins need TOTP), `/healthz`, `/readyz`.
  Milestone 6a adds `/api/v1/servers` (servers.write: owner and admin) and `/api/v1/gateways` (gateways.write: the
  owner); the table is in superpowers/specs/2026-10-07-phase4-dashboard-design.md, "Milestone 6a".
  P8 adds the HTTPS manager's `/api/v1/system/tls` (below).
  Gift codes (discounts.write): `GET /api/v1/gift-codes?status=&q=&page=&size=` (newest first; besides the page it says
  `enabled`, the owner's switch, and `available`, whether core has the master key to make codes), `POST /api/v1/gift-codes`
  (`{code}` for one code of the owner's own words, or `{count, prefix}` for a batch of generated ones, with
  `{amount, currency, max_redemptions, per_customer_limit, expires_at, note}`; the answer holds the codes and is the
  only place they can ever be read), `GET /api/v1/gift-codes.csv`, `PUT /api/v1/gift-codes/{id}/enabled` (nothing paid is
  taken back), `GET /api/v1/gift-codes/{id}/redemptions` and `.../redemptions.csv`. Audit actions: `gift_code.create`,
  `gift_code.enable`, `gift_code.disable`, `gift_code.redeem`, `gift_code.lock`, and the CSV exports like the other exports.
  Sales reports (reports.read: owner and admin; [14-reports.md](14-reports.md)) are `GET`s only: `/api/v1/reports/summary`,
  `daily-summary`, `plans`, `methods`, the CSV files `summary.csv` and `orders.csv`, and two read-only answers for the
  dashboard's charts: `/api/v1/reports/plan-series` (revenue and orders per plan per bucket of the summary's own buckets,
  per currency, the best 20 plans one by one and the rest as one row with an empty `plan_id`) and `/api/v1/reports/hours`
  (paid orders and revenue by ISO weekday and local hour, per currency). They take the summary's `range` or `from`/`to`,
  `tz`, and for the first also `group` and `calendar`; they write nothing, add no table and change no event or CSV file.

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

**The live USDT price.** Core has no internet access, so every two minutes (while automatic rates are on and an USDT address is set) it asks the bot at
`POST /internal/usdt-rate` (core's service token, no body); the bot reads two fixed public price pages (Tetherland's currencies list and Wallex's USDT/Toman
order book) at once, drops what is not a number between 1,000 and 100,000,000 Toman, refuses the reading when the sources are more than 3% apart (one of them is wrong), and answers `{"rate": <Toman per USDT, the median>, "sources": [...],
"at": <unix>}`, or 502 `{"error", "message"}` when none answered. Core keeps the last answer as the internal state `state.usdt_rate.live` and uses it while it
is at most 30 minutes old: the rate every conversion of a Toman price into USDT uses (`Service.UsdtRate`: the live price plus the margin
`payments.usdt_rate_margin` percent, rounded up; or `payments.usdt_rate` when `payments.usdt_rate_auto` is off or the live price is old). The bot's
`GetSettings` carries that rate as `payments.usdt_rate`, so the amounts it shows are the ones core charges. Dashboard: `GET /api/v1/usdt-rate` and
`POST /api/v1/usdt-rate/refresh` (the Gateways page's "Price of one USDT" card).
no internet access: the bot asks `https://api.ipify.org`, then `https://ifconfig.me/ip` (5 seconds each, no redirects,
no proxy), accepts only a 200 answer of at most 64 bytes that is a public IP address, and answers `{"ip": "..."}`.
Core's `POST /api/v1/system/tls/detect-ip` and the Detect button of the dashboard's HTTPS tab use it.

## Updating from the dashboard
Core never holds the Docker socket. System > Updates asks the server's updater (a root systemd path unit on the host,
`bobres-updater.path`, that runs `bobres update --from-request`) through one folder, `<install>/run/update` on the host,
which core sees as `/run/bobres-update` (`BOBRES_UPDATE_DIR`). Protocol version 1:

| File | core | updater | Content |
|---|---|---|---|
| `request.json` | writes (a `.request.json.tmp` file, then a rename; mode 0640; never through a link) | reads, runs, removes | `{v: 1, id, action: "update", channel: stable\|beta, version, by, at}`, one line of at most 2048 bytes |
| `status.json` | reads (no link followed, at most 64 KB, every field cut and checked) | writes (atomic) | `{v: 1, installed, helper, state: idle\|running\|done\|failed\|rolled_back, id, from, to, channel, step: verify\|backup\|pull\|restart\|health\|rollback\|done, message, started_at, finished_at, log[]}` |

- `id` is a lower-case UUID (core uses version 7); `version` is "" for the newest of the channel, else a release number
  `X.Y.Z` or `X.Y.Z-label.N` (at most 64 characters, no leading `v`; core always names the exact release the owner
  confirmed, never "", so a release that appears meanwhile is not installed unasked); `by` is the dashboard user, one line of at most 64
  characters; `at` is RFC 3339 in UTC. The updater accepts a request only from the owner uid 65532, up to 15 minutes old
  and no more than a minute in the future, and runs an id once. It verifies the release's signature itself: nothing in
  the request is trusted.
- Core keeps one row per request (`core.system_updates`, migration 00025) written before the file, so no request exists
  without its row, audit entry (`system.update.requested`, and `system.update.done|failed|rolled_back|not_started` at the
  end) and `system.update.v1` notices. At most one update is open at a time. It ends the row exactly once, when the
  updater's status for that id says so, or as `not_started` when the updater never picked the request up. Core itself
  restarts in the middle of an update, so the end is found again from the database and the file after the restart.
  A status that says `running` but that the updater has not written for 10 minutes is an update that was cut off (the
  server lost power, the helper was killed): core ends it as `failed`, tells the owners, and lets another be asked for.
- Dashboard API (`/api/v1/system/updates...`; 503 `not_configured` when core has no update service):
  - `GET /system/updates` (system.read): `{version, check, channel, helper, run, last, apply}`: the bot's last check
    (with `prerelease` and `notes_url` that core builds), the channel (`setting`, `effective`, `source`), the updater
    (`available` or the `problem`: `no_dir`, `no_status`, `not_installed`, `bad_status`, `unsupported`), the open update
    and the last ended one, and whether an update would be accepted now (`apply.ready`, else `apply.reason`). The
    updater's log lines go to the owner only.
  - `GET /system/updates/progress` (system.read): `{version, helper, run, last}` only, from the folder and the database
    (the bot is not asked), polled while an update runs and while core restarts.
  - `POST /system/updates/check` (system.write, 10 a minute): the bot reloads its settings, looks at GitHub now and
    answers; the state comes back with the fresh check (a failed look says why in `check.error` and `check.detail`).
  - `PUT /system/updates/channel` `{channel}` (system.write): `stable`, `beta`, or "" for the server's setting; a change
    makes the bot look again at once.
  - `POST /system/updates/apply` `{version, channel, confirm, accept_prerelease, code}` (system.write, 10 a minute, CSRF):
    nothing happens unless `confirm` is true and `version` is the release the page showed (the newest the bot has
    verified). Answers 202 with the progress state, or `{error, code, message}`: 400 `invalid` (`confirm_required`,
    `prerelease_unconfirmed`), 403 `code_required` and 400 `code_wrong` (the authenticator step-up), 409 `state`
    (`helper_missing`, `already_running`, `no_release`, `not_newer`, `version_changed`, `channel_changed`, `dev_build`),
    503 `unavailable` (`check_unavailable`, `request_failed`).
- The bot's `POST /internal/update-check/refresh` (core's service token) reloads its settings, looks now and answers
  with the check as `GET /internal/update-check` does; core waits at most 25 seconds.

## Downloading and restoring backups from the dashboard
System > Backups (the owner's; 07-security.md has the threat model, 06-installer.md "Restoring from the dashboard" the helper's
rules). Core never holds the Docker socket: a download is core copying a file from the backup service, a restore is a second
action in the folder protocol above.

- Backup service (internal, core's token for it only): `GET /internal/backups/file?name=bobres-<time>[-tag].bkp` streams the
  file as it is on disk (`Content-Length`, no time limit but one hour of writing); a name that is not a backup file name is
  400, a file that is missing, a link or not a plain file is 404.
- `request.json`, action `restore` (all ten fields, no others): `{v: 1, id, action: "restore", source: backup\|upload,
  file, upload, sha256, size, by, at}`. `source: backup`: `file` is a backup file name, `upload` is "". `source: upload`:
  `upload` is a lower-case UUID, the file is `run/update/upload/<upload>.bkp` (written by core: mode 0600, a file of its
  own, `O_EXCL`, no link, at most 2 GiB), and `file` is the name it had (one plain line of at most 120 characters, never a
  path). `sha256` is 64 lower-case hex digits, `size` 1 to 2147483648; the helper copies the file into one of its own while
  hashing and refuses a difference. `id`, `by` and `at` are as for an update.
- `status.json` gains `action` (`update` \| `restore`), `restore` (true: this helper can restore), `subject` (the backup),
  `source`, `by`, `requested_at`; the restore's `step` is `verify`, `safety`, `stop`, `restore`, `start`, `health`,
  `rollback` or `done`. A status older than this protocol has no `restore`: core then refuses to ask (`restore_unsupported`).
- Core keeps a restore in the same table (`core.system_updates`, migration 00031: `action`, `subject`, `source`; `channel`
  is "" for a restore), so one update or restore is open at a time. The restore replaces this database, which holds no row
  for the request: when core finds nothing open it records a finished restore of the last 3 hours from the status
  (`action: restore`, an id, `subject`, `source`, `requested_at`, `finished_at`) as a row that is born ended, with its
  audit entry (`system.restore.<outcome>`, by the system) and notices, once (the id is the key).
- Dashboard API:
  - `GET /system/restore` (system.read): `{version, helper, run, last, other, can, domain, upload, limits}`; from the folder
    and the database only (polled while a restore runs and while core restarts). `run` is the open restore (with `phase`:
    `queued`, `running`, `done`, `failed`, `rolled_back`), `last` the restore that ended last, `other` is `"update"` while
    an update is open, `can` is `{allowed, ready, reason}` (`no_domain`, `helper_missing`, `restore_unsupported`,
    `already_running`), `domain` and `upload` (`{id, name, size, sha256, at, expires_at}`) are the owner's, `limits` is
    `{max_upload, staged_seconds}`.
  - `POST /system/restore/upload?name=<file name>` (system.write, 5 an hour, CSRF): the raw body, `Content-Type:
    application/octet-stream` (415 otherwise), at most 2 GiB (413 `upload_too_large`), not empty (400 `upload_empty`), one
    at a time (409 `upload_busy`); 201 `{upload}`. Nothing is read into memory.
  - `POST /system/restore` `{backup\|upload_id, confirm, acknowledge, code}` (system.write, 6 an hour, CSRF): exactly one
    of `backup` (a file the backup service lists) and `upload_id`; `confirm` is the store's domain (case and spaces do not
    matter) and `acknowledge` must be true; an owner with an authenticator app gives `code` (403 `code_required`, 400
    `code_wrong`, checked after the cheap checks so a typing mistake does not spend a code). 202 with the state as GET gives
    it, or `{error, code, message}`: 400 `invalid` (`ack_required`, `confirm_wrong`), 404 `not_found` (`backup_unknown`,
    `upload_missing`), 409 `state` (`helper_missing`, `restore_unsupported`, `already_running`, `no_domain`), 503
    `unavailable` (`request_failed`).
  - `POST /system/backups/download-ticket` `{file, code}` (system.write, 10 an hour, CSRF) answers `{url, expires_in}`: a
    single-use address (60 seconds, for this session's user, in memory) for `GET /system/backups/download/{ticket}`
    (system.write), which streams the file with `Content-Disposition: attachment`. The audit entry
    (`system.backup.download`) and the notices are written before the first byte; if that fails, 500 and nothing is sent.
- Events: `system.backup.v1` (below); a restore's request and end are also audited as `system.restore.*`.

## Dashboard updates: the server's updater
Core cannot update the store itself (no Docker socket, no internet access). The dashboard's Updates tab asks a helper
on the host through one host directory, `<install dir>/run/update` (core sees it as `/run/bobres-update`, read-write;
owner uid 65532, mode 0700, the only part of the host core can write; `deploy/docker-compose.yml`), and two small files
in it, protocol version 1 (06-installer.md, "Updating from the dashboard", has the rules; 07-security.md the threat model):

| File | Written by | Content |
|---|---|---|
| `request.json` | core: a temp file in the same folder, then a rename; at most 2 KiB | `{"v":1,"id":"<uuid>","action":"update","channel":"stable"\|"beta","version":"","by":"<user, 1-64 chars>","at":"<RFC 3339>"}`; `version` is `""` (the newest verified release of the channel) or `X.Y.Z` / `X.Y.Z-label.N`; no other field is accepted; `at` within 15 minutes before and a minute after the server's clock; an id already handled is ignored |
| `status.json` | the helper (`bobres update --from-request`, run by `bobres-updater.service` when `bobres-updater.path` sees the request): a temp file, then a rename; root's, mode 0644 | `{"v":1,"installed":true,"helper":"0.7.0","state":"idle"\|"running"\|"done"\|"failed"\|"rolled_back","id":"","from":"0.6.1","to":"0.7.0","channel":"stable","step":"verify"\|"backup"\|"pull"\|"restart"\|"health"\|"rollback"\|"done","message":"one short sentence","started_at":null,"finished_at":null,"log":[]}` |

- The helper deletes `request.json` as soon as it has read it, whatever it held, and answers under the request's `id`
  (an `id` that could not be read is answered as `""`). `installed: true` is how core knows the helper exists; no
  `status.json` means it is not installed (yet), `installed: false` that the owner switched it off. `started_at` and
  `finished_at` are RFC 3339 times in UTC, `null` while unknown; `log` has at most 40 lines, secrets scrubbed. While an
  update runs the file is rewritten at least every 30 seconds.
- Core starts no request while `state` is `running` (it answers `already_running` itself, see "Updating from the
  dashboard" above); the helper itself refuses (as `failed`) a request that comes while another update, rollback or
  restore holds the install lock. A `running` status that has not been rewritten for 10 minutes (the heartbeat is
  30 seconds) belongs to an update that was cut off, a power cut or a killed helper: core reads it as `failed`.

## Monitoring

The dashboard's Monitoring section ([04-dashboard.md](04-dashboard.md), "Monitoring API") is fed by four sources, none of which
gives core a new power over the host:

```
host collector (root, systemd timer, every 30 s)  --- snapshot.json --->  <install>/run/monitor/  (core: /run/bobres-monitor, read only)
bot (has internet)  --- GET /internal/ci, GET /internal/stats --->  core
services' /healthz, /readyz, gRPC (servers, gateways, pending payments, logs), core's database  --->  core
core (sampler, every 30 s) ---> core.monitor_series ---> GET /api/v1/monitor/{overview,series,metrics,events}
```

### The snapshot (`snapshot.json`, schema 1)

Written by `bobres monitor snapshot` (the code is `internal/monitor/collect`, the format and its reader are `internal/monitor/snapshot`).
One JSON object, at most 1 MiB, replaced atomically (a temporary file in the same folder, synced, renamed; mode 0644, owner root).
Core reads it as untrusted text: `snapshot.Read` opens it without following a link, requires a regular file, and `Normalize` drops or
cuts anything that is not what the format says (a name that is not shaped like a name, a percentage that cannot be one, a list past its
bound, a control, bidirectional or zero-width character in any text; `<` and `>` become single-angle quotes). A schema other than 1 is refused,
not guessed at; fields core does not know are ignored. A snapshot is **fresh** for 90 seconds (three missed rounds) and counts for nothing after
that; one dated more than 5 minutes ahead of core's clock is refused.

```json
{
  "schema": 1, "generated_at": 1792521150, "generated_ms": 1792521150499, "interval_s": 30, "duration_ms": 204, "version": "0.7.0",
  "errors": ["host: the disk of the docker directory could not be read"],
  "host": {
    "hostname": "srv1", "os": "Ubuntu 24.04.5 LTS", "kernel": "6.8.0-45-generic", "arch": "amd64", "uptime_s": 1209600, "cores": 4,
    "cpu": {"pct": 12.5, "busy": 123456, "total": 987654}, "load": [0.5, 0.4, 0.3],
    "mem": {"total": 8320897024, "available": 6918156288}, "swap": {"total": 1073737728, "used": 0},
    "disks": [{"mount": "/", "fs": "ext4", "total": 107374182400, "used": 42949672960, "avail": 59055800320, "inodes_total": 6553600, "inodes_used": 210000, "roles": ["install"]}],
    "net": {"ifaces": [{"name": "eth0", "rx_bytes": 123456789, "tx_bytes": 98765432}]},
    "clock": {"synchronized": true, "ntp": true}
  },
  "docker": {
    "ok": true, "version": "29.1.0", "api": "1.52", "truncated": false,
    "containers": [{"id": "1a2b3c4d5e6f", "name": "bobres-core-1", "project": "bobres", "service": "core", "image": "ghcr.io/sobhanaz/bobres-core:0.7.0",
      "state": "running", "health": "healthy", "restarts": 0, "started_at": 1792413600, "cpu_ns": 12345678901, "cpu_pct": 3.2, "mem_bytes": 91750400, "mem_limit": 536870912,
      "net_rx_bytes": 123456, "net_tx_bytes": 234567, "blk_read_bytes": 0, "blk_write_bytes": 4096, "pids": 12}],
    "df": {"images_bytes": 3200000000, "images_reclaimable_bytes": 800000000, "containers_bytes": 12000000, "volumes_bytes": 900000000, "build_cache_bytes": 0, "collected_at": 1792520000, "attempted_at": 1792520000},
    "compose": {"expected": ["backup", "bot", "caddy", "core", "payments", "postgres", "provisioner", "redis", "tls-agent"], "missing": []}
  },
  "units": [{"name": "bobres-monitor.timer", "load": "loaded", "active": "active", "sub": "waiting", "enabled": "enabled", "since": 1792400000}],
  "journal": {"window_s": 3600, "sources": [{"name": "docker", "errors": 1, "capped": false, "lines": [{"at": 1792520500, "msg": "level=error msg=\"...\""}]}]},
  "kubernetes": {"detected": false, "reason": "kubectl and k3s were not found on this host"},
  "update": {"from": "0.6.1", "to": "0.7.0", "at": 1792300600}
}
```

| Section | What it holds, and what it never holds |
|---|---|
| `host` | `/proc` figures (CPU jiffies and the share since the previous snapshot, load, memory, swap, uptime, network byte counters of the real interfaces), the OS name and kernel, the hostname, and the filesystems of the install directory and of Docker's data root (by mount point; `roles` says which). Never a path inside a home directory, a process list, a user name or an address |
| `docker` | From the Engine API over its socket, read only (`GET /containers/json`, `/containers/{id}/json`, `/containers/{id}/stats?stream=false&one-shot=true`, `/info`, `/version`, `/system/df`): per container only the short id, the name, the Compose project and service, the image reference and tag **without a digest**, the state and health words, restart count, start time, exit code, whether it was killed for memory, the CPU, memory, network and block counters and the process count. **Never** its environment, command line, labels (apart from the two Compose ones), mounts, ports, addresses or networks. At most 64 containers; `compose.expected` is what the install is meant to run (from `.env`'s profiles) and `missing` what has no running container |
| `units` | `systemctl show` of `bobres-monitor.{timer,service}`, `bobres-updater.{path,service}` and `docker.service`, those that are installed |
| `journal` | A count of error-level lines of the last hour for `docker` (the engine and containerd), `bobres` (BOBRES's own units) and `caddy` (read from its container's log), and up to 5 lines each, **scrubbed**: tokens, bearer and key-value secrets, URL credentials and query strings, subscription tokens, e-mail addresses, long hex or base64 strings and IP addresses are replaced; each line at most 240 characters |
| `kubernetes` | Optional: only when `kubectl` or `k3s kubectl` is found in a fixed list of directories and answers. Node name, readiness, roles, version; pod namespace, name, phase, readiness, restart count and node (at most 32 nodes and 200 pods). Never a pod spec, annotation, env or secret |
| `update` | The versions and the time of the newest update, from the restore point `bobres update` leaves; none of its paths |
| `errors` | In a few words, the parts the run could not read (at most 10) |

CPU shares and rates need the previous snapshot: the collector keeps the cumulative counters and divides by the time between the two
(`generated_ms`); with none, or one older than 10 minutes, or after a reboot, the share is `null`, not zero.

### The bot's internal endpoints

Both need core's service token (`Authorization: Bearer <BOBRES_BOT_INTERNAL_TOKEN>`, as every `/internal/` endpoint of the bot) and answer `401`
otherwise. Core is the only caller.

`GET /internal/ci` answers the repository's public CI/CD facts (`internal/cicd`). The bot asks GitHub's REST API **anonymously** (no token, no
cookie; 60 requests an hour per address), `GET /repos/{repo}/actions/runs?branch=main&per_page=10`,
`.../actions/workflows/release.yml/runs?per_page=5` and `.../releases?per_page=5`, with the update check's conventions: 10 seconds per request,
2 MiB per answer, `User-Agent: bobres/<version> (+https://github.com/<repo>)`, conditional requests (`If-None-Match`; a `304` costs no quota),
a 5-minute cache, two retries with a doubling pause after a network error or a `5xx`, and once GitHub says the quota is gone (`403` or `429`
with `X-RateLimit-Remaining: 0` or `Retry-After`) **no request is made until it is back**. The repository is a constant of the build
(`release.Repo`), never read from a setting or a request. It answers `200` with the status even when GitHub cannot be reached:

```json
{"available": true, "checked_at": 1792499900, "error": "", "detail": "", "repo": "sobhanaz/bobres-3x-ui-telegram-panel",
 "runs": [{"workflow": "CI", "branch": "main", "event": "push", "status": "completed", "conclusion": "success", "url": "https://github.com/.../actions/runs/1", "sha": "abc1234", "at": 1792480000}],
 "release_runs": [], "releases": [{"tag": "v0.7.0", "published_at": 1792300500, "prerelease": false, "url": "https://github.com/.../releases/tag/v0.7.0"}],
 "rate_reset_at": 0}
```

`error` is `""`, `not_checked`, `unreachable`, `rate_limited` or `invalid`; with an error the earlier facts stay (`available` stays true when
there are any). GitHub's text is untrusted: only these fields are kept, each shaped and cut (a branch, tag, event and hash must look like one; a
workflow name is plain text of at most 64 characters; a link is kept only if it points into the repository on `github.com`), and core checks
the answer again before it reaches a page (`cicd.Sanitize`).

`GET /internal/stats` answers the bot's own counters:

```json
{"started_at": 1792413700, "updates_total": 120345, "updates_queued": 0, "warnings_total": 17, "errors_total": 2, "goroutines": 41, "heap_bytes": 18874368}
```

`*_total` only grow while the bot runs; core turns them into per-minute rates between two readings and records no rate when `started_at`
changed (the bot restarted) or the readings are more than 5 minutes apart.

### Alert events

The monitoring rules are checks of core's watchdog, so they are announced as `system.alert.v1` like every other
([runbooks/README.md](runbooks/README.md)). Their bot texts (`alert.monitor_*`, Persian and English) and values:

| Key | `title_key` | `args` |
|---|---|---|
| `monitor.disk:<mount>` | `alert.monitor_disk` | `mount`, `percent`, `free` (bytes) |
| `monitor.memory` | `alert.monitor_memory` | `percent`, `minutes` |
| `monitor.container.<service>.down` | `alert.monitor_container_down` | `container` |
| `monitor.container.<service>.unhealthy` | `alert.monitor_container_unhealthy` | `container` |
| `monitor.container.<service>.restarting` | `alert.monitor_container_restarting` | `container`, `count`, `minutes` |
| `monitor.collector` | `alert.monitor_collector_stale` | `minutes` |
| `monitor.ci.main` | `alert.monitor_ci` | `workflow` |

## Open points
- DECIDED (2026-10-03): outbox + gRPC pull feed with per-consumer cursors (replaces LISTEN/NOTIFY); NATS only if load requires it.
- Implemented (2026-10-12): payments publishes `payment.succeeded.v1` / `payment.rejected.v1` (core owns wallets and
  applies each intent exactly once) and `payment.deposit_unmatched.v1` / `deposit_claimed.v1`. Core publishes for the
  bot: `order.paid.v1`, `wallet.credited.v1`, `payment.rejected.v1`, `subscription.provisioned.v1`, `extended.v1`,
  `provision_failed.v1` and `reminder.v1`, the four `ticket.*.v1`, `system.alert.v1`, `report.daily_summary.v1` and the two
  deposit events.
  `payment.rejected.v1` is for a manual payment a reviewer rejected and for a gateway payment that failed or expired;
  it carries the intent's `provider` and the `reason` (core's sales reports tell the two apart with them).
  The other rows of the catalog are not events: those steps are gRPC calls (orders, payment intents, provisioning,
  the usage sync) or happen inside core (licenses). Expiry and traffic reminders are `subscription.reminder.v1`.
- DECIDED: bot -> core is gRPC (core's CoreService), from Phase 1 on.
