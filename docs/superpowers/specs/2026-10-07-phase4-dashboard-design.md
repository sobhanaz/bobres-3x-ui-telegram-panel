# Phase 4: web dashboard (design and status)

Written 2026-10-07. PLAN.md section 10: "Web dashboard + branding editor." The owner chose all 17
sections, every feature (including the later-phase pages), login both from the bot and with a
password, two-factor codes required for passwords, and a calm dark + light look. It ships in six
milestones, one pull request each. **Milestones 1 (foundation) and 2 (customers and sales) are
built**; the rest is planned below.

## Milestones

1. **Foundation (built).** JSON API on core, both logins, sessions, CSRF, permissions, audit,
   an emergency login link from the CLI, the dashboard shell with all 17 sections in the menu,
   Persian/English with right-to-left, dark/light, the Overview page, and the builds (Makefile,
   Docker, CI).
2. **Customers and sales (built).** Users, Services, Plans and packages, Orders and payments
   (the receipt review queue with the photos), Wallet and ledger with CSV export, Discounts and
   referrals. See "Milestone 2" below.
3. **Store setup.** Branding and texts (every bot string), Settings, Staff, Audit log.
4. **Support and broadcasts.** Tickets answered through the bot, broadcasts sent with a rate limit.
5. **Resellers.**
6. **Infrastructure.** Several 3x-ui servers (servers per plan), payment gateways with encrypted
   keys, System (health, versions, update check, backups through a backup container), License
   (checked with internal/license).

**Not in Phase 4:** restoring a backup from the dashboard (CLI only), a customer portal, the vendor
license server.

## Stack

- **Vue 3.5, Vite 8, TypeScript**, built into `web/dashboard/dist/app` and embedded in the core
  binary (`web/dashboard/embed.go`, `//go:embed all:dist`).
- **PrimeVue 4.5 (MIT)** with the Aura theme. PrimeVue 5 needs a commercial license, so the
  owner chose 4.5. Icons are primeicons 7, the last MIT release.
- **vue-router, pinia, vue-i18n 11.** vue-i18n 11 compiles messages without `eval`, so it runs
  under the strict CSP.
- **Fonts are bundled** (Vazirmatn for Persian, Inter for English): the CSP allows no third-party
  hosts, and Google Fonts is unreliable from Iran.
- **Versions are pinned exactly** in package.json. CI fails on a known vulnerability in anything
  that ships to the browser (`npm audit --omit=dev`).

## Serving

- **Paths.** Core serves the app at `/admin/` and the API at `/api/v1` on its HTTP port; Caddy
  already routes both to core. Every unknown path under `/admin/` returns `index.html`, so the
  app's own routes survive a reload.
- **Caching.** Assets have content-hashed names and are cached for a year. `index.html` and API
  answers are never cached. Caddy's `?Cache-Control "no-store"` only applies when core sets none.
- **CSP.** Core sends the policy itself (`dashboard.CSP`), and Caddy sends the same one; a test
  keeps them equal, since two different policies would both apply. Scripts, fonts and API calls
  come from the same origin only. Styles may also be inline, because PrimeVue injects its theme.
  Images may also be `data:` URIs, for the authenticator QR code. The built `index.html` has no
  inline script.
- **Builds.**
  - Docker builds the dashboard in a Node stage on the build machine's own platform. Its output
    is the same for every target, so a multi-arch release does not run npm under emulation.
  - Locally, run `make dashboard` before `make build`. A core built without it shows a short
    "not built" notice at `/admin`.

## Staff and logins

- **Who is staff.** Staff are users with role owner, admin or support. M3 adds the Staff page;
  until then the owner is the only staff member (`BOBRES_ADMIN_TELEGRAM_ID`).
- **Login from the bot (main way).**
  - The /admin panel has a **Dashboard** button that sends a one-time link. The token is 256-bit,
    stored only as a SHA-256 hash, single use, and valid for 2 minutes.
  - The token travels in the URL fragment (`/admin/login#t=…`), which browsers never send to a
    server or in a Referer header. The app removes it from the address bar right after use.
