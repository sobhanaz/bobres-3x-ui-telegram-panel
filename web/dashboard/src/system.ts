import type { LogLine, ServiceHealth, SystemReply, UpdateCheck } from './types'

// The System page's health tab: what each service's /healthz and /readyz
// say, and whether every service runs the same version.

/** The services core checks, in the order the page lists them. */
export const SERVICE_ORDER = ['core', 'bot', 'payments', 'provisioner', 'backup']

export type ServiceState = 'ok' | 'not_ready' | 'down' | 'not_installed' | 'not_configured'
const STATES: ServiceState[] = ['ok', 'not_ready', 'down', 'not_installed', 'not_configured']

/** serviceState reads the status core gives: fine, answering but a
 * readiness check failed, not answering, not part of this install, or
 * without an address in core's settings. Anything else counts as down. */
export function serviceState(s: Pick<ServiceHealth, 'status'>): ServiceState {
  return STATES.includes(s.status as ServiceState) ? (s.status as ServiceState) : 'down'
}

export const serviceSeverity: Record<ServiceState, 'success' | 'warn' | 'danger' | 'secondary'> = {
  ok: 'success',
  not_ready: 'warn',
  down: 'danger',
  not_installed: 'secondary',
  not_configured: 'secondary',
}

/** absent: a service this install does not run or core cannot reach by
 * design, so it has no version or checks to show. */
export function absent(s: Pick<ServiceHealth, 'status'>): boolean {
  const st = serviceState(s)
  return st === 'not_installed' || st === 'not_configured'
}

/** sortedServices lists the known services first, in their usual order. */
export function sortedServices(list: readonly ServiceHealth[]): ServiceHealth[] {
  const rank = (n: string) => {
    const i = SERVICE_ORDER.indexOf(n)
    return i < 0 ? SERVICE_ORDER.length : i
  }
  return [...list].sort((a, b) => rank(a.name) - rank(b.name) || a.name.localeCompare(b.name))
}

/** checkList is a service's readiness checks by name, each passed or not. */
export function checkList(s: Pick<ServiceHealth, 'checks'>): { name: string; ok: boolean }[] {
  return Object.entries(s.checks ?? {})
    .map(([name, v]) => ({ name, ok: v === 'ok' }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

/** mismatched are the services core found running another version than
 * its own. */
export function mismatched(reply: Pick<SystemReply, 'services'>): ServiceHealth[] {
  return sortedServices(reply.services).filter((s) => s.name !== 'core' && !absent(s) && s.version_mismatch === true)
}

/** The command that installs an update on the server; the page only shows
 * it, applying stays on the command line. */
export const UPDATE_COMMAND = 'bobres update'

export type UpdateState = 'newer' | 'current' | 'unknown'

/** updateState reads the bot's update check: a newer release, up to date,
 * or not known (the check failed, has not run yet, or core is too old to
 * say). A newer release read before a later check failed still shows. */
export function updateState(u: Pick<UpdateCheck, 'latest' | 'newer' | 'error'> | null | undefined): UpdateState {
  if (!u || !u.latest) return 'unknown'
  if (u.newer) return 'newer'
  return u.error ? 'unknown' : 'current'
}

export type UpdateReason = 'unreachable' | 'bot_unreachable' | 'not_checked' | 'unsupported' | 'other'

/** updateReason says why the latest release is not known, or why the last
 * check failed; null when nothing went wrong. */
export function updateReason(u: Pick<UpdateCheck, 'latest' | 'error' | 'checked_at'> | null | undefined): UpdateReason | null {
  if (!u) return 'unsupported'
  const e = (u.error ?? '').trim().toLowerCase()
  if (e === 'unreachable') return 'unreachable'
  if (e === 'bot unreachable' || e === 'bot_unreachable') return 'bot_unreachable'
  if (e) return 'other'
  return u.latest ? null : 'not_checked'
}

// The logs tab (owner): the warnings and errors each service kept since it
// last started.

/** The services whose logs core can read, in the order the picker lists
 * them. */
export const LOG_SERVICES = ['core', 'bot', 'payments', 'provisioner']

export type LogLevel = 'warn' | 'error'

/** logLevel reads a line's level; anything but an error counts as a
 * warning (only warnings and errors are kept). */
export function logLevel(l: Pick<LogLine, 'level'>): LogLevel {
  return l.level.trim().toUpperCase().startsWith('ERR') ? 'error' : 'warn'
}

export const logSeverity: Record<LogLevel, 'warn' | 'danger'> = { warn: 'warn', error: 'danger' }

/** newestFirst lists the lines newest first (services keep them oldest
 * first); lines of the same second keep their reversed order. */
export function newestFirst(lines: readonly LogLine[] | null | undefined): LogLine[] {
  return [...(lines ?? [])].reverse().sort((a, b) => b.time - a.time)
}

/** logAttrs is a line's other fields to show, or '' when it has none. */
export function logAttrs(l: Pick<LogLine, 'attrs'>): string {
  const a = (l.attrs ?? '').trim()
  return a === '{}' || a === 'null' ? '' : a
}

/** logsReason says why a service's logs could not be read: core could not
 * ask it, or the service's own words; null when they were read. */
export function logsReason(error: string | null | undefined): 'unreachable' | 'not_configured' | 'other' | null {
  const e = (error ?? '').trim().toLowerCase()
  if (!e) return null
  if (e === 'unreachable') return 'unreachable'
  return e === 'not configured' ? 'not_configured' : 'other'
}
