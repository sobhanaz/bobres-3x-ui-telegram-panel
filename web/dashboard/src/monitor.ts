import type { Calendar } from './calendar'
import type { Lang } from './format'
import { ALERT_FALLBACK, alertArgs, alertCount, alertTextKey, fills } from './system'

// The System page's Monitoring tab (docs/04-dashboard.md, "Monitoring API"): what core's
// /api/v1/monitor/{overview,series,metrics,events} answer, and the pure helpers the tab draws
// from. Unknown numbers are null (never zero), lists are lists, and a metric with no sample for a
// moment has no point: the charts draw that as a gap.

export type Range = '1h' | '6h' | '24h' | '7d' | '30d'
export const RANGES: Range[] = ['1h', '6h', '24h', '7d', '30d']
export const RANGE_SECONDS: Record<Range, number> = { '1h': 3600, '6h': 21_600, '24h': 86_400, '7d': 604_800, '30d': 2_592_000 }
/** the seconds one point of a series stands for, as core buckets them */
export const RANGE_STEP: Record<Range, number> = { '1h': 30, '6h': 60, '24h': 300, '7d': 1800, '30d': 7200 }

/** how often the overview and the events are asked again while the tab is shown (core shares one asking for 10 s) */
export const REFRESH_MS = 20_000
/** how often a range's series are asked again: no faster than a new point can appear */
export const SERIES_EVERY_S: Record<Range, number> = { '1h': 30, '6h': 60, '24h': 300, '7d': 900, '30d': 3600 }
/** the host collector command the page shows when it has no host figures */
export const ENABLE_COMMAND = 'sudo bobres monitor enable'
export const STATUS_COMMAND = 'sudo bobres monitor status'
/** core's limit of metrics in one series request */
export const SERIES_BATCH = 12

export const rangeOf = (v: unknown): Range => (RANGES.includes(v as Range) ? (v as Range) : '1h')

// ---------------------------------------------------------------- what core answers

export interface MonitorComponent {
  id: string
  name: string
  kind: string
  status: string
  detail: string
  version: string
  uptime_s: number | null
  since: number | null
}
export interface HostDisk {
  mount: string
  used: number
  total: number
  avail: number
  pct: number
  roles: string[]
}
export interface DockerDisk {
  images: number
  images_reclaimable: number
  containers: number
  volumes: number
  build_cache: number
  collected_at: number
}
export interface HostView {
  available: boolean
  reason: string
  hostname: string
  cpu_pct: number | null
  load: number[]
  cores: number
  mem: { used: number; total: number }
  swap: { used: number; total: number }
  disks: HostDisk[]
  net: { rx_bps: number | null; tx_bps: number | null }
  uptime_s: number
  os: string
  kernel: string
  docker: { ok: boolean; version: string; api: string; error: string }
  clock: { synchronized: boolean | null; ntp: boolean | null } | null
  docker_disk: DockerDisk | null
}
export interface Box {
  name: string
  project: string
  service: string
  image: string
  state: string
  health: string
  restarts: number
  started_at: number
  cpu_pct: number | null
  mem_bytes: number
  mem_limit: number
  net_rx_bytes: number
  net_tx_bytes: number
  oom_killed: boolean
  exit_code: number | null
}
export interface SystemdUnit {
  name: string
  load: string
  active: string
  sub: string
  enabled: string
  result: string
  since: number
  restarts: number
}
export interface KubeNode {
  name: string
  ready: boolean
  roles: string
  version: string
}
export interface KubePod {
  namespace: string
  name: string
  phase: string
  ready: boolean
  restarts: number
  node: string
}
export interface KubeView {
  detected: boolean
  reason: string
  nodes: KubeNode[]
  pods: KubePod[]
  truncated: boolean
}
export interface Counters {
  open_alerts: number | null
  errors_1h: number | null
  orders_pending: number | null
  payments_pending: number | null
  provision_queue: number | null
  outbox_backlog: number | null
  tickets_open: number | null
  services_active: number | null
  customers_total: number | null
}
export interface CiRun {
  workflow: string
  branch: string
  event: string
  status: string
  conclusion: string
  url: string
  sha: string
  at: number
}
export interface CiRelease {
  tag: string
  published_at: number
  prerelease: boolean
  url: string
}
export interface CiView {
  available: boolean
  checked_at: number
  error: string
  detail: string
  repo: string
  runs: CiRun[]
  release_runs: CiRun[]
  latest_release: CiRelease | null
  rate_reset_at: number
  main_failing: boolean | null
}
export interface CollectorView {
  installed: boolean
  fresh: boolean
  age_s: number
  error: string
  version: string
  generated_at: number
  interval_s: number
  duration_ms: number
  notes: string[]
}
export interface Deployment {
  id: string
  from: string
  to: string
  channel: string
  at: number
  finished_at: number
  outcome: string
  source: string
  by: string
  message: string
}
export interface Overview {
  generated_at: number
  status: string
  components: MonitorComponent[]
  host: HostView
  containers: Box[]
  units: SystemdUnit[]
  kubernetes: KubeView
  counters: Counters
  ci: CiView
  collector: CollectorView
  deployments: Deployment[]
}
export interface SeriesAnswer {
  metric: string
  unit: string
  step_s: number
  agg: string
  points: [number, number][]
}
export interface MetricInfo {
  metric: string
  unit: string
  agg: string
}
export interface EventItem {
  at: number
  level: string
  source: string
  kind: string
  key?: string
  state?: string
  title_key?: string
  args?: Record<string, string>
  title: string
  detail: string
}
export interface EventsReply {
  items: EventItem[]
  logs_visible: boolean
  unreachable: string[]
  truncated: boolean
}

