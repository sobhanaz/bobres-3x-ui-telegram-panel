# 04 - Dashboard pages and roles (DRAFT)

Stack: Vue 3 + Vite + TypeScript (embedded in `core` via embed.FS), JSON API on `core`, behind the gateway. See PLAN.md §12. Branded per install (logo, colors, name).
Sessions: secure cookie, CSRF. Login with a one-time link from the bot (or `bobres admin link`), or with a password that always needs a TOTP code.
Decisions, milestones and status: `superpowers/specs/2026-10-07-phase4-dashboard-design.md`.

## Roles and permissions
| Role | Scope |
|---|---|
| owner | Everything incl. license, staff, gateway secrets, danger zone |
| admin | Users, orders, plans, discounts, broadcasts, settings, branding, 3x-ui servers (write-only tokens), audit log (no staff, license, gateway secrets or payment details) |
| support | Tickets, read-only users/orders, limited actions (extend a service, read it from the panel) |
| reseller | Not a staff role and no dashboard access: works only in the Telegram bot (reseller panel). Owner and admin manage resellers on the Resellers page (`resellers.write`) |
| finance (optional) | Payments, ledger, reports, exports, no user edits |
Permissions are a list of granular flags (e.g. `users.write`, `wallet.adjust`) so custom roles are possible later.
Every write goes to `audit_log`; sensitive actions (balance adjust, refund, ban) need a reason field.

## Pages
1. **Overview:** revenue, new users, active services, expiring soon, failed provisions, gateway health (on, off, details missing, failing), panel health per server (switched-off servers are not counted).
2. **Users:** search/filter, profile (services, orders, wallet, tickets), ban, adjust balance, impersonate-view (read-only).
3. **Services:** all subscriptions, status, usage, extend, reset traffic, delete, resync from 3x-ui.
4. **Plans:** CRUD, trial flag, ordering, enable/disable, the servers a plan's new services may use (empty = any) (reseller tier prices are set on the Resellers page).
5. **Orders and payments:** list, states, retry provisioning, refund, manual receipt review queue.
6. **Wallet and ledger:** read-only ledger explorer, exports (CSV).
7. **Discounts and referrals:** codes, limits, usage, referral rules.
8. **Resellers:** list (search, tier, in debt), tiers with a discount or fixed price per plan and a commission, make a customer a reseller (also from their page), credit limits, recording payments received, their services, customers, statement and orders.
9. **Servers (3x-ui):** several panels: URL, write-only token, subscription URL, weight, on/off for new services, version, health, services and plans per server, a connection test, and an inbound list whose attach toggles affect new services only. A panel on a private network (plain http allowed there) is the owner's to add, or to give another address or token; admins may rename, reweight or switch one off.
10. **Payment gateways (owner):** Zarinpal on/off, sandbox, write-only merchant id and proxy, test connection, payments of the last 7 and 30 days; the manual methods and Stars with their on/off switch and details (moved from Settings).
11. **Support:** ticket inbox, canned replies, assignment.
12. **Broadcasts:** compose (fa/en), audience filter, schedule, progress.
13. **Branding and texts:** name, logo, colors, welcome message, all bot strings per language, legal links, currency.
14. **Settings:** general, notifications, forced-join channel, rate limits, maintenance mode, timezone.
15. **Staff:** invite, roles, 2FA status, sessions.
16. **System:** health of every service, version, update available, backup status/run/restore, logs viewer (recent), license status.
17. **Audit log:** filterable, exportable.

## Non-goals for v1
No public marketing site, no end-user web portal (users stay in Telegram), no mobile app.

## Open points
- DECIDED (2026-10-07): staff switch between fa (default, RTL) and en.
- Data export and GDPR-style deletion for a user (delete/anonymize but keep ledger integrity).
