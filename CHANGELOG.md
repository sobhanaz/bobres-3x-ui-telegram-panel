# Changelog

All notable changes to BOBRES are written here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

What comes next is in [docs/ROADMAP.md](docs/ROADMAP.md).

### Owners and payment review
- Several owners (root admins). `BOBRES_ADMIN_TELEGRAM_ID` takes one Telegram id or several separated by commas (at
  most 10, none twice, no space inside an id: `12 34` is refused, not read as two people); a single id works exactly as
  before. Each is made an owner when core starts or when they first send /start, none can be edited, demoted or removed
  from the dashboard or the bot, and each can give the owner role to others. They are named only on the server, so a
  hijacked Telegram or dashboard account cannot make a permanent owner.
- `bobres admin owners list|add|remove [telegram id]` changes the list (validated like `bobres config set`, only core
  and the bot are recreated). `bobres menu` item 8 is now "Owners", `bobres install --admin-id 111,222` takes a list, and
  `bobres admin password --id N` gives the initial dashboard password to the owner N instead of the main one.
  Removing an id never demotes the account by itself (a typo must not lock the owner out); the command says so.
- The main owner (the first id) is the only one who takes no staff seat; the other owners take one each, as a second
  owner always did ([05-license.md](docs/05-license.md)).
- Every owner gets the payment, ticket and system alerts (until `notify.recipients = staff` widens them to every owner and
  admin) and the daily Telegram backup: it is uploaded once, to the main owner, and the others get it from Telegram by
  file id (an owner who cannot be reached is only logged).
- Payments to review, in the bot: "Review payments (N)" is a list, oldest first, eight to a page, each payment a button
  (who paid, how much, when, a warning sign for a possible duplicate) that opens its card with the receipt and Approve /
  Reject. The alert's Review button opens that very payment (it used to open the oldest). Every owner and admin sees
  the same list; a payment one of them decides leaves everyone's list, and an alert tapped later says who decided it and
  when. After a decision the list comes back.
- API: core's `AdminGetPayment` (new RPC, the proto stays compatible) shows one manual payment with who decided it and
  when; `ListStaffContacts`, the review calls and the dashboard are unchanged.

### Maintenance
- Go dependencies: `golang.org/x/term` 0.47.0, `github.com/redis/go-redis/v9` 9.23.0, `golang.org/x/crypto` 0.58.0 and
  `github.com/pressly/goose/v3` 3.29.0, with their indirect modules (`x/net`, `x/sync`, `x/sys`, `x/text`, `puddle`,
  `go-retry`, `go.uber.org/atomic`, `genproto/rpc`). The licence texts of all of them are unchanged;
  `THIRD_PARTY_NOTICES.md` lists the new versions. These replace the four open Dependabot pull requests for Go modules,
  whose CI failed on the notices check.
- The three tests that run `install.sh` through bash (`TestInstallSh*`) pass the script's path as an environment
  variable, so they also pass in a checkout whose path has spaces in it.
- README, `docs/ROADMAP.md` and `docs/PLAN.md` say what v0.4.0 contains and what is still open.

### Marzban
- Marzban is now tested against a real panel, not only a fake. CI has a job, "Marzban integration", that starts the
  `gozargah/marzban:v0.8.4` image (pinned by tag and digest) and runs against it: the client, the adapter, the
  dashboard's server pages and a whole purchase through core and the provisioner (add the server, buy, renew, top up,
  switch off and on, reset the traffic, delete). It sends real traffic through the panel's Shadowsocks inbound, so
  usage counting, a quota being enforced and a switched-off client being kept out are seen on the real Xray core, and it
  lets the panel's login token really expire. The job is not a required check. `tools/marzbandev` prepares a fresh
  panel, and `tools/marzbandev/local.sh up|test|down` runs the same panel and tests on a Mac or Linux machine with Docker.
- The version tested is **Marzban 0.8.4** (2025-01-09, Xray core 24.12.31), the newest stable release. The 1.0 line is
  still beta and does not work: v1.0.0-beta-3 lists its inbounds as an array, refuses the `SUDO_USERNAME` account
  outside debug mode, and has a full-screen admin tool. The server test now says that BOBRES works with Marzban 0.8
  instead of "not a Marzban panel".
