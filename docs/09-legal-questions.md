# 09 - Legal question list for a lawyer (DRAFT, not legal advice)

I am not a lawyer. This list is to bring to a qualified professional in your jurisdiction (and your customers' likely jurisdictions).
Goal: know the risks before selling, and get templates reviewed.

Status (2026-10-14): the owner decided how BOBRES is sold (free core, a subscription per install, [pricing.md](pricing.md))
and the first drafts exist: the top-level [LICENSE](../LICENSE) (source-available, all rights reserved),
[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md), and the operator documents in [legal/](legal/README.md) (Terms of
Service, Privacy Policy, Acceptable Use, Refund policy, a template for an operator's own store; each in English and
Persian). **A lawyer has reviewed none of them.** Every place that needs one is marked `[LAWYER: ...]`: list them with
`git grep -n "\[LAWYER:" -- LICENSE docs`. The questions below are what to settle first.

## A. Your business (selling BOBRES software)
1. Which legal entity should sell/license the software (individual vs company), and in which country?
2. Is distributing software whose purpose is to help sell VPN/proxy access restricted or regulated where you and your buyers live?
3. Export/sanction issues: can you license to customers in sanctioned or restricted countries? Payment routing for license fees?
4. Taxes: VAT/sales tax on software licenses, invoicing, withholding.
5. Payment processing for your license fees: which providers are permitted for your model?

## B. Licensing and IP
6. Proprietary EULA for signed binaries: grant, limits (installs, servers), no reverse engineering, no redistribution, termination, updates/support terms.
   Draft: the LICENSE (source-available, all rights reserved; the free tier is allowed without a license, everything else needs
   a paid one) and the Terms of Service. The repository is public: does the wording hold for a public repository, GitHub forks,
   contributions (a contributor license?), and a free tier that anyone can use?
7. License key terms: revocation conditions, grace periods, transfer/reactivation rules.
   Built: a subscription per install, short tokens renewed daily, 14 days of grace, 3 moves a year, and a fall back to the free
   tier when payment lapses. Is automatic lapse and revocation without notice acceptable? What notice is owed?
8. Trademark: is "BOBRES" registrable/available in relevant classes? Other projects already use the name. Any conflict risk?
9. Open-source obligations: our Go dependencies (licenses), fonts, and the fact that 3x-ui is GPL-3.0 (we only call its HTTP API and do not copy code) - confirm this interaction is compliant.
   Two more to check: the compose file pulls `redis:7-alpine`, and Redis changed its license after 7.2 (it is no longer BSD) -
   we do not redistribute it, but is that acceptable, or should the stack move to a BSD-licensed fork? And the `backup` image is
   built on `postgres:16-alpine`, which carries GPL components (BusyBox): does shipping it oblige us to offer their source?
10. Copyright notices and third-party notice files needed for distribution. Draft: THIRD_PARTY_NOTICES.md (generated from the
    Go modules linked into the binaries and the dashboard bundle). Is it enough?

## C. Liability and responsibility
11. Disclaimer/limitation of liability for what operators do with the software.
12. Acceptable-use policy: what operators may not do; your right to terminate.
13. Who is responsible for end-user content/traffic and for legal takedowns (should be the operator; how to state it).
14. Indemnification clauses.
15. Warranty/SLA language for updates and support.

## D. Data protection and privacy
16. Roles: operator = controller, you = ? (you do not receive end-user data; confirm structure).
17. Privacy policy for your website/license server (license id, install id, version, health telemetry).
18. Telemetry consent (opt-in) wording.
19. Template privacy policy and terms operators can adapt for their own end users; data retention/deletion (anonymize while keeping ledger).
20. Cross-border data considerations for customers' hosting locations.

## E. Payments (for operators, templates you provide)
21. Refund policy template and consumer-protection rules by region.
22. Rules of specific gateways (Zarinpal, Telegram Stars, crypto processors) about VPN-related merchants: are they allowed?
    Findings so far (2026-10-04, not legal advice): a 2021 Shaparak directive reportedly names VPN sales among
    merchants to cut; Zarinpal's terms forbid unlawful sales; Crypto Pay, Oxapay and NOWPayments exclude Iran;
    Telegram's Bot Developer ToS 6.2 requires Stars for digital goods sold inside Telegram.
23. Crypto: any registration/AML implications for operators receiving crypto payments? Reseller credit ("pay later") implications?

## F. Support and operations
24. Support commitments and response times in the contract.
25. Data processing and security incident notification duties.
26. What you may/must log or hand over on legal request (you hold little data by design).

## Deliverables to request from the lawyer
- Review and finish the drafts in `docs/legal/` (Terms of Service, Privacy Policy, Acceptable Use Policy, Refund policy, the template for
  an operator's own store), the LICENSE and THIRD_PARTY_NOTICES.md; a DPA-style clause if needed; trademark search and registration advice.
- The Persian texts next to each English draft need the same review, by someone who reads legal Persian.

## Open points
- Which country you operate from and where customers are expected. This changes almost every answer above.
