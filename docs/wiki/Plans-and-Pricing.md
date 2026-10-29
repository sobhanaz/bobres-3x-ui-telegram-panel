# Plans and Pricing

This page is about what you sell to your customers. The price of BOBRES itself is on [Licensing](Licensing).

## Plans

Plans are made in the dashboard under **Plans**. Each plan has:

| Field | Meaning |
|---|---|
| Name | In Persian and in English |
| Limits | **Time only** (unlimited traffic), **Traffic only** (no end date), or **Time and traffic** |
| Days and traffic (GB) | The size of the plan |
| Price and currency | Whole numbers in the currency's smallest unit. Prices are normally in Toman |
| Order | Where it sits in the list |
| Group | Optional plan group (below) |
| Servers | Which panels new services of this plan may use. Empty means any server |
| Inbounds | Which inbounds of each server a new service joins. Empty means every attachable inbound |
| Free trial | Marks the plan as the trial. Each Telegram account can take it once |
| Traffic package | A plan that adds traffic to a running service instead of making a new one |
| Reward code | See below |

A change to servers or inbounds affects new services only. Renewals and traffic packages stay on the service's own server.

You can also manage plans in the bot with `/admin`, except for plan groups, which only the dashboard changes.

## Plan groups

Put plans into named groups, each with a text in Persian and English. In the bot and the Mini App the customer first picks a group, reads its text (what it is for, who it suits), then picks a plan of it. Plans with no group are listed under **Other plans**. A store with no groups lists its plans as one list. Deleting a group keeps its plans on sale, ungrouped. You can have up to 50 groups.

Only Buy is grouped. Renew, Add traffic, a reseller's Buy for a customer and Bulk purchase list plans flat.

## How plans look in the bot

`bot.plan_layout` chooses between a **table** (the default: a header row, then each plan as a row of two buttons with its name and its price) and a **list** (one button per plan). A table with more than 46 plans is drawn as a list.

## How renewal works

When a customer renews a service with a plan, the plan's **traffic is added** to the traffic the service has now, but the **time starts over**: the days that were left are dropped, and the plan's days count from the day of the renewal. A 30-day plan always means 30 days from the renewal, never more. The customer is told this before choosing a plan to renew with and again at the checkout, in the bot and in the Mini App. A traffic package adds traffic only and does not touch the time. When your staff add days to a service from the dashboard, those days are still added to the time left.

## Deleting a plan

Only the owner can delete a plan (permission `plans.delete`). The trash button asks for a confirmation. The plan is taken off sale and out of every list, but it is never erased. Its orders, reports and refunds keep it, and services already made from it keep working.

## Restoring a deleted plan

Under the plans table, **Deleted plans** lists what was deleted. The owner (the same permission, `plans.delete`) can press **Restore**. The plan comes back **off sale** and in no group, so nothing is sold by accident: switch it on and group it again when you are ready.

## Discounts and codes

| Code | What it does | Where |
|---|---|---|
| Discount code | Takes a percent or a fixed amount off a purchase. Has a use limit, a per-customer limit and dates | Discounts & referrals |
| Gift code | Adds a fixed amount of credit to a customer's wallet. You can make one of your own words, or a batch of up to 500. The codes are shown once, so copy or download them | Gift codes |
| Reward code | Made by the bot after a purchase (below) | Plans |
| Referral reward | A percent of an invited friend's first paid purchase goes to the inviter's wallet (`referral.reward_percent`) | Discounts & referrals |
| Cashback | A share of every delivered order goes back to the buyer's wallet (below) | Settings |
| Daily wheel | A free spin a day for customers who have paid orders, for a chance at wallet credit (below) | Settings |

Customers can apply gift codes unless you switch `gift_codes.enabled` off.

### Reward codes

Edit a plan and set **Reward code after a purchase**: nothing, a percent (1 to 90), or a fixed amount in the plan's currency. Set how many days it lasts (1 to 365, default 10) and a text for the buyer in both languages. After each paid purchase or renewal of that plan, the buyer gets a discount code in a message. It works on any plan they buy next, for them only, once. An amount never takes more than the price, and only works on a plan in its currency. A reseller's purchase, a free order, the trial, a traffic package and one service of a bulk purchase give no code.

