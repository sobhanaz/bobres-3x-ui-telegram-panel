import { bytes, date, isolate, num, type Lang } from './format'
import type { BackupFile, LogLine, ServiceHealth, SystemAlert, SystemBackups, SystemReply, UpdateCheck } from './types'

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

// The open problems core's watchdog found: each check that failed twice in a
// row, since when. The bot messages staff when one starts, once a day while
// it lasts and when it is fixed (notify.system_alerts).

export type AlertLevel = 'critical' | 'warning'

/** alertLevel reads a problem's severity: critical (customers are affected
 * now) or a warning (needs a look soon); anything else counts as a warning. */
export function alertLevel(a: Pick<SystemAlert, 'severity'>): AlertLevel {
  return (a.severity ?? '').trim().toLowerCase() === 'critical' ? 'critical' : 'warning'
}

export const alertSeverity: Record<AlertLevel, 'danger' | 'warn'> = {
  critical: 'danger',
  warning: 'warn',
}

export type AlertsState = 'unsupported' | 'unreadable' | 'ok'

/** alertsState reads core's answer: no alerts at all (an older core, which
 * has no watchdog), null (core could not read them), or the list. Neither
 * of the first two is ever shown as "all clear". */
export function alertsState(reply: Pick<SystemReply, 'alerts'> | null | undefined): AlertsState {
  if (!reply || reply.alerts === undefined) return 'unsupported'
  return Array.isArray(reply.alerts) ? 'ok' : 'unreadable'
}

/** openAlerts lists the problems critical first, then the oldest first (as
 * core does); null when there is no list (see alertsState). */
export function openAlerts(reply: Pick<SystemReply, 'alerts'> | null | undefined): SystemAlert[] | null {
  if (!reply || !Array.isArray(reply.alerts)) return null
  const rank = (a: SystemAlert) => (alertLevel(a) === 'critical' ? 0 : 1)
  const since = (a: SystemAlert) => unixOf(a.since) ?? Number.MAX_SAFE_INTEGER
  return [...reply.alerts].sort((a, b) => rank(a) - rank(b) || since(a) - since(b) || a.key.localeCompare(b.key))
}

/** worstLevel is the most severe level among the problems, or null when
 * there are none. */
export function worstLevel(list: readonly SystemAlert[]): AlertLevel | null {
  if (!list.length) return null
  return list.some((a) => alertLevel(a) === 'critical') ? 'critical' : 'warning'
}

/** alertTextKey is the page's text for a problem: the bot's text key
 * (alert.backup_stale) under system.alerts.kind, so the page and the
 * Telegram message name it the same way; '' without one. */
export function alertTextKey(a: Pick<SystemAlert, 'title_key'>): string {
  const name = (a.title_key ?? '').trim().replace(/^alert\./, '')
  return /^[a-z0-9_]+$/.test(name) ? 'system.alerts.kind.' + name : ''
}

/** ALERT_FALLBACK names a check the page has no text for, by its key (the
 * bot's alert.check). */
export const ALERT_FALLBACK = 'system.alerts.kind.check'

/** alertArgs are a problem's values for its text, as the bot writes them:
 * count, days, hours and minutes as numbers, free (bytes) as a size, date
 * (unix seconds) as a day, and anything else as a name (service names
 * through name), each kept whole inside a sentence. key is the check's key
 * when core does not give one. */
export function alertArgs(
  a: Pick<SystemAlert, 'key' | 'args'>,
  lang: Lang,
  name: (arg: string, value: string) => string = (_, v) => v,
): Record<string, string> {
  const out: Record<string, string> = { key: isolate(a.key) }
  for (const [k, raw] of Object.entries(a.args ?? {})) {
    const v = String(raw)
    const n = /^-?\d+$/.test(v.trim()) ? Number(v) : NaN
    if (Number.isNaN(n)) out[k] = isolate(name(k, v))
    else if (['count', 'days', 'hours', 'minutes'].includes(k)) out[k] = num(n, lang)
    else if (k === 'free') out[k] = bytes(n, lang)
    else if (k === 'date') out[k] = date(n, lang)
    else out[k] = isolate(name(k, v))
  }
  return out
}

/** fills: every {value} a text needs is in args, so it never shows a hole
 * (a newer core may name a problem with other values). */
export function fills(message: unknown, args: Record<string, string>): boolean {
  if (typeof message !== 'string') return true
  return [...message.matchAll(/\{\s*([A-Za-z0-9_]+)\s*\}/g)].every((m) => (args[m[1]] ?? '') !== '')
}

/** UPDATE_COMMAND installs the latest release on the server: bobres update
 * checks its signature, backs up the database first and rolls back on its
 * own if the store does not come back healthy. The page only shows it;
 * applying stays on the server. */
export const UPDATE_COMMAND = 'sudo bobres update'

