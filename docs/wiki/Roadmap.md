# Roadmap

What has shipped, what is still open, and what was left out on purpose. Nothing here is promised for a date. The detailed, ordered list is [docs/ROADMAP.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/ROADMAP.md), and each release's notes are in the [changelog](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/CHANGELOG.md).

As of v0.17.2, 2026-10-28.

## Shipped

| Release | Date | Main content |
|---|---|---|
| v0.1.0 | 2026-10-14 | The first public release |
| v0.2.0, v0.2.1 | | Sales reports, then the daily summary in Telegram |
| v0.3.0 | | The new installer and command line |
| v0.4.0 | 2026-10-17 | Marzban panels, the subscription page, the Mini App, IDPay and NextPay, churn and Jalali reports |
| v0.5.0 | 2026-10-20 | Several owners, the list of payments to review, Marzban tested on a real panel |
| v0.6.0, v0.6.1 | 2026-10-20 | The Mini App on by default and complete, reseller management, the required channel in the app |
| v0.7.0 | 2026-10-20 | Updating from the dashboard, the Reports calendar and CSV files, maintenance mode in every option, growth features of the bot |
| v0.8.0 | 2026-10-20 | Server monitoring, charts and a 3D report, backup download and restore from the dashboard, full uninstall, help center, plan table, bulk purchase, gift codes, several required channels |
| v0.9.0 to v0.10.1 | 2026-10-21 | The vendor's own features: license sales and a supplier's accounts (not in customer installs) |
| v0.11.0 | 2026-10-21 | Plan groups with a text each |
| v0.12.0 | 2026-10-22 | A reseller's group and prefix in the 3x-ui panel, and 3x-ui v3.10 tested |
| v0.13.0 | 2026-10-22 | Reward codes, sold-out digital accounts, the owner's menu buttons, find a service by its link |
| v0.14.0 | 2026-10-23 | Reward codes as an amount, deleting a plan, digital accounts by category, the live USDT price, your own Terms and Privacy pages, Karing and NPV Tunnel |
| v0.15.0 | 2026-10-23 | Buying a license in one screen, restoring a deleted plan, Undo in the bot's admin menu, this wiki |
| v0.16.0 | 2026-10-23 | Digital accounts in the Mini App, and the rate shown beside a USDT amount |
| v0.16.1 | 2026-10-23 | The supplier's product dialog fits a phone |
| v0.17.0 | 2026-10-23 | One wallet in Toman, the renewal rule, reports that count every sale and start from zero, the reseller's search by link |
| v0.17.1 | 2026-10-28 | The store comes back by itself after a reboot; a tls-agent that stays healthy after a restart; alerts that say what to type |
| v0.17.2 | 2026-10-28 | Services whose client is gone from the panel are marked, listed, noticed once a day, and made again with a button; repeated log lines fold into one row |

## Still open

These are not built:

- **S-UI and Hiddify** panels, if operators ask for them, and an adapter for a newer 3x-ui major version when one exists and is tested. Marzban 1.0 is beta and unsupported.
- **Growth tools**: cashback, a lottery or wheel, reseller targets. (Gift codes, Top users and invite cards are built.)
- **More bot languages**: Arabic, Russian and Turkish. The texts are already keys, so the work is translation and review.
- **Per-server capacity limits** beyond today's weights.
- **A Prometheus endpoint** and an uptime page.
- **Two-factor sign-in inside the bot.**
- Re-encrypting data when a **master key** changes (the keys are not rotated today).
- More crypto networks, if a route that works from Iran exists.
- A hosted offering, and an affiliate program for BOBRES itself.

## Left out on purpose

- **Third-party crypto processors.** They exclude Iran.
- **Adapters for older 3x-ui versions.** They are built only when a customer needs one.
- **Direct database access** for operators or customers. It would bypass the ledger's rules, the audit log and the license checks. Use the dashboard, reports, CSV files, backups and the CLI.
- **A non-Docker install.** It is not planned for v1.

## Checks only the real world can give

Some work cannot be proved with tests and fakes. These stay open until someone tries them on real devices and accounts:

- The subscription page's app import buttons on real phones.
- The Mini App on every Telegram client.
- IDPay, NextPay and Aqayepardakht with real merchant accounts, and automatic TON with a real wallet (both built, not yet tried for real).
- Real payments for each method, and real Let's Encrypt issuance including the IP-address mode.
- A native Persian speaker's review of every bot and dashboard text.

## The business track

These belong to the owner, not to the code: the lawyer's review of the license and legal drafts, the brand checks (domain, bot name, trademark), the support scope for paid tiers, and the first customers. They are listed in [docs/launch-checklist.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/launch-checklist.md).

## Tell us what you need

Ideas and changes of order go in the roadmap file. You can also open an issue on the [issue tracker](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/issues). New panels such as S-UI and Hiddify are planned only if operators ask for them. See [Development](Development) for how a change gets built.
