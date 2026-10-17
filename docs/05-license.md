# 05 - License and entitlements

Goal: one mechanism for trial / one-time / subscription / per-server / add-ons, without code changes.
Honest limit: any self-hosted software can be tampered with. The license deters casual sharing and
enables billing. The free tier gets every update (security updates are never withheld), so the value that is sold is
the premium features (resellers, automatic USDT, more servers, services and staff), white label and support, not
access to updates.

**Policy: free core, paid premium** (decided 2026-10-14). An install without a valid license is not blocked. It runs
on a **free tier**, and a 30-day **implicit trial** protects every install when it first runs a version with this
policy. A license unlocks more. The limits only refuse **new** actions: what exists keeps working (see "Free core,
paid premium" below). This replaces the earlier policy, under which a missing or invalid license gated nothing.

Status (2026-10-14): verification, storage, the free tier and the trial, the dashboard's License tab, the CLI and the
vendor's signing tool are built. Online activation with a license key and the daily refresh are built too, against the
vendor's license server ([13-license-server.md](13-license-server.md)). The server is built but must still be deployed
by the owner (`docs/launch-checklist.md`); hand-signing with `tools/license` works without it.

**The vendor's public key is embedded.** Licenses signed by the key id `vendor-v1-2026` verify, and an install accepts
them. The key's fingerprint is `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7`
(`internal/license/keys/vendor-v1-2026.pub`; `go run ./tools/license fingerprint` prints it). Only the public half is in
the repository: the private half stays with the vendor, outside it.

## Token format
`base64url(payload JSON) "." base64url(Ed25519 signature of those payload bytes)`, both without padding
(`internal/license`). The payload is never parsed before a signature verifies. Version 1:

```json
{
  "v": 1,
  "kid": "vendor-v1-2026",
  "license_id": "lic_...",
  "customer": "cust_... or a name",
  "install_id": "the install id from the License tab (optional binding)",
  "domain": "panel.customer.com",
  "tier": "trial|standard|pro",
  "issued_at": "2026-10-11T00:00:00Z", "expires_at": "2027-10-11T00:00:00Z", "grace_days": 14,
  "entitlements": {
    "white_label": false,
    "max_servers": 1,
    "max_active_services": 500,
    "resellers": true,
    "gateways": ["zarinpal", "idpay", "nextpay", "stars", "manual_card", "manual_crypto", "manual_zarinpal"],
    "crypto_watcher": false,
    "dashboard_staff_seats": 3,
    "updates_until": "2027-09-30"
  }
}
```

- `kid` names the vendor key that signed it; it must be the key that verifies (rotation).
- Limits: `-1` is unlimited and `0` (or a missing field) is none, so a forgotten limit never grants unlimited use. A
  license never leaves less than the free tier, though: each limit it sets or omits is raised to the free tier's
  where that is larger (see "Free core, paid premium"), so a license that forgets `max_active_services` still sells
  25 services. `tools/license sign` and `licensesrv key create|update` print a WARNING ("max_active_services is 0 or
  missing: the install will floor it at the free tier's 25") when they make such a license, and still make it.
- `grace_days` is 0..90; `expires_at` must be after `issued_at`; `issued_at` gets 5 minutes of clock skew.
- Gateway names: `zarinpal`, `idpay`, `nextpay`, `stars`, `usdt_trc20_auto`, `manual_card`, `manual_crypto`, `manual_zarinpal`. The
  wallet is always allowed. Automatic USDT (`usdt_trc20_auto`, Phase 6a) also needs the `crypto_watcher` feature.
- `updates_until` (YYYY-MM-DD) is the end of the updates the license was sold with, and it is **advisory**. The
  release's day is the `# issued` line of its signed `checksums.txt` (UTC). `bobres update`, its `--check`, the menu,
  the dashboard's Updates tab and core's answer for the bot's update check add an informational note ("this release is
  newer than your license's update date") when a release was issued after it, and **never refuse, hide or withhold an
  update**: the free tier, the trial and an ended license get every release, so a license that stops at a date must
  not get less than no license (06-installer.md, "License coverage"). Running versions are never affected. Only a
  license in force (active or in its grace period) has an `updates_until` to compare: **security updates are never
  withheld** from any install.

## Binding
- **Domain (required):** core accepts only tokens whose `domain` equals the store's `BOBRES_DOMAIN`
  (lower case, no port or trailing dot). Moving to a new domain needs a new token.
- **Install id (optional):** `core.install` holds one random id, made when the core schema is created
  (migration 00012). A token that names `install_id` works only on that install. The License tab and
  `bobres license status` show the id to send the vendor.

## States
The state describes the installed token. What applies to the install is a separate question (next section).

| State | Meaning | What applies |
|---|---|---|
| `missing` | no token installed | the trial, then the free tier |
| `invalid` | does not verify here (`reason`: `unknown_key`, `malformed`, `bad_signature`, `version`, `invalid`, `domain`, `install_id`, `unbound`, `not_yet_valid`) | the same: it counts as no license |
| `active` | before `expires_at` | the license's entitlements |
| `grace` | expired, inside `grace_days` | the license's entitlements, with a warning |
| `expired` | past grace | the trial, if it still runs, else the free tier |

`unknown_key` appears only on a build that carries no vendor key at all (a development build). A release carries
`vendor-v1-2026`, so there a token that none of the built-in keys signed (a changed token, or one signed by a key this
version does not carry yet after a rotation) is `bad_signature`.

## Free core, paid premium
`Service.Effective` (`internal/core/domain/license.go`, over `license.Resolve` in `internal/license/policy.go`) is the
one answer to "what may this install do now". Every gate, the dashboard, the bot's refusals and `bobres license status`
read it. It takes the first that applies:

1. a license that verified for this install and is `active`: its entitlements, never lower than the free tier;
2. such a license in its `grace` period: the same;
3. the implicit **trial**, while it runs: the trial's entitlements;
4. otherwise (no license, one that does not verify, one past its grace period, the trial over): the **free tier**.

A license wins over the trial, and an ended license falls back to the trial while that still runs.

**A license is never below the free tier.** `Entitlements.AtLeast` raises the license's (and its grace period's)
entitlements to the free tier's: each numeric limit is the larger of the two (`-1`, unlimited, counts as infinite), the
gateway lists are united, and a feature is on when either has it. So a license `{max_servers: 2, resellers: true,
gateways: ["zarinpal"], dashboard_staff_seats: 3}` with no `max_active_services` still sells 25 services and offers the
manual methods, and an all-zero license is the free tier, not a lock. A license can still be lower than the trial
(fewer servers, no automatic USDT). The trial's own start is clamped to now when it lies in the future (a clock that
was set back, a restore), so it never has more than 30 days left, and core writes the clamped start back.

