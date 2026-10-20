// The Gift codes page's rules: what the form accepts, what it sends, and the
// file of the codes just made. Core checks everything again (it is the source
// of truth); this only spares staff a round trip.
import { csvFile } from './csv'
import { asciiNumber, latinDigits, type Money } from './format'
import { typedAmount } from './resellers'

export const statuses = ['active', 'disabled', 'expired', 'used_up'] as const
export type GiftStatus = (typeof statuses)[number]

/** The most core takes (internal/core/domain/giftcodes.go). */
export const limits = {
  batch: 500,
  uses: 1_000_000,
  perCustomer: 100,
  note: 200,
  prefix: 8,
  codeMin: 8,
  codeMax: 32,
  codeDistinct: 5,
  days: 3650,
  amount: 1_000_000_000_000,
} as const

export interface GiftForm {
  mode: 'batch' | 'own'
  amount: string
  currency: string
  maxRedemptions: string
  perCustomer: string
  days: string
  note: string
  count: string
  prefix: string
  code: string
}

export const blankForm = (): GiftForm => ({
  mode: 'batch',
  amount: '',
  currency: 'IRT',
  maxRedemptions: '',
  perCustomer: '1',
  days: '',
  note: '',
  count: '10',
  prefix: '',
  code: '',
})

/** severity is the colour of a code's status tag. */
export function severity(status: string): 'success' | 'danger' | 'secondary' | 'warn' {
  switch (status) {
    case 'active':
      return 'success'
    case 'disabled':
      return 'danger'
    case 'used_up':
      return 'warn'
  }
  return 'secondary'
}

// What is dropped from a typed code: spaces, dashes, dots, underscores and
// the invisible marks Persian keyboards and chat apps add.
function isSeparator(cp: number): boolean {
  return (
    cp === 0x20 || cp === 0x09 || cp === 0x0a || cp === 0x0d || cp === 0x2d || cp === 0x5f || cp === 0x2e ||
    cp === 0xa0 || cp === 0xad || cp === 0x61c || cp === 0x2212 || cp === 0x2060 || cp === 0xfeff ||
    (cp >= 0x2010 && cp <= 0x2015) || (cp >= 0x200b && cp <= 0x200f) || (cp >= 0x202a && cp <= 0x202e) || (cp >= 0x2066 && cp <= 0x2069)
  )
}

/** canonicalCode is a code as core compares it: capitals and digits, no
 * separators, Persian digits as Latin ones. null when it holds anything else. */
export function canonicalCode(raw: string): string | null {
  let out = ''
  for (const ch of latinDigits(raw)) {
    if (isSeparator(ch.codePointAt(0) ?? 0)) continue
    if (!/^[A-Za-z0-9]$/.test(ch)) return null
    out += ch.toUpperCase()
  }
  return out
}

/** A problem with a field: a code for giftcodes.invalid.<code>. */
export type Problems = Partial<Record<keyof GiftForm, string>>

/** codeProblem judges a code staff typed: letters and digits only, 8 to 32 of
 * them, at least 5 different (nothing as easy to guess as AAAAAAAA). */
export function codeProblem(raw: string): string | null {
  const c = canonicalCode(raw)
  if (c === null) return 'code_chars'
  if (c.length < limits.codeMin || c.length > limits.codeMax) return 'code_length'
  if (new Set(c).size < limits.codeDistinct) return 'code_easy'
  return null
}

function wholeIn(s: string, min: number, max: number): number | null {
  const v = asciiNumber(s)
  if (!/^\d{1,9}$/.test(v)) return null
  const n = Number(v)
  return n >= min && n <= max ? n : null
}

/** minorUnits is the typed amount as minor units, or null when it is not a
 * positive amount core takes. */
export function minorUnits(f: GiftForm): number | null {
  const n = typedAmount(f.amount, f.currency)
  return n !== null && n > 0 && n <= limits.amount ? n : null
}

/** formProblems lists what is wrong with a form, by field. Empty fields are
 * wrong too (a required one is "missing" to the form and its button). */
