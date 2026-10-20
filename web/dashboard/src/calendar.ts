// Calendar dates for the Reports page: Gregorian and Jalali (Persian), both
// ways, exactly. A date is always carried as the Gregorian ISO text the API
// takes ("2026-10-14"); the calendar only decides how it is written, typed and
// picked. Nothing here reads the browser's time zone: a date is calendar
// arithmetic (Date.UTC where a weekday or a day count is needed), never an
// instant, so it cannot slip a day across a zone, a daylight-saving change or
// midnight.
//
// The Jalali arithmetic is the one core and the bot use (the 33-year rule:
// internal/bot/i18n.GregorianToJalali), so a report's Jalali weeks and months
// are the days this file writes. Intl is only the oracle of the tests (the two
// agree for every day from 1900 to 2123 and the API accepts 2000 to 2100).
import type { Lang } from './format'

export type Calendar = 'gregorian' | 'jalali'

export interface YMD {
  y: number
  m: number
  d: number
}

/** The first and last day core reports on (years 2000 to 2100). */
export const DATE_MIN = '2000-01-01'
export const DATE_MAX = '2100-12-31'

const DAY_MS = 86_400_000

// ---------------------------------------------------------------- Gregorian

export function isGregorianLeap(y: number): boolean {
  return (y % 4 === 0 && y % 100 !== 0) || y % 400 === 0
}

export function gregorianMonthLength(y: number, m: number): number {
  return m === 2 ? (isGregorianLeap(y) ? 29 : 28) : [4, 6, 9, 11].includes(m) ? 30 : 31
}

// ------------------------------------------------------------------- Jalali

/** Leap years follow the 33-year rule: in each cycle of 33 years, the years
 * that are a multiple of four from the cycle's start (8 of them) have 30
 * days in Esfand. Cycles start in 1408, 1441, ... (and 1375, 1342, ...). */
export function isJalaliLeap(y: number): boolean {
  return (((25 * y + 11) % 33) + 33) % 33 < 8
}

/** Farvardin to Shahrivar have 31 days, Mehr to Bahman 30, Esfand 29 (30 in a leap year). */
export function jalaliMonthLength(y: number, m: number): number {
  return m <= 6 ? 31 : m <= 11 ? 30 : isJalaliLeap(y) ? 30 : 29
}

const GDM = [0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334]

/** The Jalali date of a Gregorian one (the same arithmetic as core's). */
export function gregorianToJalali(gy: number, gm: number, gd: number): YMD {
  const gy2 = gm > 2 ? gy + 1 : gy
  let days = 355666 + 365 * gy + Math.trunc((gy2 + 3) / 4) - Math.trunc((gy2 + 99) / 100) + Math.trunc((gy2 + 399) / 400) + gd + GDM[gm - 1]
  let jy = -1595 + 33 * Math.trunc(days / 12053)
  days %= 12053
  jy += 4 * Math.trunc(days / 1461)
  days %= 1461
  if (days > 365) {
    jy += Math.trunc((days - 1) / 365)
    days = (days - 1) % 365
  }
  if (days < 186) return { y: jy, m: 1 + Math.trunc(days / 31), d: 1 + (days % 31) }
  return { y: jy, m: 7 + Math.trunc((days - 186) / 30), d: 1 + ((days - 186) % 30) }
}

/** The Gregorian date of a Jalali one. The caller has checked that the
 * Jalali date exists (jalaliMonthLength); the result is the exact inverse of
 * gregorianToJalali. */
export function jalaliToGregorian(jy: number, jm: number, jd: number): YMD {
  const y = jy + 1595
  let days =
    -355668 + 365 * y + Math.trunc(y / 33) * 8 + Math.trunc(((y % 33) + 3) / 4) + jd + (jm < 7 ? (jm - 1) * 31 : (jm - 7) * 30 + 186)
  let gy = 400 * Math.trunc(days / 146097)
  days %= 146097
  if (days > 36524) {
    gy += 100 * Math.trunc(--days / 36524)
    days %= 36524
    if (days >= 365) days++
  }
  gy += 4 * Math.trunc(days / 1461)
  days %= 1461
  if (days > 365) {
    gy += Math.trunc((days - 1) / 365)
    days = (days - 1) % 365
  }
  let gd = days + 1
  let gm = 0
  while (gm < 12 && gd > gregorianMonthLength(gy, gm + 1)) {
    gd -= gregorianMonthLength(gy, gm + 1)
    gm++
  }
  return { y: gy, m: gm + 1, d: gd }
}