- Where the real panel differed from the fake the adapter was written against, and what changed:
  - A client created without an `inbounds` object got no inbound, so no links at all; the adapter and the fake took
    it that the panel would attach them all. Every new client now names its inbounds, also on a plan that attaches
    all of them.
  - The username rule is stricter than the check was: at least three letters, digits or underscores before the first
    `-`, `.` or `@`. `u10-...` is fine; `u7-...` (a Telegram id of one digit; real ones have more) is refused by the
    panel. It is now refused before anything is reserved, with the rule in the message.
  - Putting a client that has never connected back on hold, after staff switched it off, failed with HTTP 422: the
    panel wants the hold duration in the same request. It is sent.
  - Resetting the traffic of a disabled client switched it on again (a slip in the panel's code), and turned an on-hold
    one into an active client with no expiry. The status is put back after the reset.
  - Deleting a client made through the panel's `SUDO_USERNAME` account (it has no owner admin) failed with HTTP 500
    after its row was gone, and the deleted account kept working in the running Xray core until the panel restarted.
    The adapter now switches such a client off first and counts that 500 as a deletion. Register an admin made with
    `marzban cli admin create` instead (it does not need to be a sudo admin).
  - A refusal sent as `{"detail": {"field": "message"}}` (HTTP 422) showed as "Unprocessable Entity"; it now names the
    field and the reason. The dashboard's address (`.../dashboard/`) entered as the panel's address gets a hint
    instead of "Method Not Allowed".
  - The fake in `internal/marzbanfake` now answers as the panel does in all of this, and in the smaller things: the
    hold duration is `null` for a client that is not on hold, the subscription link is `/sub/<token>` with a token made
    afresh on every read (all of them valid) and that route serves the share links, a missing and a bad login token get
    two different 401 texts, and a protocol the panel does not serve is a 400 that names the enum member.
- Good to know when adding a Marzban server (checked on 0.8.4): enter the panel's own address, not the dashboard's.
  Marzban listens on the server's loopback only unless it has its own TLS certificate and key (`UVICORN_SSL_CERTFILE`,
  `UVICORN_SSL_KEYFILE`), so BOBRES's containers cannot reach it until you put a reverse proxy in front or give it a
  certificate; BOBRES trusts the usual certificate authorities only, so a self-signed certificate is refused.
  Subscription links are the panel's own `/sub/<token>` ones, behind the "Subscription address" if you set it.

## [0.4.0] — 2026-10-17

### Marzban support
- A server is now 3x-ui or Marzban. Pick the panel software when adding a server (it is fixed afterwards); for Marzban
  enter an admin's `username:password` where 3x-ui takes an API token. It is stored encrypted in the provisioner like
  a token and never shown again. Existing servers are 3x-ui. Migration provisioner 00004 adds the `kind` column; going
  back down removes Marzban servers and their clients.
- The Marzban adapter creates, renews, resets, disables and deletes users and reads their usage through Marzban's API.
  Subscription links are the panel's own `subscription_url` (the "Subscription address" field only replaces its
  prefix). A Marzban username is the client email, so it must fit Marzban's username rule.
- Tested against a fake modelled on Marzban's source. A CI job against the real Marzban image is not in yet, so try a
  Marzban server with a test plan before selling on it. (Since then it has been tested against a real panel: see
  Unreleased, Marzban.)
- The dashboard says "panel servers" where it said "3x-ui servers".

### Dashboard
- Reports: which provider paid each order. The Paid-with table splits gateway and staff-approved sales by provider
  (Zarinpal, Stars, automatic USDT, card to card, crypto). Orders paid before this change show "provider not recorded"
  and still add up to revenue.
- `ledger.csv` and `audit.csv` exports are as careful as the reports files: no header-only file when the query fails,
  an `# export incomplete: <reason>` line when it dies half-way, a 60 s limit, at most two exports at once, and an
  audit entry with the outcome and row count. Text cells are quoted against spreadsheet formulas more strictly.

- Reports: Jalali weeks and months. `calendar=gregorian|jalali` on the report endpoints (default gregorian; anything
  else is a 400). Jalali weeks start on Saturday and months on Jalali day 1; a bucket's date stays its Gregorian start
  day. The Reports page has a Calendar switch (Jalali by default in Persian), names Jalali months by month and year,
  and carries the calendar into the CSV links. The summary CSV is named `...-jalali.csv` for Jalali, and the
  `export.reports` audit entry records the calendar.

- Reports now show churn: services lost (ended more than 7 days ago and not renewed), customers lost, and services at
  risk (ended, still inside the grace period), per day, week or month in both calendars. Trials, unlimited services
  and pending renewals are not counted. In the summary CSV the people rows gain `churned_services`,
  `churned_customers` and `at_risk_services` as the last three columns. The API gains `churn_grace_days` (0 to 90,
  default 7). Migration core 00020 adds an index on `subscriptions.expires_at`.
- Branded subscription page: each service has a page at `/sub/<token>` under the store's own brand: usage and days
  left, the subscription link and QR code, import buttons for v2rayNG, Hiddify, Streisand and V2Box (the link
  schemes are not yet verified on real devices), and a Renew button that opens the bot. Server-rendered with no
  JavaScript, in Persian and English, no personal data, an unguessable 128-bit address that can be regenerated from
  the dashboard. New settings `branding.sub_page.enabled` and `branding.bot_username`. Migration core 00021.
