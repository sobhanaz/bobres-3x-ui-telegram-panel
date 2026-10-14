# Claims checklist (English)

Every claim the public material makes (the page in `site/`, the posts, the outreach texts, the pitch), with the file
that proves it. Persian version: [claims-checklist.fa.md](claims-checklist.fa.md), same numbers.

**How to use it.** Before you publish or change a sentence, find its row. If there is no row, there is no proof:
do not publish it. A row's status:
- **Verified**: the proof file says it, read on 2026-10-14 against `main` at 308383d. The license policy
  (`docs/05-license.md`, `internal/license/policy.go`, `internal/license/tiers.json`) and `docs/pricing.md` are on it.
- **Owner decision**: it comes from a decision of the owner that no file settles. Only row 63 is left (where people
  reach the owner).
- **Confirm**: a step the owner must still take before the page goes public. None is left: `v0.1.0` was published on
  2026-10-14 (row 53).

Rows 71 and up were added by the pre-publication review (2026-10-14); they sit in their sections. Rows 40, 44, 46, 55,
60, 61, 63, 64 and 66 were reworded by it: the old wording said more than the proof file does. Rows 54 to 61, 64 and 71
waited for the license policy (they were "Owner decision", and row 58 "Confirm"); once it and `docs/pricing.md` were
merged, each was checked against them.
Rows 7, 27, 48, 49, 65, 75 and 76 were changed or added by the review's last pass.

## A. What BOBRES is

| # | Claim | Where | Proof | Status |
|---|---|---|---|---|
| 1 | Launch your own VPN store on Telegram in one command. | page hero, post 1 | `README.md` (intro); `docs/08-brand.md` (one-liner); `install/install.sh` | Verified |
| 2 | It sells and manages 3x-ui (Xray) VPN subscriptions. | page, posts, pitch | `README.md` (intro) | Verified |
| 3 | A Telegram bot for customers and a web dashboard for staff. | page, posts | `README.md` (intro); `CHANGELOG.md` (Bot, Dashboard) | Verified |
| 4 | It runs on the operator's own server, with their domain, bot, panels and brand. | page, posts, pitch | `README.md` (intro: "each operator runs it on their own server") | Verified |
| 5 | The repository is public. | outreach reply 5 | `gh repo view`: visibility PUBLIC (2026-10-14) | Verified |
| 6 | BOBRES is an independent project, not affiliated with Telegram or 3x-ui. | page footer | Editorial: no agreement exists; names belong to their owners | Verified |
| 7 | No telemetry or analytics. Free installs are not counted: only licenses that activate reach the license server. | content calendar | `grep -ril "telemetry\|analytics\|beacon" internal cmd install deploy`: no match (2026-10-14); `docs/05-license.md` (Privacy); `docs/legal/privacy-policy.md` (4. What the Vendor receives) | Verified |
| 73 | Operators are responsible for local law, Telegram's terms and their payment providers' rules. No text promises that using BOBRES is legal anywhere. | page footer, pitch | Editorial; `docs/09-legal-questions.md` (the open legal questions, items 21 to 23) | Verified |
| 75 | Everything in the page's feature list is built, and nothing that is not built is listed. | page (Features) | `README.md` (Status: phases 0 to 7 built and tested in CI; Features); `CHANGELOG.md` (the Bot, Dashboard, Payments, Operations and Security lists); rows 8 to 45 give the proof of each card, item by item. The rule behind the sentence: a feature with no row here is not listed | Verified |

## B. The bot

