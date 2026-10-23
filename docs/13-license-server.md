# 13 - Vendor license server

`licensesrv` (`cmd/licensesrv`, `internal/licensesrv`) is the vendor's own server. It turns a secret activation
key into a signed license token for one install, refreshes that token every day, limits how often a key moves to
another install, and stops refreshing revoked licenses. This page is the vendor's runbook. How installs check
tokens: [05-license.md](05-license.md).

Status (2026-10-14): built and tested. The vendor key `vendor-v1-2026` (fingerprint `2E02 AB14 5379 C040 2DBD 7BC2 51A3
C9D7`) exists and its `.pub` is built into BOBRES, so installs verify what it signs. The
deployment kit is `deploy/vendor/` and the day-to-day runbook is
[runbooks/vendor-license-server.md](runbooks/vendor-license-server.md): start there to deploy the server and to sell
licenses. This page keeps the rules, the API and the details. Not deployed yet: that is the owner's step
([launch-checklist.md](launch-checklist.md)). What a license costs: [pricing.md](pricing.md).

## What it is, and what it is not

- **Vendor-only.** It is not in the release images, not in the customer compose and not in `internal/migrate`. It
  has its own Postgres database and its own embedded migrations (schema `licensesrv`).
- **Installs never talk to it from core.** Core has no internet access. `bobres license activate` (it asks for the
  key without echoing it) calls it from the host, and the dashboard's activation and the daily refresh go through
  the bot, which only passes the call on and never stores a token. Core verifies every answer before installing it.
  Both ways need the server's address as `BOBRES_LICENSE_SERVER` in the install's `.env`, set with
  `sudo bobres config set BOBRES_LICENSE_SERVER https://...`.
- **It does not change the install policy.** An install without a valid license is not blocked: it runs on the free
  tier, after its 30-day trial ("free core, paid premium", 05-license.md), and a license only ever gates new actions.
  Revoking a license means the server stops refreshing it: the install keeps its last token until that token
  expires and its grace ends. After that the trial (if it still runs) or the free tier applies, and new servers,
  services, staff, resellers and payment methods beyond the free tier are refused. Nothing is deleted, and an owner
  who removes the token is simply on the free tier. A stricter "vendor mode" is a later policy decision.

## The rules

- **Keys.** `BOBRES-` followed by 8 groups of 4 base32 letters (160 random bits), shown once at
  `licensesrv key create`. The database keeps only the key's SHA-256. A fast unsalted hash is enough because the
  key is random and cannot be guessed. A database leak gives no usable key, and a lost key cannot be shown again:
  make a new license instead.
- **Activation** (`POST /v1/activate`) binds the license to an (install id, domain) pair:
  - Activating again from the pair that is already active is a refresh: nothing is counted.
  - Activating from another pair moves the license there and ends the old activation.
  - The first activation is free. After that, at most **3 moves in any 365 days** are allowed. Moving back to an
    earlier install counts too, and so does a new domain on the same install.
  - Reinstalling with a fresh database (a new install id) or changing `BOBRES_DOMAIN` uses a move. Restoring a
    backup keeps the install id.
  - When the limit is reached, the answer names the date the next move becomes possible (a year after the oldest
    counted move).
- **Refresh** (`POST /v1/refresh`): the install's current token is the credential.
  - It must verify against a key this build knows: the online key, or any key in `internal/license/keys`. Tokens
    signed before a key rotation therefore still refresh.
  - Its install id and domain must be the license's active activation.
  - The license must not be revoked or ended.
  - An expired token still refreshes while its binding holds, for example from an install that was offline for
    weeks. A stolen token is worthless elsewhere: the new token is bound to the same install and domain.
- **Tokens** are short-lived:
  - `expires_at` is the earlier of the license's end and now + 30 days.
  - `grace_days` is the license's grace (14 by default, 0 to 90).
  - `issued_at` is backdated 5 minutes, because installs refuse a token issued more than 5 minutes ahead of their
    clock.
  - `license_ends_at` is the license's own end, for display. It is left out for a license with no end date, and
    older cores ignore it.
  - Everything else (customer, tier, entitlements) comes from the license as it is now. A renewal or an upgrade
    therefore reaches the install at its next daily refresh.
- **Ended licenses.** Once `ends_at` has passed, activation and refresh are refused (`expired`). The install
  decays through grace, as after a revocation.
