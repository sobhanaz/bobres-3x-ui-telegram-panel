# HTTPS

Your dashboard, the Mini App, the subscription pages and the payment callbacks are all served over HTTPS by Caddy. Most stores never touch this page: the default works. Read on if you want to open the store on the server's IP address, use a certificate you bought, or fix a certificate alert.

## The three modes

| Mode | Serves | Certificate | Renewed |
|---|---|---|---|
| **Domain, automatic** (`auto`, the default) | Your domain, and the Zarinpal domain if you use one | Free, from Let's Encrypt, got by Caddy | By Caddy |
| **Server IP address** (`ip`) | The same, and the store on the server's public IP address too | The domain keeps its own. The IP address gets a Let's Encrypt certificate that lasts about 6 days | By Caddy, about 2 days before it ends |
| **Your own certificate** (`manual`) | Your domain, with a chain and private key that you provide | Yours | By you. Nothing renews it |

## What Let's Encrypt needs

For the domain:

- The domain's A record (and AAAA, if you have one) points to this server.
- Ports 80 and 443 reach Caddy from the internet. `bobres harden` opens them in ufw. Your hosting provider's firewall must allow them too.

For the server's IP address, also:

- A public IPv4 or IPv6 address that really is this server's. Private and carrier-grade NAT addresses cannot get a certificate.
- Ports 80 and 443 reachable from the whole internet. Let's Encrypt checks from several places, so a firewall that allows only one country fails.
- Let's Encrypt limits: at most 5 certificates for the same address in 7 days, and 5 failed validations per address per hour. Find the cause before you retry.

## Change the mode

Only the owner can change it. A change is saved only after Caddy accepts it. If Caddy refuses, nothing changes and the page shows Caddy's own words.

**In the dashboard:** open **System > HTTPS**, pick a card, and press **Apply**. For the IP address, press **Detect** or type the address. For your own certificate, paste the certificate chain (yours first, then the intermediates) and the **unencrypted** private key, both PEM. The key is cleared from the form after sending and never shown again.

**On the server:**

```bash
sudo bobres ssl status
```

```bash
sudo bobres ssl auto
```

```bash
sudo bobres ssl ip
```

```bash
sudo bobres ssl manual --cert fullchain.pem --key privkey.pem
```

`ssl ip` accepts `--ip X` and `--yes`. The menu has the same choices under **HTTPS certificate**.

## Read the status

The HTTPS tab shows, for each address, who issued the certificate, the expiry date, whether browsers trust it, and the live error if there is one. On the server, `sudo bobres doctor` prints a TLS line for every address, and `sudo bobres ssl status` adds what this host's port 443 answers.

An IP address as your `BOBRES_DOMAIN` (for example in a lab) is served with Caddy's own authority. Browsers do not trust it and warn. Use a real domain, or `ip` mode with a public address, for a store with customers.

## Alerts

The watchdog checks every minute and messages the staff who get system alerts.

| Alert | Meaning | What to do |
|---|---|---|
| Caddy is not answering | Stopped, restarting or crash-looping for 3 minutes | `bobres status`, then `bobres logs --tail 100 caddy`, then `sudo bobres start` |
| A certificate expires in N days | Renewal is failing, or your own certificate is not replaced | Automatic: check DNS and ports 80 and 443. Own: upload a new pair |
| No valid certificate for 30 minutes | Issuing is failing, or the certificate is wrong for the name | Look at the address's row on the HTTPS tab, then the logs of Caddy |
| The saved setting is not in use | Your own certificate cannot be used (domain changed, key or master key changed, expired) | Upload a new certificate, or choose automatic |
| The HTTPS applier did not answer | The `tls-agent` is down | `bobres status`, then `bobres logs --tail 100 tls-agent` |

## Locked out of the dashboard

A wrong own certificate, or an IP address that does not lead to this server, can lock browsers out. On the server:

```bash
sudo bobres ssl auto
```

This puts the store back on the automatic certificate. It needs neither the dashboard nor a login, and it is saved even if Caddy is not running. Then `sudo bobres admin link` prints a login link if a session ended.

## Warnings for `ip` mode

- Dashboard links move to the IP address only while Caddy serves a publicly trusted certificate for it and the bot's own detection of the server's public address is that same address. Otherwise links stay on the domain. To force it, set `BOBRES_PUBLIC_URL` in `.env`.
- The panel answers anyone who connects to the IP address. It still needs a login.
- Let's Encrypt publishes the IP address in public Certificate Transparency logs.
- Payment returns and Telegram webhooks keep using the domain.

## Why a separate `tls-agent`

Core has no internet access and no control of Caddy. A tiny service, the `tls-agent`, has no network at all. It re-checks core's request, writes Caddy's files from fixed templates, and asks Caddy to load them. A hacked core cannot reach Caddy. See [Security](Security).

The full procedure is in the [HTTPS runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/https.md).
