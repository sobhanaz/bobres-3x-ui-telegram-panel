# A hosted offering and an affiliate program

Two vendor-side items of the roadmap. They were plans until 2026-10-28; on 2026-10-30 the owner decided how each works and both are
**built** (after 0.20.0): the affiliate program in full, and the hosted offering's assisted install (step 1) with the central view (step 3).
Automation through a cloud provider's API (step 2) is not built. Both are the vendor's own: they work only on a store whose license carries
`private_features` (the owner's own store, [13-license-server.md](13-license-server.md)), and the affiliate program also needs license sales
on.

## 1. A hosted offering ("BOBRES, run for you")

**Goal.** A customer who does not want to run a server pays monthly and gets a working store (bot, dashboard, Mini App) on a domain they choose.

**The model: one VPS per customer.** The normal install, made by our tool on a VPS (in our cloud account or the customer's), kept up to date
by the dashboard updater. A customer's secrets never share a process or a database with another customer's. A shared deployment for all
(tenants) would be a different product and is not planned.

### What is built

**Assisted install: `tools/hosted`.** The owner runs it from their own computer against a fresh VPS reachable over SSH with a key (root,
or a user with password-less sudo; `BatchMode`, so it never asks for a password):

```sh
go run ./tools/hosted check --host root@203.0.113.7
go run ./tools/hosted install --host root@203.0.113.7 --domain shop.example.com --customer "Acme VPN" \
    --admin-id 123456789 --bot-token-file ~/hosted/acme.token [--channel stable|beta] [--port 22] [--identity ~/.ssh/id_ed25519] \
    [--license-server https://license.example.com --license-key-file ~/hosted/acme.key --license-id lic_...] [--out ~/hosted]
```

1. It checks the server: root, its system, and that BOBRES is not installed yet (`--rerun` runs the installer again on one that has it; the
   installer keeps every secret).
2. It downloads `install/install.sh` and runs it with `--yes` (no question), the domain, the customer's Telegram id and the bot token from
   the file. The token travels in the script ssh reads on its standard input, never on a command line (where `ps` and the shell history keep
   it), and the tool warns when the token file can be read by other users.
3. It sets the update channel (`BOBRES_UPDATE_CHANNEL`), hardens the server (`bobres harden --yes`, keeping the SSH port open), and with a
   key file activates the store's license (`bobres config set BOBRES_LICENSE_SERVER ...`, then `bobres license activate` with the key on
   standard input).
4. It makes the token of the store's `/metrics` (`bobres admin metrics-token generate`, new in this version).
5. It prints the customer's **backup key once** ("give it to the customer and tell them to save it"): the customer keeps `BOBRES_BACKUP_KEY`;
   the tool writes it nowhere, and the owner should not keep it.
6. It prints a **registration code** (`bobres-hosted:v1:...`) and saves it as `hosted-<domain>.txt` (mode 600) in `--out`. The code carries
   the domain, the customer, the version installed, the channel, the license id and the metrics token, never the bot token or the backup
   key. Delete the file once the store is registered.

The DNS of the domain must point at the server before the install (the installer's certificate needs it). The tool is tested against a fake
SSH runner (`tools/hosted/main_test.go`): it never touched a real server.

**The register and the central view: dashboard > Hosted stores.** Paste the registration code into **Add a store** (or type the fields:
customer, domain, channel, license id, metrics token, notes). The register (`core.hosted_stores`, migration 00055) keeps the store id,
customer, domain, version installed, license id, channel, notes, created, and the metrics token **encrypted** with core's master key
(write-only: answers say only whether one is set). Every five minutes, and on **Read now**, core reads each store's `https://<domain>/metrics`
with its token **through the bot** (core has no internet access; the bot's `POST /internal/hosted/metrics` calls that one URL on a public
address only: no IP literal, no port, no redirect, and the connection is refused when the name resolves to a private, loopback, link-local
or shared address) and shows:

- up, needs a look (a service down, a critical alert, the database down, a license within 7 days of its end), down (with the reason: the
  token refused, `/metrics` off, no answer, not a BOBRES store) or not read yet;
- the version it runs (with **behind** when another hosted store runs a newer one) and the update channel;
- services up of all, orders paid in 24 hours, open alerts, license days left, the age of the newest backup.

**The "Hosted" license product.** **Make the Hosted license** on a store's row makes a **pro** license on the license server (through the
signed admin API, like every license the dashboard makes) for 1 to 36 months (core takes up to 120), named `Hosted: <customer> (<domain>)`, and records its id on
the store. Its key is shown once: pass it to `tools/hosted --license-key-file`, or activate it on the store. A store keeps one license
(clear the id to make another). A new token tier was not added: installs refuse a tier they do not know, so a hosted store runs on the pro
tier and the register says it is hosted. Billing is by hand for the pilots (renew the license from the Licenses page).

**What is never on the vendor side:** a customer's bot token (it stays in the store's `.env`), a customer's backup key, and a metrics token
in clear.

