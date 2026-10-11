// Helpers for the Payment gateways page: the Zarinpal form (write-only
// merchant id and proxy), the cards of the manual methods and Stars (their
// settings), and the payments report; and the overview's gateways card.
// Core and payments check everything again. Pure functions (no API, no Vue).
import { ApiError } from './api'
import { latinDigits, type Money } from './format'
import { effective } from './settings'
import type { GatewayConfig, GatewayHealth, GatewayReport, OverviewGateway, SettingItem } from './types'

/** Zarinpal's merchant id: the 36-character id from its panel. */
const merchantRe = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

/** normalizeMerchant is a typed merchant id as payments reads it. */
export function normalizeMerchant(raw: string): string {
  return latinDigits(raw).replace(/\s/g, '').toLowerCase()
}

export function merchantValid(raw: string): boolean {
  return merchantRe.test(normalizeMerchant(raw))
}

/** proxyValid: an http(s) proxy address (it may carry a user and password). */
export function proxyValid(raw: string): boolean {
  const v = raw.trim()
  if (!v || /\s/.test(v)) return false
  try {
    const u = new URL(v)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.hostname !== ''
  } catch {
    return false
  }
}

/** The Zarinpal card, as staff edit it. */
export interface ZarinpalForm {
  enabled: boolean
  sandbox: boolean
  /** a new merchant id; '' keeps the stored one */
  merchant: string
  /** a new proxy; '' keeps the stored one */
  proxy: string
  clearProxy: boolean
}

export function zarinpalFormOf(c: GatewayConfig): ZarinpalForm {
  return { enabled: c.enabled, sandbox: c.sandbox, merchant: '', proxy: '', clearProxy: false }
}

/** zarinpalProblems lists the fields that are not right yet, by the names
 * core uses. Turning it on needs a merchant id. */
export function zarinpalProblems(f: ZarinpalForm, c: GatewayConfig): string[] {
  const out: string[] = []
  if (f.merchant.trim() && !merchantValid(f.merchant)) out.push('merchant_id')
  if (f.proxy.trim() && !proxyValid(f.proxy)) out.push('proxy')
  if (f.enabled && !c.configured && !f.merchant.trim()) out.push('enabled')
  if (f.sandbox && !c.sandbox && c.sandbox_allowed === false) out.push('sandbox')
  return out
}

/** zarinpalDirty: the form would change something. */
export function zarinpalDirty(f: ZarinpalForm, c: GatewayConfig): boolean {
  return f.enabled !== c.enabled || f.sandbox !== c.sandbox || !!f.merchant.trim() || !!f.proxy.trim() || f.clearProxy
}

/** zarinpalBody is what PUT /gateways/zarinpal takes: '' keeps a secret. */
export function zarinpalBody(f: ZarinpalForm) {
  const merchant = f.merchant.trim() ? normalizeMerchant(f.merchant) : ''
  return { enabled: f.enabled, sandbox: f.sandbox, merchant_id: merchant, proxy: f.clearProxy ? '' : f.proxy.trim(), clear_proxy: f.clearProxy }
}

/** zarinpalTestBody is what POST /gateways/zarinpal/test takes: the typed
 * values, and '' for the stored ones. */
export function zarinpalTestBody(f: ZarinpalForm) {
  const { sandbox, merchant_id, proxy, clear_proxy } = zarinpalBody(f)
  return { sandbox, merchant_id, proxy, clear_proxy }
}

/** blockedByOpen: the form changes the merchant id or the sandbox while
 * payments are open, which payments refuses (they could not be verified). */
export function blockedByOpen(f: ZarinpalForm, c: GatewayConfig): boolean {
  const open = c.health?.open ?? 0
  return c.configured && open > 0 && (!!f.merchant.trim() || f.sandbox !== c.sandbox)
}

/** openPayments is how many open payments core named when it refused a new
 * merchant id or sandbox (they would never be verified), or null. */
export function openPayments(e: unknown): number | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null
  return typeof e.data.open === 'number' ? e.data.open : null
}

/** ok: payments go through; error: the last event was an error; idle:
 * nothing to tell yet. */
export function healthState(h: GatewayHealth | null | undefined): 'ok' | 'error' | 'idle' {
  if (!h) return 'idle'
  if (!h.healthy) return 'error'
  return h.last_paid_at ? 'ok' : 'idle'
}

// ---- The manual methods and Stars: one card each, from the payments settings.

export interface Method {
  key: 'manual_card' | 'manual_crypto' | 'manual_zarinpal' | 'stars'
  /** the on/off setting */
  toggle: string
  /** its details, in the order shown */
  fields: string[]
  /** offered only once one of these is set */
  needs: string[]
}

