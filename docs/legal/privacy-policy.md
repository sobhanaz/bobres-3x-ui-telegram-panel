# Privacy Policy

> **DRAFT. A lawyer must review this text before it is used.** It is not legal advice and it does not state the law of
> any country. `[OWNER: ...]` is for the owner to fill in. `[LAWYER: ...]` needs a lawyer.

Version: draft 0.1, [OWNER: date]. It covers two things, and says for each who is responsible:

1. **The BOBRES software on an Operator's server**: what it stores about the Operator's customers and staff.
2. **The Vendor's license service and website**: what the Vendor receives from an Operator.

"The Vendor" is **[OWNER: legal name]**. "The Operator" is whoever runs a BOBRES install. "Customers" are the people
who buy from the Operator's store.

## 1. Who is responsible for what

- **The Operator is the controller of the customers' data.** BOBRES runs on the Operator's own server, with the
  Operator's database, bot, domain and payment accounts. The Operator decides why and how the data is used, and
  answers customers' requests.
- **The Vendor never receives the customers' data.** It is not stored on, copied to or sent to any Vendor system. The
  Vendor is therefore neither the controller nor the processor of it. [LAWYER: confirm these roles, and whether any
  data-processing clause is needed anyway.]
- **The Vendor is the controller of what the license service receives** (section 4), and of the Operator's contact and
  payment records.

## 2. What the install stores (on the Operator's server)

| Data | What it holds |
|---|---|
| Customers | Telegram ID, Telegram username, language, status (active or banned), who invited them, and when they joined |
| Wallet and ledger | Balances and an append-only list of every money movement |
| Orders and payments | What was bought, the amount, the payment method and its reference, and the receipt images or transaction ids that customers send as proof |
| VPN services | The client name on the 3x-ui panel, the subscription link, expiry, and traffic used. The panel holds the live VPN account |
| Support | Conversations: the text, photos and PDFs the customer and staff send |
| Staff | Username, password (stored as a hash), authenticator secret (encrypted), login sessions |
| Audit log | Who did what in the dashboard, the bot or the command line, when, and from which IP address |
| Settings | Branding, texts, prices, payment details of the store, and the license token. Secrets such as panel tokens and gateway keys are stored encrypted |

BOBRES does not ask for a phone number, an e-mail address or a name. It does not collect browsing or traffic content: it
reads only how much traffic a service used from the 3x-ui panel.

## 3. Where it goes

- **The Operator's server**: PostgreSQL and Redis in Docker volumes. Redis holds only what can be rebuilt.
- **The 3x-ui panels** the Operator adds: they hold the VPN client records (each service's client name and its usage).
- **Telegram.** Customers talk to the bot through Telegram, so Telegram handles those messages under its own policy.
  The bot's token is held by the Operator.
- **Payment providers** the Operator switches on (Zarinpal, Telegram Stars, the TRON network for automatic USDT)
  receive what a payment needs, under their own policies. BOBRES asks for no card numbers, but a receipt image that a customer sends may show some.
- **Backups.** Each day the install makes an encrypted backup. It holds all the data above and the store's keys. The
  newest 7 stay on the server, each is sent to the Operator on Telegram, and the Operator may add copies in
  S3-compatible storage (the newest 30 by default). The files are encrypted with the backup key, which stays on the
  Operator's server and is shown to the Operator. The Operator keeps the key safe: whoever has a backup and the key can
  read everything.

## 4. What the Vendor receives

Only an install with `BOBRES_LICENSE_SERVER` set talks to the Vendor. BOBRES has no telemetry.

| When | What is sent | Why |
|---|---|---|
| Activating with a license key | The key, the install id, the domain and the BOBRES version | To bind the license to that install |
| The daily renewal | The installed license token and the BOBRES version | To renew the license while it is paid |
| Every request | The IP address it came from | Rate limits and abuse control |

The license server stores:

- the license: a hash of the key (never the key), the customer name the Vendor gave it, the tier, the dates and the
  entitlements;
- each activation: the domain, the install id, the version and when it was last seen;
- an audit of what the Vendor did and of refused requests, with the IP address of requests from the network.

Logs hold license ids, domains, install ids, IP addresses and refusal codes. They never hold a key or a token.

The Vendor also keeps the Operator's contact details and payment records for sales, taxes and support:
[OWNER: what is kept and where]. [LAWYER: legal basis and the rules on payment and tax records.]

## 5. How long

- **On the install.** The ledger and the audit log are append-only and kept as long as the install has its data.
  Customers, orders, services and support conversations are kept while the Operator keeps them. Service logs rotate
  (about 50 MB per service). Uninstalling keeps the data unless the Operator asks to purge it. The Operator decides the
  retention that applies to the store and publishes it. [LAWYER: retention periods the Operator must apply.]
- **At the Vendor.** License and activation records are kept while the license exists and [OWNER: how long after].
  IP addresses in the audit and the logs: [OWNER: retention period]. Sales and tax records: [OWNER: period]. [LAWYER:
  periods that are lawful and reasonable.]

## 6. Rights and requests

- **Customers** ask the Operator. The Operator can delete a customer's VPN service, ban a customer and answer from the
  data in the dashboard. BOBRES has no one-click export or erasure of a customer's data yet; because the ledger and the
  audit log are append-only, erasing a customer means anonymizing, not deleting, what the ledger holds. [LAWYER: what the
  law of the Operator's customers requires, and what the product must do to meet it.]
- **Operators** ask the Vendor ([OWNER: contact]) for what the Vendor holds about them. The Vendor can correct it and,
  after a license ends, delete the records it need not keep.

## 7. Security

Each service has its own database role and token; secrets are stored encrypted with a key per service; staff log in with
a password and an authenticator code; the dashboard and the license service use HTTPS; the license service keeps only
key hashes. No system is perfectly secure. The Operator reports a problem to [OWNER: security contact]. The server's
security is the Operator's job: the Vendor cannot reach it.

## 8. Other countries

Data moves between the Operator's server, Telegram and the payment providers wherever they are. The license service
runs at [OWNER: country and host]. [LAWYER: rules on sending data across borders for the Operator's customers and for
the Vendor.]

## 9. Children

BOBRES is not made for children. The Operator decides who may buy from the store.

## 10. Changes and contact

The Vendor publishes changes at [OWNER: address]. Questions: [OWNER: e-mail address and postal address].
