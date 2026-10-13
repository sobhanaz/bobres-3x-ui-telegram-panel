# First real install (staging run)

The last step before the first public release (v0.1.0): one install on a real server with a
real bot and a real 3x-ui panel, then one real purchase. CI covers the core path with a fake
Telegram and a throwaway 3x-ui container (the `docker` and `xui` jobs): install, a trial and a
wallet purchase provisioned on the panel, a backup and a restore, status, logs and uninstall. It
does not cover card payment with receipt approval, QR codes, reinstalling over existing data,
or a VPN app actually connecting. This run checks those, plus your network, your domain,
Telegram itself and your panel's settings, the parts added in Phases 4 to 7 (section 7) and the HTTPS manager
(section 8).

## 1. What you need

- A VPS with Ubuntu 22.04/24.04 or Debian 12, 2 GB RAM, 20 GB disk, ports 80 and 443 open.
- A domain (or subdomain) whose A record points to the VPS.
- A NEW Telegram bot from @BotFather (not the one 3x-ui's own bot uses) and your numeric
  Telegram ID (ask @userinfobot).
- Your 3x-ui panel, v3.8.5 or newer:
  - the panel URL including its web base path, e.g. `https://panel.example.com:2053/abc123`;
  - an API token: Settings → Security → API tokens → create, scope `admin`;
  - the subscription prefix: Settings → Subscription (e.g. `https://panel.example.com:2096/sub/`;
    new panels use a random path instead of `/sub/`, so copy it from there);
  - at least one enabled inbound (new customers are attached to every enabled inbound).
- Panel on the same VPS:
  - If the panel has its own TLS certificate, use its public https URL and leave out
    `--xui-allow-private`.
  - If it serves plain http, use `http://host.docker.internal:<port>/<path>` with
    `--xui-allow-private`. This reaches the host through the Docker bridge, so the panel must
    listen on all interfaces (not only 127.0.0.1) and a firewall such as ufw must allow
    traffic from the Docker networks to the panel port. `127.0.0.1` or `localhost` do not
    work: inside the BOBRES containers they are the container itself, and the installer
    refuses them.

## 2. Publish a release candidate (owner decision)

Images are published by tagging, e.g. `v0.1.0-rc.1`: the Release workflow runs the unit
tests and govulncheck, builds signed multi-arch images to `ghcr.io/sobhanaz/bobres-*`, and
creates a GitHub pre-release with the `bobres` CLI binaries and signed checksums. It needs
the `RELEASE_SIGNING_KEY` secret and the `RELEASE_KEY_ID` variable (both are set; see
`tools/sign/README.md`). A pre-release tag does not move the `latest` image tag.

The repository is public, so `install/install.sh` downloads the CLI from its latest GitHub
release (a pre-release such as `-rc.1` is not "latest": use a plain `v0.1.0` tag for the
one-command install, or step 3's manual way for a release candidate). One thing to check
before customers install:
- The five `bobres-*` image packages on GHCR (core, bot, payments, provisioner, backup) must be
  public, or every customer must log in to pull them: GitHub → your profile → Packages → each
  `bobres-*` → Package settings → Change visibility → Public. For your own server you can instead log in once as root (the installer
  runs docker as root). This reads the token without echoing it or saving it in the history:
  `read -rs PAT && echo "$PAT" | sudo docker login ghcr.io -u sobhanaz --password-stdin; unset PAT`

## 3. Install

With a plain release tag (`v0.1.0`), as root on the VPS:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
```

For a release candidate, download `bobres_linux_amd64` from its GitHub pre-release
(`gh release download v0.1.0-rc.1 -p bobres_linux_amd64`, or the release page), copy it to
the VPS as `bobres`, `chmod +x bobres`, and run:

```bash
sudo ./bobres install --domain panel.example.com --admin-id <your id> \
  --xui-url https://panel.example.com:2053/abc123 --xui-sub-url https://panel.example.com:2096/sub/ \
  --version 0.1.0-rc.1
```

It asks for the bot token and the panel token (hidden input), checks the server, writes
`/opt/bobres`, starts everything and waits until all services are healthy. Then:

```bash
sudo bobres status     # or ./bobres for the downloaded binary; plain `bobres` opens the menu
```

## 4. Check, in this order

1. Message the bot `/start`, pick a language: the welcome comes with the bottom menu, then the main menu.
2. As admin: `/plan_add 10000 IRT 1 1 Test 1GB | تست ۱ گیگ` and `/trial 1 1`.
3. From a second Telegram account: take the free trial. The service message must arrive with a
   link and QR, and the client must appear in the panel (Clients page) on every enabled inbound.
4. Import the link into a VPN app (v2rayNG, Hiddify, Streisand) and open a website.
5. Card payment: `/set payments.card_number ...` and `/set payments.card_holder ...`, buy the
   test plan with "card", send a screenshot of the transfer (photo or image file) with the
   bank reference number as its caption, approve it from the admin panel; the second
   service must be delivered.
6. Wallet: credit the second account from Admin → Find user → Adjust, buy with the wallet.
7. `sudo ./bobres logs core` shows no errors; `sudo ./bobres uninstall` keeps the data,
   `install` again brings the same store back.

Anything that fails here: run `sudo ./bobres logs <service>` and keep the output for the fix.
Panel problems are logged by the provisioner (`sudo ./bobres logs provisioner`). To correct
the panel URL, token, subscription prefix or options, edit the server in the dashboard
(**Servers**): the installer's panel values only seed the first server, so running `install`
again does not change it. A changed subscription prefix also refreshes the links of the
services already sold on that server.

## 5. Phase 2 payments (when you enable them)

- **Telegram Stars:** `/set payments.stars_rate 1500` (Toman per Star). Buy the test plan
  with "Pay with Telegram Stars" from a second account: Telegram shows the invoice, and the
  service is delivered after paying (a few Stars at a low test price are enough; refund
  them later with Telegram's refund from the bot's Stars balance if needed).
- **Zarinpal link (manual, no merchant API):** `/set payments.zarinpal_link https://zarinp.al/yourname`.
  Buy the test plan with "Zarinpal (payment link)", pay the shown amount on that page, send the payment
  screenshot (the tracking code as caption is optional), approve it from the admin panel; the
  service must be delivered. USDT works the same way: a screenshot of the transfer or the TXID.
- **Zarinpal:** read the owner decisions in the Phase 2 design document first. Then
  re-run install with `--zarinpal-merchant-id <id>` (the sandbox is for tests only and is
  refused in production). Register this server's IP, or the proxy's
  (`--zarinpal-proxy`), in the Zarinpal panel; the domain in `--zarinpal-public-url` (or
  your main domain) must be the one registered with Zarinpal and reachable from Iran
  without a VPN. Buy with "Pay online", turn the VPN off, pay, and check that you come back
  to a "Payment received" page and the service arrives in the bot.

## 6. Phase 3 retention

- **Renew:** from a second account with a delivered service: My services → the service →
  Renew → the plan → pay. The service message says "renewed" with the new expiry and
  traffic, and in the panel the client has 30 more days and 50 GB more, with the same
  subscription link (nothing to re-import in the VPN app).
- **Traffic packages:** `/topup_add 30000 IRT 10 +10 GB | ۱۰ گیگ اضافه`, then the service →
  Add traffic → the package → pay: 10 GB more, same expiry.
- **Discount codes:** `/discount_add TEST50 50% 1 7`, then buy with "I have a discount code"
  and `TEST50`: half price; a second try says it was already used.
- **Referrals:** `/set referral.reward_percent 10`; open "Invite friends", start the bot from
  another account with that link, buy: the inviter gets 10% in the wallet.
- **Reminders:** they come from the usage sync (every 10 minutes): in the panel, set a
  test client's expiry to tomorrow or its traffic close to the limit, and the bot warns the
  customer once, with Renew / Add traffic buttons.

## 7. Phases 4 to 7

Commands use the `dc` helper from the [runbooks README](runbooks/README.md#before-you-start).

- **Dashboard:** send `/dashboard` to the bot and open the link. The Overview loads; **System →
  Health** shows every service healthy, on the same version; **Servers** shows the panel from the
  install, healthy, with its version.
- **Backups:** `sudo bobres backup`. The file arrives in the owner's Telegram, and **System →
  Backups** lists it. With S3 storage set up, the backup shows its off-site copy.
- **System alerts:** `dc stop provisioner`, wait 3 minutes: the owner gets "The provisioner service
  is down". `dc start provisioner`: a "Fixed" message follows about 5 minutes later.
- **Restore (test store only):** `sudo bobres restore /opt/bobres/backups/<newest>.bkp`. The store
  comes back healthy with the data of that backup.
- **Support bundle:** `sudo bobres support-bundle`, then open it and check that no token or
  password is in it.
- **Automatic USDT (when you enable it):** set the store's TRON address in **Payment gateways**,
  test it, then buy the test plan with "USDT TRC20 (automatic)" and send exactly the amount shown.
  The service arrives within a minute or two of the transfer.

## 8. HTTPS certificates

Commands use the `dc` helper from the [runbooks README](runbooks/README.md#before-you-start). Runbook:
[HTTPS certificates](runbooks/https.md). The lab cannot test the server IP address mode (see below).

- **Domain, automatic:** **System → HTTPS** shows the mode "Domain (automatic)", the domain "In use" with Let's
  Encrypt as issuer, "Trusted by browsers: Yes", its expiry date and days left, and the applier's last result.
  `sudo bobres ssl status` says the same. `sudo bobres doctor` has a TLS line for each address, and they pass. `dc ps`
  shows `tls-agent` healthy, and `bobres status` lists it (the menu's header counts 9 services with backups). The
  Overview and the System page show no HTTPS alert.
- **Your own certificate:** make a test pair for the domain (`openssl req -x509 -newkey rsa:2048 -nodes -days 30 -keyout
  key.pem -out cert.pem -subj /CN=<domain> -addext subjectAltName=DNS:<domain>`, in a folder you delete afterwards).
  - Apply a key that does not match: the form says so and nothing changes.
  - Apply the real pair: it is accepted, with a warning that it is self-signed. The domain row shows your certificate,
    "Trusted by browsers: No", and browsers warn. The key field is empty afterwards.
  - From the server, `sudo bobres ssl auto`: the dashboard opens with the Let's Encrypt certificate again (the
    recovery path when a wrong certificate locks browsers out).
  - Apply `sudo bobres ssl manual --cert cert.pem --key key.pem`, then `sudo bobres config set BOBRES_DOMAIN
    <another domain pointing here>`: the HTTPS tab says your certificate is not in use because it does not cover the
    domain, the alert "The store's HTTPS setting is not working as saved" arrives, and the store serves the automatic
    certificate. Set the domain back, then `sudo bobres ssl auto`.
- **Server IP address (a real server only):** the server needs a public IPv4 address that is not behind carrier-grade
  NAT, with ports 80 and 443 reachable from the internet. In the HTTPS tab choose "Server IP address", press **Detect**,
  read the warnings, apply. Wait for "The certificate is in use" (a few minutes). Check `curl -sv https://<ip>/healthz`
  (an issuer of Let's Encrypt, `shortlived` in its name or a validity of about 6 days) and that the dashboard's
  login links (`/dashboard` in the bot) now use `https://<ip>`. They move within a minute or two of the certificate, and
  only when the address the bot detects is that same address: on a server whose bot leaves through another address
  (a VPN, a NAT egress) they stay on the domain, which is the design (the runbook has `BOBRES_PUBLIC_URL`). Then choose
  "Domain (automatic)" again.
- **Update and roll back across the layout change.** The HTTPS manager moved Caddy's file to `caddy/Caddyfile` and added
  `tls/`. Check it on a real update:
  1. Start from an install of 0.1.0-rc.1, the last release candidate from before the HTTPS manager (the old layout, and
     no `tls-agent`: it exists from 0.1.0-rc.2 on). Note `ls /opt/bobres`: a top-level `Caddyfile`, no `caddy/` or
     `tls/`.
  2. `sudo bobres update`. Afterwards `ls -l /opt/bobres` shows `caddy/Caddyfile` (root, 0644) and `tls/` (65532:65532,
     0750) with `global.caddy` and `sites.caddy`; the top-level `Caddyfile` is still there, unchanged. `sudo bobres
     status` shows every service healthy, `tls-agent` included, and the dashboard opens over HTTPS with the same
     certificate (`dc logs caddy` shows the reload and no new issuing). `/opt/bobres/.restore-point/files/` holds the
     old files the 0.1.0-rc.1 `bobres` listed (`.env`, `docker-compose.yml`, `Caddyfile`, `postgres-init.sh`): the old
     layout has no `caddy/Caddyfile` to keep.
  3. `sudo bobres rollback`. The previous version comes back: `dc ps` has no `tls-agent` (the rollback removes it with
     `up -d --remove-orphans`), Caddy runs from the top-level `Caddyfile`, and the dashboard opens over HTTPS. `caddy/`
     and `tls/` stay on disk and are ignored.
  4. `sudo bobres update` once more, to see that an update works again after a rollback. This time the restore point
     (made by the new `bobres`) holds `caddy/Caddyfile` as well.
- **Downgrade to 0.1.0-rc.1 and back: the `tls` profile.** The compose file always defines `tls-agent`, but the rc.1
  image has no `tls-agent` command, so the service is behind the Compose profile `tls`, which every `bobres install`
  turns on or off from the image version. On the rc.2 install of the check above:
  1. `sudo bobres install --version 0.1.0-rc.1 --skip-checks` (the documented downgrade, with the new `bobres`).
     `grep COMPOSE_PROFILES /opt/bobres/.env` no longer has `tls`; `dc ps -a` has no `tls-agent` at all (the installer
     removed the one that was running, so nothing restarts in a loop); `sudo bobres status` is healthy and lists none;
     the run itself ended with "All services are healthy" (it did not wait for an agent).
  2. `sudo bobres ssl status` prints "this install predates the HTTPS manager (release 0.1.0-rc.1): run `sudo bobres
     update`" and exits 1; `dc logs core` shows no second start of core ("address in use") after it; `sudo bobres
     doctor` has one SKIP line, "HTTPS manager not installed yet"; the menu's HTTPS item says the same instead of
     opening its choices. A plain re-run, `sudo bobres install --skip-checks` (it keeps rc.1), changes nothing of this.
  3. `sudo bobres update` (to rc.2 or newer): `COMPOSE_PROFILES` has `tls` again, `dc ps` shows `tls-agent` healthy, and
     `sudo bobres ssl status` works as before.
  4. At the very end of the lab run, after a rollback has happened once, `sudo bobres uninstall --purge` (type the
     domain): `docker volume ls` lists no `bobres_*` volume, not even `bobres_tls_request`, `bobres_tls_result` or
     `bobres_caddy_admin`, which the rolled-back compose file does not declare (`down --volumes` alone leaves them).
- **Alerts (optional):** `dc stop tls-agent` and apply a change from the dashboard: after 20 seconds it answers "The
  HTTPS applier did not answer", and nothing changes. `dc start tls-agent` brings it back. If the agent stays stopped
  for 5 minutes with a request waiting, the "HTTPS setting is not working as saved" alert arrives, and a "Fixed"
  message follows about 5 minutes after the agent answers again. `dc stop caddy` for 4 minutes: the alert "Caddy, which
  serves the store's HTTPS addresses, is not answering" arrives (the dashboard is down too, so Telegram is the only
  sign), and "Fixed" follows about 5 minutes after `dc start caddy`.

In the lab (`deploy/lab`) Caddy's ports are published on the Mac's 127.0.0.1 only, so Let's Encrypt cannot reach it:
the domain is `bobres.localhost` with Caddy's own certificate, and the IP address mode cannot be tested. The lab can
try the form, the refusals, an own certificate and the recovery command, but not a Let's Encrypt certificate.

## 9. After it passes

Tag `v0.1.0` (the owner's decision: the release publishes the images and the CLI). Make the five
`bobres-*` image packages on GHCR public first (section 2).
