import { describe, expect, it } from 'vitest'
import {
  batches, byKind, ciProblem, collectorState, count, duration, eventTitle, githubLink, gridTimes, healthOf, imageName, lastKnown, onGrid, orderServices, outcomeOf, overallOf,
  peak, pct, problems, rangeOf, repeatOf, runState, shortSha, size, subjects, tickLabel, tidy, unitFormat, versionOf, wantedMetrics, type EventItem, type MonitorComponent,
} from '../monitor'
import en from '../locales/en.json'

// The pure parts of the Monitoring tab: the time grid a series is put on (gaps stay gaps), which metrics the
// page asks for, the states, and how sizes, durations and values are written in each language.

const c = (id: string, kind: string, status: string): MonitorComponent => ({ id, name: id, kind, status, detail: '', version: '', uptime_s: null, since: null })

describe('the time grid', () => {
  it('ends at the bucket now falls in and is one step apart, a point more than the steps in the range', () => {
    const t = gridTimes('1h', 1_792_500_017)
    expect(t).toHaveLength(121)
    expect(t[120]).toBe(1_792_500_000)
    expect(t[0]).toBe(1_792_500_000 - 3600)
    expect(t[1] - t[0]).toBe(30)
    expect(gridTimes('30d', 1_792_500_017)).toHaveLength(361) // every 2 hours for 30 days
    expect(gridTimes('24h', 1_792_500_017, 300)).toHaveLength(289)
  })

  it('puts the points where their bucket is, and leaves a gap (null) where nobody sampled', () => {
    const times = gridTimes('1h', 1_792_500_000)
    const v = onGrid(times, 30, [[1_792_500_000 - 60, 5], [1_792_500_000, 7]])
    expect(v[120]).toBe(7)
    expect(v[118]).toBe(5)
    expect(v[119]).toBeNull() // the bucket between had no sample: a gap, never 0
    expect(v.filter((x) => x !== null)).toHaveLength(2)
    expect(lastKnown(v)).toBe(7)
    expect(peak(v)).toBe(7)
    expect(lastKnown([null, null])).toBeNull()
    expect(peak([null])).toBeNull()
  })

  it('drops what is off the grid or not a number, and an empty answer is all gaps', () => {
    const times = gridTimes('1h', 1_792_500_000)
    expect(onGrid(times, 30, [[0, 1], [1_792_500_000 + 3600, 2], [1_792_500_000, Number.NaN], [1_792_500_000, null as unknown as number]]).every((x) => x === null)).toBe(true)
    expect(onGrid(times, 30, [])).toHaveLength(121)
    expect(onGrid(times, 30, undefined).every((x) => x === null)).toBe(true)
    expect(onGrid([], 30, [[1, 1]])).toEqual([])
    expect(onGrid(times, 0, [[1_792_500_000, 1]]).every((x) => x === null)).toBe(true)
  })

  it('keeps only a known range', () => {
    expect(rangeOf('24h')).toBe('24h')
    expect(rangeOf('1y')).toBe('1h')
    expect(rangeOf(null)).toBe('1h')
  })
})

