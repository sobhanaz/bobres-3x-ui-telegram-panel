# Updates, Backups, Restore

Three jobs keep a store alive: updating it safely, backing it up, and being able to put it back. All commands run on the server as root. Your install is in `/opt/bobres` unless you chose another folder (then add `--dir`).

## Updates

Every release is signed. `bobres update` checks the signature before it installs anything.

Look first:

```bash
sudo bobres update --check
```

Then update:

```bash
sudo bobres update
```

What it does:

1. Verifies the release and checks for 2 GB of free disk.
2. Asks you to confirm (`--yes` skips the question).
3. Leaves a **restore point** in `/opt/bobres/.restore-point/`: a backup of the database, copies of `.env` and the compose files, and the current `bobres`.
4. Installs the new version, pulls the images and waits up to five minutes for every service to be healthy.
5. **Rolls back by itself** if the install fails or the store is not healthy. If a database migration had run, it also puts the old database back.

The bot and dashboard are away for a minute or two. Choose a quiet hour. Core and the bot always move together. A migration is additive, so a rollback keeps your orders and credit.

To undo the last update by hand:

```bash
sudo bobres rollback
```

Only the last update can be rolled back.

### Channels and the dashboard

The default channel is **stable**. Beta (pre-releases) is opt-in: the owner picks it on **System > Updates**, or `sudo bobres config set BOBRES_UPDATE_CHANNEL beta`. `--channel stable|beta` overrides it for one command.

The owner can press **Update now** on the Updates tab. A helper on the server runs the same `bobres update`, with the same checks, backup and rollback. Core never touches Docker itself. The first update to a version that has this feature must still be run over SSH. If the dashboard says updating is not available, run `sudo bobres update` once.

Signed releases expire after 45 days unless re-signed. If an update says the release metadata has expired, the vendor must re-sign it. See the [releases runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/releases.md).

## Backups

The `backup` service makes an encrypted backup of the whole database every day at `BOBRES_BACKUP_TIME` (default 03:30, in `BOBRES_TIMEZONE`). Each backup:

- is kept in `/opt/bobres/backups` (the newest 7);
- is sent to the owners on Telegram (files up to 50 MB; above that the owner gets a message and the file stays on the server);
- is optionally copied to S3-compatible storage (Arvan, MinIO, Backblaze B2, AWS).

Run one now with `sudo bobres backup`, from the menu, or on **System > Backups**.

### The backup key

The installer shows the **backup key** once at the end, and the menu shows it again (**Backup key**, after a confirmation). It is never sent over Telegram. Store it away from the server. **Without it a backup cannot be read.** A backup carries your three master keys (inside the encrypted file), so a backup plus the backup key restores everything on a new server, including panel tokens and gateway settings.

### Off-server copies

A backup that lives only on the server is lost with the server. Turn on S3 copies at install time with `--s3-endpoint`, or later with `sudo bobres config set BOBRES_BACKUP_S3_ENDPOINT https://...` and the other `BOBRES_BACKUP_S3_*` settings. The bucket keeps the newest 30 by default. Only the encrypted file is uploaded, so the bucket holds nothing readable without your key.

## Restore

`bobres restore` replaces **all** data with a backup's. It checks the whole file first, backs up the current data (as `backups/bobres-<time>-before-restore.bkp`), restores in one transaction, and puts the backup's master keys in `.env`.

```bash
sudo bobres restore /opt/bobres/backups/bobres-20261011T033000Z.bkp
```

A backup from another server needs that server's key on one line in a file:

```bash
sudo bobres restore /root/bobres-20261011T033000Z.bkp --key-file /root/old-backup-key.txt
```

From S3-compatible storage:

```bash
sudo bobres restore s3://my-shop-backups/bobres/bobres-20261011T033000Z.bkp
```

The owner can also restore, and download a backup, from **System > Backups**. That path asks for the store's domain typed in full, and for an authenticator code if you have one.

### Move to a new server

1. Stop the old store with `sudo bobres stop`. Two programs cannot poll one bot token.
2. Point the domain's DNS to the new server.
3. Install BOBRES on the new server with the same domain and bot token.
4. Copy the backup file and your backup key to it.
5. Run `bobres restore` with `--key-file`.

After a restore, orders made after the backup are gone. Check your gateways' panels (Zarinpal) for payments in that gap, and check your 3x-ui panels for accounts created after the backup.

## Habits

- Test a restore on a spare server now and then. A backup you cannot restore is not a backup.
- Keep the backup key and a recent `.bkp` file off the server.
- Check `sudo bobres check`. It warns when the newest backup is over 48 hours old.
- Delete old `-before-restore` and `-before-rollback` files when they are no longer needed. Retention never removes them.

If backups do not arrive, see the [backups runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/backups-not-arriving.md). The full procedures are in the [restore](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/restore.md) and [update and rollback](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/update-rollback.md) runbooks.
