# Changelog

All notable changes to BOBRES are written here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

What comes next is in [docs/ROADMAP.md](docs/ROADMAP.md).

## [0.16.0] — 2026-10-23

**Digital accounts in the Mini App, and the rate behind a USDT amount.** The Mini App now has the bot's digital-accounts shop for the owner's own store: the supplier's categories, a product page with its text and stock, a quantity, an order summary, and the wallet pays; the account is shown to its buyer and kept in My purchases. Paying a Toman price with manual USDT now also says the rate the amount was worked out at (the live price of USDT plus your margin when the store follows it).

### Updating
- No database migration, no new service, nothing to configure, no license change.
- Update from the dashboard (System > Updates) or `sudo bobres update`. Core and the bot move together; the Mini App is served by core.
- The digital-accounts section exists only on the store that has the vendor's own features (the owner's own store); every other store shows no new section.

### Known limits
- The Mini App has not been tried in a real Telegram client on iOS, Android and Desktop; the accounts are shown as text and copied with one button.
- A purchase the supplier has not delivered yet shows as "being delivered" and the app asks again every 3 seconds for about four minutes; after that it points to My purchases and support.
- The rate shown is the one in force when the payment screen was drawn; a customer who pays much later should read the amount on the screen, not the rate.

### Added
- **Digital accounts in the Mini App.** On the store that has the vendor's own features, **More > Digital accounts** is the bot's shop in the app: the supplier's categories, a category's products, a product page with its text and stock, a quantity (1 to 25, no more than the supplier has), an order summary with the wallet balance, and the wallet pays. The account is shown to its buyer and kept in **My purchases**; a purchase the supplier has not delivered yet is followed every 3 seconds, and a short wallet is sent to the Wallet tab. It is the same shelf, wallet and idempotent purchase as the bot (`/api/mini/v1/digital`, `/digital/buy`, `/digital/orders`). No database migration.

### Changed
- Paying a Toman price with manual USDT in the Mini App now shows the rate the USDT amount was worked out at (the live price when the store follows it); the payment answer carries it as `usdt_rate`.

## [0.15.0] — 2026-10-23

**Buy a license in one screen, bring back a deleted plan, Undo in the bot's admin menu, a project wiki, and a native read of the newest Persian texts.** When a customer's wallet is short for a license, the bot now says how much is missing and, after the top-up lands, buys the license by itself and sends the key; the owner can bring a deleted plan back from the Plans page (off sale, so nothing is sold by accident); the last change made in the bot's admin menu can be taken back with one tap for an hour; the project has a GitHub Wiki in English and Persian, kept in `docs/wiki/` and published after every merge; the Persian wording of the newest texts was read as a native speaker would read it; and two flaky tests that failed the v0.14.0 release gate are fixed.

### Updating
- One database migration, additive (a rollback keeps every order and credit): core 00038 (`vendor_intents`). No new service, nothing to configure, no license change.
- Update from the dashboard (System > Updates) or `sudo bobres update`. Core and the bot move together.
- The license purchase by itself only exists on the owner's own store (the one that sells BOBRES licenses); on every other store nothing changes.

### Known limits
- A wish to buy a license is kept 48 hours and is carried out only by a top-up that arrives after it; a top-up that arrives later, a gift code or a staff adjustment buys nothing.
- Undo keeps only the last change of each admin, for one hour, and only for the bot's admin menu (a setting, a plan switched on or off, a discount code switched on or off).
- A restored plan comes back off sale and in no group: switch it on and group it again.
- The wiki pages say what has not been tried on real devices; the Mini App still has no digital accounts (they are in the bot only).

### Added
- **Licenses: pay and top up in one screen.** When a customer's USDT wallet is short, the confirm screen says how much is missing; tapping the top-up saves what they wanted (tier and months, 48 hours) and, when the payment lands, the store buys the license by itself and sends the key. A manual buy, a refusal or a refund clears the wish, so nothing is bought twice. One database migration, additive (core 00038, `vendor_intents`). Only on the owner's store (`private_features`).
- **Restore a deleted plan.** The Plans page lists the deleted plans under the table, with a Restore button for the owner; the plan comes back off sale and ungrouped, so the owner decides when to sell it again (`plan.restore` in the audit log).
- **Undo in the bot's admin menu.** The last setting, plan or discount-code change made with the menu's buttons (or `/set`) can be taken back for an hour with one tap.

### Changed
- Persian wording of the newest texts, read as a native speaker would: the reward code message, the reseller prefix errors, the reward and Terms/Privacy hints, the supplier text import messages, and the USDT rate help and stale warning (bot and dashboard).

### Fixed
- A flaky test, `TestTLSSetRefusedByCaddy`, which made the v0.14.0 Release gate fail on a slow runner: the fake Caddy now refuses once and answers normally after that.
- The HTTPS watcher no longer asks the bot for the server's address twice in a minute (in ip mode it asks first, once, and the view reuses the answer); a flaky test, `TestTLSWatchRefreshesTheDetection`, came from this.

### Documentation
- **A project wiki**: 24 English pages and 13 Persian ones (`docs/wiki/`) for the people who run a store: install, the dashboard, the bot and Mini App, plans, payments, panels, resellers, licensing, HTTPS, updates and backups, security, troubleshooting, a FAQ and the architecture. A new workflow (`wiki.yml`) publishes them to the GitHub Wiki after each merge, so the pages are reviewed like code.
- The launch checklist (English and Persian) and `docs/PLAN.md` say what is done: the prices (Standard 4 and Pro 12 USDT a month), the license server at `license.bobresai.cv`, the owner's own Pro license and the landing pages with prices.

## [0.14.0] — 2026-10-23

**A reward you can set as an amount, deleting a plan, digital accounts by category, the live price of USDT, your own Terms and Privacy pages, and Karing and NPV Tunnel.** A plan can give its buyer a discount code of a fixed amount or a percent, for any plan, for the days and with the text you choose; the owner can delete a plan (it is taken off sale and never erased); the digital accounts in the bot follow the supplier's categories with a product page, a quantity and an order summary; crypto payments are priced at the live Toman price of USDT; every store serves its own Terms and Privacy pages; and Karing and NPV Tunnel join the apps with guides and tutorials.

### Updating
- One database migration, additive (a rollback keeps every order and credit): core 00037 (`plans.reward_amount`, `reward_days`, `reward_text_i18n`, `deleted_at`, and a looser reward-code constraint). No new service, nothing to configure, no license change.
- Update from the dashboard (System > Updates) or `sudo bobres update`. Core and the bot move together. The update also brings the new Caddyfile route for `/terms` and `/privacy`.
- **Check two things after the update.** (1) The USDT rate: **Live USDT rate** is on by default, so a store that had typed its own **Toman per USDT** now charges the live price (plus the margin you set); turn it off in Gateways to keep only your own rate. (2) Your Terms and Privacy pages at `https://<domain>/terms` and `/privacy`: read them and edit them in Branding.
- A store that already uses the tutorials gets the Karing and NPV Tunnel ones once, at the end of the list.

### Known limits
- A deleted plan cannot be brought back from the dashboard yet.
- The reward code is still sent in a message; the customer's own list of codes is not built.
- The Karing import link, like the other app links, has not been tried on a real device; NPV Tunnel has no import link, only steps.
- The supplier's category is read from its `category` field; a product with none is listed under "Other".
- The digital accounts and the live rate are in the bot only (the Mini App does not have them yet).
- The Persian and English texts of the new screens are a first draft that wants a native read; nothing of this has been seen in every Telegram client.

### Reward codes as a fixed amount for any plan, and deleting a plan
- **A reward can be an amount**, not only a percent: in a plan's editor, **Reward code after a purchase** has a kind (none, a percent, a fixed amount in
  the plan's currency), **Valid for (days)** (1 to 365, default 10; the plans already marked keep 30) and a **text for the buyer** in Persian and English that
  comes with the code. The code is **for any plan** the buyer buys next (before, only the plan that gave it), one use, for them only. An amount takes that
  amount off and never more than the price (the rest is lost), and only on a plan in its currency. A free plan (the trial) takes no code. Codes made
  before keep the plan they were bound to. Migration 00037.
- **Delete a plan** (the trash button on the Plans page, with a confirmation, **owner only**: permission `plans.delete`; nothing deletes a plan by itself): the plan is taken off sale and out of every list. It is
  **never erased**, with orders or without: its orders, reports and refunds keep it, and services already made from it keep working. Migration 00037
  (`plans.deleted_at`).
- **The live price of USDT for crypto payments**: the Toman price of one USDT is read live (the bot reads Tetherland's and Wallex's public pages every couple of minutes; core
  has no internet) and every Toman price is turned into USDT at it, so a crypto payment is priced at the moment and the amount the customer must send is
  worked out by itself. Settings **Live USDT rate** (on by default) and **Margin on the live rate (%)** (0 to 30, for the exchange's spread and fees); your own
  **Toman per USDT** stays as the fallback when the live price cannot be read or is older than 30 minutes, and is used alone when the live rate is off.
  The Gateways page shows what is charged now, where it comes from and a **Read it now** button; the bot's crypto screen shows "1 USDT = N Toman".
  No migration. This changes what a store that had set its own rate charges: turn **Live USDT rate** off to keep using only your own.
- **Karing and NPV Tunnel** next to v2rayNG, Hiddify, Streisand and V2Box: on the subscription page Karing has its **Add to Karing** button (`karing://install-config?url=...`,
  not yet tried on a device, like the others) and NPV Tunnel has its steps (it documents no import link, so there is no button); the help center gets a
  **Download Karing** and a **Download NPV Tunnel** tutorial, with their store and download buttons. A store that already uses its tutorials gets the two once, at the end
  of the list (unless it has one for that app); what the owner deletes is not put back. Streisand was there already: its button, its guide and its tutorial.
- **The store's own Terms and Privacy pages**: every store serves `https://<domain>/terms` and `/privacy` (Persian and English, `?l=en`), with a general default text that puts
  in the store's name and support contact. The bot's Terms and Privacy buttons, `/terms` and the subscription page's footer use them while the owner has set no address of
  their own. The owner edits either text per language on the Branding page (Your Terms and Privacy pages), or goes back to the default; `{store}` and `{contact}`
  are filled in. The text is a small markdown, escaped before it is shown. Caddy routes `/terms` and `/privacy` to core.
- **The sign-in page on a phone**: the six boxes of the authenticator code share the row evenly, and a command in a Persian sentence (`bobres admin password`, `/dashboard`) is a
  small left-to-right chip that no longer scrambles the sentence. The same boxes on the Account page.
- **Digital accounts in the bot, like the supplier's own**: the home lists the supplier's **categories** in a grid (two to a row, red when nothing in one is in
  stock), a category lists its products (green; red and "(out of stock)" when sold out; a red Back), a **product page** shows the description, the unit price
  and the stock with **Buy now**, then **how many** (buttons or a typed number, no more than the stock) and an **order summary** with **Pay with wallet**.
  A purchase takes up to 25 (it was 1 in the bot). The category comes from the supplier's own `category`; no migration.
- **A product's description comes from the supplier** when the owner wrote none (it was hidden from customers); the owner edits it per language, and **Copy the
  supplier's name and text** starts from it. **Export texts / Import texts** on the Digital accounts page move every name and text through a file, so they can
  be translated to Persian in one go; prices are changed as before in each product's editor.
- **Fixed: four bot texts showed a literal `\n`** instead of a line break (the reward code message, Find by link's service details and the reseller prefix
  screens); a test now keeps a backslash-n out of every text.

## [0.13.0] — 2026-10-22

**Reward codes, sold-out digital accounts, the owner's buttons in the bottom menu, and finding a service by its link.** The owner marks a plan and
each paid purchase of it gives the buyer a discount code for their next purchase of the same plan (for them only, one use, 30 days); a digital
account that is out of stock is shown as sold out instead of hidden; the bot's bottom menu has the licenses and digital accounts buttons; and a
customer can look a service up by its subscription link in My services and renew it.

### Updating
- One database migration, additive (a rollback keeps every order and credit): core 00036 (`plans.reward_percent`, and three columns on `discount_codes`).
  No new service, nothing to configure, no license change.
- Update from the dashboard (System > Updates) or `sudo bobres update`. Core and the bot move together.
- **Nothing changes for customers** until the owner marks a plan with a reward (dashboard, Plans, the plan's editor). The sold-out display, the menu
  buttons and Find by link are on as soon as the update is in.

### Known limits
- A reward code is sent in a message; the customer's own list of codes is not built (staff see every code in Discounts).
- Find by link finds the services BOBRES made (it knows their links); it renews, it does not add traffic.
- The Persian and English texts of the new screens are a first draft that wants a native read; nothing of this has been seen in a real Telegram.

### Reward codes, sold-out digital accounts and the owner's buttons in the bottom menu
- **A reward code for the unlimited plan (or any plan the owner marks)**: in the dashboard a plan has **Reward code after a purchase** (1 to 90 %). A paid
  purchase or renewal of it by a customer gives the buyer a discount code for their next purchase of **the same plan**: bound to them (for anyone
  else it is not valid), to the plan, one use, 30 days; the bot sends it in its own message. A reseller, a free order, a traffic package and one service of
  a bulk purchase give none. Migration 00036 (`plans.reward_percent`, and on `discount_codes` the buyer, the plan and the purchase that gave it).
- **Digital accounts that are out of stock are shown**, marked Sold out, with their description (they cannot be bought); a product the supplier no longer
  lists is shown the same way. While one is sold out the catalog is read every five minutes (it was every half hour).
- **My services has Find by link**: a customer sends a subscription link (or the address of its page) and the bot finds the service, shows its state
  (only what the link already shows) and offers **Renew**, paid from the customer's own account. A renewal of a service that is not the buyer's needs the
  link's secret, which core checks at the quote and at the order; traffic packages stay the owner's.
- **The bottom menu has the BOBRES licenses and Digital accounts buttons** (each while it is on), under Buy.

## [0.12.0] — 2026-10-22

**Resellers get their own names in the 3x-ui panel.** A reseller's configs are in a group of their own, `BOBRES-<NAME>` (the owner gives the
name), and are named with the reseller's own prefix (`sob-u123-...`, chosen once in the bot or the Mini App); every other client is in the
group `BOBRES` (renamed from `bobres_bot`). 3x-ui v3.10.0 and v3.10.2 are tested too (the range is now v3.8.5 to v3.10.2).

### Updating
- Two database migrations, both additive (a rollback keeps every order and credit): core 00035 (three columns on `resellers`) and provisioner 00006
  (a column on `client_map` and one on `xui_servers`). No new service, nothing to configure, no license change.
- Update from the dashboard (System > Updates) or `sudo bobres update`. Core, the bot and the provisioner move together.
- After the update the provisioner goes through every 3x-ui panel once: the clients in the group `bobres_bot` (or in none) move to `BOBRES`. A
  group someone chose by hand is never touched.
- **Nothing changes for a reseller until the owner gives them a group name** (dashboard, Resellers, the reseller's page, Panel names): until then
  their configs are named as ever and are in `BOBRES`. Once named, their existing configs move into `BOBRES-<NAME>` in the background.

### Known limits
- A 3x-ui client is in one group, so a reseller's clients are not in `BOBRES` any more: filter on the reseller's group to see them.
- A prefix applies to the configs made after it; the names of existing configs are not changed.
- The Persian and English texts of the new screens are a first draft that wants a native read; nothing of this has been seen in a real Telegram.

### Reseller groups and prefixes in the 3x-ui panel
- **The default 3x-ui client group is now `BOBRES`** (it was `bobres_bot`). After the update the provisioner moves the clients that are in the old
  group (or in none) to it, once per panel; a group an operator chose is never touched.
- **A reseller's configs are in a group of their own, `BOBRES-<NAME>`**: the owner writes NAME when making the reseller (dashboard Resellers, or
  later in the reseller's page, **Panel names**); capitals, digits and `_`, 2 to 24, unique. New configs are made in it; the reseller's existing
  configs move into it in the background (and again with **Move the existing configs**). A client is in one group, so they are no longer in `BOBRES`.
- **A reseller's configs are named with their own prefix**: `<prefix>-u<id>-<random>`. The reseller chooses the prefix **once** in the bot (reseller
  panel, **Config prefix**, with a confirmation) or the Mini App (2 to 10 of a-z and 0-9, unique); afterwards only the owner changes it in the
  dashboard. It applies to the configs made after it.
- Core: migration 00035 (`resellers.config_prefix`, `panel_group`, `groups_synced_at`), `ResellerInfo` carries them, `SetResellerConfigPrefix` in the
  proto, the dashboard API `PUT /resellers/{id}/naming` and `POST /resellers/{id}/group-sync`, the Mini App's `POST /reseller/prefix`. Provisioner:
  migration 00006, `CreateClient.group`, `SetClientGroups`, and a backfill that converges the panel to each client's group.
- A reseller with no group name or prefix is as before (the usual names, in `BOBRES`).

### Changed
- **3x-ui v3.10.0 and v3.10.2 are tested**: CI runs the adapter and provisioner integration suite (including the `bobres_bot` group tests) against real
  panels of both, next to v3.8.5 and v3.9.0, and the tested range is now **v3.8.5 to v3.10.2**. A panel in that range is no longer marked
  "untested" on the Servers page. No code change was needed for the new panels.

## [0.11.0] — 2026-10-21

**Plan groups**: the operator puts plans in named groups with a text for each, and the customer picks a group first, reads its
text, then picks a plan of it, in the bot and in the Mini App. A store that makes no group lists its plans exactly as before.

### Updating
- One database migration, additive (so a rollback keeps every plan and order): 00034 (the table `plan_groups` and the column
  `plans.group_id`, null for every plan that exists). No new service, nothing to configure, no license change.
- Update from the dashboard (System > Updates) or `sudo bobres update`. Core and the bot move together; a bot that does not know
  groups lists plans flat, so a half-updated install keeps selling.
- After the update **nothing changes for customers** until the operator makes a group (Plans > Plan groups in the dashboard).

### Known limits
- Only Buy and the Mini App's new purchase are grouped. Renew, Add traffic, a reseller's Buy for a customer and the Bulk purchase
  list the plans flat.
- The bot's own plan editor does not change a plan's group (the dashboard does).
- The Persian and English texts of the new screens are a first draft that wants a native read. Nothing of this has been seen in a
  real Telegram yet: the flow is covered by tests that drive the bot end to end.

### Added
- **Plans can be put in named groups with a text for each**, and the customer picks a group first, reads its text, then picks a plan of
  it: in the bot (Buy shows the groups, a group's screen shows its text and its plans, Back from the payment menu returns to the group,
  plans with no group are under "Other plans") and in the Mini App. A store with no group lists its plans exactly as before.
- The dashboard's Plans page has a groups table (a name and a text in Persian and English, an order; deleting a group keeps its plans
  on sale, ungrouped) and a Group field in the plan editor. The audit log names `plan_group.upsert` and `plan_group.delete`.