- IDPay and NextPay, two more Iranian card gateways built like Zarinpal: an API key in Payment gateways (write-only,
  encrypted; IDPay has a sandbox outside prod), callbacks at `/webhooks/idpay` and `/webhooks/nextpay` that are never
  trusted (every payment is settled by the gateway's verify call, with an amount check; a repeated callback changes
  nothing), watchdog alerts, licence gateway names `idpay` and `nextpay`, bot buttons and en/fa texts. Off until
  credentials are set. Implemented to the public documentation and tested only against fakes; see
  `docs/16-idpay-nextpay.md`. Payments migration 00008.

### Owner and operator tools
- `bobres` (no arguments) opens on a status screen: services up or down by name, version, licence tier and days left,
  HTTPS certificate days left, newest backup, dashboard address. `bobres status --json` prints the same facts.
- `bobres check [--dir D] [--json]` runs config, services, disk, database, Redis, licence, 3x-ui servers, watchdog
  alerts, HTTPS, backup age, release-signature expiry, bot token and webhook checks, and exits 1 on a failure. It is
  also the menu item "Check everything". The core side is `core check --json`. `install.sh` and `bobres update` now
  keep the verified `checksums.txt` in the state directory (a same-version re-signed release too), which the
  release-signature check reads; a server installed before this change shows SKIP until its next update.
- Owner sign-in without Telegram. `bobres admin password` (and a fresh `bobres install`) issues a random 16-character
  initial password for the login `owner`, shown once and stored only as a hash. It opens a restricted one-hour session
  where the dashboard shows only My account: choose a new password and set up two-factor authentication, then the rest
  unlocks. Running it again resets the login after a lost phone or password, ends the owner's sessions and lifts a
  lock. It also creates the owner's account when the owner never sent /start. Audited as
  `dashboard.password.reset_cli` and `dashboard.password.initial_changed`, never with the password.
- The first 3x-ui panel a new install adds is named after its host (for example `panel.example.com`) instead of
  `default`. Existing installs keep `default`.

### Plans
- A plan can name which inbounds of each 3x-ui server its customers are attached to. With no choice, nothing changes:
  the client goes on every inbound that has "attach" on. An explicit choice wins over the attach switch; if a chosen
  inbound is gone from the panel the purchase ends as `provision_failed` with a clear reason and the usual alert. The
  plan editor lists the inbounds of each chosen server and flags one that disappeared. Core migration 00023
  (`core.plan_inbounds`); `Plan` and `AdminUpsertPlan` carry the selection, and the provisioner's `CreateClient` takes
  it.

### Bot
- A complete button-driven admin menu. `/admin` now reaches plans (add, edit, archive), the trial, top-up packs,
  discounts, every setting (an editor by kind, explained, with range and default), notifications, broadcast, today's
  numbers, 3x-ui servers (switch, weight) and, for the owner, payment details and staff. Every input step explains what
  is wanted and has Back and Cancel. The old commands still work; `/set` with no arguments opens the settings screen.
  Eight new admin RPCs back it. Tested with fakes only.
- The BOBRES logo is the bot's default photo, applied once at first start. A photo you set, or remove, later is kept.

### Mini App
- A Telegram Mini App store at `/app/`: plans and discount codes, buying by wallet, Zarinpal, IDPay, NextPay or
  automatic USDT, my services with renew and extra traffic, wallet balance, history and top-up. Persian and English,
  right to left, Telegram theme. Off by default (`miniapp.enabled`). The bot checks Telegram's `initData` for core
  (`POST /internal/miniapp/verify`), so core never holds the bot token. Stars, card to card and trials stay in the bot.
  `make miniapp`, a CI job, `docs/17-mini-app.md`. Not checked against a real Telegram yet.

### API
- `SubscriptionLinks` has `page_url` (field 4); the bot's "service ready" message uses it for an Open page button.
- `order.paid.v1` has an optional `provider`; `/reports/methods` rows have a `provider` field; `orders.csv` has a new
  last column `provider`.
- `Settings.white_label` (core proto, field 3): the bot learns from core whether a white-label license is active.

### Security and release
- Postgres and Redis are pinned by digest in the compose files, and the backup image's Postgres base in the
  Dockerfile too (Caddy already was), so a moved tag changes nothing. `deploy/images_pinned_test.go` keeps them pinned.
  The digests change only by hand, with a version bump.
- The site shows the vendor license key's fingerprint (`vendor-v1-2026`, checked against `bobres about`).
- The four legal drafts are built as draft pages in `site/legal/` (`go run ./tools/sitelegal`), marked as drafts,
  `noindex`, not in the sitemap and not linked yet. They wait for the lawyer's review.

### Branding
- "Made by TECSO-DEV" is in the dashboard footer and login page and at the end of the bot's about text. A white-label
  license hides it, along with "Powered by BOBRES"; the credit is dropped from the bot's text when the text would pass
  120 characters.

