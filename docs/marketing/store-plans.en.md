# Plans, price list and bot texts for the owner's own VPN store (English)

A template for the store you run on BOBRES yourself. Persian version: [store-plans.fa.md](store-plans.fa.md).

Nothing here is a price or a promise: every `{{...}}` is yours to fill in, and **the braces must not stay in anything
you paste into the dashboard or the bot**. The store's name, its support handle, its card and wallet details and its
prices do not exist in this repository on purpose.

## 1. Plans

Prices are in Toman (`IRT`), whole numbers. Enter them in the dashboard (**Plans → New plan**) or with the bot's
`/plan_add` (an admin sends it in the bot): `/plan_add price currency days GB English name | Persian name`, where 0 days
or 0 GB means unlimited.

| Plan (English / Persian) | GB | Days | Price | Bot command |
|---|---|---|---|---|
| Free trial / آزمایشی رایگان | 1 | 1 | free | `/trial 1 1` |
| 10 GB · 30 days / ۱۰ گیگ · ۳۰ روزه | 10 | 30 | `{{PRICE_10GB_30D}}` | `/plan_add {{PRICE_10GB_30D}} IRT 30 10 10 GB · 30 days \| ۱۰ گیگ · ۳۰ روزه` |
| 30 GB · 30 days / ۳۰ گیگ · ۳۰ روزه | 30 | 30 | `{{PRICE_30GB_30D}}` | `/plan_add {{PRICE_30GB_30D}} IRT 30 30 30 GB · 30 days \| ۳۰ گیگ · ۳۰ روزه` |
| 100 GB · 30 days / ۱۰۰ گیگ · ۳۰ روزه | 100 | 30 | `{{PRICE_100GB_30D}}` | `/plan_add {{PRICE_100GB_30D}} IRT 30 100 100 GB · 30 days \| ۱۰۰ گیگ · ۳۰ روزه` |
| Unlimited traffic · 30 days / ترافیک نامحدود · ۳۰ روزه | unlimited | 30 | `{{PRICE_UNLIMITED_30D}}` | `/plan_add {{PRICE_UNLIMITED_30D}} IRT 30 0 Unlimited traffic · 30 days \| ترافیک نامحدود · ۳۰ روزه` |
| 300 GB · 90 days / ۳۰۰ گیگ · ۹۰ روزه | 300 | 90 | `{{PRICE_300GB_90D}}` | `/plan_add {{PRICE_300GB_90D}} IRT 90 300 300 GB · 90 days \| ۳۰۰ گیگ · ۹۰ روزه` |
| Traffic package: +10 GB / بسته ۱۰ گیگ ترافیک | 10 | none | `{{PRICE_TOPUP_10GB}}` | `/topup_add {{PRICE_TOPUP_10GB}} IRT 10 +10 GB \| ۱۰ گیگ اضافه` |

Notes
- Start with the trial and three or four plans. Customers choose faster from a short list.
- Keep the same name style in both languages: size, then days. The bot shows `name · price` on each button.
- Reseller prices (if you use resellers) are set per tier on the dashboard's **Resellers** page, not here.
- Discount codes: `/discount_add WELCOME20 20% 100 30` (code, discount, maximum uses, days valid) or the dashboard's
  **Discounts & referrals** page. The referral reward is a percentage you set there too.

## 2. The price-list message for your channel

Pin it, and update it whenever a price changes.

Persian:

```
📋 لیست قیمت {{STORE_NAME}}

🎁 تست رایگان: ۱ گیگ، ۱ روز (از منوی ربات)
۱۰ گیگ · ۳۰ روزه: {{PRICE_10GB_30D}} تومان
۳۰ گیگ · ۳۰ روزه: {{PRICE_30GB_30D}} تومان
۱۰۰ گیگ · ۳۰ روزه: {{PRICE_100GB_30D}} تومان
ترافیک نامحدود · ۳۰ روزه: {{PRICE_UNLIMITED_30D}} تومان
۳۰۰ گیگ · ۹۰ روزه: {{PRICE_300GB_90D}} تومان
➕ بستهٔ ۱۰ گیگ حجم اضافه: {{PRICE_TOPUP_10GB}} تومان

پرداخت: کارت به کارت، تتر، استارز تلگرام {{AND_OTHER_METHODS_YOU_TURNED_ON}}
خرید و تمدید: @{{BOT_USERNAME}}
پشتیبانی: @{{SUPPORT_USERNAME}}
```

