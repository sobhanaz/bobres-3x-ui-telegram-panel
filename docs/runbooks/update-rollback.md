# Runbook: update and roll back

Use this to move the store to a new BOBRES release, and to go back when the new release misbehaves. All commands run on
the server as root; the install is in `/opt/bobres` unless you installed elsewhere (then add `--dir <dir>`). How it
works in detail: [06-installer.md](../06-installer.md), "Restore, update and rollback".

## Is there an update?

The dashboard's System page (Updates tab) shows the latest release the bot found; it checks every 12 hours. On the
server:

```sh
sudo bobres update --check
```

It prints this install's version, the update channel, the latest release (its signature verified) and whether it is
newer. Exit 1 means the check failed (GitHub unreachable, or a release that does not pass the signature check: never
install that one). "Newer: yes, but not covered by your license" means the license's updates ended before that release
came out: `bobres update` will not install it (see the table below).

The store follows the **stable** channel unless `/opt/bobres/.env` says `BOBRES_UPDATE_CHANNEL=beta` (pre-releases
too). `--channel stable|beta` overrides it for one command. How to change channel, and what going back to stable
means: [Releases and update channels](releases.md#change-a-stores-channel-store-owners).

## Update

```sh
sudo bobres update
# a given release instead of the latest:
sudo bobres update --version 1.3.0
```

It checks the release signature and that there are 2 GB free for the backup and the new images, asks, then:

1. downloads the new `bobres` for this server and checks it against the signed checksums,
2. leaves a restore point in `/opt/bobres/.restore-point/` (root only): a backup of the database, copies of `.env`
   and the compose files, a copy of the current `bobres`, and `restore-point.json`,
3. replaces `bobres` (the old one also stays as `bobres.prev` next to it),
4. runs the new version's install, which pulls the new images and starts them,
5. waits until every service is healthy (up to five minutes).

The bot is unavailable for the minute or two the services restart. Plan the update for a quiet hour. Add `--yes` to
skip the question.

The restore point is not in `backups/`: it holds a plain copy of `.env` (with the backup key), and `backups/` is the
folder you copy off the server. Copy `.restore-point/` nowhere you would not copy `.env`. Its backup is not one of the
7 daily ones and is never pruned: the restore point lasts until the next update replaces it.

If the new version fails to install or is not healthy in time, the update **rolls back on its own** and says so. It
exits 1, and the restore point of the update before (if any) is the current one again.
- When no database migration ran (most often: the image pull failed, so the old version kept running and taking
  orders), the old `bobres` and the old install files and version come back and **the database stays as it is**:
  no order is lost.
- When the new version had migrated the database, the services stop, the current data is saved in
  `/opt/bobres/backups/bobres-<now>-before-rollback.bkp` (what the new version wrote), and the old `bobres`, the old
  install files and version, and the database from the restore point come back. If that backup cannot be made (disk
  full?), nothing is changed and the services start again: free the disk and run `sudo bobres rollback`.

### What update refuses

| Message | Meaning | Fix |
|---|---|---|
| `the release did not pass the signature check` / `not signed` | Tampered, unsigned, revoked or expired | Do not install it. Check the system clock (`date -u`); otherwise escalate |
| `release X is older than this install` | Update never goes back | `sudo bobres rollback` (the last update), or see "Going further back" |
| `release X is older than release Y, which this server installed before` | `/var/lib/bobres/release.version` says a newer release was installed here and held (an update that rolled back, on its own or with `bobres rollback`, does not count) | Update to Y or newer. Never a release older than one this server ran |
| `the release's key list (seq N) is older than the one this server has seen` | An old key list, as a replay of an old release would bring | Do not install it. Escalate |
| `GB free in ...: an update needs at least 2 GB` | Not enough room for the backup and the new images | Free some space (old `-before-restore`/`-before-rollback` backups, `docker image prune`), run again. Nothing was changed |
| `BOBRES already runs X` | Nothing to do | - |
| `release X is newer than your license covers: it was issued on D, and the license's updates end on U` | The license includes the releases published until its "Updates until" date (`sudo bobres license status`) | Renew the license's updates with the seller. The current version keeps working. Nothing was changed |
| `warning: the license could not be read` | Core did not answer, so the license's update date was not checked | Nothing to fix for the update; check `bobres status` afterwards |
| `release X is a beta release and this install follows stable` | The release is a pre-release (signed for the beta channel) | Use the stable releases, or `--channel beta` if you want that pre-release |
| `BOBRES runs the pre-release X, newer than the latest stable release` | This store took a beta, then went back to stable | Nothing to do: it updates once a stable release is newer (or use `--channel beta`) |
| `the beta channel's releases could not be listed` | `api.github.com` did not answer (filtered, or over its 60 requests an hour) | Try again later, or use the stable channel, which needs only `github.com` |
| `BOBRES_UPDATE_CHANNEL in .env: unknown update channel` | A typo in `.env` | Set it to `stable` or `beta` (or remove the line), then `sudo bobres start` |
| `release metadata expired at ... (stale or replayed release)` with a correct clock | No release was published or re-signed for 45 days | Escalate: the vendor must re-sign the release ([releases runbook](releases.md)) |
| `checksum mismatch for bobres_linux_amd64` | The download was changed or cut | Run it again; if it repeats, escalate. Nothing was changed |
| `no CLI for this system` | The release has no binary for this CPU | Escalate |
| `this bobres is a development build` | A CLI built from source cannot replace itself | Install the released CLI with the install command from the README |
| `the backup before the update failed` | The database could not be backed up | Nothing was changed. Check `bobres status` and `df -h /opt/bobres`, fix, run again |
| `download ...: ... (is GitHub reachable from this server?)` | No route to GitHub | Check the server's network or proxy; GitHub must be reachable for updates |
| `bobres update: not implemented yet` | An old `bobres` | Update `bobres` itself first with the install command from the README, then `sudo bobres update` |

## Roll back the last update

When the new release runs but something is wrong with it:

```sh
sudo bobres rollback
```

It shows the restore point (from which version to which, and when), asks, stops the services, saves the current data in
`/opt/bobres/backups/bobres-<now>-before-rollback.bkp`, then puts back the previous `bobres`, the previous install files
and version, and **the database as it was before the update**, and starts the store. Orders, payments and changes made
since the update are undone (they stay in the backup it just made). Exit 1 when there is no restore point, when it was
already used, when its backup does not open, or when its folder could be changed by someone other than root.

Only the last update has a restore point, and it works once. After a rollback, run `sudo bobres update` again once the
problem is fixed. The rollback also puts back `/var/lib/bobres/release.version` (the "never older than what this
server installed" floor) as it was before the update, so the update undone does not hold back the releases below it:
after a beta you rolled back, the next stable release installs as usual.

The `-before-rollback` backup is not one of the 7 daily backups and is never pruned: delete it once you no longer need
it.

## If a rollback does not finish

The command says which step failed. The data from before the update is in the restore point's backup
(`/opt/bobres/.restore-point/bobres-<stamp>.bkp`; `restore-point.json` next to it).

- `the database was not restored`: the restore runs in one transaction, so the database is unchanged. Fix what the
  message says (often: the database was not running yet) and run `sudo bobres rollback` again; the restore point is
  still unused.
- The store is unhealthy after the rollback: `bobres status`, `bobres logs --tail 100 core`. If the previous images were
  removed (`docker image prune`), `bobres start` pulls them again.
- `the backup of the current data failed`: nothing was changed (the services were started again). Free the disk
  (`df -h /opt/bobres`), then `sudo bobres rollback`.
- The previous `bobres` was not put back (the warning says why): the rollback continues with the current one. The
  previous one is in `/opt/bobres/.restore-point/bobres`; copy it over `/usr/local/bin/bobres` if you need it.

## Going further back

There is no restore point for older updates. To run an older version:

```sh
sudo bobres install --version 1.2.3          # moves the images (keeps settings and data)
sudo bobres restore /opt/bobres/backups/bobres-<stamp>.bkp   # only if the data must go back too
```

Use a backup made while that version ran (or older): a newer version's database may not work with older code.

## Escalate when

- A release does not pass the signature check with a correct clock.
- The automatic rollback did not finish, or the store is unhealthy after a rollback.
- The same update rolls back twice.

Send: the full output of the command, `bobres status`, `/opt/bobres/.restore-point/restore-point.json` (it holds no
secrets; never send the rest of that folder: `files/.env` is the server's secrets), and `sudo bobres support-bundle`
(redacted logs and status, with the restore point's versions; read it through before you send it). See the
[runbooks index](README.md#escalate).
