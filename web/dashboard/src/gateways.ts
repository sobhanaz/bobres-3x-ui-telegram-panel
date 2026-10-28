// Helpers for the Payment gateways page: the Zarinpal form (write-only
// merchant id and proxy), the IDPay, NextPay and Aqayepardakht forms
// (write-only API key, or PIN, and proxy), the automatic USDT form (address, write-only
// TronGrid key and proxy) and its unmatched deposits, the cards of the manual methods and Stars (their settings),
// and the payments report; and the overview's gateways card.
// Core and payments check everything again. Pure functions (no API, no Vue).
import { ApiError } from './api'
import { latinDigits, type Money } from './format'
import { effective } from './settings'
import { normalizeTronAddress, tronAddressProblem } from './tron'
import type { GatewayCheckError, GatewayConfig, GatewayHealth, GatewayReport, OverviewGateway, SettingItem, UnmatchedDeposit } from './types'

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

// ---- IDPay, NextPay and Aqayepardakht: built like Zarinpal, but the credential
// is one write-only secret instead of a merchant id: an API key (a 36-character
// id from the gateway's panel) for IDPay and NextPay, the gateway's PIN for
// Aqayepardakht. IDPay has a sandbox switch; NextPay has no sandbox.
// Aqayepardakht has no switch either: its test system is the PIN "sandbox"
// itself, which a production install refuses.

export const keyedProviders = ['idpay', 'nextpay', 'aqayepardakht'] as const
export type KeyedProvider = (typeof keyedProviders)[number]

export function isKeyedProvider(p: string): p is KeyedProvider {
  return (keyedProviders as readonly string[]).includes(p)
}

/** keyedOrder is where a gateway's card stands among the keyed ones. */
export function keyedOrder(p: string): number {
  return (keyedProviders as readonly string[]).indexOf(p)
}

/** keyedHasSandbox: only IDPay has a sandbox switch. */
export function keyedHasSandbox(p: string): boolean {
  return p === 'idpay'
}

/** keyedUsesPin: Aqayepardakht's credential is a PIN, not an API key. */
export function keyedUsesPin(p: string): boolean {
  return p === 'aqayepardakht'
}

/** An Aqayepardakht PIN: 4 to 100 printable characters, no spaces. It is not an
 * id and its case counts, so it is never lowercased. */
const pinRe = /^[\x21-\x7e]{4,100}$/

/** What the PIN field takes: a little more than a PIN may have, so a value
 * that is too long shows as invalid instead of being cut to a valid length. */
export const pinFieldMax = 128

/** The PIN of Aqayepardakht's test system. Payments turns on its sandbox for
 * it, and refuses it in production. */
export const sandboxPin = 'sandbox'

/** normalizePin is a typed PIN as payments reads it: trimmed, nothing else. */
export function normalizePin(raw: string): string {
  return raw.trim()
}

export function pinValid(raw: string): boolean {
  return pinRe.test(normalizePin(raw))
}

/** keyedSecretValid: a typed API key or PIN, by the rule of its gateway. */
export function keyedSecretValid(raw: string, provider: string): boolean {
  return keyedUsesPin(provider) ? pinValid(raw) : merchantValid(raw)
}

/** normalizeKeyedSecret is a typed API key or PIN as payments reads it: a key
 * is lowercased like a merchant id, a PIN keeps its case. */
export function normalizeKeyedSecret(raw: string, provider: string): string {
  return keyedUsesPin(provider) ? normalizePin(raw) : normalizeMerchant(raw)
}

/** The IDPay / NextPay / Aqayepardakht card, as staff edit it. */
export interface KeyedForm {
  enabled: boolean
  /** IDPay's sandbox switch; always off for the others */
  sandbox: boolean
  /** a new API key, or Aqayepardakht's PIN; '' keeps the stored one */
  apiKey: string
  /** a new proxy; '' keeps the stored one */
  proxy: string
  clearProxy: boolean
}

export function keyedFormOf(c: GatewayConfig): KeyedForm {
  return { enabled: c.enabled, sandbox: keyedHasSandbox(c.provider) && c.sandbox, apiKey: '', proxy: '', clearProxy: false }
}

/** keyedProblems lists the fields that are not right yet, by the names core
 * uses. Turning it on needs an API key (or PIN). Production refuses the test
 * system, which core names 'sandbox': IDPay's switch, or the PIN "sandbox". */