- Core: `plan_groups` and `plans.group_id` (migration 00034, additive), `/api/v1/plan-groups`, `group_id` on a plan (absent keeps the
  group, `""` clears it, so the bot's plan editor never loses it), `Plan.group_id` and `ListPlansResponse.groups` in the proto, and
  `group` and `groups` in the Mini App's plans. A group has a name of at most 60 characters and a text of at most 1500 in each language,
  a store has at most 50 groups, and a free trial or a traffic package is never in a group.
- Renew, Add traffic, a reseller's Buy for a customer and the Bulk purchase still list the plans flat.

## [0.10.1] — 2026-10-21

A patch for the owner's **Digital accounts**: the supplier's balance is now **always in view** (on the page and on the Overview),
and it is read from more shapes of the supplier's answer. Nothing changes in a customer's bot, dashboard or license.

### Updating
- No database migration, no new service, nothing to configure.
- Update from the dashboard (System > Updates) or `sudo bobres update`. No license change is needed.

### Known limits
- The balance is read from the supplier's answer by the names such APIs use; when the answer is not understood the page shows it
  as it came, so the reading can be taught in a later patch.

### Changed
- **The owner's balance at the supplier is shown all the time**: a card at the top of the Digital accounts page and on the Overview
  (owner only, when the store resells), read when the page opens and every two minutes while it is open, with a button to read it
  now. Core keeps a balance it read for a minute, so a busy dashboard is not a call to the supplier at every page view.
- The balance is read from more answer shapes (a number or a text like "5.53 USDT", at the top of the answer or inside `data`, under
  any key that names a balance). When the supplier's answer still cannot be understood the page **shows the answer itself** (the API key
  is taken out of it), so the reading can be fixed without guessing.
- The supplier's product names and texts no longer carry Telegram custom-emoji placeholders (`{pe:5244675575502628242}`) or Windows line
  breaks; the product's raw JSON is kept as it came.
- No migration, nothing to configure.

## [0.10.0] — 2026-10-21

