# Runbook: backups not arriving

The owner does not get the daily backup in Telegram, or the System page shows a backup that failed or was not sent,
or (with off-server copies set up) one that was not copied to the S3 bucket.
Commands run on the server as root; `dc` is defined in the [runbooks README](README.md#before-you-start). How backups
work: [06-installer.md](../06-installer.md), "Backups".

## Symptoms

- No backup file from the bot in the owner's Telegram after the daily time (`BOBRES_BACKUP_TIME`, default 03:30, in
  `BOBRES_TIMEZONE`).
- The System page, **Backups** tab: "The last backup failed: ...", or a backup marked **Not sent** / **Failed** with a
  reason, or "The backup service is not installed", or "Core could not reach the backup service".
- No new `bobres-<stamp>.bkp` in `/opt/bobres/backups`.
- With off-server copies (`BOBRES_BACKUP_S3_ENDPOINT` in `.env`): a backup marked as not copied to the bucket, or no
  new object in the bucket, or the system alert "The latest backup was not copied to the off-site (S3) storage"
  (`backup.offsite`).

## Checks

1. The Backups tab: the list (newest first), each one's Telegram state and reason, "Next backup", the free disk.

2. On the server:

   ```sh
   bobres status                                   # is "backup" in the list, running and healthy?
   ls -lhtr /opt/bobres/backups
   cat "$(ls -t /opt/bobres/backups/*.json | head -1)"   # the newest sidecar: sent_at, telegram_error, error,
                                                         # uploaded_at, uploaded_to, upload_error
   bobres logs --tail 100 backup
   bobres logs --tail 300 bot | grep -i backup
   grep -q '^BOBRES_TOKEN_BACKUP=.' /opt/bobres/.env && echo "backup token: set" || echo "backup token: missing"
   grep '^COMPOSE_PROFILES=' /opt/bobres/.env      # must contain "backup"
   ```

3. Make one now and watch the result:

   ```sh
   sudo bobres backup
   ```

## No backup is made

