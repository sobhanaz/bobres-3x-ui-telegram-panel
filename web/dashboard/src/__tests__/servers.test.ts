import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import {
  addressChanged,
  blankServer,
  canTest,
  capacityOf,
  choiceOf,
  goneInbounds,
  healthOf,
  hostOf,
  inboundOptions,
  inboundsBody,
  maxClientsOf,
  nearFull,
  needsToken,
  noInbound,
  noUsableServer,
  panelURLProblem,
  privateNeedsOwner,
  serverBody,
  serverFormOf,
  serverNames,
  serverOptions,
  serverProblems,
  serverSummary,
  subURLProblem,
  testBody,
  weightOf,
} from '../servers'
import type { InboundItem, OverviewServer, PlanItem, ServerItem } from '../types'

const t0 = 1_760_000_000
const sid1 = '11111111-1111-4111-8111-111111111111'
const sid2 = '22222222-2222-4222-8222-222222222222'
const sid3 = '33333333-3333-4333-8333-333333333333'
const gone = '99999999-9999-4999-8999-999999999999'
const server = (over: Partial<ServerItem> = {}): ServerItem => ({
  id: sid1,
  name: 'de-1',
  base_url: 'https://panel.example.com:2053/secret',
  sub_base_url: 'https://sub.example.com/sub/',
  enabled: true,
  allow_private: false,
  weight: 100,
  panel_version: 'v3.9.0',
  healthy: true,
  health_error: '',
  checked_at: t0,
  clients: 12,
  has_token: true,
  plans: [{ id: 'p1', name: { fa: 'ماهانه', en: 'Monthly' } }],
  ...over,
})
const de2 = server({ id: sid2, name: 'de-2', healthy: false, health_error: 'dial tcp 10.0.0.9:2053: i/o timeout', clients: 3, weight: 50, version_untested: true, panel_version: 'v3.7.1', plans: [] })
const de3 = server({ id: sid3, name: 'de-3', enabled: false, clients: 0, has_token: false, allow_private: true, plans: [] })

describe('server health', () => {
  it('reads the last check, and an off server as off', () => {
    expect(healthOf(server())).toBe('ok')
    expect(healthOf(de2)).toBe('down')
    // before the first check, "healthy" means nothing
    expect(healthOf(server({ healthy: false, checked_at: null }))).toBe('unknown')
    expect(healthOf(server({ healthy: true, checked_at: null }))).toBe('unknown')
    // an off server is not checked: its old result does not count
    expect(healthOf(server({ enabled: false, healthy: false, health_error: 'x' }))).toBe('off')
  })
  it('sums up the overview from the servers taking new services', () => {
    const ov = (id: string, healthy: boolean, enabled?: boolean): OverviewServer => ({ id, name: id, healthy, detail: healthy ? '' : 'refused', clients: 1, ...(enabled === undefined ? {} : { enabled }) })
    // switched off because its panel is down: the failed check it kept does not count
    const off = ov('off', false, false)
    expect(serverSummary([ov('a', true, true), ov('b', false, true), off])).toEqual({ live: 2, down: [ov('b', false, true)] })
    expect(serverSummary([ov('a', true, true), off])).toEqual({ live: 1, down: [] })
    expect(serverSummary([off, ov('off2', true, false)])).toEqual({ live: 0, down: [] })
    // an older core does not say which are on: all of them count
    expect(serverSummary([ov('a', true), ov('b', false)])).toEqual({ live: 2, down: [ov('b', false)] })
    expect(serverSummary([])).toEqual({ live: 0, down: [] })
  })
})

describe('server addresses', () => {
  it('wants https, or plain http only for a panel on a private network', () => {
    expect(panelURLProblem('https://panel.example.com:2053/secret', false)).toBe('')
    expect(panelURLProblem(' https://1.2.3.4:54321/ ', false)).toBe('')
    expect(panelURLProblem('http://10.0.0.5:2053/x', false)).toBe('https')
    expect(panelURLProblem('http://10.0.0.5:2053/x', true)).toBe('')
    expect(panelURLProblem('https://admin:pw@panel.example.com/', true)).toBe('credentials')
    expect(panelURLProblem('panel.example.com', false)).toBe('url')
    expect(panelURLProblem('ftp://panel.example.com', true)).toBe('url')
    expect(panelURLProblem('https://panel example.com', false)).toBe('url')
    expect(panelURLProblem('', false)).toBe('url')
  })
  it('takes an empty or http(s) subscription address', () => {
    expect(subURLProblem('')).toBe('')
    expect(subURLProblem('  ')).toBe('')
    expect(subURLProblem('https://sub.example.com/sub/')).toBe('')
    expect(subURLProblem('http://sub.example.com:2096/sub/')).toBe('')
    expect(subURLProblem('sub.example.com')).toBe('url')
    expect(subURLProblem('javascript:alert(1)')).toBe('url')
  })
  it('notices a moved panel, not a trailing slash', () => {
    const s = server()
    expect(addressChanged({ ...serverFormOf(s), baseURL: s.base_url + '/' }, s)).toBe(false)
    expect(addressChanged({ ...serverFormOf(s), baseURL: 'https://other.example.com/x' }, s)).toBe(true)
    expect(addressChanged(blankServer(), null)).toBe(false)
  })
})