The owner's own bot can now **resell a supplier's digital accounts** (the Digital accounts button, a dashboard page for the
supplier's key, products, prices and orders), and the features that are the vendor's own (selling licenses, managing the
license server, this resale) are **locked behind a license entitlement, `private_features`**: a customer's install does not
show them, refuses them and has them off. Nothing changes in a customer's bot, dashboard or license.

### Updating
- One database migration, additive (so a rollback keeps every order and credit): 00033 (the ledger kinds `supplier_purchase` and
  `supplier_refund` and the tables `supplier_config`, `supplier_products` and `supplier_orders`, empty on every install that
  does not resell). No new service, nothing to configure.
- **The vendor's own store** needs a license that carries `private_features` to keep its Licenses page and to use Digital accounts:
  rebuild the license server first (it must know the new field), give the license the entitlement (docs/13-license-server.md), then
  update the store; the store takes the entitlement at its next daily refresh. Without it those pages are hidden.
- Update from the dashboard (System > Updates) or `sudo bobres update`.

### Known limits
- **Nothing of the resale has met a real supplier yet:** the catalog and order answers are read tolerantly and each product's raw
  JSON is kept, but the first real catalog read and a first real (cheap) order will show how the delivery reads.
- The lock hides and refuses the features; it does not hide their code, which is in every build. A separate private build is on the list.
- Prices of resold accounts are in Toman only; the bot buys one at a time.

### The vendor's own features are locked behind the license
- A new entitlement, **`private_features`**, unlocks the features that are the vendor's own and not for sale: selling BOBRES licenses,
  managing the license server from the dashboard, and reselling a supplier's accounts. Only a license the vendor signs for its own
  store carries it (no tier preset does). **Without it** the dashboard does not show those pages, core answers 404 to their paths,
  refuses to keep their keys, the bot's switches read off whatever they were set to, and nothing is sold. Pending orders and what
  customers already bought keep settling and stay readable. A license signed before this version has none of it, as it should.

### The owner's own bot resells a supplier's digital accounts (off unless set up)
- **Digital accounts**: the store reads a supplier's catalog (the reseller API of ssondigitalworks.online: products, balance, order),
  imports every product **off and unpriced**, and the owner writes the **price in Toman** and the Persian and English texts and
  switches each on. A customer pays from the wallet and gets the account in the bot (and again under My purchases). One
  transaction takes the money and writes the order; the supplier is asked with the order id (so it never charges twice); the
  delivery is stored encrypted; a supplier that does not answer leaves the order paid and a worker asks again; a refusal for good
  refunds. The owner can retry or refund an order the supplier never delivers.
- **Core has no internet access**: it calls the supplier through the bot's new `POST /internal/supplier`, which talks to the one
  supplier address the bot is configured for (`BOBRES_SUPPLIER_URL`).
- Dashboard > **Digital accounts** (owner only): the API key (encrypted, write-only), the supplier balance, the products (cost, stock,
  price, texts, order, switch) and the orders. Migration 00033, the setting `supplier.enabled`, four core RPCs. docs/supplier.md.

## [0.9.1] — 2026-10-21

The owner's own store can now **manage every license of the license server from the dashboard**, not only the ones the
bot sold: the **All licenses** tab, with making one by hand, changing, renewing, revoking, re-keying and the details.
Nothing changes in a customer's bot, dashboard or license.

### Updating
- **Update the license server first** (it is built from this repository on the vendor server: its admin API gained `update`,
  a custom end on create and a detailed `get`), **then the store**: `sudo bobres update` or System > Updates > Update now.
  A store that is ahead of its license server gets a 'no such path' from the new buttons, and nothing else.
- No database migration, no new service, nothing to configure.

### Dashboard > Licenses: manage every license of the license server
- The Licenses page gets an **All licenses** tab that reads the license server itself (through the signed admin API, by way of the
  bot): the store's own license, the ones made by hand with `licensesrv key` and the ones the bot sold, each with the domain it
  runs on, when it was last seen and its moves. The owner can **make a license by hand** (a trial, a gift, a customer who paid
  outside the bot: Standard, Pro or a trial, ending after some months, on a date, or never; the key is shown once), **change** one
  (customer, tier, end date, or renew by some months), **revoke** one, **issue a new key** for one nobody activated, and open its
  **details** (what it grants, where it ran, its history). What the bot sold stays in step with a change made here.
- The license server's admin API gains `update`, creating with an end date or no end (and a trial), and a `get` with entitlements,
  activations and audit. The bot's pass-through endpoint allows the new path. Every change carries a ref, so a double click is one
  change; the audit names who did it and holds no key.

## [0.9.0] — 2026-10-21

**Selling BOBRES licenses from the owner's own bot**: Standard and Pro, one month or a year, paid from the wallet in USDT,
the key delivered in the chat, a Licenses page in the dashboard, and a signed admin API on the license server behind
it. It is **off on every install unless the owner sets it up**: a customer's bot, dashboard and license are unchanged.

### Updating
- **From the dashboard:** System > Updates > Update now (owner only), or `sudo bobres update` over SSH. Core and the bot
  move together.
- One database migration, additive (so a rollback keeps every order and credit): 00032 (the ledger kinds `license_purchase`
  and `license_refund`, and the tables `vendor_config`, `vendor_licenses` and `vendor_orders`, empty on every install
  that does not sell licenses). Nothing to configure, and no new service or systemd unit.
- After the update **nothing changes in the bot or the dashboard** except a **Licenses** section for the owner in the
  dashboard's menu, which says sales are off. To sell licenses the owner makes an admin key pair on the license server
  (`licensesrv adminkey`), turns the signed admin API on there (`BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE`) and serves
  `/admin/v1/` next to `/v1/`, pastes the private key in Licenses, and switches sales on (docs/13-license-server.md).
  The requests go through the bot to the server it is configured for (`BOBRES_LICENSE_SERVER`): core has no internet.
- The bot's top-up helpers now take a currency; every existing call still tops up in Toman.

### Known limits
- **Nothing of this has been seen in a real Telegram:** the licenses screens, the USDT top-up for a shortfall and the
  Persian texts (a first draft that wants a native read). The flow is covered by tests against a fake license server
  that verifies every signature, and the signed API against a real Postgres.
- A buyer needs a **USDT wallet**: a short wallet is offered a top-up of exactly the shortfall (automatic TRC20 or a
  manual USDT payment); after paying they come back and tap Pay. One screen that does both is not built.
- A key lost between the license server making it and core storing it is replaced by a new one (possible only while
  nobody has activated the license); if even that fails an operator re-keys it by hand.
- Refunds of a sold license are the owner's call: revoking does not move money.

### Selling BOBRES licenses from the owner's own store (core)
- A buyer pays from the wallet (any top-up method fills it, automatic USDT TRC20 included) and core asks the license
  server's signed admin API for a key. One transaction takes the money and writes the order; the server is asked
  outside it with a ref made from the order, so a retry never makes a second license or extends twice; a server that is
  down leaves the order paid and a worker asks again with a growing wait; a refusal for good (any 4xx but 408 and 429)
  gives the money back. The activation key is stored encrypted with core's master key so its owner can be shown it
  again. Migration 00032 (the ledger kinds `license_purchase` and `license_refund`, `vendor_config`, `vendor_licenses`,
  `vendor_orders`). Off by default (`vendor.enabled`) and inert without the admin key. Core has no internet access, so it
  signs each request and the bot passes it to the license server it is configured for (`BOBRES_LICENSE_SERVER`),
  at the new `POST /internal/vendor/admin`. Prices come from internal/license/tiers.json.

### The owner's bot sells BOBRES licenses (bot; off unless `vendor.enabled`)
- A **BOBRES licenses** button on the home menu of the owner's own store: Standard and Pro, one month or twelve, at the
  prices in the tier file, paid from the wallet in USDT. A buyer short of money is told how much to top up and offered
  automatic TRC20 USDT or a manual USDT payment for exactly that (the bot's top-up now works in any currency, not only
  Toman). The key is in the same message, and again under **My licenses**, which also renews. The license server being
  down leaves the purchase paid and pending; the key shows up in My licenses when core's worker gets an answer.
- **Dashboard > Licenses** (owner only): the admin key that signs the requests to the license server (kept
  encrypted, never shown again), the switch for sales, the licenses sold, months as a gift, and revoking one (the audit
  names who did it).
- Core RPCs `ListVendorOffers`, `BuyVendorLicense`, `ListMyVendorLicenses`, `GetVendorLicenseKey`. The sales texts are
  exempt from the rule that a customer never reads about licensing (they are the product).

### License server: a signed admin API (off by default)
- `licensesrv` can serve `/admin/v1/licenses/{create,extend,tier,revoke,rekey,get,list}` for a program that sells
  licenses. It is on only when `BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE` names an Ed25519 public key (`licensesrv adminkey`
  makes the pair). Every request is signed (method, URI, timestamp, one-time nonce, body hash); every failure to
  authenticate is the same 401; every change takes a `ref` that works once, so a retry changes nothing twice. A key
  is returned once and only its hash is kept. Migration 00002 of the license database adds `admin_ops` and
  `admin_nonces`. Customer installs are not affected. See docs/13-license-server.md.
- The tier file carries the owner's prices (Standard 4, Pro 12 USDT a month; a year at ten months).
- The landing page shows them and sends buyers to the owner's bot instead of the GitHub issue tracker.

## [0.8.0] — 2026-10-20

The rest of the owner's requests of 2026-10-20, and one more: **server monitoring** in the dashboard, **charts and a
3D professional report** on the Reports page, **downloading and restoring a backup** from the dashboard, a full
**uninstall** that leaves a server as on day one, the bot's **FAQ and tutorials**, a **two-column plan list**,
**bulk purchase**, **gift codes**, and **more than one required channel or group** (one on the free tier and Standard,
any number on Pro). The prices are set too: **Standard 4 USDT a month, Pro 12 USDT a month** (docs/pricing.md).

### Updating
- **From the dashboard:** System > Updates > Update now (owner only) updates a server that has the updater helper
  (every server installed or updated to 0.7.0). Without the helper, or if it does not go through, `sudo bobres update`
  over SSH always does. Core and the bot move together: update both, as either way does.
- Six database migrations, all additive (so a rollback keeps every order and credit): 00026 bulk orders, 00027 gift
  codes, 00028 help content, 00029 required chats, 00030 the monitor's samples, 00031 the `action` of dashboard
  updates (restores use it too). Nothing to configure.
- **Two more systemd units on the host**, `bobres-monitor.timer` and `bobres-monitor.service` (root, every 30 seconds:
  they write one snapshot of the host and Docker into `run/monitor`, which core reads through a read-only mount).
  `bobres update` sets them up; `sudo bobres monitor disable` (or `BOBRES_MONITOR_COLLECTOR=off`) removes them.
  `bobres check` and `bobres doctor` have a Host monitor line.
- **Restoring from the dashboard needs this version's updater helper**: `bobres update` installs it. A server whose
  helper is older is told to run `sudo bobres update` once.
- After the update the bot lists plans as a table (`bot.plan_layout` = `list` brings back one button per plan), the
  home menu gains a green Bulk purchase button (`bulk.enabled` = off hides it) and FAQ / Tutorials buttons, and the
  wallet screen gains Apply gift code (`gift_codes.enabled` = off hides it; it needs core's master key, which every
  install made by the installer has).
- **A license signed before this version has no `max_join_chats`**, so it counts one required chat until the seller
  re-applies its tier (`licensesrv key update <id> -tier pro -reason "..."`, then the install's next daily renewal).
  Nothing that exists is lost: there are no extra required chats before this version.
- `sudo bobres uninstall --purge` is new. A server installed before this release has no install receipt until its next
  `sudo bobres install` or `sudo bobres update`; without one the uninstall takes conservative defaults.

### Known limits
- **Nothing of this has been seen in a real Telegram:** the table of plans on phones, the one message with many links of
  a bulk purchase, the Join buttons of many chats, the gift code screen, the FAQ and the tutorials. The Persian texts of
  all of them are a first draft that wants a native read (every one can be edited).
- A bulk purchase is paid from the wallet only, takes no discount code, buys a paid plan only and is in the bot only. The
  FAQ, the tutorials and the bulk purchase are not in the Mini App.
- **Monitoring** has run against the real systemd and Docker of a lab server and against recorded answers of Kubernetes
  and GitHub; no live cluster, and no hours-long refresh, has been tried.
- **Restore from the dashboard** has been proven on a lab server with real systemd, Docker and Postgres (a restore, an
  upload, a wrong key, a rollback, a symbolic link as the upload), not with a 2 GiB file through Caddy or across schema
  versions. It restores only backups this server's `BOBRES_BACKUP_KEY` opens (a backup of another server still needs
  `bobres restore --key-file`), and turning the dashboard updater off turns the dashboard restore off too.
- **The uninstall** was proven on a lab server (Ubuntu 24.04, systemd); Debian and Ubuntu 22.04 are untried. Docker's
  iptables chains stay in memory until the next reboot after `--remove-docker`.
- The Reports page has been looked at in Chrome only (not Safari or Firefox), and the Save-as-PDF dialog was not tried.

### Monitoring: the server, the containers and the stack in one place

A **Monitoring** section for the dashboard: one overview of the whole store and the server it runs on. This is the backend and its API;
the tab that shows it is described under "Monitoring in the dashboard" below.

**Added.**

- **Host collector.** `bobres monitor snapshot` (a root systemd timer, `bobres-monitor.timer`, every 30 seconds) writes the host's
  figures (CPU, load, memory, swap, disks, network, uptime), every Docker container (state, health, restarts, CPU, memory, network),
  Docker's disk use, the error lines of Docker's and BOBRES's journals and of Caddy (scrubbed of tokens, addresses and e-mails), the
  last update, and, if `kubectl` or `k3s` is on the host, the nodes and pods, into `/opt/bobres/run/monitor/snapshot.json`. It is on by
  default; `sudo bobres monitor disable` turns it off for good. Core reads the file through a **read-only** mount and treats it as
  untrusted text; core still has no Docker socket. `bobres check` and `bobres doctor` show a **Host monitor** line.
- **Core samples** the host, the containers, itself, the queues, the bot, the services, the 3x-ui servers and the gateways every 30
  seconds into the database (7 days at full resolution, then 5-minute averages up to 30 days, rolled up and pruned by core). A
  source that is unavailable leaves a gap, never a zero.
- **API** (owner and admin, read only, permission `monitor.read`): `GET /api/v1/monitor/overview`, `/series`, `/metrics` and `/events`,
  documented in `docs/04-dashboard.md` ("Monitoring API") with the status rules, the components, every field and the metric list.
- **Alerts**, through the usual Telegram alerts and the System page's open problems: disk past 85%, memory past 90% for 10 minutes, a
  container down, unhealthy or restarting repeatedly, the collector silent for 5 minutes, and the CI of `main` failing (off by default).
  The thresholds are in Settings, in a new Monitoring group (`monitor.enabled`, `monitor.disk_pct`, `monitor.mem_pct`,
  `monitor.mem_minutes`, `monitor.restarts`, `monitor.collector_minutes`, `monitor.ci_alert`).
- **CI/CD facts through the bot**: the latest runs of the repository's workflows on `main`, the Release workflow's runs and the latest
  releases, read from GitHub's public API anonymously (cached 5 minutes, conditional requests, a clear message when GitHub is
  unreachable or the request limit is used up), and the **deployment history**: the updates asked for on the System page and the
  last one run by hand on the server. New bot endpoints `GET /internal/ci` and `GET /internal/stats` (core's token only).
- A runbook, `docs/runbooks/monitoring.md`.

**Updating.**

- One database migration (00030): two tables for the time series and an index on the outbox for the alert history. It only adds;
  rolling it back drops them.
- `sudo bobres update` puts the collector's two units and the folder in place and adds the read-only mount to `docker-compose.yml`.
  Until the update has run, the Monitoring page works without the host figures and says so. Core and the bot move together here too (the
  bot has two new internal endpoints); `sudo bobres update` does both.
- To keep the host collector off: `sudo bobres monitor disable` (or `sudo bobres config set BOBRES_MONITOR_COLLECTOR off`).
- Not verified: the Kubernetes section was tried with recorded `kubectl` answers, not a live cluster; nothing here can be tried in a real Telegram.

### Backups: download and restore from the dashboard
System > Backups can now hand the owner a backup and put one back, without a shell on the server. Both are the owner's.
- **Download.** A button on each backup. A dialog says the file is encrypted and that the key (`BOBRES_BACKUP_KEY`) is
  never sent with it; the authenticator code is asked for where the session has one. The browser takes a single-use
  address as its own download, and core streams the file from the backup service in chunks, never in memory. Every
  download is audited (`system.backup.download`) and told to every owner on Telegram before the first byte is sent.
- **Restore** a backup from the list, or a file uploaded in the new *Restore from a file* card (at most 2 GiB). The
  dialog says what is lost (everything after the backup's time) and that the dashboard and the bot are unavailable for
  a few minutes; the owner types the store's domain, ticks that they understand, and gives the authenticator code. The
  server's updater (the helper of Update now) takes a safety backup of the current data, restores, checks that the
  store is healthy and goes back to the safety backup on its own if it is not. The backup is copied into a file of the
  helper's own while its SHA-256 is checked, and read with this server's key before anything is stopped: a backup of
  another server (or a damaged one) is refused with the reason. The page shows the steps, survives core restarting
  and finds the end again after the restore replaced core's own database. Every request and its end is audited
  (`system.restore.*`) and told to every owner on Telegram. Persian first, English second.
- Updating: one database migration (00031: three columns on `core.system_updates`), and a new helper. Run
  `sudo bobres update`; until the helper is the new one the Backups tab says so and keeps the `sudo bobres restore`
  command. Core and the bot move together (a new event, `system.backup.v1`). Nothing to configure; turning the
  dashboard's updater off (`BOBRES_UPDATE_FROM_DASHBOARD=off`) turns the restore off with it.
- Limits: only a backup this server's key opens can be restored from the dashboard (one from another server needs
  `sudo bobres restore FILE --key-file KEYFILE`); the audit log after a restore is the backup's plus one entry for the
  restore; the restored data has its own login sessions, so the owner may be asked to log in again.

### Reports: charts, a scrolling table and a professional report
- **Charts on the Reports page**, one currency at a time: revenue and what was kept after refunds, **revenue by plan**
  over the periods, paid orders by type, how orders were paid (a donut), the top plans, **the busiest hours of the
  week** (a heatmap) and new customers, free trials and services lost. Every chart follows the range, the grouping and
  the Calendar switch (Jalali or Gregorian), has a legend that shows and hides series, a tooltip, keyboard keys
  (the arrow that points right goes right on screen, which is earlier in Persian) and a table of its numbers, and works
  in light, dark and right to left. The cards carry a small trend line.
- **The "by period" table scrolls inside its own box** (down and sideways) with its header row, its period column and
  a new totals row held in place, so a long range or a phone no longer makes the page long or wide.
- **A Professional report view** (`?view=report`) to read, print or save as PDF: a letterhead, a headline per currency
  in plain words (Persian and English), each headline number with its **change on the period before** and a sentence
  about it (rises in lost services are shown as bad news), the charts, and two **3D views**: an isometric column scene
  of revenue by plan and a tilting donut of how orders were paid. They are SVG (no WebGL, no library), load on demand
  in their own small chunks, turn with a drag or two sliders, and give way to the flat charts on a phone, when the
  person prefers reduced motion and for more than 62 periods. The print button uses a paper stylesheet (A4, light
  colours, no menus).
- **A chart kit** under `web/dashboard/src/components/charts/` (line and area, bars, donut, heatmap, sparkline, with
  accessibility, right to left, themes and a README) for the dashboard's other pages; the System > Monitoring page is
  meant to use it.
- Two new read-only API calls feed the new charts, **no migration and no new table**: `GET /api/v1/reports/plan-series`
  and `GET /api/v1/reports/hours` (`reports.read`, documented in [docs/14-reports.md](docs/14-reports.md) and
  [docs/02-events-and-apis.md](docs/02-events-and-apis.md)). The CSV files, the daily summary and every existing answer
  are unchanged. An older core without the two calls still works: the page leaves those two charts out.
- Core and the dashboard are one binary, so there is nothing to update separately and nothing to configure.
- Not seen in a real Telegram (nothing here is in Telegram). Checked in a real browser at 375 px and on a desktop in both
  languages and both themes against a stand-in for the report API, and in the tests; the 3D views and the print layout
  were looked at in Chrome only.

### Uninstall: everything BOBRES put on a server, gone
- **`sudo bobres uninstall --purge` is now the day-one command**: it removes everything installing BOBRES put on the
  server (the install directory with its secrets and backups, the Docker project's containers, volumes, networks and
  images, the systemd units named `bobres-*` such as the dashboard updater's, the firewall rules, the fail2ban jail and
  the other files the hardening wrote, the swap file, BOBRES's state and logs, and last the `bobres` command), so that
  the server is as it was. It prints the exact plan with sizes and counts, offers a **verified final backup**
  (`--backup-to FILE`: written outside the install directory and read back completely before anything is deleted;
  when it fails nothing is deleted), and asks for the install's domain **typed in full**. A script needs
  `--yes --confirm <the domain>` and `--backup-to` or `--no-backup`. `--dry-run` prints the plan and changes nothing.
  Plain `bobres uninstall` is unchanged (containers only, data kept). The menu's Uninstall item is a submenu with the
  same questions.
- It deletes only what it can prove is BOBRES's: Docker objects by the project label and the `bobres_` name, units by
  their name (found, not listed, so a unit a later release adds is covered), files by the header BOBRES wrote in them
  or the receipt, a directory only when it is no system directory or symbolic link and holds BOBRES's marker files.
  It never touches SSH settings or the SSH firewall rule, DNS, other users' data, or other Docker workloads. It is
  resumable and harmless to run twice, logs to `/var/log/bobres-uninstall.log` (kept), and ends with a leftover scan
  ("Nothing of BOBRES is left on this server", or what is and why; exit 1) that is also `bobres uninstall --verify`.
- **Docker and the packages BOBRES installed go only on request** (`--remove-docker`, `--remove-packages`), only when
  the install receipt says BOBRES installed them, and only when apt and Docker agree nothing else needs them. ufw
  stays on by default (`--disable-firewall` turns it off where BOBRES turned it on); `--keep-backups` keeps `backups/`.
- **The install receipt**: `bobres install` and `bobres harden` now write `/var/lib/bobres/install-receipt.json` (root
  only, outside the install directory so that the uninstall can read it last): the packages that were new after each
  apt run, whether ufw was on before, the ufw rules BOBRES added, each file the hardening replaced with its original,
  the swap file, and the images the install pulled. `install.sh` records what Docker's installer and openssl added in
  `/var/lib/bobres/installed/`. It is additive and tolerant: no receipt (an install made by an earlier release)
  means the uninstall uses the markers the installer wrote and takes the conservative choice (see
  [docs/06-installer.md](docs/06-installer.md), "Uninstall").
- A complete inventory of what installing BOBRES changes on a server, in three classes (created by BOBRES only, changed
  in a shared file, installed software), is in [docs/06-installer.md](docs/06-installer.md); tests fail when an
  installer step, a unit file or a path literal is added that the uninstall does not know. Runbook:
  [docs/runbooks/uninstall.md](docs/runbooks/uninstall.md) (including restoring the final backup on a new server).
- CI's end-to-end job now runs the purge as root with `--confirm` and `--no-backup`.

### Help content: a FAQ and tutorials the owner writes
- **The bot has a FAQ and a Tutorials menu, written by the owner in the dashboard** (Branding, "Help content" tab,
  `branding.write`, in Persian and English). A FAQ button is the first button of the Support screen (and stays beside
  "ticket history" while a ticket is open); a small FAQ opens as one numbered message, a large one as a numbered list
  of questions, and every FAQ screen ends with a "Send a message to support" button. A Tutorials button on the home
  screen and in the bottom menu lists the tutorials, each opening its text with up to 8 https URL buttons (an app's
  download link) and a way back. Both buttons appear only while there is something enabled to show.
- The dashboard editor adds, edits, reorders, switches on or off and deletes entries, checks them as you type and
  previews them as the bot will show them, in either language. Every change is audited (`help.*`, entity
  `help_entry`) and reaches the bot within seconds; an edit from an out-of-date copy is refused.
- A new install is offered a generic FAQ and tutorials (the subscription link, then v2rayNG, Hiddify, Streisand,
  V2Box and HAPP) once, to edit or delete; they do not come back when deleted. An empty list can be given them again.

### Bulk purchase
- **New: Bulk purchase on the home menu.** The customer picks a plan, how many services (2, 3, 5 or 10 by a button, or
  any number typed, up to `bulk.max_quantity`, 20 by default and at most 100), may give them a name (`Name 1`,
  `Name 2`, ... as the services are labelled) and sees the unit price, the total and the wallet beside it before
  paying.
- **All or nothing.** A bulk purchase is N ordinary orders, each with its own wallet debit, made in **one database
  transaction**: either every service is paid for or the wallet is untouched. A new table, `core.bulk_orders`, holds
  the purchase with its **request key** (unique), so a retry, a double tap or a second open checkout returns the first
  result and never charges twice; two taps at the same instant make one purchase. The price is checked against the one
  the customer saw (a changed price asks again), a reseller's balance may use their credit limit for the whole total, and
  the license's limit is checked for all N services at once (a store that fits 3 more services refuses 5 with the
  usual "new purchases are paused", never half).
- **One message for the whole purchase.** The provisioner works on the services as it does for any order; when all
  are ready, or the last ones have failed three times and are retried in the background, the customer gets one message
  with every link (split in a few, with a text file of `name<TAB>link` lines, when it would not fit) and a line for each
  service still being prepared; a service that is ready later is sent on its own, as for any order. A failed service
  uses the ordinary `provision_failed` path: staff are alerted and can retry or refund, and no paid order is lost.
- **The rest is ordinary.** Commissions, the referral reward, revenue, the daily summary and the reports see N orders
  (each with its own purchase entry); staff get one sale alert per bulk, not N. The audit log has one `order.bulk` entry.
  A banned customer, maintenance mode and the required channel stop it as they stop any purchase.
- New settings (Settings > Plans and bulk purchase): `bulk.enabled` (on) and `bulk.max_quantity` (20).
- New gRPC calls (additive): `QuoteBulkOrder`, `CreateBulkOrder`, `GetBulkOrder`. New event `order.bulk_ready.v1`; a
  `bulk_id` (and, on `order.paid.v1`, `bulk_index` and `bulk_quantity`) on three events. See
  [docs/02-events-and-apis.md](docs/02-events-and-apis.md) and [docs/03-bot-ux.md](docs/03-bot-ux.md).

### Two-column plan list
- **The plan lists of the bot are a table**: a header row, then each plan as a row of two buttons, its name and its
  price, both opening the plan. It is the same in Buy, a reseller's Buy for a customer, and the renewal and traffic
  package pickers. A long name is cut with an ellipsis, and a store with so many plans that a table would not fit
  Telegram's limits (100 buttons, 8 to a row) falls back to one button per plan. `bot.plan_layout` = `list` is the old
  look.

### Gift codes
- **A gift code adds a fixed amount of credit to a customer's wallet.** The customer taps **Apply gift code** on the
  bot's wallet screen and sends the code, written any way (small letters, Persian digits, spaces or dashes). Unlike a
  discount code, which takes money off a purchase, this is money in the wallet; resellers may use them too.
- **Staff make them in the dashboard** (page **Gift codes**, permission `discounts.write` as for discounts): one code of
  their own words, or a batch of up to 500 generated ones with a prefix such as `NOWRUZ`, with an amount and currency,
  uses in all, uses per customer, an optional expiry and a note. Generated codes are 12 characters from `crypto/rand`
  (about 59 bits) in an alphabet with no 0, O, 1, I or L, shown as `ABCD-EFGH-JKMN`. They are listed with their last four
  characters, the value, used / in all and status, and can be switched off (what a code paid stays paid), exported as a CSV
  and traced to who redeemed them and when.
- **A code is never stored.** Core keeps an HMAC-SHA256 of it under a key derived from the master key, and its last four
  characters; a copy of the database cannot be read back into codes or tried offline. **The codes are shown once**, when
  they are made, in a dialog that copies them or downloads them as a CSV; no log, audit entry or export holds one.
- **Built for money:** one transaction locks the code's row and the wallet, so the last use of a code has exactly one
  winner, a customer's limit holds under any number of taps at once, and a code never pays above its limit (the database
  refuses it as well). A request sent again (Telegram redelivering the message) credits once. Each redemption writes one
  ledger entry (`gift_code`) and one record that names it, and sends the audit entries `gift_code.create`, `.enable`,
  `.disable` and `.redeem`.
