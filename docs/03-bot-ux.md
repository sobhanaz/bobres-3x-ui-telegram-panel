# 03 - Bot UX flows and menu map

All texts live in i18n files (fa + en), overridable per install from the dashboard. Persian is RTL:
avoid mixing raw English inside RTL lines; use consistent numerals; test long-line wrapping.

## Principles
- One primary action per screen; max 2 taps to the common goals.
- Inline keyboards edit the same message (no chat spam). Always a Back and a Home button.
- A bottom menu (a persistent reply keyboard) holds the main actions: Buy, My services, Wallet, Free trial,
  Support, then Invite friends (only when referrals are on; resellers get the Reseller panel instead) beside
  Language; owners and admins also get Admin panel and Dashboard, support staff only Dashboard. A tap does what
  the same inline button does. It comes with the welcome, after a language change and with `/menu`, and again
  ahead of the next answer whenever it changed (role, language, an edited label). A tap is never taken as the
  answer to an open question (a bank reference, a service name), and the gates treat it as any message.
- Buttons are colored by what they do, the same on every keyboard: green pays or confirms, blue opens a main
  action, red cancels or refuses (Telegram has these three colors). Link buttons too: the payment link is
  green, sharing an invite link and joining the channel are blue. Navigation, lists and links to read (terms,
  a transaction, the dashboard) keep the app's look.
- Every error tells what happened and what to do next. No raw exceptions.
- Language chosen on first /start, changeable in Settings.
- Rate limit per user; ignore duplicate taps (callback idempotency).

## Main menu (user)
1. Buy service
2. My services
3. Wallet (balance, top up, history)
4. Free trial (once per user, abuse-checked)
5. Invite friends (referral link, earnings)
6. Support (tickets, FAQ)
7. Settings (language, notifications)
(+ Reseller panel button when role = reseller; + Admin button when role = admin)

## Key flows
**Buy:** plan list -> plan detail (price, GB, days) -> discount code (optional) -> payment method
(wallet / card / Zarinpal / Stars / crypto) -> confirm -> pay -> delivery message (subscription link, QR, connect guide per platform).
**Automatic USDT (Phase 6a):** the payment screen shows the exact amount to send (copyable, Latin digits), the store's
TRON address, the deadline (45 minutes) and a Check button. The payment settles by itself once the transfer is seen on
the chain. A different amount is an unmatched deposit: staff are told and credit it by hand.
**Renew / add traffic:** My services -> service -> Renew | Add traffic -> pay -> updated expiry shown.
**Top up wallet:** amount presets + custom -> method -> pay -> balance updated message.
**Manual payment:** show card / Zarinpal link / address + amount + unique reference -> user sends a screenshot of the payment (photo or image file; card transfers also need the bank reference number, crypto may send the TXID instead) -> every owner gets an alert whose Review button opens that payment (a staff member's card: the receipt and approve/reject) -> result to user.
**Trial:** one tap -> service created (small quota/time) -> delivery message.
**Service detail:** status, expiry, used/total (progress bar), online?, subscription link, QR, reset link, renew. An "Open page" button (a URL button, `?l=<language>`) opens the branded page `/sub/<token>` while core has a public URL and the page is on ([15-subscription-page.md](15-subscription-page.md)).
**Mini App store:** the same purchase, renewal, top-up and services list in a web app inside Telegram, on by default (setting `miniapp.enabled`) and opened from a Store button on the home screen, a Store item at the top of the bottom menu (it answers with the button that opens the app) and Telegram's menu button; they appear only when the store has a public HTTPS address. It pays by the wallet, Zarinpal, IDPay, NextPay, Telegram Stars or automatic USDT, takes card-to-card, manual USDT and Zarinpal-link payments with a receipt image (sent to the customer's chat with the bot, then reviewed as in the bot), and has the free trial, invites and support ([17-mini-app.md](17-mini-app.md)).
**Support:** new ticket (category, text, optional photo) -> status list -> reply thread -> close.
Built (2026-10-10): one open conversation per customer, numbered; text, photos and PDFs; staff replies arrive in the
chat with [Reply] and [Close]; a Telegram reply to them continues the ticket; past tickets can be read.
**Referral:** personal link, count, reward rules, balance credited on first paid order of the referred.

## Admin in bot (every task is a button; the dashboard stays the full tool)
`/admin` is the front door for owners and admins (it says when maintenance mode is on). Nobody has to remember a
command: the old ones still work, and each usage text ends with "open /admin and use the buttons".

