# 04 - Dashboard pages and roles (DRAFT)

Stack: Go + templ + HTMX, served by `core`, behind the gateway. Branded per install (logo, colors, name).
Sessions: secure cookie, CSRF, optional/required TOTP 2FA for staff. Login by password (+ 2FA) or Telegram login widget.

## Roles and permissions
| Role | Scope |
|---|---|
| owner | Everything incl. license, staff, gateway secrets, danger zone |
| admin | Users, orders, plans, discounts, broadcasts, settings (no license/secrets) |
| support | Tickets, read-only users/orders, limited actions (extend, reset traffic) |
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
- Dashboard language: fa/en switch for staff.
- Data export and GDPR-style deletion for a user (delete/anonymize but keep ledger integrity).
