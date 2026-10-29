import { describe, expect, it } from 'vitest'
import { ago, bytes, clock, daysLeft, exactMoney, isolate, latinDigits, money, newKey, num, wholeNumber, wholeUsdt } from '../format'

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

  it('reads a whole number of USDT, and takes a point or a lone comma for a decimal mark, not a thousands separator', () => {
    for (const [typed, n] of [['25', 25], ['۲۵', 25], ['٢٥', 25], [' 7 ', 7], ['007', 7], ['1,000', 1000], ['۱٬۰۰۰', 1000], ['10,000', 10000], ['10 000', 10000], ['1,000,000', 1_000_000]] as const) {
      expect(wholeUsdt(typed), JSON.stringify(typed)).toBe(n)
    }
    // wholeNumber reads "0.5" and "0,5" as 5 (a thousands separator); for USDT that would be ten times what was typed
    expect(wholeNumber('0.5')).toBe(5)
    for (const typed of ['0.5', '0,5', '۰٫۵', '1.5', '5.0', '5.', '1.000', '1,00', '12,34', '-5', '+5', '1e3', 'abc', '12x', '', '  ', '1,,000', '1 0 0']) {
      expect(wholeUsdt(typed), JSON.stringify(typed)).toBeNaN()
    }
  })

  it('writes a time left as minutes and seconds, in the digits of the language', () => {
    expect(clock(120, 'en')).toBe('02:00')
    expect(clock(119, 'en')).toBe('01:59')
    expect(clock(59.4, 'en')).toBe('00:59')
    expect(clock(1, 'en')).toBe('00:01')
    expect(clock(0, 'en')).toBe('00:00')
    expect(clock(-5, 'en')).toBe('00:00') // never a minus sign
    expect(clock(600, 'en')).toBe('10:00')
    expect(clock(30 * 60 + 5, 'en')).toBe('30:05')
    expect(clock(120, 'fa')).toBe('۰۲:۰۰')
    expect(clock(119, 'fa')).toBe('۰۱:۵۹')
    expect(clock(0, 'fa')).toBe('۰۰:۰۰')
    expect(clock(3 * 3600, 'en')).toBe('180:00') // minutes keep counting past an hour
  })

  it('counts days left', () => {
    const now = Date.UTC(2026, 0, 1)
    expect(daysLeft(now / 1000 + 3 * 86400, now)).toBe(3)
    expect(daysLeft(now / 1000 - 2 * 86400, now)).toBe(-2)
  })

  it('says how long ago something was, in minutes, hours and days, in either language', () => {
    const now = Date.UTC(2026, 0, 10, 12)
    const before = (seconds: number) => now / 1000 - seconds
    expect(ago(before(5), now, 'en')).toBe('now')
    expect(ago(before(59), now, 'en')).toBe('now')
    expect(ago(before(60), now, 'en')).toBe('1 minute ago')
    expect(ago(before(5 * 60 + 30), now, 'en')).toBe('5 minutes ago')
    expect(ago(before(59 * 60 + 59), now, 'en')).toBe('59 minutes ago')
    expect(ago(before(3600), now, 'en')).toBe('1 hour ago')
    expect(ago(before(23 * 3600 + 3599), now, 'en')).toBe('23 hours ago')
    expect(ago(before(86400), now, 'en')).toBe('yesterday')
    expect(ago(before(3 * 86400 + 100), now, 'en')).toBe('3 days ago')
    expect(ago(before(45 * 86400), now, 'en')).toBe('45 days ago')
    // a clock that is behind the server's is not a time in the future
    expect(ago(before(-3600), now, 'en')).toBe('now')
    // Persian digits and words
    expect(ago(before(3 * 3600), now, 'fa')).toBe('۳ ساعت پیش')
    expect(ago(before(3 * 86400), now, 'fa')).toBe('۳ روز پیش')
    expect(ago(before(86400), now, 'fa')).toBe('دیروز')
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
