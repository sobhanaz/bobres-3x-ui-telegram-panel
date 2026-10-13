import { ApiError } from './api'
import { errorText } from './errors'
import { isolate, latinDigits } from './format'
import { UPDATE_COMMAND } from './system'
import type { TlsAddress, TlsAgent, TlsView } from './types'

// The System page's HTTPS tab: how the store's addresses get their
// certificates (automatic for the domain, Let's Encrypt for the server's IP
// address, or the owner's own), what each address serves now, and the form
// that changes it. Core checks everything again; the checks here only save a
// round trip and say what is wrong next to the field.

export type TlsMode = 'auto' | 'ip' | 'manual'
export const TLS_MODES: TlsMode[] = ['auto', 'ip', 'manual']

/** modeOf reads the mode core gives; anything else counts as automatic. */
export function modeOf(s: string | null | undefined): TlsMode {
  return TLS_MODES.includes(s as TlsMode) ? (s as TlsMode) : 'auto'
}

/** RECOVERY_COMMAND puts the store back on the automatic certificate from
 * the server, when a wrong choice made the dashboard unreachable. */
export const RECOVERY_COMMAND = 'sudo bobres ssl auto'
export const STATUS_COMMAND = 'sudo bobres status'
export const START_COMMAND = 'sudo bobres start'
export const CADDY_LOGS_COMMAND = 'sudo bobres logs caddy'

/** The commands the texts name, each kept whole inside a sentence. */
const commands = {
  update: isolate(UPDATE_COMMAND),
  status: isolate(STATUS_COMMAND),
  start: isolate(START_COMMAND),
  logs: isolate(CADDY_LOGS_COMMAND),
}

/** PEM_MAX is how large the certificate chain and the key may each be (the
 * API refuses more). */
export const PEM_MAX = 64 * 1024

/** How the tab reads the state again after a change, while the certificate
 * is issued: every ms, for at most timeoutMs. */
export const httpsPoll = { ms: 3000, timeoutMs: 90_000 }

// Codes the API answers with, each with its own text under system.https.

/** What was sent is refused (400 invalid: tlsconf's problem codes). */
export const INVALID_CODES = [
  'bad_mode', 'no_domain', 'bad_domain', 'bad_zarinpal_host', 'ip_required', 'bad_ip', 'ip_not_public', 'ip_domain_mismatch', 'cert_required',
  'cert_missing', 'cert_not_pem', 'cert_has_key', 'cert_parse', 'cert_too_large', 'chain_order', 'chain_too_long',
  'key_missing', 'key_not_pem', 'key_encrypted', 'key_parse', 'key_too_large', 'key_count', 'key_mismatch',
  'cert_expired', 'cert_not_yet_valid', 'cert_name_mismatch',
]
/** The change was understood but not made (409 and 503). busy: another
 * change is being applied, here or in the CLI; try again in a moment. */
export const REFUSAL_CODES = ['ip_unconfirmed', 'no_master_key', 'agent_unavailable', 'agent_timeout', 'agent_failed', 'caddy_refused', 'caddy_unreachable', 'busy']
/** Other failures of this tab's calls. */
export const OTHER_CODES = ['detect_failed', 'rate_limited', 'not_configured']
/** Accepted, with a note (PUT's warnings). */
export const WARNING_CODES = ['expires_soon', 'self_signed', 'chain_incomplete', 'untrusted', 'covers_zarinpal']
/** What the tls-agent reports for its last request (status.agent.code). */
export const AGENT_CODES = ['invalid', 'write_failed', 'caddy_refused', 'caddy_unreachable', 'agent_error']
/** Why the saved setting is not in use, besides tlsconf's codes (problem.code). */
export const PROBLEM_CODES = ['no_master_key', 'key_unreadable', 'zarinpal_host_left_out', 'agent_unavailable']

const ERROR_CODES = new Set([...INVALID_CODES, ...REFUSAL_CODES, ...OTHER_CODES])

/** The codes whose English message says more than the text does (a name, a
 * date, Caddy's own words): shown under the text. */
const DETAIL_CODES = new Set([
  'caddy_refused', 'agent_failed', 'agent_unavailable', 'cert_name_mismatch', 'cert_expired', 'cert_not_yet_valid', 'cert_parse', 'cert_not_pem',
  'key_not_pem', 'ip_domain_mismatch', 'ip_not_public', 'bad_ip', 'bad_domain', 'bad_zarinpal_host',
])

/** codeKey is the text for an error code, or '' for a code this page does not know. */
export function codeKey(code: string): string {
  return ERROR_CODES.has(code) ? 'system.https.code.' + code : ''
}

/** fieldOf says which field of the form a code is about (the API names it
 * too; this is for codes found here). */