**Menu:** payments to review (count) | today and find a user | plans and discounts | notifications and broadcast |
settings and servers | owner only: payment methods and staff | dashboard link | home.
- **Payments to review:** a list, oldest first, eight to a page: each payment is a button (who paid, how much, when; a
  warning sign when the bank reference was seen before) that opens its card with the receipt or TXID and Approve / Reject.
  Every owner and admin sees the same list and the same alerts; a payment one of them decides leaves everyone's list, and
  an alert tapped later says who decided it and when ("Already approved by @name"). A payment is settled once, whoever
  taps first (core's review is atomic). The dashboard's receipt queue is the same queue.
- **Today:** orders, revenue and refunds per currency for today and the last 7 days, new users, the servers' health.
- **Plans:** list, one plan's card (price, volume, days, devices, on or off, edit each field, archive), add, the trial
  (set or switch off) and the top-up packs (add, switch off).
- **Discounts:** list, add, switch off.
- **Settings:** groups, then every setting the role may change, shown with what it does and its current value.
  The editor depends on the kind: on and off and the choices are buttons, a number or text is typed, `-` clears it.
  Core still validates every value and its reason is shown (`Not accepted: ...`). Payment details, staff and the
  licence are the owner's, like the dashboard permissions (gateways, staff, licence, system).
- **Servers** (owner and admin, as on the dashboard): health, switch on or off, weight. The address and token are never
  shown or asked here.
- **Staff** (owner only): list, add by Telegram id (the person must have started the bot) with a role, remove.
- **Broadcast:** segment, text in Persian and in English, a preview with the audience size, then send.

**Input steps** (plan, top-up, trial, discount, setting, server weight, staff, broadcast) share one engine: each step
says "Step n of m", what is wanted, the allowed range and an example; a refused answer is explained above the same
prompt; Back and Cancel are on every step; a last screen shows everything to check before it is saved. Cancel leaves
nothing behind. Callbacks are `adm:<verb>:<arg>`, at most 64 bytes (tested).

**Roles:** the bot hides what a role may not do and answers politely when a button is tapped by hand; core enforces
the role on every call, so a crafted callback cannot do more than the dashboard allows.

`/set` with no arguments opens the settings screen. `/dashboard` and a menu button send any staff member, support
included, a one-time dashboard login link.

## Store rules the bot applies (dashboard Settings, 2026-10-10)
- **Maintenance mode:** customers get the maintenance text (a popup for taps, the message at most once a minute).
  Staff, the configured owner, `/paysupport`, payment checks and receipts of payments under way still work.
- **Required channel:** customers join it before using the bot (a join button and an "I've joined" check). Members
  are remembered for 10 minutes; if Telegram cannot answer, users are let in. Staff and a newcomer's `/start` and
  language choice are never blocked. The Mini App holds the same rule, with the same exemptions and the same choice
  when Telegram cannot answer ([17-mini-app.md](17-mini-app.md), "The required channel").
- **Limits:** taps and messages per 10 seconds, support messages per day.
- **Staff alerts:** new payments to review, new tickets, failed deliveries and (optionally) every sale, to every owner the
  server names (`BOBRES_ADMIN_TELEGRAM_ID` lists one or several), or to every owner and admin. "Payment taken but not recorded" is always sent. The **daily sales summary** (off until the
  store turns it on, `reports.daily_summary`) goes once a day to every owner and admin, whatever `notify.recipients` says,
  because it holds revenue (14-reports.md).
- `/terms`, and terms and privacy link buttons on the support screen, when the links are set.
- Dates use the store's time zone; prices use the store's name for the Toman.
- **A refused purchase (the license's limit):** when the install is at its limit of active services (the free tier's 25,
  a license's, 05-license.md), a customer's new order, free trial or reseller purchase is refused with a popup:
  "New purchases are temporarily unavailable. Please contact support." (`error.purchases_paused`). A customer never
  sees a word about licensing (the same text is shown when a reseller's purchase is refused because resellers are no
  longer included). Renewals, traffic top-ups and payments of orders already made go
  through. The staff get one alert a day, "A new purchase was refused: this install is at its limit of ... active
  services. Buy or renew a license to sell more." (`alert.license_limit`).

## Reseller panel
Built (milestone 5, 2026-10-11; managing their customers' services and the customers who joined through their link,
2026-10-20). Resellers work in the bot and in the Mini App's reseller section ([17-mini-app.md](17-mini-app.md)),
never in the dashboard; the owner and admins make them, set their tier, credit limits and commission, and record their
payments in the dashboard. Customers see none of this.
- **Home:** a "Reseller panel" button instead of "Invite friends" (their link is in the panel).
- **Panel:** tier, discount and commission %; per currency the balance (a negative balance is shown as "debt X",
  never with a minus), credit limit and what they can spend (balance + limit); customers who joined through their
  link and commission earned. Buttons: Buy for a customer, Customer services, Customers, Statement, My link, Home.
- **Buy for a customer:** the plans at their prices, then "type a name for this service, like Ali's phone, or tap
  Skip" (one line, at most 64 characters), then the regular checkout. Their regular Buy, renewals and traffic
  packages show their prices too.
- **Checkout for resellers:** no discount-code button; the wallet button shows only when the balance plus the credit
  limit covers the price, with the amount they can spend; other payment methods stay. Past the limit they see what
  they can still spend, the other methods and a Panel button.
- **Customer services** (also their "My services"): 8 a page with Previous/Next, each the name or #short id, status
  and expiry; a search by part of the name or the #number, paged with the search. Under the list the services are
  grouped, each group a button with its count: All, Active, Ending soon, Ended (the time or the traffic ran out) and
  Paused. The group shown is ticked; a search started inside a group stays inside it and its pages keep both. Every
  service is in exactly one group, so the counts add up, and the counts do not follow the group or the search. A
  service opens the usual page, with a Rename button (a new name, or Remove the name).
- **A customer service's page** (the reseller's own services only): beside Renew, Add traffic, QR code and Rename:
  - **Pause / Resume:** turns the service off or on in the panel (the customer's link stops working while it is
    paused; a service paused shows "Paused"). Doing it twice changes nothing. A service that **support** paused cannot
    be resumed by the reseller: the popup says to contact support. A service being delivered cannot be paused.
  - **Note:** a private note, one line of at most 200 characters, shown on the page under the service; only the
    reseller sees it (not the customer, not the public page). Send a new text to replace it, or Remove the note.
  - **Delete:** a confirmation screen first ("removed from the server for good, the customer's link stops working at
    once, this cannot be undone, nothing is refunded"), then Yes, delete it or No, keep it. A service being delivered,
    or with a renewal still being applied, cannot be deleted yet. Money never moves.
  A reseller cannot reset a service's traffic or give free days or free traffic: those stay with staff. Pausing and
  deleting are in the audit log under the reseller's name, source `bot`.
- **Customers** (the panel's Customers button): the people who joined through the reseller's invite link, newest
  first, 10 a page. Each line: their @username (or their Telegram id when they have none), the day they joined, how
  many paid orders they made (refunded and free ones do not count), what they spent per currency, and the commission
  those purchases paid the reseller. Nobody who did not join through this reseller's link is shown, and nothing else
  about them.
- **When resellers are no longer in the license** the reseller keeps their account and their services: the plain list
  of services still opens (without the groups) and each service shows its link, and renewals still work. Pause,
  Resume, Delete, Note and Customers answer "This is not available right now. Please contact support." (never a word
  about licensing) and change nothing ([05-license.md](05-license.md)).
- **Names in messages:** the service page, the delivery message, renewals and top-ups, and reminders start with the
  service's name when it has one.
- **Statement** (also their wallet History): 10 transactions a page, commissions and payments to the store included.
- **My link:** their invite link and commission %, even when the store's referral reward is 0.
- **Notifications:** a commission ("from a customer's purchase", never naming the customer) and a payment the staff
  recorded, each with the balance after.
- Resellers are gated like customers (maintenance, required channel); a banned reseller loses the panel.

## Notifications
Expiry (3d/1d/expired), traffic (80%/100%), payment result, ticket reply, wallet change (resellers: commission and
recorded payments), panel/server alerts (admin only).
All user-configurable except billing-critical ones.

## Copy and states to write (see 08-brand)
Welcome, help, each menu, each flow confirmation, each error (payment failed, expired, no stock, banned, rate limited,
panel down), empty states, delivery message, connect guides (Android/iOS/Windows/macOS), FAQ.

## Open points
- DECIDED: forced channel join is an optional setting (the required channel, above).
- Anti-abuse for trial: one per Telegram ID is built. Telegram account age heuristics and an optional phone check
  are not.
- DECIDED: long polling by default (no domain needed for the bot alone). Webhook mode is optional:
  `BOBRES_TELEGRAM_WEBHOOK_URL` with `BOBRES_TELEGRAM_WEBHOOK_SECRET`, delivered to `/tg/webhook`.