- **Revocation is final.** A revoked license cannot be activated, refreshed or updated. To undo a mistake, make a
  new license for the customer.

## API

Both endpoints take and return JSON. They answer only requests that arrived over HTTPS at a trusted proxy (see
Hardening). Unknown request fields are ignored, so newer installs can send more than the server reads. Every
answer carries `Cache-Control: no-store`.

```text
POST /v1/activate  {"key": "BOBRES-XXXX-...", "install_id": "<uuid>", "domain": "panel.example.com", "version": "0.2.0"}
POST /v1/refresh   {"token": "<the install's current token>", "version": "0.2.0"}      (version optional)

200 {"token": "<signed token>"}
4xx/5xx {"error": "<code>", "message": "<English sentence for the operator>"}
```

- The key is matched case-insensitively and spaces are ignored. `install_id` is the install's UUID
  (`bobres license status`). `domain` is normalized as core does it: lower case, with no port or trailing dot.

| Status | `error` | Meaning |
|---|---|---|
| 400 | `invalid` | Bad JSON, a key not in the `BOBRES-…` form, a bad install id, domain or version, or an empty token |
| 403 | `invalid_key` | No license has this key |
| 403 | `revoked` | The license is revoked |
| 403 | `expired` | The license has ended (the message has the date) |
| 403 | `invalid_token` | Refresh: the token does not verify here, is not bound to an install, or names no license |
| 403 | `https_required` | The request did not come over HTTPS through a trusted proxy |
| 409 | `reactivation_limit` | A fourth move inside 365 days (the message has the date of the next free move) |
| 409 | `not_active` | Refresh: the license is now active on another install or domain; activate again with the key |
| 429 | `rate_limited` | Too many requests; `Retry-After` says when to try again |
| 500 | `internal` | Server or database trouble; try later |

An install should keep its current token on any refusal, so it decays through grace and never drops at once.
`/healthz` and `/readyz` answer on the same listener for the local health check. The proxy should not expose them.

## The vendor's own features and the `private_features` entitlement

Selling licenses from a bot, managing this server from the dashboard and reselling a supplier's accounts are the vendor's own and are
not for sale. They are part of every BOBRES build but locked: a store's license must carry the entitlement `"private_features": true`
(`internal/license`, `Entitlements.PrivateFeatures`), which no tier preset has. Give it to the vendor's own store only:

```bash
docker compose --env-file .env exec -T licensesrv /app key update LICENSE_ID -reason "own store: private features" -entitlements - < ent.json
```

with the license's entitlements plus `"private_features": true`. The store picks it up at its next daily refresh.

**One screen to pay and top up.** When a buyer's USDT wallet is short, the bot's confirm screen says how much is missing and
offers the top-up. Tapping it saves the buyer's *intent* (a row in `core.vendor_intents`: the tier, 1 or 12 months, the
license it is for; one per buyer, kept 48 hours, dropped by a manual buy) and opens the payment. When a top-up (a ledger entry of kind
`topup`) is credited **after** the intent was made and the wallet covers the price, core's worker (`vendor-intents`, every 30 s)
buys the license by itself through the same wallet-debit and `vendor_orders` path as a manual purchase, and publishes
`license.order_settled.v1`; the bot then sends the key (it fetches the key from core, the event carries none). Money that was
in the wallet before the intent, a gift code or a staff adjustment buys nothing. The intent is claimed (deleted) in the same
transaction as the debit, so a wish that was replaced, bought by hand or taken by another worker is not carried out twice;
a failure that may pass (the database, a balance that moved) keeps it until it expires, one that never can (a banned
buyer, a license that ended) forgets it. A refused or refunded order clears the intent, so nothing is bought twice or
against the buyer's will.

## The signed admin API (optional, off by default)

