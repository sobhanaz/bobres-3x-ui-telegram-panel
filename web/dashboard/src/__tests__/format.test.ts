import { describe, expect, it } from 'vitest'
import { asciiNumber, bytes, date, daysLeft, decimalOf, gbOf, latinDigits, money, num, planName, validAmount } from '../format'
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
