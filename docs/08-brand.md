# 08 - Brand kit and copywriting (DRAFT)

Two layers, keep them separate:
- **Product brand: BOBRES** - what YOU sell to operators (installer, dashboard chrome, docs, license, website).
- **Operator brand** - what each customer's END USERS see in the bot. Fully configurable; BOBRES hidden when `white_label` entitlement is on.

Trademark/domain note: other GitHub projects already use "bobres". Check trademark and domain availability before public launch.
`bobres.io` looked free in a quick WHOIS check (unverified); `.com` and `.net` are taken.

## Product positioning (draft)
- One-liner EN: "Launch your own VPN store on Telegram in one command."
- One-liner FA (draft, needs native review): "فروشگاه VPN خودتان را با یک دستور روی تلگرام راه بیندازید."
- Promise: install in minutes, sell automatically, manage everything from one place, own your data and brand.
- Audience: VPN sellers/operators running 3x-ui, from solo sellers to resellers with agents.

## Voice and tone
- Clear, calm, confident, short sentences. No hype, no fear, no jargon without explanation.
- Friendly but professional; helpful errors ("Payment not confirmed yet. Wait 2 minutes or contact support.").
- Persian: natural modern Persian (not literal translation), formal-friendly register (شما), consistent terms; Latin brand names and technical words kept as-is.
- Emoji: sparing, functional (status), never decorative spam.

## Visual identity direction (draft)
- Name style: BOBRES in caps as wordmark; simple, sturdy.
- Palette idea: deep navy/ink base, one strong accent (electric teal or amber), neutral grays; dark and light modes.
- Type: Inter/Vazirmatn (Vazirmatn covers Persian well and pairs with Latin).
- Logo direction: simple geometric mark, works at 32px avatar size (bot profile picture) and on dark backgrounds.
- Default operator theme ships neutral; operators upload their own logo/colors.

## Copy deliverables (to write in Phase 1 with the bot, fa + en)
1. Bot: /start welcome, main menu labels, purchase flow, delivery message, connect guides, wallet, referral, trial, support, settings.
2. System messages: errors, empty states, rate-limit notice, maintenance, banned, panel down.
3. Notifications: expiry, quota, payment result, ticket reply.
4. Dashboard UI labels and help texts.
5. Installer/CLI output messages (clear, actionable).
6. Website/docs: landing page, install guide, admin guide, FAQ, changelog style.
7. Legal pages templates: Terms, Privacy, Refund policy, Acceptable use (drafts only; lawyer must review).
8. Copyright and licensing text: "© 2026 BOBRES. All rights reserved." Third-party notices file (Go deps, fonts).

## Copy sample (draft, not final)
- EN welcome: "Welcome to {brand}. Pick a plan, pay, and get your connection in seconds."
- FA welcome (draft): «به {brand} خوش آمدید. پلن انتخاب کنید، پرداخت کنید و در چند ثانیه متصل شوید.»
- EN payment pending: "We're waiting for your payment. This usually takes under 2 minutes."
- EN service ready: "Your service is ready. Tap the link or scan the QR code to connect."

## Name options (only if BOBRES is dropped) - none needed now.

## Open points
- Final logo/wordmark (design work, or generate with the brandkit skill later).
- Domain for vendor site/installer (`get.` subdomain) and docs.
- Persian copy must be reviewed by a native speaker before release.