export function formProblems(f: GiftForm): Problems {
  const p: Problems = {}
  if (minorUnits(f) === null) p.amount = f.amount.trim() === '' ? 'required' : 'amount'
  const uses = wholeIn(f.maxRedemptions, 1, limits.uses)
  if (uses === null) p.maxRedemptions = f.maxRedemptions.trim() === '' ? 'required' : 'uses'
  const per = wholeIn(f.perCustomer, 1, limits.perCustomer)
  if (per === null || (uses !== null && per > uses)) p.perCustomer = 'per_customer'
  if (f.days.trim() !== '' && wholeIn(f.days, 1, limits.days) === null) p.days = 'days'
  if ([...f.note].length > limits.note || /[\x00-\x1f\x7f]/.test(f.note)) p.note = 'note'
  if (f.mode === 'own') {
    if (f.code.trim() === '') p.code = 'required'
    else {
      const c = codeProblem(f.code)
      if (c) p.code = c
    }
  } else {
    if (wholeIn(f.count, 1, limits.batch) === null) p.count = 'count'
    const prefix = canonicalCode(f.prefix)
    if (prefix === null || prefix.length > limits.prefix) p.prefix = 'prefix'
  }
  return p
}

export const valid = (f: GiftForm): boolean => Object.keys(formProblems(f)).length === 0

/** requestBody is what POST /gift-codes takes. A day count becomes the moment
 * it ends, counted from now. */
export function requestBody(f: GiftForm, now = Date.now()): Record<string, unknown> {
  const days = f.days.trim() === '' ? null : Number(asciiNumber(f.days))
  const body: Record<string, unknown> = {
    amount: asciiNumber(f.amount),
    currency: f.currency,
    max_redemptions: Number(asciiNumber(f.maxRedemptions)),
    per_customer_limit: Number(asciiNumber(f.perCustomer)) || 1,
    expires_at: days === null ? null : Math.floor(now / 1000) + days * 86_400,
    note: f.note.trim(),
  }
  if (f.mode === 'own') body.code = latinDigits(f.code.trim())
  else {
    body.count = Number(asciiNumber(f.count))
    body.prefix = f.prefix.trim()
  }
  return body
}

/** howMany is the codes a form makes. */
export function howMany(f: GiftForm): number {
  return f.mode === 'own' ? 1 : (wholeIn(f.count, 1, limits.batch) ?? 0)
}

/** mostPaid is the most a form can pay out in all: every code used as often as
 * it may be, as an exact amount (null while the form cannot say). */
export function mostPaid(f: GiftForm): Money | null {
  const each = minorUnits(f)
  const uses = wholeIn(f.maxRedemptions, 1, limits.uses)
  const codes = howMany(f)
  if (each === null || uses === null || codes === 0) return null
  // Past 2^53 a number is no longer exact: say so with the biggest it can be.
  return { amount: Math.min(each * uses * codes, Number.MAX_SAFE_INTEGER), currency: f.currency }
}

/** What the file of new codes says about them. */
export interface Terms {
  amount: Money
  max_redemptions: number
  per_customer_limit: number
  expires_at: number | null
  note: string
}

/** codesFile is the new codes as the text of a CSV file, one a row with their
 * terms (the one copy of them there is: core keeps no code). */
export function codesFile(codes: readonly string[], terms: Terms): string {
  const when = terms.expires_at === null ? '' : new Date(terms.expires_at * 1000).toISOString()
  const decimals = terms.amount.currency === 'USDT' ? 6 : 0
  const amount = decimals === 0 ? String(terms.amount.amount) : (terms.amount.amount / 10 ** decimals).toFixed(decimals)
  return csvFile([
    ['code', 'amount', 'currency', 'max_redemptions', 'per_customer_limit', 'expires_utc', 'note'],
    ...codes.map((c) => [c, amount, terms.amount.currency, terms.max_redemptions, terms.per_customer_limit, when, terms.note]),
  ])
}
