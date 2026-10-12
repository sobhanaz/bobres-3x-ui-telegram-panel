# Runbook: license

The License tab is not **Active**, a license token is refused, or the dashboard refuses an action because of the
license. Commands run on the server as root. How licenses work: [05-license.md](../05-license.md).

A missing or invalid license never stops the store: everything keeps working, unlicensed. A license only ever limits
**new** servers, staff, resellers and payment methods, and only once it is active (or expired); nothing already set
up is ever removed.

## Symptoms

- The System page, **License** tab, shows a state other than **Active** (staff who see the page also get a chip in
  the top bar).
- Adding a 3x-ui server, a staff member, a reseller, or switching a payment method on is refused, naming the license
  (the server limit, the staff seats, the resellers feature, the allowed gateways).
- `bobres license import` answers `not installed: <reason>`.

## Checks

```sh
sudo bobres license status
date -u                     # the server's clock: licenses are dated
```

It prints the state, this install's id, the domain and, for an installed token, its license id, tier, expiry, grace
end, binding and entitlements. The same is on the License tab, with each limit's use.

| State | Means | Effect |
|---|---|---|
| missing | No token installed | Nothing is limited ("Unlicensed") |
| invalid | The saved token does not verify here (the reason below) | Nothing is limited |
| active | Valid until its expiry | New servers, staff, resellers and payment methods are checked against it |
| grace | Expired, inside its grace days | As active, with a warning: renew it |
| expired | Past the grace too | Those new actions are refused; everything set up keeps working |

## Fixes by reason

| Reason | Meaning | Fix |
|---|---|---|
| `unknown_key`: "this build has no public key that could verify a license yet" | This BOBRES version knows no vendor key that signed it | Until the vendor ships its key in a release, every token is refused this way and nothing is limited. Update (`sudo bobres update`) once the vendor says the key is in |
| `malformed`: "this is not a license token" | The token was cut or changed when copied | Get it again as a file and import the file (below) |
| `bad_signature` | Changed, or not from the BOBRES vendor | Ask the vendor for the token again |
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
sudo nano /opt/bobres/.env        # add: BOBRES_LICENSE_SERVER=https://<the address from the seller>
sudo bobres start                 # recreates the bot with it (restart does not)
sudo bobres license activate      # asks for the key; it is not shown
```

From a file: `sudo bobres license activate < /root/key.txt`, then delete the file. Do not type the key after
`activate`: it would stay in the shell history and in sudo's log, and the key alone can move the license to another
server. On the dashboard: System > License, "Activate with a license key". Core then renews the license once a day
by itself; the Source row of `bobres license status` then says "renewed online on <date>".

| Message | Fix |
|---|---|
| "Online activation is not set up" (dashboard), or `no license server` (CLI) | `BOBRES_LICENSE_SERVER` is not in `.env`, or the bot was not recreated: the steps above |
| `the license server refused: ... (invalid_key)` | A typo in the key, or a key that no longer exists: ask the seller |
| `... (reactivation_limit)` | The key moved to another install too often this year (the message has the date): ask the seller |
| `the license server did not answer` | The address is wrong, or this server cannot reach it |
| The renewal date in `bobres license status` stops moving | The daily refresh fails: `sudo bobres logs core \| grep "license refresh"`. Check `BOBRES_LICENSE_SERVER` and the bot's internet access. Nothing is limited before the token's expiry and grace end |

**Renew before the end.** The License tab and `bobres license status` show the days left and the grace end. In grace
nothing changes yet; after it, new servers, staff, resellers and payment methods are refused until a new token is
installed.

**Moving to a new server:** restore the backup there (`bobres restore`, see [restore](restore.md)) with the same
domain: the database carries the install id and the token, so the license keeps working. A fresh install without the
restore has a new install id.

## Escalate when

- A token the vendor just issued for this domain and install id is refused (`bad_signature`, `unknown_key` on a
  version the vendor says knows the key).
- The License tab and `bobres license status` disagree.

Send: the output of `sudo bobres license status` (it holds the install id and license id, not the token) and the
release (`bobres version`). Send the token itself only to the vendor, privately.
