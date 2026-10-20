import { describe, expect, it } from 'vitest'
import type { ReportBucket, ReportCell, ReportMethod, ReportSummary } from '../reports'
import {
  alignPoints,
  averageOrder,
  axisLabel,
  bestBucket,
  busiest,
  currencyKpis,
  deltaOf,
  hourGrid,
  hourLabel,
  isoToJs,
  methodSlices,
  orderSeries,
  peopleKpis,
  peopleSeries,
  planStack,
  previousRange,
  revenueSeries,
  toneOf,
  trendWords,
  weekdayOrder,
  type HourRow,
  type PlanSeriesRow,
} from '../reportsvisual'

// What the charts and the professional report make of the numbers core gives. The rules of the comparison are
// the daily summary in Telegram's (internal/bot/notify), so the dashboard and the bot never word one change two ways.

const flow = (count = 0, amount = 0) => ({ count, amount })
const cell = (c: Partial<ReportCell> = {}): ReportCell => ({
  revenue: 0, refunds: flow(), net: 0, orders: 0, new_orders: 0, renewals: 0, traffic_topups: 0, topups: flow(), rejected: flow(), gateway_failed: flow(), cancelled: flow(), ...c,
})
const bucket = (date: string, irt: Partial<ReportCell>, people: Partial<ReportBucket> = {}): ReportBucket => ({
  date, partial: false, new_customers: 0, trials: 0, churned_services: 0, churned_customers: 0, at_risk: 0, currencies: { IRT: cell(irt) }, ...people,
})
const summary = (buckets: ReportBucket[], totals: Partial<ReportSummary['totals']> = {}): ReportSummary => ({
  from: buckets[0].date, to: buckets[buckets.length - 1].date, group: 'day', calendar: 'gregorian', tz: 'Asia/Tehran', churn_grace_days: 7, currencies: ['IRT'], buckets,
  totals: {
    new_customers: 0, trials: 0, churned_services: 0, churned_customers: 0, at_risk: 0,
    currencies: { IRT: cell({ revenue: buckets.reduce((n, b) => n + b.currencies.IRT.revenue, 0), net: buckets.reduce((n, b) => n + b.currencies.IRT.net, 0), orders: buckets.reduce((n, b) => n + b.currencies.IRT.orders, 0) }) },
    ...totals,
  },
})

