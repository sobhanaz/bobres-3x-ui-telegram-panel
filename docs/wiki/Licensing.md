# Licensing

BOBRES is **free core, paid premium**, with one subscription per install. You do not need a license to run a store. A license unlocks more servers, services, staff, resellers, automatic USDT and white label.

## The tiers

| | Free | Trial | Standard | Pro |
|---|---|---|---|---|
| Price per month | free | free | 4 USDT | 12 USDT |
| Price per year | free | free | 40 USDT | 120 USDT |
| Length | no limit | 30 days | while paid | while paid |
| 3x-ui servers | 1 | unlimited | 3 | unlimited |
| Active services | 25 | unlimited | 500 | unlimited |
| Dashboard staff seats | 2 | 5 | 5 | unlimited |
| Required channels and groups | 1 | unlimited | 1 | unlimited |
| Resellers | no | yes | yes | yes |
| Automatic USDT | no | yes | yes | yes |
| Other payment methods | yes | yes | yes | yes |
| White label (no "Powered by BOBRES") | no | no | no | yes |
| Updates and security fixes | every release | every release | every release | every release |

Prices are per install, in USDT on TRON (TRC20). A year costs ten months.

- The **free tier** is for your own store. Its payment methods are the wallet, Zarinpal, IDPay, NextPay, Aqayepardakht, Stars, card to card, manual USDT and the manual Zarinpal link.
- **Automatic USDT and automatic TON** need the crypto watcher, which the paid tiers and the trial have; a license that names automatic USDT covers automatic TON too.
- The **trial** starts by itself on a new or upgraded install, lasts 30 days, and has everything except white label.
- **Active services** are the services customers have now, active or about to expire, plus paid orders on their way.
- **Staff seats** count admins, support members and extra owners. The main owner (the first ID in `BOBRES_ADMIN_TELEGRAM_ID`) takes none.
- A license is never worth less than the free tier.

Support is self-service: the docs, the runbooks and `bobres support-bundle`. Paid tiers are meant to add support from the seller, but its scope and response times are not final yet.

## Buy and activate

1. Pay the seller. You receive a license key that looks like `BOBRES-XXXX-...`. It is shown once.
2. Tell your install where the seller's license server is. The seller gives the address:

```bash
sudo bobres config set BOBRES_LICENSE_SERVER https://license.example.com
```

3. Activate. Either in the dashboard (**System > License > Activate with a license key**, owner only, a reason is required), or on the server, where the key is asked without echo:

```bash
sudo bobres license activate
```

With a signed token instead of a key, paste it on the License tab, or run `sudo bobres license import < token.txt`.

The install gets a signed token that lasts at most 30 days and renews it by itself every day, through the bot (core has no internet). It is bound to your domain and your install ID. `sudo bobres license status` shows the state, your install ID and what is in effect.

A license key can move to a new install or domain up to 3 times in a year. Each store needs its own license.

## What happens when it ends

1. The seller stops renewing. The last token runs to the end of the paid period.
2. A **grace period** follows (14 days by default). Everything still works, with a warning on the dashboard.
3. After that the install falls back to the trial if it still runs, else to the free tier.

Nothing is deleted. Existing services keep working, renewing and topping up, even over the free tier's limits. Only what is new and beyond the free tier is refused until you renew:

- a new purchase or free trial when customers already have 25 active services,
- a second server, a new reseller, automatic USDT, a new staff member over the seats, a second required channel.

Your customers see only "New purchases are temporarily unavailable. Please contact support." Your staff get an alert, at most once a day, when a purchase is refused for the limit. Renewing brings the features back at the next daily renewal, with no reinstall.

## Other facts

- **"Powered by BOBRES"** is on the dashboard's footer and login page unless a valid license includes white label (Pro).
- **Updates are never withheld.** A license may carry an update date, but it is advisory: `bobres update` prints a note for a newer release and installs it anyway.
- **What is sent to the vendor.** Activation sends the license key, the install ID, the domain and the version. The daily renewal sends the installed token and the version. Both happen only when `BOBRES_LICENSE_SERVER` is set, and the license server sees the request's IP address. Your customers' data is not sent.
- **The vendor's own features** (selling BOBRES licenses, reselling a supplier's accounts) are in no tier. See [Digital Accounts](Digital-Accounts).
- **Terms.** The source license and the legal texts are drafts, and the lawyer's review is still open. Copying or redistributing the code, hosting it for others, reselling it and white-labelling it need a paid license from the owner. See the [LICENSE](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/LICENSE).

## Reading the License tab

The tab shows one of five states: no license, invalid (the reason is shown, such as a wrong domain; it counts as no license), active, in grace (renew now), or expired. Follow the [license runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/license.md) if it is not what you expect. The model behind it is in [docs/pricing.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/pricing.md) and [docs/05-license.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/05-license.md).
