import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { RANGE_STEP, stamp, type EventsReply, type MetricInfo, type Overview, type Range, type SeriesAnswer, type UptimeReply } from '../monitor'

// The System page's Monitoring tab, booted like the real app against a fake core: the overview and what it says
// when all is well, when something needs a look and when something is down; a server without the host
// collector; a cluster that is not there; GitHub that cannot be reached; the owner and the admin; charts with
// nothing to draw and with gaps; an API that fails; the range, the refresh and the browser tab going to the
// background; and the page in Persian.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const all = (root: ParentNode, sel: string) => [...root.querySelectorAll<HTMLElement>(sel)]
const q = (sel: string) => document.querySelector<HTMLElement>(sel)
const at = (o: unknown, path: string): string => {
  const s = path.split('.').reduce<unknown>((x, k) => (x as Record<string, unknown> | undefined)?.[k], o)
  if (typeof s !== 'string') throw new Error(`no text "${path}"`)
  return s
}
const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replaceAll('{' + k + '}', v), s)

const NOW = 1_792_500_000 // a multiple of 30, so the grid ends exactly here
const OWNER = ['overview.read', 'system.read', 'system.write', 'monitor.read']
const ADMIN = ['overview.read', 'system.read', 'monitor.read']
const NO_MONITOR = ['overview.read', 'system.read']

const comp = (id: string, name: string, kind: string, status = 'ok', extra: Record<string, unknown> = {}) => ({
  id, name, kind, status, detail: 'answers in 2 ms', version: '0.7.0', uptime_s: 86_400, since: NOW - 86_400, ...extra,
})
const box = (service: string, extra: Record<string, unknown> = {}) => ({
  name: `bobres-${service}-1`, project: 'bobres', service, image: `ghcr.io/sobhanaz/bobres-${service}:0.7.0`, state: 'running', health: 'healthy', restarts: 0,
  started_at: NOW - 7200, cpu_pct: 3.2, mem_bytes: 100 * 2 ** 20, mem_limit: 512 * 2 ** 20, net_rx_bytes: 123_456, net_tx_bytes: 234_567, oom_killed: false, exit_code: null, ...extra,
})
const run = (workflow: string, conclusion = 'success', extra: Record<string, unknown> = {}) => ({
  workflow, branch: 'main', event: 'push', status: 'completed', conclusion, url: 'https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/actions/runs/1', sha: 'abc1234def', at: NOW - 3600, ...extra,
})

function healthy(): Overview {
  return {
    generated_at: NOW,
    status: 'ok',
    components: [
      comp('core', 'Core', 'service'), comp('bot', 'Telegram bot', 'service'), comp('payments', 'Payments', 'service'), comp('provisioner', 'Provisioner', 'service'),
      comp('postgres', 'Postgres', 'datastore'), comp('redis', 'Redis', 'datastore'), comp('host', 'Server', 'host', 'ok', { detail: 'cpu 12%, memory 41%' }),
      comp('panel.s1', 'DE-1', 'panel', 'ok', { detail: '212 clients', uptime_s: null }), comp('gateway.zarinpal', 'zarinpal', 'gateway', 'ok', { uptime_s: null }),
      comp('collector', 'Host collector', 'job'), comp('backups', 'Backups', 'job'), comp('ci', 'CI of main', 'job'),
    ],
    host: {
      available: true, reason: '', hostname: 'srv1', cpu_pct: 12.5, load: [0.5, 0.4, 0.3], cores: 4, mem: { used: 3_400_000_000, total: 8_300_000_000 },
      swap: { used: 1_048_576, total: 1_073_741_824 },
      disks: [
        { mount: '/', used: 42_949_672_960, total: 107_374_182_400, avail: 59_055_800_320, pct: 42.1, roles: ['install'] },
        { mount: '/var/lib/docker', used: 96 * 2 ** 30, total: 100 * 2 ** 30, avail: 4 * 2 ** 30, pct: 96, roles: ['docker'] },
      ],
      net: { rx_bps: 18_432.5, tx_bps: 20_211.1 }, uptime_s: 1_209_600, os: 'Ubuntu 24.04.5 LTS', kernel: '6.8.0-45-generic',
      docker: { ok: true, version: '29.1.0', api: '1.52', error: '' }, clock: { synchronized: true, ntp: true },
      docker_disk: { images: 3_200_000_000, images_reclaimable: 800_000_000, containers: 12_000_000, volumes: 900_000_000, build_cache: 0, collected_at: NOW - 100 },
    },
    containers: ['core', 'bot', 'payments', 'provisioner', 'postgres', 'redis', 'caddy', 'tls-agent'].map((s) => box(s)),
    units: [
      { name: 'bobres-monitor.timer', load: 'loaded', active: 'active', sub: 'waiting', enabled: 'enabled', result: '', since: NOW - 5000, restarts: 0 },
      { name: 'docker.service', load: 'loaded', active: 'active', sub: 'running', enabled: 'enabled', result: '', since: NOW - 90_000, restarts: 0 },
    ],
    kubernetes: { detected: false, reason: 'not detected', nodes: [], pods: [], truncated: false },
    counters: { open_alerts: 0, errors_1h: 0, orders_pending: 2, payments_pending: 1, provision_queue: 0, outbox_backlog: 0, tickets_open: 4, services_active: 212, customers_total: 640 },
    ci: {
      available: true, checked_at: NOW - 60, error: '', detail: '', repo: 'sobhanaz/bobres-3x-ui-telegram-panel', runs: [run('CI')], release_runs: [run('Release', 'success', { branch: 'v0.7.0' })],
      latest_release: { tag: 'v0.7.0', published_at: NOW - 86_400, prerelease: false, url: 'https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.7.0' },
      rate_reset_at: 0, main_failing: false,
    },
    collector: { installed: true, fresh: true, age_s: 12, error: '', version: '0.7.0', generated_at: NOW - 12, interval_s: 30, duration_ms: 180, notes: [] },
    deployments: [
      { id: 'd1', from: '0.6.1', to: '0.7.0', channel: 'stable', at: NOW - 90_000, finished_at: NOW - 89_820, outcome: 'done', source: 'dashboard', by: 'Sam', message: '' },
      { id: 'cli-1', from: '0.6.0', to: '0.6.1', channel: '', at: NOW - 900_000, finished_at: 0, outcome: 'unknown', source: 'cli', by: '', message: '' },
    ],
  }
}

/** a server without the host collector: no figures, never zeros */
function withoutCollector(): Overview {
  const o = healthy()
  o.components = o.components.filter((c) => !['host', 'collector'].includes(c.id)).concat([comp('collector', 'Host collector', 'job', 'unknown', { detail: 'not installed', uptime_s: null, version: '' })])
  o.host = { ...o.host, available: false, reason: 'the host collector is not installed or is switched off: sudo bobres monitor enable', hostname: '', cpu_pct: null, load: [0, 0, 0], cores: 0, mem: { used: 0, total: 0 }, swap: { used: 0, total: 0 }, disks: [], net: { rx_bps: null, tx_bps: null }, uptime_s: 0, os: '', kernel: '', docker: { ok: false, version: '', api: '', error: '' }, clock: null, docker_disk: null }
  o.containers = []
  o.units = []
  o.collector = { installed: false, fresh: false, age_s: 0, error: '', version: '', generated_at: 0, interval_s: 0, duration_ms: 0, notes: [] }
  return o
}