| # | Claim | Where | Proof | Status |
|---|---|---|---|---|
| 8 | The bot speaks Persian and English; customers pick a language and can change it. | page, posts | `CHANGELOG.md` (Bot, first line) | Verified |
| 9 | A bottom menu stays under the chat, with buttons colored by what they do: green to pay or confirm, blue for the main actions, red to cancel. | page, posts | `CHANGELOG.md` (Bot); `internal/bot/i18n/meta.go` (`buttonStyles`) | Verified |
| 10 | Buy a plan (traffic, time or both) with an optional discount code; the service arrives with its subscription link and a QR code. | page, posts | `CHANGELOG.md` (Bot) | Verified |
| 11 | Renewing keeps the same subscription link; customers can add traffic packages. | page, posts | `CHANGELOG.md` (Bot: My services) | Verified |
| 12 | A wallet with top-ups and a history of transactions. | page, posts | `CHANGELOG.md` (Bot) | Verified |
| 13 | One free trial per Telegram account. | page, posts | `CHANGELOG.md` (Bot) | Verified |
| 14 | Invite links reward the inviter in the wallet on the friend's first paid purchase. | page, posts | `CHANGELOG.md` (Bot) | Verified |
| 15 | Reminders before a service or its traffic ends, with Renew and Add traffic buttons. | page, posts | `CHANGELOG.md` (Bot: reminders 3 days and 1 day before expiry, at 80% of traffic and at the end) | Verified |
| 16 | Support in the bot, with text, photos and PDFs. | pitch | `CHANGELOG.md` (Bot: Support) | Verified |
| 17 | Resellers buy for their customers inside the bot at their tier's prices, on credit up to a limit, with a statement and a commission. | page, posts, pitch | `CHANGELOG.md` (Bot: Reseller panel); `README.md` (Resellers) | Verified |
| 18 | Every bot text can be changed in the dashboard, in both languages. | page, posts | `CHANGELOG.md` (Dashboard: Store setup) | Verified |
| 19 | Staff get `/dashboard` in the bot for a one-time dashboard login link. | page (step 3), post 4 | `CHANGELOG.md` (Bot: Staff in the bot) | Verified |

## C. The dashboard

| # | Claim | Where | Proof | Status |
|---|---|---|---|---|
| 20 | Roles: owner, admin and support; each sees only what they may. | page, pitch | `CHANGELOG.md` (Dashboard); `docs/04-dashboard.md` (Roles and permissions) | Verified |
| 21 | Persian and English, dark and light. | page, pitch | `CHANGELOG.md` (Dashboard) | Verified |
| 22 | Log in with a one-time link, or with a password plus an authenticator code. | page | `CHANGELOG.md` (Dashboard) | Verified |
| 23 | A receipt review queue with the receipt photo, amount and reference, approve or reject; a repeated reference is flagged. | page, posts | `CHANGELOG.md` (Dashboard: orders and payments; Payments) | Verified |
| 24 | Orders, plans, users, discount codes, broadcasts, a support inbox, branding and every bot text. | page, pitch | `CHANGELOG.md` (Dashboard); `docs/04-dashboard.md` (Pages) | Verified |
| 25 | You can change the name, logo, color and the bot's profile photo. | page, posts | `CHANGELOG.md` (Dashboard: branding); `docs/08-brand.md` (Telegram bot profile) | Verified |
| 26 | The audit log only ever grows. | page | `CHANGELOG.md` (Security: append-only audit log) | Verified |

## D. Payments

| # | Claim | Where | Proof | Status |
|---|---|---|---|---|
| 27 | Card to card, USDT (TRC20 or ERC20) and a Zarinpal payment link (optional): the customer sends a receipt (for USDT a transaction ID is enough) and an admin approves it. | page FAQ, posts | `CHANGELOG.md` (Payments) | Verified |
| 28 | Telegram Stars, at the store's own rate in Toman. | page FAQ, posts | `CHANGELOG.md` (Payments) | Verified |
| 29 | Zarinpal online is off until a merchant ID is set. | page FAQ | `CHANGELOG.md` (Payments) | Verified |
| 30 | Automatic USDT on TRON (TRC20): each payment gets its own exact amount and 45 minutes; an exact match settles by itself. | page FAQ | `CHANGELOG.md` (Payments) | Verified |
| 31 | Each payment settles exactly once; the wallet ledger is append-only. | page, posts | `CHANGELOG.md` (Payments: last item) | Verified |
| 32 | The wallet is always on. | page FAQ | `CHANGELOG.md` (Payments: first item) | Verified |
| 72 | Zarinpal online needs the operator's own approved merchant account. It is unverified whether Zarinpal accepts VPN stores (the page says so and says to ask Zarinpal); card to card, USDT and Telegram Stars work without it. Zarinpal is marked optional wherever it is listed. | page FAQ and table, posts, outreach, pitch | `docs/PLAN.md` (Decided: "Still UNVERIFIED: ... whether it keeps a terminal that sells VPNs"); `docs/09-legal-questions.md` (item 22) | Verified |

## E. Operations and safety

