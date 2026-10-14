# Runbook: the vendor license server (for the seller)

This runbook is for **you, the seller**, not for operators. It covers running the license server on a VPS and the
routine jobs of selling licenses: issue, renew, revoke, watch, back up, rotate the key. How a license works, for
operators: [pricing.md](../pricing.md) and [05-license.md](../05-license.md). The server's rules and API:
[13-license-server.md](../13-license-server.md). Operators with a license problem use [license.md](license.md).

Commands run on the vendor's VPS as root, unless a step says it runs on your own computer.

## What you run

`deploy/vendor/` is a small Docker stack: **licensesrv** (the server), **Postgres** (its database) and **Caddy** (HTTPS,
with a certificate that Caddy gets and renews itself). Only Caddy publishes ports (80 and 443). The server and the
database sit on internal networks. The server runs as a non-root user on a read-only filesystem with no capabilities.

The **signing key** is a file on the VPS, outside the repository and outside `.env`, mounted read-only into the server.
Its public half (key id `vendor-v1-2026`, fingerprint `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7`) is built into every
BOBRES release, so installs accept what it signs.

## What an install does with what you sell

The policy is "free core, paid premium" ([05-license.md](../05-license.md), [pricing.md](../pricing.md)). Customers ask
about it, so know it:

- **The free tier** (no license, trial over): 1 server, 25 active services, 2 staff seats, the Zarinpal, Stars and manual
  payment methods and the wallet. No resellers, no automatic USDT, no white label ("Powered by BOBRES" is shown). It never
  ends and has no update limit. Seats: every staff member takes one **except the owner the install names**
  (`BOBRES_ADMIN_TELEGRAM_ID`); a second owner takes a seat like an admin or a support member.
- **The trial**: 30 days from the first start of a version that has the policy, for a fresh install and an upgraded one
  alike. It is kept once in `core.install`. It has every premium feature except white label, and 5 staff seats.
  Restoring a backup made before the policy, or rolling back and then updating, starts it again: an accepted limit. You
  cannot extend it. For a prospect who needs longer, issue a `trial` license with `-days`.
- **A license** is used instead of the trial, and is never worth less than the free tier. Its token lasts 30 days at
  most; the install renews it **every day** through your server.
- **When a license lapses** (it is not renewed, it is revoked, or it has an end date): the install keeps working through
  the token's end and the grace period (14 days by default), then **falls to the free tier**, or to its trial if that
  still runs. **Nothing is deleted.** Existing services keep working, renewing and topping up. A new purchase beyond 25
  active services is refused: the customer reads "New purchases are temporarily unavailable. Please contact support." (no
  word about licensing) and the staff get an alert. A second server, resellers, automatic USDT and staff beyond the seats
  are refused too.
- **Updates are never withheld.** `updates_until` on a license is only an informational date: `bobres update` and the
  dashboard note that a release came out after it, and install it all the same. Do not sell "updates until" as a feature.
- **"Powered by BOBRES"** is shown on every install except one with a valid license that includes white label (Pro).

## 1. Deploy on a VPS

You need a VPS with 1 GB of RAM, Docker with the Compose plugin, a name such as `license.example.com` (its DNS A record
pointing at the VPS), and the private key file (it was made on your computer: `~/bobres-vendor-keys/`).

1. **Firewall.** Allow only SSH (22), 80 and 443.
2. **Get the code**, at a release tag, in a folder of its own:

   ```sh
   git clone https://github.com/sobhanaz/bobres-3x-ui-telegram-panel /opt/bobres-vendor
   cd /opt/bobres-vendor && git checkout <the release tag>
   ```

3. **Put the key on the VPS.** On your computer, copy it over (the file stays mode 0600):

   ```sh
   scp ~/bobres-vendor-keys/vendor-v1-2026.key root@<vps>:/root/
   ```

   On the VPS, install it for the server's user (uid 65532) and remove the copy:

   ```sh
   install -d -m 700 -o 65532 -g 65532 /etc/bobres-vendor
   install -m 600 -o 65532 -g 65532 /root/vendor-v1-2026.key /etc/bobres-vendor/vendor-v1-2026.key
   shred -u /root/vendor-v1-2026.key
   ```

4. **Settings.** Copy the template, set the name, and generate the two database passwords **before the first start** (the
   database takes them once, when it is created):

   ```sh
   cd /opt/bobres-vendor/deploy/vendor
   install -m 600 .env.example .env
   sed -i "s/^LICENSE_DOMAIN=.*/LICENSE_DOMAIN=license.example.com/" .env
   sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 24)/" .env
   sed -i "s/^DB_LICENSESRV_PASSWORD=.*/DB_LICENSESRV_PASSWORD=$(openssl rand -hex 24)/" .env
   ```

   `LICENSE_SIGNING_KEY_FILE` in `.env` is only the **path** of the key file. The key never goes in `.env`. The server
   refuses to start with a `CHANGE_ME` password left in.
