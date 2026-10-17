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
| `docs/brand/bot-avatar.jpg` | Round 3D close-up portrait, 640x640 | Telegram profile photo of a bot, uploaded in the dashboard (below) |
| `docs/brand/bot-description.jpg` | The banner cut to 640x360 | A bot's description picture, above its description in an empty chat, set with @BotFather (below) |
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
  operator's. "Powered by BOBRES" shows on every install except one whose valid license includes `white_label` (the free tier, the
  trial and a license without it show it; see 05-license.md).
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

### Telegram bot profile
**What the bot sets itself.** At start and after every change of its texts, the branding or the staff, the
bot sets in Telegram, in Persian and English (Persian also for every other language):
- its **menu commands**: `/start`, `/menu`, `/paysupport`, and `/terms` when the store links its terms or
  privacy policy; owners and admins also get `/admin` and `/dashboard` in their own chat, support staff
  `/dashboard`;
- its **description**, the text of an empty chat with the bot ("What can this bot do?");
- its **short description** ("about"), on its profile page and in the preview when it is shared;
- its **profile photo**, uploaded in the dashboard (**Branding & texts → Store → Telegram bot**).

The words are bot texts (dashboard: **Branding & texts → Bot texts → Bot profile**, keys `profile.*`), so a store
writes its own like any other text. It only sets what changed, and remembers what it set across restarts.
**Settings → General → Keep the bot's Telegram profile up to date** (`general.bot_profile`) turns it off for an
operator who prefers to set them in @BotFather: the bot then takes back its Persian and English values and the
staff menus, which would hide @BotFather's (@BotFather sets one value for every language), and leaves that value
for every language as it is, to be edited there. The photo stays as it is too.

**The profile photo.** Upload a PNG or JPEG of at most 512 KB, 160 to 2048 pixels on each side (WebP cannot be
converted). Core stores it as the square JPEG Telegram takes: a JPEG's EXIF orientation is applied (a phone's
picture comes out upright), a picture that is not square is centred on white, and transparency becomes white.
Telegram shows it in a circle; 640x640 looks best (`docs/brand/bot-avatar.jpg` for the BOBRES beaver). The bot
uploads it (Bot API `setMyProfilePhoto`) only when it changes (uploading the same picture again changes
nothing), and once Telegram refuses one for good it tries that photo again only after an hour. Right after
setting it, the bot reads back which photo is now its current one (`getUserProfilePhotos`). Removing the photo
in the dashboard makes the bot take off the photo it set, but only while that photo is still the current one: a
photo set later in @BotFather stays, and so does the photo when Telegram cannot say which is current. While
**Keep the bot's Telegram profile up to date** is off, nothing changes in Telegram, whatever is uploaded or
removed here. A local Bot API server (`BOBRES_TELEGRAM_API_URL`) must be version 9.4 or newer for this.

**The default photo.** Until you set your own, the bot's photo is the BOBRES logo (`docs/brand/bot-avatar.jpg`,
embedded in core as `internal/branding/assets/bot-avatar.jpg`). Core stores it once, at its first start after
the database is migrated, and only when no bot photo exists yet: a photo you uploaded before is never touched. To
replace it, upload yours in the dashboard (**Branding & texts → Store → Telegram bot**); to have none, remove the
photo there. Core remembers that it offered the default (the internal marker `state.bot_photo.default_applied`,
which is not a setting and is not shown in the dashboard), so a photo you replaced or removed does not come back
at a restart or an update. With **Keep the bot's Telegram profile up to date** off, the default is stored but
never sent to Telegram.

**What to set in @BotFather** (BOBRES leaves these alone), once, from the account that created
the bot:
1. **Name:** `/setname` (the store's name; `branding.name` is what the bot's messages call the store).
2. **Description picture** (optional): `/mybots` → the bot → **Edit Bot** → **Edit Description Picture**, then
   send a 640x360 picture: the store's own, or `docs/brand/bot-description.jpg`.
3. **Telegram Stars:** nothing to switch on; the bot answers `/paysupport`, which Telegram asks of bots that sell
   with Stars.

Leave inline mode, groups, Mini Apps, the login widget, games and @BotFather's Payments (a third-party
provider token) as they are: the bot does not use them. Telegram's **Serverless** hosting (@BotFather → Serverless)
cannot run BOBRES: it runs JavaScript handlers per update in Telegram's sandbox, with a per-bot SQLite database
and no always-on processes, while the bot is one Go service of a self-hosted stack (core, payments,
provisioner, Postgres, Redis) with workers that run all the time (event feed, broadcasts, usage sync, payment
watchers) and that holds the store's 3x-ui tokens and payment details.

The `bobres install` summary points to the profile photo upload among the first steps.

## Copy deliverables (to write in Phase 1 with the bot, fa + en)
1. Bot: /start welcome, main menu labels, purchase flow, delivery message, connect guides, wallet, referral, trial, support, settings.
2. System messages: errors, empty states, rate-limit notice, maintenance, banned, panel down.
3. Notifications: expiry, quota, payment result, ticket reply.
4. Dashboard UI labels and help texts.
5. Installer/CLI output messages (clear, actionable).
6. Website/docs: landing page, install guide, admin guide, FAQ, changelog style.
7. Legal pages templates: Terms, Privacy, Refund policy, Acceptable use (drafts only; lawyer must review).
8. Copyright and licensing text: "© 2026 BOBRES. All rights reserved." Third-party notices file (Go deps, fonts).

Status (2026-10-12): 1 to 5 are written. The bot's texts exist in fa and en and every one can be changed on the
dashboard; the dashboard and the CLI have their own. 6: the docs in `docs/` and CHANGELOG.md; no landing page yet. 7
and 8 are not written: they wait for the legal review. The Persian texts still need a native speaker's review.

## Copy sample (draft, not final)
- EN welcome: "Welcome to {brand}. Pick a plan, pay, and get your connection in seconds."
- FA welcome (draft): «به {brand} خوش آمدید. پلن انتخاب کنید، پرداخت کنید و در چند ثانیه متصل شوید.»
- EN payment pending: "We're waiting for your payment. This usually takes under 2 minutes."
- EN service ready: "Your service is ready. Tap the link or scan the QR code to connect."

## Name options (only if BOBRES is dropped) - none needed now.

## Open points
- Domain for vendor site/installer (`get.` subdomain) and docs.
- Persian copy must be reviewed by a native speaker before release.
