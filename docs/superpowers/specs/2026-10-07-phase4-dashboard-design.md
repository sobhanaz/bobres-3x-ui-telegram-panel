# Phase 4: web dashboard (design and status)

Written 2026-10-07. PLAN.md section 10: "Web dashboard + branding editor." The owner chose all 17
sections, every feature (including the later-phase pages), login both from the bot and with a
password, two-factor codes required for passwords, and a calm dark + light look. It ships in six
milestones, one pull request each (the sixth in two). **All six are built** (2026-10-12): 1 (foundation), 2
(customers and sales), 3 (store setup), 4 (support and broadcasts), 5 (resellers), 6a (servers and payment gateways)
and 6b (System page, backups and license).

## Milestones

1. **Foundation (built).** JSON API on core, both logins, sessions, CSRF, permissions, audit,
   an emergency login link from the CLI, the dashboard shell with all 17 sections in the menu,
   Persian/English with right-to-left, dark/light, the Overview page, and the builds (Makefile,
   Docker, CI).
2. **Customers and sales (built).** Users, Services, Plans and packages, Orders and payments
   (the receipt review queue with the photos), Wallet and ledger with CSV export, Discounts and
   referrals. See "Milestone 2" below.
3. **Store setup (built).** Branding and texts (every bot string), Settings, Staff, Audit log.
   See "Milestone 3" below.
4. **Support and broadcasts (built).** Tickets answered through the bot, broadcasts sent with a rate
   limit. See "Milestone 4" below.
5. **Resellers (built).** Resellers buy for their own customers at a tier price on credit, and earn a
   commission on purchases by the customers who joined through their link. They work only in the
   bot; owner and admins manage them in the dashboard. See "Milestone 5" below.
6. **Infrastructure,** in two pull requests:
   - **6a, servers and payment gateways (built).** Several 3x-ui servers (servers per plan, health, inbounds), and
     payment gateways whose keys stay encrypted in the payments service. See "Milestone 6a" below.
   - **6b, System and License (built).** Health, versions, update check, logs, backups through a backup container,
     and the license (checked with internal/license). See "Milestone 6b (part 1)" and the sections after it.

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
  it, and that the bot's /admin panel covers it meanwhile. (Since milestone 6b every section is built.)
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
| `GET /api/v1/reports/summary`, `…/plans`, `…/methods`, `…/summary.csv`, `…/orders.csv` (added after v0.1.1, see 14-reports.md) | reports.read |
| `GET /api/v1/discounts`, `POST /api/v1/discounts`, `PUT /api/v1/discounts/{code}/enabled` | discounts.write |
| `GET /api/v1/referrals`, `PUT /api/v1/referrals` | discounts.write |

Lists take `?page=&size=` (25 by default, at most 100) and answer `{items, total, page, size}`.
Amounts go in as decimal text in major units and come out as `{amount, currency}` in minor units.
Changes that move money or add days take a request key, so a retried request happens once.

## Milestone 3: store setup

