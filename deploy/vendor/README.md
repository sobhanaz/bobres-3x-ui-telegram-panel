# Vendor license server stack

The seller's own stack: the license server (`licensesrv`), its Postgres and Caddy for HTTPS (or a Cloudflare Tunnel, see
below). It is **not** part of a BOBRES
install and not in any release. Run it on its own small VPS.

| File | What it is |
|---|---|
| `docker-compose.yml` | The three services, hardened like a BOBRES install: non-root, read-only, no capabilities, internal networks, health checks |
| `.env.example` | The settings; copy it to `.env` (mode 600) and replace every `CHANGE_ME` |
| `caddy/Caddyfile` | HTTPS in front of the server; only `/v1/` is reachable |
| `postgres-init.sh` | Makes the server's own database role (not a superuser) when the database is first created |

**One server with a store on it:** `tunnel/` is the same stack for a server that already runs a BOBRES store (whose
Caddy owns ports 80 and 443): no port is published, a `cloudflared` container carries the license host and the public
site (`tunnel/site/`) through a Cloudflare Tunnel. Its compose file explains the networks; the runbook has a section
"One server, behind a Cloudflare Tunnel".

The **signing key** is a file outside this repository, mounted read-only (`LICENSE_SIGNING_KEY_FILE` in `.env` is only its
path). It is never an environment variable and never in `.env`.

Deploy, sell, renew, revoke, back up and rotate the key:
[docs/runbooks/vendor-license-server.md](../../docs/runbooks/vendor-license-server.md). The server's rules and API:
[docs/13-license-server.md](../../docs/13-license-server.md).

```sh
docker compose -f deploy/vendor/docker-compose.yml --env-file deploy/vendor/.env.example config -q   # validate
```
