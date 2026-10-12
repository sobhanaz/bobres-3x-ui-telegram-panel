# Runbook: disk full

The server is running out of disk. Postgres stops writing when the disk is full, and then nothing can be sold or
delivered. Commands run on the server as root; `dc` and `bsql` are defined in the
[runbooks README](README.md#before-you-start).

## Symptoms

- `bobres status` shows little free disk ("Disk: 0.4 GB free").
- The System page, Backups tab: "The last backup failed: not enough free disk space in /backups: ... free, ...
  needed (twice the last backup)". The backup service refuses to start a backup without that room.
- `bobres update` refuses: "GB free in ...: an update needs at least 2 GB there".
- Services turn unhealthy; the Health tab's Database check fails; the logs show `No space left on device`, `could not
  extend file` or `could not write to file` (`bobres logs --tail 100 postgres`).
- `docker compose up` or an update cannot pull images.

## Checks

```sh
df -h /opt/bobres /var/lib/docker /
du -sh /opt/bobres/backups /opt/bobres/.restore-point 2>/dev/null
ls -lhtr /opt/bobres/backups
ls -lh /opt/bobres/bobres-support-*.tar.gz 2>/dev/null
docker system df
du -sh /var/lib/docker/volumes /var/lib/docker/overlay2 /var/lib/docker/containers 2>/dev/null
journalctl --disk-usage
```

How big is the database, and which tables (when postgres still runs):

```sh
bsql "SELECT pg_size_pretty(pg_database_size('bobres')) AS database;"
bsql "SELECT schemaname || '.' || relname AS table_name, pg_size_pretty(pg_total_relation_size(relid)) AS size
      FROM pg_catalog.pg_statio_user_tables ORDER BY pg_total_relation_size(relid) DESC LIMIT 10;"
```

What normally uses space:
- `/opt/bobres/backups`: the 7 newest daily backups and their sidecars, plus every `-before-restore` and
  `-before-rollback` backup the CLI made (those are never deleted for you).
- `/opt/bobres/.restore-point`: the last update's restore point, with a full backup.
- Docker images: each update pulls new images; the old ones stay until pruned.
- Container logs: capped by the compose file at 5 files of 10 MB per service.
- The database volume (`/var/lib/docker/volumes/bobres_pg_data`).

## Fixes, safest first

1. **Old safety backups and support bundles.** Copy the ones you may need off the server first (`scp`), then:

   ```sh
   rm /opt/bobres/backups/bobres-*-before-restore.* /opt/bobres/backups/bobres-*-before-rollback.*
   rm /opt/bobres/bobres-support-*.tar.gz
   ```

2. **Unused Docker images and build cache:**

   ```sh
   docker image prune          # dangling images only
   docker image prune -a       # every image no container uses (a later rollback pulls the old ones again)
   docker builder prune
   ```

   Never prune volumes (`docker volume prune`, `docker system prune --volumes`): that can delete the database.

3. **System files:** `journalctl --vacuum-size=200M`, `apt-get clean`.

4. **Older daily backups.** Each one under 50 MB is also in the owner's Telegram. Copy the older ones off the server
   and delete them with their `.json` sidecar; keep the newest. The backup service keeps 7 and prunes the rest itself.

5. **The restore point**, only once the last update is fine and you will not roll back: `rm -rf
   /opt/bobres/.restore-point`. After that `bobres rollback` is no longer possible.

6. **Grow the disk** (your hosting panel), when the database itself is what fills it.

If postgres stopped because the disk filled up, start the store once there is room; postgres recovers from its log on
its own:

```sh
bobres start
```

Then check that backups work again: `sudo bobres backup`, and the System page's Backups tab. A backup needs free space
of twice the last one.

## Prevent it

- Keep at least twice the newest backup free, plus 2 GB for updates (`bobres status` shows the free space; it exits 1
  when a service is unhealthy, so it can run from a monitoring check).
- Delete `-before-restore` and `-before-rollback` backups once the restored or rolled-back store is fine.
- After each update holds for a few days, `docker image prune -a`.

## Escalate when

- The database grows fast with no matching growth in customers or orders (the table sizes above show which).
- Postgres does not start after space was freed (`bobres logs --tail 200 postgres`).
- Free space keeps dropping and none of the places above explains it.

Send: the output of the checks above, and `sudo bobres support-bundle` (read it first; it needs a few MB free).
