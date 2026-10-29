// Numbers, money and dates in the dashboard's language: Persian digits and
// the Jalali calendar for fa (the browser's own Intl, no library), Latin
// digits and Gregorian dates for en.
import { reactive } from 'vue'
import { formatDate, type Calendar } from './calendar'

export type Lang = 'fa' | 'en'

export interface Money {
  amount: number
  currency: string
}

const numberLocale = (lang: Lang) => (lang === 'fa' ? 'fa-IR' : 'en-US')

export function num(n: number, lang: Lang): string {
  return new Intl.NumberFormat(numberLocale(lang)).format(n)
}

// Currencies BOBRES charges in, with their minor-unit scale (TON: nanoton, 9 decimals).
const scales: Record<string, number> = { IRT: 0, USDT: 6, XTR: 0, TON: 9 }
const names: Record<string, Record<Lang, string>> = {
  IRT: { fa: 'تومان', en: 'Toman' },
  USDT: { fa: 'تتر', en: 'USDT' },
  XTR: { fa: 'ستاره', en: 'Stars' },
  TON: { fa: 'تون', en: 'TON' },
}

// The store's own name for Toman (Branding page), per language; "" = the
// default above. Reactive, so amounts already on screen follow a change.
const irtNames = reactive<Record<Lang, string>>({ fa: '', en: '' })

/** setCurrencyNames sets how Toman is written in each language (empty or
 * missing = the default name). */
export function setCurrencyNames(custom: { fa?: string; en?: string }): void {
  irtNames.fa = custom.fa?.trim() ?? ''
  irtNames.en = custom.en?.trim() ?? ''
}

function currencyName(currency: string, lang: Lang): string {
  if (currency === 'IRT' && irtNames[lang]) return irtNames[lang]
  return names[currency]?.[lang] ?? currency
}

/** money writes an amount in its currency; signed adds "+" to gains (the
 * number formatter places signs right in both directions). */
export function money(m: Money, lang: Lang, signed = false): string {
  const scale = scales[m.currency] ?? 0
  const value = m.amount / 10 ** scale
  const formatted = new Intl.NumberFormat(numberLocale(lang), {
    maximumFractionDigits: Math.min(scale, 2),
    signDisplay: signed ? 'exceptZero' : 'auto',
  }).format(value)
  return `${formatted} ${currencyName(m.currency, lang)}`
}

/** percent writes whole percent in the language ("50%", "۵۰٪"). */
export function percent(n: number, lang: Lang): string {
  return new Intl.NumberFormat(numberLocale(lang), { style: 'percent', maximumFractionDigits: 0 }).format(n / 100)
}

/** percentBP writes hundredths of a percent ("2.5%", "۲٫۵٪"; 250 is 2.5%). */
export function percentBP(bp: number, lang: Lang): string {
  return new Intl.NumberFormat(numberLocale(lang), { style: 'percent', maximumFractionDigits: 2 }).format(bp / 10000)
}

/** shortId is the part of an id the bot shows customers: its last 6 hex digits. */
export function shortId(id: string): string {
  return id.replaceAll('-', '').slice(-6)
}

/** isolate keeps a name or id that reads left to right whole inside a
 * sentence in either direction ("@boss" must not become "boss@"). */
export function isolate(s: string): string {
  return '\u2068' + s + '\u2069'
}

export function dateTime(unix: number, lang: Lang): string {
  const locale = lang === 'fa' ? 'fa-IR-u-ca-persian' : 'en-GB'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(unix * 1000))
}

/** ago says how long before nowMs a time was, in the language and its digits ("12 seconds ago", "۵ دقیقه پیش"):
 * seconds up to a minute and a half, then minutes, hours and days. A time that has not come yet (a clock that is
 * off) is "0 seconds ago". */
export function ago(unix: number, nowMs: number, lang: Lang): string {
  const rtf = new Intl.RelativeTimeFormat(numberLocale(lang), { numeric: 'always' })
  const seconds = Math.max(0, Math.floor(nowMs / 1000 - unix))
  if (seconds < 90) return rtf.format(-seconds, 'second')
  const minutes = Math.round(seconds / 60)
  if (minutes < 90) return rtf.format(-minutes, 'minute')
  const hours = Math.round(minutes / 60)
  if (hours < 48) return rtf.format(-hours, 'hour')
  return rtf.format(-Math.round(hours / 24), 'day')
}

export function date(unix: number, lang: Lang): string {
  const locale = lang === 'fa' ? 'fa-IR-u-ca-persian' : 'en-GB'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(unix * 1000))
}

