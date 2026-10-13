import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { ApiError } from '../api'
import { date, dateTime } from '../format'
import { auditLink } from '../staff'
import {
  addressSeverity,
  addressState,
  agentState,
  AGENT_CODES,
  canonicalIP,
  certificateReady,
  certProblem,
  codeKey,
  failureText,
  fieldOf,
  formProblems,
  gatewayDown,
  httpsPoll,
  INVALID_CODES,
  ipProblem,
  isIPLiteral,
  keyProblem,
  linkSource,
  linksOnIp,
  modeOf,
  needsConfirmation,
  OTHER_CODES,
  PEM_MAX,
  problemText,
  PROBLEM_CODES,
  putBody,
  RECOVERY_COMMAND,
  REFUSAL_CODES,
  waitForCertificate,
  WARNING_CODES,
  warningText,
  type HttpsForm,
} from '../https'
import type { TlsAddress, TlsAgent, TlsCert, TlsView } from '../types'

const DAY = 86_400
const now = () => Math.floor(Date.now() / 1000)
const cert = (over: Partial<TlsCert> = {}): TlsCert => ({
  subject: 'shop.example.com', issuer: "Let's Encrypt E7", dns_names: ['shop.example.com'], ips: [],
  not_before: now() - 30 * DAY, not_after: now() + 60 * DAY, ...over,
})
const address = (over: Partial<TlsAddress> = {}): TlsAddress => ({
  address: 'shop.example.com', role: 'domain', expect: 'public', reachable: true, served: true, valid: true, name_ok: true, trusted: true,
  expiring: false, days_left: 60, cert: cert(), error: '', ...over,
})
const agent = (over: Partial<TlsAgent> = {}): TlsAgent => ({ generation: 3, answered: true, ok: true, code: '', error: '', applied_at: now() - 3600, ...over })
const view = (over: Partial<TlsView> = {}): TlsView => ({
  state: { mode: 'auto', ip: '', cert: null, updated_at: now() - DAY, domain: 'shop.example.com', zarinpal_host: '', manual_allowed: true },
  effective: { mode: 'auto', ip: '' },
  problem: null,
  status: { checked_at: now(), addresses: [address()], agent: agent() },
  public_url: 'https://shop.example.com',
  detected_ip: null,
  ...over,
})
const form = (over: Partial<HttpsForm> = {}): HttpsForm => ({ mode: 'auto', ip: '', cert: '', key: '', confirmIp: false, ...over })
const pem = (label: string, body = 'MIIB') => `-----BEGIN ${label}-----\n${body}\n-----END ${label}-----\n`
const CERT = pem('CERTIFICATE')
const KEY = pem('PRIVATE KEY')

// What t and te do in the page, with the Persian catalog.
const lookup = (k: string) => k.split('.').reduce<unknown>((o, p) => (o as Record<string, unknown> | undefined)?.[p], fa)
const te = (k: string) => typeof lookup(k) === 'string'
const t = (k: string, p: Record<string, unknown> = {}) => {
  const s = lookup(k)
  if (typeof s !== 'string') return k
  return Object.entries(p).reduce((out, [name, v]) => out.replaceAll('{' + name + '}', String(v)), s)
}
const api = (status: number, error: string, body: Record<string, unknown> = {}) => new ApiError(status, error, String(body.message ?? error), { error, ...body })

describe('modes and the form', () => {
  it('reads the mode core gives, anything else as automatic', () => {
    expect(modeOf('ip')).toBe('ip')
    expect(modeOf('manual')).toBe('manual')
    expect(modeOf('auto')).toBe('auto')
    expect(modeOf('odd')).toBe('auto')
    expect(modeOf('')).toBe('auto')
    expect(modeOf(undefined)).toBe('auto')
  })
  it('tells an IP address from a host name, and writes it the way core compares it', () => {
    expect(isIPLiteral('8.8.8.8')).toBe(true)
    expect(isIPLiteral(' ۸.۸.۸.۸ ')).toBe(true)
    expect(isIPLiteral('2606:4700:4700::1111')).toBe(true)
    expect(isIPLiteral('shop.example.com')).toBe(false)
    expect(isIPLiteral('')).toBe(false)
    expect(canonicalIP(' ۱.۱.۱.۱ ')).toBe('1.1.1.1')
    expect(canonicalIP('2606:4700:4700:0:0:0:0:1111')).toBe('2606:4700:4700::1111')
    expect(canonicalIP('2606:4700:4700::ABCD')).toBe('2606:4700:4700::abcd')
    expect(canonicalIP(' Not An IP ')).toBe('not an ip')
  })
  it('names what is plainly wrong with a server address, with core codes', () => {
    expect(ipProblem('')).toBe('ip_required')
    expect(ipProblem('   ')).toBe('ip_required')
    expect(ipProblem('shop.example.com')).toBe('bad_ip')
    expect(ipProblem('300.1.1.1')).toBe('bad_ip')
    expect(ipProblem('01.1.1.1')).toBe('bad_ip')
    expect(ipProblem('1.1.1')).toBe('bad_ip')
    expect(ipProblem('2606:::1')).toBe('bad_ip')
    for (const ip of ['10.0.0.5', '127.0.0.1', '192.168.1.10', '172.16.0.1', '172.31.255.1', '169.254.1.1', '100.64.0.1', '100.127.255.254', '0.0.0.0', '224.0.0.1', '198.18.0.1', '192.0.2.4', '203.0.113.9', '::1', '::', 'fe80::1', 'fd00::1', 'ff02::1', '2001:db8::1']) {
      expect(ipProblem(ip), ip).toBe('ip_not_public')
    }
    // the edges of the private ranges are public
    for (const ip of ['172.15.0.1', '172.32.0.1', '100.63.0.1', '100.128.0.1', '8.8.8.8', '2606:4700:4700::1111']) {
      expect(ipProblem(ip), ip).toBeNull()
    }
    // typed in Persian digits
    expect(ipProblem('۸.۸.۸.۸')).toBeNull()
  })
  it('checks a pasted certificate chain', () => {
    expect(certProblem('')).toBe('cert_missing')
    expect(certProblem('  \n ')).toBe('cert_missing')
    expect(certProblem('hello')).toBe('cert_not_pem')
    expect(certProblem(CERT)).toBeNull()
    expect(certProblem(CERT + CERT)).toBeNull()
    // the key pasted into the certificate field
    expect(certProblem(CERT + KEY)).toBe('cert_has_key')
    expect(certProblem(pem('RSA PRIVATE KEY'))).toBe('cert_has_key')
    expect(certProblem(CERT + 'A'.repeat(PEM_MAX))).toBe('cert_too_large')
  })
  it('checks a pasted private key', () => {
    expect(keyProblem('')).toBe('key_missing')
    expect(keyProblem(CERT)).toBe('key_not_pem')
    expect(keyProblem(KEY)).toBeNull()
    expect(keyProblem(pem('EC PRIVATE KEY'))).toBeNull()
    expect(keyProblem(pem('RSA PRIVATE KEY'))).toBeNull()
    expect(keyProblem(pem('ENCRYPTED PRIVATE KEY'))).toBe('key_encrypted')
    expect(keyProblem(pem('RSA PRIVATE KEY', 'Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,AA\n\nMIIB'))).toBe('key_encrypted')
    expect(keyProblem(KEY + KEY)).toBe('key_count')
    expect(keyProblem(KEY + 'A'.repeat(PEM_MAX))).toBe('key_too_large')
  })
  it('lists what stops each mode from being sent', () => {
    const o = { domain: 'shop.example.com', manualAllowed: true }
    expect(formProblems(form(), o)).toEqual([])
    // automatic never needs a certificate, even with leftovers typed in the other modes
    expect(formProblems(form({ mode: 'auto', ip: 'x', cert: 'y', key: 'z' }), o)).toEqual([])

    expect(formProblems(form({ mode: 'ip' }), o)).toEqual([{ field: 'ip', code: 'ip_required' }])
    expect(formProblems(form({ mode: 'ip', ip: '10.0.0.1' }), o)).toEqual([{ field: 'ip', code: 'ip_not_public' }])
    expect(formProblems(form({ mode: 'ip', ip: '8.8.8.8' }), o)).toEqual([])
    // the domain is itself an address: ip mode can only use that one
    const byIp = { domain: '1.1.1.1', manualAllowed: true }
    expect(formProblems(form({ mode: 'ip', ip: '8.8.8.8' }), byIp)).toEqual([{ field: 'ip', code: 'ip_domain_mismatch' }])
    expect(formProblems(form({ mode: 'ip', ip: ' ۱.۱.۱.۱ ' }), byIp)).toEqual([])

    expect(formProblems(form({ mode: 'manual' }), o)).toEqual([{ field: 'cert', code: 'cert_missing' }, { field: 'key', code: 'key_missing' }])
    expect(formProblems(form({ mode: 'manual', cert: CERT, key: KEY }), o)).toEqual([])
    expect(formProblems(form({ mode: 'manual', cert: CERT, key: CERT }), o)).toEqual([{ field: 'key', code: 'key_not_pem' }])
    // no master key in core: the pair could not be sealed
    expect(formProblems(form({ mode: 'manual', cert: CERT, key: KEY }), { ...o, manualAllowed: false })).toEqual([{ field: 'mode', code: 'no_master_key' }])
  })
  it('asks for a confirmation when the address is not the one the bot detected, or none was', () => {
    expect(needsConfirmation('8.8.8.8', '8.8.8.8', false)).toBe(false)
    expect(needsConfirmation(' ۸.۸.۸.۸ ', '8.8.8.8', false)).toBe(false)
    expect(needsConfirmation('2606:4700:4700:0:0:0:0:1111', '2606:4700:4700::1111', false)).toBe(false)
    expect(needsConfirmation('1.1.1.1', '8.8.8.8', false)).toBe(true)
    // nothing detected: core says when it needs one (409 ip_unconfirmed)
    expect(needsConfirmation('1.1.1.1', '', false)).toBe(false)
    expect(needsConfirmation('1.1.1.1', null, true)).toBe(true)
    expect(needsConfirmation('', '8.8.8.8', true)).toBe(false)
    // once core asked, it asked: the address it remembered from earlier (or the same one it names) does not take the question back
    expect(needsConfirmation('8.8.8.8', '8.8.8.8', true)).toBe(true)
    expect(needsConfirmation('8.8.8.8', '', true)).toBe(true)
    expect(needsConfirmation('1.1.1.1', '8.8.8.8', true)).toBe(true)
  })
  it('sends only what the mode uses', () => {
    expect(putBody(form({ mode: 'auto', ip: '8.8.8.8', cert: CERT, key: KEY, confirmIp: true }), true)).toEqual({ mode: 'auto' })
    expect(putBody(form({ mode: 'ip', ip: ' ۸.۸.۸.۸ ', cert: CERT, key: KEY, confirmIp: true }), true)).toEqual({ mode: 'ip', ip: '8.8.8.8', confirm_ip: true })
    // a tick left from before does not confirm an address that needs none
    expect(putBody(form({ mode: 'ip', ip: '8.8.8.8', confirmIp: true }), false)).toEqual({ mode: 'ip', ip: '8.8.8.8', confirm_ip: false })
    expect(putBody(form({ mode: 'ip', ip: '8.8.8.8', confirmIp: false }), true)).toEqual({ mode: 'ip', ip: '8.8.8.8', confirm_ip: false })
    expect(putBody(form({ mode: 'manual', ip: '8.8.8.8', cert: CERT, key: KEY }), false)).toEqual({ mode: 'manual', cert: CERT, key: KEY })
  })
})

