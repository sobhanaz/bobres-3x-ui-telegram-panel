# Resellers

A reseller is a customer you trust to sell for you. They buy services at your tier prices, hand them to their own customers, and manage those services. Resellers work only in the Telegram bot and the Mini App. They have no dashboard access. You manage them on the **Resellers** page.

Resellers need a license that includes them: Standard, Pro, or the 30-day trial. The free tier has none. See [Licensing](Licensing).

## Make a reseller

1. A customer can ask for it: the bot's **Become a reseller** button takes one text message and files it as a support ticket in the category **Reseller**. You get an alert with an **Open in dashboard** button. You answer in the dashboard and the reply reaches them in the bot.
2. Open **Resellers**, find the customer (or use their user page) and make them a reseller. The application itself does not change their role.
3. Choose their **tier** and, if you like, a **group name** for their configs in your 3x-ui panel.

Only the owner can write the group name. An admin can make a reseller but not name their group.

## Tiers, credit and commission

| Setting | Meaning |
|---|---|
| Tier discount | 0 to 90% off your store price |
| Fixed price per plan | A price for a plan that overrides the tier's discount |
| Commission | 0 to 50% of the purchases of customers who joined through the reseller's invite link. It can be set per reseller |
| Credit limit | Per currency. A reseller can buy on credit until their balance reaches minus the limit. A debt is shown as "debt X" |

When you receive money from a reseller, record it as a payment received. It raises their balance. The reseller's statement shows every purchase, commission and payment.

## Monthly targets

Targets give a reseller a bonus for selling more. Open **Settings**, the **Reseller targets** card. It is **off until you switch it on**.

- **Tiers:** up to 10 rows, each a number of new services sold in a month and a bonus in Toman. For example 10 services pays 50,000 Toman and 25 services pays 150,000 Toman (the second tier pays on top of the first).
- **Calendar of the month:** Gregorian (the 1st to the last day) or Jalali (Persian months). The month is read in your store's time zone, the one the reports use.
- **What counts:** a new service that the reseller bought and that was delivered. Renewals, traffic packages, free trials and free orders do not. Sales are counted from the moment you switch targets on.
- **The bonus** goes to the reseller's wallet the moment the service that reaches a tier is delivered, once for the month, and the bot tells them. Editing the tiers in the middle of a month never pays a tier twice.
- **Refunds:** a refunded sale leaves the count of its month, but a bonus already paid is **not** taken back.
- The reseller sees their progress in the bot's reseller panel and in the Mini App. You see it on the reseller's page in the dashboard, and each bonus in their statement as **Monthly target bonus**.

## Messages to resellers

The **Messages** tab of the Resellers page (or **Message resellers** above the list) sends a message that only resellers get in the bot.

- **Who:** only those who owe (the default), every reseller, or one tier; and all languages or one. The count shows how many get it. Banned resellers and those who blocked the bot are left out.
- **Text:** one per language. Tap a chip to add a value that is filled in for each reseller: `{name}`, `{debt}` (what they owe, "0 Toman" when nothing), `{balance}`, `{credit_limit}`, `{tier}` and `{customers}`. Anything else in braces, like `{dept}`, is refused. `{name}` is the reseller's @username (else their 3x-ui group name).
- **Preview as** shows the text as one reseller gets it. **Send to me** sends it to your own Telegram first.
- **Buttons:** a link button if you like, and **Settle / Top up** (on by default), which opens the reseller's wallet in the bot so they can top up.
- **Send now** asks first, then sends it like a broadcast: its page shows the progress and how many got it. It is in the Broadcasts list too.

### Weekly settlement reminder

The card under it reminds resellers of what they owe once a week. It is **off until you switch it on**.

- **Day and time:** Thursday 21:00 by default, in your store's time zone (Settings, General).
- **Only resellers who owe:** on by default. Off, every reseller gets it.
- **Text:** "Your account debt is {debt}. Please settle it as agreed." in Persian and English, each reseller in their own bot language. Change it with the same values, or go back to the default.
- It goes out **once a week**, even after a restart. If the server was down at that time it goes when the server is back within 6 hours; a week missed for longer is not sent later. Switched on after this week's time, it starts next week.
- The card shows the next reminder, the last one and how many got it. **Send a test to me now** sends the saved reminder to you, as a reseller would get it.

## What a reseller can do

In the bot and in the Mini App's **More** section, a reseller has a panel with:

- **Buy for a customer:** the plans at their prices. They can name each service ("Ali's phone"). The wallet button shows only when the balance plus the credit limit covers the price. They cannot use discount codes.
- **Bulk purchase:** several services at once, with names "name 1", "name 2" and so on.
- **Customer services:** a list of their services in groups (All, Active, Ending soon, Ended, Paused) with a search by name, number, subscription link, page address or a client config (vless://, vmess://, trojan://) pasted whole.
- **Per service:** Renew, Add traffic, QR code, Rename, **Pause** and **Resume**, a private **Note**, and **Delete** (after a confirmation, with no refund). A service that support paused cannot be resumed by the reseller.
- **Customers:** the people who joined through their link, with what they bought.
- **Statement** and **My link**, their invite link and commission rate.

A reseller cannot reset a service's traffic or give free days or traffic. Those stay with staff. Pausing and deleting go to the audit log under the reseller's name.

## Naming in your 3x-ui panel

A reseller's configs are easy to tell apart in the panel:

- They are named with the reseller's own **prefix**, for example `sob-u123-...`. The reseller chooses the prefix once, in the bot (**Config prefix**) or the Mini App. It is 2 to 10 characters of a-z and 0-9, and unique. After that only the owner can change it, and it applies to configs made afterwards.
- They are in a client group of their own, **`BOBRES-<NAME>`**. NAME is the group name you gave (capital letters, digits and `_`, 2 to 24 characters, unique). Their earlier configs move into it in the background. On the reseller's page, **Panel names** lets you change the name, and **Move the existing configs** asks again.

A client is in one group, so a reseller's clients are not in `BOBRES` any more. Until you give a reseller a group name, their configs are named as usual and are in `BOBRES`. Marzban has no groups.

## When the license drops

If resellers leave your license, a reseller keeps their account and what they bought. The plain list of their services and each service's link still open, and renewals still work. New purchases, pausing, deleting, notes and the customer list are refused, and the reseller is told only that it is not available. Nothing about licensing is shown to them.

## Removing a reseller

Removing a reseller cancels their open orders. A payment that still arrives for one stays in their wallet.

## Related

- Reseller prices appear in [Plans and Pricing](Plans-and-Pricing).
- The reseller section in the Mini App is described in [Mini App](Mini-App).
- The full flows are in [docs/03-bot-ux.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/03-bot-ux.md), under "Reseller panel".