`licensesrv key ...` is the admin tool. For a program that sells licenses (the owner's own BOBRES store), the same
changes are available as a signed HTTP API, **off unless `BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE` names a file with
the admin public key**. The key is not the license signing key: make a pair with `licensesrv adminkey` (it prints
both once). The public key stays on this server; the private key goes only into the store that sells.

```text
POST /admin/v1/licenses/create   {"ref", "customer", "tier": "trial|standard|pro", and one of "months": 1..120, "ends_at": "YYYY-MM-DD" (or RFC3339,
                                 at most 10 years away) or "no_end": true; a trial may give none: its preset's 30 days}  -> the license and its key, once
POST /admin/v1/licenses/extend   {"ref", "id", "months"}     renews: adds months to the end (or to now, when it has passed)
POST /admin/v1/licenses/update   {"ref", "id", and any of "customer", "tier", "ends_at" or "no_end"}   a new tier takes its preset entitlements
POST /admin/v1/licenses/tier     {"ref", "id", "tier"}       standard or pro: the new tier's preset entitlements
POST /admin/v1/licenses/revoke   {"ref", "id", "reason"}
POST /admin/v1/licenses/rekey    {"ref", "id"}               a new key, only while nobody has activated the license
POST /admin/v1/licenses/get      {"id"}                      with its entitlements, where it ran (30 rows) and its audit (30 rows)
POST /admin/v1/licenses/list     {"status": "" | "active" | "revoked"}      at most 500, newest first
```

- Every request carries `X-Bobres-Ts`, `X-Bobres-Nonce` and `X-Bobres-Sig`: an Ed25519 signature over the method,
  the request URI, the timestamp, the nonce and the SHA-256 of the body (`internal/license/adminsig.go`, one
  implementation for the client and the server). The timestamp must be within two minutes of the server's clock, and
  a nonce works once (kept ten minutes). **Every authentication failure is the same `401 unauthorized`.**
- `ref` (8 to 100 characters of `A-Za-z0-9._:-`) makes a change happen once: a retry with the same `ref` returns the
  first answer with `"replayed": true` and changes nothing; the same `ref` for another action is `409 ref_reused`.
  A refused request (404, 409, 400) leaves its `ref` free. A key is in the answer to `create` (and `rekey`) only.
- It is rate limited per client address (`BOBRES_LICENSESRV_RATE_ADMIN`, default `60/1m`), answers only over HTTPS
  through a trusted proxy like `/v1`, and every change is in the audit with the actor `api:admin` and the address.
- **Who calls it:** the owner's own store. Its core has no internet access, so core signs each request with the admin
  private key (dashboard > Licenses; stored encrypted, write-only) and the bot passes it, unchanged, to the one license
  server the bot is configured for (`BOBRES_LICENSE_SERVER`; the bot's `POST /internal/vendor/admin`, core's service
  token). The bot never holds the admin key and cannot be told to call another address.
- **Reaching it:** the tunnel or proxy that serves `/v1/` must also serve `/admin/v1/` (with the Cloudflare Tunnel kit:
  add a public hostname rule for the license host with the path `/admin/v1/*` to the same service; leave it out and the
  API answers locally only).

## Set up the server

The examples use the host name `license.example.com`, the system user `licensesrv`, and the directory
`/etc/bobres-licensesrv`.

### 1. The signing key

The vendor has one signing key, `vendor-v1-2026`. It signs the tokens the server issues and the ones made by hand with
`tools/license`; its public half is `internal/license/keys/vendor-v1-2026.pub`, built into every BOBRES release. (You could
keep a second key for the server, apart from the one used by hand, if you want to be able to retire one without the other:
make it the same way and add its `.pub` next to the first.) A key is made outside any repository. The tool refuses a path
inside a git checkout and never overwrites a file:

```sh
go run ./tools/license keygen -kid vendor-v2-2028 -out ~/bobres-vendor-keys/vendor-v2-2028.key   # prints the public key line
```

- Save the printed line as `internal/license/keys/<kid>.pub`, add the key to `vendorKeys` in
  `internal/license/keys_test.go`, commit **only the `.pub`**, and ship a release.
- Until installs run a release with that `.pub`, they refuse the server's tokens as `bad_signature`, so they run on the
  trial or the free tier (nothing is deleted). The server logs a warning at start while its own build lacks the key.
- It refuses to start if the build carries a different key under the same kid.
- Keep two offline copies of the private key, for example on an encrypted USB stick. Never commit it, and never put it
  in a container image.
- The server refuses a key file inside a git checkout and warns when other users can read it (`chmod 600`).

### 2. Postgres

The server's own database:

```sql
CREATE ROLE licensesrv LOGIN PASSWORD '<random>';
CREATE DATABASE bobres_license OWNER licensesrv;
```

The server makes its schema `licensesrv` and applies its migrations at every start (also `licensesrv migrate`). The
role needs to own the database (or have CREATE on it).

### 3. Build and install

```sh
go build -trimpath -ldflags "-s -w -X github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version.Version=$(git describe --tags)" \
  -o licensesrv ./cmd/licensesrv
sudo install -m 755 licensesrv /usr/local/bin/licensesrv
```

To run it in Docker instead (the recommended way), use the kit in `deploy/vendor/`: a compose file with the server,
Postgres and Caddy (HTTPS), hardened like a BOBRES install, with the key mounted read-only from a file outside the
repository. It builds the image from `deploy/Dockerfile` with `SERVICE=licensesrv`. Never push that image to the public
registry. The steps are in [runbooks/vendor-license-server.md](runbooks/vendor-license-server.md).

### 4. Environment

`/etc/bobres-licensesrv/env` (owned by `licensesrv`, mode 600). The database URL, with its password, goes in its
own file next to it (same owner and mode):

```sh
BOBRES_DATABASE_URL_FILE=/etc/bobres-licensesrv/database-url    # postgres://licensesrv:...@127.0.0.1/bobres_license
BOBRES_LICENSESRV_SIGNING_KEY_FILE=/etc/bobres-licensesrv/vendor-v1-2026.key
BOBRES_HTTP_ADDR=127.0.0.1:8080
BOBRES_LOG_FORMAT=json
```

| Variable | Default | Meaning |
|---|---|---|
| `BOBRES_DATABASE_URL` / `_FILE` | (required) | The server's Postgres |
| `BOBRES_LICENSESRV_SIGNING_KEY_FILE` | (required for `serve`) | The online key file from `tools/license keygen` |
| `BOBRES_HTTP_ADDR` | `127.0.0.1:8080` | The plain-HTTP listener the proxy forwards to; keep it off the network |
| `BOBRES_LICENSESRV_TRUSTED_PROXIES` | `127.0.0.1/32,::1/128` | Proxies whose `X-Forwarded-For` / `X-Forwarded-Proto` are believed (CIDRs or addresses) |
| `BOBRES_LICENSESRV_RATE_IP` | `120/1h` | Requests per client address (an IPv6 /64 counts as one) |
| `BOBRES_LICENSESRV_RATE_KEY` | `10/1h` | Requests per license: activations with its key and refreshes of its tokens together |
| `BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE` | (empty: off) | The admin public key file, which turns the signed admin API on |
| `BOBRES_LICENSESRV_RATE_ADMIN` | `60/1m` | Requests per client address to the admin API |
| `BOBRES_LICENSESRV_ALLOW_HTTP` | `false` | Accept requests that did not come over HTTPS: local testing only |
| `BOBRES_LOG_LEVEL`, `BOBRES_LOG_FORMAT`, `BOBRES_SHUTDOWN_WAIT` | as every service | Logging and shutdown |

### 5. systemd

`/etc/systemd/system/licensesrv.service`:

```ini
[Unit]
Description=BOBRES license server
After=network-online.target postgresql.service

[Service]
User=licensesrv
EnvironmentFile=/etc/bobres-licensesrv/env
ExecStart=/usr/local/bin/licensesrv serve
Restart=always
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadOnlyPaths=/etc/bobres-licensesrv

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload && sudo systemctl enable --now licensesrv
journalctl -u licensesrv -n 20     # "license server ready" with the kid, rates and proxies
curl -fsS http://127.0.0.1:8080/readyz
```

### 6. HTTPS proxy

The listener speaks plain HTTP on localhost. TLS ends at the vendor's proxy, which must send
`X-Forwarded-Proto` and `X-Forwarded-For`. Expose only `/v1/`. Caddy sends both headers by default:

```text
license.example.com {
    handle /v1/* {
        reverse_proxy 127.0.0.1:8080
    }
    respond 404
}
```

With nginx, set the headers yourself:

```nginx
location /v1/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

Check from outside: `curl -s https://license.example.com/v1/activate -d '{}'` answers `400 invalid`. If every
request gets `https_required`, the proxy is not in `BOBRES_LICENSESRV_TRUSTED_PROXIES` (for example, a Docker
network address) or does not send `X-Forwarded-Proto`. Installs need the URL as `BOBRES_LICENSE_SERVER`
(`https://license.example.com`, https only) in `/opt/bobres/.env`. The compose file passes it to the bot.
`sudo bobres config set BOBRES_LICENSE_SERVER https://license.example.com` checks the URL, writes it and recreates
the bot; a value edited into `.env` by hand that is not https stops the bot.

## Daily work

Run the admin commands on the server as the service user, with the same environment (only the database is needed):

```sh
sudo -u licensesrv sh -c 'set -a; . /etc/bobres-licensesrv/env; licensesrv key list'
```

The audit names who ran each command (`cli:<user>`: the user behind `sudo`, else the system user).

### Sell a license

The tiers (`standard`, `pro`, `trial`) are presets, in `internal/license/tiers.json` (docs/pricing.md). A license of a tier
takes its entitlements and its default grace; the trial lasts 30 days if no end is given:

```sh
licensesrv key create -customer "Acme VPN" -tier standard -months 1
licensesrv key create -customer "Acme VPN" -tier pro -months 12
licensesrv key create -customer "Maybe Co" -tier trial
```

For a custom deal, write the entitlements yourself: the same JSON as in docs/05-license.md. It is read strictly, so a
misspelt field fails. A limit of `-1` means unlimited, and a missing limit means none, but an install never lets a license sit below the
free tier: a missing `max_active_services` still sells 25 services, and `licensesrv key create` and `key update` print a
WARNING ("max_active_services is 0 or missing: the install will floor it at the free tier's 25") and carry on.
`go run ./tools/license entitlements pro` prints a preset's JSON as a starting point.

```json
{"white_label": false, "max_servers": 2, "max_active_services": 500, "resellers": true,
 "gateways": ["zarinpal", "stars", "manual_card"], "crypto_watcher": false,
 "dashboard_staff_seats": 3, "max_join_chats": 1, "updates_until": "2027-10-31"}
```

```sh
licensesrv key create -customer "Acme VPN" -tier standard -entitlements standard.json -days 365
```

- `-months N` is N calendar months from now (a subscription period). `-ends-at 2027-10-31` ends the license at 00:00 UTC on
  that day. `-days N` counts from now, and `-no-end` sells a license with no end date. `updates_until` only says up to
  which date the license was sold with updates: installs add a note to a later release and install it all the same
  (docs/05-license.md), so it never limits the license's life. The entitlements an install gets are never lower than
  the free tier's: a license that omits `max_active_services` still sells 25 services (the server warns when a license
  sets it to 0 or leaves it out).