describe('what an error or a warning says', () => {
  it('has a text in both languages for every code the API answers with', () => {
    const groups: [string, string[]][] = [
      ['code', [...INVALID_CODES, ...REFUSAL_CODES, ...OTHER_CODES]],
      ['warning', WARNING_CODES],
      ['agent_code', AGENT_CODES],
      ['problem', PROBLEM_CODES],
    ]
    for (const [group, codes] of groups) {
      for (const code of codes) {
        for (const [lang, page] of [['en', en], ['fa', fa]] as const) {
          const text = (page.system.https as unknown as Record<string, Record<string, string>>)[group][code]
          expect(text, `${lang} ${group}.${code}`).toBeTruthy()
        }
      }
    }
    // and the same placeholders in both
    const holes = (m: string) => [...m.matchAll(/\{(\w+)\}/g)].map((x) => x[1]).sort()
    const flat = (o: object, p = ''): [string, string][] =>
      Object.entries(o).flatMap(([k, v]) => (typeof v === 'object' ? flat(v, p + k + '.') : [[p + k, v as string] as [string, string]]))
    const faTexts = new Map(flat(fa.system.https))
    for (const [k, v] of flat(en.system.https)) expect(holes(faTexts.get(k) ?? ''), k).toEqual(holes(v))
  })
  it('names the field a code is about', () => {
    expect(fieldOf('bad_mode')).toBe('mode')
    expect(fieldOf('ip_required')).toBe('ip')
    expect(fieldOf('bad_ip')).toBe('ip')
    expect(fieldOf('ip_domain_mismatch')).toBe('ip')
    expect(fieldOf('cert_expired')).toBe('cert')
    expect(fieldOf('chain_order')).toBe('cert')
    expect(fieldOf('key_mismatch')).toBe('key')
    expect(fieldOf('caddy_refused')).toBe('')
    expect(codeKey('key_mismatch')).toBe('system.https.code.key_mismatch')
    expect(codeKey('something_new')).toBe('')
  })
  it('translates a 400 and shows the field it names', () => {
    const f = failureText(api(400, 'invalid', { code: 'key_mismatch', field: 'key', message: 'the private key does not belong to the certificate' }), t, te)
    expect(f.text).toBe(fa.system.https.code.key_mismatch)
    expect(f.field).toBe('key')
    expect(f.code).toBe('key_mismatch')
    // a name or a date is worth reading in English under the text
    const name = failureText(api(400, 'invalid', { code: 'cert_name_mismatch', field: 'cert', message: 'the certificate covers a.example.com, not shop.example.com' }), t, te)
    expect(name.detail).toBe('the certificate covers a.example.com, not shop.example.com')
    // not one a text already says in full
    expect(f.detail).toBe('')
    // the field comes from the code when the answer does not name it
    expect(failureText(api(400, 'invalid', { code: 'ip_not_public', message: 'x' }), t, te).field).toBe('ip')
  })
  it('puts the address in the confirmation, the detected one or none', () => {
    const other = failureText(api(409, 'confirm', { code: 'ip_unconfirmed', detected_ip: '8.8.8.8', message: 'm' }), t, te, '1.1.1.1')
    expect(other.code).toBe('ip_unconfirmed')
    expect(other.detectedIp).toBe('8.8.8.8')
    expect(other.text).toBe(fa.system.https.code.ip_unconfirmed_other.replace('{detected}', '⁨8.8.8.8⁩').replace(/\{ip\}/g, '⁨1.1.1.1⁩'))
    const none = failureText(api(409, 'confirm', { code: 'ip_unconfirmed', detected_ip: null, message: 'm' }), t, te, '1.1.1.1')
    expect(none.detectedIp).toBe('')
    expect(none.text).toBe(fa.system.https.code.ip_unconfirmed.replace('{ip}', '⁨1.1.1.1⁩'))
  })
  it("shows Caddy's own words under a refusal", () => {
    const f = failureText(api(409, 'refused', { code: 'caddy_refused', message: 'adapting config: server listening on [:443] is missing a certificate' }), t, te)
    expect(f.text).toBe(fa.system.https.code.caddy_refused)
    expect(f.detail).toBe('adapting config: server listening on [:443] is missing a certificate')
    const agentFailed = failureText(api(409, 'refused', { code: 'agent_failed', message: 'the applier could not write /tls/sites.caddy' }), t, te)
    expect(agentFailed.detail).toBe('the applier could not write /tls/sites.caddy')
  })
  it('translates the 409 and 503 refusals, with the commands kept whole', () => {
    expect(failureText(api(409, 'state', { code: 'no_master_key', message: 'm' }), t, te).text).toBe(fa.system.https.code.no_master_key)
    expect(failureText(api(503, 'unavailable', { code: 'agent_timeout', message: 'm' }), t, te).text).toContain('⁨sudo bobres status⁩')
    expect(failureText(api(503, 'unavailable', { code: 'caddy_unreachable', message: 'm' }), t, te).text).toContain('⁨sudo bobres start⁩')
    const missing = failureText(api(503, 'unavailable', { code: 'agent_unavailable', message: 'no tls-agent' }), t, te)
    expect(missing.text).toContain('⁨sudo bobres update⁩')
    expect(missing.detail).toBe('no tls-agent')
  })
  it('says that another change is being applied, in both languages', () => {
    // 503 busy: the engine's answer while another change (here or in the CLI) holds the lock
    const f = failureText(api(503, 'unavailable', { code: 'busy', message: 'another HTTPS change is being applied: try again in a moment' }), t, te)
    expect(f.text).toBe(fa.system.https.code.busy)
    expect(f.code).toBe('busy')
    expect(f.field).toBe('')
    // the text says it all: core's English words are not repeated under it
    expect(f.detail).toBe('')
    expect(REFUSAL_CODES).toContain('busy')
    expect(codeKey('busy')).toBe('system.https.code.busy')
    expect(en.system.https.code.busy).toBe('Another change is being applied. Try again in a moment.')
    expect(fa.system.https.code.busy).toBeTruthy()
    // an unknown 503 still gets the general text, never a raw code
    expect(failureText(api(503, 'unavailable', { code: 'brand_new', message: 'x' }), t, te).text).toBe(fa.error.unavailable)
  })
  it('knows the failures that carry no code of this page', () => {
    expect(failureText(api(502, 'unavailable', { code: 'detect_failed', message: 'm' }), t, te).text).toBe(fa.system.https.code.detect_failed)
    expect(failureText(new ApiError(429, 'rate_limited', 'at most 10 changes a minute'), t, te).text).toBe(fa.system.https.code.rate_limited)
    expect(failureText(new ApiError(503, 'not_configured', 'HTTPS is not managed by this core'), t, te).text).toBe(fa.system.https.code.not_configured)
    // anything else gets the general text, never a raw code
    expect(failureText(new ApiError(500, 'error', 'boom'), t, te).text).toBe(fa.error.generic)
    expect(failureText(api(400, 'invalid', { code: 'brand_new_code', message: 'a newer core' }), t, te).text).toBe(fa.error.invalid)
    expect(failureText(new ApiError(0, 'network', 'network'), t, te).text).toBe(fa.error.network)
    expect(failureText(new Error('x'), t, te).text).toBe(fa.error.generic)
  })
  it("translates PUT's warnings, and keeps core's words for one it does not know", () => {
    for (const code of WARNING_CODES) expect(warningText({ code, message: 'english' }, t, te)).toBe((fa.system.https.warning as Record<string, string>)[code])
    expect(warningText({ code: 'brand_new', message: 'a newer warning' }, t, te)).toBe('a newer warning')
  })
  it('says why the saved setting is not in use', () => {
    expect(problemText({ code: 'key_unreadable', message: 'x' }, t, te)).toEqual({ text: fa.system.https.problem.key_unreadable, detail: '' })
    expect(problemText({ code: 'no_master_key', message: 'x' }, t, te).text).toBe(fa.system.https.problem.no_master_key)
    // an applier that cannot be reached: core's reason too
    expect(problemText({ code: 'agent_unavailable', message: 'volume not mounted' }, t, te)).toEqual({ text: fa.system.https.problem.agent_unavailable, detail: 'volume not mounted' })
    // a certificate that cannot be used any more says why
    const expired = problemText({ code: 'cert_expired', message: 'the certificate expired on 2026-10-01' }, t, te)
    expect(expired.text).toBe(fa.system.https.problem.manual_unused.replace('{reason}', fa.system.https.code.cert_expired))
    expect(expired.detail).toBe('the certificate expired on 2026-10-01')
    const name = problemText({ code: 'cert_name_mismatch', message: 'm' }, t, te)
    expect(name.text).toContain(fa.system.https.code.cert_name_mismatch)
    expect(problemText({ code: 'ip_domain_mismatch', message: 'm' }, t, te).text).toBe(fa.system.https.problem.ip_unused.replace('{reason}', fa.system.https.code.ip_domain_mismatch))
    // not known: core's words under the general text
    expect(problemText({ code: 'brand_new', message: 'something else' }, t, te)).toEqual({ text: fa.system.https.problem.other, detail: 'something else' })
  })
  it('names the recovery command of the lock-out warning', () => {
    expect(RECOVERY_COMMAND).toBe('sudo bobres ssl auto')
    expect(en.system.https.warn.manual_lockout).toContain('{command}')
    expect(fa.system.https.warn.manual_lockout).toContain('{command}')
  })
})

