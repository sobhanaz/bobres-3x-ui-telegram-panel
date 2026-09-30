# 05 - License and entitlement format (DRAFT)

Goal: one mechanism for trial / one-time / subscription / per-server / add-ons, without code changes.
Honest limit: any self-hosted software can be tampered with. The license deters casual sharing and
enables billing; the real protection is value that needs your updates and signed images.

## License token
Signed JSON (Ed25519). Vendor holds the private key offline; the public key is embedded in the binaries.
```json
{
  "v": 1,
  "license_id": "lic_...",
  "customer": "cust_...",
  "install_id": "hash of install fingerprint (nullable until activated)",
  "domain": "panel.customer.com (optional binding)",
  "tier": "trial|standard|pro",
  "issued_at": "...", "expires_at": "...", "grace_days": 14,
  "entitlements": {
    "white_label": false,
    "max_servers": 1,
    "max_active_services": 500,
    "resellers": true,
    "gateways": ["manual","zarinpal","stars"],
    "crypto_watcher": false,
    "dashboard_staff_seats": 3,
    "updates_until": "2027-09-30"
  },
  "sig": "..."
}
```

## Behavior
- Activation: installer sends license key + install fingerprint to the license server; receives the signed token; stored locally.
- Runtime checks are local (signature + expiry) - no network needed per request.
- Periodic refresh (e.g. daily) to pick up upgrades, renewals, revocations.
- Expired: warn -> grace period (features intact) -> degrade gracefully (existing services keep working, new sales/features restricted). Never delete customer data or cut existing end users off.
- Offline installs: manual token file import.
- Revocation: list fetched on refresh; short-lived tokens limit the window.
- `updates_until`: images signed newer than this are not installable, but the running version keeps working.

## Enforcement points
`core` exposes `HasFeature(name)` / `Limit(name)`; every gated feature calls it. Gateways and services check at startup and on `license.changed`.
Limits are soft-warned at 90% before hard-stopping.

## Vendor side (separate repo/service later)
Admin UI to create licenses, plans -> entitlement templates, customers, activations, revocations, trial issuance, usage view (only what installs voluntarily report).

## Privacy
Installs send only: license id, install fingerprint, version, health flags. Never end-user data, tokens or payment info. Telemetry beyond that is opt-in.

## Open points
- DECIDED: bind to domain + soft install ID; 3 re-activations per year.
- Trial length and whether trial is tied to a Telegram/email identity to stop repeats.
- Anti-tamper level: obfuscation is not planned; rely on signatures + updates + contract.
