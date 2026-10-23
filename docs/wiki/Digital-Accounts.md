# Digital Accounts

**This feature is not part of a normal BOBRES install.** It belongs to the vendor's own store. Selling a supplier's digital accounts, and selling BOBRES licenses from a bot, are locked behind an entitlement that no tier has: not Free, not the trial, not Standard, not Pro. On your install they are hidden, refused and off. You will not see **Digital accounts** or **BOBRES licenses** in your dashboard, and you cannot turn them on with a setting.

The page exists so that you know what these two names mean, and so that a question about them has an answer.

## What it is

The owner of the vendor's own store can resell the accounts of a supplier through the same bot. The supplier has a reseller API. BOBRES reads the supplier's catalog, the owner prices each product in Toman and writes its names and texts, and customers buy from their wallet.

If you want to sell VPN subscriptions, you do not need it. Use [Plans and Pricing](Plans-and-Pricing).

## How it works

- **Core has no internet access.** Every call to the supplier goes through the bot. Core names an action and gives the API key for that call. The bot calls the one supplier address it is configured for.
- **The catalog** is read every half hour, and every five minutes while something on the shelf is sold out. A new product arrives switched off and without a price. The owner sets the price, writes the names and descriptions in both languages, and switches it on.
- **A sold-out product** is still shown, marked as sold out, and cannot be bought. The same holds for a product the supplier no longer lists.
- **A purchase** is paid from the customer's wallet in Toman. One database transaction takes the money and writes the order. The supplier is then asked, using the order's own ID as the external order ID, so a repeated request never charges twice. The account the supplier returns is stored encrypted and shown to the customer in the bot and under **My purchases**.
- **If the supplier does not answer,** the order stays paid and waiting, and a worker asks again with growing waits. If the supplier refuses for good, the money goes back at once. The owner can retry or refund an order that was never delivered.
- **The supplier balance** is shown to the owner on the Digital accounts page and on the Overview.

## In the bot

The home of the feature shows the supplier's categories in a grid, red when nothing in a category is in stock. A category lists its products. A product page shows the description, the unit price and the stock, with **Buy now**. Then the customer picks how many (buttons, or a typed number, up to 25 and no more than the stock), sees an order summary, and pays with the wallet. The bottom menu gets a Digital accounts button while the feature is on.

## Texts

The owner can use **Export texts** to download every product's name and text as a file, have the Persian texts filled in by a translator, and **Import texts** to bring them back. Prices and switches are not part of that file.

## Limits

- Prices are in Toman only.
- A purchase takes 1 to 25 accounts.
- The supplier's stock figure is trusted as read.
- What a delivery looks like depends on the supplier's answer. It is shown as text, so check the first real order.

## About the lock

The lock is enforced in core, not only hidden in the menu: the dashboard section is not shown and the calls are refused unless the install's license grants the vendor's own features. A customer's bot, dashboard and wallet stay as they are.

**Buying a license in one screen.** A customer pays for a BOBRES license from their USDT wallet. When the wallet is short, the confirm screen says how much is missing and offers the top-up. Tapping it saves what they wanted (the tier and 1 or 12 months) for 48 hours, and when the payment lands the store buys the license by itself and sends the key. A manual purchase, a refusal or a refund clears the wish, so nothing is bought twice.

The design notes are in [docs/supplier.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/supplier.md). The tiers and what they include are in [Licensing](Licensing).