- **Emergency link.** When the bot is down, `bobres admin link` (also in the `bobres` menu) prints
  a link from the server's shell: it runs `core login-link` in the core container.
- **Password (optional second way in).**
  - Turned on from "My account": a username (3-32 of `a-z 0-9 _ . -`) and a password of at least
    10 characters, always with a 6-digit code from an authenticator app (RFC 6238 TOTP: SHA-1,
    30 seconds, one step of clock drift either way).
  - A code works once: the last step used is stored, and a replayed or older code fails.
  - The password is hashed with argon2id (t=3, 32 MiB, p=2).
  - The TOTP secret is encrypted with the core master key (`BOBRES_MASTER_KEY`). Without that key,
    password login is unavailable and the page says so.
  - Turning a password on logs out every other session.
- **Failed logins.**
  - Five failures lock the username for 15 minutes.
  - An unknown username is checked against a dummy hash, so it takes as long as a wrong
    password. The error never says which part was wrong.
  - Both login endpoints check the request comes from the dashboard's own origin, and allow 20
    attempts a minute per IP.

## Sessions, CSRF, permissions, audit

- **Sessions.**
  - A `__Host-bobres_session` cookie: Secure, HttpOnly, SameSite=Strict. The server stores only
    its SHA-256 hash.
  - A session lasts 12 hours at most and ends after 2 idle hours. Logging out revokes it.
  - The user's role is read again on every request, so a demoted or banned staff member loses
    access at once.
- **CSRF.** Every request that changes something carries the session's CSRF token in
  `X-CSRF-Token` (SameSite=Strict is the first line of defence).
- **Permissions.** Handlers check one permission each; the menu hides what a role cannot use.

  | Role | May |
  | --- | --- |
  | owner | everything |
  | admin | everything except staff accounts, the license, gateway keys and system actions (backups) |
  | support | overview, read users/services/payments, extend a service, answer tickets |

- **Audit.** Logins, turning a password on and turning it off go to the audit log, with the
  method and IP. M3 adds the Audit log page.

## Interface

- **Sections.** The menu groups the 17 sections into Store, Customers, Sales, Setup and
  Administration. A section that is not built yet shows what it will do, the milestone that builds
  it, and that the bot's /admin panel covers it meanwhile.
- **Language.** Persian by default, right-to-left; English is left-to-right. The choice is
  remembered in the browser. Layout uses logical CSS properties only, so it mirrors itself.
  - Mixed-direction text (usernames, plan names) is isolated with `<bdi>`.
  - Persian shows Persian digits and Jalali dates (`fa-IR-u-ca-persian`); IDs and codes stay
    Latin and left-to-right.
  - PrimeVue's own words (dialog buttons, empty lists, screen-reader labels) follow the language
    too; a test lists any new PrimeVue word left in English.
- **Look.** Teal accent and deep-navy surfaces in dark mode. It follows the system by default;
  the top bar switches between system, dark and light.
- **Phones.** Under 900px the menu moves into a drawer that opens from the reading side.
- **Overview.**
  - Users (total, new today, new this week) and active services (with ending soon and ended).
  - Revenue today and over 30 days, per currency, from the ledger.
  - Payments waiting for review, and paid orders the panel has not created yet.
  - The 3x-ui panel's health, and the latest orders.
  - When the payments or provisioner service does not answer, that card shows "unknown" or "not
    answering" and the rest still loads.

## API (milestone 1)

| Method and path | What |
| --- | --- |
| `POST /api/v1/auth/link` | log in with a link token |
| `POST /api/v1/auth/login` | log in with username, password and code |
| `POST /api/v1/auth/logout` | end the session |
| `GET /api/v1/me` | who is logged in, permissions, CSRF token, session, password state |
| `POST /api/v1/me/password` | start password setup (returns the QR code and key) |
| `POST /api/v1/me/password/confirm` | finish it with a code |
| `DELETE /api/v1/me/password` | turn password login off |
| `GET /api/v1/overview` | the Overview numbers |