const list = <T>(v: T[] | null | undefined): T[] => (Array.isArray(v) ? v : [])
const orNull = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null)

/** tidy fills in what an answer may leave out (an older core has no `units`, a snapshot-less host has
 * no disks), so the page never reads a hole. Lists stay lists and an unknown number stays null. */
export function tidy(o: Partial<Overview> | null | undefined): Overview {
  const h: Partial<HostView> = o?.host ?? {}
  const c: Partial<Counters> = o?.counters ?? {}
  const ci: Partial<CiView> = o?.ci ?? {}
  const k: Partial<KubeView> = o?.kubernetes ?? {}
  const col: Partial<CollectorView> = o?.collector ?? {}
  return {
    generated_at: o?.generated_at ?? 0,
    status: o?.status ?? '',
    components: list(o?.components),
    host: {
      available: h.available === true,
      reason: h.reason ?? '',
      hostname: h.hostname ?? '',
      cpu_pct: orNull(h.cpu_pct),
      load: list(h.load),
      cores: h.cores ?? 0,
      mem: h.mem ?? { used: 0, total: 0 },
      swap: h.swap ?? { used: 0, total: 0 },
      disks: list(h.disks).map((d) => ({ ...d, roles: list(d.roles) })),
      net: { rx_bps: orNull(h.net?.rx_bps), tx_bps: orNull(h.net?.tx_bps) },
      uptime_s: h.uptime_s ?? 0,
      os: h.os ?? '',
      kernel: h.kernel ?? '',
      docker: h.docker ?? { ok: false, version: '', api: '', error: '' },
      clock: h.clock ?? null,
      docker_disk: h.docker_disk ?? null,
    },
    containers: list(o?.containers).map((b) => ({ ...b, cpu_pct: orNull(b.cpu_pct), exit_code: orNull(b.exit_code) })),
    units: list(o?.units),
    kubernetes: { detected: k.detected === true, reason: k.reason ?? '', nodes: list(k.nodes), pods: list(k.pods), truncated: k.truncated === true },
    counters: {
      open_alerts: orNull(c.open_alerts),
      errors_1h: orNull(c.errors_1h),
      orders_pending: orNull(c.orders_pending),
      payments_pending: orNull(c.payments_pending),
      provision_queue: orNull(c.provision_queue),
      outbox_backlog: orNull(c.outbox_backlog),
      tickets_open: orNull(c.tickets_open),
      services_active: orNull(c.services_active),
      customers_total: orNull(c.customers_total),
    },
    ci: {
      available: ci.available === true,
      checked_at: ci.checked_at ?? 0,
      error: ci.error ?? '',
      detail: ci.detail ?? '',
      repo: ci.repo ?? '',
      runs: list(ci.runs),
      release_runs: list(ci.release_runs),
      latest_release: ci.latest_release ?? null,
      rate_reset_at: ci.rate_reset_at ?? 0,
      main_failing: typeof ci.main_failing === 'boolean' ? ci.main_failing : null,
    },
    collector: {
      installed: col.installed === true,
      fresh: col.fresh === true,
      age_s: col.age_s ?? 0,
      error: col.error ?? '',
      version: col.version ?? '',
      generated_at: col.generated_at ?? 0,
      interval_s: col.interval_s ?? 0,
      duration_ms: col.duration_ms ?? 0,
      notes: list(col.notes),
    },
    deployments: list(o?.deployments),
  }
}