The customer finds the code again under Wallet > **My codes** (or `/codes`, or **More > My codes** in the Mini App), with its state: not used yet, used, expired or switched off. You see the same list on the customer's page in the dashboard, in the **Codes** tab.

### Cashback

Cashback gives a share of what an order cost back to the buyer's wallet. It is **off** until you switch it on in **Settings**, card **Cashback**:

| Setting | Meaning |
|---|---|
| `cashback.enabled` | The switch. Off by default |
| `cashback.percent` | From 0.01 to 20, for example 2.5. Worked out on the price after any discount, in whole Toman, rounded down |
| `cashback.max_per_order` | The most one order earns, in Toman. Empty or 0 means no maximum |
| `cashback.min_order` | An order must cost at least this much to earn. Empty or 0 means any order that costs something |
| `cashback.renewals` | On by default: renewals and traffic packages earn too. Off: only new services |

When an order has been **paid and delivered**, the buyer's wallet is credited once, whatever retries the delivery. The customer gets a message ("You earned X cashback") and sees it in the Mini App, on the Wallet tab and on the result of the purchase. The services of a bulk purchase are told in the bulk's one message.

It is wallet credit, not cash: it can only be spent in your store, and an order paid with it earns cashback again. If you **refund** the order, the cashback is taken back in the same step, from the refund, and never takes a wallet below zero (if it cannot all be taken, the audit log says how much was short). Resellers (they have [monthly targets](Resellers)), free orders, the free trial, orders priced in USDT and banned customers earn nothing. Reports have a line for the cashback given; it is not revenue and it is not taken off the net.

### Daily wheel

The wheel gives each customer who has paid for enough orders one free spin a day, for a chance at wallet credit. It is **off** until you switch it on in **Settings**, card **Daily wheel**. It also stays closed until it has prizes and a daily budget.

| Setting | Meaning |
|---|---|
| `wheel.enabled` | The switch. Off by default |
| `wheel.prizes` | Up to 12 prizes. Each has an amount in Toman and a **weight**, its share of the chances: a prize of weight 3 comes up three times as often as one of weight 1. The card shows the chance of each |
| `wheel.no_prize_weight` | The weight of the piece that pays nothing. The chance of no prize is this out of all the weights added up. 0 or empty: every spin wins something |
| `wheel.daily_budget` | The most the wheel pays out in one day (store time). Empty or 0 closes the wheel |
| `wheel.min_orders` | Paid orders a customer needs before they may spin, at least 1. Refunded orders and orders that cost nothing do not count |

A customer spins once per day (the day is your store's day), in the bot (a **Daily wheel** button) or in the Mini App (a wheel that turns). Asked again the same day, they get the same spin back: it is never drawn or paid twice. The draw is made on the server, not on the customer's phone.

The budget is checked one spin at a time. When what is left of it cannot cover the prize that comes up, nothing is paid, the customer's spin is **not** used, and they are asked to come back tomorrow. Only customers spin: not resellers, staff or banned customers. A prize is wallet credit, not cash; it can only be spent in your store. Under the card, a small report shows today against the budget and each day of the last month with a spin.

## Bulk purchase

A customer can buy 2 or more services of one plan at once, paid from the wallet only. A discount code does not apply to a bulk purchase. Control it with `bulk.enabled` (on by default) and `bulk.max_quantity` (2 to 100, default 20).

## Prices in USDT

Plans have one price in Toman. When a customer pays in USDT, the bot works out the USDT amount from the live rate. See [Payments](Payments) for the rate settings.

## Reseller prices

Resellers pay by tier: a percent off your price, or a fixed price per plan. Tiers are set on the Resellers page. See [Resellers](Resellers).

## What your own license limits

On the free tier a store may have 1 server and 25 active services. Standard allows 3 servers and 500 services, and Pro has no limit. A new purchase beyond the limit is refused, but existing services keep working and renewing. See [Licensing](Licensing).

## Tips

- Start with 2 or 3 plans. Make one a trial and test it yourself.
- Write group texts and reward texts in both languages, or the missing language shows the other one.
- Check a new plan with a small test purchase before you advertise it.
