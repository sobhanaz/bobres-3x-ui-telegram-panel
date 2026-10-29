// Sales reports (docs/14-reports.md): the shapes core answers with, the
// query a choice of range and group makes, and the arithmetic of the chart.
// Amounts are integers in each currency's minor unit and are never added
// across currencies: every list here is grouped by currency first.
import { daysBetween, formatMonth, inRange, parseIso, type Calendar } from './calendar'
import { localDate, type Lang } from './format'

export type { Calendar } from './calendar'

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
  /** the licenses the store sold and the digital accounts it resold: part of `orders` and `revenue`, not of new, renewals and traffic (absent from an older core) */
  licenses?: ReportFlow
  digital?: ReportFlow
  /** the cashback given to wallets, less what refunds took back (count: the credits); not revenue and not in `net` (absent from an older core) */
  cashback?: ReportFlow
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
  /** services whose final expiry is in this period and that stayed ended past the grace period */
  churned_services: number
  /** people whose last service ended in this period, with no other service running */
  churned_customers: number
  /** services that ended in this period but are still inside the grace period: they may yet renew */
  at_risk: number
  currencies: Record<string, ReportCell>
}

export interface ReportSummary {
  from: string
  to: string
  group: Group
  /** which calendar the weeks and months follow; a bucket's date is Gregorian either way */
  calendar: Calendar
  tz: string
  /** how many days after its end a service counts as churned */
  churn_grace_days: number
  /** every currency with any number in the range, sorted */
  currencies: string[]
  buckets: ReportBucket[]
  totals: Omit<ReportBucket, 'date' | 'partial'>
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
  /** who took the payment (zarinpal, idpay, nextpay, aqayepardakht, stars, usdt_trc20_auto, ton_auto, manual_card, ...); '' for wallet, unknown and orders paid before it was recorded */
  provider: string
  currency: string
  orders: number
  revenue: number
}

export type Group = 'day' | 'week' | 'month'
// Calendar (calendar.ts): gregorian, whose weeks start on Monday and months are calendar months, or jalali, whose
// weeks start on Saturday and months are Jalali months. It also decides how every date of the page is written,
// typed and picked; the API and the queries keep Gregorian ISO dates.
export type Range = 'today' | 'last_7_days' | 'last_30_days' | 'this_month' | 'last_month' | 'custom'

export const groups: Group[] = ['day', 'week', 'month']
export const calendars: Calendar[] = ['gregorian', 'jalali']

/** defaultCalendar is the calendar a person reads: Jalali in Persian. */
export function defaultCalendar(lang: Lang): Calendar {
  return lang === 'fa' ? 'jalali' : 'gregorian'
}

const calendarKey = 'bobres.reports.calendar'

/** savedCalendar is the calendar the person chose on this browser last time
 * (kept in localStorage like the language and the theme); null when none was
 * chosen, or storage is blocked or holds something else. */
export function savedCalendar(): Calendar | null {
  try {
    const v = localStorage.getItem(calendarKey)
    if (v === 'gregorian' || v === 'jalali') return v
  } catch {
    /* storage blocked: the default applies */
  }
  return null
}

/** rememberCalendar keeps a choice for the next visit. */
export function rememberCalendar(c: Calendar): void {
  try {
    localStorage.setItem(calendarKey, c)
  } catch {
    /* not remembered: fine */
  }
}

/** initialCalendar is the calendar the page starts in: the one chosen before,
 * else the language's own. */
export function initialCalendar(lang: Lang): Calendar {
  return savedCalendar() ?? defaultCalendar(lang)
}

/** csvCalendar is the calendar to ask the ledger and audit CSV files for: the
 * one the Reports page is set to ("jalali" adds a column with the Jalali date
 * of every row), nothing for Gregorian, which is what the files assume. */
export function csvCalendar(lang: Lang): 'jalali' | undefined {
  return initialCalendar(lang) === 'jalali' ? 'jalali' : undefined
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

/** What is wrong with a custom range: a date that is none (or is missing), a
 * date core does not report on (years 2000 to 2100), an end before the start,
 * or more than 400 days. */
export type CustomProblem = 'format' | 'range' | 'order' | 'span'

/** customProblem says why a custom range cannot be asked for, or null when it
 * can: two real dates, in order, inside the years core reports on, at most 400
 * days apart (core refuses more). The dates are Gregorian ISO text whatever
 * calendar they were typed in. */
export function customProblem(from: string, to: string): CustomProblem | null {
  if (!parseIso(from) || !parseIso(to)) return 'format'
  if (!inRange(from) || !inRange(to)) return 'range'
  if (to < from) return 'order'
  if (daysBetween(from, to) + 1 > 400) return 'span'
  return null
}

/** validCustom says whether a custom range can be asked for. */
export function validCustom(from: string, to: string): boolean {
  return customProblem(from, to) === null
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

/** share is a part of a total, in whole percent (0 when the total is 0). */
export function share(part: number, total: number): number {
  return total > 0 ? Math.round((part * 100) / total) : 0
}

/** hasData says whether a report holds anything to show. */
export function hasData(s: ReportSummary): boolean {
  const t = s.totals
  return s.currencies.length > 0 || t.new_customers > 0 || t.trials > 0 || t.churned_services > 0 || t.at_risk > 0
}

/** jalaliMonth writes the Jalali month a date ("2026-10-23") starts, as
 * "Aban 1405" or "آبان ۱۴۰۵": the month's name and its year, nothing else. */
export function jalaliMonth(date: string, lang: Lang): string {
  return formatMonth(date, lang, 'jalali')
}

/** periodLabel names a period by the date it starts on, written in a calendar:
 * a day by its date, a week as "Week of 12 Oct 2026" ("Week of 20 Mehr 1405" in
 * Jalali), a Gregorian month as "Month of 1 Oct 2026". A Jalali month is named
 * by its Jalali month and year.
 *
 * `calendar` is how the report cut its periods and `dates` the calendar the
 * page is set to write them in. They are the same once a report has arrived;
 * for the moment after the calendar was switched, while the old periods are
 * still on screen, the dates are already the new calendar's (a Gregorian month
 * is then "Month of 9 Mehr 1405", which is true) and only the report's own
 * Jalali months get a month's name. */
export function periodLabel(date: string, group: Group, lang: Lang, t: T, calendar: Calendar = 'gregorian', dates: Calendar = calendar): string {
  if (group === 'month' && calendar === 'jalali' && dates === 'jalali') return jalaliMonth(date, lang)
  const d = localDate(date, lang, 'medium', dates)
  return group === 'day' ? d : t('reports.period.' + group + '_of', { date: d })
}