// ---------------------------------------------------------------- status

export type Health = 'ok' | 'warn' | 'down' | 'unknown'
const HEALTH: Health[] = ['ok', 'warn', 'down', 'unknown']
/** healthOf reads a component's state; anything core might add later counts as not known */
export const healthOf = (s: string): Health => (HEALTH.includes(s as Health) ? (s as Health) : 'unknown')
export const healthSeverity: Record<Health, 'success' | 'warn' | 'danger' | 'secondary'> = { ok: 'success', warn: 'warn', down: 'danger', unknown: 'secondary' }
/** the state as an icon besides its colour and its word */
export const healthIcon: Record<Health, string> = { ok: 'pi pi-check-circle', warn: 'pi pi-exclamation-triangle', down: 'pi pi-times-circle', unknown: 'pi pi-question-circle' }

export type Overall = 'ok' | 'degraded' | 'down' | 'unknown'
export const overallOf = (s: string): Overall => (s === 'ok' || s === 'degraded' || s === 'down' ? s : 'unknown')
export const overallSeverity: Record<Overall, 'success' | 'warn' | 'danger' | 'secondary'> = { ok: 'success', degraded: 'warn', down: 'danger', unknown: 'secondary' }
export const overallIcon: Record<Overall, string> = { ok: 'pi pi-check-circle', degraded: 'pi pi-exclamation-triangle', down: 'pi pi-times-circle', unknown: 'pi pi-question-circle' }

/** problems are the components that make the whole worse: down first, then those that need a look. A
 * component whose state is not known never counts (a server never checked, a collector not installed). */
export function problems(o: Pick<Overview, 'components'>): MonitorComponent[] {
  const rank = (c: MonitorComponent) => (healthOf(c.status) === 'down' ? 0 : 1)
  return o.components.filter((c) => ['warn', 'down'].includes(healthOf(c.status))).sort((a, b) => rank(a) - rank(b))
}

export const COMPONENT_KINDS = ['service', 'datastore', 'host', 'job', 'panel', 'gateway'] as const
/** byKind groups the components in the order the page lists them; a kind core adds later goes last */
export function byKind(components: readonly MonitorComponent[]): { kind: string; items: MonitorComponent[] }[] {
  const kinds = [...new Set([...COMPONENT_KINDS.filter((k) => components.some((c) => c.kind === k)), ...components.map((c) => c.kind)])]
  return kinds.map((kind) => ({ kind, items: components.filter((c) => c.kind === kind) }))
}

/** the services core asks and the data stores: the ones the Services section lists */
export const isService = (c: Pick<MonitorComponent, 'kind'>) => c.kind === 'service' || c.kind === 'datastore'

// ---------------------------------------------------------------- the host collector

export type CollectorState = 'ready' | 'not_installed' | 'unusable' | 'quiet' | 'no_host'

/** collectorState says why the host figures are there or not: no snapshot file (not installed or switched
 * off), a file that cannot be used, a collector gone quiet, or fine. `no_host` is the rare case of a fresh
 * snapshot without a host part. */
export function collectorState(o: Pick<Overview, 'host' | 'collector'>): CollectorState {
  if (o.host.available) return 'ready'
  const c = o.collector
  if (!c.installed) return 'not_installed'
  if (c.error) return 'unusable'
  if (!c.fresh) return 'quiet'
  return 'no_host'
}

// ---------------------------------------------------------------- series on a time grid

/** gridTimes are the buckets of a range ending at `now`: oldest first, `step` seconds apart, the last one
 * the bucket `now` falls in. */
