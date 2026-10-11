import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import { absent, checkList, logAttrs, logLevel, logsReason, mismatched, newestFirst, serviceState, sortedServices, UPDATE_COMMAND, updateReason, updateState } from '../system'
import {
  bindingOf,
  bindings,
  canSave,
  entitlementRows,
  graceEndsAt,
  isValid,
  limitState,
  normalizeDomain,
  outsideLicense,
  poweredBy,
  reasonKey,
  stateOf,
  timeLeft,
  TOKEN_MAX,
  tokenOf,
  tokenProblem,
  warnings,
} from '../license'
import type { LicenseEntitlements, LicenseInfo, LicenseToken, LogLine, ServiceHealth, SystemLogs, SystemReply, UpdateCheck } from '../types'

const DAY = 86_400
const iid = 'aaaaaaaa-1111-4aaa-8aaa-aaaaaaaaaaaa'
const svc = (name: string, over: Partial<ServiceHealth> = {}): ServiceHealth => ({
  name, status: 'ok', version: 'v1.4.0', checks: { db: 'ok' }, detail: '', latency_ms: 4, version_mismatch: false, ...over,
})
// what GET /license answers about a license's grants, with this install's use
const ents = (over: Partial<LicenseEntitlements> = {}): LicenseEntitlements => ({
  white_label: false, resellers: true, crypto_watcher: false,
  max_servers: { limit: 5, used: 3, near: false, full: false },
  max_active_services: { limit: 500, used: 460, near: true, full: false },
  dashboard_staff_seats: { limit: 2, used: 2, near: true, full: true },
  gateways: { licensed: ['zarinpal', 'manual_card'], enabled: ['zarinpal', 'stars'] },
  updates_until: '2027-09-30', ...over,
})
const token = (now: number, over: Partial<LicenseToken> = {}): LicenseToken => ({
  license_id: 'lic_7Q2', customer: 'cust_rose', tier: 'standard', kid: 'lic-2026a',
  issued_at: now - 300 * DAY, expires_at: now + 40 * DAY + 3600, grace_days: 14,
  domain: 'Shop.Example.com.', install_id: '', ...over,
})

describe('system health', () => {
  it('reads each service as working, not ready, down, not installed or not configured', () => {
    expect(serviceState(svc('core'))).toBe('ok')
    expect(serviceState(svc('payments', { status: 'not_ready' }))).toBe('not_ready')
    expect(serviceState(svc('bot', { status: 'down' }))).toBe('down')
    expect(serviceState(svc('backup', { status: 'not_installed' }))).toBe('not_installed')
    expect(serviceState(svc('backup', { status: 'not_configured' }))).toBe('not_configured')
    // a status this page does not know is not taken for working
    expect(serviceState(svc('bot', { status: 'odd' }))).toBe('down')
    expect(serviceState(svc('bot', { status: '' }))).toBe('down')
    expect(absent(svc('backup', { status: 'not_installed' }))).toBe(true)
    expect(absent(svc('backup', { status: 'not_configured' }))).toBe(true)
    expect(absent(svc('bot', { status: 'down' }))).toBe(false)
  })
  it('lists the known services first, in their usual order', () => {
    const list = [svc('backup'), svc('zeta'), svc('bot'), svc('provisioner'), svc('core'), svc('alpha'), svc('payments')]
    expect(sortedServices(list).map((s) => s.name)).toEqual(['core', 'bot', 'payments', 'provisioner', 'backup', 'alpha', 'zeta'])
  })
  it('lists the readiness checks by name', () => {
    expect(checkList(svc('core', { checks: { grpc: 'fail', db: 'ok' } }))).toEqual([{ name: 'db', ok: true }, { name: 'grpc', ok: false }])
    expect(checkList(svc('x', { checks: undefined }))).toEqual([])
    expect(checkList(svc('x', { checks: null }))).toEqual([])
  })
  it('names the services core found on another version, never core or an absent one', () => {
    const reply: SystemReply = {
      version: 'v1.4.0',
      services: [
        svc('payments', { version: '1.3.0', version_mismatch: true }),
        svc('core', { version_mismatch: true }),
        svc('bot', { version: 'v1.3.9', version_mismatch: true }),
        svc('provisioner', { version: '1.4.0' }),
        svc('backup', { status: 'not_installed', version_mismatch: true }),
      ],
    }
    expect(mismatched(reply).map((s) => s.name)).toEqual(['bot', 'payments'])
    expect(mismatched({ services: [svc('bot', { version_mismatch: undefined })] })).toEqual([])
  })
})

describe('update check', () => {
  const upd = (over: Partial<UpdateCheck> = {}): UpdateCheck => ({
    current: '1.5.0', latest: '1.6.0', newer: true, verified: true, checked_at: 1_790_000_000, error: '', ...over,
  })
  it('reads a newer release, up to date, or not known', () => {
    expect(updateState(upd())).toBe('newer')
    expect(updateState(upd({ latest: '1.5.0', newer: false }))).toBe('current')
    expect(updateState(upd({ latest: '', newer: false, error: 'unreachable' }))).toBe('unknown')
    // not checked yet
    expect(updateState(upd({ latest: '', newer: false, checked_at: 0 }))).toBe('unknown')
    // a release read before a later check failed still counts; "up to date" does not
    expect(updateState(upd({ error: 'unreachable' }))).toBe('newer')
    expect(updateState(upd({ latest: '1.5.0', newer: false, error: 'unreachable' }))).toBe('unknown')
    // an older core says nothing
    expect(updateState(undefined)).toBe('unknown')
    expect(updateState(null)).toBe('unknown')
  })
  it('says why the latest release is not known', () => {
    expect(updateReason(upd())).toBeNull()
    expect(updateReason(upd({ latest: '', error: 'unreachable' }))).toBe('unreachable')
    expect(updateReason(upd({ latest: '', error: 'bot unreachable' }))).toBe('bot_unreachable')
    expect(updateReason(upd({ latest: '', error: 'checksums.txt: no version header' }))).toBe('other')
    expect(updateReason(upd({ latest: '', checked_at: 0 }))).toBe('not_checked')
    expect(updateReason(upd({ error: 'unreachable' }))).toBe('unreachable')
    expect(updateReason(undefined)).toBe('unsupported')
    for (const r of ['unreachable', 'bot_unreachable', 'not_checked', 'unsupported', 'other']) {
      expect((fa.system.updates.reason as Record<string, string>)[r]).toBeTruthy()
    }
    expect(UPDATE_COMMAND).toBe('bobres update')
  })
})

