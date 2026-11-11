import { money, num, type Lang } from './format'
import type { UserBrief } from './types'

// Internal orders (docs/14-reports.md, "Internal orders"): what owner accounts buy is marked internal while the switch in Settings > Reports is
// on, left out of revenue and every report, and shown apart in a card on the Reports page. The two actions in the same Settings card count the
// owner accounts' earlier purchases as internal, or put them back.

/** One currency of the internal purchases: how many, how much, and what refunds gave back of them. */
export interface InternalCurrency {
  currency: string
  orders: number
  amount: number
  refunds: number
  refunds_amount: number
}

export interface InternalAccount {
  user: UserBrief | null
  orders: number
  currencies: InternalCurrency[]
}

/** GET /api/v1/reports/internal */
export interface InternalReport {
  from: string
  to: string
  tz: string
  /** whether the switch is on now */
  enabled: boolean
  orders: number
  currencies: InternalCurrency[]
  accounts: InternalAccount[]
}

/** The answer of POST /api/v1/reports/internal/backfill and /undo: what was (dry_run: would be) moved. */
export interface InternalMove {
  dry_run: boolean
  orders: number
  ledger_entries: number
  /** the purchases and refunds among the moved rows, per currency */
  currencies: InternalCurrency[]
}

/** showCard: the card has something to say: the switch is on, or owner accounts bought something in the range. */
export function showCard(r: InternalReport | null): r is InternalReport {
  return !!r && (r.enabled || r.orders > 0 || r.currencies.some((c) => c.refunds > 0))
}

/** moveEmpty: there is nothing to move (nothing to confirm). */
export function moveEmpty(m: InternalMove): boolean {
  return m.orders === 0 && m.ledger_entries === 0
}

/** amounts writes each currency's amount on its own, never added to another's: "6,000 Toman, 5 USDT". */
export function amounts(rows: { currency: string; amount: number }[], lang: Lang): string {
  return rows
    .filter((r) => r.amount !== 0)
    .map((r) => money({ amount: r.amount, currency: r.currency }, lang))
    .join(lang === 'fa' || lang === 'ar' ? '، ' : ', ')
}

/** counts: the numbers the confirmation and the toast fill in. */
export function counts(m: InternalMove, lang: Lang) {
  return { orders: num(m.orders, lang), entries: num(m.ledger_entries, lang), amounts: amounts(m.currencies, lang) || '—' }
}
