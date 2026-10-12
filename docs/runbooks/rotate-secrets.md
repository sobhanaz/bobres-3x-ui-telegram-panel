# Runbook: rotate secrets

Replace a token, password or key: after a leak (a screenshot of `.env`, a laptop lost, a staff member who left), or on
a schedule. Commands run on the server as root; `dc` and `bsql` are defined in the
[runbooks README](README.md#before-you-start). There is no `bobres secrets` command yet: this runbook is the way.

## Where each secret lives

| Secret | Where | Rotate with | Downtime |
|---|---|---|---|
| Telegram bot token | `.env` `BOBRES_TELEGRAM_BOT_TOKEN` | @BotFather, then the menu ([bot token revoked](bot-token-revoked.md)) | The bot restarts (seconds) |
| 3x-ui API tokens | The provisioner's database, encrypted | The dashboard: 3x-ui servers > Edit server | None |
| Zarinpal merchant id and proxy | The payments database, encrypted | The dashboard: Payment gateways (owner) | None |
| Staff passwords and authenticators | The core database, encrypted | My account > Change password; Staff > Reset password (owner) | None |
| Staff logins (sessions) | The core database | My account > Log out other devices; Staff > the member > Sessions > Log out everywhere | None |
| Service tokens `BOBRES_TOKEN_CORE`, `_BOT`, `_BACKUP`, `_CORE_BACKUP` | `.env` | Delete the line, re-run the installer (below) | Services restart (seconds) |
| `REDIS_PASSWORD` (and `BOBRES_REDIS_URL`) | `.env` | Delete the `REDIS_PASSWORD` line, re-run the installer | Redis and the bot restart |
| `DB_BACKUP_PASSWORD` | `.env` and the database role `svc_backup` | Delete the line, re-run the installer (it updates the role) | Postgres restarts (seconds to a minute); every service reconnects: a quiet hour |
| `DB_CORE_PASSWORD`, `DB_PAYMENTS_PASSWORD`, `DB_PROVISIONER_PASSWORD`, `POSTGRES_PASSWORD` | `.env` and the database roles | The database procedure (below) | About a minute |
| `BOBRES_BACKUP_KEY` | `.env` | Delete the line, re-run the installer; keep the old key for the old backups | None |
| `BOBRES_TELEGRAM_WEBHOOK_SECRET` (webhook mode only) | `.env` | Edit it, `bobres start` | The bot restarts |
| Master keys `BOBRES_CORE_MASTER_KEY`, `_PROVISIONER_`, `_PAYMENTS_` | `.env`, and inside every backup | Only after a leak, with the procedure below: it makes what they encrypt unreadable | Partial |

**Never delete a master key's line by mistake:** the installer would make a new key, and the 3x-ui tokens, staff
authenticators and gateway settings saved with the old one could no longer be read.

## Before you start

- Keep a copy of `.env` until the store is healthy again (it holds every secret: root only, delete it after):

  ```sh
  cp -p /opt/bobres/.env /root/env.before-rotation
  ```

- `bobres rollback` puts back the `.env` from before the last update. After a rollback, rotate again whatever you
  rotated since that update.
- A rotation that ends with `bobres install` keeps every other setting and secret, writes `.env` (mode 0600), then
  recreates only the containers whose settings changed and waits until they are healthy.

## Service tokens, Redis password, backup database password

Delete the line; the installer generates a new random value for each missing secret.

```sh
sed -i '/^BOBRES_TOKEN_CORE=/d' /opt/bobres/.env      # one line per secret, e.g. ^REDIS_PASSWORD= or ^DB_BACKUP_PASSWORD=
bobres install --skip-checks --quiet
bobres status
```

Every service that uses the token gets the new value in the same run. For `DB_BACKUP_PASSWORD` the installer also sets
the new password on the `svc_backup` role in the running database. That password is also in the postgres container's
environment, so the run recreates Postgres: core, payments, the provisioner and the backup service lose the database
until it is healthy again (seconds to a minute), and payment returns and dashboard calls fail meanwhile. Rotate it in a
quiet hour. Check: `bobres status` all healthy; for the backup password, `sudo bobres backup` succeeds.

## Database passwords

The services' roles keep their password inside the database, so the role and `.env` change together:

```sh
ROLE=svc_core; VAR=DB_CORE_PASSWORD      # or svc_payments/DB_PAYMENTS_PASSWORD, svc_provisioner/DB_PROVISIONER_PASSWORD,
                                         # or bobres/POSTGRES_PASSWORD (the database's owner)
sed -i "/^$VAR=/d" /opt/bobres/.env
bobres install --skip-checks --quiet --no-start   # writes a new password into .env, starts nothing
dc up -d postgres                                 # postgres restarts with the new value in its environment
until dc exec -T postgres pg_isready -U bobres -d bobres; do sleep 2; done
dc exec -T postgres sh -c "psql -v ON_ERROR_STOP=1 -q -U bobres -d bobres -v role=$ROLE" <<SQL
\\getenv pw $VAR
ALTER ROLE :"role" WITH PASSWORD :'pw';
SQL
bobres start                                      # recreates the service with its new address, waits until healthy
```

psql reads the new password from the container's environment (`\getenv`), so it is on no command line and in no
shell history. Between the `ALTER ROLE` and `bobres start` the service cannot open new connections: run the steps
together, in a quiet hour. If `bobres start` reports the service unhealthy, its log says why
(`bobres logs --tail 50 core`); the copy of `.env` from "Before you start" has the old values.

## The backup key

```sh
sed -i '/^BOBRES_BACKUP_KEY=/d' /opt/bobres/.env
bobres install --skip-checks --quiet
```

The run prints the new key once ("Backup key (shown only now ...)"): store it away from the server. New backups use
it. **Every older backup still needs the old key**: keep the old key next to those backups (`bobres restore <file>
--key-file <old key file>` opens them), or delete the old backups once a new one exists and opens. The last update's
restore point keeps its own copy of the old `.env`, so `bobres rollback` still opens its backup. Check: `sudo bobres
backup`, then [check that it opens](backups-not-arriving.md#check-that-a-backup-opens).

## Master keys (only after a leak)

A master key alone decrypts nothing: it protects the 3x-ui tokens, staff authenticators and gateway settings stored in
the database and in backups. If someone may have both a master key and the database (or a backup and the backup key),
first rotate what it protects: new 3x-ui API tokens (and delete the old ones in each panel), staff password resets,
the Zarinpal proxy password. Then, if you also want a new key:

| Key | After a new key | Then |
|---|---|---|
| `BOBRES_PROVISIONER_MASTER_KEY` | Every server shows "the stored API token cannot be decrypted"; new orders wait | 3x-ui servers > Edit server > a new API token, for every server |
| `BOBRES_PAYMENTS_MASTER_KEY` | Payment gateways: "The stored settings cannot be read"; Zarinpal is not offered | Payment gateways > enter the merchant id (and proxy) again |
| `BOBRES_CORE_MASTER_KEY` | Staff password logins fail (links from the bot still work) | Staff > each member > Reset password; they set a new one in My account |

```sh
sed -i '/^BOBRES_PROVISIONER_MASTER_KEY=/d' /opt/bobres/.env   # the one key to replace
bobres install --skip-checks --quiet
```

Then do the "Then" column at once. Backups made before carry the old key, and `bobres restore` of such a backup puts the
old key back with the old data (that is what makes them readable): after a restore, check the three pages above.

## After any rotation

- `bobres status`: everything healthy. Open the bot, the dashboard, and a 3x-ui server's Test connection.
- `sudo bobres backup`: the next backup carries the new values.
- Delete the copy: `rm /root/env.before-rotation`.

## Escalate when

- A service stays unhealthy after a rotation and the old value does not bring it back.
- A master key leaked together with the database or a backup.

Send: which secret you rotated (never its value), the commands' output, and `sudo bobres support-bundle` (read it
first).