describe('server form', () => {
  it('reads weights from 1 to 1000 in either digit set', () => {
    expect(weightOf('۲۰۰')).toBe(200)
    expect(weightOf('1000')).toBe(1000)
    expect(weightOf('1')).toBe(1)
    expect(weightOf('0')).toBeNull()
    expect(weightOf('1001')).toBeNull()
    expect(weightOf('1.5')).toBeNull()
    expect(weightOf('-5')).toBeNull()
    expect(weightOf('')).toBeNull()
  })
  it('needs a token only while none can be kept', () => {
    expect(serverProblems(blankServer(), null)).toEqual(['name', 'base_url', 'token'])
    const typed = { ...blankServer(), name: ' de-2 ', baseURL: 'https://p.example.com/x', token: ' secret ' }
    const stored = { has_token: true, base_url: 'https://p.example.com/old-path' }
    expect(serverProblems(typed, null)).toEqual([])
    expect(serverProblems({ ...typed, token: '' }, null)).toEqual(['token'])
    expect(serverProblems({ ...typed, token: '' }, stored)).toEqual([])
    expect(serverProblems({ ...typed, token: '' }, { ...stored, has_token: false })).toEqual(['token'])
    expect(serverProblems({ ...typed, token: 'two words' }, stored)).toEqual(['token'])
    // a stored token never goes to another host or port
    expect(serverProblems({ ...typed, token: '', baseURL: 'https://evil.example.com/x' }, stored)).toEqual(['token'])
    expect(serverProblems({ ...typed, token: '', baseURL: 'https://p.example.com:8443/x' }, stored)).toEqual(['token'])
    expect(serverProblems({ ...typed, baseURL: 'https://evil.example.com/x' }, stored)).toEqual([])
    expect(serverProblems({ ...typed, name: 'ب'.repeat(65) }, null)).toEqual(['name'])
    expect(serverProblems({ ...typed, name: 'ب'.repeat(64) }, null)).toEqual([])
    expect(serverProblems({ ...typed, subURL: 'nope', weight: '0' }, null)).toEqual(['sub_base_url', 'weight'])
  })
  it('never fills in a token, and sends an empty one to keep the stored token', () => {
    const f = serverFormOf(server())
    expect(f.token).toBe('')
    expect(f).toMatchObject({ name: 'de-1', baseURL: 'https://panel.example.com:2053/secret', weight: '100', enabled: true, allowPrivate: false, skipCheck: false })
    expect(serverBody(f)).toEqual({
      kind: 'xui', name: 'de-1', base_url: 'https://panel.example.com:2053/secret', sub_base_url: 'https://sub.example.com/sub/', token: '',
      weight: 100, max_clients: 0, enabled: true, allow_private: false, skip_check: false,
    })
    expect(serverBody({ ...f, name: ' de-1 ', token: ' new ', weight: '۵۰', skipCheck: true })).toMatchObject({ name: 'de-1', token: 'new', weight: 50, skip_check: true })
  })
  it('compares hosts as the provisioner does', () => {
    expect(hostOf('https://Panel.Example.com:2053/secret')).toBe('panel.example.com:2053')
    expect(hostOf(' https://admin:pw@panel.example.com/x ')).toBe('panel.example.com')
    expect(hostOf('http://[::1]:2053/')).toBe('[::1]:2053')
    expect(hostOf('panel.example.com')).toBe('')
    const s = server()
    expect(needsToken({ baseURL: 'https://PANEL.example.com:2053/other' }, s)).toBe(false)
    expect(needsToken({ baseURL: 'https://panel.example.com:2054/secret' }, s)).toBe(true)
    expect(needsToken({ baseURL: 'https://panel.example.com/secret' }, s)).toBe(true)
    expect(needsToken({ baseURL: s.base_url }, { ...s, has_token: false })).toBe(true)
    expect(needsToken({ baseURL: s.base_url }, null)).toBe(true)
  })
  it('takes a Marzban login as username:password, and keeps a saved server’s kind', () => {
    const m = { ...blankServer(), kind: 'marzban' as const, name: 'mz', baseURL: 'https://m.example.com', token: 'admin:pw' }
    expect(serverProblems(m, null)).toEqual([])
    for (const bad of ['admin', ':pw', 'admin:', 'ad min:pw']) expect(serverProblems({ ...m, token: bad }, null)).toEqual(['token'])
    expect(serverProblems({ ...m, token: '' }, { has_token: true, base_url: 'https://m.example.com' })).toEqual([])
    expect(serverBody(m)).toMatchObject({ kind: 'marzban', token: 'admin:pw' })
    expect(testBody(m)).toMatchObject({ kind: 'marzban' })
    expect(serverFormOf({ ...server({ id: sid1 }), kind: 'marzban' }).kind).toBe('marzban')
    expect(serverFormOf(server({ id: sid1 })).kind).toBe('xui')
  })

  it('tests the typed connection, or a saved server’s with its stored token', () => {
    const f = { ...blankServer(), baseURL: ' https://p.example.com/x ', token: ' tok ', allowPrivate: true }
    expect(testBody(f)).toEqual({ kind: 'xui', base_url: 'https://p.example.com/x', token: 'tok', allow_private: true })
    expect(testBody({ ...f, token: '' }, sid1)).toEqual({ id: sid1, kind: 'xui', base_url: 'https://p.example.com/x', token: '', allow_private: true })
    expect(canTest(f, null)).toBe(true)
    expect(canTest({ ...f, token: '' }, null)).toBe(false)
    expect(canTest({ ...f, token: '' }, { has_token: true, base_url: 'https://p.example.com/old' })).toBe(true)
    expect(canTest({ ...f, token: '' }, { has_token: true, base_url: 'https://elsewhere.example.com/x' })).toBe(false)
    expect(canTest({ ...f, allowPrivate: false, baseURL: 'http://p.example.com' }, null)).toBe(false)
  })
  it('knows which private-network changes core leaves to the owner', () => {
    const lan = { allow_private: true, base_url: 'http://10.0.0.5:2053/x' }
    const f = { allowPrivate: true, baseURL: 'http://10.0.0.5:2053/x', token: '' }
    // a new private panel, or one newly marked private
    expect(privateNeedsOwner({ ...f, token: 'tok' }, null)).toBe(true)
    expect(privateNeedsOwner(f, { ...lan, allow_private: false })).toBe(true)
    // a private panel as it is (a trailing slash aside) may be renamed, reweighted or switched off
    expect(privateNeedsOwner(f, lan)).toBe(false)
    expect(privateNeedsOwner({ ...f, baseURL: ' http://10.0.0.5:2053/x/ ' }, lan)).toBe(false)
    expect(privateNeedsOwner({ ...f, token: '  ' }, lan)).toBe(false)
    // but not pointed elsewhere, nor given a new token
    expect(privateNeedsOwner({ ...f, baseURL: 'http://10.0.0.5:2053/y' }, lan)).toBe(true)
    expect(privateNeedsOwner({ ...f, baseURL: 'http://10.0.0.6:2053/x' }, lan)).toBe(true)
    expect(privateNeedsOwner({ ...f, token: 'new' }, lan)).toBe(true)
    // nothing private: nothing for the owner
    expect(privateNeedsOwner({ ...f, allowPrivate: false, token: 'new' }, null)).toBe(false)
    expect(privateNeedsOwner({ ...f, allowPrivate: false, baseURL: 'https://p.example.com/' }, lan)).toBe(false)
  })
})