### The free tier
`license.FreeEntitlements()` holds the numbers in one place, as constants the owner may tune
(`FreeMaxServers`, `FreeMaxActiveServices`, `FreeStaffSeats`).

| Entitlement | Free tier |
|---|---|
| `max_servers` | 1 |
| `max_active_services` | 25 (active or about to expire) |
| `resellers` | no |
| `white_label` | no: "Powered by BOBRES" is shown |
| `crypto_watcher` | no: no automatic USDT |
| `dashboard_staff_seats` | 2 (the owner the server names, `BOBRES_ADMIN_TELEGRAM_ID`, takes none; every other staff member takes one, an owner too) |
| `gateways` | `zarinpal`, `idpay`, `nextpay`, `stars`, `manual_card`, `manual_crypto`, `manual_zarinpal`; the wallet is always allowed |
| `updates_until` | none: security updates are never withheld |

### The trial
Every install has **30 days** (`license.TrialDays`) from the first start of a version with this policy. A fresh install
and an upgraded one are treated alike: migration `00017` writes `core.install.trial_started_at` once, when it first
runs, and nothing updates it afterwards. The trial's entitlements (`license.TrialEntitlements()`): unlimited servers
and services, resellers, every payment method including automatic USDT (`crypto_watcher`), 5 staff seats. It does
**not** include white label, so the "Powered by BOBRES" mark stays during the trial. The trial is not tied to a
Telegram or email identity: a store is its install. When it ends nothing is deleted or switched off; the free tier's
limits start to apply to new actions. The CI and end-to-end installs have no license and run in their trial.

### What is gated, and what never is
Only **new** actions are refused, and only by the effective entitlements. A refusal is the same 409 as a valid license's
(next section).

| New action | Refused when |
|---|---|
| adding a 3x-ui server | the servers would pass `max_servers` |
| a new service: a new order, a free trial, a reseller's purchase | the active services, counting those on their way, are at `max_active_services` |
| a reseller's purchase of a new service | `resellers` is off (the reseller keeps their account and what they bought) |
| making a reseller | `resellers` is off |
| adding an admin, a support member or another owner | the seats are taken (only the owner the server names takes none) |
| switching a payment method on | it is not in `gateways`; automatic USDT also needs `crypto_watcher` |
| white label | not a switch: "Powered by BOBRES" shows unless the effective entitlements include `white_label`, which only a valid license can grant |

