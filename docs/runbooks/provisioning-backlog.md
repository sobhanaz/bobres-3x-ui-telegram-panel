# Runbook: provisioning backlog

Paid orders are not turned into VPN services: new purchases, renewals or traffic top-ups wait. Commands run on the
server as root; `dc` and `bsql` are defined in the [runbooks README](README.md#before-you-start).

## Symptoms

- The **Overview** shows "Failed deliveries" (paid orders the panel did not create yet) above zero.
- Staff got the bot alert "Provisioning failed 3 times for order ... (user ...)" with the error.
- Customers got "Preparing your service is taking longer than usual" (sent with that alert).
- On Orders & payments, orders stay **Paid**, **Delivering** or **Delivery failed**. The order's page, section
  Delivery, shows the attempts, the next try and the last error.

## How delivery works

Core's delivery worker takes paid orders one at a time and asks the provisioner, which creates or extends the client
on a 3x-ui panel. A failed attempt is tried again after 30 seconds, then 1, 2, 5 and 10 minutes, then every 30
minutes, **with no end**: an order is never dropped. The alert goes out once, at the third failure. A retry of a new
service stays on the panel its first attempt chose (so a customer never gets two accounts); renewals and top-ups go
to the panel the service is on. An order stuck in **Delivering** for over 2 minutes (core restarted mid-call) is
picked up again. Every step is safe to repeat.

How much load one install takes, and the queries to watch before a backlog builds up:
[Load test](../12-load-test.md#what-to-watch-in-production).

## Checks

1. Are the services up? `bobres status`: core and provisioner must be running and healthy.

2. How big is the backlog, and why:

   ```sh
   bsql "SELECT status, count(*), min(updated_at) AS oldest FROM core.orders
         WHERE status IN ('paid','provisioning','provision_failed') GROUP BY status;"
   bsql "SELECT left(last_error, 90) AS error, count(*) FROM core.orders
         WHERE status = 'provision_failed' GROUP BY 1 ORDER BY 2 DESC;"
   bsql "SELECT id, type, provision_attempts, next_attempt_at, left(last_error, 120) AS last_error
         FROM core.orders WHERE status = 'provision_failed' ORDER BY updated_at LIMIT 20;"
   ```

3. The logs:

   ```sh
   bobres logs --tail 300 core | grep -i 'provisioning'
   bobres logs --tail 300 provisioner | grep -i -E 'warn|error'
   ```

4. The panels: **3x-ui servers** page, column Health. Any "Not answering": follow [3x-ui down](xui-down.md) first.

## Fixes by error

The last error reads like `provisioner.CreateClient: rpc error: code = FailedPrecondition desc = <reason>`; the
reason is what matters.

| The reason | Meaning | Fix |
|---|---|---|
| `no 3x-ui server: add one in the dashboard (Servers)` | No panel at all | Add one on the 3x-ui servers page |
| `no enabled and healthy 3x-ui server` | Every panel is off or failing its health check | [3x-ui down](xui-down.md); or switch a healthy panel's "Takes new services" on |
| `none of the candidate 3x-ui servers is enabled and healthy` | The plan may use only certain panels (Plans page), and none of them works | Fix those panels, or let the plan use another one (Plans > the plan > servers) |
| `server "<name>" is full (N of N clients): ...`, `every server a new client may go to is full (N servers, each at its client limit): ...` | Every server the plan may use has reached its **Service limit** (Servers > Edit). The services they hold keep working; only new ones wait. The watchdog's daily "server is full" notice says the same | Raise the limit of a server (or empty the field for no limit), or add a server, or let the plan use another one. The waiting orders are delivered by the next retry, or press **Retry delivery now** |
| `the 3x-ui server is disabled: it takes no new clients` | The first attempt chose a panel that was switched off since | Switch "Takes new services" back on until these orders are delivered, or refund them |
| `the panel has no enabled inbound to attach clients to` | No inbound is both on in the panel and chosen for new services | 3x-ui servers > the panel > Inbounds: choose one (and turn it on in the panel) |
| `panel create failed`, `panel client failed`, `panel inbounds failed`, `panel set limits failed` | A call to the panel failed | The provisioner's log says why: `bobres logs --tail 300 provisioner \| grep '3x-ui call failed'` (a refused token, a timeout, a refused connection). Then [3x-ui down](xui-down.md) |
| `xui: panel URL must use https ...`, `xui: plain http is allowed only to private ...`, `xui: panel address is not allowed ...` | The panel's address breaks the address policy (the token must never cross the internet in plain http; private addresses need the owner's consent) | Edit server: use the panel's https address, or (owner, for a panel on this server or a private network) turn on "Panel on a private network" |
| `another client on the panel already uses this email` | Someone created a client with BOBRES's email (`u<telegram id>-<order id>`) on the panel by hand | Escalate; do not delete clients on the panel by guess |
| `the service is ...` (deleted, disabled) | A renewal or top-up for a service that cannot take it | Refund the order (below) |
| `code = Unavailable`, `connection refused` without a panel operation | Core cannot reach the provisioner | `bobres status`; `dc restart provisioner`; then `bobres logs --tail 100 provisioner` |

After fixing the cause:
- Wait: every failed order is tried again within 30 minutes on its own.
- Or deliver one now: the order's page > **Retry delivery now** (orders in **Delivery failed**). There is no bulk
  retry; the worker clears the rest within 30 minutes.
- An order that cannot be delivered (the panel is gone for good, the service was deleted): the order's page >
  **Refund to wallet**. The order is cancelled, a half-made panel client is removed, the money goes to the wallet
  and the customer is told; they buy again.

Never mark an order delivered by hand or create the client in 3x-ui yourself for a waiting order: the worker would
create a second one.

While a panel is down for long and it is your only one, consider **maintenance mode** (Settings > Maintenance mode):
customers see the maintenance message instead of buying services that cannot be delivered. Active services keep
working and payments under way finish.

## Escalate when

- Orders stay **Paid** (never tried) while core and the provisioner are healthy.
- The same error comes back on a panel that passes its Test connection.
- A customer got two accounts for one order, or a delivered order has no client on the panel.

Send: an order id, the `last_error` query's output, and `sudo bobres support-bundle` (read it first).