| What you see | Meaning | Fix |
|---|---|---|
| No `backup` service in `bobres status`; "The backup service is not installed" | The install runs a version from before backups, or the `backup` profile was taken out of `COMPOSE_PROFILES` | An old version: `sudo bobres update`. Otherwise `sudo bobres install --skip-checks`, which turns the profile on for every version that has the service |
| `not enough free disk space in /backups: ... (twice the last backup)` | The backup service refuses without room for two backups | [Disk full](disk-full.md) |
| `password authentication failed for user "svc_backup"`, `role "svc_backup" does not exist` | The read-only database role is missing or has another password | `sudo bobres install --skip-checks` (it creates or updates the role in the running database), or the SQL in [06-installer.md](../06-installer.md), "The database role" |
| `backup directory not writable` | `/opt/bobres/backups` is not the backup service's (uid 65532) | `chown 65532:65532 /opt/bobres/backups && chmod 700 /opt/bobres/backups` |
| `the backup key must be at least ...` | `BOBRES_BACKUP_KEY` is missing or short | `sudo bobres install --skip-checks` creates it and prints it once: store it away from the server |
| `a backup is already running` | Another backup (the daily one, the dashboard's button) is under way | Wait for it; the Backups tab updates when it is done |
| A `pg_dump` error | The dump itself failed | Escalate with the error |

## A backup is made but not sent

The file stays in `/opt/bobres/backups` either way. The Backups tab (and the sidecar's `telegram_error`) says why:

| Reason | Meaning | Fix |
|---|---|---|
| Over Telegram's 50 MB limit (`too_big`) | The Bot API sends files up to 50 MB; the owner gets a message instead | Copy backups off the server yourself (`scp root@server:/opt/bobres/backups/bobres-<stamp>.bkp .`), or run a local Bot API server (`BOBRES_TELEGRAM_API_URL`), which allows 2000 MB |
| The bot has no owner set (`no_owner`) | `BOBRES_ADMIN_TELEGRAM_ID` is empty | `sudo bobres` > "Owners", or `sudo bobres admin owners add <telegram id>` |
| The owner has blocked the bot (`blocked`) | The owner blocked the bot, or never pressed /start | Open the bot from the owner's account, unblock it, send /start |
| The bot was busy sending another backup (`busy`) | Two backups at once | Nothing; the next one goes. Or `sudo bobres backup` |
| Telegram refused it (`telegram_error`) | Telegram answered with an error (shown) | A token problem: [bot token revoked](bot-token-revoked.md). Otherwise the error says what |
| The bot did not answer (`unreachable`) | The bot was down or restarting | `bobres status`, `bobres logs --tail 100 bot` |
| Not sent, no reason, and "backup token: missing" above | `BOBRES_TOKEN_BACKUP` is empty: backups are kept on the server only | `sudo bobres install --skip-checks` fills the missing token and recreates the services |

After the fix, run `sudo bobres backup`: the file arrives in the owner's Telegram and the Backups tab shows it as
**Sent to Telegram**.

## A backup is made but not copied to S3

Only with off-server copies set up. The file stays in `/opt/bobres/backups` and is still sent to Telegram: a failed
copy never fails the backup. The sidecar's `upload_error` (the Backups tab, and `sudo bobres backup`, which prints
"Not copied off the server (...)") says why. Look at the settings (secrets masked) and the service's own words:

```sh
bobres config list | grep BOBRES_BACKUP_S3_
bobres config get BOBRES_BACKUP_NETWORK          # must be "egress" while the endpoint is set
bobres logs --tail 100 backup | grep -i s3
```

| Reason | Meaning | Fix |
|---|---|---|
| `the S3 upload is not set up correctly: ...` | A setting the service cannot use (the rest of the line names it): a missing bucket or key, an `http://` endpoint, a bad `BOBRES_BACKUP_S3_KEEP` | `sudo bobres config set <key> <value>` for the plain settings; a missing key: `sudo bobres install --skip-checks --s3-endpoint <url> --s3-bucket <bucket>` asks for it |
| `dial tcp ...`, `no such host`, `i/o timeout`, `connection refused` | The service cannot reach the storage | `bobres config get BOBRES_BACKUP_NETWORK` must say `egress` (`sudo bobres install --skip-checks` sets it from the endpoint, `.env` edited by hand may lack it). Then the endpoint's spelling, and whether this server can reach it at all: `curl -sSI https://<endpoint>` |
| `HTTP 403: SignatureDoesNotMatch` / `InvalidAccessKeyId` | Wrong access or secret key | Put the right keys in `.env` (`BOBRES_BACKUP_S3_ACCESS_KEY`, `_SECRET_KEY`), then `sudo bobres install --skip-checks` |
| `HTTP 403: AccessDenied` | The keys may not write to that bucket or folder | Give the key write, list and delete rights on the bucket in the storage's panel |
| `HTTP 403: RequestTimeTooSkewed` | This server's clock is off by more than 15 minutes | Fix the time: `timedatectl` must say "System clock synchronized: yes" (`timedatectl set-ntp true`) |
| `HTTP 404: NoSuchBucket` | The bucket does not exist, or the endpoint style is wrong | Create the bucket, or check the name. A self-hosted endpoint like `https://backups.example.com` is path style; only `https://<bucket>.<service host of three or more labels>` is read as virtual-hosted |
| `HTTP 400: AuthorizationHeaderMalformed`, `HTTP 301: PermanentRedirect` | The region (or the endpoint) is not the bucket's | `sudo bobres config set BOBRES_BACKUP_S3_REGION <the bucket's region>`, or the endpoint the storage names |
| `upload part N: ...`, then a 5xx | The storage failed during a multipart upload (files over 100 MiB); the parts were aborted | Usually passes: `sudo bobres backup`. If it repeats, the storage's status page |

After the fix, `sudo bobres backup` prints "Copied off the server to s3://...". The bucket keeps the newest
`BOBRES_BACKUP_S3_KEEP` (default 30) `bobres-<stamp>.bkp` under the folder and deletes older ones; nothing else in
the bucket is touched. A prune that fails is only logged (`old backups in S3 not pruned`).

## Check that a backup opens

A backup nobody can open is no backup. To check one without changing anything, start a restore and answer **N**: the
whole file is verified (key, damage) before the question.

```sh
sudo bobres restore /opt/bobres/backups/bobres-<stamp>.bkp
```

It prints when the backup was made, the size of the dump and the master keys it carries; "Cancelled. Nothing was
changed." after the N. Do this after changing the backup key, and now and then on a test server
([restore](restore.md)). Keep the backup key (`sudo bobres` > "Backup key") away from the server.

## Escalate when

- The backup fails with a `pg_dump` or database error.
- A backup made here does not open with this server's key.
- Backups are made and the reason says sent, but the owner receives nothing.
- The copy to S3 fails with keys and settings the storage's panel confirms.

Send: the newest sidecar (`.json`, no secrets in it), `bobres logs --tail 100 backup`, and `sudo bobres
support-bundle` (read it first). Never send a `.bkp` file or the backup key.