describe('the audit log', () => {
  it('names the HTTPS change in both languages and opens the HTTPS tab', () => {
    expect(en.audit.action.system.tls.set).toBe('HTTPS setting changed')
    expect(fa.audit.action.system.tls.set).toBeTruthy()
    expect(en.audit.entity.tls).toBeTruthy()
    expect(fa.audit.entity.tls).toBeTruthy()
    expect(en.audit.group.system).toBeTruthy()
    expect(fa.audit.group.system).toBeTruthy()
    const entry = { entity: 'tls', entity_id: '', after: { mode: 'ip' }, actor: { id: 'u1', telegram_id: 1, username: 'boss' } }
    expect(auditLink(entry)).toEqual({ name: 'system', query: { tab: 'https' } })
  })
  it('has a label for each alert the watchdog raises about HTTPS', () => {
    for (const kind of ['tls_expiring', 'tls_failing', 'tls_caddy_down', 'tls_problem']) {
      expect((en.system.alerts.kind as Record<string, string>)[kind], kind).toBeTruthy()
      expect((fa.system.alerts.kind as Record<string, string>)[kind], kind).toBeTruthy()
    }
  })
})

describe('what each address serves', () => {
  it('reads an address as in use, ending soon, not valid, or without a certificate', () => {
    expect(addressState(address())).toBe('ok')
    expect(addressState(address({ expiring: true }))).toBe('expiring')
    expect(addressState(address({ valid: false }))).toBe('invalid')
    expect(addressState(address({ valid: false, expiring: true }))).toBe('invalid')
    expect(addressState(address({ served: false, valid: false }))).toBe('none')
    expect(addressSeverity.ok).toBe('success')
    expect(addressSeverity.expiring).toBe('warn')
    expect(addressSeverity.invalid).toBe('danger')
    expect(addressSeverity.none).toBe('warn')
  })
  it('reads an address Caddy did not answer on, and tells when no address was reachable', () => {
    const down = (over: Partial<TlsAddress> = {}) => address({ reachable: false, served: false, valid: false, trusted: null, cert: null, error: 'dial tcp 172.18.0.5:443: connection refused', ...over })
    expect(addressState(down())).toBe('unreachable')
    // not "no certificate": nothing is known about its certificate
    expect(addressState(down({ served: true }))).toBe('unreachable')
    expect(addressSeverity.unreachable).toBe('danger')
    // a core that does not say counts as answered
    const { reachable: _omit, ...older } = address()
    expect(addressState(older)).toBe('ok')

    expect(gatewayDown([down(), down({ address: '8.8.8.8', role: 'ip' })])).toBe(true)
    expect(gatewayDown([down()])).toBe(true)
    // one address that did not answer among others that did is its own problem
    expect(gatewayDown([down(), address({ address: '8.8.8.8', role: 'ip' })])).toBe(false)
    expect(gatewayDown([address(), address({ served: false, valid: false })])).toBe(false)
    // nothing was checked yet
    expect(gatewayDown([])).toBe(false)
    // an older core sends no `reachable`: never reported as down
    expect(gatewayDown([older, older])).toBe(false)
  })
  it('has the texts for the gateway line in both languages, with the commands kept whole', () => {
    for (const page of [en, fa]) {
      expect(page.system.https.caddy_down.title).toBeTruthy()
      expect(page.system.https.caddy_down.detail).toContain('{status}')
      expect(page.system.https.caddy_down.detail).toContain('{logs}')
      expect(page.system.https.state.unreachable).toBeTruthy()
    }
    expect(en.system.https.caddy_down.title).toBe('HTTPS gateway (Caddy) is not reachable')
  })
  it("reads the applier's last result", () => {
    expect(agentState(null)).toBe('none')
    expect(agentState(undefined)).toBe('none')
    expect(agentState(agent({ generation: 0, answered: false }))).toBe('idle')
    expect(agentState(agent({ answered: false }))).toBe('waiting')
    expect(agentState(agent())).toBe('ok')
    expect(agentState(agent({ ok: false, code: 'caddy_refused', error: 'x' }))).toBe('failed')
  })
  it('moves links to the IP address only once its certificate is proven', () => {
    const ipState = { ...view().state, mode: 'ip', ip: '8.8.8.8' }
    expect(linksOnIp({ state: ipState, public_url: 'https://8.8.8.8' })).toBe(true)
    expect(linksOnIp({ state: ipState, public_url: 'https://shop.example.com' })).toBe(false)
    expect(linksOnIp({ state: { ...ipState, ip: '2606:4700:4700::1111' }, public_url: 'https://[2606:4700:4700::1111]' })).toBe(true)
    expect(linksOnIp({ state: { ...ipState, mode: 'auto' }, public_url: 'https://8.8.8.8' })).toBe(false)
  })
  it('tells where links point: the domain, the IP address, or an address of the owner (BOBRES_PUBLIC_URL)', () => {
    const ipState = { ...view().state, mode: 'ip', ip: '8.8.8.8' }
    expect(linkSource({ state: ipState, public_url: 'https://shop.example.com' })).toBe('domain')
    expect(linkSource({ state: ipState, public_url: 'https://8.8.8.8' })).toBe('ip')
    expect(linkSource({ state: { ...ipState, ip: '2606:4700:4700::1111' }, public_url: 'https://[2606:4700:4700::1111]' })).toBe('ip')
    expect(linkSource({ state: { ...ipState, domain: '2606:4700:4700::1111' }, public_url: 'https://[2606:4700:4700::1111]' })).toBe('domain')
    // core puts the owner's address first, whatever the mode and the proof
    expect(linkSource({ state: ipState, public_url: 'https://panel.example.org' })).toBe('override')
    expect(linkSource({ state: ipState, public_url: 'https://shop.example.com:8443' })).toBe('override')
    expect(linkSource({ state: { ...ipState, mode: 'auto', ip: '' }, public_url: 'https://panel.example.org' })).toBe('override')
    // no domain and no address: no links to speak of
    expect(linkSource({ state: { ...ipState, domain: '' }, public_url: '' })).toBe('none')
  })
  it('counts a change as done when the address it is about serves what it should', () => {
    const domainOk = address()
    const ipPending = address({ address: '8.8.8.8', role: 'ip', served: false, valid: false, cert: null })
    const ipOk = address({ address: '8.8.8.8', role: 'ip' })
    const withState = (addresses: TlsAddress[]) => view({ state: { ...view().state, mode: 'ip', ip: '8.8.8.8' }, status: { checked_at: now(), addresses, agent: agent() } })
    // ip mode waits for the IP address, not for the domain that was fine already
    expect(certificateReady(withState([domainOk, ipPending]), 'ip')).toBe(false)
    expect(certificateReady(withState([domainOk, ipOk]), 'ip')).toBe(true)
    // the other modes wait for the domain
    expect(certificateReady(withState([address({ valid: false }), ipOk]), 'auto')).toBe(false)
    expect(certificateReady(withState([domainOk, ipPending]), 'manual')).toBe(true)
    // PUT's answer has no live addresses: nothing to call ready yet
    expect(certificateReady(view({ status: { checked_at: null, addresses: [], agent: agent() } }), 'auto')).toBe(false)
  })
})