describe('which metrics are asked for', () => {
  const have = new Set([
    'host.cpu_pct', 'host.mem_pct', 'host.disk_pct:/', 'host.disk_pct:/var/lib/docker', 'core.errors_per_min', 'bot.updates_per_min',
    'container.cpu_pct:postgres', 'container.cpu_pct:core', 'container.cpu_pct:bot', 'container.mem_bytes:core', 'service.latency_ms:bot', 'service.latency_ms:core',
  ])

  it('names the metrics that have data, in the order of the page, and no other', () => {
    const want = wantedMetrics(have)
    expect(want).toEqual([
      'host.cpu_pct', 'host.mem_pct', 'host.disk_pct:/', 'host.disk_pct:/var/lib/docker',
      'container.cpu_pct:core', 'container.cpu_pct:bot', 'container.cpu_pct:postgres', 'container.mem_bytes:core',
      'bot.updates_per_min', 'core.errors_per_min', 'service.latency_ms:core', 'service.latency_ms:bot',
    ])
    expect(wantedMetrics(new Set())).toEqual([])
  })

  it('asks for the first six disks and the first eight services at most', () => {
    const many = new Set<string>()
    for (let i = 0; i < 9; i++) many.add(`host.disk_pct:/d${i}`)
    for (let i = 0; i < 12; i++) many.add(`container.cpu_pct:s${String(i).padStart(2, '0')}`)
    const want = wantedMetrics(many)
    expect(want.filter((m) => m.startsWith('host.disk_pct:'))).toHaveLength(6)
    expect(want.filter((m) => m.startsWith('container.cpu_pct:'))).toHaveLength(8)
  })

  it('lists the subjects of a family, the services core asks first, and cuts requests to what core accepts', () => {
    expect(subjects(have, 'host.disk_pct')).toEqual(['/', '/var/lib/docker'])
    expect(orderServices(['redis', 'bot', 'zeta', 'core', 'bot'])).toEqual(['core', 'bot', 'redis', 'zeta'])
    expect(batches(Array.from({ length: 25 }, (_, i) => i)).map((b) => b.length)).toEqual([12, 12, 1])
    expect(batches([])).toEqual([])
  })
})

describe('what an answer may leave out', () => {
  it('fills the holes of an older or partial answer, keeping lists lists and unknown numbers null', () => {
    const o = tidy({ generated_at: 5, status: 'ok' })
    expect(o.components).toEqual([])
    expect(o.units).toEqual([])
    expect(o.containers).toEqual([])
    expect(o.deployments).toEqual([])
    expect(o.host.available).toBe(false)
    expect(o.host.cpu_pct).toBeNull()
    expect(o.host.disks).toEqual([])
    expect(o.kubernetes.detected).toBe(false)
    expect(o.counters.open_alerts).toBeNull()
    expect(o.ci.runs).toEqual([])
    expect(o.ci.latest_release).toBeNull()
    expect(o.ci.main_failing).toBeNull()
    expect(o.collector.installed).toBe(false)
    expect(tidy(null).status).toBe('')
    expect(tidy({ counters: { open_alerts: 0 } as never }).counters.open_alerts).toBe(0) // a real zero stays
  })
})

