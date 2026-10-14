# Launch posts for the Telegram channel (English)

Eight posts, in the order to publish them (the 14-day plan in [content-calendar.en.md](content-calendar.en.md) says
on which day). Persian version: [launch-posts.fa.md](launch-posts.fa.md).

**Before you post**
- Replace `{{LINK}}` with the page's address once Pages is on, or, until then, with the repository:
  `https://github.com/sobhanaz/bobres-3x-ui-telegram-panel`. There is no domain and no Telegram channel name yet:
  never invent one.
- Every claim below is in [claims-checklist.en.md](claims-checklist.en.md) with the file that proves it. If you
  change a sentence, check it there.
- Post the **JPG** images from `docs/marketing/images/` (made from the screenshots and illustrations): Telegram
  turns a dropped WebP into a sticker. `en/` has the English ones, `fa/` the Persian ones.
- The dashboard pictures use demo data and the bot pictures are illustrations. The posts say so, keep that line.
- The release must exist before the install command works (`v0.1.0`, see the README). Post 4 onward assume it does.
  Publish only after `v0.1.0` is released and `docs/pricing.md` exists: the tier numbers in post 7 come from it.
- Contact: until the owner has a private channel, the contact in a post is the project's **public** issue tracker
  (`https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues/new`). Everyone can read it, so say "never paste tokens or private data" next to it, as post 8 does.
- Prices are never written in a post. "Contact us" is the whole answer.

---

## Post 1: Launch

**Image:** `images/en/01-launch.jpg` (the banner).

```
BOBRES is here: launch your own VPN store on Telegram in one command.

It is a Telegram bot for your customers and a web dashboard for your team. It sells and manages 3x-ui subscriptions, and everything runs on your own server, with your own domain, bot and brand.

• Persian and English bot with a colored menu
• Buy, renew, wallet and a free trial
• Card to card, USDT, Telegram Stars and, optionally, Zarinpal
• One-command install, daily backups, updates that roll back by themselves

The core is free. Every new install starts with a 30-day trial of the premium features.

Pictures, pricing and FAQ: {{LINK}}
```

## Post 2: Why BOBRES

**Image:** `images/en/02-why-overview.jpg` (the dashboard overview, demo data).

```
Selling VPN by hand works until it doesn't.

Copying subscription links. Chasing receipts. Remembering who expires tomorrow. Answering "did my payment arrive?" at midnight.

BOBRES takes that loop off your hands:
1. The customer picks a plan in the bot.
2. They pay: the wallet, card to card, USDT, Stars, or Zarinpal if you switch it on.
3. The service is created on your 3x-ui panel and arrives with its link and a QR code.
4. The bot reminds them before it ends, with a Renew button.

You keep the part that needs a person: approving a receipt, answering a ticket, setting a price.

(The screenshot is a real install with demo data.)
{{LINK}}
```

## Post 3: The features, one by one

**Images (an album of three):** `images/en/03-features-a-welcome.jpg`, `images/en/03-features-b-checkout.jpg`,
`images/en/03-features-c-payments.jpg`.

```
What is inside, in short:

🛒 Buy, renew, free trial: traffic, time or both; discount codes; renewals that keep the same link; extra traffic packages; one trial per Telegram account.
👛 Wallet and invites: top-ups with history, and a reward when an invited friend makes a first purchase.
💳 Payments: card to card, USDT, Telegram Stars, and Zarinpal if you want it. Each payment settles exactly once.
🤝 Resellers (Standard and up): they buy for their customers inside the bot, on credit up to a limit, with a statement.
🖥 Many 3x-ui servers (Standard and up): each checked every minute, with a weight and servers per plan.
🎨 Your brand: name, logo, color, the bot's photo and every text, in both languages.

Pictures 1 and 2 are illustrations drawn from the bot's real messages; picture 3 is the dashboard with demo data.
{{LINK}}
```

## Post 4: Install, step by step

**Image:** `images/en/04-install.jpg` (the install frame of the demo).

```
Installing BOBRES, step by step.

You need:
• An Ubuntu 22.04/24.04 or Debian 11/12/13 server (1 GB of RAM, 2 GB is better; ports 80 and 443 free)
• A domain that points to it
• A bot from @BotFather, and your numeric Telegram ID
• A 3x-ui panel (3.8.5 to 3.9.0 are tested) with an API token

Then, as root on the server:

bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)

The installer checks the release's signature, asks for the domain, your ID, the bot token and the panel (tokens are typed hidden), creates every secret and starts the store. When it ends, send /dashboard to your bot and open the one-time login link.

Questions: the FAQ at {{LINK}}
```

## Post 5: Safety: HTTPS, backups, updates

**Images (an album of two):** `images/en/05-safety-a-https.jpg`, `images/en/05-safety-b-backups.jpg`.

```
Three things that keep a store running when you are not looking.

🔒 HTTPS you can change without fear: automatic for your domain by default, Let's Encrypt for the server's IP address, or your own certificate. A change is saved only once the web server accepts it, and one command brings you back if a wrong certificate locks you out.

💾 Backups every day, encrypted: the newest 7 stay on the server, each one (up to 50 MB) is sent to you on Telegram, and you can add a copy in S3 storage. Restoring is one command.

🔄 Updates with a net: the release's signature is checked, a backup is made, and if the new version does not come up healthy, BOBRES goes back by itself.

Plus alerts on Telegram when a service, a panel, the disk or a certificate has a problem, and again when it is fixed.
(Pictures: the dashboard with demo data.)
{{LINK}}
```

## Post 6: FAQ

**Image:** `images/en/06-faq-support.jpg` (the support inbox, demo data).

```
Your questions, short answers.

Do I need 3x-ui? Yes. Version 3 is supported, tested on 3.8.5 and 3.9.0.
Where is my data? On your server: your customers, orders and wallets stay in your database.
Can I use a language other than Persian? The bot speaks Persian and English.
What if the bot's texts are not what I want? Every text can be changed in the dashboard, in both languages.
What does a subscription change? See the next post.

More in the FAQ: {{LINK}}
```

## Post 7: Free and paid

**Image:** `images/en/07-free-vs-paid.jpg` (the tiers table of the page).

```
Free and paid, in plain words.

The core is free: the bot, the dashboard, the manual payment methods, Stars, Zarinpal (optional), backups and HTTPS.

Free: 1 server, up to 25 active services, 2 staff seats.
Standard: up to 3 servers, up to 500 active services, 5 staff seats, resellers and automatic USDT.
Pro: unlimited servers and services, white-label (no BOBRES mark), everything.

A subscription covers one install. Every new install starts with a 30-day trial of the premium features (all but white-label).
If a subscription ends, nothing is deleted: your customers keep their services and you go back to Free.

Prices: contact us. {{LINK}}
```

## Post 8: Closing call to action

**Image:** `images/en/08-cta.jpg` (the demo's first frame), or post the short demo video `site/assets/demo-en.mp4`.

```
Ready to try it?

1. Prepare a server, a domain, a bot and a 3x-ui panel.
2. Run the one command.
3. Add your plans and share your bot.

It starts free, with a 30-day trial of the premium features (all but removing the BOBRES mark). If you get stuck, or you want a subscription, open an issue in the project's repository. It is public, so never paste tokens or private data:
https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues/new

The page: {{LINK}}

The beaver will be waiting. 🦫
```
