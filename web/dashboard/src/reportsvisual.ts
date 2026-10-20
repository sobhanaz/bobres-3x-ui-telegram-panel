// The arithmetic behind the Reports page's charts and the professional report:
// what core answers (reports.ts) turned into the series, slices and grids the
// chart kit draws, and the comparison with the period before. Pure functions,
// no Vue: every rule here has a unit test. Amounts stay integers in each
// currency's minor unit and are never added across currencies.
import { addDays, daysBetween, DATE_MIN, digits, formatDate, formatMonth, type Calendar } from './calendar'
import type { DonutSlice } from './components/charts/types'
import type { Lang } from './format'
import type { Group, ReportBucket, ReportCell, ReportMethod, ReportSummary } from './reports'

// ------------------------------------------------------------- what core answers

/** One plan's sales in one bucket (GET /reports/plan-series). */
export interface PlanPoint {
  date: string
  orders: number
  revenue: number
}

/** One plan's sales in one currency across the report's buckets; plan_id is ''
 * for every plan beyond the currency's best twenty. Only the buckets where it
 * sold are listed. */
export interface PlanSeriesRow {
  plan_id: string
  name: Record<string, string>
  currency: string
  orders: number
  revenue: number
  points: PlanPoint[]
}

export interface PlanSeriesAnswer {
  from: string
  to: string
  group: Group
  calendar: Calendar
  tz: string
  series: PlanSeriesRow[]
}

/** Paid orders in one hour of the week (GET /reports/hours): weekday is ISO
 * (1 Monday to 7 Sunday), hour 0 to 23, in the store's zone. */
export interface HourRow {
  weekday: number
  hour: number
  currency: string
  orders: number
  revenue: number
}

export interface HoursAnswer {
  from: string
  to: string
  tz: string
  hours: HourRow[]
}

// ------------------------------------------------------------- series of a currency

const NO_CELL: ReportCell = {
  revenue: 0,
  refunds: { count: 0, amount: 0 },
  net: 0,
  orders: 0,
  new_orders: 0,
  renewals: 0,
  traffic_topups: 0,
  topups: { count: 0, amount: 0 },
  rejected: { count: 0, amount: 0 },
  gateway_failed: { count: 0, amount: 0 },
  cancelled: { count: 0, amount: 0 },
}

/** cellOf is a bucket's numbers in a currency, zeros when it has none. */
export const cellOf = (b: ReportBucket, currency: string): ReportCell => b.currencies[currency] ?? NO_CELL

/** axisLabel is the short name of a period for a chart's axis: a day or week
 * by its first day ("14 Oct", "22 Mehr"), a month by its name and year when the
 * report cut its months in the calendar the page writes ("Aban 1405", "Oct
 * 2026"). `report` is how core cut the periods, `dates` the calendar the page
 * writes in (the same, except for the moment after the switch is flipped). */
export function axisLabel(date: string, group: Group, lang: Lang, report: Calendar, dates: Calendar): string {
  if (group === 'month' && report === dates) return formatMonth(date, lang, dates)
  return formatDate(date, lang, dates, 'short')
}

/** partialFlags marks the buckets the range cuts. */
export const partialFlags = (s: ReportSummary): boolean[] => s.buckets.map((b) => b.partial)

/** revenueSeries are a currency's revenue and what is kept after refunds, per bucket. */
export function revenueSeries(s: ReportSummary, currency: string): { revenue: number[]; net: number[] } {
  return { revenue: s.buckets.map((b) => cellOf(b, currency).revenue), net: s.buckets.map((b) => cellOf(b, currency).net) }
}

/** orderSeries are a currency's paid orders per bucket, split by what they were. */
export function orderSeries(s: ReportSummary, currency: string): { new: number[]; renew: number[]; traffic: number[]; total: number } {
  const cells = s.buckets.map((b) => cellOf(b, currency))
  return {
    new: cells.map((c) => c.new_orders),
    renew: cells.map((c) => c.renewals),
    traffic: cells.map((c) => c.traffic_topups),
    total: cells.reduce((n, c) => n + c.orders, 0),
  }
}

/** peopleSeries are the counts of people per bucket (they have no currency). */
export function peopleSeries(s: ReportSummary): { newCustomers: number[]; trials: number[]; lost: number[]; atRisk: number[] } {
  return {
    newCustomers: s.buckets.map((b) => b.new_customers),
    trials: s.buckets.map((b) => b.trials),
    lost: s.buckets.map((b) => b.churned_services),
    atRisk: s.buckets.map((b) => b.at_risk),
  }
}

/** averageOrder is what a paid order was worth on average, in whole minor units. */
export const averageOrder = (c: Pick<ReportCell, 'revenue' | 'orders'>): number => (c.orders > 0 ? Math.round(c.revenue / c.orders) : 0)

/** alignPoints puts a plan's sparse points on the report's buckets (zero where it sold nothing). */
export function alignPoints(points: PlanPoint[], buckets: ReportBucket[], metric: 'revenue' | 'orders'): number[] {
  const by = new Map(points.map((p) => [p.date, p[metric]]))
  return buckets.map((b) => by.get(b.date) ?? 0)
}

