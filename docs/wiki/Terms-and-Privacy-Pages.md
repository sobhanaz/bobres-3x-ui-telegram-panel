# Terms and Privacy Pages

Every BOBRES store serves two public pages of its own, in Persian and English:

- `https://shop.example.com/terms`
- `https://shop.example.com/privacy`

Add `?l=en` for English. Without it the page is in Persian. Caddy routes both addresses to core, so they work as soon as the store is up.

## What is on them at first

A general default text. It already says what a store like yours sells (a VPN subscription for a time, a traffic amount, or both), how accounts, payments, wallets and renewals work, what customers may not do, how refunds work for undelivered services, and who to ask for help. It fills in two placeholders for you:

| Placeholder | Filled with |
|---|---|
| `{store}` | Your store's name (Branding) |
| `{contact}` | Your support contact (Branding) |

**The default is a starting point, not legal advice.** Read it, change what does not fit your store, and have a lawyer review it where you can. The laws that apply depend on where you and your customers are.

## Where customers see them

While you have set no address of your own, these pages are what the bot's **Terms** and **Privacy** buttons open, what `/terms` shows, and what the footer of the [subscription page](Subscription-Page) links to.

## Edit the text

1. Open the dashboard, then **Branding & texts**.
2. Find **Your Terms and Privacy pages**.
3. Edit the Terms or the Privacy text in Persian, in English, or both. A language you leave empty falls back to the default.
4. Save. To go back to the default for a language, choose the option to restore it.

The settings behind this are `legal.terms.fa`, `legal.terms.en`, `legal.privacy.fa` and `legal.privacy.en`.

### Text format

The text is a small version of markdown:

| You write | You get |
|---|---|
| `# Title` | A large heading |
| `## Part` | A smaller heading |
| `- item` | A bullet |
| `**bold**` | Bold text |
| An `https://` link | A link |

Everything else is plain text. The page escapes the text before showing it, so HTML or scripts you paste in are shown as text and do not run.

## Use your own pages instead

If you already have terms and a privacy policy on another site, put their addresses in **Branding & texts**:

- `branding.terms_url`
- `branding.privacy_url`

While either is set, the bot and the subscription page link to your address, and not to the built-in pages. The addresses must be `https://`.

## What to cover

A good store text answers, in plain words:

- What you sell, for how long, and what you do not promise (speed, uptime).
- How and when a payment counts, and how the wallet works.
- What a customer may not do with the service.
- When you refund, and when you do not.
- Which personal data you keep, why, for how long, and who sees it. BOBRES itself keeps a Telegram ID, a language and service data, and no phone number or e-mail unless a feature needs one.
- How to reach you.

There is a longer template for an operator's own store in the repository, with spaces for you to fill in: [operator-store-template.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/legal/operator-store-template.md). It is marked as a draft for a lawyer to review.

## Not the same as BOBRES's own terms

The terms and privacy policy in `docs/legal` that mention "the Vendor" are between you and the vendor of BOBRES: your license, what the vendor receives, and what you may not do with the software. They are drafts, and the legal review is still open. They do not replace what you tell your own customers. Your customers' data stays on your server and is not sent to the vendor. See [Licensing](Licensing) for what activation does send.

## Checklist

- [ ] Read both default texts in the language you use.
- [ ] Set your store name and a real support contact in Branding.
- [ ] Edit both pages, or enter your own addresses.
- [ ] Open `https://<your domain>/terms` and `/privacy` in a private window and check them.
- [ ] Review them again when you add a payment method or a country.
