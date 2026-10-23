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

## Deleting a plan

Only the owner can delete a plan (permission `plans.delete`). The trash button asks for a confirmation. The plan is taken off sale and out of every list, but it is never erased. Its orders, reports and refunds keep it, and services already made from it keep working.

## Discounts and codes

| Code | What it does | Where |
|---|---|---|
| Discount code | Takes a percent or a fixed amount off a purchase. Has a use limit, a per-customer limit and dates | Discounts & referrals |
| Gift code | Adds a fixed amount of credit to a customer's wallet. You can make one of your own words, or a batch of up to 500. The codes are shown once, so copy or download them | Gift codes |
| Reward code | Made by the bot after a purchase (below) | Plans |
| Referral reward | A percent of an invited friend's first paid purchase goes to the inviter's wallet (`referral.reward_percent`) | Discounts & referrals |

Customers can apply gift codes unless you switch `gift_codes.enabled` off.

### Reward codes

Edit a plan and set **Reward code after a purchase**: nothing, a percent (1 to 90), or a fixed amount in the plan's currency. Set how many days it lasts (1 to 365, default 10) and a text for the buyer in both languages. After each paid purchase or renewal of that plan, the buyer gets a discount code in a message. It works on any plan they buy next, for them only, once. An amount never takes more than the price, and only works on a plan in its currency. A reseller's purchase, a free order, the trial, a traffic package and one service of a bulk purchase give no code.

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