export function gridTimes(range: Range, now: number, step: number = RANGE_STEP[range]): number[] {
  const end = Math.floor(now / step) * step
  const n = Math.round(RANGE_SECONDS[range] / step)
  return Array.from({ length: n + 1 }, (_, i) => end - (n - i) * step)
}

/** onGrid puts a series' points on the grid: the value of a bucket where it has a point, null (a gap) where
 * nobody sampled. A point off the grid or not a number is dropped. */
export function onGrid(times: readonly number[], step: number, points: readonly (readonly [number, number])[] | null | undefined): (number | null)[] {
  const out: (number | null)[] = times.map(() => null)
  if (!times.length || !(step > 0)) return out
  for (const p of points ?? []) {
    if (!Array.isArray(p) || !Number.isFinite(p[0]) || typeof p[1] !== 'number' || !Number.isFinite(p[1])) continue
    const i = Math.round((p[0] - times[0]) / step)
    if (i >= 0 && i < times.length) out[i] = p[1]
  }
  return out
}

/** lastKnown is the newest measured value of a series, null when it has none */
export function lastKnown(values: readonly (number | null)[]): number | null {
  for (let i = values.length - 1; i >= 0; i--) if (values[i] !== null) return values[i]
  return null
}
/** peak is the highest measured value, null when there is none */
export function peak(values: readonly (number | null)[]): number | null {
  let top: number | null = null
  for (const v of values) if (v !== null && (top === null || v > top)) top = v
  return top
}

// ---------------------------------------------------------------- which metrics the page draws

export const HOST_METRICS = ['host.cpu_pct', 'host.mem_pct', 'host.swap_pct', 'host.load1', 'host.net_rx_bps', 'host.net_tx_bps']
export const STORE_METRICS = [
  'bot.updates_per_min', 'core.errors_per_min', 'bot.errors_per_min',
  'queue.provisioning', 'queue.outbox', 'queue.pending_orders', 'queue.pending_payments',
  'core.goroutines', 'core.db_conns',
]
/** the BOBRES services core asks, in the order the page lists them */
export const CORE_SERVICES = ['core', 'bot', 'payments', 'provisioner', 'backup']
const MAX_DISKS = 6
/** the chart kit has eight colours: a ninth thing would be a repeat */
export const MAX_SERIES = 8

/** subjects lists what a metric family has data for: the part after the colon, "/" and "/var/lib/docker"
 * for host.disk_pct. */
export function subjects(available: ReadonlySet<string>, family: string): string[] {
  return [...available].filter((m) => m.startsWith(family + ':')).map((m) => m.slice(family.length + 1)).sort()
}

/** orderServices lists service names the BOBRES way: the services core asks first, then the rest by name */
export function orderServices(names: readonly string[]): string[] {
  const rank = (n: string) => {
    const i = CORE_SERVICES.indexOf(n)
    return i < 0 ? CORE_SERVICES.length : i
  }
  return [...new Set(names)].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
}

/** The metrics the page asks for, out of those core has data for (so a bare install asks for little, and a
 * metric that does not exist never turns a whole request into a 400). */
export function wantedMetrics(available: ReadonlySet<string>): string[] {
  const out: string[] = HOST_METRICS.filter((m) => available.has(m))
  out.push(...subjects(available, 'host.disk_pct').slice(0, MAX_DISKS).map((m) => 'host.disk_pct:' + m))
  const boxes = orderServices(subjects(available, 'container.cpu_pct')).slice(0, MAX_SERIES)
  out.push(...boxes.map((s) => 'container.cpu_pct:' + s).filter((m) => available.has(m)))
  out.push(...boxes.map((s) => 'container.mem_bytes:' + s).filter((m) => available.has(m)))
  out.push(...STORE_METRICS.filter((m) => available.has(m)))
  out.push(...orderServices(subjects(available, 'service.latency_ms')).map((s) => 'service.latency_ms:' + s).filter((m) => available.has(m)))
  return out
}

/** batches cuts a list into the requests core accepts */
export function batches<T>(items: readonly T[], size = SERIES_BATCH): T[][] {
  const out: T[][] = []
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size))
  return out
}

// ---------------------------------------------------------------- numbers, sizes, durations and times

