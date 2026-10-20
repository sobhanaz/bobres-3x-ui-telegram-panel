# Telegram Mini App store

A web store that opens inside Telegram: browse plans, buy with the wallet or an online gateway, see and renew your
services, top up the wallet, take the free trial, invite friends and write to support. It is the same store as the bot
(same plans, prices, discount codes, wallet, orders and support inbox), in a nicer screen. Manual card and Stars
payments and the reseller panel are still done in the bot (see "Not built").

It is **on by default**, for every store, with nothing to set up in @BotFather: the bot opens it from a **Store** button
at the top of its bottom menu and from Telegram's own **menu button** next to the message box. Switch it off with the
setting `miniapp.enabled` (Settings, General) and both buttons go within a minute and `/app/` answers 404.

## Set up

1. The store needs a public HTTPS address (the dashboard's domain): the app lives at `https://<domain>/app/`. Nothing
   else is required. With an IP address, `localhost`, a private or `.local` name or plain http, Telegram could not open
   the page, so the bot adds no button (the app itself is still served).
2. Core tells the bot that address with its settings, and the bot (which reloads them every minute and at once on a
   change in the dashboard) sets the menu button, titled "Store" / "فروشگاه", and puts a Store button first in its
   bottom menu. The menu button is set for all chats, once (and again when the address or the title changes),
   replacing a button set by hand in @BotFather before; Telegram's default button is given back when the app is
   switched off or the address is lost, but only if the bot had set the button. A Web App button on another keyboard or
   an inline button with the same URL works too.
3. Open the bot on a phone, tap Store, and check it signs in (the checklist at the end of this page).

The bot's username (Branding, `branding.bot_username`) is used for the "Open the bot" button the app shows where it
cannot finish something itself.

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
| `GET /me`, `POST /logout` | The customer, wallets, branding, maintenance flag, top-up bounds; end the session |
| `GET /plans` | Plans on sale (enabled, not a trial) with the customer's price (reseller prices apply), `topup` marks traffic packages |
| `GET /services` | The customer's own services: label, status, expiry, traffic, link, page address, `can_renew`, `can_topup` |
| `GET /wallet` | Balances and the last 20 ledger entries |
| `GET /methods?currency=&amount=` | Online methods usable for that amount |
| `POST /quote` | Price with a discount code, without making an order |
| `POST /orders` | New order (`new`, `renew`, `traffic_topup`) with a request `key`; the same key returns the same order |
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
same: price and discount are decided on the server, a wallet cannot go below zero, an order is the customer's own or
a 404. Changes are audited with source `bot` (the audit log's allowed sources do not include a Mini App one, and the
store is the bot's second face), under the customer's id.

While the store is in **maintenance**, the app can still sign in and read `/me`, an order and a payment check (so a
payment in flight finishes), but browsing, buying and top-up answer 503 `maintenance`.

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

- The reseller panel (still in the bot).
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

## Check by hand with a real Telegram (nothing here can prove it)

1. The init data signature with the real bot token: open the app from the menu button on a phone and a desktop client;
   it must show the plans, not "open from Telegram".
2. The Store button in the bot's bottom menu and Telegram's menu button, a minute after an update or a change of
   `miniapp.enabled`: both appear on, both go off, and a store with only an IP address gets neither.
3. The CSP and framing in Telegram's web clients (web.telegram.org) and the mobile apps; nothing blocked in the console.
4. Theme colours, dark and light, safe-area (notch) and Telegram's Back button inside a purchase.
5. A payment page opening from the app (Zarinpal, IDPay, NextPay) and coming back; the automatic USDT address, exact
   amount and countdown.
6. A purchase, a renewal and a wallet top-up end to end, and the same customer seen in the bot and the dashboard.
7. Persian right to left layout and digits, and English.