describe('waiting for the certificate', () => {
  const pending = () => view({ status: { checked_at: now(), addresses: [address({ served: false, valid: false, cert: null })], agent: agent() } })
  const clock = () => {
    let at = 0
    const slept: number[] = []
    return { slept, now: () => at, sleep: async (ms: number) => { slept.push(ms); at += ms } }
  }
  it('reads every 3 seconds, for up to 90, as the page says', () => {
    expect(httpsPoll).toEqual({ ms: 3000, timeoutMs: 90_000 })
  })
  it('reads at once and stops when the certificate is in use', async () => {
    const c = clock()
    const reads = [pending(), pending(), view()]
    let n = 0
    const seen: TlsView[] = []
    const r = await waitForCertificate(async () => reads[n++], 'auto', { ...c, onView: (v) => seen.push(v) })
    expect(r.outcome).toBe('ready')
    expect(r.view).toBe(reads[2])
    expect(c.slept).toEqual([3000, 3000])
    expect(n).toBe(3)
    expect(seen).toHaveLength(3)
    // already in use: no wait at all
    const quick = clock()
    expect((await waitForCertificate(async () => view(), 'auto', quick)).outcome).toBe('ready')
    expect(quick.slept).toEqual([])
  })
  it('gives up after 90 seconds', async () => {
    const c = clock()
    let n = 0
    const r = await waitForCertificate(async () => (n++, pending()), 'auto', c)
    expect(r.outcome).toBe('timeout')
    expect(r.view).not.toBeNull()
    expect(c.slept.every((ms) => ms === 3000)).toBe(true)
    expect(c.slept.reduce((a, b) => a + b, 0)).toBe(90_000)
    expect(n).toBe(31)
  })
  it('tries a failed read again, and stops after three in a row', async () => {
    const c = clock()
    let n = 0
    const flaky = await waitForCertificate(async () => {
      if (n++ === 0) throw new ApiError(503, 'unavailable', 'down')
      return view()
    }, 'auto', c)
    expect(flaky.outcome).toBe('ready')
    expect(c.slept).toEqual([3000])

    const d = clock()
    let m = 0
    const dead = await waitForCertificate(async () => {
      m++
      throw new ApiError(0, 'network', 'network')
    }, 'auto', d)
    expect(dead.outcome).toBe('failed')
    expect(dead.view).toBeNull()
    expect(m).toBe(3)

    // two failures, then an answer: the count starts over
    const e = clock()
    const answers: (TlsView | Error)[] = [new Error('a'), new Error('b'), pending(), new Error('c'), new Error('d'), view()]
    let k = 0
    const mixed = await waitForCertificate(async () => {
      const a = answers[k++]
      if (a instanceof Error) throw a
      return a
    }, 'auto', e)
    expect(mixed.outcome).toBe('ready')
  })
  it('stops when the tab is gone or another change began', async () => {
    const c = clock()
    let gone = false
    let n = 0
    const r = await waitForCertificate(async () => {
      n++
      gone = true
      return pending()
    }, 'auto', { ...c, cancelled: () => gone && n > 0 })
    expect(r.outcome).toBe('cancelled')
    expect(n).toBe(1)
    const early = await waitForCertificate(async () => view(), 'auto', { ...clock(), cancelled: () => true })
    expect(early.outcome).toBe('cancelled')
    expect(early.view).toBeNull()
  })
})

