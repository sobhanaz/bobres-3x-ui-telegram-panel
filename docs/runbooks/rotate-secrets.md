# Runbook: rotate secrets

Replace a token, password or key: after a leak (a screenshot of `.env`, a laptop lost, a staff member who left), or on
a schedule. Commands run on the server as root; `dc` and `bsql` are defined in the
[runbooks README](README.md#before-you-start). Most secrets are rotated with `bobres secrets rotate <name>`; the backup
key and the master keys are not, on purpose, and have their own procedures below.

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
| Master keys `BOBRES_CORE_MASTER_KEY`, `_PROVISIONER_`, `_PAYMENTS_` | `.env`, and inside every backup | Only after a leak, with the procedure below: it makes what they encrypt unreadable | Partial |

**Never delete a master key's line by mistake:** the installer would make a new key, and the 3x-ui tokens, staff
authenticators and gateway settings saved with the old one could no longer be read. `bobres secrets rotate` never
touches them (`bobres secrets rotate master-keys` only explains why).

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
changes the database roles' passwords, and a revoked token must not come back. The backup key, S3 key and master key
procedures below are by hand: after a rollback, repeat those. A rollback or a restore also puts back the database of
its backup, with the 3x-ui tokens, gateway settings and staff passwords saved in it then: change again the ones you
rotated since.

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

## Master keys (only after a leak)

A master key alone decrypts nothing: it protects the 3x-ui tokens, staff authenticators and gateway settings stored in
the database and in backups. Re-encrypting them with a new key is not built yet, so a new key means entering them
again. If someone may have both a master key and the database (or a backup and the backup key), first rotate what it
protects: new 3x-ui API tokens (and delete the old ones in each panel), staff password resets, the Zarinpal proxy
password, the automatic USDT TronGrid key and proxy password. Then, if you also want a new key:

| Key | After a new key | Then |
|---|---|---|
| `BOBRES_PROVISIONER_MASTER_KEY` | Every server shows "the stored API token cannot be decrypted"; new orders wait | 3x-ui servers > Edit server > a new API token, for every server |
| `BOBRES_PAYMENTS_MASTER_KEY` | Payment gateways: "The stored settings cannot be read" on Zarinpal and on USDT TRC20 (automatic); neither is offered | Payment gateways > enter Zarinpal's merchant id (and proxy) again, and on USDT TRC20 (automatic) the address, TronGrid key and proxy again |
| `BOBRES_CORE_MASTER_KEY` | Staff password logins fail (links from the bot still work) | Staff > each member > Reset password; they set a new one in My account |

```sh
cp -p /opt/bobres/.env /root/env.before-rotation              # root only; delete it once all is well
sed -i '/^BOBRES_PROVISIONER_MASTER_KEY=/d' /opt/bobres/.env   # the one key to replace
bobres install --skip-checks --quiet
```

Then do the "Then" column at once, and repeat the change after a `bobres rollback` (which puts back the `.env` from
before the last update). Backups made before carry the old key, and `bobres restore` of such a backup puts the
old key back with the old data (that is what makes them readable): after a restore, check the three pages above.

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
