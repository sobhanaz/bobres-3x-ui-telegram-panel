# Automatic TON (Toncoin)

A sibling of automatic USDT on TRON: the customer sends an exact amount of TON, with the payment's own comment, to the
store's TON address, and BOBRES confirms the payment by itself. Provider name: `ton_auto`. It is **off until the owner
sets the store's address and switches it on** in Payment gateways, and it needs the automatic crypto watcher in the
license (`crypto_watcher`, as automatic USDT; a license that names `usdt_trc20_auto` covers `ton_auto` too).

> **Not verified against the real network.** It is implemented to TON Center's public HTTP API (version 2,
> `getTransactions`) and the two exchanges' public price pages, and tested only against fakes in this repository
> (`internal/tonfake`). Nobody has yet paid an order with real TON through it. Do the checklist at the end with a small
> real payment before telling customers about it.

BOBRES never holds a private key: it only reads a public address. The money goes straight to the owner's wallet.

## How a payment goes

1. **The price.** Core turns the order's price into USDT exactly as for automatic USDT (a Toman price at the store's
   USDT rate, the live rate plus the owner's margin, whole cents rounded up; 0.1 to 100,000 USDT). Payments turns that
   into TON at the TON price it reads every 30 seconds from **Binance** (`TONUSDT`) and **OKX** (`TON-USDT`): when both
   answer and differ by more than 5 %, there is no price; one answering is enough. A price older than **3 minutes** is
   not used: **with no fresh price the method is not offered at all** (the bot and the Mini App do not show it, and a
   payment cannot be started), and nothing is guessed. Amounts are integers all the way: micro-USDT per TON, and
   `nanoton = ceil(usdt_micro × 10⁹ / price_micro)`, so a payment is never short.
2. **The payment.** It carries the exact amount in nanoton, the store's address (in the non-bounceable form a wallet
   shows, `UQ…`), its comment `BOB-` and the last 8 hex digits of its id (unique for ever: a database index), and a
   **30-minute** window. The amount is fixed at creation (`quote_usdt`, `quote_rate`, `quote_source` are kept). A
   customer asking again for the same payment while it is open is shown that payment again; one customer can hold at
   most 10 unpaid TON payments in 2 days.
3. **The watcher** reads the address with `getTransactions` every 30 seconds (and when the customer taps "I paid,
   check", at most every 10 seconds across all customers). It pages by logical time and hash, keeps a cursor per
   address, and acts on a transaction only once it is **a minute old** (final). Each incoming transfer is recorded
   once (`ton_transfers`, unique by hash and logical time) before anything is settled with it.
4. **Matching** is by comment, then amount: the comment names the payment; the amount must be **at least** what was
   asked (more is accepted; less is not). The payment settles through the same compare-and-set as every gateway
   (`payment.succeeded.v1`, ledger key `gateway:<id>`, in one transaction), exactly once, whatever the order of the
   watcher's reads, the check button and the reconciler.
5. **Everything else is recorded and, unless it is dust, sent to staff** as an unmatched deposit
   (`payment.deposit_unmatched.v1` with `network: "TON"`, the comment, the payment it named and the amount it
   expected), to credit by hand and mark handled on the dashboard. **Nothing unmatched is ever credited by itself.**
   The reasons: no comment or one that names no payment (`no_payment`), a payment already paid (`already_paid`), one
   that can no longer be paid (`closed`: failed, or over 48 hours old), less than asked (`underpaid`), or a late
   transfer worth less than the price at today's TON price (`late_price`). A transfer under 0.01 TON with no payment
   is ignored silently (address poisoning), and so is one that bounced back to the sender.
6. **Late payments.** An open payment expires once a read that started 5 minutes after its window has gone through.
   A transfer for it that arrives later, within 48 hours of its creation, settles it only if what arrived is still
   worth at least its USDT price at the fresh price now; otherwise it is `late_price`, for staff.

## Settings (Payment gateways, owner only)

| Field | |
|---|---|
| Address | the store's TON wallet address, pasted in any form a wallet shows (`EQ…`, `UQ…` or `0:<hex>`); kept and shown in the non-bounceable form; mainnet, workchain 0 only. Stored encrypted. It cannot change while payments to the old one are open. |
| TON Center API key | optional (without one TON Center allows about one request a second, which is enough for one store); write-only, stored encrypted, never logged or shown again |
| Proxy | optional http(s) proxy for TON Center, write-only |

**Test** reads the address once and says when its newest transaction happened (`latest_tx_at`, null when it has none
yet); it changes nothing. The card also shows the TON price payments read last, where and when, and whether it is
fresh. No environment variable seeds automatic TON; nothing new in `.env`.

## Where it shows

- **Bot:** "💎 TON (automatic)" at checkout and for a wallet top-up; the screen gives the exact amount (Latin digits,
  all 9 decimals), the address and the comment, each in a tap-to-copy block, the price in Toman, the minutes left and
  the warning that a transfer without the comment is not recognised; "I paid, check" asks payments to read now.
- **Mini App:** the same, with copy buttons and a countdown (`scale_gateway` 9, `details.memo`).
- **Dashboard:** the TON card (address, key, proxy, switch, test, price), the unmatched TON deposits with their comment
  and **Mark handled** (`POST /api/v1/gateways/deposits/resolve` with `network: "TON"` and the hash), the transfer log
  (`GET /api/v1/gateways/deposits?network=TON`), the report's provider breakdown and the system alerts (the watcher
  cannot read the address for over 5 minutes; unmatched deposits).

## Database

Payments migration **00010** (additive): `payment_intents` gains `pay_memo` (unique for `ton_auto`), `quote_usdt`,
`quote_rate`, `quote_source`; new tables `ton_transfers` and `ton_cursors`; the provider checks accept `ton_auto`.
No core migration.

## Checklist before telling customers

1. Set the store's address and press **Test**: it should answer and show the address's newest transaction.
2. Check the card shows a fresh TON price, and that the bot offers TON for a plan.
3. Buy the cheapest plan with TON from a real wallet, with the comment: the order should be delivered within a minute or
   two of the transfer.
4. Send a small amount without a comment: it should appear on the dashboard as an unmatched deposit, and staff should
   get the alert. Mark it handled.
5. If TON Center is unreachable from the server, set a proxy or an API key and test again.
