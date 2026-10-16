# Launch checklist: what is left before selling

Persian version: [launch-checklist.fa.md](launch-checklist.fa.md) (same numbers).

The code side of selling is built: the license policy, the vendor key, the tier presets, the license server and its
deployment kit, the source license (a draft), the legal drafts, the landing page and the launch texts. What is left is
**the owner's**: names, money, a lawyer, a server and the first customers. Each step says who does it and how. Do them
in this order; the first group blocks charging anyone. The State column was checked against the repository and GitHub
on 2026-10-14.

Done already (2026-10-14), so you do not repeat it:

- The vendor signing key `vendor-v1-2026` exists. Its private half is in `~/bobres-vendor-keys/` on your Mac
  (outside the repository). Its public half is built into BOBRES. Fingerprint: `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7`.
- The license policy is built ([05-license.md](05-license.md)): a free tier (1 server, 25 active services, 2 staff seats, no
  update limit), a 30-day trial for every install, and paid tiers that unlock more. Updates are never withheld.
- The tiers are in [`internal/license/tiers.json`](../internal/license/tiers.json), with `SET ME` for every price.
- The license server is built, with its deployment kit in `deploy/vendor/` and its runbook
  [runbooks/vendor-license-server.md](runbooks/vendor-license-server.md). It is **not deployed**: that is step 10.
- The [LICENSE](../LICENSE), the [third-party notices](../THIRD_PARTY_NOTICES.md) and the [legal drafts](legal/README.md) are
  written as drafts.
- Three releases are published, signed and carry the vendor key: v0.1.0 (the first), v0.1.1 and v0.2.0 (sales
  reports, the latest). Each was installed or updated in the Docker lab ([11-staging-run.md](11-staging-run.md)).
- The landing and pricing page is live, in Persian and English: https://sobhanaz.github.io/bobres-3x-ui-telegram-panel/
  (source in `site/`). The launch posts, outreach texts and a demo are in [marketing/](marketing/README.md).
- The repository has a website link and topics, GitHub's private vulnerability reporting is on, a
  [SECURITY.md](../SECURITY.md) points to it, and the `main` branch is protected (a pull request and a green CI are
  needed). The roadmap is in [ROADMAP.md](ROADMAP.md).

## Blockers at a glance

Nothing in the code stops a sale; these things do, and they are the owner's. Some are decided in the repository and
left on purpose until you decide: the `SET ME` prices in `internal/license/tiers.json` (step 7), the empty seller
contact (step 8), the `[LAWYER: ...]` and `[OWNER: ...]` placeholders (steps 5 and 6), the license server that is not
deployed (step 10), the backups of the vendor private key (step 1), the release-signature deadline **2026-11-28**
(step 24), and everything that has only been tried with fakes and the Docker lab (steps 30 to 37). Run `bobres check`
on a store to see the install-side half of this list.

## A. Before you charge anyone

