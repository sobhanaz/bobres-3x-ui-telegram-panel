# Panels

A panel is the 3x-ui, Marzban, S-UI or Hiddify server that really creates your customers' VPN accounts. BOBRES talks to it through the **provisioner**, the only service that holds the panels' tokens. You can connect several panels at once.

## Tested versions

| Panel | Versions |
|---|---|
| 3x-ui | v3.8.5, v3.9.0, v3.10.0 and v3.10.2. CI runs the integration tests against a real panel of each |
| Marzban | 0.8.4, tested against the real image in CI. Marzban 0.8 is supported |
| S-UI | v1.6.6, tested against the real image in CI |
| Hiddify Manager | v14.0.5, tested with a fake panel in every CI run, and against the real image in a CI job that may fail (Hiddify's Docker image is experimental and heavy) |

Marzban 1.0 is still beta, has another API, and is not supported. A 3x-ui panel outside the tested range is marked **Untested** on the Servers page. It may still work.

## Add a server

Open **Panel servers** and press **Add server**.

| Field | Meaning |
|---|---|
| Name | Shown in Plans and on services. The installer names the first panel after its host |
| Panel software | 3x-ui, Marzban, S-UI or Hiddify. It cannot be changed later |
| Panel address | The panel's full address as you open it in a browser, with port and secret path, for example `https://panel.example.com:2053/path`. S-UI: with its web path, `https://panel.example.com:2095/app`. Hiddify: with the admin proxy path, `https://panel.example.com/AdminProxyPath` |
| API token | 3x-ui: create it under **Settings**, **API tokens**. S-UI: under **Admins**, **API Tokens**, with no expiry. It is stored encrypted and never shown again |
| Admin login | Marzban only: a dedicated admin written as `username:password` |
| API key | Hiddify only: the UUID of a dedicated admin, the part of the admin link after the admin proxy path |
| Subscription address | Optional. Where customers' subscription links point, such as the panel's subscription domain. S-UI: the address before the client's name, `https://sub.example.com:2096/sub/`. Hiddify: with the client proxy path, `https://sub.example.com/ClientProxyPath` |
| Weight | 1 to 1000. A panel with weight 200 gets about twice the new services of one with 100 |
| Service limit | Optional. The most services this server takes; empty or 0 means no limit. See [A limit on a server](#a-limit-on-a-server) |
| Takes new services | When off, no new service goes here. Existing services keep working and renewing |
| Panel on a private network | For a panel on this server or your private network. Only the owner can turn it on |

Press **Test connection** first. It reports the panel version and the number of inbounds without saving anything. The address must be https. Plain http is accepted only for a private network, and then every connection must go to a private address.

To change a panel's address or token to a different host, you must enter the token again. A server that has services cannot be deleted. Switch it off instead.

## How a new service finds a panel

A new service goes to the healthy panel with the fewest services for its weight, among the panels its plan allows that are not full. A plan can name the panels it may use, and the inbounds it joins on each. See [Plans and Pricing](Plans-and-Pricing). A retry of the same order stays on the panel its first attempt chose, so a customer never gets two accounts.

## A limit on a server

A panel on a small machine should not take more services than it can serve. Give a server a **Service limit** and BOBRES counts the services it holds (an order being delivered counts too). When the server has that many:

- **New services go to your other servers.** A full server is passed over, whatever its weight.
- **A plan that names only full servers waits.** The order stays paid and is delivered by the first retry after you raise the limit or add a server, as with a panel that does not answer. If it stays stuck, the usual "could not be delivered" alerts reach you too.
- **The services it has keep working**, renewing and syncing.
- Two orders arriving at the same moment cannot take the last place twice.

The Servers page shows **N / limit** with a bar, an orange chip with the percentage (for example **92% full**) from 90%, and a red **Full** at the limit. The Overview names such servers, and the bot sends you a notice (once a day while it lasts) when a server reaches 90% and again when it is full. Raising the limit ends the notice without a message. A limit set below the number of services a server already has moves none of them: the server is simply full. A server you switched off is not warned about, since it takes nothing anyway.

## Inbounds

For each server you choose which inbounds new services join (the **attach** switches). The panel's own switch still applies. A choice on a plan wins over the server switch. Existing services keep the inbounds they have. If no inbound is both on in the panel and chosen, new services on that server fail until you fix it.

## Client groups in 3x-ui and S-UI

Every client BOBRES makes in a 3x-ui or S-UI panel is put in the client group `BOBRES`. A reseller's clients go in `BOBRES-<NAME>`, with the name you give the reseller, so you can filter by who made them. A group you set by hand is never touched. Marzban and Hiddify have no groups. See [Resellers](Resellers).

## S-UI and Hiddify: what is different

- **S-UI** builds the share links from the address BOBRES calls it at. Give the panel's public domain as its address (or give the inbounds their own addresses in S-UI). The subscription link is S-UI's own (its subscription settings, or your subscription address, then the client's name).
- **Hiddify** gives every customer all of its proxies, so a Hiddify server lists one inbound, "all proxies". It counts expiry in whole days by its own calendar (UTC in its Docker image): a service that ends at 15:00 ends at the following midnight there, and BOBRES reads that date back. It has no "unlimited traffic": BOBRES writes its largest quota, 1,000,000 GB, and reads it back as unlimited. The subscription link is Hiddify's automatic one (`.../<UUID>/auto/`). A customer deleted in Hiddify and made again from BOBRES gets the same links back.
- Names: BOBRES finds its clients by name. If someone else already has the name in S-UI or Hiddify, the new service is refused, as on the other panels.

## Health

The provisioner checks every switched-on panel once a minute. The Servers page shows **Working**, **Not answering**, **Not checked yet** or **Off**, and the Overview counts the panels that do not answer.

When a panel fails its check:

- New services go to the other healthy panels the plan allows.
- Orders that already chose this panel, and renewals of services on it, wait and are retried. Nothing is lost.
- When it answers again, the waiting orders go through.

Customers' VPN connections depend on the panel's own host, not on BOBRES. If it is your only panel and it stays down, consider **maintenance mode** so customers do not buy what cannot be delivered.

## Things to know

- **Core has no internet.** All calls to panels come from the provisioner.
- **Panel on the same server.** Use `http://host.docker.internal:<port>/...` (the installer rewrites `127.0.0.1` for you) and turn on **Panel on a private network**.
- **Marzban** listens on the server's loopback only unless it has its own TLS certificate. BOBRES's containers cannot reach it until you put a reverse proxy in front or give it a certificate. A self-signed certificate is refused.
- **Token refused:** create a new API token in 3x-ui or S-UI (or check the Hiddify admin's UUID) and save it on the server's edit page.
- **Roles:** the owner and admins can add and edit servers. Only the owner can allow a private network.
- **License:** the free tier allows 1 server, Standard 3, Pro and the trial any number.

If a panel is down, follow the [3x-ui down runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/xui-down.md). If paid orders are not delivered, see the [provisioning backlog runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/provisioning-backlog.md).
