# 04 - Dashboard pages and roles

Stack: Vue 3 + Vite + TypeScript (embedded in `core` via embed.FS), JSON API on `core`, behind the gateway. See PLAN.md §12. Branded per install (logo, colors, name).
Sessions: secure cookie, CSRF. Login with a one-time link from the bot (or `bobres admin link`), or with a password that always needs a TOTP code. The owner can also sign in with no Telegram at all: the installer (or `bobres admin password`) issues a random initial password for the login `owner`; it opens a restricted session where only My account works (a forced screen to choose a new password and turn on 2FA, with no menu), and the rest of the dashboard follows once the first code is confirmed (see 06-installer.md, Owner login without Telegram).
Decisions, milestones and status: `superpowers/specs/2026-10-07-phase4-dashboard-design.md`. All 17 pages below are
built (Phase 4, 2026-10-12), and page 18, Reports, was added after v0.1.1; the few parts that are not built are marked. Page 19, Monitoring,
is the **Monitoring tab of the System page** (item 19 below; the API, the collector and the alerts behind it are described in "Monitoring API").

## Roles and permissions
| Role | Scope |
|---|---|
| owner | Everything incl. license, staff, gateway secrets, danger zone |
| admin | Users, orders, plans, discounts, broadcasts, settings, branding, 3x-ui servers (write-only tokens), reports, audit log (no staff, license, gateway secrets or payment details) |
| support | Tickets, read-only users/orders, limited actions (extend a service, read it from the panel) |
| reseller | Not a staff role and no dashboard access: works only in the Telegram bot and the Mini App (the reseller panel and section: buy for customers, pause, resume, delete and keep notes on their own customers' services, see the customers who joined through their link). Owner and admin manage resellers on the Resellers page (`resellers.write`) |
| finance (optional) | Payments, ledger, reports, exports, no user edits. Not built: owner, admin and support cover v1 (`reports.read` is its own flag, so a finance role can be given it later) |
Permissions are a list of granular flags (e.g. `users.write`, `wallet.adjust`, `reports.read`) so custom roles are possible later. Roles are defined in code (`internal/core/web/perms.go`), so a new flag reaches existing installs with the update and needs no migration: `reports.read` is held by owner and admin, not by support, and `reports.reset` (starting the reports from zero) by the owner only.
Every write goes to `audit_log`; sensitive actions (balance adjust, refund, ban) need a reason field. A bulk purchase is one `order.bulk` entry (the plan, the quantity, the total and the buyer, source `bot`) beside the ordinary order and wallet entries of each service. What a reseller does to their own services is in it too: pausing, resuming and deleting a service are `subscription.disable`, `subscription.enable` and `subscription.delete` with the reseller as the actor (role reseller), source `bot` (the Mini App is the bot's second face) and the reason "paused by the reseller", "resumed by the reseller" or "deleted by the reseller". A service the reseller paused can be told from one a staff member paused by the actor; the reseller cannot resume the second.

## Approving sign-ins in Telegram

An option for a store whose owner wants a second look at every staff sign-in: **Settings > Sign-in security** (owner only, `security.write`). It is **off by default**, and with it off nothing about signing in changes.

When it is on, a staff sign-in (a login link from the bot or from `bobres admin link`, or a password with its authenticator code) passes every check it had before and then **waits**: it gets no session at all, only a pending request of 5 minutes (the `login_approvals` table, migrations 00046 and 00047). The bot messages the staff member, and, for a sign-in by someone who is not an owner, the owners too when **Also ask the owners** (`security.login_approval_notify_owner`, on by default) is on. The message says the time, the IP address (and whether this address was seen on this account before), the browser and the system, how the person got through the first checks, and has **Approve** and **Deny**. The sign-in page shows "waiting for approval in Telegram" with the time left, asks core every 2 seconds, and goes in as soon as it was approved.

- **Who can answer.** The staff member whose sign-in it is, or an owner, and only while that person is still staff (a demoted or removed account's answer does nothing). Each approver has a nonce of their own, hashed in the database, so a button forwarded to someone else does nothing, an old message does nothing, and a button works once: the first answer decides, a second one is told the request has ended. Deny, expiry (the database clock decides, not a server's), and the person giving up on the page all end the request without a session ever having existed; the Telegram messages are edited to show the result and lose their buttons.
- **No session before the approval.** The browser holds a separate pending cookie (`__Host-bobres_pending`, bound to the request), which opens nothing. Approval makes the session in core; the page's next poll picks it up and sets the real cookie. Two sign-ins at once do not mix: each has its own request, nonce and cookie.
- **Limits.** At most 3 requests open and 6 started in 15 minutes for one account; beyond that the page says to wait. Answers are rate limited per Telegram account too.
- **Fails closed, with a way out.** When no approval can be asked (the bot is down or not connected, the person blocked the bot, Telegram does not answer, nobody can be reached) the sign-in page says so at once and names the command, instead of waiting for nothing: `sudo bobres admin approval off` on the server (see 06-installer.md). The command also ends the requests that wait. `sudo bobres admin approval status` says whether it is on. It never turns the approval on: only the owner, signed in, can.
- **Turning it on** needs the owner signed in, who reads what it does in a dialog and ticks that they have understood it, and gives their authenticator code when the session needs one. Before it takes effect core asks the acting owner in Telegram, as a preflight, whether the message arrives, so the approval cannot be turned on with nobody able to answer. It cannot be set from the bot's `/set` or from the generic settings form, nor by an admin.
- **Audit.** `dashboard.login_requested`, `dashboard.login_approved`, `dashboard.login_denied` (who answered, from which role), `dashboard.login_expired`, `dashboard.login_cancelled`, `dashboard.login_undeliverable` and `dashboard.login_approval_invalid` (a forged or stale answer); turning it on or off is a `setting.set`, source `dashboard` or `cli`.

The runbook is [login-approval](runbooks/login-approval.md).

## Pages
1. **Overview:** revenue (only in the answer for staff with `reports.read`: support does not get it), new users, active services, expiring soon, failed provisions, gateway health (on, off, details missing, failing), panel health per server (switched-off servers are not counted).
2. **Users:** search/filter, profile (services, orders, wallet, tickets), ban, adjust balance, impersonate-view (read-only; not built). The customer's page has a read-only **Codes** tab (for staff with `users.read`, support included): the reward codes issued to the customer and the gift codes they applied, newest first, with the code (a gift code only by its last characters: core never keeps one whole), what it is worth, its state (not used yet, used, expired, switched off), when it ends and was used, and the plan and order that gave it (a link to the order). `GET /api/v1/codes?user=<customer id>&page=&size=` (`users.read`, `user` required; 400 without it or when it is not an id, 404 for an unknown customer, a banned customer's list can be read); it is the list the customer sees in the bot and the Mini App, and holds nothing that can not be attributed to them.
3. **Services:** all subscriptions, status, usage, extend, reset traffic, delete, resync from 3x-ui, and the address of the service's branded page with a Regenerate button (`services.write`, with a reason; [15-subscription-page.md](15-subscription-page.md)).
    - **Services the panel has no client for** (an operator deleted it in 3x-ui or Marzban: the customer cannot connect while the service still looks active; every service object has `panel_missing_since`, the unix time core first saw it, `null` otherwise). The list marks them with a **Not on the panel** badge and filters with an **Only services not on the panel** switch (`GET /api/v1/services?panel=missing`, kept in the address as `?panel=missing`). The service's page warns since when, that the customer cannot connect and that expiry reminders are paused, and offers **Make the client again** (`services.write`, with a reason): `POST /api/v1/services/{id}/recreate-client` makes the client again on the same server with the limits core holds (the panel's used traffic restarts from zero); `409 state` for a deleted service or one being delivered, `503` while the panel does not answer. **Bulk actions** (built 2026-10-29): with the switch on, staff with `services.write` get a checkbox on each row and one in the header that selects the whole page (the selection is of the page shown; another page or filter drops what is not on it), and **Make again** / **Delete** for the selection with **one reason dialog for all** (it lists the selected emails). The page then sends one request per service, one after the other, to the existing `POST /api/v1/services/{id}/recreate-client` or `/delete` with `{reason, only_missing: true}`, with a progress bar, and ends with a summary: done, skipped and failed, the reason for each (a skip says "on the panel again" or "deleted already", a failure says the error and core's words), a toast, and the list read again. `only_missing` makes core act only while the panel still lacks the client: a service that is back on the panel (or no longer live) or deleted since the list was loaded is answered `200 {"service": ..., "skipped": "back" | "deleted"}` and nothing is done to it (a delete would otherwise remove the client the panel has again); without the flag both endpoints are what they were. Each action is audited as the per-service one (`subscription.recreate_client`, `subscription.delete`, with the reason). The service page keeps its own buttons. The Overview shows a **Not on the panel** card while `services.panel_missing` is above zero, and links to the filtered list. The Monitoring events show **×N** and the first and last time for an entry core folded from identical lines (`count`, `first_at`; `at` is the newest).
4. **Plans:** CRUD, trial flag, ordering, enable/disable, a **reward code** (a percent from 1 to 90 or a fixed amount in the plan's currency, the days it lasts, 1 to 365 (default 10), and a text in Persian and English for the buyer: a paid purchase or renewal gives the buyer a discount code for any plan they buy next, for them only, one use; `reward` on a plan is `{kind: none|percent|amount, value, days, text}`: absent keeps it, `none` takes it away; the plan's JSON has `reward: {kind, percent | amount, days, text}` or null; a trial and a traffic package give none), **deleting a plan** (the trash button, with a confirmation; `DELETE /api/v1/plans/{id}` (permission `plans.delete`: the owner only; admins and support do not see the button) takes the plan off sale and out of every list but **never erases it**: its orders, reports and refunds keep it, and services made from it keep working; the answer is `{"result": "deleted"}`; `plan.delete` in the audit log), **restoring a deleted plan** (the list "Deleted plans" under the table, owner only, same permission `plans.delete`; Restore brings the plan back **off sale** with no group, so the owner decides when to sell it again: `GET /api/v1/plans/deleted`, `POST /api/v1/plans/{id}/restore`; `plan.restore` in the audit log), **plan groups** (a table above the plans: a name and a text in Persian and English and an order for each group; a plan's editor has a Group field; in the bot and the Mini App a customer picks a group and reads its text before choosing a plan; deleting a group keeps its plans on sale, ungrouped; `GET`/`POST` `/api/v1/plan-groups`, `PUT`/`DELETE` `/api/v1/plan-groups/{id}`, `group_id` on a plan: absent keeps, `""` clears; `plan_group.upsert` and `plan_group.delete` in the audit log), the servers a plan's new services may use (empty = any) and, per server, the inbounds they join (empty = every attachable inbound; a choice wins over the Servers page's attach switch, only inbounds that are on in the panel are used, and a warning shows when a chosen inbound is gone from the panel) (reseller tier prices are set on the Resellers page).
5. **Orders and payments:** list, states, retry provisioning, refund (which takes back the cashback the order earned, see Cashback below), manual receipt review queue. An order's page shows the **cashback** it earned its buyer (the amount, the percent it was worked out at on what the order cost, a note when it was cut to the maximum per order) and what a refund took back; the list does not carry it.
6. **Wallet and ledger:** read-only ledger explorer, exports (CSV).
7. **Discounts and referrals:** codes, limits, usage, referral rules.
   **Gift codes** (its own page next to Discounts, `discounts.write` like them; a code that adds a fixed amount of wallet
   credit when a customer applies it in the bot, unlike a discount, which takes money off a purchase): make one code of
   your own words (at least 8 characters, at least 5 different) or a batch of up to 500 generated ones (12 random
   characters from an alphabet without 0, O, 1, I and L, shown as `ABCD-EFGH-JKMN`, about 59 bits, with an optional prefix of
   at most 8 letters or digits), each with an amount and currency, uses in all, uses per customer, an optional expiry and a
   note. **The codes are shown once**, in a dialog that copies them or downloads them as a CSV (UTF-8 with a byte-order
   mark, formulas quoted); core keeps only a keyed hash and the last four characters, so they cannot be shown again. The
   list shows the last four characters, the value, used / in all, status (active, used up, expired, switched off), the
   expiry, the last use and the note, filters by status and searches a note or the last four characters, exports as a CSV (the rules of [14-reports.md](14-reports.md#csv-files): English headers, UTC and local times, a Jalali column in a Jalali file),
   and has a switch per code (what a code paid stays paid) and a list of who redeemed it and when (also a CSV). A banner
   says when customers cannot use gift codes (the setting `gift_codes.enabled`, with a button to turn it on for staff who
   may) or when core has no master key and cannot make them.
8. **Resellers:** list (search, tier, in debt), a reseller's page shows their **monthly target** while targets are on (the month's dates, the services sold, each tier with its bonus and whether it is reached and paid, and what is left to the next; `targets` in `GET /resellers/{id}`), tiers with a discount or fixed price per plan and a commission, make a customer a reseller (also from their page; the owner may write the reseller's **group name** in the 3x-ui panel there: the group is `BOBRES-<NAME>`), credit limits, recording payments received, their services, customers, statement and orders. The reseller's page shows their **3x-ui group** and **config prefix** and, for the owner, a **Panel names** dialog: the group name (a new one moves the reseller's earlier configs into it in the background, or **Move the existing configs** asks again), and the prefix (the reseller chooses it once in the bot or the Mini App; the owner changes or clears it; `PUT /api/v1/resellers/{id}/naming`, `POST /api/v1/resellers/{id}/group-sync`; audited as `reseller.panel_group`, `reseller.config_prefix` and `reseller.group_sync`; an admin sees them and may make a reseller but not name its group).
   - **Messages tab** (and the **Message resellers** button above the list; it needs `broadcasts.write` as well as `resellers.write`, so owner and admin): a message that only resellers get in the bot. *Who:* only those who owe (the default), every reseller, or one tier, and a bot language (all, Persian, English), with a live count; banned resellers and those who blocked the bot are left out. *Text:* one per language the audience reads, Telegram HTML, with placeholders filled in for each reseller: `{name}` (their @username; without one, their 3x-ui group name, else their Telegram id: core does not keep Telegram first names), `{debt}` (what they owe, as the bot writes money, with the store's own name for the Toman: every currency in debt joined with " + ", "0 Toman" when nothing), `{balance}` and `{credit_limit}` (Toman always, then any other currency they have), `{tier}` and `{customers}`. Chips under the title add them. A word in braces that is none of these (`{dept}`) is refused when the text is checked, previewed, tested or sent (the problem `placeholder_unknown`), and a text is shorter than Telegram's limit by 64 characters for each placeholder in it, so what is written from it fits. *Preview:* the text as one chosen reseller gets it (or the first of the audience; sample figures while the store has no reseller). *Buttons:* an optional link button as in a broadcast, and a **Settle / Top up** button (on by default) that opens the reseller's wallet in the bot (Top up is there; the message stays as it is). **Send to me** sends the open language's text, written for the previewed reseller, to the staff member's own Telegram (at most 5 test sends a minute, `reseller_message.test`). **Send now** asks first with the count, then the message goes out through the broadcast machinery: the same queue and pace, blocked users skipped and marked, and its broadcast page with progress and delivered, blocked and failed counts (`reseller_message.send`). Each reseller's text is written when it starts and kept with them as a recipient (the figures of that moment). API: `POST /api/v1/reseller-messages` `{debt_only, tier_id, language, texts, button, settle_button}`, `POST /api/v1/reseller-messages/audience`, `/check` (`{lang, text}`: the live check with the placeholders), `/preview` and `/test` (`{reseller_id, lang, text, ...}`).
   - **Weekly settlement reminder** (a card on the Messages tab; **off by default**): on, core sends the resellers a reminder once a week at a weekday and time of the store's time zone (`general.timezone`; **Thursday 21:00** by default): "Your account debt is {debt}. Please settle it as agreed." in each reseller's own bot language. Settings (all audited as `setting.set`, group `reseller_reminder`, also in the bot's admin settings): `resellers.reminder.enabled`, `.weekday` (saturday to friday), `.time` (HH:MM), `.debt_only` (default on: only resellers who owe; off: every active reseller), `.settle_button` (default on) and `.text.fa` / `.text.en` (the same placeholders; empty or equal to the built-in text = the built-in text). The reminder is a message to resellers queued by core (kind `reminder`, no author) and goes out the same way. **Exactly once a week:** its claim (`reseller_reminder_claims`, keyed by the local date it was due on) is written in the transaction that writes the week's broadcast, so a restart, a crash or a second core neither loses nor repeats it. It goes when the clock has passed the weekday and time (a time skipped when clocks go forward is reached when they land after it; one that happens twice, the first time), only if the reminder was switched on before that week's time, at most 6 hours late (a core that was down longer skips that week: missed weeks are **not** made up) and never within 6 days of the last one (a moved weekday does not send twice in a week). The card shows the time zone, the next reminder, the last one (when, and delivered to N of M, with a link to its broadcast) and **Send a test to me now** (the saved reminder, in the staff member's bot language, as a chosen reseller or the first of its audience would get it; `reseller_reminder.test`). Each week's reminder is audited as `reseller_reminder.queue`. API: `GET`/`PUT /api/v1/reseller-messages/reminder` (`{values: {key: value}}`, only the reminder's keys), `POST /api/v1/reseller-messages/reminder/test` `{reseller_id}`.
9. **Servers (3x-ui):** several panels: URL, write-only token, subscription URL, weight, **a limit on its services**, on/off for new services, version, health, services and plans per server, a connection test, and an inbound list whose attach toggles affect new services only. The **service limit** (`max_clients`, 0 to 1,000,000; empty or 0 is no limit, which every server starts with; in the Edit dialog as "Service limit", by anyone with `servers.write`, audited in `server.add` and `server.update`) is the most services the server takes. The Services column shows "N / limit" with a bar, a warning chip from 90% (`CAPACITY_WARN_PCT`) and "Full" at the limit, and a line above the table names the servers that need attention (a switched-off server is shown but not warned about); the Overview's servers card names them too. A full server is passed over when a new service is made and refuses a request that names it, and a plan whose servers are all full waits like one with no working server (the order stays paid and is delivered by the first retry after the limit is raised or a server added). Staff are told by a daily notice from the watchdog, to Telegram and the System page's open problems (key `server.<id>.capacity`, `alert.server_capacity` from 90%, `alert.server_full` at the limit; raising the limit ends it without a message). `GET /api/v1/servers` carries `max_clients` on every item, and `POST`/`PUT` take it (a number; left out it keeps the stored limit on an edit). A limit set below what the server holds moves no service: it is full and takes no more. A panel on a private network (plain http allowed there) is the owner's to add, or to give another address or token; admins may rename, reweight or switch one off.
10. **Payment gateways (owner):** Zarinpal on/off, sandbox, write-only merchant id and proxy, test connection, payments of the last 7 and 30 days and recent check errors; IDPay, NextPay and Aqayepardakht (on/off, a write-only API key, or PIN for Aqayepardakht, whose test system is the PIN `sandbox` and not a switch, a write-only proxy, test connection, the same report); automatic USDT (Phase 6a: on/off, the TRON address, an optional write-only TronGrid key and proxy, test connection, its report, and the unmatched deposits staff mark handled once they credited the customer by hand); the manual methods and Stars with their on/off switch and details (moved from Settings).
11. **Support:** ticket inbox, canned replies, assignment. The inbox filters by category: Payment, Service, Other and **Reseller application**, the category of a ticket the bot files when a customer taps Become a reseller and writes about themselves (the ticket page and the Mini App name it too; making the applicant a reseller is the Resellers page's action, as before).
12. **Broadcasts:** compose (fa/en), audience filter, schedule, progress. The list also holds the messages to resellers (Resellers page, Messages tab) and the weekly reminders, with their audience ("Resellers · only those who owe · weekly reminder"); those are never opened in the composer (cancel one and write a new one), and a reminder's author is "Automatic".
13. **Branding and texts:** name, logo, colors, welcome message, all bot strings per language, legal links, currency, and **the store's own Terms and Privacy pages** (`GET /terms` and `GET /privacy` on the store's domain, `?l=en` for English: a general default text in Persian and English with `{store}` and `{contact}` filled in; the owner edits either text per language on the Branding page, "Your Terms and Privacy pages", or goes back to the default; settings `legal.terms.fa|en` and `legal.privacy.fa|en`, saved with `PUT /api/v1/branding` as `legal`; the text is a small markdown (`#`, `##`, `-`, `**bold**`, https links) escaped before it is shown; while `branding.terms_url` and `branding.privacy_url` are empty the bot and the subscription page link these pages).
    - **Help content** (a tab of Branding, `?tab=help`, `branding.write` like the texts): the FAQ and the tutorials the bot shows ([03-bot-ux.md](03-bot-ux.md)). Each list adds, edits, reorders (up and down), switches on or off and deletes entries; an entry has a title (the question) and a text in Persian and English, tutorials also up to 8 https URL buttons with a label per language. The editor checks as you type (lengths, a language with only half a pair, button addresses; core checks the markup and has the last word), and a live preview shows the entry as the bot shows it in either language with the fallback, how the FAQ will be laid out (one message or a list) and the support button. An edit made from an out-of-date copy is refused ("changed meanwhile"). Every change is audited as `help.*` (entity `help_entry`) and reaches the bot within seconds. An empty list offers the default content. API: `GET/POST /api/v1/help`, `POST /api/v1/help/check|order|defaults`, `PUT/DELETE /api/v1/help/{id}`, `PUT /api/v1/help/{id}/enabled`.
14. **Settings:** general, notifications, the daily sales summary in Telegram (`reports.daily_summary` on or off, `reports.daily_summary_time`), the Growth card (`growth.leaderboard.enabled`: the bot's Top users board of the customers who invited the most people, on by default, shown while the referral program is on), the Plans and bulk purchase card (`bot.plan_layout`: the bot's plan list as a table, a header row and each plan as a row of two buttons for its name and its price, or as a list with one button per plan; `bulk.enabled`: the Bulk purchase button on the bot's home menu and core's side of it, on by default; `bulk.max_quantity`: the most services one bulk purchase holds, 2 to 100, 20 by default), the **Required channels and groups** card (below), the **Prometheus metrics** card under the monitoring settings (the token of `GET /metrics`, [Monitoring API](#the-prometheus-endpoint)), rate limits, maintenance mode, timezone.
    - **Required channels and groups** (`GET` and `PUT /api/v1/join-chats`; the list customers must join before they can use the bot or the Mini App, [03-bot-ux.md](03-bot-ux.md) "Required chats"). It replaces the old Join card (the generic `GET`/`PUT /api/v1/settings` still list and take `join.channel`, `join.link` and the new `join.label`, so nothing that used them breaks). One editable **list**: each row has the channel or group (a public one's `@name`, a `t.me` link or Persian digits are understood; a private one's `-100…` id), its join link (`https://t.me/…`, required for a private chat) and an optional name for the button (at most 32 characters), a **Check channel** button that asks the bot whether it can see the chat and is its administrator (a channel or a group; `POST /api/v1/settings/channel-check`), and, besides the first row, move up, move down and remove. The **first row is the setting `join.channel`** (with `join.link` and `join.label`) and stays first: the others are the extra chats and can be reordered among themselves; removing the first row makes the next one the first, and an empty list turns the requirement off. The card counts the rows against the license (**"2 of 1"** when more are saved than the plan counts), disables **Add** at the limit with a note that Pro has no limit and a link to the License tab, marks the rows the plan does not count **"Inactive: needs Pro"** (dashed, they stay saved and can be edited, reordered and removed), and checks every row where it is typed with the rules core applies (a chat once in the list, a private chat needs its link, `https://t.me/` links, names on one line). It is saved **all at once** (one `PUT` with `{"chats":[{"chat","link","label"}]}`): all or nothing, a refused row is named by its field (`chats.2.link`), and a chat added past the license is a `409` `{"error":"state","license":"max_join_chats"}` with the usual license sentence. **Owner only** to change (`join.write`, an owner-only flag like `license.write`); an admin sees the list read only (`GET` needs `settings.write`) and keeps `/set join.channel`, `join.link` and `join.label` in the bot, as before. The list holds at most 50 rows. Every change is in the audit log (`setting.set` for the first row; `join_chat.add`, `.update`, `.move`, `.remove` for the others, entity `join_chat`, the chat as its id), the extras are in every backup (the `core.join_chats` table, migration 00029), and the bot and the Mini App pick the new list up at once (core tells the bot to reload; the Mini App forgets its cached list).
15. **Staff:** invite, roles, 2FA status, sessions.
16. **System (owner and admin):** tabs. *Health*: every service (core, bot, payments, provisioner, backup) with its version, health and readiness checks, a warning when versions differ, and the 3x-ui servers and payment methods. *License*: what applies now (the free tier, the 30-day trial with its days left, or the license's tier) with whom to contact to buy or renew a license (the owner's `BOBRES_SELLER_CONTACT`, or "ask the seller you got BOBRES from" when none is set); the installed token's state (none, invalid with the reason, active, grace, expired), tier, ids, dates and days left, the domain and install binding, this install's id to copy, and each entitlement in effect with its use (a warning from 90%); the owner pastes or opens a token, checks it, and saves it with a reason, or activates one with a license key from the seller (05-license.md). *Updates*: the running and newest versions, with a **Check again** button (the bot looks at GitHub now and the page shows the result, including why a look failed), the update channel, and **Update now**. The channel is the owner's choice (stable, or beta which includes pre-releases; saved as the `updates.channel` setting, audited as `setting.set`; unset, the server's `BOBRES_UPDATE_CHANNEL` decides) and changing it makes the bot look again at once. **Update now** (the owner; `system.write`) opens a confirmation that names the versions and channel, says the dashboard and the bot are away for a few minutes while customers' VPN links keep working, and that a backup comes first and the update goes back on its own if the new version does not come up healthy; a pre-release needs its own tick, and an owner with a password login and an authenticator app gives a current code (as for replacing the password). Core never touches Docker: it records the request (audit `system.update.requested`, a Telegram notice to every owner) and writes `request.json` into a folder it shares with the host's updater (a systemd path unit, `bobres-updater.path`, installed by `bobres install` and `bobres update`), which runs the same signed, backed-up, rolling-back `bobres update` as the command line. The page then shows the steps (verify, backup, pull, restart, health check), the updater's sentence and, for the owner, the newest log lines; it says so and keeps trying while core restarts, and offers to reload the dashboard when the new version is up. The outcome (done, failed, rolled back, or never picked up) is audited and sent to every owner. Where the updater is not installed yet (an install from before it, or the folder not mounted) the page says updating from the dashboard is not available yet and keeps the `sudo bobres update` command to copy. An informational note shows when the newest release is newer than the license's update date (the update is offered all the same: security updates are never withheld). *Backups*: each backup with its size, Telegram delivery and off-site copy, a button to run one now, and the off-site storage's state. The owner (`system.write`) can **download** any backup that is a file (a dialog says it is encrypted and that the key is never sent with it, asks for the authenticator code where the session has one, and the browser then takes a single-use address as its own download; the file is streamed, never held in memory; every download is audited as `system.backup.download` and told to every owner on Telegram) and **restore** one, either from the list or from a file uploaded in the *Restore from a file* card (at most 2 GiB, kept 30 minutes for the confirmation). A restore replaces ALL the store's data with the backup's, so its dialog says what is lost (everything after the backup's time: orders, payments, new customers and services, balances, tickets, settings), that the dashboard and the bot are unavailable for a few minutes while customers' VPN links keep working, that a safety backup of the current data comes first and the store goes back to it on its own if the restored data is not healthy, and that every owner is told; the owner types the store's domain, ticks that they understand, and gives the authenticator code where there is one. Only a backup made by this server (opened by its backup key) can be restored from the dashboard. The server's updater does it (the same helper as the update; the page says to run `sudo bobres update` once when the updater is from before restoring), and the page shows the steps (verify, safety backup, stop, restore, start, health check, and rollback when needed), the updater's sentence and, for the owner, the newest log lines; it says so and keeps trying while core restarts, and finds the end of the request again when core is back on the restored data. The request and its outcome are audited (`system.restore.requested` and `.done`, `.failed`, `.rolled_back`, `.not_started`; `system.restore.upload` for a file received) and sent to every owner on Telegram. Admins see the state of a restore but have no buttons. *HTTPS*: how each address the store is served on (the domain, the server's IP address, the Zarinpal domain) gets its certificate, with the issuer, expiry, whether browsers trust it, the live error and the HTTPS applier's last result (and a red line, "HTTPS gateway (Caddy) is not reachable", when Caddy answers on none of them); the owner switches between the domain's automatic certificate, a Let's Encrypt certificate for the server's IP address and an own certificate and key, with the warnings of each mode (`system.write`, audited as `system.tls.set`; the runbook is runbooks/https.md). *Logs* (owner only): the newest warnings and errors of every service, redacted. The Health tab also lists the open system problems. A backup of another server, or a restore while the dashboard is down, is done on the server (`bobres restore`). Without a valid license the install runs on the free tier after its trial, and its limits refuse only new servers, services, staff, resellers and payment methods (a refusal names the limit and says to buy or renew a license); staff who see this page get a small chip in the top bar for the free tier, the trial (with its days left) or a license that needs a look. "Powered by BOBRES" shows at the foot of the dashboard and the login page on every install except one whose license includes white label.
17. **Audit log:** filterable, exportable.
18. **Reports** (`reports.read`: owner and admin; support has neither the page nor the revenue cards of the Overview, while single orders' amounts stay visible to support through Orders by design): revenue per day, week or month for a range (today, last 7 or 30 days, this month, last month, or two dates), always per currency (never summed), with paid orders (new, renewals, traffic packages, and the licenses and digital accounts the store sells, each with its own line and chart series when there are any), new customers, free trials, churn (services and customers lost after a 7-day grace period, and the services still inside it; trials, unlimited services and pending renewals are not counted), wallet top-ups, refunds, the **cashback** given (the credits less what refunds took back; not revenue, not taken off what was kept), payments rejected by staff, failed or expired gateway payments and cancelled orders; charts per currency (revenue and what was kept, revenue by plan, orders by type, how orders were paid, the top plans, the busiest hours of the week, new customers and lost services; each with a legend, a tooltip, keyboard keys and a table of its numbers, drawn by the chart kit in `components/charts/`), the by-period table in a box of its own that scrolls with its header, first column and totals held in place, the top plans and how orders were paid (wallet, staff-approved, gateway, each split by the provider that took the payment: Zarinpal, Stars, automatic USDT, card to card...); a **Professional report** view (`?view=report`) to read, print or save as PDF, with a headline and a sentence per number in plain words, each number's change on the period before, and 3D charts (an isometric column scene and a tilting donut, loaded on demand, flat on phones and for people who prefer reduced motion); CSV files of the summary and of the paid orders, audited as `export.reports` with their outcome. A Calendar switch picks Gregorian (weeks from Monday, calendar months) or Jalali (weeks from Saturday, Jalali months named by month and year), and every date of the page follows it, in both directions: the days covered, the charts, the table, the custom range (dates typed or picked from a month grid in the calendar shown, converted exactly to the Gregorian days the API takes) and the CSV files' extra Jalali date column and names. The choice is kept in the browser; until one is made it is Jalali in Persian and Gregorian in English. All in the store's time zone (the Settings page, else `BOBRES_TIMEZONE`, else Asia/Tehran). A line under the hint says whether the daily summary in Telegram is on, at what time and in which zone, and links to its settings for staff who may change settings. A line above it says where the reports start counting, and the owner has **Start from zero** there (with a confirmation: from then on the reports, the CSV files, the daily summary and the Overview's revenue count only what is sold after it; nothing is deleted) and **Count everything again** (`GET /reports/since`, `POST`/`DELETE /reports/reset`, audited as `reports.reset` and `reports.reset_clear`). Every number is defined in [14-reports.md](14-reports.md).
19. **Monitoring** (a tab of the System page, shown to staff with `monitor.read`: owner and admin, read only; support has no access and a role without it does not see the tab, and `?tab=monitoring` falls back to Health): one overview of the whole store and the server it runs on, built from what core samples every 30 seconds (the full contract, with every JSON field, is in [Monitoring API](#monitoring-api) below). The tab opens with a **range switch** (1 hour, 6 hours, 24 hours, 7 days, 30 days, kept in the browser), an **automatic refresh** switch (on) with a **Check again** button and the time of the last reading, and links to its sections. It asks for the overview and the events every 20 seconds while the tab is shown and the browser tab is in front (it pauses when the tab is hidden or another System tab is open, and asks at once when it is shown again) and for the history of the chosen range when a new point can have appeared. Sections: **Overview** (a verdict in words and with an icon: everything is working, working but something needs a look, or something important is down; what needs a look, worst first, in plain words; every part core checks grouped as services, data stores, server, jobs, panel servers and payment methods, each with its state as an icon and a word; and the store in numbers: open alerts, errors in the last hour, orders and payments waiting, paid orders not yet delivered, the event backlog, tickets, active services and customers; a figure core could not read is "not known", never 0); **Bot and store** (charts: updates the bot handles per minute, errors logged per minute, the queues, core's tasks and database connections); **Server** (CPU, load, memory, swap, traffic, uptime, every disk with its share, OS, kernel, Docker engine and clock, with charts of the range); **Docker** (every container with image version, state, health, restarts, how long it has run, CPU, memory and traffic; what Docker keeps on the disk; CPU and memory by container); **Kubernetes** (nodes and pods where the collector found a cluster; otherwise a plain note that none was found, never an error); **Services** (the BOBRES services and data stores with state, version and uptime, their response time over the range, and the systemd units the collector watches: its own timer, the updater and Docker); **Uptime** (how available each BOBRES service and each panel server was over 24 hours, 7 days or 30 days, worked out from the stored samples: the percentage of the time that was observed, the number and total length of the outages, the longest one, and a bar with a cell for each half hour, three hours or twelve hours; a moment nobody sampled is **no data**, a hatched cell that is left out of the percentage and is neither up nor down, and a service or server nobody sampled yet says "No data", never 0% or 100%; a panel server that is switched off is marked and its off time is no data; the section states how long the samples are kept (30 days) and since when there are any, and whether the public status page is on; the period switch is kept in the browser; it has an answer of its own, so it stays when the overview cannot be read, and is asked for once a minute); **CI/CD** (the latest runs on `main`, the Release workflow's runs and the latest release, each linking to GitHub; links that do not point to github.com are not links; when GitHub cannot be reached, or its hourly limit is used up, the tab says so and keeps what was last read); **Deployments** (the update history: who asked, from and to which version, the outcome and how long it took); **Alerts and logs** (the newest warnings and errors in one list with a filter for warnings and errors or errors only: the watchdog's alerts raised and fixed, written in the language of the page from the same texts as the Telegram message, the error lines of the host's journal, and, for the owner only, the lines the services logged, as on the System page's logs tab; the admin is told that these lines are the owner's). Charts are the chart kit's line charts (`components/charts/`): a moment nobody sampled is a **gap in the line**, never a zero, and the chart's tooltip and table say "no data" there; they follow the language's digits, the calendar chosen on the Reports page and the theme, and each has the kit's table, legend and keyboard keys. The host figures, the containers, the systemd units and the host's journal need the **host collector** (`bobres monitor`, a root timer on the server that is on by default, [06-installer.md](06-installer.md) "Host monitoring"); without it the tab still shows everything core can ask itself and says so where the figures would be: `host.available` is `false`, and the tab says whether the collector is not installed, has not reported for some time, or writes a file core cannot use, with the command `sudo bobres monitor enable` (and `sudo bobres monitor status`) to copy; the charts keep the history that was recorded. If the overview cannot be read the tab says so and leaves out what is drawn from it. **Settings → Monitoring** has the switch (`monitor.enabled`, on), the public status page (`status_page.enabled`, **off**: [The public status page](#the-public-status-page)) and the alert thresholds (`monitor.disk_pct` 85, `monitor.mem_pct` 90, `monitor.mem_minutes` 10, `monitor.restarts` 3, `monitor.collector_minutes` 5, `monitor.ci_alert` off). The alerts reach the staff on Telegram and the System page's open problems like every other alert ([runbooks/monitoring.md](runbooks/monitoring.md)).
20. **The vendor's own pages** (owner, `license.write`, and only on a store whose license grants `private_features`: the owner's own store,
    [13-license-server.md](13-license-server.md)). **Licenses**: the license server's key, the switch of the sales, every license of the
    server and the licenses the bot sold, each with **Give months**, **Revoke** and **Refund** (the buyer gets back what they paid in their
    wallet, the license is revoked, and an affiliate's commission is taken back within 30 days of the payment; `POST
    /api/v1/vendor/licenses/{id}/refund {reason}`, audited as `vendor.refund`). **Digital accounts**: the supplier resale
    ([supplier.md](supplier.md)). **Affiliates** ([18-hosted-and-affiliate-plans.md](18-hosted-and-affiliate-plans.md), section 2): the
    program's switch (off by default), percent (20 by default) and minimum payout (`PUT /api/v1/affiliates/settings`); the affiliates with
    their code, percent (own or the program's), status, customers brought, licenses, earned, paid and wallet (`GET /api/v1/affiliates`); one
    affiliate's commissions, with what refunds took back, and payouts (`GET /api/v1/affiliates/{id}`); a percent of their own or pausing them
    (`PUT /api/v1/affiliates/{id} {percent, status}`); **Record a payout** made by hand (`POST /api/v1/affiliates/{id}/payouts {amount,
    note, request_id}`: at least the minimum, at most what is earned and not paid, leaves their wallet). **Hosted stores** (section 1): the
    register of the customers' stores the owner runs, from the code `tools/hosted` prints or typed (`GET`/`POST /api/v1/hosted`,
    `PUT`/`DELETE /api/v1/hosted/{id}`), each store's state, version, channel and figures read from its `/metrics` through the bot every five
    minutes or on **Read now** (`POST /api/v1/hosted/{id}/check`), and **Make the Hosted license** (a pro license, its key shown once; `POST
    /api/v1/hosted/{id}/license {months, request_id}`). The metrics token is write-only (`has_token`); a customer's bot token and backup key
    are never here.

## Monitoring API

Five read-only endpoints under `/api/v1/monitor/`, the dashboard's usual session cookie (no CSRF token, they are `GET`s), the
permission `monitor.read` (owner and admin; support gets `403 {"error":"forbidden"}`), `Cache-Control: no-store`, and `503
{"error":"not_configured"}` when this build of core was started without the monitor. A request that is wrong is `400
{"error":"invalid","message":"..."}`; nothing here writes. Every list is a list (`[]`, never `null`); a figure core does not
know is `null` or absent (`"available": false`), never `0`. Times are unix seconds; sizes are bytes; rates are per second.

### `GET /api/v1/monitor/overview`

Everything the page draws, in one answer. It costs one asking of the other services at most every 10 seconds however many pages are open
(the answer is shared), so a page may poll every 15 to 30 seconds.

```json
{
  "generated_at": 1792500000,
  "status": "degraded",
  "components": [
    {"id": "core", "name": "Core", "kind": "service", "status": "ok", "detail": "answers in 2 ms", "version": "0.7.0", "uptime_s": 86400, "since": 1792413600},
    {"id": "bot", "name": "Telegram bot", "kind": "service", "status": "ok", "detail": "answers in 4 ms", "version": "0.7.0", "uptime_s": 86300, "since": 1792413700},
    {"id": "postgres", "name": "Postgres", "kind": "datastore", "status": "ok", "detail": "answers in 1 ms, 84.2 MiB of data", "version": "16-alpine", "uptime_s": 864000, "since": 1791636000},
    {"id": "redis", "name": "Redis", "kind": "datastore", "status": "ok", "detail": "running, healthy", "version": "7-alpine", "uptime_s": 864000, "since": 1791636000},
    {"id": "caddy", "name": "Caddy (web gateway)", "kind": "service", "status": "warn", "detail": "running, but its health check fails", "version": "2.11.7-alpine", "uptime_s": 600, "since": 1792499400},
    {"id": "host", "name": "Server", "kind": "host", "status": "ok", "detail": "cpu 12%, memory 41%, disk / 55%", "version": "Ubuntu 24.04.5 LTS", "uptime_s": 1209600, "since": 1791290400},
    {"id": "panel.0d7c...", "name": "DE-1", "kind": "panel", "status": "ok", "detail": "212 clients", "version": "2.4.0", "uptime_s": null, "since": null},
    {"id": "gateway.zarinpal", "name": "zarinpal", "kind": "gateway", "status": "ok", "detail": "offered to customers", "version": "", "uptime_s": null, "since": null},
    {"id": "collector", "name": "Host collector", "kind": "job", "status": "ok", "detail": "last snapshot 12 seconds ago", "version": "0.7.0", "uptime_s": null, "since": null},
    {"id": "backups", "name": "Backups", "kind": "job", "status": "ok", "detail": "last backup 3 hours ago", "version": "", "uptime_s": null, "since": 1792489200},
    {"id": "ci", "name": "CI of main", "kind": "job", "status": "ok", "detail": "the latest CI run on main passed", "version": "", "uptime_s": null, "since": 1792480000}
  ],
  "host": {
    "available": true,
    "hostname": "srv1",
    "cpu_pct": 12.5,
    "load": [0.5, 0.4, 0.3],
    "cores": 4,
    "mem": {"used": 3400000000, "total": 8300000000},
    "swap": {"used": 1048576, "total": 1073741824},
    "disks": [{"mount": "/", "used": 42949672960, "total": 107374182400, "avail": 59055800320, "pct": 42.1, "roles": ["install"]}],
    "net": {"rx_bps": 18432.5, "tx_bps": 20211.1},
    "uptime_s": 1209600,
    "os": "Ubuntu 24.04.5 LTS",
    "kernel": "6.8.0-45-generic",
    "docker": {"ok": true, "version": "29.1.0", "api": "1.52", "error": ""},
    "clock": {"synchronized": true, "ntp": true},
    "docker_disk": {"images": 3200000000, "images_reclaimable": 800000000, "containers": 12000000, "volumes": 900000000, "build_cache": 0, "collected_at": 1792499800}
  },
  "containers": [
    {"name": "bobres-core-1", "project": "bobres", "service": "core", "image": "ghcr.io/sobhanaz/bobres-core:0.7.0", "state": "running", "health": "healthy",
     "restarts": 0, "started_at": 1792413600, "cpu_pct": 3.2, "mem_bytes": 91750400, "mem_limit": 536870912, "net_rx_bytes": 123456, "net_tx_bytes": 234567,
     "oom_killed": false, "exit_code": null}
  ],
  "units": [
    {"name": "bobres-monitor.timer", "load": "loaded", "active": "active", "sub": "waiting", "enabled": "enabled", "result": "", "since": 1792494000, "restarts": 0},
    {"name": "docker.service", "load": "loaded", "active": "active", "sub": "running", "enabled": "enabled", "result": "", "since": 1791410000, "restarts": 0}
  ],
  "kubernetes": {"detected": false, "reason": "not detected", "nodes": [], "pods": [], "truncated": false},
  "counters": {"open_alerts": 1, "errors_1h": 3, "orders_pending": 2, "payments_pending": 1, "provision_queue": 0, "outbox_backlog": 0,
               "tickets_open": 4, "services_active": 212, "customers_total": 640},
  "ci": {
    "available": true, "checked_at": 1792499900, "error": "", "detail": "", "repo": "sobhanaz/bobres-3x-ui-telegram-panel",
    "runs": [{"workflow": "CI", "branch": "main", "event": "push", "status": "completed", "conclusion": "success", "url": "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/actions/runs/1", "sha": "abc1234", "at": 1792480000}],
    "release_runs": [{"workflow": "Release", "branch": "v0.7.0", "event": "push", "status": "completed", "conclusion": "success", "url": "https://github.com/...", "sha": "def5678", "at": 1792300000}],
    "latest_release": {"tag": "v0.7.0", "published_at": 1792300500, "prerelease": false, "url": "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.7.0"},
    "rate_reset_at": 0, "main_failing": false
  },
  "collector": {"installed": true, "fresh": true, "age_s": 12, "error": "", "version": "0.7.0", "generated_at": 1792499988, "interval_s": 30, "duration_ms": 180, "notes": []},
  "deployments": [
    {"id": "5f1c...", "from": "0.6.1", "to": "0.7.0", "channel": "stable", "at": 1792300600, "finished_at": 1792300780, "outcome": "done", "source": "dashboard", "by": "Sam", "message": ""}
  ]
}
```

**`status`** is the stack at a glance, computed by these rules: `down` when a **required** component is `down` (`core`, `bot`,
`payments`, `provisioner`, `postgres`); otherwise `degraded` when any other component is `warn` or `down`, or an alert at the
critical level is open; otherwise `ok`. A component whose state is `unknown` never worsens it (a server that was never checked,
a collector that is not installed).

**`components[]`**: `id` is stable (a page may translate it), `name` is its English label, `detail` a short sentence, `version`
`""` when not known, `uptime_s` and `since` (when it started) `null` when not known.

| `kind` | ids | `ok` / `warn` / `down` / `unknown` |
|---|---|---|
| `service` | `core`, `bot`, `payments`, `provisioner`, `backup` (only where installed): the services core asks at `/healthz` and `/readyz`; `caddy`, `tls-agent` and any other Compose service of the install, from the containers (only while the collector's snapshot is fresh) | `ok` answers; `warn` alive but not ready (`detail` names the failing check), running with a failing Docker health check, paused, or running another version than core; `down` does not answer, not running, restarting in a loop, or listed by Compose with no container; `unknown` not configured |
| `datastore` | `postgres` (core pings it and says how big it is), `redis` (from its container) | as above |
| `host` | `host` | `warn` when a disk or the memory is past its alert threshold (`detail` says which); `unknown` while the collector says nothing |
| `panel` | `panel.<server id>` for every 3x-ui server | `ok` answers, `down` does not (`detail` is the server's last error), `unknown` never checked |
| `gateway` | `gateway.<provider>` for every payment gateway that is switched on (`zarinpal`, `idpay`, `nextpay`, `aqayepardakht`, `usdt_auto`, `ton_auto`) | `ok` offered to customers; `warn` switched on but not offered, or its settings have a problem. This is the state the payments service reports, not a live call to the provider |
| `job` | `collector` (the host collector), `backups` (the newest backup and its age), `ci` (the CI of `main`) | `collector`: `ok` fresh, `warn` it has gone quiet, `unknown` not installed. `backups`: `warn` older than 26 hours or its last run failed, `unknown` none yet. `ci`: `warn` the latest finished CI run on `main` failed, `unknown` GitHub unreachable or no finished run |

**`host`**: when the collector is not installed, is off, or its snapshot is older than 90 seconds, `available` is `false`, `reason`
says why in a sentence, and every other field is empty (`null`, `0`, `[]`, `""`); show "no data", not zeros. Otherwise: `cpu_pct`
is the busy share since the previous snapshot (`null` on the first), `load` the 1, 5 and 15 minute averages, `mem.used` is total
less what a program could still get without swapping, `disks[]` are the filesystems of the install directory and of Docker's
data root (`pct` is what `df` shows; `roles` says `install` and/or `docker`), `net.rx_bps` and `net.tx_bps` are the real
interfaces' traffic between two snapshots (`null` until there are two), `docker.ok` is `false` when the engine did not answer
(`error` says why), `clock.synchronized` and `clock.ntp` are `null` when not known, `docker_disk` is `null` or what `docker
system df` showed at `collected_at` (it is refreshed every 15 minutes).

**`containers[]`**: every container the engine lists (at most 64), `project` is its Compose project (`bobres` for the stack),
`state` is `running`, `restarting`, `paused`, `created`, `removing`, `exited`, `dead` or `unknown`, `health` is `none`,
`starting`, `healthy` or `unhealthy`, `cpu_pct` is the share of one core since the previous snapshot (`null` when unknown),
`mem_limit` is `0` for no limit, `exit_code` is `null` while it runs. Empty while the host is not available.

**`units[]`**: the systemd units the host collector watches (the fixed list: `bobres-monitor.timer` and `.service`, `bobres-updater.path` and `.service`, `docker.service`; one that is not installed is left out), as systemd reports them: `load` (`loaded`, `not-found`), `active` and `sub`
(`active`/`waiting`, `active`/`running`, `inactive`/`dead`, `failed`/`failed`...), `enabled` (the unit file's state), `result` (why it last ended, `""` when it did not fail), `since` (when it entered its state) and `restarts`
(`0` when systemd does not say). Empty while the host is not available, like `containers[]`.

**`kubernetes`**: `detected` is `false` where there is no `kubectl` (or `k3s kubectl`) or no cluster answers (`reason` says
which); otherwise `nodes[]` is `{name, ready, roles, version}` and `pods[]` is `{namespace, name, phase, ready, restarts, node}`
(at most 32 nodes and 200 pods; `truncated`). BOBRES itself does not run on Kubernetes; this shows a cluster that happens to be on the server.

**`counters`**: `open_alerts` (the watchdog's open problems), `errors_1h` (error lines the services kept in the last hour: what
their log buffers still hold, at most 500 lines each), `orders_pending` (created or awaiting payment), `payments_pending` (manual
payments waiting for a review, at most 200), `provision_queue` (paid and not yet delivered), `outbox_backlog` (core events the
slowest consumer has not read), `tickets_open`, `services_active`, `customers_total`. A figure core could not read is `null`.

**`ci`**: from GitHub's public API through the bot (core has no internet access), cached for about 5 minutes. `available` is `true` when
there are facts (possibly older ones: then `error` says what is wrong now), `error` is `""`, `not_checked` (the first look has not
finished), `unreachable` (GitHub or the bot did not answer), `rate_limited` (GitHub's 60 requests an hour per address are used up; `rate_reset_at`
says when they come back) or `invalid`; `detail` is a sentence. `runs[]` are the newest runs of any workflow on `main` (at most 10, newest first),
`release_runs[]` the newest runs of the Release workflow (`branch` is the tag it ran for), `latest_release` the newest published release
(`null` when none), `main_failing` whether the newest finished CI run on `main` failed (`null` when none). Links point into the repository only.

**`collector`**: `installed` (a snapshot file exists), `fresh` (not older than 90 seconds), `age_s`, `error` (`""`, `invalid`, `too_large`
or `unreadable`: the file cannot be used), `version` (the bobres that wrote it), `notes[]` (the parts the last run could not read).

**`deployments[]`**: the updates of the install, newest first, at most 10. `source` is `dashboard` (asked for on the System page: `by`
is who, `outcome` is `running` while it is open, then `done`, `failed`, `rolled_back` or `not_started`, `message` the updater's sentence)
or `cli` (run by hand with `bobres update`, only the newest is known: `outcome` is `unknown`, or `rolled_back` once `bobres rollback`
used its restore point). `at` is when it began, `finished_at` is `0` while it is open or not known.

### `GET /api/v1/monitor/series?metric=<id>&range=1h|6h|24h|7d|30d`

```json
{"metric": "host.cpu_pct", "unit": "percent", "step_s": 30, "agg": "avg", "points": [[1792496400, 11.2], [1792496430, 13.8]]}
```

`range` defaults to `1h`. `points` are `[unix_seconds, value]`, oldest first; each is one bucket of `step_s` seconds (1h: 30, 6h: 60, 24h: 300,
7d: 1800, 30d: 7200, so at most about 360 points) and its time is the bucket's start. A bucket nobody sampled has **no point**: draw a gap.
`agg` says how the samples of a bucket became one value: `avg`, `min` (up/down metrics, so any downtime shows) or `max` (counters and queue
depths). A metric with no data yet is `200` with `"points": []`; a name that is not a metric (or needs a subject it has not got) is `400`.
Several at once: `metrics=a,b,c` (at most 12) answers `{"range": "1h", "series": [<the object above>, ...]}`; `metric` and `metrics` are not
combined.

`GET /api/v1/monitor/metrics` lists the metrics that have data, `{"metrics": [{"metric", "unit", "agg"}]}`, so a page can offer this
install's disks and containers.

| Metric (`<subject>` after a colon) | Unit | What |
|---|---|---|
| `host.cpu_pct`, `host.load1`, `host.mem_pct`, `host.swap_pct` | `percent`, `load` | the host (needs the collector) |
| `host.disk_pct:<mount>` | `percent` | one filesystem, e.g. `host.disk_pct:/var/lib/docker` |
| `host.net_rx_bps`, `host.net_tx_bps` | `bytes_per_s` | the host's traffic |
| `container.cpu_pct:<service>`, `container.mem_bytes:<service>`, `container.restarts:<service>` | `percent`, `bytes`, `count` | a Compose service of the stack (`core`, `bot`, `postgres`, ...); a service with several containers is summed (restarts: the largest) |
| `core.goroutines`, `core.heap_bytes`, `core.db_conns`, `core.db_size_bytes` | `count`, `bytes` | core itself, its database connections in use, the database's size |
| `core.errors_per_min`, `core.warnings_per_min`, `bot.errors_per_min`, `bot.warnings_per_min`, `bot.updates_per_min` | `per_min` | rates from counters; no value for the round after a restart |
| `bot.updates_queued`, `bot.goroutines`, `bot.heap_bytes` | `count`, `bytes` | the bot's own gauges |
| `queue.provisioning`, `queue.outbox`, `queue.pending_orders`, `queue.pending_payments` | `count` | queue depths |
| `service.up:<name>`, `service.latency_ms:<name>` | `bool`, `ms` | `core`, `bot`, `payments`, `provisioner`, `backup`: `1` answers, `0` does not; latency only while it answers |
| `panel.up:<server id>`, `gateway.up:<provider>`, `license.ok` | `bool` | `1` healthy, `0` not; `license.ok` is `0` only in the license's grace period; `panel.up` is recorded only for a server that is switched on and has been checked (a server that is off is not checked, so its time off is no data, not downtime) |

### `GET /api/v1/monitor/events?level=warn|error&limit=100`

```json
{"items": [
   {"at": 1792499000, "level": "warn", "source": "alert", "kind": "alert", "key": "monitor.disk:/", "state": "open",
    "title_key": "alert.monitor_disk", "args": {"mount": "/", "percent": "88", "free": "12884901888"},
    "title": "The disk at / is 88% full (12 GB free).", "detail": "87.8% of 98.0 GiB used"},
   {"at": 1792498500, "level": "error", "source": "host.docker", "kind": "journal", "title": "level=error msg=\"failed to start\"", "detail": ""},
   {"at": 1792498000, "level": "error", "source": "bot", "kind": "log", "title": "send failed", "detail": "{\"user_id\":42}"},
   {"at": 1792497900, "level": "warn", "source": "core", "kind": "log", "title": "usage unavailable", "detail": "{\"err\":\"deadline exceeded\"}",
    "count": 48, "first_at": 1792440000}
 ],
 "logs_visible": true, "unreachable": [], "truncated": false}
```

An alert also carries `title_key` (the bot's text, `alert.monitor_disk`) and `args` (its values: numbers as strings, `free` in bytes, names as they are), so a page writes the sentence in its own language as the
Telegram message does; `title` stays the English sentence for a page that has no text for it. A log line or a journal line has neither field.

A service that logs the same line again and again (the same message and values from one source at one level, whatever the ids in it) is one entry: `at` is the newest line, `count` how many there were and `first_at` the oldest one's time (a single line has neither field). Alerts are never folded.

Newest first. `level=warn` (the default) lists warnings and errors, `level=error` errors only; `limit` is 1 to 200 (default 100); anything else
is `400`. `source`/`kind`: `alert` (`kind: "alert"`, `key` is the check, `state` is `open` while the problem is open, `closed` for one that was
raised and has been fixed since, `resolved` for the entry that says it was fixed; `level` is the alert's: `error` for critical, `warn` for
warning), `host.docker` / `host.bobres` / `host.caddy` (`kind: "journal"`: error lines of the host's journal the collector scrubbed), and
`core` / `bot` / `payments` / `provisioner` (`kind: "log"`: lines the service logged, with its fields in `detail`). **The log lines carry
Telegram ids, so only the owner gets them** (`logs_visible` is `false` for an admin, who gets alerts and the journal); `unreachable` names
the services that did not answer. An alert's `detail` is shown only to those who may read it elsewhere (a gateway's to the owner). All text
is plain and cut (240 characters; `detail` 200 for a log's fields). `truncated` says more matched than `limit`.

### `GET /api/v1/monitor/uptime`

The availability of every service and every panel server over three windows, from the stored `service.up:<name>` and `panel.up:<id>` samples.
Core never computes it from a guess: a moment with no sample is **no data**, which is left out of the percentage and of the outages and is
not up and not down.

```json
{
  "generated_at": 1793000000, "retention_days": 30, "sample_every_s": 30,
  "since": 1792741200,
  "windows": [{"name": "24h", "span_s": 86400, "bucket_s": 1800}, {"name": "7d", "span_s": 604800, "bucket_s": 10800}, {"name": "30d", "span_s": 2592000, "bucket_s": 43200}],
  "services": [{"id": "core", "name": "core", "windows": {"24h": {
      "availability_pct": 99.653, "observed_s": 85800, "window_s": 86400,
      "outages": 2, "down_s": 300, "longest_s": 240,
      "bar": [[1800, 0], [1800, 300], [0, 0]]
  }, "7d": {}, "30d": {}}}],
  "panels": [{"id": "8f14e45f-...", "name": "DE-1", "enabled": true, "windows": {}}],
  "panels_unavailable": false,
  "status_page": false
}
```

- `services` are those with samples and those core asks now, in the order core, bot, payments, provisioner, backup (others by name); `panels`
  are the registry's servers in its order, by their name; a server that was removed is gone from the list. `panels_unavailable` is `true` (and
  `panels` empty) when the provisioner did not answer, so the page says it, not "no panel servers".
- Per window: `availability_pct` is the share of the **observed** time that the component was up, in percent to three decimals, `null` when
  nothing was observed in the window. Some downtime is never `100`: it is capped at `99.999`. `observed_s` is the seconds of the window that
  had a sample and `window_s` the length of the window.
- `outages` counts runs of samples in which the component was down (a gap of no data divides two runs), `down_s` is their total length and
  `longest_s` the longest. A stored 5-minute average (samples older than 7 days) counts for the share of its 5 minutes that was down.
- `bar` has a cell for each `bucket_s` of the window, oldest first, `[observed_s, down_s]` of that stretch: `[0, 0]` is no data, `down_s` of
  `0` is up.
- `since` is the start of the oldest sample of any listed component, `null` when nothing was sampled yet; before it there is no data.
- `enabled` (panels only) is whether the server takes new services; a server that is switched off is not checked, so it has no samples for
  that time. `status_page` is whether the public status page is on.

### The alert rules

They run in core's watchdog once a minute, so they have its debounce: seen failing twice in a row, announced, repeated after 6 hours and
daily, fixed once passing for 5 minutes ([runbooks/README.md](runbooks/README.md)); they go to the staff on Telegram and show in the System
page's open problems. The keys, thresholds and settings:

| Rule | Key | Fails when | Setting (default, range) |
|---|---|---|---|
| Disk | `monitor.disk:<mount>` | a monitored filesystem is fuller than the threshold (critical at 95%) | `monitor.disk_pct` (85, 50 to 99) |
| Memory | `monitor.memory` | memory use is above the threshold for the whole hold (critical at 98%) | `monitor.mem_pct` (90, 50 to 99), `monitor.mem_minutes` (10, 1 to 120) |
| Container down | `monitor.container.<service>.down` | a container of the stack is not running (or Compose lists the service with none) for 2 minutes. Not for `core`, `bot`, `payments`, `provisioner` and `backup`: their `service.<name>` checks already say it | |
| Container unhealthy | `monitor.container.<service>.unhealthy` | its Docker health check fails for 3 minutes | |
| Container restarting | `monitor.container.<service>.restarting` | it restarted this many times within 15 minutes (the rises of the recorded restart counter) | `monitor.restarts` (3, 2 to 50) |
| Collector quiet | `monitor.collector` | no snapshot for this long, or the file is unusable (only where the collector was installed: switching it off with `bobres monitor disable` removes its file and the alert) | `monitor.collector_minutes` (5, 2 to 60) |
| CI of main | `monitor.ci.main` | the newest finished `CI` run on `main` failed | `monitor.ci_alert` (**off**: a store that runs releases does not need to hear about `main`) |

While the snapshot is stale the host rules are not evaluated and their open alerts stay as they are (no evidence is not a recovery);
`monitor.enabled` off forgets them all without a message. A bot or GitHub that cannot be reached neither raises nor clears the CI alert.

### The Prometheus endpoint

`GET /metrics` (no session; on the store's own domain through the gateway) gives the same figures to Prometheus in its text format 0.0.4
([runbooks/monitoring.md](runbooks/monitoring.md#prometheus-and-grafana) has the metrics, a scrape configuration, alert rules and a Grafana
dashboard). It **does not exist until the owner makes a token**: `404` to everybody. With a token it wants `Authorization: Bearer <token>`
(never the address): a missing or wrong one is `401` with `WWW-Authenticate: Bearer`, and one client address gets `429` after 30 wrong tries in a
minute (the right token is never counted). The answer is `200`, `Content-Type: text/plain; version=0.0.4; charset=utf-8`, `Cache-Control: no-store`.
It is built from the monitor's shared look at the services (not from the history), so it costs the Monitoring tab's one asking every 10 seconds
whatever the number of readers, and does not depend on `monitor.enabled`.

The token is write-only, in **Settings → Monitoring → Prometheus metrics** (a card under the monitoring settings; owner and admin see whether it is
on, only the owner changes it):

| Request | Permission | Answer |
|---|---|---|
| `GET /api/v1/metrics-token` | `settings.write` | `{"enabled": true|false}`, never the token |
| `POST /api/v1/metrics-token/generate` | `system.write` (owner) | `{"enabled": true, "token": "bm_..."}`: 256 random bits, **the only time it is shown**; it replaces any token |
| `PUT /api/v1/metrics-token` `{"token": "..."}` | `system.write` | a token of the owner's own: 24 to 128 characters of letters, digits and `. _ ~ + / = -` (`400` with `field: "token"` otherwise); `{"enabled": true}` |
| `DELETE /api/v1/metrics-token` | `system.write` | `{"enabled": false}`: `/metrics` is `404` again |

Core stores the SHA-256 of the token in the setting `monitor.metrics_token` (hex), never the token. The key is not one of the Settings page's
settings, `PUT /api/v1/settings` refuses it, the bot's `/set` cannot name it and core's gRPC `GetSettings` leaves it out for the bot. The audit
log has `metrics.token.generate`, `metrics.token.set` and `metrics.token.clear` with `{"enabled": ...}` and no value. Making a token again, or
removing it, ends the old one at once.

### The public status page

`GET /status` (a page) and `GET /status.json` (JSON) on the store's own domain, for customers and for a monitoring service
([runbooks/monitoring.md](runbooks/monitoring.md#uptime-and-the-status-page)). They are **off by default**: the setting `status_page.enabled`
(Settings → Monitoring → Public status page; `settings.write`, owner and admin) turns them on, and while it is off both answer `404`, the page with
the same bytes as every address that is not there. No session, no cookie.

```json
{"status": "ok", "updated_at": 1793000000, "components": [{"id": "dashboard", "name": "Dashboard", "status": "ok"}, {"id": "bot", "name": "Bot", "status": "ok"},
  {"id": "payments", "name": "Payments", "status": "ok"}, {"id": "panels", "name": "Panels", "status": "ok"}]}
```

- `status` is `ok` when every component is `ok`, else `degraded`; the page says "All systems working" or "Some systems are down". A
  component is `ok`, `degraded` (the service answers but is not ready) or `down`. The components are `dashboard` (core), `bot`, `payments` and
  `panels` (the panel servers that are switched on and were checked, with the provisioner that reaches them: `ok` when all answer, `degraded`
  when some do, `down` when none does or the provisioner is down). A component core cannot say anything about is left out, not guessed;
  `dashboard` is always there.
- **Nothing else is in it**: no server name, address or id, no version, no error text, no count, no customer, no uptime figure. The names are
  the four general ones above (`?l=en` gives the page in English; Persian is the default).
- **Cost.** One look at the stack serves everyone for 30 seconds (the answer for each language is kept, `Cache-Control: public,
  max-age=30`; the page reloads itself every minute), through the monitor's shared look; one client address may make 60 requests a minute
  and gets `429` with `Retry-After: 60` after that. It does not depend on `monitor.enabled`.
- **The page** carries the store's name, logo and colour, a verdict with an icon and each part with an icon and a word (colour is never the only
  sign), the time of the look in the store's time zone, the support contact, no script, `noindex`, and a CSP that allows only its own styles
  and logo. `/status.json` has `Access-Control-Allow-Origin: *` and `X-Content-Type-Options: nosniff`.
- Caddy routes `/status` and `/status.json` to core ([the gateway's Caddyfile](../deploy/caddy/Caddyfile)).

### Storage and retention

Core's own database (`core.monitor_metric`, `core.monitor_series`, migration 00030, [01-database.md](01-database.md)): 30-second samples
for 7 days, then 5-minute averages for 23 more days (30 days in all), rolled up and pruned by core itself, so a restart or a long
outage loses nothing and writes a slot twice at most once. About 60 metrics make a few megabytes. The backup carries the history.

## Non-goals for v1
No public marketing site, no end-user web portal (users stay in Telegram), no mobile app.

## Open points
- DECIDED (2026-10-07): staff switch between fa (default, RTL) and en.
- Data export and GDPR-style deletion for a user (delete/anonymize but keep ledger integrity).