// --------------------------------------------------------------------- ISO

const ISO = /^(\d{4})-(\d{2})-(\d{2})$/

/** parseIso reads "2026-10-14"; null for text that is no date of the
 * Gregorian calendar (month 13, 31 September, 29 February of a common year). */
export function parseIso(s: string): YMD | null {
  const m = ISO.exec(s)
  if (!m) return null
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])]
  if (mo < 1 || mo > 12 || d < 1 || d > gregorianMonthLength(y, mo)) return null
  return { y, m: mo, d }
}

const pad = (n: number, width: number) => String(n).padStart(width, '0')

export function toIso(v: YMD): string {
  return `${pad(v.y, 4)}-${pad(v.m, 2)}-${pad(v.d, 2)}`
}

/** addDays moves a date by whole days (negative goes back). */
export function addDays(iso: string, n: number): string {
  const v = parseIso(iso)
  if (!v) return iso
  const t = new Date(Date.UTC(v.y, v.m - 1, v.d) + n * DAY_MS)
  return toIso({ y: t.getUTCFullYear(), m: t.getUTCMonth() + 1, d: t.getUTCDate() })
}

/** weekday is 0 for Sunday to 6 for Saturday. */
export function weekday(iso: string): number {
  const v = parseIso(iso)
  return v ? new Date(Date.UTC(v.y, v.m - 1, v.d)).getUTCDay() : 0
}

// ------------------------------------------------------- in a given calendar

/** partsOf is a Gregorian ISO date as year, month and day of a calendar. */
export function partsOf(iso: string, calendar: Calendar): YMD {
  const g = parseIso(iso)
  if (!g) return { y: 0, m: 0, d: 0 }
  return calendar === 'jalali' ? gregorianToJalali(g.y, g.m, g.d) : g
}

export function monthLength(calendar: Calendar, y: number, m: number): number {
  return calendar === 'jalali' ? jalaliMonthLength(y, m) : gregorianMonthLength(y, m)
}

/** isoOf is the Gregorian ISO date of a year, month and day of a calendar;
 * null when that date does not exist there. */
export function isoOf(calendar: Calendar, y: number, m: number, d: number): string | null {
  if (!Number.isInteger(y) || !Number.isInteger(m) || !Number.isInteger(d) || y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 || d > monthLength(calendar, y, m)) return null
  const g = calendar === 'jalali' ? jalaliToGregorian(y, m, d) : { y, m, d }
  return g.y >= 0 && g.y <= 9999 ? toIso(g) : null
}

/** inRange says whether core reports on a date. */
export function inRange(iso: string): boolean {
  return iso >= DATE_MIN && iso <= DATE_MAX
}

/** firstWeekday is the weekday a week starts on in a calendar's grid, as
 * the report cuts its weeks: Saturday for Jalali, Monday for Gregorian. */
export function firstWeekday(calendar: Calendar): number {
  return calendar === 'jalali' ? 6 : 1
}

/** monthGrid lays a month out in weeks: ISO dates, and null for the empty
 * cells before the 1st and after the last day. */
export function monthGrid(calendar: Calendar, y: number, m: number): (string | null)[][] {
  const first = isoOf(calendar, y, m, 1)
  if (!first) return []
  const lead = (weekday(first) - firstWeekday(calendar) + 7) % 7
  const cells: (string | null)[] = Array.from({ length: lead }, () => null)
  for (let d = 1; d <= monthLength(calendar, y, m); d++) cells.push(addDays(first, d - 1))
  while (cells.length % 7 !== 0) cells.push(null)
  const weeks: (string | null)[][] = []
  for (let i = 0; i < cells.length; i += 7) weeks.push(cells.slice(i, i + 7))
  return weeks
}

