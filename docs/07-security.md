# 07 - Security and threat model (DRAFT)

This product moves money and holds a full-admin token to a VPN panel, so the main goal is: no theft of
funds, no takeover of the 3x-ui panel, no leak of user identity. Each customer is single-tenant, which limits blast radius.

## Assets (what attackers want)
1. Wallet balances and payment flows (free credit, double credit, refund abuse).
2. 3x-ui API token (full admin over the customer's VPN infrastructure).
3. Telegram bot token (impersonate the bot, message all users).
4. Payment gateway keys and crypto deposit addresses.
5. User data: Telegram IDs, usernames, subscription links (a leaked link = free VPN + identity link).
6. Dashboard admin sessions.
7. Vendor: license signing key, release signing key, customer list.

## Trust boundaries
Internet -> gateway (Caddy) -> services on a private Docker network. Only gateway exposes 80/443.
Postgres/Redis are never published to the host. Provisioner is the only service with the 3x-ui token;
payments is the only one with gateway secrets.

## Threats and mitigations (STRIDE-style, condensed)
| Threat | Mitigation |
|---|---|
| Forged payment callback | Verify provider signature/server-to-server re-check; never trust callback params; amount+order match |
| Replayed or duplicate callback | provider_events UNIQUE(provider, event_id), idempotency keys, ledger unique key |
| Race: double spend of wallet | DB transaction with row lock on wallet, CHECK balance >= 0 |
| Underpay / wrong currency crypto | Match exact amount + network + confirmations; unique deposit reference |
| Manual receipt fraud | Admin review queue, receipt hash de-dup, audit log |
| Trial / referral abuse | One per Telegram ID, velocity limits, delayed referral reward until first paid order |
| Bot flooding / DoS | Per-user and global rate limits, callback de-dup, queue for broadcasts |
| Dashboard brute force | Rate limit, lockout, 2FA, secure cookies, CSRF tokens, short sessions |
| SQL injection / XSS | Parameterized queries (pgx), Vue template escaping, CSP headers, input validation |
| SSRF via admin-entered URLs (3x-ui URL, webhooks) | Allow/deny lists, block metadata/loopback ranges unless explicitly allowed |
| Secret leakage in logs/backups | Redaction middleware, secrets never logged, backups encrypted, support-bundle scrubs |
| Stolen 3x-ui token | Token stored encrypted at rest, provisioner isolated, rotation command, recommend private/HTTPS access, IP allowlist on panel. The panel URL must be https; plain http is accepted only with allow-private, and every connection is then checked to go to a private, loopback or link-local address (`xui.ErrPublicPlaintext`). This bounds the first hop only: a private next hop that NATs or proxies onward is the operator's network, and a custom HTTP client (`xui.WithHTTPClient`) is refused for plain http |
| Supply chain | Pinned deps, govulncheck, trivy, signed images + SBOM, minimal base images, CI with least-privilege tokens |
| Malicious update / MITM of installer | Signature verification (ed25519/cosign), HTTPS, checksums, no unsigned code paths |
| License key sharing | Install binding, re-activation limits, revocation; accept residual risk |
| Compromised host | Non-root containers, read-only FS, dropped capabilities, ufw, fail2ban, unattended upgrades |
| Insider/staff abuse | Roles with least privilege (payment details, staff and the owner role are the owner's), mandatory reason on money and staff actions, an append-only audit log (database triggers refuse UPDATE, DELETE and TRUNCATE) that records where each change was made |
| Subscription link leak | Long random tokens, rotate/reset button, optional per-device limits (HWID in 3x-ui) |

## Crypto and secrets
- Secrets at rest: envelope encryption with an install master key (from `.env` / OS keyring); DB stores ciphertext for tokens and gateway keys.
- Passwords: argon2id. TOTP for staff. Constant-time comparisons for tokens/signatures.
- TLS everywhere external; internal service auth via one token per service, each server accepting only its legitimate callers (mTLS later; auth is one interceptor), see PLAN.md §11.
- Key rotation procedures documented and scripted (`bobres secrets rotate`).

## Privacy
- Data minimization: Telegram ID + language + service data. No phone/email unless a feature needs it.
- Retention limits for logs and provider events; user deletion = anonymize, keep ledger integrity.
- Vendor never receives customer end-user data.

## Security process
- Threat-model review each phase; dependency updates weekly; security tests for money flows (race, replay, tamper).
- `SECURITY.md` with a disclosure contact; incident runbook (rotate secrets, revoke license/tokens, notify).
- Pentest or external review before selling to third parties.

## Residual risks (accepted, stated)
- Self-hosted code can be modified by its owner. License protects billing, not secrecy.
- The 3x-ui API token is inherently full-admin; we can only isolate and protect it.
- Legal/regulatory exposure of the VPN business is outside technical control (see 09).

## Open points
- DECIDED: per-service tokens first, mTLS later.
- Where the master encryption key lives to survive reinstall (backup of key = part of restore drill).
- Whether to offer optional forced-2FA for owner accounts by default.
