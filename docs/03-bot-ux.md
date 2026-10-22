# 03 - Bot UX flows and menu map

All texts live in i18n files (fa + en), overridable per install from the dashboard. Persian is RTL:
avoid mixing raw English inside RTL lines; use consistent numerals; test long-line wrapping.

## Principles
- One primary action per screen; max 2 taps to the common goals.
- Inline keyboards edit the same message (no chat spam). Always a Back and a Home button.
- A bottom menu (a persistent reply keyboard) holds the main actions: Buy, My services, Wallet, Free trial,
  Support, then Invite friends (only when referrals are on; resellers get the Reseller panel instead) beside
  Language, then a row with Become a reseller (customers only, while the license includes resellers) and Top users
  (while the referral program and the Top users board are on); owners and admins also get Admin panel and Dashboard,
  support staff only Dashboard. A tap does what the same inline button does. It comes with the welcome, after a
  language change and with `/menu`, and again ahead of the next answer whenever it changed (role, language, an edited
  label, a switch that adds or takes away one of those buttons). A tap is never taken as the answer to an open
  question (a bank reference, a service name), and the gates treat it as any message.
- The commands `/new`, `/renew` and `/status` do what Buy, Renew and My services do. They are in the bot's command
  menu with a Persian and an English description (`profile.cmd.new`, `profile.cmd.renew`, `profile.cmd.status`, edited
  like any text), and they pass every gate exactly as the buttons do (see "Commands" below).
- Buttons are colored by what they do, the same on every keyboard: green pays or confirms, blue opens a main
  action, red cancels or refuses (Telegram has these three colors). Link buttons too: the payment link is
  green, sharing an invite link and joining the channel are blue. Navigation, lists and links to read (terms,
  a transaction, the dashboard) keep the app's look.
- Every error tells what happened and what to do next. No raw exceptions.
- Language chosen on first /start, changeable in Settings.
- Rate limit per user; ignore duplicate taps (callback idempotency).

## Main menu (user)
1. Buy service
2. My services
3. Wallet (the account lines, balance, top up, history, apply a gift code)
4. Free trial (once per user, abuse-checked)
5. Invite friends (referral link, earnings, a card to forward)
6. Support (tickets, FAQ)
7. Settings (language, notifications)
8. Top users (only while the referral program is on and the owner has not switched the board off)
9. Become a reseller (customers who are not resellers or staff, only while the license includes resellers)
10. Bulk purchase (a second green button right under Buy, for everyone, while the owner has not switched it off:
    setting `bulk.enabled`; it has no item in the bottom menu)
(+ Reseller panel button when role = reseller; + Admin button when role = admin)

