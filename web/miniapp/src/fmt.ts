// Formatting in the current language, for templates.
import { lang, planName as pickName, tr, type Key } from './i18n'
import { ago, bytes, clock, date, dateTime, exactMoney, money, num } from './format'
import type { Plan, PlanGroup } from './api'

export const fmtMoney = (amount: number, currency: string, scale: number) => money(amount, currency, scale, lang.value)
/** fmtExact is an amount to send, with every decimal (fmtMoney keeps two). */
export const fmtExact = (amount: number, currency: string, scale: number) => exactMoney(amount, currency, scale, lang.value)
export const fmtBytes = (n: number) => bytes(n, lang.value)
export const fmtDate = (unix: number) => date(unix, lang.value)
export const fmtDateTime = (unix: number) => dateTime(unix, lang.value)
export const fmtNum = (n: number) => num(n, lang.value)
/** fmtClock is a time left as minutes:seconds, in the language's digits. */
export const fmtClock = (seconds: number) => clock(seconds, lang.value)
/** fmtAgo is how long ago a time was (unix seconds), by the device's clock. */
export const fmtAgo = (unix: number) => ago(unix, Date.now(), lang.value)
export const nameOf = (p: Plan) => pickName(p.names, lang.value)
/** A plan group's name and its text in the language (the text is empty when the store wrote none). */
export const groupNameOf = (g: PlanGroup) => pickName(g.names, lang.value)
export const groupTextOf = (g: PlanGroup) => pickName(g.texts, lang.value)

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
    case 'gift_code':
      return 'ledger.gift_code'
    case 'adjust':
      return 'ledger.adjustment'
    case 'license_purchase':
    case 'license_refund':
      return 'ledger.license'
    case 'supplier_purchase':
    case 'supplier_refund':
      return 'ledger.digital'
    case 'wallet_convert':
      return 'ledger.convert'
    case 'cashback':
    case 'cashback_reversal':
    case 'target_bonus':
    case 'wheel_prize':
      return `ledger.${kind}`
    case 'affiliate_commission':
    case 'affiliate_reversal':
    case 'affiliate_payout':
      return 'ledger.affiliate'
    default:
      return 'ledger.other'
  }
}