5. **Start.** The first build takes a few minutes (the same Dockerfile as every BOBRES image):

   ```sh
   docker compose up -d --build
   docker compose ps                          # all three: healthy
   docker compose logs licensesrv | grep "license server ready"
   ```

   The line must show `"kid":"vendor-v1-2026"`, and there must be **no** warning that "this build does not carry the
   signing key's public key". If there is, the key file and the key built into the image do not match: stop and check you
   copied the right file.
6. **Check from outside**, from any computer:

   ```sh
   curl -s -o /dev/null -w '%{http_code}\n' -X POST https://license.example.com/v1/activate -d '{}'   # 400
   curl -s -o /dev/null -w '%{http_code}\n' https://license.example.com/readyz                          # 404
   ```

   `400` means the server answered through HTTPS. `https_required` for every request means the server does not trust the
   proxy: check `LICENSE_EDGE_SUBNET` in `.env` against `docker network inspect bobres-vendor_edge`.
7. **A shortcut for the commands below.** Add to `~/.bashrc` (it records who ran each command in the audit):

   ```sh
   lic() { docker compose -f /opt/bobres-vendor/deploy/vendor/docker-compose.yml exec -T -e SUDO_USER="${SUDO_USER:-$USER}" licensesrv /app key "$@"; }
   ```

   Then `lic list` lists the licenses. `lic` alone prints the usage.

**What installs must be told.** Every install that activates online needs `BOBRES_LICENSE_SERVER=https://license.example.com`
in its `.env`: your server's public address, **https only**, the same name as `LICENSE_DOMAIN`. The customer sets it with
`sudo bobres config set BOBRES_LICENSE_SERVER https://license.example.com` (it checks the address and recreates the bot).

**Update the server** after a release: `cd /opt/bobres-vendor && git fetch --tags && git checkout <tag>`, then
`cd deploy/vendor && docker compose up -d --build`. Migrations run at start. The database and the key stay as they are.

## 2. The sales flow (manual)

There is no checkout page. You sell by your own channel (message, invoice, bank, crypto):

1. **The customer pays** you, by the channel you agreed ([refund-policy.md](../legal/refund-policy.md)). They tell you the
   domain of their store.
2. **You issue a license** (below). The command prints a **license key** once, and only its hash is kept.
3. **You send the key privately** (not in a group chat), with the server address and these steps:

   ```text
   Your BOBRES license (Standard, 1 month) for panel.customer.com.
   1. On your server, as root:   sudo bobres config set BOBRES_LICENSE_SERVER https://license.example.com
   2. Then:                      sudo bobres license activate        (paste the key; it is not shown)
      or in the dashboard: System > License > "Activate with a license key".
   Your key (keep it private): BOBRES-XXXX-XXXX-...
   ```

4. **The install keeps itself alive.** Once it has activated, core renews the token every day through the bot. You do not
   touch it.
5. **They pay again** for the next period: you renew (below).
6. **They stop paying:** you do nothing. The license ends on its date; the server refuses to renew it. The install keeps its
   last token, then 14 days of grace, then runs on the free tier (or its trial, if that still runs). Nothing of theirs is
   deleted and their customers' services keep working.

A customer who cannot reach the server can use a **hand-signed token** instead (section 4): they paste it in the License tab
or run `sudo bobres license import < token.txt`. It is not renewed, so you issue a new one each period.

### Issue

One command per tier; the tiers and their entitlements are in `internal/license/tiers.json` (`go run ./tools/license
tiers` lists them). A month is a calendar month. Add `-grace-days N` to change the 14 days.

| Sale | Command |
|---|---|
| Standard, monthly | `lic create -customer "Acme VPN" -tier standard -months 1` |
| Standard, yearly | `lic create -customer "Acme VPN" -tier standard -months 12` |
| Pro, monthly | `lic create -customer "Acme VPN" -tier pro -months 1` |
| Pro, yearly | `lic create -customer "Acme VPN" -tier pro -months 12` |
| Trial for a prospect (30 days, every premium feature but white label; every install already has an implicit one) | `lic create -customer "Maybe Co" -tier trial` |
| Your own store, no end date | `lic create -customer "Own store" -tier pro -no-end` |
| A custom deal (entitlements JSON on stdin, as in [05-license.md](../05-license.md)) | `lic create -customer "Big Co" -tier pro -entitlements - -months 12 < deal.json` |