describe('license', () => {
  const now = 1_790_000_000
  it('reads its state, and which states are a verified license', () => {
    expect(stateOf('active')).toBe('active')
    expect(stateOf('grace')).toBe('grace')
    expect(stateOf('expired')).toBe('expired')
    expect(stateOf('missing')).toBe('missing')
    expect(stateOf('invalid')).toBe('invalid')
    expect(stateOf('revoked')).toBe('invalid')
    expect(stateOf('')).toBe('missing')
    expect(stateOf(undefined)).toBe('missing')
    expect(['active', 'grace', 'expired'].every(isValid)).toBe(true)
    expect(['missing', 'invalid', 'odd', ''].some(isValid)).toBe(false)
  })
  it('shows "Powered by BOBRES" only under a valid license without white label', () => {
    // core says so
    expect(poweredBy({ state: 'active', white_label: false, powered_by: false })).toBe(false)
    expect(poweredBy({ state: 'missing', white_label: false, powered_by: true })).toBe(true)
    // read here when it does not
    expect(poweredBy({ state: 'active', white_label: false })).toBe(true)
    expect(poweredBy({ state: 'grace', white_label: false })).toBe(true)
    expect(poweredBy({ state: 'expired', white_label: false })).toBe(true)
    expect(poweredBy({ state: 'active', white_label: true })).toBe(false)
    // never on an install without a license of its own
    expect(poweredBy({ state: 'missing', white_label: false })).toBe(false)
    expect(poweredBy({ state: 'invalid', white_label: false })).toBe(false)
    expect(poweredBy(undefined)).toBe(false)
    expect(poweredBy(null)).toBe(false)
  })
  it('explains every reason core gives, and others plainly', () => {
    for (const r of ['malformed', 'bad_signature', 'unknown_key', 'version', 'domain', 'install_id', 'unbound', 'invalid', 'not_yet_valid', 'expired']) {
      expect(reasonKey(r)).toBe('license.reason.' + r)
      expect((fa.license.reason as Record<string, string>)[r]).toBeTruthy()
    }
    expect(reasonKey('')).toBe('license.reason.other')
    expect(reasonKey('weird')).toBe('license.reason.other')
  })
  it('compares the binding as core does', () => {
    expect(normalizeDomain(' Shop.Example.com. ')).toBe('shop.example.com')
    expect(normalizeDomain('shop.example.com:8443')).toBe('shop.example.com')
    expect(normalizeDomain('[::1]:443')).toBe('::1')
    expect(normalizeDomain('fe80::1')).toBe('fe80::1')
    expect(bindingOf('Shop.Example.com.', 'shop.example.com', true)).toBe('match')
    expect(bindingOf('other.example.com', 'shop.example.com', true)).toBe('mismatch')
    expect(bindingOf('', 'shop.example.com', true)).toBe('unbound')
    expect(bindingOf(iid.toUpperCase(), iid)).toBe('match')
    expect(bindingOf(iid, 'bbbbbbbb-1111-4aaa-8aaa-aaaaaaaaaaaa')).toBe('mismatch')
    expect(bindingOf('', iid)).toBe('unbound')
    // core's comparison wins; the page's own fills in what it leaves out
    const here = { domain: 'shop.example.com', install_id: iid, license: token(now, { install_id: iid }) }
    expect(bindings({ ...here, binding: { domain: 'mismatch', install_id: 'match' } })).toEqual({ domain: 'mismatch', install_id: 'match' })
    expect(bindings({ ...here, binding: null })).toEqual({ domain: 'match', install_id: 'match' })
    expect(bindings({ ...here, binding: { domain: 'odd', install_id: '' } })).toEqual({ domain: 'match', install_id: 'match' })
    expect(bindings({ ...here, license: null })).toBeNull()
  })
  it('counts down to the expiry, then the grace, then since', () => {
    const l = { expires_at: now + 10 * DAY + 60, grace_days: 14 }
    expect(graceEndsAt(l)).toBe(now + 24 * DAY + 60)
    expect(graceEndsAt({ expires_at: now, grace_days: -3 })).toBe(now)
    // core's own end wins
    expect(graceEndsAt({ ...l, grace_ends_at: now + 99 })).toBe(now + 99)
    expect(timeLeft(l, now * 1000)).toEqual({ kind: 'left', days: 10 })
    expect(timeLeft(l, (now + 11 * DAY) * 1000)).toEqual({ kind: 'grace', days: 13 })
    expect(timeLeft(l, (now + 30 * DAY) * 1000)).toEqual({ kind: 'over', days: 5 })
    // no grace: straight from left to over
    expect(timeLeft({ expires_at: now, grace_days: 0 }, (now + 1) * 1000)).toEqual({ kind: 'over', days: 0 })
  })
  it('warns from 90% of a limit', () => {
    expect(limitState(3, -1)).toBe('unlimited')
    expect(limitState(null, -1)).toBe('unlimited')
    expect(limitState(null, 5)).toBe('unknown')
    expect(limitState(undefined, 5)).toBe('unknown')
    // 0 grants none: nothing used is fine, anything used is over
    expect(limitState(0, 0)).toBe('none')
    expect(limitState(1, 0)).toBe('over')
    expect(limitState(3, 5)).toBe('ok')
    expect(limitState(449, 500)).toBe('ok')
    expect(limitState(450, 500)).toBe('near')
    expect(limitState(499, 500)).toBe('near')
    expect(limitState(500, 500)).toBe('full')
    expect(limitState(501, 500)).toBe('over')
    expect(limitState(1, 1)).toBe('full')
  })
  it('names the methods turned on outside the license, the wallet never', () => {
    expect(outsideLicense(['zarinpal', 'Manual_Card'], ['wallet', 'ZarinPal', 'manual_card', 'stars'])).toEqual(['stars'])
    expect(outsideLicense([], ['wallet'])).toEqual([])
    expect(outsideLicense([], ['manual_crypto'])).toEqual(['manual_crypto'])
  })
  it('lists what it grants with what this install uses', () => {
    const rows = entitlementRows({ entitlements: ents({ max_active_services: { limit: -1, used: 460 } }) })
    expect(rows).toEqual([
      { key: 'servers', kind: 'limit', used: 3, limit: 5, state: 'ok' },
      { key: 'active_services', kind: 'limit', used: 460, limit: -1, state: 'unlimited' },
      { key: 'staff_seats', kind: 'limit', used: 2, limit: 2, state: 'full' },
      { key: 'gateways', kind: 'gateways', allowed: ['zarinpal', 'manual_card'], outside: ['stars'] },
      { key: 'resellers', kind: 'feature', on: true },
      { key: 'white_label', kind: 'feature', on: false },
      { key: 'crypto_watcher', kind: 'feature', on: false },
      { key: 'updates_until', kind: 'date', value: '2027-09-30' },
    ])
    expect(warnings(rows)).toBe(2)
    // a count core could not read is unknown, never a warning
    const blind = entitlementRows({ entitlements: ents({ max_servers: { limit: 5, used: null }, updates_until: undefined, gateways: { licensed: [], enabled: [] } }) })
    expect(blind[0]).toEqual({ key: 'servers', kind: 'limit', used: null, limit: 5, state: 'unknown' })
    expect(blind.find((r) => r.key === 'updates_until')).toEqual({ key: 'updates_until', kind: 'date', value: '' })
    expect(warnings(blind)).toBe(2)
    expect(entitlementRows({ entitlements: null })).toEqual([])
    expect(entitlementRows({})).toEqual([])
  })
  it('saves only a token that verifies here and has not expired', () => {
    expect(canSave('active')).toBe(true)
    expect(canSave('grace')).toBe(true)
    expect(canSave('expired')).toBe(false)
    expect(canSave('invalid')).toBe(false)
    expect(canSave('missing')).toBe(false)
    expect(canSave(undefined)).toBe(false)
  })
  it('takes a pasted token without the breaks a chat adds', () => {
    expect(tokenOf('  eyJ2Ijox\n fQ.c2ln\r\n')).toBe('eyJ2IjoxfQ.c2ln')
    expect(tokenProblem('eyJ2IjoxfQ.c2ln-_')).toBe('')
    expect(tokenProblem('eyJ2Ijox\nfQ.c2ln')).toBe('')
    expect(tokenProblem('   ')).toBe('empty')
    expect(tokenProblem('no-dot-here')).toBe('format')
    expect(tokenProblem('a.b.c')).toBe('format')
    expect(tokenProblem('abc+/=.sig')).toBe('format')
    expect(tokenProblem('.sig')).toBe('format')
    expect(tokenProblem('a'.repeat(TOKEN_MAX) + '.b')).toBe('long')
  })
})