## [0.3.0] — 2026-10-14

The installer and the command line, made clear and professional.

### Installer
- The installer has a new look. `install.sh`, `bobres install`, `bobres update` and `bobres menu` start with the
  BOBRES banner (the beaver, the version, the tagline, `© 2026 BOBRES, all rights reserved`, `Made by TECSO-DEV`, the
  license summary and the links), and the install shows one progress bar from 0 to 100% that continues from `install.sh` into
  `bobres install`: the percent is real (steps and images or services done), never goes down and reaches 100% only
  when the install is finished. Each step ends with a line that says OK, a warning or a failure and how long it took.
  `bobres version`, `bobres help`, `bobres status`, `bobres doctor` and the menu got the same colors and marks on a
  terminal.
- When the output is a pipe, a log, `NO_COLOR` or a dumb terminal (or with `--plain` or `BOBRES_PLAIN=1`) the installer
  prints plain ASCII lines like `[ 42%] Step 6/13: Pulling images`, with no colors and no redrawing. `--no-banner`
  or `BOBRES_NO_BANNER=1` leave the banner out. Commands whose output other programs read are unchanged.
- New command `bobres about`: the version, commit, license and copyright, the vendor license key's fingerprint, the
  store installed on this server and the documentation, support and security links in one place.
- What Docker's installer, apt and `docker compose` print no longer fills the screen: it goes to
  `/var/log/bobres-install.log`, and a failure shows the last lines and what to do. The install ends with a
  "BOBRES is ready" card: the address, how to log in, the health of every service, the license (the 30-day trial and
  its days left), three next steps (the first is to send /start to your bot, which makes you the owner) and the
  links.
- The wizard now asks only what has no safe default: the domain, your Telegram id, the bot token and, optionally, a
  3x-ui panel. The S3 backup questions and the hardening questions are gone (S3 stays available as flags, and the
  summary says how); a panel on the same server needs no question either. `--domain` and `--admin-id` can also come
  from `BOBRES_DOMAIN` and `BOBRES_ADMIN_TELEGRAM_ID`.
- The server is hardened by itself: the firewall (SSH, 80, 443), fail2ban for SSH, automatic security updates and a
  swap file on a small server, one line each. A step that is not safe (the SSH port not found, 3x-ui or another
  service on this server) is skipped with a warning and the command to run. `--no-harden` skips it all and
  `--ask-harden` brings back the questions. The installer never changes `sshd_config`, SSH password login or root
  login, and never restarts sshd.
- The 3x-ui panel is checked while you answer, before anything is written: it must be reachable (with the reason
  when it is not) and accept the API token (a version outside 3.8.5 to 3.9.0 is noted and used anyway). A refused token says what to do
  ("In 3x-ui open Settings, then API tokens (Security)...") and asks again; before, the install said everything was
  fine and the panel refused the token later.
- The install keeps going if your SSH connection drops, and you can follow it with
  `tail -f /var/log/bobres-install.log`. `sudo bobres admin link` before the owner has sent /start to the bot now says
  "Open @yourbot in Telegram and send /start once, then run this again."

## [0.2.1] — 2026-10-14

The daily sales summary, the second half of the sales reports.