A custom deal is checked like a token is: a limit below -1 or a misspelt field fails. A license whose `max_active_services` is 0
or missing is still made, with a `WARNING: max_active_services is 0 or missing: the install will floor it at the free tier's 25`
on stderr: the install never lets a license sit below the free tier. Set the limit (or -1 for unlimited) to say what you mean.
`updates_until` in a deal is only informational.

Put the payment reference in the customer name or keep a sheet of your own: the server records who a license is for, not what
they paid. The license id (`lic_...`) is on stderr; the key on stdout. `lic show lic_...` shows the entitlements and the
installs that use it.

### Renew

When the customer pays for the next period:

```sh
lic update lic_... -extend-months 1 -reason "paid 2026-11-14, ref 1234"
```

`-extend-months` adds the months to the current end, so someone who pays early loses nothing, and to **today** if the license
has already ended, so someone who pays late gets a full period. The install picks it up at its next daily renewal. A license
that has ended can be renewed the same way.

### Change tier

```sh
lic update lic_... -tier pro -reason "upgrade to Pro, paid 2026-11-20"
```

A new tier takes that tier's preset (limits and features). Add `-extend-months` or `-ends-at YYYY-MM-DD` if the end changes
too. To turn a trial into a paid license: `lic update lic_... -tier standard -months 1 -reason "trial converted"`.

### Revoke

```sh
lic revoke lic_... -reason "chargeback"
```

Revocation is final: the server stops renewing the license at once. The install keeps its last token until it expires
(30 days at most) and its grace ends, then runs on the free tier (or its trial, if that still runs). Nothing is deleted.
To undo a mistake, issue a new license.

### Look things up

- `lic list` shows every license, where it runs and when it was last seen. `lic list -status revoked` shows the revoked ones.
- `lic show lic_...` shows one license, its last 20 activations and its last 20 audit rows.
- The support cases (a key that moved too often, a customer who moved domain, an install that stopped renewing) are in
  [13-license-server.md](../13-license-server.md), "Support cases".

## 3. Monitoring

- **Is it up?** The check above (`POST /v1/activate` with `{}` gives `400`) from a computer other than the VPS, every few
  minutes: use an uptime service that can send a POST, or a cron job on another machine that mails you when it prints something
  else. `docker compose ps` on the VPS shows the health of the three containers.
