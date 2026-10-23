# Branded subscription page

Each service has a page of its own under the store's address: `https://<store>/sub/<token>`. It is for the customer
to look at, not for a VPN app to fetch. It shows how much traffic and time are left, the subscription link with a QR
code, one button per VPN app, and a "Renew" button that opens the bot.

## What the page is (and is not)

- **For people only.** It shows the `sub_link` core already stores for the service. It does not proxy the config and it
  does not stand in for the panel's own subscription URL: apps keep using the panel link.
- **No script.** The page is one server-rendered HTML document with inline CSS, no JavaScript and no outside
  requests, so it loads on a poor connection. The QR code is a PNG inside the page (a `data:` URI).
- **Language** comes from `?l=fa` or `?l=en` and defaults to Persian. The bot adds it to the link it sends, in the
  customer's language. The layout flips right to left for Persian.
- **What it shows:** the store's name, colour and logo (Branding), the service's status, days left, a traffic bar,
  the link (read-only box and QR), the four app buttons and their guides, support contact and terms link when they
  are set, and "Powered by BOBRES" while the licence says so (the same rule as the rest of the product).
- **What it never shows:** the client e-mail, a reseller's label for the service, the customer's Telegram id or
  username, the service id, the panel's sub id, or the server's address except inside the link the customer already has.

## The address

`page_token` is 32 lowercase hex characters (128 bits from the system random generator), unique per service, stored
in `core.subscriptions.page_token` (migration core 00021; services that existed get one at the upgrade). It is not
derived from anything else, so knowing a service id, a client e-mail or a panel sub id tells nobody the page address.

The page answers the **same 404** (same body and headers) for a token that is malformed, unknown, pending or deleted,
and for every token while the page is switched off. A request that is not a GET is a 404 too. A token of the wrong
shape is refused before any query.

An operator can give a service a new address (the old one stops at once) with **Regenerate page address** on the
service's page in the dashboard. It needs `services.write` (owner and admin; support may not), a reason, and it is
audited as `subscription.page_regenerate` with the reason and without either token. The sub link itself is not
changed. The bot shows an "Open page" button on the service page and sends the new address from then on.

## Where the address shows up

- The dashboard's service page (`page_url` in `GET /api/v1/services/{id}`), only while the page is on. The services
  list does not carry it.
- The bot's service page: an "🌐 Open page" URL button, with `?l=<language>`. Core fills `page_url` in the gRPC
  `Subscription` (field 14) while the store has a public URL (the HTTPS manager's) and the page is on, and not for
  pending or deleted services.
- The "service ready" message has an Open page button too, with the page address from `GetSubscriptionLinks`
  (`page_url`, field 4). If core cannot say, the message goes out without the button.

## Settings (Branding)

| Key | Meaning |
|---|---|
| `branding.sub_page.enabled` | The page is on unless this is `false`. Off: every address is a 404 and no `page_url` is given. |
| `branding.bot_username` | The bot's username (`@my_vpn_bot` or `my_vpn_bot`). The "Renew" button opens `https://t.me/<username>`; with no username the button is not shown. |

The name, colour, logo, support and terms link are the existing Branding settings. The guide under each app is an
editable bot text: `subpage.guide.v2rayng`, `.hiddify`, `.streisand`, `.v2box`, `.karing` and `.npv`, in both languages (Branding page,
Texts; plain text only, it is escaped on the page).

## Limits and headers

- 30 requests a minute per client address (HTTP 429 with `Retry-After`); a wrong token counts like a right one.
- `Cache-Control: no-store, private`, `X-Robots-Tag: noindex, nofollow`, `Referrer-Policy: no-referrer`,
  `X-Content-Type-Options: nosniff`, and a CSP that allows only inline style and the page's own images
  (`default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`).
- A server's **subscription base URL** (Servers page) may not point at core's own address under `/sub/`: core's `/sub/`
  is this page, and a panel link there would be shadowed. The dashboard refuses it with a field error on `sub_base_url`.

## App buttons: not verified on devices

Each button is a deep link that asks the app to import the subscription:

| App | Link |
|---|---|
| v2rayNG | `v2rayng://install-config?url=<encoded link>` |
| Hiddify | `hiddify://import/<link>` |
| Streisand | `streisand://import/<link>` |
| V2Box | `v2box://install-sub?url=<encoded link>` |
| Karing | `karing://install-config?url=<encoded link>` |
| NPV Tunnel | none: it documents no import link, so its entry is the steps only (copy the link above, add it as a subscription in the app) |

**These are the schemes commonly used for each app, written from memory of their documentation. They have not been tried on real devices**, and apps change
them between versions. Try each on a phone before you rely on it; the link and the QR code are always there to copy or
scan by hand. If an app changes its scheme, the fix is one line in `internal/core/web/subpage.go`.

## Tests

`internal/core/web/subpage_test.go` (both languages, escaping of a hostile brand name or link, no script, no private
data, identical 404s, headers, rate limit, every state, editable guides, regenerate and its permission and audit, the
`page_url` in the API, the base URL collision), `internal/migrate/core_sub_page_test.go` (backfill, default, format,
uniqueness, Down and Up again), `internal/core/server/subpage_test.go` (`page_url` over gRPC), `internal/bot/handler/subpage_test.go`
(the button) and the store and domain unit tests.
