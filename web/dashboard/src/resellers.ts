// Helpers for the Resellers pages: amounts as core sends them, balances and
// debt per currency (Toman first), the price a tier gives a plan, and what
// the forms send. Pure functions (no API, no Vue).
import { asciiNumber, currencies, decimalOf, latinDigits, minorOf, validAmount, type Lang, type Money } from './format'
import type { PlanItem, ResellerBalance, ResellerDetail, ResellerItem, TierItem, UserBrief } from './types'

/** Limits core checks again. */
export const MAX_DISCOUNT = 90
export const MAX_COMMISSION = 50
export const TIER_NAME_MAX = 40
export const NOTE_MAX = 500

/** amountOf is an amount as core sends it: minor units as a number (every
 * money field), or a decimal of major units as a string (tier prices).
 * Anything else counts as 0. */
export function amountOf(v: unknown, currency: string): number {
  if (typeof v === 'number' && Number.isFinite(v)) return v
  if (typeof v === 'string') return minorOf(asciiNumber(v), currency) ?? 0
  return 0
}

// Toman first, then the other currencies staff can choose, then the rest.
function rank(currency: string): number {
  const i = (currencies as readonly string[]).indexOf(currency)
  return i < 0 ? currencies.length : i
}

/** byCurrency sorts amounts Toman first. */
export function byCurrency<T extends { currency: string }>(list: readonly T[]): T[] {
  return [...list].sort((a, b) => rank(a.currency) - rank(b.currency) || a.currency.localeCompare(b.currency))
}

/** A reseller's wallet in one currency, in minor units. */
export interface WalletRow {
  currency: string
  /** negative: what they owe */
  balance: number
  limit: number
  /** what they can still spend: balance + limit */
  available: number
}

/** walletRows reads core's balances, Toman first; a reseller without a
 * wallet yet shows an empty Toman one. */
export function walletRows(list: readonly ResellerBalance[] | null | undefined): WalletRow[] {
  const rows = byCurrency(list ?? []).map((b) => {
    const balance = amountOf(b.balance, b.currency)
    const limit = amountOf(b.credit_limit, b.currency)
    return { currency: b.currency, balance, limit, available: balance + limit }
  })
  return rows.length ? rows : [{ currency: 'IRT', balance: 0, limit: 0, available: 0 }]
}

/** owed is what a balance below zero comes to (a positive amount), or null. */
export function owed(m: Money): Money | null {
  return m.amount < 0 ? { amount: -m.amount, currency: m.currency } : null
}

/** debts are the amounts owed, per currency. */
export function debts(rows: readonly WalletRow[]): Money[] {
  return rows.filter((r) => r.balance < 0).map((r) => ({ amount: -r.balance, currency: r.currency }))
}

/** percent writes a whole percent in the language ("۲۰٪", "20%"). */
export function percent(n: number, lang: Lang): string {
  return new Intl.NumberFormat(lang === 'fa' ? 'fa-IR' : 'en-US', { style: 'percent', maximumFractionDigits: 0 }).format(n / 100)
}

/** A reseller as the pages show it. */
export interface Reseller {
  id: string
  user: UserBrief & { status: string }
  tier: { id: string; name: string }
  /** the tier's discount off store prices */
  discount: number | null
  /** the commission in force, and the reseller's own when set */
  commission: number
  override: number | null
  wallets: WalletRow[]
  customers: number
  earned: Money[]
  createdAt: number
  note: string
  version: number
  canRemove: boolean
}