export function fieldOf(code: string): 'mode' | 'ip' | 'cert' | 'key' | '' {
  if (code === 'bad_mode') return 'mode'
  if (code.startsWith('ip_') || code === 'bad_ip') return 'ip'
  if (code.startsWith('key_')) return 'key'
  if (code.startsWith('cert_') || code.startsWith('chain_')) return 'cert'
  return ''
}

type Tr = (key: string, params?: Record<string, unknown>) => string
type Has = (key: string) => boolean

export interface TlsFailureText {
  text: string
  /** English words worth showing under the text (Caddy's, a name, a date) */
  detail: string
  /** the form field it is about, '' for none */
  field: 'mode' | 'ip' | 'cert' | 'key' | ''
  /** 409 ip_unconfirmed: the address the bot detected, '' when it could not */
  detectedIp: string
  /** the code this page translated, '' when it fell back to the general text */
  code: string
}

/** failureText is what a failed call shows: the text for its code in the
 * staff member's language and, for some, the server's own words. ip is the
 * address that was asked for. A failure with no code this page knows (the
 * network, a session that ended) gets the general text. */
export function failureText(e: unknown, t: Tr, te: Has, ip = ''): TlsFailureText {
  const none: TlsFailureText = { text: '', detail: '', field: '', detectedIp: '', code: '' }
  if (!(e instanceof ApiError)) return { ...none, text: errorText(e, t).text }
  let code = typeof e.data.code === 'string' ? e.data.code : ''
  if (!code && e.status === 429) code = 'rate_limited'
  if (!code && e.code === 'not_configured') code = 'not_configured'
  const detectedIp = typeof e.data.detected_ip === 'string' ? e.data.detected_ip : ''
  // two texts for an address to confirm: the bot saw another one, or none
  const key = code === 'ip_unconfirmed' && detectedIp ? 'system.https.code.ip_unconfirmed_other' : codeKey(code)
  if (!key || !te(key)) {
    const err = errorText(e, t)
    return { ...none, text: err.text, detail: err.detail }
  }
  const field = typeof e.data.field === 'string' && ['mode', 'ip', 'cert', 'key'].includes(e.data.field) ? (e.data.field as TlsFailureText['field']) : fieldOf(code)
  return {
    text: t(key, { ...commands, ip: isolate(ip), detected: isolate(detectedIp) }),
    detail: DETAIL_CODES.has(code) ? e.message : '',
    field,
    detectedIp,
    code,
  }
}

/** warningText is the text for one of PUT's warnings; its English words when
 * a newer core sends one this page has no text for. */
export function warningText(w: { code: string; message: string }, t: Tr, te: Has): string {
  const key = 'system.https.warning.' + w.code
  return WARNING_CODES.includes(w.code) && te(key) ? t(key) : w.message
}

export interface ProblemText {
  /** the text in the staff member's language */
  text: string
  /** core's English words, shown under it ('' when the text says it all) */
  detail: string
}

/** problemText says why the saved setting is not in use. A few problems have
 * a text of their own; a certificate or an address that can no longer be
 * used says so with the reason; anything else shows core's words. */
export function problemText(p: { code: string; message: string }, t: Tr, te: Has): ProblemText {
  if (PROBLEM_CODES.includes(p.code) && te('system.https.problem.' + p.code)) return { text: t('system.https.problem.' + p.code), detail: p.code === 'agent_unavailable' ? p.message : '' }
  const reason = codeKey(p.code)
  const field = fieldOf(p.code)
  const why = () => t(reason, commands)
  if (reason && te(reason) && (field === 'cert' || field === 'key')) return { text: t('system.https.problem.manual_unused', { reason: why() }), detail: p.message }
  if (reason && te(reason) && field === 'ip') return { text: t('system.https.problem.ip_unused', { reason: why() }), detail: p.message }
  return { text: t('system.https.problem.other'), detail: p.message }
}

// The form.

export interface HttpsForm {
  mode: TlsMode
  ip: string
  /** the certificate chain, PEM */
  cert: string
  /** the private key, PEM: never shown again and cleared after it is sent */
  key: string
  /** the owner confirmed the address is this server's */
  confirmIp: boolean
}

export interface FormProblem {
  field: 'mode' | 'ip' | 'cert' | 'key'
  code: string
}

/** isIPLiteral: an IPv4 or IPv6 address, not a host name. */
export function isIPLiteral(s: string): boolean {
  const v = latinDigits(s).trim()
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(v) || (v.includes(':') && v6(v.toLowerCase()) !== null)
}

function v6(s: string): string | null {
  if (!/^[0-9a-f:.]+$/.test(s)) return null
  try {
    return new URL(`http://[${s}]/`).hostname.slice(1, -1)
  } catch {
    return null
  }
}

/** canonicalIP writes an address the way core compares them: digits in
 * 0-9, IPv6 in lower case and compressed. Text that is not an address
 * comes back trimmed and lower-cased. */
