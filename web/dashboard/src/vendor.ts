// The Licenses page's types and small rules (internal/core/web/vendor.go). Core checks everything again.
import type { Money } from './format'

export interface VendorStatus {
  available: boolean
  on: boolean
  configured: boolean
  tiers: { name: string; monthly: Money; yearly: Money }[]
}

export interface SoldLicense {
  id: string
  license_id: string
  tier: 'standard' | 'pro'
  status: 'active' | 'revoked'
  ends_at: number | null
  created_at: number
  buyer: { id: string; telegram_id: number; username: string } | null
}

/** The months the owner may give for nothing (core takes 1 to 36). */
export const grantMonths = [1, 2, 3, 6, 12] as const

export const licenseStatuses = ['active', 'revoked'] as const

export function statusSeverity(s: string): 'success' | 'danger' | 'secondary' {
  return s === 'active' ? 'success' : s === 'revoked' ? 'danger' : 'secondary'
}

/** requestId makes a click one grant: core keeps the first answer for the same id. */
export function requestId(): string {
  const b = new Uint8Array(12)
  crypto.getRandomValues(b)
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

/** keyOk: an admin private key (licensesrv adminkey): 32 bytes of base64 (43 characters without padding). */
export function keyOk(s: string): boolean {
  return /^[A-Za-z0-9_-]{43}={0,1}$/.test(s.trim()) || /^[A-Za-z0-9+/]{43}=$/.test(s.trim())
}

// ---- Every license of the license server (internal/core/web/vendor_servers.go)

export interface ServerActivation {
  install_id: string
  domain: string
  counted: boolean
  activated_at: number
  last_seen_at: number
  version: string
  deactivated_at: number | null
}

export interface ServerAudit {
  at: number
  actor: string
  action: string
  ip: string
  details: Record<string, unknown> | null
}

export interface ServerLicense {
  id: string
  customer: string
  tier: 'trial' | 'standard' | 'pro'
  status: 'active' | 'revoked'
  ends_at: number | null
  created_at: number
  reason?: string
  active_domain?: string
  last_seen_at: number | null
  grace_days: number
  moves: number
  key?: string
  replayed?: boolean
  entitlements?: Record<string, unknown>
  activations?: ServerActivation[]
  audit?: ServerAudit[]
}

export const allTiers = ['trial', 'standard', 'pro'] as const
export type EndMode = 'months' | 'date' | 'none' | 'default'
/** The terms a license can be made for, in months (core takes 1 to 120). */
export const monthChoices = [1, 3, 6, 12, 24, 36] as const

/** The farthest end date core takes from now, in years (a longer one is a typo: use "no end"). */
export const maxEndYears = 10

/** dateOk: YYYY-MM-DD, a real day, after today and within maxEndYears. */
export function dateOk(s: string, now = new Date()): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s.trim())
  if (!m) return false
  const d = new Date(Date.UTC(+m[1], +m[2] - 1, +m[3]))
  if (d.getUTCFullYear() !== +m[1] || d.getUTCMonth() !== +m[2] - 1 || d.getUTCDate() !== +m[3]) return false
  const limit = new Date(now)
  limit.setUTCFullYear(limit.getUTCFullYear() + maxEndYears)
  return d.getTime() > now.getTime() && d.getTime() <= limit.getTime()
}

export interface NewLicenseForm {
  customer: string
  tier: (typeof allTiers)[number]
  mode: EndMode
  months: number
  date: string
}

export const blankLicense = (): NewLicenseForm => ({ customer: '', tier: 'standard', mode: 'months', months: 12, date: '' })

/** newLicenseProblem is the first thing wrong with the form, or '' (core checks everything again). */
export function newLicenseProblem(f: NewLicenseForm, now = new Date()): '' | 'customer' | 'date' | 'default' {
  const c = f.customer.trim()
  if (c === '' || c.length > 200 || /[\u0000-\u001f\u007f]/.test(c)) return 'customer'
  if (f.mode === 'date' && !dateOk(f.date, now)) return 'date'
  if (f.mode === 'default' && f.tier !== 'trial') return 'default' // only a trial has a length of its own
  return ''
}

/** newLicenseBody is what the form sends: exactly one way to say when it ends (a trial may say none: its own days). */
export function newLicenseBody(f: NewLicenseForm, requestId: string): Record<string, unknown> {
  const body: Record<string, unknown> = { customer: f.customer.trim(), tier: f.tier, request_id: requestId }
  if (f.mode === 'months') body.months = f.months
  else if (f.mode === 'date') body.ends_at = f.date.trim()
  else if (f.mode === 'none') body.no_end = true
  return body
}

export interface EditLicenseForm {
  customer: string
  tier: (typeof allTiers)[number]
  mode: 'keep' | 'date' | 'none'
  date: string
}

/** editBody sends only what changed, so a click that changes nothing is refused here and not at the license server. */
export function editBody(orig: Pick<ServerLicense, 'customer' | 'tier'>, f: EditLicenseForm, requestId: string): Record<string, unknown> | null {
  const body: Record<string, unknown> = { request_id: requestId }
  let changed = false
  if (f.customer.trim() !== orig.customer) {
    body.customer = f.customer.trim()
    changed = true
  }
  if (f.tier !== orig.tier) {
    body.tier = f.tier
    changed = true
  }
  if (f.mode === 'date') {
    body.ends_at = f.date.trim()
    changed = true
  } else if (f.mode === 'none') {
    body.no_end = true
    changed = true
  }
  return changed ? body : null
}

/** daysLeft: whole days until an end (negative once it has passed); null for no end. */
export function daysLeft(ends: number | null, now = Date.now()): number | null {
  return ends === null ? null : Math.floor((ends * 1000 - now) / 86_400_000)
}
