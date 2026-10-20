# Pricing and tiers

Status: the model, the tiers and the policy behind them are decided (2026-10-14; the policy is
[05-license.md](05-license.md), "Free core, paid premium"). **The prices are the owner's to set**: they are placeholders
until then. The one file that holds the tiers and their prices is [`internal/license/tiers.json`](../internal/license/tiers.json);
`go run ./tools/license tiers` prints it as a table. The legal texts that go with it are in [legal/](legal/README.md).

## The model

**Free core, paid premium, one subscription per install.**

- BOBRES is free to run for your own store on the **free tier**, with the limits below. There is no time limit, and
  every update reaches every install: security fixes are never held back.
- A **license** unlocks more: more servers and services, resellers, automatic USDT, more staff, and white label. It is a
  subscription for **one install**, monthly or yearly, and it is bound to that install's domain.
- Every install, new or upgraded, also gets an **implicit trial of 30 days**, counted from the first start of a version
  that has this policy. It has every premium feature except white label, so nobody has to ask before trying the whole
  product. It is kept once on the install (in `core.install`) and is not tied to a person or an account.
- The source code is public and **source-available**, all rights reserved ([LICENSE](../LICENSE), a draft): you can read
  it, but copying it, hosting it for others, reselling it and white-labelling it need a paid license.

## The tiers

Prices are shown per install. "SET ME" is a placeholder for the owner's number.

| | Free | Standard | Pro | Trial |
|---|---|---|---|---|
| **Price per month** | free | SET ME | SET ME | free |
| **Price per year** | free | SET ME | SET ME | free |
| **Currency** | | SET ME | SET ME | |
| **Length** | no limit | while paid | while paid | 30 days |
| 3x-ui servers | 1 | 3 | unlimited | unlimited |
| Active services | 25 | 500 | unlimited | unlimited |
| Dashboard staff seats (the main owner takes none) | 2 | 5 | unlimited | 5 |
| Required channels and groups (customers must join to use the bot) | 1 | 1 | unlimited | unlimited |
| Resellers | no | yes | yes | yes |
| Automatic USDT (TRON watcher) | no | yes | yes | yes |
| Other payment methods (wallet, card to card, manual USDT, Zarinpal, Stars) | yes | yes | yes | yes |
| White label (no "Powered by BOBRES") | no | no | yes | no |
| Updates (every release, security fixes included) | yes | yes | yes | yes |
| Support | self-service | SET ME | SET ME | self-service |

How to read it:

- **Active services** are the services your customers have now: active, or about to expire. Orders on their way count
  too (paid or being delivered, and unpaid ones made in the last 2 hours), so many orders made together cannot overshoot
  the limit.
- **Staff seats** are the admins, the support members and the owners on the dashboard. **Every staff member takes a seat
  except the main owner the install names** (the first id in `BOBRES_ADMIN_TELEGRAM_ID` in its settings): a second owner
  takes a seat like anyone else, whether they were added in the dashboard or named in that same setting
  (`bobres admin owners add`). Nobody is removed when a limit is lowered; staff who are already over keep their access.
- **Required channels and groups** are the channels and groups customers must join before they can use the bot or
  the Mini App (Settings, Required channels and groups). Every tier can require **one**; **Pro** (and the trial)
  any number (a technical bound of 50 holds). Adding a second on the free tier or Standard is refused with a message
  that names the limit and the Pro plan. If a license ends, the chats already saved stay: only the first one is
  enforced, the dashboard marks the others "Inactive: needs Pro", and they count again with a license that allows
  them.
- **Payment methods.** The wallet is always on. The free tier also has Zarinpal, Telegram Stars, card to card, manual USDT
  and manual Zarinpal. Automatic USDT needs a paid tier (or the trial). Standard and Pro name every method.
- **"Powered by BOBRES"** is shown on the dashboard's footer and login page on every install, **unless a valid license
  includes white label** (Pro). The free tier, the trial, Standard and a lapsed license all show it.
- The **Trial** column is two things with the same limits: the implicit trial every install gets, and the `trial`
  license a seller can issue to a prospect (`licensesrv key create -customer ... -tier trial`). Neither costs anything,
  and neither renews. A license, when the install has one, is used instead of the implicit trial.
- The numbers come from the tier presets, so the table changes when the file does. The free tier is code, not part of that
  file: `license.FreeEntitlements()` in `internal/license/policy.go`, and it applies to every install, the owner's own
  store included.
- A license is never worth less than the free tier: an install raises every limit that a license sets lower, or omits,
  to the free tier's.
- A new action beyond a limit is refused with a message naming the limit. Raising the limit means a higher tier. What
  already exists is never removed or stopped.