- **Guessing is limited per customer:** five wrong codes in an hour close entering codes for an hour (`gift_code.lock` in
  the audit log); only wrong codes count and a success does not reset them. Unknown, switched-off and expired codes get
  one answer, so nothing tells a guesser which codes exist.
- The two CSV files (the list, and a code's redemptions) follow the CSV rules of
  [docs/14-reports.md](docs/14-reports.md): English headers, UTC and local times with the zone, a Jalali column when the
  page's calendar is Jalali, formulas quoted. The Mini App's wallet history names the credit "Gift code".
- API (additive): the `RedeemGiftCode` call (the bot only) and `/api/v1/gift-codes*` in the dashboard's API
  ([docs/02-events-and-apis.md](docs/02-events-and-apis.md)). Tables are in [docs/01-database.md](docs/01-database.md),
  the bot's flow in [docs/03-bot-ux.md](docs/03-bot-ux.md), the page in [docs/04-dashboard.md](docs/04-dashboard.md).

### Required channels and groups: more than one, and any number on Pro
- **The owner can now require several channels and groups** before customers use the bot or the Mini App, not just
  one. Settings has a new card, **Required channels and groups**: a list where each row has the channel or group
  (a public one's `@name` or a private one's `-100…` id), its join link (required for a private one), an optional
  name for the button and a **Check channel** button; add, remove and reorder rows, and the first row stays first. The
  card counts the rows against the license (**"2 of 1"** when more are saved than the plan counts) and marks the rows
  the plan does not count "Inactive: needs Pro". Only the owner changes the list; an admin sees it.
- **The limit is a new license entitlement, `max_join_chats`: the free tier and Standard 1, Pro and the 30-day trial
  any number** (a technical bound of 50 whatever the license says). A second chat on the free tier or Standard is
  refused with a message naming the limit and the Pro plan. The prices are not touched. When a license ends, nothing is
  deleted: the first chat stays enforced, the others stay saved but inactive, and can still be edited, reordered and
  removed, but not added to; they count again when the limit allows.
- **The bot's join message lists every chat the customer has not joined, each with its own Join button** (the name,
  else the `@name`, else "channel N"; never a private chat's id), and one "I've joined". The check asks about all of
  them again and its popup names the ones still missing. With one required chat everything reads and works as before.
  A chat the bot cannot check (it is not its administrator) lets the customer in for that chat only, as the single
  channel always did, and is logged once a minute; the others still hold. Telegram is asked as little as can be: members
  are remembered per chat for 10 minutes (a member of the whole list costs one cache read), a chat not joined is not
  asked about again by an ordinary update for 2 seconds, and the lookups run 4 at a time for a customer and 8 at a time
  for all of them, each with a timeout. Maintenance still comes before the join message.
- **The Mini App holds the same list.** Its 403 `join_required` answer keeps `join_url` and, when more than one chat is
  required, adds `join_chats` (each chat still lacking, with its link and name); its join screen shows a Join button
  for each and **I've joined** shows only what is still missing. The check endpoint of the bot is unchanged, so core
  asks about one chat at a time, four at once.
- The channel check now accepts a **group** or supergroup as well as a channel, since a required chat can be either.
- API (additive): `Settings.join_chats` (field 8) in the gRPC contract, `GET` and `PUT /api/v1/join-chats` in the
  dashboard's API (`join.write`, owner only), a new setting `join.label` (the first chat's name), and the entitlement
  `max_join_chats` in a license token and in `GET /api/v1/license`. `join.channel` and `join.link` stay the first
  chat, so the bot's `/set`, an older bot and an older Mini App keep working.

## [0.7.0] — 2026-10-20

The owner's next batch of 2026-10-20, sent with twelve screenshots of another store's bot: **the dashboard can now
update the server by itself** (System > Updates: a Check again that checks, a stable or beta channel, and Update now),
the **Reports page changes every date** with its Calendar switch and the **CSV files were reviewed** and fixed,
**maintenance mode holds every option** of the bot (Support included), and five features from the other bot arrive:
Top users, account lines, a card to forward, `/new` `/renew` `/status`, and a reseller application. The rest of that batch
(gift codes, bulk purchase, a help center, server monitoring, a full uninstall, backup download and restore in the
dashboard) comes in later releases.

### Updating
- `sudo bobres update --check` shows it and `sudo bobres update` does it. **This update must still be made over SSH**:
  the new `bobres` it installs is what sets up the helper that lets the dashboard update the server. From the next
  release on, System > Updates can do it (owner only).
- One database migration (00025, the table `system_updates`) and one new mount in `docker-compose.yml` (`./run/update`);
  `bobres update` does both. Nothing to configure. `sudo bobres check` has a new line, Dashboard updates.
- Two systemd units are added on the host (`bobres-updater.path` and `bobres-updater.service`); `sudo bobres config set
  BOBRES_UPDATE_FROM_DASHBOARD off` removes them.
- After the update every customer gets a new bottom menu once, with their next message (a new row: Become a reseller,
  and Top users while the referral program is on).
- The CSV files of the reports, the ledger and the audit log have new columns at the end and `\r\n` line ends; a script
  that reads them by column name keeps working.
- Core and the bot move together (the bot asks core a new question and reads a new field of the settings): update both,
  as `sudo bobres update` does.

### Known limits
- **Updating from the dashboard has not run end to end.** The server's updater was tried on a lab server with a real
  systemd and Docker (Ubuntu 24.04), with core's half emulated; core's half was tried against the updater's protocol and a
  fake folder, and the two meet in a test of the protocol. If System > Updates does not work for you, `sudo bobres update`
  over SSH always does. Ubuntu 22.04, Debian 12 and arm64 are untried, and a rollback that restores the database is
  covered by unit tests only.
- Nothing of the bot's new features, the Mini App or the dashboard changes has been seen in a real Telegram. The Persian
  texts of the new features are a first draft and want a native read (every one can be edited).
- Top users shows the customers' names and Telegram ids only masked, and there is no board in the Mini App.
- A customer with another open conversation with support must close it before applying to be a reseller (a customer has
  one open ticket at a time).
- A flood of messages over the bot's rate limit (`limits.bot_per_10s`) stays unanswered, in maintenance as at any time.
- The CSV files were checked as files (byte by byte and with Go's reader), not opened in Excel or LibreOffice.

### Updating from the dashboard: System > Updates
- **System > Updates can now update the install**: the owner confirms and core asks the server's updater (a root systemd
  path unit installed by `bobres install` and `bobres update`) to run the same signed, backed-up, self-rolling-back
  `bobres update`. Core never gets the Docker socket. The page shows the steps, the updater's sentence and (to the owner)
  its log, copes with core restarting in the middle, and offers a reload when the new version is up. A request, its
  outcome and a Telegram notice to every owner are recorded (`system.update.requested` and `system.update.<outcome>` in
  the audit log, a new `system.update.v1` event). An install without the updater keeps the `sudo bobres update` command.
- **The update channel is a setting** (stable or beta, owner only, `updates.channel`) instead of only `BOBRES_UPDATE_CHANNEL`;
  unset, the server's variable still decides. A pre-release needs its own confirmation.
- **An update that was cut off does not block the next one.** If the server loses power in the middle of an update, the
  updater's status keeps saying "running"; the dashboard now takes a status the updater has not rewritten for 10
  minutes (it does every 30 seconds) for an interrupted update, records it as failed, tells the owners and lets another
  be asked for. Without it the page would wait for ever and only `sudo bobres update` on the server could clear it.