- `-grace-days` defaults to the tier's (14 for Standard and Pro, 0 for the trial).
- The key is printed once on stdout; stderr has the license id. Send the key privately, with the server's address.
  The customer runs `sudo bobres config set BOBRES_LICENSE_SERVER https://license.example.com` (it checks the URL
  and recreates the bot). Then they either use "Activate with a license key" on the License tab, or run
  `sudo bobres license activate`, which asks for the key and does not echo it. From a file, they run
  `sudo bobres license activate < key.txt` and delete the file afterwards.
- Tell the customer not to put the key on the command line (`sudo bobres license activate BOBRES-…`). It works, but
  the key then stays in the shell history and in sudo's log (`auth.log` or the journal), and other users on the
  server can see it in `ps` while the command runs. The key alone is enough to move the license to another install,
  within the move limit.

### Look things up

- `licensesrv key list [-status active|revoked]`: every license with where it runs, when it was last seen and its
  moves in the last year.
- `licensesrv key show lic_…`: the license, its entitlements, its last 20 activations (domain, install id, first
  or move, last seen, version) and its last 20 audit rows, including refused moves. Keys are never shown.

### Renew, upgrade, rename

```sh
licensesrv key update lic_… -extend-months 1 -reason "paid 2026-11-14"
licensesrv key update lic_… -tier pro -reason "upgrade to pro"
licensesrv key update lic_… -tier pro -entitlements pro.json -reason "custom upgrade"
```

