# Roadmap

What was planned after v0.1.0, what is built and what is still open. Each item says what it is, why now, how big it is,
what it needs and when it is done. Sizes: S is a day or two of work, M about a week, L a few weeks. The order follows the
owner's picks (2026-10-14) and the effort: the cheaper pieces that help selling come first.

Nothing here is promised for a date. Released so far ([CHANGELOG.md](../CHANGELOG.md)): v0.1.0 (2026-10-14, the first
public release), v0.1.1, v0.2.0 (sales reports), v0.2.1 (the daily summary), v0.3.0 (the installer and the command line),
v0.4.0 (2026-10-17: Marzban panels, the subscription page, the Mini App, IDPay and NextPay, churn and Jalali reports,
and the owner's tools), v0.5.0 (2026-10-20: several owners, a list of the payments to review, Marzban tested against
a real panel), v0.6.0 (2026-10-20: the Mini App on by default with Stars, receipts, trial, invites, support and a
reseller panel; reseller management; the 3x-ui group `bobres_bot`), v0.6.1 (2026-10-20: the Mini App holds the
store's required channel and takes a half-made manual payment up again) v0.7.0 (2026-10-20: updating from the
dashboard, the Reports calendar and the CSV files, maintenance mode in every option, and the growth features of the bot)
v0.8.0 (2026-10-20: server monitoring, charts and a 3D report, backup download and restore from the dashboard, a full
uninstall, the help center, the plan table, bulk purchase, gift codes and several required channels)
and v0.9.0 (2026-10-21: selling BOBRES licenses from the owner's own bot, the signed admin API of the license server and the
Licenses page of the dashboard)
and v0.9.1 (2026-10-21: every license of the license server managed from the dashboard)
v0.10.0 (2026-10-21: reselling a supplier's accounts, and the lock on the vendor's own features)
v0.10.1 (2026-10-21: the supplier balance always in view)
and v0.11.0 (2026-10-21: plan groups with a text each)
v0.12.0 (2026-10-22: a reseller's group and prefix in the 3x-ui panel, and 3x-ui v3.10 tested)
v0.13.0 (2026-10-22: reward codes, sold-out digital accounts, the owner's menu buttons and find a service by its link)
v0.14.0 (2026-10-23: reward amounts, deleting a plan, digital accounts by category, the live USDT price, the store's own Terms and Privacy pages, and Karing and NPV Tunnel)
v0.15.0 (2026-10-23: buying a license in one screen, restoring a deleted plan, Undo in the bot's admin menu, a project wiki, a native read of the newest Persian texts)
v0.16.0 (2026-10-23: digital accounts in the Mini App, and the rate shown beside a USDT amount)
v0.16.1 (2026-10-23: the supplier's product dialog fits a phone)
v0.17.0 (2026-10-23: one wallet in Toman, the renewal rule, reports that count every sale and start from zero, the reseller's search by link)
and v0.17.1 (2026-10-28: the store comes back by itself after a reboot, a heartbeat the tls-agent can write after a restart, and alerts that say what to type).

## Where things stand (after v0.17.1)

Everything in the owner's four picks below is built, and so is the owner's list of requests of 2026-10-14 and of 2026-10-20 (the Mini App on by default and complete, reseller management, the 3x-ui
group).

Also built in v0.7.0, from the owner's requests of 2026-10-20: **updating from the dashboard** (System > Updates checks
again for real, follows a stable or beta channel, and updates the server through a root helper on the host that runs the
same signed `bobres update`; the first update to v0.7.0 itself is still made over SSH), the **Reports calendar** that
converts every date and the reviewed **CSV files** ([14-reports.md](14-reports.md)), and **maintenance mode** that holds
every option of the bot.

Built in v0.7.0 (the owner picked them on 2026-10-20 from another store's bot, see
[03-bot-ux.md](03-bot-ux.md)): the **Top users** board of the customers who invited the most people, with names and
Telegram ids masked and an owner switch (`growth.leaderboard.enabled`); the account lines (Telegram id, number of
services, join date) on the wallet screen; a **card to forward** in Invite friends; the commands `/new`, `/renew` and
`/status` in the bot's command menu; and the **reseller application** (a button for customers, a ticket in a new
"reseller" category, an alert to staff, the category in the dashboard's inbox and in the Mini App).

Built after v0.17.1 (unreleased, the security release): **approving dashboard sign-ins in Telegram** (Settings > Sign-in security, owner only,
off by default: a staff sign-in gets no session until the staff member or an owner taps Approve in the bot, with the way out
`sudo bobres admin approval off`; [04-dashboard.md](04-dashboard.md), [runbooks/login-approval.md](runbooks/login-approval.md)). This
is the "two-factor sign-in inside the bot" item of the list below.

What is still open:

- **Checks only the real world can give:** the table below (a real certificate, real payments, a native Persian
  review); the subscription page's app links on real devices; the Mini App in a real Telegram. (Marzban was on this
  list: it is now tested against the real 0.8.4 image in CI; Marzban 1.0 is not supported yet.)
- **Built in v0.8.0, from the owner's requests of 2026-10-20:** the **two-column plan list**, the **bulk purchase**,
  **gift credit codes**, the **help center** (an owner-editable FAQ and tutorials), **several required channels and
  groups** (one on the free tier and Standard, any number on Pro), **server monitoring** in the dashboard (the host,
  Docker, Kubernetes pods when there are any, services, CI/CD, alerts and logs, one overview), **charts and a 3D
  professional report** on the Reports page, **downloading and restoring a backup** from the dashboard, and a full
  **uninstall** that leaves a server as on day one (`sudo bobres uninstall --purge`). See the changelog for what has
  and has not been tried for real.
- **Still on the list, not built:** the Aqayepardakht gateway; S-UI and Hiddify panels, if operators ask; growth tools
  (cashback, a lottery or wheel, reseller targets: gift codes are built); more bot languages (Arabic, Russian, Turkish); per-server
  capacity limits; a Prometheus endpoint and an uptime page; Persian documentation generated from the code. (Two-factor
  sign-in inside the bot is built: see above.)
- **The owner's business track** (not code): the lawyer's review, the brand checks, the prices, the license server and the
  first customers ([launch-checklist.md](launch-checklist.md)).

## v0.1.x: after the first install on a real server

These cannot be done in the Docker lab ([11-staging-run.md](11-staging-run.md)).

| Item | Size | Done when |
|---|---|---|
| Real Let's Encrypt issuance, including the server IP mode, on a public server | S | A certificate for the domain and one for the IP are issued, renewed once, and the alerts stay quiet |
| Real payments in the field: a Zarinpal merchant id, Telegram Stars, card to card, automatic USDT | S | One real purchase per method, delivered, with the receipt flow checked |
| A native Persian speaker reviews every bot and dashboard text | S | Edits merged; the texts need no change for a month |
| A support channel and the contacts the legal texts name (`SECURITY.md` exists and sends reports to GitHub's private form) | S | The contacts exist and answer a test report ([launch-checklist.md](launch-checklist.md) item 15) |
| The sales flow dry run with a friend (issue a license, activate, renew, lapse) | S | [launch-checklist.md](launch-checklist.md) item ticked |
| Image signature check on the server | M | Done by pinning: Postgres and Redis are pinned by digest in `deploy/docker-compose.yml` (the backup image's base too), as Caddy is, and a test keeps them pinned. Open: the digests move only by hand when we bump a version, and Redis 7.4 is under the post-7.2 license ([09-legal-questions.md](09-legal-questions.md)) |

## The owner's picks, in order of effort (all four built, v0.2.0 to v0.4.0)

### 1. Sales reports and analytics (M) — first
- **What:** revenue by day, week and month; new customers, renewals and churn; top plans; wallet top-ups; refunds and
  rejected payments; a daily summary message to the owner in Telegram; CSV export.
- **Built (the MVP, [14-reports.md](14-reports.md)):** a read-only Reports section for staff with `reports.read` (owner
  and admin): revenue per day, week or month per currency (never summed), paid orders, new customers, renewals, free
  trials, wallet top-ups, refunds, payments staff rejected, failed or expired gateway payments (apart) and cancelled
  orders, a chart per currency, top plans, how orders were paid (wallet, staff-approved, gateway), CSV files of the
  summary and of the paid orders. The numbers equal the ledger to the last unit in a test with seeded data in several
  time zones, in fa and en. Migration 00018 adds three indexes.
- **Built (the daily summary):** an opt-in message in Telegram (`reports.daily_summary`, off by default, at
  `reports.daily_summary_time`, 09:00 in the store's zone) with yesterday's sales per currency, the day-before
  comparison, refunds, top-ups, new customers and trials, to every owner and admin in their own language, queued exactly once
  a local day, with catch-up after downtime, no flood after an outage and no make-up for days before it was switched
  on. A day with no sales sends one line. Migration 00019 adds its claim table
  ([14-reports.md](14-reports.md#the-daily-summary-in-telegram)).
- **Built since:** which provider paid each order (Zarinpal, Stars, automatic USDT, card, crypto; recorded on
  `order.paid.v1`, older orders show "provider not recorded"); the ledger and audit CSV files now follow the reports'
  export rules (late header, deadline, "export incomplete" note, shared slots, audit with outcome).
- **Built since:** Jalali weeks and months (`calendar=jalali`).
- **Built since:** churn (final-expiry with a 7-day grace, customers lost, services at risk; migration 00020). Still
  open: a churn rate from a stored history, churn in the daily summary, revenue by customer.
- **Why first:** it is the cheapest piece, needs no new service, and the owner and the first customers want numbers
  from day one. It also shows the paid tiers' value.
- **Risks:** time zones and the Jalali calendar in the numbers; money in several currencies (Toman, USDT, Stars) must
  never be added together without a stated rate.
- **Done when:** the numbers match the ledger to the last unit in a test with seeded data, in fa and en (done), and the
  daily summary is queued once (done).

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
- **Status: built.** Left to do: a check with a real Telegram (sign-in, payments, theme, Persian layout). Everything
  the bot sells is in the app: the wallet and the online gateways, Telegram Stars, card to card, USDT and a Zarinpal
  link with a receipt (a payment left half-way is taken up again from the wallet), the free trial, invites and support.
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

## Also built in v0.4.0

- A status screen on `bobres`, `bobres check`, `bobres admin password` with an initial password and owner login
  without /start, a per-plan inbound choice, the first panel named by its host, the admin menu in the bot by buttons,
  and the BOBRES logo as the default bot photo. See the changelog; the open checks are launch-checklist steps 33 to 37.

## The owner's requests of 2026-10-14 (built in v0.4.0)

Asked for while testing on the first real server. All of it is built in v0.4.0 (see the changelog), apart from what is marked "not built" below. The table keeps the original wording of each request.

| Request | What it means |
|---|---|
| A live status screen when `bobres` opens, and a much fuller option 1 | The menu opens with the status already shown (version, domain, each service and its health, the bot's @username, every 3x-ui panel with a live check, license, disk, memory, last backup, certificate expiry); option 1 adds uptime, memory, versions, counts and the last errors |
| Dashboard login at once, with a password shown and changeable from the CLI | Core creates the owner's account at its first start (today the owner must send `/start` to the bot first, and `bobres admin link` fails until then); the installer shows the login details once at the end; `bobres admin password` changes it. Password login keeps its authenticator code (2FA) by design |
| The dashboard works without any 3x-ui panel | Already true (the panel step can be skipped); the installer summary should say so, and a test should pin it |
| `bobres check`, a self-test of every part | One command: bot connection, dashboard, core, database, Redis, payments, provisioner, a live authenticated call to every 3x-ui panel with its saved token, gateways, certificate, backups, license, with a pass/fail table and what to do |
| The BOBRES logo as the bot's photo by default | Core serves the bundled beaver when no photo was uploaded, except under a white-label license; an uploaded photo always wins. The bot's description, short description and commands are already set automatically; their texts get a review |
| "Made by TECSO-DEV" everywhere | Done in the README, the site, `install.sh`, the CLI, the dashboard footer and login page, and the bot's about text (name only, no links yet). The dashboard and the bot hide it under a white-label license, with "Powered by BOBRES"; the bot learns the license from `Settings.white_label` and shows the credit when core does not say |
| The bot's owner panel entirely by buttons, with every field explained | Today the settings screen is a list of `key = value` lines changed with typed commands (`/set key value`, `/plan_add`, `/trial`, `/topup_add`, `/discount_add`), and an unset value only says "not set". Replace the typed commands with guided button flows: settings grouped in menus (store, payments, channel and join, notifications, referral, texts); every setting opens a screen with what it does, where customers see it, the current value, an example and the allowed range; on and off settings are toggle buttons; a value is asked for with a prompt that shows the format, is checked on the spot with a clear message, and has Cancel and Back; changes end with a confirmation and an Undo for the last change. The same for plans, the free trial, volume packages, discount codes, top-ups, broadcasts and resellers. The goal is fewer questions to support: nobody needs to remember a command or a key name. The typed commands keep working for people who prefer them. **Built in v0.4.0** (changelog, Bot): the button-driven `/admin` menu with explained inputs, Back and Cancel. **Not built:** the confirmation with an Undo for the last change |
| The first 3x-ui panel is not named "default" | The installer registers the panel it is given as `default` (`EnsureDefaultServer` in the provisioner), and that name shows in the Plans table and on every service. Name it after the panel's host (or ask for a friendly name in the wizard), and keep "default" only as a fallback. The name can already be changed in the dashboard (3x-ui servers, Edit) |
| Each plan chooses its own inbounds | Today the inbounds a customer is attached to are decided per server (the attach switches in 3x-ui servers) and are the same for every plan; a plan can only choose which servers it may use. Add a per-plan inbound choice in the plan form (for example one plan on 3 inbounds, another on all), stored per plan, with "all enabled inbounds" as the default so existing plans do not change; the provisioner attaches only those, and a change affects new services only. Until then: register the same panel twice under two names with different attach switches and point each plan at its server (untested) |
| Database access | Decided: no direct database login for operators or customers (it would bypass the ledger's rules, the audit log and the license checks). Use the dashboard, the reports and CSV exports, backups and CLI commands. A read-only reporting account over an SSH tunnel can come later if operators ask |

## The plan after the first real-server test (status after v0.4.0)

From the comparison with Mirza Bot and WizWiz (2026-10-14): both support more panels and more Iranian gateways, and Mirza has a Mini App
and more growth tools. BOBRES is ahead in security, operations and tests, so the plan closes the gaps that matter to Iranian operators
and keeps that lead. The version numbers below are the ones first planned; the work shipped faster and in other batches, so each
item says where it landed.

1. **The installer and CLI makeover: built, v0.3.0.** A banner, an honest progress bar, quiet third-party output, automatic hardening that never touches SSH, a live check of the 3x-ui token while it is typed, and an install that survives an SSH drop.
2. **Trust and first login: built, v0.4.0.** The owner's requests above (status screen, login at once with a password, `bobres check`, the default logo, the footer credit) and the bot's owner panel by buttons with every field explained (without the Undo).
3. **More panels: Marzban built, v0.4.0; tested against the real 0.8.4 image in CI since.** A server has a kind, 3x-ui or Marzban; the adapter is checked against the real panel by the "Marzban integration" CI job (not a required check), which found and fixed six differences from the fake. Marzban 1.0 (beta) has another API and is not supported; flagging untested Marzban versions in the dashboard, as 3x-ui's are, is open. Then S-UI and Hiddify if asked. The adapter interface exists; each panel gets the same CI integration test the 3x-ui versions have.
4. **More Iranian gateways: IDPay and NextPay built, v0.4.0** (item 3 of the picks above). Aqayepardakht is open; each gateway stays off until its merchant id is set.
5. **Growth tools: gift codes built (unreleased, see the changelog); the rest open.** Cashback, a lottery or wheel, reseller tiers with targets; more bot languages (Arabic, Russian, Turkish). Built in v0.7.0: the Top users board, the card to forward in Invite friends, the reseller application, the account lines on the wallet screen and the `/new`, `/renew`, `/status` commands (see the top of this page).
6. **The branded subscription page: built, v0.4.0** (item 2 of the picks above). Per-server capacity limits are open.
7. **The Telegram Mini App: built, v0.4.0; on by default and complete since v0.6.0, and holding the required channel since v0.6.1** (item 4 of the picks above: trial, invites, support, Stars, receipts and a reseller panel), not yet checked in a real Telegram.
8. **Alongside:** the audit-log export is built (`audit.csv`). Open: Persian documentation generated from the code (Mirza's 61 pages are the bar), a Prometheus endpoint and an uptime page, two-factor sign-in inside the bot.
9. **The business track (the owner's, not code):** the lawyer's review, the prices, the license server, the first customers ([launch-checklist.md](launch-checklist.md)).

## Later, not yet ordered

- More bot languages (Arabic, Russian, Turkish): the texts are already keys, so the work is translation and review.
- TON payments; more crypto networks, if a way that works from Iran exists.
- Metrics for operators (Prometheus endpoint, a Grafana example) and an uptime page per server.
- Automatic node balancing and per-server capacity limits beyond the weights in use today.
- Two-factor sign-in for staff inside the bot.
- A hosted offering, so a customer who does not want to run a server can pay for one.
- An affiliate program for BOBRES itself, on top of the license server.
- An adapter for a newer 3x-ui major version, when one is released and tested in CI.

## How an item gets built

1. A short spec in `docs/` (scope, API, data, security, tests), reviewed by the owner.
2. A branch and a pull request; independent reviewers read the diff for correctness, security and the texts.
3. CI green, then merged; the release notes say what changed.
4. A release candidate on the lab, then a release.

Ideas and changes of order go in this file; the plan's decisions stay in [PLAN.md](PLAN.md).