- **Fixed: "Check again" and the update buttons on System > Updates did nothing useful.** "Check again" only re-read the
  bot's cached answer (`GET /api/v1/system`), because core had no way to make the bot look again, so "Last checked" never
  moved and a failed check (GitHub unreachable when the bot started) stayed for an hour. It now calls
  `POST /api/v1/system/updates/check`, which has the bot reload its settings and look at GitHub at once, and shows when
  it looked and why a look failed (GitHub's own words included). The update button was never one: the page only showed
  `sudo bobres update` with a copy button that did nothing at all in a browser without a clipboard (a dashboard opened
  over plain http), and the command ignored the channel the page showed. The command now names the channel, copying
  falls back to the older way and says when it cannot, and there is an Update now.
- New core settings and files: `BOBRES_UPDATE_DIR` (default `/run/bobres-update`), migration 00025 (`system_updates`).
  Updating from the dashboard needs the server's updater (below) and the `./run/update` mount in `docker-compose.yml`;
  [docs/02-events-and-apis.md](docs/02-events-and-apis.md) has the protocol.

### Updating from the dashboard: the server's updater
- **A server can now be updated without logging in to it.** `bobres install` (so every `bobres update`) sets up two
  systemd units, `bobres-updater.path` and `bobres-updater.service`, and `bobres update --from-request` (not in the
  help) is what the service runs: it reads the one request the dashboard dropped into `/opt/bobres/run/update`,
  deletes it, takes the same install lock as `update`, `rollback` and `restore`, and runs exactly what
  `bobres update --yes --channel C [--version V]` runs (signed release, backup, restore point, install, health check,
  automatic rollback). It reports each step in `run/update/status.json`, which the dashboard shows.
- **The request carries only a channel (`stable` or `beta`) and a release name** and is checked as strictly as it can
  be: the file is opened without following a link, must be a regular file of core's user of 2 KiB at most, has exactly
  the protocol's fields, is dated within 15 minutes, and an id seen before is ignored. A compromised dashboard can at
  worst start an update to a release the vendor signed, at a time the owner did not choose.
- **The channel chosen in the dashboard is kept**: after a successful run `BOBRES_UPDATE_CHANNEL` in `.env` follows the
  request's channel, so `sudo bobres update` and the menu agree with it.
- Core's container gets one new mount, `run/update` (read-write), and no other power over the host; `bobres install`
  makes the directory (owned by core's user, mode 0700).
- `sudo bobres check` and `bobres doctor` have a "Dashboard updates" line (WARN, never FAIL). `sudo bobres config set
  BOBRES_UPDATE_FROM_DASHBOARD off` removes the units, `on` installs them, and `bobres uninstall --purge` removes them
  with the install.
- `bobres update`, `bobres rollback` and `bobres restore` now take one lock (`/opt/bobres/.update.lock`): a second one
  that starts while another runs says so and changes nothing.

### Reports: the calendar and the CSV files
- **The Calendar switch of the Reports page changes every date, both ways.** It used to cut Jalali weeks and months
  while the dates themselves followed the language (Jalali in Persian, Gregorian in English) and the custom range was a
  browser date field, which is Gregorian whatever the page says. Now the days covered, the chart axes, tooltips and
  summaries, the table, the week and month names and the **From** and **To** fields all follow the switch at once,
  with Persian digits in Persian and Latin in English. A custom range is typed (`1405/07/22`, `14050722`, Persian or
  Latin digits) or picked from a month grid in the calendar shown (Saturday-first in Jalali, Monday-first in Gregorian,
  usable from the keyboard) and converted exactly to the Gregorian days the API takes: month lengths, the leap days
  (30 Esfand 1403), the new year, and "today" in the store's time zone, never the browser's; a date the calendar does
  not have, one outside 2000 to 2100 or an end before the start is not sent and the page says what a date looks like.
  The choice is remembered in the browser. The six range buttons wrap on a phone instead of widening the page.
- **CSV files, reviewed one by one** (`summary.csv`, `orders.csv`, `ledger.csv`, `audit.csv`: every CSV the product
  makes). One set of rules now, documented in [docs/14-reports.md](docs/14-reports.md#csv-files) and tested as a
  spreadsheet sees the files:
  - one kind of line break: rows and line breaks inside a cell are all `\r\n` (rows were `\n` with raw `\r\n`, `\n` or
    `\r` left in cells);
  - dates: instants in UTC (ISO 8601, `Z`), days as Gregorian ISO in the store's zone, and a **`tz`** column on every
    file that has local times or days (`paid_at_local` had no zone, `ledger.csv` and `audit.csv` had UTC only, so
    `time_local` is new there); with `calendar=jalali` one more column at the end carries the Jalali date in Latin
    digits (`period_start_jalali`, `paid_at_jalali`, `time_jalali`);
  - `summary.csv` says what its rows are about (new last columns `group`, `calendar`, `tz`); all headers are English
    and stable, new columns only at the end, and a test pins every one;
  - file names carry the days covered in the file's calendar (`ledger-2026-10-14-to-2026-10-20.csv`,
    `reports-summary-1405-08-09-to-1405-08-12-by-day-jalali.csv`) and keep to ASCII safe characters; `ledger.csv` and
    `audit.csv` were named by a UTC time of day;
  - formulas: the rule that quotes a text a spreadsheet could run as a formula skipped ASCII spaces, tabs and line
    breaks only; it now looks past any Unicode space, control character and invisible mark (the right-to-left mark
    Persian text carries, a no-break space) in front of `=`, `+`, `-` or `@`. Numbers are never quoted, so a negative
    amount stays a number; a cell is cut at 32,000 characters (a spreadsheet moves the rest to the next row);
  - `ledger.csv` and `audit.csv` take `tz` and `calendar` (a 400 for an unknown one); the ledger, the audit log and a
    reseller's statement ask for the Jalali column when the Reports page is set to Jalali;
  - checked and fine: the byte-order mark and content type, exact decimals with no thousands separator and the
    currency on every row, who may export (owner and admin; support gets 403), streaming and the row cap, nothing secret
    in any file, and that the rows add up to the report's totals (new tests).

### The bot holds maintenance mode in every option
- **Maintenance mode now holds the whole bot.** With `maintenance.enabled` on, the maintenance text did not come from
  every option, Support among them (the owner reported it). Three things let a customer past it: the text was sent at
  most once a minute per customer, so the second tap on the bottom menu within a minute got no answer at all; a
  customer who had written to support went on with their ticket (Support in the bottom menu showed it, and their
  messages, photos, files, voice messages and albums, their Telegram replies to a staff answer, and the Reply, Close
  and history buttons all worked); and the language buttons opened the picker, switched the language and brought back
  the home menu and the bottom menu. Now one decision (`maintenancePasses` in `internal/bot/handler/gates.go`), made
  before any command, button, scene or conversation sees the update, answers every update of a customer with the
  maintenance text, every time: a popup for a button, a message for anything typed or sent (an album, which Telegram
  delivers a part at a time, gets one). The owner's own text for the message ("maintenance" under Branding and texts)
  is used when set. What still works is a short list and nothing else: staff and the owners the server names (also
  before they have an account), `/paysupport`, a payment already under way (its receipt, bank reference or TXID, its
  Check button, a Stars invoice already sent) and a newcomer's `/start` and language choice. Maintenance still comes
  before the required channel, and switching either on or off takes hold at the bot's next settings reload (at once
  for a change in the dashboard or by `/set`), never at a restart.
- **A customer can no longer write to support while maintenance is on**, or answer a staff reply: they are told the
  bot is under maintenance, and what they sent is not saved. Staff answers to open tickets still reach them. The
  required channel is not changed: it still lets a customer go on with a ticket they have (starting one asks them to
  join first) and still lets anyone's language picker through.
- Tests: every command, button verb, bottom-menu item and scene is sent to the bot in maintenance, twice in a row,
  from the state a customer would have been in, and a test reads the handler's source and fails when one is missing
  from its tables. The Mini App, which already held the same rule, got the same treatment: its routes are read from the
  source and swept for a customer, for each staff role and for an owner the server names; its three copies of the rule
  are one function now. No change of behavior there, and nothing for the dashboard (staff only) or a service's own
  page (`/sub/<token>`, which keeps working) to change.

### The bot: growth and account features
Picked by the owner from another store's bot: a Top users board, account lines on the wallet screen, a card to forward in
Invite friends, the commands `/new`, `/renew` and `/status`, and a reseller application. Every text is in Persian and
English and can be edited like the others.

#### Telegram bot
- **Top users.** A button on the home screen and in the bottom menu opens the board of the customers who invited the most
  people, five to a page (Others pages on, to rank 50), each place with the rank, a masked name and Telegram id (the first
  and last three characters around `***`: `Par***mad (181***314)`; `***` for no username; a short name shows less, so that
  at least three characters always stay hidden) and the number of invites, the viewer's own count on a button (which
  opens their Invite friends screen) and their place marked when it is on the page. Core masks the name and the id, so
  the bot never holds a whole one. Staff, resellers (their invites are their commission link) and banned users are never
  on it; a tie goes to whoever joined first. It is shown only while the referral program is on
  (`referral.reward_percent` above 0), and the owner can switch it off with **`growth.leaderboard.enabled`** (Settings,
  Growth; on by default). New core call `GetTopInviters`: one query for every customer, kept for 30 seconds.
- **Account lines on the wallet screen**: the customer's numeric Telegram id, how many services they have and the day
  they joined the bot (Jalali for Persian, Gregorian for English), above the balance and the buttons.
- **A card to forward in Invite friends** (and a reseller's My link): besides its screen, a message of its own with the
  store's promo text ("get safe, fast services without interruption through the bot") ending in the customer's own invite
  link as a text link, then a line telling them to forward it to their friends. The promo text, the link's label and the
  line are texts the owner can edit (`ref.promo`, `ref.promo_link`, `ref.promo_hint`). The Share button stays.
- **`/new`, `/renew` and `/status`**: Buy, a list of the services that can be renewed (each opens its plans), and My
  services, in the command menu with Persian and English descriptions. They pass the gates as the buttons do (maintenance,
  the required channel, the language not chosen yet), and a command is never taken as the answer to an open question (a
  ticket message, a discount code, a bank reference): it closes the question and does what it says.
- **Become a reseller.** A button for customers who are not resellers or staff, shown while the license includes
  resellers, asks for one message about the applicant and files it as a support ticket in a new **reseller** category.
  Staff get the usual new-ticket alert, worded as an application, with the applicant and a button that opens the ticket
  (the `notify.new_ticket` switch applies); the customer is told its number and to wait for an admin, who answers from the
  dashboard. One open application per customer, and one open ticket at a time: another open conversation is named and
  asked to be closed first. Core refuses an application from a reseller, staff, a banned customer or while the license has
  no resellers, and a customer is never told about licensing. Making the applicant a reseller stays the Resellers page.
- Core: `Settings.resellers_enabled`, `GetTopInviters` and `AddTicketMessage` with category `reseller` are additive
  (`buf breaking` is clean).

#### Dashboard
- The support inbox lists and filters by the new category **Reseller application**, and the ticket page names it, in
  Persian and English.
- Settings has a **Growth** card with the Top users switch.

#### Telegram Mini App
- A ticket's category is shown on its card and in its conversation, **Reseller application** included. The topics to write
  under stay Payment, Service and Other: an application is made from the bot's button, and a message from the app that
  names the reseller category is filed as Other.

## [0.6.1] — 2026-10-20

Two gaps of the Mini App that the 0.6.0 notes named are closed: it now holds the store's required channel as the bot
does, and a customer can take up a manual payment again after leaving its screen.

### Updating
- `sudo bobres update` does it (core and the bot move together: core now asks the bot a new question). There is no
  database migration and no change to the services' calls. Nothing has to be configured.
- **A store that already has a required channel (`join.channel`) has it enforced in the app after this update**, which
  is the point of it: a customer who is not in the channel sees a join screen until they join. A store whose bot is not
  an administrator of the channel keeps letting everyone in, with a warning in core's log once a minute (look for it
  if the gate should be on and is not).
- Not tried in a real Telegram yet, like the rest of the Mini App: the join screen and the payments list are checks 12
  and 13 of the hand-check list in [docs/17-mini-app.md](docs/17-mini-app.md).

### Telegram Mini App
- **The Mini App now holds the store's required channel** (`join.channel`), as the bot does: a customer who is not in
  it can no longer skip the gate by opening the app. Core answers a sign-in, and any later call except what a payment
  under way needs, with `403 join_required` and the channel's public link (`join_url`), and the app shows a full-screen
  gate, in Persian and English, with **Join the channel** and **I've joined** (which signs in again and asks at once).
  Only the bot can ask Telegram who is in a channel, so core asks it, through a new internal endpoint of the bot
  (`POST /internal/miniapp/channel-member`, core's token, never routed from outside; additive).
- The bot's own rules, copied: staff and the configured owners are never asked, and nobody is while the store is in
  maintenance; a confirmed member is remembered for 10 minutes and a customer outside the channel never, so "I've
  joined" works at once; and **when Telegram cannot tell** (the bot is not the channel's administrator, no such
  channel, a timeout) **the customer is let in**, as the bot lets them in, with a warning in core's log once a minute
  and no new question about that customer for a minute. The owner turning the requirement on stops the app's open
  sessions at their next call; a member who leaves the channel is stopped within 10 minutes.
- Updating: the bot and core move together (`sudo bobres update`). A core with a bot too old to know the question
  lets everyone in and logs the warning, as when Telegram cannot answer. Nothing to configure. Not tried in a real
  Telegram: [17-mini-app.md](docs/17-mini-app.md), checks 12 and 13.
- Not as in the bot: a customer outside the channel cannot go on with a support conversation in the app (they can in
  the bot), and one who has to sign in again cannot send a payment proof from the app.
- **A manual payment left half-way can be taken up again.** When Telegram reloaded the app (or the customer closed it)
  between paying at the bank and sending the receipt, the screen that showed the card, the addresses or the link and
  took the proof was gone, and reopening a plan made a new order. The Wallet tab now lists the customer's manual payments
  that still wait for their receipt ("Payments waiting for your receipt", the newest 10): the method, whether it is for
  a purchase or a top-up, the amount, the payment reference and how long ago it began, with a **Send the receipt**
  button that opens the same payment screen again and ends in the existing proof call. In Persian and English.
- New call `GET /api/mini/v1/payments/pending`, served in maintenance like the proof call and counted in the usual
  limits. It lists only the signed-in customer's own manual payments that are still pending, of a method the store
  still offers and, when they have an order, of an order that still waits for payment; it takes nothing from the
  request, and shows the price the payment has and the card, addresses or link the store has now. It uses the payments
  service's existing history filter: no database migration, no change to the services' calls.

### Known limits
- A customer outside the required channel cannot go on with a support conversation in the app (they can in the bot),
  and one who has to sign in again cannot send a payment proof from the app.
- The bot itself has no list of a customer's unpaid manual payments: a payment started in the bot is listed in the app,
  but the bot's proof flow goes on only with the payment its conversation is on.
- If support pauses a service its reseller had already paused, nothing is recorded, so the reseller can still resume it.
- Nothing of the Mini App has been tried in a real Telegram yet (see the 0.6.0 notes).

## [0.6.0] — 2026-10-20

The Telegram Mini App is on by default and now reaches most of what the bot does (the free trial, invites, support,
Telegram Stars, card to card with receipts, and a reseller panel); resellers can manage their own customers' services;
and every client the bot makes in a 3x-ui panel is put in the group `bobres_bot`.

### Updating
- `sudo bobres update` does it: all the services move together (the bot and core call each other through new
  endpoints, so they must not be left on different versions). Two small database
  migrations run by themselves after the update's backup: core 00024 (a `note` column on services) and provisioner
  00005 (a `group_backfilled_at` column on the 3x-ui servers). `sudo bobres rollback` undoes the update with the
  database from before it. Nothing has to be configured.
- **Every store has the Mini App after the update**: it was off until switched on, and is on now. It opens from a Store
  button on the bot's home screen, a Store item in its bottom menu and Telegram's menu button, which the bot adds by
  itself, and only when the store's address is a public HTTPS one. Customers get the new bottom menu once, with their
  next message. To keep the app closed, turn off Settings, General, "Telegram Mini App store": the buttons go within a
  minute and `/app/` answers "not found".
- The provisioner puts the clients the bot made before into the 3x-ui group `bobres_bot` in the background after it
  starts. Its log has a line per 3x-ui server when it is done
  (`3x-ui groups: backfill finished`). Nobody is disconnected, and a client you moved to another group by hand stays there.
- After updating, open the bot on a phone and walk through the Mini App checks (the last section of
  [docs/17-mini-app.md](docs/17-mini-app.md) and item 34 of [docs/launch-checklist.md](docs/launch-checklist.md)): the
  Store buttons, the screens, a Stars payment and a receipt upload have not been tried in a real Telegram.

### Telegram Mini App
- The Mini App store is **on by default**, for every store: `miniapp.enabled` used to be off until set, and is now on
  unless the owner switches it off, so a store that never touched it has the app after `bobres update`. The bot opens
  it from a **Store** button on its home screen, a **Store** item in its bottom menu (it answers with the button that
  opens the app: Telegram gives a Mini App opened from a reply keyboard button no sign-in data) and Telegram's own
  menu button, and sets them itself: there is nothing to configure in @BotFather. They appear only when the store's
  address is a public HTTPS one (a domain, or a public IP address; not plain http, `localhost` or a private name,
  which Telegram could not open) and go again when the app is switched off (the bot only takes back a menu button it
  set itself).
- New in the app: the **free trial** (the bot's trial: one per account, whichever door it was taken through),
  **invite friends** (the customer's own link, how many joined and bought, what it earned) and **support** (write to
  staff, follow the conversation, close it; the same inbox as the bot and the same daily limit, which the app counts
  from the database, so it includes the messages sent through the bot).
  They are sections of the More tab and plug in by themselves ([17-mini-app.md](docs/17-mini-app.md)).
- Core passes the app's address to the bot with its settings (`miniapp_url`, an additive field of `Settings`); a bot
  older than core simply shows no button. The other way round, the bot tells core its own @username with every sign-in
  check, so the app's invite link and "open the bot" button work without `branding.bot_username` (which still wins
  when set).

### Mini App payments
- The Mini App store now takes **Telegram Stars** and the **manual payment methods** (card to card, USDT by hand, a
  Zarinpal payment link, each with a receipt), besides Zarinpal, IDPay, NextPay and automatic USDT. `GET /api/mini/v1/methods`
  lists exactly what the store offers for the amount and currency, by the bot's own rule (`payments.*` settings and
  switches), and `pay` and `topup` accept each of them, creating the payment as the bot does (the same keys and
  statuses, so a payment started in the bot goes on in the app and the other way round).
- **Stars:** the bot has a new internal endpoint, `POST /internal/miniapp/invoice`, that makes an invoice link with the
  Bot API's `createInvoiceLink` (the same title, description, payload and XTR price as the bot's invoice message; new
  `CreateInvoiceLink` in `internal/bot/tg`). The app opens it with `Telegram.WebApp.openInvoice`; the bot's existing
  pre-checkout and successful-payment handlers settle it, and a test shows they do for a link (no message of the bot's
  needed). `internal/tgfake` makes invoice links and can play a customer paying one.