describe('the change on the period before', () => {
  it('says up, down, the same, about the same, new or nothing, as the daily summary does', () => {
    expect(deltaOf(150, 100)).toEqual({ kind: 'up', pct: 50, lots: false })
    expect(deltaOf(60, 100)).toEqual({ kind: 'down', pct: 40, lots: false })
    expect(deltaOf(100, 100)).toEqual({ kind: 'same', pct: 0, lots: false })
    expect(deltaOf(1002, 1000).kind).toBe('about') // 0.2%: rounds to nothing, but the amounts differ
    expect(deltaOf(998, 1000).kind).toBe('about')
    expect(deltaOf(50, 0)).toEqual({ kind: 'new', pct: 0, lots: false })
    expect(deltaOf(0, 0).kind).toBe('none')
    expect(deltaOf(5, null).kind).toBe('none') // no earlier period to compare with
    expect(deltaOf(5, undefined).kind).toBe('none')
  })

  it('never says more than the amounts do', () => {
    // a rise past 999% is "more than 999%", not 500000000%
    expect(deltaOf(5_000_000, 1)).toEqual({ kind: 'up', pct: 999, lots: true })
    expect(deltaOf(10_000, 1000)).toEqual({ kind: 'up', pct: 900, lots: false })
    // a period that had sales is at most "down 99%": "down 100%" is a period with none
    expect(deltaOf(1, 1000)).toEqual({ kind: 'down', pct: 99, lots: false })
    expect(deltaOf(0, 1000)).toEqual({ kind: 'down', pct: 100, lots: false })
  })

  it('counts a rise as good news for sales and bad news for lost services', () => {
    const up = deltaOf(150, 100)
    const down = deltaOf(60, 100)
    expect(toneOf(up)).toBe('good')
    expect(toneOf(down)).toBe('bad')
    expect(toneOf(up, false)).toBe('bad')
    expect(toneOf(down, false)).toBe('good')
    expect(toneOf(deltaOf(5, 0), false)).toBe('bad') // lost services where there were none
    expect(toneOf(deltaOf(100, 100))).toBe('flat')
    expect(toneOf(deltaOf(0, 0), false)).toBe('flat')
  })

  it('takes the same number of days right before a range, unless that starts before the first day core reports on', () => {
    expect(previousRange('2026-10-15', '2026-10-17')).toEqual({ from: '2026-10-12', to: '2026-10-14', days: 3 })
    expect(previousRange('2026-10-20', '2026-10-20')).toEqual({ from: '2026-10-19', to: '2026-10-19', days: 1 })
    expect(previousRange('2026-03-01', '2026-03-31')).toEqual({ from: '2026-01-29', to: '2026-02-28', days: 31 }) // across a month end
    expect(previousRange('2000-01-10', '2000-01-12')).toEqual({ from: '2000-01-07', to: '2000-01-09', days: 3 })
    expect(previousRange('2000-01-02', '2000-01-04')).toBeNull() // would start in 1999
    expect(previousRange('2000-01-01', '2000-01-01')).toBeNull()
    expect(previousRange('2026-10-17', '2026-10-15')).toBeNull() // an inverted range is no range
  })

  it('builds the headline numbers of a currency with their change, and an average order', () => {
    const now = summary([bucket('2026-10-15', { revenue: 100, net: 100, orders: 1 }), bucket('2026-10-16', { revenue: 300, net: 200, orders: 2 })])
    const before = summary([bucket('2026-10-13', { revenue: 200, net: 200, orders: 4 })])
    const k = currencyKpis('IRT', now, before)
    expect(k.map((x) => x.key)).toEqual(['revenue', 'net', 'orders', 'aov'])
    expect(k[0]).toMatchObject({ value: 400, prev: 200, delta: { kind: 'up', pct: 100 }, spark: [100, 300] })
    expect(k[1]).toMatchObject({ value: 300, prev: 200, delta: { kind: 'up', pct: 50 } })
    expect(k[2]).toMatchObject({ value: 3, prev: 4, delta: { kind: 'down', pct: 25 }, spark: [1, 2] })
    expect(k[3]).toMatchObject({ value: 133, prev: 50, spark: [100, 150] }) // 400 / 3, rounded; 200 / 4
    expect(averageOrder({ revenue: 10, orders: 0 })).toBe(0)
    // with no earlier period nothing is compared
    expect(currencyKpis('IRT', now, null).every((x) => x.prev === null && x.delta.kind === 'none')).toBe(true)
    // a currency the report has no numbers for is zeros, not a crash
    expect(currencyKpis('EUR', now, before)[0]).toMatchObject({ value: 0, prev: 0 })
  })

  it('compares the counts of people, where fewer lost services is the good direction', () => {
    const now = summary([bucket('2026-10-15', {}, { new_customers: 2, churned_services: 3 })], { new_customers: 2, trials: 1, churned_services: 3, churned_customers: 2 })
    const before = summary([bucket('2026-10-12', {}, {})], { new_customers: 4, trials: 1, churned_services: 1, churned_customers: 0 })
    const p = peopleKpis(now, before)
    expect(p.map((x) => x.key)).toEqual(['new_customers', 'trials', 'churned_services', 'churned_customers'])
    expect(p[0]).toMatchObject({ value: 2, prev: 4, goodWhenUp: true, delta: { kind: 'down', pct: 50 } })
    expect(p[1].delta.kind).toBe('same')
    expect(p[2]).toMatchObject({ goodWhenUp: false, delta: { kind: 'up', pct: 200 }, spark: [3] })
    expect(p[3].delta.kind).toBe('new')
    expect(toneOf(p[2].delta, p[2].goodWhenUp)).toBe('bad')
    expect(peopleKpis(now, null).every((x) => x.delta.kind === 'none')).toBe(true)
  })

  it('names the best period, and says how a series moved in words', () => {
    const s = summary([bucket('2026-10-15', { revenue: 100 }), bucket('2026-10-16', { revenue: 900 }), bucket('2026-10-17', { revenue: 300 })])
    expect(bestBucket(s, 'IRT')).toEqual({ date: '2026-10-16', revenue: 900 })
    expect(bestBucket(summary([bucket('2026-10-15', {})]), 'IRT')).toBeNull()
    expect(trendWords([1, 5, 9])).toBe('up')
    expect(trendWords([9, 5, 1])).toBe('down')
    expect(trendWords([3, 8, 3])).toBe('flat')
    expect(trendWords([0, 0, 0])).toBe('none')
    expect(trendWords([5])).toBe('none')
  })
})

