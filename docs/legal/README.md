# Legal drafts

> **DRAFT. A lawyer must review every file in this folder before the public launch.** This is not legal advice, and it
> does not state the law of any country. Text in `[OWNER: ...]` is for the owner to fill in. Text in `[LAWYER: ...]`
> needs a lawyer: it is where the right words depend on a jurisdiction.

Drafts for BOBRES operators, in plain English. Each has a Persian version next to it (`.fa.md`). If the two differ, the
English text is the one to fix first: then bring the Persian text into line.

| Document | For | English | Persian |
|---|---|---|---|
| Terms of Service for BOBRES operators | What the operator and the vendor agree to, on the free tier and on a paid license | [terms-of-service.md](terms-of-service.md) | [terms-of-service.fa.md](terms-of-service.fa.md) |
| Privacy Policy | What BOBRES stores on an operator's server, what the vendor receives, and who is responsible for what | [privacy-policy.md](privacy-policy.md) | [privacy-policy.fa.md](privacy-policy.fa.md) |
| Acceptable Use Policy | What an operator may not do with BOBRES, and who answers for the VPN service | [acceptable-use.md](acceptable-use.md) | [acceptable-use.fa.md](acceptable-use.fa.md) |
| Refund policy for licenses | Subscriptions, how to cancel, what happens when one lapses | [refund-policy.md](refund-policy.md) | [refund-policy.fa.md](refund-policy.fa.md) |
| Operator's own store (template) | Terms and a privacy notice an operator can adapt for their own customers | [operator-store-template.md](operator-store-template.md) | [operator-store-template.fa.md](operator-store-template.fa.md) |

The source license is the top-level [LICENSE](../../LICENSE) (a draft too), and the dependencies' licenses are in
[THIRD_PARTY_NOTICES.md](../../THIRD_PARTY_NOTICES.md). The questions to take to the lawyer are in
[09-legal-questions.md](../09-legal-questions.md). What a license costs and allows is in [pricing.md](../pricing.md).

The four public texts (not the operator template) are also built as draft web pages in `site/legal/`, with a draft banner and `noindex`:
after you change a draft, run `go run ./tools/sitelegal` (a test fails if the pages are out of date).

Before the launch: fill in every `[OWNER: ...]`, have the lawyer settle every `[LAWYER: ...]`, publish the final
texts on the vendor's website, and link the Terms and the Privacy Policy from the place where operators get a license
([launch-checklist.md](../launch-checklist.md)).
