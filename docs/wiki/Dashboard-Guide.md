# Dashboard Guide

The dashboard is where you run the store. It lives at `https://shop.example.com/admin` (your own domain), works in Persian and English, in dark and light themes, and shows only the sections your role may use.

## Signing in

| Way | Details |
|---|---|
| One-time link | Send `/dashboard` to your bot, or run `sudo bobres admin link` on the server. No password or code needed |
| Password and authenticator | Every password login needs a code from an authenticator app |
| Initial owner password | A fresh install, or `sudo bobres admin password`, gives the login `owner` and a random password, shown once. The first session only lets you pick a new password and set up the authenticator. Then the rest of the dashboard opens |

Sessions use secure cookies and CSRF tokens. If you lose your phone, `sudo bobres admin password` is the way back.

### Approve sign-ins in Telegram (optional)

The owner can turn on **Settings > Sign-in security > Approve sign-ins in Telegram**. It is off by default. When it is on, a staff sign-in (a link, or a password with its code) does not open the dashboard by itself. The sign-in page says **Waiting for approval in Telegram**, and the bot messages the staff member (and the owners, for other staff members' sign-ins, unless you turn that off) with the time, the IP address, the browser and the system, and two buttons: **Approve** and **Deny**. Approve opens the dashboard within a few seconds. Deny, or five minutes without an answer, opens nothing.

Turning it on shows what it does and asks you to confirm, and sends you a test message first, so you cannot turn it on with nobody able to answer. If the bot or Telegram cannot be reached, the sign-in page says so and does not wait. The way out is on the server: `sudo bobres admin approval off` (`sudo bobres admin approval status` shows whether it is on). It needs neither the bot nor a dashboard login. See the [runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/login-approval.md).

## Roles

| Role | Can do |
|---|---|
| Owner | Everything: license, staff, payment gateways, system actions, deleting a plan |
| Admin | Users, orders, plans, discounts, broadcasts, settings, branding, panel servers, reports and the audit log. Not staff, license or gateway secrets |
| Support | Tickets, read-only users and orders, and a few actions such as extending a service |

Resellers are not staff and have no dashboard access. They work in the bot and the Mini App. See [Resellers](Resellers). Owners are named on the server (`bobres admin owners`). Everyone else is invited on the Staff page. A staff seat counts against your license: see [Licensing](Licensing).

Every change is written to an append-only audit log. Sensitive actions, such as a balance change, a refund or a ban, ask for a reason.

## Sections

| Group | Section | What it is for |
|---|---|---|
| Store | Overview | Users, services, revenue, failed deliveries, gateway and panel health at a glance |
| Customers | Users | Find a customer, see their services, orders and wallet, ban, adjust a balance |
| | Services | Every subscription: extend, reset traffic, disable, delete, resync from the panel, regenerate the service's page address |
| | Support | Ticket inbox with canned replies. A customer's reseller application arrives here too |
| | Broadcasts | Send a message to all or some customers, now or later |
| Sales | Plans | Plans, plan groups, traffic packages, the free trial, reward codes. See [Plans and Pricing](Plans-and-Pricing) |
| | Orders & payments | Orders, the receipt **review queue**, retry delivery, refunds |
| | Wallet & ledger | Every wallet movement, with CSV export |
| | Reports | Sales per currency. See [Reports](Reports) |
| | Discounts & referrals | Discount codes, the referral reward, top inviters |
| | Gift codes | Codes that add credit to a wallet |
| | Resellers | Tiers, prices, credit limits, settlements |
| Setup | Branding & texts | Name, logo, colors, every bot text, FAQ and tutorials, your Terms and Privacy pages |
| | Settings | Time zone, maintenance mode, required channels and groups, notifications, limits, bulk purchase, daily sales summary |
| | Panel servers | Your 3x-ui and Marzban panels. See [Panels](Panels) |
| | Payment gateways | Owner only. See [Payments](Payments) |
| Administration | Staff | Who may use the dashboard, roles, sessions |
| | System | Health and version of every service, updates, backups, HTTPS, logs, license |
| | Audit log | Every staff change, searchable and exportable |

Two more sections, **BOBRES licenses** and **Digital accounts**, belong to the vendor's own store and are hidden on a normal install. See [Digital Accounts](Digital-Accounts).

## The System page

| Tab | Use it to |
|---|---|
| Health | See each service's version and readiness, open problems, panels and payment methods |
| Monitoring | Watch the host, containers and charts (owner and admin) |
| Updates | See the running and newest versions, choose the **stable** or **beta** channel, press **Update now** (owner) |
| Backups | See each backup, run one now, download or restore one (owner) |
| HTTPS | Choose the certificate mode. See [HTTPS](HTTPS) |
| Logs | Read the newest warnings and errors of every service (owner) |
| License | See the plan in effect, paste a token or activate a key. See [Licensing](Licensing) |

**Update now** asks for a confirmation that names the versions and the channel. The dashboard and bot are away for a few minutes while customers' VPN links keep working. A backup comes first, and the update rolls back by itself if the new version is not healthy. The first update to a version that has this feature must still be run over SSH with `sudo bobres update`. See [Updates, Backups, Restore](Updates-Backups-Restore).

## Habits that save trouble

- Check **Overview** each day for failed deliveries and payments to review.
- Keep **Settings > Staff notifications** pointed at the right people. Alerts for payments to review, new tickets, failed deliveries and system problems arrive in Telegram.
- Use **Test connection** on a panel or gateway before you save it.
- Do not change rows in the database by hand. Use the dashboard, the bot or `bobres`, so that the ledger, audit log and events stay consistent.

For the full list of pages and API details, read [docs/04-dashboard.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/04-dashboard.md).
