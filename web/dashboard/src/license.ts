import { latinDigits } from './format'
import type { LicenseInfo, LicenseLimit, LicenseToken } from './types'

// The License tab: the installed license's state, what it binds to, what it
// grants against what this install uses, and the token the owner pastes.
// A missing or invalid license never limits anything in Phase 4.

export type LicenseState = 'missing' | 'invalid' | 'active' | 'grace' | 'expired'

export const licenseSeverity: Record<LicenseState, 'success' | 'warn' | 'danger' | 'secondary'> = {
  missing: 'secondary',
  invalid: 'danger',
  active: 'success',
  grace: 'warn',
  expired: 'danger',
}

/** stateOf reads a state core sent; anything unknown counts as invalid. */
export function stateOf(s: string | undefined): LicenseState {
  return s === 'missing' || s === 'active' || s === 'grace' || s === 'expired' ? s : s ? 'invalid' : 'missing'
}

/** isValid: a token whose signature and binding checked out, expired or not
 * (an expired one grants none of its entitlements). */
export function isValid(s: string | undefined): boolean {
  const st = stateOf(s)
  return st === 'active' || st === 'grace' || st === 'expired'
}

/** poweredBy: "Powered by BOBRES" shows only under a valid license without
 * white label, never on an unlicensed install (core says so; read here when
 * it does not). */
export function poweredBy(l: { state: string; white_label: boolean; powered_by?: boolean } | null | undefined): boolean {
  if (!l) return false
  return typeof l.powered_by === 'boolean' ? l.powered_by : isValid(l.state) && !l.white_label
}

/** The reasons core gives for a token it does not accept (each has a
 * sentence): expired only when saving one is refused. */
export const REASONS = ['malformed', 'bad_signature', 'unknown_key', 'version', 'domain', 'install_id', 'unbound', 'invalid', 'not_yet_valid', 'expired']

/** reasonKey is the locale key explaining why a token is invalid. */
export function reasonKey(reason: string): string {
  return 'license.reason.' + (REASONS.includes(reason) ? reason : 'other')
}

/** normalizeDomain compares domains as core does: lower case, no port, no
 * trailing dot. */
export function normalizeDomain(d: string): string {
  const s = d.trim().toLowerCase()
  const port = s.match(/^([^:[\]]+):\d+$/) ?? s.match(/^\[([^\]]+)\]:\d+$/)
  return (port ? port[1] : s).replace(/\.$/, '')
}

export type Binding = 'match' | 'mismatch' | 'unbound'

/** bindingOf compares what the token is bound to with this install. */
export function bindingOf(bound: string, ours: string, domain = false): Binding {
  if (!bound) return 'unbound'
  const norm = domain ? normalizeDomain : (s: string) => s.trim().toLowerCase()
  return norm(bound) === norm(ours) ? 'match' : 'mismatch'
}

/** bindings: core's comparison, or the page's own when it gives none. */
export function bindings(info: Pick<LicenseInfo, 'license' | 'binding' | 'domain' | 'install_id'>): { domain: Binding; install_id: Binding } | null {
  const l = info.license
  if (!l) return null
  const known = (v: string | undefined, own: Binding): Binding => (v === 'match' || v === 'mismatch' || v === 'unbound' ? v : own)
  return {
    domain: known(info.binding?.domain, bindingOf(l.domain, info.domain, true)),
    install_id: known(info.binding?.install_id, bindingOf(l.install_id, info.install_id)),
  }
}

export const bindingSeverity: Record<Binding, 'success' | 'danger' | 'secondary'> = { match: 'success', mismatch: 'danger', unbound: 'secondary' }

const DAY = 86_400

/** graceEndsAt is when a license stops working after it expires. */
export function graceEndsAt(l: Pick<LicenseToken, 'expires_at' | 'grace_days' | 'grace_ends_at'>): number {
  return l.grace_ends_at ?? l.expires_at + Math.max(0, l.grace_days) * DAY
}

/** timeLeft is what the page counts down: days until it expires, days of
 * grace left, or days since the grace ended. */
