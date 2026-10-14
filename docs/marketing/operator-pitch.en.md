# BOBRES: a one-page pitch for VPN operators (English)

Persian version: [operator-pitch.fa.md](operator-pitch.fa.md). Print it, paste it in a message, or turn it into a PDF.
Fill `{{CONTACT}}` with the owner's private channel. Until there is one, use the project's **public** issue tracker
(`https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues/new`) and say it is public: never paste tokens or private data there.

---

## BOBRES: your VPN store on Telegram, on your own server

**The problem.** You sell 3x-ui subscriptions. Every sale is a small chain of manual jobs: send the plan, wait for
a receipt, check it, create the client, copy the link, remember the expiry, answer "did my payment arrive?".
It works at ten customers and hurts at a hundred.

**The answer.** BOBRES is a Telegram bot for your customers and a web dashboard for your team. It does the chain
for you, and it runs on your server, with your domain, your bot and your brand.

### What a customer sees
- A bot in Persian or English, with a bottom menu and buttons colored by what they do.
- Pick a plan, pay (wallet, card to card, USDT, Telegram Stars, Zarinpal if you switch it on), and get the subscription link and a QR
  code in the same chat.
- Renew (the same link stays), add traffic, a wallet, one free trial, invite links, support with photos and PDFs,
  and reminders before a service or its traffic ends.

### What you see
- A dashboard with owner, admin and support roles: the receipt review queue, orders, plans, users, discount codes,
  broadcasts, a support inbox, branding and every bot text, in Persian or English, dark or light.
- Several 3x-ui servers (Standard and up), each checked every minute, with a weight and servers per plan.
- Resellers (Standard and up) who buy for their own customers inside the bot, on credit up to a limit, with a
  statement and a commission.
- Alerts on Telegram when a service, a panel, a payment gateway, the disk, a backup or a certificate has a problem.

### What keeps it running
- A one-command installer that verifies the signed release before it runs; `bobres update` verifies the release too
  and rolls back by itself if the new version does not come up healthy.
- Daily encrypted backups: the newest 7 on the server, each sent to you on Telegram, optionally a copy in S3.
- An HTTPS manager: automatic for your domain, Let's Encrypt for the server's IP address, or your own certificate.
- Four separate services with their own database roles and tokens, secrets encrypted at rest, hardening in one
  command.

### What it needs
An Ubuntu 22.04/24.04 or Debian 11/12/13 server (1 GB of RAM, 2 GB is better), a domain pointing to it, a bot from
@BotFather, your numeric Telegram ID and a 3x-ui panel (3.8.5 to 3.9.0 are tested) with an API token.

### What it costs
| | Free | Standard | Pro |
|---|---|---|---|
| 3x-ui servers | 1 | up to 3 | unlimited |
| Active services | up to 25 | up to 500 | unlimited |
| Staff seats | 2 | 5 | unlimited |
| Resellers, automatic USDT | no | yes | yes |
| White-label (no BOBRES mark) | no | no | yes |

The core is free. A subscription covers one install and every new install starts with a 30-day trial of the
premium features (all but white-label). If a subscription ends, nothing is deleted and you go back to Free. Prices: contact us.

### Straight answers
- **Whose data is it?** Yours. It stays in your server's database.
- **Is it secure?** It has real measures (see the FAQ), and no guarantee: review it before you go public.
- **Can I try it?** Yes: the Free tier, and the trial.
- **What about the law?** Operators are responsible for local law, Telegram's terms and their payment providers' rules.

**Next step:** run the install command and send `/dashboard` to your bot. Questions or a subscription: {{CONTACT}}