Never gated, over any limit: every customer's existing service (it keeps working, **renewing and topping up**), an
order made before the limit was reached and paid after it (the money is already in), staff who are already over the
seats (they keep their access, and may change roles without taking a new seat), existing resellers (their account, and
what they already bought), servers and payment methods (they keep working; one switched off cannot be switched on
again unless it is allowed), staff extensions of a service, backups, updates and security fixes.

**The services on their way count.** The active-services gate runs before payment, so counting only delivered
services would let many orders made together overshoot the limit once they are paid. `store.CountActiveServices`
counts the active and expiring services, the subscriptions being delivered (`pending`), the new orders paid or being
delivered that have no subscription yet, and the new orders awaiting payment made within the last 2 hours (unpaid
orders never expire, so counting all of them would pin places forever; one paid later is counted as a paid order).
The count and the insert of the order are one step: `CreateOrder` and `StartTrial` check it inside the order's
transaction, under a fixed advisory lock (`pg_advisory_xact_lock`), so 12 customers ordering at 24 of 25 get one place.
The same count is what the License tab and the CLI show as the services in use, and what ends the `license.limit` alert.

**Premium methods stop being offered, quietly.** The gates fire when something is switched on, so a method turned on
during the trial would otherwise work for ever. At the point of use, core leaves out of what the customer is offered
the payment methods the effective entitlements do not include: `PaymentMethods` (the list the bot builds checkout
from) and `CreatePaymentIntent` (a stale menu) skip automatic USDT without `crypto_watcher`, and any gateway outside
the effective `gateways`. A customer is told nothing about licensing (a stale menu gets the answer for a method that
is switched off). Payments already started still settle, and the USDT watcher keeps settling open deposits: only
the offer stops. A reseller's purchase of a new service is refused the same way once `resellers` is off.