- **Every week**: `lic list`.
  - **ENDS** within 7 days: remind the customer, or renew if they paid.
  - **LAST SEEN** older than 3 days on a license that should be active: the install cannot reach the server
    (`docker compose logs licensesrv`, and the customer's `sudo bobres logs core | grep "license refresh"`).
- **In the logs** (`docker compose logs --since 24h licensesrv`): `reactivation_limit` (a key moving too often),
  `invalid_token` or `invalid_key` in numbers (someone guessing or sharing keys), and `rate_limited` (a client calling too
  often). A normal install renews once a day.
- **The certificate** is renewed by Caddy by itself; `docker compose logs caddy` says why if it cannot (port 80 closed, DNS
  changed).
- **The VPS**: keep its system packages updated, and watch its disk (`df -h`). The database is small.

## 4. Hand-signed tokens

On **your own computer**, in a checkout of the repository, with Go installed. The key file is the same one the server uses.

```sh
go run ./tools/license sign -key ~/bobres-vendor-keys/vendor-v1-2026.key \
  --tier standard --domain panel.customer.com --customer "Acme VPN" --months 1
```

It prints the token. Send it privately. Options: `--months N`, `--days N` or `--expires YYYY-MM-DD` (the trial has a default of
30 days), `--grace-days N`, `--updates-until YYYY-MM-DD` (informational only: updates are never withheld), `--install-id ID`
(bind it to one install), `--license-id`. `go run ./tools/license tiers` lists the tiers. For a payload of your own
(`-in payload.json`), the tool prints a WARNING, and still signs, when `max_active_services` is 0 or missing (the install floors it
at the free tier's 25). (`keygen` refuses to write a key inside a git checkout; keep yours outside one.)

## 5. Backups

**The database** holds the license list: key hashes, customers, activations and the audit. Without it every key stops
working, because only hashes were kept. Back it up every night and keep copies off the server:

```sh
install -d -m 700 /var/backups/bobres-vendor
crontab -e     # add:
0 3 * * * umask 077; cd /opt/bobres-vendor/deploy/vendor && docker compose exec -T postgres pg_dump -U postgres -Fc bobres_license > /var/backups/bobres-vendor/license-$(date +\%F).dump && find /var/backups/bobres-vendor -name 'license-*.dump' -mtime +30 -delete
```

Copy `/var/backups/bobres-vendor` to another place each day (`rsync`, or an encrypted bucket): the dump names your customers and
their domains. **Restore** on this or a new VPS (deploy it as in section 1, start it once, then):

```sh
cd /opt/bobres-vendor/deploy/vendor
docker compose stop licensesrv
docker compose exec -T postgres pg_restore -U postgres -d bobres_license --clean --if-exists < /path/license-2026-11-14.dump
docker compose start licensesrv
lic list                                    # your licenses are back
```

**The signing key.** Keep **two offline copies** (an encrypted USB stick in a safe place, and a password manager attachment
or similar). Note the key id and the fingerprint next to them. If you lose every copy you must rotate the key (section 6):
installs keep their tokens until they expire, then lapse. Never put the key in a cloud note, a chat, a ticket or the repository.

## 6. Rotating the signing key

Do it once in a while (every two years, say), when a person who had the key leaves, or because you want to. If you think the
key leaked, use the next section instead.

1. On your computer: `go run ./tools/license keygen -kid vendor-v2-2028 -out ~/bobres-vendor-keys/vendor-v2-2028.key`.
   It prints the public key line. Back up the new key as in section 5.
2. Save the line as `internal/license/keys/vendor-v2-2028.pub` (keep the old file for now). Add the new key id and its
   fingerprint (`go run ./tools/license fingerprint internal/license/keys/vendor-v2-2028.pub`) to `vendorKeys` in
   `internal/license/keys_test.go`, and to the table in `internal/license/keys/README.md`. Commit only the `.pub`.
3. Publish a release. Wait until your customers have updated: `lic show` lists each install's version.
4. Copy the new key to the VPS as in section 1, point `LICENSE_SIGNING_KEY_FILE` at it in `.env`, and
   `docker compose up -d`. New tokens are signed with the new key; tokens signed with the old one still renew, because the server
   checks renewals against every key in its build.
5. Sign hand-made tokens with the new key from now on.
6. After every token signed by the old key has expired (30 days, plus the longest grace you gave), remove the old `.pub` and
   its line in the test, and publish a release.

## 7. If the private key leaks

Anyone with the key can make license tokens that installs accept, for any domain, until the installs stop trusting its public
key. It cannot read your customers' data and cannot change an install. Treat it as an emergency anyway: the damage is lost sales.
The same goes if the VPS may have been broken into (the key sits on it) or a backup of the key was exposed.

1. **Make a new key** on a clean computer: `go run ./tools/license keygen -kid vendor-v2-<year> -out <path outside the repo>`.
2. **Ship a release without the leaked public key.** Save the new `.pub`, **delete** the old `.pub`, update `vendorKeys` in
   `internal/license/keys_test.go`, and publish a release. Installs on older versions keep trusting the leaked key until they
   update: tell customers (below).
3. **Switch the server to the new key** (section 1, step 3, and `LICENSE_SIGNING_KEY_FILE`), then `docker compose up -d --build`
   with the new release. Tokens signed with the leaked key no longer renew.
4. **Ask every customer to update and activate again**: `sudo bobres update`, then `sudo bobres license activate` with their
   key (the same key works; activating again from the same install counts as a renewal, not a move). Hand-signed tokens must be
   issued again.
5. **Look for forged licenses.** A forged token never reaches your server unless its install tries to renew it. In
   `docker compose logs licensesrv`, a flood of `invalid_token` for license ids that are not in `lic list` is the sign.
6. **Remove the old key** from every place (laptop, USB sticks, backups), find out how it leaked, and write down what you changed.

Message to customers:

```text
We replaced the license signing key of BOBRES. Nothing of yours is affected and your license stays valid.
Please run, as root on your server:  sudo bobres update    and then    sudo bobres license activate
(paste the same license key). After the update and until you activate again, the License tab shows the license as not
valid, and the store runs on its trial or the free tier. Nothing is deleted and your customers' services keep working.
```

## Escalate when

- The server will not start and the log names the key file: check its owner (uid 65532), mode (0600) and path, and that it is
  the file made by `tools/license keygen`.
- The log says the build carries a different key under the same id: you deployed a build whose `internal/license/keys/<kid>.pub`
  is not this key's public half. Never edit a `.pub` to make it start: find out which key file is the right one.
- Customers report `bad_signature` on tokens you just issued: the key on the server is not the one their
  BOBRES version trusts. Compare fingerprints: `go run ./tools/license fingerprint internal/license/keys/<kid>.pub`.
