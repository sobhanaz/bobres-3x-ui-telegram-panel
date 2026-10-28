# Mini App

The Mini App is your store as a web app inside Telegram. It is the same store as the bot, on a web screen instead of chat buttons: the same plans, prices, discount codes, wallet, orders and support inbox, and the same rules for bans, maintenance mode and the required channel.

## What customers can do

- Browse plans (in groups, if you made them), buy, and pay.
- See their services, copy the link, open the service's page, and renew or add traffic.
- Use the wallet: balances, history, top-up. A top-up is 10,000 to 50,000,000 Toman.
- Take the free trial, invite friends, and write to support.
- See their own codes under **More > My codes**: the reward codes made for them (with a Copy button) and the gift codes they applied, newest first, with each one's worth, state and date. It is the same list as the bot's Wallet > My codes.
- Resume a manual payment that was left half-way: card to card or USDT payments that still wait for their receipt are listed in the wallet tab.

It does not do bulk purchase, and it does not reset traffic or give free days. Those stay with the bot or with staff.

### Payment methods

The wallet, an online gateway (Zarinpal, IDPay, NextPay), Telegram Stars, automatic USDT, and the manual methods (card to card, USDT, a Zarinpal link). For a manual payment the customer uploads a receipt image. It reaches the customer's chat with the bot and your staff review it as usual. A customer who never pressed Start in the bot gets an "open the bot" message. See [Payments](Payments). When a Toman price is paid in manual USDT, the screen also shows the rate the USDT amount was worked out at (the live price when the store follows it).

### Resellers

A reseller sees an extra section under **More**: the reseller panel, buying for customers, the services they manage (pause, resume, delete, note, rename), their customers, the statement, and their invite link. It disappears when resellers leave the license. See [Resellers](Resellers).

### Digital accounts

On the vendor's own store only (see [Digital Accounts](Digital-Accounts)), a **Digital accounts** section appears under **More**: the supplier's categories, a category's products, a product page with its text and stock, a quantity, an order summary, and the wallet pays. The account is shown to its buyer, and every purchase is kept in **My purchases**. When the supplier has not delivered yet the app asks again every few seconds, and a short wallet is sent to the Wallet tab to top up. It is the same shelf, wallet and purchase as the bot.

## It is on by default

You do not set anything up in @BotFather. When the store has a public HTTPS address, the bot adds:

- a **Store** button first on its home screen,
- a **Store** item first in its bottom menu,
- Telegram's own **menu button** next to the message box.

The app lives at `https://shop.example.com/app/`. With plain http, or an address such as `localhost`, a private address or a `.local` name, Telegram cannot open it, so the bot adds no button.

The bottom-menu item is a text item on purpose. Telegram gives a Mini App opened from a reply-keyboard button no sign-in data. So a tap on **Store** in the bottom menu makes the bot answer with a message that holds the inline button, and that button opens the app. The home-screen button opens it at once.

To switch it off, turn off `miniapp.enabled` in Settings (General). The buttons go within a minute and `/app/` answers 404.

## How sign-in works

Telegram opens the page with signed `initData`. Only the bot holds the bot token that checks the signature, and core has no internet and never sees the token. So core sends the data to the bot, the bot verifies it (it must be less than an hour old), and core opens a session for that customer. A session lasts at most 6 hours. The init data is never logged or stored. A banned customer gets nothing. See [Architecture](Architecture).

## Rules it follows

- **Maintenance mode:** the app shows the maintenance screen.
- **Required channel:** a customer who has not joined sees a gate with **Join** and **I've joined** buttons, and nothing of the store behind it. Staff and payments already under way are not blocked. If Telegram cannot say whether someone joined (for example the bot is not the channel's administrator), the customer is let in for that chat.
- **Bans:** a banned customer is refused.

Unlike the bot, the app also closes support to a customer outside the required channel.

## What is not verified

The tests play Telegram with a fake. Only a real phone can prove these, and they are still open:

- Sign-in with the real bot token on iOS, Android and Desktop.
- Telegram's web clients, theme colors, the notch and the Back button.
- A payment page opening from the app and coming back (Zarinpal, IDPay, NextPay).
- Stars payments made through a link.
- Receipt photos from a camera, including large and HEIC photos.
- The required-channel gate with a real channel.

Try each with a small plan before you tell customers about the app.

## Troubleshooting

| Symptom | Check |
|---|---|
| No Store button | Is the store on a public HTTPS address? Wait one minute after a change. Is `miniapp.enabled` on? |
| App says it cannot sign in | Is the bot running? (`bobres status`). Core asks the bot to verify every sign-in |
| Customers outside a channel are blocked | By design. Check **Settings > Required channels and groups**, and make the bot an administrator of the channel |

More detail: [docs/17-mini-app.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/17-mini-app.md).