`-extend-months N` renews: it adds N months to the current end, or to today when the license has already ended. A new tier
takes that tier's preset unless `-entitlements` is given. Only the flags given change. The audit keeps the license before and after, with the reason. The install picks up
the change at its next daily refresh. A license that has already ended can be renewed the same way: once the
customer's install refreshes, or they activate again, it works again.

### Revoke

```sh
licensesrv key revoke lic_… -reason "chargeback"
```

From then on, activation and refresh answer `revoked`. The install keeps its last token for at most 30 days plus
grace, then the trial (if it still runs) or the free tier applies: new actions beyond the free tier are refused and
nothing is deleted.

### Support cases

| Case | What to do |
|---|---|
| "Activation says the key has moved too often" | `key show` lists the moves and the refused attempt. Either the customer waits for the date in the message, or you make a new license (`key create`) and revoke the old one |
| "My key does not work" | `invalid_key`: a typo, or a key from a license whose key was lost. Keys cannot be shown again; make a new license |
| A customer moved their store to a new domain or server | Expected: their activation uses one move. The old install stops refreshing (`not_active`) and decays |
| A customer's install stopped refreshing | `key show` gives the last-seen time and version. The bot needs internet access, and `BOBRES_LICENSE_SERVER` must be set on the install (`sudo bobres config get BOBRES_LICENSE_SERVER`; `sudo bobres config set BOBRES_LICENSE_SERVER https://...` sets it and recreates the bot). Without it the bot answers `not_configured` and core logs "license refresh failed" every 3 hours |
| A leaked key | Revoke the license and make a new one for the customer. A leaked key lets someone else take the license, within the move limit |

