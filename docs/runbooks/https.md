# Runbook: HTTPS certificates

How the store gets its HTTPS certificates, how to change that, and what to do when a certificate is expiring, failing
or locks browsers out. Commands run on the server as root; `dc` is defined in the
[runbooks README](README.md#before-you-start). How it is built: [06-installer.md](../06-installer.md), "HTTPS
certificates", and [07-security.md](../07-security.md), "HTTPS manager".

## Symptoms

- A Telegram alert: "The HTTPS certificate of ... expires in ... days", "... has not served a valid HTTPS certificate
  for 30 minutes", "Caddy, which serves the store's HTTPS addresses, is not answering" or "The store's HTTPS setting is
  not working as saved".
- Browsers warn about the dashboard's certificate, or cannot open the dashboard at all after a change.
- The System page, **HTTPS** tab, shows an address as "Not valid", "No certificate" or "Not reachable", the line "HTTPS
  gateway (Caddy) is not reachable", or a notice that your certificate is not in use.
- You want to open the store on the server's IP address, or use a certificate you bought.

## The three modes

| Mode | What the store serves | Certificate | Renewed |
|---|---|---|---|
| **Domain, automatic** (`auto`, the default) | `BOBRES_DOMAIN`, and the Zarinpal domain when there is one | Caddy gets a free one from Let's Encrypt | By Caddy |
| **Server IP address** (`ip`) | The same, and the store on the server's public IP address too | The domain keeps its automatic certificate. The IP address gets a Let's Encrypt certificate that lasts about 6 days (160 hours) | By Caddy, about 2 days before it ends |
| **Your own certificate** (`manual`) | `BOBRES_DOMAIN` with a certificate chain and private key you provide. The Zarinpal domain keeps its automatic certificate | Yours | By you: nothing renews it |

If `BOBRES_DOMAIN` is itself an IP address, `ip` mode must use that same address.

An IPv4 address as `BOBRES_DOMAIN` (the installer takes one: a private or LAN address of a lab or an office, or a public
one) is served in `auto` mode with Caddy's own certificate authority (`tls internal`). It is the only certificate
such an address can get there. Browsers do not trust it and warn, `bobres doctor` shows WARN, and the HTTPS tab says
"Caddy's own certificate, which browsers do not trust". Host names that Caddy never takes to a public CA (`localhost`,
and names ending in `.localhost`, `.local`, `.internal` or `.home.arpa`) get the same. A public address that is the
domain gets a Let's Encrypt certificate only in `ip` mode, with that same address.

## Check what the store serves

On the dashboard: **System > HTTPS**. Everyone who can open the System page sees the status; the owner also gets the
form. For each address (the domain, the IP address, the Zarinpal domain) it shows the issuer, the expiry date with the
days left, whether browsers trust it, and the live error when there is one. It also shows the address dashboard links
use now, the last answer of the HTTPS applier (the `tls-agent`) and, when the saved setting is not in use, why. When
Caddy answers on none of the addresses, a red line says "HTTPS gateway (Caddy) is not reachable" and every address reads
"Not reachable". One address that does not answer while the others do is that address's own problem.

On the server:

```sh
sudo bobres ssl status             # the same, plus what this host's port 443 answers
sudo bobres doctor                 # a TLS line for every served address (--dir D for an install elsewhere)
bobres logs --tail 100 tls-agent   # what was asked of Caddy, and Caddy's answer
bobres logs --tail 100 caddy       # issuing and renewing: look for "obtain" and "error"
```

`bobres doctor` looks at each served address through this host's port 443 (the line is `TLS` for the domain, `TLS ip`
and `TLS zarinpal` for the others; without `--domain` it reads `BOBRES_DOMAIN` from the install's `.env`, which only
root can read). PASS: a publicly trusted certificate that is not close to its end. WARN: not publicly trusted (a
self-signed one, one from a private authority, or Caddy's own authority), or close to its end because the renewal is
failing. FAIL: no certificate, one for other names, or an expired one. When Caddy keeps restarting (a crash loop), one FAIL line says
so; when it is not running at all, the line is SKIP.

## Switch the mode

Only the owner can change it. A change is saved only once Caddy has accepted it. If Caddy refuses, nothing changes and
the page shows Caddy's own words.

**On the dashboard** (System > HTTPS > the form):

1. Pick a card: **Domain, automatic**, **Server IP address** or **Your own certificate**.
2. For the IP address, press **Detect** (the bot asks an outside service for the server's public address), or type it.
   An address that is not the detected one needs the confirmation box ticked.
3. For your own certificate, paste the certificate chain (your certificate first, then the intermediate ones) and the
   unencrypted private key, or choose the files. Both are PEM. The key is cleared from the form after every send and is
   never shown again.
4. Press **Apply**. For a certificate Let's Encrypt must issue, the page says "Waiting for the certificate…" and reads
   the state every 3 seconds, for up to 90 seconds. Issuing can take a few minutes, so the page may stop waiting
   first: come back and look at the address's state.

**On the server** (works without the dashboard):

```sh
sudo bobres ssl auto                                          # the domain's automatic certificate
sudo bobres ssl ip                                            # also the server's public IP address
sudo bobres ssl ip --ip <public IP> --yes                     # an address you give, without the question
sudo bobres ssl manual --cert fullchain.pem --key privkey.pem # your own pair, read from these files
```

`bobres ssl ip` without `--ip` uses the server's public address, detected from the host. The same menu is in `sudo
bobres` under "HTTPS certificate".

## What Let's Encrypt needs

For the domain (`auto`, and the domain in `ip` mode):

- The domain's A (and AAAA) record points at this server.
- Ports 80 and 443 reach Caddy from the internet. The firewall (`bobres harden` opens them) and the hosting provider's
  firewall must allow them.

For the server's IP address (`ip`), in addition:

- A public IPv4 or IPv6 address that is really this server's. A private address and a carrier-grade NAT address
  (100.64.0.0/10) cannot get a certificate: the dashboard and the CLI refuse private ones, and Let's Encrypt rejects
  the rest. Some providers put several servers behind one address; then ports 80 and 443 do not reach you.
- Inbound 80 and 443 reachable from the internet at large. Let's Encrypt checks from several places, not one, so a
  firewall that allows one country, or geoblocks, fails. IP addresses have no DNS fallback.
- Caddy 2.11.3 or newer. The compose file pins
  `caddy:2.11.7-alpine@sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f` (the digest of the
  multi-arch index), which has it. The floors are 2.11.3 for IP certificates and 2.11.1 for the reload by signal that
  the installer sends after an update. `sudo bobres update` brings the pinned image of the release.
- Let's Encrypt's rate limits: an IPv4 address counts as its own domain, an IPv6 address as its /64; at most 5
  certificates for the same address in 7 days; at most 5 failed validations per address per hour. Renewals are not
  limited. When issuing fails, find the cause before you try again and again.

Read these warnings before you choose the IP address; the dashboard shows them too:

- Dashboard links (login links, tickets) move to `https://<ip>` only while two things hold: Caddy serves a publicly
  trusted certificate for the IP address, and the bot's own detection of the server's public address is that same
  address. The detection is renewed every minute and counts for 5 minutes; core asks the bot for it. Until both hold,
  links stay on the domain. A certificate alone proves nothing after an address has changed hands, because Caddy keeps
  serving a short-lived certificate for days, and whoever holds the recycled address could read a login link that still
  pointed at it.
- The price: where the bot reaches the internet from another address than the one visitors reach the server on (a VPN,
  a NAT egress, a second network interface, a floating IP address that moved), the two never agree, and the links stay
  on the domain. To make them use an address anyway, set `BOBRES_PUBLIC_URL=https://<address>` in `/opt/bobres/.env`
  and recreate core (`dc up -d core`; `bobres restart` keeps the old environment). That setting wins over everything,
  in any mode, and `bobres config` does not manage it. The HTTPS tab then says that links use it and do not move to
  the IP address. Remove the line, and recreate core again, to go back.
- The panel answers anyone who connects to the IP address, not only visitors who use the domain.
- Let's Encrypt publishes the IP address in public Certificate Transparency logs.
- Payment returns and Telegram webhooks keep using the domain, which keeps its own certificate.

## Renewal

- **Domain certificates:** Caddy checks every 10 minutes and renews at the time Let's Encrypt's renewal information
  names or, without it, when about a third of the certificate's life is left. Nothing to do.
- **The IP address's certificate:** it lasts 160 hours (a little under 7 days). Without renewal information from Let's
  Encrypt, Caddy renews it when about a third is left, about 2 days (53 hours) before it ends; Let's Encrypt can name an
  earlier time. A renewal changes nothing for visitors.
- **Your own certificate:** never renewed. The dashboard warns when you apply one that is close to its end, and the
  alert below fires before it ends. Upload the new pair the same way as the first (the old one is replaced).

## Alerts

The core watchdog (every minute) sends these to the staff who get system alerts, and shows them on **System > Health**.
`<role>` is `domain`, `ip` or `zarinpal`. A key marked "held" must fail that long before it is raised; the others
(`tls.<role>.expiring`, `tls.setting`) are raised once they are seen failing twice in a row, a minute apart. How every
alert is announced and fixed is in the [runbooks README](README.md#system-alerts-in-telegram).

| Alert | Key | Means | What to do |
|---|---|---|---|
| Caddy is not answering | `tls.caddy` (critical, held 3 minutes) | Core could not connect to Caddy on any of the served addresses for 3 minutes: it is stopped, restarting or crash-looping. A restart during an update is shorter. While it lasts, the two certificate alerts below are neither raised nor cleared: nothing can be said about certificates | `bobres status`, `bobres logs --tail 100 caddy`, `sudo bobres start`. A missing or broken `tls/sites.caddy` stops Caddy at its start; `sudo bobres install` writes the default again when the file is missing |
| The HTTPS certificate of an address expires in N days | `tls.<role>.expiring` | Less than the smaller of 14 days and a quarter of the certificate's life is left. For an automatic certificate Caddy has failed to renew it; for your own it has not been replaced | Automatic: check ports 80 and 443 and the DNS record, then `bobres logs --tail 200 caddy`. Your own: upload a new pair |
| An address has not served a valid HTTPS certificate for 30 minutes | `tls.<role>.failing` (critical, held 30 minutes) | Caddy answers, but issuing is failing, or the certificate is wrong for the name or not trusted where it should be. The 30 minutes give a new certificate time to be issued | Check the address's row on the HTTPS tab for the live error. Then the requirements above, and `bobres logs --tail 200 caddy` |
| The store's HTTPS setting is not working as saved | `tls.setting` (not held), `tls.agent` (held 5 minutes) | The saved setting cannot be used (the detail says why: `tls.setting`), or the HTTPS applier has not answered core for 5 minutes (`tls.agent`) | `tls.setting`: the next section. `tls.agent`: "The HTTPS applier did not answer" under Other problems, and `bobres status` for `tls-agent` |

## Recovery when the dashboard is unreachable

A wrong own certificate, or an IP address that does not lead here, can lock browsers out of the dashboard. On the
server:

```sh
sudo bobres ssl auto
```

It puts the store back on the automatic certificate for the domain. It is saved even when Caddy is not running, and
needs neither the dashboard nor a login. Then `sudo bobres ssl status` shows the domain's certificate. If the domain's
certificate cannot be issued either, fix that first (DNS, ports 80 and 443). When browsers accept the certificate
again, `sudo bobres admin link` prints a login link if a session ended meanwhile.

`bobres ssl` needs core to run. If it does not, start with `bobres status` and `bobres logs --tail 100 core`.

## An own certificate after a domain change, or a new master key

Core checks the saved certificate every time it starts. If it cannot be used any more, the store serves the automatic
certificate instead, the saved row stays, the alert "The store's HTTPS setting is not working as saved" is raised, and
the HTTPS tab says "Your own certificate is not in use" with the reason:

- **The domain changed** (`bobres config set BOBRES_DOMAIN`): the certificate does not cover the new name. Upload a
  certificate for the new domain, or choose the automatic mode.
- **A new master key** (a restore without the old keys, `--keep-keys`): the private key cannot be unsealed. Upload the
  certificate and key again.
- **It expired:** upload a new pair.
- **Core has no master key** (`BOBRES_CORE_MASTER_KEY` empty): an own certificate cannot be used at all, and the form
  turns it off. Run `sudo bobres install` to create the key.

## Other problems

| Problem | Cause and fix |
|---|---|
| "Caddy refused the change" | Caddy's words are shown under the message. The usual causes are an own certificate that does not match its key, or a name Caddy cannot use. Nothing was changed |
| "The HTTPS applier is not set up on this server", or `bobres ssl` says "this install predates the HTTPS manager" | The install has no working `tls-agent`: it runs images older than 0.1.0-rc.2 (the service exists from that release on), or its compose file is one from before the HTTPS manager (an install rolled back to an older release), or `COMPOSE_PROFILES` in `.env` lacks `tls` (the service sits behind that compose profile; `sudo bobres install --skip-checks` turns it on for releases that have it). `bobres status` does not list the service then, and `bobres ssl`, the menu item and `bobres doctor` run nothing in core (doctor shows one SKIP line). `sudo bobres update` |
| `bobres install` or `update` warns that a file in `tls/` "was a symbolic link, not a file" | The applier owns `tls/`, so anything that runs as it could plant a link or a pipe where the installer writes a default. The installer removes it (it never follows a link as root) and writes the default in its place. If it keeps coming back, treat the `tls-agent` container as compromised: `sudo bobres logs --tail 200 tls-agent`, then escalate. A directory in that place stops the install: remove it by hand |
| "The HTTPS applier did not answer" | `bobres status`: is `tls-agent` running and healthy? `bobres logs --tail 100 tls-agent`. `dc restart tls-agent` |
| "The HTTPS applier could not apply the change", with a line like "the request names the domain ..., not this install's domain" | The applier accepts only this install's own `BOBRES_DOMAIN` and `BOBRES_ZARINPAL_HOST`, from its environment, and refuses a request for any other name. Core and the applier run with different values: one was recreated after a change of `.env`, the other not. `bobres config set` recreates both. After editing `.env` by hand: `dc up -d core tls-agent` |
| The tab says "HTTPS gateway (Caddy) is not reachable" | Core cannot connect to Caddy on any address: stopped, restarting or crash-looping. `bobres status`, `bobres logs --tail 100 caddy`, `sudo bobres start`. See the alert `tls.caddy` above |
| "Caddy is not running" | `sudo bobres start`, then apply again. The automatic mode is saved anyway, and Caddy loads it when it starts |
| The IP address stays "No certificate" | Let's Encrypt cannot reach ports 80 and 443 at that address (the requirements above), or it is a carrier-grade NAT address. The log of Caddy has the reason. Choose the automatic mode while you fix it |
| Links still use the domain in `ip` mode | By design until the IP address's certificate is in use and the bot detects the server's public address as that same address (see the warnings above). The tab says so (with `BOBRES_PUBLIC_URL` set it says that instead: links never move then). If the certificate is in use and the links never move, the bot's address differs from the one visitors use (a VPN, a NAT egress, a floating IP address): press **Detect** to see what the bot reports, or set `BOBRES_PUBLIC_URL` |
| Telegram webhooks or an app refuse the store after an own certificate | They need a publicly trusted chain: a self-signed certificate, or one without its intermediate certificates, is not enough. The apply warned about it. Add the intermediate certificates, or go back to the automatic mode |
| The own certificate also covers the Zarinpal domain | Caddy then serves it there too, instead of an automatic one, until it ends. The apply warned about it |

## Escalate when

- `sudo bobres ssl auto` fails, or Caddy keeps restarting (`bobres status`).
- An automatic certificate is not issued although DNS and ports 80 and 443 are right.

Make a support bundle (`sudo bobres support-bundle`; it never holds certificates or keys) and send it with the output of
`sudo bobres ssl status`.
