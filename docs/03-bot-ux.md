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
**Manual payment:** show card / Zarinpal link / address + amount + unique reference -> user sends a screenshot of the payment (photo or image file; card transfers also need the bank reference number, crypto may send the TXID instead) -> admin approve/reject buttons in admin chat -> result to user.
**Trial:** one tap -> service created (small quota/time) -> delivery message.
**Service detail:** status, expiry, used/total (progress bar), online?, subscription link, QR, reset link, renew.
**Support:** new ticket (category, text, optional photo) -> status list -> reply thread -> close.
Built (2026-10-10): one open conversation per customer, numbered; text, photos and PDFs; staff replies arrive in the
chat with [Reply] and [Close]; a Telegram reply to them continues the ticket; past tickets can be read.
**Referral:** personal link, count, reward rules, balance credited on first paid order of the referred.

## Admin in bot (quick actions, full power is in dashboard)
Stats snapshot, find user, add balance, ban/unban, approve receipts, broadcast (queued), toggle gateway, panel health.
Built: `/admin` for owners and admins (it says when maintenance mode is on): stats (users, active services, payments
to review, failed deliveries, the servers' health), the payments to review with approve and reject, find a user
(adjust a balance, ban or unban), plans and the trial, and settings. `/set key value` (it shows why core refused a
value; payment details are the owner's). `/plan_add`, `/trial`, `/topup_add`, `/discount_add`, `/discounts`,
`/discount_off`. `/dashboard` plus a menu button send any staff member, support included, a one-time dashboard login
link. Broadcasts and the payment methods' switches are on the dashboard.

## Store rules the bot applies (dashboard Settings, 2026-10-10)
- **Maintenance mode:** customers get the maintenance text (a popup for taps, the message at most once a minute).
  Staff, the configured owner, `/paysupport`, payment checks and receipts of payments under way still work.
- **Required channel:** customers join it before using the bot (a join button and an "I've joined" check). Members
  are remembered for 10 minutes; if Telegram cannot answer, users are let in. Staff and a newcomer's `/start` and
  language choice are never blocked.
- **Limits:** taps and messages per 10 seconds, support messages per day.
- **Staff alerts:** new payments to review, new tickets, failed deliveries and (optionally) every sale, to the owner
  or to every owner and admin. "Payment taken but not recorded" is always sent. The **daily sales summary** (off until the
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
Built (milestone 5, 2026-10-11). Resellers work only in the bot; the owner and admins make them, set their tier,
credit limits and commission, and record their payments in the dashboard. Customers see none of this.
- **Home:** a "Reseller panel" button instead of "Invite friends" (their link is in the panel).
- **Panel:** tier, discount and commission %; per currency the balance (a negative balance is shown as "debt X",
  never with a minus), credit limit and what they can spend (balance + limit); customers who joined through their
  link and commission earned. Buttons: Buy for a customer, Customer services, Statement, My link, Home.
- **Buy for a customer:** the plans at their prices, then "type a name for this service, like Ali's phone, or tap
  Skip" (one line, at most 64 characters), then the regular checkout. Their regular Buy, renewals and traffic
  packages show their prices too.
- **Checkout for resellers:** no discount-code button; the wallet button shows only when the balance plus the credit
  limit covers the price, with the amount they can spend; other payment methods stay. Past the limit they see what
  they can still spend, the other methods and a Panel button.
- **Customer services** (also their "My services"): 8 a page with Previous/Next, each the name or #short id, status
  and expiry; a search by part of the name or the #number, paged with the search. A service opens the usual page,
  with a Rename button (a new name, or Remove the name).
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
