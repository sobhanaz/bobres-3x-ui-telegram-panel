# Payments

Customers can pay in several ways. The owner switches each on, and sets its details, under **Payment gateways** in the dashboard. Secrets such as a Zarinpal merchant ID or an API key are stored encrypted in the payments service and are never shown again.

## The methods

| Method | How the customer pays | How it settles |
|---|---|---|
| Wallet | From their balance | At once |
| Card to card | Transfers to your card, then sends a receipt screenshot and the bank reference number | A staff member approves the receipt |
| USDT, manual | Sends USDT (TRC20 or ERC20) to your address, then sends a screenshot or the transaction ID | A staff member approves |
| Zarinpal link, manual | Pays on your Zarinpal payment link, then sends a screenshot | A staff member approves |
| Zarinpal, online | Pays on the bank page | Payments asks Zarinpal to confirm. Nothing is trusted from the callback alone |
| IDPay, NextPay, Aqayepardakht | Same, on the gateway's page (Aqayepardakht's is opened from your own payment page) | Payments verifies with the gateway |
| Telegram Stars | Pays an invoice inside Telegram | Telegram confirms to the bot |
| USDT, automatic (TRC20) | Sends an exact, unique amount to your TRON address, within 45 minutes | The payments service sees the transfer on the chain |
| TON, automatic | Sends an exact amount of TON to your TON address, with the payment's comment (`BOB-...`), within 30 minutes | The payments service sees the transfer on the TON network |

Rules that hold for every method:

- A payment settles exactly once. Repeated callbacks change nothing.
- The ledger is append-only, and amounts are whole numbers in minor units.
- If a panel is down after payment, the order stays paid and is delivered when the panel is back.

On the free tier you can use Zarinpal, IDPay, NextPay, Aqayepardakht, Stars and the manual methods. Automatic USDT and automatic TON need a paid license or the trial. See [Licensing](Licensing).

## One wallet, in Toman

Every customer has **one wallet, in Toman**. It is shown with its equivalent in USDT at the live price and the live USDT rate, in the bot and in the Mini App. A customer can top it up in Toman by any method, or in **USDT** (a whole number of USDT, automatic or by hand): a USDT top-up is credited as Toman at the live price (without your margin, rounded down), and the customer is told the amount and the rate. What is priced in USDT, such as a BOBRES license, is charged from the same wallet in Toman at your store's USDT rate (the live price plus your margin, rounded up). Money that still sits in a USDT wallet (an old balance, or a top-up made while no rate was known) is converted automatically as soon as a rate is known. A plan priced in USDT is paid directly by crypto, not from the wallet. In the reports everything is one currency, Toman.

## Manual payments and the review queue

Open **Orders & payments** and the **Review queue** tab. Each payment shows the receipt, the bank reference and the amount. **Seen before** means another receipt has the same reference, so look at the photo with care. **Approve** pays the order and delivers it. **Reject** asks for a reason, which the customer sees in the bot.

Every owner gets an alert when a receipt arrives, and `/admin` in the bot has the same queue. The first person to decide wins.

## Details you set

| Setting | Meaning |
|---|---|
| `payments.card_number`, `payments.card_holder` | Where card-to-card payments go |
| `payments.usdt_trc20`, `payments.usdt_erc20` | Your manual USDT addresses |
| `payments.zarinpal_link` | Your Zarinpal payment link (manual method) |
| `payments.stars_rate` | Toman per Telegram Star, used to price plans in Stars. Empty means no Stars |
| `payments.manual_card.enabled`, `payments.manual_crypto.enabled`, `payments.manual_zarinpal.enabled`, `payments.stars.enabled` | The on and off switches |

For Zarinpal online, IDPay, NextPay and Aqayepardakht, paste the key (Aqayepardakht calls it the PIN, and it is case-sensitive) on the Gateways page, press **Test**, then switch on. Register your store's payment address as the callback domain in each gateway's panel. IDPay, NextPay and Aqayepardakht are off until you set a key. Aqayepardakht has a test system: the PIN `sandbox`, which is refused on a production install.

**IDPay, NextPay and Aqayepardakht have not been tried against the real gateways.** They follow the public documentation and are tested only against fakes. Make a small real payment through each before you tell customers about them. The checklists are in [docs/16-idpay-nextpay.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/16-idpay-nextpay.md) and [docs/18-aqayepardakht.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/18-aqayepardakht.md).

## Automatic USDT on TRON

Enter your TRON address on the Gateways page, and optionally a TronGrid key. The payments service reads the chain about every 30 seconds. A customer is shown an exact amount, the address and a deadline. A transfer of any other amount is an **unmatched deposit**: nothing is credited, staff are alerted, and you credit the customer by hand and mark the deposit handled. The address cannot be changed while automatic payments are open.

## Automatic TON

Enter your TON wallet address on the Gateways page (any form your wallet shows: `UQ...`, `EQ...` or `0:...`), and optionally a TON Center API key and a proxy. The price is worked out like automatic USDT's (Toman to USDT at your rate), then turned into TON at the price the payments service reads every 30 seconds from Binance and OKX. **With no fresh TON price (under 3 minutes old) the method is not offered at all.** The customer is shown the exact amount, the address, a comment to put in the transfer and a 30-minute countdown. The transfer must carry the comment and at least the amount; anything else (no comment, less than asked, a payment already paid, a late transfer the price no longer covers) is an **unmatched deposit**: nothing is credited, staff are alerted, and you credit by hand and mark it handled. BOBRES only reads your public address; it never holds a key.

**Automatic TON has not been tried against the real network.** It follows TON Center's public API and is tested only against fakes. Make a small real payment first. The checklist is in [docs/19-ton.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/19-ton.md).

## The live USDT rate

Plans are priced in Toman. Every USDT amount is worked out from the price of one USDT in Toman.

| Setting | Default | Meaning |
|---|---|---|
| `payments.usdt_rate_auto` | on | Use the live rate |
| `payments.usdt_rate_margin` | 0 | Percent added on top of the live rate, 0 to 30, for the exchange's spread and fees |
| `payments.usdt_rate` | none | Your own Toman per 1 USDT. Used when the live rate is off, or cannot be read or is older than 30 minutes |

The bot reads Tetherland and Wallex about every two minutes. Core has no internet access, so the bot makes the call for it. The amount is rounded up to a whole Toman, so a margin never disappears. The Gateways page shows what is charged now and where it comes from, and has a **Read it now** button. The bot's crypto screen shows "1 USDT = N Toman".

If you used to set your own rate and want to keep using only that, turn `payments.usdt_rate_auto` off.

## Refunds

Open the order and choose **Refund to wallet**. The order's amount goes to the customer's wallet and the customer is told. For an order that could not be delivered, the order is cancelled and a half-made client is removed from the panel.

## When a payment is stuck

Start with the [payment-stuck runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/payment-stuck.md) and [Troubleshooting](Troubleshooting). An unconfirmed Zarinpal payment expires, but for 48 hours after it started it is still settled if Zarinpal confirms it, when the customer taps "I paid, check" or returns from the bank page.
