# 04 - Dashboard pages and roles

Stack: Vue 3 + Vite + TypeScript (embedded in `core` via embed.FS), JSON API on `core`, behind the gateway. See PLAN.md §12. Branded per install (logo, colors, name).
Sessions: secure cookie, CSRF. Login with a one-time link from the bot (or `bobres admin link`), or with a password that always needs a TOTP code.
Decisions, milestones and status: `superpowers/specs/2026-10-07-phase4-dashboard-design.md`. All 17 pages below are
built (Phase 4, 2026-10-12), and page 18, Reports, was added after v0.1.1; the few parts that are not built are marked.

## Roles and permissions
| Role | Scope |
|---|---|
| owner | Everything incl. license, staff, gateway secrets, danger zone |
| admin | Users, orders, plans, discounts, broadcasts, settings, branding, 3x-ui servers (write-only tokens), reports, audit log (no staff, license, gateway secrets or payment details) |
| support | Tickets, read-only users/orders, limited actions (extend a service, read it from the panel) |
| reseller | Not a staff role and no dashboard access: works only in the Telegram bot (reseller panel). Owner and admin manage resellers on the Resellers page (`resellers.write`) |
| finance (optional) | Payments, ledger, reports, exports, no user edits. Not built: owner, admin and support cover v1 (`reports.read` is its own flag, so a finance role can be given it later) |
Permissions are a list of granular flags (e.g. `users.write`, `wallet.adjust`, `reports.read`) so custom roles are possible later. Roles are defined in code (`internal/core/web/perms.go`), so a new flag reaches existing installs with the update and needs no migration: `reports.read` is held by owner and admin, not by support.
Every write goes to `audit_log`; sensitive actions (balance adjust, refund, ban) need a reason field.

## Pages
1. **Overview:** revenue (only in the answer for staff with `reports.read`: support does not get it), new users, active services, expiring soon, failed provisions, gateway health (on, off, details missing, failing), panel health per server (switched-off servers are not counted).
2. **Users:** search/filter, profile (services, orders, wallet, tickets), ban, adjust balance, impersonate-view (read-only; not built).
3. **Services:** all subscriptions, status, usage, extend, reset traffic, delete, resync from 3x-ui.
4. **Plans:** CRUD, trial flag, ordering, enable/disable, the servers a plan's new services may use (empty = any) (reseller tier prices are set on the Resellers page).
5. **Orders and payments:** list, states, retry provisioning, refund, manual receipt review queue.
6. **Wallet and ledger:** read-only ledger explorer, exports (CSV).
7. **Discounts and referrals:** codes, limits, usage, referral rules.
8. **Resellers:** list (search, tier, in debt), tiers with a discount or fixed price per plan and a commission, make a customer a reseller (also from their page), credit limits, recording payments received, their services, customers, statement and orders.
9. **Servers (3x-ui):** several panels: URL, write-only token, subscription URL, weight, on/off for new services, version, health, services and plans per server, a connection test, and an inbound list whose attach toggles affect new services only. A panel on a private network (plain http allowed there) is the owner's to add, or to give another address or token; admins may rename, reweight or switch one off.
10. **Payment gateways (owner):** Zarinpal on/off, sandbox, write-only merchant id and proxy, test connection, payments of the last 7 and 30 days and recent check errors; automatic USDT (Phase 6a: on/off, the TRON address, an optional write-only TronGrid key and proxy, test connection, its report, and the unmatched deposits staff mark handled once they credited the customer by hand); the manual methods and Stars with their on/off switch and details (moved from Settings).
11. **Support:** ticket inbox, canned replies, assignment.
12. **Broadcasts:** compose (fa/en), audience filter, schedule, progress.
13. **Branding and texts:** name, logo, colors, welcome message, all bot strings per language, legal links, currency.
14. **Settings:** general, notifications, forced-join channel, rate limits, maintenance mode, timezone.
15. **Staff:** invite, roles, 2FA status, sessions.
16. **System (owner and admin):** tabs. *Health*: every service (core, bot, payments, provisioner, backup) with its version, health and readiness checks, a warning when versions differ, and the 3x-ui servers and payment methods. *License*: what applies now (the free tier, the 30-day trial with its days left, or the license's tier) with whom to contact to buy or renew a license (the owner's `BOBRES_SELLER_CONTACT`, or "ask the seller you got BOBRES from" when none is set); the installed token's state (none, invalid with the reason, active, grace, expired), tier, ids, dates and days left, the domain and install binding, this install's id to copy, and each entitlement in effect with its use (a warning from 90%); the owner pastes or opens a token, checks it, and saves it with a reason, or activates one with a license key from the seller (05-license.md). *Updates*: the running and newest versions, the update channel (stable or beta), an informational note when the newest release is newer than the license's update date (the update is offered all the same: security updates are never withheld), and the `sudo bobres update` command to run on the server. *Backups*: each backup with its size, Telegram delivery and off-site copy, a button to run one now, and the off-site storage's state. *HTTPS*: how each address the store is served on (the domain, the server's IP address, the Zarinpal domain) gets its certificate, with the issuer, expiry, whether browsers trust it, the live error and the HTTPS applier's last result (and a red line, "HTTPS gateway (Caddy) is not reachable", when Caddy answers on none of them); the owner switches between the domain's automatic certificate, a Let's Encrypt certificate for the server's IP address and an own certificate and key, with the warnings of each mode (`system.write`, audited as `system.tls.set`; the runbook is runbooks/https.md). *Logs* (owner only): the newest warnings and errors of every service, redacted. The Health tab also lists the open system problems. Restoring a backup is CLI only (`bobres restore`). Without a valid license the install runs on the free tier after its trial, and its limits refuse only new servers, services, staff, resellers and payment methods (a refusal names the limit and says to buy or renew a license); staff who see this page get a small chip in the top bar for the free tier, the trial (with its days left) or a license that needs a look. "Powered by BOBRES" shows at the foot of the dashboard and the login page on every install except one whose license includes white label.
17. **Audit log:** filterable, exportable.
18. **Reports** (`reports.read`: owner and admin; support has neither the page nor the revenue cards of the Overview, while single orders' amounts stay visible to support through Orders by design): revenue per day, week or month for a range (today, last 7 or 30 days, this month, last month, or two dates), always per currency (never summed), with paid orders (new, renewals, traffic packages), new customers, free trials, wallet top-ups, refunds, payments rejected by staff, failed or expired gateway payments and cancelled orders; a revenue chart per currency, the top plans and how orders were paid (wallet, staff-approved, gateway, each split by the provider that took the payment: Zarinpal, Stars, automatic USDT, card to card...); CSV files of the summary and of the paid orders, audited as `export.reports` with their outcome. Dates are Jalali in Persian; a Calendar switch picks Gregorian (weeks from Monday, calendar months) or Jalali (weeks from Saturday, Jalali months named by month and year), Jalali by default in Persian, all in the store's time zone (the Settings page, else `BOBRES_TIMEZONE`, else Asia/Tehran). Every number is defined in [14-reports.md](14-reports.md).

## Non-goals for v1
No public marketing site, no end-user web portal (users stay in Telegram), no mobile app.

## Open points
- DECIDED (2026-10-07): staff switch between fa (default, RTL) and en.
- Data export and GDPR-style deletion for a user (delete/anonymize but keep ledger integrity).
