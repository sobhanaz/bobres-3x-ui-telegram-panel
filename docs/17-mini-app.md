# Telegram Mini App store

A web store that opens inside Telegram: browse plans, buy with the wallet or an online gateway, see and renew your
services, top up the wallet. It is the same store as the bot (same plans, prices, discount codes, wallet and
orders), in a nicer screen. The bot stays the way to do everything else (trial, referrals, support, manual card and
Stars payments).

It is **off by default**. Turn it on with the setting `miniapp.enabled` (Settings, General), then point the bot's menu
button at the app (below).

## Set up

1. Core must have a public HTTPS address (the store's domain). The app lives at `https://<domain>/app/`.
2. In the dashboard, Settings, General: **Mini App store** on. Off, `/app/` and every `/api/mini/v1/` call answer 404.
3. In Telegram, talk to @BotFather: `/mybots`, your bot, **Bot Settings**, **Menu Button**, **Configure menu button**,
   send `https://<domain>/app/`, and a button title (for example "Store"). Customers then see the button next to the
   message box. A Web App button on a keyboard or an inline button with the same URL works too.
4. Open the bot on a phone, tap the button, and check it signs in (the checklist at the end of this page).

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

The app offers **Zarinpal, IDPay, NextPay and automatic USDT**. Telegram Stars and manual card or receipt payments
are refused with a 400 here and are done in the bot. Trials start in the bot too.

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

- Trials, referrals, support tickets and the reseller panel (still in the bot).
- Stars and manual (card, receipt) payments.
- Push messages (the bot sends those).

## Check by hand with a real Telegram (nothing here can prove it)

1. The init data signature with the real bot token: open the app from the menu button on a phone and a desktop client;
   it must show the plans, not "open from Telegram".
2. The BotFather menu button and Web App URL from the steps above, with `miniapp.enabled` on and off.
3. The CSP and framing in Telegram's web clients (web.telegram.org) and the mobile apps; nothing blocked in the console.
4. Theme colours, dark and light, safe-area (notch) and Telegram's Back button inside a purchase.
5. A payment page opening from the app (Zarinpal, IDPay, NextPay) and coming back; the automatic USDT address, exact
   amount and countdown.
6. A purchase, a renewal and a wallet top-up end to end, and the same customer seen in the bot and the dashboard.
7. Persian right to left layout and digits, and English.
