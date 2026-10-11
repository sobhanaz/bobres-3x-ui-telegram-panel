import { describe, expect, it } from 'vitest'
import { date, latinDigits, money, num } from '../format'

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