describe('server capacity', () => {
  it('reads a typed limit in either digit set, and nothing typed as no limit', () => {
    expect(maxClientsOf('')).toBe(0)
    expect(maxClientsOf('  ')).toBe(0)
    expect(maxClientsOf('0')).toBe(0)
    expect(maxClientsOf('۱۲۰')).toBe(120)
    expect(maxClientsOf('1,000,000')).toBe(1_000_000)
    expect(maxClientsOf('1000001')).toBeNull()
    for (const bad of ['-1', '1.5', 'abc', '12 3a', '99999999']) expect(maxClientsOf(bad), bad).toBeNull()
  })
  it('puts the limit in the form and the request, and refuses what core would refuse', () => {
    const limited = server({ max_clients: 80 })
    expect(serverFormOf(limited).maxClients).toBe('80')
    expect(serverFormOf(server()).maxClients).toBe('') // an older core says nothing: no limit
    expect(serverFormOf(server({ max_clients: 0 })).maxClients).toBe('')
    expect(serverBody(serverFormOf(limited)).max_clients).toBe(80)
    expect(serverBody({ ...serverFormOf(limited), maxClients: '' }).max_clients).toBe(0) // clearing the field lifts the limit
    expect(serverBody({ ...serverFormOf(limited), maxClients: '۱۵۰' }).max_clients).toBe(150)
    const typed = { ...blankServer(), name: 'x', baseURL: 'https://p.example.com/x', token: 't' }
    expect(serverProblems(typed, null)).toEqual([])
    expect(serverProblems({ ...typed, maxClients: '250' }, null)).toEqual([])
    expect(serverProblems({ ...typed, maxClients: '-3' }, null)).toEqual(['max_clients'])
    expect(serverProblems({ ...typed, maxClients: '2000000' }, null)).toEqual(['max_clients'])
  })
  it('says how full a server is: none without a limit, near from 90%, full at the limit', () => {
    expect(capacityOf({ clients: 500 })).toEqual({ clients: 500, max: 0, pct: 0, state: 'none' })
    expect(capacityOf({ clients: 500, max_clients: 0 }).state).toBe('none')
    expect(capacityOf({ clients: 0, max_clients: 10 })).toEqual({ clients: 0, max: 10, pct: 0, state: 'ok' })
    expect(capacityOf({ clients: 89, max_clients: 100 })).toEqual({ clients: 89, max: 100, pct: 89, state: 'ok' })
    expect(capacityOf({ clients: 899, max_clients: 1000 }).state).toBe('ok') // 89.9%
    expect(capacityOf({ clients: 90, max_clients: 100 })).toEqual({ clients: 90, max: 100, pct: 90, state: 'near' })
    expect(capacityOf({ clients: 9, max_clients: 10 }).state).toBe('near')
    expect(capacityOf({ clients: 99, max_clients: 100 }).state).toBe('near')
    expect(capacityOf({ clients: 100, max_clients: 100 })).toEqual({ clients: 100, max: 100, pct: 100, state: 'full' })
    expect(capacityOf({ clients: 150, max_clients: 100 })).toEqual({ clients: 150, max: 100, pct: 150, state: 'full' }) // the limit was set below what it holds
    expect(capacityOf({ clients: -4, max_clients: 10 }).clients).toBe(0)
  })
  it('lists the servers that take new services and are nearly or entirely full', () => {
    const s = (id: string, clients: number, max: number, enabled?: boolean) => ({ id, clients, max_clients: max, ...(enabled === undefined ? {} : { enabled }) })
    const list = [s('a', 50, 100), s('b', 90, 100), s('c', 100, 100), s('d', 500, 0), s('e', 100, 100, false), s('f', 95, 100, true)]
    expect(nearFull(list).map((x) => x.id)).toEqual(['b', 'c', 'f'])
    expect(nearFull([])).toEqual([])
  })
})

describe('plan servers', () => {
  const list = [server(), de2, de3]
  it('offers every server, and a removed one the plan still names', () => {
    expect(serverOptions(list, [sid1, gone])).toEqual([
      { value: sid1, label: 'de-1', off: false, removed: false },
      { value: sid2, label: 'de-2', off: false, removed: false },
      { value: sid3, label: 'de-3', off: true, removed: false },
      { value: gone, label: '99999999', off: true, removed: true },
    ])
    expect(serverOptions(list)).toHaveLength(3)
  })
  it('warns when none of the chosen servers takes new services', () => {
    expect(noUsableServer(list, [])).toBe(false) // any server
    expect(noUsableServer(list, [sid3])).toBe(true)
    expect(noUsableServer(list, [gone])).toBe(true)
    expect(noUsableServer(list, [sid3, gone, sid2])).toBe(false)
  })
  it('names them, removed ones blank', () => {
    expect(serverNames([sid2, gone, sid1], list)).toEqual(['de-2', '', 'de-1'])
  })
  it('warns when no inbound would take new services', () => {
    const ib = (id: number, enabled: boolean, attach: boolean): InboundItem => ({ id, tag: 'inbound-' + id, enabled, attach })
    expect(noInbound([ib(1, true, true), ib(2, false, false)])).toBe(false)
    expect(noInbound([ib(1, true, false), ib(2, false, true)])).toBe(true)
    expect(noInbound([])).toBe(true)
  })
})

describe('plan inbound choice', () => {
  const ib = (id: number, enabled = true, tag = ''): InboundItem => ({ id, tag, enabled, attach: true })
  it('reads the plan into the form and back, per server, sorted', () => {
    expect(choiceOf(undefined)).toEqual({})
    expect(choiceOf([{ server_id: sid1, inbound_ids: [3, 1] }, { server_id: sid2, inbound_ids: [] }])).toEqual({ [sid1]: [3, 1] })
    expect(inboundsBody({ [sid2]: [2], [sid1]: [3, 1], [sid3]: [] }, [])).toEqual([
      { server_id: sid1, inbound_ids: [1, 3] },
      { server_id: sid2, inbound_ids: [2] },
    ])
  })
  it('drops the choice of a server the plan no longer names', () => {
    expect(inboundsBody({ [sid1]: [1], [sid2]: [2] }, [sid2])).toEqual([{ server_id: sid2, inbound_ids: [2] }])
  })
  it('lists the panel inbounds, those off and the ones gone', () => {
    expect(inboundOptions([ib(1, true, 'a'), ib(2, false), ib(3, true, 'c')], [2, 9])).toEqual([
      { value: 1, label: 'a', off: false, gone: false },
      { value: 2, label: '#2', off: true, gone: false },
      { value: 3, label: 'c', off: false, gone: false },
      { value: 9, label: '#9', off: true, gone: true },
    ])
    expect(goneInbounds([ib(1), ib(2, false)], [1, 2, 9])).toEqual([2, 9])
    expect(goneInbounds([], [])).toEqual([])
  })
})