describe('series for the charts', () => {
  const s = summary([
    bucket('2026-10-15', { revenue: 100, net: 90, orders: 3, new_orders: 2, renewals: 1 }, { new_customers: 2, trials: 1, churned_services: 4, at_risk: 1 }),
    bucket('2026-10-16', {}, {}),
    { ...bucket('2026-10-17', { revenue: 50, net: 50, orders: 1, traffic_topups: 1 }, { new_customers: 1 }), partial: true },
  ])

  it('lines up revenue, orders by type and people over the buckets, with zeros where nothing happened', () => {
    expect(revenueSeries(s, 'IRT')).toEqual({ revenue: [100, 0, 50], net: [90, 0, 50] })
    expect(orderSeries(s, 'IRT')).toEqual({ new: [2, 0, 0], renew: [1, 0, 0], traffic: [0, 0, 1], total: 4 })
    expect(peopleSeries(s)).toEqual({ newCustomers: [2, 0, 1], trials: [1, 0, 0], lost: [4, 0, 0], atRisk: [1, 0, 0] })
    expect(revenueSeries(s, 'USDT')).toEqual({ revenue: [0, 0, 0], net: [0, 0, 0] }) // a currency the report lacks is flat, never NaN
  })

  it('writes the axis labels in the calendar the page is set to', () => {
    expect(axisLabel('2026-10-14', 'day', 'en', 'gregorian', 'gregorian')).toBe('14 Oct')
    expect(axisLabel('2026-10-14', 'day', 'en', 'gregorian', 'jalali')).toBe('22 Mehr')
    expect(axisLabel('2026-10-23', 'month', 'en', 'jalali', 'jalali')).toBe('Aban 1405')
    expect(axisLabel('2026-10-01', 'month', 'en', 'gregorian', 'gregorian')).toBe('Oct 2026')
    // the moment after the switch: months cut one way, dates written the other way, so it is a true date
    expect(axisLabel('2026-10-01', 'month', 'en', 'gregorian', 'jalali')).toBe('9 Mehr')
    expect(axisLabel('2026-10-14', 'day', 'fa', 'jalali', 'jalali')).toBe('۲۲ مهر')
  })
})

describe('plans over the periods', () => {
  const buckets = ['2026-10-15', '2026-10-16', '2026-10-17'].map((d) => bucket(d, {}))
  const s = summary(buckets)
  const row = (id: string, cur: string, revenue: number, points: [string, number][]): PlanSeriesRow => ({
    plan_id: id, name: { fa: id, en: id }, currency: cur, orders: points.length, revenue,
    points: points.map(([date, r]) => ({ date, orders: 1, revenue: r })),
  })

  it('puts sparse points on the report buckets', () => {
    expect(alignPoints([{ date: '2026-10-16', orders: 2, revenue: 70 }], s.buckets, 'revenue')).toEqual([0, 70, 0])
    expect(alignPoints([{ date: '2026-10-16', orders: 2, revenue: 70 }], s.buckets, 'orders')).toEqual([0, 2, 0])
    expect(alignPoints([{ date: '2026-12-01', orders: 1, revenue: 9 }], s.buckets, 'revenue')).toEqual([0, 0, 0]) // a point outside is ignored
  })

  it('stacks the best plans one by one, colours them by rank, and folds the rest into "other"', () => {
    const rows = [
      row('a', 'IRT', 600, [['2026-10-15', 400], ['2026-10-16', 200]]),
      row('b', 'IRT', 300, [['2026-10-16', 300]]),
      row('c', 'IRT', 200, [['2026-10-17', 200]]),
      row('d', 'IRT', 100, [['2026-10-15', 100]]),
      row('', 'IRT', 50, [['2026-10-15', 50]]), // the rest, as core folds it past twenty plans
      row('z', 'USDT', 9, [['2026-10-15', 9]]),
    ]
    const out = planStack(rows, s, 'IRT', 2)
    expect(out.map((p) => [p.key, p.slot])).toEqual([['a', 0], ['b', 1], ['other', 'other']])
    expect(out[0].values).toEqual([400, 200, 0])
    // the two plans below the cut and core's own "other" are one grey series
    expect(out[2]).toMatchObject({ planId: '', values: [150, 0, 200], revenue: 350 })
    // every column still adds up to the bucket's revenue in this currency
    expect([0, 1, 2].map((i) => out.reduce((n, p) => n + p.values[i], 0))).toEqual([550, 500, 200])
    // another currency's plans never come in
    expect(planStack(rows, s, 'USDT').map((p) => p.key)).toEqual(['z'])
    // when every plan fits, there is no "other"
    expect(planStack(rows.slice(0, 2), s, 'IRT', 5).map((p) => p.key)).toEqual(['a', 'b'])
    expect(planStack([], s, 'IRT')).toEqual([])
  })

  it('splits a currency into how it was paid, the best ways one by one and the rest together', () => {
    const rows: ReportMethod[] = [
      { method: 'wallet', provider: '', currency: 'IRT', orders: 1, revenue: 500 },
      { method: 'manual', provider: 'manual_card', currency: 'IRT', orders: 1, revenue: 300 },
      { method: 'gateway', provider: 'zarinpal', currency: 'IRT', orders: 1, revenue: 100 },
      { method: 'gateway', provider: 'stars', currency: 'IRT', orders: 1, revenue: 60 },
      { method: 'unknown', provider: '', currency: 'IRT', orders: 1, revenue: 0 }, // nothing: no slice
      { method: 'gateway', provider: 'usdt_trc20_auto', currency: 'USDT', orders: 1, revenue: 7 },
    ]
    const name = (r: ReportMethod) => r.method + ':' + r.provider
    const slices = methodSlices(rows, 'IRT', name, 'Other', 2)
    expect(slices.map((x) => [x.label, x.value, x.slot])).toEqual([['wallet:', 500, 0], ['manual:manual_card', 300, 1], ['Other', 160, 'other']])
    expect(methodSlices(rows, 'USDT', name, 'Other')).toHaveLength(1)
    expect(methodSlices(rows, 'EUR', name, 'Other')).toEqual([])
  })
})