- **Settings are described in one place.** `domain.SettingSpecs` lists every setting: its group,
  kind (text, int, bool, enum, time zone, URL, channel, colour), default, limits and the permission
  needed to change it.
  - Values are still stored as JSON strings, so older bots read them. They are normalised on write:
    Persian digits become ASCII, `yes` becomes `true`, a colour becomes lower case.
  - `SetSettings` checks every value, then writes them all in one transaction, with one audit entry
    per changed key (the value before and after, and the key as the entry's item).
  - **Payment details are the owner's.** Card number, USDT addresses and rates, and the Zarinpal link
    need `gateways.write`; this also applies to the bot's `/set`. Before, any admin could change where
    customers' money goes.
  - The bot's `/set` list is built from the same table, so the two cannot drift apart.
- **The Settings page** has the groups general (time zone), maintenance, required channel,
  notifications, limits and payment details. Each card saves on its own.
  - A save tells the bot at once (`POST /internal/settings/refresh` with core's service token); the
    bot still reloads every minute.
  - "Check" on the required channel asks the bot (`GET /internal/channel-check`) whether it is an
    administrator there and can see who joined.
- **Branding** keeps the store name, support contact, brand colour, terms and privacy links, and the
  name of the Toman in prices (per language; the accounting currency stays the Toman).
  - **Logo.** PNG, JPEG or WebP, at most 512 KB and 16-2048 pixels a side, checked by its bytes (not
    by the name or the type sent). SVG is refused because it can carry script.
    - It is stored in `core.assets`, not in settings: the bot reads every setting each minute.
    - It is served at `/api/v1/brand/logo?v=<time>` with a sandboxing CSP, so the dashboard's CSP
      (`img-src 'self'`) needs no change.
  - `GET /api/v1/brand` is public (name, colour, logo), so the login page shows the store's look.
  - The colour changes the dashboard's primary palette while it runs; the page warns when white text
    on it would be hard to read.
  - A `:` in the store name no longer breaks the authenticator app's label.
- **Bot texts.** Every one of the bot's texts can be changed per language, with checks shared by core
  and the bot (`internal/bot/i18n`, `ValidateOverride`).
  - Each text knows where it is shown: an HTML message, a caption, a button, a short popup, an invoice
    title or description, or text that is never shown to customers.
  - **Checks.**
    - An override must keep exactly the placeholders of the default text.
    - HTML texts may only use Telegram's tags, properly nested, with links to https or tg only. They
      are checked with sample values filled in, so `<code>{addresses}</code>` (whose value has tags)
      is refused.
    - Buttons and popups take no markup.
    - Lengths are counted the way Telegram counts them, in UTF-16 units.
  - The bot checks overrides again when it loads them and skips (and logs) any that would break a
    message. Before, one broken text could make the menu, or the notification queue, stop.
  - The page has a live check while typing, a safe preview (never `v-html`), reset, and per-language
    export and import. An import is all or nothing.
  - **The payment screens' texts are the owner's** (`gateways.write`, also through `/set`): they tell
    customers where to send money, so an admin could otherwise put their own card number there. Admins
    see them read-only, and may re-import them unchanged in an exported file. Admins can still write
    free text in the other texts (the welcome, a plan's description); every change is in the audit
    log with who made it.
  - The Toman's name may not contain `<`, `>` or `&`: it goes into every price, inside HTML messages.
- **The bot follows the settings** (`internal/bot/settings`, shared by the handler and the notifier):
  - Gates come after the rate limit and never before a Stars payment: maintenance mode, then the
    required channel. Both let staff (support too), the configured owner, `/paysupport`, payment
    checks, receipts and TXIDs of payments under way, the language picker and a newcomer's `/start`
    through.
    - Maintenance: a popup for taps, the message at most once a minute per user.
    - Required channel: `getChatMember` with a 3-second limit; members are remembered in Redis for 10
      minutes, non-members never; Telegram errors let users in (logged). A newcomer is checked right
      after picking a language.
  - `limits.bot_per_10s` replaces the fixed 20; `limits.tickets_per_day` caps support messages.
  - Staff alerts can go to every active owner and admin (`ListStaffContacts`, cached at each reload)
    in their language, never about their own action; each kind can be turned off, and "every sale" on.
  - `/terms`, terms and privacy buttons on the support screen, and `/dashboard` (plus a menu button)
    so support staff can get a login link; `/admin` stays owner and admin.
  - Time zone and the Toman's name change while the bot runs.
  - When Telegram refuses a message's markup ("can't parse entities"), the bot sends it once more as
    plain text instead of failing.
- **Staff.** The owner sees each member's role, password and two-factor state, lock, last login and
  live sessions.
  - Actions: add a customer as staff, change a role, remove, see and end sessions, reset the password,
    unlock.
  - Every change needs a reason and ends the member's sessions, so their dashboard reloads with what
    the new role may do.
  - Removal also deletes the password login and unused login links.
  - **Rules.**
    - Nobody changes themselves here; that is what My account is for.
    - Nobody changes the configured owner (`BOBRES_ADMIN_TELEGRAM_ID`); that is set with
      `bobres menu`.
    - Only the configured owner gives or takes the owner role. Any owner may, on an install that
      names none.
    - A change never leaves the store without an active owner. The owners are locked inside the
      transaction, so two owners cannot demote each other at once.
  - A person must have started the bot before they can be added; support staff get their login link
    with `/dashboard` in the bot.
  - **The configured owner gets the role at core's start-up**, and when `bobres admin link` runs.
    Before, a customer made owner with `bobres menu` waited for their next language change. This is
    audited as `staff.owner_bootstrap`.
- **My account** lists the member's sessions, and can end one or all others.
  - Replacing or removing a working password now needs the current authenticator code, so a stolen
    session cannot take the password login over.
  - The exception is a login link used in the last 10 minutes, which is fresh proof of the Telegram
    account. It is also the way back for someone who lost their phone.
- **Audit log.** Filters by action or group, item, period and staff member, with a CSV export.
  - Each entry says where the change was made: dashboard, bot, cli or system.
  - New entries: failed logins (with no actor, naming the account), logouts, login links made, CSV
    exports, and the owner bootstrap.
  - Changes to settings, plans, discounts and bans keep the value before.
  - An entry's item can now be any text (a discount code, a setting key, `fa.welcome`), not only a
    uuid.
  - **The table is append-only:** database triggers refuse UPDATE, DELETE and TRUNCATE. Tests clear it
    through `testdb.Truncate`, which switches the TRUNCATE trigger off inside its own transaction.
- **Locks.** Failed logins from before a lock that has ended no longer count, so the next mistake
  does not lock the account again at once. The owner can lift a lock from the Staff page.

### API (milestone 3)

| Method and path | Permission |
| --- | --- |
| `GET /api/v1/settings`, `PUT /api/v1/settings`, `POST /api/v1/settings/channel-check` | settings.write (payment details: gateways.write) |
| `GET /api/v1/branding`, `PUT /api/v1/branding`, `PUT`/`DELETE /api/v1/branding/logo`, `GET`/`PUT`/`DELETE /api/v1/branding/bot-photo` | branding.write |
| `GET /api/v1/texts`, `POST /api/v1/texts/check`, `PUT /api/v1/texts`, `GET /api/v1/texts.json`, `POST /api/v1/texts/import` | branding.write |
| `GET /api/v1/brand`, `GET /api/v1/brand/logo` | public |
| `GET /api/v1/staff`, `POST /api/v1/staff`, `PUT /api/v1/staff/{id}/role`, `POST /api/v1/staff/{id}/remove` | staff.write (owner) |
| `GET /api/v1/staff/{id}/sessions`, `POST /api/v1/staff/{id}/sessions/revoke`, `POST /api/v1/staff/{id}/sessions/{sid}/revoke` | staff.write |
| `POST /api/v1/staff/{id}/password/reset`, `POST /api/v1/staff/{id}/unlock` | staff.write |
| `GET /api/v1/me/sessions`, `POST /api/v1/me/sessions/revoke-others`, `POST /api/v1/me/sessions/{sid}/revoke` | a session |
| `GET /api/v1/audit`, `GET /api/v1/audit/actions`, `GET /api/v1/audit.csv` | audit.read |

Field errors answer `{"error": "invalid", "field": ..., "message": ...}`; a text that cannot be used
answers `problems` (code and argument per problem). A session's id is the hex of its stored hash, a
one-way hash of the cookie, so it gives nothing away.

## Milestone 4: support and broadcasts

- **A ticket is a conversation.** Each customer has at most one ticket that is not closed. Statuses:
  - `open`: waiting for staff
  - `answered`: staff replied
  - `closed`

  A message to a closed ticket starts a new one. Tickets are numbered (`#1024`).
  - Migration 00009 turns the old one-message tickets into this. Each old text becomes the first
    message, and a customer's older open tickets are closed as merged.
- **Customers** write from the Support screen, after picking payment, service or other.
  - A message can be text, a photo, an image file or a PDF. An album is confirmed once.
  - They answer a reply with its [Reply] button or with Telegram's reply. They can close their ticket.
  - Continuing a ticket passes maintenance mode and the required channel; starting one does not.
  - Banned customers may still write: it is how they appeal.
- **Staff replies go through the bot.** Core keeps the reply and publishes `ticket.replied.v1`. The
  bot's notifier fetches it, sends it, and reports delivered, blocked or failed.
  - A reply already reported is never sent twice.
  - Events and audit entries carry ids, never message text.
  - Replies come from the store, never with a staff name. Internal notes never reach the customer.
- **Alerts.** Staff hear of a new ticket, and of a customer writing after an answer, but not of
  every message.
  - With `notify.recipients = staff` support staff get these alerts too. Payment, sale and
    failed-delivery alerts still exclude them.
  - A ticket with an assignee alerts only them.
- **Tickets close by themselves** `support.autoclose_days` (default 7) after an answer the customer
  did not reply to.
- **Dashboard support.**
  - The inbox: waiting, answered, closed. Filters for assignee and category, and a search by
    `#number`, @username, Telegram id, id suffix or text.
  - A ticket's thread: attachments come through the bot, and staff replies show their delivery.
  - Saved replies in both languages, assigning, close and reopen.
  - A menu badge with the waiting count, a Tickets tab on the user page, a card on the Overview.
  - Assigning others and editing saved replies need the new `tickets.manage` (owner, admin).
- **Broadcasts.**
  - What staff compose:
    - a text per language (Telegram's HTML subset, at most 4096 characters, checked live)
    - an optional link button
    - the audience: everyone, with an active service, expired with no active service, or never
      bought; by language
  - Banned users and users who blocked the bot are never in it.
  - Staff can test-send to themselves, send now or schedule, then pause, resume or cancel.
- **At most once.**
  - When a broadcast starts, core puts its audience in line (a snapshot).
  - The bot claims recipients under a 10-minute lease and reports each result.
  - A claim never reported counts as unknown and is never sent again, so a crash cannot send twice.
  - Unsent claims on pause, cancel or shutdown go back in line.
- **Pacing.**
  - Broadcasts send at most `limits.broadcast_per_sec` (default 20) minus the bot's own traffic,
    on their own Telegram client.
  - Any 429 pauses them until Telegram's `retry_after`, so replies to customers always go first.
  - Repeated identical refusals, a revoked token or Telegram being unreachable pause the broadcast
    with the reason shown.
- **Users who blocked the bot** are marked (`users.bot_blocked_at`). The flag is set by a broadcast
  result, a refused notification or Telegram's `my_chat_member` update. It is cleared when they
  write again or unblock.

### API (milestone 4)

| Method and path | Permission |
| --- | --- |
| `GET /api/v1/tickets`, `GET /api/v1/tickets/counts`, `GET /api/v1/tickets/assignees`, `GET /api/v1/tickets/{id}` | tickets.write |
| `POST /api/v1/tickets/{id}/messages`, `POST /api/v1/tickets/{id}/status`, `GET /api/v1/tickets/{id}/messages/{mid}/file` | tickets.write |
| `PUT /api/v1/tickets/{id}/assignee` | tickets.write (others: tickets.manage) |
| `GET /api/v1/canned-replies` | tickets.write |
| `POST /api/v1/canned-replies`, `PUT`/`DELETE /api/v1/canned-replies/{id}` | tickets.manage |
| `GET`/`POST /api/v1/broadcasts`, `GET`/`PUT`/`DELETE /api/v1/broadcasts/{id}` | broadcasts.write |
| `POST /api/v1/broadcasts/audience`, `/check`, `/test` | broadcasts.write |
| `POST /api/v1/broadcasts/{id}/send`, `/pause`, `/resume`, `/cancel`; `GET /api/v1/broadcasts/{id}/recipients` | broadcasts.write |

The bot gains the core calls `AddTicketMessage`, `GetMyTickets`, `GetMyTicket`, `CloseMyTicket`,
`GetTicketMessage`, `ReportTicketDelivery`, `SetUserBotBlocked`, `ClaimBroadcastBatch` and
`ReportBroadcastResults`. Core gains the bot's `POST /internal/broadcasts/test`.

## Milestone 5: resellers

The owner chose both models (2026-10-10):

- **Buying for customers:** a reseller buys and renews services for their own customers at a tier
  price, with a credit limit (buy now, pay later).
- **Commission:** customers who join through the reseller's link buy at store prices, and the
  reseller earns a commission.

Resellers work only in the Telegram bot. Owner and admins manage them in the dashboard
(`resellers.write`). Reseller is not a staff role: `IsStaffRole`, the dashboard's permissions and the
staff lists leave it out, so a reseller gets no login link and a staff member made one loses their
session.

- **Data (migration 00010):**
  - `reseller_tiers`:
    - name (unique, 1-40 characters)
    - `discount_percent` (0-90) off the store price
    - `commission_percent` (0-50)
    - a version for edits
  - `reseller_tier_prices`: an optional fixed price for one plan in one tier, in the plan's
    currency. It overrides the discount.
  - `resellers`:
    - the user, their tier (a tier in use cannot be deleted)
    - an optional `commission_percent` of their own
    - a staff note (at most 500 characters)
    - a version
  - A reseller is a user with role `reseller` and a `resellers` row; the two are always changed
    together.
- **Credit per currency.** `wallets.credit_limit` is per currency, like the wallet. The check
  `wallets_balance_check` became `balance >= -credit_limit`.
  - A debit may take the balance down to `-credit_limit`, read under the wallet's row lock, and no
    further. Credits and zero entries always go through, even while the balance is below zero.
  - Wallets with no limit (everyone but resellers) behave exactly as before.
  - Only resellers get a limit, a rule of the domain. Lowering a limit below the current debt is
    refused, with the amount owed in the message; the database refuses it too.
  - The Down refuses while any balance is negative. It keeps the new ledger kinds (the ledger is
    append-only) and makes resellers customers again.
- **Prices.** One `priceFor(user, plan)`, used by `CreateOrder`, `QuoteOrder` and the bot's plan
  lists, for new orders, renewals and top-ups alike:
  - an active reseller pays the tier's fixed price for the plan if set, else the store price less
    the discount (rounded down)
  - everyone else, a banned reseller included, pays the store price
  - `ListPlans(user_id)` answers the user's price in `price` and the store price in `list_price`
- **Discount codes are refused for resellers** (reason `reseller`): they get tier prices instead.
- **Paying.** A reseller pays from the wallet as anyone does, down to `-credit_limit`. Gateways,
  manual payments and top-ups work as for anyone; the money credited lowers the debt.
- **Labels.** A reseller names the service they buy for a customer ("Ali's phone", at most 64
  characters of plain text). The order keeps it (`orders.label`) and provisioning copies it to the
  new service (`subscriptions.label`). Renewals and top-ups keep the service's name, and other
  people's labels are ignored.
  - The name travels in `subscription.provisioned/extended/reminder.v1` (`label`) for the bot's
    texts.
  - `SetSubscriptionLabel` renames one of the reseller's own services; anyone else's is not found.
  - `ListSubscriptions` pages (at most 50) and searches by label or id suffix when asked. Without
    paging or a search it answers the newest 100 as before.
- **Settlement.** "Payment received" is recorded by staff: a credit of kind `settlement`, ref_type
  `admin`, key `settlement:<request key>`.
  - It has an audit entry `reseller.payment` with the reason and a `wallet.credited.v1` event with
    reason `settlement`.
  - A replay with the same key answers the first entry and changes nothing.
- **Commission.** When a paid order has an amount and is not a trial or free order:
  - **The buyer is a reseller:** nobody is paid. These are business purchases, so there is no
    referral reward and no commission.
  - **The buyer joined through an active reseller's link:** that reseller earns `amount × rate /
    100`.
    - The rate is their own if set, else the tier's.
    - The credit has kind `commission`, ref_type `order` and key `commission:<order id>`, so every
      order pays once. A 0 result pays nothing.
    - The event is `wallet.credited.v1` with reason `commission`. The bot names the amount only,
      never the customer.
    - The referral reward is not paid then. A replay is a no-op; any other ledger conflict fails
      the payment (never swallowed).
  - **Anyone else:** the referral reward, unchanged.
  - Every customer the reseller invited counts, including those who joined before they became a
    reseller (attribution is `users.referred_by`, set once when a user is created). A banned
    reseller earns nothing.
- **Refunds do not reverse commissions,** as with referral rewards; staff can adjust a balance. A
  refund of an order a reseller paid credits the reseller, which lowers their debt.
- **Lifecycle** (all audited with a reason; entity `reseller`, id the user's):
  - **Make** (`reseller.add`, before `{"role":"user"}`):
    - the person must be an active customer (role `user`), never the configured owner
    - the role changes with a compare-and-set, so a role changed meanwhile is a 409
    - the terms and credit limits are set in the same transaction
  - **Change terms** (`reseller.terms`, before and after):
    - tier, own rate, note and credit limits per currency
    - read at a version: a stale one is a 409
    - currencies left out keep their limit
  - **Remove** (`reseller.remove`): refused while any balance is negative. Credit limits go to 0,
    the row goes, and the role returns to `user`.
    - Their unpaid orders (`created`, `awaiting_payment`) are cancelled in the same transaction
      (the audit's after counts them as `cancelled_orders`). Those orders carry tier prices, and
      orders never expire: paid later as a customer, one would buy at the tier price and pay their
      inviter a commission or the referral reward.
    - A payment that still arrives for a cancelled order stays in the wallet
      (`order_not_payable`), as for any order that is no longer payable.
    - An order being created at a tier price holds the reseller's row (`FOR SHARE`) until it
      commits, so a removal waits for it and then cancels it. An order that would be priced after
      the removal is refused (`FailedPrecondition`); asked again, it gets the store price.
  - **Tiers** (`reseller_tier.upsert`, `reseller_tier.delete`; entity `reseller_tier`, an optional
    reason): create, change at a version with the whole price list, and delete. Deleting is a 409
    while resellers are on the tier.
- **Other roles.** `AddStaff` still takes customers only, so a reseller is removed as a reseller
  first. The configured owner is not made owner while they are a reseller: core logs an error, and
  removing them as a reseller lets the promotion happen. A reseller can be banned: the debt stays,
  and they cannot buy while banned.
- **Overview.** Revenue stays as it was (purchases on credit count when made). For
  `resellers.write`, the overview adds `resellers: {count, debt}`, where debt is what negative
  balances add up to, per currency.
- **License.** The `resellers` feature of the license is not checked yet; the License page
  (milestone 6) brings it.

### API (milestone 5)

| Method and path | Permission |
| --- | --- |
| `GET /api/v1/resellers?q=&tier=&in_debt=&page=&size=`, `GET /api/v1/resellers/{id}` | resellers.write |
| `POST /api/v1/resellers` (make), `PUT /api/v1/resellers/{id}` (terms, with `version`) | resellers.write |
| `POST /api/v1/resellers/{id}/payments` (with a request key), `POST /api/v1/resellers/{id}/remove` | resellers.write |
| `GET`/`POST /api/v1/reseller-tiers`, `PUT`/`DELETE /api/v1/reseller-tiers/{id}` | resellers.write |
| `GET /api/v1/users?referred_by=<id>` (a reseller's customers) | users.read |
| `GET /api/v1/overview` gains `resellers` when the viewer has resellers.write | overview.read |

- **A reseller item** has:
  - `user` (`{id, telegram_id, username, status}`) and `tier` (`{id, name, discount_percent,
    commission_percent}`)
  - `commission_percent` (the effective rate) and `commission_override`
  - `balances` (`[{currency, balance, credit_limit}]`, minor units) and `customers`
  - `commission_earned` (`[{amount, currency}]`) and `created_at`

  The page's item adds `note`, `version` and `can_remove`.
- **Answers.** One item is answered as `{item}`, a payment as `{item, entry}`, a removal as
  `{user_id, role}`, a tier deletion as 204, and field errors as 400 with `field`.
- **Amounts** go in as decimal text in major units: credit limits as `[{currency, amount}]`, tier
  prices as `[{plan_id, price}]` (empty for none). Tier prices come out the same way, with each
  plan's currency.
- **Detail tabs** reuse `/services?user=`, `/orders?user=` and `/ledger?user=`. Services and orders
  now carry `label`.

The bot gains `ListPlans.user_id` (with `Plan.list_price`), `CreateOrder.label`, `Subscription.label`,
`ListSubscriptions` paging and `query`, `Wallet.credit_limit`, and the calls `SetSubscriptionLabel`
and `GetResellerInfo` (tier, rates, balances with the limit and what is available, customers,
commissions, the invite code).

## Milestone 6a: servers and payment gateways

Two pages that change the infrastructure without core ever holding its secrets. The provisioner keeps the 3x-ui
servers and their API tokens (under its own master key); payments keeps Zarinpal's merchant id and proxy (under a new
master key of its own). Core forwards what staff type in the memory of one request, audits that something changed,
and answers hints only.

- **Servers** (`servers.write`: owner and admin).
  - The provisioner (migration provisioner/00003) gives each server a unique name, a weight (1-1000), who manages it
    (`env` or `dashboard`), the last health check (a loop every 60 s over enabled servers, which also reads the panel
    version), and an attach switch per inbound (`server_inbounds`; new clients only).
  - A new client goes to the healthy, enabled candidate with the least clients per weight. The provisioner reserves
    it (`client_map.pending`) before calling the panel, so a retry stays on the same server.
  - The environment's panel (`BOBRES_XUI_*`, now optional) is registered only while there is no server and never
    overwrites one. With no server at all, provisioning fails with "no 3x-ui server", and the order waits and retries
    like any failed delivery.
  - **Servers per plan:** `plan_servers` (core migration 00011) names the servers a plan's new services may go to; no
    row means any. Core sends them as `candidate_server_ids`. Renewals and top-ups stay on the service's server. The
    ids are opaque: core never reads the provisioner's schema. A server a plan names (on sale or not) cannot be
    deleted until staff take it out of the plan, so a plan never sells services nobody can deliver; a server removed
    another way stays named (the page shows it as removed; a plan naming only removed servers cannot deliver).
  - **Links follow the server.** The bot shows the stored `sub_link`. When a server's subscription address (or its
    address) changes, core marks the stored links of its services (`subscriptions.sub_link_stale_at`). A worker then
    fetches them again with `GetLinks`, 25 at a time with a pause. The marks survive a restart; a fetch from before a
    second change is not stored. Only a provisioner that does not answer (its server list fails) pauses the worker,
    with nothing counted. A panel that does not answer puts that service back for 1, 2, 4 ... minutes (at most an
    hour, `sub_link_attempts` counts) and after 8 attempts its link is kept, like any other refusal. The links of a
    switched-on server whose last health check failed, and which has no subscription address, wait 5 minutes without
    asking the panel or counting. So one service or panel never holds up the others.
  - Every write is audited (`server.add`, `server.update`, `server.delete`, `server.inbound`; entity `server`), with
    the settings and `{"token":"changed"}` when a token was sent, never the token.
- **Payment gateways** (`gateways.write`: the owner).
  - **Zarinpal:** on/off, sandbox, and a write-only merchant id and proxy. Payments seeds them once from the
    environment and then keeps them in `gateway_configs`, encrypted with `BOBRES_PAYMENTS_MASTER_KEY`.
  - Switching off only stops offering it: open payments keep being checked. The merchant id and the sandbox cannot
    change while payments are open; the 409 carries the count.
  - A read-only test (Zarinpal's `unVerified`) and a report by status for the last 7 and 30 days.
  - Audited in core as `gateway.update` (entity `gateway`): the switches before and after, plus
    `{"merchant_id":"changed"}` and `{"proxy":"changed"|"cleared"}`.
  - **Manual methods and Telegram Stars:** the new settings `payments.manual_card.enabled`,
    `payments.manual_crypto.enabled`, `payments.manual_zarinpal.enabled` and `payments.stars.enabled`. They are bool,
    owner only and on by default, so existing installs behave the same.
  - Core enforces them: `ListPaymentMethods` leaves Stars out, and `CreatePaymentIntent` refuses a switched-off method
    (FAILED_PRECONDITION). The bot's own buttons may lag by its one-minute cache.
  - The details and switches stay settings: the payments group moved from the Settings page to this page, which saves
    it through `PUT /api/v1/settings` (still owner only).
- **Overview** gains `servers` (`[{id, name, healthy, detail, clients}]` from the provisioner's list; a server not yet
  checked counts as healthy) and `gateways` (`[{provider, enabled, healthy}]`: Zarinpal from payments and its report,
  the others from their settings). `panel` stays as the sum: healthy when every server taking new services answers.
  Why a panel failed names its address, so a server's `detail` carries it only for `servers.write`; everyone else
  reads "does not answer" (or "not checked yet", "takes no new services").

### API (milestone 6a)

| Method and path | Permission |
| --- | --- |
| `GET /api/v1/servers` (each with its health, version, clients, weight and the plans naming it; never the token) | servers.write |
| `POST /api/v1/servers` (201 `{item}`; 409 when a server meant to be switched off could not be), `PUT /api/v1/servers/{id}` (every setting; `token: ""` keeps the stored one) | servers.write |
| `DELETE /api/v1/servers/{id}` (204; 409 while a plan names it or services are on it) | servers.write |
| `POST /api/v1/servers/test` (`{id?, base_url, token, allow_private}`; a failed connection is 200 `{ok:false, detail}`) | servers.write |
| `GET /api/v1/servers/{id}/inbounds`, `PUT /api/v1/servers/{id}/inbounds/{inbound}` (`{attach}`, 204) | servers.write |
| `GET /api/v1/plans` items and `POST`/`PUT /api/v1/plans` bodies carry `server_ids` (absent on a PUT keeps them) | plans.write |
| `GET /api/v1/gateways` (`{providers, settings}`: Zarinpal with hints and health, and the payments settings) | gateways.write |
| `PUT /api/v1/gateways/{provider}` (`{enabled, sandbox, merchant_id, proxy, clear_proxy}`; `""` keeps a secret) | gateways.write |
| `POST /api/v1/gateways/{provider}/test` (candidate settings, or `{}` for the stored ones; saves nothing) | gateways.write |
| `GET /api/v1/gateways/report?provider=&from=&to=` (unix seconds; default the last 30 days) | gateways.write |
| `GET /api/v1/overview` gains `servers` (a panel's failure reason only for servers.write) and `gateways` | overview.read |

- **Errors.** Field errors are 400 with `field` (`name`, `base_url`, `sub_base_url`, `token`, `weight`; `merchant_id`,
  `proxy`, `sandbox`; `server_ids`). A taken server name is a field error on `name`. The provisioner's and payments'
  refusals keep their reason as 409 `state` (a failed connection test, a server with clients or named by plans, the
  payments key missing). Open payments blocking a merchant change are 409 `{error: "state", open: <count>}`. A service that does
  not answer is 503.
- **Limits.** Connection tests (servers and gateways) are limited to 10 a minute per staff member. Calls that may
  test a connection wait up to 30 seconds (servers) or 40 (gateways).
- The dashboard's request and response shapes are the ones in `web/dashboard/src/types.ts`. The server test answer has
  no `version_untested`; the list carries it per server.

### Decisions (milestone 6a, for the owner to veto)

- **Private networks are the owner's.** Letting BOBRES reach a private address (`allow_private`) with a new address
  or token is the owner's alone. An admin gets 403 (field `allow_private`) on add, edit and test, because the
  provisioner would otherwise probe the compose network with a token of their choosing. Admins still rename,
  reweight, switch off and test such a server as stored.
- **A server added switched off** is added and then switched off at once (the provisioner adds servers switched on;
  for that moment it may take a new service). Once added, the rest runs to the end even if the request is cancelled.
  If switching it off fails, core deletes it again and answers 409 ("not added: try again"); if that fails too, the
  audit says it is on and the 409 says to switch it off in the list. It never stays on silently.
- **Links are refreshed** when the subscription address or the panel address changes (the latter changes the panel's
  own links when no subscription address is set).
- **Deleting a server a plan names is refused** (409 naming up to three plans, then how many more), on sale or not:
  taking it out of the plans for staff could widen a plan to any server, and leaving it could sell services nobody
  can deliver. Staff take it out of the plans first.
- **A stored link that cannot be fetched** is tried again with a growing wait and kept after 8 attempts, so one
  service never holds up the refresh; a provisioner that does not answer pauses it without counting.
- **Why a panel failed** (its address) is shown on the Servers page and, on the overview, only to `servers.write`.
- **Without the payments service**, the Payment gateways page answers 503 rather than a page without Zarinpal.
  `sandbox_allowed` comes from core's own `BOBRES_ENV`.
- **The bot's manual payment buttons** still follow the details only. The switches are enforced in core, so a
  switched-off method fails when chosen until the bot reads the switches too (open for the bot).

## Milestone 6b (part 1): system health and the license

The System page's health tab and its License tab. The update check, logs and backups are the sections after this
one (all built).

- **Health and versions** (`system.read`: owner and admin). Core asks core itself (through its own HTTP port),
  the bot, payments, provisioner and the backup service for `/healthz` and `/readyz`, all at once, 3 seconds
  each (new core config `BOBRES_PAYMENTS_URL`, `BOBRES_PROVISIONER_URL`, `BOBRES_BACKUP_URL`; the first two
  default to the compose names `http://<service>:8080`, the backup one to empty: compose sets it to
  `http://backup:8080` only where the backup profile is on). Each service is `ok`, `not_ready` (the failed checks named), `down`, `not_configured`, or
  `not_installed` (the backup service when it has no address or its name does not resolve, including Docker's
  SERVFAIL on internal networks). A version other than core's (a leading `v` ignored) is flagged per service and
  on the page. The servers and payment methods summaries are the overview's.
- **License** (`internal/license`; state for `system.read`, changes for `license.write`: the owner). Migration
  core/00012 adds `core.install` (one random install id, made with the schema) and `core.license` (one token).
  Tokens are verified on every read against the public keys built into core (`internal/license/keys/*.pub`,
  none yet, so every token is "invalid: unknown key"), bound to `BOBRES_DOMAIN` (required) and to the install id
  when the token names one. States: missing, invalid (with a reason code), active, grace, expired.
  - **Policy:** missing or invalid never gates anything. A verified license gates only new actions: making a
    reseller (`resellers`), adding a server beyond `max_servers`, adding admin or support staff beyond
    `dashboard_staff_seats` (owners take none; an owner stepping down counts), switching on a payment method the
    license does not name (wallet always allowed). Grace keeps the entitlements; expired grants none.
  - Only a token that verifies here and has not expired can be installed; `license.set` audits its ids and
    the reason, never the token. The CLI does the same (`bobres license status|import`, source `cli`).
  - **Powered by BOBRES** only with a verified license without `white_label`; never on an unlicensed install.
  - Vendor tool `tools/license` (`keygen` writes the private key outside any git checkout; `sign`). See
    docs/05-license.md.

### API (milestone 6b, part 1)

| Method and path | Permission |
| --- | --- |
| `GET /api/v1/system` (`{checked_at, version, version_mismatch, services, servers, panel, gateways, updates}`) | system.read |
| `GET /api/v1/license` (state, reason, install id, domain, the token's ids and dates, binding, entitlements with use; never the token) | system.read |
| `POST /api/v1/license/check` (`{token}`; the same answer for a token, saving nothing) | license.write |
| `PUT /api/v1/license` (`{token, reason}`; 400 `{field: "token", code}` when it does not verify here or has expired) | license.write |
| `GET /api/v1/me` gains `license: {state, white_label, powered_by}`; public `GET /api/v1/brand` gains `powered_by` | session / public |

- A new action the license does not allow is 409 `{error: "state", license: "<gate>", message}`.

### Update check (milestone 6b)

- **The bot looks, core asks the bot** (core has no internet access). At start and every 12 hours (an hour
  after a failure) the bot fetches the latest GitHub release's `checksums.txt`, `checksums.txt.sig`,
  `keys.txt` and `keys.txt.sig.1-4` through its normal egress (20 s, `HTTPS_PROXY` honoured) and keeps the
  outcome in memory.
- **Verified like the installer.** `internal/release` ports `install.sh` `verify_release` (the same two
  embedded root keys, checked against the script by a test): a root signs `keys.txt`, a listed and not
  revoked release key signs `checksums.txt`, both unexpired, the version at least `min_version`. It keeps no
  state, so there is no seq or downgrade check; `bobres update` verifies again before installing anything.
- `GET /internal/update-check` (core's token) answers `{current, latest, channel, newer, verified, checked_at,
  error, detail}`. `newer` is true only for a verified release above the running version; a `dev` or other
  non-semver build never sees one. `error` is `""`, `not_checked`, `unreachable` (latest empty), `no_release`,
  `invalid` (no version header) or `bad_signature` (unsigned, tampered with, revoked or expired: latest is the
  claimed version, never offered as an update); `detail` is a short English reason.
- `GET /api/v1/system` gains `updates`: the bot's answer, or `{error: "bot unreachable"}` when the bot is not
  configured or does not answer within 3 seconds. Applying an update stays the `bobres update` command.

### Logs (milestone 6b)

- **Kept in memory, after redaction.** Every service's logger (`internal/logging`) also writes its warnings
  and errors, whatever the stdout level and format, through the same redaction into a ring of the newest 500
  lines: `{time (unix seconds), level (WARN | ERROR), msg, attrs}`, `attrs` being the line's other fields as a
  JSON object (`service` dropped) and `msg` and `attrs` cut at 1000 bytes with a trailing `…`. Nothing is
  written to disk; a restart empties it. Postgres, Redis and Caddy stay with `bobres logs`.
- **Who serves them.** Core reads its own; the bot answers `GET /internal/logs` (core's token,
  `{lines}`); payments and provisioner a `RecentLogs` RPC on their existing services (core is their only
  peer; their HTTP mux is public through Caddy).
- `GET /api/v1/system/logs?service=core|bot|payments|provisioner` (`system.write`: the owner; the lines carry
  Telegram ids and usernames) answers `{service, lines, error}`, lines oldest first. `error` is `""`,
  `unreachable` (the service did not answer within 3 seconds; the details go to core's log) or
  `not configured` (the bot without `BOBRES_BOT_URL`); any other service name is 400.

### Backups (milestone 6b, part 2: the service)

- **A new `backup` service** (`cmd/backup` on `app.Run`; image `bobres-backup`, the Dockerfile's `backup` target
  from `postgres:16-alpine`, uid 65532; backend network only; `./backups:/backups`). It runs behind the Compose
  profile `backup`, which `bobres install` turns on (`COMPOSE_PROFILES=backup`, `BOBRES_BACKUP_URL` for core) when
  the install's image version has the service, so a pinned older version still starts.
- **A run** (`internal/backupsvc`; one at a time): disk check (free space at least twice the last backup, 100 MiB
  before the first), `pg_dump -Fc` as the read-only `svc_backup` role streamed into `internal/backup.Write` (keys
  member: the three master keys that are set) into a temp file, fsync, rename to `bobres-<UTC stamp>.bkp`, sidecar
  `{file, size, sha256, created_at, sent_at, telegram_error, error}`, delivery to the bot's
  `/internal/backups/deliver` (streamed from disk, 20 minutes; the outcome goes into the sidecar), then retention
  (`backup.Prune`, 7). A failed run leaves its sidecar with the error and no `.bkp`. Daily at `BOBRES_BACKUP_TIME`
  (03:30) in `BOBRES_TIMEZONE`.
- **HTTP for core** (core's token): `GET /internal/backups` `{backups (sidecars, newest first), running,
  last_error, next_run (unix), disk_free, dir}`; `POST /internal/backups/run` 202 `{started: true}` or 409
  `{started: false, reason: "running"}`. `bobres backup` runs `/app run-now` in the container, which uses the
  same API and waits.
- **Core's API** (core asks with its service token, within 3 seconds; core has a backup client only when
  `BOBRES_BACKUP_URL` is set, that is when the profile is on):

| Method and path | Permission |
| --- | --- |
| `GET /api/v1/system/backups`: the service's list as it answers it, `{installed: false}` when the service is not installed, `{installed: true, error: "unreachable"}` when it does not answer (details in core's log) | system.read |
| `POST /api/v1/system/backups/run`: 202 `{started: true}` (audited `system.backup`, source dashboard, no details), 409 `{started: false, reason: "running"}`, 503 `not_configured` (not installed) or `unavailable` (no answer) | system.write |
| `GET /api/v1/system` gains `backups`: `{installed, last_created_at (unix, the newest backup that worked; null before one), last_error, next_run}`, `{installed: false}` or `{installed: true, error: "unreachable"}` | system.read |

- **Next part:** the dashboard's Backups tab.

### System alerts (Phase 7, P7c)

No Prometheus or Grafana (too heavy for a small VPS): core's own watchdog, and the bot's staff alerts.

- **The watchdog** (`domain.Watchdog`, the worker `watchdog` in cmd/core) runs every minute, its sources asked at
  once, each within 10 s. Each check has a key:

| Key | Problem when | Text (`alert.*`) and values | Severity |
| --- | --- | --- | --- |
| `service.<name>` | a service is `down` or `not_ready` (`internal/core/syshealth`; not installed and not configured ones are not watched, except the backup service when core has its address and token: then a name Docker does not resolve is a stopped or restarting container, so `down`) | `service_down` / `service_not_ready` {service} | critical |
| `server.<id>` | an enabled 3x-ui server failed its last health check (`ListServers`; never checked: skipped) | `server_down` {server} | critical |
| `gateway.zarinpal.error` | Zarinpal is switched on and its report is not healthy (an error since the last paid payment, under an hour old) | `zarinpal_failing` | critical |
| `gateway.zarinpal.open` | Zarinpal is switched on and an open payment is over 3 hours old (payments keeps one open by design for its 1-hour pay window, 45 minutes of grace and its reconciler's backoff of up to 32 minutes, so a checkout the customer left is not stuck) | `zarinpal_stuck` {count} | critical |
| `backup.failed` | the backup service is installed and its last run failed | `backup_failed` | warning |
| `backup.stale` | the newest good backup is over 26 hours old (none yet: not checked). While the service does not answer, the newest one it last reported still ages (after a restart of core: the time it stopped answering), so backups do not stop silently | `backup_stale` {hours} | warning |
| `backup.disk` | the backup disk has under 2 GB free (unknown: not checked) | `disk_low` {free} | warning |
| `orders.provision_failed` | an order's last delivery attempt failed in the last hour (one being retried right now still counts: it is `provisioning` with its last error) | `provision_failed` {count} | critical |
| `events.bot_lag` | the oldest core event the bot has not read is over 10 minutes old (the feed keeps a cursor per gRPC caller: `bot`, the peer name cmd/core gives it, `domain.BotFeedConsumer`) | `bot_lag` {minutes} | critical |
| `license.expiry` | a license that verified here expires within 14 days, is in its grace period or has expired (missing or invalid: not watched) | `license_expiring` {days, date} / `license_grace` {date} / `license_expired` | warning |

- **On change only.** `core.alert_state` (migration core/00013: `key` PK, `state` ok | pending | problem, `since`,
  `last_sent_at`, `passing_since`, `detail`, `severity`, `title_key`, `args`) keeps each check's state across
  restarts.
  - A check seen failing once is `pending` (nothing is sent: a service restarting during `bobres update` is not
    announced); seen failing again a minute later it is a `problem`, announced as open since it was first seen.
  - A problem that passes stays open (and on the System page) until it has passed for 5 minutes (`passing_since`);
    failing again starts those 5 minutes over. Then it is announced as resolved, its duration counted until it first
    passed. A pending check that passes goes back to ok quietly.
  - A check that fails again within 30 minutes of its fix stays `pending` until those 30 minutes are over, and is
    announced then if it still fails. So a check that flaps (a server behind a filtered link, the disk near 2 GB)
    sends one alert, not a problem and a fix every few minutes.
  - A problem still open after 6 hours is sent again then, and every 24 hours after that (`repeat: true`, with
    the current values, e.g. the days left).
  - Another problem on the same check (the license from expiring to grace) is announced as new.
  - A source that did not answer (the provisioner, payments, the backup service) leaves its checks as they were:
    its own service check reports it (only `backup.stale` can still become a problem, above). A check its source
    no longer makes (a server removed or switched off, Zarinpal switched off, the backup service uninstalled, the
    license removed) is forgotten without a message.
  - State changes and their events commit together.
- **Delivery.** Core publishes `system.alert.v1` (see 02-events-and-apis.md). The bot sends it to the staff alert
  recipients (`notify.recipients`, like failed deliveries; never support staff), each in their language: a heading
  by severity (or "still not fixed after", "fixed after"), the sentence, and the check's own words in `<code>`.
  The setting `notify.system_alerts` (bool, default true) turns the messages off; the checks and the System page
  go on. Alerts about the bot itself or its feed reach Telegram once the bot is back (then the problem and its
  fix arrive together); the System page shows them at once.
- **API.** `GET /api/v1/system` gains `alerts`: the open problems (state `problem`), critical first, then the
  oldest: `[{key, severity, title_key, args, title (the bot's English text, without tags), detail, since
  (unix)}]`, `[]` when all is well, `null` when they could not be read. A server's error is shown only to staff
  who may manage servers, as on the overview, and Zarinpal's only to the owner, as on the gateway pages
  (`gateways.write`); the others get `detail: ""`.

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
- **Milestone 3 (Go).**
  - Core, over TLS against a real database:
    - settings: normalising, all or nothing, field errors, the private-channel rule, payment details
      refused to admins, support refused
    - branding and the logo: SVG, tiny and oversized images refused; the public brand and the
      served image with its sandbox CSP
    - bot texts: live check, refused broken texts, export, an all-or-nothing import
    - staff rules: self, configured owner, co-owners, the owner role, banned customers, sessions
      ending on role changes and removal, reset, unlock
    - two owners demoting each other (stale actor), my sessions, the password re-check, failed-login,
      link and export audit entries, the append-only trigger, the owner bootstrap
  - The text checker: every built-in text passes its own checks, and a table of broken overrides
    (unclosed and crossed tags, script, links to http or javascript, raw `<` and `&`, markup in
    buttons, Telegram lengths, `<code>{addresses}</code>`) is refused; the bot drops them on load.
  - The bot (with a fake Telegram):
    - maintenance and the required channel with every exemption, the member cache, failing open
    - limits, alert switches and staff recipients, `/terms`, `/dashboard` for support, `/set`
      showing core's reason
    - the refresh and channel-check endpoints, and the plain-text fallback
- **Milestone 3 (dashboard, vitest).** Branding helpers (contrast, the preview tokenizer, lengths), the
  settings checks, the staff action rules and device names. The pages boot against a fake core:
  - texts up to the live check
  - the store form with a field error and the colour restored on leaving
  - the settings cards and the maintenance confirm
- **By hand (milestone 3),** a local core with a stand-in for the bot's internal endpoints, in the
  browser:
  - maintenance turned on after the confirm, the bot told at once, the audit entry with before/after
    and IP; the channel check through the bot
  - store name, colour and logo saved and applied to the sidebar, the tab title and the login page;
    a broken welcome text refused live, a good one saved
  - a customer added as support, a role changed, the audit log's details
  - My account's sessions, Persian and English, dark and light, phone width with no sideways
    scrolling
  - the embedded build under the real CSP with no violations (logo, `data:` preview, colour)
- **Milestone 5 (Go).**
  - Store: debits down to the credit limit and no further (also 20 at once), credits and zero
    entries while in debt, limits per currency, the database check against a direct write, labels,
    and the paged services list with its search (label in any case, `%` and `_` as letters, id
    suffix).
  - Domain:
    - prices (fixed, discounted and rounded, customers, a banned reseller); quotes and orders for a
      reseller; codes refused; labels kept and checked, ignored for customers and renewals
    - paying on credit to the limit and refused beyond; the label through delivery, renewal and
      reminder events; payments and refunds lowering the debt
    - settlements once per key with their audit entry and event
    - commissions once per order, at the reseller's own rate, never to a banned reseller, not on a
      reseller's own purchases (nor their inviter's reward), for customers invited before the role,
      and a conflicting key rolling the payment back
    - make, terms (stale, below the debt, currencies left out), remove (refused in debt), tiers
      (field errors, names, versions, in use), renaming, the reseller's panel, and the configured
      owner not promoted while a reseller
    - removal cancelling unpaid orders (paying one afterwards is refused, a late payment stays in
      the wallet, nobody earns a commission), with an order in flight (cancelled after it commits)
      and an order that comes after the removal (refused)
  - The dashboard API: every endpoint's permissions (support 403, admin allowed), field errors, the
    409s, replays, the customers filter, labels on services and orders, the audit trail, and the
    overview field. gRPC: tier prices in `ListPlans`, labels, paging and search, renaming and the
    panel. The migration's Down: refused in debt, the old check back, the new kinds kept.
- **Milestone 6a (Go).**
  - Core store and migration: plan servers (kept, replaced, cleared, refused when not ids), stale links (only
    services on the panel, a second change wins, a link put back waits and counts, a new mark starts over), the
    migration's Down and Up.
  - Domain: server input checks with no secret in an error, audit entries with `"token": "changed"` and no token,
    the owner-only private network rule, a new subscription address refreshing the stored links (and a rename not),
    the worker pausing when the provisioner does not answer (before or during a fetch, nothing counted), one panel
    that does not answer never holding up the rest (growing waits, the link kept after the last attempt, a down
    panel's links waiting uncounted), keeping a link it refuses, a server switched off on add never left on silently
    (deleted again, or reported and audited as on; a cancelled request still switches it off), a delete refused while
    plans name the server, plan servers sent as candidates (and kept by the bot's plan edits), gateway saves audited
    without the merchant id or proxy, the health rule, and the method switches in `ListPaymentMethods` and
    `CreatePaymentIntent`.
  - The dashboard API against fakes: every endpoint and permission (admin: servers yes, gateways no; support no),
    field errors that never repeat a secret, the provisioner's and payments' refusals, the 409 with the open count,
    the rate limits, plan `server_ids`, and the overview (a panel's failure reason hidden from support).
  - End to end over gRPC: the real provisioner against the fake panel (a connection test refusing a wrong token, a
    plan's service delivered on its server, its stored link following a new subscription address, a delete refused
    while it has a client), and the real payments service against the fake Zarinpal (the merchant id stored
    encrypted, the read-only test, a merchant change refused under an open payment while switching off works).
  - The installer: no panel on a fresh install, never asked on a re-run, URL and token together, the payments key
    generated and kept; the menu no longer reports Zarinpal from `.env`.
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
