# 03 - Bot UX flows and menu map (DRAFT)

All texts live in i18n files (fa + en), overridable per install from the dashboard. Persian is RTL:
avoid mixing raw English inside RTL lines; use consistent numerals; test long-line wrapping.

## Principles
- One primary action per screen; max 2 taps to the common goals.
- Inline keyboards edit the same message (no chat spam). Always a Back and a Home button.
- Every error tells what happened and what to do next. No raw exceptions.
- Language chosen on first /start, changeable in Settings.
- Rate limit per user; ignore duplicate taps (callback idempotency).

## Main menu (user)
1. Buy service
2. My services
3. Wallet (balance, top up, history)
4. Free trial (once per user, abuse-checked)
5. Invite friends (referral link, earnings)
6. Support (tickets, FAQ)
7. Settings (language, notifications)
(+ Reseller panel button when role = reseller; + Admin button when role = admin)

## Key flows
**Buy:** plan list -> plan detail (price, GB, days) -> discount code (optional) -> payment method
(wallet / card / Zarinpal / Stars / crypto) -> confirm -> pay -> delivery message (subscription link, QR, connect guide per platform).
**Renew / add traffic:** My services -> service -> Renew | Add traffic -> pay -> updated expiry shown.
**Top up wallet:** amount presets + custom -> method -> pay -> balance updated message.
**Manual payment:** show card/address + amount + unique reference -> user sends receipt photo -> admin approve/reject buttons in admin chat -> result to user.
**Trial:** one tap -> service created (small quota/time) -> delivery message.
**Service detail:** status, expiry, used/total (progress bar), online?, subscription link, QR, reset link, renew.
**Support:** new ticket (category, text, optional photo) -> status list -> reply thread -> close.
**Referral:** personal link, count, reward rules, balance credited on first paid order of the referred.

## Admin in bot (quick actions, full power is in dashboard)
Stats snapshot, find user, add balance, ban/unban, approve receipts, broadcast (queued), toggle gateway, panel health.

## Reseller panel
Own price tier, sales list, credit limit and usage, create service for a customer, top-up request.

## Notifications
Expiry (3d/1d/expired), traffic (80%/100%), payment result, ticket reply, wallet change, panel/server alerts (admin only).
All user-configurable except billing-critical ones.

## Copy and states to write (see 08-brand)
Welcome, help, each menu, each flow confirmation, each error (payment failed, expired, no stock, banned, rate limited,
panel down), empty states, delivery message, connect guides (Android/iOS/Windows/macOS), FAQ.

## Open points
- Forced channel join before use? (optional toggle)
- Anti-abuse for trial: Telegram account age heuristics, one per Telegram ID, optional phone check.
- Webhook vs long polling default (long polling = no domain needed for the bot alone).
