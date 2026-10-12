# Runbook: 3x-ui down

A 3x-ui panel does not answer BOBRES, or refuses its token. Commands run on the BOBRES server as root unless they say
"on the panel's host"; `dc` and `bsql` are defined in the [runbooks README](README.md#before-you-start).

## Symptoms

- **Overview**: the 3x-ui servers tile says "{n} of {total} not answering".
- **3x-ui servers** page: Health **Not answering**, with a detail and "Checked {time}". The provisioner checks every
  switched-on panel once a minute.
- **System** page, Health tab: the server list shows the same.
- New orders wait ([provisioning backlog](provisioning-backlog.md)); usage and links of services on that panel do not
  refresh. The VPN connections themselves depend on the panel's host, not on BOBRES.
- The provisioner's log: `3x-ui server failed its health check` (with `server` and `detail`), then
  `3x-ui server is healthy again` when it recovers.

## What BOBRES does on its own

A panel that fails its check gets no **new** services: they go to the other healthy panels the plan allows. Orders
whose first attempt already chose this panel, and renewals or top-ups of services on it, wait and are retried (up to
every 30 minutes) until it answers again. Nothing is lost. When the panel is back, its health turns **Working**
within a minute and the waiting orders go through.

## Checks

1. The detail on the 3x-ui servers page, or the log:

   ```sh
   bobres logs --tail 300 provisioner | grep -i -E '3x-ui|panel'
   bsql "SELECT name, base_url, enabled, last_health_ok, last_checked_at, left(last_error, 120) AS last_error
         FROM provisioner.xui_servers ORDER BY name;"
   ```

2. Can this server reach the panel? Use the panel's address as on the servers page (with its port and secret path):

   ```sh
   curl -sS -m 10 -o /dev/null -w '%{http_code}\n' 'https://panel.example.com:2053/path/'
   ```

   A number (200, 302, 404) means the panel answers; a timeout or `Could not resolve host` / `Connection refused`
   means it does not. For a panel on this same server (`http://host.docker.internal:<port>/...`), try
   `http://127.0.0.1:<port>/<path>/` here.

3. Edit server > **Test connection**: it tries the address and the token without saving. "Connected: {version},
   {n} inbounds" means the panel and the token work.

4. On the panel's host: is 3x-ui running (`x-ui status`, or `systemctl status x-ui`), and does its firewall let the
   BOBRES server's IP reach the panel port?

## Fixes by detail

| Detail | Meaning | Fix |
|---|---|---|
| `the panel did not answer in time` | Down, overloaded or filtered | On the panel's host: `x-ui restart`; check its load and firewall |
| `panel unreachable: ...` (`connection refused`, `no such host`, `i/o timeout`) | Wrong address or port, DNS, panel stopped, firewall | Fix the panel or the network. If the panel moved, Edit server > Panel address (the token must be entered again for a new host or port) |
| `the panel refused the API token` | The token was deleted or regenerated in 3x-ui | In 3x-ui: Settings, API tokens, create one. In BOBRES: Edit server > API token > Test connection > Save |
| `the stored API token cannot be decrypted (was the master key changed?)` | `BOBRES_PROVISIONER_MASTER_KEY` in `.env` is not the key the tokens were saved with | Put the old key back (from a copy of `.env`, or `/opt/bobres/.restore-point/files/.env`), then `bobres install --skip-checks --quiet`. If it is lost: enter every server's token again |
| `panel configuration is invalid` | The stored address is not usable | Edit server > Panel address |
| `xui: plain http is allowed only to private ...` and the other `xui:` address refusals | A plain-http address to a public host, or a private address without the owner's consent | Use the panel's https address, or (owner) turn on "Panel on a private network" for a panel on this server or a private network |
| `xui: panel error (http 5xx): ...` | The panel answered with an error | Look at the panel's own log on its host; update or restart 3x-ui |

While it is down for long:
- With other panels: switch this one's **Takes new services** off (tick "Save without testing" while it is down), so it
  does not get new services the minute it flaps back; its existing services keep working, renewing and syncing.
- With no other panel: new orders wait. Consider **maintenance mode** (Settings) so customers do not buy what cannot
  be delivered, or add another panel (3x-ui servers > Add server; plans open to any server use it at once).
- A panel gone for good: its orders that wait can be refunded (the order's page > Refund to wallet). A server with
  services cannot be deleted; switch it off instead.

After the fix: the Health column says **Working** within a minute; `bobres logs --tail 50 provisioner` shows
`3x-ui server is healthy again`; the Overview's failed deliveries go back to 0 within 30 minutes (or press **Retry
delivery now** on an order).

## Escalate when

- Test connection succeeds but the health check keeps failing (or the reverse).
- The panel's version is outside the tested range ("This panel version is outside the versions BOBRES is tested
  with") and calls fail after a 3x-ui update.
- Services on a working panel show wrong usage or links after it recovered.

Send: the server's name, the detail, the `xui_servers` query's output, and `sudo bobres support-bundle` (read it first;
it shows panel addresses).