/** resellerOf reads a row or item answer from core. */
export function resellerOf(r: ResellerItem | ResellerDetail): Reseller {
  const d = r as Partial<ResellerDetail>
  const discount = r.tier?.discount_percent
  const wallets = walletRows(r.balances)
  return {
    id: r.user.id,
    user: r.user,
    tier: { id: r.tier?.id ?? '', name: r.tier?.name ?? '' },
    discount: typeof discount === 'number' ? discount : null,
    commission: r.commission_percent ?? 0,
    override: typeof r.commission_override === 'number' ? r.commission_override : null,
    wallets,
    customers: r.customers ?? 0,
    earned: byCurrency((r.commission_earned ?? []).map((m) => ({ amount: amountOf(m.amount, m.currency), currency: m.currency }))),
    createdAt: r.created_at,
    note: d.note ?? '',
    version: d.version ?? 0,
    // Older answers leave it out: allowed unless they owe money (core checks again).
    canRemove: typeof d.can_remove === 'boolean' ? d.can_remove : debts(wallets).length === 0,
  }
}

/** percentOf reads a typed whole percent from 0 to max ("۲۰", "20%");
 * null when it is not one. */
export function percentOf(s: string, max: number): number | null {
  const v = latinDigits(s).replace(/[%٪]/g, '').trim()
  if (!/^\d{1,3}$/.test(v)) return null
  const n = Number(v)
  return n <= max ? n : null
}

/** typedAmount reads a typed amount of at least zero as minor units; ''
 * is null, and so is anything the currency does not allow. */
export function typedAmount(s: string, currency: string): number | null {
  const v = asciiNumber(s)
  if (!v || !validAmount(v, currency)) return null
  return minorOf(v, currency)
}

/** tierPrice is a store price less a tier's discount, rounded down to the
 * currency's minor unit as core rounds it. */
export function tierPrice(price: number, discount: number): number {
  return Math.floor((price * (100 - discount)) / 100)
}

/** The plans a tier can price: everything but free trials. */
export function priceablePlans(plans: readonly PlanItem[]): PlanItem[] {
  return plans.filter((p) => !p.is_trial)
}

// ---- Tier editor

export interface TierForm {
  name: string
  discount: string
  commission: string
  /** plan id → a fixed price as typed ('' = the discount applies) */
  prices: Record<string, string>
}

export function blankTier(): TierForm {
  return { name: '', discount: '0', commission: '0', prices: {} }
}

/** tierFormOf reads a tier back into the editor (a price for a plan not
 * listed yet stays as core wrote it). */
export function tierFormOf(t: TierItem, plans: readonly PlanItem[]): TierForm {
  const prices: Record<string, string> = {}
  for (const p of t.prices ?? []) {
    const cur = plans.find((x) => x.id === p.plan_id)?.price.currency
    if (cur) prices[p.plan_id] = decimalOf({ amount: amountOf(p.price, cur), currency: cur })
    else if (typeof p.price === 'string') prices[p.plan_id] = p.price
  }
  return { name: t.name, discount: String(t.discount_percent), commission: String(t.commission_percent), prices }
}

/** tierProblems lists the fields that are not right yet, by the names core uses. */
export function tierProblems(f: TierForm, plans: readonly PlanItem[]): string[] {
  const out: string[] = []
  const name = f.name.trim()
  if (!name || [...name].length > TIER_NAME_MAX) out.push('name')
  if (percentOf(f.discount, MAX_DISCOUNT) === null) out.push('discount_percent')
  if (percentOf(f.commission, MAX_COMMISSION) === null) out.push('commission_percent')
  for (const p of priceablePlans(plans)) {
    const typed = f.prices[p.id] ?? ''
    if (typed.trim() && typedAmount(typed, p.price.currency) === null) out.push('prices.' + p.id)
  }
  return out
}

/** tierBody is what POST and PUT /reseller-tiers take (PUT adds the version):
 * only the plans with a fixed price. */
export function tierBody(f: TierForm, plans: readonly PlanItem[]) {
  return {
    name: f.name.trim(),
    discount_percent: percentOf(f.discount, MAX_DISCOUNT) ?? 0,
    commission_percent: percentOf(f.commission, MAX_COMMISSION) ?? 0,
    prices: priceablePlans(plans)
      .filter((p) => (f.prices[p.id] ?? '').trim())
      .map((p) => ({ plan_id: p.id, price: asciiNumber(f.prices[p.id]) })),
  }
}

