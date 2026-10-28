# Security

A BOBRES store moves money and holds a full-admin token to your VPN panels. The design goal is no theft of funds, no takeover of the panel, and no leak of customers' identity. Each store is single-tenant, which limits the damage of any one mistake.

## How it is built

| Protection | What it means |
|---|---|
| Separate services | Core, bot, payments and provisioner each have their own database role and their own service token. A service accepts calls only from the services that should call it |
| Panel tokens | Only the **provisioner** holds the 3x-ui tokens and Marzban logins. A hacked bot or dashboard cannot read them |
| Gateway secrets | Only **payments** holds gateway keys |
| No internet for core | Core, which serves the dashboard and holds the ledger, has no internet access. Anything it needs from the internet goes through the bot's narrow internal endpoints |
| Hardened containers | Non-root, read-only filesystem, all capabilities dropped. Postgres and Redis are never published to the host. Only Caddy exposes ports 80 and 443 |
| Encryption at rest | Panel tokens, gateway secrets and authenticator secrets are encrypted with a master key per service |
| Signed releases | Releases are signed (Ed25519). `install.sh` and `bobres update` verify the signature and checksum before running anything. The images run by digests that the signed command carries |
| No control of Caddy | A small `tls-agent` with no network is the only writer of Caddy's TLS files |
| No Docker access for core | Updates and restores from the dashboard drop a small request file. A root helper on the host runs the same signed `bobres update` |

## Money

- A payment callback is never trusted. Only the payment's ID is read from it, then the gateway is asked to verify, and the amount must match.
- A payment settles once. Repeated callbacks and double taps change nothing.
- The wallet cannot go below zero (a reseller's can go down to their credit limit). The ledger is append-only: the database refuses updates and deletes.
- Manual receipts go through a review queue with duplicate detection by reference number.

## Who can do what

- **Owners** (root admins) are named in `BOBRES_ADMIN_TELEGRAM_ID` on the server. Only the server can make or remove one: use `bobres admin owners`. A hijacked Telegram or dashboard account can never become a permanent owner.
- Every password login needs an authenticator code. A login link comes from your bot or from `bobres admin link` on the server.
- The owner can also require that every staff sign-in is **approved in Telegram** (Settings > Sign-in security, off by default). A stolen link, or a stolen password and code, then opens nothing until the staff member or an owner taps Approve. If the bot or Telegram is out of reach, `sudo bobres admin approval off` on the server is the way out. See the [Dashboard Guide](Dashboard-Guide).
- The dashboard has CSRF protection, lockout after repeated wrong passwords, a strict content security policy and an append-only audit log. Money and staff actions ask for a reason.
- Roles follow least privilege. Payment details, staff and the license are the owner's.

## What BOBRES does for the server

The installer's hardening step sets up ufw (SSH, 80, 443), fail2ban for SSH, security-only automatic updates and a swap file on small servers, each only where it is safe. **It never changes your SSH settings**, and never restarts sshd. That part is yours.

## What you should do

- Use SSH keys, and turn off SSH password login yourself if you can.
- Keep the **backup key** off the server, and keep a recent backup off the server. The key is as sensitive as `.env`, because every backup carries your master keys.
- Never send `.env`, a `.bkp` file, the backup key or anything from `/opt/bobres/.restore-point/files/` to anyone.
- Give staff the lowest role that works. Remove people who leave. Seats count against your license.
- Update often: `sudo bobres update`. Security fixes reach every install, with no license needed.
- Read the Telegram alerts. Open the System page once a week.
- If a secret leaks, rotate it (see below).
- Run `sudo bobres check` and `sudo bobres doctor` regularly.

To rotate the bot token, for example:

```bash
sudo bobres secrets rotate bot-token
```

The other names are `db-passwords`, `service-tokens` and `redis-password`. The master keys and the backup key are not rotated yet. See the [rotate-secrets runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/rotate-secrets.md).

## Privacy

BOBRES keeps a customer's Telegram ID, language and service data. It asks for no phone number or e-mail unless a feature needs one. Your customers' data is not sent to the vendor. Activation of a license sends only the license key, install ID, domain and version. See [Licensing](Licensing).

The subscription page uses a random 128-bit address and never shows a customer's Telegram data. The logs redact secrets, and `bobres support-bundle` removes tokens, passwords and public IP addresses. Read it before you send it.

## Known limits

- The code runs on your server, so its owner can modify it. The license protects billing, not secrecy.
- A 3x-ui API token is full-admin by nature. BOBRES can only isolate and protect it.
- In `ip` HTTPS mode the IP address is public in certificate logs, and anyone scanning it finds your login page.
- An own HTTPS certificate is not renewed.
- Whether selling VPN service is legal where you live is outside what software can solve.
- No external penetration test is recorded in the docs. One is on the plan before selling widely.

## Report a vulnerability

Do **not** open a public issue. Use GitHub's private form: the repository's **Security** tab, then **Report a vulnerability**. Say what you found, how to reproduce it, the version (`bobres version`) and what an attacker could do. Do not test against a store that is not yours. Reports are handled on a best-effort basis, with no promised response time. Only the latest release is supported.

The full threat model is in [docs/07-security.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/07-security.md), and the policy is in [SECURITY.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/SECURITY.md).
