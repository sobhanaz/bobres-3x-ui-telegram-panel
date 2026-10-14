// Sales reports (docs/14-reports.md): the shapes core answers with, the
// query a choice of range and group makes, and the arithmetic of the chart.
// Amounts are integers in each currency's minor unit and are never added
// across currencies: every list here is grouped by currency first.
import { localDate, type Lang } from './format'

type T = (key: string, params?: Record<string, unknown>) => string

export interface ReportFlow {
  count: number
  amount: number
}

/** One period's numbers in one currency. */
export interface ReportCell {
  revenue: number
  refunds: ReportFlow
  /** revenue less refunds */
  net: number
  orders: number
  new_orders: number
  renewals: number
  traffic_topups: number
  topups: ReportFlow
  /** manual payments a reviewer rejected */
  rejected: ReportFlow
  /** gateway payments that failed or expired */
  gateway_failed: ReportFlow
  cancelled: ReportFlow
}

export interface ReportBucket {
  /** the period's first day, a store-local date ("2026-10-14") */
  date: string
  /** the range cuts this period: it holds only some of its days */
  partial: boolean
  new_customers: number
  trials: number
  currencies: Record<string, ReportCell>
}

export interface ReportSummary {
  from: string
  to: string
  group: Group
  tz: string
  /** every currency with any number in the range, sorted */
  currencies: string[]
  buckets: ReportBucket[]
  totals: { new_customers: number; trials: number; currencies: Record<string, ReportCell> }
}

/** GET /reports/daily-summary: whether the daily sales summary in Telegram is
 * on, at what time (HH:MM) and in which store time zone. */
export interface DailySummaryStatus {
  enabled: boolean
  time: string
  tz: string
}

export interface ReportPlan {
  plan_id: string
  name: Record<string, string>
  currency: string
  orders: number
  revenue: number
}

export interface ReportMethod {
  /** wallet, manual, gateway, or unknown */
  method: string
  currency: string
  orders: number
  revenue: number
}

export type Group = 'day' | 'week' | 'month'
export type Range = 'today' | 'last_7_days' | 'last_30_days' | 'this_month' | 'last_month' | 'custom'

export const groups: Group[] = ['day', 'week', 'month']
export const ranges: Range[] = ['today', 'last_7_days', 'last_30_days', 'this_month', 'last_month', 'custom']

export interface Selection {
  range: Range
  group: Group
  /** custom range: dates "2026-10-14", both included */
  from: string
  to: string
}

const DATE = /^\d{4}-\d{2}-\d{2}$/

/** validCustom says whether a custom range can be asked for: two dates, in
 * order, at most 400 days apart (core refuses more). */
export function validCustom(from: string, to: string): boolean {
  if (!DATE.test(from) || !DATE.test(to) || to < from) return false
  const days = (Date.parse(to) - Date.parse(from)) / 86_400_000 + 1
  return Number.isFinite(days) && days <= 400
}

/** reportParams are the query values a selection makes: a named range goes
 * as it is (core reads it in the store's zone), a custom one as dates. */
export function reportParams(sel: Selection): Record<string, string> {
  return sel.range === 'custom' ? { from: sel.from, to: sel.to, group: sel.group } : { range: sel.range, group: sel.group }
}

/** exportParams pin the range core resolved, so a file holds what the page shows. */
export function exportParams(s: Pick<ReportSummary, 'from' | 'to' | 'group' | 'tz'>): Record<string, string> {
  return { from: s.from, to: s.to, group: s.group, tz: s.tz }
}

/** Currencies in the order the page shows them: core's, then any another list names. */
export function currenciesOf(summary: ReportSummary, ...lists: { currency: string }[][]): string[] {
  const seen = new Set(summary.currencies)
  for (const l of lists) for (const r of l) seen.add(r.currency)
  return [...seen].sort()
}

/** byCurrency keeps the rows of one currency. */
export function byCurrency<T extends { currency: string }>(rows: T[], currency: string): T[] {
  return rows.filter((r) => r.currency === currency)
}

export interface Bar {
  date: string
  value: number
  /** height as a share of the tallest bar, 0 to 100 */
  pct: number
  partial: boolean
}

/** bars are one currency's revenue per period, for a chart scaled to that
 * currency alone (a period with nothing is a bar of height 0, so the chart has
 * no gaps). */
export function bars(summary: ReportSummary, currency: string): Bar[] {
  const values = summary.buckets.map((b) => b.currencies[currency]?.revenue ?? 0)
  const max = Math.max(0, ...values)
  return summary.buckets.map((b, i) => ({ date: b.date, value: values[i], pct: max > 0 ? (values[i] * 100) / max : 0, partial: b.partial }))
}

/** share is a part of a total, in whole percent (0 when the total is 0). */
export function share(part: number, total: number): number {
  return total > 0 ? Math.round((part * 100) / total) : 0
}

/** hasData says whether a report holds anything to show. */
export function hasData(s: ReportSummary): boolean {
  return s.currencies.length > 0 || s.totals.new_customers > 0 || s.totals.trials > 0
}

/** periodLabel names a period by the date it starts on: a day by its date, a
 * week as "Week of 12 Oct", a month as "Month of 1 Oct" (Jalali dates in
 * Persian; the periods themselves are Gregorian weeks and months). */
export function periodLabel(date: string, group: Group, lang: Lang, t: T): string {
  const d = localDate(date, lang)
  return group === 'day' ? d : t('reports.period.' + group + '_of', { date: d })
}
