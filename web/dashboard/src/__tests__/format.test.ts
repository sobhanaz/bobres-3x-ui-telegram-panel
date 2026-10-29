import { describe, expect, it } from 'vitest'
import { ago, asciiNumber, bytes, date, daysLeft, decimalOf, gbOf, latinDigits, minorOf, money, moneyCompact, num, planName, validAmount } from '../format'
import { newKey, query } from '../api'

describe('format', () => {
  it('writes Persian digits for fa and Latin for en', () => {
    expect(num(150000, 'fa')).toBe('۱۵۰٬۰۰۰')
    expect(num(150000, 'en')).toBe('150,000')
  })
  it('shows money in its unit and scale', () => {
    expect(money({ amount: 150000, currency: 'IRT' }, 'en')).toBe('150,000 Toman')
    expect(money({ amount: 150000, currency: 'IRT' }, 'fa')).toBe('۱۵۰٬۰۰۰ تومان')
    expect(money({ amount: 2_500_000, currency: 'USDT' }, 'en')).toBe('2.5 USDT')
  })
  it('knows TON: nanoton, 9 decimals, and its name in each language', () => {
    expect(money({ amount: 2_531_000_000, currency: 'TON' }, 'en')).toBe('2.53 TON')
    expect(money({ amount: 2_531_000_000, currency: 'TON' }, 'fa')).toBe('۲٫۵۳ تون')
    expect(money({ amount: 9_000_000_000, currency: 'TON' }, 'fa')).toBe('۹ تون')
    expect(moneyCompact({ amount: 12_000_000_000_000, currency: 'TON' }, 'en')).toBe('12K TON')
    // the exact amount a wallet shows, all the way down to the last nanoton
    expect(decimalOf({ amount: 2_531_000_000, currency: 'TON' })).toBe('2.531')
    expect(decimalOf({ amount: 2_531_000_007, currency: 'TON' })).toBe('2.531000007')
    expect(decimalOf({ amount: 1, currency: 'TON' })).toBe('0.000000001')
    expect(decimalOf({ amount: 1_000_000_000, currency: 'TON' })).toBe('1')
    expect(minorOf('2.531000007', 'TON')).toBe(2_531_000_007)
    expect(minorOf('0.0000000001', 'TON')).toBeNull() // a tenth of a nanoton
    expect(validAmount('1.123456789', 'TON')).toBe(true)
    expect(validAmount('1.1234567891', 'TON')).toBe(false)
  })
  it('says how long ago a time was, in the language and its digits', () => {
    const now = Date.UTC(2026, 9, 28, 12) // ms
    const at = (secondsAgo: number) => now / 1000 - secondsAgo
    expect(ago(at(12), now, 'en')).toBe('12 seconds ago')
    expect(ago(at(12), now, 'fa')).toBe('۱۲ ثانیه پیش')
    expect(ago(at(89), now, 'en')).toBe('89 seconds ago')
    expect(ago(at(90), now, 'en')).toBe('2 minutes ago')
    expect(ago(at(600), now, 'fa')).toBe('۱۰ دقیقه پیش')
    expect(ago(at(3600), now, 'en')).toBe('60 minutes ago')
    expect(ago(at(3 * 3600), now, 'en')).toBe('3 hours ago')
    expect(ago(at(3 * 86_400), now, 'en')).toBe('3 days ago')
    // a time in the future (a clock that is off) is not a negative age
    expect(ago(at(-30), now, 'en')).toBe('0 seconds ago')
  })
  it('uses the Jalali calendar in Persian', () => {
    const nowruz1405 = Date.UTC(2026, 2, 21, 12) / 1000 // 1 Farvardin 1405
    expect(date(nowruz1405, 'fa')).toContain('۱۴۰۵')
    expect(date(nowruz1405, 'en')).toContain('2026')
  })
  it('reads Persian and Arabic digits', () => {
    expect(latinDigits('۱۲۳٤٥٦')).toBe('123456')
    expect(latinDigits('42')).toBe('42')
  })
})