export function keyedProblems(f: KeyedForm, c: GatewayConfig): string[] {
  const out: string[] = []
  if (f.apiKey.trim() && !keyedSecretValid(f.apiKey, c.provider)) out.push('api_key')
  if (f.proxy.trim() && !proxyValid(f.proxy)) out.push('proxy')
  if (f.enabled && !c.configured && !f.apiKey.trim()) out.push('enabled')
  const wantsSandbox = keyedUsesPin(c.provider) ? normalizePin(f.apiKey) === sandboxPin : f.sandbox && !c.sandbox
  if (wantsSandbox && c.sandbox_allowed === false) out.push('sandbox')
  return out
}

/** keyedDirty: the form would change something. */
export function keyedDirty(f: KeyedForm, c: GatewayConfig): boolean {
  return f.enabled !== c.enabled || f.sandbox !== (keyedHasSandbox(c.provider) && c.sandbox) || !!f.apiKey.trim() || !!f.proxy.trim() || f.clearProxy
}

/** keyedBody is what PUT /gateways/{idpay|nextpay|aqayepardakht} takes: '' keeps
 * a secret. The key is sent in the form payments stores it (a PIN as typed,
 * trimmed); sandbox is only ever true for IDPay. */
export function keyedBody(f: KeyedForm, provider: string) {
  const key = f.apiKey.trim() ? normalizeKeyedSecret(f.apiKey, provider) : ''
  return {
    enabled: f.enabled,
    sandbox: keyedHasSandbox(provider) && f.sandbox,
    api_key: key,
    proxy: f.clearProxy ? '' : f.proxy.trim(),
    clear_proxy: f.clearProxy,
  }
}

/** keyedTestBody is what POST /gateways/{idpay|nextpay|aqayepardakht}/test
 * takes: the typed values, and '' for the stored ones. */
export function keyedTestBody(f: KeyedForm, provider: string) {
  const { sandbox, api_key, proxy, clear_proxy } = keyedBody(f, provider)
  return { sandbox, api_key, proxy, clear_proxy }
}

/** keyedBlockedByOpen: the form changes the API key (or PIN) or the sandbox
 * while payments are open, which payments refuses (they could not be verified). */
export function keyedBlockedByOpen(f: KeyedForm, c: GatewayConfig): boolean {
  const open = c.health?.open ?? 0
  return c.configured && open > 0 && (!!f.apiKey.trim() || f.sandbox !== (keyedHasSandbox(c.provider) && c.sandbox))
}

// ---- Automatic USDT (TRC20): the watcher finds exact amounts sent to the
// store's TRON address. The address is public (customers see it) and comes
// back; the TronGrid API key and the proxy are write-only.

export const usdtAuto = 'usdt_trc20_auto'

/** A TronGrid API key: letters, digits and dashes (it is a UUID today), as
 * core and payments accept it. */
export const apiKeyMax = 128
const apiKeyRe = /^[A-Za-z0-9-]{8,128}$/

export function apiKeyValid(raw: string): boolean {
  return apiKeyRe.test(raw.trim())
}

/** The automatic USDT card, as staff edit it. */
export interface UsdtForm {
  enabled: boolean
  /** the address as typed; it starts as the stored one */
  address: string
  /** a new API key; '' keeps the stored one */
  apiKey: string
  clearApiKey: boolean
  /** a new proxy for the TronGrid calls; '' keeps the stored one */
  proxy: string
  clearProxy: boolean
}

export function usdtFormOf(c: GatewayConfig): UsdtForm {
  return { enabled: c.enabled, address: c.address ?? '', apiKey: '', clearApiKey: false, proxy: '', clearProxy: false }
}

/** usdtAddressError is what is wrong with the typed address: format or
 * checksum (a character is wrong), or required (a stored address can be
 * replaced, not removed); '' when nothing. */
export function usdtAddressError(f: UsdtForm, c: GatewayConfig): '' | 'format' | 'checksum' | 'required' {
  const a = normalizeTronAddress(f.address)
  if (!a) return c.address ? 'required' : ''
  return tronAddressProblem(a)
}

/** usdtProblems lists the fields that are not right yet, by the names core
 * uses. Turning it on needs an address. */
export function usdtProblems(f: UsdtForm, c: GatewayConfig): string[] {
  const out: string[] = []
  if (usdtAddressError(f, c)) out.push('address')
  if (f.apiKey.trim() && !apiKeyValid(f.apiKey)) out.push('api_key')
  if (f.proxy.trim() && !proxyValid(f.proxy)) out.push('proxy')
  if (f.enabled && !normalizeTronAddress(f.address) && !c.address) out.push('enabled')
  return out
}

/** usdtDirty: the form would change something. */
export function usdtDirty(f: UsdtForm, c: GatewayConfig): boolean {
  return (
    f.enabled !== c.enabled ||
    normalizeTronAddress(f.address) !== (c.address ?? '') ||
    !!f.apiKey.trim() ||
    f.clearApiKey ||
    !!f.proxy.trim() ||
    f.clearProxy
  )
}

