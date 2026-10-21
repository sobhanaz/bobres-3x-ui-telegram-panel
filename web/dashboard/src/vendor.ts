// The Licenses page's types and small rules (internal/core/web/vendor.go). Core checks everything again.
import type { Money } from './format'

export interface VendorStatus {
  available: boolean
  on: boolean
  configured: boolean
  admin_url: string
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

/** addressOk: the license server's address as core takes it. */
export function addressOk(s: string): boolean {
  const v = s.trim()
  return /^https:\/\/[^/\s]+(\/\S*)?$/.test(v) && v.length <= 200
}

/** keyOk: an admin private key (licensesrv adminkey): 32 bytes of base64 (43 characters without padding). */
export function keyOk(s: string): boolean {
  return /^[A-Za-z0-9_-]{43}={0,1}$/.test(s.trim()) || /^[A-Za-z0-9+/]{43}=$/.test(s.trim())
}
