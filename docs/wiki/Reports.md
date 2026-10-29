# Reports

**Reports** in the dashboard answers: what did the store sell, in which currency, to whom, and how was it paid. It is read-only. It reads the ledger, the orders and core's event log, and it changes nothing.

Owners and admins can open it. Support staff cannot, and they do not see the revenue cards on the Overview either. A reseller is not staff at all.

## The money rule

Amounts of different currencies are never added together. Toman, USDT and Stars each get their own card, chart and table. Only counts of people and events (new customers, trials, churn) have no currency. Amounts are whole numbers in each currency's smallest unit.

## Choose what to look at

| Control | Choices |
|---|---|
| Range | Today, last 7 days, last 30 days (the default), this month, last month, or two dates |
| Group | Day, week or month |
| Calendar | Gregorian (weeks start Monday) or Jalali (weeks start Saturday, Jalali months). Every date on the page follows it. The choice is kept in your browser |
| Time zone | `general.timezone` in Settings, else `BOBRES_TIMEZONE`, else `Asia/Tehran` |

A payment at 23:59 and one at 00:01 local time fall on different days. A period that your range cuts is marked partial.

## What the numbers mean

| Number | Meaning |
|---|---|
| Revenue | What customers were charged, from the sales entries of the ledger: VPN orders, and on the vendor's own store the BOBRES licenses and the digital accounts it sells. Free orders are not in it |
| Paid orders | New orders, renewals and traffic packages, plus the licenses and digital accounts sold (each has its own line). A bulk purchase of N services counts N orders |
| Refunds | Orders, licenses and digital accounts refunded to a wallet, by the day of the refund. **Net** is revenue less refunds, before payouts |
| New customers | People whose first purchase falls in the period |
| Free trials | Trials started |
| Services lost (churn) | Services whose final expiry fell in the period and ended more than the grace period ago (7 days by default). Trials, unlimited services and services with a paid renewal waiting are not counted |
| Customers lost | People whose last service ended past the grace period and who have no other live service |
| At risk | Services that ended but are still inside the grace period |
| Wallet top-ups | Money that stayed in a wallet from payments. It is money in, not a sale |
| Cashback | The cashback given to wallets in the period, less what refunds took back (so a day with only refunds can be negative). It is not revenue and it is not taken off the net. Shown as a line on the card, in the summary CSV and in the daily summary when there was any |
| Rejected / failed | Payments staff rejected, and gateway payments that failed or expired |
| Paid with | Wallet, manually approved payments, or gateways, split again by provider (Zarinpal, Stars, automatic USDT, card to card) |

Commissions and referral rewards are not subtracted from net. Churn is read as of now, not stored as a history, so a customer who comes back leaves the period they were counted in. There is no churn rate and no revenue-by-customer view. Orders paid before the provider was recorded show "provider not recorded".

## Starting from zero

If the sales so far were tests, the **owner** can press **Start from zero** at the top of the Reports page (it asks first). From that moment the reports, the CSV files, the daily summary and the Overview's revenue count only what is sold next, and the page says "Counting from ...". **Nothing is deleted**: orders, payments, wallets and the ledger stay as they are, so wallet balances are still right. **Count everything again** brings the earlier sales back. Admins can see the start but not change it, and every change is in the audit log. Services lost (churn) are not cut by it.

## Charts and the professional report

The default view has cards per currency, then charts one currency at a time: revenue and what was kept after refunds, revenue by plan, paid orders by type, how orders were paid, top plans, a heatmap of when customers pay, and new customers, trials and lost services. Each chart has a legend, a tooltip, keyboard keys and **Show as table**.

The **Professional report** view (`?view=report`) is for reading, printing or saving as PDF. It adds a letterhead, a headline in plain words, each number's change on the period before, and two 3D charts that turn with a drag. It prints on A4. On a phone, for people who prefer reduced motion, and for ranges over 62 periods, the flat charts show instead.

## CSV files

Reports can export two files: the **summary** and **paid orders**. The Wallet & ledger page exports `ledger.csv`, and the Audit log exports `audit.csv`. The files use English headers, UTC and local times, and a Jalali date column in a Jalali export. A plan priced in USDT and paid from the Toman wallet is revenue in Toman (what was charged); in the paid-orders file `list_amount`, `list_currency` and `rate` (the last three columns) keep the USDT price and the rate. Every export is written to the audit log.

## Daily summary in Telegram

Off by default. Turn it on in Settings (card **Sales summary**), or with `/set` in the bot.

| Setting | Default | Meaning |
|---|---|---|
| `reports.daily_summary` | off | Send yesterday's summary |
| `reports.daily_summary_time` | `09:00` | When, in the store's time zone |

Each owner and admin gets one message in their own language: revenue per currency with the change from the day before, paid orders, refunds, top-ups, new customers and trials. A day with no sales is one line, so you know the job is alive. It holds no customer names or order IDs. If the server was down at the due time, the summary goes out when it is back, but never one for each missed day.

## When numbers look wrong

- Check the calendar and the time zone first. The same payment can sit on different days.
- Check the currency. Each one has its own card.
- A refunded order is in Refunds by the refund day, not the sale day.

The exact definitions are in [docs/14-reports.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/14-reports.md).