### Bot
- A daily sales summary for the owner and admins, once a day in Telegram: yesterday's revenue per currency (never added
  together) with the change from the day before, paid orders, refunds, wallet top-ups, new customers and free trials, in
  Persian (Jalali date) or English. Payments staff rejected and failed gateway payments are listed only when there were
  some, and a day with no sales still gets a one-line message so you can see it is working. It is off until you turn it
  on and is queued once a day even if the server restarts (the bot sends each message at least once: a bot that is killed
  at the wrong moment can send one again). If the server was down at that time it is sent when it is back, never once for
  each missed day, and the bot drops a summary it could not send within a day. Turned on after its time of day, it starts
  the next day. A summary is not read before 00:02, so a purchase made just before midnight is in it; one that fails is
  tried again after 5, 10 and 15 minutes and given up after eight tries; a recipient Telegram refuses for good does not
  hold up the other messages; someone who stops being an owner or admin before the bot sends gets nothing; the change
  from the day before reads "about the same" under half a percent, never "down 100%" on a day with sales, and "up more
  than 999%" past that. If you change the store's time zone, a sale around the change can be in two summaries or in none,
  and the log says so ([docs/14-reports.md](docs/14-reports.md#the-daily-summary-in-telegram)).

### Dashboard
- Settings has a "Sales summary" card: a switch for the daily summary and the time of day it is sent (09:00 by default,
  in the store's time zone). The Reports page says whether the summary is on and links to those settings.

### API
- `GET /api/v1/reports/daily-summary` (`reports.read`) answers whether the daily summary is on, its time and the time
  zone. The settings `reports.daily_summary` and `reports.daily_summary_time` are in the settings API and the bot's
  `/set`. Core publishes `report.daily_summary.v1` to the bot, one event for each recipient. Migration 00019 adds a
  small table, `daily_summary_claims`, that remembers which day was sent.

## [0.2.0] — 2026-10-14

Sales reports: the first of the features after the first release ([docs/ROADMAP.md](docs/ROADMAP.md)).

### Dashboard
- A Reports section for the owner and admins (new permission `reports.read`): sales per day, week or month for
  today, the last 7 or 30 days, this or last month, or two dates; each currency on its own, never added together. It
  shows revenue, orders, new customers, renewals, free trials, wallet top-ups, refunds, payments staff rejected,
  failed or expired gateway payments (shown apart) and cancelled orders, a chart per currency, the top plans and how
  orders were paid, in Persian (Jalali dates) and English. Every number is explained in
  [docs/14-reports.md](docs/14-reports.md).
- CSV files of the summary and of the paid orders for the range, opened right by Excel in Persian and safe against
  formulas in names. A file that cannot be made says so (it never looks empty), and one that stops half way ends with a
  line that says it is not whole.
- Support no longer sees revenue on the Overview page: its cards are shown only to staff who may read the reports.

### API
- `GET /api/v1/reports/summary`, `/plans`, `/methods`, `/summary.csv` and `/orders.csv` (read-only, `reports.read`);
  exports are written to the audit log with how they ended; at most two run at once (429 with `Retry-After`); a
  report that takes too long answers 503 `timeout`. `GET /api/v1/overview` leaves `revenue` out unless the role has
  `reports.read`. Migration 00018 adds three indexes and no table.
- `payment.rejected.v1` events from core carry the payment's `provider`, so a rejection by staff and a failed gateway
  payment can be told apart.

### Operations
- `BOBRES_TIMEZONE` now reaches core too (the reports' days follow it when the Settings page names no zone), so
  `bobres config set BOBRES_TIMEZONE` recreates core as well as the bot and the backup service.

## [0.1.1] — 2026-10-14

A small fix after the first release.

### Dashboard
- English texts say "1 day" and "1 service", not "1 days" (plurals fixed in the whole dashboard, with a test that guards them).

## [0.1.0] — 2026-10-14

The first public release. It sums up what BOBRES does now: Phases 0 to 7 of the plan ([docs/PLAN.md](docs/PLAN.md) §10),
pull requests #13 to #48. Release candidates 0.1.0-rc.1 to rc.4 came first; rc.4 is this release's code. Everything below is new in this release.

### Bot
- Persian and English. Customers pick a language on their first `/start` and can change it.
- A bottom menu with the main actions stays under the chat, beside the inline menus. Buttons are colored by what
  they do: green to pay or confirm, blue for the main actions, red to cancel or refuse.
- Buy a plan: traffic-limited, time-only or both. An optional discount code, then a payment method, then the service
  arrives with its subscription link and a QR code.
- My services: status, expiry, traffic used with a bar, the link and the QR code. Renew with any plan (days and
  traffic are added) or add a traffic package; the subscription link stays the same.
- A wallet with top-ups and a history of transactions.
- One free trial per Telegram account.
- Invite friends: a personal link, and a reward in the wallet on the friend's first paid purchase.
- Reminders 3 days and 1 day before expiry, at 80% of the traffic and at the end, with Renew and Add traffic buttons.
  They come from a usage sync with the panel every 10 minutes.
- Support: one open conversation per customer, with text, photos and PDFs. Staff replies arrive in the chat with
  Reply and Close buttons; past conversations can be read.
- Store rules: maintenance mode, a required channel to join first, rate limits per user, `/terms`, and terms and
  privacy links on the support screen.
- Reseller panel: buy for a customer at the tier's price, on credit up to a limit; name a service and search by name;
  customer services, a statement and an invite link; notices of commissions and recorded payments.
- Staff in the bot: `/admin` (stats, payments to review, find a user, adjust a balance, ban or unban), `/set`,
  `/plan_add`, `/trial`, `/topup_add`, `/discount_add`, `/discounts`, `/discount_off`, and `/dashboard` for a one-time
  dashboard login link.
- Staff alerts: new payments to review, new tickets, failed deliveries, every sale (optional), and system problems.
  They go to the owner, or to every owner and admin.
- Long polling by default, so the bot needs no domain; webhook mode is optional.
- The bot keeps its own Telegram profile up to date, in Persian and English: menu commands (staff get theirs in
  their own chat), the description of an empty chat and the short description. The words are bot texts the store
  can change; a setting leaves them to @BotFather instead.
- The bot's profile photo is uploaded in the dashboard (Branding & texts → Store → Telegram bot): any PNG or JPEG
  becomes the square JPEG Telegram takes, and the bot sets it in Telegram when it changes (Bot API 9.4).

### Dashboard
- All 17 sections at `https://<domain>/admin`, built into the `core` binary (Vue 3, PrimeVue).
- Log in with a one-time link from the bot (`/dashboard`) or from the server (`bobres admin link`), or with a password
  that always needs an authenticator code. Sessions can be listed and revoked.
- Roles: owner, admin and support, with granular permissions. Persian (right to left) and English, dark and light.
- Overview: revenue, new users, active and expiring services, failed deliveries, payments to review, and the health
  of the 3x-ui servers and payment gateways.
- Customers and sales: users (ban, adjust a balance), services (extend, reset traffic, switch off, delete, read from
  the panel), plans and traffic packages, orders and payments (retry a delivery, refund to the wallet, the receipt
  review queue with the photos), the ledger with CSV export, discount codes and referral rules.
- Resellers: tiers with a discount or a fixed price per plan and a commission, credit limits, payments received, and
  each reseller's services, customers, statement and orders.
- Support inbox with saved replies and assignment; broadcasts in both languages, with an audience, a schedule, a rate
  limit, pause and resume.
- Store setup: branding (name, logo, colours, the bot's profile photo) and every bot text in both languages,
  settings, staff, and the audit log with CSV export.
- 3x-ui servers: several panels with a write-only token, weight, on/off for new services, version and health, a
  connection test, servers per plan, and an attach switch per inbound.
- Payment gateways (owner): Zarinpal and automatic USDT settings with a connection test and a report of the last 7 and
  30 days, unmatched USDT deposits, and the manual methods and Telegram Stars with their switches and details.
- System page: health and versions of every service with the open problems, the update check (with the channel and
  the license's coverage), the newest warnings and errors of every service (owner only), backups (list, run one,
  off-site copies), the HTTPS certificates, and the license (state, entitlements and their use, paste a token, or
  activate with a license key).
- HTTPS tab on the System page: everyone who sees the page sees the mode and, for each address the store is served on
  (the domain, the server's IP address, the Zarinpal domain), the issuer, expiry with the days left, whether browsers
  trust it, and the live error. The owner picks the mode: the domain's automatic certificate, a Let's Encrypt
  certificate for the server's IP address (with a Detect button and the warnings), or an own certificate and private key
  (files or paste). The page waits for the certificate to be issued and shows Caddy's own words when it refuses.
- A "Powered by BOBRES" footer on every install except one whose license includes white label (the free tier and
  the trial show it).
- The License tab opens with what applies now: the free tier, the trial with its days left, or the license's tier,
  then the entitlements with this install's use of each limit, and whom to contact to buy or renew a license. A chip
  in the menu and on the System tab says so too. A refused action in the dashboard names the limit and says to buy or
  renew a license.

### Payments
- The wallet, always on.
- Card to card, USDT (TRC20 or ERC20) and the owner's Zarinpal payment link: the customer sends a screenshot (or a
  TXID for USDT) and an admin approves it. A repeated bank reference is flagged.
- Zarinpal online (REST v4): off until the owner sets a merchant id. An optional proxy with a registered IP and a
  second, registered domain for the pay page. The payments service also asks Zarinpal about open payments itself
  (every 30 seconds for a new one, less often later); failed checks are recorded and reported.
- Telegram Stars, at the store's rate in Toman per Star.
- Automatic USDT on TRON (TRC20): each payment gets its own exact amount and 45 minutes to pay. A watcher reads the
  store's address through TronGrid every 30 seconds and settles an exact match; a late exact payment still settles
  within 48 hours. Any other transfer is recorded as an unmatched deposit and announced to staff. A transaction id
  pays only one payment, automatic or manual.
- Each method has an on/off switch. Payments already started go on when a method is switched off.
- Money is stored as whole numbers in minor units. Each payment settles exactly once, and the wallet ledger is
  append-only.

### Operations
- A one-command install on Ubuntu 22.04/24.04 or Debian 11/12/13 (amd64 or arm64). It verifies the signed CLI, asks
  for the domain, owner, bot token and an optional first 3x-ui panel, generates every secret and starts the store.
- The `bobres` menu and commands: status, logs, start, stop, restart, settings, doctor, admin link, uninstall.
- Several 3x-ui servers: a new service goes to the healthy, enabled server with the fewest clients per weight, among
  the plan's servers. Each server is checked every minute.
- Daily encrypted backups: the newest 7 kept on the server, each sent to the owner on Telegram (up to 50 MB), and
  optional copies in S3-compatible storage (Arvan, MinIO, Backblaze B2, AWS). `bobres backup` makes one now.
- `bobres restore`, from a file or from `s3://`: the file is verified first, the current data is backed up, and the
  database is replaced in one transaction.
- `bobres update` verifies the signed release, backs up, keeps a restore point and rolls back on its own when the new
  version does not come up healthy. `bobres rollback` undoes the last update. Stable and beta channels.
- System alerts on Telegram: a core watchdog checks the services, the 3x-ui servers, Zarinpal, automatic USDT,
  backups (and their off-site copies), disk, deliveries, the bot's backlog and the license every minute, and says
  when a problem is fixed.
- `bobres harden`: firewall, fail2ban for SSH, security updates and swap, each asked (also offered by the install).
- HTTPS certificates in three modes: Caddy's automatic certificate for the domain (the default), a Let's Encrypt
  certificate for the server's IP address (valid about 6 days, renewed by Caddy), and the owner's own certificate and
  key. The dashboard's HTTPS tab and `bobres ssl status|auto|ip|manual` (also in the menu, "HTTPS certificate") change
  it. A change is saved only once Caddy accepts it. `sudo bobres ssl auto` works without the dashboard when a wrong
  certificate locks browsers out. In the IP address mode, dashboard links move to the IP address only while its
  certificate is trusted and the bot sees the same public address (`BOBRES_PUBLIC_URL` overrides). `bobres doctor` has a
  TLS line for every served address (a crash-looping Caddy is a failure), and four alerts say when Caddy is not
  answering, a certificate is close to its end, an address serves no valid certificate, or the saved setting is not in
  use ([runbook](docs/runbooks/https.md)).
- The installer lays Caddy's files out as `caddy/Caddyfile` and `tls/`, makes Caddy read its config again after an
  update, and keeps the old top-level `Caddyfile` so `bobres rollback` still works across the change. The `tls-agent`
  service is in the releases from 0.1.0-rc.2 on: a rollback to 0.1.0-rc.1 has none.
- `bobres config set BOBRES_DOMAIN` and `BOBRES_ZARINPAL_PUBLIC_URL` recreate Caddy, core, payments and the `tls-agent`.
- `bobres config` for the safe settings and `bobres secrets rotate` for the bot token, database passwords, service
  tokens and the Redis password.
- `bobres support-bundle`: a redacted archive for support, without the database, backups or secret values.
- Runbooks for common incidents and routine jobs ([docs/runbooks](docs/runbooks/README.md)).
- Failure drills and a restore drill in the tests, and a load test with its numbers
  ([docs/12-load-test.md](docs/12-load-test.md)).
- Licenses: a signed token pasted on the dashboard or imported with `bobres license import`, or activated online with
  a license key (`bobres license activate`) and refreshed daily. The vendor's license server, `licensesrv`, is part of
  the repository ([docs/13-license-server.md](docs/13-license-server.md)).
- Free core, paid premium ([docs/05-license.md](docs/05-license.md)). An install without a valid license runs on a free
  tier: 1 server, 25 active services, 2 staff seats, no resellers, no automatic USDT, no white label, the Zarinpal,
  Stars and manual payment methods, and security updates always. Every install, fresh or upgraded, first has a 30-day
  trial (migration 00017) with unlimited servers and services, resellers, automatic USDT and 5 staff seats, but not
  white label. The limits refuse only new actions: a server, a new service (an order, a free trial or a reseller's
  purchase), a reseller, a staff member, a payment method. Customers' existing services keep working, renewing and
  topping up, and staff over a limit keep their access. A customer whose purchase is refused sees only "New purchases
  are temporarily unavailable. Please contact support."; staff get one `license_limit` alert a day, and a `trial_ending`
  alert in the trial's last 7 days and once when it ends. `bobres license status` shows what is in effect, the trial's
  end and the use of each limit.
- A license never leaves less than the free tier (a limit it omits, or sets lower, is raised to the free tier's, and the
  gateway lists are joined). The services on their way (being delivered, paid, or awaiting payment for up to 2 hours)
  count against the limit, which is checked in the order's own transaction so orders made together cannot overshoot it.
  Every staff member but the owner named in `.env` takes a seat, an owner too. A reseller's new purchase, and the
  premium payment methods offered to customers, follow the effective entitlements, so a lapsed install stops offering
  what the trial had switched on.
- A license's `updates_until` is advisory: `bobres update`, `--check`, the menu and the dashboard note that a release is
  newer than the license's update date and install it all the same. The seller's contact is the owner's
  (`BOBRES_SELLER_CONTACT`); none is built in.

### Security
- Four services, each with its own database role and service token; a service accepts only its legitimate callers.
  Only the provisioner holds the 3x-ui tokens, only payments holds the gateway secrets, and core has no internet
  access.
- Secrets at rest (3x-ui tokens, gateway settings, authenticator secrets) are encrypted with a master key per service.
  The dashboard never shows or logs them.
- Backups are encrypted with a backup key that never leaves the server. The backup service reads the database with a
  read-only role and reaches the internet only while S3 copies are set up.
- The installer, `bobres update` and the release use Ed25519 signatures, a signed key list and signature expiry.
  Images are signed with cosign and carry an SBOM; an install runs them by the digests built into the signed CLI,
  which the release workflow takes only from images whose signature it checked.
- The dashboard: CSRF tokens, authenticator codes for password logins, rate limits, a strict CSP, and an append-only
  audit log with a reason on money and staff actions.
- 3x-ui panels must use https; plain http is allowed only to a private address.
- Containers run as non-root, with a read-only filesystem and no capabilities. Caddy serves HTTPS and blocks the
  internal paths.
- The HTTPS manager keeps core away from the gateway. A `tls-agent` service with no network is the only writer of
  Caddy's TLS files and, besides Caddy, the only user of Caddy's admin socket. It checks every request again, accepts
  only the install's own domain and Zarinpal domain, and a change is committed only after Caddy accepts it. An own
  certificate's private key is sealed with core's master key, and never logged, audited or answered. Caddy's image is
  pinned by version and digest.
- Every install runs on a 30-day trial and then on the free tier, and a license only ever gates new actions: nothing
  that exists is removed.

### Selling BOBRES (the vendor side)
- The business model is decided: a free core and a paid subscription per install, monthly or yearly
  ([docs/pricing.md](docs/pricing.md)). The prices are the owner's: placeholders ("SET ME") in
  `internal/license/tiers.json` until they are set.
- The vendor's license-signing key `vendor-v1-2026` (fingerprint `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7`) exists and
  its public key is built into BOBRES, so installs accept the tokens it signs. Its private half stays outside the
  repository. Key rotation by `kid` works, and a test pins the keys that are built in.
- Tier presets in one file, `internal/license/tiers.json`: `standard`, `pro` and a 30-day `trial`, with their
  entitlements and placeholder prices. `go run ./tools/license sign --tier standard --domain ... --customer ... --months 1`
  signs a license of a tier, and `licensesrv key create -customer ... -tier standard -months 1` issues one on the
  server. `licensesrv key update ... -extend-months 1` renews a subscription. `go run ./tools/license tiers`,
  `entitlements` and `fingerprint` list the tiers, print a tier's entitlements and a key's fingerprint. Every preset
  names every gateway and grants at least the free tier, and the `trial` preset is the implicit trial's entitlements
  (5 staff seats); tests pin both.
- The vendor tools warn, and still make the license, when `max_active_services` is 0 or missing (`tools/license sign`,
  `licensesrv key create` and `key update`): an install floors it at the free tier's 25.
- A deployment kit for the license server, `deploy/vendor/`: the server, Postgres and Caddy (HTTPS), hardened like a
  BOBRES install (non-root, read-only, no capabilities, internal networks), with the signing key as a mounted file outside
  the repository. The seller's runbook is [docs/runbooks/vendor-license-server.md](docs/runbooks/vendor-license-server.md):
  deploy, the manual sales flow, issue, renew, revoke, monitoring, backups, key rotation and a leaked key.
- A draft source license, [LICENSE](LICENSE): source-available, all rights reserved (a lawyer must review it), and
  [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for what ships, with a test that fails when it is out of date.
- Legal drafts for operators in [docs/legal](docs/legal/README.md), each in English and Persian: Terms of Service,
  Privacy Policy, Acceptable Use, a Refund policy for licenses, and a template for an operator's own store.
- [docs/launch-checklist.md](docs/launch-checklist.md): what the owner still has to do before selling.

### Brand
- BOBRES the beaver: the mascot is the logo. A flat icon, a 3D render, a waving animation, a banner, a bot avatar and a bot description picture,
  with a navy and teal palette ([docs/08-brand.md](docs/08-brand.md)).
- The dashboard's default look: the beaver favicon and logo, and the waving beaver on the login page (a still picture
  for reduced motion). A store's own logo replaces it.
- A small beaver in the `bobres` menu header.

### Known limits
- Not built: third-party crypto processors (they exclude Iran), TON, older 3x-ui adapters (only v3.x, tested on
  v3.8.5 and v3.9.0), a branded subscription page, re-encryption with new master keys, metrics dashboards.
- The license server is built and has a deployment kit, but it is not deployed: that is the owner's step
  ([docs/launch-checklist.md](docs/launch-checklist.md)).
- Image signatures are not checked on the server (the release workflow checks them before it builds their digests
  into the CLI). Postgres and Redis come from Docker Hub by tag, and Caddy by version and digest.
- The server IP address mode needs a public address with ports 80 and 443 reachable from the internet (not behind
  carrier-grade NAT). It cannot be tried in the local Docker lab. BOBRES never renews an own certificate.
- The legal review, the brand checks and the prices are open.
- The English dashboard texts do not handle plurals yet ("1 days", "1 services on 1 servers"). The Persian ones are fine. Fixed in 0.1.1.

[Unreleased]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.3.0
[0.2.1]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.2.1
[0.2.0]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.2.0
[0.1.1]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.1.1
[0.1.0]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.1.0