English:

```
📋 {{STORE_NAME}} price list

🎁 Free trial: 1 GB, 1 day (from the bot menu)
10 GB · 30 days: {{PRICE_10GB_30D}} Toman
30 GB · 30 days: {{PRICE_30GB_30D}} Toman
100 GB · 30 days: {{PRICE_100GB_30D}} Toman
Unlimited traffic · 30 days: {{PRICE_UNLIMITED_30D}} Toman
300 GB · 90 days: {{PRICE_300GB_90D}} Toman
➕ 10 GB traffic package: {{PRICE_TOPUP_10GB}} Toman

Payment: card to card, USDT, Telegram Stars {{AND_OTHER_METHODS_YOU_TURNED_ON}}
Buy and renew: @{{BOT_USERNAME}}
Support: @{{SUPPORT_USERNAME}}
```

List only the payment methods you turned on in **Payment gateways**, with their details filled in.

## 3. Texts to paste into **Branding & texts**

### Store tab

| Field | What to put |
|---|---|
| Store name | `{{STORE_NAME}}` (at most 64 characters). It is what the bot's messages call the store. |
| Support contact | `@{{SUPPORT_USERNAME}}` |
| Brand color | your own, or leave empty for teal |
| Logo | your own square image (PNG, JPEG or WebP, up to 512 KB). The beaver is BOBRES's own mascot, not your store's. |
| Terms and privacy links | `{{TERMS_URL}}` and `{{PRIVACY_URL}}`, only once you have real pages. Empty hides the buttons. |
| Toman name, Persian / English | leave empty for «تومان» / "Toman", or your own word (at most 16 characters) |
| Bot photo | your own picture: PNG or JPEG, at least 160 px, up to 512 KB; 640 px square looks best |

### Bot texts tab

Open each text by its key, one language at a time. Keep `{brand}` exactly as written (single braces: that one is
a placeholder of the bot, not of this template).

**`welcome`**, English:

```
👋 Welcome to <b>{brand}</b>!
Pick a plan, pay, and get your connection link in minutes.
Questions? Tap Support, we answer right here.
```

**`welcome`**, Persian:

```
👋 به <b>{brand}</b> خوش آمدید!
پلن را انتخاب کنید، پرداخت کنید و لینک اتصال را در چند دقیقه بگیرید.
سؤالی دارید؟ از دکمهٔ پشتیبانی بپرسید، همین‌جا جواب می‌دهیم.
```

**`profile.description`** (the text of an empty chat, at most 512 characters), English:

```
Buy a VPN plan, pay in the chat and get your connection link right away.

Renew, add traffic, top up your wallet and talk to support, all here.

Press Start to begin.
```

Persian:

```
سرویس VPN بخرید، همین‌جا در چت پرداخت کنید و لینک اتصال را همان لحظه بگیرید.

تمدید، حجم اضافه، شارژ کیف پول و گفت‌وگو با پشتیبانی، همه همین‌جا.

برای شروع، دکمهٔ «شروع» را بزنید.
```

**`profile.about`** (the short line on the bot's profile, at most 120 characters), English:

```
{{STORE_NAME}}: buy, renew and get VPN support right here in Telegram.
```

Persian:

```
{{STORE_NAME}}: خرید، تمدید و پشتیبانی VPN، همین‌جا در تلگرام.
```

The rest of the bot's texts are written already, in both languages. Change one only when your store needs different
words. The bot sets its description, short description and menu commands in Telegram by itself; the bot's *name*
is set once in @BotFather (`/setname`).

## 4. Before you open the store

- **Payment gateways** (owner): fill in the card number and holder, the USDT address (TRC20 and/or ERC20), the
  Stars rate, and a Zarinpal merchant ID if you use it. A method with no details is shown as "on, details missing".
- Make a purchase yourself with a small plan, from a second Telegram account, and check the service arrives.
- Take a backup once (`sudo bobres backup`) and keep the backup key away from the server: the install prints it once,
  and the `bobres` menu's "Backup key" shows it again. A backup cannot be read without it.