const eventsReply = (over: Partial<EventsReply> = {}): EventsReply => ({ items: [], logs_visible: true, unreachable: [], truncated: false, ...over })
const diskAlert = { at: NOW - 600, level: 'warn', source: 'alert', kind: 'alert', key: 'monitor.disk:/', state: 'open', title_key: 'alert.monitor_disk', args: { mount: '/', percent: '88', free: String(12 * 2 ** 30) }, title: 'The disk at / is 88% full (12 GB free).', detail: '87.8% of 98.0 GiB used' }
const journal = { at: NOW - 700, level: 'error', source: 'host.docker', kind: 'journal', title: 'level=error msg="failed to start"', detail: '' }
const botLog = { at: NOW - 800, level: 'error', source: 'bot', kind: 'log', title: 'send failed', detail: '{"user_id":42}' }

const METRICS: MetricInfo[] = [
  ['host.cpu_pct', 'percent'], ['host.mem_pct', 'percent'], ['host.swap_pct', 'percent'], ['host.load1', 'load'], ['host.net_rx_bps', 'bytes_per_s'], ['host.net_tx_bps', 'bytes_per_s'],
  ['host.disk_pct:/', 'percent'], ['host.disk_pct:/var/lib/docker', 'percent'],
  ['container.cpu_pct:core', 'percent'], ['container.cpu_pct:bot', 'percent'], ['container.mem_bytes:core', 'bytes'], ['container.mem_bytes:bot', 'bytes'],
  ['bot.updates_per_min', 'per_min'], ['core.errors_per_min', 'per_min'], ['bot.errors_per_min', 'per_min'], ['queue.pending_orders', 'count'], ['queue.pending_payments', 'count'],
  ['queue.provisioning', 'count'], ['queue.outbox', 'count'], ['core.goroutines', 'count'], ['core.db_conns', 'count'],
  ['service.latency_ms:core', 'ms'], ['service.latency_ms:bot', 'ms'],
].map(([metric, unit]) => ({ metric, unit, agg: 'avg' }))

/** ten minutes of samples on the step of the range, with a hole of two minutes in the middle, ending at NOW */
const points = (value: number, step = 30, gap = true): [number, number][] => {
  const out: [number, number][] = []
  for (let t = NOW - 600; t <= NOW; t += step) if (!(gap && t >= NOW - 360 && t < NOW - 240)) out.push([t, value])
  return out
}

/** the availability of a component over a window: a bar of cells of [observed, down] seconds */
const avail = (pct: number | null, extra: Record<string, unknown> = {}) => ({
  availability_pct: pct, observed_s: pct === null ? 0 : 86_400, window_s: 86_400, outages: 0, down_s: 0, longest_s: 0,
  bar: Array.from({ length: 48 }, () => (pct === null ? [0, 0] : [1800, 0])), ...extra,
})
const comp3 = (id: string, name: string, a24: Record<string, unknown>, extra: Record<string, unknown> = {}) => ({
  id, name, windows: { '24h': a24, '7d': { ...a24, window_s: 604_800, bar: Array.from({ length: 56 }, () => [10_800, 0]) }, '30d': { ...a24, window_s: 2_592_000, bar: Array.from({ length: 60 }, () => [43_200, 0]) } }, ...extra,
})
/** what core answers for the uptime: the services all up, one panel with an outage, one switched off, one new */
function uptimeReply(over: Partial<UptimeReply> = {}): UptimeReply {
  const outage = avail(99.653, { outages: 2, down_s: 300, longest_s: 240, observed_s: 86_000, bar: Array.from({ length: 48 }, (_, i) => (i === 10 ? [1800, 300] : [1800, 0])) })
  return {
    generated_at: NOW,
    retention_days: 30,
    sample_every_s: 30,
    since: NOW - 3 * 86_400,
    windows: [{ name: '24h', span_s: 86_400, bucket_s: 1800 }, { name: '7d', span_s: 604_800, bucket_s: 10_800 }, { name: '30d', span_s: 2_592_000, bucket_s: 43_200 }],
    services: ['core', 'bot', 'payments', 'provisioner'].map((id) => comp3(id, id, avail(100))),
    panels: [comp3('s1', 'DE-1', outage, { enabled: true }), comp3('s2', 'NL-1', avail(null), { enabled: false }), comp3('s3', 'Brand new', avail(null), { enabled: true })],
    panels_unavailable: false,
    status_page: false,
    ...over,
  } as unknown as UptimeReply
}

interface Env {
  perms?: string[]
  lang?: 'en' | 'fa'
  uptime?: () => Response
  overview?: () => Response
  events?: (url: string) => Response
  metrics?: () => Response
  series?: (url: string, names: string[]) => Response
  path?: string
}

async function boot(env: Env = {}) {
  unmount()
  const lang = env.lang ?? 'en'
  localStorage.setItem('bobres.lang', lang)
  vi.resetModules()
  window.history.replaceState(null, '', env.path ?? '/admin/system?tab=monitoring')
  document.body.innerHTML = '<div id="app"></div>'
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
  const calls: string[] = []
  const fetchMock = vi.fn(async (url: string) => {
    calls.push(url)
    if (url === '/api/v1/me') {
      return json(200, {
        user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'owner', language: lang }, permissions: env.perms ?? OWNER, csrf: 'c',
        session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
      })
    }
    if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
    if (url === '/api/v1/monitor/overview') return env.overview?.() ?? json(200, healthy())
    if (url === '/api/v1/monitor/uptime') return env.uptime?.() ?? json(200, uptimeReply())
    if (url.startsWith('/api/v1/monitor/events')) return env.events?.(url) ?? json(200, eventsReply())
    if (url === '/api/v1/monitor/metrics') return env.metrics?.() ?? json(200, { metrics: METRICS })
    if (url.startsWith('/api/v1/monitor/series')) {
      const p = new URL(url, 'https://x.example').searchParams
      const names = (p.get('metrics') ?? '').split(',')
      if (env.series) return env.series(url, names)
      const step = RANGE_STEP[(p.get('range') ?? '1h') as Range]
      const series: SeriesAnswer[] = names.map((metric) => ({ metric, unit: 'percent', step_s: step, agg: 'avg', points: points(40, step) }))
      return json(200, { range: p.get('range'), series })
    }
    return json(503, { error: 'unavailable', message: 'not in this test' })
  })
  vi.stubGlobal('fetch', fetchMock)
  await import('../main')
  const { router } = await import('../router')
  await router.isReady()
  return { fetchMock, router, calls }
}

const found = async <T extends Element>(selector: string) =>
  vi.waitFor(() => {
    const el = document.querySelector<T>(selector)
    expect(el, selector).not.toBeNull()
    return el!
  }, { timeout: 8000 })