## Enforcement points (core)
Core refuses a new action beyond the effective entitlements with 409 `{"error": "state", "license": "<gate>",
"message": "..."}` (the same shape for a license, the trial and the free tier). The message is for staff: it names the
limit and says to buy or renew a license, for example "the free tier allows 1 server: buy or renew a license to add
servers". The dashboard shows a sentence per gate in its language, with core's words as the detail.
- `resellers`: making a reseller without the `resellers` feature;
- `max_servers`: adding a 3x-ui server beyond the limit;
- `reseller_purchase`: a reseller's **new** purchase when the effective entitlements have no `resellers`;
- `max_active_services`: a **new** service (a new order, a free trial or a reseller's purchase) at the limit.
  A renewal, a traffic top-up, a staff extension and the payment of an order already made are never refused. A retry
  of a request that already made its order (the same idempotency key) is not a new service;
- `dashboard_staff_seats`: adding an admin, a support member or another owner beyond the seats. Every staff member but
  the owner the server names takes a seat (an owner too: promoting the two seat holders to owner leaves the count at
  two), and changing role between the three takes or frees none. Nobody is removed over a limit;
- `gateways`: switching a payment method on (Zarinpal, IDPay, NextPay or automatic USDT in Payment gateways, or a manual
  method's or Stars' switch from off to on) that the effective entitlements do not name;
- `crypto_watcher`: switching automatic USDT on without the `crypto_watcher` feature.

**What a customer sees.** A customer never sees licensing text. When a new purchase or a free trial is refused for
`max_active_services` (or a reseller's purchase for `reseller_purchase`), the gRPC answer to the bot is `FailedPrecondition` with the neutral message "new purchases are
paused" (no gate, no limit, no license), and the bot says in the customer's language: "New purchases are temporarily
unavailable. Please contact support." (`error.purchases_paused`). Every other refusal comes from a staff action.

**What staff see.** Core notes the refusal (`core.install.limit_hit_at`), and its watchdog raises one alert, `license.limit`
(bot text `alert.license_limit`, "A new purchase was refused: this install is at its limit of {limit} {name}. Buy or
renew a license to sell more."). It is announced when the first purchase is refused and at most once every 24 hours
while purchases keep being refused, with no 6-hour reminder and no "fixed" message. It closes quietly after 24 hours
without a refusal, or at once when a license raises the limit. A transient error reading the refused purchases leaves
an open `license.limit` notice as it is (the license scope is then "not covered"), rather than forgetting it. The
`license.expiry` alert (`alert.license_expired`) now says the install is on the free tier (or the trial).

**The trial's end.** The watchdog's `license.trial` check warns before the trial ends and once after it: for the last
7 days of the trial it raises `alert.trial_ending` ("The free trial ends in {days} days, on {date}...", warning), announced
when it starts and then reminded like an expiring license (after 6 hours, then every 24 hours, with the days left going
down); when the trial has ended and the install runs on the free tier (no license in the picture: one that ended says so
itself) it raises `alert.trial_ended` once (no reminders), kept on the System page for a week and then closed quietly.
A license installed meanwhile ends the notice quietly. The dashboard's chip and plan tag turn from "info" to "warn" in
the same last 7 days.

The License tab shows each entitlement in effect with its use (servers from the provisioner, active services, staff
seats, the payment methods switched on) and warns from 90 % of a limit.

## Where it lives
- `core.license` (one row: token, when, by whom, `source` and `refreshed_at`). The token is verified every time it
  is read against the public keys built into core (`internal/license/keys/<kid>.pub`), so a row proves nothing by
  itself. `source` says how the token in use came: `manual` (pasted or imported), `activation` (fetched with a
  license key) or `refresh` (renewed by the daily refresh, at `refreshed_at`; migration 00014).
- Dashboard (System page, License tab): `GET /api/v1/license` (system.read: owner and admin; it carries `source`
  and `refreshed_at`); `POST /api/v1/license/check`, `PUT /api/v1/license` and `POST /api/v1/license/activate
  {key, reason}` (license.write: the owner; a reason is required). The token is never answered back or audited:
  `license.set` keeps `license_id`, `kid`, `tier` and `expires_at`, before and after.
  The answer has `effective` (`basis`: `license`, `grace`, `trial` or `free`; `tier`; `trial_running`;
  `trial_ends_at`; `trial_days_left`; `trial_days`, how long the trial lasts; `contact`) and `entitlements`: what is in effect with this install's use of each
  limit. A check answers only for the token it checked (no `effective`). `/api/v1/me` carries
  `license: {state, white_label, powered_by, basis, tier, trial_days_left}`.
  The tab shows the plan in effect (Free tier, Trial with its days left, or the license's tier), the entitlements
  with usage against each limit, and a line telling the owner to contact the seller (or, when no contact is set, to ask
  the seller they got BOBRES from). The menu and the tab carry a chip for the free tier, the trial (with its days,
  warning from the last 7), or a license that was refused, is in grace or has ended; the chip's link is named
  "License: ..." for screen readers.
- The seller's contact is the owner's to set: `BOBRES_SELLER_CONTACT` in `.env` (a Telegram handle, an address, a few
  words; `sudo bobres config set BOBRES_SELLER_CONTACT @handle` recreates core), read by core
  (`config.Core.SellerContact`) and shown by the dashboard (`effective.contact`) and the CLI (`bobres license status`
  prints its "Buy or renew" row only when one is set; `license info --json` has `contact`). There is **no default**:
  `license.DefaultSellerContact` is empty, a made-up handle could belong to a stranger, and the owner of a build may
  put a build-time default there. With none set, the screens say "ask the seller you got BOBRES from".
- CLI: `bobres license status`, `bobres license import < token.txt` (stdin) and `bobres license activate`, which
  run `/app license status|info|import` in the core container. `status` shows the token and then what is in
  effect: `In effect`, `Trial ends` (or `Trial ended`), `Servers`, `Services` and `Staff seats` as the limit with the
  use (`25 (3 in use)`; the servers' use is known only to the provisioner, so the CLI leaves it out), `Gateways`,
  `Features` and the seller to contact. `license info --json` has the same under `effective`. An import or an
  activation is audited as `license.set` from the CLI.
- Only a token that verifies for this install and has not expired can be installed.

## "Powered by BOBRES"
Shown on the dashboard footer and the login page on **every install except one whose effective entitlements include
`white_label`**: the free tier, the trial, a license without `white_label`, and an expired license all show it. It is the
default, so it also fails open: when core cannot read the license, or an answer does not say, the mark is shown, never
hidden. No bot attribution. `/api/v1/me` carries `license: {state, white_label, powered_by, ...}` and the public
`/api/v1/brand` carries `powered_by`.

## Vendor flow
1. The signing key `vendor-v1-2026` exists. Its private half is a file **outside the repository** (it is never committed,
   copied into a checkout or put in an image); its public half is `internal/license/keys/vendor-v1-2026.pub`, built into
   every release. To make another key (a rotation), the tool refuses a path inside a git checkout and never overwrites a
   file: `go run ./tools/license keygen -kid vendor-v2-2028 -out ~/secure/vendor-v2-2028.key`. It prints the public key;
   save that line as `internal/license/keys/vendor-v2-2028.pub`, commit only the `.pub` file and release. Keep the private
   key offline (mode 0600); never commit it.
2. For a customer, pick a tier preset (`internal/license/tiers.json`: `standard`, `pro`, `trial`), or write the payload
   JSON (as above, without `kid`, `v` and optionally `issued_at`) with their domain and, after they send it, their
   install id.
3. Sign: `go run ./tools/license sign -key <the private key file> --tier standard --domain panel.customer.com --customer
   "Acme VPN" --months 1`, or `-in payload.json` for a payload. The tool fills in `kid`, `v` and `issued_at`, checks the
   payload the way core does, verifies the result and prints the token. It warns (and still signs) when
   `max_active_services` is 0 or missing: the install floors it at the free tier's 25.
4. The customer pastes it on the License tab (check, then install) or runs `bobres license import`.
5. Rotation: ship the new `.pub` next to the old one, sign new tokens with the new key, and remove the old
   file once its tokens have expired.

## Activation and daily refresh
The vendor's license server ([13-license-server.md](13-license-server.md)) turns a secret license key
(`BOBRES-XXXX-…`) into a short-lived token (30 days at most) bound to this install's id and domain. An install
reaches it only when `BOBRES_LICENSE_SERVER=https://…` is in `.env` (the seller gives the address), set with
`sudo bobres config set BOBRES_LICENSE_SERVER https://…`, which checks it and recreates the bot. The compose file
passes it to the bot (06-installer.md, "License activation"). Core has no
internet access, so every call goes through the bot or the host, and core verifies every answer as it verifies a
pasted token.
- **Activation.** There are two ways:
  - `sudo bobres license activate` on the host. It asks for the key without echoing it, or reads it from stdin. A
    key typed as the argument stays in the shell history and sudo's log.
  - The License tab's "Activate with a license key" (`POST /api/v1/license/activate`). It goes core → bot
    `/internal/license/activate` (core's token) → the server's `/v1/activate`.

  Either way, the token is installed with source `activation` and audited as `license.set`. A refusal (a wrong key,
  a revoked or ended license, too many moves) changes nothing. Without `BOBRES_LICENSE_SERVER` the dashboard answers
  "Online activation is not set up", and pasting a token still works.
- **Daily refresh.** Core renews an activated (or already refreshed) license through the bot
  (`/internal/license/refresh`, the installed token as the credential):
  - Core looks every hour and renews a token installed or refreshed 24 hours ago. After a failure or a refused
    answer, it waits 3 hours before it asks again.
  - It installs the answer only when it verifies here, has not expired, is for the same license and was issued
    after the installed token. Neither the server nor the bot can install an older token or another license.
  - The token becomes source `refresh` with `refreshed_at`. It is audited (`license.refresh`) only when the tier or
    the entitlements change, so renewals, upgrades and revocations reach the install within a day.
  - A token pasted or imported by hand (`manual`) is never refreshed. Installing one clears `refreshed_at`.
- **When refresh fails.** The bot cannot reach the server, `BOBRES_LICENSE_SERVER` is missing, or the license was
  revoked or moved. Nothing changes, and the failure itself gates nothing: the installed token keeps working
  until its `expires_at`, then runs through `grace_days`, then is `expired` like any other token (the trial or the free
  tier then applies). Core logs "license refresh failed" each time.

## Not built
- A vendor admin web UI (licenses, plans as entitlement templates, customers, activations, revocations, trials). The
  license server's CLI is the admin interface (13-license-server.md).

## Privacy
Only an install with `BOBRES_LICENSE_SERVER` set sends anything. Activation sends the license key, the install id,
the domain and the version. The daily refresh sends the installed token (which the vendor signed) and the version.
Neither sends end-user data or payment info, and the bot keeps nothing it passes on. Telemetry beyond that is
opt-in.

## Open points
- DECIDED: bind to domain + soft install ID; 3 re-activations per year (vendor server).
- DECIDED (2026-10-14): the implicit trial is 30 days from an install's first start with this policy and is not tied
  to an identity; the free tier's numbers are the owner's to tune (`internal/license/policy.go`).
- A vendor "trial" license (`tier: "trial"`) is still a license: it grants exactly its entitlements, and the vendor's
  server may issue one.
- Anti-tamper level: obfuscation is not planned; rely on signatures + updates + contract.
- The clock is trusted: rolling it back can extend a license or the trial (accepted limit of local verification). The
  trial's start is local data too: a database restored from before this version, or a rollback followed by an update,
  starts the trial again. A start in the future is clamped to now, so a trial never has more than 30 days left.
- Anyone who runs the code can patch the free tier's limits out. The policy keeps honest operators honest and makes
  the paid tier worth buying; the protection is the signed updates and images, as above.
