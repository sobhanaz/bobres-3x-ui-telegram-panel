# Outreach templates for VPN operators (English)

For people who already sell VPN (3x-ui subscriptions, usually on Telegram). Persian version:
[outreach.fa.md](outreach.fa.md).

**Rules of thumb**
- One person at a time, by hand. Never bulk-send, never add people to groups, never write to someone who did not
  publish a way to reach them about business.
- Fill in the `{{...}}` parts from what they really do (their channel, their bot, a plan they sell). If you cannot,
  use the short message and leave the personal line out.
- Say only what is true: [claims-checklist.en.md](claims-checklist.en.md). No customer counts, no testimonials, no
  guarantees, no prices.
- One follow-up, no more. If they say no, thank them and stop.
- Where the text says "write to me", that is a promise you make, so keep it.
- `{{LINK}}`: the page's address once Pages is on, or the repository
  `https://github.com/sobhanaz/bobres-3x-ui-telegram-panel` until then. There is no domain and no Telegram handle
  for BOBRES yet.

---

## 1. Short DM

```
Hi {{NAME}}, I saw {{THEIR_CHANNEL_OR_BOT}}. I built BOBRES: a Telegram bot and a web dashboard that sell and manage 3x-ui subscriptions on your own server (buying, renewals, wallet, card/USDT/Stars, optional Zarinpal). It installs with one command and the core is free; resellers and automatic USDT come with a subscription. Worth a look? {{LINK}}
```

## 2. Long DM

```
Hi {{NAME}},

I follow {{THEIR_CHANNEL_OR_BOT}} and I thought you might like something I have been building.

BOBRES runs a Telegram bot for your customers and a web dashboard for your team, on your own server, with your own domain, bot and brand. A customer picks a plan, pays (the wallet, card to card, USDT, Telegram Stars, or Zarinpal if you switch it on) and gets the subscription link and a QR code in the chat. It renews, adds traffic and reminds them before a service ends. You review receipts in the dashboard, answer tickets, send broadcasts and watch revenue.

It has daily encrypted backups (also sent to you on Telegram), an HTTPS manager, and updates that roll back by themselves. The bot speaks Persian and English, and every text can be changed. Several 3x-ui servers and resellers are part of the paid tiers.

The core is free (1 server, up to 25 active services). If you grow, there are Standard and Pro subscriptions, and every new install starts with a 30-day trial of the premium features (all but removing the BOBRES mark).

Screens and the install command: {{LINK}}

If you try it and get stuck, write to me here and I will help. If it is not for you, no problem at all.
```

## 3. Follow-up (once, 4 to 5 days later)

```
Hi {{NAME}}, a short follow-up on BOBRES, no pressure. One thing people ask first: yes, it runs on your own server and your customers stay in your database. The 30-day trial of the premium features starts by itself on a new install, so you can try resellers and automatic USDT before deciding anything. Here is the page again: {{LINK}}. If it is not useful to you, just tell me and I will not write again.
```

*(Variant for someone who answered but went quiet:)*

```
Hi {{NAME}}, did you get a chance to look? If something was unclear, tell me which part, and I will answer it or fix the page.
```

## 4. Reply to "Is it free?"

```
The core is free: the bot, the dashboard, the manual payment methods, Stars, Zarinpal (optional), backups and HTTPS. The Free tier covers 1 server and up to 25 active services, with 2 staff seats.

You pay only if you need more: Standard (up to 3 servers, up to 500 active services, 5 staff seats, resellers and automatic USDT) or Pro (unlimited servers and services, white-label, everything). A subscription covers one install, and every new install starts with a 30-day trial of the premium features (all but removing the BOBRES mark). For prices, contact us here.

If a subscription ends, nothing is deleted: your customers keep their services and you go back to Free.
```

## 5. Reply to "Is it secure?"

```
I will not promise you "secure": nobody can. Here is what is actually in it, so you can judge:

• Four separate services, each with its own database role and token. The 3x-ui tokens live only in the provisioner, the gateway secrets only in payments.
• Secrets are encrypted at rest, with a separate key for each service.
• Releases are signed (Ed25519), and BOBRES's own Docker images are signed with cosign. The Postgres, Redis and Caddy images come from others and are pinned by version (Caddy also by digest). The installer and the updater check the release's signature before they run it.
• The dashboard has CSRF protection, an authenticator code for every password login, a strict CSP and an append-only audit log.
• BOBRES's own services run as an unprivileged user, read-only, with no extra capabilities.
• Backups are encrypted.

The design is written down in docs/07-security.md in the repository ({{LINK}}), and the repository is public, so you can read it. I would still review the install yourself before you open it to customers.
```

## 6. Reply to "Can I try it?"

```
Yes. You need a server (Ubuntu 22.04/24.04 or Debian 11/12/13, 1 GB of RAM, 2 GB is better), a domain pointing to it, a new bot from @BotFather with your numeric Telegram ID, and a 3x-ui panel (3.8.5 to 3.9.0 are tested) with an API token.

Then, as root on the server:
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)

It checks the release's signature, asks for those four things (tokens are typed hidden) and starts the store. Send /dashboard to your bot to log in. There is a free tier with the limits above, and a new install starts with a 30-day trial of the premium features, so you can test everything except removing the BOBRES mark. If you get stuck, write to me here.
```
