# Launch checklist: what is left before selling

The code side of selling is built: the license policy, the vendor key, the tier presets, the license server and its
deployment kit, the source license (a draft) and the legal drafts. What is left is **the owner's**: names, money, a lawyer, a server and the
first customers. Each step says who does it and how. Do them in this order; the first group blocks charging anyone.

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

## A. Before you charge anyone

| # | Step | Who | How |
|---|---|---|---|
| 1 | **Back up the private key.** Two offline copies, in different places | Owner | Copy `~/bobres-vendor-keys/vendor-v1-2026.key` to an encrypted USB stick and to a password manager attachment. Write the key id and the fingerprint beside each. Read `README.txt` in that folder. Never email it, never put it in the repository, a chat or a cloud note. If every copy is lost the key must be rotated (runbook, section 6) |
| 2 | **Choose the sales name.** The domain | Owner | Pick the domain for the product (and the license server's name, for example `license.<domain>`). Check it is free at a registrar and buy it. Point DNS where it is needed later (steps 10 and 14) |
| 3 | **Check the handles.** A Telegram `@username` for the product, and a GitHub organization | Owner | In Telegram, search the name and ask @BotFather for `/newbot` to see if it is taken (a bot name must end in `bot`); a channel or support username is a separate check. On GitHub, open `github.com/<name>` to see if the organization exists. The repository is under the owner's account now; moving it to an organization changes the installer's download address, so do it before the first release |
| 4 | **Search for trademark conflicts** on BOBRES | Owner, then the lawyer | Search the name (and the beaver logo) in the trademark databases of the countries where you sell: the national office, WIPO Global Brand Database, EUIPO TMview. Look at the software and online-services classes (9, 42, and 35 for the store). Bring what you find to the lawyer (docs/09-legal-questions.md, question 8) before registering |
| 5 | **Lawyer review** of [LICENSE](../LICENSE), [docs/legal](legal/README.md) and the [notices](../THIRD_PARTY_NOTICES.md) | Owner hires, lawyer reviews | Give the lawyer [09-legal-questions.md](09-legal-questions.md) and these files. List what they must settle: `git grep -n "\[LAWYER:" -- LICENSE docs`. Ask especially about: which entity sells, where; the VPN business; sanctions and payment channels for license fees; the refund rules; the source license wording; the Redis image license and the GPL parts of the backup image |
| 6 | **Fill in the owner's placeholders** | Owner | List them: `git grep -n "\[OWNER:" -- LICENSE docs`. Your legal name and contacts, the dates, where prices are published, how customers pay and renew, the refund numbers, the support scope and the response times, the data retention periods. Then put the final texts on the website |
| 7 | **Set the prices.** Replace every `SET ME` | Owner | Edit [`internal/license/tiers.json`](../internal/license/tiers.json): currency, monthly and yearly price of Standard and Pro (plain numbers as text). Check with `go run ./tools/license tiers`. Copy the numbers into the table of [pricing.md](pricing.md). The prices are not read by any program, so nothing else changes |
| 8 | **Set your seller contact** | Owner | `BOBRES_SELLER_CONTACT` (a Telegram handle or an address): the dashboard's License tab, `bobres license status` and the API show it as whom to ask to buy or renew a license. Set it on your own store with `sudo bobres config set BOBRES_SELLER_CONTACT @your_handle`, and set the build-time default `license.DefaultSellerContact` (`internal/license/policy.go`) before the first release, so that every install says where to buy. Until you do, the dashboard says "ask the seller you got BOBRES from" |
| 9 | **Decide how you take payment** for licenses | Owner, with the lawyer | Pick the channel or channels (bank, card, crypto, an invoice). Which ones are lawful and sensible for you depends on where you are (09-legal-questions.md, A3 to A5). Write it in the Terms and the Refund policy, and keep a sheet of your own: customer, license id, tier, paid until, payment reference |
| 10 | **Deploy the license server** | Owner (or an AI session on the owner's VPS) | Runbook, section 1: a VPS, the DNS record, the key file, `deploy/vendor/.env`, `docker compose up -d --build`, and the checks from outside |
| 11 | **Issue your own license**, then set it on your own store | Owner | `lic create -customer "Own store" -tier pro -no-end`; then on the store's server `sudo bobres config set BOBRES_LICENSE_SERVER https://license.<domain>` and `sudo bobres license activate`. The dashboard's License tab must say Active, and `bobres license status` says "renewed online" a day later |
| 12 | **Tune the free tier numbers**, if you want a different free offer | Owner | `license.FreeEntitlements()` in `internal/license/policy.go` (the constants `FreeMaxServers`, `FreeMaxActiveServices`, `FreeStaffSeats`: now 1 server, 25 active services, 2 staff seats). They apply to **every install, your own store included**: issue your own Pro license first (step 11), or your own store meets the free tier too. When you change a number, change it in [pricing.md](pricing.md), [05-license.md](05-license.md) and the legal drafts that print it (`git grep -n "25 active services"`), and in the tests that pin it |
| 13 | **Publish a release** that carries the key and the presets | Owner decides, then an AI session | The first public release, v0.1.0, comes after the first install on a real server ([11-staging-run.md](11-staging-run.md)). Check on a fresh install that `bobres license status` knows one key. Moving the repository to an organization (step 3) comes first |

## B. To launch

| # | Step | Who | How |
|---|---|---|---|
| 14 | **Publish the landing page** | Owner (an AI session can build it) | One page: what BOBRES is, the pricing table, how to install, how to buy (your contact), the Terms, the Privacy Policy, the Acceptable Use Policy, the Refund policy, and the **fingerprint** of the vendor key so that people can check it. Any static host will do. Link the page from the README |
| 15 | **Write `SECURITY.md`** with a contact for disclosures, and set up the support, abuse and security contacts that the legal texts name | Owner | A mailbox or Telegram account that you read. The texts say `[OWNER: ...]` where they need it |
| 16 | **Dry-run the whole sales flow** with a friend | Owner | Issue a trial (`lic create -tier trial`), let them install and activate it, renew it with `-extend-months`, let one lapse and watch it fall back to the free tier (nothing is deleted; existing services keep working, and a new purchase beyond 25 active services is refused with "temporarily unavailable"), revoke one. Fix the wording of your message to customers |
| 17 | **First customers** | Owner | Start with a few operators you know. Give each a trial first, then a paid license. Follow the sales flow in the runbook, section 2. Ask each what was unclear |

## C. After the launch

| # | Step | Who | How |
|---|---|---|---|
| 18 | **Weekly:** `lic list`; **monthly:** renewals | Owner | Runbook, sections 2 and 3: ENDS within 7 days, LAST SEEN older than 3 days |
| 19 | **Test the license database restore** every quarter | Owner | Runbook, section 5, on a spare machine or the same VPS: a dump you cannot restore is not a backup |
| 20 | **Renew the release key list** before it expires (365 days from 2026-09-30) | Owner | `tools/sign/README.md`: build and sign a new `keys.txt` with a higher `--seq`, with the two root keys |
| 21 | **Rotate the signing key** about every two years, and the day you think it leaked | Owner | Runbook, sections 6 and 7 |
| 22 | **Regenerate the third-party notices** when a dependency changes | An AI session | The test `tools/ci/notices` fails when the file is out of date; its last section says how to update it |
| 23 | **Review the legal texts** when the business changes: a new country, a new payment channel, a new tier | Owner, lawyer | Change the English text first, then the Persian one next to it |