const urls = (f: { mock: { calls: unknown[][] } }) => f.mock.calls.map((c) => String(c[0]))
const count = (calls: string[], prefix: string) => calls.filter((u) => u.startsWith(prefix)).length
const press = (container: string, label: string) => {
  const b = all(document, `${container} [role="button"], ${container} button, ${container} [role="radio"]`).find((e) => text(e) === label)
  expect(b, label).toBeDefined()
  b!.click()
}
/** the rows of a table, once the reading has filled it (before that it holds a row saying there is nothing) */
const rowsOf = (selector: string, n: number) =>
  vi.waitFor(() => {
    const rows = all(document, selector)
    expect(rows).toHaveLength(n)
    return rows
  }, { timeout: 8000 })
const chartOf = (title: string) =>
  vi.waitFor(() => {
    const el = all(document, '.metric-chart').find((c) => text(c.querySelector('.title')) === title)
    expect(el, title).toBeDefined()
    return el!
  }, { timeout: 8000 })
/** the numbers of a chart as its table gives them: a row per point */
const chartRows = async (chart: HTMLElement) => {
  const btn = chart.querySelector<HTMLButtonElement>('.table-btn')!
  if (btn.getAttribute('aria-pressed') !== 'true') btn.click()
  await vi.waitFor(() => expect(chart.querySelector('.chart-table')).not.toBeNull())
  return all(chart, '.chart-table tbody tr').map((r) => [...r.children].map(text))
}