export type UpdateState = 'newer' | 'current' | 'unknown'

/** updateState reads the bot's update check: a newer release, up to date,
 * or not known (the check failed, has not run yet, or core is too old to
 * say). A newer release read before a later check failed still shows. */
export function updateState(u: Pick<UpdateCheck, 'latest' | 'newer' | 'error'> | null | undefined): UpdateState {
  if (!u || !u.latest) return 'unknown'
  if (u.newer) return 'newer'
  return u.error ? 'unknown' : 'current'
}

export type UpdateReason =
  | 'unreachable'
  | 'bot_unreachable'
  | 'not_checked'
  | 'no_release'
  | 'bad_signature'
  | 'invalid'
  | 'unsupported'
  | 'other'

/** The codes the bot's check gives (internal/release), each with its own
 * explanation on the page. */
const UPDATE_ERRORS: UpdateReason[] = ['unreachable', 'not_checked', 'no_release', 'bad_signature', 'invalid']

/** updateReason says why the latest release is not known, or why the last
 * check failed; null when nothing went wrong. */
export function updateReason(u: Pick<UpdateCheck, 'latest' | 'error' | 'checked_at'> | null | undefined): UpdateReason | null {
  if (!u) return 'unsupported'
  const e = (u.error ?? '').trim().toLowerCase()
  if (e === 'bot unreachable' || e === 'bot_unreachable') return 'bot_unreachable'
  if (UPDATE_ERRORS.includes(e as UpdateReason)) return e as UpdateReason
  if (e) return 'other'
  return u.latest ? null : 'not_checked'
}

/** updateDetail is the bot's own words to show under the reason (English):
 * its detail for a release that failed the checks, or the whole error for
 * one the page does not know; '' otherwise. */
export function updateDetail(u: Pick<UpdateCheck, 'error' | 'detail'> | null | undefined, reason: UpdateReason | null): string {
  if (!u) return ''
  const detail = (u.detail ?? '').trim()
  switch (reason) {
    case 'bad_signature':
    case 'invalid':
      return detail
    case 'other':
      return [(u.error ?? '').trim(), detail].filter(Boolean).join(': ')
    default:
      return ''
  }
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

// The backups tab: the backups the backup service keeps (the last 7), made
// daily and on demand, each sent by the bot to the owner on Telegram.

/** The command that adds the backup service to an install. */
export const INSTALL_COMMAND = 'bobres install'

export type BackupsState = 'not_installed' | 'unreachable' | 'ok'

/** backupsState reads core's answer: this install has no backup service
 * (core says installed: false), core could not ask it, or its list. A list
 * without the flag is still a list: only false means not installed. */
export function backupsState(r: Pick<SystemBackups, 'installed' | 'error'>): BackupsState {
  if (r.installed === false) return 'not_installed'
  return r.error ? 'unreachable' : 'ok'
}

/** unixOf reads a time as unix seconds: core gives seconds, the service
 * itself an RFC 3339 time; null when there is none. */
export function unixOf(v: number | string | null | undefined): number | null {
  if (typeof v === 'number') return v > 0 ? v : null
  if (!v) return null
  const ms = Date.parse(v)
  return Number.isNaN(ms) ? null : Math.floor(ms / 1000)
}

export type BackupSent = 'sent' | 'not_sent' | 'failed'

/** backupSent says whether a backup reached the owner on Telegram, was kept
 * on the server only, or is a run that failed (no file to send). */
export function backupSent(b: Pick<BackupFile, 'sent_at' | 'error'>): BackupSent {
  if (b.error) return 'failed'
  return unixOf(b.sent_at) !== null ? 'sent' : 'not_sent'
}

/** The reasons the bot gives for a backup it did not send. */
const NOT_SENT = ['too_big', 'no_owner', 'busy', 'blocked', 'telegram_error', 'unreachable']

/** notSentReason reads why a backup was not sent: a reason the page explains
 * (with Telegram's own words, if any, as detail), or other with the whole
 * text; null when nothing was said. */
export function notSentReason(e: string | null | undefined): { reason: string; detail: string } | null {
  const s = (e ?? '').trim()
  if (!s) return null
  const i = s.indexOf(':')
  const head = (i < 0 ? s : s.slice(0, i)).trim().toLowerCase()
  const detail = i < 0 ? '' : s.slice(i + 1).trim()
  if (NOT_SENT.includes(head)) return { reason: head, detail }
  if (head === 'the bot is unreachable') return { reason: 'unreachable', detail }
  return { reason: 'other', detail: s }
}

/** How often the tab reads the list again while a backup is being made:
 * every ms for the first slowAfter reads, then five times slower (sending a
 * big file to Telegram can take minutes), at most tries times. */
export const backupPoll = { ms: 3000, slowAfter: 20, tries: 100 }