export interface PlanStack {
  /** the plan id, or 'other' for the rest */
  key: string
  planId: string
  name: Record<string, string>
  /** the colour slot: the rank among the currency's plans, 'other' for the rest */
  slot: number | 'other'
  values: number[]
  orders: number
  revenue: number
}

/** planStack lays one currency's plans out as stacked series over the report's
 * buckets: the best `top` plans one by one (their rank is their colour) and
 * everything else together as "other", so a column still adds up to the bucket's
 * revenue. */
export function planStack(rows: PlanSeriesRow[], s: ReportSummary, currency: string, top = 5, metric: 'revenue' | 'orders' = 'revenue'): PlanStack[] {
  const mine = rows.filter((r) => r.currency === currency && r.plan_id !== '')
  const rest = rows.filter((r) => r.currency === currency && r.plan_id === '')
  const named = mine.slice(0, top)
  const out: PlanStack[] = named.map((r, i) => ({
    key: r.plan_id,
    planId: r.plan_id,
    name: r.name,
    slot: i,
    values: alignPoints(r.points, s.buckets, metric),
    orders: r.orders,
    revenue: r.revenue,
  }))
  const folded = [...mine.slice(top), ...rest]
  if (folded.length) {
    const values = s.buckets.map(() => 0)
    for (const r of folded) alignPoints(r.points, s.buckets, metric).forEach((v, i) => (values[i] += v))
    out.push({
      key: 'other',
      planId: '',
      name: {},
      slot: 'other',
      values,
      orders: folded.reduce((n, r) => n + r.orders, 0),
      revenue: folded.reduce((n, r) => n + r.revenue, 0),
    })
  }
  return out
}

/** methodSlices are one currency's revenue by how it was paid, the best `top`
 * ways one by one and the rest as one "other" slice. `label` names a row. */
export function methodSlices(rows: ReportMethod[], currency: string, label: (m: ReportMethod) => string, otherLabel: string, top = 6): DonutSlice[] {
  const mine = rows.filter((r) => r.currency === currency && r.revenue > 0).sort((a, b) => b.revenue - a.revenue)
  const slices: DonutSlice[] = mine.slice(0, top).map((r, i) => ({ key: `${r.method}:${r.provider}`, label: label(r), value: r.revenue, slot: i }))
  const rest = mine.slice(top).reduce((n, r) => n + r.revenue, 0)
  if (rest > 0) slices.push({ key: 'other', label: otherLabel, value: rest, slot: 'other' })
  return slices
}

// ------------------------------------------------------------- the hours of the week

/** hourLabel writes an hour of the day as the clock does, in the language's digits ("21:00", "۲۱:۰۰"). */
export const hourLabel = (h: number, lang: Lang): string => digits(String(h).padStart(2, '0') + ':00', lang)

/** weekdayOrder lists the ISO weekdays in the order a calendar's week reads:
 * from Saturday in Jalali, from Monday in Gregorian. */
export function weekdayOrder(calendar: Calendar): number[] {
  return calendar === 'jalali' ? [6, 7, 1, 2, 3, 4, 5] : [1, 2, 3, 4, 5, 6, 7]
}

/** isoToJs turns an ISO weekday (1 Monday to 7 Sunday) into the 0 Sunday to 6 Saturday of Date. */
export const isoToJs = (iso: number): number => iso % 7

export interface HourGrid {
  /** the ISO weekdays, one per row */
  weekdays: number[]
  /** values[row][hour] */
  values: number[][]
}

/** hourGrid puts a currency's paid orders (or revenue) on a weekday-by-hour grid. */
export function hourGrid(hours: HourRow[], currency: string, metric: 'orders' | 'revenue', calendar: Calendar): HourGrid {
  const weekdays = weekdayOrder(calendar)
  const values = weekdays.map(() => new Array<number>(24).fill(0))
  for (const h of hours) {
    if (h.currency !== currency) continue
    const row = weekdays.indexOf(h.weekday)
    if (row >= 0 && h.hour >= 0 && h.hour < 24) values[row][h.hour] += h[metric]
  }
  return { weekdays, values }
}

/** busiest is the weekday and hour with the most, or null when nothing was sold. */
export function busiest(grid: HourGrid): { weekday: number; hour: number; value: number } | null {
  let best: { weekday: number; hour: number; value: number } | null = null
  grid.values.forEach((row, r) =>
    row.forEach((v, hour) => {
      if (v > 0 && (!best || v > best.value)) best = { weekday: grid.weekdays[r], hour, value: v }
    }),
  )
  return best
}

// ------------------------------------------------------------- the period before

export interface PreviousRange {
  from: string
  to: string
  days: number
}

/** previousRange is the stretch of the same number of days right before a
 * range ("the 30 days before"), or null when it would start before the first
 * day core reports on. */
export function previousRange(from: string, to: string): PreviousRange | null {
  const days = daysBetween(from, to) + 1
  if (days < 1) return null
  const pf = addDays(from, -days)
  return pf >= DATE_MIN ? { from: pf, to: addDays(from, -1), days } : null
}