export const methods: Method[] = [
  { key: 'manual_card', toggle: 'payments.manual_card.enabled', fields: ['payments.card_number', 'payments.card_holder'], needs: ['payments.card_number'] },
  {
    key: 'manual_crypto',
    toggle: 'payments.manual_crypto.enabled',
    fields: ['payments.usdt_trc20', 'payments.usdt_erc20', 'payments.usdt_rate'],
    needs: ['payments.usdt_trc20', 'payments.usdt_erc20'],
  },
  { key: 'manual_zarinpal', toggle: 'payments.manual_zarinpal.enabled', fields: ['payments.zarinpal_link'], needs: ['payments.zarinpal_link'] },
  { key: 'stars', toggle: 'payments.stars.enabled', fields: ['payments.stars_rate'], needs: ['payments.stars_rate'] },
]

/** on: offered to customers; off: turned off; incomplete: on, but a detail
 * it needs is empty, so customers do not see it. */
export type MethodState = 'on' | 'off' | 'incomplete'

export interface MethodCard {
  /** a method's key, or 'other' for payments settings no card claims */
  key: Method['key'] | 'other'
  items: SettingItem[]
  state: MethodState | null
}

/** methodState is what customers get from the saved values. A core without
 * the switch counts as on. */
export function methodState(m: Method, items: readonly SettingItem[]): MethodState {
  const value = (key: string) => {
    const it = items.find((i) => i.key === key)
    return it ? effective(it) : ''
  }
  const toggle = items.find((i) => i.key === m.toggle)
  if (toggle && effective(toggle) !== 'true') return 'off'
  return m.needs.some((k) => value(k) !== '') ? 'on' : 'incomplete'
}

/** methodCards splits the payments settings into one card per method (the
 * switch first), and any key no card claims into a last card. */
export function methodCards(settings: readonly SettingItem[]): MethodCard[] {
  const claimed = new Set<string>()
  const cards: MethodCard[] = []
  for (const m of methods) {
    const items = [m.toggle, ...m.fields].map((k) => settings.find((i) => i.key === k)).filter((i): i is SettingItem => !!i)
    items.forEach((i) => claimed.add(i.key))
    if (items.length) cards.push({ key: m.key, items, state: methodState(m, items) })
  }
  const rest = settings.filter((i) => !claimed.has(i.key))
  if (rest.length) cards.push({ key: 'other', items: rest, state: null })
  return cards
}

export const stateSeverity: Record<MethodState, 'success' | 'secondary' | 'warn'> = { on: 'success', off: 'secondary', incomplete: 'warn' }

// ---- The overview's gateways card

/** off; ok: payments go through; incomplete: on, but a detail it needs is
 * missing, so customers do not see it (the Gateways page's "On, details
 * missing"); down: offered, but its payments fail. */
export type OverviewGatewayState = 'off' | 'ok' | 'incomplete' | 'down'

const manualKeys = new Set<string>(methods.map((m) => m.key))

/** overviewGatewayState reads a method on the overview. The manual methods
 * and Stars cannot fail: for them, not healthy only means a detail is
 * missing. Core may also say so for any method (ready: false). */
export function overviewGatewayState(g: OverviewGateway): OverviewGatewayState {
  if (!g.enabled) return 'off'
  if (g.ready === false) return 'incomplete'
  if (g.healthy) return 'ok'
  return manualKeys.has(g.provider) ? 'incomplete' : 'down'
}

export const overviewGatewaySeverity: Record<OverviewGatewayState, 'secondary' | 'success' | 'warn' | 'danger'> = {
  off: 'secondary',
  ok: 'success',
  incomplete: 'warn',
  down: 'danger',
}

// ---- The report

/** reportRange is a period of the last `days` days up to now, in unix seconds. */
export function reportRange(days: number, now = Date.now()): { from: number; to: number } {
  const to = Math.floor(now / 1000)
  return { from: to - days * 86_400, to }
}

export interface ReportCell {
  count: number
  total: Money[]
}

export interface ReportRow {
  status: string
  week: ReportCell
  month: ReportCell
}

/** Intent statuses in the order the report lists them; others follow. */
const statusOrder = ['succeeded', 'confirming', 'pending', 'failed', 'expired']

/** reportRows puts two periods side by side, one row per status. */
export function reportRows(week: GatewayReport | null, month: GatewayReport | null): ReportRow[] {
  const empty = (): ReportCell => ({ count: 0, total: [] })
  const cell = (r: GatewayReport | null, status: string): ReportCell => {
    const s = r?.totals?.find((x) => x.status === status)
    return s ? { count: s.count, total: s.amounts ?? [] } : empty()
  }
  const seen = new Set<string>(statusOrder)
  for (const r of [week, month]) for (const s of r?.totals ?? []) seen.add(s.status)
  return [...seen]
    .map((status) => ({ status, week: cell(week, status), month: cell(month, status) }))
    .filter((row) => row.week.count > 0 || row.month.count > 0 || statusOrder.includes(row.status))
}
