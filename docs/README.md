# BOBRES docs index (Phases 0-7 built, 2026-10-12)

Where to start: the [README](../README.md) for what BOBRES does and how to install it, [PLAN.md](PLAN.md) §10 for the
status of each phase, and the [CHANGELOG](../CHANGELOG.md) for what the first release holds.

## Reference

| Doc | Topic |
|---|---|
| [wiki/](wiki/) | The GitHub Wiki's pages, in English and Persian (`FA-*`): guides for the people who run a store. A workflow publishes them to the wiki after each merge to `main` |
| [PLAN.md](PLAN.md) | Master plan: decisions, architecture, 3x-ui integration, DevOps, white-label model, phases and their status |
| [ROADMAP.md](ROADMAP.md) | What comes after v0.1.0, in order: the checks a real server needs, then sales reports, the subscription page, more gateways and the Mini App store |
| [01-database.md](01-database.md) | Postgres schemas, money rules, ledger, state machines |
| [02-events-and-apis.md](02-events-and-apis.md) | Event catalog (Postgres outbox), gRPC APIs, payment provider + panel adapter interfaces |
| [03-bot-ux.md](03-bot-ux.md) | Bot menus and flows, notifications, store rules, admin and reseller panels in the bot |
| [04-dashboard.md](04-dashboard.md) | Web dashboard pages, roles and permissions |
| [05-license.md](05-license.md) | Signed license token, entitlements, enforcement, online activation and daily refresh |
| [06-installer.md](06-installer.md) | One-command installer and the `bobres` CLI: backups, restore, update and rollback, support bundle, hardening, settings and secrets, HTTPS certificates |
| [07-security.md](07-security.md) | Threat model and mitigations, including the HTTPS manager's trust boundaries |
| [08-brand.md](08-brand.md) | Brand (BOBRES the beaver: assets, rules, prompts), tone, copy deliverables |
| [09-legal-questions.md](09-legal-questions.md) | Questions for a lawyer (not legal advice) |
| [10-agent-workflow.md](10-agent-workflow.md) | How the work was split between AI agents and model tiers (build notes; not needed to run BOBRES) |
| [11-staging-run.md](11-staging-run.md) | Checklist for the first install on a real server, the last step before v0.1.0 |
| [12-load-test.md](12-load-test.md) | Load test: numbers, limits, what to watch |
| [13-license-server.md](13-license-server.md) | The vendor's license server (`licensesrv`): license keys, activation, daily refresh, revocation; the vendor's runbook |
| [14-reports.md](14-reports.md) | Sales reports: what each number means in terms of the ledger, orders and events, currencies, time zone, CSV files, the daily summary in Telegram, the `reports.read` permission and limits |
| [15-subscription-page.md](15-subscription-page.md) | The branded subscription page `/sub/<token>`: what it shows and never shows, the page token, regenerating it, settings, limits, headers, and the app deep links that are not yet verified on devices |
| [IDPay and NextPay](16-idpay-nextpay.md) | The two more Iranian gateways: how they work, setup, and what the owner must check by hand |
| [17-mini-app.md](17-mini-app.md) | The Telegram Mini App store (`/app/`, on by default): the buttons that open it, how the bot verifies Telegram's init data for core, the `/api/mini/v1` calls (shop, trial, invites, support, Stars and receipts, the reseller panel), the store's required channel, what it does not do, security and the checks that need a real Telegram |
| [18-hosted-and-affiliate-plans.md](18-hosted-and-affiliate-plans.md) | Two long-term plans, not built: a hosted offering (a store run for the customer) and an affiliate program for BOBRES itself, with the decisions each waits for |
| [pricing.md](pricing.md) | The pricing model (free core, a subscription per install), the tiers side by side, what happens when a subscription lapses, FAQ |
| [legal/](legal/README.md) | Legal drafts for a lawyer to review: Terms of Service, Privacy Policy, Acceptable Use, Refund policy and a template for an operator's own store, each in English and Persian |
| [launch-checklist.md](launch-checklist.md) | What the owner still has to do before selling: names, trademark, lawyer, prices, the license server, the first customers (Persian: [launch-checklist.fa.md](launch-checklist.fa.md)) |
| [marketing/](marketing/README.md) | Launch material: posts, outreach text, pitch, price-list template, calendar and the claims checklist; the public site is in [../site](../site) |
| [brand/](brand/) | The brand assets: icon, mascot, waving animation, banner, bot avatar (rules in 08-brand.md) |