export function canonicalIP(raw: string): string {
  const s = latinDigits(raw).trim().toLowerCase()
  return (s.includes(':') && v6(s)) || s
}

/** hostPort writes an address as a URL does: IPv6 in brackets. */
export function hostPort(h: string): string {
  return h.includes(':') ? `[${h}]` : h
}

/** ipProblem is what is plainly wrong with a typed server address (core's
 * code for it), or null. It knows the common private ranges; core knows them
 * all. */
export function ipProblem(raw: string): 'ip_required' | 'bad_ip' | 'ip_not_public' | null {
  const s = canonicalIP(raw)
  if (!s) return 'ip_required'
  if (s.includes(':')) {
    const c = v6(s)
    if (c === null) return 'bad_ip'
    const nonPublic = c === '::' || c === '::1' || /^fe[89ab]/.test(c) || /^f[cd]/.test(c) || /^ff/.test(c) || c.startsWith('2001:db8:')
    return nonPublic ? 'ip_not_public' : null
  }
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s)
  if (!m || m.slice(1).some((p) => (p.length > 1 && p.startsWith('0')) || Number(p) > 255)) return 'bad_ip'
  const [a, b, c] = [Number(m[1]), Number(m[2]), Number(m[3])]
  const nonPublic =
    a === 0 || a === 10 || a === 127 || a >= 224 ||
    (a === 100 && b >= 64 && b <= 127) || // carrier-grade NAT
    (a === 169 && b === 254) ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) ||
    (a === 192 && b === 0 && (c === 0 || c === 2)) ||
    (a === 198 && (b === 18 || b === 19)) ||
    (a === 198 && b === 51 && c === 100) ||
    (a === 203 && b === 0 && c === 113)
  return nonPublic ? 'ip_not_public' : null
}

const sizeOf = (s: string) => new Blob([s]).size
const keyBegin = /-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----/

/** certProblem checks a pasted or chosen certificate chain. */
export function certProblem(text: string): string | null {
  if (!text.trim()) return 'cert_missing'
  if (sizeOf(text) > PEM_MAX) return 'cert_too_large'
  if (keyBegin.test(text)) return 'cert_has_key'
  return /-----BEGIN CERTIFICATE-----/.test(text) ? null : 'cert_not_pem'
}

/** keyProblem checks a pasted or chosen private key. */
export function keyProblem(text: string): string | null {
  if (!text.trim()) return 'key_missing'
  if (sizeOf(text) > PEM_MAX) return 'key_too_large'
  if (/-----BEGIN ENCRYPTED PRIVATE KEY-----/.test(text) || /Proc-Type:\s*4,\s*ENCRYPTED/i.test(text)) return 'key_encrypted'
  const blocks = text.match(new RegExp(keyBegin.source, 'g'))?.length ?? 0
  if (blocks === 0) return 'key_not_pem'
  return blocks > 1 ? 'key_count' : null
}

/** formProblems lists what stops the form from being sent. domain is
 * BOBRES_DOMAIN: when it is an IP address, ip mode can only use that same one. */
export function formProblems(f: HttpsForm, o: { domain: string; manualAllowed: boolean }): FormProblem[] {
  const out: FormProblem[] = []
  if (f.mode === 'ip') {
    const code = ipProblem(f.ip)
    if (code) out.push({ field: 'ip', code })
    else if (isIPLiteral(o.domain) && canonicalIP(o.domain) !== canonicalIP(f.ip)) out.push({ field: 'ip', code: 'ip_domain_mismatch' })
  }
  if (f.mode === 'manual') {
    if (!o.manualAllowed) out.push({ field: 'mode', code: 'no_master_key' })
    const cert = certProblem(f.cert)
    if (cert) out.push({ field: 'cert', code: cert })
    const key = keyProblem(f.key)
    if (key) out.push({ field: 'key', code: key })
  }
  return out
}

/** needsConfirmation: the address must be confirmed as this server's before
 * it is sent: core asked for it (409 ip_unconfirmed, whatever address of its
 * own it named: it trusts a detection only for a few minutes, so the one this
 * page remembers may be older than its own), or the bot detected another one. */
export function needsConfirmation(ip: string, detected: string | null | undefined, asked: boolean): boolean {
  const want = canonicalIP(ip)
  if (!want) return false
  if (asked) return true
  return !!detected && canonicalIP(detected) !== want
}

/** putBody is the body of PUT /system/tls for a form. */
export function putBody(f: HttpsForm, confirm: boolean): Record<string, unknown> {
  switch (f.mode) {
    case 'ip':
      return { mode: 'ip', ip: latinDigits(f.ip).trim(), confirm_ip: confirm && f.confirmIp }
    case 'manual':
      return { mode: 'manual', cert: f.cert, key: f.key }
    default:
      return { mode: 'auto' }
  }
}

// The status.