Answers are JSON and never cached; errors are `{"error": code, "message": text}`. Request bodies
are limited to 1 MB and unknown fields are rejected.

## Milestone 2: customers and sales

- **Users.** Search by Telegram id, @username or invite code. A customer's page shows:
  - the profile, wallets and invitations
  - services, orders, payments and wallet history, each a paged list
  - ban or unban, which needs a reason
  - changing a balance (add or take money, with a reason)
  Staff cannot be banned from here: staff are the owner's business (M3). This also applies to the
  bot's /ban now; before, an admin could ban another admin.
- **Services.** Search by Telegram id, @username, panel email, or any part of the service id (the
  bot shows customers the last 6 hex digits). A service's page has extend, read from the panel now,
  reset traffic, turn off/on and delete; each change asks for a reason.
  - **Extending.** An extension is a free, already-paid renewal order (a top-up when it adds traffic
    only) that carries its own days and GB (`orders.extend_days/extend_bytes/created_by`).
    - The provisioning worker applies it in turn with the customer's own renewals, so they can never
      race, and a retry never adds twice.
    - The bot tells the customer as for a renewal they bought.
    - Rules: no days for a service that never expires, no GB for unlimited traffic, and an expired
      service needs days.
  - **Turned off** means status `disabled`: the client cannot connect, renewals are refused, and
    there is no usage sync or reminders. A renewal paid before the service was turned off waits (it
    is retried) instead of touching the panel. Turning it on reads the panel at once for the right
    status.
  - **Deleted** removes the client from the panel. The row stays with status `deleted` (orders point
    at it) and leaves the customer's list. A service with a delivery still running cannot be deleted.
