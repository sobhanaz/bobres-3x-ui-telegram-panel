# Phase 2 design: automated payments

PLAN.md section 10, Phase 2: "Payments: Zarinpal, Telegram Stars, third-party crypto."
Written 2026-10-04 from provider research (sources were collected at the time; the
facts that matter for the code are restated here).

## Outcome

| Provider | Status | Why |
|---|---|---|
| Telegram Stars | built, on when `payments.stars_rate` is set | Telegram's rule for digital goods; no credentials needed |
| Zarinpal | built, off until a merchant id is set | owner decision on legal/terminal risk, see below |
| Third-party crypto | **not built** | Crypto Pay, Oxapay and NOWPayments all exclude Iran (see below) |

## One contract, one set of rules

- `internal/payments/gateway`: `Create(charge) -> {external id, pay URL}` and
  `Check(external id, charge) -> {paid | pending | failed, amount, reference}`.
- The intent fixes what the customer pays at the gateway when it is created
  (`gateway_amount`, `gateway_currency`): Rial for Zarinpal (Toman x 10), whole Stars
  for Telegram (`ceil(price / payments.stars_rate)`). Core computes it from its rates;
  payments holds the customer to it. Settlement compares the gateway's report with
  these stored values, never with a recomputed price.
- Settlement is exactly once: a compare-and-set on the intent status, the payments
  ledger entry and the `payment.succeeded` event in one transaction. Concurrent reports
  (browser return, reconciler, "check payment" button) settle once.
- A gateway saying "paid" with a different amount fails the intent with
  `amount_mismatch` (the admin refunds); cancelled, unknown and expired intents fail with
  a reason. Reasons reach the customer through `payment.rejected`, translated by the bot.
- A gateway payment link is offered for 1 hour; the intent expires only 45 minutes after
  that (longer than a gateway session), so the reconciler still sees a payment started
  at the last minute. A payment the gateway confirms later still settles within 48 hours
  of creation (lost return, expiry passed, a concurrent check failed the intent first):
  through the return page or the customer's "check payment". Amount mismatches and
  reversed payments stay failed. Core sends money for an order that is no longer payable
  to the wallet.
- The reconciler (every 30 s) checks open gateway intents, earliest due first, backing
  off per intent up to 64 intervals; only its own checks count towards the backoff. User
  checks ("I paid, check") reach the gateway at most every 3 s per intent (stored in the
  database); the return page has its own refresh throttle and is always verified.
- Audit: `payments.gateway_events` records what each gateway said (no card numbers,
  no tokens).

## Telegram Stars

- The bot sends an invoice (currency XTR, one price line, payload = the intent id,
  non-empty `start_parameter` so a forwarded copy cannot be paid by someone else).
- `pre_checkout_query` (must be answered within 10 s, so it is neither de-duplicated nor
  rate-limited, and it skips the per-user queue so a slow update cannot delay it): the
  intent must be an open, unexpired Stars intent of this user, for exactly these Stars.
  The bot now subscribes to `pre_checkout_query` explicitly.
- `successful_payment`: settles the intent with `telegram_payment_charge_id` (unique per
  provider: one charge settles at most one intent). The Stars are already taken, so it is
  handled before de-duplication, the user lookup and the rate limit, logged first,
  recorded with retries on a context that survives shutdown; an intent that expired a
  moment earlier still settles and a banned user's payment is still recorded. Anything
  but a confirmed settlement (a second charge, a wrong payer or amount, an outage) tells
  the payer to contact support with the charge id and alerts the admin with the details.
- `/paysupport` answers with the support contact (Telegram requires it).
- Later: subscriptions via `createInvoiceLink` + `subscription_period` (30 days, at most
  10,000 Stars) belong to Phase 3 (renewals); refunds with `refundStarPayment` from the
  admin panel; reconciliation with `getStarTransactions`.

## Zarinpal (REST v4)

- Amount in Rial, no currency field; minimum 10,000 Rial, maximum 1,000,000,000 Rial.
- Verify answers 100 once and 101 afterwards; both mean paid, so verify is safe to retry.
  `-51` (not paid) falls back to inquiry: still at the bank stays pending, FAILED or
  REVERSED fails. `-50` is an amount mismatch. Configuration errors (`-10`, `-11`, `-15`,
  `-19`) and rate limits (`-12`) are errors to retry, never verdicts. Errors arrive with
  HTTP 401/422 and an `errors` object; success has an `errors` array.
