# Runbook: license

The License tab is not **Active**, a license token is refused, or the dashboard refuses an action because of the
license. Commands run on the server as root. How licenses work: [05-license.md](../05-license.md).

A missing or invalid license never stops the store: everything keeps working. Without a valid license the install runs
on its 30-day trial and then on the free tier (1 server, 25 active services, 2 staff seats, no resellers, no automatic
USDT, no white label). A license never leaves less than the free tier. Licenses signed by the vendor's key
`vendor-v1-2026` (fingerprint `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7`) verify on every release. Those limits, and a
license's, only refuse **new** servers, services, staff, resellers and payment methods; nothing already set up is ever
removed or stopped, and customers keep renewing.

## Symptoms

- The System page, **License** tab, shows a state other than **Active**, or the free tier or the trial (staff who see
  the page also get a chip in the top bar).
- Adding a 3x-ui server, a staff member (an owner takes a seat like anyone else but the owner named in `.env`), a
  reseller, or switching a payment method on is refused, naming the limit
  and saying to buy or renew a license (the server limit, the staff seats, the resellers feature, the allowed
  gateways, automatic USDT).
- Customers are told "New purchases are temporarily unavailable. Please contact support.", and the staff get a
  warning, "A new purchase was refused: this install is at its limit of ... active services": the install is at its
  limit of active services and a new order or free trial was refused. Existing services keep working and renewing.
  Buy or renew a license that allows more, or wait for services to end.
- `bobres license import` answers `not installed: <reason>`.

## Checks

```sh
sudo bobres license status
date -u                     # the server's clock: licenses are dated
```

It prints the state, this install's id, the domain and, for an installed token, its license id, tier, expiry, grace
end and binding; then what is in effect (`In effect`: license, trial or free tier; `Trial ends`) with each limit and
its use (`Services: 25 (3 in use)`, counting the services on their way), the gateways and features, and whom to
contact (the "Buy or renew" row, only when `BOBRES_SELLER_CONTACT` is set: `sudo bobres config set BOBRES_SELLER_CONTACT
@handle`). The same is on the License tab. Staff get a `license.trial` alert in the last 7 days of the trial and once
when it ends.

| State | Means | What applies |
|---|---|---|
| missing | No token installed | The trial (30 days from the first start of this version), then the free tier |
| invalid | The saved token does not verify here (the reason below) | The same: it counts as no license |
| active | Valid until its expiry | The license's entitlements |
| grace | Expired, inside its grace days | The license's entitlements, with a warning: renew it |
| expired | Past the grace too | The trial if it still runs, else the free tier; everything set up keeps working |

## Fixes by reason

| Reason | Meaning | Fix |
|---|---|---|
| `unknown_key`: "this build carries no vendor key, so it cannot verify a license" | This build carries no vendor key at all: only a development build does | Install an official release (`sudo bobres update`) |
| `malformed`: "this is not a license token" | The token was cut or changed when copied | Get it again as a file and import the file (below) |
| `bad_signature` | Changed, not from the BOBRES vendor, or signed with a key this version does not carry yet (after the vendor rotates its key) | Get the token again from the vendor and import it as a file. If the vendor says it changed its key, `sudo bobres update` first |
| `version` | Newer format than this version understands | `sudo bobres update` |
| `domain`: "the license is for another domain" | `BOBRES_DOMAIN` is not the domain the token names (the store moved domains) | Ask the vendor for a token for the current domain |
| `install_id`: "the license is for another install" | The token names another install id: this install was set up again without restoring its database | Send the vendor the install id from `bobres license status`; or restore the old database (`bobres restore`), which brings the old id back |
| `not_yet_valid`: "the license starts in the future (check the server clock)" | The clock is behind | `timedatectl set-ntp true`, check `date -u`, import again |
| `expired` | Too late to install | Ask the vendor to renew it |

**Install a token** (the owner), from a file so that nothing is lost in copying:

```sh
sudo bobres license import < /root/license.txt
```

"License installed." means done; it is checked first (signature, domain, install id, dates) and audited as
`license.set`. Or on the dashboard: System > License, paste the token or open the file, check it, then save it with a
reason. Delete the file afterwards; keep the token private.

**Activate with a license key.** Use this when the seller gave a key (`BOBRES-…`) and a license server address
instead of a token:

```sh
sudo bobres config set BOBRES_LICENSE_SERVER https://<the address from the seller>   # checks it, recreates the bot
sudo bobres license activate      # asks for the key; it is not shown
```

`bobres config set` refuses an address that is not https (the bot would not start with it) and waits until the bot
is healthy again. Do not edit `.env` by hand for it: a typo there stops the bot.

From a file: `sudo bobres license activate < /root/key.txt`, then delete the file. Do not type the key after
`activate`: it would stay in the shell history and in sudo's log, and the key alone can move the license to another
server. On the dashboard: System > License, "Activate with a license key". Core then renews the license once a day
by itself; the Source row of `bobres license status` then says "renewed online on <date>".

| Message | Fix |
|---|---|
| "Online activation is not set up" (dashboard), or `no license server` (CLI) | `BOBRES_LICENSE_SERVER` is not set (`sudo bobres config get BOBRES_LICENSE_SERVER` prints an empty line): the steps above. Set but the bot not recreated since (set with `--no-restart`, or while the store was stopped): `sudo bobres start` |
| `the license server refused: ... (invalid_key)` | A typo in the key, or a key that no longer exists: ask the seller |
| `... (reactivation_limit)` | The key moved to another install too often this year (the message has the date): ask the seller |
| `the license server did not answer` | The address is wrong (`sudo bobres config set BOBRES_LICENSE_SERVER <the right one>`), or this server cannot reach it |
| The renewal date in `bobres license status` stops moving | The daily refresh fails: `sudo bobres logs core \| grep "license refresh"`. Check `sudo bobres config get BOBRES_LICENSE_SERVER` and the bot's internet access. The token keeps working until its expiry and grace end |

**Renew before the end.** The License tab and `bobres license status` show the days left and the grace end. In grace
nothing changes yet; after it, the free tier's limits (or the trial's, while it runs) apply to new servers, services,
staff, resellers and payment methods until a new token is installed.

**Moving to a new server:** restore the backup there (`bobres restore`, see [restore](restore.md)) with the same
domain: the database carries the install id and the token, so the license keeps working. A fresh install without the
restore has a new install id.

## Escalate when

- A token the vendor just issued for this domain and install id is refused (`bad_signature` on a version the vendor says
  knows the key).
- The License tab and `bobres license status` disagree.

Send: the output of `sudo bobres license status` (it holds the install id and license id, not the token) and the
release (`bobres version`). Send the token itself only to the vendor, privately.
