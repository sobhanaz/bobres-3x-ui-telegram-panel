# Bot Guide

The Telegram bot is where customers buy and get help. It speaks Persian and English, uses your name, texts and links, and every message can be edited in the dashboard (Branding & texts). The customer picks a language on the first `/start` and can change it in Settings.

## For customers

### Main menu

| Button | What it does |
|---|---|
| Buy service | Plans, then payment, then the service |
| My services | Status, expiry and traffic of each service. Renew or add traffic |
| Wallet | Balance, top-up, history, apply a gift code |
| Free trial | One free service per Telegram account, if you made a trial plan |
| Invite friends | The customer's invite link and a card to forward |
| Support | Tickets and the FAQ |
| Settings | Language and notifications |
| Top users | A board of the customers who invited the most people. Shown while the referral reward is above 0 and you have not switched the board off |
| Become a reseller | Shown only while your license includes resellers |
| Bulk purchase | Buy several services of one plan at once, from the wallet |

A **bottom menu** keeps the main buttons one tap away. Buttons are colored by what they do: green pays or confirms, blue opens a main action, red cancels.

### Commands

| Command | Does |
|---|---|
| `/start`, `/menu` | Open the main menu |
| `/new` | Buy a new service |
| `/renew` | Renew a service |
| `/status` | My services |
| `/terms` | Terms of service and privacy |
| `/paysupport` | Help with a payment |
| `/cancel` | Leave what you were doing |

### Buying

1. Pick a plan. If you made [plan groups](Plans-and-Pricing), the customer picks a group first and reads its text. The list can be a two-column table (name and price) or one button per plan (`bot.plan_layout`).
2. Optionally enter a discount code.
3. Pick a payment method. See [Payments](Payments).
4. After payment, the bot sends the subscription link, a QR code and a button to open the service's page. See [Subscription Page](Subscription-Page).

If your panel is down, the customer keeps a paid order and gets the service when it is back.

### After the purchase

- **Renew and add traffic** from My services, with the same link.
- **Find by link:** a customer who does not see a service in their list sends its subscription link, sees its state, and can renew it.
- **Reminders** come 3 days and 1 day before expiry, at 80% of the traffic and when it ends, with Renew and Add traffic buttons.
- **Support** is one open conversation per customer. They can send text, photos and PDFs. Staff replies arrive in the chat.
- **Help center:** the FAQ and tutorials you write in the dashboard (Branding & texts, Help content).
- **Gift codes** add credit to the wallet. **Reward codes** are discount codes you hand out after a purchase. See [Plans and Pricing](Plans-and-Pricing).

## For staff

Owners and admins send `/admin` and get a menu made of buttons. Support staff get `/dashboard` only.

| Menu | What you can do |
|---|---|
| Payments to review | See each payment's receipt and Approve or Reject. Every owner and admin sees the same list. The first decision counts |
| Today and find a user | Orders, revenue and refunds for today and 7 days, new users, panel health. Find and manage a user |
| Plans and discounts | List, add, edit and archive plans, set the trial and traffic packages, make discount codes |
| Notifications and broadcast | Choose who gets alerts, send a broadcast with a preview |
| Settings and servers | Every setting your role may change, and panel health, switches and weights |
| Payment methods and staff | Owner only |
| Dashboard link | A one-time login link |

`/set` opens the settings screen. The dashboard remains the full tool.

Staff get alerts in Telegram for payments to review, new tickets, failed deliveries, sales (optional) and system problems. Every owner also receives the daily backup file.

## Rules the bot applies

| Rule | Where | Effect |
|---|---|---|
| Maintenance mode | Settings (`maintenance.enabled`) | Customers see the maintenance text and nothing else. Staff and payments already under way still work. Active services keep working |
| Required channel or group | Settings | Customers must join before using the bot. Staff and payments under way are not blocked. One is allowed on the free tier and Standard, any number on Pro and during the trial |
| Rate limits | Settings | Taps and messages per 10 seconds, support messages per day |
| Bans | Users | A banned customer cannot use the bot |
| Terms and privacy | Branding | `/terms` and the buttons use your [Terms and Privacy pages](Terms-and-Privacy-Pages) |

When your install is at its license limit, a new purchase shows the customer only "New purchases are temporarily unavailable. Please contact support." They never see a word about licensing. Renewals and top-ups continue.

The bot also sets its own commands, description and profile photo. You can upload a photo in the dashboard (Branding & texts, Store, Telegram bot).

For every flow in detail, read [docs/03-bot-ux.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/03-bot-ux.md). If customers get no answer, see [Troubleshooting](Troubleshooting).
