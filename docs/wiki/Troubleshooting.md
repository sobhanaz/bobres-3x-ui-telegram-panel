# Troubleshooting

Start here when something is wrong. Run every command on the server as root. Add `--dir` if you installed outside `/opt/bobres`.

## First look

1. See which services run. Exit code 1 means something needs attention:

```bash
sudo bobres status
```

2. Check every part of the install (PASS, WARN, FAIL or SKIP lines):

```bash
sudo bobres check
```

3. Read the logs of the service involved (`core`, `bot`, `payments`, `provisioner`, `backup`, `postgres`, `redis`, `caddy`, `tls-agent`). Flags go before the name:

```bash
sudo bobres logs --tail 100 core
```

4. In the dashboard, open **System > Health** for versions, readiness checks and open problems. The owner can read recent warnings and errors on the **Logs** tab.

If the bot is down, get into the dashboard with a one-time link:

```bash
sudo bobres admin link
```

## By symptom

| Symptom | Likely cause | What to do |
|---|---|---|
| The bot does not answer | Telegram rejects the token, the server cannot reach Telegram, or the bot is down | `bobres logs --tail 50 bot`. If the token was revoked, use **Change the bot token** in `sudo bobres`. "Conflict: terminated by other getUpdates request" means another program uses the same token (an old install, or 3x-ui's own bot). [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/bot-token-revoked.md) |
| A customer paid, the order still waits | A manual receipt is not approved yet, a gateway has not confirmed, or an automatic USDT transfer did not match | Open the order and look at its Payments section. Check the Review queue. Use **Test connection** on the gateway. [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/payment-stuck.md) |
| The order is paid but no service arrived | The panel is down or refuses the token, or no inbound is chosen | Overview shows failed deliveries. Fix the panel, then press **Retry delivery now**, or wait: retries go on, up to every 30 minutes. [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/provisioning-backlog.md) |
| A server shows "Not answering" | The panel is down, filtered, or its token was regenerated | On **Panel servers**, use **Test connection**. Create a new API token in 3x-ui if it was refused. [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/xui-down.md) |
| Browsers warn about the dashboard, or it does not open | Certificate not issued, DNS or ports wrong, or a bad own certificate | `sudo bobres ssl status`. To go back to the automatic certificate: `sudo bobres ssl auto`. See [HTTPS](HTTPS) |
| You cannot sign in | Lost password or authenticator | `sudo bobres admin link` for a link, or `sudo bobres admin password` for a new owner password |
| The sign-in page waits for an approval that never comes | Sign-in approval is on and the bot is down, Telegram is out of reach, or the message was not delivered | `sudo bobres admin approval off`, then fix the bot and turn it on again in Settings > Sign-in security. [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/login-approval.md) |
| No daily backup in Telegram | The backup failed, the disk is full, or the bot is down | `bobres logs backup`, then `sudo bobres backup`. [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/backups-not-arriving.md) |
| The disk is almost full | Backups, old `-before-restore` files, Docker images | `df -h /opt/bobres`. Delete old safety backups and run `docker image prune`. [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/disk-full.md) |
| An update failed | The signature, the disk, or health after the install | It rolls back by itself. To undo by hand: `sudo bobres rollback`. For a dashboard update: `sudo journalctl -u bobres-updater` and `/opt/bobres/run/update/status.json`. [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/update-rollback.md) |
| A new purchase is refused | The install is at its license limit | `sudo bobres license status`. See [Licensing](Licensing). [Runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/license.md) |
| The dashboard, the Mini App and the payment pages do not open after a reboot, but the bot answers | Docker left Caddy, the gateway, down after the reboot (a 0.17.0 install and update marked it "manually stopped") | `sudo bobres start`. From 0.17.1 a unit, `bobres.service`, starts the store at every boot, and `sudo bobres check` has a **Start at boot** line. A store you stopped on purpose with `sudo bobres stop` stays stopped after a reboot until `sudo bobres start` |
| The Mini App has no Store button | No public HTTPS address, or `miniapp.enabled` is off | See [Mini App](Mini-App) |
| A subscription page shows 404 | The page is off, the service is pending or deleted, or the address was regenerated | See [Subscription Page](Subscription-Page) |
| The installer fails | Ports 80 or 443 taken, little RAM or disk, a wrong token | Read `/var/log/bobres-install.log`, fix it and run the install again. It keeps your settings |
| A customer's VPN will not connect | The panel's host or inbound, not BOBRES | Check the service in the dashboard (status, expiry, usage) and in the panel itself. The connection runs on the panel's host |

## Alerts in Telegram

Core checks the store every minute and messages the staff who get system alerts. A problem must be seen twice in a row before it is announced, and a fix is announced once it has held for 5 minutes. The [runbooks index](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/README.md) maps each alert to its runbook. The bot cannot alert about itself while it is down. The alerts arrive when it is back.

## Do not

- Do not change database rows by hand. Use the dashboard, the bot or `bobres`, so the ledger, audit log and events stay consistent.
- Do not run `docker volume prune` or `docker system prune --volumes`. Depending on the Docker version they can delete your database.
- Do not run `docker compose config`, `docker inspect` or `env` in a container while someone watches your screen. They print secrets.
- Do not create a client in 3x-ui for a waiting order. The worker would make a second one.

## Asking for help

Make a support bundle:

```bash
sudo bobres support-bundle
```

It writes `/opt/bobres/bobres-support-<time>.tar.gz`, readable by root only: versions, service status, the doctor's checks, the newest 300 log lines of each service, the names of the `.env` keys (never values) and the list of backups. Tokens, passwords, keys and public IP addresses are removed. Logs can still show Telegram IDs, usernames, order IDs and panel host names, so read it before you send it, never post it in a public issue, and delete it afterwards.

Never send `.env`, a `.bkp` file, the backup key or anything from `/opt/bobres/.restore-point/files/`. For a security problem, use the private form on [Security](Security). Anything else can go on the [issue tracker](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues), without secrets.

See also [FAQ](FAQ).