## Hardening

- **HTTPS only.** `/v1` answers only requests that a trusted proxy received over HTTPS (`X-Forwarded-Proto: https`).
  The listener stays on localhost, and the clients accept only `https://` URLs.
- **Client address.** Only a trusted proxy's `X-Forwarded-For` counts. It is read from the right: the first
  address that is not one of our proxies is the client, and anything to its left could be forged.
- **Rate limits.** The limits are kept in memory:
  - per client address (an IPv6 /64 counts as one);
  - per license: activations with its key and refreshes of its tokens share one budget. It is counted only once
    the key is found or the token verifies, so random keys cannot fill the table, and a forged token naming
    someone's license cannot use up its budget.

  Many installs can share one exit address (a proxy abroad), so the IP limit is generous. Each table holds at most
  100,000 callers. When it is full, a new caller is still served and the server forgets the window that started
  first, so a flood of addresses (an IPv6 /48 holds 65,536 /64s) cannot lock real installs out of activating or
  refreshing. Forgetting a window only lets that caller in early. Run **one** server process: each process counts
  on its own, and a restart forgets the counts.
- **What is stored.** The database holds key hashes, never keys, and it never holds tokens. The audit table is
  append-only (a trigger refuses UPDATE, DELETE and TRUNCATE). It records who did what, the activations, moves and
  refused moves, with the caller's IP address for API rows.
- **What is logged.** Logs give license ids, domains, install ids, IP addresses and refusal codes. They never
  contain a key or a token.
- **Concurrency.** An activation locks its license row, so two installs activating one key at the same moment are
  handled one after the other, and a partial unique index allows only one active activation per license.

## Rotating the signing key

1. Make `vendor-v2-2028` (step 1). Ship its `.pub` in a release next to `vendor-v1-2026.pub`.
2. Once installs have that release, point `BOBRES_LICENSESRV_SIGNING_KEY_FILE` at the new key and restart the
   server. Tokens signed with `vendor-v1-2026` still refresh, because the server checks refresh tokens against every key
   in its build.
3. Remove `vendor-v1-2026.pub` in a later release, once every token it signed has expired (30 days plus grace).

If the key leaks, anyone can make tokens that installs accept, until installs drop its `.pub`. Ship a release without
that `.pub` and with a new key at once, switch the server to the new key, and tell customers to update and activate again.
Until they update, their installs still accept the leaked key. After the update their installs show the license as
invalid (`bad_signature`) and run on the trial or the free tier until they activate again; nothing is deleted and
existing services keep working. The step-by-step is in
[runbooks/vendor-license-server.md](runbooks/vendor-license-server.md), section 7.

## Backups

- **The database.** Back it up daily (`pg_dump -Fc bobres_license`) and keep a copy off the server. Without it,
  every key stops working: only hashes were kept, so keys cannot be rebuilt.
- **The signing key.** Keep two offline copies. If every copy is lost, rotate (above). Installs keep their tokens until they
  expire.

## Known limits

- Policy: an install without a valid license runs on the free tier, and the limits refuse only new actions
  (05-license.md). The server cannot force anything; it only stops refreshing.
- Installs trust their clock. Short tokens and daily refresh narrow, but do not close, the "roll the clock back"
  gap.
- There is one process and in-memory rate limits (above). No admin web UI: the CLI is the admin interface.
- There are no trials tied to a Telegram or email identity yet, and no signed revocation list (`GET /v1/revoked`).

## Open points

- A support override that frees a move without making a new license.
- Whether to keep caller IP addresses in the audit for less time than the rest of it (privacy).