// The pages, booted with the whole app against a fake core.
describe('servers pages', () => {
  const adminPerms = ['overview.read', 'users.read', 'plans.write', 'settings.write', 'servers.write', 'audit.read']
  const supportPerms = ['overview.read', 'users.read', 'services.read', 'services.extend', 'payments.read', 'tickets.write']
  const ownerPerms = [...adminPerms, 'gateways.write', 'staff.write']
  const plan = (id: string, over: Partial<PlanItem> = {}): PlanItem => ({
    id, name: { fa: 'پلن ' + id, en: 'Plan ' + id }, kind: 'both', duration_days: 30, traffic_bytes: 50 * 2 ** 30,
    price: { amount: 120000, currency: 'IRT' }, enabled: true, is_trial: false, is_topup: false, sort: 0, sales: 4, server_ids: [], ...over,
  })
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const text = (path: string) => (path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) as string | undefined) ?? path
  const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replace('{' + k + '}', v), s)

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | Promise<Response> | undefined
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(path: string, permissions: string[], answer: Answer = () => undefined, role = 'admin') {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${raw}`.trim())
        const own = answer(url, init, raw ? (JSON.parse(raw) as Record<string, unknown>) : {})
        if (own) return own
        const get = (init?.method ?? 'GET') === 'GET'
        if (url === '/api/v1/me') {
          return json(200, { user: { id: 'u1', telegram_id: 1, username: 'boss', role, language: 'fa' }, permissions, csrf: 'c', session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop' })
        }
        if (get && url === '/api/v1/servers') return json(200, { items: [server(), de2, de3], tested_min_version: 'v3.8.5', tested_max_version: 'v3.9.0' })
        if (get && url === '/api/v1/plans') return json(200, { items: [plan('p1')] })
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
  const rows = () => [...document.querySelectorAll('.servers .p-datatable-tbody tr')] as HTMLElement[]
  const button = (root: Element, selector: string) => root.querySelector(selector) as HTMLButtonElement

  it('lists servers with their health, load and plans', async () => {
    const { calls } = await boot('/admin/servers', adminPerms)
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    expect(calls).toContain('GET /api/v1/servers')
    const [r1, r2, r3] = rows()
    expect(r1.querySelector('.health')?.textContent).toBe(text('server.health_state.ok'))
    expect(r1.textContent).toContain('https://panel.example.com:2053/secret')
    expect(r1.textContent).toContain('v3.9.0')
    expect(r1.querySelector('.clients')?.textContent).toBe('۱۲')
    expect(r1.textContent).toContain('ماهانه')
    expect(r1.textContent).toContain('۱۰۰')
    // a panel that does not answer says why, and an untested version is marked
    expect(r2.querySelector('.health')?.textContent).toBe(text('server.health_state.down'))
    expect(r2.textContent).toContain('i/o timeout')
    expect(r2.querySelector('.untested')?.textContent).toBe(text('server.untested'))
    expect(r2.querySelector('.untested')?.getAttribute('aria-label')).toBe(fill(text('server.version_range'), { min: '\u2068v3.8.5\u2069', max: '\u2068v3.9.0\u2069' }))
    expect(r2.textContent).toContain(text('server.plans_any'))
    // an off server, without a token, on a private network
    expect(r3.querySelector('.health')?.textContent).toBe(text('server.health_state.off'))
    expect(r3.textContent).toContain(text('server.no_token'))
    expect(r3.textContent).toContain(text('server.private'))
    // only a server without services can be deleted
    expect(button(r1, 'button.delete').disabled).toBe(true)
    expect(button(r3, 'button.delete').disabled).toBe(false)
    // admins manage servers, but gateways stay with the owner
    const menu = document.querySelector('.sidebar')?.textContent ?? ''
    expect(menu).toContain(text('section.servers.title'))
    expect(menu).not.toContain(text('section.gateways.title'))
  })

  it('shows how full a server with a limit is, with a warning from 90% and a mark when it is full', async () => {
    const limited = [
      server({ id: sid1, name: 'half', clients: 40, max_clients: 100 }),
      server({ id: sid2, name: 'near', clients: 92, max_clients: 100 }),
      server({ id: sid3, name: 'full', clients: 100, max_clients: 100 }),
      server({ id: 'free', name: 'free', clients: 300, max_clients: 0 }),
      server({ id: 'off', name: 'off', clients: 100, max_clients: 100, enabled: false }),
    ]
    await boot('/admin/servers', adminPerms, (url, init) => {
      if (url === '/api/v1/servers' && (init?.method ?? 'GET') === 'GET') return json(200, { items: limited })
    })
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    const [half, near, full, free, off] = rows()
    // "N / max" with a bar, in the language's digits
    expect(half.querySelector('.clients')?.textContent).toBe('۴۰ / ۱۰۰')
    expect(half.querySelector('.p-progressbar')?.getAttribute('aria-label')).toBe(fill(text('server.capacity_label'), { percent: '۴۰' }))
    expect(half.querySelector('.chip')).toBeNull()
    // from 90% a warning chip, at the limit another
    expect(near.querySelector('.chip.near')?.textContent).toContain(fill(text('server.capacity_near'), { percent: '۹۲' }))
    expect(full.querySelector('.chip.full')?.textContent).toContain(text('server.capacity_full'))
    expect(full.querySelector('.chip.near')).toBeNull()
    // no limit: only the count; a switched-off server shows its fill but is not warned about
    expect(free.querySelector('.clients')?.textContent).toBe('۳۰۰')
    expect(free.querySelector('.p-progressbar')).toBeNull()
    expect(off.querySelector('.clients')?.textContent).toBe('۱۰۰ / ۱۰۰')
    expect(off.querySelector('.chip')).toBeNull()
    // one line above the table names the servers that need attention (not the one that is off)
    const banner = document.querySelector('.filling')
    expect(banner?.textContent).toContain(text('server.filling'))
    expect([...document.querySelectorAll('.filling-name')].map((e) => e.textContent)).toEqual(['near', 'full'])
  })

  it('has no warning when no server is close to its limit', async () => {
    await boot('/admin/servers', adminPerms)
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    expect(document.querySelector('.filling')).toBeNull()
    expect(document.querySelector('.chip')).toBeNull()
  })

  it('keeps the page from support staff', async () => {
    const { router, calls } = await boot('/admin/servers', supportPerms)
    expect(router.currentRoute.value.name).toBe('overview')
    expect(document.querySelector('.sidebar')?.textContent).not.toContain(text('section.servers.title'))
    expect(calls.some((c) => c.includes('/api/v1/servers'))).toBe(false)
  })

  it('adds a server after a test, and never shows the token again', async () => {
    const token = 'tok-Secret-123'
    let refuse = true
    // the owner: only they may add a panel on a private network
    const { calls } = await boot('/admin/servers', ownerPerms, (url, init, b) => {
      if (url === '/api/v1/servers/test') return json(200, { ok: true, panel_version: 'v3.9.0', inbounds: 3, detail: '' })
      if (url === '/api/v1/servers' && init?.method === 'POST') {
        if (refuse) {
          refuse = false
          return json(400, { error: 'invalid', field: 'base_url', message: 'base_url: the panel did not answer' })
        }
        return json(201, { item: server({ ...b, id: 'new', clients: 0 } as Partial<ServerItem>) })
      }
    }, 'owner')
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    ;(document.querySelector('button.add') as HTMLButtonElement).click()
    const d = await found('.server-dialog')
    const save = () => button(d, 'button.save')
    const test = () => button(d, 'button.test')
    expect(save().disabled).toBe(true)
    expect(test().disabled).toBe(true)
    type(d.querySelector('input.name'), ' de-4 ')
    type(d.querySelector('input.base-url'), 'http://10.0.0.5:2053/x')
    // plain http is refused until the panel is marked private
    await vi.waitFor(() => expect(d.querySelector('.url-problem')?.textContent).toBe(text('server.invalid.https')))
    // nothing is stored yet, so the token field is open, masked
    const tokenInput = d.querySelector('#server-token') as HTMLInputElement
    expect(tokenInput.type).toBe('password')
    expect(tokenInput.autocomplete).toBe('new-password')
    type(tokenInput, ` ${token} `)
    ;(d.querySelector('#server-private') as HTMLInputElement).click()
    await vi.waitFor(() => expect(d.querySelector('.private-warning')?.textContent).toBe(text('server.allow_private_warning')))
    expect(d.querySelector('.url-problem')).toBeNull()
    type(d.querySelector('input.weight'), '۲۰۰')
    type(d.querySelector('input.max-clients'), '۵۰')
    await vi.waitFor(() => expect(test().disabled).toBe(false))
    test().click()
    await vi.waitFor(() => expect(d.querySelector('.test-result')?.textContent).toContain(fill(text('server.test_ok'), { version: '\u20683x-ui v3.9.0\u2069', n: '۳' })))
    expect(sent(calls, 'POST', '/servers/test')).toEqual([{ kind: 'xui', base_url: 'http://10.0.0.5:2053/x', token, allow_private: true }])
    ;(d.querySelector('#server-skip-check') as HTMLInputElement).click()
    await vi.waitFor(() => expect(save().disabled).toBe(false))
    save().click()
    // core's words go under the field
    await vi.waitFor(() => expect(d.textContent).toContain('base_url: the panel did not answer'))
    expect(d.querySelector('input.base-url')?.classList.contains('p-invalid')).toBe(true)
    save().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/servers')).toHaveLength(2))
    expect(sent(calls, 'POST', '/servers')[1]).toEqual({
      kind: 'xui', name: 'de-4', base_url: 'http://10.0.0.5:2053/x', sub_base_url: '', token, weight: 200, max_clients: 50, enabled: true, allow_private: true, skip_check: true,
    })
    await vi.waitFor(() => expect(document.querySelector('.server-dialog')).toBeNull())
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/servers')).toHaveLength(2))
    expect(document.querySelector('.p-toast')?.textContent).toContain(text('server.saved'))
    // the token is nowhere on the page
    expect(document.body.innerHTML).not.toContain(token)
  })

  it('edits a server, keeping its token unless it is replaced', async () => {
    const { calls } = await boot('/admin/servers', adminPerms, (url, init) => {
      if (url === `/api/v1/servers/${sid1}` && init?.method === 'PUT') return json(200, { item: server() })
      if (url === '/api/v1/servers/test') return json(200, { ok: false, panel_version: '', inbounds: 0, detail: 'unauthorized: bad token' })
    })
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    button(rows()[0], 'button.edit').click()
    let d = await found('.server-dialog')
    expect((d.querySelector('input.name') as HTMLInputElement).value).toBe('de-1')
    expect((d.querySelector('input.weight') as HTMLInputElement).value).toBe('100')
    // a stored token shows as set: no field, nothing to read
    expect(d.querySelector('#server-token')).toBeNull()
    expect(d.querySelector('.secret-field .state')?.textContent).toBe(text('secret.set'))
    // moving the address of a server with services is warned about, and a
    // new host needs the token again (the stored one never goes there)
    type(d.querySelector('input.base-url'), 'https://other.example.com/x')
    await vi.waitFor(() => expect(d.querySelector('.moved')?.textContent).toContain(fill(text('server.address_changed'), { n: '۱۲' })))
    await vi.waitFor(() => expect(d.querySelector('#server-token')).not.toBeNull())
    expect(d.querySelector('button.keep')).toBeNull()
    expect(d.querySelector('.secret-field')?.textContent).toContain(text('server.token_again'))
    expect(button(d, 'button.save').disabled).toBe(true)
    expect(button(d, 'button.test').disabled).toBe(true)
    // back on the same host (another path): the stored token is kept again
    type(d.querySelector('input.base-url'), 'https://panel.example.com:2053/secret/')
    await vi.waitFor(() => expect(d.querySelector('.moved')).toBeNull())
    await vi.waitFor(() => expect(d.querySelector('#server-token')).toBeNull())
    type(d.querySelector('input.weight'), '50')
    button(d, 'button.save').click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', `/servers/${sid1}`)).toHaveLength(1))
    expect(sent(calls, 'PUT', `/servers/${sid1}`)[0]).toEqual({
      kind: 'xui', name: 'de-1', base_url: 'https://panel.example.com:2053/secret/', sub_base_url: 'https://sub.example.com/sub/', token: '',
      weight: 50, max_clients: 0, enabled: true, allow_private: false, skip_check: false,
    })
    await vi.waitFor(() => expect(document.querySelector('.server-dialog')).toBeNull())

    // Replace, then keep after all: the stored token stays.
    button(rows()[0], 'button.edit').click()
    d = await found('.server-dialog')
    // the dialog has taken the focus as it opened
    await vi.waitFor(() => expect(document.activeElement?.closest('.server-dialog')).not.toBeNull())
    button(d, 'button.replace').click()
    const input = await found<HTMLInputElement>('#server-token')
    expect(input.value).toBe('')
    await vi.waitFor(() => expect(document.activeElement).toBe(input))
    type(input, 'new-token')
    button(d, 'button.keep').click()
    await vi.waitFor(() => expect(d.querySelector('#server-token')).toBeNull())
    button(d, 'button.save').click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', `/servers/${sid1}`)).toHaveLength(2))
    expect(sent(calls, 'PUT', `/servers/${sid1}`)[1].token).toBe('')
    await vi.waitFor(() => expect(document.querySelector('.server-dialog')).toBeNull())

    // A new token is tested with the saved server's id, then saved.
    button(rows()[0], 'button.edit').click()
    d = await found('.server-dialog')
    button(d, 'button.replace').click()
    type(await found('#server-token'), 'new-token')
    // the eye shows only what was typed
    const reveal = button(d, 'button.reveal')
    await vi.waitFor(() => expect(reveal.disabled).toBe(false))
    reveal.click()
    await vi.waitFor(() => expect((d.querySelector('#server-token') as HTMLInputElement).type).toBe('text'))
    button(d, 'button.test').click()
    await vi.waitFor(() => expect(d.querySelector('.test-result')?.textContent).toContain(text('server.test_failed')))
    expect(d.querySelector('.test-result')?.textContent).toContain('unauthorized: bad token')
    expect(sent(calls, 'POST', '/servers/test')).toEqual([{ id: sid1, kind: 'xui', base_url: 'https://panel.example.com:2053/secret', token: 'new-token', allow_private: false }])
    button(d, 'button.save').click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', `/servers/${sid1}`)).toHaveLength(3))
    expect(sent(calls, 'PUT', `/servers/${sid1}`)[2].token).toBe('new-token')
  })

  it('leaves panels on a private network to the owner', async () => {
    const lan = server({ id: sid3, name: 'lan-1', base_url: 'http://10.0.0.5:2053/x', allow_private: true, clients: 2, plans: [] })
    const { calls } = await boot('/admin/servers', adminPerms, (url, init) => {
      if (url === '/api/v1/servers' && (init?.method ?? 'GET') === 'GET') return json(200, { items: [server(), lan] })
      if (url === `/api/v1/servers/${sid3}` && init?.method === 'PUT') return json(200, { item: lan })
    })
    await vi.waitFor(() => expect(rows()).toHaveLength(2))
    // A new panel: an admin cannot mark it private, and is told who can.
    ;(document.querySelector('button.add') as HTMLButtonElement).click()
    let d = await found('.server-dialog')
    const priv = () => d.querySelector('#server-private') as HTMLInputElement
    expect(priv().disabled).toBe(true)
    expect(d.querySelector('.owner-only')?.textContent).toBe(text('server.private_owner_only'))
    type(d.querySelector('input.base-url'), 'http://10.0.0.9:2053/x')
    await vi.waitFor(() => expect(d.querySelector('.url-problem')?.textContent).toBe(text('server.invalid.https_owner')))
    ;([...d.querySelectorAll('button')].find((b) => b.textContent?.includes(text('app.cancel'))) as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.server-dialog')).toBeNull())

    // A private panel as it is: the switch is open, nothing is said.
    button(rows()[1], 'button.edit').click()
    d = await found('.server-dialog')
    expect(priv().checked).toBe(true)
    expect(priv().disabled).toBe(false)
    expect(d.querySelector('.owner-only')).toBeNull()
    expect(d.querySelector('.owner-needed')).toBeNull()
    // a new token for it is the owner's to give: neither saved nor tested
    button(d, 'button.replace').click()
    type(await found('#server-token'), 'new-token')
    await vi.waitFor(() => expect(d.querySelector('.owner-needed')?.textContent).toBe(text('server.private_owner_change')))
    expect(d.querySelector('.allow-private')?.classList.contains('p-invalid')).toBe(true)
    expect(button(d, 'button.save').disabled).toBe(true)
    expect(button(d, 'button.test').disabled).toBe(true)
    button(d, 'button.keep').click()
    await vi.waitFor(() => expect(d.querySelector('.owner-needed')).toBeNull())
    // and so is another address on the same host
    type(d.querySelector('input.base-url'), 'http://10.0.0.5:2053/other')
    await vi.waitFor(() => expect(d.querySelector('.owner-needed')).not.toBeNull())
    expect(button(d, 'button.save').disabled).toBe(true)
    expect(button(d, 'button.test').disabled).toBe(true)
    type(d.querySelector('input.base-url'), 'http://10.0.0.5:2053/x/')
    await vi.waitFor(() => expect(d.querySelector('.owner-needed')).toBeNull())
    // renaming it stays open to admins
    type(d.querySelector('input.name'), 'lan-2')
    await vi.waitFor(() => expect(button(d, 'button.save').disabled).toBe(false))
    expect(button(d, 'button.test').disabled).toBe(false)
    button(d, 'button.save').click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', `/servers/${sid3}`)).toHaveLength(1))
    expect(sent(calls, 'PUT', `/servers/${sid3}`)[0]).toMatchObject({ name: 'lan-2', base_url: 'http://10.0.0.5:2053/x/', token: '', allow_private: true })
    expect(calls.some((c) => c.startsWith('POST /api/v1/servers/test'))).toBe(false)
  })

  it('shows why core refused a panel on a private network', async () => {
    const reason = 'only the store owner can let BOBRES reach a panel on a private network'
    const { calls } = await boot('/admin/servers', ownerPerms, (url, init) => {
      if (url === '/api/v1/servers/test' || (url === '/api/v1/servers' && init?.method === 'POST')) {
        return json(403, { error: 'forbidden', field: 'allow_private', message: reason })
      }
    }, 'owner')
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    ;(document.querySelector('button.add') as HTMLButtonElement).click()
    const d = await found('.server-dialog')
    type(d.querySelector('input.name'), 'lan-1')
    type(d.querySelector('input.base-url'), 'http://10.0.0.5:2053/x')
    type(d.querySelector('#server-token'), 'tok')
    ;(d.querySelector('#server-private') as HTMLInputElement).click()
    await vi.waitFor(() => expect(button(d, 'button.test').disabled).toBe(false))
    // the owner was made an admin meanwhile: core's reason shows at the switch
    button(d, 'button.test').click()
    await vi.waitFor(() => expect(d.querySelector('.test-result')?.textContent).toContain(text('error.forbidden')))
    expect(d.querySelector('.test-result')?.textContent).toContain(reason)
    expect(d.querySelector('.private-error')?.textContent).toBe(reason)
    expect(d.querySelector('.allow-private')?.classList.contains('p-invalid')).toBe(true)
    button(d, 'button.save').click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/servers')).toHaveLength(1))
    await vi.waitFor(() => expect(d.querySelector('.p-message.error')?.textContent).toContain(text('error.forbidden')))
    expect(d.querySelector('.p-message.error')?.textContent).toContain(reason)
    expect(d.querySelector('.private-error')?.textContent).toBe(reason)
  })

  it('asks to wait when connection tests come too fast', async () => {
    const { calls } = await boot('/admin/servers', adminPerms, (url) =>
      url === '/api/v1/servers/test' ? json(429, { error: 'rate_limited', message: 'at most 10 connection tests a minute' }) : undefined,
    )
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    button(rows()[0], 'button.edit').click()
    const d = await found('.server-dialog')
    button(d, 'button.test').click()
    await vi.waitFor(() => expect(d.querySelector('.test-result')?.textContent).toContain(text('error.rate_limited')))
    expect(d.querySelector('.test-result')?.textContent).not.toContain(text('error.generic'))
    expect(sent(calls, 'POST', '/servers/test')).toHaveLength(1)
  })

  it('deletes only a server without services, after a confirm', async () => {
    let inUse = false
    const { calls } = await boot('/admin/servers', adminPerms, (url, init) => {
      if (url === `/api/v1/servers/${sid3}` && init?.method === 'DELETE') {
        return inUse ? json(409, { error: 'state', message: 'the server still has clients' }) : new Response(null, { status: 204 })
      }
    })
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    button(rows()[2], 'button.delete').click()
    const accept = await found<HTMLButtonElement>('.p-confirmdialog-accept-button')
    expect(document.querySelector('.p-confirmdialog')?.textContent).toContain('de-3')
    expect(calls.some((c) => c.startsWith('DELETE'))).toBe(false)
    accept.click()
    await vi.waitFor(() => expect(calls).toContain(`DELETE /api/v1/servers/${sid3}`))
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('server.deleted')))

    // A service was created there meanwhile: core refuses.
    inUse = true
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).toBeNull())
    button(rows()[2], 'button.delete').click()
    ;(await found<HTMLButtonElement>('.p-confirmdialog-accept-button')).click()
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('server.in_use')))
  })

  it('chooses the inbounds new services join', async () => {
    const { calls } = await boot('/admin/servers', adminPerms, (url, init) => {
      if (url === `/api/v1/servers/${sid1}/inbounds`) {
        return json(200, { items: [{ id: 1, tag: 'inbound-443', protocol: 'vless', port: 443, enabled: true, attach: true }, { id: 2, tag: 'inbound-8443', enabled: false, attach: true }] })
      }
      if (url === `/api/v1/servers/${sid1}/inbounds/1` && init?.method === 'PUT') return json(200, { id: 1, tag: 'inbound-443', enabled: true, attach: false })
      if (url === `/api/v1/servers/${sid1}/inbounds/2` && init?.method === 'PUT') return json(503, { error: 'unavailable', message: 'provisioner down' })
    })
    await vi.waitFor(() => expect(rows()).toHaveLength(3))
    button(rows()[0], 'button.inbounds').click()
    const drawer = await found('.inbounds-drawer')
    await vi.waitFor(() => expect(drawer.querySelectorAll('.inbounds .p-datatable-tbody tr')).toHaveLength(2))
    expect(drawer.textContent).toContain(fill(text('server.inbounds_of'), { name: '\u2068de-1\u2069' }))
    expect(drawer.textContent).toContain(text('server.inbounds_note'))
    expect(drawer.textContent).toContain('inbound-8443')
    expect(drawer.textContent).toContain('vless:443')
    expect(drawer.textContent).toContain(text('server.inbound_off'))
    expect(drawer.querySelector('.none-attached')).toBeNull()
    const attach = (id: number) => drawer.querySelector(`.attach-${id} input`) as HTMLInputElement
    expect(attach(1).checked).toBe(true)
    attach(1).click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', `/servers/${sid1}/inbounds/1`)).toEqual([{ attach: false }]))
    // the only inbound on in the panel is now skipped: new services would fail
    await vi.waitFor(() => expect(drawer.querySelector('.none-attached')?.textContent).toBe(text('server.no_inbound')))
    expect(attach(1).checked).toBe(false)
    // a refused change goes back
    attach(2).click()
    await vi.waitFor(() => expect(drawer.querySelector('.save-error')?.textContent).toContain(text('error.unavailable')))
    await vi.waitFor(() => expect(attach(2).checked).toBe(true))
    expect(sent(calls, 'PUT', `/servers/${sid1}/inbounds/2`)).toEqual([{ attach: false }])
  })

  it('lets a plan choose its servers, and keeps them on every save', async () => {
    const plans = [plan('p1', { server_ids: [sid1, gone] }), plan('p2')]
    const { calls } = await boot('/admin/plans', adminPerms, (url, init) => {
      if (url === '/api/v1/plans' && (init?.method ?? 'GET') === 'GET') return json(200, { items: plans })
      if (url.startsWith('/api/v1/plans') && init?.method && init.method !== 'GET') return json(200, { plan: plans[0] })
    })
    const body = await found('.p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    await vi.waitFor(() => expect(body.querySelector('.plan-servers')?.textContent).toBe('de-1، ' + text('plan.server_removed')))
    expect(body.querySelectorAll('.plan-servers')[1].textContent).toBe(text('plan.servers_any'))

    // turning a plan off keeps its servers
    ;(body.querySelector('tr .p-toggleswitch input') as HTMLInputElement).click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/plans/p1')).toHaveLength(1))
    expect(sent(calls, 'PUT', '/plans/p1')[0]).toMatchObject({ enabled: false, server_ids: [sid1, gone] })

    // the editor: a removed server can be taken out; servers that are off are marked
    ;(body.querySelector('tr button[aria-label="' + text('common.edit') + '"]') as HTMLButtonElement).click()
    const d = await found('.p-dialog')
    const ms = await found<HTMLElement>('.p-dialog .servers')
    expect(ms.textContent).toContain('de-1')
    expect(ms.textContent).toContain(fill(text('plan.server_removed_id'), { id: '99999999' }))
    ms.click()
    const option = async (label: string) =>
      vi.waitFor(() => {
        const el = [...document.querySelectorAll('.p-multiselect-option')].find((o) => o.textContent?.trim() === label) as HTMLElement | undefined
        expect(el, label).toBeDefined()
        return el!
      })
    ;(await option('de-1')).click()
    ;(await option(fill(text('plan.server_removed_id'), { id: '99999999' }))).click()
    ;(await option(fill(text('plan.server_off'), { name: 'de-3' }))).click()
    // only an off server is left: orders would wait
    await vi.waitFor(() => expect(d.querySelector('.no-usable')?.textContent).toBe(text('plan.servers_none_usable')))
    ;(await option('de-2')).click()
    await vi.waitFor(() => expect(d.querySelector('.no-usable')).toBeNull())
    ;(d.querySelector('button[type=submit]') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/plans/p1')).toHaveLength(2))
    expect(sent(calls, 'PUT', '/plans/p1')[1].server_ids).toEqual([sid3, sid2])
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())

    // a new plan may use any server
    ;([...document.querySelectorAll('.app-page-head button')].find((b) => b.textContent?.includes(text('plan.new'))) as HTMLButtonElement).click()
    const d2 = await found('.p-dialog')
    const inputs = [...d2.querySelectorAll('input.p-inputtext')] as HTMLInputElement[]
    type(inputs[0], 'پلن تازه')
    type(d2.querySelector('input[inputmode=decimal]'), '20')
    await vi.waitFor(() => expect((d2.querySelector('button[type=submit]') as HTMLButtonElement).disabled).toBe(false))
    ;(d2.querySelector('button[type=submit]') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/plans')).toHaveLength(1))
    expect(sent(calls, 'POST', '/plans')[0].server_ids).toEqual([])
  })

  it('lets a plan choose its inbounds per server, warns about gone ones, and sends the choice back', async () => {
    const p2 = plan('p2', { server_ids: [sid1] })
    delete p2.inbounds
    const plans = [plan('p1', { server_ids: [sid1], inbounds: [{ server_id: sid1, inbound_ids: [2, 7] }] }), p2]
    const { calls } = await boot('/admin/plans', adminPerms, (url, init) => {
      const get = (init?.method ?? 'GET') === 'GET'
      if (get && url === '/api/v1/plans') return json(200, { items: plans })
      if (get && url === `/api/v1/servers/${sid1}/inbounds`) {
        return json(200, { items: [
          { id: 1, tag: 'inbound-443', enabled: true, attach: true },
          { id: 2, tag: 'inbound-8443', enabled: true, attach: false },
          { id: 3, tag: 'inbound-9000', enabled: false, attach: true },
        ] })
      }
      if (url.startsWith('/api/v1/plans') && !get) return json(200, { plan: plans[0] })
    })
    const body = await found('.p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    ;(body.querySelector('tr button[aria-label="' + text('common.edit') + '"]') as HTMLButtonElement).click()
    const d = await found('.p-dialog')
    const row = await found<HTMLElement>('.p-dialog .inbound-row')
    expect(row.dataset.server).toBe(sid1)
    // the chosen inbound shows by its tag, the one the panel lost by its number, with a warning
    await vi.waitFor(() => expect(row.textContent).toContain('inbound-8443'))
    expect(row.textContent).toContain(fill(text('plan.inbound_gone_id'), { id: '7' }))
    await vi.waitFor(() => expect(row.querySelector('.inbound-gone')).not.toBeNull())
    row.querySelector<HTMLElement>('.inbound-select')!.click()
    const option = async (label: string) =>
      vi.waitFor(() => {
        const el = [...document.querySelectorAll('.p-multiselect-option')].find((o) => o.textContent?.trim() === label) as HTMLElement | undefined
        expect(el, label).toBeDefined()
        return el!
      })
    ;(await option('inbound-443')).click()
    ;(await option(fill(text('plan.inbound_gone_id'), { id: '7' }))).click()
    await vi.waitFor(() => expect(row.querySelector('.inbound-gone')).toBeNull())
    // an inbound that is off in the panel is marked, not hidden
    await option(fill(text('plan.inbound_off'), { name: 'inbound-9000' }))
    ;(d.querySelector('button[type=submit]') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/plans/p1')).toHaveLength(1))
    expect(sent(calls, 'PUT', '/plans/p1')[0]).toMatchObject({ server_ids: [sid1], inbounds: [{ server_id: sid1, inbound_ids: [1, 2] }] })
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())

    // a plan that came without the choice (an older core) is saved without it too
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    ;(body.querySelectorAll('tr')[1].querySelector('button[aria-label="' + text('common.edit') + '"]') as HTMLButtonElement).click()
    const d2 = await found('.p-dialog')
    await vi.waitFor(() => expect(d2.querySelector('.inbound-row')).toBeNull())
    ;(d2.querySelector('button[type=submit]') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/plans/p2')).toHaveLength(1))
    expect(sent(calls, 'PUT', '/plans/p2')[0]).not.toHaveProperty('inbounds')
  })

  it('shows each server on the overview', async () => {
    const overview = (extra: Record<string, unknown>) => ({
      users: { total: 10, new_24h: 1, new_7d: 2 }, services: { active: 5, expiring: 1, ended: 0 }, orders: { paid_24h: 1, provision_failed: 0 },
      revenue: { day: [], month: [] }, pending_payments: 0, panel: { healthy: false, detail: 'one server is down' }, recent_orders: [],
      tickets: { waiting: 0, oldest_waiting_at: null }, ...extra,
    })
    const de1 = { id: sid1, name: 'de-1', healthy: true, detail: '', clients: 12, enabled: true }
    // switched off because its panel was down: it is not checked any more, so
    // the failed check it kept says nothing
    const de3off = { id: sid3, name: 'de-3', healthy: false, detail: 'connection refused', clients: 4, enabled: false }
    let extra: Record<string, unknown> = {
      servers: [de1, { id: sid2, name: 'de-2', healthy: false, detail: 'i/o timeout', clients: 3, enabled: true }, de3off],
      gateways: [
        { provider: 'zarinpal', enabled: true, healthy: false },
        { provider: 'manual_card', enabled: true, healthy: true },
        { provider: 'manual_crypto', enabled: true, healthy: false },
        { provider: 'manual_zarinpal', enabled: false, healthy: false },
        { provider: 'stars', enabled: true, healthy: true, ready: false },
      ],
    }
    await boot('/admin/', adminPerms, (url) => (url === '/api/v1/overview' ? json(200, overview(extra)) : undefined))
    const card = await found<HTMLElement>('.servers-card')
    expect(card.tagName).toBe('A') // admins open the Servers page from it
    expect(card.getAttribute('href')).toBe('/admin/servers')
    expect(card.querySelector('.servers-state')?.textContent).toBe(fill(text('overview.servers_down'), { n: '۱', total: '۲' }))
    expect(card.textContent).toContain('de-2')
    expect(card.textContent).toContain('i/o timeout')
    expect(card.textContent).not.toContain('de-1')
    expect(card.textContent).not.toContain('de-3')
    expect(card.textContent).not.toContain('connection refused')
    // its services still count
    expect(card.textContent).toContain(
      fill(text('overview.servers_clients'), { services: fill(text('overview.services_n'), { n: '۱۹' }), servers: fill(text('overview.servers_n'), { n: '۳' }) }),
    )
    // the single panel card gives way to the servers card
    expect(document.body.textContent).not.toContain(text('overview.panel'))
    // gateways are the owner's: shown, but not a link for an admin
    const gw = document.querySelector('.gateways-card') as HTMLElement
    expect(gw.tagName).toBe('DIV')
    const states = Object.fromEntries([...gw.querySelectorAll('.gateway')].map((g) => [g.querySelector('span')?.textContent, g.querySelector('.gateway-state')?.textContent]))
    expect(states).toEqual({
      [text('payment.provider.zarinpal')]: text('overview.gateway_state.down'),
      [text('payment.provider.manual_card')]: text('overview.gateway_state.ok'),
      // on, but its details are missing: not a failure (customers do not see it)
      [text('payment.provider.manual_crypto')]: text('overview.gateway_state.incomplete'),
      [text('payment.provider.manual_zarinpal')]: text('overview.gateway_state.off'),
      [text('payment.provider.stars')]: text('overview.gateway_state.incomplete'),
    })
    expect(gw.querySelectorAll('.gateway-state.p-tag-danger')).toHaveLength(1)

    // Every server switched off: none answers for new services, nobody is down.
    extra = { servers: [{ ...de1, enabled: false }, de3off] }
    await boot('/admin/', adminPerms, (url) => (url === '/api/v1/overview' ? json(200, overview(extra)) : undefined))
    const off = await found<HTMLElement>('.servers-card')
    expect(off.querySelector('.servers-state')?.textContent).toBe(text('overview.servers_all_off'))
    expect(off.querySelector('.servers-state')?.classList.contains('p-tag-warn')).toBe(true)
    expect(off.querySelectorAll('.down')).toHaveLength(0)

    // An older core without the switch: every server counts.
    extra = { servers: [{ ...de1, enabled: undefined }, { ...de3off, enabled: undefined }] }
    await boot('/admin/', adminPerms, (url) => (url === '/api/v1/overview' ? json(200, overview(extra)) : undefined))
    await vi.waitFor(() => expect(document.querySelector('.servers-card .servers-state')?.textContent).toBe(fill(text('overview.servers_down'), { n: '۱', total: '۲' })))

    // An older core without the list: the panel card as before.
    extra = {}
    await boot('/admin/', adminPerms, (url) => (url === '/api/v1/overview' ? json(200, overview(extra)) : undefined))
    await vi.waitFor(() => expect(document.body.textContent).toContain(text('overview.panel_down')))
    expect(document.querySelector('.servers-card')).toBeNull()
    expect(document.querySelector('.gateways-card')).toBeNull()
  })
})
