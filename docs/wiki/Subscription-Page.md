# Subscription Page

Every service has a page of its own under your store's address: `https://shop.example.com/sub/<token>`. It is for your customer to look at, in your brand. It is not the address a VPN app fetches.

## What the page shows

- Your store's name, color and logo (from Branding).
- The service's status, days left and a traffic bar.
- The subscription link in a read-only box, with a QR code.
- One button per VPN app, with a short guide under each.
- A **Renew** button that opens your bot.
- Your support contact and terms link when they are set, and "Powered by BOBRES" while your license says so.

The language comes from `?l=fa` or `?l=en` and defaults to Persian. The bot adds it to the link it sends, in the customer's language.

## What it never shows

The client e-mail, a reseller's label for the service, the customer's Telegram ID or username, the service ID, the panel's own subscription ID, and the server's address, except inside the link the customer already has.

## How customers get to it

The bot's service page and the "service ready" message have an **Open page** button. The dashboard's Services page shows the address for staff too. The page appears only while your store has a public HTTPS address.

## The address is private

The token is 32 random lowercase hex characters (128 bits), not derived from anything else, so knowing a service ID or a panel ID tells nobody the address. Anything unknown, malformed, pending or deleted gets the same 404, and so does every address while the page is switched off.

If a customer's address leaks, open the service in the dashboard and press **Regenerate page address**. You need the `services.write` permission (owner and admin) and a reason. The old address stops at once. The subscription link itself does not change.

## Settings

| Key | Meaning |
|---|---|
| `branding.sub_page.enabled` | The page is on unless this is `false`. When off, every address is a 404 |
| `branding.bot_username` | Your bot's username, such as `@my_vpn_bot`. The Renew button opens `https://t.me/<username>`. Without it the button is not shown |

The guides under each app are editable bot texts, in both languages: `subpage.guide.v2rayng`, `.hiddify`, `.streisand`, `.v2box`, `.karing` and `.npv`. Edit them in Branding & texts. They are plain text.

## App buttons

Each button is a deep link that asks the app to import the subscription:

| App | Link form |
|---|---|
| v2rayNG | `v2rayng://install-config?url=<encoded link>` |
| Hiddify | `hiddify://import/<link>` |
| Streisand | `streisand://import/<link>` |
| V2Box | `v2box://install-sub?url=<encoded link>` |
| Karing | `karing://install-config?url=<encoded link>` |
| NPV Tunnel | None. It documents no import link, so there are only steps: copy the link and add it as a subscription |

**These links have not been tried on real devices.** They are the schemes commonly used for each app, and apps change them between versions. Try each one on a phone before you rely on it. The link and the QR code are always there to copy or scan by hand. If an app changes its scheme, the fix is one line in `internal/core/web/subpage.go`.

The help center also gets download tutorials for Karing and NPV Tunnel, next to v2rayNG, Hiddify, Streisand and V2Box. Edit or delete them in Branding & texts.

## Safety and limits

- The page is one HTML document with inline CSS, no JavaScript and no outside requests, so it loads on a weak connection. The QR code is an image inside the page.
- It allows 30 requests a minute per client address.
- It sends `no-store`, `noindex` and no-referrer headers, and a strict content security policy.
- A server's **Subscription address** on the Panels page may not point at core's own `/sub/` path, because that path belongs to this page. The dashboard refuses it.

## Troubleshooting

| Symptom | Check |
|---|---|
| The page shows 404 | Is `branding.sub_page.enabled` off? Is the service pending or deleted? Was the address regenerated? |
| No Open page button | The store needs a public HTTPS address, and the page must be on |
| Renew button missing | Set `branding.bot_username` |
| An app button does nothing | Not tried on devices. Use the link or QR code, and tell us which app and version on the issue tracker |

More detail: [docs/15-subscription-page.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/15-subscription-page.md).
