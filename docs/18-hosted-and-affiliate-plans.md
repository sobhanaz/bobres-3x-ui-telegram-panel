# A hosted offering and an affiliate program: plans

Two long-term items of the roadmap, written down as plans and not built. The owner chose on 2026-10-28 to keep them as plans until the first customers exist and the lawyer's review is done. Each ends with the decisions it waits for.

## 1. A hosted offering ("BOBRES, run for you")

**Goal.** A customer who does not want to run a server pays monthly and gets a working store (bot, dashboard, Mini App) on a domain they choose.

**Two ways to build it.**

| | One VPS per customer (recommended) | One shared deployment for all |
|---|---|---|
| What it is | The normal install, made by automation on a VPS in our cloud account, kept up to date by the dashboard updater | One core, bot, payments and database serve many stores (tenants) |
| Isolation | A virtual machine per store; a customer's secrets never share a process or a database | Every table, every secret and every bot token needs a tenant, for ever |
| Work | Provisioning script, DNS, onboarding form, billing, central monitoring | A rewrite of the data model and of every service: months, and a security review of every query |
| Cost per store | A small VPS (about 5 to 6 USD a month) | Lower, shared |

The first is a service around what exists. The second is a different product and is not planned.

**Steps.**
1. *Assisted install* (no code): the customer sends a domain and a bot token; we run `install.sh` on a VPS in our account (or theirs), set `BOBRES_UPDATE_CHANNEL`, and hand over the dashboard link. Billing is a license product ("Hosted") in the license store. Three paying pilots before anything is automated.
2. *Automation*: a small tool that creates the VPS through the cloud provider's API, points DNS at it (Cloudflare API, as the vendor site already does), runs the installer, and records the store in a register.
3. *Central view*: every hosted store's `/metrics` (the Prometheus endpoint of 0.19.0, behind its token) scraped into one Grafana, with alerts to us as well as to the customer.

**What it needs.** Terms for hosting (data processing, liability, jurisdiction, what we do with a store that sells something unlawful); custody rules (the customer keeps `BOBRES_BACKUP_KEY`; we never keep their bot token anywhere but their server); a support routine and runbooks; a cost model; a policy for abuse.

**Risks.** Legal exposure of running other people's VPN stores; secrets of many customers on our side of the line; support load; collecting money in USDT.

**Waits for.** The lawyer's review (the launch checklist), three pilots, and a price.

## 2. An affiliate program for BOBRES itself

**Goal.** People who bring a new license buyer earn a commission.

**Built on what exists.** The license store and `licensesrv` sell licenses (vendor orders); the customers' referral system (invite link, reward percent) and the reseller commission show the patterns; the ledger already has the kinds `referral` and `commission`.

**Shape.**
- An affiliate code per person (`/affiliate` in the store's bot gives it, and a link `?start=aff_<code>`); the code is kept when the buyer starts the purchase and is recorded on the vendor order.
- A commission rate per affiliate (a default, and an override), paid on the first payment only, or on every renewal for a limited number of months: one setting.
- The commission is a ledger entry on the affiliate's wallet once the order is paid and delivered; a refund within 30 days takes it back (the wallet may not go negative; the shortfall is audited).
- Guards: no self-referral (same Telegram account, same payment address), a minimum payout, and an audit entry for every change.
- A dashboard page "Affiliates": codes, referred licenses, earned, paid.

**Open decisions.** The percentage; first payment only or recurring; how affiliates are paid (wallet credit, then USDT by hand); the legal and tax side.

**Size.** About two to three days after 0.19.0.