describe('states', () => {
  it('reads a component, the whole and the problems that make it worse', () => {
    expect(healthOf('warn')).toBe('warn')
    expect(healthOf('weird')).toBe('unknown')
    expect(overallOf('degraded')).toBe('degraded')
    expect(overallOf('')).toBe('unknown')
    const list = [c('a', 'service', 'ok'), c('b', 'service', 'warn'), c('c', 'panel', 'down'), c('d', 'job', 'unknown'), c('e', 'gateway', 'warn')]
    expect(problems({ components: list }).map((x) => x.id)).toEqual(['c', 'b', 'e']) // down first; unknown never counts
    expect(byKind(list).map((g) => g.kind)).toEqual(['service', 'job', 'panel', 'gateway'])
    expect(byKind([c('z', 'newkind', 'ok'), c('a', 'service', 'ok')]).map((g) => g.kind)).toEqual(['service', 'newkind'])
  })

  it('says why there are no host figures', () => {
    const base = tidy({})
    expect(collectorState({ ...base, host: { ...base.host, available: true } })).toBe('ready')
    expect(collectorState(base)).toBe('not_installed')
    expect(collectorState({ ...base, collector: { ...base.collector, installed: true, error: 'invalid' } })).toBe('unusable')
    expect(collectorState({ ...base, collector: { ...base.collector, installed: true, fresh: false } })).toBe('quiet')
    expect(collectorState({ ...base, collector: { ...base.collector, installed: true, fresh: true } })).toBe('no_host')
  })

  it('reads runs, GitHub problems, links and outcomes', () => {
    expect(runState({ status: 'completed', conclusion: 'success' })).toBe('success')
    expect(runState({ status: 'completed', conclusion: 'timed_out' })).toBe('failure')
    expect(runState({ status: 'in_progress', conclusion: '' })).toBe('running')
    expect(runState({ status: 'completed', conclusion: 'cancelled' })).toBe('cancelled')
    expect(runState({ status: 'completed', conclusion: 'neutral' })).toBe('other')
    expect(ciProblem({ error: '' })).toBeNull()
    expect(ciProblem({ error: 'rate_limited' })).toBe('rate_limited')
    expect(ciProblem({ error: 'something new' })).toBe('other')
    expect(githubLink('https://github.com/o/r/actions/runs/1')).toBe('https://github.com/o/r/actions/runs/1')
    for (const bad of ['http://github.com/o/r', 'https://evil.example/github.com', 'https://github.com.evil.example/x', 'javascript:alert(1)', 'https://u:p@github.com/x', '', undefined]) {
      expect(githubLink(bad as string)).toBe('')
    }
    expect(shortSha('abc1234def5678')).toBe('abc1234')
    expect(shortSha('not a sha')).toBe('')
    expect(outcomeOf({ outcome: 'rolled_back' })).toBe('rolled_back')
    expect(outcomeOf({ outcome: 'who knows' })).toBe('unknown')
  })

  it('reads an image', () => {
    expect(versionOf('ghcr.io/sobhanaz/bobres-core:0.7.0')).toBe('0.7.0')
    expect(imageName('ghcr.io/sobhanaz/bobres-core:0.7.0')).toBe('bobres-core')
    expect(versionOf('localhost:5000/app')).toBe('') // a port is not a tag
    expect(imageName('localhost:5000/app')).toBe('app')
    expect(versionOf('redis:7-alpine@sha256:abc')).toBe('7-alpine')
    expect(versionOf('busybox')).toBe('')
  })
})

describe('numbers, sizes, durations and values', () => {
  it('writes them in the language\'s digits', () => {
    expect(count(1234.5, 'en', 1)).toBe('1,234.5')
    expect(count(1234, 'fa')).toBe('۱٬۲۳۴')
    expect(pct(12.5, 'en')).toBe('12.5%')
    expect(pct(12.5, 'fa')).toBe('۱۲٫۵٪')
    expect(size(0, 'en')).toBe('0 byte')
    expect(size(1536, 'en')).toBe('1.5 kB')
    expect(size(100 * 2 ** 20, 'en')).toBe('100 MB')
    expect(size(3 * 2 ** 30, 'en', true)).toBe('3 GB/s')
    expect(size(2 ** 40 * 1.5, 'fa')).toContain('۱٫۵')
    expect(duration(45, 'en')).toBe('45 seconds')
    expect(duration(90_061, 'en')).toBe('1 day 1 hour')
    expect(duration(1_209_600, 'fa')).toBe('۱۴ روز')
    expect(duration(0, 'en')).toBe('0 seconds')
  })

  it('formats a chart\'s values by their unit, full and short', () => {
    expect(unitFormat('percent', 'en')(40)).toBe('40%')
    expect(unitFormat('bytes', 'en')(1.5 * 2 ** 30)).toBe('1.5 GB')
    expect(unitFormat('bytes', 'en', true)(1.5 * 2 ** 30)).toBe('2 GB')
    expect(unitFormat('bytes_per_s', 'en')(2048)).toBe('2 kB/s')
    expect(unitFormat('ms', 'en')(3.456)).toBe('3.5 ms')
    expect(unitFormat('load', 'en')(0.456)).toBe('0.46')
    expect(unitFormat('per_min', 'fa')(2.5)).toBe('۲٫۵')
    expect(unitFormat('count', 'en')(1200)).toBe('1,200')
  })

  it('shortens the time axis by the range: the time of day within a day, the date beyond', () => {
    const at = Date.UTC(2026, 9, 20, 12, 30) / 1000
    expect(tickLabel(at, '1h', 'en', 'gregorian')).toMatch(/^\d{2}:\d{2}$/)
    expect(tickLabel(at, '30d', 'en', 'gregorian')).toMatch(/Oct/)
    expect(tickLabel(at, '30d', 'fa', 'jalali')).toMatch(/مهر/) // the Jalali month Mehr
    expect(tickLabel(at, '7d', 'fa', 'gregorian')).toMatch(/اکتبر/)
  })
})