/** shiftMonth moves a year and month by whole months, in a calendar. */
export function shiftMonth(v: Pick<YMD, 'y' | 'm'>, by: number): Pick<YMD, 'y' | 'm'> {
  const n = v.y * 12 + (v.m - 1) + by
  return { y: Math.floor(n / 12), m: (n % 12 + 12) % 12 + 1 }
}

// ---------------------------------------------------------------- writing

const jalaliMonths: Record<Lang, string[]> = {
  fa: ['فروردین', 'اردیبهشت', 'خرداد', 'تیر', 'مرداد', 'شهریور', 'مهر', 'آبان', 'آذر', 'دی', 'بهمن', 'اسفند'],
  en: ['Farvardin', 'Ordibehesht', 'Khordad', 'Tir', 'Mordad', 'Shahrivar', 'Mehr', 'Aban', 'Azar', 'Dey', 'Bahman', 'Esfand'],
}

const PERSIAN_DIGITS = '۰۱۲۳۴۵۶۷۸۹'

/** digits writes the digits of a text the way the language does (Persian
 * digits in Persian, Latin in English). */
export function digits(s: string, lang: Lang): string {
  return lang === 'fa' ? s.replace(/\d/g, (d) => PERSIAN_DIGITS[Number(d)]) : s
}

/** latin turns Persian and Arabic-Indic digits into 0-9. */
function latin(s: string): string {
  return s.replace(/[۰-۹]/g, (d) => String(d.charCodeAt(0) - 0x06f0)).replace(/[٠-٩]/g, (d) => String(d.charCodeAt(0) - 0x0660))
}

const formatters = new Map<string, Intl.DateTimeFormat>()
function intl(locale: string, opts: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const key = locale + JSON.stringify(opts)
  let f = formatters.get(key)
  if (!f) {
    f = new Intl.DateTimeFormat(locale, { ...opts, timeZone: 'UTC' })
    formatters.set(key, f)
  }
  return f
}

function utcDate(iso: string): Date {
  const v = parseIso(iso)
  return v ? new Date(Date.UTC(v.y, v.m - 1, v.d)) : new Date(NaN)
}

/** monthName names a month of a calendar in the language. */
export function monthName(calendar: Calendar, lang: Lang, m: number, y = 2026): string {
  if (calendar === 'jalali') return jalaliMonths[lang][m - 1] ?? ''
  return intl(lang === 'fa' ? 'fa-IR-u-ca-gregory' : 'en-GB', { month: 'long' }).format(new Date(Date.UTC(y, m - 1, 1)))
}

/** formatDate writes a date ("2026-10-14") in a calendar and a language:
 * "14 Oct 2026", "22 Mehr 1405", "۱۴ اکتبر ۲۰۲۶", "۲۲ مهر ۱۴۰۵"; short leaves
 * the year out ("14 Oct"). Digits follow the language, the month names the
 * calendar. */
export function formatDate(iso: string, lang: Lang, calendar: Calendar, style: 'medium' | 'short' = 'medium'): string {
  if (!parseIso(iso)) return iso
  if (calendar === 'jalali') {
    const v = partsOf(iso, 'jalali')
    const day = digits(String(v.d), lang)
    const name = jalaliMonths[lang][v.m - 1]
    return style === 'short' ? `${day} ${name}` : `${day} ${name} ${digits(String(v.y), lang)}`
  }
  const locale = lang === 'fa' ? 'fa-IR-u-ca-gregory' : 'en-GB'
  return intl(locale, style === 'short' ? { month: 'short', day: 'numeric' } : { dateStyle: 'medium' }).format(utcDate(iso))
}

/** formatMonth writes the month a date falls in with its year ("Aban 1405",
 * "آبان ۱۴۰۵", "Oct 2026"). */
