import { describe, expect, it } from 'vitest'
import { bytes, daysLeft, exactMoney, isolate, latinDigits, money, newKey, num, wholeNumber } from '../format'

describe('format', () => {
  it('writes numbers with Persian digits for fa', () => {
    expect(num(120, 'fa')).toBe('۱۲۰')
    expect(num(120, 'en')).toBe('120')
  })

  it('writes money by its scale and currency name', () => {
    expect(money(100000, 'IRT', 0, 'en')).toBe('100,000 Toman')
    expect(money(100000, 'IRT', 0, 'fa')).toContain('تومان')
    expect(money(2_500_000, 'USDT', 6, 'en')).toBe('2.5 USDT')
    expect(money(2_500_000, 'USDT', 6, 'fa')).toContain('تتر')
  })

  it('writes an amount to send with every decimal it has, in Latin digits in either language', () => {
    // money() keeps two decimals, which is right for a price and wrong for "send exactly": a payment of 2.5
    // does not match one that was asked to be 2.500731.
    expect(money(2_500_731, 'USDT', 6, 'en')).toBe('2.5 USDT')
    expect(exactMoney(2_500_731, 'USDT', 6, 'en')).toBe('2.500731 USDT')
    expect(exactMoney(2_500_731, 'USDT', 6, 'fa')).toBe('2.500731 تتر')
    expect(exactMoney(2_500_000, 'USDT', 6, 'en')).toBe('2.5 USDT')
    expect(exactMoney(1, 'USDT', 6, 'en')).toBe('0.000001 USDT')
    expect(exactMoney(150_000, 'IRT', 0, 'en')).toBe('150000 Toman')
  })

  it('writes traffic in MB, GB or TB', () => {
    expect(bytes(512 * 2 ** 20, 'en')).toBe('512 MB')
    expect(bytes(50 * 2 ** 30, 'en')).toBe('50 GB')
    expect(bytes(2 * 2 ** 40, 'en')).toBe('2 TB')
    expect(bytes(50 * 2 ** 30, 'fa')).toContain('گیگابایت')
  })

  it('reads typed amounts in any digits', () => {
    expect(latinDigits('۱۲۳٤٥')).toBe('12345')
    expect(wholeNumber('۱۰۰٬۰۰۰')).toBe(100000)
    expect(wholeNumber('100,000')).toBe(100000)
    expect(wholeNumber('12.5x')).toBeNaN()
    expect(wholeNumber('')).toBeNaN()
    expect(wholeNumber('-5')).toBeNaN()
  })

  it('counts days left', () => {
    const now = Date.UTC(2026, 0, 1)
    expect(daysLeft(now / 1000 + 3 * 86400, now)).toBe(3)
    expect(daysLeft(now / 1000 - 2 * 86400, now)).toBe(-2)
  })

  it('makes distinct valid request keys', () => {
    const a = newKey()
    expect(a).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
    expect(newKey()).not.toBe(a)
  })

  it('isolates left-to-right text', () => {
    expect(isolate('T9x')).toBe('\u2068T9x\u2069')
  })
})
