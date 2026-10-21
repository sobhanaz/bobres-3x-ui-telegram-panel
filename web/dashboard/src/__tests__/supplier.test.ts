import { describe, expect, it } from 'vitest'
import { costText, maxPrice, orderSeverity, parsePrice, sellableProblem } from '../supplier'
import { keyPattern } from '../supplierkey'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

describe('the Digital accounts page rules', () => {
  it('reads a price in Toman: whole, positive, at most core\'s limit, in any digits', () => {
    expect(parsePrice('450000')).toBe(450000)
    expect(parsePrice('450,000')).toBe(450000)
    expect(parsePrice(' ۴۵۰٬۰۰۰ ')).toBe(450000)
    expect(parsePrice('٤٥٠٠٠٠')).toBe(450000)
    expect(parsePrice(String(maxPrice))).toBe(maxPrice)
    for (const bad of ['', '0', '-5', '1.5', '12a', String(maxPrice + 1), '9'.repeat(14)]) expect(parsePrice(bad), bad).toBeNull()
  })
  it('writes the supplier\'s cost', () => {
    expect(costText(2_600_000, 'USDT')).toBe('2.6 USDT')
    expect(costText(4_000_000, 'USDT')).toBe('4 USDT')
    expect(costText(1_234_567, 'USDT')).toBe('1.234567 USDT')
    expect(costText(null, 'USDT')).toBe('')
  })
  it('takes an API key as core does', () => {
    expect(keyPattern.test('supkey-xxxxxxxxxxxxxxxx')).toBe(true)
    for (const bad of ['', 'short', 'has space 12345', 'line\nbreak1234', 'x'.repeat(301)]) expect(keyPattern.test(bad), bad).toBe(false)
  })
  it('says why a product cannot be switched on', () => {
    expect(sellableProblem({ sell_price: null, listed: true })).toBe('price')
    expect(sellableProblem({ sell_price: 1, listed: false })).toBe('unlisted')
    expect(sellableProblem({ sell_price: 1, listed: true })).toBe('')
    expect(orderSeverity('paid')).toBe('warn')
    expect(orderSeverity('done')).toBe('success')
  })
  it('has its texts in both languages', () => {
    const keys = ['intro', 'connection', 'api_key', 'turn_on', 'tab_products', 'tab_orders', 'sync', 'price', 'sell', 'refund', 'refund_help', 'no_orders']
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of keys) expect(cat.supplier[k], k).toBeTruthy()
      for (const s of ['paid', 'done', 'refunded']) expect(cat.supplier.order_status[s], s).toBeTruthy()
      expect(cat.section.supplier.title && cat.section.supplier.desc).toBeTruthy()
    }
  })
})