const SIZE_UNITS = ['byte', 'kilobyte', 'megabyte', 'gigabyte', 'terabyte'] as const
const intlLocale = (lang: Lang) => (lang === 'fa' ? 'fa-IR' : 'en-US')
const cache = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat | Intl.ListFormat>()
function memo<T extends Intl.NumberFormat | Intl.DateTimeFormat | Intl.ListFormat>(key: string, make: () => T): T {
  let v = cache.get(key) as T | undefined
  if (!v) {
    v = make()
    cache.set(key, v)
  }
  return v
}
const numberFormat = (lang: Lang, opts: Intl.NumberFormatOptions = {}) => memo(`n|${lang}|${JSON.stringify(opts)}`, () => new Intl.NumberFormat(intlLocale(lang), opts))

/** count writes a number in the language's digits, with at most `digits` decimals */
export const count = (n: number, lang: Lang, digits = 0): string => numberFormat(lang, { maximumFractionDigits: digits }).format(n)

/** percent writes a percentage with one decimal at most ("12.5%", "۱۲٫۵٪") */
export const pct = (n: number, lang: Lang): string => numberFormat(lang, { style: 'percent', maximumFractionDigits: 1 }).format(n / 100)

/** size writes bytes in B, KB, MB, GB or TB (1 KB = 1024 bytes, as the host counts), per second when asked;
 * the unit names are the language's own (Intl). */
export function size(n: number, lang: Lang, perSecond = false, digits = 1): string {
  let v = Math.max(0, n)
  let i = 0
  while (v >= 1024 && i < SIZE_UNITS.length - 1) {
    v /= 1024
    i++
  }
  const unit = SIZE_UNITS[i] + (perSecond ? '-per-second' : '')
  return numberFormat(lang, { style: 'unit', unit, unitDisplay: 'short', maximumFractionDigits: i === 0 ? 0 : digits }).format(v)
}

/** milliseconds writes a latency */
export const milliseconds = (n: number, lang: Lang): string =>
  numberFormat(lang, { style: 'unit', unit: 'millisecond', unitDisplay: 'short', maximumFractionDigits: n < 10 ? 1 : 0 }).format(n)

/** duration writes a length of time with its two largest units ("3 days 4 hours", "۳ روز و ۴ ساعت") */
export function duration(seconds: number, lang: Lang): string {
  let s = Math.max(0, Math.floor(seconds))
  const parts: [string, number][] = []
  for (const [unit, len] of [['day', 86_400], ['hour', 3600], ['minute', 60], ['second', 1]] as const) {
    const n = Math.floor(s / len)
    s -= n * len
    if (n > 0 || (unit === 'second' && !parts.length)) parts.push([unit, n])
    if (parts.length === 2) break
  }
  const words = parts.map(([unit, n]) => numberFormat(lang, { style: 'unit', unit, unitDisplay: 'long' }).format(n))
  return memo(`l|${lang}`, () => new Intl.ListFormat(intlLocale(lang), { type: 'unit', style: 'narrow' })).format(words)
}

const dateLocale = (lang: Lang, cal: Calendar) => (lang === 'fa' ? 'fa-IR' : 'en-GB') + '-u-ca-' + (cal === 'jalali' ? 'persian' : 'gregory')
const dateFormat = (lang: Lang, cal: Calendar, opts: Intl.DateTimeFormatOptions) =>
  memo(`d|${lang}|${cal}|${JSON.stringify(opts)}`, () => new Intl.DateTimeFormat(dateLocale(lang, cal), opts))