| # | Claim | Where | Proof | Status |
|---|---|---|---|---|
| 33 | Several 3x-ui servers, each checked every minute, with a weight and servers per plan; a new service goes to a healthy server with fewer clients for its weight. | page, posts | `CHANGELOG.md` (Operations: Several 3x-ui servers) | Verified |
| 34 | Daily encrypted backups: the newest 7 on the server, each (up to 50 MB) sent to the owner on Telegram, optionally a copy in S3-compatible storage. | page, posts, pitch | `CHANGELOG.md` (Operations); `README.md` (Operations) | Verified |
| 35 | Restoring a backup is one command (`bobres restore`). | page, post 5 | `CHANGELOG.md` (Operations: `bobres restore`) | Verified |
| 36 | Alerts on Telegram when a service, a 3x-ui server, a gateway, the disk, a backup or a certificate has a problem, and when it is fixed. | page, posts | `CHANGELOG.md` (Operations: System alerts) | Verified |
| 37 | HTTPS in three modes: automatic for the domain (default), Let's Encrypt for the server's IP address, or your own certificate and key; a change is saved only once Caddy accepts it; `sudo bobres ssl auto` is the way back. | page, post 5 | `CHANGELOG.md` (Operations: HTTPS certificates); `README.md` | Verified |
| 38 | `bobres update`: verifies the signed release, backs up, keeps a restore point, rolls back by itself if the new version does not come up healthy; `bobres rollback`; stable and beta channels. | page FAQ, posts | `CHANGELOG.md` (Operations: `bobres update`) | Verified |
| 39 | `bobres harden`: firewall, fail2ban for SSH, security updates and swap, each asked. | page | `CHANGELOG.md` (Operations: `bobres harden`) | Verified |
| 40 | A one-command installer that verifies the signed release (signature and checksum) before it runs, asks for the domain, owner ID, bot token and panel (tokens typed hidden) and generates every password and key. `install.sh` itself is fetched over HTTPS and has no signature, so no text calls it a "signed installer". | page, posts, pitch | `README.md` (Install); `CHANGELOG.md` (Operations); `docs/07-security.md` (supply-chain rows: "Not covered by a signature: install.sh itself") | Verified |
| 41 | The installer installs Docker if needed. | page FAQ | `README.md` (Install) | Verified |
| 42 | Four services, each with its own database role and token; 3x-ui tokens only in the provisioner, gateway secrets only in payments. | page FAQ, outreach | `README.md` (Security); `CHANGELOG.md` (Security) | Verified |
| 43 | Secrets are encrypted at rest with a master key per service; backups are encrypted. | page FAQ, outreach | `CHANGELOG.md` (Security) | Verified |
| 44 | Releases are signed (Ed25519). BOBRES's own images are signed with keyless cosign and run by the digests built into the signed CLI. The Postgres, Redis and Caddy images are third-party: Caddy is pinned by version and digest, Postgres and Redis by version, and none of the three is signature-checked. | page FAQ, outreach | `README.md` (Security); `CHANGELOG.md` (Security); `docs/07-security.md` (supply chain); `deploy/docker-compose.yml` (image lines) | Verified |
| 45 | Dashboard: CSRF tokens, an authenticator code for every password login, a strict CSP, an append-only audit log. | page FAQ, outreach | `CHANGELOG.md` (Security) | Verified |
| 46 | BOBRES's own services run as an unprivileged user (65532), with a read-only filesystem and all capabilities dropped. Not claimed for Caddy (runs as 0:65532 with NET_BIND_SERVICE), Postgres or Redis: third-party images with their own settings. | page FAQ, outreach | `deploy/docker-compose.yml` (`x-app`: `read_only`, `cap_drop: [ALL]`, `no-new-privileges`); `deploy/Dockerfile` (`USER 65532:65532`); `CHANGELOG.md` (Security) | Verified |
| 47 | These are measures, not a guarantee; review before going public. | page FAQ, outreach | Editorial: the project makes no guarantee anywhere | Verified |
| 74 | In the HTTPS server-IP mode the bot asks `api.ipify.org` (then `ifconfig.me`) for the server's public address, because core has no internet access. No customer data is in that request. | page FAQ | `docs/02-events-and-apis.md` (Internal only: `GET /internal/public-ip`); `internal/bot/runner/publicip.go` | Verified |
| 48 | Customers, orders and wallets stay in the server's database. Nothing about customers is sent to the project. Only an install with online license activation (`BOBRES_LICENSE_SERVER` set) talks to the license server: activation sends the key, the install ID, the domain and the version; the daily refresh sends the installed license token and the version; the server also sees the address each request comes from. A free install that never activates sends nothing. Releases come from GitHub (the bot, naturally, also talks to Telegram, the payment providers and the panels; row 74 for the public-IP lookup). | page FAQ, posts | `docs/05-license.md` (Privacy; Activation and daily refresh); `docs/13-license-server.md` (API: the `POST /v1/activate` and `POST /v1/refresh` bodies); `docs/legal/privacy-policy.md` (4. What the Vendor receives); `docs/06-installer.md` (update check via GitHub); `CHANGELOG.md` (core has no internet access) | Verified |
| 49 | A license never stops what already exists; it only affects new actions. | page FAQ | `docs/05-license.md` (Free core, paid premium: "What is gated, and what never is"); `docs/pricing.md` (When a subscription lapses: "Nothing is deleted"); `CHANGELOG.md` (Security: last item) | Verified |