- **Manual payments:** the app shows the card, the USDT addresses and the exact amount in USDT, or the Zarinpal link,
  with the payment reference and a copy button for each, and `POST /api/mini/v1/payments/{id}/proof` takes the proof
  once: a receipt image (JPEG, PNG or WebP by its bytes, 8 MB at most) with the bank's tracking number, or a
  transaction hash. It ends in the same domain call the bot's proof flow ends in, so the review list, the possible
  duplicate warning and the admin card with the receipt work unchanged. Core keeps no file and holds no Telegram
  token: the bot (`POST /internal/miniapp/receipt`) sends the image to the customer's own chat and core stores the
  Telegram file id, like the bot's own receipts. A third endpoint, `POST /internal/miniapp/proof-alert`, has the bot
  tell the staff (every owner) about a proof, as it does for one that comes in its chat. The app shrinks big phone
  photos before sending them.
- Limits and care: every call acts on the customer's own order and payment (another's is a 404, also for the
  payment check, which used to answer 500, as it did for an id that is not a payment id), prices come from the order, a
  payment starts only by a method on offer, a proof is accepted once (also in maintenance, so that a customer who has
  paid can say so, as in the bot), a receipt is no more than 8 MB and 40 megapixels (read from the header, WebP
  included), uploads are limited to 6 in 10 minutes and one at a time for a customer on top of the write limit (4 in
  all), and no receipt or file id is ever logged or audited. See [docs/17-mini-app.md](docs/17-mini-app.md) for the
  API, the new error codes and what only a real Telegram can prove.
- Fixed: the Mini App showed the amount of an automatic USDT payment rounded to cents ("Send exactly 2.5 USDT" for a
  payment of 2.500731, whose last digits are what tells it from the others, so it was not matched and waited for the
  staff); it shows every decimal now, as the bot does. The bot's "payment received" message for a top-up paid by Stars
  says the wallet was credited (it said a service is being prepared), and the pre-checkout refusal no longer sends a
  Mini App customer "to the bot".
- Updating: no migration and no change to the gRPC API. The new bot endpoints (`/internal/miniapp/invoice`, `/receipt`,
  `/proof-alert`) are called by core, so update core and the bot together with `sudo bobres update`; a core newer than
  its bot would list Stars and the manual methods and answer "Telegram could not be reached" when one is picked.

### Reseller management
- Resellers manage their customers' services, in the bot and in the Telegram Mini App (a new **Reseller panel** section
  in its More tab, Persian first and English). A reseller can **pause and resume** a service (the customer's link stops
  and starts again), **delete** one after an explicit confirmation (no refund), keep a private **note** next to the
  name, filter the list by group (all, active, ending soon, ended, paused) with a count on each group, and see the
  **customers who joined through their invite link** (their @username or Telegram id, the day they joined, paid orders,
  what they spent and the commission it paid, newest first). A reseller still cannot reset a service's traffic or give
  free days or free traffic: those stay with staff, and a service **support** paused cannot be resumed by its reseller.
- The Mini App's reseller section: the panel (tier, discount, commission, balance with credit and what can be spent,
  the invite link with a Share button), buying for a customer (plans at the reseller's price, a name for the service,
  then the shop's checkout with the credit counted in the wallet button and no discount-code field), the customers'
  services with renew, add traffic, rename, note, pause, resume and delete, the customers from the link, and the
  statement. `POST /api/mini/v1/orders` takes an optional `label` (a reseller's name for the new service; ignored for
  anyone else), and the wallets in `/me` and `/wallet` carry `credit_limit`.
- Every call is the reseller's own and is checked by core each time: an active reseller on an install whose license
  includes resellers, a service that is theirs (anything else is 404 and changes nothing), no price or amount from the
  client, the shared Mini App rate limits. A reseller after `resellers` left the license keeps their account and their
  services, renewals and the plain list in the bot, but the new powers answer 403 (the Mini App section is hidden) with
  no word about licensing. Pausing and deleting are in the audit log under the reseller's name, source `bot`.
- Updating: one database migration, 00024 (a nullable `note` column on `subscriptions`, with a down migration), and
  additive gRPC calls (`ListResellerServices`, `SetResellerServiceEnabled`, `DeleteResellerService`,
  `SetSubscriptionNote`, `ListInvitedCustomers`, `Subscription.note`): update core and the bot together with
  `sudo bobres update`. An older bot keeps working; a newer bot against an older core shows the plain list.

### 3x-ui groups
- Every client the bot makes in a 3x-ui panel is now in the panel's client group **`bobres_bot`** (3x-ui v3.8.5 and
  v3.9.0, the versions BOBRES is tested with, have client groups: a group list with counts, and a group filter on the
  Clients page), so one filter shows everything the bot made. The group is only a label: a client's links, limits and
  subscription are the same as before. A renewal, new limits, switching a client off and on, and a traffic reset all
  keep it. A client you move to another group by hand stays there: BOBRES never gives a group to a client that has one.
- The clients made before this version are put in the group once, by the provisioner, after the update. Only the
  clients BOBRES holds in its own records (the ones it made on that server) that are in **no** group are touched; a
  client of the panel's other users, and one you put in a group of your own, are left as they are (a client you took
  out of every group by hand before this run is put back). It runs when the provisioner starts, when a server is added
  and when a server that did not answer answers again, and is tried again every 5 minutes for a panel that is down; it
  is safe to repeat and is recorded per server (`provisioner.xui_servers.group_backfilled_at`, a column added when the
  provisioner starts), so it does not run on every start. The provisioner's log has one line per server when it is done,
  `3x-ui groups: backfill finished`, with `tagged`, `already_in_a_group` and `missing` (clients BOBRES holds that the
  panel does not have). Marzban 0.8 has no groups: nothing changes on a Marzban server. Tagging does not restart the
  panel's Xray, so nobody is disconnected (watched on both versions: the panel's restart count did not move while 200
  clients were put in the group and taken out again). A panel without client groups (older than the tested versions,
  not tried) cannot finish the backfill: the provisioner logs why, once, and tries again every 5 minutes.
- Checked on real panels, v3.8.5 and v3.9.0, which behave alike here, in CI's "3x-ui integration" job (it now also
  runs the provisioner's tests, with a database): a new client is in the group; a renewal, new limits, off and on and a
  traffic reset keep it; the backfill tags old clients, skips one in another group, and ignores a client BOBRES does
  not hold. What the code relies on, and the tests assert so that a 3x-ui release that changes it fails CI first:
  - `clients/update` replaces the whole client row and **clears the group of a body that has none**. BOBRES sends the
    stored row back with its group; code that sent a row without it would have emptied the group at the first renewal
    or switch-off.
  - `clients/groups/bulkAdd` sets the group of the clients it is given **whatever group they have**, ignores an email it
    does not know, and counts only what changed. So the backfill lists first and sends only the clients with no group,
    at most 200 emails a call.
  - A create of a client the panel has (same subscription id, on the inbounds it is on) is a success that changes
    nothing, group included. The retry of a create that was started before this version puts the client in the group
    itself.
  - The panel lists 200 clients a page at most, and its whole listing is about 750 bytes a client, so the backfill reads
    the paged listing (`clients/list/paged`), not `clients/list`.
  - A group made by a client's own label goes from the group list with its last client; one that `bulkAdd` made for a
    new name stays, empty. Attaching and detaching a client leaves its group.
- The in-memory 3x-ui fake used by the tests (`internal/xuifake`) follows all of this now, with its own tests.

### Known limits
- Nothing of the Mini App has been tried in a real Telegram yet: the sign-in with a real bot token, the Store buttons,
  the Stars payment sheet (and that Telegram reports a payment made through an invoice link to the bot as it does for an
  invoice message), receipt uploads over a mobile network, the layout in Telegram's own WebView. Tests play Telegram
  with a fake ([17-mini-app.md](docs/17-mini-app.md), "Check by hand with a real Telegram").
- The Mini App does not enforce the store's required channel (`join.channel`) that the bot enforces.
- A manual payment cannot be taken up again once its screen was left (Telegram reloading the app between paying at the
  bank and sending the receipt): the customer starts a new order and support finds the old payment by its reference.
- If support pauses a service its reseller had already paused, nothing is recorded, so the reseller can still resume it.

## [0.5.0] — 2026-10-20

Several owners, a list of the payments waiting for review, and Marzban tested against a real panel.

### Updating
- `sudo bobres update` does it (core and the bot move together; there is no database migration). Nothing changes for a
  store with one owner. To name more owners: `sudo bobres admin owners add <telegram id>`; each opens the bot and sends
  /start once, or you run `sudo bobres admin password --id <telegram id>`.
- A "Review" button on an alert sent before the update still works: it opens the list of the payments to review.

### Owners and payment review
- Several owners (root admins). `BOBRES_ADMIN_TELEGRAM_ID` takes one Telegram id or several separated by commas (at
  most 10, none twice, no space inside an id: `12 34` is refused, not read as two people); a single id works exactly as
  before. Each is made an owner when core starts or when they first send /start, none can be edited, demoted or removed
  from the dashboard or the bot, and each can give the owner role to others. They are named only on the server, so a
  hijacked Telegram or dashboard account cannot make a permanent owner.
- `bobres admin owners list|add|remove [telegram id]` changes the list (validated like `bobres config set`, only core
  and the bot are recreated). `bobres menu` item 8 is now "Owners", `bobres install --admin-id 111,222` takes a list, and
  `bobres admin password --id N` gives the initial dashboard password to the owner N instead of the main one.
  Removing an id never demotes the account by itself (a typo must not lock the owner out); the command says so.
- The main owner (the first id) is the only one who takes no staff seat; the other owners take one each, as a second
  owner always did ([05-license.md](docs/05-license.md)).
- Every owner gets the payment, ticket and system alerts (until `notify.recipients = staff` widens them to every owner and
  admin) and the daily Telegram backup: it is uploaded once, to the main owner, and the others get it from Telegram by
  file id (an owner who cannot be reached is only logged).
- Payments to review, in the bot: "Review payments (N)" is a list, oldest first, eight to a page, each payment a button
  (who paid, how much, when, a warning sign for a possible duplicate) that opens its card with the receipt and Approve /
  Reject. The alert's Review button opens that very payment (it used to open the oldest). Every owner and admin sees
  the same list; a payment one of them decides leaves everyone's list, and an alert tapped later says who decided it and
  when. After a decision the list comes back.
- API: core's `AdminGetPayment` (new RPC, the proto stays compatible) shows one manual payment with who decided it and
  when; `ListStaffContacts`, the review calls and the dashboard are unchanged.

### Marzban
- Marzban is now tested against a real panel, not only a fake. CI has a job, "Marzban integration", that starts the
  `gozargah/marzban:v0.8.4` image (pinned by tag and digest) and runs against it: the client, the adapter, the
  dashboard's server pages and a whole purchase through core and the provisioner (add the server, buy, renew, top up,
  switch off and on, reset the traffic, delete). It sends real traffic through the panel's Shadowsocks inbound, so
  usage counting, a quota being enforced and a switched-off client being kept out are seen on the real Xray core, and it
  lets the panel's login token really expire. The job is not a required check. `tools/marzbandev` prepares a fresh
  panel, and `tools/marzbandev/local.sh up|test|down` runs the same panel and tests on a Mac or Linux machine with Docker.
- The version tested is **Marzban 0.8.4** (2025-01-09, Xray core 24.12.31), the newest stable release. The 1.0 line is
  still beta and does not work: v1.0.0-beta-3 lists its inbounds as an array, refuses the `SUDO_USERNAME` account
  outside debug mode, and has a full-screen admin tool. The server test now says that BOBRES works with Marzban 0.8
  instead of "not a Marzban panel".
- Where the real panel differed from the fake the adapter was written against, and what changed:
  - A client created without an `inbounds` object got no inbound, so no links at all; the adapter and the fake took
    it that the panel would attach them all. Every new client now names its inbounds, also on a plan that attaches
    all of them.
  - The username rule is stricter than the check was: at least three letters, digits or underscores before the first
    `-`, `.` or `@`. `u10-...` is fine; `u7-...` (a Telegram id of one digit; real ones have more) is refused by the
    panel. It is now refused before anything is reserved, with the rule in the message.
  - Putting a client that has never connected back on hold, after staff switched it off, failed with HTTP 422: the
    panel wants the hold duration in the same request. It is sent.
  - Resetting the traffic of a disabled client switched it on again (a slip in the panel's code), and turned an on-hold
    one into an active client with no expiry. The status is put back after the reset.
  - Deleting a client made through the panel's `SUDO_USERNAME` account (it has no owner admin) failed with HTTP 500
    after its row was gone, and the deleted account kept working in the running Xray core until the panel restarted.
    The adapter now switches such a client off first and counts that 500 as a deletion. Register an admin made with
    `marzban cli admin create` instead (it does not need to be a sudo admin).
  - A refusal sent as `{"detail": {"field": "message"}}` (HTTP 422) showed as "Unprocessable Entity"; it now names the
    field and the reason. The dashboard's address (`.../dashboard/`) entered as the panel's address gets a hint
    instead of "Method Not Allowed".
  - The fake in `internal/marzbanfake` now answers as the panel does in all of this, and in the smaller things: the
    hold duration is `null` for a client that is not on hold, the subscription link is `/sub/<token>` with a token made
    afresh on every read (all of them valid) and that route serves the share links, a missing and a bad login token get
    two different 401 texts, and a protocol the panel does not serve is a 400 that names the enum member.
- Good to know when adding a Marzban server (checked on 0.8.4): enter the panel's own address, not the dashboard's.
  Marzban listens on the server's loopback only unless it has its own TLS certificate and key (`UVICORN_SSL_CERTFILE`,
  `UVICORN_SSL_KEYFILE`), so BOBRES's containers cannot reach it until you put a reverse proxy in front or give it a
  certificate; BOBRES trusts the usual certificate authorities only, so a self-signed certificate is refused.
  Subscription links are the panel's own `/sub/<token>` ones, behind the "Subscription address" if you set it.

### Maintenance
- Go dependencies: `golang.org/x/term` 0.47.0, `github.com/redis/go-redis/v9` 9.23.0, `golang.org/x/crypto` 0.58.0 and
  `github.com/pressly/goose/v3` 3.29.0, with their indirect modules (`x/net`, `x/sync`, `x/sys`, `x/text`, `puddle`,
  `go-retry`, `go.uber.org/atomic`, `genproto/rpc`). The licence texts of all of them are unchanged;
  `THIRD_PARTY_NOTICES.md` lists the new versions. These replace the four open Dependabot pull requests for Go modules,
  whose CI failed on the notices check.
- The three tests that run `install.sh` through bash (`TestInstallSh*`) pass the script's path as an environment
  variable, so they also pass in a checkout whose path has spaces in it.
- README, `docs/ROADMAP.md` and `docs/PLAN.md` say what v0.4.0 contains and what is still open.

## [0.4.0] — 2026-10-17

### Marzban support
- A server is now 3x-ui or Marzban. Pick the panel software when adding a server (it is fixed afterwards); for Marzban
  enter an admin's `username:password` where 3x-ui takes an API token. It is stored encrypted in the provisioner like
  a token and never shown again. Existing servers are 3x-ui. Migration provisioner 00004 adds the `kind` column; going
  back down removes Marzban servers and their clients.
- The Marzban adapter creates, renews, resets, disables and deletes users and reads their usage through Marzban's API.
  Subscription links are the panel's own `subscription_url` (the "Subscription address" field only replaces its
  prefix). A Marzban username is the client email, so it must fit Marzban's username rule.
