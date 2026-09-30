# 09 - Legal question list for a lawyer (DRAFT, not legal advice)

I am not a lawyer. This list is to bring to a qualified professional in your jurisdiction (and your customers' likely jurisdictions).
Goal: know the risks before selling, and get templates reviewed.

## A. Your business (selling BOBRES software)
1. Which legal entity should sell/license the software (individual vs company), and in which country?
2. Is distributing software whose purpose is to help sell VPN/proxy access restricted or regulated where you and your buyers live?
3. Export/sanction issues: can you license to customers in sanctioned or restricted countries? Payment routing for license fees?
4. Taxes: VAT/sales tax on software licenses, invoicing, withholding.
5. Payment processing for your license fees: which providers are permitted for your model?

## B. Licensing and IP
6. Proprietary EULA for signed binaries: grant, limits (installs, servers), no reverse engineering, no redistribution, termination, updates/support terms.
7. License key terms: revocation conditions, grace periods, transfer/reactivation rules.
8. Trademark: is "BOBRES" registrable/available in relevant classes? Other projects already use the name. Any conflict risk?
9. Open-source obligations: our Go dependencies (licenses), fonts, and the fact that 3x-ui is GPL-3.0 (we only call its HTTP API and do not copy code) - confirm this interaction is compliant.
10. Copyright notices and third-party notice files needed for distribution.

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
23. Crypto: any registration/AML implications for operators receiving crypto payments? Reseller credit ("pay later") implications?

## F. Support and operations
24. Support commitments and response times in the contract.
25. Data processing and security incident notification duties.
26. What you may/must log or hand over on legal request (you hold little data by design).

## Deliverables to request from the lawyer
- EULA, Terms of Service, Privacy Policy, Acceptable Use Policy, Refund policy template for operators, DPA-style clause if needed, trademark search/registration advice.

## Open points
- Which country you operate from and where customers are expected. This changes almost every answer above.
