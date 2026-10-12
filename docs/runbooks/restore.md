# Runbook: restore a backup

Use this to put the store back to a backup: after data was deleted or damaged, after a bad change, or to move the
store to a new server. `bobres restore` replaces **all** data (customers, orders, ledger, settings) with the backup's.
It backs up the current data first, so a restore can itself be undone.

All commands run on the server as root. The install is in `/opt/bobres` unless you installed elsewhere (then add
`--dir <dir>` to each command). How it works in detail: [06-installer.md](../06-installer.md), "Restore, update and
rollback".

## When to use it

- Data was lost or damaged (a wrong bulk change, a broken migration, a disk problem the database survived).
- You are moving to a new server, or rebuilding this one.
- An update went wrong and `bobres rollback` is not possible (no restore point, or it was already used).

Not for: undoing the last update while its restore point exists. Use `bobres rollback`
([update-rollback.md](update-rollback.md)), which also puts back the previous version.

## Checks first

1. Find the backup. Backups are in `/opt/bobres/backups` (the newest 7 daily ones, plus any `-before-restore` or
   `-before-rollback` backup the CLI made) and each daily one was also sent to the owner on Telegram:

   ```sh
   ls -l /opt/bobres/backups/*.bkp
   ```

   A backup from Telegram: download it on your computer and copy it to the server, for example
   `scp bobres-20261011T033000Z.bkp root@server:/root/`.

2. Have the right key. A backup opens only with the `BOBRES_BACKUP_KEY` of the install that made it.
   - Same server: nothing to do, `.env` has it.
   - New server: a fresh install made its own key. Put the old server's key (the one you stored when it was installed,
     or from `bobres menu` > "Backup key" on the old server) on one line in a file. Paste it into `cat`, not onto the
     command line, so that it never lands in the shell history:

     ```sh
     umask 077; cat > /root/old-backup-key.txt
     ```

     Paste the key, press Enter, then Ctrl-D.

3. Free disk: the restore first writes a backup of the current data next to the others (about the size of the
   newest backup).

   ```sh
   df -h /opt/bobres
   ```

4. The database must be running: `bobres status` shows `postgres` as running and healthy. On a new server, install
   first (`install.sh`, same domain and bot token as before) and let it start.

## Restore

```sh
sudo bobres restore /opt/bobres/backups/bobres-20261011T033000Z.bkp
# a backup from another server:
sudo bobres restore /root/bobres-20261011T033000Z.bkp --key-file /root/old-backup-key.txt
```

It checks the whole file first and prints when it was made, the size of the database dump and which master keys it
carries, then asks before it changes anything. Add `--yes` to skip the question (scripts). Then it:

1. stops core, bot, payments, provisioner and backup (postgres, redis and Caddy stay up; the bot does not answer), so
   nothing is written after the next step,
2. saves the current data in `/opt/bobres/backups/bobres-<now>-before-restore.bkp` (the line "Current data saved in
   ..."),
3. restores the database in one transaction,
4. puts the backup's master keys in `.env` where they differ (it prints the names),
5. starts everything and waits until it is healthy (up to five minutes).

Afterwards check the store: `bobres status`, open the bot, look at the dashboard's System page. Orders paid after the
backup are not in it (see "After a restore").

The `-before-restore` backup is listed on the System page but is not one of the 7 daily backups: retention never
deletes it, however often a restore is tried. Delete it yourself once the restored store is fine (or copy it away
first): `rm /opt/bobres/backups/bobres-<stamp>-before-restore.*`.

On a new server, delete the key file when done: `rm /root/old-backup-key.txt`.

## What it refuses, and what to do

| Message | Meaning | Fix |
|---|---|---|
| `wrong backup key, or the file is damaged at its start` | The key does not open this backup | A backup from another server needs `--key-file` with that server's key. Same server: was the key changed in `.env`? Use the old one with `--key-file` |
| `the file is damaged or incomplete` | The file was cut or changed (an interrupted copy or download) | Copy it again, or take an older backup. Nothing was changed |
| `this is not a BOBRES backup file` | Wrong file | Use a `bobres-<stamp>.bkp` file |
| `the backup was made by a newer BOBRES` | The backup comes from a newer version | Update first (`sudo bobres update`), then restore |
| `the backup key from ... is too short` | The key file holds something else | Put the whole key (64 characters on installs from `bobres install`) on one line |
| `this install has no backup key` | An install from before backup keys: the restore backs up the current data with this install's key first | Run `sudo bobres install` first (it keeps the settings and creates the key), then the restore (with `--key-file` for a backup from another server). Nothing was changed |
| `the backup of the current data failed` | The safety backup could not be made (database down, disk full) | Nothing was changed: the services were started again. Check `bobres status` and `df -h`, fix, run again |
| `the restore failed: ...` | The database refused the dump (see the lines above it) | Nothing was changed: the restore runs in one transaction and the services were started again. Keep the output and escalate |

Exit codes: 0 restored, 1 refused or failed, 2 wrong command line.

## Master keys

Each backup carries the three master keys (`BOBRES_CORE_MASTER_KEY`, `BOBRES_PROVISIONER_MASTER_KEY`,
`BOBRES_PAYMENTS_MASTER_KEY`). They encrypt the 3x-ui panel tokens, staff 2FA secrets and gateway settings in the
database, so the restored data needs the keys it was made with. `bobres restore` puts them in `.env` where they differ
and prints which. `--keep-keys` keeps this install's keys instead: only for a backup whose keys you know are wrong,
because the encrypted settings then cannot be read (the services may not start, panels and gateways need to be set
again). `BOBRES_BACKUP_KEY` itself never changes: new backups use this install's key.

## If the store is not healthy afterwards

The restore prints where to look; also:

```sh
bobres status
bobres logs --tail 100 core
```

- `core` cannot read its data: the master keys. Run the restore again without `--keep-keys`.
- A service does not start for another reason: go back to the data from before the restore. The restore printed the
  file ("Current data saved in ..."):

  ```sh
  sudo bobres restore /opt/bobres/backups/bobres-<that stamp>-before-restore.bkp --yes
  ```

## After a restore

- Orders, payments and changes made after the backup are gone. Payment gateways keep their own records: compare the
  gateway panel (Zarinpal) with the dashboard's orders for that time and credit customers by hand where needed.
- VPN accounts created on the 3x-ui panels after the backup still exist there but not in the store: check the panels.
- The restored data may hold staff accounts and settings from the time of the backup.

## Escalate when

- The restore fails with a database error (`the restore failed: ...`): keep the whole output and the file name.
- No backup opens with any key you have.
- The store is unhealthy after the restore and after going back to the safety backup.

Send: the output of the command, `bobres status`, and `bobres logs --tail 300` (read it through before you send it).