export function timeLeft(l: Pick<LicenseToken, 'expires_at' | 'grace_days' | 'grace_ends_at'>, now = Date.now()): { kind: 'left' | 'grace' | 'over'; days: number } {
  const t = Math.floor(now / 1000)
  if (t < l.expires_at) return { kind: 'left', days: Math.floor((l.expires_at - t) / DAY) }
  const end = graceEndsAt(l)
  if (t < end) return { kind: 'grace', days: Math.floor((end - t) / DAY) }
  return { kind: 'over', days: Math.floor((t - end) / DAY) }
}

/** Limits warn from 90% of what the license allows. */
export const NEAR_LIMIT = 0.9

export type LimitState = 'unlimited' | 'none' | 'ok' | 'near' | 'full' | 'over' | 'unknown'

/** limitState compares what is used with a limit (-1 = unlimited, 0 = none):
 * full means no new one can be added, over that some were added before. */
export function limitState(used: number | null | undefined, limit: number): LimitState {
  if (limit < 0) return 'unlimited'
  if (used === null || used === undefined) return 'unknown'
  if (limit === 0) return used > 0 ? 'over' : 'none'
  if (used > limit) return 'over'
  if (used === limit) return 'full'
  return used >= limit * NEAR_LIMIT ? 'near' : 'ok'
}

export const limitSeverity: Record<LimitState, 'success' | 'warn' | 'danger' | 'secondary'> = {
  unlimited: 'secondary',
  none: 'secondary',
  ok: 'success',
  near: 'warn',
  full: 'warn',
  over: 'danger',
  unknown: 'secondary',
}

/** Payment methods every license allows. */
export const ALWAYS_ALLOWED = ['wallet']

/** outsideLicense are the turned-on payment methods the license does not
 * name (case does not matter, as in core). */
export function outsideLicense(allowed: readonly string[], enabled: readonly string[]): string[] {
  const ok = new Set([...allowed, ...ALWAYS_ALLOWED].map((g) => g.toLowerCase()))
  return enabled.filter((g) => !ok.has(g.toLowerCase()))
}

export type EntitlementRow =
  | { key: 'servers' | 'active_services' | 'staff_seats'; kind: 'limit'; used: number | null; limit: number; state: LimitState }
  | { key: 'resellers' | 'white_label' | 'crypto_watcher'; kind: 'feature'; on: boolean }
  | { key: 'gateways'; kind: 'gateways'; allowed: string[]; outside: string[] }
  | { key: 'updates_until'; kind: 'date'; value: string }

/** entitlementRows lists what a license grants, each limit with its use. */
export function entitlementRows(info: Pick<LicenseInfo, 'entitlements'>): EntitlementRow[] {
  const e = info.entitlements
  if (!e) return []
  const limit = (key: 'servers' | 'active_services' | 'staff_seats', l: LicenseLimit | undefined): EntitlementRow => {
    const max = l?.limit ?? 0
    const used = l?.used ?? null
    return { key, kind: 'limit', used, limit: max, state: limitState(used, max) }
  }
  const allowed = e.gateways?.licensed ?? []
  return [
    limit('servers', e.max_servers),
    limit('active_services', e.max_active_services),
    limit('staff_seats', e.dashboard_staff_seats),
    { key: 'gateways', kind: 'gateways', allowed, outside: outsideLicense(allowed, e.gateways?.enabled ?? []) },
    { key: 'resellers', kind: 'feature', on: e.resellers },
    { key: 'white_label', kind: 'feature', on: e.white_label },
    { key: 'crypto_watcher', kind: 'feature', on: e.crypto_watcher },
    { key: 'updates_until', kind: 'date', value: e.updates_until ?? '' },
  ]
}

/** warnings counts the limits at 90% or more, and methods outside it. */
export function warnings(rows: readonly EntitlementRow[]): number {
  return rows.filter((r) => (r.kind === 'limit' && ['near', 'full', 'over'].includes(r.state)) || (r.kind === 'gateways' && r.outside.length > 0)).length
}

/** The longest token the page sends (core refuses longer). */
export const TOKEN_MAX = 16 * 1024
/** The largest token file the page reads. */
export const TOKEN_FILE_MAX = 64 * 1024

/** tokenOf is a pasted token without the spaces and line breaks a mail or
 * chat may add (Persian digits never occur in one, but are read anyway). */
export function tokenOf(raw: string): string {
  return latinDigits(raw).replace(/\s+/g, '')
}

