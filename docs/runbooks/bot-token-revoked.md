# Runbook: bot token revoked

The bot stops answering because Telegram no longer accepts its token: it was revoked in @BotFather (by you, another
admin of the bot, or after a leak), or the bot was deleted. Commands run on the server as root; `dc` is defined in the
[runbooks README](README.md#before-you-start).

## Symptoms

- Customers and staff get no answer from the bot; no staff alerts; daily backups do not reach Telegram.
- `bobres status` shows `bot` restarting or unhealthy (it refuses to start with a token Telegram rejects).
- The bot's log:

  ```sh
  bobres logs --tail 50 bot
  ```

  - at start: `telegram rejected the bot token or is unreachable (check BOBRES_TELEGRAM_BOT_TOKEN)`;
  - while running: `getUpdates failed; retrying` with `401` / `Unauthorized`.

While the bot is down, staff get into the dashboard with `bobres admin link` (a one-time link printed on the server).

## Checks

1. Is it the token, or the network? Ask Telegram with the token from `.env`, without putting the token on a command
   line (it is passed to curl on stdin):

   ```sh
   printf 'url = "https://api.telegram.org/bot%s/getMe"\n' \
     "$(sed -n 's/^BOBRES_TELEGRAM_BOT_TOKEN=//p' /opt/bobres/.env | tr -d "'")" | curl -sS -m 15 -K -
   ```

   | Answer | Meaning |
   |---|---|
   | `{"ok":false,"error_code":401,"description":"Unauthorized"}` | The token is revoked, or the bot deleted: fix below |
   | `{"ok":true,"result":{...,"username":"..."}}` | The token works: the problem is elsewhere (see "Other causes") |
   | a timeout, `Could not resolve host`, `Connection reset` | This server cannot reach Telegram: a network or hosting problem, not the token |

2. In Telegram, open @BotFather > `/mybots`: is the bot still there? Who else has access to the account that owns it?

## Fix: a new token

1. In @BotFather: `/mybots` > your bot > **API Token**. If the old token may have leaked, choose **Revoke current
   token**. Copy the token it shows. (A deleted bot cannot come back: create a new one with `/newbot`; customers must
   then find the new username, so announce it in your channel.)
2. On the server, put it in with the menu, which reads it without showing it and checks it with Telegram first:

   ```sh
   sudo bobres
   ```

   Choose **Change the bot token** and paste the token. The menu re-runs the installer with the new token (the other
   settings and secrets stay) and recreates the bot. "Saved. The change is live." means done.

   Without the menu (a script), pass it in the environment, never as an argument:

   ```sh
   read -rs BOBRES_TELEGRAM_BOT_TOKEN && export BOBRES_TELEGRAM_BOT_TOKEN
   bobres install --skip-checks --quiet
   unset BOBRES_TELEGRAM_BOT_TOKEN
   ```

3. Check: `bobres status` (bot healthy), `bobres logs --tail 20 bot` shows `telegram bot ready` with the username,
   and `/start` in the bot answers. In webhook mode the bot registers its webhook with the new token when it starts.

Customers keep their accounts, services and wallets: they are tied to their Telegram ids, not to the token.

## Other causes

- `Conflict: terminated by other getUpdates request` in the log: another program uses the same token (an old install,
  a test copy, 3x-ui's own Telegram bot). Stop it, or give BOBRES its own bot: 3x-ui's bot must have a different
  token.
- The token works from elsewhere but this server cannot reach Telegram
  (`curl -sS -m 10 -o /dev/null -w '%{http_code}\n' https://api.telegram.org/` times out): fix the server's DNS,
  firewall or route with the hosting provider. The bot needs `api.telegram.org` (or the local Bot API server set in
  `BOBRES_TELEGRAM_API_URL`).
- After `bobres rollback`: it brings back the `.env` from before the last update, with the token of that time. If
  the token was changed since, change it again with the menu.

## Escalate when

- `getMe` answers `ok` but the bot still does not start or answer.
- @BotFather shows the bot, but every new token is rejected.

Send: the `getMe` answer (it holds no secret: never send the token), and `sudo bobres support-bundle` (read it first).
