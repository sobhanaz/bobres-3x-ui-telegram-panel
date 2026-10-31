// Numbers, money and dates in the customer's language (the browser's own
// Intl, no library): Persian digits and the Jalali calendar for fa,
// Arabic-Indic digits for ar, Gregorian dates and each language's own
// separators for en, ar, ru and tr.
import { plainAmount } from './pay'

/** Lang is a language of the app: Persian and English, and in beta (machine-translated, awaiting a native review) Arabic,
 * Russian and Turkish. */
export type Lang = 'fa' | 'en' | 'ar' | 'ru' | 'tr'

export const LANGS: readonly Lang[] = ['fa', 'en', 'ar', 'ru', 'tr']

/** isLang tells a language of the app from any other string. */
export function isLang(v: unknown): v is Lang {
  return typeof v === 'string' && (LANGS as readonly string[]).includes(v)
}

/** rtl: the language is written right to left (Persian, Arabic). */
export const rtl = (lang: Lang): boolean => lang === 'fa' || lang === 'ar'

const numberLocales: Record<Lang, string> = { fa: 'fa-IR', en: 'en-US', ar: 'ar-u-nu-arab', ru: 'ru-RU', tr: 'tr-TR' }
const dateLocales: Record<Lang, string> = { fa: 'fa-IR-u-ca-persian', en: 'en-GB', ar: 'ar-u-ca-gregory-nu-arab', ru: 'ru-RU', tr: 'tr-TR' }

/** numberLocale is the Intl locale of the language's numbers. */
export const numberLocale = (lang: Lang): string => numberLocales[lang] ?? 'en-US'

export function num(n: number, lang: Lang): string {
  return new Intl.NumberFormat(numberLocale(lang)).format(n)
}

// A name with forms per plural category ("one|few|many|other") takes the one its amount needs.
const names: Record<string, Record<Lang, string>> = {
  IRT: { fa: 'تومان', en: 'Toman', ar: 'تومان', ru: 'томан|томана|томанов|томана', tr: 'Toman' },
  USDT: { fa: 'تتر', en: 'USDT', ar: 'USDT', ru: 'USDT', tr: 'USDT' },
  TON: { fa: 'تون', en: 'TON', ar: 'TON', ru: 'TON', tr: 'TON' },
}
const ruCategories = ['one', 'few', 'many', 'other']

function currencyName(currency: string, lang: Lang, value: number): string {
  const name = names[currency]?.[lang] ?? currency
  if (!name.includes('|')) return name
  const forms = name.split('|')
  const i = ruCategories.indexOf(new Intl.PluralRules(numberLocale(lang)).select(Math.abs(value)))
  return forms[i >= 0 ? i : forms.length - 1]
}

/** money writes an amount in minor units in its currency (Toman has none; USDT has 6; TON has 9). */
export function money(amount: number, currency: string, scale: number, lang: Lang): string {
  const value = amount / 10 ** scale
  const digits = Math.min(scale, 2)
  const formatted = new Intl.NumberFormat(numberLocale(lang), { maximumFractionDigits: digits }).format(value)
  return `${formatted} ${currencyName(currency, lang, Number(value.toFixed(digits)))}`
}

/**
 * exactMoney writes an amount with every decimal it has, in Latin digits in either language: the amount a customer is told
 * to send ("2.500731 USDT") is typed or pasted into a wallet, and a payment of a cent less is not matched to the order.
 */
export function exactMoney(amount: number, currency: string, scale: number, lang: Lang): string {
  return `${plainAmount(amount, scale)} ${currencyName(currency, lang, amount / 10 ** scale)}`
}

const byteUnits: Record<Lang, [string, string, string]> = {
  fa: ['مگابایت', 'گیگابایت', 'ترابایت'],
  en: ['MB', 'GB', 'TB'],
  ar: ['ميغابايت', 'غيغابايت', 'تيرابايت'],
  ru: ['МБ', 'ГБ', 'ТБ'],
  tr: ['MB', 'GB', 'TB'],
}

