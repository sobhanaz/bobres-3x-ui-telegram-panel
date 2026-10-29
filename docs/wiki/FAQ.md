# FAQ

## Getting started

**What do I need to run BOBRES?**
A server with Ubuntu 22.04/24.04 or Debian 11/12/13 (amd64 or arm64), 1 GB of RAM (2 GB recommended), 10 GB of free disk, ports 80 and 443 free, root access, a domain pointing to it, a Telegram bot from @BotFather and your numeric Telegram ID. See [Getting Started](Getting-Started).

**Do I need a 3x-ui panel to install?**
No. You can skip it and add servers later in the dashboard. You need one before you can deliver services.

**Can 3x-ui run on the same server?**
Yes. Use `--xui-allow-private` and see [Install Options](Install-Options). The installer then skips the firewall step and tells you why.

**Can I use my 3x-ui panel's own Telegram bot token for BOBRES?**
No. Two programs cannot poll one bot token. Give BOBRES a bot of its own.

**Which panels work?**
3x-ui v3.8.5 to v3.10.2 and Marzban 0.8. Marzban 1.0 (beta), S-UI and Hiddify are not supported. See [Panels](Panels).

**Which languages does the bot speak?**
Persian and English. Arabic, Russian and Turkish are on the [Roadmap](Roadmap).

## Money and licenses

**Do I need a license?**
No. The free tier covers 1 server, 25 active services and 2 staff seats, and every install gets a 30-day trial of everything but white label. Standard costs 4 USDT a month and Pro 12. See [Licensing](Licensing).

**What happens if my license ends?**
After a grace period the install falls back to the trial or the free tier. Nothing is deleted. Existing services keep working and renewing. Only new servers, services, resellers, staff and payment methods beyond the free tier are refused.

**Do my customers see anything about licensing?**
No. At a limit they see "New purchases are temporarily unavailable. Please contact support."

**Does the vendor get my customers' data?**
No. Activation sends the license key, install ID, domain and version, and only if you set a license server.

**Can I remove "Powered by BOBRES"?**
Only with a Pro license (white label).

**Can I host BOBRES for other people or resell it?**
Not without a paid license from the owner. See the [LICENSE](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/LICENSE).

**Which payment methods work?**
Wallet, card to card, manual USDT, a Zarinpal link, Zarinpal online, IDPay, NextPay, Aqayepardakht, Telegram Stars, automatic USDT on TRON and automatic TON. IDPay, NextPay, Aqayepardakht and automatic TON are not yet tried against the real services. See [Payments](Payments).

**How is the USDT price worked out?**
The bot reads the live Toman price of USDT from Tetherland and Wallex every couple of minutes. You can add a margin of 0 to 30%, or use your own rate.

**Can I delete a plan?**
Only the owner can. It is taken off sale but never erased: old orders, reports and refunds keep it, and its services keep working.

## Running the store

**I lost my dashboard password or my authenticator.**
Run `sudo bobres admin password` for a new owner password, or `sudo bobres admin link` for a one-time link.

**How do I add another owner?**
Owners are named on the server: `sudo bobres admin owners add <telegram id>`. Admins and support staff are invited on the Staff page. Seats count against your license.

**Where is my backup key?**
It is shown once at the end of the install, and again in `sudo bobres`, under **Backup key**. Without it backups cannot be read. Keep it off the server.

**How do I replace the master keys?**
`sudo bobres rotate-key --dry-run`, then `sudo bobres rotate-key`. It takes a backup, seals everything the keys protect again with new ones, and puts the old keys back by itself if anything fails. The backup key is a different key and stays. See [Security](Security).

**How do I move to a new server?**
Install on the new server with the same domain and bot token, then restore a backup with its key. See [Updates, Backups, Restore](Updates-Backups-Restore).

**Will customers lose service if BOBRES is down?**
Active VPN services depend on your panel's host, not on BOBRES. While BOBRES is down nobody can buy or renew.

**Why did a customer's service not arrive after payment?**
The panel is probably down. The order stays paid and is retried. See [Troubleshooting](Troubleshooting).

**Can I see sales by day?**
Yes. [Reports](Reports) shows revenue per currency by day, week or month, in the Gregorian or Jalali calendar, with CSV files and an optional daily summary in Telegram.

**Do the subscription page's app buttons work?**
They have not been tried on real devices. The link and QR code always work. See [Subscription Page](Subscription-Page).

**Is the Mini App checked on every Telegram client?**
No. Tests use a fake Telegram, and real phones and desktops still need checks. See [Mini App](Mini-App).

## Security and trust

**Is it safe to run the one-line installer?**
The script verifies the release signature and checksum before it runs the `bobres` command, and you can read it first. What remains trusted is the script itself, fetched over HTTPS, and Docker's own installer. See [Security](Security).

**Does the installer lock me out of SSH?**
No. It never changes SSH settings. The firewall keeps your detected SSH port open, and stays off if it cannot find one.

**How do I report a vulnerability?**
Privately, from the repository's **Security** tab. Do not open a public issue.

## Other

**Is the source open?**
It is source-available, with all rights reserved. You can read and audit it, and run an official release for your own store on the free tier.

**How do I remove BOBRES?**
`sudo bobres uninstall --purge` removes everything BOBRES put on the server. It asks about a final backup first. Use `--dry-run` to see the plan. See [The bobres Command](The-bobres-Command).
