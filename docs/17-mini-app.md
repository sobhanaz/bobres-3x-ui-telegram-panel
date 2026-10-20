# Telegram Mini App store

A web store that opens inside Telegram: browse plans, buy with the wallet or an online gateway, see and renew your
services, top up the wallet, take the free trial, invite friends, write to support and, for a reseller, manage their
customers' services. It is the same store as the bot (same plans, prices, discount codes, wallet, orders and support
inbox), in a nicer screen. Manual card and Stars payments are still done in the bot (see "Not built").

It is **on by default**, for every store, with nothing to set up in @BotFather: the bot opens it from a **Store** button
on its home screen, a **Store** item in its bottom menu and Telegram's own **menu button** next to the message box.
Switch it off with the setting `miniapp.enabled` (Settings, General) and the buttons go within a minute and `/app/`
answers 404.

## Set up

1. The store needs a public HTTPS address (the dashboard's domain): the app lives at `https://<domain>/app/`. Nothing
   else is required. With an IP address, `localhost`, a private or `.local` name or plain http, Telegram could not open
   the page, so the bot adds no button (the app itself is still served).
2. Core tells the bot that address with its settings, and the bot (which reloads them every minute and at once on a
   change in the dashboard) sets the menu button, titled "Store" / "فروشگاه", puts a Store button first on its home
   screen and a Store item first in its bottom menu. The menu button is set for all chats, once (and again when the
   address or the title changes), replacing a button set by hand in @BotFather before; Telegram's default button is
   given back when the app is switched off or the address is lost, but only if the bot had set the button.
   An inline Web App button with the same URL works anywhere else too.

   **Why the bottom menu holds a text item and not the app button itself:** Telegram gives a Mini App opened from a
   *reply keyboard* button no sign-in data (`WebAppInitData` is empty; such a Mini App can only `sendData` back), so
   core could not tell who the customer is. Inline buttons and the menu button do give it. A tap on the Store item
   therefore sends its label as a message, and the bot answers with a message holding the inline button that opens
   the app. (The home screen's button opens it at once.)
3. Open the bot on a phone, tap Store, and check it signs in (the checklist at the end of this page).

The bot's username is used for the "Open the bot" button the app shows where it cannot finish something itself, and for
the invite link. The bot tells core its own username (from `getMe`) with every sign-in check, so nothing has to be typed;
`branding.bot_username` in Branding, when set, wins.

## How a customer is recognised

Telegram opens the page with `initData`, a signed string that names the user. Only the **bot** holds the bot token that
checks the signature, and core never has it. So core asks the bot:

```
Mini App   --initData-->  core  --POST /internal/miniapp/verify (Bearer core token)-->  bot
                                 <-- {"ok":true,"user":{...},"auth_date":...} or {"ok":false,"problem":"malformed|signature|expired"}
```

- The bot validates the HMAC-SHA256 signature (`internal/tgauth`), and refuses init data older than **1 hour** or from
  the future. The same call is the only place the token is used; it is never sent to core.
- Core then looks the customer up by Telegram id (creating the account if they have never used the bot, keeping the
  language the bot chose when there is one) and hands the app a **session token**.
- The init data is never logged, audited or stored. The session token is kept as a SHA-256 hash in core's memory.
  Sessions last 6 hours and end after 1 hour without a call; a restart of core ends them all. The app then signs in
  again with the same init data, which works while it is under an hour old; after that the customer reopens the app.
- A banned customer gets 403 and nothing else. A user that is gone ends the session.

## The API (`/api/mini/v1`)

JSON only, for the app. Errors are `{"error": "<code>", "message": "..."}` with codes such as `unauthorized`,
`rate_limited`, `banned`, `maintenance`, `insufficient_funds`, `plan_unavailable`, `not_extendable`, `not_payable`,
`state`, `invalid`, `unavailable` (a service behind core is busy), `disabled` (the setting is off).

| Call | What it does |
|---|---|
| `POST /auth` `{init_data}` | Verify, start a session. Returns `me` and `token`, `expires_in`. Public, 20 a minute per client address |
| `GET /me`, `POST /logout` | The customer, wallets (a reseller's carry their `credit_limit`), branding, maintenance flag, top-up bounds, `features` (what the sections show, for example `reseller`); end the session |
| `GET /plans` | Plans on sale (enabled, not a trial) with the customer's price (reseller prices apply), `topup` marks traffic packages |
| `GET /services` | The customer's own services: label, status, expiry, traffic, link, page address, `can_renew`, `can_topup` |
| `GET /wallet` | Balances and the last 20 ledger entries |
| `GET /methods?currency=&amount=` | Online methods usable for that amount |
| `POST /quote` | Price with a discount code, without making an order |
| `POST /orders` | New order (`new`, `renew`, `traffic_topup`) with a request `key`; the same key returns the same order. A reseller may add a `label` (the name of the new service they buy for a customer, at most 64 characters, one line); core ignores it for anyone else and on a renewal or top-up |
| `POST /orders/{id}/pay-wallet` | Pay from the wallet |
| `POST /orders/{id}/pay` `{method}` | Start an online payment; returns a pay URL, or the USDT address and exact amount |
| `GET /orders/{id}` | Order status (poll it until delivered) |
| `POST /topup` `{amount, method, key}` | Wallet top-up, 10,000 to 50,000,000 Toman, same bounds as the bot |
| `POST /payments/{id}/check` | Ask the gateway whether a payment landed ("I have paid") |
| `GET /trial`, `POST /trial` | The free trial's plan and whether the customer can still take it; take it (one per account, shared with the bot). The order is polled like a purchase's |
| `GET /invite` | The customer's invite link, how many joined, how many bought, what it earned. Offered when `referral.reward_percent` is above 0, the bot's username is known and the customer is not a reseller |
| `GET /support`, `GET /support/tickets/{id}`, `POST /support/messages` `{text, category}`, `POST /support/tickets/{id}/close` | The customer's conversations with staff: the open one and the recent ones, one thread (staff notes are never shown), a message (joins the open conversation or starts one), closing |

The app offers **Zarinpal, IDPay, NextPay and automatic USDT**. Telegram Stars and manual card or receipt payments
are refused with a 400 here and are done in the bot.

### Account sections

These three are sections of the More tab (see "Adding a section"); each says in `features` whether this customer sees it.

- **Free trial** (`features.trial`): shown while the store has an enabled trial plan and the customer has never ordered a
  trial. It is the bot's trial: the same domain call and the same request key (`trial:<telegram id>`), so a trial taken
  in either place is used in both, and a second tap or a second device gets 409 `trial_used`.
- **Invite friends** (`features.invite`): the link is `https://t.me/<bot>?start=r_<code>`, as in the bot. A reseller has
  a commission link of their own in their panel and is not offered this one.
- **Support** (`features.support`): the bot's support, with the same rules. One conversation is open at a time, a message
  joins it or starts one, staff answers still arrive in the bot and show in the thread, and a customer's message needs
  text (photos and files are sent from the bot and show as an attachment line). `limits.tickets_per_day` is counted in
  the app from the database, so it includes the messages sent through the bot (the bot keeps a count of its own, so a
  customer who uses both can send up to the limit in each); over it, the answer is 429 `daily_limit`.

The calls use the same domain functions as the bot (orders, wallet, discount codes, payments), so every rule is the
same: price and discount are decided on the server, a wallet cannot go below zero (a reseller's may go down to minus
their credit limit), an order is the customer's own or a 404. Changes are audited with source `bot` (the audit log's
allowed sources do not include a Mini App one, and the store is the bot's second face), under the customer's id.

While the store is in **maintenance**, the app can still sign in and read `/me`, an order and a payment check (so a
payment in flight finishes), but browsing, buying and top-up answer 503 `maintenance`.

## The reseller section

A reseller (a customer the owner or an admin made one, [03-bot-ux.md](03-bot-ux.md)) gets **Reseller panel** in the
More tab, in Persian (right to left) and English like the rest of the app. It is the bot's reseller panel with the same
rules, plus managing the customers' services. Core says in `/me` (`features.reseller`) whether to show it: true only for
an **active** reseller on an install whose license includes `resellers` ([05-license.md](05-license.md)); a customer,
staff, a banned or former reseller and a reseller after `resellers` left the license do not see it.

Five tabs:

- **Panel:** tier, discount and commission, per currency the balance (a debt is written "Debt X", never with a minus),
  the credit limit and what can be spent (balance plus limit), how many services are in each group (a tap opens the list
  in that group), the customers who joined through the link and the commission earned, and the **invite link** with a
  Share button (Telegram's share sheet) and Copy.
- **Buy:** the plans at the reseller's tier price (the shop's `/plans` already answers with it), then an optional name
  for the service (one line, at most 64 characters), then the shop's own checkout (the `label` of `POST /orders`).
  The checkout has no discount-code field for a reseller (they pay tier prices, core refuses codes), and its wallet
  button counts the credit: a reseller pays from the balance and the credit, down to the limit, through the usual
  `pay-wallet`. Online methods work as for any order.
- **Services:** the customers' services, 10 a page, newest first, in groups with their counts (All, Active, Ending
  soon, Ended, Paused), a search by name or number (#a1b2c3), and per service the status, time left, usage bar, the
  private note, and (opened) the subscription link to copy, the service page, **Renew** and **Add traffic** (the shop's
  checkout, `choosing` a plan like the "My services" tab does), **Rename**, **Note**, **Pause / Resume** and **Delete**.
  Delete asks first ("removed from the server for good, the link stops working at once, nothing is refunded") and the
  call itself refuses to run without `{"confirm": true}`. Where the reseller is (group, search, page, the open service)
  is kept while they go to the checkout and come back.
- **Customers:** the people who joined through their invite link, newest first, 10 a page: @username or Telegram id,
  the day they joined, paid orders, what they spent and what it paid the reseller. Nobody else's, nothing more.
- **Statement:** their wallet movements, 10 a page, commissions and payments to the store included.

What a reseller cannot do, here or in the bot: reset a service's traffic, give free days or free traffic (staff's), or
resume a service **support** paused (the app says to contact support, code `paused_by_staff`).

| Call | What it does |
|---|---|
| `GET /reseller` | Tier, discount, commission, balances with credit and available, customers, commission earned, `ref_code`, `invite_link` (`""` while the bot's username is unknown: Branding's, else the one the bot told at a sign-in), `counts` per group |
| `GET /reseller/services?status=&q=&page=&size=` | The reseller's services (`status` is `active`, `expiring_soon`, `expired`, `paused` or `pending`; `size` at most 50, 10 by default) with `total` and `counts`; each has `label`, `note`, `status`, `expires_at`, `traffic_total`, `traffic_used`, `link`, `page_url`, `can_renew`, `can_topup`, `can_pause`, `can_resume`, `can_delete` |
| `POST /reseller/services/{id}/pause`, `.../resume` `{}` | Turn the service off or on in the panel; repeating changes nothing; returns the service |
| `POST /reseller/services/{id}/delete` `{confirm: true}` | Delete for good (no refund); repeating is fine; 400 without the confirmation |
| `POST /reseller/services/{id}/label` `{label}`, `.../note` `{note}` | Name it (at most 64 characters) or keep a private note (at most 200); empty clears; one line each |
| `GET /reseller/customers?page=&size=` | The customers who joined through the link: `telegram_id`, `username`, `joined_at`, `paid_orders`, `spent` and `commission` per currency |
| `GET /reseller/statement?page=&size=` | Wallet movements with `has_more` (no total) |

Security of the section, each point has a test:

- Every call is the signed-in reseller's own: the user comes from the session, a service id must be a UUID of one of
  their own services (another reseller's, a customer's, a deleted or an unknown one is `404`, and nothing changes).
- Core checks on every call (not only in `/me`) that the caller is an active reseller with `resellers` in the license.
  A customer, staff, a former reseller and a reseller after the license is `403 forbidden`; a banned one is `403 banned`.
  The words never mention licensing.
- No call takes a price, an amount or a user: the plan's price comes from core, the order's amount too, and a body with
  such a field is refused (unknown fields are a `400`).
- Pausing and deleting are audited as the reseller's (`subscription.disable|enable|delete`, role reseller, source `bot`).
- The shared limits apply: 120 calls and 20 changes a minute per customer, 16 KiB bodies, `503 maintenance` while the
  store is in maintenance.
- The invited-customers list holds only the users whose `referred_by` is this reseller, and only the fields above.

## Where it is served

Core serves the built app (`web/miniapp`, embedded into the binary like the dashboard) under `/app/`, only while the
setting is on. `/app` redirects to `/app/`. Without a build, a short notice is shown instead: `make miniapp` builds it
(Docker images build it in their own stage).

## Security

- **Signed, bot-verified identity.** No customer id from the browser is ever trusted: every call is bound to the
  session's own user, order and service ids must match a UUID shape, and an order or service of another customer is a
  404.
- **Telegram's script** (`https://telegram.org/js/telegram-web-app.js`) is the only outside code the page loads. If it
  cannot be loaded, the app still reads the init data from the URL fragment Telegram opens it with.
- **Headers** (`web/miniapp/embed.go`, repeated in the Caddyfile `/app` block, and a test keeps the two equal):
  `Content-Security-Policy: default-src 'self'; script-src 'self' https://telegram.org; img-src 'self' data:;
  style-src 'self' 'unsafe-inline'; frame-ancestors https://*.telegram.org; base-uri 'none'; form-action 'self'`,
  `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store` for the API. The app
  may be framed only by Telegram's own sites; the dashboard stays unframeable.
- **Limits:** 20 sign-ins a minute per client address, 120 calls and 20 changes a minute per customer, 16 KiB request
  bodies, at most 5 live sessions per customer.
- **Money:** a payment is created once per order and method (the gateway key is the bot's), so a second tap or a retry
  never charges twice; the app's order key is `mini:<user>:<key>`.
- The session token is held in memory by the page (no cookie, no local storage), so it is not sent by the browser on
  its own and a page of another site cannot make the app act.

## Not built

- For a reseller: recording what they paid the store (staff do it, in the dashboard).
- Resetting a service's traffic and free days or free traffic for a customer's service (staff only, by design).
- Stars and manual (card, receipt) payments.
- Photos and files in support (sent from the bot).
- Push messages (the bot sends those).

## Adding a section (for developers)

Beyond the shop, the app has sections listed in its **More** tab. A section touches no shared file:

- **Core:** a file `internal/core/web/miniapp_<name>.go` whose `init` calls `registerMiniFeature(miniFeature{routes, me})`.
  `routes` adds the section's calls under `/api/mini/v1/` (behind `s.miniSession`, so every call is the signed-in
  customer's own); `me` adds the section's flags to `features` in the sign-in and `/me` answers.
- **App:** a folder `web/miniapp/src/sections/<name>/` with `index.ts` (calls `registerSection({ id, title, visible,
  component })`, where `visible` reads `me.features`) and `messages.ts` (default export `{ fa, en }`, the texts in both
  languages). `sections/index.ts` loads every folder (`main.ts` imports it by that file name: `./sections` alone is
  the registry, `sections.ts`); a test fails if a text a view asks for is missing in either language, and another if
  the app's entry does not register every folder.
- A section's screen may emit `close`, and ask the app for the shop's own checkout: `buy` (`{ plan, type: 'new', label }`
  to buy a new service), `renew` or `topup` (with the service). The app shows the checkout and brings the customer
  back to the section, so a section keeps where the customer was in a module of its own (`reseller/state.ts`).

## Check by hand with a real Telegram (nothing here can prove it)

1. The init data signature with the real bot token: open the app from the menu button on a phone and a desktop client;
   it must show the plans, not "open from Telegram".
2. The Store item in the bot's bottom menu, the Store button on its home screen and Telegram's menu button, a minute
   after an update or a change of `miniapp.enabled`: all three appear on, all three go off, and a store with only an IP
   address gets none. Each must open the app signed in (the bottom menu's after one more tap, on the bot's answer).
3. The CSP and framing in Telegram's web clients (web.telegram.org) and the mobile apps; nothing blocked in the console.
4. Theme colours, dark and light, safe-area (notch) and Telegram's Back button inside a purchase.
5. A payment page opening from the app (Zarinpal, IDPay, NextPay) and coming back; the automatic USDT address, exact
   amount and countdown.
6. A purchase, a renewal and a wallet top-up end to end, and the same customer seen in the bot and the dashboard.
7. Persian right to left layout and digits, and English.
8. The reseller section with a real reseller: it shows in More only for them; the panel and the invite link's Share
   button (Telegram's share sheet) and Copy; buying for a customer with a name, paying from the credit; opening a
   service, copying its link, pausing and resuming it (the customer's VPN stops and starts), a service support paused
   stays paused, deleting it after the confirmation, the note and the new name; the customers and the statement; the
   section disappears (and its calls answer 403) when resellers leave the license, and the Telegram back button steps
   out of the checkout to the section's screen you left.
