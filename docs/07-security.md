# 07 - Security and threat model

This product moves money and holds a full-admin token to a VPN panel, so the main goal is: no theft of
funds, no takeover of the 3x-ui panel, no leak of user identity. Each customer is single-tenant, which limits blast radius.

## Assets (what attackers want)
1. Wallet balances and payment flows (free credit, double credit, refund abuse).
2. 3x-ui API token (full admin over the customer's VPN infrastructure).
3. Telegram bot token (impersonate the bot, message all users).
4. Payment gateway keys and crypto deposit addresses.
5. User data: Telegram IDs, usernames, subscription links (a leaked link = free VPN + identity link).
6. Dashboard admin sessions.
7. Vendor: license signing key, release signing key, customer list.

## Trust boundaries
Internet -> gateway (Caddy) -> services on a private Docker network. Only gateway exposes 80/443.
Postgres/Redis are never published to the host. Provisioner is the only service with the 3x-ui token;
payments is the only one with gateway secrets.

Since milestone 6a both are set from the dashboard, through core, without core ever keeping them:
- A 3x-ui API token and a Zarinpal merchant id or proxy URL pass through core only in the memory of one request. Core
  never stores, logs or audits them, and no API answers them: the dashboard sees `has_token`, a merchant hint (last 4
  characters) and `proxy_set`. The audit records `{"token":"changed"}`, `{"merchant_id":"changed"}` and
  `{"proxy":"changed"|"cleared"}`. Error messages never repeat a typed secret (a proxy URL can carry a password).
- The provisioner encrypts tokens with its own master key; payments encrypts the merchant id and proxy with
  `BOBRES_PAYMENTS_MASTER_KEY` (its `BOBRES_MASTER_KEY`), a key of its own. Without that key payments runs Zarinpal
  from `.env` as before and refuses changes; a stored setting it cannot decrypt is not used (the owner enters the
  merchant id again).
- A stored panel token is only ever sent to the host it was entered for: moving a server to another host or port
  needs the token again.
- `servers.write` is the owner's and admins'. Letting BOBRES reach a private address (`allow_private`) with a new
  address or token is the owner's alone (403 for an admin, on save and on test), because the provisioner would
  otherwise be a probe of the internal network. Connection tests are limited to 10 a minute per staff member.
- Why a panel failed its health check names the panel's host and port, so only `servers.write` reads it (the Servers
  page, and a server's `detail` on the overview). Everyone else who reads the overview, support included, sees "does
  not answer".
- `gateways.write` (Zarinpal settings, the manual methods' details and their on/off switches) is the owner's.
- Automatic USDT (Phase 6a) works the same way: the store's TRON address, the optional TronGrid key and proxy are
  encrypted with the payments master key and never answered back. The address cannot change while automatic payments
  are open, so no customer pays an address nobody watches. A transaction id pays at most one payment, across the
  automatic and the manual path.

## Root admins
The store owners named in `BOBRES_ADMIN_TELEGRAM_ID` (one Telegram id, or several separated by commas, at most 10) are
the root of trust:
- They are made and unmade only on the server: the installer, `bobres menu` and `bobres admin owners`. The dashboard and
  the bot cannot create, edit, demote or remove one, and an owner who is not named there cannot give the owner role, so
  a hijacked Telegram or dashboard account never becomes a permanent owner.
- Each is made an owner by core when it starts or on their first contact with the bot, audited as
  `staff.owner_bootstrap` with the source "server settings".
- Removing an id from the setting does not demote the account: a typo must never lock the owner out. The command says so,
  and the dashboard's Staff page demotes or removes the account.
- Every root admin gets the payment alerts and the Telegram backups, and may review payments; a payment settles once,
  whoever decides first. Only the first (the main owner) takes no staff seat ([05-license.md](05-license.md)).

## HTTPS manager
The dashboard's HTTPS tab and `bobres ssl` choose how the gateway gets its certificates: automatic for the domain, a
Let's Encrypt certificate for the server's IP address, or the operator's own certificate and key
([installer, "HTTPS certificates"](06-installer.md), [runbook](runbooks/https.md)). Core keeps two properties it has
had since Phase 1: no internet access, and no control of the internet-facing gateway. So core never touches Caddy. A
small service, the `tls-agent`, does.

Who mounts what (a deploy test asserts exactly this):

| | core | tls-agent | caddy |
|---|---|---|---|
| `tls_request` (`/request`): core's request, one JSON file | read-write | read-only | none |
| `tls_result` (`/tls-result` in core, `/result` in the agent): the agent's answer | read-only | read-write | none |
| `./tls` (`/tls`): the files Caddy loads | none | read-write | read-only |
| `./caddy`: the main Caddyfile | none | read-only | read-only |
| `caddy_admin` (`/run/caddy`): Caddy's admin socket | none | read-write | read-write |
| `caddy_data`: Caddy's ACME account keys and issued certificates | none | none | read-write |

- **The tls-agent** is core's image run as `/app tls-agent`: user 65532:65532, `network_mode: none` (no network at all),
  read-only root, all capabilities dropped, `no-new-privileges`, 64 MB. It exists from release 0.1.0-rc.2 on. Every 2
  seconds it reads core's request. It trusts nothing in it. It runs the same checks as core again (`internal/tlsconf`)
  and renders Caddy's files itself, from fixed templates: a host name must match the installer's rule, an IP address
  must parse (ip mode's address as a public one), and no text of the request is copied into a file as it is. For an own
  certificate it checks the pair (readable PEM, a key that matches, not expired, covers the domain, key not encrypted).
  It writes the certificate files before the snippets that name them, each as a temp file named `.<name>.tmp` (no import
  matches it) and then a rename, and asks Caddy to load its main Caddyfile. It is the only writer of Caddy's TLS files
  and, besides Caddy, the only user of the admin socket.
- **The names are pinned.** The agent gets the install's own `BOBRES_DOMAIN` and `BOBRES_ZARINPAL_HOST` in its
  environment (compose gives it the same values as core's; they are no secret, and it has no token and no key). It
  answers `invalid` to a request that names another domain or another Zarinpal host, and writes nothing. Without
  `BOBRES_DOMAIN` it refuses every request. So a compromised core cannot have Caddy serve, or ask a certificate
  authority for, a name the install does not own. Changing the domain therefore recreates the agent with core
  (`bobres config set`).
- **The admin socket.** Caddy's admin endpoint used to be off. It is now a unix socket, `admin
  unix//run/caddy/admin.sock|0220`, in the `caddy_admin` volume, with no TCP port. A unix socket gets no Host or Origin
  check, so the file mode is the only guard: 0220 lets the owner (Caddy, running as `0:65532`) and the group 65532 (the
  agent) connect, and nobody else. The admin API can load any configuration, so whoever reaches the socket controls the
  gateway. That is why only the agent and Caddy mount it, and why the agent has no network and reads one small file.
- **Only accepted states are committed.** Core treats a change as a proposal. It validates it, writes a request with the
  next generation number, and waits up to 20 seconds for the agent's answer to that generation. On success it commits
  the row in `core.tls_config` and writes the audit entry `system.tls.set` (the mode, the IP address, the certificate's
  subject, issuer and dates; never the key). On any other answer, or none, it writes the committed state back as a new
  request and returns the agent's or Caddy's own error. A mutex and a Postgres advisory lock make the dashboard and
  `/app ssl` take turns: a change that finds another one running waits up to 3 seconds, then answers `busy`. If Caddy
  refuses a load, the agent writes its last accepted files back and loads them again. It keeps them in memory only
  (after its own restart, the files it finds on disk), so no extra copy of a key is written anywhere.
- **Startup never takes the store down.** At start core asks the agent for the committed state again. If a stored own
  certificate cannot be used any more (its key cannot be unsealed because the master key changed, it does not cover the
  current domain, or it has expired), core asks for the automatic certificate instead, leaves the row alone, and raises
  the alert `tls_problem` with the reason. No TLS error is ever returned from core's start. An own certificate is refused
  when core has no master key.
- **The private key.** Core seals it with its master key (`BOBRES_CORE_MASTER_KEY`, the envelope that also protects
  authenticator secrets) before it stores it in `core.tls_config.key_sealed`. It is never logged, audited, answered by an
  API (GET gives the certificate's names and dates only) or put in the support bundle, and the dashboard form clears
  the key field after every send. While an own certificate is in use the key exists in clear in four more places: the
  request file `tls.json` (mode 0600, in a volume only core and the agent mount), `tls/certs/key.pem` (0640, group
  65532), the agent's memory (its last accepted files) and Caddy's memory. Switching to another mode deletes the stored pair and the file, and the next request
  carries no key.
- **File modes and the 65532 group.** `tls/` is owned by 65532:65532 with mode 0750, and the agent writes its files
  0640, so group 65532 can read them. Caddy runs as `0:65532` with every capability dropped but `NET_BIND_SERVICE`, so it
  reads them as a group member. Every BOBRES image runs as 65532, so the mode does not keep other services out. The
  mount list above does. The mode keeps other users on the host out.
- **Caddy's image is pinned** by version and digest
  (`caddy:2.11.7-alpine@sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f`, the multi-arch index) in
  the compose file. Caddy 2.11.3 is the oldest version that issues IP certificates, and 2.11.1 the oldest that reloads
  on SIGUSR1. The image is not signature-checked on the server (see the supply-chain row below).
- **Who may change it.** Everyone with `system.read` sees the status. Changing the mode and detecting the server's IP
  address need `system.write`, which is the owner's, at most 10 changes a minute. `bobres ssl` needs root on the server.

What a compromised core can do: ask for one of the three modes, for the install's own domain and Zarinpal host (the
agent refuses any other name) and, in ip mode, for any public IP address, which makes Caddy try to get a certificate for
it (it fails unless the address leads to this server, and counts against Let's Encrypt's limits). It can also choose an
own certificate for the domain, which locks browsers out until the operator runs `sudo bobres ssl auto` on the server.
What it cannot do: make the gateway answer for another host name, add or change a Caddy directive (the agent writes
fixed templates), reach the admin socket or load a configuration of its own, read Caddy's ACME account keys or the keys
of certificates Caddy issued, or write outside the request file. A compromised agent would control the gateway, which
is why it has no network, no secrets of its own and a single small input.

## Updating from the dashboard
The dashboard's Updates tab can update the store, and core must still never get the Docker socket or any other power
over the host. So core only asks: it drops one small file, `request.json`, into `/opt/bobres/run/update` (core's
`/run/bobres-update`), and a systemd path unit on the host (`bobres-updater.path`) runs `bobres update --from-request` as
root, which runs the same update `sudo bobres update` runs and reports in `status.json` in the same folder
([installer, "Updating from the dashboard"](06-installer.md), [runbook](runbooks/update-rollback.md)).

| | core | the helper (root, on the host) | others |
|---|---|---|---|
| `run/update/` (`/run/bobres-update` in core): owner uid 65532, mode 0700 | read-write, the only mount of it | reads `request.json`, writes `status.json` | none |
| the Docker socket, the rest of `/opt/bobres`, `.env` | none | all (it is root, as `sudo bobres update` is) | root only |

- **What a request can say** is a channel (`stable` or `beta`) and a release (`""` for the newest, or `X.Y.Z[-label.N]`),
  plus an id, the dashboard user's name and a time. The file is decoded into exactly those fields; an unknown one, a
  missing one, text after the object, a bad value, a time older than 15 minutes or a minute ahead, or an id already
  handled (kept in root's `.updater-handled`) refuses the request, which is deleted all the same. No URL, base URL,
  environment variable or flag can be named, and the release is the vendor's: verified against the keys built into
  bobres, with the same refusals as `bobres update` (an older release, a replayed key list, an expired or
  other-channel signature).
- **The helper reads a directory core controls**, so it checks the directory first (real directories, not writable by
  others, owned by root or core), opens the request without following a link or waiting for a pipe, requires a regular
  file of at most 2 KiB owned by uid 65532 (checked on the open descriptor, the read capped) and removes whatever it
  finds in the request's place, links without following them. It writes `status.json` by a temp file and a rename, which
  replaces a link instead of writing through it. The log it keeps there is scrubbed of the install's secrets, bot
  tokens, long hex strings and public IP addresses.
- **The service** runs as root with `ProtectSystem=strict` (write access to the install, the folder bobres sits in,
  `/etc/systemd/system`, the release state and the install log only), `NoNewPrivileges`, a private `/tmp` and the
  kernel, clock and cgroup protections; it is killed after 3 hours at most. It takes the lock that `bobres update`,
  `rollback` and `restore` take, so none of them overlaps another.
- **What a compromised core can do:** start an update to a signed release newer than the installed one (so downtime, with
  the backup first and the automatic rollback), at a time the owner did not choose; keep the helper busy with requests
  (past systemd's start limit of 5 starts in 10 seconds the path unit stops, `failed`, until `bobres install`, `bobres update`
  or `systemctl reset-failed` brings it back); fill
  the disk with files in its folder; lie in what the dashboard shows. **What it cannot do:** download from anywhere but
  the vendor's release address, install anything the vendor did not sign, roll back, restore, change a setting other than
  the update channel (kept in `.env` after a successful update), run a command, or reach the Docker socket or any file
  outside the folder.
- **Not covered:** Docker with user namespace remapping (container uid 65532 is then not host uid 65532, as for `backups/` and
  `tls/`), and any program on the host that runs as uid 65532 (it could drop a request: the same signed update).
- **Off switch:** `sudo bobres config set BOBRES_UPDATE_FROM_DASHBOARD off` removes the units. The compose mount can stay: with
  no helper nobody reads the file.

## Threats and mitigations (STRIDE-style, condensed)
| Threat | Mitigation |
|---|---|
| Forged payment callback | Verify provider signature/server-to-server re-check; never trust callback params; amount+order match |
| Replayed or duplicate callback | provider_events UNIQUE(provider, event_id), idempotency keys, ledger unique key |
| Race: double spend of wallet | DB transaction with row lock on wallet, CHECK balance >= 0 |
| Underpay / wrong currency crypto | Match exact amount + network + confirmations; unique deposit reference |
| Manual receipt fraud | Admin review queue, receipt hash de-dup, audit log |
| Trial / referral abuse | One per Telegram ID, velocity limits, delayed referral reward until first paid order |
| Gift code guessing, a leaked code used twice, a code used past its limit, a database copy that gives the codes away | A code is 12 characters of a 31-symbol alphabet (about 59 bits) from `crypto/rand`; a custom code must be 8 to 32 letters and digits with at least 5 different ones (a custom code is as strong as the person who chose it; the abuse lock below is what protects it). The database keeps only a keyed hash of the code (HMAC-SHA256 under core's master key, with a domain label) and its last four characters, so a copy of the database alone gives no code and its hashes cannot be tried offline without the key (a backup with its backup key carries the master key too: generated codes still hold at 59 bits, a weak custom code would not); the code is shown once, to the staff member who made it, and is never logged, audited, exported or sent in an event. Five wrong codes in an hour lock that customer for an hour (a counter kept in the database, bumped in one statement so concurrent guesses are all counted); a locked customer is told to wait, whatever they send, and an unknown, switched-off or expired code gets one neutral answer. The redemption is one transaction that locks the code's row, checks the switch, the expiry, the total and the per-customer limit, and writes the ledger credit and the redemption together, under a key made of the code, the customer and the redemption number: a retry credits nothing twice, and the last use has exactly one winner (a concurrency test under `-race`, and a database CHECK as the last guard). Banned customers cannot redeem; maintenance mode and the required channel hold the button and the question like any other |
| Bot flooding / DoS | Per-user and global rate limits, callback de-dup, queue for broadcasts |
| A customer gets past maintenance mode or the required channel by a path nobody gated (a command, an old button, a bottom-menu tap, a question left open, a reply to a staff answer) | One gate decides every update before any handler or scene sees it, with a short allowlist for maintenance (staff and the owners the server names, `/paysupport`, a payment under way, a newcomer's first steps) and every other update answered with the maintenance text, never ignored. The Mini App holds the same rule per call through one check. Tests send the bot every command, button verb, bottom-menu item and scene (and fail when one is missing from the tables) and sweep every Mini App route read from the source ([03-bot-ux.md](03-bot-ux.md), [17-mini-app.md](17-mini-app.md)) |
| Dashboard brute force | Rate limit, lockout, 2FA, secure cookies, CSRF tokens, short sessions |
| Subscription page address guessed or scraped | 128-bit random `page_token` unrelated to any other id; the same 404 for unknown, malformed, pending, deleted or page-off; 30 requests/min per address; `no-store`, `noindex`, no referrer; no e-mail, label or Telegram data on the page; an operator can regenerate the address (audited, tokens not logged) |
| Mini App store: forged or replayed Telegram identity, another customer's order, request flooding | Telegram's `initData` is checked by the bot (only it holds the token: HMAC-SHA256, at most 1 hour old) and core never sees the token; core hands out a random session token kept as a hash, 6 hours at most, in the page's memory only; every call is bound to the session's own user and another customer's order or service is a 404; the app's sign-in is 20 a minute per address and its calls 120 a minute per customer; the page runs under a CSP that allows only itself and telegram.org and may be framed only by Telegram; on by default, and switched off it is 404; the support messages it takes are held to the store's daily limit; the store's required channel is held at the sign-in and at every call, asked of the bot (which alone can ask Telegram) and cached for 10 minutes, and the refusal carries only the channel's public link. Stars and manual payments: the amount comes from the order, a method starts only if the store offers it, a proof is accepted once for the customer's own manual payment, a receipt is a JPEG, PNG or WebP by its bytes and at most 8 MB, limited to 6 in 10 minutes, kept by Telegram (core stores only its file id and holds no bot token) and never logged ([17-mini-app.md](17-mini-app.md)) |
| SQL injection / XSS | Parameterized queries (pgx), Vue template escaping, CSP headers, input validation |
| SSRF via admin-entered URLs (3x-ui URL, webhooks) | Allow/deny lists, block metadata/loopback ranges unless explicitly allowed |
| Secret leakage in logs/backups | Redaction middleware, secrets never logged, backups encrypted with `BOBRES_BACKUP_KEY` (which is never sent with them), support-bundle scrubs. The System page's logs (the newest 500 warnings and errors per service, in memory) are kept after the same redaction and shown to the owner only, since they carry Telegram ids |
| Stolen 3x-ui token | Token stored encrypted at rest, provisioner isolated, rotation command, recommend private/HTTPS access, IP allowlist on panel. The panel URL must be https; plain http is accepted only with allow-private, and every connection is then checked to go to a private, loopback or link-local address (`xui.ErrPublicPlaintext`). This bounds the first hop only: a private next hop that NATs or proxies onward is the operator's network, and a custom HTTP client (`xui.WithHTTPClient`) is refused for plain http |
| Supply chain | Pinned deps (`go.sum`, `package-lock.json`), a govulncheck gate and a gitleaks secret scan in CI (no trivy image scan yet), release images signed with keyless cosign, with SBOM and provenance, and run by the digests built into the signed CLI (next row), minimal base images, CI with least-privilege tokens |
| Malicious update / MITM of installer | install.sh and `bobres update` check `keys.txt` against the embedded root keys, the release key's Ed25519 signature on `checksums.txt` (version, channel, expiry) and the CLI's SHA-256 before they run it, over HTTPS. BOBRES's five images run by the digests built into that signed CLI (`BOBRES_IMAGE_DIGEST_<SERVICE>`, appended to each image's tag): the release workflow takes a digest only after checking its keyless cosign signature from that workflow and tag, so a moved tag on ghcr.io changes nothing. The servers do not run cosign. Not covered by a signature: install.sh itself (fetched over HTTPS), Docker's install script (get.docker.com), the Caddy image (Docker Hub; pinned by version and digest in the compose file, which stops a moved tag but is not a signature check), the Postgres and Redis images (Docker Hub, by tag), and BOBRES images run by tag (a `BOBRES_VERSION` other than the CLI's own, or a development build of the CLI) |
| Hostile or mistaken update from the dashboard (a compromised core, a staff account) | Core has no Docker socket and no network: it can only drop a small `request.json` (channel `stable`/`beta`, an exact release name, a user name, a time) into `run/update`, the one host directory it can write, and a systemd path unit starts `bobres update --from-request` as root. That reads the file opened without following links, as a regular file of core's uid of at most 2 KiB, with every field checked and no other accepted, deletes it, ignores an id it has seen, and runs the same code as `sudo bobres update`: the release is verified against the keys built into bobres (signature, channel, key-list `seq`, expiry), never older than the installed one, backed up first, and rolled back if the store does not come back. A request has no URL, command, environment or flag to carry. Worst case: one update, to a signed release, at a time the owner did not choose, with the downtime of a restart. Not covered: Docker user-namespace remapping, or another program running as host uid 65532 (06-installer.md, "Updating from the dashboard"). `BOBRES_UPDATE_FROM_DASHBOARD=off` removes the helper |
| Hostile or mistaken HTTPS change (a compromised core, a staff account) | The owner only, 10 a minute, audited without the key. Core cannot reach Caddy: the tls-agent checks the request again, accepts only the install's own domain and Zarinpal host, and writes fixed templates. A change is committed only after Caddy accepts it, and `sudo bobres ssl auto` on the server undoes any of them |
| Hostile or mistaken update from the dashboard (a stolen owner session, a compromised core) | The owner only (`system.write`), CSRF-guarded, 10 a minute, an authenticator code when the owner has one, and an explicit confirmation for the exact release the page showed (a pre-release needs its own). Core never holds the Docker socket or any host power: it writes one small request file (an id, the channel, an optional release number, who asked, when; no URL, flag or environment) into a folder shared with a root systemd path unit, whose updater accepts it only from core's own uid, fresh, once, and then runs the same `bobres update` as the command line: the release's signed `checksums.txt`, the CLI's SHA-256, a backup first and an automatic rollback if the new version is not healthy. So a hijacked session or core can start the update of a genuine, signed release of the channel and nothing else: it cannot choose code, a source or a downgrade past what the updater verifies. Every request and its end is audited (`system.update.*`) and sent to every owner on Telegram, so an update nobody asked for is noticed. The updater's `status.json` is read as untrusted text (no link followed, size and field limits, escaped in the page, its log shown to the owner only). Cost of a hijack: an unwanted but genuine update and a few minutes of downtime; `sudo bobres update` and the runbook's rollback undo it |
| Hostile or mistaken update request (a compromised core, a staff account, a stale or forged request file) | Core has no Docker socket and no write access to the host but one folder; its request names only a channel and a release, checked by pattern, fresh (15 minutes), used once and read the careful way (owner uid 65532, no link, no pipe, 2 KiB); the update is `bobres update` itself, so the release must be vendor-signed and newer than the install, with the backup first, the automatic rollback and one lock for update, rollback and restore. At worst: an untimely update of a signed release (downtime), a busy helper, a full disk. `sudo bobres config set BOBRES_UPDATE_FROM_DASHBOARD off` removes the helper |
| Leak of an own certificate's private key | Sealed with core's master key in the database; never logged, audited, answered or put in the support bundle; deleted when the mode changes. It is in clear only in the request volume, `tls/certs/` and the memory of the agent and of Caddy while that mode is on |
| The panel answers anyone who scans the server's IP address (ip mode) | Warned about in the dashboard and the runbook, and the owner confirms an address the bot did not detect. Dashboard links move to the IP address only while Caddy serves a publicly trusted certificate for it and the bot's own detection of the server's public address, at most 5 minutes old, is that same address (a recycled address keeps a valid short-lived certificate for days, so the certificate alone proves nothing). Where the two never agree (a VPN, a NAT egress, a floating IP address) links stay on the domain, and `BOBRES_PUBLIC_URL` overrides. The panel still needs a login |
| License key sharing | Install binding, re-activation limits, revocation; accept residual risk |
| Compromised host | Non-root containers, read-only FS, dropped capabilities, ufw, fail2ban, unattended upgrades |
| Insider/staff abuse | Roles with least privilege (payment details, staff and the owner role are the owner's), mandatory reason on money and staff actions, an append-only audit log (database triggers refuse UPDATE, DELETE and TRUNCATE) that records where each change was made |
| Subscription link leak | Long random tokens, rotate/reset button, optional per-device limits (HWID in 3x-ui) |

## Crypto and secrets
- Secrets at rest: envelope encryption with one master key per service (from `.env`: `BOBRES_CORE_MASTER_KEY`,
  `BOBRES_PROVISIONER_MASTER_KEY`, `BOBRES_PAYMENTS_MASTER_KEY`, each passed to its own service only); the DB stores
  ciphertext for panel tokens, gateway secrets and authenticator secrets. A backup of the database is useless for
  these without the matching key, so the keys are backed up with it.
- Gift codes are not encrypted but hashed with a key: HMAC-SHA256 under a key derived from `BOBRES_CORE_MASTER_KEY`
  (see the threats table). The codes cannot be recovered from the database, and a master key that is changed or
  lost makes the codes already made unredeemable (the hashes no longer match), so make new ones; the key is in every
  backup, and a restore brings it back.
- Passwords: argon2id. TOTP for staff. Constant-time comparisons for tokens/signatures.
- TLS everywhere external; internal service auth via one token per service, each server accepting only its legitimate callers (mTLS is not built; auth is one interceptor), see PLAN.md §11.
- Backups (the `backup` service, docs/06-installer.md): each file is encrypted with `BOBRES_BACKUP_KEY` (a random
  file key per backup, wrapped with a key derived from it by HKDF; 64 KiB AES-256-GCM chunks with counters and a
  final flag, so reordered, cut or edited files fail). The installer shows the key once and the menu shows it again
  after a confirmation; it is never sent over Telegram, and the backup files are. **The archive carries the three
  master keys** (owner decision, milestone 6b): a backup plus the backup key restores everything, including panel
  tokens, gateway settings and staff 2FA secrets, so the backup key is as sensitive as the whole `.env`.
- The backup service reads the database as `svc_backup`, a read-only role (`pg_read_all_data`, CONNECT; no
  schema ownership, no writes), never as the admin role. Its password reaches pg_dump in `PGPASSWORD`, not on a
  command line, and pg_dump gets no other environment. The service is on `backend` plus its own internal `offline`
  network (no internet). Only while the owner has the S3 upload set up (`BOBRES_BACKUP_NETWORK=egress`, which the
  installer and `bobres config` derive from `BOBRES_BACKUP_S3_ENDPOINT`, never set by hand) is it on `egress`
  instead of `offline`: then the container that holds the master keys and `BOBRES_BACKUP_KEY` reaches the internet,
  and it uploads there only the `.bkp` file as it is on disk, already encrypted with `BOBRES_BACKUP_KEY` (which
  never leaves the server). Core lists and starts backups with a token for that service only
  (`BOBRES_TOKEN_CORE_BACKUP`), never its own service token: the container holds the master keys, and core's token
  would let code running in it act as core towards payments, provisioner and the bot (whose broadcast test sends any
  text to any chat). The service hands files only to the bot, with its own token (`BOBRES_TOKEN_BACKUP`), which sends
  them to the configured owners and to no chat the caller names: the file is uploaded once, to the main owner, and the
  others get it from Telegram's servers by its file id.
- Key rotation procedures documented and scripted (`bobres secrets rotate`).

## Privacy
- Data minimization: Telegram ID + language + service data. No phone/email unless a feature needs it.
- Retention limits for logs and provider events; user deletion = anonymize, keep ledger integrity.
- Vendor never receives customer end-user data.

## Security process
- Threat-model review each phase; dependency updates weekly; security tests for money flows (race, replay, tamper).
- `SECURITY.md` with a disclosure contact (not written); incident runbooks (built: docs/runbooks/, including rotating
  secrets).
- Pentest or external review before selling to third parties.

## Residual risks (accepted, stated)
- Self-hosted code can be modified by its owner. License protects billing, not secrecy.
- The 3x-ui API token is inherently full-admin; we can only isolate and protect it.
- Legal/regulatory exposure of the VPN business is outside technical control (see 09).
- In ip mode the server's IP address is published in Certificate Transparency logs, and Caddy answers any client that
  connects to that address without a name (`default_sni`). Anyone who scans the address finds the store's login page.
  The same `default_sni` answers on an install whose `BOBRES_DOMAIN` is an IP address, in any mode.
- An IP address as `BOBRES_DOMAIN` in auto mode (a private or LAN address of a lab or an office) is served with
  Caddy's own certificate authority: browsers do not trust it, so a visitor learns to click through a warning. Use a
  domain, or ip mode with a public address, for a store with customers.
- An own certificate is not renewed. When it ends, browsers refuse the store until the operator uploads a new one (the
  alert `tls_expiring` gives notice).

## Open points
- DECIDED: per-service tokens first. mTLS is not built.
- DECIDED (milestone 6b): the master keys travel inside each encrypted backup, so a backup and `BOBRES_BACKUP_KEY`
  restore everything on a new server. The backup key itself is shown once by the installer and again in `bobres
  menu`; the operator keeps it off the server.
- DECIDED: every password login needs an authenticator code, for every role. Link logins come from the bot or from
  `bobres admin link` on the server. The one exception is the initial password (below): it needs no code, because
  no authenticator exists yet, and it opens a restricted session that can only choose a new password and set up 2FA.
- DECIDED (WP-B): the owner's initial dashboard password. `bobres admin password` (and a fresh install) runs core's
  `admin-password`: 16 characters from an unambiguous alphabet (crypto/rand), stored as an argon2id hash with
  `must_change = true` and no authenticator; shown once on the terminal and never written to a log, a state file,
  `.env` or the audit entry (which says only that it was reset from the CLI). It works only while `must_change` is
  set and no authenticator is confirmed; wrong tries count and lock like any password login (5 failures, 15 minutes,
  the same answer text). The session is `password_initial`: it lasts 1 hour, cannot be refreshed, and core answers
  403 `must_change_password` on every route except `GET /me`, logout and the two password endpoints. The initial
  password cannot be reused as the new one (400 `password_reused`). Confirming the first code clears `must_change`,
  upgrades the session to a normal one and logs the other devices out. Running the command again resets the login
  (new password, authenticator removed, every session ended, the lock lifted); a loss of the phone is handled the
  same way. A link login is always a full session. The command needs shell access to the server (docker), which
  is already the trust boundary of `bobres admin link`.
- Rotating the master keys (re-encrypting what they protect) is not built.