| # | Step | State | Who | How |
|---|---|---|---|---|
| 1 | **Back up the private key.** Two offline copies, in different places | Open | Owner | Copy `~/bobres-vendor-keys/vendor-v1-2026.key` to an encrypted USB stick and to a password manager attachment. Write the key id and the fingerprint beside each. Read `README.txt` in that folder. Never email it, never put it in the repository, a chat or a cloud note. If every copy is lost the key must be rotated (runbook, section 6) |
| 2 | **Choose the sales name.** The domain | Open | Owner | Pick the domain for the product (and the license server's name, for example `license.<domain>`). Check it is free at a registrar and buy it. Point DNS where it is needed later (steps 10 and 14) |
| 3 | **Check the handles.** A Telegram `@username` for the product, and a GitHub organization | Open | Owner | In Telegram, search the name and ask @BotFather for `/newbot` to see if it is taken (a bot username must end in `bot`; if it is free, `/newbot` creates the bot on the spot and gives you its token); a channel or support username is a separate check. On GitHub, open `github.com/<name>` to see if the organization exists. The repository is under the owner's account (`sobhanaz`), and that name is already in the install command, the image names and the site address of v0.1.0 to v0.2.0. Moving to an organization is still possible (GitHub redirects the repository), but the installer, the image names and the site address must then change in one release, so decide early |
| 4 | **Search for trademark conflicts** on BOBRES | Open | Owner, then the lawyer | Search the name (and the beaver logo) in the trademark databases of the countries where you sell: the national office, WIPO Global Brand Database, EUIPO TMview. Look at the software and online-services classes (9, 42, and 35 for the store). Bring what you find to the lawyer (docs/09-legal-questions.md, question 8) before registering |
| 5 | **Lawyer review** of [LICENSE](../LICENSE), [docs/legal](legal/README.md) and the [notices](../THIRD_PARTY_NOTICES.md) | Open | Owner hires, lawyer reviews | Give the lawyer [09-legal-questions.md](09-legal-questions.md) and these files. List what they must settle: `git grep -n "\[LAWYER:" -- LICENSE docs`. Ask especially about: which entity sells, where; the VPN business; sanctions and payment channels for license fees; the refund rules; the source license wording; the Redis image license and the GPL parts of the backup image. The draft LICENSE has been public on `main` since 2026-10-14, and it is in v0.1.0 |
| 6 | **Fill in the owner's placeholders** | Open | Owner | List them: `git grep -n "\[OWNER:" -- LICENSE docs`. Your legal name and contacts, the dates, where prices are published, how customers pay and renew, the refund numbers, the support scope and the response times, the data retention periods. Then put the final texts on the website (the site has no legal pages yet) |
| 7 | **Set the prices.** Replace every `SET ME` | Open | Owner | Edit [`internal/license/tiers.json`](../internal/license/tiers.json): currency, monthly and yearly price of Standard and Pro (plain numbers as text). Check with `go run ./tools/license tiers`. Copy the numbers into the table of [pricing.md](pricing.md), including its Support row. No program reads the prices. The landing page (`site/`) and the marketing texts say "contact us" today, so after you set prices, put the numbers into `site/index.html`, `site/en/index.html` and the files in `docs/marketing/` too (see `docs/marketing/claims-checklist.en.md`, row 63 and the pricing rows) |
| 8 | **Set your seller contact** | Open | Owner | `BOBRES_SELLER_CONTACT` (a Telegram handle or an address): the dashboard's License tab, `bobres license status` and the API show it as whom to ask to buy or renew a license. Set it on your own store with `sudo bobres config set BOBRES_SELLER_CONTACT @your_handle`, and set the build-time default `license.DefaultSellerContact` (`internal/license/policy.go`, empty today) in the next release, so that every new install says where to buy. v0.1.0 to v0.2.0 shipped with it empty: the dashboard says "ask the seller you got BOBRES from" |
| 9 | **Decide how you take payment** for licenses | Open | Owner, with the lawyer | Pick the channel or channels (bank, card, crypto, an invoice). Which ones are lawful and sensible for you depends on where you are (09-legal-questions.md, A3 to A5). Write it in the Terms and the Refund policy, and keep a sheet of your own: customer, license id, tier, paid until, payment reference |
| 10 | **Deploy the license server** | Open | Owner (or an AI session on the owner's VPS) | Runbook, section 1: a VPS, the DNS record, the key file, `deploy/vendor/.env`, `docker compose up -d --build`, and the checks from outside |
| 11 | **Issue your own license**, then set it on your own store | Open | Owner | `lic create -customer "Own store" -tier pro -no-end`; then on the store's server `sudo bobres config set BOBRES_LICENSE_SERVER https://license.<domain>` and `sudo bobres license activate`. The dashboard's License tab must say Active, and `bobres license status` says "renewed online" a day later |
| 12 | **Tune the free tier numbers**, if you want a different free offer | Optional | Owner | `license.FreeEntitlements()` in `internal/license/policy.go` (the constants `FreeMaxServers`, `FreeMaxActiveServices`, `FreeStaffSeats`: now 1 server, 25 active services, 2 staff seats). They apply to **every install, your own store included**: issue your own Pro license first (step 11), or your own store meets the free tier too. When you change a number, change it everywhere it is printed: [pricing.md](pricing.md), [05-license.md](05-license.md), the legal drafts, the landing page (`site/`), the launch texts (`docs/marketing/`), the dashboard and bot texts (`web/dashboard/src/locales/`, `internal/bot/i18n/`) and the tests that pin it. Search for the number in both digit styles: `git grep -n "25 active services"` misses the Persian texts, which print ۲۵ |
| 13 | **Publish a release** that carries the key and the presets | Done | Owner decides, then an AI session | Done 2026-10-14: v0.1.0, v0.1.1 and v0.2.0 are published and carry the vendor key (`vendor-v1-2026`) and the tier presets. They followed the staging run in the Docker lab, not an install on a real server: that check is step 30. Every release must be re-signed or followed by a new one within 45 days (step 24) |

## B. To launch

| # | Step | State | Who | How |
|---|---|---|---|---|
| 14 | **Publish the landing page** | Partly | Owner (an AI session can build it) | Done: it is live at https://sobhanaz.github.io/bobres-3x-ui-telegram-panel/ (Persian at `/`, English at `/en/`, source in `site/`, deployed by GitHub Pages) with what BOBRES is, the tier table (no prices, "contact us"), the install command and a contact through the public issue tracker. The **fingerprint** of the vendor key is on both pages (checked against `bobres about`). The Terms, the Privacy Policy, the Acceptable Use Policy and the Refund policy are built as draft pages in `site/legal/` (written by `go run ./tools/sitelegal` from `docs/legal`; a test fails if they drift): marked as drafts, `noindex`, not in the sitemap and not linked from the landing page. After the lawyer's review and the owner's fill-in of every `[OWNER: ...]`, remove the banner and `noindex`, add them to the sitemap and link them from the footer. The README already names the page in its Status paragraph |
| 15 | **Write `SECURITY.md`** with a contact for disclosures, and set up the support, abuse and security contacts that the legal texts name | Partly | Owner | Done: [SECURITY.md](../SECURITY.md) exists and points to GitHub's private vulnerability reporting, which is on. Still yours: a mailbox or Telegram account that you read for support and abuse. The legal texts say `[OWNER: ...]` where they need it |
| 16 | **Dry-run the whole sales flow** with a friend | Open | Owner | Issue a trial (`lic create -tier trial`), let them install and activate it, renew it with `-extend-months`, let one lapse and watch it fall back to the free tier (nothing is deleted; existing services keep working, and a new purchase beyond 25 active services is refused with "temporarily unavailable"), revoke one. Fix the wording of your message to customers |
| 17 | **First customers** | Open | Owner | Start with a few operators you know. Give each a trial first, then a paid license. Follow the sales flow in the runbook, section 2. Ask each what was unclear. The texts to send are in [marketing/](marketing/README.md); fill their `{{...}}` placeholders first |

## C. After the launch

| # | Step | State | Who | How |
|---|---|---|---|---|
| 18 | **Weekly:** `lic list`; **monthly:** renewals | Open | Owner | Runbook, sections 2 and 3: ENDS within 7 days, LAST SEEN older than 3 days |
| 19 | **Test the license database restore** every quarter | Open | Owner | Runbook, section 5, on a spare machine or the same VPS: a dump you cannot restore is not a backup |
| 20 | **Renew the release key list** before it expires (2027-09-30) | Open | Owner | `tools/sign/README.md`: build and sign a new `keys.txt` with a higher `--seq`, with the two root keys. Do it a month ahead |
| 21 | **Rotate the signing key** about every two years (around 2028-10), and the day you think it leaked | Open | Owner | Runbook, sections 6 and 7 |
| 22 | **Regenerate the third-party notices** when a dependency changes | Done (current) | An AI session | The test `tools/ci/notices` fails when the file is out of date; its last section says how to update it |
| 23 | **Review the legal texts** when the business changes: a new country, a new payment channel, a new tier | Open | Owner, lawyer | Change the English text first, then the Persian one next to it |

## D. Also yours, found after the launch

| # | Step | State | Who | How |
|---|---|---|---|---|
| 24 | **Release signatures expire after 45 days.** A scheduled workflow re-signs the latest release for you; check that it keeps running. **The deadline without it was 2026-11-28** | Automatic, check | Owner (check), the workflow | `.github/workflows/resign.yml` runs on the 1st and 16th of each month and re-signs the latest stable release when fewer than 25 days are left, with the release key that is a GitHub secret (`RELEASE_SIGNING_KEY`). GitHub emails you if a run fails; look at the Actions tab, "Re-sign the latest release", about the 16th of each month. A calendar reminder on 2026-11-18 is the safety net. Without a recent release or re-sign, `install.sh` and `bobres update` refuse once a signature expires (`docs/runbooks/releases.md`, "Signatures expire after 45 days"). GitHub pauses scheduled workflows after 60 days with no activity in the repository |
| 25 | **Back up the release signing keys too**: the two offline root keys and the release key `rel-2026a` | Open | Owner | The release key is in `~/.bobres-secrets` on your Mac (created 2026-09-30), and a copy is the repository secret `RELEASE_SIGNING_KEY`, which GitHub never shows again, so the Mac copy is the only one you can read. `tools/sign/README.md` says to keep the two root keys in separate places. Step 1 covers only the vendor license key |
| 26 | **Revoke the lab's test Telegram bot token** in @BotFather (or keep it on purpose as a test bot) | Open | Owner | Only you can do it: @BotFather, `/mybots`, the test bot, API Token, Revoke current token. The lab uses it only for testing; nothing in the repository holds it |
| 27 | **Send the launch posts and outreach texts**, after you fill their placeholders | Open | Owner | [marketing/](marketing/README.md): `{{LINK}}`, `{{STORE_NAME}}`, `{{CONTACT}}`, `{{SUPPORT_USERNAME}}`, `{{BOT_USERNAME}}` and others. The braces must not stay in what you send. Nothing was sent for you |
| 28 | **Replace the public issue tracker** as the contact on the site | Open | Owner | The site and the pitch send people to GitHub's public issue tracker, where everyone reads what they write. Use a private Telegram handle or a mailbox once you have one (`site/index.html`, `site/en/index.html`, `{{CONTACT}}`; row 63 of `docs/marketing/claims-checklist.en.md`) |
| 29 | **Have a native Persian speaker review every text** | Open | Owner | The bot, the dashboard (including the new Reports texts, `docs/14-reports.md`), the site and the marketing texts. Edits are merged by an AI session |
| 30 | **Install once on a real public server** with a real 3x-ui panel | Open | Owner | The Docker lab cannot show these: a VPN app connecting through a service the bot sold, the QR code, real Let's Encrypt issuance for the domain and for the server IP address (needs ports 80 and 443 reachable from the internet), `bobres update` and `bobres rollback` on real data ([ROADMAP.md](ROADMAP.md), the v0.1.x table) |
| 31 | **Try real payments**, one purchase per method | Open | Owner | A Zarinpal merchant id, Telegram Stars, card to card and automatic USDT: buy once with each, check the receipt flow and that the service is delivered ([ROADMAP.md](ROADMAP.md)) |
| 32 | **Look at the colored buttons and the bottom menu in the real Telegram app** (iOS, Android, Desktop) | Open | Owner | The lab cannot show them ([03-bot-ux.md](03-bot-ux.md)); the chat pictures on the site come from a renderer, not from Telegram |
| 33 | **Try IDPay and NextPay** with real merchant accounts | Open | Owner | Built to the public documents and tested only with fakes ([16-idpay-nextpay.md](16-idpay-nextpay.md)). Put in the key, pay once, abandon one payment, refund one; each gateway stays off until its key is set |
| 34 | **Open the Telegram Mini App on a phone and on Desktop** | Open | Owner | Turn on `miniapp.enabled`, set the web app URL in @BotFather ([17-mini-app.md](17-mini-app.md)); check sign-in, a wallet purchase, a gateway payment, a renewal, Persian layout and the Back button. Never run against a real Telegram yet |
| 35 | **Walk through the owner menu in the bot** (`/admin`) | Open | Owner | Every wizard (plans, discounts, trial, top-ups, settings, servers, staff, broadcast) was tested with fakes only ([03-bot-ux.md](03-bot-ux.md)). Run each once on a test store, and read the explanations in Persian |
| 36 | **Log in with the initial password and read the status screen** | Open | Owner | On a fresh install: `bobres install` prints the one-time password for `owner`; log in, set a new password and 2FA. Then `bobres` (status screen) and `bobres check` must agree with what you see ([06-installer.md](06-installer.md)). On a server installed before this change, the release-signature check of `bobres check` shows SKIP until the next `bobres update` |
| 37 | **Check the subscription page links on real phones** | Open | Owner | The app deep links on the page ([15-subscription-page.md](15-subscription-page.md)) were not tried on iOS and Android |