- **Plans.** List with sales, create and edit. Traffic is entered in GB (2^30 bytes, as the bot
  counts) and the price in major units. A traffic package must be a paid plan of kind traffic
  (checked before the database's own constraint).
- **Orders.** Filters by status and type, and an order page with:
  - its payment attempts (from payments' new `ListIntents`)
  - delivery attempts and the last error
  - retry now (makes the next attempt due at once)
  - refund to the wallet
- **Refunds.** A refund credits what the customer paid, once (ledger key `refund:<order id>`).
  - A paid order not delivered yet is also cancelled. Its deliveries stop, and a half-made panel
    client is removed; if that removal fails the refund stands and staff are told.
  - A delivered order keeps its service (delete it separately).
  - The bot tells the customer ("An order was refunded…").
- **Payments.** Three views:
  - the review queue: receipt photo with a zoomable preview, reference number or transaction id,
    and the duplicate-reference warning; approve, or reject with a reason the customer sees
  - all orders
  - the payment history
- **Receipt photos.** The bot serves them to core at `GET /internal/files/{id}`. This changed from
  the plan ("core gets the bot token") because core has no internet access in the compose file:
  - core presents its service token; the bot checks it (`BOBRES_PEER_CORE_TOKEN`)
  - only images and PDFs up to 10 MB are served
  - Caddy never routes `/internal/` from outside
  - core reaches the bot at `BOBRES_BOT_URL` (compose: `http://bot:8080`)
  - the dashboard gets the photo from core with `Cache-Control: private` and a sandboxing CSP
- **Ledger.** Filters by kind, currency, period (today, 7 days, 30 days, counted in the browser's
  time zone) and customer. CSV export details:
  - UTF-8 with a BOM, for spreadsheet programs
  - at most 100,000 rows, with a note line when more matched
  - user-chosen text starting with `= + - @` is defused
- **Discounts and referrals.** Create a percent or fixed-amount code, with optional max uses and an
  end date. Codes can be turned off or on. There is also the invitation reward (0 turns it off) and
  the top inviters.
- **Permissions per action.**
  - Support may read users, services and payments, and extend a service and read it from the panel.
  - Reset, turn off/on and delete need `services.write` (admin, owner); refunds and reviews need
    `payments.review`.

### API (milestone 2)

| Method and path | Permission |
| --- | --- |
| `GET /api/v1/users`, `GET /api/v1/users/{id}` | users.read |
| `POST /api/v1/users/{id}/status` | users.write |
| `POST /api/v1/users/{id}/balance` | wallet.adjust |
| `GET /api/v1/services`, `GET /api/v1/services/{id}` | services.read |
| `POST /api/v1/services/{id}/extend`, `…/sync` | services.extend |
| `POST /api/v1/services/{id}/reset-traffic`, `…/enabled`, `…/delete` | services.write |
| `GET /api/v1/plans`, `POST /api/v1/plans`, `PUT /api/v1/plans/{id}` | plans.write |
| `GET /api/v1/orders`, `GET /api/v1/orders/{id}` | payments.read |
| `POST /api/v1/orders/{id}/retry`, `…/refund` | payments.review |
| `GET /api/v1/payments`, `GET /api/v1/payments/pending`, `GET /api/v1/payments/{id}/receipt` | payments.read |
| `POST /api/v1/payments/{id}/review` | payments.review |
| `GET /api/v1/ledger`, `GET /api/v1/ledger.csv` | ledger.read |
| `GET /api/v1/discounts`, `POST /api/v1/discounts`, `PUT /api/v1/discounts/{code}/enabled` | discounts.write |
| `GET /api/v1/referrals`, `PUT /api/v1/referrals` | discounts.write |

Lists take `?page=&size=` (25 by default, at most 100) and answer `{items, total, page, size}`.
Amounts go in as decimal text in major units and come out as `{amount, currency}` in minor units.
Changes that move money or add days take a request key, so a retried request happens once.

## Tests

- **Go.** TOTP against the RFC 6238 vectors and password hashing. The handlers end to end, over
  TLS against a real database: both logins, lockout, rate limit, cross-site requests, CSRF,
  logout, password setup, permissions, and sessions ending on demotion and when idle. The embedded
  file server, the CSP matching Caddy's, and the CLI `admin link` command.
- **Dashboard (vitest).** Formatting (Persian digits, Jalali dates, money, traffic, typed amounts
  in either digit set), the section list and its texts in both languages, every text compiling in
  vue-i18n's message syntax, the API client, PrimeVue's Persian words, and the login link flow
  booting the whole app.
- **Milestone 2 (Go).** Every new endpoint over TLS against a real database with fake payments,
  provisioner and bot-file services. That covers search, balance changes and their replay,
  overdrafts, bans, and support's limits. It also covers extensions applied by the worker, turning
  services off and on, reset, delete, the panel being down (503), and retries and refunds. Then the
  review queue, receipts, plans, CSV, discounts and referrals.
  - Domain tests: renewals waiting while a service is off, and the extension rules.
  - Payments: the history query with its filters. The bot: the files endpoint against tgfake (token
    checked, images only).
  - The provisioner's enable/disable passed the real-panel integration test against 3x-ui v3.9.0
    here; CI runs it on v3.8.5 and v3.9.0.
- **By hand (milestone 2).** A local core, payments and provisioner against the real 3x-ui panel on
  this machine, with seeded data:
  - an extension applied on the panel (read back from it), and a service turned off (checked in the
    panel) and on
  - a receipt approved, the order delivered, and the inviter rewarded 10%
  - a duplicate receipt rejected with a reason, a failed delivery refunded, and a service deleted
    (gone from the panel)
  - plans edited, balances changed with Persian digits, Persian and English, dark and light, phone
    width, the support role's view, and no CSP violations
- **By hand, in a browser,** against a locally built core:
  - link login; password + code setup, login, and turning it off
  - Persian and English, dark and light, the phone drawer
  - every placeholder section
  - no CSP violations

## Owner decisions, defaults chosen

- **Persian is the default language;** English is one click away.
- **The password is optional.** A link from the bot is enough, and a password always needs a code.
- **Session lengths:** 12 hours at most, 2 hours idle.
- **Support staff** can extend a service but not change plans, prices or balances.
