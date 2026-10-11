# Phase 3: retention (design and status)

Written 2026-10-06. PLAN.md section 10: "Retention: renew, traffic top-up, discounts,
referrals, expiry/usage notifications." All five are built; the choices below are defaults the
owner can change.

## Renewals and traffic top-ups

- **Orders.** A renewal or top-up is an order (`type` renew / traffic_topup) that names the
  subscription it extends (`orders.subscription_id`; a new order never has one).
- **What a renewal sells.** A renewal takes a regular plan. Its days are added to the time left,
  counted from now once the service expired. Its traffic is added to the quota, so unused traffic
  carries over.
- **What a top-up sells.** A top-up takes a traffic package: a plan with `is_topup`
  (`/topup_add price currency GB name | نام`). It adds traffic only, and only to a running service
  with a traffic limit; an expired one must be renewed.
- **Retries never add twice.** The limits an order sets are computed once, when the worker first
  claims it, and stored on the order (`target_expires_at`, `target_traffic_bytes`). The
  provisioner's `SetClientLimits` then sets those **absolute** values and enables the client, so a
  retry after a crash or panel error never adds twice.
- **Why not 3x-ui's bulkAdjust.** It adds relative days and bytes, and keeps an expired client
  expired.
- **How the update works on the panel.** `clients/update` replaces the whole row, so the
  provisioner sends the stored client back with only the limits changed.
- **Checked on a real panel.** On 3x-ui v3.9.0 the client keeps its subscription id and share
  links after the update. CI checks the same on v3.8.5 and v3.9.0 (integration test, e2e
  renewal).
- **Owner decision, default chosen:** carry-over rather than "reset usage on renewal".

## Usage sync and reminders

- **Sync.** Core's usage worker syncs every delivered subscription with the panel every 10
  minutes, 50 per 30-second tick. It reads the traffic used and the panel's limits, which an
  operator may have changed there. A start-on-first-use expiry set in the panel is left to the
  panel.
- **Reminders.** Each goes out once per period, with Renew / Add traffic buttons:
  - 3 days before expiry
  - 1 day before expiry
  - at 80% of the traffic used
  - when the service expires or runs out
- **New periods.** A renewal or top-up starts a new period. A reminder flag whose condition no
  longer holds (extended in the panel) is cleared.
- **Panel down.** When the panel does not answer, the time-based reminders still go out from
  core's own dates.
- **Old endings.** A service that ended more than 3 days ago is recorded without a message
  (e.g. services that ended before this release).
- **Bug fixed on the way.** A retried CreateClient (response lost after the client was mapped)
  reported zero limits, which core stored as "never expires". It now reports the panel's limits.

## Discount codes

- **Shape.** A percentage (1-100) or a fixed amount in one currency, with optional max uses and
  expiry. Each customer can use a code once.
  - `/discount_add SPRING20 20% 100 30` (20% off, 100 uses, 30 days)
  - `/discount_add GIFT 50000 IRT 10`
  - `/discount_off CODE`, `/discounts`
- **Checkout.** Every checkout (new, renewal, top-up) has "I have a discount code". `QuoteOrder`
  checks it and the menu shows the reduced price. The order stores the code and the amount taken
  off; its amount is what the customer pays.
- **Counting uses.** A use is counted when the order is paid, in the same transaction. Orders
  created before the last use was paid can push a capped code a little over its limit; their price
  stands. 100% off makes a free order (paid at once, delivered, its use counted).

## Referrals

- **Invite codes and links.** Every user gets an 8-character invite code on first request. The
  link is `t.me/<bot>?start=r_<code>`.
- **Linking.** A new user who arrives through a link is linked to the inviter when they pick a
  language and are created. Existing users are never re-linked.
- **Reward.** The inviter gets `referral.reward_percent` of the invited user's first paid purchase
  in their wallet. It only counts purchases made while the program is on, never trials or free
  orders. It is a ledger credit with a per-invited-user key, so it is paid once.
- **Visibility.** The program and its "Invite friends" button are off while the setting is empty
  or 0: `/set referral.reward_percent 10`.

## Bot

- **One checkout menu.** Every purchase goes through one menu saved under its nonce
  (`pay:<method>:<nonce>`): what is bought, for which service, with which code. Buttons stay under
  Telegram's 64 bytes. Menus sent before this release (`pay:<method>:<plan>:<nonce>`) keep
  working.
- **Service page.** It shows traffic used with a bar, plus Renew and Add traffic.

## Not done (later)

- Per-plan renewal prices, automatic renewal from the wallet, reseller discounts.
- A configurable reminder schedule (3 days / 1 day / 80% are constants).
- Bulk usage reads (one panel call per subscription today; fine for hundreds of services).
