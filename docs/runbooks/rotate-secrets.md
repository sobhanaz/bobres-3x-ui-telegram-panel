# Runbook: rotate secrets

Replace a token, password or key: after a leak (a screenshot of `.env`, a laptop lost, a staff member who left), or on
a schedule. Commands run on the server as root; `dc` and `bsql` are defined in the
[runbooks README](README.md#before-you-start). Most secrets are rotated with `bobres secrets rotate <name>`; the master
keys have their own command, `bobres rotate-key`, and the backup key has its own procedure below.

## Where each secret lives

| Secret | Where | Rotate with | Downtime |
|---|---|---|---|
| Telegram bot token | `.env` `BOBRES_TELEGRAM_BOT_TOKEN` | @BotFather, then `bobres secrets rotate bot-token` ([bot token revoked](bot-token-revoked.md)) | The bot restarts (seconds) |
| 3x-ui API tokens | The provisioner's database, encrypted | The dashboard: 3x-ui servers > Edit server | None |
| Zarinpal merchant id and proxy | The payments database, encrypted | The dashboard: Payment gateways (owner) | None |
| Automatic USDT: TronGrid API key and proxy | The payments database, encrypted | A new key on trongrid.io (and a new proxy password, if the proxy has one), entered on the dashboard as the owner: Payment gateways > USDT TRC20 (automatic); then delete the old key on trongrid.io ([below](#automatic-usdt-trongrid-key-proxy-and-address)) | None |
| Automatic USDT: the store's TRON (TRC20) address | The payments database, encrypted | Only when the wallet changes (its keys leaked, or a new wallet): the new address on the same dashboard card, once no automatic USDT payment is open ([below](#automatic-usdt-trongrid-key-proxy-and-address)) | None |
| Staff passwords and authenticators | The core database, encrypted | My account > Change password; Staff > Reset password (owner) | None |
| Staff logins (sessions) | The core database | My account > Log out other devices; Staff > the member > Sessions > Log out everywhere | None |
| Service tokens `BOBRES_TOKEN_CORE`, `_BOT`, `_BACKUP`, `_CORE_BACKUP` | `.env` | `bobres secrets rotate service-tokens` (all four together) | Every BOBRES service restarts (seconds) |
| `REDIS_PASSWORD` (and `BOBRES_REDIS_URL`) | `.env` | `bobres secrets rotate redis-password` | Redis and the bot restart (seconds) |
| `DB_CORE_PASSWORD`, `DB_PAYMENTS_PASSWORD`, `DB_PROVISIONER_PASSWORD`, `DB_BACKUP_PASSWORD`, `POSTGRES_PASSWORD` | `.env` and the database roles | `bobres secrets rotate db-passwords [--role R]` | The services whose role changed restart (seconds); Postgres keeps running |
| `BOBRES_BACKUP_KEY` | `.env` | Delete the line, re-run the installer; keep the old key for the old backups (below) | None |
| S3 access and secret keys `BOBRES_BACKUP_S3_ACCESS_KEY`, `_SECRET_KEY` (off-server copies only) | `.env` | A new key in the storage's panel, delete both lines, re-run the installer (it asks for them); then revoke the old key in the panel ([below](#s3-keys-off-server-copies)) | The backup service restarts (seconds) |
| `BOBRES_TELEGRAM_WEBHOOK_SECRET` (webhook mode only) | `.env` | Edit it, `bobres start` | The bot restarts |
| Master keys `BOBRES_CORE_MASTER_KEY`, `_PROVISIONER_`, `_PAYMENTS_` | `.env`, and inside every backup | `sudo bobres rotate-key` ([below](#master-keys)): a backup first, then it seals everything they protect again with new keys | The BOBRES services stop for about a minute; the database stays up |

**Never delete a master key's line by mistake, and never edit one by hand:** the installer would make a new key, and the
3x-ui tokens, staff authenticators and gateway settings saved with the old one could no longer be read. Change a master
key only with `bobres rotate-key`, which re-encrypts what it protects. `bobres secrets rotate master-keys` only points
there.

## How `bobres secrets rotate` works

Every rotation, whatever the secret:

1. asks first (unless `--yes`; for the bot token, typing the token is the answer);
2. keeps a copy of the current `.env` as `/opt/bobres/.env.before-rotation` (root only) until it ends well, for a run
   cut short (a dropped SSH session);
3. writes the new values to `.env` (mode 0600, every other setting and secret kept), prints which services restart,
   recreates only those and waits until they are healthy;
4. when they do not come back healthy, puts the previous values back (in the database too, for database passwords),
   restarts them again, and says so. It exits 1 then: nothing was rotated.

A stopped store gets the new `.env` and starts with it later (`bobres start`); database passwords need the database
running and are refused then. New values are random and never printed. The last update's restore point gets the new
values too, so `bobres rollback` (which puts back the `.env` from before that update) keeps them: a restore never
changes the database roles' passwords, and a revoked token must not come back. The backup key and S3 key
procedures below are by hand: after a rollback, repeat those. A rollback or a restore also puts back the database of
its backup, with the 3x-ui tokens, gateway settings and staff passwords saved in it then: change again the ones you
rotated since. The master keys are the exception to the exception: the restore point holds the database *and* the
`.env` of before the update, which belong together, so a rollback after `bobres rotate-key` brings back the old keys
with the old data (and loses the rotation along with everything else since the update): rotate again then.

## Bot token

Get the new token in @BotFather (`/mybots` > your bot > **API Token**, and **Revoke current token** after a leak), then:

```sh
sudo bobres secrets rotate bot-token
```

It asks for the token without showing it, checks it with Telegram (getMe; through `BOBRES_TELEGRAM_API_URL` when you
use a local Bot API server), writes it and restarts the bot. If this server cannot reach Telegram, add `--offline` to
skip the check. From a script, pipe the token on stdin (never as an argument, where `ps` and the shell history keep
it): `printf '%s\n' "$TOKEN" | bobres secrets rotate bot-token`.

## Service tokens

```sh
sudo bobres secrets rotate service-tokens
```

`BOBRES_TOKEN_CORE`, `_BOT`, `_BACKUP` and `_CORE_BACKUP` change together, so no service is left with a stale peer
token, and core, payments, the provisioner, the bot and the backup service restart at once (seconds: payment returns
and dashboard calls may fail meanwhile). Check: `bobres status` all healthy; `sudo bobres backup` succeeds.

## Redis password

```sh
sudo bobres secrets rotate redis-password
```

`REDIS_PASSWORD` and `BOBRES_REDIS_URL` change together; Redis and the bot are recreated (Redis reads its password
when it starts). The bot's data in Redis is kept on its volume.

## Database passwords

```sh
sudo bobres secrets rotate db-passwords                  # every role
sudo bobres secrets rotate db-passwords --role payments  # one: core, payments, provisioner, backup or postgres
```

1. It takes a backup of the database first: `backups/bobres-<stamp>-before-rotation.bkp` (it needs the backup key in
   `.env`, as `bobres restore` does). If the backup fails, nothing changes.
2. The database takes the new passwords in one transaction (`ALTER ROLE ... WITH PASSWORD`, run by psql in the
   postgres container). The passwords reach psql on its standard input and through `\getenv`: they are on no command
   line and in no shell history.
3. `.env` takes them, and only the services whose role changed restart: `svc_core` core, `svc_payments` payments,
   `svc_provisioner` the provisioner, `svc_backup` the backup service. `postgres` is the superuser (`bobres`):
   nothing logs in with its password (the CLI uses the database's local socket), so nothing restarts.

Postgres itself keeps running. Its container also holds these values in its environment, used only to create a new
database; it takes them the next time it is recreated (an update, `bobres start` after a change). If a service stays
unhealthy, the old passwords are put back in the database and in `.env` and the service restarts again. Check:
`bobres status` all healthy; for the backup role, `sudo bobres backup` succeeds.

## The backup key

```sh
sed -i '/^BOBRES_BACKUP_KEY=/d' /opt/bobres/.env
bobres install --skip-checks --quiet
```

The run prints the new key once ("Backup key (shown only now ...)"): store it away from the server. New backups use
it. **Every older backup still needs the old key**: keep the old key next to those backups (`bobres restore <file>
--key-file <old key file>` opens them), or delete the old backups once a new one exists and opens. The last update's
restore point keeps its own copy of the old `.env`, so `bobres rollback` still opens its backup. Check: `sudo bobres
backup`, then [check that it opens](backups-not-arriving.md#check-that-a-backup-opens). `bobres secrets rotate
backup-key` does not do this for you: it only explains.

## S3 keys (off-server copies)

Only with off-server copies set up (`BOBRES_BACKUP_S3_ENDPOINT` in `.env`). The key pair can write, list and delete
in the bucket, so whoever has it can delete the copies there (they are encrypted: reading them still needs the backup
key). In the storage's panel, make a new key with write, list and delete rights on the bucket (and its folder), then:

```sh
sed -i -e '/^BOBRES_BACKUP_S3_ACCESS_KEY=/d' -e '/^BOBRES_BACKUP_S3_SECRET_KEY=/d' /opt/bobres/.env
bobres install --skip-checks --quiet    # asks for the access key, then the secret key (not shown)
```

The installer writes them to `.env` (mode 0600) and recreates the backup service; if it stops before the end, run it
again (it asks while the keys are missing). Putting the new values in `.env` by hand, then the same install command,
works too. Check: `sudo bobres backup` prints "Copied off the server to s3://...". Only then revoke (delete) the old
key in the storage's panel. If the copy fails, [a backup is made but not copied to
S3](backups-not-arriving.md#a-backup-is-made-but-not-copied-to-s3) has the reasons. `bobres config` never shows or
changes the two keys.

## Automatic USDT: TronGrid key, proxy and address

The TronGrid API key only raises TronGrid's request limit; the proxy URL may carry a user name and password. Both are
stored encrypted in the payments database and never shown again. Make a new key on trongrid.io (or a new proxy
password at the proxy), then on the dashboard as the owner: Payment gateways > **USDT TRC20 (automatic)**, enter the
new key (or proxy), **Test connection**, **Save**. An empty field keeps the stored value, and the address stays as it
is. Then delete the old key on trongrid.io. Payments already open keep being checked, with the new settings.

**The address** is where customers send USDT, so it is no secret, but BOBRES never holds the wallet's keys: change it
when the wallet changes (its seed or keys leaked, or you move to a new wallet). Move the money out of the old wallet
in the wallet itself. Then, on the same card, enter the new **TRON (TRC20) address** and **Save**. It is refused while
automatic USDT payments to the old address are open (the card says how many), since those customers were told to pay
there: switch automatic USDT off and **Save** so no new ones start, wait until the open ones are paid or expire (45
minutes), then enter the new address and switch it on again. The old address is still read until 48 hours after its
last payment started, so a late transfer of an exact amount to it still settles. Tell customers who keep the old
address in their wallet's history to use the new one.

## Master keys

Each service encrypts what it must keep secret with a master key of its own (`BOBRES_CORE_MASTER_KEY`,
`BOBRES_PROVISIONER_MASTER_KEY`, `BOBRES_PAYMENTS_MASTER_KEY`): the 3x-ui API tokens, the staff authenticator secrets,
the gateway settings (merchant id, proxy, USDT address, API keys), an own HTTPS certificate's private key, the license
server's admin key and the license keys sold, the supplier's API key and the accounts it delivered. Replace them
after a leak (`.env`, or a backup with its backup key, may have been seen), or on a schedule:

```sh
sudo bobres rotate-key --dry-run    # counts and checks every encrypted value, changes nothing
sudo bobres rotate-key              # the rotation
```

**What it does**, in this order, and why it is safe:

1. **Checks.** The store must be up and healthy, there must be room for a backup, and the keys now in `.env` must read
   *every* encrypted value (a dry run of each service's `rekey`). With a key that does not, it stops here and nothing
   has changed: it will not replace a key it cannot use.
2. **Backs up and checks the backup.** `bobres-<stamp>-before-key-rotation.bkp` in `/opt/bobres/backups`, read back in
   full, and required to carry the three keys. It holds the OLD keys and the old data, a consistent pair: it restores
   as it was. It stays until you delete it, and it is as secret as the old keys.
3. **Makes new keys** (32 random bytes each) and writes `.env` atomically, mode 0600: the new key as
   `BOBRES_<SERVICE>_MASTER_KEY`, the old one as `BOBRES_<SERVICE>_MASTER_KEY_PREVIOUS` (read, never written with),
   and `BOBRES_<SERVICE>_TAG_KEY`, the key that gift code hashes and 3x-ui subscription ids are made with. The tag key
   is set once, to what the first old key made, and never changes again: replacing it would make every gift code
   unredeemable and every client of a 3x-ui panel unfindable. (It means the tag key of a leaked first key stays
   known to whoever has it; gift codes are 59 bits long, which is the protection that remains.)
4. **Stops the BOBRES services** (not the database, not Caddy), so nothing writes while values are sealed again.
5. **Seals everything again**, in a container of each service, with its own database role. Each table is one
   transaction; each value is read with whichever key fits, sealed with the new key and read back with the new key
   alone before the old bytes are replaced; the rows are locked meanwhile. Before it writes anything it checks every
   value of the service is readable at all.
6. **Finishes `.env`.** The previous keys leave the settings and stay as comments (`# BOBRES_CORE_MASTER_KEY_PREVIOUS=...`)
   so that a database copy or a backup older than the rotation can still be opened by hand. Nothing reads a comment.
   `--forget-old` keeps no comment.
7. **Starts the services** in the order they depend on each other (provisioner and payments, core, then the bot and the
   backup service) and waits until they are healthy.

**Nothing is lost if it fails or is cut off.** The new ciphertext carries the id of the key that sealed it, and while
the rotation runs every service holds both keys, so a value is readable whichever side of the line it is on.

- *It fails* (a database error, a service that is not healthy with the new keys): the command puts the old keys back
  *by itself*: the keys swapped, everything sealed again with the old one, `.env` back byte for byte, the services
  started. It says "nothing was rotated" and exits 1. The install is as it was; read the messages above that line.
- *It cannot even do that*, or it was killed (a dropped SSH session, a power cut): the store is left, or comes back
  with `bobres start`, running with **both** keys, which reads every value. `bobres check` warns "a master key rotation
  was left unfinished". Run `sudo bobres rotate-key` again: it finds the previous key in `.env` and finishes the work
  with the same new keys, without making others. Running it again after it finished makes *another* rotation (new keys).
- A backup made while a rotation is unfinished carries both keys (`..._MASTER_KEY_PREVIOUS` too), so it restores whole.

**What it does not change.** `BOBRES_BACKUP_KEY` is another key: it encrypts the backup files, not the database, and
it stays. To replace it, see [The backup key](#the-backup-key) below. Database passwords and service tokens have their
own commands (`bobres secrets rotate`). **Old backups** (made before the rotation) carry the old keys and old data:
`bobres restore` puts the old keys back in `.env` with the old data, and the store works as it did then. The tag key
is carried by every backup made after a rotation, so a restore on another server keeps gift codes and subscription ids.

**After it.**

- `sudo bobres backup`: the newest backup then carries the new keys. Check `bobres status` and open the dashboard.
- If the old key leaked: after the rotation it protects nothing in the database any more, but it still opens the
  backups and database copies made before. Delete the old backups you do not need, `--forget-old` at the next
  rotation (or delete the `#` lines in `.env`), and delete `bobres-*-before-key-rotation.bkp` when you trust the new
  state. If someone may have had both an old key and a copy of the database, also rotate what the keys protected:
  new 3x-ui API tokens (and delete the old ones in each panel), staff password resets, the Zarinpal proxy password, the
  automatic USDT TronGrid key and proxy password, the supplier's API key: a rotation protects the future, it cannot
  un-leak what was already read.
- `bobres rollback` after a rotation brings back the old keys together with the old database of before the update
  (they are one pair in the restore point); the rotation is gone with everything since the update. Rotate again.
- An install that has no payments key (one from before payments had one) rotates the two keys it has.
- The command takes the install lock: it cannot run beside an update, a restore or a rollback, and says so.

## After any rotation

- `bobres status`: everything healthy. Open the bot, the dashboard, and a 3x-ui server's Test connection.
- `sudo bobres backup`: the next backup carries the new values.
- A `/opt/bobres/.env.before-rotation` left behind means a rotation did not finish: read its messages, and delete the
  file (it holds the old secrets) once the store is healthy. Delete `/root/env.before-rotation` too if you made one.

## Escalate when

- A service stays unhealthy after a rotation and the old value does not bring it back.
- `bobres secrets rotate` says the database kept the new passwords while the services are unhealthy.
- A master key leaked together with the database or a backup.

Send: which secret you rotated (never its value), the commands' output, and `sudo bobres support-bundle` (read it
first).