/** The price a reseller on this tier pays for a plan, and whether it is fixed. */
export function planPriceFor(plan: PlanItem, f: Pick<TierForm, 'discount' | 'prices'>): { amount: number; fixed: boolean } {
  const fixed = typedAmount(f.prices[plan.id] ?? '', plan.price.currency)
  if (fixed !== null) return { amount: fixed, fixed: true }
  return { amount: tierPrice(plan.price.amount, percentOf(f.discount, MAX_DISCOUNT) ?? 0), fixed: false }
}

// ---- Terms (make a reseller, change their terms)

export interface TermsForm {
  tier: string
  /** currency → a credit limit as typed ('' = none) */
  limits: Record<string, string>
  /** '' = the tier's */
  commission: string
  note: string
}

export function blankTerms(): TermsForm {
  return { tier: '', limits: { IRT: '' }, commission: '', note: '' }
}

/** termsFormOf reads a reseller's terms into the editor. */
export function termsFormOf(r: Reseller): TermsForm {
  const limits: Record<string, string> = {}
  for (const c of currencies) limits[c] = ''
  for (const w of r.wallets) if (w.limit) limits[w.currency] = decimalOf({ amount: w.limit, currency: w.currency })
  return { tier: r.tier.id, limits, commission: r.override === null ? '' : String(r.override), note: r.note }
}

/** limitsBody is the credit limits core takes: each typed one, and 0 for a
 * wallet whose limit was cleared (an empty field with no wallet sends nothing). */
export function limitsBody(limits: Record<string, string>, wallets: readonly WalletRow[] = []): { currency: string; amount: string }[] {
  return currencies
    .filter((c) => (limits[c] ?? '').trim() || wallets.some((w) => w.currency === c && w.limit))
    .map((c) => ({ currency: c, amount: asciiNumber(limits[c] ?? '') || '0' }))
}

/** termsProblems lists the fields that are not right yet, by the names core
 * uses; a credit limit below what they owe is one. */
export function termsProblems(f: TermsForm, wallets: readonly WalletRow[] = []): string[] {
  const out: string[] = []
  if (!f.tier) out.push('tier_id')
  for (const c of currencies) {
    const typed = f.limits[c] ?? ''
    const limit = typed.trim() ? typedAmount(typed, c) : 0
    const debt = -(wallets.find((w) => w.currency === c)?.balance ?? 0)
    if (limit === null || limit < debt) out.push('credit_limit.' + c)
  }
  if (f.commission.trim() && percentOf(f.commission, MAX_COMMISSION) === null) out.push('commission_percent')
  if ([...f.note].length > NOTE_MAX) out.push('note')
  return out
}

/** termsBody is what POST /resellers and PUT /resellers/{id} take, apart
 * from user_id or version. */
export function termsBody(f: TermsForm, reason: string, wallets: readonly WalletRow[] = []) {
  return {
    tier_id: f.tier,
    credit_limits: limitsBody(f.limits, wallets),
    commission_percent: f.commission.trim() ? percentOf(f.commission, MAX_COMMISSION) : null,
    note: f.note.trim(),
    reason,
  }
}

// ---- Payments

/** paymentAmount reads a typed payment: more than zero, in the currency. */
export function paymentAmount(s: string, currency: string): number | null {
  const n = typedAmount(s, currency)
  return n !== null && n > 0 ? n : null
}

/** afterPayment is the balance a payment leaves in its currency. */
export function afterPayment(wallets: readonly WalletRow[], currency: string, amount: number): Money {
  const balance = wallets.find((w) => w.currency === currency)?.balance ?? 0
  return { amount: balance + amount, currency }
}

/** paymentCurrency is where a payment most likely goes: the first currency
 * they owe in, else Toman. */
export function paymentCurrency(wallets: readonly WalletRow[]): string {
  return wallets.find((w) => w.balance < 0)?.currency ?? 'IRT'
}
