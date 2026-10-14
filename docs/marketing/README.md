# Launch marketing material

The landing and pricing page lives in [`site/`](../../site/) (Persian at `/`, English at `/en/`). This folder holds
the words, the pictures' sources and the tools. Everything is in Persian (`*.fa.md`) and English (`*.en.md`).

| Material | Persian | English |
|---|---|---|
| Eight launch posts for a Telegram channel | [launch-posts.fa.md](launch-posts.fa.md) | [launch-posts.en.md](launch-posts.en.md) |
| Outreach texts for VPN operators | [outreach.fa.md](outreach.fa.md) | [outreach.en.md](outreach.en.md) |
| One-page operator pitch | [operator-pitch.fa.md](operator-pitch.fa.md) | [operator-pitch.en.md](operator-pitch.en.md) |
| Plans, price list and bot texts for the owner's own store | [store-plans.fa.md](store-plans.fa.md) | [store-plans.en.md](store-plans.en.md) |
| 14-day content calendar | [content-calendar.fa.md](content-calendar.fa.md) | [content-calendar.en.md](content-calendar.en.md) |
| Claims checklist: every public claim and its proof | [claims-checklist.fa.md](claims-checklist.fa.md) | [claims-checklist.en.md](claims-checklist.en.md) |

`images/{fa,en}/` are the JPG pictures for the posts (Telegram turns a dropped WebP into a sticker).

> **`v0.1.0` is released (2026-10-14), so the page can go public.** The page and every text show the install command
> and the tier numbers, and none of them says "coming soon". The license policy, the vendor's public key and
> `docs/pricing.md` are on `main` now (the tier numbers of the page match `docs/pricing.md`; check again if either
> changes), and they shipped with that release. What is still the owner's before selling (prices, the lawyer, the license
> server) is in [docs/launch-checklist.md](../launch-checklist.md). Posts and outreach wait for the owner's own
> checks (the placeholders, the prices, the lawyer).

## Rules the material follows
- Free core plus paid premium, a subscription per install: the Free, Standard and Pro **tiers** (in English "plans"
  are what an operator sells to customers), and a 30-day trial of everything but removing the BOBRES mark. **No price
  number anywhere:** the answer is "contact us", and for now the contact is the project's **public** issue tracker
  (`https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues/new`): the texts say to paste no tokens or private data there.
- No domain, company name, phone number, Telegram handle, customer count, testimonial, guarantee or installation
  time that does not exist. Every claim has a row in the claims checklist, with the file that proves it.
- The dashboard pictures come from a real install with demo data, the bot pictures are illustrations drawn from the
  bot's real messages. The pages and the posts say so.
- Nothing promises that selling VPN is legal anywhere: the page's footer and the pitch say operators answer for local
  law, Telegram's terms and their payment providers' rules. Zarinpal is always "optional" (unverified for VPN stores).
- In Persian: «سطح» for a tier, «لایسنس» for the license, «نمایندگان» for resellers, «سگ آبی» for the beaver.

## What the owner fills in
- `{{LINK}}` in the posts and texts: the page's address once Pages is on, or the repository until then.
- `{{CONTACT}}` in the pitch: the owner's private channel. Until there is one, the project's public issue tracker
  (`https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues/new`), said to be public. There is no Telegram handle for BOBRES yet.
- The store's own `{{STORE_NAME}}`, `{{SUPPORT_USERNAME}}`, `{{BOT_USERNAME}}`, `{{TERMS_URL}}`, `{{PRIVACY_URL}}` and
  every `{{PRICE_...}}` in [store-plans.en.md](store-plans.en.md). The braces must not stay in what you paste.
- The claims checklist's open row: row 63, the one **Owner decision** (where people reach you; the public issue tracker for now). All the
  others are Verified against `main`, tiers included (checked against `docs/pricing.md` and
  `internal/license/tiers.json`): re-check the tier rows when either changes. The prices, the lawyer and the license
  server are in [docs/launch-checklist.md](../launch-checklist.md).
- The Persian text needs a native speaker's review (as `docs/08-brand.md` already says of the bot's copy).
- Before the site is public: the legal pages (the footer has none yet) and, if you want one, a domain. The absolute
  URLs (Open Graph image, hreflang, sitemap) need no edit: the Pages workflow writes them from the site's address.

## Publishing the site
`.github/workflows/pages.yml` deploys `site/` on pushes to `main` that change it, and by hand. It changes no
setting: turn Pages on once (Settings → Pages → Source: GitHub Actions), and only after the note at the top. The
sources use relative URLs, so they work at `/<repository>/` and at a domain's root. The workflow copies `site/` to
`_site/` and, there, makes the link-preview image (`og:image`, `twitter:image`), the hreflang alternates and
`sitemap.xml` absolute and adds the `Sitemap:` line to `robots.txt`, all from the address `configure-pages` reports;
it fails if the markup no longer has the tags it rewrites. Crawlers read `robots.txt` only at a host's root, so under a
project path it is ignored; a custom domain fixes that.

## Tools (`tools/`, not shipped)
Node scripts with `puppeteer-core` and `sharp` (`npm install` in `tools/`; `node_modules` is ignored), Chrome and
ffmpeg. They read the repository and write into `site/assets/` and `images/`.

| Script | What it does |
|---|---|
| `seed-demo.py`, `lab-psql.sh` | **Lab only.** Fills the Docker lab (`deploy/lab`) with made-up customers, orders, receipts and tickets by SQL (no events: no Telegram message goes out). |
| `receipt.mjs`, `shots.mjs`, `convert-shots.mjs`, `lib.mjs` | Log in to the lab's dashboard with a one-time link and take the Persian and English screenshots, then make the WebP files. |
| `chat/render.mjs`, `convert-chat.mjs` | The Telegram-like chat renderer: the bot's real texts (`internal/bot/i18n/locales`) and button colors (`buttonStyles` in `meta.go`), and the conversion of its PNG files to the site's WebP. |
| `demo/make-demo.mjs` | The two short demo videos from those pictures (ffmpeg crossfades, no audio). Re-render them after any picture they use changes. |
| `make-brand-images.mjs` | The link-preview picture `site/assets/brand/og-image.jpg` (the mascot and the name, no tagline: right in both languages) and the launch pictures `images/{en,fa}/01-launch.jpg`, from `docs/brand/bobres-banner.webp`. |
| `make-post-images.mjs` | The other JPG pictures for the posts. |
| `strip-metadata.mjs` | Takes EXIF, XMP and text chunks (such as the credit line an image generator adds) out of PNG and JPEG files without re-encoding them. Run it on every image that comes from a generator. |

To redo the dashboard pictures: take `docker exec bobres-lab-bot bobres backup` (the file is in `/opt/bobres/backups`
there), seed (`seed-demo.py` takes the owner, the plan and the 3x-ui server ids, see its docstring), run `shots.mjs` and
`convert-shots.mjs` within ten minutes (after that the core's usage sync asks the panel about the made-up services), then
put the lab back with `docker exec bobres-lab-bot bobres restore /opt/bobres/backups/<that backup> --yes` and check
`bobres status` and that one user is back. The seed renames the owner and writes straight into the database: never run
it on a real store. The seed picks counts that read right in English (the dashboard has no plurals: a three-day
trial, two resellers, two servers). Do not keep the new picture of the Backups tab: after a restore its list shows
the before-restore file as "Not sent"; the committed one shows a backup sent to Telegram.
