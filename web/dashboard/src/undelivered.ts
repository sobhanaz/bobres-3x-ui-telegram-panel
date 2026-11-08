// Undelivered orders (docs/04-dashboard.md, "Undelivered orders"): paid orders whose service could not be made yet
// (status provision_failed), which core tries again on its own. Helpers for the Payments page's Undelivered tab and
// the order page: how a server reads in "Deliver on another server", and when the next try is.
import { api } from './api'
import { numberLocale, type Lang } from './format'
import type { OrderItem, UndeliveredPage, UndeliveredServer } from './types'

/** the longest note to the buyer core takes with a refund */
export const NOTE_MAX = 500

/** ok: answering; down: its last check failed; unknown: not checked yet; full: at its client limit, it takes no new
 * client whatever its health. */
export type MoveState = 'ok' | 'down' | 'unknown' | 'full'

export function moveState(s: Pick<UndeliveredServer, 'healthy' | 'checked' | 'full'>): MoveState {
  if (s.full) return 'full'
  if (!s.checked) return 'unknown'
  return s.healthy ? 'ok' : 'down'
}

export const moveSeverity: Record<MoveState, 'success' | 'danger' | 'secondary' | 'warn'> = {
  ok: 'success',
  down: 'danger',
  unknown: 'secondary',
  full: 'warn',
}

/** moveChoices lists the servers for the select: the working ones first, then not checked, not answering and full,
 * each group by name. */
export function moveChoices(servers: UndeliveredServer[]): UndeliveredServer[] {
  const rank: Record<MoveState, number> = { ok: 0, unknown: 1, down: 2, full: 3 }
  return [...servers].sort((a, b) => rank[moveState(a)] - rank[moveState(b)] || a.name.localeCompare(b.name))
}

/** canMove: a new service waiting for delivery may go to another server; a renewal or a top-up stays on its
 * service's server. */
export function canMove(o: Pick<OrderItem, 'status' | 'subscription_id'>): boolean {
  return o.status === 'provision_failed' && !o.subscription_id
}

/** serverName names the server an order was moved to, from the list; '' when it is not in it (switched off or
 * deleted since). */
export function serverName(id: string | null | undefined, servers: UndeliveredServer[]): string {
  if (!id) return ''
  return servers.find((s) => s.id === id)?.name ?? ''
}

/** inTime says how long until a time that is still ahead ("in 12 minutes", "۵ دقیقه بعد"), or '' when it has come
 * (the try is due now). */
export function inTime(unix: number, nowMs: number, lang: Lang): string {
  const seconds = Math.floor(unix - nowMs / 1000)
  if (seconds <= 0) return ''
  const rtf = new Intl.RelativeTimeFormat(numberLocale(lang), { numeric: 'always' })
  if (seconds < 90) return rtf.format(Math.max(1, seconds), 'second')
  const minutes = Math.round(seconds / 60)
  if (minutes < 90) return rtf.format(minutes, 'minute')
  const hours = Math.round(minutes / 60)
  if (hours < 48) return rtf.format(hours, 'hour')
  return rtf.format(Math.round(hours / 24), 'day')
}

/** moveServers reads the servers an order may be moved to, for the order page (the servers list itself needs
 * servers.write; this one is payments.read, like the order). */
export async function moveServers(): Promise<{ servers: UndeliveredServer[]; servers_error: string }> {
  const res = await api<UndeliveredPage>('GET', '/orders/undelivered?page=1&size=1')
  return { servers: res.servers ?? [], servers_error: res.servers_error ?? '' }
}
