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
  /** which calendar the weeks and months follow; a bucket's date is Gregorian either way */
  calendar: Calendar
  tz: string
  /** every currency with any number in the range, sorted */
  currencies: string[]
  buckets: ReportBucket[]
  totals: { new_customers: number; trials: number; currencies: Record<string, ReportCell> }
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
  /** who took the payment (zarinpal, stars, usdt_trc20_auto, manual_card, ...); '' for wallet, unknown and orders paid before it was recorded */
  provider: string
  currency: string
  orders: number
  revenue: number
}

export type Group = 'day' | 'week' | 'month'
/** gregorian: weeks start on Monday, months are calendar months. jalali: weeks start on Saturday, months are Jalali months. */
export type Calendar = 'gregorian' | 'jalali'
export type Range = 'today' | 'last_7_days' | 'last_30_days' | 'this_month' | 'last_month' | 'custom'

export const groups: Group[] = ['day', 'week', 'month']
export const calendars: Calendar[] = ['gregorian', 'jalali']

/** defaultCalendar is the calendar a person reads: Jalali in Persian. */
export function defaultCalendar(lang: Lang): Calendar {
  return lang === 'fa' ? 'jalali' : 'gregorian'
}
export const ranges: Range[] = ['today', 'last_7_days', 'last_30_days', 'this_month', 'last_month', 'custom']

export interface Selection {
  range: Range
  group: Group
  calendar: Calendar
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
 * as it is (core reads it in the store's zone), a custom one as dates. The
 * calendar is sent only when it is Jalali: Gregorian is what core assumes. */
export function reportParams(sel: Selection): Record<string, string> {
  const p: Record<string, string> =
    sel.range === 'custom' ? { from: sel.from, to: sel.to, group: sel.group } : { range: sel.range, group: sel.group }
  if (sel.calendar === 'jalali') p.calendar = 'jalali'
  return p
}

/** exportParams pin the range core resolved, so a file holds what the page
 * shows (the calendar too, when it is Jalali). */
export function exportParams(s: Pick<ReportSummary, 'from' | 'to' | 'group' | 'tz'> & { calendar?: Calendar }): Record<string, string> {
  const p: Record<string, string> = { from: s.from, to: s.to, group: s.group, tz: s.tz }
  if (s.calendar === 'jalali') p.calendar = 'jalali'
  return p
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

/** jalaliMonth writes the Jalali month a date ("2026-10-23") starts, as
 * "Aban 1405" or "آبان ۱۴۰۵": the month's name and its year, nothing else. */
export function jalaliMonth(date: string, lang: Lang): string {
  const [y, m, d] = date.split('-').map(Number)
  const parts = new Intl.DateTimeFormat(lang === 'fa' ? 'fa-IR-u-ca-persian' : 'en-US-u-ca-persian', { month: 'long', year: 'numeric', timeZone: 'UTC' }).formatToParts(
    new Date(Date.UTC(y, m - 1, d)),
  )
  const pick = (type: string) => parts.find((p) => p.type === type)?.value ?? ''
  return `${pick('month')} ${pick('year')}`
}

/** periodLabel names a period by the date it starts on: a day by its date, a
 * week as "Week of 12 Oct", a month as "Month of 1 Oct" (Jalali dates in
 * Persian). A Jalali month is named by its Jalali month and year. */
export function periodLabel(date: string, group: Group, lang: Lang, t: T, calendar: Calendar = 'gregorian'): string {
  if (group === 'month' && calendar === 'jalali') return jalaliMonth(date, lang)
  const d = localDate(date, lang)
  return group === 'day' ? d : t('reports.period.' + group + '_of', { date: d })
}