## How a license works

1. You pay, through the seller's channel ([refund-policy.md](legal/refund-policy.md)), and receive a **license key**
   (`BOBRES-XXXX-...`), shown once.
2. You set the seller's address on your install (`sudo bobres config set BOBRES_LICENSE_SERVER https://...`) and activate
   with the key: the dashboard's System > License tab, or `sudo bobres license activate`.
3. The install gets a signed token that lasts 30 days at most and renews it **every day** by itself, through the seller's
   license server. It is bound to your domain and your install id.
4. To keep it, **pay again before the period ends**. To stop, stop paying.

A license key can move to another install or domain up to 3 times in a year (a new server, a new domain). Each install
needs its own license: a staging copy that is not a store can use the free tier or the trial.

Technical details: [05-license.md](05-license.md), [13-license-server.md](13-license-server.md).

## Updates and support

- **Updates.** Every release is signed and any install can install it with `bobres update`: the free tier, the trial,
  a paid license and a lapsed one all get every release. A license does not buy updates and none is ever withheld, so
  fixes reach everyone.
- **Support.** Everyone has the docs, the runbooks and `bobres support-bundle`. Paid tiers add support from the seller:
  - Standard: [OWNER: scope and response time].
  - Pro: [OWNER: scope and response time].
  - Support covers BOBRES itself. It does not cover your server, 3x-ui, your payment gateways, your customers or the VPN
    service you sell.

## When a subscription lapses

1. You do not pay. The seller stops renewing the license; the install's last token runs to the end of the paid period.
2. A **grace period** follows (14 days by default). Everything still works, with a warning on the dashboard.
3. When it ends, the install falls to the **free tier** (or to the implicit trial, if that still runs). "Powered by
   BOBRES" comes back unless the license was the white-label one.

**Nothing is deleted.** Your data, your customers' services, your servers, your staff accounts and your settings stay.
**Existing services keep working, renewing and topping up**, over the free tier's limits too. Only what is **new** and
beyond the free tier is refused, until you renew:

- a **new purchase** or free trial when your customers already have 25 active services. Your customers see "New purchases
  are temporarily unavailable. Please contact support." and nothing about licensing. An order made before the limit and
  paid after it is still delivered;
- a second server, a reseller (existing resellers keep their account and what they bought), automatic USDT and a new
  staff member over the seats;
- your staff get an alert, once a day at most, that a purchase was refused because of the limit.

Renewing brings the paid features back at once, on the next daily renewal. Nothing is reinstalled.

## FAQ

**Do I need a license to try BOBRES?** No. Install it: you get the first 30 days with every premium feature except white
label, then the free tier.

**Can I run my own store on the free tier for ever?** Yes, within its limits (LICENSE, section 2): 1 server, 25 active
services, 2 staff seats and 1 required channel or group. Past them, a license.

**Does the trial start again if I restore a backup?** It can. The trial's start is kept in the install's database, so a
backup made before this version existed, or a rollback to an older version followed by an update, starts the 30 days
again. This is a known, accepted limit.

**What is not allowed without a paid license?** Hosting BOBRES for other people, reselling it, copying or redistributing it,
and removing the BOBRES name. See the LICENSE, section 3. Ask the seller for an agreement if you want to be an agency or a
reseller of installs.

**What happens when I reach a limit?** The action that would go over it is refused, with a message that names the limit
(your customers only hear that new purchases are temporarily unavailable). What is already set up keeps working.

**What if the license server is down?** The install keeps working: its token lasts up to 30 days, then the grace period
runs. A short outage changes nothing.

**What if I pay late?** The license starts again when the seller renews it, and the install picks it up on its next daily
renewal. The seller counts the new period from the day you paid, or from the old end if you paid early.

**Can I move to another server or domain?** Yes, up to 3 times in 365 days. Restoring a backup on a new server keeps the
install id, so it does not count as a move if the domain stays the same.

**Do I get an invoice?** [OWNER: invoices and taxes; see the Terms of Service.]

**Is my customers' data sent to the seller?** No. Activation sends the license key, the install id, the domain and the
version; the daily renewal sends the installed license token and the version. Both happen only when you switch online
activation on, and the license server sees the request's IP address ([legal/privacy-policy.md](legal/privacy-policy.md)).

**Can I change tier?** Yes: tell the seller. An upgrade is on your install by its next daily renewal. [OWNER: how a change is
charged.]

**Can I get a refund?** [legal/refund-policy.md](legal/refund-policy.md).

## Open points

- The prices (the owner's) and the currency.
- Support scope and response times per tier.
- The legal texts: a lawyer must review them ([launch-checklist.md](launch-checklist.md)).