## F. Requirements

| # | Claim | Where | Proof | Status |
|---|---|---|---|---|
| 50 | Ubuntu 22.04/24.04 or Debian 11/12/13, amd64 or arm64, 1 GB of RAM (2 GB recommended), 10 GB free disk, ports 80 and 443 free, root access. | page FAQ, post 4, outreach | `README.md` (Install) | Verified |
| 51 | A domain whose A record points to the server; a bot from @BotFather and the numeric Telegram ID; a 3x-ui panel with an API token. | page FAQ, post 4 | `README.md` (Install) | Verified |
| 52 | 3x-ui versions 3.8.5 to 3.9.0 are tested; only version 3 is supported; older versions only when a customer needs an adapter. | page FAQ, posts | `README.md` (Install; Status); `CHANGELOG.md` (Known limits) | Verified |
| 53 | The install command works. | page, posts | `README.md` (install); the release `v0.1.0` (GitHub, 2026-10-14, with signed assets); the CI job "Docker images + compose + e2e install"; a lab install updated to it with `bobres update` (signature checked, nine services healthy) | Verified |

## G. Tiers and licensing (decided 2026-10-14, written in `docs/pricing.md`)

| # | Claim | Where | Proof | Status |
|---|---|---|---|---|
| 54 | The core is free. | page, posts | `docs/pricing.md` (The model: free core, paid premium); `README.md` (Free core, paid premium); `docs/05-license.md` (an install without a license is not blocked) | Verified |
| 55 | Free: 1 server, up to 25 active services, 2 staff seats; no resellers, white-label or automatic USDT. | page table, post 7, pitch, outreach | `internal/license/policy.go` (`FreeEntitlements`: 1, 25, 2, no resellers, no white label, no `crypto_watcher`); `docs/05-license.md` (The free tier); `docs/pricing.md` (The tiers) | Verified |
| 56 | Standard: up to 3 servers, up to 500 active services, resellers, automatic USDT, 5 staff seats. | page table, post 7, pitch | `internal/license/tiers.json` (`standard`: `max_servers` 3, `max_active_services` 500, `resellers` true, `usdt_trc20_auto` with `crypto_watcher` true, `dashboard_staff_seats` 5); `docs/pricing.md` (The tiers) | Verified |
| 57 | Pro: unlimited servers and services, white-label, everything. | page table, post 7, pitch | `internal/license/tiers.json` (`pro`: `max_servers` -1, `max_active_services` -1, `white_label` true, resellers, every gateway, `crypto_watcher`, `dashboard_staff_seats` -1); `docs/pricing.md` (The tiers) | Verified |
| 58 | Pro: unlimited staff seats. | page table, pitch | `internal/license/tiers.json` (`pro`: `dashboard_staff_seats` -1, unlimited); `docs/pricing.md` (Dashboard staff seats: unlimited) | Verified |
| 59 | A subscription covers one install. | page, pitch | `docs/pricing.md` (The model: "a subscription for **one install**", bound to its domain); `docs/05-license.md` (Binding) | Verified |
| 60 | Every new install starts with a 30-day trial of the premium features, everything except removing the BOBRES mark (white-label): the trial has none. No text says "try everything". Besides white-label, the trial's 5 staff seats are the one place where it is below Pro (unlimited). | page, posts, outreach, pitch | `internal/license/policy.go` (`TrialDays` 30; `TrialEntitlements`: unlimited servers and services, resellers, every gateway, 5 seats, no white label); `docs/05-license.md` (The trial: a fresh and an upgraded install alike); `internal/license/tiers.json` (`trial`) | Verified |
| 61 | If a subscription ends, nothing is deleted and the install goes back to the Free tier. | page FAQ, posts | `docs/pricing.md` (When a subscription lapses: a grace period of 14 days by default first, then the free tier or the trial if it still runs; "Nothing is deleted"); `docs/05-license.md` (States; Free core, paid premium) | Verified |
| 62 | White-label means no BOBRES mark. | page, pitch | `docs/08-brand.md` (usage rules); `docs/05-license.md` ("Powered by BOBRES") | Verified |
| 63 | Prices: contact us, through the issue tracker of the project's repository on GitHub, which is public: everyone can read what is written there, so the texts say "never paste tokens or private data". The owner's private channel (`{{CONTACT}}`) replaces it once it exists. | page, posts, pitch | Owner decision (no price numbers anywhere in public material; the repository is the contact for now); `gh repo view`: visibility PUBLIC; `internal/license/tiers.json` (every price is still "SET ME") and `docs/launch-checklist.md` (steps 7 and 8: the prices and the seller contact are the owner's) | Owner decision |
| 64 | Every tier includes the bot, the dashboard, backups, alerts, the HTTPS manager, updates with rollback and hardening; the manual payment methods, Stars and Zarinpal (optional, row 72) are in every tier. | page table | `docs/05-license.md` (What is gated, and what never is: only servers, services, resellers, staff seats, payment methods and white label are gated; backups, updates and security fixes never); `internal/license/policy.go` (`FreeEntitlements` gateways: `zarinpal`, `stars`, `manual_card`, `manual_crypto`, `manual_zarinpal`; the wallet is always on); `docs/pricing.md` (the tiers table) | Verified |
| 71 | A free tier exists, with the limits of row 55 (1 server, up to 25 active services, 2 staff seats). It is a tier with limits, not a promise that nothing will ever change; the texts say "a free tier with the limits above", never "always there". | page FAQ, outreach reply 6 | `internal/license/policy.go` (`FreeEntitlements`; the constants are the owner's to tune); `docs/05-license.md` (The free tier); `docs/pricing.md` (The model: no time limit, within the limits) | Verified |

## H. The page and its pictures

| # | Claim | Where | Proof | Status |
|---|---|---|---|---|
| 65 | The dashboard screenshots come from a real BOBRES install with demo data. | page, posts | Lab: a lab build of BOBRES in Docker (`deploy/lab/README.md`), seeded by `docs/marketing/tools/seed-demo.py`, captured by `docs/marketing/tools/shots.mjs`; restored afterwards | Verified |
| 66 | The bot pictures are illustrations drawn from the bot's real messages and button colors. The checkout picture draws the manual USDT button, because the demo store has automatic USDT off (the overview picture shows it Off). | page, posts | `docs/marketing/tools/chat/render.mjs` reads `internal/bot/i18n/locales/{fa,en}.json` and `buttonStyles` | Verified |
| 67 | The HTTPS picture shows the mode chooser only. | page | Cropped on purpose: the lab's certificate is Caddy's local one | Verified |
| 68 | The short demo is made from those pictures, with no sound. | page, posts | `docs/marketing/tools/demo/make-demo.mjs` | Verified |
| 69 | The page makes no request to another host (fonts, images and scripts are local). | page | Checked on 2026-10-14: browser request log and `grep` of the HTML, CSS and JS | Verified |
| 70 | Fonts: Vazirmatn and Inter, both under the SIL Open Font License. | page | `site/assets/fonts/OFL-*.txt` | Verified |
| 76 | The dashboard has a layout for small screens: on a narrow screen the menu folds into a drawer. The three phone pictures are real captures at 390 px wide. It is not claimed that every page fits. | page (Screens) | `web/dashboard/index.html` (viewport meta); `web/dashboard/src/layouts/AppLayout.vue` (the sidebar hides under 900 px and a menu button opens a `Drawer`); `docs/marketing/tools/shots.mjs` (mobile viewport 390 x 844); `site/assets/shots/dash-*-m.webp` | Verified |

## Not claimed, on purpose
A domain, a company name, a phone number, a Telegram handle or channel, a price, a customer count, a testimonial, an
uptime or speed figure, an installation time, a guarantee, "open source" (the code is source-available under a draft license, all rights reserved: `LICENSE`), and
anything about trademarks (the brand checks are still open: `docs/08-brand.md`).
