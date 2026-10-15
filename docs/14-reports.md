# 14 - Sales reports

The Reports section of the dashboard (`/admin/reports`) and the API behind it. The page and the API are read-only: they write
no table and send no message (the optional [daily summary](#the-daily-summary-in-telegram) is a separate worker). They answer "what did the store sell, in which currency, to whom and how was it paid" for any range
of days, from data core already keeps: the ledger, the orders and core's own event log (`outbox_core`).

- Who: staff with the `reports.read` permission. Owner and admin have it; support has not, and a reseller is not staff
  at all. The menu hides the page without it, and every report and CSV endpoint answers 403. Support does not see
  revenue elsewhere either: the Overview page's revenue cards (`GET /api/v1/overview`, `revenue`) are only in the
  answer for staff who hold `reports.read`. What support can still see are the amounts of single orders, through
  Orders and Payments, by design (they answer customers about them); the aggregate reports and the CSV exports are
  the ones that need the permission.
- Where: `core` only. The code is `internal/core/store/reports.go` (SQL), `internal/core/domain/reports.go` (ranges and
  buckets), `internal/core/web/reports.go` (JSON and CSV) and `web/dashboard/src/pages/ReportsPage.vue`.
- Tests: the numbers are checked against a model of every seeded row to the last unit, in several time zones, in
  `internal/core/store/reports_test.go`; the real money flows (orders of every type, discounts, a free order, a reseller
  on credit, payment events, the wallet, refunds, trials, an over-payment, failed and rejected payments) are run through
  the service in `internal/core/domain/reports_test.go`.

## The money rule

Amounts are integers in the currency's minor unit (Toman 0 decimals, USDT 6, Stars 0) from the query to the answer.
**Amounts of different currencies are never added**: every sum is grouped by currency, every answer holds a map keyed
by currency, and the dashboard shows each currency in its own card, chart and table. Only counts of people (new
customers, free trials) and counts of events have no currency. The dashboard formats amounts; the API never rounds.

## Time

Days are local days in one explicit time zone, chosen in this order: the request's `tz`, the store's
`general.timezone` setting (Settings page), `BOBRES_TIMEZONE` (compose gives it to core, the bot and the backup
service; `bobres config set BOBRES_TIMEZONE ...` recreates all three), `Asia/Tehran`. A setting or a variable that is
no zone core can load is logged and skipped. The page shows the zone it used. A bucket is computed in SQL with
`date_trunc(group, created_at AT TIME ZONE tz)`: a payment at 23:59 and one at 00:01 local time fall on different
days, and the same instants fall on other days in another zone. Daylight saving is handled by PostgreSQL (a day can be
23 or 25 hours long). A range is its local dates, tested as `(created_at AT TIME ZONE tz)::date BETWEEN from AND to`,
so even a day whose clocks show 00:xx twice (America/Havana on 1 November 2026) belongs whole to one bucket; the
instant bounds next to that test are a day wider on each side and only serve the index.

- `group=day|week|month` and `calendar=gregorian|jalali` (default `gregorian`; anything else is a 400 "calendar must
  be gregorian or jalali"). Gregorian: weeks are ISO weeks (they start on **Monday**) and months are calendar months.
  Jalali: weeks start on **Saturday** and months are Jalali months (1 Farvardin, 1 Ordibehesht, ...; the leap years
  follow the 33-year rule `core` and the Persian calendar share, checked against `Intl` for 1399 to 1479). A day
  bucket is the same in both calendars. A bucket is named by the Gregorian local date it starts on (`"2026-10-12"`),
  whatever the calendar; the page writes it in Jalali in Persian. `this_month` and `last_month` follow the calendar
  (a Jalali month for `jalali`). The store is not changed by this: for a Jalali week or month core reads the days and
  sums them into buckets (`foldDays`), so a bucket is exactly the sum of its days, the totals equal the Gregorian
  ones for the same range, and a range that cuts a week or month marks it `partial` as before. The page sends
  `calendar` only when it is Jalali, and defaults to Jalali in Persian and Gregorian in English.
- The range is two local dates, both included (`from=2026-10-01&to=2026-10-14`), or a named range read in the store's
  zone: `range=today|last_7_days|last_30_days|this_month|last_month` (the default is the last 30 days). A named range
  and `from`/`to` together are refused.
- Data counts only inside the range. A week or month that the range cuts holds only the days inside it and is marked
  `partial`.
- Empty buckets are returned as zeros, for every currency the range saw, so a chart has no gaps.
- Limits: at most 400 days and at most 400 buckets per request, dates from 2000 to 2100, a time zone Go and PostgreSQL
  both know. Anything else is a 400 with the reason.
- Time and load: every report runs in one read-only transaction on one snapshot (per request: the page makes three
  requests, summary, plans and methods, so the three are not one snapshot, and a sale made between them can show in
  one and not in another). PostgreSQL cancels a statement after 15 seconds for a report and 60 seconds for an export;
  a report that runs out of time answers 503 `timeout` ("choose a shorter range", shown in the dashboard in the
  staff member's language). At most two CSV exports run at once; a third gets 429 with `Retry-After: 5`. An export
  may take up to 60 seconds in all (core's HTTP server would cut a response at 30 seconds, so the export handlers
  lift that limit for themselves).

## What each number means

All of these read `core.ledger_entries` (append-only), `core.orders` and `core.outbox_core`. A time is the row's
`created_at` (an order has no "paid at" of its own, so a payment is dated by its ledger entry), except where it says
`updated_at`.

| Number | Definition |
|---|---|
| **Revenue** (per currency) | `-sum(amount)` of the ledger entries with `kind = 'purchase'` in the range. One entry exists per order that cost money (`purchase:<order id>`), written when the order is paid, by the wallet or by a payment event, in the same transaction that marks the order paid. It is what customers were charged, whatever put the money in their wallet: a payment, a refund credit spent again, a staff change, a referral reward, a reseller's commission spent by the reseller. A reseller's purchase counts at the price they paid, including what they buy on credit (their balance may go down to `-credit_limit`). Free orders (a trial, a 100% discount, a service staff extended) have no purchase entry and carry no revenue. |
| **Paid orders** | The number of those purchase entries. `new_orders`, `renewals` and `traffic_topups` split them by `orders.type` (`new`, `renew`, `traffic_topup`). |
| **Refunds** (count, amount) | Ledger entries with `kind = 'refund'` (`refund:<order id>`), dated when staff refunded. A refund credits the order's whole amount to the wallet. **Net** is revenue less refunds in the same currency, before payouts: reseller commissions and referral rewards are not subtracted. |
| **New customers** | Users whose **first** purchase entry (any currency, whole history, ties broken by entry id) falls in the bucket. A customer whose first order was refunded still counts; one who only had a trial or a free order does not. People, so no currency. |
| **Free trials started** | Ledger entries with `kind = 'trial_grant'` (one per user, ever). |
| **Wallet top-ups** (count, amount, per currency) | Money that **stayed in the wallet** from payments. A payment is credited to the wallet as a `topup` entry (`pay-in:<intent id>`) and, when it was for an order, the order's price is taken from it in the same transaction, so the entries carry the same time. What a `topup` entry has left after the purchases of the same user and currency at its instant is the top-up: all of it for a payment with no order or one that arrived for an order already paid another way or cancelled, none for an exact payment, the surplus for a payment larger than the order's price. An entry with nothing left is not counted. A top-up is money in, not a sale: the order it later pays shows up as revenue, paid with the wallet. Admin adjustments, referral rewards, reseller commissions and settlements are other ledger kinds and are not in the reports. |
| **Rejected by staff** (count, amount) | Events `payment.rejected.v1` in core's outbox for a **manual** payment (card to card, crypto proof, Zarinpal link) a reviewer rejected. The amount is the payment's. Nothing moved. |
| **Failed or expired gateway payments** (count, amount) | The same event for an **automated gateway** payment (Zarinpal, Telegram Stars, automatic USDT) that failed, did not match, or ran out of time. Payments publishes `payment.rejected.v1` for both kinds; core passes it on with the intent's `provider` and the `reason`. The provider decides: a manual provider means staff (whatever words the reviewer typed, "expired" included), any other provider a gateway, so a new gateway lands here, never on the staff line. An event from before core passed the provider on has none, and is told by its reason: a gateway reason (`gateway_failed`, `amount_mismatch`, `expired`, `not_found`, `reversed`, `payer_mismatch`) means a gateway, any other text means staff. The two lists are in one place, `ManualProviders` and `GatewayFailureReasons` in `internal/core/store/reports.go`, and a test reads payments' sources, so a new reason or a new manual provider fails it until someone decides where it goes. |
| **Cancelled orders** (count, amount) | Orders with `status IN ('cancelled', 'expired')` and `refunded_at IS NULL`, dated by `updated_at` (nothing changes an order after that); the amount is what the order would have cost. Today only cancellation exists (a reseller's removal cancels their open orders); nothing sets `expired`. An order refunded before delivery is also `cancelled` but was paid: it counts as a refund, not here. |
| **Paid with** (per currency) | Events `order.paid.v1` in core's outbox with `amount > 0`, grouped by their `source`: **wallet** (the customer's balance, including a reseller's credit), **manual** (a payment staff approved: card to card, crypto, Zarinpal link) and **gateway** (Zarinpal, Telegram Stars or automatic USDT confirmed it). Each paid order publishes its event in the transaction of its purchase; a purchase with no event would be listed as **unknown**, so the rows always add up to revenue. A gateway or manual row is split again by the event's `provider`, the payments provider that took the money (`zarinpal`, `stars`, `usdt_trc20_auto`, `manual_card`, `manual_crypto`, `manual_zarinpal`), so Zarinpal, Stars and automatic USDT are separate rows (and card to card, crypto and Zarinpal link). Wallet, trial and free orders have no provider. Events from before the provider was recorded have none either: they stay in one row of their method with an empty provider, shown as "Gateway (provider not recorded)" or "Staff-approved (provider not recorded)", and the rows still add up to revenue. |
| **Top plans** (per currency) | The purchase entries joined to their order and plan: paid orders and revenue per plan, best revenue first, at most 20 plans per currency. A plan sold in two currencies appears under each. |

## What the reports do not say (yet)

- **Which provider, for payments made before it was recorded.** `order.paid.v1` carries the `provider` from now on.
  Orders paid before that have none, and core cannot read payments' `payment_intents` (schema `payments`) to fill it in,
  so they stay in their method's "provider not recorded" row; no history is rewritten. (`GatewayReport` exists in
  payments, per gateway and for the owner only.)
- **Expired payment intents of manual payments, and intents that were never paid.** They live in the payments schema.
  What core knows is the failed and rejected payments above and the cancelled orders.
- **Churn, and revenue by customer.** Not built.

## The daily summary in Telegram

Once a day core can message the staff with yesterday's numbers, the same ones the Reports page shows for that day.

- **Switch and hour.** `notify.daily_summary` (Settings, group Notifications; off by default) and
  `notify.daily_summary_hour` (0 to 23, default 9). The day is the store's day, in `general.timezone` (else
  `BOBRES_TIMEZONE`, else Asia/Tehran), the same zone the reports use.
- **When.** A core worker checks every minute. After the hour it publishes `sales.daily_summary.v1` for the day before.
  The date of the last day published is kept in `core.settings` under `state.daily_summary.last` (internal state, not a
  setting the dashboard shows) and is written in the same transaction as the event, under an advisory lock, so a day
  goes out exactly once, also after a restart or with two cores running. If core was down at the hour, the summary goes
  out when it is back; days missed are not made up, only the last finished day is sent. Turning the switch on in the
  afternoon sends yesterday's summary at once.
- **Content.** The date (Jalali in Persian), then one block per currency that had revenue, orders, refunds or wallet
  top-ups: revenue, orders (new and renewals), refunds with the net, and top-ups. Currencies are never added together.
  Then the new customers and free trials of the day. A day with no paid orders is still sent ("No paid orders.").
- **To whom.** The staff alert recipients (`notify.recipients`: the owner, or every owner and admin; never support
  staff, who do not hold `reports.read`), each in their language.

## Ledger and audit CSV files

`ledger.csv` and `audit.csv` use the same export path as the reports files (`internal/core/web/csvexport.go`):

- Nothing is sent until the first data row exists. A failure before that (for example it runs out of time) is an error
  answer (503 "the export took too long: narrow the filter or choose a shorter range"), never a header-only file. If
  nothing matches, the file has its header alone.
- A failure after rows went out ends the file with `# export incomplete: <reason>`.
- An export may run for at most the export window (60 s by default); each database statement is read-only and capped
  at the same limit.
- Ledger, audit and report exports share two slots; a third request while two run gets 429 with `Retry-After: 5`.
- Each export writes its audit entry (`export.ledger`, `export.audit`) when it ends, with the filters, plus `outcome`
  (`complete`, `incomplete`, `failed`) and `rows`. An export refused with 429 is not recorded. So `audit.csv` does
  not list its own export; the next one does.
- Text cells that could run as a formula are quoted with a leading `'`, also text that starts with spaces and then
  `=`, `+`, `-` or `@`. Both files still stop at 100,000 rows and add the "more entries match" note.

## API

All under `/api/v1/reports`, `GET`, session cookie, permission `reports.read`. Query: `range` or `from` + `to`,
`group` (summary only), `calendar` (`gregorian` or `jalali`; summary, and the CSV files as the summary's), `tz`.

| Endpoint | Answer |
|---|---|
| `/summary` | `{from, to, group, calendar, tz, currencies, buckets, totals}`. A bucket is `{date, partial, new_customers, trials, currencies}`; `currencies` maps each currency to `{revenue, refunds: {count, amount}, net, orders, new_orders, renewals, traffic_topups, topups: {count, amount}, rejected: {count, amount}, gateway_failed: {count, amount}, cancelled: {count, amount}}` (`rejected` is by staff, `gateway_failed` is the gateway line). `totals` has the same shape, summed bucket by bucket within each currency. |
| `/plans` | `{from, to, tz, plans: [{plan_id, name: {fa, en}, currency, orders, revenue}]}` |
| `/methods` | `{from, to, tz, methods: [{method, provider, currency, orders, revenue}]}`; `provider` is always there, empty for wallet, unknown and orders paid before it was recorded |
| `/summary.csv` | The summary as a file (`...-jalali.csv` when `calendar=jalali`). |
| `/orders.csv` | Every order paid in the range, as a file. |

### CSV files

`text/csv; charset=utf-8`, `Content-Disposition: attachment`, a UTF-8 byte-order mark (spreadsheet programs then read
Persian right), rows streamed from the database one at a time. Amounts are plain decimals in the currency's major
unit (`2.500000` USDT), times in UTC (and local time where it says so), and any text a customer or staff chose
(username, plan names) gets a single quote in front when it starts with `=`, `+`, `-`, `@`, a tab or a carriage
return, or with spaces or line breaks and then one of the first four, so a spreadsheet cannot run it as a formula.
Numbers are numbers: a negative net stays `-100000`. (`ledger.csv` and `audit.csv` follow the same rules, see below.)

Nothing is sent until the file has its first row. A failure before that (the range holds too much to read in time, the
database is down) is an error answer (503 with `timeout` or `unavailable`), never a 200 with only a header that looks
like "no orders". A failure after rows went out ends the file with a line `# export incomplete: <reason>`. Every export
is written to the audit log as `export.reports` when it ends, with its kind, range, group, calendar, zone, number of rows and
outcome: `complete`, `incomplete` (the file ended short) or `failed` (nothing was sent).

- `summary.csv`: one row per bucket and currency (`period_start`, `partial_period`, `currency`, `revenue`,
  `refund_amount`, `net`, `paid_orders`, `new_orders`, `renewals`, `traffic_topups`, `topup_count`, `topup_amount`,
  `refund_count`, `rejected_count`, `rejected_amount` (by staff), `gateway_failed_count`, `gateway_failed_amount`,
  `cancelled_count`, `cancelled_amount`), and one row per bucket with an **empty currency** that holds `new_customers`
  and `free_trials` (the counts of people), so no column adds up two currencies or counts a person twice. There is no
  totals row, for the same reason.
- `orders.csv`: `paid_at_utc`, `paid_at_local`, `order_id`, `type`, `telegram_id`, `username`, `plan_fa`, `plan_en`,
  `amount`, `currency`, `paid_with` (wallet, manual, gateway, or empty), `refunded_amount`, `provider` (the provider that
  took the payment, empty for wallet, trial and free orders and for orders paid before it was recorded; always the last
  column, so earlier columns keep their positions); oldest first; at most
  100,000 rows, then a note line asks for a shorter range.

## Indexes

Migration `00018_reports_indexes.sql` adds three partial indexes and nothing else: `outbox_core_reports_idx`
(`topic, created_at` for the two event topics the reports read), `outbox_core_order_paid_idx` (the order id of each
`order.paid.v1` event: the orders export looks up how each order was paid, one probe per order) and
`orders_unpaid_end_idx` (`updated_at` of cancelled and expired orders). The ledger queries use the indexes of
migration 00007. `Down` drops all three.

The order id index is what keeps the orders export fast at any volume. Measured on a scratch database with 150,000
paid orders and 1.5 million outbox rows (561 MB): a 400-day export of the first 100,000 orders took 4.0 s, all
150,000 rows 6.0 s, a 30-day range (45,000 orders) 1.9 s; creating the index took 2.7 s on those 1.5 million rows and
it is 8.7 MB. The first version of the export joined the orders to a subquery of the outbox instead; past about 100,000 orders PostgreSQL switched to a plan that ran for minutes and hit the 60-second limit.
A test (`TestOrdersExportFindsHowOrdersWerePaidThroughAnIndex`) fails if the planner stops using the index.
