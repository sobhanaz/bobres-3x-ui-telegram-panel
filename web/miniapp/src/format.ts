// Numbers, money and dates in the customer's language: Persian digits and the
// Jalali calendar for fa, Latin digits and Gregorian dates for en (the
// browser's own Intl, no library).
import { plainAmount } from './pay'

export type Lang = 'fa' | 'en'

const numberLocale = (lang: Lang) => (lang === 'fa' ? 'fa-IR' : 'en-US')

export function num(n: number, lang: Lang): string {
  return new Intl.NumberFormat(numberLocale(lang)).format(n)
}

const names: Record<string, Record<Lang, string>> = {
  IRT: { fa: 'تومان', en: 'Toman' },
  USDT: { fa: 'تتر', en: 'USDT' },
}

/** money writes an amount in minor units in its currency (Toman has none; USDT has 6). */
export function money(amount: number, currency: string, scale: number, lang: Lang): string {
  const value = amount / 10 ** scale
  const formatted = new Intl.NumberFormat(numberLocale(lang), { maximumFractionDigits: Math.min(scale, 2) }).format(value)
  return `${formatted} ${names[currency]?.[lang] ?? currency}`
}

/**
 * exactMoney writes an amount with every decimal it has, in Latin digits in either language: the amount a customer is told
 * to send ("2.500731 USDT") is typed or pasted into a wallet, and a payment of a cent less is not matched to the order.
 */
export function exactMoney(amount: number, currency: string, scale: number, lang: Lang): string {
  return `${plainAmount(amount, scale)} ${names[currency]?.[lang] ?? currency}`
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

export function date(unix: number, lang: Lang): string {
  const locale = lang === 'fa' ? 'fa-IR-u-ca-persian' : 'en-GB'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(unix * 1000))
}

export function dateTime(unix: number, lang: Lang): string {
  const locale = lang === 'fa' ? 'fa-IR-u-ca-persian' : 'en-GB'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(unix * 1000))
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
