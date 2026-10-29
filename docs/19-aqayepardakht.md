# Aqayepardakht (آقای‌پرداخت)

A fourth Iranian card gateway, built like Zarinpal, IDPay and NextPay ([16-idpay-nextpay.md](16-idpay-nextpay.md)). It is
**off until the owner sets the gateway's PIN and switches it on** in Payment gateways.

> **Not verified against the real gateway.** It is implemented to the gateway's public documentation (web service
> version 2, <https://aqayepardakht.ir/api/>, the page dated 5 Shahrivar 1405) and tested only against a fake of it in
> this repository (`internal/aqayepardakhtfake`). Nobody has yet made a payment through it. Do the checklist at the end
> with a small real payment before telling customers about it.

## How a payment goes

1. The customer picks "Pay online (Aqayepardakht)" in the bot or the Mini App. Core asks payments to start a payment;
   payments calls `POST /api/v2/create` and gets a `transid`. The customer is sent to **BOBRES's own `/pay/<id>` page**,
   which links to `https://panel.aqayepardakht.ir/startpay/<transid>`. The gateway refuses a payment whose browser does
   not carry the terminal's registered domain as the Referer (its error 16), as Zarinpal does, so a plain link would not
   do; the page sends the origin as the Referer.
2. The customer pays on the bank page and is sent back to `/webhooks/aqayepardakht` on the public URL
   (`BOBRES_ZARINPAL_PUBLIC_URL`, else `BOBRES_PUBLIC_URL`). BOBRES asks for a `GET` (`callback_method`, which the
   gateway's documentation recommends); a `POST` is read too. The callback carries `transid`, `cardnumber` (masked),
   `tracking_number`, `invoice_id` (our payment's id), `bank` and `status` (1 paid, 0 not).
3. **The callback is never trusted.** Only the `transid` is read from it, to find our payment (and the `invoice_id`,
   when it comes, must be that payment's: a callback naming another payment is refused, not verified). What settles it
   is `POST /api/v2/verify` from payments, with the amount BOBRES holds: the gateway refuses an amount that differs from
   the payment's (error 10), so its `success` answer with code `1` is the gateway confirming the amount. A mismatch or
   any other error settles nothing.
4. A second callback, a refreshed return page or the reconciler checking an open payment is harmless: a payment settles
   once. If a verify's own answer was lost, the next one says "verified before" (error code `2`); that is the same
   payment, paid, and it settles once in the database.
5. Callbacks are rate limited and their body is capped. A payment left open is checked by the reconciler and expires like
   a Zarinpal one. The gateway has one "not completed" answer (code `0`) for a payment not paid yet and one the customer
   cancelled, so a customer who came back unpaid fails the payment (nothing is credited); a verify that later finds it
   paid reopens it, as for every gateway.

Amounts: prices are in Toman; payments holds Rial like for every gateway and the client converts (Rial / 10) on the wire,
because the gateway takes Toman.

| | Aqayepardakht |
|---|---|
| Credential | the gateway's **PIN** (case-sensitive; 4 to 100 printable characters, no spaces) |
| Per payment | 1,000 to 400,000,000 Toman |
| Sandbox | the PIN `sandbox` (never when `BOBRES_ENV=prod`); there is no switch |
| Callback | `/webhooks/aqayepardakht`, GET (asked for) or POST: `transid`, `cardnumber`, `tracking_number`, `invoice_id`, `bank`, `status` |
| Verify | `POST /api/v2/verify {pin, amount, transid}`: `success` + `1` paid; error `0` not completed; error `2` verified before |

The PIN is stored encrypted (like the other gateways' keys), is write-only in the dashboard (shown only as "set"), and
never appears in logs, events or the support bundle. An optional proxy (an egress IP registered with the gateway) works
as for Zarinpal.

## Setup

In the dashboard, Payment gateways > Aqayepardakht: paste the PIN, optionally a proxy, use **Test** to check the PIN
without saving, then switch on. On the first start the values can also come from `BOBRES_AQAYEPARDAKHT_PIN` and
`BOBRES_AQAYEPARDAKHT_PROXY` (commented in `deploy/.env.example`); after that the dashboard owns them. Replacing the PIN
is refused while payments made with the old one are open. The PIN `sandbox` is the gateway's test system: a test payment
needs no real card, so it is refused when `BOBRES_ENV=prod`, in the dashboard, in the environment and in the stored
settings.

**Test** verifies a transaction that cannot exist and reads the answer: "not found" means the PIN was accepted, "PIN is
wrong" means it was not. It creates and settles nothing. (The documentation does not say whether the gateway checks the PIN
or the transaction first; if it looks the transaction up first, a wrong PIN passes the test and the first real payment
shows it.)

A license must name the gateway (`aqayepardakht`; the free tier names it): see [05-license.md](05-license.md).

Network: core has no internet. Payments (on the `egress` network) makes every call: `panel.aqayepardakht.ir`. If you
filter outbound traffic by host, allow it. Caddy serves the callback path on the same hosts as `/webhooks/zarinpal` and
`/pay/*` (the payments hostname), not on the dashboard host's admin routes.

The system alerts watch it like Zarinpal: `Aqayepardakht is failing` (a verify error after the last paid payment within
the hour) and `... payments stuck` (one open for over 3 hours).

## Owner checklist (by hand, once)

- [ ] **Register the callback domain** in the gateway's panel: the host of `BOBRES_ZARINPAL_PUBLIC_URL` (or
      `BOBRES_PUBLIC_URL`) must be the domain the terminal was issued for. The gateway refuses a callback on another domain
      (error 15) and a payment page opened without that domain as the Referer (error 16).
- [ ] Put the PIN in, press **Test**, then pay a small real amount and check: the order is paid once, the amount in the
      dashboard matches, a repeated callback (reload the return page) changes nothing.
- [ ] **Confirm the callback parameters** really are `transid`, `invoice_id` and `status` on a real payment, by GET.
- [ ] Abandon one payment at the bank page: the order must stay unpaid and nothing is credited.
- [ ] The server's public IP address is allowed in the panel if the gateway restricts by IP (or set a proxy).
- [ ] The sandbox: with the PIN `sandbox` on a non-prod install, a test payment settles in the dashboard; on a prod
      install the PIN is refused.

## Good to know

- The gateway's PHP SDK on GitHub (`aqayepardakht/php-sdk`) talks to a newer address, `https://api.aqayepardakht.ir/v3/`,
  with `pay` and `verify` calls and a `tracking_code`. That version is not described on the public documentation page, so
  BOBRES uses the documented version 2. If version 2 is ever switched off, the gateway package
  (`internal/payments/gateway/aqayepardakht`) is the one place to change.
- The documentation lists its error codes both negative and positive. A verify's positive `2` is "verified before"; the
  error table's `-2` ("the PIN is empty") is an error and is never read as a payment.

If one of these differs from what is written above, the code follows the public documentation and needs fixing: the
gateway package is `internal/payments/gateway/aqayepardakht` (it has the facts it relies on at the top of its file).