export function formatMonth(iso: string, lang: Lang, calendar: Calendar): string {
  const v = partsOf(iso, calendar)
  if (calendar === 'jalali') return `${jalaliMonths[lang][v.m - 1]} ${digits(String(v.y), lang)}`
  return intl(lang === 'fa' ? 'fa-IR-u-ca-gregory' : 'en-GB', { month: 'short', year: 'numeric' }).format(utcDate(iso))
}

/** weekdayName names a day of the week (0 Sunday to 6 Saturday), in full
 * or as the one or two letters over a grid. */
export function weekdayName(wd: number, lang: Lang, style: 'long' | 'narrow'): string {
  const sunday = new Date(Date.UTC(2026, 9, 18)) // a Sunday
  const d = new Date(sunday.getTime() + wd * DAY_MS)
  if (style === 'narrow' && lang === 'en') return ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'][wd]
  return intl(lang === 'fa' ? 'fa-IR' : 'en-GB', { weekday: style }).format(d)
}

/** formatInput writes a date the way it is typed into a field: year, month
 * and day, with the separator of the language (a slash in Persian, a dash in
 * English) and its digits. */
export function formatInput(iso: string, lang: Lang, calendar: Calendar): string {
  if (!parseIso(iso)) return ''
  const v = partsOf(iso, calendar)
  const sep = lang === 'fa' ? '/' : '-'
  return digits(`${pad(v.y, 4)}${sep}${pad(v.m, 2)}${sep}${pad(v.d, 2)}`, lang)
}

// ----------------------------------------------------------------- reading

// Marks that sit in a typed date without being part of it: directional marks
// and isolates, joiners, the byte-order mark, a no-break space.
const INVISIBLE = /[​-‏‪-‮⁦-⁩﻿ ]/g
// What may separate year, month and day: a slash, a dash, a dot, a comma, a
// space, an underscore, a backslash, the Persian decimal and comma marks, and
// the dashes and minus signs of other keyboards.
const SEPARATORS = /[\s/\-._,\\،٫٬‐-―−]+/

/** parseInput reads a date typed in a calendar: year, month and day (four
 * digits for the year, one or two for the others), in any digit set, with a
 * slash, a dash, a dot or a space between them; or eight digits together.
 * It returns the Gregorian ISO date, or null when the text is no date of
 * that calendar (a month of 31 days that has 30, Esfand 30 of a common year). */
export function parseInput(text: string, calendar: Calendar): string | null {
  const s = latin(text).replace(INVISIBLE, '').trim()
  let parts: string[]
  if (/^\d{8}$/.test(s)) parts = [s.slice(0, 4), s.slice(4, 6), s.slice(6, 8)]
  else parts = s.split(SEPARATORS)
  if (parts.length !== 3 || !parts.every((p) => /^\d+$/.test(p))) return null
  const [ys, ms, ds] = parts
  if (ys.length !== 4 || ms.length > 2 || ds.length > 2) return null
  return isoOf(calendar, Number(ys), Number(ms), Number(ds))
}

// --------------------------------------------------------------------- today

/** todayIn is today's date in a time zone ("Asia/Tehran"), the way core
 * reads "today" for a report; the browser's own zone when none is given or
 * the browser does not know it. */
export function todayIn(tz?: string, now: number = Date.now()): string {
  try {
    const parts = new Intl.DateTimeFormat('en-US', { timeZone: tz || undefined, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date(now))
    const pick = (type: string) => Number(parts.find((p) => p.type === type)?.value)
    return toIso({ y: pick('year'), m: pick('month'), d: pick('day') })
  } catch {
    const d = new Date(now)
    return toIso({ y: d.getFullYear(), m: d.getMonth() + 1, d: d.getDate() })
  }
}

/** daysBetween counts the days from one date to another: 2026-10-02 to
 * 2026-10-03 is 1, and negative when the second date is the earlier. */
export function daysBetween(from: string, to: string): number {
  return Math.round((utcDate(to).getTime() - utcDate(from).getTime()) / DAY_MS)
}