- Tested against a fake modelled on Marzban's source. A CI job against the real Marzban image is not in yet, so try a
  Marzban server with a test plan before selling on it. (Since then it has been tested against a real panel: see
  Unreleased, Marzban.)
- The dashboard says "panel servers" where it said "3x-ui servers".

### Dashboard
- Reports: which provider paid each order. The Paid-with table splits gateway and staff-approved sales by provider
  (Zarinpal, Stars, automatic USDT, card to card, crypto). Orders paid before this change show "provider not recorded"
  and still add up to revenue.
- `ledger.csv` and `audit.csv` exports are as careful as the reports files: no header-only file when the query fails,
  an `# export incomplete: <reason>` line when it dies half-way, a 60 s limit, at most two exports at once, and an
  audit entry with the outcome and row count. Text cells are quoted against spreadsheet formulas more strictly.

- Reports: Jalali weeks and months. `calendar=gregorian|jalali` on the report endpoints (default gregorian; anything
  else is a 400). Jalali weeks start on Saturday and months on Jalali day 1; a bucket's date stays its Gregorian start
  day. The Reports page has a Calendar switch (Jalali by default in Persian), names Jalali months by month and year,
  and carries the calendar into the CSV links. The summary CSV is named `...-jalali.csv` for Jalali, and the
  `export.reports` audit entry records the calendar.

- Reports now show churn: services lost (ended more than 7 days ago and not renewed), customers lost, and services at
  risk (ended, still inside the grace period), per day, week or month in both calendars. Trials, unlimited services
  and pending renewals are not counted. In the summary CSV the people rows gain `churned_services`,
  `churned_customers` and `at_risk_services` as the last three columns. The API gains `churn_grace_days` (0 to 90,
  default 7). Migration core 00020 adds an index on `subscriptions.expires_at`.
- Branded subscription page: each service has a page at `/sub/<token>` under the store's own brand: usage and days
  left, the subscription link and QR code, import buttons for v2rayNG, Hiddify, Streisand and V2Box (the link
  schemes are not yet verified on real devices), and a Renew button that opens the bot. Server-rendered with no
  JavaScript, in Persian and English, no personal data, an unguessable 128-bit address that can be regenerated from
  the dashboard. New settings `branding.sub_page.enabled` and `branding.bot_username`. Migration core 00021.
- IDPay and NextPay, two more Iranian card gateways built like Zarinpal: an API key in Payment gateways (write-only,
  encrypted; IDPay has a sandbox outside prod), callbacks at `/webhooks/idpay` and `/webhooks/nextpay` that are never
  trusted (every payment is settled by the gateway's verify call, with an amount check; a repeated callback changes
  nothing), watchdog alerts, licence gateway names `idpay` and `nextpay`, bot buttons and en/fa texts. Off until
  credentials are set. Implemented to the public documentation and tested only against fakes; see
  `docs/16-idpay-nextpay.md`. Payments migration 00008.

### Owner and operator tools
- `bobres` (no arguments) opens on a status screen: services up or down by name, version, licence tier and days left,
  HTTPS certificate days left, newest backup, dashboard address. `bobres status --json` prints the same facts.
- `bobres check [--dir D] [--json]` runs config, services, disk, database, Redis, licence, 3x-ui servers, watchdog
  alerts, HTTPS, backup age, release-signature expiry, bot token and webhook checks, and exits 1 on a failure. It is
  also the menu item "Check everything". The core side is `core check --json`. `install.sh` and `bobres update` now
  keep the verified `checksums.txt` in the state directory (a same-version re-signed release too), which the
  release-signature check reads; a server installed before this change shows SKIP until its next update.
- Owner sign-in without Telegram. `bobres admin password` (and a fresh `bobres install`) issues a random 16-character
  initial password for the login `owner`, shown once and stored only as a hash. It opens a restricted one-hour session
  where the dashboard shows only My account: choose a new password and set up two-factor authentication, then the rest
  unlocks. Running it again resets the login after a lost phone or password, ends the owner's sessions and lifts a
  lock. It also creates the owner's account when the owner never sent /start. Audited as
  `dashboard.password.reset_cli` and `dashboard.password.initial_changed`, never with the password.
- The first 3x-ui panel a new install adds is named after its host (for example `panel.example.com`) instead of
  `default`. Existing installs keep `default`.

### Plans
- A plan can name which inbounds of each 3x-ui server its customers are attached to. With no choice, nothing changes:
  the client goes on every inbound that has "attach" on. An explicit choice wins over the attach switch; if a chosen
  inbound is gone from the panel the purchase ends as `provision_failed` with a clear reason and the usual alert. The
  plan editor lists the inbounds of each chosen server and flags one that disappeared. Core migration 00023
  (`core.plan_inbounds`); `Plan` and `AdminUpsertPlan` carry the selection, and the provisioner's `CreateClient` takes
  it.

### Bot
- A complete button-driven admin menu. `/admin` now reaches plans (add, edit, archive), the trial, top-up packs,
  discounts, every setting (an editor by kind, explained, with range and default), notifications, broadcast, today's
  numbers, 3x-ui servers (switch, weight) and, for the owner, payment details and staff. Every input step explains what
  is wanted and has Back and Cancel. The old commands still work; `/set` with no arguments opens the settings screen.
  Eight new admin RPCs back it. Tested with fakes only.
- The BOBRES logo is the bot's default photo, applied once at first start. A photo you set, or remove, later is kept.

### Mini App
- A Telegram Mini App store at `/app/`: plans and discount codes, buying by wallet, Zarinpal, IDPay, NextPay or
  automatic USDT, my services with renew and extra traffic, wallet balance, history and top-up. Persian and English,
  right to left, Telegram theme. Off by default (`miniapp.enabled`). The bot checks Telegram's `initData` for core
  (`POST /internal/miniapp/verify`), so core never holds the bot token. Stars, card to card and trials stay in the bot.
  `make miniapp`, a CI job, `docs/17-mini-app.md`. Not checked against a real Telegram yet.

### API
- `SubscriptionLinks` has `page_url` (field 4); the bot's "service ready" message uses it for an Open page button.
- `order.paid.v1` has an optional `provider`; `/reports/methods` rows have a `provider` field; `orders.csv` has a new
  last column `provider`.
- `Settings.white_label` (core proto, field 3): the bot learns from core whether a white-label license is active.

### Security and release
- Postgres and Redis are pinned by digest in the compose files, and the backup image's Postgres base in the
  Dockerfile too (Caddy already was), so a moved tag changes nothing. `deploy/images_pinned_test.go` keeps them pinned.
  The digests change only by hand, with a version bump.
- The site shows the vendor license key's fingerprint (`vendor-v1-2026`, checked against `bobres about`).
- The four legal drafts are built as draft pages in `site/legal/` (`go run ./tools/sitelegal`), marked as drafts,
  `noindex`, not in the sitemap and not linked yet. They wait for the lawyer's review.

### Branding
- "Made by TECSO-DEV" is in the dashboard footer and login page and at the end of the bot's about text. A white-label
  license hides it, along with "Powered by BOBRES"; the credit is dropped from the bot's text when the text would pass
  120 characters.

## [0.3.0] — 2026-10-14

The installer and the command line, made clear and professional.

### Installer
- The installer has a new look. `install.sh`, `bobres install`, `bobres update` and `bobres menu` start with the
  BOBRES banner (the beaver, the version, the tagline, `© 2026 BOBRES, all rights reserved`, `Made by TECSO-DEV`, the
  license summary and the links), and the install shows one progress bar from 0 to 100% that continues from `install.sh` into
  `bobres install`: the percent is real (steps and images or services done), never goes down and reaches 100% only
  when the install is finished. Each step ends with a line that says OK, a warning or a failure and how long it took.
  `bobres version`, `bobres help`, `bobres status`, `bobres doctor` and the menu got the same colors and marks on a
  terminal.
- When the output is a pipe, a log, `NO_COLOR` or a dumb terminal (or with `--plain` or `BOBRES_PLAIN=1`) the installer
  prints plain ASCII lines like `[ 42%] Step 6/13: Pulling images`, with no colors and no redrawing. `--no-banner`
  or `BOBRES_NO_BANNER=1` leave the banner out. Commands whose output other programs read are unchanged.
- New command `bobres about`: the version, commit, license and copyright, the vendor license key's fingerprint, the
  store installed on this server and the documentation, support and security links in one place.
- What Docker's installer, apt and `docker compose` print no longer fills the screen: it goes to
  `/var/log/bobres-install.log`, and a failure shows the last lines and what to do. The install ends with a
  "BOBRES is ready" card: the address, how to log in, the health of every service, the license (the 30-day trial and
  its days left), three next steps (the first is to send /start to your bot, which makes you the owner) and the
  links.
- The wizard now asks only what has no safe default: the domain, your Telegram id, the bot token and, optionally, a
  3x-ui panel. The S3 backup questions and the hardening questions are gone (S3 stays available as flags, and the
  summary says how); a panel on the same server needs no question either. `--domain` and `--admin-id` can also come
  from `BOBRES_DOMAIN` and `BOBRES_ADMIN_TELEGRAM_ID`.
- The server is hardened by itself: the firewall (SSH, 80, 443), fail2ban for SSH, automatic security updates and a
  swap file on a small server, one line each. A step that is not safe (the SSH port not found, 3x-ui or another
  service on this server) is skipped with a warning and the command to run. `--no-harden` skips it all and
  `--ask-harden` brings back the questions. The installer never changes `sshd_config`, SSH password login or root
  login, and never restarts sshd.
- The 3x-ui panel is checked while you answer, before anything is written: it must be reachable (with the reason
  when it is not) and accept the API token (a version outside 3.8.5 to 3.9.0 is noted and used anyway). A refused token says what to do
  ("In 3x-ui open Settings, then API tokens (Security)...") and asks again; before, the install said everything was
  fine and the panel refused the token later.
- The install keeps going if your SSH connection drops, and you can follow it with
  `tail -f /var/log/bobres-install.log`. `sudo bobres admin link` before the owner has sent /start to the bot now says
  "Open @yourbot in Telegram and send /start once, then run this again."

## [0.2.1] — 2026-10-14

The daily sales summary, the second half of the sales reports.

