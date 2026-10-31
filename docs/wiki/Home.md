# BOBRES Wiki

[فارسی](FA-Home)

BOBRES sells and manages VPN subscriptions on [3x-ui](https://github.com/MHSanaei/3x-ui) (Xray) panels and on Marzban, S-UI and Hiddify Manager. Your customers buy, pay, renew and ask for help in a Telegram bot or its Mini App. You run the store from a web dashboard. One `bobres` command on your server installs, updates, backs up and repairs everything. Each store is single-tenant and white-label: it runs on your own server, with your own domain, bot, panels and brand, and your customers' data stays on that server.

**As of v0.20.0, 2026-10-29.**

## Install in one command

Run it as root on a fresh Ubuntu 22.04/24.04 or Debian 11/12/13 server whose domain already points to it:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
```

It asks for your domain, your Telegram ID, your bot token and (optionally) your first 3x-ui panel. See [Getting Started](Getting-Started) for the whole walk-through and [Install Options](Install-Options) for the flags.

## Who needs which page

| You are... | Start here |
|---|---|
| Installing BOBRES for the first time | [Getting Started](Getting-Started), then [Install Options](Install-Options) |
| Learning the `bobres` command on the server | [The bobres Command](The-bobres-Command) |
| Running the store day to day | [Dashboard Guide](Dashboard-Guide), [Bot Guide](Bot-Guide), [Mini App](Mini-App) |
| Deciding what to sell and how to charge | [Plans and Pricing](Plans-and-Pricing), [Payments](Payments) |
| Connecting your 3x-ui, Marzban, S-UI or Hiddify servers | [Panels](Panels) |
| Working with resellers | [Resellers](Resellers) |
| Giving customers a page for their service, with your terms | [Subscription Page](Subscription-Page), [Terms and Privacy Pages](Terms-and-Privacy-Pages) |
| Watching sales | [Reports](Reports) |
| Buying or renewing a BOBRES license | [Licensing](Licensing) |
| Keeping the store safe and recoverable | [Updates, Backups, Restore](Updates-Backups-Restore), [HTTPS](HTTPS), [Security](Security) |
| Something is broken | [Troubleshooting](Troubleshooting), [FAQ](FAQ) |
| Reading or changing the code | [Architecture](Architecture), [Development](Development), [Roadmap](Roadmap) |

[Digital Accounts](Digital-Accounts) describes a feature of the vendor's own store. It is not available on a normal install.

## What you get

- **Customers:** a Persian and English Telegram bot and Mini App with plans, a wallet, a free trial, renewals, traffic packages, bulk purchase, gift and reward codes, invite links, support and a page for every service.
- **You:** a dashboard for plans, payments, users, resellers, reports, branding, panels, gateways, staff, backups, updates and licenses, plus Telegram alerts.
- **Payments:** wallet, card to card, USDT (manual, and automatic on TRON), TON (automatic), Zarinpal, IDPay, NextPay, Aqayepardakht and Telegram Stars.
- **Panels:** 3x-ui v3.8.5 to v3.10.2, Marzban 0.8, S-UI 1.6 and Hiddify Manager 14, several at once.
- **Price:** the core is free to run. A free tier, a 30-day trial of everything, then a paid license per install. See [Licensing](Licensing).

## Links

- Site: <https://bobresai.cv>
- Releases: <https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases>
- Issues: <https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues>
- Source and docs: <https://github.com/sobhanaz/bobres-3x-ui-telegram-panel>
- Security reports (private): see [Security](Security)

BOBRES is source-available and all rights are reserved. You may run an official release for your own store on the free tier. See the [LICENSE](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/LICENSE).