/** localDate writes a calendar date given as "2026-10-14" (a date in the
 * store's time zone, not an instant) in a calendar: the one asked for, else
 * the language's own (Jalali in Persian). Digits follow the language, month
 * names the calendar. It is calendar arithmetic, so the browser's own zone can
 * never move the date to the day before or after. */
export function localDate(ymd: string, lang: Lang, style: 'medium' | 'short' = 'medium', calendar: Calendar = lang === 'fa' ? 'jalali' : 'gregorian'): string {
  return formatDate(ymd, lang, calendar, style)
}

/** moneyCompact writes an amount short for a chart's axis ("1.2M Toman"). */
export function moneyCompact(m: Money, lang: Lang): string {
  const scale = scales[m.currency] ?? 0
  const formatted = new Intl.NumberFormat(numberLocale(lang), { notation: 'compact', maximumFractionDigits: 1 }).format(m.amount / 10 ** scale)
  return `${formatted} ${currencyName(m.currency, lang)}`
}

/** latinDigits turns Persian and Arabic-Indic digits into 0-9 (typed codes, amounts). */
export function latinDigits(s: string): string {
  return s.replace(/[۰-۹]/g, (d) => String(d.charCodeAt(0) - 0x06f0)).replace(/[٠-٩]/g, (d) => String(d.charCodeAt(0) - 0x0660))
}

const byteUnits: Record<Lang, [string, string, string]> = {
  fa: ['مگابایت', 'گیگابایت', 'ترابایت'],
  en: ['MB', 'GB', 'TB'],
}

/** bytes shows a traffic amount in MB, GB or TB (1 GB = 2^30 bytes, as the bot counts). */
export function bytes(n: number, lang: Lang): string {
  const [mb, gb, tb] = byteUnits[lang]
  const fmt = (v: number) => new Intl.NumberFormat(numberLocale(lang), { maximumFractionDigits: 2 }).format(v)
  if (n >= 2 ** 40) return `${fmt(n / 2 ** 40)} ${tb}`
  if (n >= 2 ** 30) return `${fmt(n / 2 ** 30)} ${gb}`
  return `${fmt(n / 2 ** 20)} ${mb}`
}

/** asciiNumber turns typed text into a plain decimal: Persian and Arabic
 * digits, the Persian decimal separator, and no thousands separators. */
export function asciiNumber(s: string): string {
  return latinDigits(s)
    .replace(/[\u066b\/]/g, '.') // Persian decimal separator (and the slash often typed for it)
    .replace(/[,\u066c\u060c\s]/g, '')
}

/** validAmount checks a typed amount (after asciiNumber) for a currency. */
export function validAmount(s: string, currency: string, signed = false): boolean {
  const scale = scales[currency]
  if (scale === undefined) return false
  const re = new RegExp(`^${signed ? '-?' : ''}\\d{1,15}(\\.\\d{1,${Math.max(scale, 1)}})?$`)
  if (!re.test(s)) return false
  return scale > 0 || !s.includes('.')
}

/** planName is a plan's name in the language, or in the other one. */
export function planName(names: Record<string, string> | undefined, lang: Lang): string {
  if (!names) return ''
  return names[lang] || names.en || names.fa || ''
}

/** daysLeft is the whole days until a unix time (negative once past). */
export function daysLeft(unix: number, now = Date.now()): number {
  return Math.floor((unix * 1000 - now) / 86_400_000)
}

/** currencies staff can choose for prices and balances. */
export const currencies = ['IRT', 'USDT'] as const

/** decimalOf writes minor units as the plain decimal staff type ("2.5"). */
export function decimalOf(m: Money): string {
  const scale = scales[m.currency] ?? 0
  if (scale === 0) return String(m.amount)
  const neg = m.amount < 0
  const digits = String(Math.abs(m.amount)).padStart(scale + 1, '0')
  const frac = digits.slice(-scale).replace(/0+$/, '')
  return (neg ? '-' : '') + digits.slice(0, -scale) + (frac ? '.' + frac : '')
}

/** minorOf reads a plain decimal ("2.5", "-1200") as minor units of a
 * currency, the inverse of decimalOf; null when it is not one. */
export function minorOf(s: string, currency: string): number | null {
  const scale = scales[currency]
  if (scale === undefined) return null
  const m = /^(-?)(\d{1,15})(?:\.(\d+))?$/.exec(s.trim())
  if (!m || (m[3] ?? '').length > scale) return null
  const n = Number(m[2] + (m[3] ?? '').padEnd(scale, '0'))
  if (!Number.isSafeInteger(n)) return null
  return m[1] && n ? -n : n
}

/** gbOf writes a byte count as GB with at most 3 decimals ("1.5"). */
export function gbOf(n: number): string {
  return String(Math.round((n / 2 ** 30) * 1000) / 1000)
}
