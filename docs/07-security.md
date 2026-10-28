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
  on SIGUSR1 (the installer reloads with `caddy reload` through the admin socket, never with a signal through Docker: see
  [installer, "Starting at boot"](06-installer.md)). The image is not signature-checked on the server (see the supply-chain row below).
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

## Starting at boot
`bobres.service` runs `bobres start --dir /opt/bobres` as root at every boot (`Requires=docker.service`), so that the store
comes back whatever Docker's restart policy did ([installer, "Starting at boot"](06-installer.md)). It takes no input: no
environment, no `.env`, no request file, no network of its own; the one file it reads is `run/stopped`, which only root
writes (`run/` is root's, mode 0755). It adds no way in for core: core has no Docker socket and no way to start the unit.
The unit is hardened like the other two (`NoNewPrivileges`, private `/tmp`, no new namespaces, a short list of address
families), and `bobres check` shows when it is missing.

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

## Downloading and restoring backups from the dashboard
The Backups tab can hand the owner a backup file and replace the store's data with a backup, and core must still never get
the Docker socket. A download is core copying a file from the backup service; a restore is a second action of the update
helper above ([installer, "Restoring from the dashboard"](06-installer.md), [runbook](runbooks/restore.md)): core drops a
`request.json` with `"action":"restore"`, and the root helper runs the code `sudo bobres restore` runs.

- **Download.** The owner only, with the authenticator code where the session has one, 10 an hour. The code buys a ticket
  (a random value, single use, a minute, for that session's user only), because a code does not belong in an address; the
  browser follows the ticket as its own download. The file is the encrypted backup as it is on disk, copied through in
  chunks (never held in memory); **the key is never sent**, and a downloaded file is useless without `BOBRES_BACKUP_KEY`.
  The backup service serves only a backup file name (no path, no sidecar), only a plain file, opened without following a
  link, and only to core's token for it. Every download is audited (`system.backup.download`) and told to every owner
  on Telegram, written before the first byte is sent: if that cannot be recorded, nothing is sent.
- **Restore.** The owner only, CSRF-guarded, 6 requests and 5 uploads an hour, one upload at a time and at most 2 GiB. It
  needs the store's **domain typed**, a **ticked acknowledgement** that everything after the backup is lost and that the
  dashboard and bot are unavailable meanwhile, and the **authenticator code** where there is one. Every request and its
  end (`system.restore.requested`, `.done`, `.failed`, `.rolled_back`, `.not_started`, and `system.restore.upload` for
  a file received) is audited and told to every owner on Telegram, also the end of a restore that replaced the database
  that held the request (core records it again from the helper's status, once).
- **What a restore request can say** is a source (a backup of the server, or an upload), a name, the file's SHA-256 and
  size, and the usual id, user name and time; decoded into exactly those fields, with the same refusals as an update's
  (stale, replayed, unknown or missing fields). For a backup of the server, core takes the size and hash from the backup
  service's own list, never from the page, and a name that is not a backup file name never reaches the service. It
  cannot name a path: an upload is always `run/update/upload/<uuid>.bkp`, the uuid being checked.
- **The helper reads files core controls**, so for an upload it checks the folder (a real directory, not writable by others,
  owned by root or core), opens the file without following a link or waiting for a pipe, requires a regular file of
  exactly the size named and owned by core (or, for a backup of the server, by the backup service's user or root), and
  **copies it into a file of its own while hashing it**: a file that changed, a link swapped in, a hash that differs are
  all refused, and what is checked and restored is the copy, which core cannot reach. Two requests at once cannot
  both run (the install lock, shared with `update`, `rollback` and `restore`; the database allows one open request).
- **Nothing is stopped before the backup is known good.** The format, this server's key and every chunk are read first. A
  backup of another server (its key does not open it) or of a newer format is refused with the reason and nothing
  changes. The dashboard restores only backups this server's `BOBRES_BACKUP_KEY` opens; a backup of another server is
  restored on the server with `--key-file`, by someone with a shell.
- **It can go back.** A safety backup of the current data is made before the data is replaced; the dump loads in one
  transaction; if the restored store is not healthy within 5 minutes the helper loads the safety backup (and the master
  keys the restore replaced) and starts the store on it. If that fails too, the status says so and gives the command.
- **The domain is only a confirmation, not a secret**: it stops a slip, not an attacker who holds an owner session and
  the code. Such an attacker can already do the worst the dashboard allows, replace the data with an older copy of the
  store's own; the safety backup, the audit log and every owner's Telegram message are what limit it.
- Not covered: a backup of this server made while the key was different (it cannot be opened: restore it on the server with
  the old key); an attacker who can write to `backups/` as the backup service's user and also holds the key; a host
  where another program runs as uid 65532 and reaches `run/update`.

## Host monitoring

The Monitoring section ([04-dashboard.md](04-dashboard.md)) needs to see the server and its containers; core must still never get the Docker
socket or any other power over the host. So a root helper on the host (the collector, `bobres monitor snapshot`, run every 30 seconds by
`bobres-monitor.timer`; [installer, "Host monitoring"](06-installer.md)) writes one file into a folder core mounts **read only**, the same
model as the updater turned around.

| | core | the collector (root, on the host) | others |
|---|---|---|---|
| `run/monitor/` (`/run/bobres-monitor` in core): owner root, mode 0755 | **read only** mount, the only one | writes `snapshot.json` by a temporary file and a rename | none |
| the Docker socket, the rest of `/opt/bobres`, `.env` | none | the socket (read only calls), the install directory's `.env` for the list of expected services only | root only |

- **Nothing in the folder is core's to write.** Core cannot change the file, plant a link in the folder or fill it: the mount is read only
  and the folder is root's. The collector still checks the folder before it writes or reads there (real directories, owned by root, not
  writable by others), and reads back its own previous snapshot (for CPU shares) only if the file is a regular file of its own user that
  others cannot write. A snapshot that does not pass is not used.
- **What the snapshot holds is allow-listed field by field** (the structs of `internal/monitor/snapshot` are the whole list; there is no
  generic map and no pass-through of anything the engine or `systemctl` printed). Per container: the short id, name, Compose project and
  service, image reference and tag (a digest is dropped), state, health, restart count, times, counters. **Never** a container's
  environment, command line, labels apart from the two Compose ones, mounts, ports, networks or addresses; never an environment
  variable of the host, a process list, a user, a file from a home directory, `.env`, or a key. `docker inspect` is never printed: only
  named fields are decoded from it. The test suite plants secrets in recorded engine and `kubectl` answers (an environment variable,
  a label, a mount, a pod's env and annotations) and fails if one reaches the file.
- **The free text that does get in is scrubbed:** the journal and Caddy lines are cut to 240 characters and have bot tokens, `Bearer`
  values, `key=value` secrets (`token`, `secret`, `password`, `key`, `authorization`), URL credentials and query strings, `/sub/<token>`
  paths, e-mail addresses, long hexadecimal or base64 runs and IP addresses replaced before they are written. On the lab server
  the real file was searched for each of the 32 values of the install's `.env` that are 6 characters or longer: none was in it.
- **Core reads the file as untrusted text** whoever wrote it ([02-events-and-apis.md](02-events-and-apis.md), "The snapshot"): opened
  without following a link, a regular file of at most 1 MiB, strict schema version, every list bounded, every name checked against a
  pattern, every number clamped, every text made plain (controls, bidirectional and zero-width characters removed, `<` and `>` replaced).
  The dashboard shows these as text, never as HTML. A file that is wrong is "the collector's file cannot be read" on the page and
  the alert **Collector quiet**, never a crash. A snapshot older than 90 seconds says nothing about now, and one dated in the future
  is refused (like the updater's status, which core takes for an interrupted update after a while).
- **The collector's own surface.** It is root, so the unit is confined (`ProtectSystem=strict` with write access to `run/monitor` only,
  `NoNewPrivileges`, a private `/tmp`, the kernel, clock and cgroup protections, address families limited, 25% of a core and 192 MiB,
  30 seconds at most). It talks to Docker with `GET` requests only and never to a TCP port of the engine, runs programs from a fixed
  list of system directories with a minimal environment, caps what it reads from each (4 MiB of `/proc` files, the engine's answers, 64 KiB of
  a restore point), and gives each source its own time limit, so a hung `docker` or `kubectl` costs its own section and not the run.
  `journalctl` runs with `-p err` for fixed units; `kubectl` with `get` only.
- **What a compromised core can do with this:** nothing it could not before. It can read the snapshot (the figures above, which the
  dashboard shows to owner and admin anyway). It cannot write it, start or stop the collector, or reach the socket. **What a compromised
  collector (root on the host) can do:** everything root can, as `bobres update` can; the snapshot is how it could lie to the dashboard.
- **Who sees what.** `monitor.read` (owner and admin); read only. The services' own log lines in the events list carry Telegram ids and are
  the owner's only, as on the System page's logs tab. An alert's detail follows the permission it needs elsewhere (a gateway's error is the
  owner's). The hostname, OS, kernel, the names and images of the containers and the mount points are visible to owner and admin.
- **The CI feed** is the bot's, not core's: it asks GitHub's public API anonymously, for a repository fixed in the build; no credential is
  sent or stored, the answer is bounded, shaped and checked again by core, and a link is kept only if it points into the repository.
- **Kubernetes** is optional and only looked at where `kubectl` or `k3s` is on the host; BOBRES does not run there. The cluster's
  answer is reduced to names, phases, readiness and restart counts. It was tried with recorded and constructed `kubectl` answers, not
  against a live cluster.
- **Off switch:** `sudo bobres monitor disable` removes the units and the snapshot. The compose mount can stay: with no collector the folder is empty.

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
| Many required channels turn one customer's updates into a storm of Telegram lookups (or one slow chat holds every tap) | At most 50 chats whatever the license; a confirmed membership is remembered per customer and chat for 10 minutes and a member of the whole list costs one cache read; a chat not joined is not asked again by an ordinary update for 2 seconds; one update's lookups run 4 at a time and the bot's 8 at a time in all, each with a 3-second timeout and 8 seconds for the update; a chat that cannot answer is skipped (logged once a minute), never a lock-out. The Mini App asks 4 at a time within 10 seconds ([03-bot-ux.md](03-bot-ux.md), "Required chats") |
| Dashboard brute force | Rate limit, lockout, 2FA, secure cookies, CSRF tokens, short sessions |
| A stolen login link, or a stolen password and authenticator code, used from another machine | The owner can turn on the approval of staff sign-ins in Telegram (Settings > Sign-in security, `security.login_approval`, off by default, owner only, needs the owner signed in, an explicit acknowledgement and the authenticator code where there is one, and a preflight message that proves the owner can be reached). A sign-in that passed every check then gets **no session at all** until the staff member, or an owner, taps Approve in the bot: only a pending row exists (`login_approvals`, 5 minutes by the database clock) and a separate cookie that opens nothing. The message shows the time, IP address, browser and system. Approval is one-time and bound to the request: a per-approver random nonce (kept hashed) in the callback, the approver's Telegram id and current staff role are checked in the same locked transaction that decides, a forged, replayed, stale or foreign answer does nothing and is audited (`dashboard.login_approval_invalid`), and the first of two answers wins. Deny, expiry and giving up all end without a session ever existing. New requests are limited per account (3 open, 6 per 15 minutes) and answers per Telegram account. It **fails closed** (no bot, blocked, Telegram down: the page says so at once and does not wait) and has a way out that needs neither the bot nor Telegram: `sudo bobres admin approval off` on the server, which can only switch it off. Not covered: a stolen Telegram account of the staff member or owner (the approver); the place of an IP address is not shown, since core has no internet and no geolocation database ships with it |
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
| A backup taken from the dashboard (a stolen owner session) | The owner only, an authenticator code where there is one (a single-use ticket carries it to the browser's download), 10 an hour, every download audited (`system.backup.download`) and told to every owner on Telegram before a byte is sent. The file is encrypted with `BOBRES_BACKUP_KEY`, which is never sent with it and is not in the dashboard; the backup service serves only backup file names, plain files, to core's token. Cost of a hijack: an encrypted copy of the data, useless without the key, and an alert nobody can suppress |
| Hostile or mistaken restore from the dashboard (a stolen owner session, a compromised core) | The owner only, CSRF-guarded, 6 requests and 5 uploads an hour, at most 2 GiB, one upload at a time; the store's domain typed, an acknowledgement that the data after the backup is lost, and the authenticator code where there is one. Core has no Docker socket: it drops a request file; the root helper takes the install lock, copies the backup into a file of its own while checking its SHA-256 and size, opens everything core can write without following links or waiting for pipes, and reads the whole backup with this server's key **before it stops anything**. A safety backup of the current data comes first, the dump loads in one transaction, and the helper goes back to the safety backup if the store is not healthy. Every request and its end is audited and told to every owner on Telegram, also after the restore replaced the database that held the request. Cost of a hijack: the data replaced by an older copy of this server's own (a backup this server's key opens), some minutes of downtime, the safety backup and the alerts to undo it; `sudo bobres config set BOBRES_UPDATE_FROM_DASHBOARD off` removes the helper |
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
- The envelope (`internal/crypto`) has two formats. Version 1 is `0x01 || nonce || AES-256-GCM` and carries no key
  id. Version 2, which everything new is written in, is `0x02 || key id || nonce || AES-256-GCM`, with the version and
  the key id as authenticated data: a value cannot be passed off as sealed by another key or version. The key id is 4
  bytes of an HKDF output of the master key (`bobres:keyid:v1`): a public fingerprint, not a way to find the key. An
  envelope holds the current master key and, during and after a rotation, the previous one: it seals with the current
  key and opens with whichever the key id (or, for version 1, a trial) names. Nothing else decides which key opens a
  value, so a rotation that stops half way leaves nothing unreadable.
- **Master key rotation** (`bobres rotate-key`, docs/06-installer.md, runbooks/rotate-secrets.md): every encrypted
  column is listed in `internal/rekey`, and a test fails when a migration adds one that is not (so a new secret cannot
  be forgotten). The command refuses unless the store is healthy and the current keys read every value, takes and
  reads back a backup, stops the services, writes the new keys to `.env` with the old ones beside them, seals every
  value again (each: read with whichever key fits, sealed with the new, read back with the new alone, replaced under a
  condition on the old bytes; one transaction per table, rows locked, the table checked again before it commits),
  then finishes `.env` and starts the services. A failure puts the old keys back, which is the same run with the keys
  swapped. Keys appear in no output and no log, only in `.env` (0600, one rename), the backups and, as comments, the
  retired keys (until deleted or `--forget-old`). Residual: a retired key kept as a comment is as secret as `.env`; the
  safety backup of before holds the old keys with the old data.
- Gift codes are not encrypted but hashed with a key: HMAC-SHA256 under the **tag key** (`crypto.Envelope.Tag`),
  which is derived from `BOBRES_CORE_MASTER_KEY` until the first rotation. The codes cannot be recovered from the
  database, and a master key that is lost makes the codes already made unredeemable (the hashes no longer match), so
  make new ones. A rotation does not change what Tag answers: it saves the key the old master key derived as
  `BOBRES_CORE_TAG_KEY` (and `_PROVISIONER_` for the 3x-ui subscription ids, which the panels already hold) and keeps it
  through every later rotation. So the tag key is not rotated: the protection of a gift code against an offline guess
  by someone who has the database and that key is its 59 bits, and for a custom code its length. Every backup carries
  the tag keys that are set, and a restore brings them back.
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
- Key rotation procedures documented and scripted (`bobres secrets rotate`; `bobres rotate-key` for the master keys).

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
- DECIDED (release 0.19.0): the master keys are rotated by `bobres rotate-key`, which re-encrypts what they protect (the key id in
  the version 2 format, a dual-key envelope, a tag key that never changes). Not rotated: `BOBRES_BACKUP_KEY` (it encrypts
  backup files, which older backups need), the tag keys, and the database and service secrets, which have their own
  commands.
