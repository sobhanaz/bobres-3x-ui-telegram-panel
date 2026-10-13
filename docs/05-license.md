# 05 - License and entitlements

Goal: one mechanism for trial / one-time / subscription / per-server / add-ons, without code changes.
Honest limit: any self-hosted software can be tampered with. The license deters casual sharing and
enables billing; the real protection is value that needs your updates and signed images.

Status (2026-10-12, Phase 6b): verification, storage, the dashboard's License tab, the CLI and the vendor's
signing tool are built. Online activation with a license key and the daily refresh are built too, against the
vendor's license server ([13-license-server.md](13-license-server.md)), which is built but not deployed yet.
**No vendor public key is embedded yet**, so every token is refused as "unknown key" until one is added (below).

## Token format
`base64url(payload JSON) "." base64url(Ed25519 signature of those payload bytes)`, both without padding
(`internal/license`). The payload is never parsed before a signature verifies. Version 1:

```json
{
  "v": 1,
  "kid": "v1-2026",
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
    "gateways": ["zarinpal", "stars", "manual_card", "manual_crypto", "manual_zarinpal"],
    "crypto_watcher": false,
    "dashboard_staff_seats": 3,
    "updates_until": "2027-09-30"
  }
}
```

- `kid` names the vendor key that signed it; it must be the key that verifies (rotation).
- Limits: `-1` is unlimited and `0` (or a missing field) is none, so a forgotten limit fails closed.
- `grace_days` is 0..90; `expires_at` must be after `issued_at`; `issued_at` gets 5 minutes of clock skew.
- Gateway names: `zarinpal`, `stars`, `usdt_trc20_auto`, `manual_card`, `manual_crypto`, `manual_zarinpal`. The
  wallet is always allowed. Automatic USDT (`usdt_trc20_auto`, Phase 6a) also needs the `crypto_watcher` feature.
- `updates_until` (YYYY-MM-DD) covers the releases issued up to that day: the release's day is the `# issued` line of
  its signed `checksums.txt` (UTC). `bobres update` refuses a later one, and its `--check`, the menu and the
  dashboard's Updates tab say "not covered by your license" (06-installer.md, "License coverage"). Running versions
  are never affected. As everywhere, a missing or invalid license gates nothing.

## Binding
- **Domain (required):** core accepts only tokens whose `domain` equals the store's `BOBRES_DOMAIN`
  (lower case, no port or trailing dot). Moving to a new domain needs a new token.
- **Install id (optional):** `core.install` holds one random id, made when the core schema is created
  (migration 00012). A token that names `install_id` works only on that install. The License tab and
  `bobres license status` show the id to send the vendor.

## States
| State | Meaning | Effect |
|---|---|---|
| `missing` | no token installed | nothing is gated ("Unlicensed" badge) |
| `invalid` | does not verify here (`reason`: `unknown_key`, `malformed`, `bad_signature`, `version`, `invalid`, `domain`, `install_id`, `unbound`, `not_yet_valid`) | nothing is gated |
| `active` | before `expires_at` | entitlements gate new actions |
| `grace` | expired, inside `grace_days` | as active, with a warning |
| `expired` | past grace | it grants nothing: the gated new actions are all refused |

**Policy for Phase 4:** a missing or invalid license never stops anything; the owner runs their own
install. A license only ever gates **new** actions, never what exists: existing resellers, servers, staff,
payment methods and every customer's service keep working.

## Enforcement points (core)
With a verified license, core refuses (409 `{"error": "state", "license": "<gate>"}`):
- `resellers`: making a reseller without the `resellers` feature;
- `max_servers`: adding a 3x-ui server beyond the limit;
- `dashboard_staff_seats`: adding an admin or support member beyond the seats, or an owner stepping down
  to admin or support when the seats are full (owners take no seat; nobody is removed over a limit);
- `gateways`: switching a payment method on (Zarinpal or automatic USDT in Payment gateways, or a manual
  method's or Stars' switch from off to on) that the license does not name;
- `crypto_watcher`: switching automatic USDT on without the `crypto_watcher` feature.

The License tab shows each entitlement with its use (servers from the provisioner, active services, staff
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
- CLI: `bobres license status`, `bobres license import < token.txt` (stdin) and `bobres license activate`, which
  run `/app license status|info|import` in the core container. An import or an activation is audited as
  `license.set` from the CLI.
- Only a token that verifies for this install and has not expired can be installed.

## "Powered by BOBRES"
Shown on the dashboard footer and the login page only when a verified license is installed without
`white_label` (an expired one included: it grants no white label). Never on an unlicensed install, and no
bot attribution in Phase 4. `/api/v1/me` carries `license: {state, white_label, powered_by}` and the public
`/api/v1/brand` carries `powered_by`.

## Vendor flow
1. Make a signing key once, **outside the repository** (the tool refuses a path inside a git checkout and
   never overwrites a file):
   `go run ./tools/license keygen -kid v1-2026 -out ~/secure/bobres-license-v1-2026.key`
   It prints the public key; save that line as `internal/license/keys/v1-2026.pub`, commit only the
   `.pub` file and release. Keep the private key offline (mode 0600); never commit it.
2. For a customer, write the payload JSON (as above, without `kid`, `v` and optionally `issued_at`) with
   their domain and, after they send it, their install id.
3. Sign: `go run ./tools/license sign -key ~/secure/bobres-license-v1-2026.key -in payload.json`. The
   tool fills in `kid`, `v` and `issued_at`, checks the payload the way core does, verifies the result and
   prints the token.
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
  revoked or moved. Nothing changes, and nothing is gated because of the failure: the installed token keeps working
  until its `expires_at`, then runs through `grace_days`, then is `expired` like any other token. Core logs "license
  refresh failed" each time.

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
- Trial length and whether trial is tied to a Telegram/email identity to stop repeats.
- Anti-tamper level: obfuscation is not planned; rely on signatures + updates + contract.
- The clock is trusted: rolling it back can extend a license (accepted limit of local verification).
