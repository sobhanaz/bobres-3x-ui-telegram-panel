// The Affiliates page's types and small rules (internal/core/web/affiliates.go). Core checks everything again.
import { asciiNumber, type Money } from './format'

export interface AffiliateSettings {
  enabled: boolean
  /** on: the program runs (it needs license sales on: vendor_on). */
  on: boolean
  vendor_on: boolean
  percent: number
  min_payout: number
}

export interface Affiliate {
  id: string
  code: string
  /** the affiliate's own percent; null: the program's */
  percent: number | null
  status: 'active' | 'disabled'
  created_at: number
  user: { id: string; telegram_id: number; username: string } | null
  referred: number
  licenses: number
  earned: Money
  paid: Money
  balance: Money
}

export interface AffiliateCommission {
  order_id: string
  buyer: { id: string; telegram_id: number; username: string } | null
  tier: string
  license_id: string
  order_status: string
  base: Money
  percent: number
  amount: Money
  created_at: number
  reversed: Money | null
  reversed_at: number | null
}

export interface AffiliatePayout {
  id: string
  amount: Money
  note: string
  created_at: number
}

export interface AffiliateDetail {
  affiliate: Affiliate
  effective_percent: number
  commissions: AffiliateCommission[]
  payouts: AffiliatePayout[]
}

/** open is what an affiliate earned and was not paid yet (never below zero): the most one payout may be. */
export function openAmount(a: Pick<Affiliate, 'earned' | 'paid'>): number {
  return Math.max(a.earned.amount - a.paid.amount, 0)
}

/** wholeToman reads a whole number of Toman typed by the owner (Persian digits and separators allowed); null when it is not one. */
export function wholeToman(s: string): number | null {
  const v = asciiNumber(s).replace(/[,\s_٬]/g, '')
  if (!/^\d{1,12}$/.test(v)) return null
  return Number(v)
}

/** payoutProblem is the first thing wrong with a payout, or '' (core checks it again under the wallet's lock). */
export function payoutProblem(amount: number | null, a: Pick<Affiliate, 'earned' | 'paid' | 'balance'>, minPayout: number): '' | 'amount' | 'min' | 'open' | 'wallet' {
  if (amount === null || amount <= 0) return 'amount'
  if (minPayout > 0 && amount < minPayout) return 'min'
  if (amount > openAmount(a)) return 'open'
  if (amount > a.balance.amount) return 'wallet'
  return ''
}

/** percentProblem checks an affiliate's own percent: '' (the program's), or 0 to 100. */
export function percentValue(s: string): number | null | 'bad' {
  const v = asciiNumber(s).trim()
  if (v === '') return null
  if (!/^\d{1,3}$/.test(v) || Number(v) > 100) return 'bad'
  return Number(v)
}

/** reversedNote says what a refund took back of a commission: '' (not refunded), 'all', or 'part'. */
export function reversedState(c: Pick<AffiliateCommission, 'reversed' | 'amount'>): '' | 'all' | 'part' {
  if (!c.reversed) return ''
  return c.reversed.amount >= c.amount.amount ? 'all' : 'part'
}
