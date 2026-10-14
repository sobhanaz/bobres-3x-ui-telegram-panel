# Terms of Service for BOBRES operators

> **DRAFT. A lawyer must review this text before it is used.** It is not legal advice and it does not state the law of
> any country. `[OWNER: ...]` is for the owner to fill in. `[LAWYER: ...]` needs a lawyer.

Version: draft 0.1, [OWNER: date]. Between **[OWNER: legal name of the vendor]** ("the Vendor") and the person or
company that installs or uses BOBRES ("the Operator").

## 1. What these terms cover

BOBRES is software for running a VPN store on Telegram: a bot, a web dashboard and the `bobres` command. The Operator
installs it on the Operator's own server. These terms cover the Operator's use of the software and of the Vendor's
license service. The source code is under the [LICENSE](../../LICENSE); where these terms and the LICENSE differ for a
paid license, these terms apply.

By installing BOBRES, installing a license key or token, or paying for a license, the Operator accepts these terms.
[LAWYER: how acceptance works for a free download, and whether the Operator must click or sign something.]

This text is written in English and in Persian ([terms-of-service.fa.md](terms-of-service.fa.md)). [LAWYER: which
version prevails if they differ.]

## 2. The software and the free tier

- Without a license, an install runs on the **free tier**: 1 server, 25 active services, 2 staff seats (the owner the
  install names takes none), the Zarinpal, Stars and manual payment methods, no resellers, no white label and no
  automatic USDT. The free tier has no time limit and no limit on updates. "Powered by BOBRES" is shown on every install
  unless a valid license includes white label. The numbers are in [pricing.md](../pricing.md). The Vendor may change the
  free tier for future releases; a change never takes a service away from customers who are already set up.
- Every install also has an implicit **trial of 30 days**, counted from the first start of a version that has this
  policy. During it the install has every premium feature except white label. The trial is kept once on the install and
  is not tied to a person. Restoring a backup made before this policy, or rolling back to an older version and then
  updating, starts it again, and the Vendor accepts that.
- The free tier is free of charge for the Operator's own store. It is not a license to host BOBRES for others, to
  resell it or to white-label it (see the LICENSE, sections 2 and 3).

## 3. Paid licenses

- **A subscription per install.** A paid license covers one install, bound to its domain and install id. It is monthly
  or yearly. The tiers (Standard and Pro) and what they allow are in [pricing.md](../pricing.md). The price is the one
  shown when the Operator orders. [OWNER: say where prices are published.]
- **The license key** is secret. The Operator keeps it private and does not share it. The key moves to another install
  or domain up to 3 times in 365 days; more needs the Vendor's help.
- **How it stays active.** The Operator's install asks the Vendor's license server for a short-lived token (30 days at
  most) and renews it every day. The Vendor does not charge anyone automatically. [OWNER: say how the Operator pays
  and renews: the Vendor's own channel.]
- **Payment, taxes and invoices.** [LAWYER: taxes on software licenses, invoicing, withholding, and which payment
  channels are allowed for the Vendor and the Operator.]
- **When payment lapses.** The Vendor stops renewing the license. The Operator keeps what the license gave until its
  end date plus its grace period (14 days by default), then the install falls to the free tier (or to the trial, if
  that still runs). Nothing the Operator stored is deleted. Customers' existing services keep working, renewing and
  topping up. What is new and beyond the free tier is refused: new purchases once 25 services are active (customers are
  told that new purchases are temporarily unavailable, and the staff get an alert), a second server, resellers,
  automatic USDT and more staff seats. Details: [pricing.md](../pricing.md).

## 4. Updates and support

- **Updates.** Releases are published as signed files. Every install can install them with `bobres update`: the free
  tier, the trial, a paid license and a lapsed one. A license does not buy updates, and the Vendor does not withhold
  any, security fixes included.
- **Support.** The scope and the response times per tier are: [OWNER: support scope and response times per tier].
  Support covers BOBRES. It does not cover the Operator's server, 3x-ui, payment gateways, the Operator's customers or
  the VPN service itself.

## 5. The Operator's responsibilities

- The Operator runs the store and the VPN service, sets the prices, serves the customers and answers to them.
- The Operator is responsible for obeying the laws that apply to the Operator and to the store: the VPN service, selling it,
  taking payments, taxes and customers' data. [LAWYER: laws that apply to the Operator; none are stated here.]
- The Operator follows the [Acceptable Use Policy](acceptable-use.md), and the rules of Telegram, the payment
  gateways and the 3x-ui panel they use.
- The Operator keeps the server, its backups and the backup key safe. The Vendor cannot read or restore the
  Operator's data.
- The Operator is the controller of the customers' data ([Privacy Policy](privacy-policy.md)) and publishes the terms
  and privacy notice its customers need ([operator-store-template.md](operator-store-template.md) is a starting
  point).

## 6. What the Vendor receives

Only an install with `BOBRES_LICENSE_SERVER` set talks to the Vendor. The Vendor then receives the license key, the
install id, the domain, the BOBRES version and the IP address of the request, and later the installed token. The Vendor
receives no customer data, no payment data, no bot token and no keys. The [Privacy Policy](privacy-policy.md) has the
detail.

## 7. Ownership

The Vendor owns BOBRES, its name, the beaver mascot and the brand assets. The Operator owns the Operator's own data,
brand, bot, domain and customer relationships. The Operator may use the BOBRES name only as the LICENSE allows. An
Operator who sends feedback lets the Vendor use it freely.

## 8. No warranty

BOBRES is provided "as is". The Vendor does not promise that it is free of errors, that the license service is always
up, or that BOBRES fits the Operator's purpose. [LAWYER: wording that is valid where the Vendor and the Operator are.]

## 9. Limit of liability

To the extent the law allows, the Vendor is not liable for lost sales, lost data, downtime, the Operator's
customers' claims, or what the Operator's customers do. Where a liability cannot be excluded, it is limited to the
fees the Operator paid in the 12 months before the claim. [LAWYER: the cap, and the cases in which it cannot apply.]

## 10. Indemnity

The Operator answers to the Vendor for claims that come from the Operator's store or the VPN service the Operator
sells, to the extent the law allows. [LAWYER: wording, or whether to include it at all.]

## 11. Ending

- The Operator can stop at any time: stop paying, or tell the Vendor ([refund-policy.md](refund-policy.md)). The install
  goes back to the free tier at the end of the paid period and its grace.
- The Vendor can revoke a license for a serious breach of these terms or of the Acceptable Use Policy, or for a
  chargeback or a payment reversed. A revoked license is not renewed; the install keeps its last token until it
  expires and its grace ends, then runs on the free tier. [LAWYER: notice and the right to answer before a
  revocation.]
- Sections 5, 7, 8, 9 and 10 survive the end.

## 12. Changes

The Vendor may change these terms for the future. It publishes the new version at [OWNER: address] and, for paid
licenses, tells the Operator [OWNER: how, and how long before]. An Operator who does not accept a change can stop at
the end of the paid period.

## 13. Law and courts

[LAWYER: governing law, venue and how disputes are settled. Do not fill this in without a lawyer: the right answer
depends on where the Vendor and the Operator are.]

## 14. Contact

[OWNER: e-mail address, Telegram contact and postal address of the Vendor.]