describe('logs', () => {
  const line = (time: number, level: string, msg: string, attrs = ''): LogLine => ({ time, level, msg, attrs })
  it('lists the lines newest first, keeping the order within a second', () => {
    const got = newestFirst([line(10, 'WARN', 'a'), line(20, 'ERROR', 'b'), line(20, 'WARN', 'c'), line(15, 'WARN', 'd')])
    expect(got.map((l) => l.msg)).toEqual(['c', 'b', 'd', 'a'])
    expect(newestFirst(null)).toEqual([])
  })
  it('reads the level, the other fields, and why logs could not be read', () => {
    expect(logLevel({ level: 'ERROR' })).toBe('error')
    expect(logLevel({ level: 'error' })).toBe('error')
    expect(logLevel({ level: 'WARN' })).toBe('warn')
    expect(logLevel({ level: 'WARN+2' })).toBe('warn')
    expect(logAttrs({ attrs: '{"user":"[redacted]"}' })).toBe('{"user":"[redacted]"}')
    expect(logAttrs({ attrs: '{}' })).toBe('')
    expect(logAttrs({})).toBe('')
    expect(logsReason('')).toBeNull()
    expect(logsReason(undefined)).toBeNull()
    expect(logsReason('unreachable')).toBe('unreachable')
    expect(logsReason('not configured')).toBe('not_configured')
    expect(logsReason('rpc error: code = Unimplemented')).toBe('other')
  })
})

