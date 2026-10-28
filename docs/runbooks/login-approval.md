# Approving dashboard sign-ins in Telegram

Use this when the owner turned on **Settings > Sign-in security > Approve sign-ins in Telegram** (it is off by default) and a
staff sign-in does not get through, or when you want to know what the option does and how to leave it. How it works is in
[docs/04-dashboard.md](../04-dashboard.md), "Approving sign-ins in Telegram".

## What staff see

- After the login link or the password and the authenticator code, the page says **Waiting for approval in Telegram**
  and counts down 5 minutes. The bot has messaged the staff member (and the owners, for a sign-in by someone who is not
  an owner, unless **Also ask the owners** is off) with the time, the IP address, the browser and the system, and two
  buttons: Approve and Deny.
- Approve: the page goes in on its own within a few seconds. Deny, or 5 minutes without an answer: the page says so and
  nothing was ever opened. The Telegram message is edited to show the result.
- "No approval can be asked": the page says why (the bot is not running, the person blocked the bot or never started it,
  Telegram does not answer, nobody can be reached) and shows the command below. It does not wait for nothing.

## The way out: switch it off on the server

This needs neither the bot, nor Telegram, nor a dashboard session. Only the database must be running.

```sh
sudo bobres admin approval status     # is it on? who is asked? how many sign-ins wait now?
sudo bobres admin approval off        # switch it off; the sign-ins that wait are ended
```

`off` works through the same code as the dashboard: the audit log records who and from where (`setting.set`, source
`cli`) and the owners are told in Telegram when the bot can be reached. If core is not running, the command runs in a
new container of core's image. There is no `on` here: the owner turns it back on in the dashboard (Settings > Sign-in security),
signed in, after reading what it does.

After `off`, staff sign in at once again. A sign-in that was waiting is ended: sign in again.

## Problems

| Symptom | Likely cause | What to do |
|---|---|---|
| The page says the bot is not running (`bot_down` / `no_bot`) | The bot container is down, or core cannot reach it | `sudo bobres status`, `sudo bobres logs bot`; start it with `sudo bobres start`. A link from `sudo bobres admin link` does not help: it waits for approval like any other sign-in, so use `sudo bobres admin approval off` to get in meanwhile |
| "The message could not be delivered" (`blocked`) | The staff member (and the owners) blocked the bot or never started it | Open the bot in Telegram and send `/start`; or switch it off |
| "Telegram did not answer" (`telegram`) | The server cannot reach Telegram, or the bot token was revoked | [Bot token revoked](bot-token-revoked.md); then `approval off` if it lasts |
| "Nobody answered" (`no_answer`) | The message went out but nobody tapped in 5 minutes | Look in Telegram; sign in again |
| "Wait a few minutes" (rate limited) | 3 requests open or 6 started in 15 minutes for the account | Answer or let the open ones end; do not retry in a loop |
| Approve does nothing / "This request has ended already" | The request expired, was denied, was cancelled, or was answered by someone else first | Sign in again: each attempt is a new request |
| An old or forwarded message does nothing | By design: a button works once, only for the person it was sent to | Use the newest message |
| A message you did not expect | Someone is signing in as you | Tap **Deny**. Then change the dashboard password (My account), and look at the audit log (`dashboard.login_requested`) for the IP address |

## What is recorded

`dashboard.login_requested`, `dashboard.login_approved`, `dashboard.login_denied`, `dashboard.login_expired`,
`dashboard.login_cancelled`, `dashboard.login_undeliverable` and `dashboard.login_approval_invalid` (a forged or stale
answer) in the Audit log, with the account, the IP address and, for an answer, who gave it and in which role.

## Lost access altogether

If every owner lost their Telegram account and the approval is on, no one can approve. Run
`sudo bobres admin approval off` on the server, then use `sudo bobres admin password` or `sudo bobres admin link` as usual.
Owners are named only on the server (`sudo bobres admin owners`), so a stolen dashboard account cannot add one.
