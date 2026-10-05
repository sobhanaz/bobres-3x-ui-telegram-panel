# First real install (staging run)

The last Phase 1 step: one install on a real server with a real bot and a real 3x-ui panel,
then one real purchase. CI covers the core path with a fake Telegram and a throwaway 3x-ui
container (the `docker` and `xui` jobs): install, a trial and a wallet purchase provisioned
on the panel, status, logs and uninstall. It does not cover card payment with receipt
approval, QR codes, reinstalling over existing data, or a VPN app actually connecting. This
run checks those, plus your network, your domain, Telegram itself and your panel's settings.

## 1. What you need

- A VPS with Ubuntu 22.04/24.04 or Debian 12, 2 GB RAM, 20 GB disk, ports 80 and 443 open.
- A domain (or subdomain) whose A record points to the VPS.
- A NEW Telegram bot from @BotFather (not the one 3x-ui's own bot uses) and your numeric
  Telegram ID (ask @userinfobot).
- Your 3x-ui panel, v3.8.5 or newer:
  - the panel URL including its web base path, e.g. `https://panel.example.com:2053/abc123`;
  - an API token: Settings → Security → API tokens → create, scope `admin`;
  - the subscription prefix: Settings → Subscription (e.g. `https://panel.example.com:2096/sub/`);
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
- The four `bobres-*` image packages on GHCR must be public, or every customer must log in to
  pull them: GitHub → your profile → Packages → each `bobres-*` → Package settings → Change
  visibility → Public. For your own server you can instead log in once as root (the installer
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

1. Message the bot `/start`, pick a language: the main menu appears.
2. As admin: `/plan_add 10000 IRT 1 1 Test 1GB | تست ۱ گیگ` and `/trial 1 1`.
3. From a second Telegram account: take the free trial. The service message must arrive with a
   link and QR, and the client must appear in the panel (Clients page) on every enabled inbound.
4. Import the link into a VPN app (v2rayNG, Hiddify, Streisand) and open a website.
5. Card payment: `/set payments.card_number ...` and `/set payments.card_holder ...`, buy the
   test plan with "card", send a photo as receipt, approve it from the admin panel; the second
   service must be delivered.
6. Wallet: credit the second account from Admin → Find user → Adjust, buy with the wallet.
7. `sudo ./bobres logs core` shows no errors; `sudo ./bobres uninstall` keeps the data,
   `install` again brings the same store back.

Anything that fails here: run `sudo ./bobres logs <service>` and keep the output for the fix.
Panel problems are logged by the provisioner (`sudo ./bobres logs provisioner`). To correct
the panel URL, token or options, run `install` again with the right values: the default
panel is updated on the next start, and the data is kept.

## 5. Phase 2 payments (when you enable them)

- **Telegram Stars:** `/set payments.stars_rate 1500` (Toman per Star). Buy the test plan
  with "Pay with Telegram Stars" from a second account: Telegram shows the invoice, and the
  service is delivered after paying (a few Stars at a low test price are enough; refund
  them later with Telegram's refund from the bot's Stars balance if needed).
- **Zarinpal:** read the owner decisions in the Phase 2 design document first. Then
  re-run install with `--zarinpal-merchant-id <id>` (the sandbox is for tests only and is
  refused in production). Register this server's IP, or the proxy's
  (`--zarinpal-proxy`), in the Zarinpal panel; the domain in `--zarinpal-public-url` (or
  your main domain) must be the one registered with Zarinpal and reachable from Iran
  without a VPN. Buy with "Pay online", turn the VPN off, pay, and check that you come back
  to a "Payment received" page and the service arrives in the bot.

## 6. After it passes

Tag `v0.1.0`. Phase 1 is done when this checklist passes on a real server.
