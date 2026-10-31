// The Hosted stores page's types and small rules (internal/core/web/hosted.go, internal/hosted). Core checks everything again.

export interface HostedFigures {
  version?: string
  services_up?: number
  services_total?: number
  database_up?: boolean
  orders_paid_24h?: number
  alerts_critical?: number
  alerts_warning?: number
  license_days_left?: number
  license_basis?: string
  backup_age_seconds?: number
  backup_last_ok?: boolean
  panels_up?: number
  panels_total?: number
  disk_used_ratio?: number
  pending_payments?: number
}

export interface HostedStore {
  id: string
  customer: string
  domain: string
  version: string
  license_id: string
  channel: 'stable' | 'beta'
  notes: string
  /** whether the store's metrics token is kept (it is never shown) */
  has_token: boolean
  created_at: number
  checked_at: number | null
  up: boolean | null
  last_version: string
  figures: HostedFigures
  last_error: string
}

/** The prefix of the code tools/hosted prints after an install. */
export const registrationPrefix = 'bobres-hosted:v1:'

/** looksLikeRegistration: a pasted text is a registration code (spaces and line breaks a terminal adds are ignored). */
export function looksLikeRegistration(s: string): boolean {
  return s.replace(/\s+/g, '').startsWith(registrationPrefix)
}

/** domainOk mirrors core's rule: lower case, at least two labels, no port, not an IP address. */
export function domainOk(s: string): boolean {
  const d = s.trim().toLowerCase()
  if (d.length > 253 || !/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$/.test(d)) return false
  return !/^\d+$/.test(d.split('.').pop() ?? '')
}

/** tokenOk mirrors core's rule for a metrics token. */
export function tokenOk(s: string): boolean {
  return /^[A-Za-z0-9._~+/=-]{24,128}$/.test(s.trim())
}

export type HostedState = 'up' | 'warn' | 'down' | 'unknown'

/** stateOf is a store at a glance: never read (or no token), down, up with a problem (a service down, a critical alert, a license near its
 * end), or up. */
export function stateOf(h: Pick<HostedStore, 'up' | 'figures' | 'checked_at'>): HostedState {
  if (h.up === null || h.checked_at === null) return 'unknown'
  if (!h.up) return 'down'
  const f = h.figures ?? {}
  const servicesDown = (f.services_total ?? 0) > 0 && (f.services_up ?? 0) < (f.services_total ?? 0)
  const licenseEnding = f.license_days_left !== undefined && f.license_days_left <= 7
  if (servicesDown || (f.alerts_critical ?? 0) > 0 || f.database_up === false || licenseEnding) return 'warn'
  return 'up'
}

export function stateSeverity(s: HostedState): 'success' | 'warn' | 'danger' | 'secondary' {
  return s === 'up' ? 'success' : s === 'warn' ? 'warn' : s === 'down' ? 'danger' : 'secondary'
}

/** behind says the store runs an older version than the one installed most recently on another store (a plain dotted comparison). */
export function older(a: string, b: string): boolean {
  const pa = a.replace(/^v/, '').split(/[.-]/).map((x) => parseInt(x, 10) || 0)
  const pb = b.replace(/^v/, '').split(/[.-]/).map((x) => parseInt(x, 10) || 0)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) < (pb[i] ?? 0)
  }
  return false
}

/** newest is the highest version the stores run, '' when none was read. */
export function newest(stores: Pick<HostedStore, 'last_version'>[]): string {
  return stores.reduce((best, s) => (s.last_version && (best === '' || older(best, s.last_version)) ? s.last_version : best), '')
}