describe('https page', () => {
  const adminPerms = ['overview.read', 'users.read', 'plans.write', 'settings.write', 'servers.write', 'audit.read', 'system.read']
  const ownerPerms = [...adminPerms, 'gateways.write', 'staff.write', 'system.write', 'license.write']
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  // a text that is not in the catalog fails here, instead of standing in as its own path
  const text = (path: string) => {
    const s = lookup(path)
    if (typeof s !== 'string') throw new Error(`fa.json has no text "${path}"`)
    return s
  }
  const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replaceAll('{' + k + '}', v), s)

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | undefined
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(o: { perms?: string[]; role?: string; tls?: TlsView; answer?: Answer } = {}) {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', '/admin/system?tab=https')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${raw}`.trim())
        const own = o.answer?.(url, init, raw ? (JSON.parse(raw) as Record<string, unknown>) : {})
        if (own) return own
        const get = (init?.method ?? 'GET') === 'GET'
        if (url === '/api/v1/me') {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: o.role ?? 'admin', language: 'fa' }, permissions: o.perms ?? adminPerms, csrf: 'c',
            session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
          })
        }
        if (get && url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
        if (get && url === '/api/v1/system') return json(200, { version: 'v1.4.0', services: [], checked_at: now() })
        if (get && url === '/api/v1/system/tls') return json(200, o.tls ?? view())
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }
  afterEach(() => {
    unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })
  const btn = (selector: string) => document.querySelector(selector) as HTMLButtonElement
  const input = (selector: string) => document.querySelector(selector) as HTMLInputElement
  const type = (selector: string, value: string) => {
    const el = input(selector)
    el.value = value
    el.dispatchEvent(new Event('input'))
  }
  const pick = (mode: string) => input(`.mode-${mode} input[type="radio"]`).click()
  const sent = (calls: string[], method: string) =>
    calls.filter((c) => c.startsWith(`${method} /api/v1/system/tls `)).map((c) => JSON.parse(c.slice(`${method} /api/v1/system/tls `.length)) as Record<string, unknown>)
  const count = (calls: string[], c: string) => calls.filter((x) => x === c).length
  const puts = (calls: string[]) => calls.filter((c) => c.startsWith('PUT /api/v1/system/tls'))
  // what PUT answers: the new state without live addresses, and the notes
  const putOk = (over: Partial<TlsView> = {}, extra: Record<string, unknown> = {}) =>
    json(200, { ...view({ ...over, status: { checked_at: null, addresses: [], agent: agent() } }), warnings: [], pending: false, ...extra })

  it('shows everyone with system.read the status, and no form', async () => {
    const t0 = now()
    const tls = view({
      state: { mode: 'ip', ip: '8.8.8.8', cert: null, updated_at: t0, domain: 'shop.example.com', zarinpal_host: 'pay.example.com', manual_allowed: true },
      effective: { mode: 'ip', ip: '8.8.8.8' },
      status: {
        checked_at: t0,
        addresses: [
          address({ expiring: true, days_left: 5, cert: cert({ not_after: t0 + 5 * DAY }) }),
          address({ address: '8.8.8.8', role: 'ip', cert: cert({ subject: '8.8.8.8', not_after: t0 + 4 * DAY, issuer: "Let's Encrypt shortlived" }), days_left: 4 }),
          address({ address: 'pay.example.com', role: 'zarinpal', served: false, valid: false, trusted: null, cert: null, error: 'no certificate yet' }),
        ],
        agent: agent({ ok: false, code: 'caddy_refused', error: 'adapting config: bad site address', applied_at: t0 - 60 }),
      },
      public_url: 'https://shop.example.com',
      detected_ip: '8.8.8.8',
    })
    const { calls } = await boot({ tls })
    await found('.addresses')
    // the tab is there for an admin, with the form left out
    expect(document.querySelector('.tab-https')?.textContent?.trim()).toBe(text('system.tab.https'))
    expect(document.querySelector('.https-form')).toBeNull()
    expect(document.querySelector('.owner-only')?.textContent?.trim()).toBe(text('system.https.owner_only'))
    expect(calls).toContain('GET /api/v1/system/tls')

    expect(document.querySelector('.saved-mode')?.textContent).toContain(text('system.https.mode.ip'))
    expect(document.querySelector('.domain')?.textContent).toBe('shop.example.com')
    expect(document.querySelector('.zarinpal-host')?.textContent).toBe('pay.example.com')
    expect(document.querySelector('.server-ip')?.textContent).toBe('8.8.8.8')
    // links still on the domain: the IP certificate is not proven in the answer
    expect(document.querySelector('.public-url')?.textContent).toContain('https://shop.example.com')
    expect(document.querySelector('.links-domain')?.textContent).toBe(text('system.https.status.links_domain'))

    const [domain, ip, zarinpal] = [...document.querySelectorAll('.addresses .address')] as HTMLElement[]
    expect(domain.querySelector('.host')?.textContent).toBe('shop.example.com')
    expect(domain.querySelector('.role')?.textContent).toBe(text('system.https.role.domain'))
    expect(domain.querySelector('.address-state')?.textContent).toBe(text('system.https.state.expiring'))
    expect(domain.querySelector('.address-state')?.classList.contains('p-tag-warn')).toBe(true)
    expect(domain.querySelector('.issuer')?.textContent).toBe("Let's Encrypt E7")
    // the Jalali date and the days left, in Persian digits
    expect(domain.querySelector('.expires')?.textContent).toBe(`${date(t0 + 5 * DAY, 'fa')} · ${fill(text('system.https.addresses.days_left'), { n: '۵' })}`)
    expect(domain.querySelector('.trusted')?.textContent).toBe(text('system.https.addresses.trusted_yes'))
    expect(ip.querySelector('.role')?.textContent).toBe(text('system.https.role.ip'))
    expect(ip.querySelector('.address-state')?.textContent).toBe(text('system.https.state.ok'))
    expect(ip.querySelector('.address-state')?.classList.contains('p-tag-success')).toBe(true)
    // nothing served: said, with the server's words, and no table of dates
    expect(zarinpal.querySelector('.address-state')?.textContent).toBe(text('system.https.state.none'))
    expect(zarinpal.querySelector('.address-error')?.textContent).toBe('no certificate yet')
    expect(zarinpal.querySelector('.expires')).toBeNull()

    // the applier's last result, with Caddy's words
    expect(document.querySelector('.agent-state')?.textContent).toContain(fill(text('system.https.agent.failed'), { time: dateTime(t0 - 60, 'fa'), reason: text('system.https.agent_code.caddy_refused') }))
    expect(document.querySelector('.agent-state')?.classList.contains('p-tag-danger')).toBe(true)
    expect(document.querySelector('.agent-error')?.textContent).toBe('adapting config: bad site address')
    expect(document.querySelector('.https-problem')).toBeNull()
    // read only: nothing is sent from here
    expect(calls.some((c) => !c.startsWith('GET '))).toBe(false)
  })

  it('says in one line that the HTTPS gateway (Caddy) is not reachable, when no address answered', async () => {
    const refused = 'dial tcp 172.18.0.5:443: connect: connection refused'
    const down = (a: Partial<TlsAddress>) => address({ reachable: false, served: false, valid: false, trusted: null, cert: null, error: refused, ...a })
    const tls = view({
      state: { ...view().state, mode: 'ip', ip: '8.8.8.8' },
      effective: { mode: 'ip', ip: '8.8.8.8' },
      status: { checked_at: now(), addresses: [down({}), down({ address: '8.8.8.8', role: 'ip' })], agent: agent() },
    })
    await boot({ tls })
    const line = await found('.caddy-down')
    expect(line.querySelector('.caddy-down-title')?.textContent?.trim()).toBe(text('system.https.caddy_down.title'))
    expect(line.querySelector('.caddy-down-detail')?.textContent).toBe(
      fill(text('system.https.caddy_down.detail'), { status: '⁨sudo bobres status⁩', logs: '⁨sudo bobres logs caddy⁩' }),
    )
    // Caddy's reason once, in English, not once per address
    expect(line.querySelector('.caddy-down-why')?.textContent).toBe(refused)
    expect(document.querySelectorAll('.address-error')).toHaveLength(0)
    // every address says it was not reachable, not that it has no certificate
    const states = [...document.querySelectorAll('.addresses .address-state')]
    expect(states).toHaveLength(2)
    for (const el of states) {
      expect(el.textContent).toBe(text('system.https.state.unreachable'))
      expect(el.classList.contains('p-tag-danger')).toBe(true)
    }
    expect(document.querySelector('.addresses .expires')).toBeNull()
  })

  it('does not say the gateway is down while an address answered, or when the core says nothing of it', async () => {
    // one address answered (it serves no certificate yet): Caddy is up, and the address's own error shows
    const mixed = view({
      status: {
        checked_at: now(),
        addresses: [
          address(),
          address({ address: 'pay.example.com', role: 'zarinpal', reachable: false, served: false, valid: false, trusted: null, cert: null, error: 'timed out' }),
        ],
        agent: agent(),
      },
    })
    await boot({ tls: mixed })
    await found('.addresses')
    expect(document.querySelector('.caddy-down')).toBeNull()
    const [ok, lost] = [...document.querySelectorAll('.addresses .address')] as HTMLElement[]
    expect(ok.querySelector('.address-state')?.textContent).toBe(text('system.https.state.ok'))
    expect(lost.querySelector('.address-state')?.textContent).toBe(text('system.https.state.unreachable'))
    expect(lost.querySelector('.address-error')?.textContent).toBe('timed out')

    // a core from before the field: nothing is claimed
    const { reachable: _omit, ...older } = address({ served: false, valid: false, cert: null, error: 'no certificate yet' })
    await boot({ tls: view({ status: { checked_at: now(), addresses: [older as TlsAddress], agent: agent() } }) })
    await found('.addresses')
    expect(document.querySelector('.caddy-down')).toBeNull()
    expect(document.querySelector('.address-state')?.textContent).toBe(text('system.https.state.none'))
    expect(document.querySelector('.address-error')?.textContent).toBe('no certificate yet')

    // nothing checked yet: no line either
    await boot({ tls: view({ status: { checked_at: null, addresses: [], agent: agent() } }) })
    await found('.no-addresses')
    expect(document.querySelector('.caddy-down')).toBeNull()
  })

  it('says when the saved setting is not in use, and why', async () => {
    const tls = view({
      state: { mode: 'manual', ip: '', cert: cert({ subject: 'shop.example.com', not_after: now() - DAY }), updated_at: now(), domain: 'shop.example.com', zarinpal_host: '', manual_allowed: true },
      effective: { mode: 'auto', ip: '' },
      problem: { code: 'cert_expired', message: 'the certificate expired on 2026-10-12' },
    })
    await boot({ tls })
    const box = await found('.https-problem')
    expect(box.textContent).toContain(fill(text('system.https.problem.manual_unused'), { reason: text('system.https.code.cert_expired') }))
    expect(box.querySelector('.detail')?.textContent).toBe('the certificate expired on 2026-10-12')
    expect(document.querySelector('.saved-mode')?.textContent).toContain(text('system.https.mode.manual'))
    expect(document.querySelector('.serving')?.textContent).toContain(text('system.https.mode.auto'))
    expect(document.querySelector('.saved-cert')?.textContent).toContain('shop.example.com')
    expect(document.querySelector('.saved-cert')?.textContent).toContain(text('system.https.addresses.expired'))
  })

  it('says when there is no applier, and when the state cannot be read', async () => {
    await boot({ tls: view({ status: { checked_at: now(), addresses: [address()], agent: null } }) })
    const state = await found('.agent-state')
    expect(state.textContent).toContain('sudo bobres update')
    expect(state.classList.contains('p-tag-warn')).toBe(true)

    let fail = true
    await boot({
      answer: (url) => {
        if (url === '/api/v1/system/tls' && fail) {
          fail = false
          return json(503, { error: 'unavailable', message: 'down' })
        }
      },
    })
    const err = await found('.load-error')
    expect(err.textContent).toContain(text('system.https.load_error'))
    expect(err.textContent).toContain(text('error.unavailable'))
    btn('button.refresh').click()
    await found('.addresses')
    expect(document.querySelector('.load-error')).toBeNull()
  })

  it('lets the owner apply the automatic mode, and waits until the certificate is in use', async () => {
    let put = false
    const pending = view({ status: { checked_at: now(), addresses: [address({ served: false, valid: false, cert: null })], agent: agent() } })
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner', tls: view({ state: { ...view().state, mode: 'manual', cert: cert() }, effective: { mode: 'manual', ip: '' } }),
      answer: (url, init) => {
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          put = true
          return putOk()
        }
        if (url === '/api/v1/system/tls' && put) return json(200, pending)
      },
    })
    await found('.https-form')
    // starts from the saved mode
    expect(input('.mode-manual input[type="radio"]').checked).toBe(true)
    pick('auto')
    await vi.waitFor(() => expect(input('.mode-auto input[type="radio"]').checked).toBe(true))
    expect(document.querySelector('.warn-auto')).not.toBeNull()
    btn('.apply').click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(1))
    expect(sent(calls, 'PUT')).toEqual([{ mode: 'auto' }])
    // then reads the state again while nothing is served yet
    const waiting = await found('.waiting')
    expect(waiting.textContent).toContain(text('system.https.wait.title'))
    expect(waiting.textContent).toContain(fill(text('system.https.wait.detail'), { every: '۳', max: '۹۰' }))
    expect(count(calls, 'GET /api/v1/system/tls')).toBe(2)
    expect(document.querySelector('.ready')).toBeNull()
  })

  it('says the certificate is in use once the state shows it', async () => {
    let put = false
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          put = true
          return putOk()
        }
        // the first read after the change: the certificate is in use
        if (url === '/api/v1/system/tls' && put) return json(200, view())
      },
    })
    await found('.https-form')
    btn('.apply').click()
    const ready = await found('.ready')
    expect(ready.textContent?.trim()).toBe(text('system.https.wait.ready'))
    expect(document.querySelector('.waiting')).toBeNull()
    expect(puts(calls)).toHaveLength(1)
  })

  const ipView = view({
    state: { ...view().state, mode: 'ip', ip: '1.1.1.1' }, effective: { mode: 'ip', ip: '1.1.1.1' },
    status: { checked_at: now(), addresses: [address(), address({ address: '1.1.1.1', role: 'ip' })], agent: agent() },
    public_url: 'https://1.1.1.1',
  })

  it('sends the server IP address after the owner confirms an address the bot did not detect', async () => {
    let put = false
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (url === '/api/v1/system/tls/detect-ip') return json(200, { ip: '8.8.8.8' })
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          put = true
          return putOk({ state: ipView.state, effective: ipView.effective, public_url: ipView.public_url })
        }
        if (url === '/api/v1/system/tls' && put) return json(200, ipView)
      },
    })
    await found('.https-form')
    pick('ip')
    await found('.ip-input')
    // the warnings of this mode are on the screen before anything is sent
    const warn = document.querySelector('.warn-ip')!
    expect(warn.textContent).toContain(text('system.https.warn.ip_links'))
    expect(warn.textContent).toContain(text('system.https.warn.ip_scan'))
    expect(warn.textContent).toContain(text('system.https.warn.ip_ct'))
    expect(warn.textContent).toContain(text('system.https.warn.ip_ports'))
    // Apply with nothing typed: the field says what is wrong, nothing is sent
    btn('.apply').click()
    expect((await found('.ip-problem')).textContent).toBe(text('system.https.code.ip_required'))
    expect(puts(calls)).toHaveLength(0)
    // a private address is refused before it is sent
    type('.ip-input', '10.0.0.4')
    await vi.waitFor(() => expect(document.querySelector('.ip-problem')?.textContent).toBe(text('system.https.code.ip_not_public')))

    // the bot detects this server's address and it fills the field
    btn('.detect').click()
    await vi.waitFor(() => expect(input('.ip-input').value).toBe('8.8.8.8'))
    expect(calls).toContain('POST /api/v1/system/tls/detect-ip')
    expect(document.querySelector('.detected')?.textContent).toContain('8.8.8.8')
    expect(document.querySelector('.confirm')).toBeNull()
    expect(document.querySelector('.ip-problem')).toBeNull()

    // another address: the owner must confirm it is this server's
    type('.ip-input', '1.1.1.1')
    const confirm = await found('.confirm')
    expect(confirm.querySelector('.confirm-why')?.textContent).toContain('8.8.8.8')
    expect(confirm.querySelector('.confirm-text')?.textContent).toContain('1.1.1.1')
    expect(btn('.apply').disabled).toBe(true)
    input('.confirm-box input').click()
    await vi.waitFor(() => expect(btn('.apply').disabled).toBe(false))
    btn('.apply').click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(1))
    expect(sent(calls, 'PUT')).toEqual([{ mode: 'ip', ip: '1.1.1.1', confirm_ip: true }])
    await found('.ready')
    // the tick does not stay for the next change
    expect(document.querySelector('.confirm')).not.toBeNull()
    expect(input('.confirm-box input').checked).toBe(false)
  })

  it('shows the address the bot saw when core asks for a confirmation', async () => {
    let n = 0
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          if (++n === 1) return json(409, { error: 'confirm', code: 'ip_unconfirmed', detected_ip: '8.8.8.8', message: 'confirm the address' })
          return putOk({ state: ipView.state, effective: ipView.effective, public_url: ipView.public_url })
        }
        if (url === '/api/v1/system/tls' && n > 1) return json(200, ipView)
      },
    })
    await found('.https-form')
    pick('ip')
    await found('.ip-input')
    type('.ip-input', '1.1.1.1')
    await vi.waitFor(() => expect(input('.ip-input').value).toBe('1.1.1.1'))
    // nothing detected here yet: no tick asked for before core says so
    expect(document.querySelector('.confirm')).toBeNull()
    btn('.apply').click()
    const err = await found('.apply-error')
    expect(err.textContent).toContain(fill(text('system.https.code.ip_unconfirmed_other'), { detected: '\u20688.8.8.8\u2069', ip: '\u20681.1.1.1\u2069' }))
    expect(sent(calls, 'PUT')[0]).toEqual({ mode: 'ip', ip: '1.1.1.1', confirm_ip: false })
    const confirm = await found('.confirm')
    expect(confirm.querySelector('.confirm-why')?.textContent).toContain('8.8.8.8')
    expect(btn('.apply').disabled).toBe(true)
    input('.confirm-box input').click()
    await vi.waitFor(() => expect(btn('.apply').disabled).toBe(false))
    btn('.apply').click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(2))
    expect(sent(calls, 'PUT')[1]).toEqual({ mode: 'ip', ip: '1.1.1.1', confirm_ip: true })
    await found('.ready')
  })

  it('asks for a confirmation when core could not detect the address at all', async () => {
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => (init?.method === 'PUT' && url === '/api/v1/system/tls'
        ? json(409, { error: 'confirm', code: 'ip_unconfirmed', detected_ip: null, message: 'confirm the address' })
        : undefined),
    })
    await found('.https-form')
    pick('ip')
    await found('.ip-input')
    type('.ip-input', '1.1.1.1')
    await vi.waitFor(() => expect(input('.ip-input').value).toBe('1.1.1.1'))
    btn('.apply').click()
    const err = await found('.apply-error')
    expect(err.textContent).toContain(fill(text('system.https.code.ip_unconfirmed'), { ip: '\u20681.1.1.1\u2069' }))
    const confirm = await found('.confirm')
    expect(confirm.querySelector('.confirm-why')?.textContent).toBe(text('system.https.ip.unknown'))
    expect(btn('.apply').disabled).toBe(true)
    expect(puts(calls)).toHaveLength(1)
  })

  it('turns your own certificate off when core has no master key, with the reason', async () => {
    const { calls } = await boot({ perms: ownerPerms, role: 'owner', tls: view({ state: { ...view().state, manual_allowed: false } }) })
    await found('.https-form')
    const radio = input('.mode-manual input[type="radio"]')
    expect(radio.disabled).toBe(true)
    expect(document.querySelector('.mode-manual .manual-off')?.textContent).toBe(text('system.https.form.manual_off'))
    radio.click()
    expect(document.querySelector('.manual-fields')).toBeNull()
    expect(input('.mode-auto input[type="radio"]').checked).toBe(true)
    expect(puts(calls)).toHaveLength(0)
  })

  it('sends a certificate and a key, shows the notes, and never keeps the key', async () => {
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          return putOk(
            { state: { ...view().state, mode: 'manual', cert: cert() } },
            { warnings: [{ code: 'chain_incomplete', message: 'the chain has no intermediate certificates' }, { code: 'brand_new', message: 'a newer warning' }] },
          )
        }
      },
    })
    await found('.https-form')
    pick('manual')
    await found('.cert-input')
    expect(document.querySelector('.warn-manual')?.textContent).toContain(text('system.https.warn.manual_lockout').replace('{command}', '⁨sudo bobres ssl auto⁩'))
    expect(document.querySelector('.warn-manual')?.textContent).toContain(text('system.https.warn.manual_webhooks'))
    // the key field hides nothing from the owner's own paste, but is not offered to a password manager
    expect(input('.key-input').getAttribute('autocomplete')).toBe('off')
    // empty: both fields say so, nothing is sent
    btn('.apply').click()
    await vi.waitFor(() => expect(document.querySelector('.cert-problem')?.textContent).toBe(text('system.https.code.cert_missing')))
    expect(document.querySelector('.key-problem')?.textContent).toBe(text('system.https.code.key_missing'))
    expect(puts(calls)).toHaveLength(0)
    // a key in the certificate field
    type('.cert-input', CERT + KEY)
    await vi.waitFor(() => expect(document.querySelector('.cert-problem')?.textContent).toBe(text('system.https.code.cert_has_key')))
    // the real pair
    type('.cert-input', CERT)
    type('.key-input', KEY)
    await vi.waitFor(() => expect(document.querySelector('.cert-problem')).toBeNull())
    expect(document.querySelector('.key-problem')).toBeNull()
    btn('.apply').click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(1))
    expect(sent(calls, 'PUT')).toEqual([{ mode: 'manual', cert: CERT, key: KEY }])
    // PUT's notes in the page's language, core's words for one it does not know
    const notes = await found('.accepted-notes')
    expect([...notes.querySelectorAll('li')].map((l) => l.textContent)).toEqual([text('system.https.warning.chain_incomplete'), 'a newer warning'])
    // the key is gone from the form, and from the page
    expect((document.querySelector('.key-input') as HTMLTextAreaElement).value).toBe('')
  })

  it("shows Caddy's words when it refuses, and still clears the key", async () => {
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          return json(409, { error: 'refused', code: 'caddy_refused', message: 'loading new config: tls: private key does not match public key' })
        }
      },
    })
    await found('.https-form')
    pick('manual')
    await found('.cert-input')
    type('.cert-input', CERT)
    type('.key-input', KEY)
    await vi.waitFor(() => expect(input('.key-input').value).toBe(KEY))
    btn('.apply').click()
    const err = await found('.apply-error')
    expect(err.textContent).toContain(text('system.https.code.caddy_refused'))
    expect(err.querySelector('.detail')?.textContent).toBe('loading new config: tls: private key does not match public key')
    expect(puts(calls)).toHaveLength(1)
    // nothing was changed: no waiting
    expect(document.querySelector('.waiting')).toBeNull()
    expect((document.querySelector('.key-input') as HTMLTextAreaElement).value).toBe('')
    // the emptied field is not flagged: the answer above is the news
    expect(document.querySelector('.key-problem')).toBeNull()
    // the certificate stays for a second try
    expect(input('.cert-input').value).toBe(CERT)
    // editing the form clears the error
    type('.cert-input', CERT + CERT)
    await vi.waitFor(() => expect(document.querySelector('.apply-error')).toBeNull())
  })

  it('shows a field error from core next to its field', async () => {
    await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          return json(400, { error: 'invalid', code: 'key_mismatch', field: 'key', message: 'the private key does not belong to the certificate' })
        }
      },
    })
    await found('.https-form')
    pick('manual')
    await found('.cert-input')
    type('.cert-input', CERT)
    type('.key-input', KEY)
    await vi.waitFor(() => expect(input('.key-input').value).toBe(KEY))
    btn('.apply').click()
    await vi.waitFor(() => expect(document.querySelector('.key-problem')?.textContent).toBe(text('system.https.code.key_mismatch')))
    expect(document.querySelector('.cert-problem')).toBeNull()
  })

  it('says when the applier is not set up, when it is slow, and when changes come too fast', async () => {
    const answers = [
      json(503, { error: 'unavailable', code: 'agent_unavailable', message: 'no tls-agent' }),
      json(503, { error: 'unavailable', code: 'agent_timeout', message: 'the applier did not answer' }),
      json(429, { error: 'rate_limited', message: 'at most 10 changes a minute' }),
    ]
    let n = 0
    await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => (init?.method === 'PUT' && url === '/api/v1/system/tls' ? answers[n++].clone() : undefined),
    })
    await found('.https-form')
    for (const [i, key] of ['agent_unavailable', 'agent_timeout', 'rate_limited'].entries()) {
      btn('.apply').click()
      await vi.waitFor(() => expect(n).toBe(i + 1))
      await vi.waitFor(() => expect(document.querySelector('.apply-error')?.textContent).toContain(fill(text('system.https.code.' + key), {
        update: '⁨sudo bobres update⁩', status: '⁨sudo bobres status⁩', start: '⁨sudo bobres start⁩',
      })))
      // editing the form clears the answer
      pick('ip')
      pick('auto')
      await vi.waitFor(() => expect(document.querySelector('.apply-error')).toBeNull())
    }
  })

  it('tells to try again in a moment when another change is being applied', async () => {
    let n = 0
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (init?.method !== 'PUT' || url !== '/api/v1/system/tls') return undefined
        return n++ === 0
          ? json(503, { error: 'unavailable', code: 'busy', message: 'another HTTPS change is being applied: try again in a moment' })
          : putOk()
      },
    })
    await found('.https-form')
    btn('.apply').click()
    await vi.waitFor(() => expect(n).toBe(1))
    const err = await found('.apply-error')
    expect(err.textContent).toContain(text('system.https.code.busy'))
    // the text says it all: no English words under it, no field marked
    expect(err.querySelector('.detail')).toBeNull()
    expect(document.querySelector('.problem')).toBeNull()
    expect(document.querySelector('.waiting')).toBeNull()
    expect(btn('.apply').disabled).toBe(false)
    // and trying again works
    btn('.apply').click()
    await vi.waitFor(() => expect(n).toBe(2))
    await vi.waitFor(() => expect(document.querySelector('.apply-error')).toBeNull())
    expect(puts(calls)).toHaveLength(2)
  })

  it('tells that Caddy is not running, and that the setting is loaded when it starts', async () => {
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => (init?.method === 'PUT' && url === '/api/v1/system/tls'
        ? putOk({}, { pending: true })
        : undefined),
    })
    await found('.https-form')
    btn('.apply').click()
    const pending = await found('.pending')
    expect(pending.textContent?.trim()).toBe(text('system.https.form.pending'))
    // nothing will be issued until it starts: no polling, only the one read that brings the live status back
    expect(document.querySelector('.waiting')).toBeNull()
    await vi.waitFor(() => expect(count(calls, 'GET /api/v1/system/tls')).toBe(2))
  })

  it('keeps the live status on screen after a change made while Caddy is not running', async () => {
    const refused = 'dial tcp 172.18.0.5:443: connect: connection refused'
    const down = (a: Partial<TlsAddress>) => address({ reachable: false, served: false, valid: false, trusted: null, cert: null, error: refused, ...a })
    const live = view({ status: { checked_at: now(), addresses: [down({}), down({ address: '8.8.8.8', role: 'ip' })], agent: agent() } })
    let put = false
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner', tls: live,
      answer: (url, init) => {
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          put = true
          return putOk({}, { pending: true })
        }
        if (url === '/api/v1/system/tls' && put) return json(200, live)
      },
    })
    await found('.caddy-down')
    expect(document.querySelectorAll('.addresses .address')).toHaveLength(2)
    btn('.apply').click()
    await found('.pending')
    // PUT's answer has no addresses; the read after it puts them back
    await vi.waitFor(() => expect(count(calls, 'GET /api/v1/system/tls')).toBe(2))
    await vi.waitFor(() => expect(document.querySelectorAll('.addresses .address')).toHaveLength(2))
    expect(document.querySelector('.caddy-down')).not.toBeNull()
    expect(document.querySelector('.pending')).not.toBeNull()
    expect(document.querySelector('.waiting')).toBeNull()
    // and only the one read: nothing to wait for while Caddy is down
    await new Promise((r) => setTimeout(r, 50))
    expect(count(calls, 'GET /api/v1/system/tls')).toBe(2)
  })

  // The confirmation of a server address.

  it('asks again for the tick when the address changes after it was ticked', async () => {
    let put = false
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (url === '/api/v1/system/tls/detect-ip') return json(200, { ip: '8.8.8.8' })
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          put = true
          return putOk({ state: ipView.state, effective: ipView.effective, public_url: ipView.public_url })
        }
        if (url === '/api/v1/system/tls' && put) return json(200, ipView)
      },
    })
    await found('.https-form')
    pick('ip')
    await found('.ip-input')
    btn('.detect').click()
    await vi.waitFor(() => expect(input('.ip-input').value).toBe('8.8.8.8'))
    // the owner means 1.1.1.1 and ticks that it is this server's
    type('.ip-input', '1.1.1.1')
    await vi.waitFor(() => expect(document.querySelector('.confirm-text')?.textContent).toContain('1.1.1.1'))
    input('.confirm-box input').click()
    await vi.waitFor(() => expect(btn('.apply').disabled).toBe(false))
    // then fixes a typo: the tick was for the address before it
    type('.ip-input', '1.1.1.2')
    await vi.waitFor(() => expect(document.querySelector('.confirm-text')?.textContent).toContain('1.1.1.2'))
    expect(input('.confirm-box input').checked).toBe(false)
    expect(btn('.apply').disabled).toBe(true)
    btn('.apply').click()
    expect(puts(calls)).toHaveLength(0)
    // the new address is confirmed on its own
    input('.confirm-box input').click()
    await vi.waitFor(() => expect(btn('.apply').disabled).toBe(false))
    btn('.apply').click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(1))
    expect(sent(calls, 'PUT')).toEqual([{ mode: 'ip', ip: '1.1.1.2', confirm_ip: true }])
  })

  it('does not end in a loop when the address core remembered is gone by the time it is asked', async () => {
    // GET still shows a detection from earlier, and the owner types that very address; core
    // trusts a detection for ten minutes only, tries again, fails and asks to confirm with none
    let n = 0
    const { calls } = await boot({
      perms: ownerPerms, role: 'owner', tls: view({ detected_ip: '8.8.8.8' }),
      answer: (url, init) => {
        if (init?.method === 'PUT' && url === '/api/v1/system/tls') {
          if (++n === 1) return json(409, { error: 'confirm', code: 'ip_unconfirmed', detected_ip: null, message: 'confirm the address' })
          return putOk({ state: ipView.state, effective: ipView.effective, public_url: ipView.public_url })
        }
        if (url === '/api/v1/system/tls' && n > 1) return json(200, ipView)
      },
    })
    await found('.https-form')
    pick('ip')
    await found('.ip-input')
    type('.ip-input', '8.8.8.8')
    await vi.waitFor(() => expect(document.querySelector('.detected')?.textContent).toContain('8.8.8.8'))
    expect(document.querySelector('.confirm')).toBeNull()
    btn('.apply').click()
    await vi.waitFor(() => expect(n).toBe(1))
    expect(sent(calls, 'PUT')[0]).toEqual({ mode: 'ip', ip: '8.8.8.8', confirm_ip: false })
    // the question is on the screen, with the words for "none detected", and Apply waits for the tick
    const confirm = await found('.confirm')
    expect(confirm.querySelector('.confirm-why')?.textContent).toBe(text('system.https.ip.unknown'))
    expect(document.querySelector('.detected')).toBeNull()
    expect(btn('.apply').disabled).toBe(true)
    input('.confirm-box input').click()
    await vi.waitFor(() => expect(btn('.apply').disabled).toBe(false))
    btn('.apply').click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(2))
    expect(sent(calls, 'PUT')[1]).toEqual({ mode: 'ip', ip: '8.8.8.8', confirm_ip: true })
    await found('.ready')
  })

  // The certificate and the key, from files.

  const fileInput = (which: 'cert' | 'key') => document.querySelectorAll<HTMLInputElement>('input.file')[which === 'cert' ? 0 : 1]
  const choose = (which: 'cert' | 'key', file: File) => {
    const el = fileInput(which)
    Object.defineProperty(el, 'files', { value: [file], configurable: true })
    el.dispatchEvent(new Event('change'))
  }
  const SECRET = pem('PRIVATE KEY', 'TheKeyBodyThatMustNeverBeShown')
  async function manual(answer?: Answer) {
    const booted = await boot({ perms: ownerPerms, role: 'owner', answer })
    await found('.https-form')
    pick('manual')
    await found('.cert-input')
    return booted
  }

  it('reads a certificate and a key from files, and never shows the key', async () => {
    const { calls } = await manual((url, init) => (init?.method === 'PUT' && url === '/api/v1/system/tls' ? putOk({ state: { ...view().state, mode: 'manual', cert: cert() } }) : undefined))
    // the certificate goes into its field, trimmed and ended with a new line
    choose('cert', new File(['  ' + CERT + '\n\n'], 'chain.pem'))
    await vi.waitFor(() => expect(input('.cert-input').value).toBe(CERT.trim() + '\n'))
    expect(document.querySelector('.cert-problem')).toBeNull()
    // the key is read, named, and not put on the page
    choose('key', new File([SECRET], 'server.key'))
    const loaded = await found('.key-loaded')
    expect(loaded.querySelector('.key-loaded-text')?.textContent).toContain('server.key')
    expect(document.querySelector('.key-input')).toBeNull()
    expect(document.body.innerHTML).not.toContain('TheKeyBodyThatMustNeverBeShown')
    expect(document.body.textContent).not.toContain('TheKeyBodyThatMustNeverBeShown')
    expect([...document.querySelectorAll('textarea')].some((t) => t.value.includes('TheKeyBodyThatMustNeverBeShown'))).toBe(false)
    // the group is named by the key's label, since its field is not there
    expect(loaded.getAttribute('role')).toBe('group')
    expect(document.getElementById(loaded.getAttribute('aria-labelledby')!)?.textContent).toBe(text('system.https.key.label'))
    // what is sent is what was read
    btn('.apply').click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(1))
    expect(sent(calls, 'PUT')).toEqual([{ mode: 'manual', cert: CERT.trim() + '\n', key: SECRET.trim() + '\n' }])
    // and once sent the key is gone from the form, with its file name
    await vi.waitFor(() => expect(document.querySelector('.key-loaded')).toBeNull())
    expect((document.querySelector('.key-input') as HTMLTextAreaElement).value).toBe('')
    expect(document.body.innerHTML).not.toContain('TheKeyBodyThatMustNeverBeShown')
  })

  it('lets go of a key read from a file when asked, and sends nothing of it', async () => {
    const { calls } = await manual()
    type('.cert-input', CERT)
    choose('key', new File([SECRET], 'server.key'))
    await found('.key-loaded')
    btn('.key-remove').click()
    await vi.waitFor(() => expect(document.querySelector('.key-loaded')).toBeNull())
    expect(input('.key-input').value).toBe('')
    expect(document.body.innerHTML).not.toContain('TheKeyBodyThatMustNeverBeShown')
    // no key: the field says so and nothing is sent
    btn('.apply').click()
    await vi.waitFor(() => expect(document.querySelector('.key-problem')?.textContent).toBe(text('system.https.code.key_missing')))
    expect(puts(calls)).toHaveLength(0)
  })

  it('says when a file is too large or cannot be read, and takes the words back when the field is typed in', async () => {
    await manual()
    for (const which of ['cert', 'key'] as const) {
      const good = which === 'cert' ? CERT : KEY
      // larger than 64 KiB
      choose(which, new File(['A'.repeat(PEM_MAX + 1)], 'big.pem'))
      await vi.waitFor(() => expect(document.querySelector(`.${which}-problem`)?.textContent).toBe(text('system.https.file_too_big')))
      expect(document.querySelector('.key-loaded')).toBeNull()
      // the owner pastes the real one instead: the old complaint goes
      type(`.${which}-input`, good)
      await vi.waitFor(() => expect(document.querySelector(`.${which}-problem`)).toBeNull())
      expect(input(`.${which}-input`).value).toBe(good)

      // a file the browser cannot read
      const unreadable = new File(['x'], 'locked.pem')
      Object.assign(unreadable, { text: () => Promise.reject(new Error('NotReadableError')) })
      choose(which, unreadable)
      await vi.waitFor(() => expect(document.querySelector(`.${which}-problem`)?.textContent).toBe(text('system.https.file_unreadable')))
      expect(document.querySelector('.key-loaded')).toBeNull()
      type(`.${which}-input`, good + ' ')
      await vi.waitFor(() => expect(document.querySelector(`.${which}-problem`)).toBeNull())
      // and a good file after a bad one starts clean
      choose(which, new File(['A'.repeat(PEM_MAX + 1)], 'big.pem'))
      await vi.waitFor(() => expect(document.querySelector(`.${which}-problem`)?.textContent).toBe(text('system.https.file_too_big')))
      if (which === 'cert') {
        choose('cert', new File([CERT], 'chain.pem'))
        await vi.waitFor(() => expect(document.querySelector('.cert-problem')).toBeNull())
        expect(input('.cert-input').value).toBe(CERT.trim() + '\n')
      }
    }
  })

  // What a screen reader gets.

  /** the elements an aria-describedby names (each id must be on the page) */
  const describedBy = (selector: string) =>
    (document.querySelector(selector)!.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean).map((id) => {
      const el = document.getElementById(id)
      expect(el, `#${id} named by ${selector}`).not.toBeNull()
      return el!
    })
  /** the live region around a message that was not there before */
  const liveOf = (message: Element) => message.closest('[aria-live]')

  it('ties the address messages to the field and announces them from a region that is already there', async () => {
    await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url) => (url === '/api/v1/system/tls/detect-ip' ? json(200, { ip: '8.8.8.8' }) : undefined),
    })
    await found('.https-form')
    pick('ip')
    await found('.ip-input')
    // before anything is wrong: the hint, and a polite region with nothing in it
    expect(describedBy('.ip-input').map((e) => e.textContent)).toEqual([text('system.https.ip.hint')])
    expect(input('.ip-input').getAttribute('aria-invalid')).toBe('false')
    const region = document.querySelector('.ip-fields .live')!
    expect(region.getAttribute('aria-live')).toBe('polite')
    expect(region.textContent).toBe('')

    type('.ip-input', '10.0.0.4')
    const problem = await found('.ip-problem')
    // the same region, now with the message, and the message named by the field
    expect(liveOf(problem)).toBe(region)
    expect(problem.getAttribute('aria-live')).toBeNull()
    expect(describedBy('.ip-input')).toContain(problem)
    expect(describedBy('.ip-input')[0].textContent).toBe(text('system.https.ip.hint'))
    expect(input('.ip-input').getAttribute('aria-invalid')).toBe('true')

    // the bot's answer is a message too
    btn('.detect').click()
    const note = await found('.detected')
    expect(liveOf(note)).toBe(region)
    expect(describedBy('.ip-input')).toContain(note)
    expect(document.querySelector('.ip-problem')).toBeNull()
    expect(input('.ip-input').getAttribute('aria-invalid')).toBe('false')
    expect(document.querySelector('.ip-fields .live')).toBe(region)
  })

  it('ties the failed detection to the address field', async () => {
    await boot({
      perms: ownerPerms, role: 'owner',
      answer: (url) => (url === '/api/v1/system/tls/detect-ip' ? json(502, { error: 'unavailable', code: 'detect_failed', message: 'the bot could not detect this server\'s public IP address' }) : undefined),
    })
    await found('.https-form')
    pick('ip')
    await found('.ip-input')
    const region = document.querySelector('.ip-fields .live')!
    btn('.detect').click()
    const err = await found('.detect-error')
    expect(err.textContent).toBe(text('system.https.code.detect_failed'))
    expect(liveOf(err)).toBe(region)
    expect(describedBy('.ip-input')).toContain(err)
  })

  it('ties the certificate and key messages to their fields', async () => {
    await manual()
    for (const which of ['cert', 'key'] as const) {
      const field = `.${which}-input`
      const help = text(`system.https.${which}.hint`)
      expect(describedBy(field).map((e) => e.textContent)).toEqual([help])
      const region = document.querySelector(field)!.closest('.app-field')!.querySelector('.live')!
      expect(region.getAttribute('aria-live')).toBe('polite')
      expect(region.textContent).toBe('')

      type(field, 'hello')
      const problem = await found(`.${which}-problem`)
      expect(liveOf(problem)).toBe(region)
      expect(problem.getAttribute('aria-live')).toBeNull()
      expect(describedBy(field).map((e) => e.textContent)).toEqual([help, problem.textContent])
      expect(input(field).getAttribute('aria-invalid')).toBe('true')

      // a file that is too large speaks from the same place
      choose(which, new File(['A'.repeat(PEM_MAX + 1)], 'big.pem'))
      await vi.waitFor(() => expect(document.querySelector(`.${which}-problem`)?.textContent).toBe(text('system.https.file_too_big')))
      expect(describedBy(field).map((e) => e.textContent)).toEqual([help, text('system.https.file_too_big')])
      expect(document.querySelector(field)!.closest('.app-field')!.querySelector('.live')).toBe(region)
    }
  })

  // Where dashboard links point.

  const ipSaved = (public_url: string) => view({
    state: { ...view().state, mode: 'ip', ip: '8.8.8.8' }, effective: { mode: 'ip', ip: '8.8.8.8' }, public_url,
    status: { checked_at: now(), addresses: [address(), address({ address: '8.8.8.8', role: 'ip' })], agent: agent() },
  })

  it('says links move to the IP address only while they follow the domain', async () => {
    await boot({ perms: ownerPerms, role: 'owner', tls: ipSaved('https://shop.example.com') })
    await found('.https-form')
    expect(document.querySelector('.links-domain')?.textContent).toBe(text('system.https.status.links_domain'))
    expect(document.querySelector('.links-override')).toBeNull()
    expect(document.querySelector('.warn-ip .warn-links')?.textContent).toBe(text('system.https.warn.ip_links'))
    expect(document.querySelector('.warn-links-override')).toBeNull()
  })

  it('says so when BOBRES_PUBLIC_URL decides where links point, and not that they will move', async () => {
    await boot({ perms: ownerPerms, role: 'owner', tls: ipSaved('https://panel.example.org') })
    await found('.https-form')
    expect(document.querySelector('.public-url')?.textContent).toContain('https://panel.example.org')
    expect(document.querySelector('.links-override')?.textContent).toBe(text('system.https.status.links_override'))
    expect(document.querySelector('.links-domain')).toBeNull()
    expect(document.querySelector('.warn-ip .warn-links-override')?.textContent).toBe(text('system.https.warn.ip_links_override'))
    expect(document.querySelector('.warn-ip .warn-links')).toBeNull()
    expect(document.querySelector('.warn-ip')?.textContent).not.toContain(text('system.https.warn.ip_links'))
    // the other warnings of the mode stay
    expect(document.querySelector('.warn-ip')?.textContent).toContain(text('system.https.warn.ip_scan'))
  })

  it('has no note about links to move once they are on the IP address, or from another mode', async () => {
    await boot({ perms: ownerPerms, role: 'owner', tls: ipSaved('https://8.8.8.8') })
    await found('.https-form')
    expect(document.querySelector('.links-domain')).toBeNull()
    expect(document.querySelector('.links-override')).toBeNull()

    // another mode with the owner's address: nothing about the IP address to say in the status, the form's warning says it will not move
    await boot({ perms: ownerPerms, role: 'owner', tls: view({ public_url: 'https://panel.example.org' }) })
    await found('.https-form')
    expect(document.querySelector('.links-domain')).toBeNull()
    expect(document.querySelector('.links-override')).toBeNull()
    pick('ip')
    await found('.warn-ip')
    expect(document.querySelector('.warn-ip .warn-links-override')).not.toBeNull()
  })

  // Layout: jsdom lays nothing out, so the rules are read from the source. The
  // global .app-form label (a field's label over its input) beats a rule that
  // does not say its own direction.
  it('sets the direction of the mode cards and the confirmation, with logical properties only', () => {
    const src = Object.values(import.meta.glob('../components/system/HttpsTab.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>)[0]
    const style = src.slice(src.indexOf('<style'))
    const rule = (selector: string) => new RegExp(`\\n${selector.replace('.', '\\.')} \\{([^}]*)\\}`).exec(style)?.[1] ?? ''
    expect(rule('.card')).toMatch(/display:\s*flex/)
    expect(rule('.card')).toMatch(/flex-direction:\s*row/)
    expect(rule('.confirm')).toMatch(/display:\s*flex/)
    expect(rule('.confirm')).toMatch(/flex-direction:\s*row/)
    expect(style).not.toMatch(/\b(margin|padding|border)-(left|right|top|bottom)\b/)
    expect(style).not.toMatch(/(^|[\s;{])(left|right)\s*:/)
    expect(style).not.toMatch(/text-align:\s*(left|right)/)
  })
})