describe('the hours of the week', () => {
  const hours: HourRow[] = [
    { weekday: 6, hour: 21, currency: 'IRT', orders: 5, revenue: 500 }, // Saturday
    { weekday: 6, hour: 21, currency: 'USDT', orders: 9, revenue: 9 },
    { weekday: 1, hour: 0, currency: 'IRT', orders: 2, revenue: 200 }, // Monday
    { weekday: 7, hour: 13, currency: 'IRT', orders: 1, revenue: 100 }, // Sunday
    { weekday: 3, hour: 30, currency: 'IRT', orders: 1, revenue: 1 }, // not an hour: ignored
  ]

  it('writes an hour as the clock does, in the digits of the language', () => {
    expect(hourLabel(9, 'en')).toBe('09:00')
    expect(hourLabel(21, 'en')).toBe('21:00')
    expect(hourLabel(0, 'fa')).toBe('۰۰:۰۰')
    expect(hourLabel(21, 'fa')).toBe('۲۱:۰۰')
  })

  it('reads the week from Saturday in Jalali and from Monday in Gregorian', () => {
    expect(weekdayOrder('jalali')).toEqual([6, 7, 1, 2, 3, 4, 5])
    expect(weekdayOrder('gregorian')).toEqual([1, 2, 3, 4, 5, 6, 7])
    expect([7, 1, 6].map(isoToJs)).toEqual([0, 1, 6]) // ISO Sunday is 7, JavaScript's is 0
  })

  it('lays a currency out on a grid of weekdays and hours, in the order of the calendar', () => {
    const jalali = hourGrid(hours, 'IRT', 'orders', 'jalali')
    expect(jalali.weekdays).toEqual([6, 7, 1, 2, 3, 4, 5])
    expect(jalali.values).toHaveLength(7)
    expect(jalali.values.every((r) => r.length === 24)).toBe(true)
    expect(jalali.values[0][21]).toBe(5) // Saturday first
    expect(jalali.values[1][13]).toBe(1)
    const greg = hourGrid(hours, 'IRT', 'revenue', 'gregorian')
    expect(greg.values[0][0]).toBe(200) // Monday first
    expect(greg.values[5][21]).toBe(500)
    expect(greg.values.flat().reduce((a, b) => a + b, 0)).toBe(800) // the other currency and the odd hour are not in it
    expect(hourGrid([], 'IRT', 'orders', 'gregorian').values.flat().every((v) => v === 0)).toBe(true)
  })

  it('finds the busiest hour, or nothing when nothing was sold', () => {
    expect(busiest(hourGrid(hours, 'IRT', 'orders', 'gregorian'))).toEqual({ weekday: 6, hour: 21, value: 5 })
    expect(busiest(hourGrid(hours, 'USDT', 'orders', 'jalali'))).toEqual({ weekday: 6, hour: 21, value: 9 })
    expect(busiest(hourGrid([], 'IRT', 'orders', 'gregorian'))).toBeNull()
  })
})
