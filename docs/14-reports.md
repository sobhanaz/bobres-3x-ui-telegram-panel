# 14 - Sales reports

The Reports section of the dashboard (`/admin/reports`) and the API behind it. It is read-only: it writes no table and
sends no message. It answers "what did the store sell, in which currency, to whom and how was it paid" for any range
of days, from data core already keeps: the ledger, the orders and core's own event log (`outbox_core`). The one thing
beside it that does send a message and write a row is [the daily summary in Telegram](#the-daily-summary-in-telegram),
which is off until the store turns it on.

- Who: staff with the `reports.read` permission. Owner and admin have it; support has not, and a reseller is not staff
  at all. The menu hides the page without it, and every report and CSV endpoint answers 403. Support does not see
  revenue elsewhere either: the Overview page's revenue cards (`GET /api/v1/overview`, `revenue`) are only in the
  answer for staff who hold `reports.read`. What support can still see are the amounts of single orders, through
  Orders and Payments, by design (they answer customers about them); the aggregate reports and the CSV exports are
  the ones that need the permission.
- Where: `core` only. The code is `internal/core/store/reports.go` (SQL), `internal/core/domain/reports.go` (ranges and
  buckets), `internal/core/web/reports.go` (JSON and CSV), `internal/core/web/csvcells.go` and `csvexport.go` (the rules of
  every CSV file), `web/dashboard/src/pages/ReportsPage.vue` and, for the calendar and the dates, `web/dashboard/src/calendar.ts`
  and `components/reports/DateField.vue`; for the charts and the professional report see
  [Charts and the professional report](#charts-and-the-professional-report).
- Tests: the numbers are checked against a model of every seeded row to the last unit, in several time zones, in
  `internal/core/store/reports_test.go`; the real money flows (orders of every type, discounts, a free order, a reseller
  on credit, payment events, the wallet, refunds, trials, an over-payment, failed and rejected payments) are run through
  the service in `internal/core/domain/reports_test.go`. The CSV files are checked as a spreadsheet sees them in
  `internal/core/web/csv_files_test.go`, `csv_totals_test.go` and `csvcells_test.go` (headers, line breaks, numbers and
  formulas, dates in both calendars, permissions, no secrets, the rows adding up to the report), and the calendar in
  `web/dashboard/src/__tests__/calendar.test.ts` and `reports.test.ts`. The plan series and the hours of the week have their own
  tests in the three layers (`store/reports_series_test.go`, `domain/reports_series_test.go`, `web/reports_test.go`), and the
  charts and the professional report are tested in `charts*.test.ts`, `reportsvisual.test.ts` and `reports-visual.test.ts`.

## The money rule

Amounts are integers in the currency's minor unit (Toman 0 decimals, USDT 6, Stars 0) from the query to the answer.
**Amounts of different currencies are never added**: every sum is grouped by currency, every answer holds a map keyed
by currency, and the dashboard shows each currency in its own card, chart and table. Only counts of people (new
customers, free trials, churn) and counts of events have no currency. The dashboard formats amounts; the API never rounds.

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
  whatever the calendar; the page writes it in the calendar it is set to (see below). `this_month` and `last_month`
  follow the calendar (a Jalali month for `jalali`). The store is not changed by this: for a Jalali week or month core
  reads the days and sums them into buckets (`foldDays`), so a bucket is exactly the sum of its days, the totals equal
  the Gregorian ones for the same range, and a range that cuts a week or month marks it `partial` as before. The page
  sends `calendar` only when it is Jalali.
- The range is two local dates, both included (`from=2026-10-01&to=2026-10-14`), or a named range read in the store's
  zone: `range=today|last_7_days|last_30_days|this_month|last_month` (the default is the last 30 days). A named range
  and `from`/`to` together are refused.
- Data counts only inside the range. A week or month that the range cuts holds only the days inside it and is marked
  `partial`.
- Empty buckets are returned as zeros, for every currency the range saw, so a chart has no gaps.
- Limits: at most 400 days and at most 400 buckets per request, dates from 2000 to 2100, a time zone Go and PostgreSQL
  both know. Anything else is a 400 with the reason.
- Time and load: every report runs in one read-only transaction on one snapshot (per request: the page makes five
  requests, summary, plans, methods, plan-series and hours (six, with the period before, in the professional report), so they are not one snapshot, and a sale made between them can show in
  one and not in another). PostgreSQL cancels a statement after 15 seconds for a report and 60 seconds for an export;
  a report that runs out of time answers 503 `timeout` ("choose a shorter range", shown in the dashboard in the
  staff member's language). At most two CSV exports run at once; a third gets 429 with `Retry-After: 5`. An export
  may take up to 60 seconds in all (core's HTTP server would cut a response at 30 seconds, so the export handlers
  lift that limit for themselves).

## The calendar on the page

The **Calendar** switch (Jalali or Gregorian) decides every date of the Reports page, not only how weeks and months
are cut. Before it did only that: dates followed the language (Jalali in Persian, Gregorian in English) and the custom
range was a browser date field, which is Gregorian whatever the page says.

- **What follows the switch**, at once and in both directions: the days a report covers, the ends of each chart's axis,
  its tooltips and its text summary, the period column of the table, the week and month names ("Week of 25 Mehr 1405",
  "Aban 1405", "Month of 1 Oct 2026"), the **From** and **To** fields of a custom range (the same two days written in
  the other calendar, the request unchanged), the month grid they open, the message about a wrong date, and the CSV
  files (their extra date column and their file names, see [CSV files](#csv-files)). Digits and month names follow the
  language: Persian digits in Persian (`۲۲ مهر ۱۴۰۵`, and `۱۴ اکتبر ۲۰۲۶` for Gregorian), Latin in English
  (`22 Mehr 1405`, `14 Oct 2026`). The API and every query keep Gregorian ISO dates (`from=2026-10-14`).
- **Typing and picking a custom range.** A date is typed as year, month and day (`1405/07/22`, `1405-7-22`, `14050722`;
  Persian, Arabic-Indic or Latin digits; a slash, dash, dot or space between; the year in four digits) in the calendar
  shown, or picked from a month grid that starts on Saturday in Jalali and Monday in Gregorian (the days a report cuts
  its weeks on), with the arrow, Home, End and Page keys. A date that the calendar does not have (31 Mehr, 30 Esfand of
  a year that is not leap, 29 February of 2026), one outside the years core reports on (2000 to 2100) or an end before
  the start is not sent: the field is marked and a line says what a date looks like. Typing waits a moment after the
  last key before asking.
- **The conversion is exact.** It is calendar arithmetic, never a `Date` in the browser's zone, so no time zone,
  daylight-saving change or midnight can move a day (`dashboard/src/calendar.ts`). The Jalali arithmetic is the one
  core and the bot use (the 33-year rule, `GregorianToJalali` in `internal/bot/i18n`), so a Jalali month the page
  names is the month core cut. A test converts every day from 1900 to 2200 and back, every Jalali day from 1300 to 1500
  one after the other, compares every day of 2000 to 2100 with the browser's own Persian calendar, and pins the new
  year, the leap days and the month lengths. "Today" in the grid and in the examples is the store's today (the zone the
  report named), not the browser's.
- **It is remembered.** The choice is kept in this browser (`localStorage`, key `bobres.reports.calendar`), as the
  language and the theme are; nothing is stored on the server. Until one is made the language decides (Jalali in
  Persian, Gregorian in English); a blocked or odd value is ignored. The ledger, the audit log and the reseller
  statement ask their CSV files for the Jalali column when the Reports page is set to Jalali.

## Charts and the professional report

The page has two views of the same numbers, switched at the top (`?view=report` in the address, so a view can be linked
and survives a reload). Both follow the range, the grouping and the **Calendar** switch (every date they write is in
the calendar chosen), and both are Persian first and English second.

**Charts and tables** (the default). Cards per currency, customers and churn, each with a small trend line; then the
charts, **one currency at a time** (a switch when there is more than one: amounts of two currencies never share an
axis), then the table:

- revenue and what was kept after refunds, per period (areas and lines; a period the range cuts is joined by a dashed
  segment);
- revenue **by plan** over the periods (stacked columns: the five best plans one by one, then "other");
- paid orders by type (new, renewals, traffic packages), stacked;
- how orders were paid (a donut with each way's value and share) and the top plans (a ranking);
- **when customers pay**: paid orders by weekday and hour of the day, in the store's time zone, the week read from
  Saturday in Jalali and from Monday in Gregorian (a heatmap);
- new customers, free trials and services lost per period.

Every chart has a legend that shows and hides its series, a tooltip listing every series at the point under the pointer
or the keyboard's focus, and **a table with the same numbers** behind a "Show as table" button (the alternative for screen
readers and for exact values). The arrow keys move between points, the right arrow going the way it points on screen
(earlier, in Persian). The **by period table** scrolls inside its own box (down and sideways) with its header row, its
period column and a totals row held in place, so neither a long range nor a phone makes the page long or wide; the box
can be focused and scrolled with the keyboard. The charts are the chart kit in `web/dashboard/src/components/charts/`
(documented in its `README.md`: props, colours, keyboard, accessibility, right to left), which the System page's Monitoring tab reuses.

**Professional report** (`components/reports/ProfessionalReport.vue`, its own chunk: loaded only when this view opens).
A report to read, print or show someone, per currency and then for the people:

- a letterhead: the store's name, "Sales report", the days (in the calendar chosen), the time zone, the date it was
  prepared and what the changes are compared with;
- a headline in plain words (how many orders, worth how much, the best day, refunds, the best-selling plan, the way most
  money came in, the busiest time) and, for each headline number (revenue, kept after refunds, paid orders, average
  order; then new customers, free trials, services lost, customers lost), the number, its **change on the period before**,
  a small trend line and a sentence ("Revenue was 301,230,000 Toman, 51% higher than the previous 30 days.");
- the charts above, with two 3D views (below);
- a footer that says how money is counted (never added across currencies, net before payouts) and that churn is "as of
  now".

*The period before* is the same number of days right before the range ("the previous 30 days"), fetched with the same summary call (`from`, `to`, the same `tz`, `group=month` so only its few buckets are
read; only its totals are used) and only while the report view is open. It is not asked for when it would start before
2000-01-01 (the note says there is no earlier period), and when the call fails the note says so and every number is
written alone, with no change. The rules are the daily summary in Telegram's: a rise or fall in whole percent, "exactly
the same", "about the same" (under half a percent), "while the period before had none" (nothing before), a rise past 999%
is "more than 999%", and a period that had sales is at most "down 99%". A **rise in lost services or customers is drawn
as bad news** (red), a rise in sales as good (green), always with an arrow and the percent beside the colour.

*3D*: the plan columns are an isometric 3D stacked-column scene and the payment methods a thick donut that leans back;
both turn with a drag or two sliders (Rotate, Tilt) and reset. They are **SVG polygons from an orthographic camera**
(`components/charts3d/projection.ts`): no WebGL and no library, so they print and scale and need no fallback for a missing
GPU, and they are two small chunks loaded on demand. The flat charts show instead (and are one switch away) on a screen
under 640 px wide, for a person who prefers reduced motion (3D is one click away and has no entrance motion then), and
for ranges of more than 62 periods, which a 3D scene cannot show readably. The 3D charts have the same legend, tooltip,
keys and table as the flat ones.

*Print or save as PDF*: the button calls the browser's print. A paper stylesheet (A4, 12 mm margins) leaves out the
menus, the toolbar and the buttons, prints the light colours whatever theme is on screen, keeps cards and charts from
being cut across pages and starts every currency after the first, and the customers, on a new page.

## Starting from zero

When the sales so far were tests, the owner (permission `reports.reset`, owner only) presses **Start from zero** on the Reports page: from that
moment the reports and the Overview's revenue count only what happens next. It is a cutoff, not a deletion: the setting `reports.since` (an RFC 3339
instant) is read by every report (`readOnly` hands it to the transaction as `bobres.reports_since` and `rangeOf` stops at it) and by the overview;
orders, payments, wallets, the ledger and the audit log stay as they are, so a wallet balance is still the sum of its ledger. **Count everything
again** takes the cutoff away. `GET /reports/since` (anyone who reads reports) tells it, `POST /reports/reset` with `{"confirm": true}` and
`DELETE /reports/reset` change it (`reports.reset` and `reports.reset_clear` in the audit log). Churn counts services by their expiry and are not
cut. A customer who bought before the start and buys again after it is not a new customer of the new count.

## What each number means

All of these read `core.ledger_entries` (append-only), `core.orders` and `core.outbox_core`. A time is the row's
`created_at` (an order has no "paid at" of its own, so a payment is dated by its ledger entry), except where it says
`updated_at`.

| Number | Definition |
|---|---|
| **Revenue** (per currency) | `-sum(amount)` of the ledger entries that are sales in the range: `kind = 'purchase'` (VPN orders), `'license_purchase'` (BOBRES licenses the owner's store sells) and `'supplier_purchase'` (digital accounts it resells). `licenses` and `digital` (count and amount, per bucket and currency) say how much of revenue and of paid orders those two made; a license is paid in USDT and a digital account in Toman, so each lands in its own currency card, never added to the other. For the VPN orders: one entry exists per order that cost money. One entry exists per order that cost money (`purchase:<order id>`), written when the order is paid, by the wallet or by a payment event, in the same transaction that marks the order paid. It is what customers were charged, whatever put the money in their wallet: a payment, a refund credit spent again, a staff change, a referral reward, a reseller's commission spent by the reseller. A reseller's purchase counts at the price they paid, including what they buy on credit (their balance may go down to `-credit_limit`). Free orders (a trial, a 100% discount, a service staff extended) have no purchase entry and carry no revenue. |
| **Paid orders** | The number of those sales entries. `new_orders`, `renewals` and `traffic_topups` split the VPN orders among them by `orders.type` (`new`, `renew`, `traffic_topup`). A bulk purchase of N services is N ordinary orders, each with its own purchase entry, so it adds N to the count (all `new`) and the sum of their prices to the revenue, at the instant they were paid together; the reports need no special case for it (`core.bulk_orders` is not read). |
| **Refunds** (count, amount) | Ledger entries with `kind` `'refund'` (`refund:<order id>`), `'license_refund'` or `'supplier_refund'` (a license or a digital account the supplier refused for good), dated when staff refunded or the refusal was recorded. A refund credits the order's whole amount to the wallet. **Net** is revenue less refunds in the same currency, before payouts: reseller commissions and referral rewards are not subtracted. |
| **New customers** | Users whose **first** purchase entry (any currency, whole history, ties broken by entry id) falls in the bucket. A customer whose first order was refunded still counts; one who only had a trial or a free order does not. People, so no currency. |
| **Free trials started** | Ledger entries with `kind = 'trial_grant'` (one per user, ever). |
| **Services lost (churn)** | A service **churned** in a bucket when its **current** `expires_at`, as a local date in the report's zone, is in the bucket, and it ended more than the **grace period** (7 days by default, `churn_grace_days` from 0 to 90) before the moment the report is read. Only the service's final expiry counts, so a service that was renewed is in the bucket of its last end, never of an earlier one. Not counted: services that are `pending`, `disabled` or `deleted`, services with no expiry (unlimited), services of a **trial** plan (`plans.is_trial`, so a trial that lapses is no loss) and services with a **paid renewal waiting** (a `renew` order that is `paid`, `provisioning` or `provision_failed` and has not moved `expires_at` yet). People, so no currency. The grace is counted in 24-hour days, so it does not depend on the zone. |
| **Customers lost** | A person whose **last** expiry over their non-trial, live services is in the bucket and past the grace period, and who has **no other live service**: a service with no expiry, or one with a renewal waiting, keeps them. A person with three expired services is counted once, in the bucket of the last. |
| **At risk** | Services that ended in the bucket but are still inside the grace period: they are not churn yet and may renew. Same exclusions as services lost. When the grace period passes they move into services lost. |
| **Wallet top-ups** (count, amount, per currency) | Money that **stayed in the wallet** from payments. A payment is credited to the wallet as a `topup` entry (`pay-in:<intent id>`) and, when it was for an order, the order's price is taken from it in the same transaction, so the entries carry the same time. What a `topup` entry has left after the purchases of the same user and currency at its instant is the top-up: all of it for a payment with no order or one that arrived for an order already paid another way or cancelled, none for an exact payment, the surplus for a payment larger than the order's price. An entry with nothing left is not counted. A top-up is money in, not a sale: the order it later pays shows up as revenue, paid with the wallet. Admin adjustments, referral rewards, gift codes, reseller commissions and settlements are other ledger kinds and are not in the reports. |
| **Rejected by staff** (count, amount) | Events `payment.rejected.v1` in core's outbox for a **manual** payment (card to card, crypto proof, Zarinpal link) a reviewer rejected. The amount is the payment's. Nothing moved. |
| **Failed or expired gateway payments** (count, amount) | The same event for an **automated gateway** payment (Zarinpal, IDPay, NextPay, Aqayepardakht, Telegram Stars, automatic USDT) that failed, did not match, or ran out of time. Payments publishes `payment.rejected.v1` for both kinds; core passes it on with the intent's `provider` and the `reason`. The provider decides: a manual provider means staff (whatever words the reviewer typed, "expired" included), any other provider a gateway, so a new gateway lands here, never on the staff line. An event from before core passed the provider on has none, and is told by its reason: a gateway reason (`gateway_failed`, `amount_mismatch`, `expired`, `not_found`, `reversed`, `payer_mismatch`) means a gateway, any other text means staff. The two lists are in one place, `ManualProviders` and `GatewayFailureReasons` in `internal/core/store/reports.go`, and a test reads payments' sources, so a new reason or a new manual provider fails it until someone decides where it goes. |
| **Cancelled orders** (count, amount) | Orders with `status IN ('cancelled', 'expired')` and `refunded_at IS NULL`, dated by `updated_at` (nothing changes an order after that); the amount is what the order would have cost. Today only cancellation exists (a reseller's removal cancels their open orders); nothing sets `expired`. An order refunded before delivery is also `cancelled` but was paid: it counts as a refund, not here. |
| **Paid with** (per currency) | Licenses and digital accounts are always paid from the wallet and publish no `order.paid.v1`: they are added to the **wallet** row. For the VPN orders: events `order.paid.v1` in core's outbox with `amount > 0`, grouped by their `source`: **wallet** (the customer's balance, including a reseller's credit), **manual** (a payment staff approved: card to card, crypto, Zarinpal link) and **gateway** (Zarinpal, Telegram Stars or automatic USDT confirmed it). Each paid order publishes its event in the transaction of its purchase; a purchase with no event would be listed as **unknown**, so the rows always add up to revenue. A gateway or manual row is split again by the event's `provider`, the payments provider that took the money (`zarinpal`, `stars`, `usdt_trc20_auto`, `manual_card`, `manual_crypto`, `manual_zarinpal`), so Zarinpal, Stars and automatic USDT are separate rows (and card to card, crypto and Zarinpal link). Wallet, trial and free orders have no provider. Events from before the provider was recorded have none either: they stay in one row of their method with an empty provider, shown as "Gateway (provider not recorded)" or "Staff-approved (provider not recorded)", and the rows still add up to revenue. |
| **Top plans** (per currency) | The VPN purchase entries joined to their order and plan (licenses and digital accounts are not plans: they are in `licenses` and `digital`): paid orders and revenue per plan, best revenue first, at most 20 plans per currency. A plan sold in two currencies appears under each. |

## What the reports do not say (yet)

- **Which provider, for payments made before it was recorded.** `order.paid.v1` carries the `provider` from now on.
  Orders paid before that have none, and core cannot read payments' `payment_intents` (schema `payments`) to fill it in,
  so they stay in their method's "provider not recorded" row; no history is rewritten. (`GatewayReport` exists in
  payments, per gateway and for the owner only.)
- **Expired payment intents of manual payments, and intents that were never paid.** They live in the payments schema.
  What core knows is the failed and rejected payments above and the cancelled orders.
- **Churn is a state read now, not a history.** The numbers use each service's current expiry and the moment the report
  is read ("as of now"). A customer who comes back after the grace period (a new purchase, or a renewal of the old
  service) leaves the bucket they were counted in, so a past period's churn can shrink later. Nothing records when a
  service was last seen churned. There is no churn **rate** (churned over active at the start of the period): the
  active-at-start count would have to be rebuilt from expiries that were later moved, and the result would not be
  reproducible. Divide the numbers yourself if you need an approximate rate.
- **Revenue by customer.** Not built.

## The daily summary in Telegram

Once a local day the bot messages yesterday's sales to the staff who may read the reports. It is **off until the
store turns it on**, on the Settings page (card "Sales summary") or with `/set` in the bot:

| Setting | Default | Meaning |
|---|---|---|
| `reports.daily_summary` | `false` | `true` turns the summary on |
| `reports.daily_summary_time` | `09:00` | `HH:MM`, 24-hour, in the store's time zone (typed as `9:30` or in Persian digits it is stored as `09:30`; anything else is refused) |

The Reports page says under its hint whether the summary is on, at what time and in which zone, and links to the
switch for staff who may change settings (`GET /api/v1/reports/daily-summary`, `reports.read`: `{enabled, time, tz}`).

- **What it says.** One local day, **yesterday**, from the same function as the Reports page (`SalesReport`, over
  yesterday and the day before, no SQL of its own). Per currency, in a block of its own (amounts of different
  currencies are never added): revenue and, next to it, the change from the day before in whole percent (up, down,
  "the same" when the two amounts are equal, "about the same" when they differ by under half a percent, or "new" when
  the day before had no sales in that currency; nothing when neither had). The words never say more than the amounts: a
  day that had sales is at most "down 99%" ("down 100%" is a day with none), and a rise past 999% is "up more than
  999%", so a day after almost nothing does not read "up 500000000%". Then paid orders (new,
  renewals, traffic packages), refunds (count and amount), net after refunds, wallet top-ups and, **only when there are
  some**, payments staff rejected and failed or expired gateway payments. Then the people (new customers, free trials)
  and the time zone the day was read in. A currency that did nothing that day (not even a refund, a top-up or a
  failed payment) is left out. A day with nothing at all to report (no sale, refund, top-up, rejected or failed
  payment, new customer or trial) is **one line**, "No sales on 13 Oct 2026.", so the owner sees the job is alive. The
  message is in each person's own language, Persian digits and the Jalali date in Persian, and money as the bot shows it
  everywhere. It holds no names, customers, plans, order or payment ids.
- **Who.** Active staff who hold `reports.read` (owner and admin, as the dashboard's permission table says: core is
  given that table, it does not copy it) and have a Telegram account, each in their own language. Support staff,
  customers, banned staff and anyone demoted or removed **before the day is claimed** get nothing: the list is read in
  the claim's own transaction. The bot checks again before it sends, because an event can wait (the bot or its consumer
  down, a retry): when it knows the staff list and the person is no longer on it, or is on it as support staff, the
  event is dropped and logged, so someone demoted, banned or removed after the claim does not get the revenue figures
  when delivery resumes (the bot does not have the permission table: it knows only that support staff never hold
  `reports.read`; a staff member added in the last minutes, before the bot refreshed its list, misses that day's
  message). A staff list the bot does not know yet decides nothing. It does not follow `notify.recipients`, which is for
  alerts. With nobody to tell, nothing is claimed, so whoever becomes an admin later the same day still gets it.
- **When.** Core's worker looks at the clock once a minute, in the store's time zone (the same chain as the reports:
  `general.timezone`, else `BOBRES_TIMEZONE`, else Asia/Tehran; a value that is no zone is logged once and skipped).
  It sends yesterday's summary when all three hold: the summary is on and was switched on **at or before the time
  of day it is due**; the local clock has reached that time; and yesterday has no claim yet. The time is never read as
  earlier than **00:02**, whatever the store chose (00:00 is read at 00:02): a purchase that began just before midnight
  and commits a moment after it is dated the old day, and reading at 00:00 would leave it out of a summary the claim then
  makes final. The Reports page keeps showing the time the store chose.
- **Queued exactly once.** The claim is a row in `core.daily_summary_claims` (migration 00019: `day` primary key, `zone`,
  `recipients`, `claimed_at`; `Down` drops it), keyed by the local date the summary is **about**, written in the
  **same transaction** as the outbox events. A crash before the commit leaves neither (it is sent a minute later), one
  after leaves both; a restart finds the claim; two workers at once or two core processes cannot both win
  (`INSERT ... ON CONFLICT DO NOTHING`; the loser of a claim still open waits for it and gets it if it rolls back).
  That is the **queueing**; delivery is **at least once**, as for every event (see Delivery).
- **Catch-up.** Core down at the due time: the summary goes out as soon as it is back, the same local day, once. Core
  down for a day or more: only **yesterday's** summary is sent when it is back, never one for each day it missed.
  Switching the summary on after its time of day has passed does not make up for anything: the first summary is the next
  day's (it is for the day it was switched on); switched on before that time it goes out today. Turning it off and on
  again after the time works the same way, and saving the settings page again with the same values does not count as
  switching it on. The bot adds its own limit (see Delivery): a summary it could not send within a day of its being
  queued is dropped. Changing the time to a later one waits for it; to one that has already
  passed it sends once, now, if today's was not sent yet; once sent, no change of the time sends it again.
- **Daylight saving and clocks.** The comparison is of the local wall clock, so a time that does not exist on the day
  clocks go forward (02:30 when they go from 02:00 to 03:00) is reached when they land on 03:00, and one that happens
  twice (01:30 when they go back) is reached the first time and not again. The days that have 23 or 25 hours are
  summed as whole local days, by the same date test as the reports. A clock set back never repeats a summary (the
  claim); set back across midnight to a day whose summary was never sent, it sends that one, once. A clock set forward
  sends the day it lands on, once. A **change of the store's time zone** moves the day edges and the local date, and the claim
  is by local date, so around the change the summaries are not a clean cut: a sale can be in two summaries (Tehran sent
  the 13th, the store moves to Auckland, whose 14th starts 9½ hours before Tehran's 13th ended) or in none (Auckland sent
  the 14th, the store moves to Los Angeles before its 15th: the 14th is claimed, so Los Angeles's 14th is not sent, and a
  sale between the end of Auckland's 14th and the start of Los Angeles's 15th is in no summary). No date is sent twice.
  The worker logs a warning, with both zones, when it queues the first summary read in a zone other than the last
  summary's; the Reports page has every day in the current zone to settle a figure.
- **Failures.** A summary that cannot be made (the report hits its statement timeout on a big ledger, the database
  is down, a time zone the database does not know) is not made again every minute until midnight: after a failed attempt
  the worker waits 5 minutes, then 10, then 15 between attempts, and gives up on that day after 8 attempts (about an
  hour and a half; one line at error level says so, and each failed attempt is a warning with its attempt number and
  the wait).
  The next day is a fresh start (yesterday's summary goes at the usual time), and so is a restart of core.
- **Delivery.** Core publishes one `report.daily_summary.v1` event for each recipient (see
  [02-events-and-apis.md](02-events-and-apis.md)); the bot turns it into the message. Delivery is **at least once**:
  the bot applies a batch of up to 100 events and acknowledges it once, so a bot killed (SIGKILL, out of memory) after it
  sent the summary and before the acknowledgement sends the same message again when it restarts, as for every notifier
  event. The bot does not remember event ids, so it cannot tell. A recipient who blocked the bot is skipped (and marked,
  as for any notification). The events of a feed are applied **in order** and a failing one is retried before the ones
  behind it, so a failing recipient does hold up everything behind it (customers' subscription links and payment
  confirmations too) until it is sent or dead-lettered. Therefore a refusal no retry can change (Telegram answers a
  4xx about this chat or message other than a blocked bot, a flood limit, a refused token or a reply that is not
  Telegram's: say `PEER_ID_INVALID` or "have no rights to send a message") is marked permanent and **dead-lettered at
  once**, and only a failure that may pass (the network, a flood limit, a server error) is retried, with the consumer's
  back-off, until its 20th failure dead-letters it. A summary the bot is asked to send more than a day after core queued
  it is dropped and logged (a bot that was down for days sends the newest summary, not every day it missed, back to
  back). The events are written once per day: there is no retry of the day itself.
- **Tests.** `internal/core/domain/daily_summary_test.go` runs the worker with a fake clock (off by default, once a
  day, restart, outage, a changed time, switching on after the time, saving the settings again, the two minutes after
  midnight, a failing report and its back-off, time zones (and a change of zone, both ways), daylight saving days in
  America/New_York, the figures against a model kept in Go values in two currencies, a quiet day, who is told, the
  claim and the events in one transaction, two workers); `internal/core/store/daily_summary_test.go` the claim
  (concurrent claimers, a claimer waiting for an open one, whatever the pool size) and the last zone;
  `internal/migrate/core_daily_summary_test.go` the migration up, down and up;
  `internal/bot/notify/daily_summary_test.go` the messages in both languages, the comparison's words, the delivery
  with a recipient who blocked the bot, which failures are permanent (and, with a real consumer, that a refused
  summary does not hold up the events behind it), a recipient who is no longer staff and a summary that waited too
  long; `cmd/core/daily_summary_test.go` that the role test core is given is the dashboard's permission table.

## API

All under `/api/v1/reports`, `GET`, session cookie, permission `reports.read`. Query: `range` or `from` + `to`,
`group` (summary only), `churn_grace_days` (summary and its CSV: 0 to 90, 7 when missing; anything else is a 400), `calendar` (`gregorian` or `jalali`; summary, and the CSV files as the summary's), `tz`.

| Endpoint | Answer |
|---|---|
| `/daily-summary` | `{enabled, time, tz}`: whether the daily summary in Telegram is on, its time (`HH:MM`) and the store's zone (ignores the range) |
| `/summary` | `{from, to, group, calendar, tz, churn_grace_days, currencies, buckets, totals}`. A bucket is `{date, partial, new_customers, trials, churned_services, churned_customers, at_risk, currencies}`; `currencies` maps each currency to `{revenue, refunds: {count, amount}, net, orders, new_orders, renewals, traffic_topups, topups: {count, amount}, rejected: {count, amount}, gateway_failed: {count, amount}, cancelled: {count, amount}}` (`rejected` is by staff, `gateway_failed` is the gateway line). `totals` has the same shape, summed bucket by bucket within each currency. |
| `/plans` | `{from, to, tz, plans: [{plan_id, name: {fa, en}, currency, orders, revenue}]}` |
| `/methods` | `{from, to, tz, methods: [{method, provider, currency, orders, revenue}]}`; `provider` is always there, empty for wallet, unknown and orders paid before it was recorded |
| `/plan-series` | `{from, to, group, calendar, tz, series: [{plan_id, name: {fa, en}, currency, orders, revenue, points: [{date, orders, revenue}]}]}`: what each plan sold in each bucket of the report, per currency (takes `group` and `calendar` like the summary, and its buckets are the summary's: a Jalali week or month is cut the same way, `date` is the bucket's Gregorian start). Only the buckets where a plan sold are listed. A currency's best 20 plans come one by one (the order and the totals of `/plans`) and the rest together as one row with `plan_id: ""` and no name, so a bucket's rows add up to its revenue (when every purchase has its order and plan, as `/plans` assumes). `[]` when nothing was sold. |
| `/hours` | `{from, to, tz, hours: [{weekday, hour, currency, orders, revenue}]}`: paid orders by ISO weekday (1 Monday to 7 Sunday) and hour of the local day (0 to 23, the wall clock in `tz`), per currency, only where something was sold. It reads the same purchase entries as the revenue. |
| `/summary.csv` | The summary as a file (`...-jalali.csv` when `calendar=jalali`, with the Jalali days in its name). |
| `/orders.csv` | Every order paid in the range, as a file. |

`ledger.csv` (`ledger.read`), `audit.csv` (`audit.read`) and the two gift code files (`discounts.write`, see the Gift codes
page) follow the same rules and take the same `tz` and `calendar` (see below).

### CSV files

Six files are what the product exports as CSV from core: `summary.csv` and `orders.csv` here, `ledger.csv`
(`ledger.read`), `audit.csv` (`audit.read`, with its own page) and, on the Gift codes page (`discounts.write`),
`gift-codes.csv` and a code's `redemptions.csv`. They share one set of rules (`internal/core/web/csvcells.go`
and `csvexport.go`). (The dialog that shows new gift codes once also offers them as a file; the dashboard writes that
one itself, in the browser, with the same quoting, and core never holds the codes.) Who may export: the permission of the thing exported, which owner and admin hold and support does
not (403; nobody without a session either, 401); a reseller is no staff at all. Every export is written to the audit
log as `export.reports`, `export.ledger`, `export.audit`, `export.gift_codes` or `export.gift_redemptions` when it ends, with its filters, range, calendar, zone, number
of rows and outcome: `complete`, `incomplete` (the file ended short) or `failed` (nothing was sent). A file holds no
secret: no session, token, link or key of the dashboard, a bot, a gateway or a server is in any of them (the audit log
records a changed token as "changed", never its value; a test looks for the session cookie, the CSRF token and a login
link's token in every file).

- **The file.** `text/csv; charset=utf-8`, `Content-Disposition: attachment`, `Cache-Control: no-store`, a UTF-8
  byte-order mark (spreadsheet programs then read Persian right), commas between cells, double quotes around a cell
  that holds a comma, a quote (doubled) or a line break, and `\r\n` after every row **and** for any line break inside a
  cell (a cell's `\n`, `\r\n` or `\r` all become one `\r\n`): RFC 4180, what Excel writes itself, and never a mix of two
  kinds in one file. Rows are streamed from the database one at a time and pushed out every 500 rows, so memory does
  not grow with the file.
- **File names** keep to letters, digits, dot, dash and underscore (anything else would become a dash) and carry the days
  the file covers, in the calendar of the file: `reports-summary-2026-10-31-to-2026-11-03-by-day.csv`,
  `reports-orders-2026-10-31-to-2026-11-03.csv`, `ledger-2026-10-14-to-2026-10-20.csv` (`ledger-all-to-2026-10-20.csv`
  when nothing starts it; the days of its `from` and `to` filters, in the store's zone), `audit-...`. A Jalali file has
  the Jalali days and ends in `-jalali` (`reports-summary-1405-08-09-to-1405-08-12-by-day-jalali.csv`).
- **Headers** are English `snake_case` names, the same in both languages of the dashboard. A file is read by scripts,
  pivot tables and other people's spreadsheets, and a column named in Persian for one person and in English for another
  would break all of them. They are the file's contract and a test pins every one: **a column is only ever added at the
  end**, so earlier columns keep their positions.
- **Text** (names, usernames, plan names, reasons, ids, the JSON of an audit entry): a cell that a spreadsheet could run
  as a formula gets a single quote in front. That is a cell that starts with `=`, `+`, `-`, `@`, a tab or a carriage
  return, or one of the four after spaces, line breaks, control characters or invisible marks (a right-to-left mark in
  front of an `=` is still an `=`). A text longer than 32,000 characters is cut and ends in ` …[cut]`: a spreadsheet keeps
  32,767 and moves the rest into the next row, which would break the file's rows. **Numbers are never quoted**, so a
  negative amount stays a number (`-100000`).
- **Money** is a plain decimal in the currency's major unit, exact: `100000` for Toman (no decimals), `2.500000` for
  USDT (six, never rounded: the dashboard shows two), `.` for the decimal point, no thousands separator, no symbol. The
  currency is a column of every row that has money (`currency`), and amounts of two currencies are never in one cell.
  Add a column up only after choosing one currency (a plain sum of `revenue` adds Toman to USDT: that is also why there
  is no totals row). A spreadsheet whose regional settings use another decimal separator reads `2.500000` as text: import
  with Data, From Text/CSV and choose English (United States).
- **Dates follow one rule.** The machine columns never change with the calendar. An **instant** is UTC, ISO 8601 to
  the second with a `Z` (`2026-10-31T20:29:00Z`: `paid_at_utc`, `time_utc`). A **day** of a report is a Gregorian ISO
  date in the store's zone (`period_start`, `2026-10-31`). The same instant on the **store's wall clocks** is
  `YYYY-MM-DD HH:MM:SS` (`paid_at_local`, `time_local`: `2026-10-31 23:59:00`; a space and not a `T`, so a spreadsheet
  reads it as a date), and every file that has local times or days has a `tz` column naming the zone (`Asia/Tehran`): a
  local text alone cannot tell the two 01:30 of the night the clocks go back, the UTC column can. The zone is the
  request's `tz`, else the store's (`general.timezone`, `BOBRES_TIMEZONE`, `Asia/Tehran`), as for the reports; an unknown
  one is a 400. **For a person**, when the calendar is Jalali (`calendar=jalali`, which is what the Reports page sends
  when it is set to Jalali; `ledger.csv` and `audit.csv` take the same parameter) there is **one more column at the very
  end**, the Jalali date written with Latin digits so that it sorts and filters: `YYYY-MM-DD` for a day
  (`period_start_jalali`: `1405-08-09`) and with the local time for an instant (`paid_at_jalali`, `time_jalali`:
  `1405-08-09 23:59:00`). It turns over at the store's local midnight, like the day buckets. A Gregorian file has no such
  column: its machine columns are already Gregorian. `calendar` other than `gregorian` or `jalali` is a 400 in every file.
- **Nothing is sent until the file has its first row.** A failure before that (the range holds too much to read in
  time, the database is down) is an error answer (503 with `timeout` or `unavailable`), never a 200 with only a header
  that looks like "no orders". A failure after rows went out ends the file with a line `# export incomplete: <reason>`.
  An export of nothing is the header alone (`summary.csv` has a row of zeros for each period instead, as the page does).
  At most 100,000 rows go into `orders.csv`, `ledger.csv`, `audit.csv`, `gift-codes.csv` and a code's `redemptions.csv`, and a last line asks for a shorter range or a
  narrower filter. At most two exports run at once (429 and `Retry-After` for a third), for at most 60 seconds.

Columns, in order:

- `summary.csv`: one row per bucket and currency (`period_start`, `partial_period`, `currency`, `revenue`,
  `refund_amount`, `net`, `paid_orders`, `new_orders`, `renewals`, `traffic_topups`, `topup_count`, `topup_amount`,
  `refund_count`, `rejected_count`, `rejected_amount` (by staff), `gateway_failed_count`, `gateway_failed_amount`,
  `cancelled_count`, `cancelled_amount`; and, at the very end of the file, after `tz` and the Jalali column, `license_orders`, `license_revenue`, `digital_orders`, `digital_revenue`: the part of `paid_orders` and `revenue` that licenses and digital accounts made), and one row per bucket with an **empty currency** that holds `new_customers`
  and `free_trials` (the counts of people), so no column adds up two currencies or counts a person twice. The people row
  also holds the churn counts, `churned_services`, `churned_customers` and `at_risk_services` (empty on the currency
  rows). Then, the same on every row of a file: `group` (`day`, `week`, `month`), `calendar` (`gregorian`, `jalali`: how
  weeks and months were cut) and `tz`; and `period_start_jalali` in a Jalali file. There is no totals row: the rows of a
  currency add up to the page's totals for that currency, in every grouping and both calendars (a test sums the file
  and compares it with the report).
- `orders.csv`: `paid_at_utc`, `paid_at_local`, `order_id`, `type`, `telegram_id`, `username`, `plan_fa`, `plan_en`,
  `amount`, `currency`, `paid_with` (wallet, manual, gateway, or empty), `refunded_amount`, `provider` (the provider
  that took the payment, empty for wallet, trial and free orders and for orders paid before it was recorded), `tz`, and
  `paid_at_jalali` in a Jalali file; oldest first. What the orders cost adds up to the report's revenue: a license has `type` `license` and a digital account `type` `digital` (its name and quantity in `plan_fa` and `plan_en`), both paid with the wallet.
- `ledger.csv` (newest first, the ledger page's filters, `?user=`, `kind`, `currency`, `from`, `to`): `time_utc`,
  `telegram_id`, `username`, `kind`, `amount` (signed: a purchase is negative), `currency`, `balance_after`, `ref_type`,
  `ref_id`, `entry_id`, `time_local`, `tz`, and `time_jalali` in a Jalali file.
- `gift-codes.csv` (newest first, the Gift codes page's `status` and `q` filters; the file name is `gift-codes-all-to-<day>.csv`): `created_utc`,
  `code_last4`, `kind` (`custom` or `generated`), `amount`, `currency`, `max_redemptions`, `per_customer_limit`, `redeemed`,
  `status` (`active`, `disabled`, `expired`, `used_up`), `expires_utc`, `last_redeemed_utc`, `note`, `created_by_telegram_id`,
  `code_id`, `created_local`, `tz`, and `created_jalali` in a Jalali file. It never holds a code, only its last four characters.
- A code's `redemptions.csv` (newest first; `gift-code-<last four>-redemptions-all-to-<day>.csv`): `time_utc`, `telegram_id`, `username`,
  `number` (the customer's n-th use of the code), `amount`, `currency`, `balance_after`, `redemption_id`, `time_local`, `tz`, and
  `time_jalali` in a Jalali file.
- `audit.csv` (newest first, the audit page's filters): `time_utc`, `actor_telegram_id`, `actor_username`, `actor_role`,
  `action`, `entity`, `entity_id`, `reason`, `ip`, `source`, `before`, `after` (JSON), `entry_id`, `time_local`, `tz`,
  and `time_jalali` in a Jalali file.

What was looked at and **left as it is**, and why: the English headers (above); the six decimals of USDT (a file must be
exact; the page rounds for the eye); `paid_at_local` as `YYYY-MM-DD HH:MM:SS` rather than ISO 8601 with an offset (a
spreadsheet reads it as a date-time, and the zone and the UTC column make it exact); the `# ...` note lines at the end of
a file that is not whole (a script sees a row with one cell; an unmarked short file would be worse); a download link
that fails shows the browser's own "failed" and not a translated message, since a link cannot carry one.

## Indexes

Migration `00019_daily_summary.sql` adds the summary's claim table and no index of its own; the reports are not touched
by it.

Migration `00020_churn_index.sql` adds `subscriptions_expires_idx` (`expires_at` of the services that have an expiry and are not pending or deleted), which the churn queries read by range; `Down` drops it. A test fails if the planner stops using it.

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