- The customer must start the payment from a page on the merchant's registered domain
  (Zarinpal checks the Referer; since Aug 2025 a mismatch shows an interstitial and counts
  as a violation). So the bot links to `/pay/<intent>` on our domain, which links to
  StartPay with `Referrer-Policy: origin`; a plain redirect would not carry the Referer.
- The return URL `/webhooks/zarinpal` never trusts its query string: it always verifies,
  so a forged `Status=NOK` cannot cancel a paid payment. Refreshes are throttled.
- The sandbox (any merchant id works, the pay page needs no card) is refused when
  `BOBRES_ENV=prod`.
- Prices outside Zarinpal's limits (1,000 to 100,000,000 Toman) do not show the button.
- Since Sep 2026 Zarinpal accepts API calls only from up to 5 static IPs registered in
  its panel. `BOBRES_ZARINPAL_PROXY` routes the calls through a proxy on a registered
  (Iranian) server when the BOBRES server's IP cannot be registered.
- Buyers must pay with their VPN off (Shaparak limits internet payments to Iranian IPs),
  so the pay page and the return URL must be reachable from Iran without a VPN. Domains
  of VPN shops are often filtered; `BOBRES_ZARINPAL_PUBLIC_URL` can point at a separate,
  registered domain. Caddy serves it too (`BOBRES_ZARINPAL_HOST`, set by the installer),
  but only the pay page and the return URL: every other path is a 404 there, so no
  subscription links or admin panel live on a domain registered with a payment gateway.
  The payments service can also run in another region behind its own proxy.
- Caddy only sets `Referrer-Policy: no-referrer` where the upstream sets none, so the pay
  page's `origin` reaches Zarinpal; the pay links also carry `referrerpolicy="origin"`.

## Decisions the owner must take

1. **Third-party crypto.** Crypto Pay (@CryptoBot) states it is unavailable to anyone
   located in, resident of or a citizen of Iran; Oxapay excludes businesses registered in
   Iran; NOWPayments excludes sanctioned countries. Building on them for an Iranian store
   breaks their terms. Recommendation: skip third-party crypto and bring the self-hosted
   chain watcher (PLAN.md Phase 6: watch the operator's own TRC20/TON addresses, no third
   party) forward as the automated crypto method. Manual TXID approval stays.
2. **Zarinpal risk.** A 2021 Shaparak directive (as reported by the press) told payment
   facilitators to cut merchants selling VPNs; Zarinpal's terms forbid selling anything
   contrary to law; Iran criminalised selling VPNs. Ask Zarinpal support whether they
   would keep such a terminal and whether a non-Iranian egress IP can be registered.
   The code is ready and off by default.
3. **Telegram's terms (Bot Developer ToS 6.2).** Digital goods sold inside Telegram must
   be paid exclusively with Stars; a bot using other payment systems can be hidden from
   store builds or terminated. That covers card-to-card, crypto and Zarinpal inside the
   bot (Phase 1 included). Stars is the compliant option; the rest is a business risk.

## Configuration

| Where | Key | Meaning |
|---|---|---|
| core setting | `payments.stars_rate` | Toman per Star; empty = no Stars |
| payments env | `BOBRES_ZARINPAL_MERCHANT_ID` | enables Zarinpal |
| payments env | `BOBRES_ZARINPAL_SANDBOX` | sandbox host (tests; refused in prod) |
| payments env | `BOBRES_ZARINPAL_PROXY` | http(s) proxy with a registered IP |
| payments env | `BOBRES_ZARINPAL_PUBLIC_URL` | registered domain for the pay page and return URL |
| caddy env | `BOBRES_ZARINPAL_HOST` | that domain's host when it differs from `BOBRES_DOMAIN` |

Installer flags: `--zarinpal-merchant-id`, `--zarinpal-proxy`, `--zarinpal-public-url`.

## Tests

- Domain: start-once, settle-once under 8 concurrent reports, mismatch, cancel, expiry,
  reconciler backoff, every Stars rule.
- Zarinpal adapter and pages against `internal/zpfake` (real envelopes, 100 then 101).
- Bot: full Stars and Zarinpal purchases through the in-process backend.
- CI end-to-end: a Stars purchase through the real stack, provisioned on a real 3x-ui.
