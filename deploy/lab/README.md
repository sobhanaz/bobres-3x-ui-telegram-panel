# Lab: two Ubuntu 24.04 "servers" in Docker

Two plain Ubuntu 24.04 containers that behave like fresh VPSes: systemd is PID 1, SSH is on,
nothing of the project is installed. One is meant for the BOBRES stack (the Telegram bot), the
other for a 3x-ui panel. You install both yourself, exactly as on real servers.

| Server | Container | Hostname | Lab IP | Purpose |
|---|---|---|---|---|
| bot | `bobres-lab-bot` | `bot-server` | `10.10.0.10` | BOBRES (bot, dashboard, Postgres, Redis, Caddy) |
| xui | `bobres-lab-xui` | `xui-server` | `10.10.0.20` | 3x-ui panel + Xray |

The two servers see each other by name (`bot`, `xui`) and by lab IP, and both reach the internet.

## Start, stop, wipe

```bash
cd deploy/lab
cp -n .env.example .env            # once; set LAB_SSH_PUBKEY to your public key if not ~/.ssh/id_ed25519.pub
docker compose up -d --build       # build the Ubuntu image, start both servers
docker compose ps                  # both must be "running"
docker compose stop                # power off (everything you installed is kept)
docker compose start               # power on again
docker compose down -v             # DESTROY both servers and their volumes
```

`docker compose down` (without `-v`) also deletes the containers: only what lives on the
volumes survives (`/opt`, the inner Docker's data and the panel's files, see the compose file).
Use `stop` / `start` for day-to-day; `down -v` when you want fresh servers.

## Access

Shell as root, no password:

```bash
docker exec -it bobres-lab-bot bash
docker exec -it bobres-lab-xui bash
```

SSH (your public key is installed for root; host keys change whenever a server is recreated,
hence the two `-o` options):

```bash
ssh -p 2201 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no root@127.0.0.1   # bot
ssh -p 2202 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no root@127.0.0.1   # xui
```

Password logins are allowed too, once you set one inside a server with `passwd`.

Ports published on your Mac (127.0.0.1 only):

| Your Mac | Server | Port inside | What |
|---|---|---|---|
| `127.0.0.1:2201` | bot | 22 | SSH |
| `127.0.0.1:8080` | bot | 80 | Caddy HTTP (after the BOBRES install) |
| `127.0.0.1:8443` | bot | 443 (TCP+UDP) | Caddy HTTPS: the dashboard |
| `127.0.0.1:2202` | xui | 22 | SSH |
| `127.0.0.1:2053` | xui | 2053 | 3x-ui panel |
| `127.0.0.1:2096` | xui | 2096 | 3x-ui subscription server |
| `127.0.0.1:10443` | xui | 443 | an Xray inbound on 443 |
| `127.0.0.1:10080` | xui | 80 | an Xray inbound on 80 |

Need another port? Add it to `deploy/lab/docker-compose.yml` and run `docker compose up -d`
(the container is recreated; what you installed on a volume stays, the rest does not).

## Installing (reference)

3x-ui on `xui`, as on a VPS:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/mhsanaei/3x-ui/master/install.sh)
```

When it asks whether to customise the panel port, answer `y` and use `2053` (that is the port
published to your Mac). The panel is then at `http://127.0.0.1:2053/<web base path>` from the
Mac and at `http://10.10.0.20:2053/<web base path>` (or `http://xui:2053/...`) from the bot server.
`x-ui` opens the panel's management menu; `systemctl status x-ui` shows the service.

Without questions (a fixed version, port 2053, no certificate), as the staging run did:

```bash
curl -fsSL -o install-3xui.sh https://raw.githubusercontent.com/mhsanaei/3x-ui/v3.9.0/install.sh
XUI_NONINTERACTIVE=1 XUI_PANEL_PORT=2053 XUI_SSL_MODE=none bash install-3xui.sh v3.9.0
```

The generated username, password, web base path and an API token are then in
`/etc/x-ui/install-result.env` (root only). New panels have no inbound: add one (for example
VLESS on port 443, reached from the Mac as `localhost:10443`). The subscription path is random
too, not `/sub/`: read it in the panel (Settings → Subscription) for `--xui-sub-url`.

BOBRES on `bot`, as on a VPS (it installs Docker inside the server first):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
```

For the panel URL give the xui server's lab address, for example `http://10.10.0.20:2053/<path>`,
and allow private addresses (`--xui-allow-private`, or answer yes when asked): it is plain HTTP
on a private network. The bot works by long polling, so no public domain is needed. Use
`--domain bobres.localhost`: Caddy then signs the dashboard's certificate with its own local
authority, and the dashboard opens at `https://bobres.localhost:8443/admin` on the Mac (accept
the browser's warning once). The installer's DNS check shows FAIL for a lab domain; that is
expected and does not stop the install. `--no-harden` leaves the firewall and swap of the lab
server alone (`bobres harden` works there too, later). So that the bot's `/dashboard` login links
point at that port, add `BOBRES_PUBLIC_URL=https://bobres.localhost:8443` to `/opt/bobres/.env`
and recreate core: `docker compose --project-name bobres -f docker-compose.yml --env-file .env up -d core`
in `/opt/bobres` (`bobres restart` keeps the old environment).

Before a release exists, build the images inside the bot server from a checkout, the way CI
does, and install that version with a `bobres` CLI built for linux/amd64:

```bash
for s in core bot payments provisioner backup; do
  t=""; [ "$s" = backup ] && t="--target backup"
  docker build -f deploy/Dockerfile $t --build-arg SERVICE=$s --build-arg VERSION=0.1.0-lab.1 \
    -t ghcr.io/sobhanaz/bobres-$s:0.1.0-lab.1 .
done
bobres install --version 0.1.0-lab.1 --domain bobres.localhost --no-harden ...
```

If a build stops at `go mod download` with `EOF` (the inner Docker's network), run it again with
`docker build --network host ...`.

## How it works

- `privileged: true` on both: systemd as PID 1 needs it, and so does running Docker inside
  the bot server. These are lab machines, not a production layout.
- `/var/lib/docker` and `/var/lib/containerd` of the bot server are named volumes, so the
  Docker installed inside it gets a real overlay store instead of overlay-on-overlay (which
  the kernel refuses with "invalid argument" when a container is started).
- `/run`, `/run/lock` and `/tmp` are tmpfs, as systemd expects; the stop signal is systemd's
  power-off signal, so `docker compose stop` is a clean shutdown.
- Units that cannot work in a container (networkd, resolved, udev) are masked; the network and
  DNS come from Docker, which is also what makes `bot` and `xui` resolve.