describe('traffic and typed amounts', () => {
  it('shows traffic in the language', () => {
    expect(bytes(50 * 2 ** 30, 'en')).toBe('50 GB')
    expect(bytes(1.5 * 2 ** 30, 'fa')).toBe('۱٫۵ گیگابایت')
    expect(bytes(512 * 2 ** 20, 'en')).toBe('512 MB')
    expect(bytes(2 * 2 ** 40, 'en')).toBe('2 TB')
  })
  it('reads what staff type, in either digit set', () => {
    expect(asciiNumber('۱۲۰٬۰۰۰')).toBe('120000')
    expect(asciiNumber('2,500')).toBe('2500')
    expect(asciiNumber('۱٫۵')).toBe('1.5')
    expect(asciiNumber('۲/۵')).toBe('2.5')
    expect(asciiNumber(' 12 500 ')).toBe('12500')
  })
  it('checks amounts against the currency', () => {
    expect(validAmount('120000', 'IRT')).toBe(true)
    expect(validAmount('1.5', 'IRT')).toBe(false) // Toman has no fractions
    expect(validAmount('1.234567', 'USDT')).toBe(true)
    expect(validAmount('1.2345678', 'USDT')).toBe(false)
    expect(validAmount('-50', 'IRT')).toBe(false)
    expect(validAmount('-50', 'IRT', true)).toBe(true)
    expect(validAmount('12', 'EUR')).toBe(false)
    expect(validAmount('', 'IRT')).toBe(false)
  })
  it('writes amounts and traffic back exactly', () => {
    expect(decimalOf({ amount: 2_500_000, currency: 'USDT' })).toBe('2.5')
    expect(decimalOf({ amount: 1_234_567, currency: 'USDT' })).toBe('1.234567')
    expect(decimalOf({ amount: 7, currency: 'USDT' })).toBe('0.000007')
    expect(decimalOf({ amount: -1_500_000, currency: 'USDT' })).toBe('-1.5')
    expect(decimalOf({ amount: 120000, currency: 'IRT' })).toBe('120000')
    expect(gbOf(50 * 2 ** 30)).toBe('50')
    expect(gbOf(1234567890)).toBe('1.15')
  })
  it('reads decimals back into minor units exactly', () => {
    expect(minorOf('2.5', 'USDT')).toBe(2_500_000)
    expect(minorOf('0.000007', 'USDT')).toBe(7)
    expect(minorOf('-1.5', 'USDT')).toBe(-1_500_000)
    expect(minorOf('120000', 'IRT')).toBe(120000)
    expect(minorOf(' 42 ', 'IRT')).toBe(42)
    expect(minorOf('-0', 'IRT')).toBe(0)
    expect(minorOf('1.5', 'IRT')).toBeNull() // Toman has no fractions
    expect(minorOf('1.2345678', 'USDT')).toBeNull()
    expect(minorOf('12', 'EUR')).toBeNull()
    expect(minorOf('', 'IRT')).toBeNull()
    expect(minorOf('1e5', 'IRT')).toBeNull()
    expect(minorOf('999999999999999.999999', 'USDT')).toBeNull() // past what a number holds exactly
    for (const [amount, currency] of [[2_500_000, 'USDT'], [1_234_567, 'USDT'], [-7, 'USDT'], [120000, 'IRT']] as const) {
      expect(minorOf(decimalOf({ amount, currency }), currency)).toBe(amount)
    }
  })
  it('names plans and counts days', () => {
    expect(planName({ fa: 'ماهانه', en: 'Monthly' }, 'fa')).toBe('ماهانه')
    expect(planName({ fa: 'ماهانه' }, 'en')).toBe('ماهانه')
    const now = Date.UTC(2026, 9, 7)
    expect(daysLeft(now / 1000 + 3 * 86400 + 60, now)).toBe(3)
    expect(daysLeft(now / 1000 - 86400, now)).toBe(-1)
  })
  it('builds queries and request keys', () => {
    expect(query({ q: 'ali', status: '', page: 2, size: undefined })).toBe('?q=ali&page=2')
    expect(query({})).toBe('')
    expect(newKey()).toMatch(/^[0-9a-f]{32}$/)
    expect(newKey()).not.toBe(newKey())
  })
})