export type AddressState = 'ok' | 'expiring' | 'invalid' | 'none' | 'unreachable'

/** addressState: it serves what it should; that is close to its end; it
 * serves a certificate that is not good; it serves none; Caddy did not
 * answer at all (a core that does not say, `reachable` missing, counts as
 * answered). */
export function addressState(a: Pick<TlsAddress, 'served' | 'valid' | 'expiring'> & { reachable?: boolean }): AddressState {
  if (a.reachable === false) return 'unreachable'
  if (!a.served) return 'none'
  if (!a.valid) return 'invalid'
  return a.expiring ? 'expiring' : 'ok'
}

export const addressSeverity: Record<AddressState, 'success' | 'warn' | 'danger'> = {
  ok: 'success',
  expiring: 'warn',
  invalid: 'danger',
  none: 'warn',
  unreachable: 'danger',
}

/** gatewayDown: Caddy, the HTTPS gateway, answers on none of the addresses
 * (it is down, stopped or restarting). One address that did not answer
 * among others that did is that address's own problem. */
export function gatewayDown<A extends object>(addresses: (A & { reachable?: boolean })[]): boolean {
  return addresses.length > 0 && addresses.every((a) => a.reachable === false)
}

export type AgentState = 'none' | 'idle' | 'waiting' | 'ok' | 'failed'

/** agentState reads the tls-agent's last result: no agent in this install,
 * nothing asked of it yet, a request it has not answered, applied, or failed. */
export function agentState(a: TlsAgent | null | undefined): AgentState {
  if (!a) return 'none'
  if (a.generation === 0) return 'idle'
  if (!a.answered) return 'waiting'
  return a.ok ? 'ok' : 'failed'
}

/** linksOnIp: dashboard links point at the server's IP address (ip mode, and
 * the certificate proven); until then they stay on the domain. */
export function linksOnIp(v: Pick<TlsView, 'state' | 'public_url'>): boolean {
  return modeOf(v.state.mode) === 'ip' && v.state.ip !== '' && v.public_url === 'https://' + hostPort(v.state.ip)
}

export type LinkSource = 'domain' | 'ip' | 'override' | 'none'

/** linkSource says where dashboard links point: at the domain, at the
 * server's IP address, at an address of the owner's own (BOBRES_PUBLIC_URL,
 * which core always puts first: the notes about links moving to the IP
 * address do not hold then), or nowhere (no domain). */
export function linkSource(v: Pick<TlsView, 'state' | 'public_url'>): LinkSource {
  const url = v.public_url
  if (!url) return 'none'
  if (v.state.domain && url === 'https://' + hostPort(v.state.domain)) return 'domain'
  if (v.state.ip && url === 'https://' + hostPort(v.state.ip)) return 'ip'
  return 'override'
}

/** certificateReady: every address the change is about serves what it
 * should (a live status is needed: the PUT answer has none). The change is
 * about the IP address in ip mode and the domain otherwise. */
export function certificateReady(v: TlsView, mode: TlsMode): boolean {
  const addrs = v.status.addresses.filter((a) => (mode === 'ip' ? a.address === v.state.ip : a.role === 'domain'))
  return addrs.length > 0 && addrs.every((a) => a.valid)
}

export interface WaitOptions {
  /** how often and for how long (httpsPoll when unset) */
  ms?: number
  timeoutMs?: number
  sleep?: (ms: number) => Promise<void>
  now?: () => number
  /** true once the tab is gone or another change started */
  cancelled?: () => boolean
  /** every state read */
  onView?: (v: TlsView) => void
}

export type WaitResult = { outcome: 'ready' | 'timeout' | 'cancelled' | 'failed'; view: TlsView | null }

/** waitForCertificate reads the state until the certificate of a change is
 * in use: at once, then every few seconds, for at most 90. A read that fails
 * is tried again; three in a row end it. */
export async function waitForCertificate(read: () => Promise<TlsView>, mode: TlsMode, o: WaitOptions = {}): Promise<WaitResult> {
  const ms = o.ms ?? httpsPoll.ms
  const timeoutMs = o.timeoutMs ?? httpsPoll.timeoutMs
  const sleep = o.sleep ?? ((d: number) => new Promise<void>((r) => setTimeout(r, d)))
  const now = o.now ?? Date.now
  const start = now()
  let view: TlsView | null = null
  let failures = 0
  for (;;) {
    if (o.cancelled?.()) return { outcome: 'cancelled', view }
    try {
      view = await read()
      failures = 0
      if (o.cancelled?.()) return { outcome: 'cancelled', view }
      o.onView?.(view)
      if (certificateReady(view, mode)) return { outcome: 'ready', view }
    } catch {
      if (++failures >= 3) return { outcome: 'failed', view }
    }
    if (now() - start + ms > timeoutMs) return { outcome: 'timeout', view }
    await sleep(ms)
  }
}
