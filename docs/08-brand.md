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

## Visual identity (decided 2026-10-12)
The brand is **BOBRES the beaver**. The mascot is the whole logo and every branding item: the icon, the bot
avatar, the banner, the 3D pictures and the motion are all him. There is no separate geometric mark.

### Who he is
- A friendly, sturdy, trustworthy builder: beavers build and keep up what they build, as BOBRES builds and runs
  a store for its operator.
- The glowing teal shield with a keyhole stands for security and privacy.
- Warm and confident, never goofy, sly or aggressive; he matches the voice above (calm, clear, helpful).
- Look: Pixar-like 3D, warm brown fur, cream muzzle, two big front teeth, a small teal scarf, on dark navy with a
  soft teal rim light. The flat icon is his head and scarf in a teal circle.
- Name style: BOBRES in caps as the wordmark. Type: Inter/Vazirmatn (Vazirmatn covers Persian well and pairs
  with Latin).

### Palette
| Role | Colour | Hex |
|---|---|---|
| Base: backgrounds, dark mode | Navy | `#0F172A` |
| Accent: scarf, shield, the dashboard's default primary | Teal | `#14B8A6` |
| Fur | Beaver brown | `#8B5A2B` |
| Muzzle, highlights | Cream | `#F5DEB3` |

Teal is the dashboard's default colour (Aura teal 500, `DEFAULT_COLOR` in `web/dashboard/src/branding.ts`) and
the dark theme's surfaces are navy, so the mascot sits on them without a frame. Neutral greys (Tailwind slate)
do the rest; dark and light modes both.

### Assets
| File | What | Used in |
|---|---|---|
| `docs/brand/bobres-icon.svg`, same file as `web/dashboard/public/favicon.svg` | Flat vector icon: head and scarf in a teal circle, transparent corners | Browser tab icon; the dashboard's logo when the store has none (sidebar, login card); the 16px icon before "Powered by BOBRES" |
| `web/dashboard/public/apple-touch-icon.png` | The icon on navy, 180x180 PNG | The dashboard's home-screen icon on phones (the store's logo replaces it when one is uploaded) |
| `docs/brand/bobres-mascot.webp`, same file as `web/dashboard/src/assets/brand/bobres-mascot.webp` | The master 3D render, full body, 800x800, on navy | Login page with the default look, as the still picture for reduced motion; the source for every edit |
| `docs/brand/bobres-wave.webp`, same file as `web/dashboard/src/assets/brand/bobres-wave.webp` | Looping animated WebP of him waving, 420x420, 15 fps, about 350 KB | Login page with the default look (above the card, with a slow float); README ("Meet BOBRES") |
| `docs/brand/bot-avatar.jpg` | Round 3D close-up portrait, 640x640 | Telegram profile photo of a bot, set with @BotFather (below) |
| `docs/brand/bobres-banner.webp` | 1376x768 banner: the mascot and "BOBRES, Your VPN store on Telegram" | Top of the README; announcements and the vendor site |

The dashboard copies are what the build ships: Vite hashes `src/assets` into `/admin/assets/` and copies
`public/` as is, so everything loads from the dashboard's own origin under its CSP (images from `'self'` and
`data:` only). Never point the dashboard at an image on another site.

### Usage rules
- **Product brand only.** The beaver is BOBRES, the product sold to operators: the installer and CLI, the
  dashboard's chrome and default look, the docs, the license, the vendor's site and bots. He never speaks for
  an operator's store.
- **Operators' own branding wins.** A store that uploads its logo gets it in the dashboard (sidebar, login card,
  tab and home-screen icons) and the mascot leaves the login page; the bot's name, texts and photo are the
  operator's. "Powered by BOBRES" shows only under a valid license without `white_label` (see 05-license.md).
- **Clear space:** keep a margin of at least a quarter of the picture's width free on every side of the icon
  and the mascot: no text, edges or other logos inside it.
- **Never stretch** or squash him; scale proportionally only. Do not rotate, recolour, outline, crop the icon's
  circle, or put the flat icon on a busy background.
- **Sizes:** the flat icon works down to 16px; below about 96px use the icon rather than the 3D render, whose
  details get lost.
- **Motion** only where it is welcome: with `prefers-reduced-motion` the still render replaces the wave and the
  float stops.
- **Stay on model:** new pictures are edits of the master render (below), so his colours, scarf, shield and
  teeth stay the same. Put text in afterwards with a design tool; generators misspell.

### Regenerating or extending the assets
All were made with fal.ai. Keep the master render as the reference image of every edit.

1. **Master 3D render** (`bobres-mascot.webp`): `nano-banana-pro`, text to image, square, with this prompt:
   > 3D render of BOBRES, a cute and confident cartoon beaver mascot, Pixar-quality, soft studio lighting, warm
   > brown fur with fine detail, cream muzzle, two big front teeth, friendly smile, wearing a small teal scarf,
   > holding a glowing teal shield with a keyhole symbol, standing on a smooth dark navy background with a soft
   > teal rim light, centered, full body, clean product-mascot look, no text
2. **Flat icon** (`bobres-icon.svg`): a `nano-banana-pro` edit of the master render into a flat vector app icon
   of the head and scarf in a teal circle, then recraft vectorize to SVG. `apple-touch-icon.png` is that icon
   at 180x180.
3. **Bot avatar** (`bot-avatar.jpg`): a `nano-banana-pro` edit of the master render into a close-up portrait
   for a round profile picture, 640x640.
4. **Wave** (`bobres-wave.webp`): seedance 2.5 image to video from the master render, with the start frame
   the same as the end frame so it loops without a seam; then a looping animated WebP at 420px and 15 fps, for
   example `ffmpeg -i wave.mp4 -vf "fps=15,scale=420:-1:flags=lanczos" -loop 0 -c:v libwebp -q:v 70 -an bobres-wave.webp`.
   Keep it under about 400 KB: the login page loads it.
5. **Banner** (`bobres-banner.webp`): a `nano-banana-pro` edit of the master render into a wide 1376x768 banner
   with the wordmark and the tagline.

A new pose (holding a key, a server, a phone) is one more edit of the master render with the same wording for
his look. After changing the icon, the mascot or the wave in `docs/brand/`, copy it over its dashboard twin
(table above) so the two stay the same.

### Telegram bot profile photo
The Bot API has no method for a bot to set its own profile photo, so neither the installer nor the dashboard
can set it. Set it once by hand:
1. In Telegram, open @BotFather from the account that created the bot.
2. Send `/setuserpic` and choose the bot.
3. Send the picture as a photo: the store's own logo (square; Telegram shows it in a circle),
   or `docs/brand/bot-avatar.jpg` for the BOBRES beaver.

The `bobres install` summary suggests it among the first steps.

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
- Domain for vendor site/installer (`get.` subdomain) and docs.
- Persian copy must be reviewed by a native speaker before release.
