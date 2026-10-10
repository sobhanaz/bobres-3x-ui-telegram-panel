// Pure helpers for the Staff page, My account's sessions and the audit log:
// what a row may offer, a short device name from a browser's user agent, and
// where an audit entry's item lives in the dashboard.
import type { AuditItem, StaffItem, UserItem } from './types'

export type StaffRole = StaffItem['role']

/** A device in words: browser and operating system ('' when unknown). */
export interface Device {
  browser: string
  os: string
}

// Order matters: browsers built on Chrome also say "Chrome" (and Safari),
// so the specific names come first.
const browsers: [RegExp, string][] = [
  [/\bEdg(e|A|iOS)?\//, 'Edge'],
  [/\b(OPR|OPiOS|OPT)\/|\bOpera\b/, 'Opera'],
  [/\bSamsungBrowser\//, 'Samsung Internet'],
  [/\bYaBrowser\//, 'Yandex'],
  [/\bVivaldi\//, 'Vivaldi'],
  [/\b(Firefox|FxiOS)\//, 'Firefox'],
  [/\bCriOS\//, 'Chrome'],
  [/\bChromium\//, 'Chromium'],
  [/\bChrome\//, 'Chrome'],
  [/\bVersion\/[\d.]+.*\bSafari\//, 'Safari'],
]

// iPhones say "like Mac OS X" and Android says "Linux": check them first.
const systems: [RegExp, string][] = [
  [/\bWindows Phone\b/, 'Windows Phone'],
  [/\biPad\b/, 'iPadOS'],
  [/\b(iPhone|iPod)\b/, 'iOS'],
  [/\bAndroid\b/, 'Android'],
  [/\bCrOS\b/, 'ChromeOS'],
  [/\bWindows\b/, 'Windows'],
  [/\b(Macintosh|Mac OS X)\b/, 'macOS'],
  [/\bLinux\b/, 'Linux'],
]

/** deviceOf reads a browser's user agent into a short device name. */
export function deviceOf(ua: string): Device {
  const find = (list: [RegExp, string][]) => list.find(([re]) => re.test(ua))?.[1] ?? ''
  return { browser: find(browsers), os: find(systems) }
}

/** deviceKind picks an icon for the system: phones, tablets, the rest. */
export function deviceKind(os: string): 'mobile' | 'tablet' | 'desktop' {
  if (os === 'iOS' || os === 'Android' || os === 'Windows Phone') return 'mobile'
  if (os === 'iPadOS') return 'tablet'
  return 'desktop'
}

/** isLocked: too many failed password logins, and the lock has not run out. */
export function isLocked(p: StaffItem['password'], now: number): boolean {
  return p.locked_until !== null && p.locked_until > now
}

/** loginState is how a staff member can log in, for the table. */
export function loginState(s: StaffItem, now: number): 'off' | 'pending' | 'on' | 'locked' {
  if (s.password.state !== 'off' && isLocked(s.password, now)) return 'locked'
  return s.password.state
}

export type StaffAction = 'role' | 'sessions' | 'reset' | 'unlock' | 'remove'

/**
 * staffActions lists what the viewer may do to a staff member. Nothing on
 * themselves (that is My account) or on the store owner set on the server;
 * giving or taking the owner role needs canGrantOwner (the server checks
 * every rule again).
 */
export function staffActions(s: StaffItem, canGrantOwner: boolean, now: number): StaffAction[] {
  if (s.me || s.configured_owner) return []
  const out: StaffAction[] = []
  const ownerLocked = s.role === 'owner' && !canGrantOwner
  if (!ownerLocked) out.push('role')
  out.push('sessions')
  if (s.password.state !== 'off') out.push('reset')
  if (isLocked(s.password, now)) out.push('unlock')
  if (!ownerLocked) out.push('remove')
  return out
}

/** roleOptions are the roles the viewer can give. */
export function roleOptions(canGrantOwner: boolean): StaffRole[] {
  return canGrantOwner ? ['support', 'admin', 'owner'] : ['support', 'admin']
}

/** addable: only active customers (not banned, not staff) can join the staff. */
export function addable(u: Pick<UserItem, 'role' | 'status'>): boolean {
  return u.role === 'user' && u.status === 'active'
}

const uuidRe = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** entityIdLabel shortens an audit entry's item id: the last 6 hex digits of
 * a UUID, a setting key or discount code as it is (cut when very long). */
export function entityIdLabel(id: string): string {
  if (uuidRe.test(id)) return id.replaceAll('-', '').slice(-6)
  return id.length > 32 ? id.slice(0, 31) + '…' : id
}

export interface AuditLink {
  name: string
  params?: Record<string, string>
  query?: Record<string, string>
}

function orderOf(after: unknown): string {
  if (after && typeof after === 'object' && 'order_id' in after) {
    const id = (after as { order_id: unknown }).order_id
    if (typeof id === 'string' && uuidRe.test(id)) return id
  }
  return ''
}

/** auditLink is where an audit entry's item lives in the dashboard, if anywhere. */
export function auditLink(e: Pick<AuditItem, 'entity' | 'entity_id' | 'after' | 'actor'>): AuditLink | null {
  const id = e.entity_id
  switch (e.entity) {
    case 'user':
    case 'staff':
      return uuidRe.test(id) ? { name: 'user', params: { id } } : null
    case 'subscription':
      return uuidRe.test(id) ? { name: 'service', params: { id } } : null
    case 'order':
      return uuidRe.test(id) ? { name: 'order', params: { id } } : null
    case 'payment_intent': {
      const order = orderOf(e.after)
      return order ? { name: 'order', params: { id: order } } : null
    }
    case 'plan':
      return { name: 'plans' }
    case 'discount':
      return { name: 'discounts' }
    case 'settings':
      // Branding and the referral reward are settings edited on other pages.
      if (id.startsWith('branding.')) return { name: 'branding' }
      if (id.startsWith('referral.')) return { name: 'discounts' }
      return { name: 'settings' }
    case 'text':
      return { name: 'branding', query: { tab: 'texts' } }
    case 'branding':
      return { name: 'branding' }
    case 'dashboard':
      return e.actor ? { name: 'audit', query: { actor: e.actor.id } } : null
  }
  return null
}