/** stamp writes an instant with its date (in the calendar chosen on the Reports page) and its time */
export const stamp = (unix: number, lang: Lang, cal: Calendar): string => dateFormat(lang, cal, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(unix * 1000))
/** clock writes the time of day, to the second */
export const clock = (unix: number, lang: Lang): string => dateFormat(lang, 'gregorian', { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(unix * 1000))
/** dayTime writes a point of a chart in full: the date and the time to the second */
export const pointStamp = (unix: number, lang: Lang, cal: Calendar): string =>
  dateFormat(lang, cal, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(unix * 1000))
/** tickLabel is the short name of a point on the time axis: the time of day within a day, the date beyond */
export function tickLabel(unix: number, range: Range, lang: Lang, cal: Calendar): string {
  const opts: Intl.DateTimeFormatOptions = RANGE_SECONDS[range] <= 86_400 ? { hour: '2-digit', minute: '2-digit' } : { month: 'short', day: 'numeric' }
  return dateFormat(lang, cal, opts).format(new Date(unix * 1000))
}

// ---------------------------------------------------------------- the formats of a chart's unit

export type Unit = 'percent' | 'bytes' | 'bytes_per_s' | 'count' | 'load' | 'per_min' | 'ms'
/** unitFormat writes a value of a chart's unit, in full (the tooltip) or short (the axis) */
export function unitFormat(unit: Unit, lang: Lang, short = false): (n: number) => string {
  switch (unit) {
    case 'percent':
      return (n) => pct(n, lang)
    case 'bytes':
      return (n) => size(n, lang, false, short ? 0 : 1)
    case 'bytes_per_s':
      return (n) => size(n, lang, true, short ? 0 : 1)
    case 'ms':
      return (n) => milliseconds(n, lang)
    case 'load':
      return (n) => count(n, lang, 2)
    case 'per_min':
      return (n) => count(n, lang, short ? 0 : 1)
    default:
      return (n) => count(n, lang)
  }
}

// ---------------------------------------------------------------- containers

/** versionOf is the tag of an image ("ghcr.io/sobhanaz/bobres-core:0.7.0" -> "0.7.0"); '' when it has none */
export function versionOf(image: string): string {
  const name = image.split('@')[0]
  const colon = name.lastIndexOf(':')
  return colon > name.lastIndexOf('/') ? name.slice(colon + 1) : ''
}
/** imageName is the image without its registry and tag ("ghcr.io/sobhanaz/bobres-core:0.7.0" -> "bobres-core") */
export function imageName(image: string): string {
  const name = image.split('@')[0]
  const colon = name.lastIndexOf(':')
  const bare = colon > name.lastIndexOf('/') ? name.slice(0, colon) : name
  return bare.slice(bare.lastIndexOf('/') + 1)
}

export type BoxState = 'running' | 'restarting' | 'paused' | 'created' | 'removing' | 'exited' | 'dead' | 'unknown'
const BOX_STATES: BoxState[] = ['running', 'restarting', 'paused', 'created', 'removing', 'exited', 'dead', 'unknown']
export const boxState = (b: Pick<Box, 'state'>): BoxState => (BOX_STATES.includes(b.state as BoxState) ? (b.state as BoxState) : 'unknown')
export const boxStateSeverity: Record<BoxState, 'success' | 'warn' | 'danger' | 'secondary'> = {
  running: 'success', restarting: 'danger', paused: 'warn', created: 'secondary', removing: 'warn', exited: 'danger', dead: 'danger', unknown: 'secondary',
}
export type BoxHealth = 'none' | 'starting' | 'healthy' | 'unhealthy'
export const boxHealth = (b: Pick<Box, 'health'>): BoxHealth => (['none', 'starting', 'healthy', 'unhealthy'].includes(b.health) ? (b.health as BoxHealth) : 'none')
export const boxHealthSeverity: Record<BoxHealth, 'success' | 'warn' | 'danger' | 'secondary'> = { none: 'secondary', starting: 'warn', healthy: 'success', unhealthy: 'danger' }

// ---------------------------------------------------------------- CI/CD

export type RunState = 'success' | 'failure' | 'running' | 'cancelled' | 'other'
/** runState reads a workflow run: passed, failed, still going, stopped, or anything else */
export function runState(r: Pick<CiRun, 'status' | 'conclusion'>): RunState {
  if (r.status !== 'completed') return r.status === 'queued' || r.status === 'in_progress' || r.status === 'waiting' || r.status === 'pending' ? 'running' : 'other'
  if (r.conclusion === 'success') return 'success'
  if (r.conclusion === 'cancelled' || r.conclusion === 'skipped') return 'cancelled'
  return r.conclusion === 'failure' || r.conclusion === 'timed_out' || r.conclusion === 'startup_failure' || r.conclusion === 'action_required' ? 'failure' : 'other'
}
export const runSeverity: Record<RunState, 'success' | 'danger' | 'info' | 'secondary'> = { success: 'success', failure: 'danger', running: 'info', cancelled: 'secondary', other: 'secondary' }
export const runIcon: Record<RunState, string> = { success: 'pi pi-check-circle', failure: 'pi pi-times-circle', running: 'pi pi-spin pi-spinner', cancelled: 'pi pi-ban', other: 'pi pi-question-circle' }

/** githubLink keeps a link only when it points to github.com over https (core builds them into the repository
 * already; the page does not trust a link it did not check); '' otherwise */
export function githubLink(url: string | undefined): string {
  try {
    const u = new URL(url ?? '')
    return u.protocol === 'https:' && u.hostname === 'github.com' && !u.username && !u.password ? u.href : ''
  } catch {
    return ''
  }
}

export type CiProblem = 'not_checked' | 'unreachable' | 'rate_limited' | 'invalid' | 'other'
/** ciProblem says what is wrong with the look at GitHub, null when nothing is */
export function ciProblem(ci: Pick<CiView, 'error'>): CiProblem | null {
  if (!ci.error) return null
  return ['not_checked', 'unreachable', 'rate_limited', 'invalid'].includes(ci.error) ? (ci.error as CiProblem) : 'other'
}

export const shortSha = (sha: string): string => (/^[0-9a-f]{7,40}$/i.test(sha) ? sha.slice(0, 7) : '')

// ---------------------------------------------------------------- deployments

export type Outcome = 'running' | 'done' | 'failed' | 'rolled_back' | 'not_started' | 'unknown'
const OUTCOMES: Outcome[] = ['running', 'done', 'failed', 'rolled_back', 'not_started', 'unknown']
export const outcomeOf = (d: Pick<Deployment, 'outcome'>): Outcome => (OUTCOMES.includes(d.outcome as Outcome) ? (d.outcome as Outcome) : 'unknown')
export const outcomeSeverity: Record<Outcome, 'success' | 'danger' | 'info' | 'warn' | 'secondary'> = {
  running: 'info', done: 'success', failed: 'danger', rolled_back: 'warn', not_started: 'secondary', unknown: 'secondary',
}

// ---------------------------------------------------------------- events

export type EventLevel = 'warn' | 'error'
export const levelOf = (e: Pick<EventItem, 'level'>): EventLevel => (e.level === 'error' ? 'error' : 'warn')
export const levelSeverity: Record<EventLevel, 'danger' | 'warn'> = { error: 'danger', warn: 'warn' }
export const EVENT_SOURCES = ['alert', 'core', 'bot', 'payments', 'provisioner', 'host.docker', 'host.bobres', 'host.caddy']
export type AlertState = 'open' | 'closed' | 'resolved'
export const alertStateOf = (e: Pick<EventItem, 'state'>): AlertState | null => (e.state === 'open' || e.state === 'closed' || e.state === 'resolved' ? e.state : null)

type Say = (key: string, ...args: unknown[]) => string
export interface Words {
  t: Say
  te: (key: string) => boolean
  tm: (key: string) => unknown
}

/** eventTitle writes an event: an alert in the language of the page, with the bot's own text and values (the
 * same sentence the Telegram message and the Health tab give); anything else, or an alert the page has no
 * text for, in core's words. */
export function eventTitle(e: EventItem, lang: Lang, w: Words): string {
  const own = e.kind === 'alert' ? alertTextKey({ title_key: e.title_key }) : ''
  if (own && w.te(own)) {
    const count = alertCount({ args: e.args })
    const limitName = (v: string) => (w.te('license.limit_name.' + v) ? w.t('license.limit_name.' + v, count ?? 2) : v)
    const named = (prefix: string, v: string) => (w.te(prefix + v) ? w.t(prefix + v) : v)
    const args = alertArgs({ key: e.key ?? '', args: e.args }, lang, (k, v) => (k === 'service' ? named('system.service.', v) : k === 'name' ? limitName(v) : v))
    if (fills(w.tm(own), args)) return count === undefined ? w.t(own, args) : w.t(own, args, count)
    if (e.title.trim()) return e.title.trim()
    return w.t(ALERT_FALLBACK, args)
  }
  return e.title.trim()
}