/** tokenProblem: nothing pasted, too long, or not two base64url parts. */
export function tokenProblem(raw: string): '' | 'empty' | 'long' | 'format' {
  const tok = tokenOf(raw)
  if (!tok) return 'empty'
  if (tok.length > TOKEN_MAX) return 'long'
  return /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(tok) ? '' : 'format'
}

/** canSave: core installs only a token that verifies here and has not
 * expired. */
export function canSave(state: string | undefined): boolean {
  const st = stateOf(state)
  return st === 'active' || st === 'grace'
}

// Online activation: the owner types the license key the seller gave
// (BOBRES-XXXX-…); core sends it with this install's id and domain to the
// license server through the bot (core has no way out), checks the license
// it answers and installs it. The key is a secret: sent once, never kept or
// shown again.

/** The longest key the page sends (core takes 200; a key is about 46). */
export const KEY_MAX = 200

/** keyOf is a typed or pasted key without the spaces and line breaks a mail
 * or chat may add, Persian digits read as digits, and the long dashes some
 * apps swap in read as plain ones. The license server reads it in upper case. */
export function keyOf(raw: string): string {
  return latinDigits(raw).replace(/\s+/g, '').replace(/[\u2010-\u2015\u2212]/g, '-')
}

/** keyProblem: nothing typed, a license token (it goes in the token box),
 * too long, or not BOBRES- and groups of letters and digits (the license
 * server checks the exact form). */
export function keyProblem(raw: string): '' | 'empty' | 'token' | 'long' | 'format' {
  const key = keyOf(raw)
  if (!key) return 'empty'
  if (tokenProblem(raw) === '') return 'token'
  if (key.length > KEY_MAX) return 'long'
  return /^BOBRES(-[A-Z0-9]+)+$/i.test(key) ? '' : 'format'
}

/** The license server's reasons for refusing a key, each with a sentence;
 * for some its own words follow (they carry a date: when the license ended,
 * or when it may move again). */
export const KEY_REASONS = ['invalid_key', 'revoked', 'expired', 'reactivation_limit']
const KEY_DATED = ['expired', 'reactivation_limit']

/** keyRefusal reads core's 400 about an activation: the license server
 * refused the key (error refused, its code), core did not take the typed
 * key (no code), or the license the server answered does not verify here
 * (field token, explained as for a pasted token). reason is the locale key,
 * detail core's own words when the sentence needs them; null for any other
 * failure. */
export function keyRefusal(e: { status: number; data: Record<string, unknown>; message: string }): { reason: string; detail: string } | null {
  if (e.status !== 400) return null
  const { field } = e.data
  const code = typeof e.data.code === 'string' ? e.data.code : ''
  if (field === 'token' && code) return { reason: reasonKey(code), detail: '' }
  if (field !== 'key') return null
  if (e.data.error === 'refused' && KEY_REASONS.includes(code)) {
    return { reason: 'license.activate.reason.' + code, detail: KEY_DATED.includes(code) ? e.message : '' }
  }
  return { reason: 'license.activate.reason.other', detail: e.message }
}

/** How the token in use came (core.license.source), each with its words:
 * added by hand (pasted here or imported with bobres license), activated
 * with a key, or renewed by the license server since. */
export const SOURCES = ['manual', 'activation', 'refresh']

/** sourceKey is the locale key of how the token in use came; '' when core
 * does not say (an older core) or says something the page does not know. */
export function sourceKey(source: string | null | undefined): string {
  return source && SOURCES.includes(source) ? 'license.source.' + source : ''
}

/** licenseEnds is when the license itself ends, for a short token the
 * license server renews (the token's expires_at is its own end); null when
 * the token does not say or ends with the license. */
export function licenseEnds(l: Pick<LicenseToken, 'expires_at' | 'license_ends_at'>): number | null {
  return l.license_ends_at && l.license_ends_at > l.expires_at ? l.license_ends_at : null
}

/** renews: core renews the token from the license server every day (a
 * license activated with a key); an older core that does not say how it
 * came is read from the token ending before the license. */
export function renews(info: Pick<LicenseInfo, 'license' | 'source'>): boolean {
  if (!info.license) return false
  if (info.source === 'activation' || info.source === 'refresh') return true
  return !info.source && licenseEnds(info.license) !== null
}