### Bot
- A daily sales summary for the owner and admins, once a day in Telegram: yesterday's revenue per currency (never added
  together) with the change from the day before, paid orders, refunds, wallet top-ups, new customers and free trials, in
  Persian (Jalali date) or English. Payments staff rejected and failed gateway payments are listed only when there were
  some, and a day with no sales still gets a one-line message so you can see it is working. It is off until you turn it
  on and is queued once a day even if the server restarts (the bot sends each message at least once: a bot that is killed
  at the wrong moment can send one again). If the server was down at that time it is sent when it is back, never once for
  each missed day, and the bot drops a summary it could not send within a day. Turned on after its time of day, it starts
  the next day. A summary is not read before 00:02, so a purchase made just before midnight is in it; one that fails is
  tried again after 5, 10 and 15 minutes and given up after eight tries; a recipient Telegram refuses for good does not
  hold up the other messages; someone who stops being an owner or admin before the bot sends gets nothing; the change
  from the day before reads "about the same" under half a percent, never "down 100%" on a day with sales, and "up more
  than 999%" past that. If you change the store's time zone, a sale around the change can be in two summaries or in none,
  and the log says so ([docs/14-reports.md](docs/14-reports.md#the-daily-summary-in-telegram)).

### Dashboard
- Settings has a "Sales summary" card: a switch for the daily summary and the time of day it is sent (09:00 by default,
  in the store's time zone). The Reports page says whether the summary is on and links to those settings.

### API
- `GET /api/v1/reports/daily-summary` (`reports.read`) answers whether the daily summary is on, its time and the time
  zone. The settings `reports.daily_summary` and `reports.daily_summary_time` are in the settings API and the bot's
  `/set`. Core publishes `report.daily_summary.v1` to the bot, one event for each recipient. Migration 00019 adds a
  small table, `daily_summary_claims`, that remembers which day was sent.

## [0.2.0] — 2026-10-14

Sales reports: the first of the features after the first release ([docs/ROADMAP.md](docs/ROADMAP.md)).

### Dashboard
- A Reports section for the owner and admins (new permission `reports.read`): sales per day, week or month for
  today, the last 7 or 30 days, this or last month, or two dates; each currency on its own, never added together. It
  shows revenue, orders, new customers, renewals, free trials, wallet top-ups, refunds, payments staff rejected,
  failed or expired gateway payments (shown apart) and cancelled orders, a chart per currency, the top plans and how
  orders were paid, in Persian (Jalali dates) and English. Every number is explained in
  [docs/14-reports.md](docs/14-reports.md).
- CSV files of the summary and of the paid orders for the range, opened right by Excel in Persian and safe against
  formulas in names. A file that cannot be made says so (it never looks empty), and one that stops half way ends with a
  line that says it is not whole.
- Support no longer sees revenue on the Overview page: its cards are shown only to staff who may read the reports.

### API
- `GET /api/v1/reports/summary`, `/plans`, `/methods`, `/summary.csv` and `/orders.csv` (read-only, `reports.read`);
  exports are written to the audit log with how they ended; at most two run at once (429 with `Retry-After`); a
  report that takes too long answers 503 `timeout`. `GET /api/v1/overview` leaves `revenue` out unless the role has
  `reports.read`. Migration 00018 adds three indexes and no table.
- `payment.rejected.v1` events from core carry the payment's `provider`, so a rejection by staff and a failed gateway
  payment can be told apart.

### Operations
- `BOBRES_TIMEZONE` now reaches core too (the reports' days follow it when the Settings page names no zone), so
  `bobres config set BOBRES_TIMEZONE` recreates core as well as the bot and the backup service.

## [0.1.1] — 2026-10-14

A small fix after the first release.

### Dashboard
- English texts say "1 day" and "1 service", not "1 days" (plurals fixed in the whole dashboard, with a test that guards them).

## [0.1.0] — 2026-10-14

The first public release. It sums up what BOBRES does now: Phases 0 to 7 of the plan ([docs/PLAN.md](docs/PLAN.md) §10),
pull requests #13 to #48. Release candidates 0.1.0-rc.1 to rc.4 came first; rc.4 is this release's code. Everything below is new in this release.

### Bot
- Persian and English. Customers pick a language on their first `/start` and can change it.
- A bottom menu with the main actions stays under the chat, beside the inline menus. Buttons are colored by what
  they do: green to pay or confirm, blue for the main actions, red to cancel or refuse.
- Buy a plan: traffic-limited, time-only or both. An optional discount code, then a payment method, then the service
  arrives with its subscription link and a QR code.
- My services: status, expiry, traffic used with a bar, the link and the QR code. Renew with any plan (days and
  traffic are added) or add a traffic package; the subscription link stays the same.
- A wallet with top-ups and a history of transactions.
- One free trial per Telegram account.
- Invite friends: a personal link, and a reward in the wallet on the friend's first paid purchase.
- Reminders 3 days and 1 day before expiry, at 80% of the traffic and at the end, with Renew and Add traffic buttons.
  They come from a usage sync with the panel every 10 minutes.
- Support: one open conversation per customer, with text, photos and PDFs. Staff replies arrive in the chat with
  Reply and Close buttons; past conversations can be read.
- Store rules: maintenance mode, a required channel to join first, rate limits per user, `/terms`, and terms and
  privacy links on the support screen.
- Reseller panel: buy for a customer at the tier's price, on credit up to a limit; name a service and search by name;
  customer services, a statement and an invite link; notices of commissions and recorded payments.
- Staff in the bot: `/admin` (stats, payments to review, find a user, adjust a balance, ban or unban), `/set`,
  `/plan_add`, `/trial`, `/topup_add`, `/discount_add`, `/discounts`, `/discount_off`, and `/dashboard` for a one-time
  dashboard login link.
- Staff alerts: new payments to review, new tickets, failed deliveries, every sale (optional), and system problems.
  They go to the owner, or to every owner and admin.
- Long polling by default, so the bot needs no domain; webhook mode is optional.
- The bot keeps its own Telegram profile up to date, in Persian and English: menu commands (staff get theirs in
  their own chat), the description of an empty chat and the short description. The words are bot texts the store
  can change; a setting leaves them to @BotFather instead.
- The bot's profile photo is uploaded in the dashboard (Branding & texts → Store → Telegram bot): any PNG or JPEG
  becomes the square JPEG Telegram takes, and the bot sets it in Telegram when it changes (Bot API 9.4).

### Dashboard
- All 17 sections at `https://<domain>/admin`, built into the `core` binary (Vue 3, PrimeVue).
- Log in with a one-time link from the bot (`/dashboard`) or from the server (`bobres admin link`), or with a password
  that always needs an authenticator code. Sessions can be listed and revoked.
- Roles: owner, admin and support, with granular permissions. Persian (right to left) and English, dark and light.
- Overview: revenue, new users, active and expiring services, failed deliveries, payments to review, and the health
  of the 3x-ui servers and payment gateways.
- Customers and sales: users (ban, adjust a balance), services (extend, reset traffic, switch off, delete, read from
  the panel), plans and traffic packages, orders and payments (retry a delivery, refund to the wallet, the receipt
  review queue with the photos), the ledger with CSV export, discount codes and referral rules.
- Resellers: tiers with a discount or a fixed price per plan and a commission, credit limits, payments received, and
  each reseller's services, customers, statement and orders.
- Support inbox with saved replies and assignment; broadcasts in both languages, with an audience, a schedule, a rate
  limit, pause and resume.
- Store setup: branding (name, logo, colours, the bot's profile photo) and every bot text in both languages,
  settings, staff, and the audit log with CSV export.
- 3x-ui servers: several panels with a write-only token, weight, on/off for new services, version and health, a
  connection test, servers per plan, and an attach switch per inbound.
- Payment gateways (owner): Zarinpal and automatic USDT settings with a connection test and a report of the last 7 and
  30 days, unmatched USDT deposits, and the manual methods and Telegram Stars with their switches and details.
- System page: health and versions of every service with the open problems, the update check (with the channel and
  the license's coverage), the newest warnings and errors of every service (owner only), backups (list, run one,
  off-site copies), the HTTPS certificates, and the license (state, entitlements and their use, paste a token, or
  activate with a license key).
- HTTPS tab on the System page: everyone who sees the page sees the mode and, for each address the store is served on
  (the domain, the server's IP address, the Zarinpal domain), the issuer, expiry with the days left, whether browsers
  trust it, and the live error. The owner picks the mode: the domain's automatic certificate, a Let's Encrypt
  certificate for the server's IP address (with a Detect button and the warnings), or an own certificate and private key
  (files or paste). The page waits for the certificate to be issued and shows Caddy's own words when it refuses.
- A "Powered by BOBRES" footer on every install except one whose license includes white label (the free tier and
  the trial show it).
- The License tab opens with what applies now: the free tier, the trial with its days left, or the license's tier,
  then the entitlements with this install's use of each limit, and whom to contact to buy or renew a license. A chip
  in the menu and on the System tab says so too. A refused action in the dashboard names the limit and says to buy or
  renew a license.

### Payments
- The wallet, always on.
- Card to card, USDT (TRC20 or ERC20) and the owner's Zarinpal payment link: the customer sends a screenshot (or a
  TXID for USDT) and an admin approves it. A repeated bank reference is flagged.
- Zarinpal online (REST v4): off until the owner sets a merchant id. An optional proxy with a registered IP and a
  second, registered domain for the pay page. The payments service also asks Zarinpal about open payments itself
  (every 30 seconds for a new one, less often later); failed checks are recorded and reported.
- Telegram Stars, at the store's rate in Toman per Star.
- Automatic USDT on TRON (TRC20): each payment gets its own exact amount and 45 minutes to pay. A watcher reads the
  store's address through TronGrid every 30 seconds and settles an exact match; a late exact payment still settles
  within 48 hours. Any other transfer is recorded as an unmatched deposit and announced to staff. A transaction id
  pays only one payment, automatic or manual.
- Each method has an on/off switch. Payments already started go on when a method is switched off.
- Money is stored as whole numbers in minor units. Each payment settles exactly once, and the wallet ledger is
  append-only.

### Operations
- A one-command install on Ubuntu 22.04/24.04 or Debian 11/12/13 (amd64 or arm64). It verifies the signed CLI, asks
  for the domain, owner, bot token and an optional first 3x-ui panel, generates every secret and starts the store.
- The `bobres` menu and commands: status, logs, start, stop, restart, settings, doctor, admin link, uninstall.
- Several 3x-ui servers: a new service goes to the healthy, enabled server with the fewest clients per weight, among
  the plan's servers. Each server is checked every minute.
- Daily encrypted backups: the newest 7 kept on the server, each sent to the owner on Telegram (up to 50 MB), and
  optional copies in S3-compatible storage (Arvan, MinIO, Backblaze B2, AWS). `bobres backup` makes one now.
- `bobres restore`, from a file or from `s3://`: the file is verified first, the current data is backed up, and the
  database is replaced in one transaction.
- `bobres update` verifies the signed release, backs up, keeps a restore point and rolls back on its own when the new
  version does not come up healthy. `bobres rollback` undoes the last update. Stable and beta channels.
- System alerts on Telegram: a core watchdog checks the services, the 3x-ui servers, Zarinpal, automatic USDT,
  backups (and their off-site copies), disk, deliveries, the bot's backlog and the license every minute, and says
  when a problem is fixed.
- `bobres harden`: firewall, fail2ban for SSH, security updates and swap, each asked (also offered by the install).
- HTTPS certificates in three modes: Caddy's automatic certificate for the domain (the default), a Let's Encrypt
  certificate for the server's IP address (valid about 6 days, renewed by Caddy), and the owner's own certificate and
  key. The dashboard's HTTPS tab and `bobres ssl status|auto|ip|manual` (also in the menu, "HTTPS certificate") change
  it. A change is saved only once Caddy accepts it. `sudo bobres ssl auto` works without the dashboard when a wrong
  certificate locks browsers out. In the IP address mode, dashboard links move to the IP address only while its
  certificate is trusted and the bot sees the same public address (`BOBRES_PUBLIC_URL` overrides). `bobres doctor` has a
  TLS line for every served address (a crash-looping Caddy is a failure), and four alerts say when Caddy is not
  answering, a certificate is close to its end, an address serves no valid certificate, or the saved setting is not in
  use ([runbook](docs/runbooks/https.md)).
- The installer lays Caddy's files out as `caddy/Caddyfile` and `tls/`, makes Caddy read its config again after an
  update, and keeps the old top-level `Caddyfile` so `bobres rollback` still works across the change. The `tls-agent`
  service is in the releases from 0.1.0-rc.2 on: a rollback to 0.1.0-rc.1 has none.
- `bobres config set BOBRES_DOMAIN` and `BOBRES_ZARINPAL_PUBLIC_URL` recreate Caddy, core, payments and the `tls-agent`.
- `bobres config` for the safe settings and `bobres secrets rotate` for the bot token, database passwords, service
  tokens and the Redis password.
- `bobres support-bundle`: a redacted archive for support, without the database, backups or secret values.
- Runbooks for common incidents and routine jobs ([docs/runbooks](docs/runbooks/README.md)).
- Failure drills and a restore drill in the tests, and a load test with its numbers
  ([docs/12-load-test.md](docs/12-load-test.md)).
- Licenses: a signed token pasted on the dashboard or imported with `bobres license import`, or activated online with
  a license key (`bobres license activate`) and refreshed daily. The vendor's license server, `licensesrv`, is part of
  the repository ([docs/13-license-server.md](docs/13-license-server.md)).
- Free core, paid premium ([docs/05-license.md](docs/05-license.md)). An install without a valid license runs on a free
  tier: 1 server, 25 active services, 2 staff seats, no resellers, no automatic USDT, no white label, the Zarinpal,
  Stars and manual payment methods, and security updates always. Every install, fresh or upgraded, first has a 30-day
  trial (migration 00017) with unlimited servers and services, resellers, automatic USDT and 5 staff seats, but not
  white label. The limits refuse only new actions: a server, a new service (an order, a free trial or a reseller's
  purchase), a reseller, a staff member, a payment method. Customers' existing services keep working, renewing and
  topping up, and staff over a limit keep their access. A customer whose purchase is refused sees only "New purchases
  are temporarily unavailable. Please contact support."; staff get one `license_limit` alert a day, and a `trial_ending`
  alert in the trial's last 7 days and once when it ends. `bobres license status` shows what is in effect, the trial's
  end and the use of each limit.
- A license never leaves less than the free tier (a limit it omits, or sets lower, is raised to the free tier's, and the
  gateway lists are joined). The services on their way (being delivered, paid, or awaiting payment for up to 2 hours)
  count against the limit, which is checked in the order's own transaction so orders made together cannot overshoot it.
  Every staff member but the owner named in `.env` takes a seat, an owner too. A reseller's new purchase, and the
  premium payment methods offered to customers, follow the effective entitlements, so a lapsed install stops offering
  what the trial had switched on.
- A license's `updates_until` is advisory: `bobres update`, `--check`, the menu and the dashboard note that a release is
  newer than the license's update date and install it all the same. The seller's contact is the owner's
  (`BOBRES_SELLER_CONTACT`); none is built in.

### Security
- Four services, each with its own database role and service token; a service accepts only its legitimate callers.
  Only the provisioner holds the 3x-ui tokens, only payments holds the gateway secrets, and core has no internet
  access.
- Secrets at rest (3x-ui tokens, gateway settings, authenticator secrets) are encrypted with a master key per service.
  The dashboard never shows or logs them.
- Backups are encrypted with a backup key that never leaves the server. The backup service reads the database with a
  read-only role and reaches the internet only while S3 copies are set up.
- The installer, `bobres update` and the release use Ed25519 signatures, a signed key list and signature expiry.
  Images are signed with cosign and carry an SBOM; an install runs them by the digests built into the signed CLI,
  which the release workflow takes only from images whose signature it checked.
- The dashboard: CSRF tokens, authenticator codes for password logins, rate limits, a strict CSP, and an append-only
  audit log with a reason on money and staff actions.
- 3x-ui panels must use https; plain http is allowed only to a private address.
- Containers run as non-root, with a read-only filesystem and no capabilities. Caddy serves HTTPS and blocks the
  internal paths.
- The HTTPS manager keeps core away from the gateway. A `tls-agent` service with no network is the only writer of
  Caddy's TLS files and, besides Caddy, the only user of Caddy's admin socket. It checks every request again, accepts
  only the install's own domain and Zarinpal domain, and a change is committed only after Caddy accepts it. An own
  certificate's private key is sealed with core's master key, and never logged, audited or answered. Caddy's image is
  pinned by version and digest.
- Every install runs on a 30-day trial and then on the free tier, and a license only ever gates new actions: nothing
  that exists is removed.

### Selling BOBRES (the vendor side)
- The business model is decided: a free core and a paid subscription per install, monthly or yearly
  ([docs/pricing.md](docs/pricing.md)). The prices are the owner's: placeholders ("SET ME") in
  `internal/license/tiers.json` until they are set.
- The vendor's license-signing key `vendor-v1-2026` (fingerprint `2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7`) exists and
  its public key is built into BOBRES, so installs accept the tokens it signs. Its private half stays outside the
  repository. Key rotation by `kid` works, and a test pins the keys that are built in.
- Tier presets in one file, `internal/license/tiers.json`: `standard`, `pro` and a 30-day `trial`, with their
  entitlements and placeholder prices. `go run ./tools/license sign --tier standard --domain ... --customer ... --months 1`
  signs a license of a tier, and `licensesrv key create -customer ... -tier standard -months 1` issues one on the
  server. `licensesrv key update ... -extend-months 1` renews a subscription. `go run ./tools/license tiers`,
  `entitlements` and `fingerprint` list the tiers, print a tier's entitlements and a key's fingerprint. Every preset
  names every gateway and grants at least the free tier, and the `trial` preset is the implicit trial's entitlements
  (5 staff seats); tests pin both.
- The vendor tools warn, and still make the license, when `max_active_services` is 0 or missing (`tools/license sign`,
  `licensesrv key create` and `key update`): an install floors it at the free tier's 25.
- A deployment kit for the license server, `deploy/vendor/`: the server, Postgres and Caddy (HTTPS), hardened like a
  BOBRES install (non-root, read-only, no capabilities, internal networks), with the signing key as a mounted file outside
  the repository. The seller's runbook is [docs/runbooks/vendor-license-server.md](docs/runbooks/vendor-license-server.md):
  deploy, the manual sales flow, issue, renew, revoke, monitoring, backups, key rotation and a leaked key.
- A draft source license, [LICENSE](LICENSE): source-available, all rights reserved (a lawyer must review it), and
  [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for what ships, with a test that fails when it is out of date.
- Legal drafts for operators in [docs/legal](docs/legal/README.md), each in English and Persian: Terms of Service,
  Privacy Policy, Acceptable Use, a Refund policy for licenses, and a template for an operator's own store.
- [docs/launch-checklist.md](docs/launch-checklist.md): what the owner still has to do before selling.

### Brand
- BOBRES the beaver: the mascot is the logo. A flat icon, a 3D render, a waving animation, a banner, a bot avatar and a bot description picture,
  with a navy and teal palette ([docs/08-brand.md](docs/08-brand.md)).
- The dashboard's default look: the beaver favicon and logo, and the waving beaver on the login page (a still picture
  for reduced motion). A store's own logo replaces it.
- A small beaver in the `bobres` menu header.

### Known limits
- Not built: third-party crypto processors (they exclude Iran), TON, older 3x-ui adapters (only v3.x, tested on
  v3.8.5 and v3.9.0), a branded subscription page, re-encryption with new master keys, metrics dashboards.
- The license server is built and has a deployment kit, but it is not deployed: that is the owner's step
  ([docs/launch-checklist.md](docs/launch-checklist.md)).
- Image signatures are not checked on the server (the release workflow checks them before it builds their digests
  into the CLI). Postgres and Redis come from Docker Hub by tag, and Caddy by version and digest.
- The server IP address mode needs a public address with ports 80 and 443 reachable from the internet (not behind
  carrier-grade NAT). It cannot be tried in the local Docker lab. BOBRES never renews an own certificate.
- The legal review, the brand checks and the prices are open.
- The English dashboard texts do not handle plurals yet ("1 days", "1 services on 1 servers"). The Persian ones are fine. Fixed in 0.1.1.

[Unreleased]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.3.0
[0.2.1]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.2.1
[0.2.0]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.2.0
[0.1.1]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.1.1
[0.1.0]: https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.1.0
