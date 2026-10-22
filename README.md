![BOBRES, the beaver mascot with a teal shield: Your VPN store on Telegram](docs/brand/bobres-banner.webp)

# BOBRES 3x-ui Telegram Panel

Launch your own VPN store on Telegram in one command.

BOBRES sells and manages 3x-ui (Xray) VPN subscriptions: a Telegram bot for customers and resellers, a web
dashboard for staff, and the `bobres` command on the server. It is white-label and single-tenant: each operator runs
it on their own server, with their own domain, bot, 3x-ui panels and brand.

## Status

Phases 0 to 7 of the plan are built and tested in CI ([docs/PLAN.md](docs/PLAN.md) §10, [CHANGELOG.md](CHANGELOG.md)).
Two parts were set aside on purpose: third-party crypto processors (they exclude Iran) and adapters for older 3x-ui
versions (built only when a customer needs one). The first public release, v0.1.0, came out on 2026-10-14; v0.11.0
(plan groups: plans in named groups with a text each, so a customer picks a group and reads it before choosing a plan;
v0.10.1 put the owner's balance at the supplier always in view, v0.10.0 added reselling a supplier's digital accounts from the owner's
own bot and the lock that keeps the vendor's own features out of customers' installs, v0.9.1 managing every license of the license
server from the dashboard, and v0.9.0 selling BOBRES licenses from the owner's own bot) is the latest. They were tried in a Docker lab with two Ubuntu servers
([docs/11-staging-run.md](docs/11-staging-run.md)); an install on a real server, with real payments and a real
certificate, is still to do. The landing page is live at https://sobhanaz.github.io/bobres-3x-ui-telegram-panel/.
Still open: the legal review, the brand checks (domain, bot name, trademark) and the prices. The business model is decided: a free core
and a paid subscription per install ([docs/pricing.md](docs/pricing.md)). BOBRES already carries the vendor's license
key (`vendor-v1-2026`, fingerprint `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7`), so licenses signed with it verify; the
license server is built but still has to be deployed by the owner. What is left before selling is in
[docs/launch-checklist.md](docs/launch-checklist.md).

## Features

**Telegram bot, for customers**
- Persian and English, in the operator's own name, texts and links.
- Buy a plan (traffic-limited, time-only or both), with an optional discount code. The service arrives with its
  subscription link and a QR code.
- Plan groups: the operator can put plans in named groups with a text for each (what the group is for, who it suits,
  what it does not include). In the bot and the Mini App the customer first picks a group, reads its text, then picks
  a plan of it; a store with no group lists its plans as before.
- My services: status, expiry and traffic used; renew, or add a traffic package, with the same link.
- A wallet with top-ups and history, gift codes that add credit, one free trial per Telegram account, and invite
  links that reward the inviter on a friend's first paid purchase.
- Reminders 3 days and 1 day before expiry, at 80% of the traffic and at the end, with Renew and Add traffic buttons.
- Support conversations with text, photos and PDFs; staff replies arrive in the chat.
- Store rules: maintenance mode, required channels and groups (one on the free tier, more with Pro), rate limits, terms and privacy links.
- A page for each service at `/sub/<token>`, under the store's own brand: usage and days left, the subscription link
  and QR code, import buttons for v2rayNG, Hiddify, Streisand and V2Box, and a Renew button. The import links are not
  yet verified on real devices.
- A Telegram Mini App store at `/app/`, on for every store: the bot opens it from a Store button and Telegram's menu
  button. Plans, buying, services, renewals, the wallet, the free trial, invites and support, paid with the wallet, a
  gateway, Telegram Stars, or card to card, USDT and a Zarinpal link with a receipt. It follows the store's rules as
  the bot does (bans, maintenance, the required channel). It is not yet checked against a real Telegram.

**Resellers, in the bot and the Mini App**
- A reseller panel: buy for customers at the tier's prices, on credit up to a limit; name and find their services;
  a statement; an invite link that earns a commission on their customers' purchases.
- Manage their customers' services: pause and resume, delete (after a confirmation), a private note, the list in
  groups (active, ending soon, ended, paused) with counts, and the customers who joined through their link with what
  they bought. Nothing that gives free days or traffic.

**Staff**
- In the bot: `/admin` (stats, find a user, adjust a balance, ban, a list of the payments to review with their receipts),
  `/set`, plans, trials, traffic packages, discount codes, and `/dashboard` for a one-time dashboard login link.
- Alerts in Telegram: payments to review, new tickets, failed deliveries, sales (optional) and system problems.
- Several owners (root admins), named on the server with `bobres admin owners`: all of them get the alerts and the daily
  backup and see the same list of payments to review; the first decision on a payment counts.

**Web dashboard** at `https://<domain>/admin`
- Log in with a one-time link from the bot, or with a password and an authenticator code. Roles: owner, admin,
  support. Persian and English, dark and light.
- Overview, users, services, plans, plan groups and packages, orders and payments with the receipt review queue, the ledger with
  CSV export, discounts, gift codes and referrals, resellers, the support inbox, broadcasts, branding and every bot text,
  settings, panel servers, payment gateways, staff, the System page (health, updates, logs, backups, HTTPS
  certificates, license) and the audit log.
- Reports: revenue per currency by day, week or month (Gregorian or Jalali), orders, new customers, renewals, churn,
  top plans and which provider paid, with CSV files and an optional daily summary in Telegram.

**Payments**
- The wallet; card to card, USDT (TRC20 or ERC20) and a Zarinpal payment link, each with a screenshot staff approve;
  Zarinpal online (off until a merchant id is set); Telegram Stars; automatic USDT on TRON (TRC20), which matches an
  exact, unique amount on the chain; IDPay and NextPay, two more Iranian card gateways (off until credentials are set,
  tested against fakes only).
- Each payment settles exactly once. The ledger is append-only, and amounts are whole numbers in minor units.

**Operations**
- A one-command install on Ubuntu or Debian, and the `bobres` menu and commands on the server.
- Several panel servers, each 3x-ui or Marzban (Marzban 0.8.4 is tested against the real panel in CI), with health checks, weights,
  servers per plan and inbound choices.
- Daily encrypted backups: the newest 7 kept on the server, each sent to the owner on Telegram, and optional copies
  in S3-compatible storage. `bobres restore` puts one back.
- `bobres update` with stable and beta channels and an automatic rollback; `bobres rollback` undoes the last update.
- HTTPS certificates, from the dashboard's HTTPS tab or `bobres ssl`: Caddy's automatic certificate for the domain
  (the default), a Let's Encrypt certificate for the server's IP address (about 6 days, renewed by Caddy), or your own
  certificate and key. A change is saved only once Caddy accepts it, and `sudo bobres ssl auto` is the way back when a
  wrong certificate locks browsers out.
- System alerts in Telegram, operator runbooks, a redacted support bundle, server hardening, `bobres config` and
  `bobres secrets rotate`.

**Free core, paid premium**
- No license is needed to run a store. An install without one has a free tier: 1 server, 25 active services, 2 staff
  seats, the Zarinpal, Stars and manual payment methods, and security updates always. Resellers, automatic USDT,
  more servers, services, staff and required channels, and removing "Powered by BOBRES" come with a license.
- Every install starts with a 30-day trial that has everything but white label. When it ends, the limits stop only
  new servers, services, resellers, staff and payment methods: existing services keep working and renewing, and a
  customer never sees a word about licensing ([docs/05-license.md](docs/05-license.md)).

**Security**
- Four services, each with its own database role and service token. Only the provisioner holds the 3x-ui tokens, only
  payments holds the gateway secrets, and core has no internet access.
- Secrets at rest are encrypted with a master key per service. Releases are signed (Ed25519); images are signed with
  cosign and run by the digests the signed CLI carries.
- Core still never controls the gateway: a small `tls-agent` with no network is the only writer of Caddy's TLS files.
  An own certificate's private key is sealed with core's master key.
- The dashboard has CSRF protection, authenticator codes for every password login, a strict CSP and an append-only
  audit log.
- Licenses are signed tokens bound to the store's domain and install. Without one, an install runs on a free tier
  (1 server, 25 active services, 2 staff seats) after a 30-day trial; the limits refuse only new actions, never
  what exists.

## Install

You need:
- A server with Ubuntu 22.04/24.04 or Debian 11/12/13 (amd64 or arm64), 1 GB of RAM (2 GB recommended), 10 GB free
  disk, ports 80 and 443 free, and root access.
- A domain whose A record points to the server.
- A new Telegram bot from @BotFather, and your numeric Telegram ID.
- A 3x-ui panel (v3.8.5 to v3.10.2 are tested) with an API token. You can also add panels later in the dashboard.

As root on the server:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
```

It installs Docker if needed, downloads the `bobres` CLI
from the latest GitHub release, verifies its signature and checksum, and asks for your domain, Telegram ID, bot token
and 3x-ui panel (tokens are typed hidden; the panel's token is checked before anything is written). Then it hardens
the server by itself where that is safe and ends with a "BOBRES is ready" card and your backup key. It shows the BOBRES
banner and one progress bar from 0 to 100%, keeps going if your SSH connection drops (follow it with
`tail -f /var/log/bobres-install.log`), and prints plain lines when the output is a pipe or a log (`--plain`,
`--no-banner`, `bobres about`). Details: [docs/06-installer.md](docs/06-installer.md).

Afterwards run `bobres` for the management menu:

```text
   n____n   BOBRES manager  bobres v0.11.0
  ( o  o )  /opt/bobres  ·  shop.example.com  ·  version 0.11.0
   \_||_/   Store: running (9/9 services)
  ──────────────────────────────────────────────────────────
   1. Status
   2. Live logs (Ctrl+C returns here)
   3. Start
   4. Stop
   5. Restart
  ──────────────────────────────────────────────────────────
   6. Show settings
   7. Change the bot token
   8. Owners (Telegram IDs of the store owners)
   9. Change the first 3x-ui panel (used until the dashboard has a server)
  ──────────────────────────────────────────────────────────
  10. Back up now
  11. Backup key
  12. Restore a backup
  ──────────────────────────────────────────────────────────
  13. Update BOBRES (check for a new release)
  14. Roll back the last update
  ──────────────────────────────────────────────────────────
  15. Dashboard login link (if the bot is down)
  16. Check this server
  ──────────────────────────────────────────────────────────
  17. HTTPS certificate
  ──────────────────────────────────────────────────────────
  18. Uninstall
  ──────────────────────────────────────────────────────────
   0. Exit
```

Every item is also a command: `bobres status`, `bobres logs -f`, `bobres backup`, `bobres update`, ... (`bobres help`).

### Uninstall

| Command | What it does |
|---|---|
| `sudo bobres uninstall` | Stops and removes the containers; the data, the certificates and `/opt/bobres` stay (`bobres start` brings the store back) |
| `sudo bobres uninstall --purge --dry-run` | Prints exactly what would be removed, with sizes, and changes nothing |
| `sudo bobres uninstall --purge` | **Day one:** removes everything BOBRES put on the server (data, secrets, backups, containers, volumes, images, systemd units, firewall rules, the files the hardening wrote, the `bobres` command), so that the server is as it was. Asks for a verified final backup, then for the domain typed in full |
| `sudo bobres uninstall --purge --backup-to FILE` | The same, with a final backup written outside the install directory and read back before anything is deleted |
| `sudo bobres uninstall --purge --remove-docker --remove-packages` | Also removes Docker and the packages BOBRES installed, when nothing else uses them |
| `sudo bobres uninstall --verify` | Scans the server for what is left of BOBRES |

It touches only what it can prove is BOBRES's, never SSH settings or the SSH firewall rule, and can be run again after an
interruption. What installing BOBRES changes on a server, flag by flag, and how to restore the final backup on a new
server: [docs/06-installer.md](docs/06-installer.md) and [docs/runbooks/uninstall.md](docs/runbooks/uninstall.md).

## Docs

- [Docs index](docs/README.md): every document, with what it covers.
- [Project plan](docs/PLAN.md): decisions, architecture and the status of each phase.
- [Roadmap](docs/ROADMAP.md): what comes next, in order.
- [Runbooks](docs/runbooks/README.md): what to do when something goes wrong, and the routine jobs.
- [Pricing and licenses](docs/pricing.md): the free tier, the paid tiers, and what happens when a subscription lapses.
- [Legal drafts](docs/legal/README.md): terms, privacy, acceptable use and refunds, in English and Persian (drafts for a lawyer).
- [Brand](docs/08-brand.md): BOBRES the beaver, the assets and the rules for using them.
- [Launch material](docs/marketing/README.md): posts, outreach texts, a pitch, a price-list template and the claims checklist; the public site is in [`site/`](site/).
- [Changelog](CHANGELOG.md).

## How it is built

A Go monorepo with four services (`core`, `bot`, `payments`, `provisioner`) and a `backup` service, behind Caddy, which a
small `tls-agent` (core's image, without a network) configures for HTTPS, with PostgreSQL (one schema and one role per
service) and Redis. Services call each other over gRPC and pass events through each service's outbox. The dashboard is
Vue 3, built into the `core` binary.

## Development
- Go 1.26 or newer (`go.mod` says `go 1.26.0`; CI and the Docker image track the latest 1.26 patch).
  `make` forces `GOTOOLCHAIN=local`, so it builds with the `go` on your PATH: use a 1.26.x for
  byte-for-byte parity with CI and the images. Outside `make`, an older Go auto-downloads
  go1.26.0 (the oldest patch); install a current 1.26.x instead.
  `make lint` needs golangci-lint v2: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`.
- `make build`, `make test`, `make lint`, `make proto`.
- The web dashboard (`web/dashboard`, Vue) needs Node.js 24: `make dashboard` builds it into the
  folder core embeds (run it before `make build`), `make dashboard-test` type-checks and tests it.
  For live editing, run a local core on port 8088 and `npm run dev` in `web/dashboard`.
- Two throwaway Ubuntu 24.04 "servers" in Docker (one for the bot stack, one for a 3x-ui panel),
  to install and try everything as on real VPSes: `deploy/lab/README.md`.
- Database tests need PostgreSQL 13+ (`BOBRES_TEST_DATABASE_URL`, a URL for any database on
  a server where the role may `CREATE DATABASE`; default: the local socket in `/tmp`) and
  Redis (`BOBRES_TEST_REDIS_ADDR`, default `127.0.0.1:6379`). Each test package gets its own
  throwaway database. They skip when the servers are missing, except with
  `BOBRES_TEST_REQUIRE_DB=1` (CI), where they fail. `BOBRES_TEST_POOL_MAX_CONNS=4` caps the
  tests' connection pools (pgx gives `max(4, cores)`): run it on the packages that hold locks
  and transactions while goroutines wait (`internal/core/domain`, `internal/core/store`) to
  prove a test does not need a big machine; CI does.

## License

BOBRES is **source-available, all rights reserved**: the [LICENSE](LICENSE) (a draft, to be reviewed by a lawyer before the
public launch) lets anyone read and audit the code. Without a paid license you may run an official release for your own store
on the free tier. Copying or redistributing the code, hosting it for others, reselling it and white-labelling it need a paid
license from the owner. The tiers and prices are in [docs/pricing.md](docs/pricing.md). The licenses of the components BOBRES
uses are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Credits

© 2026 BOBRES, all rights reserved. Made by TECSO-DEV.

## Meet BOBRES

<img src="docs/brand/bobres-wave.webp" alt="BOBRES the beaver waving" width="160">

The beaver is BOBRES: a friendly, sturdy builder whose shield stands for security. The brand kit and its rules are in `docs/08-brand.md`.
