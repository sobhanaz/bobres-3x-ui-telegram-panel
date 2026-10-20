# Runbook: uninstall and start over

Use this to take BOBRES off a server so that the server is as it was before you installed it ("day one"), to hand a
server on, to move the store to another server, or to start again from nothing. `sudo bobres uninstall --purge` does
the whole job and asks before it does anything. It deletes the database, the secrets and every backup in the install
directory: read "Before you start" first.

All commands run on the server as root. The install is in `/opt/bobres` unless you installed elsewhere (then add
`--dir <dir>`). How the command works, flag by flag, and the full list of what BOBRES puts on a server:
[06-installer.md](../06-installer.md), "What installing BOBRES changes on a server" and "Uninstall".

## Which command do you want?

| You want | Command |
|---|---|
| Stop the store and free its containers, but keep the data (you will start it again) | `sudo bobres uninstall`, later `sudo bobres start` |
| The server as on day one: everything BOBRES installed or wrote, gone | `sudo bobres uninstall --purge` |
| The same, and Docker and the packages BOBRES installed | `sudo bobres uninstall --purge --remove-docker --remove-packages` |
| See what would go, without changing anything | `sudo bobres uninstall --purge --dry-run` |
| Check later that nothing is left | `sudo bobres uninstall --verify` |

## Before you start