describe('an event\'s title', () => {
  const alert: EventItem = {
    at: 1, level: 'warn', source: 'alert', kind: 'alert', key: 'monitor.disk:/', state: 'open', title_key: 'alert.monitor_disk',
    args: { mount: '/', percent: '88', free: String(2 ** 30) }, title: 'The disk at / is 88% full.', detail: '',
  }
  const words = (messages: Record<string, unknown>) => ({
    t: (key: string, ...args: unknown[]) => {
      const raw = key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], messages)
      return typeof raw === 'string' ? Object.entries((args[0] ?? {}) as Record<string, string>).reduce((s, [k, v]) => s.replaceAll('{' + k + '}', v), raw) : key
    },
    te: (key: string) => typeof key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], messages) === 'string',
    tm: (key: string) => key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], messages),
  })

  it('writes an alert from the bot\'s text key and values', () => {
    expect(eventTitle(alert, 'en', words(en))).toBe('The disk at ⁨/⁩ is 88% full (1 GB free).')
  })

  it('falls back to core\'s words when the page has no text for the alert, and for anything that is not an alert', () => {
    expect(eventTitle({ ...alert, title_key: 'alert.from_the_future' }, 'en', words(en))).toBe('The disk at / is 88% full.')
    expect(eventTitle({ ...alert, args: { percent: '88' } }, 'en', words(en))).toBe('The disk at / is 88% full.') // a value the text needs is missing: never a hole
    expect(eventTitle({ ...alert, kind: 'log', source: 'bot', title: '  send failed  ' }, 'en', words(en))).toBe('send failed')
    expect(eventTitle({ ...alert, title_key: undefined, title: '' }, 'en', words(en))).toBe('')
  })
})

describe('an event that stands for several identical lines', () => {
  it('says how many, and since when, for an entry core folded', () => {
    expect(repeatOf({ at: 2000, count: 12, first_at: 1000 })).toEqual({ count: 12, first: 1000 })
    expect(repeatOf({ at: 2000, count: 2, first_at: 1999 })).toEqual({ count: 2, first: 1999 })
  })

  it('is null for a single line, and for an answer of an older core that has neither field', () => {
    expect(repeatOf({ at: 2000 })).toBeNull()
    expect(repeatOf({ at: 2000, count: 1, first_at: 1000 })).toBeNull()
    expect(repeatOf({ at: 2000, count: 0 })).toBeNull()
    expect(repeatOf({ at: 2000, first_at: 1000 })).toBeNull() // a time without a count counts nothing
  })

  it('never invents a span: the oldest time must be there and before the newest', () => {
    expect(repeatOf({ at: 2000, count: 5 })).toEqual({ count: 5, first: null })
    expect(repeatOf({ at: 2000, count: 5, first_at: 2000 })).toEqual({ count: 5, first: null })
    expect(repeatOf({ at: 2000, count: 5, first_at: 3000 })).toEqual({ count: 5, first: null })
    expect(repeatOf({ at: 2000, count: 5, first_at: 0 })).toEqual({ count: 5, first: null })
    expect(repeatOf({ at: 2000, count: 5, first_at: Number.NaN })).toEqual({ count: 5, first: null })
  })

  it('takes only a whole count', () => {
    expect(repeatOf({ at: 2000, count: 2.5, first_at: 1000 })).toBeNull()
    expect(repeatOf({ at: 2000, count: '12' as unknown as number, first_at: 1000 })).toBeNull()
    expect(repeatOf({ at: 2000, count: Number.NaN, first_at: 1000 })).toBeNull()
    expect(repeatOf({ at: 2000, count: null as unknown as number, first_at: 1000 })).toBeNull()
  })
})