## Runbooks (for operators)

| Runbook | Use it when |
|---|---|
| [Runbooks README](runbooks/README.md) | Start here: first look, system alerts in Telegram, helpers, escalation and `bobres support-bundle` |
| [Payment stuck](runbooks/payment-stuck.md) | A customer paid but the order still waits |
| [Provisioning backlog](runbooks/provisioning-backlog.md) | Paid orders are not delivered |
| [3x-ui down](runbooks/xui-down.md) | A 3x-ui server does not answer, or refuses its token |
| [Bot token revoked](runbooks/bot-token-revoked.md) | The bot does not answer and Telegram rejects its token |
| [Disk full](runbooks/disk-full.md) | Low disk, backups refused for space, the database cannot write |
| [Backups not arriving](runbooks/backups-not-arriving.md) | No daily backup in Telegram, or a failed backup on the System page |
| [Rotate secrets](runbooks/rotate-secrets.md) | A token, password or key leaked, or a scheduled rotation |
| [License](runbooks/license.md) | The License tab is not "Active", or the license refuses an action |
| [Vendor license server](runbooks/vendor-license-server.md) | For the seller: deploy the license server, sell, renew and revoke licenses, back up and rotate the signing key |
| [HTTPS certificates](runbooks/https.md) | Choose the domain's, the server IP address's or your own certificate; a certificate alert; recovery when browsers refuse the dashboard |
| [Restore a backup](runbooks/restore.md) | Put the store back to a backup, or move it to a new server |
| [Update and roll back](runbooks/update-rollback.md) | Install a new release, or undo the last one |
| [Uninstall and start over](runbooks/uninstall.md) | Take BOBRES off a server so that it is as on day one (with a final backup), move the store, or start again |
| [Releases and update channels](runbooks/releases.md) | Publish a stable or beta release, re-sign a quiet stable channel, move a store between channels |

## Design notes and plans (history of each phase)

| Doc | Topic |
|---|---|
| [Phase 1 design](superpowers/specs/2026-09-30-phase1-design.md) | The first sellable slice: services, schemas, flows (the dashboard part moved to Phase 4) |
| [Phase 1 implementation plan](superpowers/plans/2026-09-30-phase1-implementation.md) | The task-by-task plan for Phase 1, with its status |
| [Phase 2 payments design](superpowers/specs/2026-10-04-phase2-payments-design.md) | Zarinpal, Telegram Stars, the gateway contract, and the owner decisions they need |
| [Phase 3 retention design](superpowers/specs/2026-10-06-phase3-retention-design.md) | Renewals, traffic packages, reminders, discount codes, referrals |
| [Phase 4 dashboard design](superpowers/specs/2026-10-07-phase4-dashboard-design.md) | The dashboard's milestones, APIs and decisions, the System page, backups and system alerts |

The docs describe what is built as of 2026-10-12. Open points are listed at the end of each file.
Known unverified items: Zarinpal's server-location rules, BOBRES trademark and domain availability, 3x-ui versions
outside v3.8.5 to v3.10.2. Before v0.1.0: the first install on a real server (11-staging-run.md), and a native
speaker's review of the Persian texts (08-brand.md). Before selling: the owner's steps in
[launch-checklist.md](launch-checklist.md), including a lawyer's review of the [LICENSE](../LICENSE) and of the legal drafts.