### Not built (step 2: automation)

A tool that creates the VPS through the cloud provider's API, points DNS at it (Cloudflare API) and runs `tools/hosted` by itself. It waits
for the three paying pilots. A central Grafana is also not built: the Hosted stores page is the central view; the stores' `/metrics` can be
scraped by a Grafana as well ([runbooks/monitoring.md](runbooks/monitoring.md)).

**What it still needs before selling.** Terms for hosting (data processing, liability, jurisdiction, what we do with a store that sells
something unlawful); a support routine and runbooks; a cost model (a small VPS is about 5 to 6 USD a month); a policy for abuse; the
lawyer's review (the launch checklist).

## 2. An affiliate program for BOBRES itself

**Goal.** People who bring a new license buyer earn a commission.

### How it works (built)

- **Off by default.** The owner switches it on in the dashboard (**Affiliates**: `affiliate.enabled`) on a store that sells licenses
  (`vendor.enabled` and the admin key, [13-license-server.md](13-license-server.md)).
- **A code per person.** `/affiliate` in the bot (or **Affiliate program** on the licenses screen) makes the customer an affiliate once and
  shows their link `https://t.me/<bot>?start=aff_<code>`, their percent, how many customers they brought, the licenses that earned, what
  they earned, what was paid out and their wallet.
- **Who brought whom.** A customer who opens an affiliate link before they ever bought a license is recorded as brought by that affiliate
  (`core.affiliate_referrals`, the first link wins; a newcomer is recorded once they pick a language). The affiliate's own link records nothing
  (**no self-referral**: the same Telegram account is the same user), nor does a disabled affiliate's, nor a link while the program is off.
  The affiliate is written on the buyer's first new license order when it is made (`vendor_orders.affiliate_id`).
- **The commission.** When that order is paid **and** delivered (the license exists), the affiliate's Toman wallet is credited the
  program's percent of what the buyer paid, **20%** by default (`affiliate.percent`, 1 to 100), or the affiliate's own percent (0 to 100, set
  on the Affiliates page; 0 earns nothing). It is a ledger entry of kind `affiliate_commission` under the key
  `affiliate_commission:<order id>`, written in the transaction that marks the order done, with a row in `core.affiliate_commissions`.
  **First payment only:** one commission per buyer, ever (renewals and later licenses earn nothing); two first orders of one buyer finishing
  at the same moment are paid one after the other and only the first earns. The affiliate is told in the bot; the buyer is never named. The
  program switched off before the delivery pays nothing new.
- **Refunds.** **Refund** on a sold license (Licenses page, sold tab) gives the buyer back what they paid (kind `license_refund`), revokes the
  license on the license server, marks the order refunded and, **within 30 days of the payment**, takes the commission back from the
  affiliate's wallet (kind `affiliate_reversal`) as far as the wallet holds it, **never below zero**; what could not be taken is written to
  the audit log (`affiliate.reversal`: credited, taken, shortfall). After 30 days the commission stays (the refund's audit says so). A
  second refund changes nothing.
- **Payouts.** The owner pays affiliates by hand (USDT, a card transfer) and records it on the Affiliates page (**Record a payout**): the
  amount leaves the affiliate's wallet (kind `affiliate_payout`, key `affiliate_payout:<request id>`, so a double click is one payout), at
  least the **minimum payout** (`affiliate.min_payout`, Toman; 0 for none), at most what the affiliate earned and was not paid yet, and at
  most what the wallet holds. Two payouts at once are serialized on the wallet.
- **The dashboard page "Affiliates"** (owner, `license.write`): the program's switch, percent and minimum payout; each affiliate's code,
  percent (own or the program's), status, customers brought, licenses, earned, paid and wallet; their commissions (with what refunds took
  back) and payouts; a percent of their own, pausing them, and recording a payout. Every change is audited (`affiliate.update`,
  `affiliate.payout`, `affiliate.reversal`, `vendor.refund`, `setting.set`).

Migration core 00054 (additive, with Down): the ledger kinds `affiliate_commission`, `affiliate_reversal` and `affiliate_payout`, the
tables `affiliates`, `affiliate_referrals`, `affiliate_commissions`, `affiliate_payouts`, and `vendor_orders.affiliate_id`. New gRPC calls
`GetMyAffiliate` and `ApplyAffiliateCode`.

**Still open.** "Same payment address" as a self-referral guard (licenses are paid from the wallet, so there is no address to compare);
recurring commissions on renewals (the owner chose the first payment only); the legal and tax side of paying affiliates.
