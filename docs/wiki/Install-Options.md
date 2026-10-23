# Install Options

`install.sh` is a small bootstrap. It installs Docker if needed, downloads the signed `bobres` command, verifies it, and hands over to `bobres install`. Every flag of `bobres install` can be passed through `install.sh`. The output below comes from `bobres install -h`.

## Passing flags

With the one-liner, put the flags after `-s --`:

```bash
curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh | sudo bash -s -- --domain shop.example.com --admin-id 123456789
```

On a server that already has `bobres`, run the same flags directly:

```bash
sudo bobres install --domain shop.example.com --admin-id 123456789
```

Never put a token on the command line. The shell history and `ps` keep command lines. The bot token and the panel token are asked with hidden input, or read from the environment (see below).

## Flags

| Flag | Meaning |
|---|---|
| `--domain` | Public domain of this install. DNS must point here |
| `--admin-id` | Telegram user ID of the owner. For several owners, separate the IDs with commas, main owner first (at most 10) |
| `--xui-url` | First 3x-ui panel URL, with any web base path. https only; plain http only with `--xui-allow-private`. Optional: servers can be added in the dashboard |
| `--xui-sub-url` | Public prefix of the panel's subscription links, for example `https://sub.example.com:2096/sub/` |
| `--xui-allow-private` | The panel is on this host or a private network |
| `--zarinpal-merchant-id` | Enable Zarinpal with this merchant ID (36 characters, from the Zarinpal panel) |
| `--zarinpal-proxy` | An http(s) proxy on a server whose IP is registered in the Zarinpal panel |
| `--zarinpal-public-url` | Domain registered with Zarinpal for the pay page and callback (default `https://<domain>`) |
| `--s3-endpoint` | Copy each backup to S3-compatible storage, `https://host` (Arvan, MinIO, Backblaze B2, AWS). The bucket and keys are asked if not given |
| `--s3-bucket` | The bucket for `--s3-endpoint` |
| `--s3-region` | The bucket's region (default `us-east-1`) |
| `--s3-prefix` | A folder in the bucket (default none) |
| `--no-harden` | Skip the firewall, fail2ban, security updates and swap step. `bobres harden` can run it later |
| `--ask-harden` | Ask before each hardening step instead of doing what is safe |
| `--ssh-port` | An SSH port the firewall must keep open. Needed when it cannot be detected |
| `--no-start` | Write the files but do not start the stack |
| `--offline` | Skip network checks, such as the bot token check |
| `--skip-checks` | Skip the system checks |
| `--quiet` | Leave out the first-steps summary (`bobres update` runs install this way) |
| `--yes` | Never ask. A missing value is an error, and hardening is skipped |
| `--version` | Image tag to run (default: this command's version) |
| `--dir` | Install directory (default `$BOBRES_DIR`, else `/opt/bobres`) |

Every command also takes `--plain` (no colors or redrawing) and `--no-banner`.

## Environment variables

These are read instead of asking, and keep secrets out of the command line:

| Variable | Used for |
|---|---|
| `BOBRES_DOMAIN`, `BOBRES_ADMIN_TELEGRAM_ID` | Same as `--domain` and `--admin-id` |
| `BOBRES_TELEGRAM_BOT_TOKEN` | The bot token |
| `BOBRES_XUI_TOKEN` | The first panel's API token |
| `BOBRES_BACKUP_S3_ACCESS_KEY`, `BOBRES_BACKUP_S3_SECRET_KEY` | S3 keys. They are never flags |
| `BOBRES_DIR`, `BOBRES_INSTALL_LOG` | Install directory and the log path |
| `BOBRES_PLAIN=1`, `BOBRES_NO_BANNER=1` | Same as `--plain` and `--no-banner` |
| `BOBRES_UPDATE_CHANNEL=beta` | For `install.sh`: allow a release signed for the beta channel |

## Common cases

**3x-ui on the same server.** Use its local address and allow private networks. A panel typed as `http://127.0.0.1:<port>/...` is rewritten to `http://host.docker.internal:<port>/...`, which is what the containers can reach:

```bash
sudo bobres install --xui-url http://127.0.0.1:2053/path --xui-allow-private
```

When 3x-ui runs on the same server, the installer skips the firewall step and tells you why. `sudo bobres harden` runs that step later and asks before each change.

**Off-server backups.** Pass the endpoint and bucket. The keys come from the environment or are asked:

```bash
sudo bobres install --s3-endpoint https://s3.example.com --s3-bucket my-shop-backups
```

**Scripted install.** As root, read the token into the environment without echo, then give every other value and add `--yes`:

```bash
read -rs BOBRES_TELEGRAM_BOT_TOKEN && export BOBRES_TELEGRAM_BOT_TOKEN
```

```bash
bobres install --yes --domain shop.example.com --admin-id 123456789
```

## What the installer does

1. Preflight: OS and CPU, RAM, disk, ports 80 and 443, Docker, and DNS (a DNS failure only warns).
2. Asks the missing values, then checks the bot token with Telegram and the panel token against the real panel.
3. Generates secrets and writes `/opt/bobres/.env` (mode 0600).
4. Renders the compose file and Caddy files, and pulls the images. They run by the digests built into the signed command.
5. Starts PostgreSQL and Redis, runs migrations, starts the services and waits for health.
6. Hardens the server: ufw (SSH port, 80, 443), fail2ban for SSH, security-only unattended upgrades, and a swap file on a small server. It never changes `sshd_config`.
7. Prints the "BOBRES is ready" card, the backup key and the first steps.

## Running it again

A re-run keeps every secret, fills only what is missing and recreates what changed. The first-panel flags only add a server while the store has none. After that, change servers in the dashboard.

To change one setting later, use `sudo bobres config set` (see [The bobres Command](The-bobres-Command)). For a full reference of the installer, read [docs/06-installer.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/06-installer.md).