/** bytes shows a traffic amount in MB, GB or TB (1 GB = 2^30 bytes, as the bot counts). */
export function bytes(n: number, lang: Lang): string {
  const [mb, gb, tb] = byteUnits[lang]
  const fmt = (v: number) => new Intl.NumberFormat(numberLocale(lang), { maximumFractionDigits: 2 }).format(v)
  if (n >= 2 ** 40) return `${fmt(n / 2 ** 40)} ${tb}`
  if (n >= 2 ** 30) return `${fmt(n / 2 ** 30)} ${gb}`
  return `${fmt(n / 2 ** 20)} ${mb}`
}

export function date(unix: number, lang: Lang): string {
  const locale = dateLocales[lang] ?? 'en-GB'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(unix * 1000))
}

export function dateTime(unix: number, lang: Lang): string {
  const locale = dateLocales[lang] ?? 'en-GB'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(unix * 1000))
}

/**
 * ago says how long before nowMs a time was, in the customer's language and digits ("5 minutes ago", "yesterday",
 * "۳ روز پیش"): minutes up to an hour, hours up to a day, then days. A time that has not come yet (a clock that is off) is "now".
 */
export function ago(unix: number, nowMs: number, lang: Lang): string {
  const rtf = new Intl.RelativeTimeFormat(numberLocale(lang), { numeric: 'auto' })
  const seconds = Math.max(0, Math.floor(nowMs / 1000 - unix))
  if (seconds < 60) return rtf.format(0, 'second')
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return rtf.format(-minutes, 'minute')
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return rtf.format(-hours, 'hour')
  return rtf.format(-Math.floor(hours / 24), 'day')
}

/** latinDigits turns Persian and Arabic-Indic digits into 0-9 (typed amounts). */
export function latinDigits(s: string): string {
  return s.replace(/[۰-۹]/g, (d) => String(d.charCodeAt(0) - 0x06f0)).replace(/[٠-٩]/g, (d) => String(d.charCodeAt(0) - 0x0660))
}

/** wholeNumber reads a typed whole number ("۱۰۰٬۰۰۰", "100,000"); NaN when it is not one. */
export function wholeNumber(s: string): number {
  const t = latinDigits(s).replace(/[\s,٬،.]/g, '')
  return /^\d{1,12}$/.test(t) ? Number(t) : NaN
}

/**
 * wholeUsdt reads a typed whole number of USDT ("25", "۲۵", "1,000"; NaN when it is not one). Unlike wholeNumber it takes no point
 * for a thousands separator, and a comma (or a space) only between groups of three: "0.5" and "0,5" are half a USDT, which is
 * not a whole number, and must not become 5.
 */
export function wholeUsdt(s: string): number {
  const t = latinDigits(s).trim()
  if (!/^\d{1,12}$/.test(t) && !/^\d{1,3}(?:[,٬،\s]\d{3}){1,3}$/.test(t)) return NaN
  return Number(t.replace(/[,٬،\s]/g, ''))
}

/** clock writes a time left as minutes:seconds ("02:05"; with Persian digits in fa). */
export function clock(seconds: number, lang: Lang): string {
  const two = new Intl.NumberFormat(numberLocale(lang), { minimumIntegerDigits: 2, useGrouping: false })
  const s = Math.max(0, Math.floor(seconds))
  return `${two.format(Math.floor(s / 60))}:${two.format(s % 60)}`
}

/** daysLeft is whole days until a time (negative once past). */
export function daysLeft(unix: number, now: number): number {
  return Math.ceil((unix * 1000 - now) / 86_400_000)
}

/** newKey is a random request key for one purchase (letters and digits, 24). */
export function newKey(): string {
  const raw = new Uint8Array(12)
  crypto.getRandomValues(raw)
  return Array.from(raw, (b) => b.toString(16).padStart(2, '0')).join('')
}

/** isolate keeps text that reads left to right whole inside a sentence in either direction. */
export function isolate(s: string): string {
  return '\u2068' + s + '\u2069'
}
