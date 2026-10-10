# 04 - Dashboard pages and roles (DRAFT)

Stack: Vue 3 + Vite + TypeScript (embedded in `core` via embed.FS), JSON API on `core`, behind the gateway. See PLAN.md §12. Branded per install (logo, colors, name).
Sessions: secure cookie, CSRF. Login with a one-time link from the bot (or `bobres admin link`), or with a password that always needs a TOTP code.
Decisions, milestones and status: `superpowers/specs/2026-10-07-phase4-dashboard-design.md`.

## Roles and permissions
| Role | Scope |
|---|---|
| owner | Everything incl. license, staff, gateway secrets, danger zone |
| admin | Users, orders, plans, discounts, broadcasts, settings, branding, audit log (no staff, license, secrets or payment details) |
| support | Tickets, read-only users/orders, limited actions (extend a service, read it from the panel) |
| reseller | Own customers, own sales, own credit; sees only their data |
| finance (optional) | Payments, ledger, reports, exports, no user edits |
Permissions are a list of granular flags (e.g. `users.write`, `wallet.adjust`) so custom roles are possible later.
Every write goes to `audit_log`; sensitive actions (balance adjust, refund, ban) need a reason field.

## Pages
1. **Overview:** revenue, new users, active services, expiring soon, failed provisions, gateway health, panel health.
2. **Users:** search/filter, profile (services, orders, wallet, tickets), ban, adjust balance, impersonate-view (read-only).
3. **Services:** all subscriptions, status, usage, extend, reset traffic, delete, resync from 3x-ui.
4. **Plans:** CRUD, prices per tier, trial flag, ordering, enable/disable.
5. **Orders and payments:** list, states, retry provisioning, refund, manual receipt review queue.
6. **Wallet and ledger:** read-only ledger explorer, exports (CSV).
7. **Discounts and referrals:** codes, limits, usage, referral rules.
8. **Resellers:** tiers, credit limits, invoices/settlement, their customers.
9. **Servers (3x-ui):** URL, token, version, inbound list with enable toggle, health, add server (future multi-server).
10. **Payment gateways:** enable/disable, keys (write-only fields), test connection, reconciliation reports.
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
