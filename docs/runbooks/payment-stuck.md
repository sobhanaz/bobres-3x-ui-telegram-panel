# Runbook: payment stuck

A customer says they paid, but the bot still waits for the payment, or the order stays "Awaiting payment". Commands
run on the server as root; `dc` and `bsql` are defined in the [runbooks README](README.md#before-you-start).

Not this runbook: the payment went through and the order shows **Paid**, **Delivering** or **Delivery failed** but
no service arrived. That is a delivery problem: [provisioning backlog](provisioning-backlog.md).

## Symptoms

- The customer has a receipt or a bank message, the bot says "Not confirmed yet", and the order on the dashboard
  (Orders & payments, the order's page, section Payments) shows the payment as **Not paid** or **To review**.
- The **Overview** shows "Payments to review" piling up, or the Payment gateways tile shows **Problem**.
- The Payment gateways page shows "The last attempt failed" with a last error, or "{n} paid payments are waiting to
  be confirmed".
- The bot alerted staff: "A Stars payment was not recorded".

## How a payment settles

| Method | Settles when |
|---|---|
| Card to card, crypto (USDT), Zarinpal link | A staff member **approves** the receipt in the review queue. Nothing is automatic |
| Zarinpal (online) | The customer comes back from the bank page, or the payments service asks Zarinpal itself: every 30 seconds for a new payment, less often as checks pile up (at most 4 minutes apart while it can still be paid). The customer's "I paid, check" button asks at once |
| Telegram Stars | Telegram confirms it to the bot, which records it at once |

A Zarinpal payment that is not confirmed expires 45 minutes after its pay window. For **48 hours** after it was
started, it is still settled if Zarinpal confirms it: when the customer taps "I paid, check", or comes back from the
bank page. After that, or when the amount did not match, only a person can settle it.

## Checks

1. Find the order: Orders & payments, search by order id, Telegram id or @username. Open it: the Payments section
   lists every attempt with its method and state.

2. **Manual methods** (state **To review**): open the **Review queue** tab. "Seen before" means another receipt has
   the same reference number: compare the photos.

3. **Zarinpal**: on the **Payment gateways** page (owner), look at the Zarinpal card:
   - **Test connection**: "Zarinpal answered" means the merchant id and the route work; "could not be reached or
     refused these details" means calls to Zarinpal fail, and no payment can be confirmed until they work.
   - A problem line ("The stored settings cannot be read", "The sandbox is refused", "The public payment address is
     not right") says what to fix.

   Then the payments service's view:

   ```sh
   bobres logs --tail 300 payments | grep -i -E 'gateway check failed|reconcile|zarinpal'
   bsql "SELECT id, status, amount, created_at, checked_at, check_attempts, failure_reason
         FROM payments.payment_intents
         WHERE provider = 'zarinpal' AND status IN ('pending','confirming') AND created_at < now() - interval '15 minutes'
         ORDER BY created_at LIMIT 20;"
   ```

   And everything Zarinpal answered for one payment (the id from the order page or the query above; card numbers and
   tokens are never stored there):

   ```sh
   bsql "SELECT created_at, kind, outcome, detail FROM payments.gateway_events
         WHERE intent_id = '<payment id>' ORDER BY created_at;"
   ```

4. **Paid in payments, not in core.** The payment shows **Paid** but the order still waits: the payment event did not
   reach core.

   ```sh
   bsql "SELECT max(id) AS newest_event FROM payments.outbox_payments;"
   bsql "SELECT consumer, last_id, updated_at FROM payments.outbox_payments_cursors;"
   bsql "SELECT created_at, topic, left(error, 200) AS error FROM core.dead_letters
         WHERE source = 'payments' ORDER BY created_at DESC LIMIT 10;"
   bobres logs --tail 200 core | grep -i -E 'event consumer|dead-lettered|event failed'
   ```

   The `core` cursor's `last_id` should reach `newest_event` within seconds. A dead letter is an event core gave up
   on.

## Fixes

**Manual receipt waiting.** Review queue > the payment > **Approve** (the order is paid and delivered at once, or the
wallet topped up) or **Reject** with a reason the customer sees in the bot. Turn on the staff notification "New payment
to review" (Settings > Staff notifications) so receipts do not wait unseen.

**Zarinpal cannot be reached** (test fails, `gateway check failed` in the log):
- Zarinpal answers only registered server IPs (at most 5, in the Zarinpal panel). Register this server's IP, or set a
  **Proxy** on a registered server in Payment gateways.
- The merchant id was changed or suspended at Zarinpal: enter the right one in Payment gateways (owner).
- "The stored settings cannot be read": the payments master key changed. Enter the merchant id again, or put the old
  `BOBRES_PAYMENTS_MASTER_KEY` back (see [rotate secrets](rotate-secrets.md)).

Once the test passes, the payments service confirms the waiting payments by itself within minutes. Ask customers whose
payment expired to tap **"I paid, check"** in the bot, within 48 hours of starting the payment.

**Over 48 hours, or "amount_mismatch".** Look the payment up in the Zarinpal panel (amount, card, reference). If the
money arrived: credit the customer by hand (Users > the customer > Wallet > **Change balance**, with the Zarinpal
reference as the reason); they buy from the wallet. If the amount was wrong, refund it in the Zarinpal panel instead.
Never edit the database.

**Stars payment not recorded** (the staff alert names the user, the Stars and the charge id): credit the customer by
hand with Change balance (the alert's charge id as the reason). BOBRES cannot refund Stars yet.

**Paid in payments, not in core:**
- The cursor lags and core's log shows `event consumer step failed`: check that payments is healthy (`bobres status`);
  core retries with backoff and catches up on its own once payments answers. `dc restart core` if it does not move
  within a few minutes.
- A dead letter for the payment: do not credit by hand yet (the event may still apply). Escalate with the dead
  letter's row.

## Escalate when

- Zarinpal's test passes but payments stay **Not paid** after the customer tapped "I paid, check".
- A payment event is in `core.dead_letters`, or core's cursor does not move with payments healthy.
- The same Zarinpal payment shows twice, or a customer was credited twice.

Send: the order id, the payment id, the `gateway_events` rows of that payment, and `sudo bobres support-bundle` (read
it first).