## Key flows
**Buy:** plan list -> plan detail (price, GB, days) -> discount code (optional) -> payment method
(wallet / card / Zarinpal / Stars / crypto) -> confirm -> pay -> delivery message (subscription link, QR, connect guide per platform).
**Find a service by its link** (My services, **🔎 Find by link**: `fl`, `fr:<nonce>`; not for resellers, whose list is their customers' services with its own search):
for a customer who does not see a service in their list (it was made for them, or under another account). They send the subscription link (or
the address of its page, or the end of one); core looks it up by the subscription id or page token (`FindServiceByLink`: 8 to 64 of letters,
digits, `.` `_` `-`; a deleted service is not found). A service of their own opens the usual screen. Another person's shows **only what the link already
shows**: status, expiry, traffic and usage (no owner, e-mail, name or note) with **Renew** (and the page button); Renew lists the regular plans,
and the order is the **holder's own** (their wallet or gateway, their discount codes) for that service: core accepts a renewal (never a traffic
top-up) of a service that is not the buyer's only with the link's secret (`CreateOrder.link_secret`, kept in the saved checkout, never in a
button), compared in constant time, and checks it again at the quote and at the order. Back from the plans returns to the search.
**Reward codes:** the owner marks a plan in the dashboard (the plan's **Reward code after a purchase**: a percent from 1 to 90, or a fixed amount
in the plan's currency, the days it lasts (default 10, up to 365) and a text in Persian and English for the buyer). A paid purchase or renewal of it
by a customer (not a reseller, not a free order, not a traffic package, not one service of a bulk purchase) makes a discount code for the buyer's
next purchase of **any plan**: `THX-` and eight characters, bound to the buyer (for anyone else it is "not valid"), one use, the days the plan says.
An amount code takes that amount off, never more than the price (the rest is lost) and only on a plan in its currency. The bot sends it in its own
message (`reward.code`: the code, the percent or the amount, the days and the date, the owner's text, "only for you, once") with a Buy button; the
buyer enters it at the payment menu's **Discount code** as any code. Each purchase brings a new code. A code staff made is unchanged (the
dashboard's Discounts list shows reward codes too). A free plan (the trial) takes no code.
**Plan groups:** staff may put plans in named groups with a text for each (dashboard, Plans). In a store that has at least one
group with a plan on sale, Buy first shows the groups (`pg:<group id>`, one button each, `plans.group_button`) and an **Other plans**
button (`pg:-`) for the plans with no group; a group's screen shows its name and its text (in the customer's language, else any the
group has; the text is escaped, never HTML) and then its plans in the store's layout. Picking a plan opens the usual payment menu,
whose Back returns to that group. A group with nothing on sale is not listed, a stale button shows the group list again, and a
store with no group lists its plans exactly as before. Only Buy is grouped: Renew, Add traffic, a reseller's Buy for a customer
and the Bulk purchase list the plans flat. A free trial and a traffic package are never in a group.
**The plan list (built 2026-10-20):** Buy, a reseller's Buy for a customer, the Renew and Add traffic pickers and the
Bulk purchase's plan step all draw their plans with one helper, in the layout the owner chose (setting `bot.plan_layout`:
Settings, "Plans and bulk purchase" in the dashboard and in the bot's admin menu; `table` until it is set):
- **table**: a header row (`plans.head_name`, `plans.head_price`: "Plan | Price", two buttons that do nothing when
  tapped), then each plan as ONE ROW OF TWO BUTTONS, its name and its price, that both open that plan (the same callback
  data as ever), in the plan order, with the price this customer pays (a reseller's tier price), then Back and Home. In
  a right-to-left chat Telegram puts the first button of a row on the right, so in Persian the name is on the right and
  the price on the left. The buttons have no color (as the plan buttons never had). A name longer than 24 characters is cut
  with an ellipsis (never inside a vowel sign, a half-space or a joined emoji, and never ending in a space); the price is
  never cut.
- **list**: one button per plan, "name · price" (`plan.button`), as before the table existed.
Telegram's limits are kept (at most 100 buttons in a keyboard, 8 in a row, 64 bytes of callback data): a table of more
than 46 plans (2 + 2 x 46 = 94 buttons, which leaves room for Back, Home and a pager) is drawn as a list, and a list shows
at most the first 94 plans. Both the header texts and the order of the plans are the same for every customer.
**Bulk purchase (built 2026-10-20):** a customer, a reseller or staff member buys N services of one plan at once and
pays for all of them from the wallet. Plan (in the store's layout; only plans that cost something: a free plan is not
sold in bulk) -> how many (quick buttons 2, 3, 5 and 10, only those the limit allows, or **Another number**: a number
typed in any digits, from 2 to `bulk.max_quantity`; one that will not do is explained and asked for again, and Cancel
closes the question) -> the total: the services, the price of each, the total, the wallet beside it, and optionally **Name
the services** (one name, at most 62 characters for a bulk of up to 9 services, 61 up to 99 and 60 for 100: the services are
named "<name> 1" to "<name> N", as a reseller's names are; **No name** takes it off) -> **Pay** (green) -> "Paid X for N
services. They are being prepared: you will get all the links in one message." with My services and Home.
- **Price.** The price of each service is what a single purchase of the plan costs this customer (the store price; a
  reseller's tier price, fixed or by the tier's discount), times N. The total shown is core's (`QuoteBulkOrder`), and Pay
  carries it: when the price changed between the screen and the tap, nothing is charged, the customer is told "The price
  changed" and sees the new total. **A discount code is not used in a bulk purchase**: a code can be used once per customer,
  so it could only cut the price of one service out of N; to use one, buy that service on its own. Discounts a
  reseller's tier gives are in the price of every service.
- **Only the wallet pays** (decided 2026-10-20). A gateway or a manual payment is made for one order of a fixed amount: the
  payments service, its intents, its review and its refunds are built around that, and paying N orders with one payment
  would need a second order model. So a bulk is paid from the wallet, and a wallet that falls short is told what is missing
  and offered a top-up of exactly that amount (any method the store offers, then buy), as the single purchase does. A
  reseller buys on credit down to their limit: past it there is no Pay and no top-up, only "Not enough credit" and the way
  out (settle with the store).
- **All or nothing, once.** Core makes the N orders and takes the N payments in ONE database transaction: all of it, or
  nothing is written (no partial debit, no order without its payment, no payment without its order, no announcement
  without the purchase). The N orders are ordinary ones (each with its own ledger entry `purchase:<order id>`, refunded,
  retried, counted in the reports and paying a commission as any order does), tied together by a row that holds the
  request key: the nonce of the screen's Pay button, so a double tap, four taps at once or a retry after an answer that
  never came is ONE purchase and one debit. A key reused for another purchase is refused.
- **Limits.** `bulk.max_quantity` (default 20, 2 to 100) is the most in one purchase and `bulk.enabled` the switch; core
  enforces both (a stale button or a crafted call is refused), not only the bot. The bot has no limit of purchases per
  customer per day (only the taps and messages per 10 seconds, `limits.bot_per_10s`), so none is applied to a bulk.
  Maintenance mode, the required channel and a ban stop a bulk purchase as they stop any other (the buttons and the typed
  number are held by the gates; core refuses a banned buyer).
- **The license.** The places among the active services (`max_active_services`) are counted for ALL N at once under the
  lock that makes purchases take turns: three at twenty-three of twenty-five are refused whole, two are not. The customer
  is told what any new purchase refused for the limit says ("New purchases are temporarily unavailable. Please contact
  support.", never a word about licensing); the quote refuses early, before the customer is shown a total they cannot pay,
  and the staff alert "A new purchase was refused" counts it. A reseller's purchase also needs the resellers feature.
- **Delivery.** The provisioning worker picks the N orders up like any others. When every service is delivered, or has
  failed three times and is retried in the background, core announces the bulk once (`order.bulk_ready.v1`) and the bot sends
  ONE message with every link (each service named, its link in a code span to copy) and a My services button; the
  services are not announced one by one. A message that would be longer than Telegram allows (about 35 services) is a few
  messages, the first with the header and the others starting "…continued", and a **text file of all the links** (name,
  a tab, the link, one a line) follows. A service that is not delivered by then is listed as "Being prepared" under its number, the
  message says how many are, and each of those is sent on its own, as any service, when it is ready. The staff get the usual
  "Provisioning failed" alert for each failed service (the order id names it) and the customer no "taking longer" note per
  service; if none is ready the message says so ("Your N services are not ready yet"). Nothing paid for is lost: a
  service the panel cannot make stays a paid order the worker retries with growing waits, and staff can retry or refund it
  from the dashboard as any order.
- **Staff and records.** The staff alert for a sale (`notify.sales`) is one message per bulk, for the whole quantity ("New
  bulk sale: @name bought 3 × Plan for 450,000 Toman"). The audit log has one `order.bulk` entry per bulk (the buyer as the
  actor, source bot, with the plan, the quantity, the price of each and the total); the orders, the ledger and every report
  count N orders and N payments. The referral reward of an invited customer is paid on the first paid purchase, which is the
  bulk's first order (as it is for any customer's first order); a reseller's commission on a customer who joined through
  their link is paid on every order, so N times.
- **Not in the Mini App** (the app buys one service at a time).
**Automatic USDT (Phase 6a):** the payment screen shows the exact amount to send (copyable, Latin digits), the store's
TRON address, the deadline (45 minutes) and a Check button. The payment settles by itself once the transfer is seen on
the chain. A different amount is an unmatched deposit: staff are told and credit it by hand.
**Renew / add traffic:** My services -> service -> Renew | Add traffic -> pay -> updated expiry shown.
**Top up wallet:** amount presets + custom -> method -> pay -> balance updated message.
**Gift code:** Wallet -> Apply gift code (the button is absent while the owner has the setting `gift_codes.enabled` off) -> the
customer sends the code (any way it is written: small or capital letters, Persian digits, spaces or dashes; Cancel is under
the question) -> the amount credited and the new balance, with Wallet and Home buttons. A wrong code (unknown, switched off,
expired or not a code: one message for all) leaves the question open; five wrong ones in an hour close entering codes for an
hour ("try again in N minutes"). A code used up, or one the customer already used as often as allowed, says so. Core holds
every rule and counts; the bot keeps no copy of a code and never logs it. The join-channel, maintenance and ban gates apply as
everywhere; resellers may redeem. The message is the request's idempotency key, so a repeated update credits once.
**Manual payment:** show card / Zarinpal link / address + amount + unique reference -> user sends a screenshot of the payment (photo or image file; card transfers also need the bank reference number, crypto may send the TXID instead) -> every owner gets an alert whose Review button opens that payment (a staff member's card: the receipt and approve/reject) -> result to user.
**Trial:** one tap -> service created (small quota/time) -> delivery message.
**Service detail:** status, expiry, used/total (progress bar), online?, subscription link, QR, reset link, renew. An "Open page" button (a URL button, `?l=<language>`) opens the branded page `/sub/<token>` while core has a public URL and the page is on ([15-subscription-page.md](15-subscription-page.md)).
**Mini App store:** the same purchase, renewal, top-up and services list in a web app inside Telegram, on by default (setting `miniapp.enabled`) and opened from a Store button on the home screen, a Store item at the top of the bottom menu (it answers with the button that opens the app) and Telegram's menu button; they appear only when the store has a public HTTPS address. It pays by the wallet, Zarinpal, IDPay, NextPay, Telegram Stars or automatic USDT, takes card-to-card, manual USDT and Zarinpal-link payments with a receipt image (sent to the customer's chat with the bot, then reviewed as in the bot), and has the free trial, invites and support ([17-mini-app.md](17-mini-app.md)).
**Support:** new ticket (category, text, optional photo) -> status list -> reply thread -> close.
Built (2026-10-10): one open conversation per customer, numbered; text, photos and PDFs; staff replies arrive in the
chat with [Reply] and [Close]; a Telegram reply to them continues the ticket; past tickets can be read.
A ticket has a category: the customer picks Payment, Service or Other, and a reseller application (below) is filed under
its own, **Reseller**; the topics a customer picks from never include it.
**Help content (FAQ and Tutorials):** the owner writes both in the dashboard (Branding, "Help content" tab; [04-dashboard.md](04-dashboard.md)) in Persian and English; core stores them (`core.help_entries`) and the bot reads the enabled ones with `ListHelpEntries` / `GetHelpEntry`. A customer reads an entry in their own language, and in the other one where that translation is missing (an entry needs a title and a text together in a language to count as written in it).
- **FAQ** is the first button of the Support screen ("❓ FAQ"), and stays next to "ticket history" while a ticket is open. While the whole FAQ has at most 12 enabled questions and their questions and answers take at most 3000 characters (`helpcontent.FitsOneMessage`) it opens as ONE numbered message with every answer; past either limit it is a numbered list of the questions (10 to a page, the numbers are buttons, "Page 2 of 3" and previous/next), each opening its answer with a way back to that page. Every FAQ screen ends with the "Send a message to support" button, so the ticket flow is one tap away.
- **Tutorials** ("💡 Tutorials"): a home-screen button and a bottom-menu item; a list of buttons (8 to a page), each opening the tutorial's text with its https URL buttons (an app's download link) and a way back.
- Both buttons exist only while there is something enabled to show (core says so in `Settings.help_faq` / `help_tutorials`, which the bot refreshes with the other settings). Both go through every gate (join channel, maintenance, ban, language); the bottom menu item and the home button are kept in step by a test. A tap on an entry that was switched off or deleted meanwhile answers "this menu is old". Texts are the owner's, checked like the bot's own texts (Telegram's HTML subset, https links only, length limits) and the bot clips a pathologically long row written by hand.
- A new install is offered a generic FAQ (IP, renewals, devices, usage, what to do when it does not connect, refunds, with no guarantees promised) and tutorials (the subscription link; downloads for v2rayNG, Hiddify, Streisand, V2Box and HAPP, matching the subscription page) once, in Persian and English, for the owner to edit or delete. Deleting them does not bring them back.
**Referral:** personal link, count, reward rules, balance credited on first paid order of the referred.
Built (2026-10-20): besides its screen, **Invite friends** sends the customer a **card to forward**: a message of its own,
with no buttons, holding the store's promo text (`ref.promo`: "get safe, fast services without interruption through the
`{brand}` bot", edited like any text) that ends in the customer's own invite link as a text link (the label is
`ref.promo_link`), and then a line that says to forward the message above to their friends (`ref.promo_hint`). A friend
who joins through the link is the customer's invite. The Share button stays. Each time the screen is opened the card
comes again. A reseller's **My link** screen sends the same card with their link, whatever the store's referral reward.

**Top users** (built 2026-10-20): the board of the customers who invited the most people, from the **Top users**
button (home screen and bottom menu), shown only while `referral.reward_percent` is above 0 and the owner's switch
`growth.leaderboard.enabled` (Settings, Growth; on by default) is on. Five places to a page: the rank, 👤, the name
and the Telegram id **masked by core** (the first and last three characters of the username with `***` between,
`***` for no username or a short one; the id the same way: `7102607089` is `710***089`), and how many users joined
through that customer's link; a name or an id is never sent whole. A name shorter than nine characters shows fewer
characters at each end, so that at least three always stay hidden (four or fewer: none). Under the places: the
viewer's own count on a button (`Your invites: 3`, which opens their Invite friends screen) and **Others**, which pages
on (ranks 6 to 10, 11 to 15, up to rank 50); the viewer's own place is marked when it is on the page. Who is ranked:
active customers only, never staff, resellers (their invites are their commission link) or banned users, however many
they invited; the count is the one of the Invite friends screen (every user who joined through the link); a tie goes
to whoever joined the bot first; a customer who invited nobody is not ranked. The board is one query for all
customers, kept for 30 seconds in core, and took 50 to 125 ms over 100,000 users, a third of them invited by one of
about 2,000 inviters, on a busy laptop (`BOBRES_SCALE_TEST=1 go test ./internal/core/store -run TopInvitersAtScale -v`
seeds them and prints the timings and the plan); the viewer's own count is always read fresh. With the switch off (or the program off) the button is gone from both menus, and a tap on an old
button answers "not available" and shows the home screen. Not built: a board in the Mini App.

**Account lines** (built 2026-10-20): the **Wallet** screen starts with the customer's account, above the balance and
the buttons: their numeric Telegram id (Latin digits, tap to copy), how many services they have (the deleted ones do
not count) and the day they joined the bot (Jalali for Persian, Gregorian for English, in the store's time zone). A
figure core cannot give shows a dash and costs the customer nothing else.

**Commands** (built 2026-10-20): `/new` opens the buy flow (the Buy button), `/renew` the services that can be renewed,
each a button that opens its plans to renew with (a reseller: their services list, where a service has Renew), `/status`
My services. A command goes through `dispatch` as the button does, so it has the same role checks, and `Handle` runs
the gates in front of it as for any update: maintenance and the required channel answer it (staff pass), and so does
a language not chosen yet (the picker first). A command is never the answer to an open question: it closes it, as a
tap on the bottom menu does (a ticket message, a discount code, a bank reference, an application), and a command a gate
holds back leaves a payment under way untouched, so its receipt can still be sent. They also work as `/new@BotName`.

**Reseller application** (built 2026-10-20): the **Become a reseller** button (customers who are not resellers or staff,
and not banned, only while the license in effect includes resellers; core tells the bot with every settings refresh,
and a customer is never told about licensing) asks for **one text message**: the request and what is worth knowing
about the applicant, then to wait for an admin. The message is filed as a support ticket in the **reseller** category
through the usual ticket call, and the customer is told its number. Staff get the usual new-ticket alert (the
`notify.new_ticket` switch, the same recipients) in the words of an application ("New reseller application #12 from
@name") with the applicant (@username, else the Telegram id) and an **Open in dashboard** button that opens the
ticket; they answer in the dashboard and the answer reaches the customer in the bot, who can write back. One open
application per customer, and one open conversation with support at a time: a customer who has another open ticket is
told to close it or wait, and one whose application is open is told it is waiting (with a Support button that shows
it). A closed application can be made again. Turning the applicant into a reseller stays the dashboard's Resellers
page. The dashboard's inbox lists the category ("Reseller application") and filters by it, the ticket page and the
Mini App name it too. Core refuses an application from a reseller, staff, a banned customer or while resellers are not
in the license (the bot says only that it is not available), and counts it as a support message for
`limits.tickets_per_day`.

## Admin in bot (every task is a button; the dashboard stays the full tool)
`/admin` is the front door for owners and admins (it says when maintenance mode is on). Nobody has to remember a
command: the old ones still work, and each usage text ends with "open /admin and use the buttons".

**Menu:** payments to review (count) | today and find a user | plans and discounts | notifications and broadcast |
settings and servers | owner only: payment methods and staff | dashboard link | home.
- **Payments to review:** a list, oldest first, eight to a page: each payment is a button (who paid, how much, when; a
  warning sign when the bank reference was seen before) that opens its card with the receipt or TXID and Approve / Reject.
  Every owner and admin sees the same list and the same alerts; a payment one of them decides leaves everyone's list, and
  an alert tapped later says who decided it and when ("Already approved by @name"). A payment is settled once, whoever
  taps first (core's review is atomic). The dashboard's receipt queue is the same queue.
- **Today:** orders, revenue and refunds per currency for today and the last 7 days, new users, the servers' health.
- **Plans:** list, one plan's card (price, volume, days, devices, on or off, edit each field, archive), add, the trial
  (set or switch off) and the top-up packs (add, switch off).
- **Discounts:** list, add, switch off.
- **Settings:** groups, then every setting the role may change, shown with what it does and its current value.
  The editor depends on the kind: on and off and the choices are buttons, a number or text is typed, `-` clears it.
  Core still validates every value and its reason is shown (`Not accepted: ...`). Payment details, staff and the
  licence are the owner's, like the dashboard permissions (gateways, staff, licence, system).
- **Servers** (owner and admin, as on the dashboard): health, switch on or off, weight. The address and token are never
  shown or asked here.
- **Staff** (owner only): list, add by Telegram id (the person must have started the bot) with a role, remove.
- **Broadcast:** segment, text in Persian and in English, a preview with the audience size, then send.

**Input steps** (plan, top-up, trial, discount, setting, server weight, staff, broadcast) share one engine: each step
says "Step n of m", what is wanted, the allowed range and an example; a refused answer is explained above the same
prompt; Back and Cancel are on every step; a last screen shows everything to check before it is saved. Cancel leaves
nothing behind. Callbacks are `adm:<verb>:<arg>`, at most 64 bytes (tested).

**Roles:** the bot hides what a role may not do and answers politely when a button is tapped by hand; core enforces
the role on every call, so a crafted callback cannot do more than the dashboard allows.

`/set` with no arguments opens the settings screen. `/dashboard` and a menu button send any staff member, support
included, a one-time dashboard login link.

## Store rules the bot applies (dashboard Settings, 2026-10-10)
- **Maintenance mode** (`maintenance.enabled`): customers get the maintenance text and nothing else. One gate decides
  every update before any command, button, scene or conversation sees it (`internal/bot/handler/gates.go`), so there
  is no screen that forgets to ask. What a customer gets for what they do:
  - **A button:** a popup with the maintenance text (an alert, not a toast), whatever the button and however old the
    message it sits on: the home menu, the language buttons, the ticket buttons, the buttons under a notification.
  - **Anything typed or sent:** a message with the maintenance text, every time (an album, which Telegram delivers a
    part at a time, gets one): a command (`/start`, `/menu`, `/buy`, `/support`, the admin commands...), a tap on the
    bottom menu (Support too, with a ticket open or not), text, a photo, a file, a voice message, a sticker, a
    Telegram reply to a staff answer, and whatever answers a question left open from before (an amount, a discount
    code, a message to support, a reseller's name or search). The question stays open, nothing is saved.
  - **The text** is the built-in one, in Persian and English, unless the owner edited the text "maintenance" (Branding
    and texts), which is then used for the popup and the message alike. The customer's language is theirs, or the one
    Telegram reports for someone with no account.
  - **What still works is this list and nothing else:** staff (owner, admin, support) and the owners the server names,
    also before they have an account; `/paysupport`, which Telegram requires of a bot that sells with Stars; a payment
    already under way, that is its receipt, bank reference or TXID while the bot waits for one (a tap on the bottom
    menu, a command or a button in the middle of it is held), its Check button, and a Stars invoice already sent (its
    pre-checkout and its payment are always answered); and a newcomer's `/start` and language choice, which make
    the account and keep an invite code (they are then shown the welcome and the maintenance text, without the bottom
    menu). What is let through this way gets no bottom menu and no home screen with it.
  - **Not held, because it is not the bot answering:** notifications to customers (a payment confirmed, a service
    delivered, a reminder, a staff answer to a ticket) keep arriving; the buttons under them are held like any other.
    A customer cannot write to support while maintenance is on; the staff answers to their tickets still arrive.
  - **Order:** maintenance comes before the required channel: while it is on, nobody is asked to join and nobody's
    membership is looked up.
  - **Switching it on or off needs no restart.** The bot reads the setting as it last reloaded the settings: the
    dashboard makes core ask the bot to reload at once, `/set` and the admin panel's buttons reload on the spot, and
    the bot reloads by itself every minute.
  - The Mini App holds the same rule call by call, and its sign-in says so ([17-mini-app.md](17-mini-app.md)). The
    dashboard is for staff and stays open (the owner switches maintenance off there), and a service's own page
    (`/sub/<token>`) stays up: active services keep working.
- **Required channel:** customers join it before using the bot (a join button and an "I've joined" check). Members
  are remembered for 10 minutes; if Telegram cannot answer, users are let in. Staff and a newcomer's `/start` and
  language choice are never blocked, nor is a payment under way, and a customer may go on with a support ticket they
  already have (starting one asks them to join first). The Mini App holds the same rule, with the same exemptions and
  the same choice when Telegram cannot answer ([17-mini-app.md](17-mini-app.md), "The required channel"). The owner
  can require **more than one channel or group** (below).
- **Required chats** (several channels and groups; built 2026-10-20). The store can require any number of channels and
  groups, up to what the license allows: **1 on the free tier and Standard, any number on Pro and during the 30-day
  trial** (`max_join_chats`, [05-license.md](05-license.md); a technical bound of 50 holds whatever the license says,
  since every customer's update costs a lookup for each chat they have not joined and a Telegram keyboard holds 100
  buttons). Each has the chat (a public chat's `@name`, or a private chat's `-100…` id; a channel or a group), an
  invite link (`https://t.me/…`, required for a private chat; a public chat's link is its `t.me` page), and an optional
  short name (at most 32 characters) shown to customers.
  - **Where they live.** The **first** one is what it has always been, the settings `join.channel`, `join.link` and the
    new `join.label` (its name): the bot's `/set` editor, the Mini App and every older bot read it there, and emptying
    `join.channel` turns the whole requirement off, the extras too (they stay saved and do nothing). The **others** are
    rows of `core.join_chats` (migration 00029), in the order customers are asked. They are edited in the dashboard
    (Settings, Required channels and groups: the owner changes the list, an admin sees it) and saved all at once; they
    are in every backup (they are an ordinary table of the core database), and each change is in the audit log
    (`setting.set` for the first one, `join_chat.add`, `.update`, `.move`, `.remove` for the others).
  - **The prompt.** A customer who has not joined one of them gets one message with a **Join** button for **each chat
    they have not joined** (the chat's name, else its `@name`, else "channel N"; never a private chat's id) and one
    **I've joined**. With one chat required it is the message and the button it always was. **I've joined** asks about
    every chat again at once and says which are still missing in its popup ("You haven't joined yet: VIP, @team"),
    and the prompt under it keeps only their buttons; the chats the customer is already in are not named again.
    Maintenance still comes first (nobody is asked, nothing is looked up), and the exemptions are the same for every
    chat: staff, the owners the server names, a newcomer's `/start` and language choice, a payment under way, and a
    ticket already open.
  - **A chat the bot cannot check** (the bot is not its administrator, the chat does not exist, a timeout) is skipped,
    as the single required channel always was: the customer is let in **for that chat**, the bot logs it once a minute
    ("cannot check members of a required channel", naming the chat), and the other chats still hold. The "member of
    every chat" cache below is not written then, so that chat is asked about again at the next update.
  - **Few lookups.** A confirmed member of a chat is remembered for 10 minutes (per customer and chat, so a chat added
    later is the only one asked about); a customer who is a member of the whole list costs **one** cache read per
    update. A chat not joined is not asked about again by an ordinary update for 2 seconds (**I've joined** always
    asks). One update's lookups run **4 at a time** and the bot runs **8 at a time** in all, each with a 3-second
    timeout and 8 seconds for the update; a chat unanswered by then counts as one Telegram could not tell about.
  - **When the license drops** (a license that ends, the trial over): nothing is deleted. Only the first N chats of the
    list, the first one always, are enforced; the dashboard marks the others "Inactive: needs Pro" and the License
    tab counts them against the limit. The owner can still remove, edit and reorder them, but not add more than
    exist; they count again when the limit allows. The bot learns the enforced list with its settings (at most a
    minute, or at once when the dashboard saves).
- **Limits:** taps and messages per 10 seconds (checked before the gates; a message over it is not answered, a tap gets
  "Too many requests"), support messages per day.
- **Plans and bulk purchase** (Settings, the "Plans and bulk purchase" card; the bot's admin menu has the same group):
  `bot.plan_layout` (`table`, the default, or `list`), `bulk.enabled` (on by default) and `bulk.max_quantity` (2 to 100, 20
  by default). The bot reads them as it last reloaded its settings (core asks it to at once after a change); core itself
  refuses a bulk purchase while it is off or past the limit.
- **Staff alerts:** new payments to review, new tickets, failed deliveries and (optionally) every sale, to every owner the
  server names (`BOBRES_ADMIN_TELEGRAM_ID` lists one or several), or to every owner and admin. "Payment taken but not recorded" is always sent. The **daily sales summary** (off until the
  store turns it on, `reports.daily_summary`) goes once a day to every owner and admin, whatever `notify.recipients` says,
  because it holds revenue (14-reports.md).
- `/terms`, and terms and privacy link buttons on the support screen, when the links are set.
- Dates use the store's time zone; prices use the store's name for the Toman.
- **A refused purchase (the license's limit):** when the install is at its limit of active services (the free tier's 25,
  a license's, 05-license.md), a customer's new order, free trial or reseller purchase is refused with a popup:
  "New purchases are temporarily unavailable. Please contact support." (`error.purchases_paused`). A customer never
  sees a word about licensing (the same text is shown when a reseller's purchase is refused because resellers are no
  longer included). Renewals, traffic top-ups and payments of orders already made go
  through. The staff get one alert a day, "A new purchase was refused: this install is at its limit of ... active
  services. Buy or renew a license to sell more." (`alert.license_limit`).

## Reseller panel
Built (milestone 5, 2026-10-11; managing their customers' services and the customers who joined through their link,
2026-10-20). Resellers work in the bot and in the Mini App's reseller section ([17-mini-app.md](17-mini-app.md)),
never in the dashboard; the owner and admins make them, set their tier, credit limits and commission, and record their
payments in the dashboard. Customers see none of this.
- **Home:** a "Reseller panel" button instead of "Invite friends" (their link is in the panel).
- **Panel:** tier, discount and commission %; per currency the balance (a negative balance is shown as "debt X",
  never with a minus), credit limit and what they can spend (balance + limit); customers who joined through their
  link and commission earned. Buttons: Buy for a customer, Customer services, Customers, Statement, My link, Home.
- **Buy for a customer:** the plans at their prices, then "type a name for this service, like Ali's phone, or tap
  Skip" (one line, at most 64 characters), then the regular checkout. Their regular Buy, renewals and traffic
  packages show their prices too.
- **Bulk purchase** (the home menu's button, Key flows above) works for resellers: the plans and every service at their
  prices, paid from the wallet down to their credit limit (the screen says what they can spend; past it, "Not enough
  credit" and no Pay or top-up), the services named "<name> 1" to "<name> N" like a name they give a single one, and the
  one message with every link. Their credit limit is respected in the same transaction as every debit.
- **Checkout for resellers:** no discount-code button; the wallet button shows only when the balance plus the credit
  limit covers the price, with the amount they can spend; other payment methods stay. Past the limit they see what
  they can still spend, the other methods and a Panel button.
- **Customer services** (also their "My services"): 8 a page with Previous/Next, each the name or #short id, status
  and expiry; a search by part of the name or the #number, paged with the search. Under the list the services are
  grouped, each group a button with its count: All, Active, Ending soon, Ended (the time or the traffic ran out) and
  Paused. The group shown is ticked; a search started inside a group stays inside it and its pages keep both. Every
  service is in exactly one group, so the counts add up, and the counts do not follow the group or the search. A
  service opens the usual page, with a Rename button (a new name, or Remove the name).
- **A customer service's page** (the reseller's own services only): beside Renew, Add traffic, QR code and Rename:
  - **Pause / Resume:** turns the service off or on in the panel (the customer's link stops working while it is
    paused; a service paused shows "Paused"). Doing it twice changes nothing. A service that **support** paused cannot
    be resumed by the reseller: the popup says to contact support. A service being delivered cannot be paused.
  - **Note:** a private note, one line of at most 200 characters, shown on the page under the service; only the
    reseller sees it (not the customer, not the public page). Send a new text to replace it, or Remove the note.
  - **Delete:** a confirmation screen first ("removed from the server for good, the customer's link stops working at
    once, this cannot be undone, nothing is refunded"), then Yes, delete it or No, keep it. A service being delivered,
    or with a renewal still being applied, cannot be deleted yet. Money never moves.
  A reseller cannot reset a service's traffic or give free days or free traffic: those stay with staff. Pausing and
  deleting are in the audit log under the reseller's name, source `bot`.
- **Customers** (the panel's Customers button): the people who joined through the reseller's invite link, newest
  first, 10 a page. Each line: their @username (or their Telegram id when they have none), the day they joined, how
  many paid orders they made (refunded and free ones do not count), what they spent per currency, and the commission
  those purchases paid the reseller. Nobody who did not join through this reseller's link is shown, and nothing else
  about them.
- **When resellers are no longer in the license** the reseller keeps their account and their services: the plain list
  of services still opens (without the groups) and each service shows its link, and renewals still work. Pause,
  Resume, Delete, Note and Customers answer "This is not available right now. Please contact support." (never a word
  about licensing) and change nothing ([05-license.md](05-license.md)).
- **Names in messages:** the service page, the delivery message, renewals and top-ups, and reminders start with the
  service's name when it has one.
- **Statement** (also their wallet History): 10 transactions a page, commissions and payments to the store included.
- **My link:** their invite link and commission %, even when the store's referral reward is 0.
- **Config prefix** (`rs:pfx`, `rs:pfy:<prefix>`): the prefix of the names of their configs in the 3x-ui panel (`sob` makes
  `sob-u<id>-...`). Chosen once: the question takes a message (2 to 10 of a-z and 0-9, case ignored; a bad shape is asked again at once), asks
  "Use <prefix> for all your configs? You cannot change it yourself afterwards" and saves on the confirm; core refuses a prefix another
  reseller has or one that looks like `u123` (asked again), and an old confirm button after the prefix is set shows the one they have. After
  that only the owner changes it (dashboard). The panel's text shows "Config prefix: <b>..</b>" once set. Their group in the 3x-ui panel
  (`BOBRES-<NAME>`) is the owner's to give and is not shown to them.
- **Notifications:** a commission ("from a customer's purchase", never naming the customer) and a payment the staff
  recorded, each with the balance after.
- Resellers are gated like customers (maintenance, required channel); a banned reseller loses the panel.

## Notifications
Expiry (3d/1d/expired), traffic (80%/100%), payment result, ticket reply, wallet change (resellers: commission and
recorded payments), panel/server alerts (admin only).
All user-configurable except billing-critical ones.

## Copy and states to write (see 08-brand)
Welcome, help, each menu, each flow confirmation, each error (payment failed, expired, no stock, banned, rate limited,
panel down), empty states, delivery message, connect guides (Android/iOS/Windows/macOS), FAQ.

## Open points
- DECIDED: forced channel join is an optional setting (the required channel, above).
- Anti-abuse for trial: one per Telegram ID is built. Telegram account age heuristics and an optional phone check
  are not.
- DECIDED: long polling by default (no domain needed for the bot alone). Webhook mode is optional:
  `BOBRES_TELEGRAM_WEBHOOK_URL` with `BOBRES_TELEGRAM_WEBHOOK_SECRET`, delivered to `/tg/webhook`.