export type DeltaKind = 'up' | 'down' | 'same' | 'about' | 'new' | 'none'

export interface Delta {
  kind: DeltaKind
  /** whole percent, always positive; the direction is the kind */
  pct: number
  /** a rise past 999%: say "more than" */
  lots: boolean
}

/** The biggest rise written as a number. */
export const MAX_RISE = 999

/** deltaOf compares a number with the same number a period before, in the same
 * words the daily summary in Telegram uses: up or down by a whole percent, the
 * same (to the unit), about the same (under half a percent either way), new
 * (nothing before), or none (nothing to compare: no earlier period, or nothing
 * in either). A period that had a number is never "down 100%": that is nothing
 * at all, and at most 99%. */
export function deltaOf(cur: number, prev: number | null | undefined): Delta {
  const none: Delta = { kind: 'none', pct: 0, lots: false }
  if (prev === null || prev === undefined) return none
  if (prev <= 0 && cur <= 0) return none
  if (prev <= 0) return { kind: 'new', pct: 0, lots: false }
  if (cur === prev) return { kind: 'same', pct: 0, lots: false }
  const change = Math.round(((cur - prev) * 100) / prev)
  if (change > MAX_RISE) return { kind: 'up', pct: MAX_RISE, lots: true }
  if (change > 0) return { kind: 'up', pct: change, lots: false }
  if (change < 0) {
    const down = -change
    return { kind: 'down', pct: Math.min(down, cur > 0 ? 99 : 100), lots: false }
  }
  return { kind: 'about', pct: 0, lots: false }
}

export type Tone = 'good' | 'bad' | 'flat'

/** toneOf says whether a change is good news: a rise is good for revenue and
 * bad for lost services (goodWhenUp false). */
export function toneOf(d: Delta, goodWhenUp = true): Tone {
  if (d.kind === 'up' || d.kind === 'new') return goodWhenUp ? 'good' : 'bad'
  if (d.kind === 'down') return goodWhenUp ? 'bad' : 'good'
  return 'flat'
}

export type KpiKey = 'revenue' | 'net' | 'orders' | 'aov'

export interface Kpi {
  key: KpiKey
  value: number
  prev: number | null
  delta: Delta
  /** one number per bucket, for the small trend line */
  spark: number[]
}

/** currencyKpis are a currency's headline numbers with their change on the period before. */
export function currencyKpis(currency: string, s: ReportSummary, previous: ReportSummary | null): Kpi[] {
  const cur = s.totals.currencies[currency] ?? NO_CELL
  const prev = previous ? (previous.totals.currencies[currency] ?? NO_CELL) : null
  const cells = s.buckets.map((b) => cellOf(b, currency))
  const make = (key: KpiKey, value: number, before: number | null, spark: number[]): Kpi => ({ key, value, prev: before, delta: deltaOf(value, before), spark })
  return [
    make('revenue', cur.revenue, prev ? prev.revenue : null, cells.map((c) => c.revenue)),
    make('net', cur.net, prev ? prev.net : null, cells.map((c) => c.net)),
    make('orders', cur.orders, prev ? prev.orders : null, cells.map((c) => c.orders)),
    make('aov', averageOrder(cur), prev ? averageOrder(prev) : null, cells.map(averageOrder)),
  ]
}

export type PeopleKey = 'new_customers' | 'trials' | 'churned_services' | 'churned_customers'

export interface PeopleKpi {
  key: PeopleKey
  value: number
  prev: number | null
  delta: Delta
  goodWhenUp: boolean
  spark: number[]
}

/** peopleKpis are the counts of people with their change on the period before. */
export function peopleKpis(s: ReportSummary, previous: ReportSummary | null): PeopleKpi[] {
  const spec: [PeopleKey, boolean][] = [
    ['new_customers', true],
    ['trials', true],
    ['churned_services', false],
    ['churned_customers', false],
  ]
  return spec.map(([key, goodWhenUp]) => ({
    key,
    value: s.totals[key],
    prev: previous ? previous.totals[key] : null,
    delta: deltaOf(s.totals[key], previous ? previous.totals[key] : null),
    goodWhenUp,
    spark: s.buckets.map((b) => b[key]),
  }))
}

/** bestBucket is the period with the most revenue in a currency, or null when there was none. */
export function bestBucket(s: ReportSummary, currency: string): { date: string; revenue: number } | null {
  let best: { date: string; revenue: number } | null = null
  for (const b of s.buckets) {
    const r = cellOf(b, currency).revenue
    if (r > 0 && (!best || r > best.revenue)) best = { date: b.date, revenue: r }
  }
  return best
}

/** trendWords says in one line how a series moved from its first to its last
 * point, for a screen reader that cannot see the little trend line. */
export function trendWords(values: number[]): 'up' | 'down' | 'flat' | 'none' {
  const nz = values.filter((v) => v !== 0)
  if (values.length < 2 || nz.length === 0) return 'none'
  const first = values[0]
  const last = values[values.length - 1]
  return last > first ? 'up' : last < first ? 'down' : 'flat'
}
