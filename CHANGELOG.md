# Changelog

All notable changes to BOBRES are written here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] — first public release (v0.1.0)

The first release. It sums up what BOBRES does now: Phases 0 to 7 of the plan ([docs/PLAN.md](docs/PLAN.md) §10),
pull requests #13 to #39. Everything below is new in this release.

### Bot
- Persian and English. Customers pick a language on their first `/start` and can change it.
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
- Store setup: branding (name, logo, colours) and every bot text in both languages, settings, staff, and the audit
  log with CSV export.
- 3x-ui servers: several panels with a write-only token, weight, on/off for new services, version and health, a
  connection test, servers per plan, and an attach switch per inbound.
- Payment gateways (owner): Zarinpal and automatic USDT settings with a connection test and a report of the last 7 and
  30 days, unmatched USDT deposits, and the manual methods and Telegram Stars with their switches and details.
- System page: health and versions of every service with the open problems, the update check (with the channel and
  the license's coverage), the newest warnings and errors of every service (owner only), backups (list, run one,
  off-site copies), and the license (state, entitlements and their use, paste a token, or activate with a license
  key).
- A "Powered by BOBRES" footer only under a valid license without white label.

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
- `bobres config` for the safe settings and `bobres secrets rotate` for the bot token, database passwords, service
  tokens and the Redis password.
- `bobres support-bundle`: a redacted archive for support, without the database, backups or secret values.
- Runbooks for common incidents and routine jobs ([docs/runbooks](docs/runbooks/README.md)).
- Failure drills and a restore drill in the tests, and a load test with its numbers
  ([docs/12-load-test.md](docs/12-load-test.md)).
- Licenses: a signed token pasted on the dashboard or imported with `bobres license import`, or activated online with
  a license key (`bobres license activate`) and refreshed daily. The vendor's license server, `licensesrv`, is part of
  the repository ([docs/13-license-server.md](docs/13-license-server.md)).

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
- A missing or invalid license blocks nothing, and a license only ever gates new actions.

### Brand
- BOBRES the beaver: the mascot is the logo. A flat icon, a 3D render, a waving animation, a banner and a bot avatar,
  with a navy and teal palette ([docs/08-brand.md](docs/08-brand.md)).
- The dashboard's default look: the beaver favicon and logo, and the waving beaver on the login page (a still picture
  for reduced motion). A store's own logo replaces it.
- A small beaver in the `bobres` menu header.

### Known limits
- Not built: third-party crypto processors (they exclude Iran), TON, older 3x-ui adapters (only v3.x, tested on
  v3.8.5 and v3.9.0), a branded subscription page, re-encryption with new master keys, metrics dashboards.
- No vendor public key is embedded yet, so every license token is refused as "unknown key" until one ships. A missing
  license blocks nothing.
- Image signatures are not checked on the server (the release workflow checks them before it builds their digests
  into the CLI). Caddy, Postgres and Redis come from Docker Hub by tag.
- The legal review, the brand checks and license pricing are open.

[Unreleased]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/commits/main
