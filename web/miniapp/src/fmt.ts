// Formatting in the current language, for templates.
import { lang, planName as pickName, tr, type Key } from './i18n'
import { bytes, date, dateTime, exactMoney, money, num } from './format'
import type { Plan } from './api'

export const fmtMoney = (amount: number, currency: string, scale: number) => money(amount, currency, scale, lang.value)
/** fmtExact is an amount to send, with every decimal (fmtMoney keeps two). */
export const fmtExact = (amount: number, currency: string, scale: number) => exactMoney(amount, currency, scale, lang.value)
export const fmtBytes = (n: number) => bytes(n, lang.value)
export const fmtDate = (unix: number) => date(unix, lang.value)
export const fmtDateTime = (unix: number) => dateTime(unix, lang.value)
export const fmtNum = (n: number) => num(n, lang.value)
export const nameOf = (p: Plan) => pickName(p.names, lang.value)

/** planFacts: "30 days · 50 GB" for a plan. */
export function planFacts(p: Plan): string {
  const parts: string[] = []
  if (p.days) parts.push(tr('plans.days', { n: fmtNum(p.days) }))
  if (p.traffic_bytes) parts.push(fmtBytes(p.traffic_bytes))
  else if (p.kind !== 'time') parts.push(tr('plans.unlimited'))
  return parts.join(' · ')
}

export function statusKey(status: string): Key {
  switch (status) {
    case 'active':
    case 'expiring_soon':
    case 'expired':
    case 'depleted':
    case 'disabled':
      return `status.${status}` as Key
    default:
      return 'status.pending'
  }
}

export function ledgerKey(kind: string): Key {
  switch (kind) {
    case 'topup':
      return 'ledger.topup'
    case 'purchase':
      return 'ledger.purchase'
    case 'refund':
      return 'ledger.refund'
    case 'adjust':
      return 'ledger.adjustment'
    default:
      return 'ledger.other'
  }
}
