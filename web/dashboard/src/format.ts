// Numbers, money and dates in the dashboard's language: Persian digits and
// the Jalali calendar for fa (the browser's own Intl, no library), Latin
// digits and Gregorian dates for en.

export type Lang = 'fa' | 'en'

export interface Money {
  amount: number
  currency: string
}

const numberLocale = (lang: Lang) => (lang === 'fa' ? 'fa-IR' : 'en-US')

export function num(n: number, lang: Lang): string {
  return new Intl.NumberFormat(numberLocale(lang)).format(n)
}

// Currencies BOBRES charges in, with their minor-unit scale.
const scales: Record<string, number> = { IRT: 0, USDT: 6, XTR: 0 }
const names: Record<string, Record<Lang, string>> = {
  IRT: { fa: 'تومان', en: 'Toman' },
  USDT: { fa: 'تتر', en: 'USDT' },
  XTR: { fa: 'ستاره', en: 'Stars' },
}

export function money(m: Money, lang: Lang): string {
  const scale = scales[m.currency] ?? 0
  const value = m.amount / 10 ** scale
  const formatted = new Intl.NumberFormat(numberLocale(lang), { maximumFractionDigits: Math.min(scale, 2) }).format(value)
  return `${formatted} ${names[m.currency]?.[lang] ?? m.currency}`
}

export function dateTime(unix: number, lang: Lang): string {
  const locale = lang === 'fa' ? 'fa-IR-u-ca-persian' : 'en-GB'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(unix * 1000))
}

export function date(unix: number, lang: Lang): string {
  const locale = lang === 'fa' ? 'fa-IR-u-ca-persian' : 'en-GB'
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(unix * 1000))
}

/** latinDigits turns Persian and Arabic-Indic digits into 0-9 (typed codes, amounts). */
export function latinDigits(s: string): string {
  return s.replace(/[۰-۹]/g, (d) => String(d.charCodeAt(0) - 0x06f0)).replace(/[٠-٩]/g, (d) => String(d.charCodeAt(0) - 0x0660))
}