afterEach(() => {
  unmount()
  try {
    localStorage.removeItem('bobres.lang')
    localStorage.removeItem('bobres.monitor.range')
    localStorage.removeItem('bobres.monitor.uptime')
    localStorage.removeItem('bobres.reports.calendar')
  } catch {
    /* nothing was stored */
  }
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
  vi.doUnmock('../monitor')
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('the overview', () => {
  it('says everything is working, lists every part by kind and gives the store figures', async () => {
    const { calls } = await boot()
    await found('#monitor-status .verdict')
    expect(text(q('#monitor-status .verdict-title'))).toBe(en.monitor.overall.ok)
    expect(text(q('#monitor-status .verdict-note'))).toBe(fill(at(en, 'monitor.overall_note.ok').split(' | ')[1], { n: '12' }))
    expect(q('#monitor-status .reasons')).toBeNull()
    // the parts, in the kinds the page groups them by, with their state in words
    expect(all(document, '#monitor-status .group').map((g) => g.getAttribute('data-kind'))).toEqual(['service', 'datastore', 'host', 'job', 'panel', 'gateway'])
    const core = q('#monitor-status .chip[data-id="core"]')!
    expect(text(core.querySelector('.chip-name'))).toBe(en.monitor.component.core)
    expect(text(core.querySelector('.state-tag'))).toBe(en.monitor.state.ok)
    expect(text(q('#monitor-status .chip[data-id="gateway.zarinpal"]'))).toContain(at(en, 'payment.provider.zarinpal'))
    expect(text(q('#monitor-status .chip[data-id="panel.s1"]'))).toContain('DE-1')
    // the store's own figures; one that core could not read is shown as such, not as 0
    expect(all(document, '#monitor-status .tile').map((t) => text(t.querySelector('dd')))).toEqual(['0', '0', '2', '1', '0', '0', '4', '212', '640'])
    expect(urls({ mock: { calls: calls.map((c) => [c]) } }).some((u) => u === '/api/v1/monitor/overview')).toBe(true)
  })

  it('names what needs a look when degraded, worst first, and points to the alerts', async () => {
    const o = healthy()
    o.status = 'degraded'
    o.components = o.components.map((c) => (c.id === 'caddy' ? c : c))
    o.components.push(comp('caddy', 'Caddy (web gateway)', 'service', 'warn', { detail: 'running, but its health check fails' }))
    o.components.find((c) => c.id === 'redis')!.status = 'down'
    o.components.find((c) => c.id === 'redis')!.detail = 'not running'
    o.counters.open_alerts = 2
    await boot({ overview: () => json(200, o) })
    await found('#monitor-status .reasons')
    expect(text(q('#monitor-status .verdict-title'))).toBe(en.monitor.overall.degraded)
    const reasons = all(document, '#monitor-status .reason')
    expect(reasons.map((r) => r.getAttribute('data-id'))).toEqual(['redis', 'caddy']) // down first, then the one that needs a look
    expect(text(reasons[0])).toContain(en.monitor.state.down)
    expect(text(reasons[0])).toContain('not running')
    expect(text(reasons[1])).toContain(en.monitor.state.warn)
    expect(text(q('#monitor-status .open-alerts'))).toContain('2 alerts are open.')
    expect(q('#monitor-status .see-alerts')).not.toBeNull()
  })

  it('says something important is down when core says down', async () => {
    const o = healthy()
    o.status = 'down'
    o.components.find((c) => c.id === 'core')!.status = 'down'
    await boot({ overview: () => json(200, o) })
    await found('#monitor-status .reasons')
    expect(text(q('#monitor-status .verdict-title'))).toBe(en.monitor.overall.down)
    expect(q('#monitor-status .verdict-icon.danger')).not.toBeNull()
  })

  it('shows a figure core could not read as not known, never as zero', async () => {
    const o = healthy()
    o.counters.tickets_open = null
    await boot({ overview: () => json(200, o) })
    await found('#monitor-status .tiles')
    const tile = q('#monitor-status .tile[data-key="tickets_open"] dd')!
    expect(text(tile)).toContain(en.monitor.unknown_value)
    expect(text(tile)).not.toBe('0')
  })
})

describe('the host', () => {
  it('shows the figures of the server, every disk with its share, and what Docker keeps', async () => {
    await boot()
    await found('#monitor-host .tile.cpu')
    expect(text(q('#monitor-host .tile.cpu dd'))).toBe('12.5%')
    expect(text(q('#monitor-host .tile.load dd'))).toBe('0.5 / 0.4 / 0.3')
    expect(text(q('#monitor-host .tile.mem dd'))).toBe('41%') // 3.4 of 8.3 GB
    expect(text(q('#monitor-host .tile.uptime dd'))).toBe('14 days')
    const disks = all(document, '#monitor-host .disk')
    expect(disks.map((d) => d.getAttribute('data-mount'))).toEqual(['/', '/var/lib/docker'])
    expect(text(disks[1].querySelector('.disk-pct'))).toBe('96%')
    expect(disks[1].querySelector('[role="progressbar"]')?.getAttribute('aria-valuenow')).toBe('96')
    expect(disks[1].querySelector('.p-progressbar')?.className).toContain('full') // 96% is past the line
    expect(text(q('#monitor-host .about-card'))).toContain('Ubuntu 24.04.5 LTS')
    expect(q('#monitor-host .collector-notice')).toBeNull()
  })

  it('says the collector is not installed, with the command, and shows no zeros for the server', async () => {
    await boot({ overview: () => json(200, withoutCollector()) })
    await found('#monitor-host .collector-notice')
    expect(text(q('#monitor-host .notice-title'))).toBe(en.monitor.collector.not_installed)
    expect(text(q('#monitor-host .enable-command'))).toBe('sudo bobres monitor enable')
    expect(q('#monitor-host .status-command')).toBeNull() // nothing to check yet: it is not there
    // no tiles of zeros: the figures are simply not there
    expect(q('#monitor-host .tile.cpu')).toBeNull()
    expect(q('#monitor-host .disks')).toBeNull()
    // the containers and the units say what they need, rather than "none"
    expect(text(q('#monitor-docker .no-data'))).toBe(en.monitor.docker.needs_collector)
    expect(text(q('#monitor-services .no-units'))).toBe(en.monitor.services.units_need_collector)
    // the overview does not call the store degraded for it, and says the figures are missing
    expect(text(q('#monitor-status .verdict-title'))).toBe(en.monitor.overall.ok)
    expect(text(q('#monitor-status .no-host'))).toContain(en.monitor.no_host_note)
  })

  it('says since when the collector has been quiet, with both commands', async () => {
    const o = withoutCollector()
    o.collector = { installed: true, fresh: false, age_s: 245, error: '', version: '0.7.0', generated_at: NOW - 245, interval_s: 30, duration_ms: 100, notes: [] }
    o.host.reason = 'the collector sent nothing for 4 minutes: sudo bobres monitor status'
    await boot({ overview: () => json(200, o) })
    await found('#monitor-host .collector-notice')
    expect(text(q('#monitor-host .notice-title'))).toBe(fill(at(en, 'monitor.collector.quiet'), { age: '4 minutes 5 seconds' }))
    expect(text(q('#monitor-host .enable-command'))).toBe('sudo bobres monitor enable')
    expect(text(q('#monitor-host .status-command'))).toBe('sudo bobres monitor status')
    expect(text(q('#monitor-host .reason'))).toContain('sent nothing for 4 minutes')
  })

  it('says when the collector writes a file core cannot use', async () => {
    const o = withoutCollector()
    o.collector = { installed: true, fresh: false, age_s: 10, error: 'invalid', version: '', generated_at: 0, interval_s: 0, duration_ms: 0, notes: [] }
    await boot({ overview: () => json(200, o) })
    await found('#monitor-host .collector-notice')
    expect(text(q('#monitor-host .notice-title'))).toBe(fill(at(en, 'monitor.collector.unusable'), { error: 'invalid' }))
  })
})

describe('docker and kubernetes', () => {
  it('lists the containers with state, health, image version, memory and restarts', async () => {
    const o = healthy()
    o.containers[1] = box('bot', { state: 'restarting', health: 'unhealthy', restarts: 5, oom_killed: true, cpu_pct: null })
    o.containers[2] = box('payments', { state: 'exited', exit_code: 137, health: 'none', mem_limit: 0 })
    await boot({ overview: () => json(200, o) })
    const rows = await rowsOf('#monitor-docker .containers tbody tr', 8)
    const core = [...rows[0].children].map(text)
    expect(core[0]).toContain('core')
    expect(core[1]).toContain('0.7.0')
    expect(core[1]).toContain('bobres-core')
    expect(core[2]).toBe(en.monitor.docker.state.running)
    expect(core[3]).toBe(en.monitor.docker.health.healthy)
    expect(core[4]).toBe('0')
    expect(core[7]).toContain('100 MB / 512 MB')
    const bot = [...rows[1].children].map(text)
    expect(bot[2]).toContain(en.monitor.docker.state.restarting)
    expect(bot[2]).toContain(en.monitor.docker.oom)
    expect(bot[3]).toBe(en.monitor.docker.health.unhealthy)
    expect(bot[4]).toBe('5')
    expect(bot[6]).toBe('—') // CPU not known: not a zero
    const payments = [...rows[2].children].map(text)
    expect(payments[2]).toContain(fill(at(en, 'monitor.docker.exit'), { code: '137' }))
    expect(payments[7]).toBe('—') // stopped: no memory to show
    expect(text(q('#monitor-docker .docker-disk-card'))).toContain('3.2 GB'.replace('3.2', '3'))
  })

  it('says a cluster is not detected as a plain note, not an error', async () => {
    await boot()
    await found('#monitor-kubernetes .not-detected')
    expect(text(q('#monitor-kubernetes .not-detected'))).toBe(en.monitor.kube.none)
    expect(q('#monitor-kubernetes .pods')).toBeNull()
    expect(q('#monitor-kubernetes [role="alert"]')).toBeNull()
    expect(q('#monitor-kubernetes .p-message-error')).toBeNull()
  })

  it('lists nodes and pods when the collector found a cluster', async () => {
    const o = healthy()
    o.kubernetes = {
      detected: true, reason: '', truncated: true,
      nodes: [{ name: 'node-1', ready: true, roles: 'control-plane', version: 'v1.31.2' }],
      pods: [
        { namespace: 'default', name: 'web-0', phase: 'Running', ready: true, restarts: 0, node: 'node-1' },
        { namespace: 'kube-system', name: 'dns-1', phase: 'Pending', ready: false, restarts: 3, node: 'node-1' },
      ],
    }
    await boot({ overview: () => json(200, o) })
    expect(q('#monitor-kubernetes .not-detected')).toBeNull()
    await rowsOf('#monitor-kubernetes .nodes tbody tr', 1)
    const pods = await rowsOf('#monitor-kubernetes .pods tbody tr', 2)
    expect(text(pods[1])).toContain('dns-1')
    expect(text(pods[1])).toContain('Pending')
    expect(text(q('#monitor-kubernetes .truncated'))).toBe(en.monitor.kube.truncated)
  })
})

describe('services and the units', () => {
  it('lists the services with version and uptime, and the watched systemd units in their own words', async () => {
    await boot()
    const rows = await rowsOf('#monitor-services .services-table tbody tr', 6) // 4 services and the 2 data stores
    expect(text(rows[0])).toContain(en.monitor.component.core)
    expect(text(rows[0])).toContain('0.7.0')
    expect(text(rows[0])).toContain('1 day')
    const units = await rowsOf('#monitor-services .units-table tbody tr', 2)
    expect(text(units[0])).toContain('bobres-monitor.timer')
    expect(text(units[0])).toContain('active (waiting)')
    expect(text(units[0])).toContain(en.monitor.services.unit.monitor_timer)
  })
})

describe('CI/CD', () => {
  it('shows the latest runs and release with links into GitHub only', async () => {
    const o = healthy()
    o.ci.runs = [run('CI'), run('CI', 'failure', { url: 'https://evil.example/runs/2', sha: '0123456789' })]
    await boot({ overview: () => json(200, o) })
    const rows = await rowsOf('#monitor-cicd .runs-table.runs tbody tr', 2)
    expect(rows[0].querySelector('a')?.getAttribute('href')).toBe('https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/actions/runs/1')
    expect(rows[0].querySelector('a')?.getAttribute('rel')).toBe('noopener noreferrer')
    expect(text(rows[0].querySelector('.run-state'))).toBe(en.monitor.ci.run.success)
    expect(text(rows[1].querySelector('.run-state'))).toBe(en.monitor.ci.run.failure)
    expect(rows[1].querySelector('a')).toBeNull() // a link that is not into GitHub is not a link
    expect(text(rows[1])).toContain('0123456')
    expect(q('#monitor-cicd .release-card a')?.getAttribute('href')).toBe('https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.7.0')
    expect(q('#monitor-cicd .ci-problem')).toBeNull()
    expect(q('#monitor-cicd .main-failing')).toBeNull()
  })

  it('says GitHub could not be reached, and keeps what was last read', async () => {
    const o = healthy()
    o.ci.error = 'unreachable'
    o.ci.detail = 'dial tcp: i/o timeout'
    o.ci.main_failing = true
    await boot({ overview: () => json(200, o) })
    await found('#monitor-cicd .ci-problem')
    expect(q('#monitor-cicd .ci-problem')?.getAttribute('data-problem')).toBe('unreachable')
    expect(text(q('#monitor-cicd .ci-problem'))).toContain(en.monitor.ci.problem.unreachable)
    expect(text(q('#monitor-cicd .ci-problem'))).toContain('dial tcp: i/o timeout')
    expect(text(q('#monitor-cicd .main-failing'))).toBe(en.monitor.ci.main_failing)
    expect(await rowsOf('#monitor-cicd .runs-table.runs tbody tr', 1)).toHaveLength(1) // the earlier answer is still shown
    expect(await rowsOf('#monitor-cicd .runs-table.release_runs tbody tr', 1)).toHaveLength(1)
  })

  it('says when GitHub has no run to show yet, and when its hourly limit is used up', async () => {
    const o = healthy()
    o.ci = { ...o.ci, available: false, error: 'rate_limited', runs: [], release_runs: [], latest_release: null, checked_at: 0, rate_reset_at: NOW + 1800, main_failing: null }
    await boot({ overview: () => json(200, o) })
    await found('#monitor-cicd .ci-problem')
    expect(q('#monitor-cicd .ci-problem')?.getAttribute('data-problem')).toBe('rate_limited')
    expect(text(q('#monitor-cicd .ci-problem'))).toContain('hourly limit')
    expect(all(document, '#monitor-cicd .no-runs')).toHaveLength(2)
    expect(q('#monitor-cicd .release-card')).toBeNull()
  })
})

describe('deployments', () => {
  it('lists the updates with who asked, the outcome and how long they took', async () => {
    await boot()
    const rows = await rowsOf('#monitor-deployments .deployments tbody tr', 2)
    expect(text(rows[0])).toContain('0.6.1 → 0.7.0')
    expect(text(rows[0])).toContain(en.monitor.deployments.outcome.done)
    expect(text(rows[0])).toContain('Sam')
    expect(text(rows[0])).toContain('3 minutes') // finished 180 s after it began
    expect(text(rows[1])).toContain(en.monitor.deployments.source.cli)
    expect(text(rows[1])).toContain(en.monitor.deployments.outcome.unknown)
  })

  it('says there is none yet', async () => {
    const o = healthy()
    o.deployments = []
    await boot({ overview: () => json(200, o) })
    await found('#monitor-deployments .no-deployments')
    expect(text(q('#monitor-deployments .no-deployments'))).toBe(en.monitor.deployments.none)
  })
})

describe('alerts and logs: the owner and the admin', () => {
  it('lets the owner see alerts, the host journal and the services own lines, in the language of the page', async () => {
    const { calls } = await boot({ events: () => json(200, eventsReply({ items: [diskAlert, journal, botLog] })) })
    await found('#monitor-events .event')
    const items = all(document, '#monitor-events .event')
    expect(items).toHaveLength(3)
    // the alert is written as the Telegram message and the Health tab write it, from the bot's text key and values
    expect(text(items[0].querySelector('.event-title'))).toBe('The disk at \u2068/\u2069 is 88% full (12 GB free).')
    expect(text(items[0].querySelector('.event-state'))).toBe(en.monitor.events.state.open)
    expect(text(items[0].querySelector('.event-source'))).toBe(en.monitor.source.alert)
    expect(text(items[1].querySelector('.event-source'))).toBe(en.monitor.source.host_docker)
    expect(text(items[2].querySelector('.event-source'))).toBe(en.monitor.source.bot)
    expect(text(items[2].querySelector('.event-level'))).toBe(en.monitor.events.level_tag.error)
    expect(items[2].textContent).toContain('"user_id":42')
    expect(q('#monitor-events .owner-only')).toBeNull()
    expect(calls).toContain('/api/v1/monitor/events?level=warn&limit=100')
  })

  it('folds identical lines into one entry: how many there were, and when the first and the last came', async () => {
    const folded = { ...journal, at: NOW - 100, first_at: NOW - 3700, count: 12 }
    const noSpan = { ...journal, source: 'host.caddy', title: 'caddy: tls handshake error', at: NOW - 200, count: 3 } // core gave a count but no first time
    const once = { ...botLog, count: 1 } // one line is no repeat, whatever core writes
    await boot({ events: () => json(200, eventsReply({ items: [diskAlert, folded, noSpan, once, botLog] })) })
    await found('#monitor-events .event')
    const items = all(document, '#monitor-events .event')
    expect(items).toHaveLength(5)
    // a single line looks as it did: no badge, no span
    for (const i of [0, 3, 4]) {
      expect(items[i].querySelector('.event-repeat'), String(i)).toBeNull()
      expect(items[i].querySelector('.event-range'), String(i)).toBeNull()
    }
    // the folded entry: ×12 (a screen reader is told in words), the time of the newest line, and the span it covers
    const badge = items[1].querySelector('.event-repeat')!
    expect(text(badge)).toBe('×12')
    expect(badge.getAttribute('aria-hidden')).toBe('true')
    expect(text(items[1].querySelector('.sr-only'))).toBe('Repeated 12 times')
    expect(text(items[1].querySelector('.event-time'))).toBe(stamp(NOW - 100, 'en', 'gregorian'))
    expect(text(items[1].querySelector('.event-range'))).toBe(
      fill(en.monitor.events.repeat_range, { first: '\u2068' + stamp(NOW - 3700, 'en', 'gregorian') + '\u2069', last: '\u2068' + stamp(NOW - 100, 'en', 'gregorian') + '\u2069' }),
    )
    expect(text(items[1].querySelector('.event-title'))).toBe(journal.title)
    // without the first time there is the count and no span made up
    expect(text(items[2].querySelector('.event-repeat'))).toBe('×3')
    expect(items[2].querySelector('.event-range')).toBeNull()
  })

  it('tells the admin the services lines are the owner\'s, and shows the rest', async () => {
    await boot({ perms: ADMIN, events: () => json(200, eventsReply({ items: [diskAlert, journal], logs_visible: false })) })
    await found('#monitor-events .event')
    expect(all(document, '#monitor-events .event')).toHaveLength(2)
    expect(text(q('#monitor-events .owner-only'))).toBe(en.monitor.events.owner_only)
    // everything else on the tab is the same for the admin: read only
    expect(q('#monitor-status .verdict')).not.toBeNull()
    expect(q('#monitor-host .tile.cpu')).not.toBeNull()
  })

  it('names the services whose lines could not be read, and filters by level', async () => {
    const { calls } = await boot({ events: (url) => json(200, eventsReply({ items: url.includes('level=error') ? [journal] : [diskAlert, journal], unreachable: ['payments'] })) })
    await found('#monitor-events .event')
    expect(text(q('#monitor-events .unreachable'))).toBe(fill(at(en, 'monitor.events.unreachable'), { list: at(en, 'system.service.payments') }))
    press('#monitor-events .level-filter', en.monitor.events.level.error)
    await vi.waitFor(() => expect(all(document, '#monitor-events .event')).toHaveLength(1))
    expect(calls).toContain('/api/v1/monitor/events?level=error&limit=100')
  })

  it('says all is clear when there is nothing, and never shows the tab to a role without monitor.read', async () => {
    await boot()
    await found('#monitor-events .all-clear')
    expect(text(q('#monitor-events .all-clear'))).toBe(en.monitor.events.none)
    unmount()
    const { calls } = await boot({ perms: NO_MONITOR })
    await found('.tab-health')
    expect(q('.tab-monitoring')).toBeNull()
    expect(q('.monitoring')).toBeNull() // ?tab=monitoring falls back to Health
    expect(calls.some((u) => u.startsWith('/api/v1/monitor/'))).toBe(false)
  })
})

describe('the charts and their gaps', () => {
  it('draws the history of the range as lines that break where nothing was sampled', async () => {
    const { calls } = await boot()
    await found('.metric-chart')
    await vi.waitFor(() => expect(all(document, '.metric-chart svg').length).toBeGreaterThan(5))
    // which metrics have data is asked first; only those are requested, a dozen at a time at most, for the range chosen
    expect(calls).toContain('/api/v1/monitor/metrics')
    const asked = calls.filter((u) => u.startsWith('/api/v1/monitor/series'))
    expect(asked.length).toBeGreaterThanOrEqual(2)
    for (const u of asked) {
      const p = new URL(u, 'https://x.example').searchParams
      expect(p.get('range')).toBe('1h')
      expect((p.get('metrics') ?? '').split(',').length).toBeLessThanOrEqual(12)
    }
    expect(asked.some((u) => decodeURIComponent(u).includes('host.disk_pct:/var/lib/docker'))).toBe(true)
    // the CPU chart: a point for every 30 seconds of the hour; the 2 minutes without samples are gaps, not zeros
    const cpu = await chartOf(at(en, 'monitor.host.cpu'))
    const rows = await chartRows(cpu)
    expect(rows).toHaveLength(121)
    const empty = rows.filter((r) => r[1] === en.charts.no_data)
    expect(empty.length).toBeGreaterThan(100) // the hour before the samples, and the hole in the middle
    const measured = rows.filter((r) => r[1] !== en.charts.no_data)
    expect(measured.length).toBe(points(40).length)
    expect(measured.every((r) => r[1] === '40%')).toBe(true)
    // the line itself is cut at the hole: two runs of segments
    expect(cpu.querySelectorAll('svg .series path[fill="none"]').length).toBe(2)
  })

  it('says so when a metric has no sample in the period, and when nothing was ever recorded', async () => {
    await boot({
      series: (_url, names) => json(200, { range: '1h', series: names.map((metric) => ({ metric, unit: 'percent', step_s: 30, agg: 'avg', points: [] })) }),
    })
    await found('#monitor-host .charts')
    await vi.waitFor(() => expect(all(document, '#monitor-host .metric-chart .empty').length).toBeGreaterThan(0))
    expect(text(q('#monitor-host .metric-chart .empty'))).toBe(en.monitor.chart.empty)
    unmount()
    await boot({ metrics: () => json(200, { metrics: [] }) })
    await found('#monitor-host .no-history')
    expect(text(q('#monitor-host .no-history'))).toBe(en.monitor.chart.no_history)
    expect(q('#monitor-host .metric-chart')).toBeNull()
    expect(text(q('#monitor-store .no-history'))).toBe(en.monitor.chart.no_history)
  })

  it('keeps the numbers when the history cannot be read, and says so', async () => {
    await boot({ series: () => json(500, { error: 'internal' }) })
    await found('.series-failed')
    expect(text(q('.series-failed'))).toBe(en.monitor.series_error)
    expect(q('#monitor-host .tile.cpu')).not.toBeNull()
  })

  it('asks the other range, and remembers it', async () => {
    const { calls } = await boot()
    await found('.metric-chart')
    press('.range-switch', en.monitor.range['24h'])
    await vi.waitFor(() => expect(calls.some((u) => u.startsWith('/api/v1/monitor/series') && u.includes('range=24h'))).toBe(true))
    expect(localStorage.getItem('bobres.monitor.range')).toBe('24h')
    // the grid is the one of the new range: a point every 5 minutes for a day
    await vi.waitFor(async () => expect(await chartRows(await chartOf(at(en, 'monitor.host.cpu')))).toHaveLength(289), { timeout: 8000 })
  })
})

describe('refreshing', () => {
  it('asks again by itself, and stops when the browser tab goes to the background', async () => {
    vi.doMock('../monitor', async (orig) => ({ ...(await orig<typeof import('../monitor')>()), REFRESH_MS: 120 }))
    const { calls } = await boot()
    await found('#monitor-status .verdict')
    await vi.waitFor(() => expect(count(calls, '/api/v1/monitor/overview')).toBeGreaterThanOrEqual(3), { timeout: 4000 })
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' })
    document.dispatchEvent(new Event('visibilitychange'))
    await new Promise((r) => setTimeout(r, 300)) // a refresh already on its way may finish
    const before = count(calls, '/api/v1/monitor/overview')
    await new Promise((r) => setTimeout(r, 500))
    expect(count(calls, '/api/v1/monitor/overview')).toBe(before)
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.waitFor(() => expect(count(calls, '/api/v1/monitor/overview')).toBeGreaterThan(before), { timeout: 3000 })
  })

  it('stops when switched off, and refreshes on request', async () => {
    vi.doMock('../monitor', async (orig) => ({ ...(await orig<typeof import('../monitor')>()), REFRESH_MS: 120 }))
    const { calls } = await boot()
    await found('#monitor-status .verdict')
    const sw = await found<HTMLInputElement>('#monitor-auto')
    sw.click()
    await new Promise((r) => setTimeout(r, 300))
    const before = count(calls, '/api/v1/monitor/overview')
    await new Promise((r) => setTimeout(r, 500))
    expect(count(calls, '/api/v1/monitor/overview')).toBe(before)
    q('.monitoring .refresh')!.click()
    await vi.waitFor(() => expect(count(calls, '/api/v1/monitor/overview')).toBeGreaterThan(before))
  })
})

describe('when the API fails', () => {
  it('shows the error and nothing made up when the first reading fails', async () => {
    await boot({ overview: () => json(500, { error: 'internal' }) })
    await found('.load-error')
    expect(text(q('.load-error'))).toContain(en.monitor.load_error)
    expect(text(q('.load-error'))).toContain(en.error.generic)
    expect(q('#monitor-status .verdict')).toBeNull() // no verdict without a reading
    expect(q('.verdict-loading')).toBeNull() // and no "loading" that would never end
    expect(q('#monitor-host')).toBeNull() // what is drawn from the overview is left out
    expect(all(document, '.jump button').map(text)).toEqual([en.monitor.section.store, en.monitor.section.uptime, en.monitor.section.events]) // the links only lead to what is there
    expect(q('#monitor-uptime')).not.toBeNull() // the uptime has an answer of its own
    expect(q('#monitor-events')).not.toBeNull() // the events have an answer of their own
  })

  it('says a refused reading is a refusal', async () => {
    await boot({ overview: () => json(403, { error: 'forbidden' }) })
    await found('.load-error')
    expect(text(q('.load-error'))).toContain(en.error.forbidden)
  })

  it('says a build of core without the monitor has nothing to show, as a note', async () => {
    await boot({ overview: () => json(503, { error: 'not_configured' }) })
    await found('.not-configured')
    expect(text(q('.not-configured'))).toBe(en.monitor.not_configured)
    expect(q('.load-error')).toBeNull()
    expect(q('#monitor-status')).toBeNull()
  })

  it('keeps the last reading on screen when a later one fails', async () => {
    vi.doMock('../monitor', async (orig) => ({ ...(await orig<typeof import('../monitor')>()), REFRESH_MS: 120 }))
    let fail = false
    await boot({ overview: () => (fail ? json(500, { error: 'internal' }) : json(200, healthy())) })
    await found('#monitor-status .verdict')
    fail = true
    await found('.load-error')
    expect(text(q('.load-error'))).toContain(en.monitor.load_error)
    expect(q('#monitor-status .verdict')).not.toBeNull() // the old reading stays
    expect(text(q('.load-error'))).toContain('Showing what was read at')
    fail = false
    await vi.waitFor(() => expect(q('.load-error')).toBeNull(), { timeout: 4000 })
  })
})

describe('in Persian', () => {
  it('writes the verdict, the figures and the alerts in Persian, with Persian digits, right to left', async () => {
    const o = healthy()
    o.status = 'degraded'
    o.components.find((c) => c.id === 'redis')!.status = 'warn'
    o.counters.customers_total = 1234
    await boot({ lang: 'fa', overview: () => json(200, o), events: () => json(200, eventsReply({ items: [diskAlert] })) })
    await found('#monitor-status .verdict')
    expect(document.documentElement.dir).toBe('rtl')
    expect(text(q('#monitor-status .verdict-title'))).toBe(fa.monitor.overall.degraded)
    expect(text(q('#monitor-status .reason'))).toContain(fa.monitor.state.warn)
    expect(text(q('#monitor-status .tile[data-key="customers_total"] dd'))).toBe('۱٬۲۳۴')
    expect(text(q('#monitor-host .tile.cpu dd'))).toBe('۱۲٫۵٪')
    expect(text(q('#monitor-host .tile.uptime dd'))).toBe('۱۴ روز')
    await found('#monitor-events .event')
    // the alert in Persian, from the bot's own text and values (digits in Persian, the mount kept whole)
    expect(text(q('#monitor-events .event-title'))).toBe(at(fa, 'system.alerts.kind.monitor_disk').replace('{mount}', '⁨/⁩').replace('{percent}', '۸۸').replace('{free}', '۱۲ گیگابایت'))
    expect(text(q('#monitor-events .event-state'))).toBe(fa.monitor.events.state.open)
    expect(text(q('.range-switch'))).toContain(fa.monitor.range['6h'])
    expect(q('#monitor-docker')).not.toBeNull()
    expect(text(q('#monitor-kubernetes .not-detected'))).toBe(fa.monitor.kube.none)
  })

  it('writes the repeat of a folded entry in Persian, with Persian digits and the Persian calendar', async () => {
    const folded = { ...journal, at: NOW - 100, first_at: NOW - 3700, count: 12 }
    await boot({ lang: 'fa', events: () => json(200, eventsReply({ items: [folded] })) })
    await found('#monitor-events .event')
    expect(text(q('#monitor-events .event-repeat'))).toBe('×۱۲')
    expect(text(q('#monitor-events .sr-only'))).toBe(fill(fa.monitor.events.repeat_hint, { n: '۱۲' }))
    expect(text(q('#monitor-events .event-range'))).toBe(
      fill(fa.monitor.events.repeat_range, { first: '\u2068' + stamp(NOW - 3700, 'fa', 'jalali') + '\u2069', last: '\u2068' + stamp(NOW - 100, 'fa', 'jalali') + '\u2069' }),
    )
    expect(text(q('#monitor-events .event-range'))).toMatch(/[۰-۹]/)
  })

  it('writes the collector notice in Persian but keeps the command whole and left to right', async () => {
    await boot({ lang: 'fa', overview: () => json(200, withoutCollector()) })
    await found('#monitor-host .collector-notice')
    expect(text(q('#monitor-host .notice-title'))).toBe(fa.monitor.collector.not_installed)
    const cmd = q('#monitor-host .enable-command')!
    expect(text(cmd)).toBe('sudo bobres monitor enable')
    expect(cmd.className).toContain('app-ltr')
  })
})

describe('the uptime', () => {
  const rowsIn = (card: string) => all(document, `#monitor-uptime ${card} tbody tr`)
  const cells = (id: string) => all(document, `#monitor-uptime .bar[data-id="${id}"] .cell`)

  it('lists the services and the panel servers with their availability, outages and a bar', async () => {
    await boot()
    await vi.waitFor(() => expect(rowsIn('.services-card')).toHaveLength(4), { timeout: 8000 })
    expect(text(q('#monitor-uptime h2'))).toBe(en.monitor.section.uptime)
    // the services, named as the rest of the tab names them; all of them up the whole day
    const services = rowsIn('.services-card')
    expect(services.map((r) => text(r.querySelector('.name')))).toEqual([en.monitor.component.core, en.monitor.component.bot, en.monitor.component.payments, en.monitor.component.provisioner])
    expect(services.map((r) => text(r.querySelector('.pct')))).toEqual(['100%', '100%', '100%', '100%'])
    expect(services.map((r) => text(r.querySelector('.no-outages')))).toEqual(Array(4).fill(en.monitor.uptime.no_outages))
    // the panel servers: one with two outages, one switched off, one added just now
    const panels = rowsIn('.panels-card')
    expect(panels.map((r) => text(r.querySelector('.name')))).toEqual(['DE-1', 'NL-1', 'Brand new'])
    expect(text(panels[0].querySelector('.pct'))).toBe('99.653%')
    expect(text(panels[0].querySelector('.outages'))).toBe('2 outages, 5 minutes down')
    expect(text(panels[0].querySelector('.longest'))).toBe(fill(en.monitor.uptime.longest, { time: '4 minutes' }))
    expect(text(panels[1].querySelector('.off-tag'))).toBe(en.monitor.uptime.off)
    // no data is written as such: no percentage, no "0%", no outages
    for (const r of [panels[1], panels[2]]) {
      expect(text(r.querySelector('.no-data'))).toBe(en.monitor.uptime.no_data)
      expect(r.querySelector('.pct')).toBeNull()
      expect(r.querySelector('.outages, .no-outages')).toBeNull()
    }
    // the bar: a cell for each half hour of the day; the one with the outage is partial, a server nobody sampled is all no data
    expect(cells('s1')).toHaveLength(48)
    expect(cells('s1').map((c) => c.className.replace('cell', '').trim()).filter((c) => c !== 'up')).toEqual(['partial'])
    expect(cells('s1')[10].className).toContain('partial')
    expect(cells('s2').every((c) => c.className.includes('nodata'))).toBe(true)
    expect(cells('s1').every((c) => c.getAttribute('aria-hidden') === 'true')).toBe(true)
    // a screen reader gets the row in one sentence instead of the cells
    expect(q('#monitor-uptime .bar[data-id="s1"]')!.getAttribute('role')).toBe('img')
    expect(q('#monitor-uptime .bar[data-id="s1"]')!.getAttribute('aria-label')).toBe('DE-1, 24 hours: 99.653%, 2 outages, 5 minutes down')
    expect(q('#monitor-uptime .bar[data-id="s2"]')!.getAttribute('aria-label')).toBe(`NL-1, 24 hours: ${en.monitor.uptime.no_data}`)
    // the legend names all four kinds of cell
    expect(all(document, '#monitor-uptime .legend li').map(text)).toEqual([en.monitor.uptime.legend.up, en.monitor.uptime.legend.partial, en.monitor.uptime.legend.down, en.monitor.uptime.legend.nodata])
    // and the jump link leads here
    expect(all(document, '.jump button').map(text)).toContain(en.monitor.section.uptime)
  })

  it('says how much of the period was observed when part of it has no data', async () => {
    const r = uptimeReply()
    r.services[0].windows['24h'] = avail(100, { observed_s: 43_200 }) as never
    await boot({ uptime: () => json(200, r) })
    await vi.waitFor(() => expect(rowsIn('.services-card')).toHaveLength(4), { timeout: 8000 })
    expect(text(rowsIn('.services-card')[0].querySelector('.observed'))).toBe(fill(en.monitor.uptime.observed, { time: '12 hours', window: '1 day' }))
    expect(rowsIn('.services-card')[1].querySelector('.observed')).toBeNull() // observed all day: nothing to say
  })

  it('asks for the other periods, draws their cells and remembers the choice', async () => {
    await boot()
    await vi.waitFor(() => expect(cells('s1')).toHaveLength(48), { timeout: 8000 })
    press('.window-switch', en.monitor.range['7d'])
    await vi.waitFor(() => expect(cells('s1')).toHaveLength(56))
    expect(localStorage.getItem('bobres.monitor.uptime')).toBe('7d')
    expect(q('#monitor-uptime .bar[data-id="s1"]')!.getAttribute('aria-label')).toContain(en.monitor.range['7d'])
    press('.window-switch', en.monitor.range['30d'])
    await vi.waitFor(() => expect(cells('s1')).toHaveLength(60))
    // the range of the charts above is not the uptime's
    expect(localStorage.getItem('bobres.monitor.range')).toBeNull()
  })

  it('opens on the period it remembered', async () => {
    localStorage.setItem('bobres.monitor.uptime', '30d')
    await boot()
    await vi.waitFor(() => expect(cells('s1')).toHaveLength(60), { timeout: 8000 })
    localStorage.setItem('bobres.monitor.uptime', 'forever') // a value that is not a period is the default
    await boot()
    await vi.waitFor(() => expect(cells('s1')).toHaveLength(48), { timeout: 8000 })
  })

  it('states how long the samples are kept and since when there are any', async () => {
    await boot()
    await found('#monitor-uptime .retention')
    expect(text(q('#monitor-uptime .retention'))).toContain('Core keeps 30 days of samples: one every 30 seconds for the first 7 days, then 5-minute averages. Older time is not shown.')
    expect(text(q('#monitor-uptime .retention'))).toContain('The oldest sample is from')
    expect(text(q('#monitor-uptime .intro'))).toContain('A moment nobody sampled')
    unmount()
    await boot({ uptime: () => json(200, uptimeReply({ since: null, services: [], panels: [] })) })
    await found('#monitor-uptime .retention')
    expect(text(q('#monitor-uptime .retention'))).toContain(en.monitor.uptime.none_yet)
    expect(text(q('#monitor-uptime .no-panels'))).toBe(en.monitor.uptime.no_panels)
  })

  it('says when the panel servers could not be listed, instead of listing none', async () => {
    await boot({ uptime: () => json(200, uptimeReply({ panels: [], panels_unavailable: true })) })
    await found('#monitor-uptime .panels-unavailable')
    expect(text(q('#monitor-uptime .panels-unavailable'))).toBe(en.monitor.uptime.panels_unavailable)
    expect(q('#monitor-uptime .no-panels')).toBeNull()
  })

  it('points to the public status page when it is on, and says how to turn it on when it is off', async () => {
    await boot()
    await found('#monitor-uptime .status-off')
    expect(text(q('#monitor-uptime .status-off'))).toBe(en.monitor.uptime.status_off)
    expect(q('#monitor-uptime .status-page a')).toBeNull()
    unmount()
    await boot({ uptime: () => json(200, uptimeReply({ status_page: true })) })
    await found('#monitor-uptime .status-on')
    const link = q('#monitor-uptime .status-on a') as HTMLAnchorElement
    expect(link.getAttribute('href')).toBe('/status')
    expect(link.getAttribute('rel')).toContain('noopener')
    expect(text(q('#monitor-uptime .status-on'))).toBe(en.monitor.uptime.status_on + ' /status')
  })

  it('says the uptime could not be read, and the rest of the tab keeps working', async () => {
    await boot({ uptime: () => json(500, { error: 'internal' }) })
    await found('#monitor-uptime .uptime-failed')
    expect(text(q('#monitor-uptime .uptime-failed'))).toBe(en.monitor.uptime.load_error)
    expect(q('#monitor-uptime .services-card')).toBeNull() // nothing made up
    expect(q('#monitor-uptime .loading')).toBeNull() // and no "loading" that never ends
    await found('#monitor-status .verdict')
    expect(q('.load-error')).toBeNull()
  })

  it('asks for the uptime once a minute, not with every refresh of the overview', async () => {
    vi.doMock('../monitor', async (orig) => ({ ...(await orig<typeof import('../monitor')>()), REFRESH_MS: 120 }))
    const { calls } = await boot()
    await found('#monitor-status .verdict')
    await vi.waitFor(() => expect(count(calls, '/api/v1/monitor/overview')).toBeGreaterThanOrEqual(4), { timeout: 4000 })
    expect(count(calls, '/api/v1/monitor/uptime')).toBe(1)
    // "Check again" asks for it at once
    q('.monitoring .refresh')!.click()
    await vi.waitFor(() => expect(count(calls, '/api/v1/monitor/uptime')).toBe(2), { timeout: 4000 })
  })

  it('is written in Persian, with Persian digits', async () => {
    await boot({ lang: 'fa' })
    await vi.waitFor(() => expect(rowsIn('.panels-card')).toHaveLength(3), { timeout: 8000 })
    expect(text(q('#monitor-uptime h2'))).toBe(fa.monitor.section.uptime)
    const panels = rowsIn('.panels-card')
    expect(text(panels[0].querySelector('.pct'))).toBe('۹۹٫۶۵۳٪')
    expect(text(panels[0].querySelector('.outages'))).toBe(fill(fa.monitor.uptime.outages, { n: '۲', time: '۵ دقیقه' }))
    expect(text(rowsIn('.services-card')[0].querySelector('.pct'))).toBe('۱۰۰٪')
    expect(text(panels[1].querySelector('.no-data'))).toBe(fa.monitor.uptime.no_data)
    expect(text(q('#monitor-uptime .status-off'))).toBe(fa.monitor.uptime.status_off)
  })
})
