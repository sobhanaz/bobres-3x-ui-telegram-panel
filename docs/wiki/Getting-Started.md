# Getting Started

This page takes you from an empty server to a store that can sell. [Install Options](Install-Options) lists every flag the installer accepts.

## What you need

| Item | Detail |
|---|---|
| Server | Ubuntu 22.04/24.04 or Debian 11/12/13, amd64 or arm64, root access |
| Size | 1 GB of RAM works (2 GB is better), 10 GB of free disk |
| Ports | 80 and 443 free. Caddy uses them for HTTPS |
| Domain | An A record that points to the server, for example `shop.example.com` |
| Telegram bot | Create one with [@BotFather](https://t.me/BotFather) and keep its token, for example for `@my_vpn_bot` |
| Your Telegram ID | The number (like `123456789`), not your @username. It makes you the owner |
| 3x-ui panel | Optional at install time. v3.8.5 to v3.10.2 are tested. You need an API token from the panel |

To create the API token, open 3x-ui, go to **Settings**, then **API tokens**, and make one. You can also skip the panel now and add a server later in the dashboard.

## Step 1: run the installer

As root on the server:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
```

The script installs Docker if it is missing, downloads the `bobres` command from the latest GitHub release and checks its signature and checksum before it runs anything. Then it asks only what has no safe default:

1. Your domain.
2. Your numeric Telegram ID.
3. The bot token (typed hidden, and checked with Telegram).
4. Optionally the first 3x-ui panel URL and API token. The token is checked against the real panel before anything is written.

It then starts the stack and waits until every service is healthy. It also turns on the firewall, fail2ban, automatic security updates and a swap file where that is safe. It never changes your SSH settings.

If your SSH connection drops, the install keeps going. Follow it from a new session:

```bash
tail -f /var/log/bobres-install.log
```

The install ends with a **"BOBRES is ready"** card. It shows your dashboard address, your bot and the **backup key**. Copy the backup key somewhere that is not this server. Backups cannot be read without it.

## Step 2: log in

You have three ways in:

- Open your bot in Telegram and send `/start`. As the owner you see **Admin** and **Dashboard** buttons. `/dashboard` gives a one-time login link to `https://shop.example.com/admin`.
- On the server, print a one-time link:

```bash
sudo bobres admin link
```

- Use the initial owner password. A fresh install prints it once, with the login `owner`. The first sign-in only lets you choose a new password and set up an authenticator app. If you lost it:

```bash
sudo bobres admin password
```

Every password login needs an authenticator code. Link logins do not.

## Step 3: set up the store

In the dashboard:

1. **Panel servers:** add your 3x-ui or Marzban panel if the installer did not. Use **Test connection**. See [Panels](Panels).
2. **Plans:** create what you sell, with a price, traffic and days. Mark one plan as the free trial if you want one. See [Plans and Pricing](Plans-and-Pricing).
3. **Payment gateways:** switch on the ways customers can pay and fill in the details. See [Payments](Payments).
4. **Branding & texts:** your store name, logo, colors, support contact and every bot text in Persian and English. Edit your own [Terms and Privacy pages](Terms-and-Privacy-Pages) here too.
5. **Settings:** time zone, notifications, required channel, maintenance mode.

## Step 4: test it yourself

Give your own account some wallet credit (Users, your account, adjust the balance; a reason is required), then buy a small plan in the bot. Check that you receive the subscription link and QR code, and that the service appears in your 3x-ui panel in the client group `BOBRES`.

## Step 5: check the install

```bash
sudo bobres check
```

It prints PASS, WARN or FAIL for the settings, services, disk, backups, bot token, license and servers, and tells you what to do about each problem.

## What to know on day one

- Every install starts with a **30-day trial** of every premium feature except white label. After that the free tier applies (1 server, 25 active services, 2 staff seats). Nothing is deleted and existing services keep working. See [Licensing](Licensing).
- Backups run every day, and the newest is sent to you on Telegram. Read [Updates, Backups, Restore](Updates-Backups-Restore).
- If something fails, start with [Troubleshooting](Troubleshooting).