describe('system page', () => {
  const adminPerms = ['overview.read', 'users.read', 'plans.write', 'settings.write', 'servers.write', 'audit.read', 'system.read']
  const ownerPerms = [...adminPerms, 'gateways.write', 'staff.write', 'system.write', 'license.write']
  const supportPerms = ['overview.read', 'users.read', 'services.read', 'tickets.write']
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const text = (path: string) => (path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) as string | undefined) ?? path
  const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replace('{' + k + '}', v), s)
  const nowSec = () => Math.floor(Date.now() / 1000)

  const systemReply = (): SystemReply => ({
    version: 'v1.4.0',
    version_mismatch: true,
    checked_at: nowSec(),
    services: [
      svc('backup', { status: 'not_installed', version: '', checks: null }),
      svc('provisioner', { status: 'down', version: '', checks: null, detail: 'dial tcp 172.18.0.5:8080: connect: connection refused' }),
      svc('payments', { status: 'not_ready', checks: { db: 'ok', grpc: 'fail' }, version: '1.4.0', detail: 'grpc failed' }),
      svc('bot', { version: 'v1.3.9', checks: { redis: 'ok' }, version_mismatch: true }),
      svc('core', { checks: { db: 'ok', grpc: 'ok' } }),
    ],
    servers: [
      { id: 's1', name: 'de-1', healthy: true, detail: '', clients: 12, enabled: true },
      { id: 's2', name: 'nl-1', healthy: false, detail: 'i/o timeout', clients: 3, enabled: true },
      { id: 's3', name: 'old', healthy: false, detail: 'refused', clients: 0, enabled: false },
    ],
    gateways: [
      { provider: 'zarinpal', enabled: true, healthy: true, ready: true },
      { provider: 'manual_card', enabled: false, healthy: true },
    ],
    updates: { current: '1.4.0', latest: '1.5.0', newer: true, verified: false, checked_at: nowSec() - 3600, error: '' },
  })
  // as core's licenseView answers
  const missing = (): LicenseInfo => ({
    state: 'missing', reason: '', install_id: iid, domain: 'shop.example.com', keys: 1, license: null,
    binding: null, entitlements: null, updated_at: null, updated_by: null,
  })
  const active = (): LicenseInfo => ({
    state: 'active', reason: '', install_id: iid, domain: 'shop.example.com', keys: 1, license: { ...token(nowSec()), grace_ends_at: nowSec() + 54 * DAY },
    binding: { domain: 'match', install_id: 'unbound' }, entitlements: ents(), updated_at: nowSec() - DAY, updated_by: { id: 'u1', username: 'boss' },
  })

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | Promise<Response> | undefined
  interface Boot {
    perms?: string[]
    role?: string
    license?: { state: string; white_label: boolean; powered_by?: boolean } | null
    loggedIn?: boolean
    brand?: Record<string, unknown>
    answer?: Answer
  }
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(path: string, o: Boot = {}) {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    const me = () => ({
      user: { id: 'u1', telegram_id: 1, username: 'boss', role: o.role ?? 'admin', language: 'fa' }, permissions: o.perms ?? adminPerms, csrf: 'c',
      session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
      ...(o.license ? { license: o.license } : {}),
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${raw}`.trim())
        const own = o.answer?.(url, init, raw ? (JSON.parse(raw) as Record<string, unknown>) : {})
        if (own) return own
        const get = (init?.method ?? 'GET') === 'GET'
        if (url === '/api/v1/me') return o.loggedIn === false ? json(401, { error: 'unauthorized' }) : json(200, me())
        if (get && url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null, ...o.brand })
        if (get && url === '/api/v1/system') return json(200, systemReply())
        if (get && url === '/api/v1/license') return json(200, missing())
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

  function type(el: Element | null, value: string) {
    const input = el as HTMLInputElement
    input.value = value
    input.dispatchEvent(new Event('input'))
  }
  const sent = (calls: string[], method: string, path: string) =>
    calls.filter((c) => c.startsWith(`${method} /api/v1${path} `)).map((c) => JSON.parse(c.slice(`${method} /api/v1${path} `.length)) as Record<string, unknown>)
  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })
  const rows = () => [...document.querySelectorAll('.services-health .p-datatable-tbody tr')] as HTMLElement[]
  const btn = (selector: string) => document.querySelector(selector) as HTMLButtonElement

  it('shows every service with its version, health and checks', async () => {
    const { calls } = await boot('/admin/system')
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    expect(calls).toContain('GET /api/v1/system')
    // the license loads only when its tab opens
    expect(calls.some((c) => c.includes('/api/v1/license'))).toBe(false)
    const [core, bot, payments, prov, backup] = rows()
    expect(core.querySelector('.service-name')?.textContent).toBe(text('system.service.core'))
    expect(core.querySelector('.service-state')?.textContent).toBe(text('system.state.ok'))
    expect(core.querySelector('.version')?.textContent).toBe('v1.4.0')
    expect([...core.querySelectorAll('.check.pass')].map((c) => c.textContent)).toEqual([text('system.check.db'), text('system.check.grpc')])
    // the bot runs another release: marked, and named in the warning
    expect(bot.querySelector('.mismatch')?.textContent).toBe(text('system.mismatch'))
    expect(core.querySelector('.mismatch')).toBeNull()
    expect(payments.querySelector('.mismatch')).toBeNull()
    const banner = document.querySelector('.mismatch-banner')?.textContent ?? ''
    expect(banner).toContain(fill(text('system.version_mismatch'), { version: '⁨v1.4.0⁩', list: text('system.service.bot') + ' ⁨v1.3.9⁩' }))
    // answering but not ready: the failed check says which
    expect(payments.querySelector('.service-state')?.textContent).toBe(text('system.state.not_ready'))
    expect(payments.querySelector('.check.fail')?.textContent).toBe(text('system.check.grpc'))
    expect(payments.querySelector('.check.fail')?.getAttribute('aria-label')).toBe(fill(text('system.check_fail'), { name: text('system.check.grpc') }))
    // not answering, with core's reason
    expect(prov.querySelector('.service-state')?.textContent).toBe(text('system.state.down'))
    expect(prov.querySelector('.error')?.textContent).toContain('connection refused')
    expect(prov.querySelector('.version')?.textContent).toBe('—')
    // the backup service is not installed yet
    expect(backup.querySelector('.service-state')?.textContent).toBe(text('system.state.not_installed'))
    expect(backup.textContent).toContain(text('system.not_installed'))
    expect(backup.querySelector('.checks')).toBeNull()

    // the servers and payment methods, as on the overview
    const servers = [...document.querySelectorAll('.servers-summary .server')] as HTMLElement[]
    expect(servers.map((s) => s.querySelector('.server-health')?.textContent)).toEqual([
      text('server.health_state.ok'), text('server.health_state.down'), text('server.health_state.off'),
    ])
    expect(servers[0].querySelector('.clients')?.textContent).toContain('۱۲')
    expect(servers[1].textContent).toContain('i/o timeout')
    // a switched-off server's old failure is not shown
    expect(servers[2].textContent).not.toContain('refused')
    const gateways = [...document.querySelectorAll('.gateways-summary .gateway')] as HTMLElement[]
    expect(gateways.map((g) => g.textContent)).toEqual([
      text('payment.provider.zarinpal') + text('overview.gateway_state.ok'),
      text('payment.provider.manual_card') + text('overview.gateway_state.off'),
    ])
    // admins manage servers, never gateways
    expect(document.querySelector('.servers-link')).not.toBeNull()
    expect(document.querySelector('.gateways-link')).toBeNull()
    expect(document.querySelector('.sidebar')?.textContent).toContain(text('section.system.title'))

    // checks again on demand
    btn('button.refresh').click()
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(2))
  })

  it('says when the status cannot be read, and tries again', async () => {
    let fail = true
    const { calls } = await boot('/admin/system', {
      answer: (url) => {
        if (url === '/api/v1/system' && fail) {
          fail = false
          return json(503, { error: 'unavailable', message: 'down' })
        }
      },
    })
    const err = await found('.load-error')
    expect(err.textContent).toContain(text('system.load_error'))
    expect(err.textContent).toContain(text('error.unavailable'))
    btn('button.refresh').click()
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    expect(document.querySelector('.load-error')).toBeNull()
    expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(2)
  })

  it('keeps the page from support staff', async () => {
    const { router, calls } = await boot('/admin/system', { perms: supportPerms, role: 'support', license: { state: 'missing', white_label: false } })
    expect(router.currentRoute.value.name).toBe('overview')
    expect(document.querySelector('.sidebar')?.textContent).not.toContain(text('section.system.title'))
    expect(calls.some((c) => c.includes('/api/v1/system') || c.includes('/api/v1/license'))).toBe(false)
    // and no license chip for them
    expect(document.querySelector('.license-chip')).toBeNull()
  })

  it('shows a newer release with the command to install it', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { calls, router } = await boot('/admin/system')
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    btn('.tab-updates').click()
    const card = await found('.update-available')
    expect(router.currentRoute.value.query.tab).toBe('updates')
    expect(document.querySelector('.update-state')?.textContent).toBe(text('system.updates.state.newer'))
    expect(document.querySelector('.current-version')?.textContent).toBe('1.4.0')
    expect(document.querySelector('.latest-version')?.textContent).toBe('1.5.0')
    expect(card.querySelector('.available-title')?.textContent).toBe(fill(text('system.updates.available'), { version: '\u20681.5.0\u2069' }))
    expect(card.querySelector('.command')?.textContent).toBe('bobres update')
    expect(document.querySelector('.updates .checked')?.textContent).toContain(text('system.updates.checked_at').split('{')[0])
    // the bot could not check the signature: said, and that the CLI will
    expect(document.querySelector('.unverified')?.textContent?.trim()).toBe(text('system.updates.unverified'))
    expect(document.querySelector('.up-to-date')).toBeNull()
    expect(document.querySelector('.not-checked')).toBeNull()
    btn('button.copy-command').click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('bobres update'))
    // nothing is applied from the page
    expect(calls.some((c) => !c.startsWith('GET '))).toBe(false)
    // checks again on demand
    btn('.updates button.refresh').click()
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(3))
  })

  it('says when this install is up to date', async () => {
    const { calls } = await boot('/admin/system?tab=updates', {
      answer: (url) => (url === '/api/v1/system'
        ? json(200, { version: 'v1.6.0', services: [], updates: { current: '1.6.0', latest: '1.6.0', newer: false, verified: true, checked_at: nowSec(), error: '' } })
        : undefined),
    })
    await found('.up-to-date')
    expect(document.querySelector('.update-state')?.textContent).toBe(text('system.updates.state.current'))
    expect(document.querySelector('.up-to-date')?.textContent).toBe(text('system.updates.up_to_date'))
    expect(document.querySelector('.latest-version')?.textContent).toBe('1.6.0')
    expect(document.querySelector('.update-available')).toBeNull()
    expect(document.querySelector('.unverified')).toBeNull()
    expect(document.querySelector('.not-checked')).toBeNull()
    // the health tab was never opened
    expect(document.querySelector('.services-health')).toBeNull()
    expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(1)
  })

  it('says why updates could not be checked', async () => {
    const reply = (updates: Partial<UpdateCheck> | null) => (url: string) => (url === '/api/v1/system'
      ? json(200, { version: 'v1.5.0', services: [], ...(updates ? { updates: { current: '1.5.0', latest: '', newer: false, verified: false, checked_at: 0, error: '', ...updates } } : {}) })
      : undefined)
    const reason = () => document.querySelector('.not-checked .reason')?.textContent
    // GitHub unreachable
    await boot('/admin/system?tab=updates', { answer: reply({ error: 'unreachable', checked_at: nowSec() }) })
    await found('.not-checked')
    expect(document.querySelector('.update-state')?.textContent).toBe(text('system.updates.state.unknown'))
    expect(document.querySelector('.reason-title')?.textContent).toBe(text('system.updates.could_not_check'))
    expect(reason()).toBe(text('system.updates.reason.unreachable'))
    expect(document.querySelector('.current-version')?.textContent).toBe('1.5.0')
    expect(document.querySelector('.latest-version')?.textContent).toBe('—')
    expect(document.querySelector('.update-available')).toBeNull()
    expect(document.querySelector('.unverified')).toBeNull()
    // core could not ask the bot
    await boot('/admin/system?tab=updates', { answer: reply({ error: 'bot unreachable' }) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.bot_unreachable')))
    expect(document.querySelector('.updates .checked')).toBeNull()
    // the bot's own words for anything else
    await boot('/admin/system?tab=updates', { answer: reply({ error: 'checksums.txt: no version header' }) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.other')))
    expect(document.querySelector('.not-checked .error')?.textContent).toBe('checksums.txt: no version header')
    // not checked yet
    await boot('/admin/system?tab=updates', { answer: reply({}) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.not_checked')))
    // an older core: its own version, and no check
    await boot('/admin/system?tab=updates', { answer: reply(null) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.unsupported')))
    expect(document.querySelector('.current-version')?.textContent).toBe('v1.5.0')
    // a newer release read before the last check failed: still shown, with the failure
    await boot('/admin/system?tab=updates', { answer: reply({ latest: '1.6.0', newer: true, verified: true, error: 'unreachable' }) })
    await found('.update-available')
    expect(document.querySelector('.reason-title')?.textContent).toBe(text('system.updates.last_check_failed'))
    expect(reason()).toBe(text('system.updates.reason.unreachable'))
    expect(document.querySelector('.unverified')).toBeNull()
  })

  it('shows the owner each service’s recent warnings and errors, newest first', async () => {
    const t0 = nowSec() - 600
    const logs: Record<string, SystemLogs> = {
      core: {
        service: 'core',
        lines: [
          { time: t0, level: 'WARN', msg: 'slow query', attrs: '{"ms":1200}' },
          { time: t0 + 60, level: 'ERROR', msg: 'payment webhook failed', attrs: '{}' },
        ],
        error: '',
      },
      bot: { service: 'bot', lines: [], error: '' },
      payments: { service: 'payments', lines: null, error: 'unreachable' },
      provisioner: { service: 'provisioner', lines: [], error: 'rpc error: code = Unimplemented' },
    }
    const { calls, router } = await boot('/admin/system', {
      perms: ownerPerms, role: 'owner', license: { state: 'active', white_label: false },
      answer: (url) => {
        const m = /^\/api\/v1\/system\/logs\?service=(\w+)$/.exec(url)
        return m ? json(200, logs[m[1]]) : undefined
      },
    })
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    // nothing read before the tab opens
    expect(calls.some((c) => c.includes('/system/logs'))).toBe(false)
    btn('.tab-logs').click()
    await found('.log-line')
    expect(router.currentRoute.value.query.tab).toBe('logs')
    expect(calls).toContain('GET /api/v1/system/logs?service=core')
    expect(document.querySelector('.logs .note')?.textContent?.trim()).toBe(text('system.logs.note'))
    // English, left to right inside the right-to-left page, newest first
    const list = document.querySelector('.log-lines') as HTMLElement
    expect(document.documentElement.getAttribute('dir')).toBe('rtl')
    expect(list.getAttribute('dir')).toBe('ltr')
    const lines = [...list.querySelectorAll('.log-line')] as HTMLElement[]
    expect(lines.map((l) => l.querySelector('.msg')?.textContent)).toEqual(['payment webhook failed', 'slow query'])
    expect(lines.map((l) => l.querySelector('.level')?.textContent)).toEqual(['ERROR', 'WARN'])
    expect(lines[0].querySelector('.level')?.classList.contains('p-tag-danger')).toBe(true)
    expect(lines[0].querySelector('.attrs')).toBeNull()
    expect(lines[1].querySelector('.attrs')?.textContent).toBe('{"ms":1200}')
    expect(lines[1].querySelector('.msg')?.classList.contains('app-mono')).toBe(true)
    expect(document.querySelector('.logs .empty')).toBeNull()

    const pick = (name: string) => {
      const label = text('system.service.' + name)
      const b = [...document.querySelectorAll('.log-service button')].find((x) => x.textContent?.trim() === label) as HTMLButtonElement
      b.click()
    }
    // nothing went wrong since the bot started
    pick('bot')
    const empty = await found('.logs .empty')
    expect(empty.textContent?.trim()).toBe(text('system.logs.empty'))
    expect(calls).toContain('GET /api/v1/system/logs?service=bot')
    expect(document.querySelector('.log-line')).toBeNull()
    // core could not ask the service
    pick('payments')
    const unread = await found('.logs-unread')
    expect(unread.querySelector('.reason')?.textContent).toBe(fill(text('system.logs.reason.unreachable'), { service: text('system.service.payments') }))
    expect(document.querySelector('.logs .empty')).toBeNull()
    // the service's own words for anything else
    pick('provisioner')
    await vi.waitFor(() => expect(document.querySelector('.logs-unread .error')?.textContent).toBe('rpc error: code = Unimplemented'))
    expect(document.querySelector('.logs-unread .reason')?.textContent).toBe(fill(text('system.logs.reason.other'), { service: text('system.service.provisioner') }))
    // read again on demand; nothing is ever changed from here
    btn('.logs button.refresh').click()
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/system/logs?service=provisioner')).toHaveLength(2))
    expect(calls.some((c) => !c.startsWith('GET '))).toBe(false)
  })

  it('says when the logs cannot be read, and tries again', async () => {
    let fail = true
    await boot('/admin/system?tab=logs', {
      perms: ownerPerms, role: 'owner', license: { state: 'active', white_label: false },
      answer: (url) => {
        if (!url.startsWith('/api/v1/system/logs')) return undefined
        if (fail) {
          fail = false
          return json(503, { error: 'unavailable', message: 'down' })
        }
        return json(200, { service: 'core', lines: [], error: '' })
      },
    })
    const err = await found('.logs .load-error')
    expect(err.textContent).toContain(text('system.logs.load_error'))
    btn('.logs button.refresh').click()
    await found('.logs .empty')
    expect(document.querySelector('.logs .load-error')).toBeNull()
  })

  it('keeps the logs from admins', async () => {
    // even from a link to the tab: the health tab instead
    const { calls } = await boot('/admin/system?tab=logs')
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    expect(document.querySelector('.tab-health')?.getAttribute('aria-selected')).toBe('true')
    expect(document.querySelector('.tab-logs')).toBeNull()
    expect(document.querySelector('.logs')).toBeNull()
    expect(calls.some((c) => c.includes('/system/logs'))).toBe(false)
  })

  it('shows admins the license read only, with this install’s id to copy', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { calls, router } = await boot('/admin/system', { license: { state: 'missing', white_label: false } })
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    // the tab says the license needs a look
    expect(document.querySelector('.tab-license .tab-state')?.textContent).toBe(text('license.state.missing'))
    btn('.tab-license').click()
    await found('.license-state')
    expect(router.currentRoute.value.query.tab).toBe('license')
    expect(calls).toContain('GET /api/v1/license')
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.missing'))
    expect(document.querySelector('.state-hint')?.textContent).toBe(text('license.state_hint.missing'))
    expect(document.querySelector('.install-id')?.textContent).toBe(iid)
    expect(document.querySelector('.this-domain')?.textContent).toBe('shop.example.com')
    // nothing granted, no binding to compare
    expect(document.querySelector('.entitlements')).toBeNull()
    expect(document.querySelector('.binding-domain')).toBeNull()
    // only the owner adds a license
    expect(document.querySelector('.license-import')).toBeNull()
    expect(document.querySelector('.owner-only')?.textContent).toBe(text('license.owner_only'))
    btn('button.copy-install').click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(iid))
    // the health tab stays loaded: no second request going back
    btn('.tab-health').click()
    await vi.waitFor(() => expect(router.currentRoute.value.query.tab).toBe('health'))
    expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(1)
  })

  it('explains a license that was not accepted', async () => {
    await boot('/admin/system?tab=license', {
      answer: (url) => (url === '/api/v1/license' ? json(200, { ...missing(), state: 'invalid', reason: 'unknown_key', keys: 0, updated_at: nowSec(), updated_by: { id: 'u1', username: '' } }) : undefined),
    })
    await found('.license-state')
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.invalid'))
    expect(document.querySelector('.reason')?.textContent).toBe(text('license.reason.unknown_key'))
    // this build has no vendor key yet: said plainly
    expect(document.querySelector('.no-keys')?.textContent).toBe(text('license.no_keys'))
    // saved by a staff member without a username: their id
    expect(document.querySelector('.updated')?.textContent).toContain('\u2068u1\u2069')
    // the health tab was never opened
    expect(document.querySelector('.services-health')).toBeNull()
  })

  it('shows what a valid license grants against what is used', async () => {
    await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner', license: { state: 'active', white_label: false },
      answer: (url) => (url === '/api/v1/license' ? json(200, active()) : undefined),
    })
    await found('.entitlements')
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.active'))
    expect(document.querySelector('.tab-license .tab-state')).toBeNull()
    expect(document.querySelector('.tier')?.textContent).toBe(text('license.tier.standard'))
    expect(document.querySelector('.license-id')?.textContent).toBe('lic_7Q2')
    expect(document.querySelector('.kid')?.textContent).toBe('lic-2026a')
    expect(document.querySelector('.days-left')?.textContent).toContain(fill(text('license.left.left'), { n: '۴۰' }))
    expect(document.querySelector('.grace-ends')).not.toBeNull()
    expect(document.querySelector('.updated')?.textContent).toContain('\u2068@boss\u2069')
    expect(document.querySelector('.no-keys')).toBeNull()
    // bound to this domain (as core compares it), not to an install
    expect(document.querySelector('.binding-domain')?.textContent).toContain(text('license.binding_state.match'))
    expect(document.querySelector('.binding-domain')?.textContent).toContain('Shop.Example.com.')
    expect(document.querySelector('.binding-install')?.textContent).toContain(text('license.binding_state.unbound'))
    const ent = (key: string) => document.querySelector('.ent-' + key) as HTMLElement
    expect(ent('servers').querySelector('.use')?.textContent).toBe(fill(text('license.usage'), { used: '۳', limit: '۵' }))
    expect(ent('servers').querySelector('.limit-state')).toBeNull()
    expect(ent('active_services').querySelector('.limit-state')?.textContent).toBe(text('license.limit_state.near'))
    expect(ent('staff_seats').querySelector('.limit-state')?.textContent).toBe(text('license.limit_state.full'))
    expect(ent('gateways').querySelector('.outside')?.textContent).toContain(text('payment.provider.stars'))
    expect(ent('gateways').querySelector('.allowed')?.textContent).toBe(text('payment.provider.zarinpal') + '، ' + text('payment.provider.manual_card'))
    expect(ent('resellers').querySelector('.feature')?.textContent).toBe(text('license.feature.on'))
    expect(ent('white_label').querySelector('.feature')?.textContent).toBe(text('license.feature.off'))
    expect(ent('updates_until').textContent).toContain('2027-09-30')
    expect(document.querySelector('.limits-warn')?.textContent).toBe(text('license.warnings'))
    // the owner may replace it
    expect(document.querySelector('.license-import')?.textContent).toContain(text('license.import.replace'))
    // a valid license without white label shows the attribution, and no chip
    expect(document.querySelector('.powered-by')?.textContent).toBe(text('license.powered_by'))
    expect(document.querySelector('.license-chip')).toBeNull()
  })

  it('lets the owner check a token, then save it with a reason', async () => {
    const good = 'eyJ2IjoxLCJsaWNlbnNlX2lkIjoibGljXzdRMiJ9.c2lnbmF0dXJl'
    const bad = 'eyJ2IjoxfQ.YmFk'
    let saved = false
    const { calls } = await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner', license: { state: 'missing', white_label: false },
      answer: (url, init, b) => {
        if (url === '/api/v1/license/check') {
          if (b.token === bad) return json(200, { ...missing(), state: 'invalid', reason: 'bad_signature' })
          return json(200, { ...active(), updated_at: undefined })
        }
        if (url === '/api/v1/license' && init?.method === 'PUT') {
          saved = true
          return json(200, active())
        }
        if (url === '/api/v1/license' && saved) return json(200, active())
        if (url === '/api/v1/me' && saved) {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'owner', language: 'fa' }, permissions: ownerPerms, csrf: 'c',
            session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
            license: { state: 'active', white_label: false },
          })
        }
      },
    })
    const card = await found('.license-import')
    expect(card.textContent).toContain(text('license.import.add'))
    // the menu shows the install is unlicensed, and there is no attribution
    expect(document.querySelector('.license-chip')?.textContent).toBe(text('license.chip.missing'))
    expect(document.querySelector('.license-chip')?.getAttribute('href')).toBe('/admin/system?tab=license')
    expect(document.querySelector('.powered-by')).toBeNull()

    const area = card.querySelector('textarea.token-input')
    const check = () => btn('.license-import button.check')
    expect(check().disabled).toBe(true)
    type(area, 'not a token')
    await vi.waitFor(() => expect(card.querySelector('.token-problem')?.textContent).toBe(text('license.import.problem.format')))
    expect(check().disabled).toBe(true)

    // a refused token: why, and no way to save it
    type(area, bad)
    await vi.waitFor(() => expect(check().disabled).toBe(false))
    check().click()
    await found('.check-result')
    expect(sent(calls, 'POST', '/license/check')).toEqual([{ token: bad }])
    expect(card.querySelector('.result-state')?.textContent).toBe(text('license.state.invalid'))
    expect(card.querySelector('.result-reason')?.textContent).toBe(text('license.reason.bad_signature'))
    expect(card.querySelector('button.save')).toBeNull()

    // a good one, pasted with line breaks: checked as one token
    type(area, good.slice(0, 20) + '\n  ' + good.slice(20) + '\n')
    // the old result belongs to the old token
    await vi.waitFor(() => expect(card.querySelector('.check-result')).toBeNull())
    check().click()
    await vi.waitFor(() => expect(card.querySelector('.result-state')?.textContent).toBe(text('license.state.active')))
    expect(sent(calls, 'POST', '/license/check')[1]).toEqual({ token: good })
    expect(card.querySelector('.result-tier')?.textContent).toBe(text('license.tier.standard'))
    // nothing is saved by a check
    expect(calls.some((c) => c.startsWith('PUT '))).toBe(false)

    btn('.license-import button.save').click()
    const dialog = await found('.p-dialog')
    expect(dialog.textContent).toContain(text('license.import.confirm_message'))
    const confirm = () => [...dialog.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
    expect(confirm().disabled).toBe(true)
    type(dialog.querySelector('textarea'), 'bought the standard tier')
    await vi.waitFor(() => expect(confirm().disabled).toBe(false))
    confirm().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/license')).toEqual([{ token: good, reason: 'bought the standard tier' }]))
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    // the page, the chip and the footer follow the new license
    await vi.waitFor(() => expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.active')))
    expect(calls.filter((c) => c === 'GET /api/v1/me').length).toBeGreaterThanOrEqual(2)
    await vi.waitFor(() => expect(document.querySelector('.powered-by')?.textContent).toBe(text('license.powered_by')))
    expect(document.querySelector('.license-chip')).toBeNull()
    expect((document.querySelector('textarea.token-input') as HTMLTextAreaElement).value).toBe('')
    expect(document.querySelector('.check-result')).toBeNull()
  })

  it('shows why core refused to save a token', async () => {
    const tok = 'eyJ2IjoxfQ.c2ln'
    const { calls } = await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (url === '/api/v1/license/check') return json(200, { ...active(), state: 'grace' })
        if (url === '/api/v1/license' && init?.method === 'PUT') return json(400, { error: 'invalid', field: 'token', code: 'domain', message: 'token: the license is for another domain' })
      },
    })
    const card = await found('.license-import')
    type(card.querySelector('textarea.token-input'), tok)
    await vi.waitFor(() => expect(btn('.license-import button.check').disabled).toBe(false))
    btn('.license-import button.check').click()
    await vi.waitFor(() => expect(card.querySelector('.result-state')?.textContent).toBe(text('license.state.grace')))
    btn('.license-import button.save').click()
    const dialog = await found('.p-dialog')
    type(dialog.querySelector('textarea'), 'renewal')
    const confirm = () => [...dialog.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
    await vi.waitFor(() => expect(confirm().disabled).toBe(false))
    confirm().click()
    await vi.waitFor(() => expect(card.querySelector('.result-reason')?.textContent).toBe(text('license.reason.domain')))
    expect(sent(calls, 'PUT', '/license')).toHaveLength(1)
    expect(card.querySelector('button.save')).toBeNull()
    // nothing was saved: the page still shows no license
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.missing'))
  })

  it('says an expired license cannot be saved, and reads a token file', async () => {
    const tok = 'eyJ2IjoxfQ.ZXhwaXJlZA'
    await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner',
      answer: (url) => (url === '/api/v1/license/check' ? json(200, { ...active(), state: 'expired' }) : undefined),
    })
    const card = await found('.license-import')
    const input = card.querySelector('input[type="file"]') as HTMLInputElement
    // too big to be a license: refused without reading it
    Object.defineProperty(input, 'files', { value: [new File([new Uint8Array(64 * 1024 + 1)], 'big.lic')], configurable: true })
    input.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect(card.querySelector('.file-error')?.textContent).toBe(text('license.import.file_too_big')))
    Object.defineProperty(input, 'files', { value: [new File([tok + '\n'], 'shop.lic', { type: 'text/plain' })], configurable: true })
    input.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect((card.querySelector('textarea.token-input') as HTMLTextAreaElement).value).toBe(tok))
    expect(card.querySelector('.file-error')).toBeNull()
    await vi.waitFor(() => expect(btn('.license-import button.check').disabled).toBe(false))
    btn('.license-import button.check').click()
    await found('.expired-warn')
    expect(card.querySelector('.expired-warn')?.textContent).toBe(text('license.import.expired_warn'))
    // core installs no expired license: nothing to save
    expect(card.querySelector('button.save')).toBeNull()
  })

  it('hides the attribution under white label, and on older cores', async () => {
    await boot('/admin/system?tab=license', { license: { state: 'active', white_label: true } })
    await found('.license-state')
    expect(document.querySelector('.powered-by')).toBeNull()
    expect(document.querySelector('.license-chip')).toBeNull()
    // a license in grace needs a look: the chip says so
    await boot('/admin/', { license: { state: 'grace', white_label: false } })
    await found('.license-chip')
    expect(document.querySelector('.license-chip')?.textContent).toBe(text('license.chip.grace'))
    expect(document.querySelector('.powered-by')?.textContent).toBe(text('license.powered_by'))
    // a core without the license field: neither
    await boot('/admin/', { license: null })
    await found('.sidebar')
    expect(document.querySelector('.license-chip')).toBeNull()
    expect(document.querySelector('.powered-by')).toBeNull()
  })

  it('shows the attribution on the login page only when core says so', async () => {
    await boot('/admin/login', { loggedIn: false, brand: { powered_by: true } })
    await vi.waitFor(() => expect(document.querySelector('.login .powered-by')?.textContent).toBe(text('license.powered_by')))
    await boot('/admin/login', { loggedIn: false })
    await found('.login')
    await new Promise((r) => setTimeout(r, 20))
    expect(document.querySelector('.powered-by')).toBeNull()
  })
})
