# Roadmap

What comes after v0.1.0, in order. Each item says what it is, why now, how big it is, what it needs and when it is done.
Sizes: S is a day or two of work, M about a week, L a few weeks. The order follows the owner's picks (2026-10-14)
and the effort: the cheaper pieces that help selling come first.

Nothing here is promised for a date. The first public release is v0.1.0 ([CHANGELOG.md](../CHANGELOG.md)); this list
starts with v0.1.x, the fixes and checks that only a real server can give, and then v0.2.0.

## v0.1.x: after the first install on a real server

These cannot be done in the Docker lab ([11-staging-run.md](11-staging-run.md)).

| Item | Size | Done when |
|---|---|---|
| Real Let's Encrypt issuance, including the server IP mode, on a public server | S | A certificate for the domain and one for the IP are issued, renewed once, and the alerts stay quiet |
| Real payments in the field: a Zarinpal merchant id, Telegram Stars, card to card, automatic USDT | S | One real purchase per method, delivered, with the receipt flow checked |
| A native Persian speaker reviews every bot and dashboard text | S | Edits merged; the texts need no change for a month |
| `SECURITY.md`, a security contact, a support channel | S | The files exist and the contacts answer |
| The sales flow dry run with a friend (issue a license, activate, renew, lapse) | S | [launch-checklist.md](launch-checklist.md) item ticked |
| Image signature check on the server (Postgres and Redis come from Docker Hub by tag) | M | `bobres install` verifies every image it pulls, or pins them by digest |

## v0.2.0: in the owner's order of picks and effort

### 1. Sales reports and analytics (M) — first
- **What:** revenue by day, week and month; new customers, renewals and churn; top plans; wallet top-ups; refunds and
  rejected payments; a daily summary message to the owner in Telegram; CSV export.
- **Built (the MVP, [14-reports.md](14-reports.md)):** a read-only Reports section for staff with `reports.read` (owner
  and admin): revenue per day, week or month per currency (never summed), paid orders, new customers, renewals, free
  trials, wallet top-ups, refunds, payments staff rejected, failed or expired gateway payments (apart) and cancelled
  orders, a chart per currency, top plans, how orders were paid (wallet, staff-approved, gateway), CSV files of the
  summary and of the paid orders. The numbers equal the ledger to the last unit in a test with seeded data in several
  time zones, in fa and en. Migration 00018 adds three indexes.
- **Built since:** the daily summary message in Telegram (`notify.daily_summary`, [14-reports.md](14-reports.md)).
- **Built since:** which provider paid each order (Zarinpal, Stars, automatic USDT, card, crypto; recorded on
  `order.paid.v1`, older orders show "provider not recorded"); the ledger and audit CSV files now follow the reports'
  export rules (late header, deadline, "export incomplete" note, shared slots, audit with outcome).
- **Built since:** Jalali weeks and months (`calendar=jalali`).
- **Built since:** churn (final-expiry with a 7-day grace, customers lost, services at risk; migration 00019). Still
  open: a churn rate from a stored history, churn in the daily summary, revenue by customer.
- **Why first:** it is the cheapest piece, needs no new service, and the owner and the first customers want numbers
  from day one. It also shows the paid tiers' value.
- **Risks:** time zones and the Jalali calendar in the numbers; money in several currencies (Toman, USDT, Stars) must
  never be added together without a stated rate.
- **Done when:** the numbers match the ledger to the last unit in a test with seeded data, in fa and en (done), and the
  daily summary arrives once (done).

### 2. Branded subscription page (M)
- **What:** a page per customer service under the store's own brand: usage and days left, the subscription link and
  QR, a button per VPN app (v2rayNG, Hiddify, Streisand, V2Box), and a "renew" link back to the bot.
- **Why:** fewer support tickets, and customers see the store, not the panel. The Caddyfile already routes `/sub/*`
  to core; core has no handler yet.
- **Needs:** a public, unguessable URL per service; no personal data in the page; rate limits; the store's name,
  colour and logo from Branding; app guides as editable texts.
- **Risks:** the link must not leak the 3x-ui subscription id more than the customer already has it; the page must work
  without JavaScript on poor connections.
- **Done when:** a customer opens it from the bot on a phone, in both languages, and imports the config into an app.
- **Status: built** ([15-subscription-page.md](15-subscription-page.md), migration 00020). Open: check the app deep
  links on real devices.

### 3. More Iranian payment gateways (M) — built, see [16-idpay-nextpay.md](16-idpay-nextpay.md)
- **What:** automatic card gateways beyond Zarinpal (for example IDPay and NextPay), behind the same gateway interface,
  with the same review, callback and refund flow.
- **Why:** more ways to pay raises conversion; some customers cannot use Zarinpal.
- **Needs:** merchant accounts and sandbox credentials from the owner for each gateway; the provider's rules on VPN
  sales ([09-legal-questions.md](09-legal-questions.md)); the payments service's proxy and second-domain support.
- **Risks:** a provider may refuse VPN merchants; each gateway has its own callback quirks; keep each off until its
  merchant id is set, as Zarinpal is.
- **Done when:** a sandbox payment settles, a failed and an abandoned payment are handled, and the payment is verified
  with the provider, never trusted from the callback alone.

### 4. Telegram Mini App store (L) — built, see [17-mini-app.md](17-mini-app.md)
- **Status: built.** Left to do: a check with a real Telegram (sign-in, payments, theme, Persian layout). Not built: Stars
  and card-to-card payments, trials, referrals and support inside the app.
- **What:** the store as a Telegram web app: browse plans, buy with the wallet or a gateway, see and renew services,
  open the subscription page, all in one screen instead of chat buttons.
- **Why last:** the largest piece, and it needs an HTTPS domain; the bot already sells everything it would. It pays
  off when the number of plans and options makes chat menus slow.
- **Needs:** a Vue app served by core under a path of its own; Telegram's `initData` signature checked on every request;
  an API that reuses the bot's domain logic (no second implementation of prices, limits or payments);
  `setChatMenuButton` with the web app URL; the same branding and texts.
- **Risks:** authentication by `initData` only (short expiry, replay checks); a second UI to keep in sync; Mini Apps
  need the public domain and a trusted certificate (not the lab's).
- **Done when:** a customer buys and renews from the Mini App on iOS and Android, and the bot flow still works
  unchanged.

## Also built in this batch

- A status screen on `bobres`, `bobres check`, `bobres admin password` with an initial password and owner login
  without /start, a per-plan inbound choice, the first panel named by its host, the admin menu in the bot by buttons,
  and the BOBRES logo as the default bot photo. See the changelog; the open checks are launch-checklist steps 33 to 37.

## Later, not yet ordered

- More bot languages (Arabic, Russian, Turkish): the texts are already keys, so the work is translation and review.
- TON payments; more crypto networks, if a way that works from Iran exists.
- Metrics for operators (Prometheus endpoint, a Grafana example) and an uptime page per server.
- Automatic node balancing and per-server capacity limits beyond the weights in use today.
- Two-factor sign-in for staff inside the bot, and an audit-log export.
- A hosted offering, so a customer who does not want to run a server can pay for one.
- An affiliate program for BOBRES itself, on top of the license server.
- An adapter for a newer 3x-ui major version, when one is released and tested in CI.

## How an item gets built

1. A short spec in `docs/` (scope, API, data, security, tests), reviewed by the owner.
2. A branch and a pull request; independent reviewers read the diff for correctness, security and the texts.
3. CI green, then merged; the release notes say what changed.
4. A release candidate on the lab, then a release.

Ideas and changes of order go in this file; the plan's decisions stay in [PLAN.md](PLAN.md).
