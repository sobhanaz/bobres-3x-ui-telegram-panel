# IDPay and NextPay

Two more Iranian card gateways, built exactly like Zarinpal (ROADMAP item 3). Both are **off until the owner sets
credentials and switches them on** in Payment gateways.

> **Not verified against the real gateways.** Both are implemented to the gateways' public documentation
> (IDPay REST v1.1, NextPay direct web service) and tested only against fakes in this repository
> (`internal/idpayfake`, `internal/nextpayfake`). Nobody has yet made a payment through either of them. Do the
> checklist at the end with a small real payment before telling customers about them.

## How a payment goes

1. The customer picks "Pay online (IDPay)" or "(NextPay)" in the bot. Core asks payments to start a payment;
   payments calls the gateway's API and gets back a link. The bot shows that link (the gateway's own page; only
   Zarinpal goes through BOBRES's `/pay/<id>` page, because only Zarinpal checks the Referer).
2. The customer pays on the bank page and is sent back to `/webhooks/idpay` or `/webhooks/nextpay` on the
   public URL (`BOBRES_ZARINPAL_PUBLIC_URL`, else `BOBRES_PUBLIC_URL`), by GET or POST.
3. **The callback is never trusted.** Only the payment's gateway id is read from it, to find our payment. What
   settles it is a call to the gateway's verify API from payments. The amount the gateway reports must equal what
   we asked for; a mismatch fails the payment and the order is not paid.
4. A second callback (or the reconciler checking an open payment) is harmless: a payment settles once.
5. Callbacks are rate limited and their body is capped. A payment left open is checked by the reconciler and
   expires like a Zarinpal one.

Amounts: prices are in Toman; the gateways are asked for Rial (Toman x 10), as for Zarinpal.

| | IDPay | NextPay |
|---|---|---|
| Credential | API key (36 characters, from the IDPay panel) | API key (from the NextPay panel) |
| Per payment | 100 to 50,000,000 Toman | 1,000 to 100,000,000 Toman |
| Sandbox | yes (never when `BOBRES_ENV=prod`) | none |
| Callback | `/webhooks/idpay`, GET or POST | `/webhooks/nextpay`, GET (`trans_id`, `order_id`, `amount`) |
| Verify window | 10 minutes after payment, or the money returns to the payer (error 54) | 10 minutes, same rule |

The API key is stored encrypted (like the Zarinpal merchant id), is write-only in the dashboard (shown only as
"set"), and never appears in logs, events or the support bundle. An optional proxy (an egress IP registered with
the gateway) works as for Zarinpal.

## Setup

In the dashboard, Payment gateways > IDPay (or NextPay): paste the API key, optionally a proxy (and the sandbox for
IDPay, outside prod), use **Test** to check the key without saving, then switch on. On the first start the values
can also come from `BOBRES_IDPAY_API_KEY`, `BOBRES_IDPAY_SANDBOX`, `BOBRES_IDPAY_PROXY`, `BOBRES_NEXTPAY_API_KEY`
and `BOBRES_NEXTPAY_PROXY` (commented in `deploy/.env.example`); after that the dashboard owns them. Replacing a
key is refused while payments made with the old one are open.

A license must name the gateway (`idpay`, `nextpay`; the free tier names both): see [05-license.md](05-license.md).

Network: core has no internet. Payments (on the `egress` network) makes every call: `api.idpay.ir` for IDPay and
`nextpay.org` for NextPay. If you filter outbound traffic by host, allow those two. Caddy serves the two callback
paths on the same hosts as `/webhooks/zarinpal` and `/pay/*` (the payments hostname), not on the dashboard host's
admin routes.

The system alerts watch them like Zarinpal: `IDPay is failing` / `NextPay is failing` (a verify error after the last
paid payment within the hour) and `... payments stuck` (one open for over 3 hours).

## Owner checklist (by hand, once)

- [ ] **Register the callback domain** in each gateway's panel: the host of `BOBRES_ZARINPAL_PUBLIC_URL` (or
      `BOBRES_PUBLIC_URL`). IDPay refuses a callback whose domain does not match (error 38).
- [ ] **IDPay: set the callback method** (POST or GET) in the panel. Both are accepted; confirm which one your panel
      sends and that the payment completes.
- [ ] **NextPay: confirm the callback parameters** really are `trans_id`, `order_id` and `amount` on a real payment.
- [ ] Pay a small real amount through each gateway and check: the order is paid once, the amount in the dashboard
      matches, a repeated callback (reload the return page) changes nothing.
- [ ] **Verify promptly.** The reconciler verifies, but if payments was down for more than 10 minutes after the
      customer paid, the gateway returns the money (IDPay error 54; NextPay likewise). The customer is not charged;
      the order stays unpaid.
- [ ] NextPay repeated verify answers `-49` mean the payment was already verified: expected for a second callback.
- [ ] The server's public IP address is allowed in each panel if the gateway restricts by IP (or set a proxy).
- [ ] IDPay sandbox: only on a non-prod install; the sandbox flag is refused when `BOBRES_ENV=prod`.

If one of these differs from what is written above, the code follows the public documentation and needs fixing:
the gateway packages are `internal/payments/gateway/idpay` and `.../nextpay` (each has the facts it relies on at
the top of its file).