1. **Decide what happens to the data.** The purge deletes the database (customers, orders, the ledger, settings),
   the HTTPS certificates, `.env` (the secrets, the backup key) and `/opt/bobres/backups`. To keep the store, make a
   final backup: the purge offers one, or give it up front:

   ```sh
   sudo bobres uninstall --purge --backup-to /root/bobres-final.bkp
   ```

   The file is written outside the install directory, synced and read back completely (every part is checked with the
   install's key) **before anything is deleted**; if that fails, nothing is deleted. Copy it off the server
   (`scp root@server:/root/bobres-final.bkp .`) before you destroy the server.
2. **Keep the backup key.** The backup (and every older one in `backups/`) opens only with the install's
   `BOBRES_BACKUP_KEY`, which is deleted with `.env`. On a terminal the purge shows it once after the final backup.
   Otherwise read it first, from `sudo bobres` > "Backup key", and store it away from the server (a password manager).
3. **See the plan.** `sudo bobres uninstall --purge --dry-run` prints exactly what would be removed, with sizes and
   counts, and what is kept. It needs no confirmation and changes nothing.
4. **Things on this server that are not BOBRES's are not touched**: other Docker containers, images, volumes and
   networks, SSH settings and the SSH firewall rule, DNS, other users' files. If the plan lists something you did not
   expect, stop and look.

## Do it

```sh
sudo bobres uninstall --purge
```

It prints the plan, asks whether to make a final backup (Enter writes it into `/root`; type a path; or `no`), and then
asks you to type the install's domain, exactly (upper and lower case count). Anything else cancels and changes
nothing. Then it works through its steps and prints what it did; the same text goes to `/var/log/bobres-uninstall.log`,
which stays.

For a script (nothing is asked, so it must say what it means):

```sh
sudo bobres uninstall --purge --yes --confirm panel.example.com --backup-to /root/bobres-final.bkp
sudo bobres uninstall --purge --yes --confirm panel.example.com --no-backup
```

`--confirm` must be the domain (or, when the install has none, the host name) exactly.

Options that matter:

- `--keep-backups` keeps `/opt/bobres/backups` (and nothing else of the install directory). They need the old backup key.
- `--remove-docker` also removes Docker, only if `install.sh` installed it for BOBRES (it keeps a record), nothing but
  BOBRES's objects is left in it, and no other package needs the ones to go. If another container, image or volume is
  there, Docker stays and the output says what holds it.
- `--remove-packages` also removes what `bobres harden` installed (ufw, fail2ban, unattended-upgrades) and openssl if
  `install.sh` installed it, under the same conditions. ufw is kept while it holds a rule that is not BOBRES's.
- `--disable-firewall` turns ufw off again if BOBRES turned it on. By default ufw stays on, with your SSH rule, so a
  server whose other services relied on it is not opened up by an uninstall.

## What you should see at the end

```
Leftover scan
  OK    Nothing of BOBRES is left on this server.
  Kept on purpose:
        - /var/log/bobres-uninstall.log: the record of the uninstall
        ...
```

and exit status 0. "Kept on purpose" lists what stays and why (the SSH rule, ufw if it is on, Docker and the packages
when you did not ask for them to go, a file you changed after BOBRES wrote it, an image another container uses). If it
says **N thing(s) of BOBRES are still here**, the list names each with the reason, and the exit status is 1.

## If it stops half-way

Run the same command again. Every step looks at what is there now, so it finishes the job; the command and the install
receipt (`/var/lib/bobres/install-receipt.json`) are deleted last for this reason, and a run that failed before the
end leaves them in place. Typical stops:

| It says | Do |
|---|---|
| `bobres-updater.service is busy right now` | An update is running. Wait for it (`journalctl -u bobres-updater.service -f`), then run it again |
| `Docker does not answer` | `sudo systemctl start docker`, then run it again |
| `docker volume rm ... failed` | Something still uses the volume: `docker ps -a` shows it. Remove that container if it is BOBRES's; it is not touched if it is not |
| `refusing ...: it is a symbolic link` / `it is not a BOBRES install` / `it is a system directory` | The `--dir` is wrong. Nothing was changed. Point it at the install directory |
| `the BOBRES containers on this server belong to the compose project in ...` | The running stack was started from another directory than `--dir`. Use that directory |
| `run it as root` | Use `sudo` |

## Start over on the same server

After a purge the server is clean. Install again as the first time:

```sh
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
```

then, to bring the old store back, restore the final backup (below). If you used `--remove-docker`, the installer
installs Docker again.

## Restore the final backup on a new server

1. Install BOBRES on the new server with the same domain (DNS must point to it) and the same bot token, with
   `install.sh` as in the README. Let it finish and start.
2. Copy the final backup to the new server, and put the old backup key on one line in a file without letting it reach
   the shell history:

   ```sh
   umask 077; cat > /root/old-backup-key.txt
   ```

   Paste the key, press Enter, then Ctrl-D.
3. Restore (it backs up the new, empty data first, and replaces the database in one transaction):

   ```sh
   sudo bobres restore /root/bobres-final.bkp --key-file /root/old-backup-key.txt
   ```

   The backup carries the services' master keys and puts them in `.env`, so the 3x-ui tokens, staff two-factor secrets
   and gateway settings it holds can be read. Details and the checks afterwards: [restore.md](restore.md).
4. Delete the key file: `shred -u /root/old-backup-key.txt` (or `rm`).

## What the purge does not undo

Package lists apt downloaded, apt and dpkg logs, a package apt upgraded to satisfy a dependency of Docker, the
system's `/etc/group-` backup, and, until the next reboot, Docker's iptables chains. None of it is BOBRES's. The full
list is in 06-installer.md, "Not undone, by design".

## An install made before this command existed

A server installed by an older release has no install receipt. The purge still works: it uses the headers, names and
labels the installer wrote, and takes the careful choice where they prove nothing (it keeps `/swapfile`, leaves ufw
on, and refuses `--remove-docker` and `--remove-packages` because nothing proves BOBRES installed them). Run
`sudo bobres install` first if you want those too (it keeps your settings and secrets, and writes a receipt for what can
still be known; Docker, which `install.sh` installed before it kept a record, still cannot be proven).

## When to ask for help

If the scan lists something you cannot explain, or a step fails again after you fixed its cause, run
`sudo bobres support-bundle` **before** a full purge (it is deleted with the install directory otherwise) and send it
with `/var/log/bobres-uninstall.log`.