/** usdtBody is what PUT /gateways/usdt_trc20_auto takes: the whole address,
 * and '' to keep the API key or the proxy. */
export function usdtBody(f: UsdtForm) {
  return {
    enabled: f.enabled,
    address: normalizeTronAddress(f.address),
    api_key: f.clearApiKey ? '' : f.apiKey.trim(),
    clear_api_key: f.clearApiKey,
    proxy: f.clearProxy ? '' : f.proxy.trim(),
    clear_proxy: f.clearProxy,
  }
}

/** usdtTestBody is what POST /gateways/usdt_trc20_auto/test takes: the typed
 * values ('' for the stored key and proxy). A test changes nothing. */
export function usdtTestBody(f: UsdtForm) {
  const { address, api_key, clear_api_key, proxy, clear_proxy } = usdtBody(f)
  return { address, api_key, clear_api_key, proxy, clear_proxy }
}

/** usdtBlockedByOpen: the form changes the address while automatic payments
 * are open, which payments refuses (USDT sent to the old address would not
 * be found). */
export function usdtBlockedByOpen(f: UsdtForm, c: GatewayConfig): boolean {
  const open = c.health?.open ?? 0
  return !!c.address && open > 0 && normalizeTronAddress(f.address) !== c.address
}

const txidRe = /^[0-9a-f]{64}$/i

/** tronscanUrl is where staff can look a transaction up, or '' when the id
 * is not one. */
export function tronscanUrl(txid: string): string {
  return txidRe.test(txid) ? 'https://tronscan.org/#/transaction/' + txid.toLowerCase() : ''
}

/** Why a deposit matched nothing, as core names it (reason_code). */
export const unmatchedReasons = ['no_payment', 'already_paid', 'closed', 'ambiguous', 'dust'] as const

/** unmatchedReason is how a deposit's reason shows: the catalog key of a
 * reason the dashboard knows; otherwise core's own words or the code itself
 * (English, left to right); null when core said nothing. */
export function unmatchedReason(d: Pick<UnmatchedDeposit, 'reason' | 'reason_code'>): { key: string } | { raw: string } | null {
  const code = d.reason_code ?? ''
  if ((unmatchedReasons as readonly string[]).includes(code)) return { key: 'gateway.usdt.reason.' + code }
  const raw = d.reason || code
  return raw ? { raw } : null
}

/** depositKey tells the transfers apart: one transaction may carry more
 * than one. */
export function depositKey(d: Pick<UnmatchedDeposit, 'txid' | 'event_index'>): string {
  return d.txid.toLowerCase() + '#' + d.event_index
}

/** The note staff may leave on a handled deposit: payments counts it in
 * bytes (at most 500), so a Persian note gets this many characters (a
 * character takes at most 3 bytes in a text field). */
export const depositNoteMax = 160

/** resolveBody is what POST /gateways/deposits/resolve takes: the transfer
 * (its transaction and its place in it) and the note. */
export function resolveBody(d: Pick<UnmatchedDeposit, 'txid' | 'event_index'>, note: string) {
  return { txid: d.txid, event_index: d.event_index, note: note.trim() }
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
    fields: ['payments.usdt_trc20', 'payments.usdt_erc20', 'payments.usdt_rate_auto', 'payments.usdt_rate_margin', 'payments.usdt_rate'],
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

/** A report's failed payment checks: how many in each period, the newest of
 * the longer one, and how many more there are than it lists. */
export interface CheckErrors {
  week: number
  month: number
  items: GatewayCheckError[]
  more: number
}

/** checkErrors reads the failed payment checks (payments could not ask the
 * gateway, or it refused) of both periods; null when core does not report
 * them (an older core) or there were none, so the report only says something
 * when an outage left a trace. */
export function checkErrors(week: GatewayReport | null, month: GatewayReport | null): CheckErrors | null {
  const listed = (r: GatewayReport | null) => (Array.isArray(r?.check_errors) ? r.check_errors : null)
  const count = (r: GatewayReport | null) => {
    const n = r?.check_error_count
    return Math.max(typeof n === 'number' && n > 0 ? n : 0, listed(r)?.length ?? 0)
  }
  const list = listed(month) ?? listed(week)
  if (!list) return null
  const w = count(week)
  const m = Math.max(count(month), w)
  if (!m) return null
  const items = [...list].sort((a, b) => b.at - a.at)
  return { week: w, month: m, items, more: Math.max(0, m - items.length) }
}
